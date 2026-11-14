#!/usr/bin/env python3
"""Generate a no-core XcodeGen spec in a fresh external evidence directory.

The original apple/project.yml is unchanged. Local packages become absolute paths, so generated projects can live
outside the checkout; sources remain the owned checkout's real Swift files. No production configuration is included.
"""
import argparse
import hashlib
import json
import pathlib
import re
import sys


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=pathlib.Path, required=True)
    parser.add_argument("--output-dir", type=pathlib.Path, required=True, help="fresh directory outside the checkout")
    args = parser.parse_args()
    try:
        root = args.root.resolve()
        if args.output_dir.exists():
            raise ValueError("output directory already exists: " + str(args.output_dir))
        try:
            args.output_dir.resolve().relative_to(root)
        except ValueError:
            pass
        else:
            raise ValueError("output directory must be outside the source checkout")
    except ValueError as error:
        print("apple-no-core-spec: failed: " + str(error), file=sys.stderr)
        return 2
    try:
        project = root / "apple" / "project.yml"
        original = project.read_bytes()
        text = original.decode()
        if not text.startswith("name: Persona\n"):
            raise ValueError("expected the production project name at the start")
        changed, count = re.subn(r"(?m)^      - package: PersonaCore\n", "", text)
        if count != 2:
            raise ValueError("expected exactly two literal PersonaCore package dependencies; found " + str(count))
        changed = changed.replace("name: Persona\n", "name: PersonaReferenceNoCore\n", 1)
        changed = re.sub(r"(?m)^(    path: )(.*)$",
                         lambda match: match.group(1) + str(root / "apple" / match.group(2))
                         if not match.group(2).startswith("/") else match.group(0), changed)
        args.output_dir.mkdir(parents=True)
        spec = args.output_dir / "project.yml"
        spec.write_text(changed)
        manifest = {"source_project": str(project), "no_core_spec": str(spec),
                    "source_sha256": hashlib.sha256(original).hexdigest(),
                    "removed": [{"target": "Persona", "dependency": "PersonaCore"},
                                {"target": "PersonaTests", "dependency": "PersonaCore"}],
                    "production_project_unchanged_after_generation": project.read_bytes() == original,
                    "project_root": str(root / "apple"),
                    "note": "package paths are absolute for the external generated project; sources are unchanged"}
        (args.output_dir / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
        print("apple-no-core-spec: ok " + str(spec))
        return 0
    except (OSError, ValueError) as error:
        print("apple-no-core-spec: failed: " + str(error), file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
