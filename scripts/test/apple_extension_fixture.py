#!/usr/bin/env python3
"""Build a local-only Apple acceptance extension using the official SDK.

The default Deno source-mode fixture retains validation and isolation gates.
The optional process-mode fixture is a distinct supported runtime; its Apple
protocol/rendering evidence does not establish Deno installation or isolation.

The output is a new directory, outside the repository, containing authoring input
and an installable package. No production hub or credential is read. Shared
snapshot values are copied without adding platform-specific fixture content.
"""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[2]


def manifest(snapshots, runtime="deno"):
    if runtime not in ("deno", "process"):
        raise ValueError("Select the explicit deno or process runtime")
    runtime_config = {"mode": "deno", "source": {"entry": "main.ts", "bundle": "bundle/main.mjs", "sha256": ""},
                      "resources": {"memory_mb": 128, "cpu_percent": 20}} if runtime == "deno" else {
                          "mode": "process", "command": {"linux-amd64": "bin/showcase-linux-amd64"},
                          "resources": {"memory_mb": 128, "cpu_percent": 20}}
    pages = [{"id": name, "title": view.get("title") or name, "placement": "nav_page"}
             for name, view in sorted(snapshots.items())]
    return {
        "manifest_version": 1, "id": "showcase", "name": "Apple acceptance showcase", "version": "0.1.0",
        "description": "Local-only shared snapshots, UI streams, widgets and shortcuts for Apple acceptance.",
        "engines": {"extension_protocol": 1},
        "runtime": runtime_config,
        "contributes": {
            "ui": pages + [
                {"id": "showcase", "title": "Component showcase", "placement": "nav_page"},
                {"id": "showcase-card", "title": "Component showcase", "placement": "overview_card", "opens": "showcase"},
                {"id": "showcase-widget", "title": "Component showcase", "placement": "widget", "opens": "showcase",
                 "families": ["small", "medium", "large", "accessory_circular", "accessory_rectangular", "accessory_inline"],
                 "refresh_minutes": 30},
                {"id": "machine", "title": "Test computer", "placement": "machine_section"},
            ],
            "shortcuts": [
                {"id": "echo", "title": "Echo", "ui": "showcase", "params": [{"name": "text", "title": "Text", "kind": "text", "required": True}]},
                {"id": "count", "title": "Count", "params": [
                    {"name": "n", "title": "Number", "kind": "number", "required": True},
                    {"name": "unit", "title": "Unit", "kind": "choice", "choices": [{"value": "star", "title": "Stars"}, {"value": "apple", "title": "Apples"}]},
                    {"name": "loud", "title": "Toggle", "kind": "toggle"}]},
                {"id": "invalidate", "title": "Invalidate test widget"},
                {"id": "stats", "title": "Acceptance readings"},
            ],
        },
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--runtime", choices=["deno", "process"], default="deno")
    parser.add_argument("--version", default="0.1.0")
    args = parser.parse_args()
    try:
        output = args.output.resolve()
        if output.exists():
            raise ValueError("Output must be a new directory")
        files = sorted((ROOT / "schema/extension/examples/showcase").glob("*.json"))
        snapshots = {file.stem: json.loads(file.read_bytes()) for file in files}
        hashes = {file.stem: hashlib.sha256(file.read_bytes()).hexdigest() for file in files}
        document = manifest(snapshots, runtime=args.runtime)
        document["version"] = args.version
        authoring = output / "authoring"
        package = output / "package/showcase"
        authoring.mkdir(parents=True)
        package.mkdir(parents=True)
        shutil.copyfile(ROOT / "scripts/mock/extension_acceptance.ts", authoring / "main.ts")
        (authoring / "snapshots.json").write_text(json.dumps(snapshots, ensure_ascii=False))
        (authoring / "persona-extension.json").write_text(json.dumps(document, ensure_ascii=False))
        modules = authoring / "node_modules/@persona"
        modules.mkdir(parents=True)
        (modules / "extension").symlink_to(ROOT / "sdk/ts", target_is_directory=True)
        bundle = (package / "bundle/main.mjs") if args.runtime == "deno" else (authoring / "program.mjs")
        bundle.parent.mkdir(parents=True, exist_ok=True)
        subprocess.run(["bun", "build", str(authoring / "main.ts"), "--target", "browser", "--minify", "--outfile", str(bundle)],
                       cwd=ROOT, check=True, timeout=120)
        if args.runtime == "deno":
            document["runtime"]["source"]["sha256"] = hashlib.sha256(bundle.read_bytes()).hexdigest()
        else:
            wrapper = authoring / "process.ts"
            wrapper.write_text('import { main } from "@persona/extension";\nimport program from "./main.ts";\nmain(program);\n')
            binary = package / "bin/showcase-linux-amd64"
            binary.parent.mkdir()
            subprocess.run(["bun", "build", str(wrapper), "--compile", "--target=bun-linux-x64", "--minify", "--outfile", str(binary)],
                           cwd=ROOT, check=True, timeout=180)
            binary.chmod(0o755)
        (package / "persona-extension.json").write_text(json.dumps(document, indent=2, ensure_ascii=False) + "\n")
        (output / "source-sha256.json").write_text(json.dumps(hashes, indent=2, sort_keys=True) + "\n")
        (output / "runtime-evidence.json").write_text(json.dumps({"runtime": args.runtime,
            "scope": "Apple protocol/rendering fixture", "deno_install_or_isolation_verified": False}, indent=2) + "\n")
        print("RESULT Apple acceptance extension: PASS; runtime=%s; %d shared snapshots; %s" % (args.runtime, len(snapshots), package))
        return 0
    except subprocess.TimeoutExpired:
        print("RESULT Apple acceptance extension: TIMEOUT")
        return 124
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print("RESULT Apple acceptance extension: FAIL; " + str(error))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
