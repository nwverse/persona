#!/usr/bin/env python3
"""Compare two locally built iOS .app payloads, with linkage and compression kept distinct.

The caller supplies with-core and no-core app bundles built from the same source/configuration. This tool never
creates or edits a bundle, strips the original, signs, uploads or claims App Store processed/download sizes.
"""
import argparse
import hashlib
import json
import os
import pathlib
import subprocess
import sys


def command(arguments):
    try:
        result = subprocess.run(arguments, capture_output=True, timeout=180)
    except (OSError, subprocess.TimeoutExpired) as error:
        raise ValueError("tool failed: " + arguments[0] + ": " + str(error)) from error
    if result.returncode:
        raise ValueError("tool exited " + str(result.returncode) + ": " + arguments[0] + ": " +
                         result.stderr.decode(errors="replace")[-2000:])
    return result.stdout.decode(errors="replace")


def digest(path):
    hasher = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            hasher.update(chunk)
    return hasher.hexdigest()


def payload(path):
    if not path.is_dir() or path.suffix != ".app":
        raise ValueError("expected a built .app directory: " + str(path))
    executable = path / "Persona"
    if not executable.is_file():
        raise ValueError("missing Persona executable in " + str(path))
    entries = [entry for entry in path.rglob("*") if entry.is_file() and not entry.is_symlink()]
    text = command(["xcrun", "strings", "-a", str(executable)])
    core_symbols = sorted(set(line for line in text.splitlines() if "persona_core_" in line))[:200]
    return {
        "path": str(path), "regular_file_count": len(entries),
        "logical_payload_bytes": sum(entry.stat().st_size for entry in entries),
        "allocated_payload_bytes": sum(entry.stat().st_blocks * 512 for entry in entries),
        "executable_bytes": executable.stat().st_size,
        "executable_sha256": digest(executable),
        "core_symbol_count_retained_max_200": len(core_symbols), "core_symbols": core_symbols,
        "static_core_framework_present": (path / "Frameworks" / "PersonaCoreC.framework").exists(),
        "mach_o": {"uuid": command(["xcrun", "dwarfdump", "--uuid", str(executable)]),
                   "size": command(["xcrun", "size", "-m", str(executable)]),
                   "filetype": command(["xcrun", "file", str(executable)])},
        "files": sorted(({"relative_path": str(entry.relative_to(path)), "bytes": entry.stat().st_size,
                          "sha256": digest(entry)} for entry in entries), key=lambda entry: entry["relative_path"]),
    }


def zipped(app, destination):
    if destination.exists():
        raise ValueError("compressed output already exists: " + str(destination))
    destination.parent.mkdir(parents=True, exist_ok=True)
    # Keep the same parent spelling for both variants, without modifying either .app.
    command(["ditto", "-c", "-k", "--sequesterRsrc", "--keepParent", str(app), str(destination)])
    return {"path": str(destination), "compressed_zip_bytes": destination.stat().st_size,
            "sha256": digest(destination), "meaning": "local compressed transport proxy; not App Store download size"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--with-core", required=True, type=pathlib.Path)
    parser.add_argument("--without-core", required=True, type=pathlib.Path)
    parser.add_argument("--xcframework", type=pathlib.Path, help="optional source-hash provenance, not payload size")
    parser.add_argument("--output", required=True, type=pathlib.Path, help="fresh evidence directory")
    args = parser.parse_args()
    try:
        if args.output.exists():
            raise ValueError("output directory already exists: " + str(args.output))
        if args.with_core.resolve() == args.without_core.resolve():
            raise ValueError("with-core and without-core payload paths must be distinct")
        args.output.mkdir(parents=True)
        before = payload(args.without_core.resolve())
        after = payload(args.with_core.resolve())
        compressed = {"without_core": zipped(args.without_core.resolve(), args.output / "without-core.zip"),
                      "with_core": zipped(args.with_core.resolve(), args.output / "with-core.zip")}
        provenance = {}
        if args.xcframework:
            stamp = args.xcframework.parent / "Inputs.stamp"
            if not stamp.is_file():
                raise ValueError("XCFramework Inputs.stamp is missing")
            provenance = {"inputs_stamp_sha256": digest(stamp),
                          "note": "static archive slices are source diagnostics, not linked-app payload"}
        delta = {key: after[key] - before[key] for key in
                 ("logical_payload_bytes", "allocated_payload_bytes", "executable_bytes")}
        delta["compressed_zip_bytes"] = compressed["with_core"]["compressed_zip_bytes"] - compressed["without_core"]["compressed_zip_bytes"]
        report = {
            "schema_version": 1, "evidence_kind": "local_ios_app_payload_comparison",
            "with_core": after, "without_core": before, "compressed": compressed, "delta_bytes": delta,
            "xcframework_provenance": provenance,
            "store_download_size": "not_measured", "phase1_download_gate_status": "open",
            "limitations": [
                "No App Store Connect processing, per-device thinning, store export, TestFlight or physical-device artifact was used.",
                "Static linkage may select no core objects when production code does not reference them; zero delta does not estimate future integration cost.",
                "Logical, allocated, Mach-O and locally compressed bytes are different quantities; ZIP is not store download size.",
                "Raw XCFramework archives, unselected slices, debug symbols, test runners and dSYMs are not installed app payload.",
            ],
        }
        (args.output / "package-size.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
        print(json.dumps({"delta_bytes": delta, "store_download_size": "not_measured",
                          "report": str(args.output / "package-size.json")}, indent=2))
        return 0
    except (OSError, ValueError, KeyError) as error:
        print("apple-package-report: failed: " + str(error), file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
