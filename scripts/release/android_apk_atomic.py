#!/usr/bin/env python3
"""Commit a staged Android release under an exclusive publication lock."""
import argparse
import fcntl
import gzip
import hashlib
import json
import os
from pathlib import Path
import re

MAX_VERSION_CODE = 2100000000


def digest_file(path):
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def validate_release(release):
    version = release.get("version_code")
    if type(version) is not int or not 0 < version <= MAX_VERSION_CODE:
        raise ValueError("invalid version_code")
    if not re.fullmatch(r"[0-9a-f]{64}", release.get("sha256", "")):
        raise ValueError("invalid APK sha256")
    if type(release.get("size")) is not int or release["size"] <= 0:
        raise ValueError("invalid APK size")


def gzip_copy(apk, sha256):
    """Write <apk>.gz beside the staged APK, for the hub to send to downloads that take gzip
    (server/internal/hub/androidapp.go). An APK stores its dex files and resources.arsc uncompressed so the phone can
    map them, so gzip about halves the download; the client checks the SHA-256 of the decoded bytes as before. The copy
    is checked to decode to the APK and flushed before publish() renames it into place.
    """
    copy = apk.with_name(apk.name + ".gz")
    with apk.open("rb") as source, copy.open("wb") as raw:
        # No name and a fixed time in the header: the same APK always gives the same bytes.
        with gzip.GzipFile(filename="", mode="wb", compresslevel=6, fileobj=raw, mtime=0) as packed:
            for chunk in iter(lambda: source.read(1024 * 1024), b""):
                packed.write(chunk)
        raw.flush()
        os.fsync(raw.fileno())
    decoded = hashlib.sha256()
    with gzip.open(copy, "rb") as unpacked:
        for chunk in iter(lambda: unpacked.read(1024 * 1024), b""):
            decoded.update(chunk)
    if decoded.hexdigest() != sha256:
        raise ValueError("gzip copy does not decode to the APK")
    return copy


def sync_directory(directory):
    descriptor = os.open(directory, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def publish(directory, stage):
    directory, stage = Path(directory), Path(stage)
    release = json.loads((stage / "latest.json").read_text())
    validate_release(release)
    apk = stage / (release["sha256"] + ".apk")
    if apk.stat().st_size != release["size"] or digest_file(apk) != release["sha256"]:
        raise ValueError("uploaded APK hash or size mismatch")
    directory.mkdir(parents=True, exist_ok=True)
    with (directory / ".publish.lock").open("a") as lock:
        fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
        manifest = directory / "latest.json"
        if manifest.exists():
            current = json.loads(manifest.read_text())
            validate_release(current)
            if release["version_code"] < current["version_code"]:
                raise ValueError("refusing stale version_code")
            if release["version_code"] == current["version_code"]:
                if release["sha256"] != current["sha256"]:
                    raise ValueError("version_code already published with different APK")
                existing = directory / (current["sha256"] + ".apk")
                if existing.stat().st_size != current["size"] or digest_file(existing) != current["sha256"]:
                    raise ValueError("existing published APK is corrupt")
                return "already published"
        # Flush bytes before exposing names. Existing content-addressed APKs (and their
        # gzip copies) remain available to readers that already loaded the previous manifest.
        for path in (apk, stage / "latest.json"):
            with path.open("rb") as source:
                os.fsync(source.fileno())
        copy = gzip_copy(apk, release["sha256"])
        os.replace(copy, directory / copy.name)
        os.replace(apk, directory / apk.name)
        sync_directory(directory)
        os.replace(stage / "latest.json", manifest)
        sync_directory(directory)
        return "published"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", required=True, type=Path)
    parser.add_argument("--stage", required=True, type=Path)
    args = parser.parse_args()
    try:
        print(publish(args.directory, args.stage))
    except (OSError, ValueError, KeyError) as error:
        parser.exit(1, f"APK publication failed: {error}\n")


if __name__ == "__main__":
    main()
