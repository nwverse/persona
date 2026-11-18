#!/usr/bin/env python3
"""Reproduce the one Android-only Refract About Version consumer (#9694).

An explicit `generate` rebuilds the pinned public CLI and updates only its reviewed Kotlin. The default `check`
never changes the checkout: it generates both backends in a temporary directory, checks the Kotlin byte-for-byte,
and discards the schema-validation-only ArkTS artifact. No generated file is ever edited by this script.

Usage:
    python3 scripts/app/refract_about_version.py check --cli <path-to-pinned-refract>
    python3 scripts/app/refract_about_version.py generate --cli <path-to-pinned-refract>
    python3 scripts/app/refract_about_version.py check --download
    python3 scripts/app/refract_about_version.py generate --download

`--download` explicitly opts into fetching the exact public source archive from toolchain.json into an isolated
temporary source/toolchain directory. Its full commit, archive checksum, extracted source-tree digest and archived
SwiftSyntax lock are verified before `swift build`; no moving branch or unverified archive is accepted.
An ordinary Android build never invokes this script, downloads a generator or selects the experimental component.
Android CI explicitly opts into the same pinned public source build with --download; no other Gradle or CI path
downloads a generator implicitly. Without --download, no network request is made: use --cli, PERSONA_REFRACT_CLI,
or a local `.toolchain/refract` built from that exact source (this untracked path is optional and never committed).
"""
from __future__ import annotations

import argparse
import hashlib
import http.client
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
PILOT = ROOT / "android/refract/about"
SOURCE = PILOT / "AboutVersionRow.swift"
MAPPING = PILOT / "refract.json"
GENERATED = ROOT / "android/feature/settings/src/test/kotlin/persona/feature/settings/generated/AboutVersionRow.kt"
SCHEMA_ONLY_HARMONY = PILOT / "harmony-resource-validation-only"
EXPECTED = {
    "source": "https://github.com/nwverse/refract",
    "commit": "e32413fba1f13ff0652dedea5f96fc49ec4a8c96",
    "archiveSha256": "a28ae4ff35f85988057cd846fc2bf1844ae88525319eee84f13ac6b9ce8d6a83",
    "sourceTreeSha256": "cfaeafa9cee642c7f8e8832440977ec099851f834ffa31e87de281d5334a43be",
    "mappingSchemaVersion": 1,
    "swiftSyntaxVersion": "600.0.1",
    "swiftSyntaxRevision": "0687f71944021d616d34d922343dcef086855920",
}


def fail(message: str) -> None:
    raise ValueError(message)


def validate_lock() -> dict:
    lock = json.loads((PILOT / "toolchain.json").read_text(encoding="utf-8"))
    for key, expected in EXPECTED.items():
        if lock.get(key) != expected:
            fail(f"toolchain.json: {key} does not match the reviewed source lock")
    if lock.get("archiveUrl") != f"https://api.github.com/repos/nwverse/refract/tarball/{EXPECTED['commit']}":
        fail("toolchain.json: archive URL is not the reviewed commit's public source archive")
    if json.loads(MAPPING.read_text(encoding="utf-8")).get("schemaVersion") != EXPECTED["mappingSchemaVersion"]:
        fail("refract.json: unsupported mapping schema")
    return lock


def source_tree_digest(source: Path) -> str:
    entries = [(path.relative_to(source).as_posix(), hashlib.sha256(path.read_bytes()).hexdigest())
               for path in source.rglob("*") if path.is_file()]
    encoded = json.dumps(sorted(entries), separators=(",", ":")).encode("utf-8")
    return hashlib.sha256(encoded).hexdigest()


def validate_archived_lock(source: Path, lock: dict) -> None:
    actual_tree = source_tree_digest(source)
    if actual_tree != lock["sourceTreeSha256"]:
        fail(f"Source-tree SHA-256 mismatch: got {actual_tree}")
    resolved = json.loads((source / "Package.resolved").read_text(encoding="utf-8"))
    pins = [pin for pin in resolved.get("pins", []) if pin.get("identity") == "swift-syntax"]
    if len(pins) != 1:
        fail("Pinned source must contain exactly one SwiftSyntax lock")
    state = pins[0].get("state", {})
    expected = (lock["swiftSyntaxVersion"], lock["swiftSyntaxRevision"])
    if (state.get("version"), state.get("revision")) != expected:
        fail("Archived SwiftSyntax lock differs from the reviewed source lock")


def safe_extract(archive: Path, destination: Path) -> None:
    with tarfile.open(archive, "r:gz") as tar:
        members = tar.getmembers()
        roots = {member.name.split("/", 1)[0] for member in members}
        if len(roots) != 1:
            fail("Generator archive must have one source root")
        for member in members:
            path = Path(member.name)
            if path.is_absolute() or ".." in path.parts or member.issym() or member.islnk():
                fail("Generator archive has an unsafe path or link")
            if not (member.isfile() or member.isdir()):
                fail("Generator archive has an unsupported special file")
        tar.extractall(destination)
    extracted = list(destination.iterdir())
    if len(extracted) != 1 or not (extracted[0] / "Package.swift").is_file():
        fail("Generator archive does not contain a Swift package")
    shutil.move(str(extracted[0]), str(destination / "source"))


