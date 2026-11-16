#!/usr/bin/env python3
"""Check the packaged Android variants' sort-shadow native boundary, without a device."""
from __future__ import annotations

import argparse
import subprocess
from pathlib import Path

SYMBOL = "Java_persona_core_rustcore_NativeBridge_projectSort"


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("root", type=Path, help="Directory holding <variant>/<abi>/libpersona_core.so")
    parser.add_argument("readelf", type=Path)
    args = parser.parse_args()
    found = False
    libraries = sorted(args.root.glob("*/libpersona_core.so"))
    if not libraries:
        raise SystemExit(f"Release native guard found no libraries under {args.root}")
    for library in libraries:
        output = subprocess.check_output([str(args.readelf), "--dyn-syms", str(library)], text=True)
        exported = SYMBOL in output
        print(f"{library}: sort shadow export={'present' if exported else 'absent'}")
        found |= exported
    if found:
        raise SystemExit("Release Android native libraries must not export the DEBUG-only sort shadow")
    print("PASS: Release Android native libraries have no sort-shadow JNI export")


if __name__ == "__main__":
    main()