def build_cli(work: Path, lock: dict) -> Path:
    source = work / "source"
    source.mkdir()
    archive = work / "source.tar.gz"
    # The public archive is small but its connection can drop a chunk. Retry only the download, with a fixed
    # deadline; the exact archive checksum below remains the trust boundary and is never retried away.
    for attempt in range(3):
        try:
            request = urllib.request.Request(lock["archiveUrl"], headers={"Accept": "application/vnd.github+json"})
            with urllib.request.urlopen(request, timeout=90) as response, archive.open("wb") as output:
                shutil.copyfileobj(response, output)
            break
        except (OSError, urllib.error.URLError, http.client.IncompleteRead):
            if attempt == 2:
                raise
            time.sleep(1)
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    if digest != lock["archiveSha256"]:
        fail(f"Source archive SHA-256 mismatch: got {digest}")
    safe_extract(archive, source)
    validate_archived_lock(source / "source", lock)
    subprocess.run(
        ["swift", "build", "--package-path", str(source / "source"), "--scratch-path", str(work / "build"),
         "--product", "refract", "--disable-sandbox", "--jobs", os.environ.get("PERSONA_REFRACT_BUILD_JOBS", "2")],
        check=True, cwd=ROOT, timeout=900,
    )
    cli = work / "build/debug/refract"
    if not cli.is_file():
        fail("The pinned generator build produced no CLI")
    # The pinned CLI's `--version` is checked before every invocation. Do not stamp or trust a source entry path.
    return cli


def run(cli: Path, mode: str, android_out: Path, harmony_out: Path) -> None:
    version = subprocess.run([str(cli), "--version"], cwd=ROOT, capture_output=True, text=True, timeout=30)
    if version.returncode != 0:
        fail("CLI does not expose the required `--version` source identity; rebuild the exact pinned public source")
    expected = f"refract 0.1.0 ({EXPECTED['commit']})"
    if version.stdout.strip() != expected:
        fail(f"CLI identity mismatch: expected `{expected}`, got `{version.stdout.strip()}`; rebuild the pinned source")
    subprocess.run(
        [str(cli), mode, "--input", str(SOURCE), "--mapping", str(MAPPING),
         "--android-out", str(android_out), "--harmony-out", str(harmony_out)],
        check=True, cwd=ROOT, timeout=90,
    )


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("check", "generate"), nargs="?", default="check")
    parser.add_argument("--cli", type=Path, help="an already-built CLI from the exact pinned source (not downloaded)")
    parser.add_argument("--download", action="store_true",
                        help="explicitly fetch and build the pinned public CLI when --cli is not supplied")
    args = parser.parse_args()
    try:
        lock = validate_lock()
        if args.download and args.cli is not None:
            parser.error("--download and --cli are mutually exclusive")
        if args.download and shutil.which("swift") is None:
            fail("--download needs Swift on PATH; without Swift, supply the already-built pinned CLI with --cli")
        if args.cli is None and not args.download:
            cached = os.environ.get("PERSONA_REFRACT_CLI")
            if cached:
                args.cli = Path(cached)
            else:
                cached_path = PILOT / ".toolchain/refract"
                if cached_path.is_file():
                    args.cli = cached_path
                else:
                    fail("No pinned CLI was supplied. Use --cli <path-to-pinned-refract> (or PERSONA_REFRACT_CLI), "
                         "or opt into a public source download/build with --download. Nothing was downloaded; "
                         "Kotlin compilation and tests can still run without network access.")
        if args.cli is not None and not args.cli.is_file():
            fail("--cli must name an existing generator executable")
        with tempfile.TemporaryDirectory(prefix="persona-refract-about-") as temporary:
            work = Path(temporary)
            cli = args.cli.resolve() if args.cli is not None else build_cli(work, lock)
            if args.mode == "generate":
                # The mapping's own output.directory is not used: this command is run only from Persona's root.
                # Its second backend is schema validation only and never published as a Persona destination.
                run(cli, "generate", GENERATED.parent, work / "schema-only")
            else:
                run(cli, "generate", work / "android", work / "schema-only")
                actual = work / "android/AboutVersionRow.kt"
                if not actual.is_file() or actual.read_bytes() != GENERATED.read_bytes():
                    fail("Generated Kotlin is stale or missing; run this command with `generate` and review the diff")
            text = GENERATED.read_text(encoding="utf-8")
            required = (
                "// Generated by Refract; DO NOT EDIT.",
                "// Source: AboutVersionRow.swift",
                "import persona.core.designsystem.JRow",
                "value = model.versionName.value",
            )
            if any(fragment not in text for fragment in required):
                fail("Generated Kotlin lost the reviewed source/native-model boundary")
            if re.search(r"(?i)(?:^|\s)(?:import|include|require)\s*[^\n]*refract", text):
                fail("Generated Kotlin must not import a Refract runtime")
            forbidden = ("android.content", "android.content.res", "PackageManager", "ClipboardManager")
            if any(fragment in text for fragment in forbidden):
                fail("Generated UI must not own framework metadata or clipboard behavior")
            if not (SCHEMA_ONLY_HARMONY / "string.json").is_file():
                fail("Missing CLI schema-validation resource fixture")
            for production in (ROOT / "android").glob("**/src/main/**/*.kt"):
                source = production.read_text(encoding="utf-8")
                if "AboutVersionModel" in source or "AboutVersionSnapshot" in source or "generated.AboutVersionRow" in source:
                    fail(f"Ordinary production source must exclude the pilot: {production.relative_to(ROOT)}")
            if args.mode == "generate":
                # A second real invocation is the deterministic output gate, not a handwritten golden.
                before = GENERATED.read_bytes()
                run(cli, "generate", GENERATED.parent, work / "schema-only")
                if GENERATED.read_bytes() != before:
                    fail("Pinned generation is not deterministic")
                print(f"Generated {GENERATED.relative_to(ROOT)} with pinned source {lock['commit']}")
            print(f"Refract About Version {args.mode}: PASSED ({lock['commit']}); Android-only, no runtime")
    except (ValueError, OSError, subprocess.SubprocessError, json.JSONDecodeError, tarfile.TarError,
            urllib.error.URLError, http.client.HTTPException) as error:
        print(f"Refract About Version: FAILED: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
