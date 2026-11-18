#!/usr/bin/env python3
"""The DEBUG-only launch arguments (simulator switches) that must not be in a Release build of the iPhone app.

debug_switches.txt lists them, one name per line, sorted and without duplicates, so a change that adds a switch adds one
line instead of editing a regex every other pull request edits too (#10616); .gitattributes merges the file with
merge=union. A line of the Release binary's `strings` equal to one of them means DEBUG code got into the build (or a
property, an enum case or a literal of the same name, which scripts/test/test_apple_release_switch_names.py catches
before merge), and release.sh stops before the upload.

  python3 scripts/release/debug_switches.py check path/to/Persona.app/Persona   # exit 1 when a switch is in its strings
  python3 scripts/release/debug_switches.py check --strings strings.txt          # the same on saved `strings` output
  python3 scripts/release/debug_switches.py sort                                  # sort the list, drop duplicates

The list is kept by hand, not read from the Swift sources: the app reads its switches in many shapes (`forKey:` on
UserDefaults or on a local alias of it, arrays of keys, ProcessInfo's arguments), its DEBUG code also reads settings
keys that do ship, and the list keeps retired switches no source names any more.
"""
import argparse
import re
import subprocess
import sys
from pathlib import Path

LIST = Path(__file__).resolve().with_name("debug_switches.txt")
NAME = re.compile(r"[A-Za-z][A-Za-z0-9_]*")
MESSAGE = "DEBUG switches found in the Release binary"


def split(text: str):
    """(header, names): the comment lines before the first name, and the names in file order. Blank lines and
    comments are skipped; any other line that is not a name raises ValueError (a typo or a merge conflict marker must
    not quietly match nothing)."""
    header, names = [], []
    for number, raw in enumerate(text.splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            if not names and line:
                header.append(raw)
            continue
        if not NAME.fullmatch(line):
            raise ValueError(f"line {number}: {raw!r} is not a switch name (letters, digits and _)")
        names.append(line)
    return header, names


def names(path: Path = LIST) -> list:
    return split(path.read_text(encoding="utf-8"))[1]


def problems(text: str) -> list:
    """What keeps the list from being sorted (byte order, as LC_ALL=C sort) and free of duplicates."""
    try:
        listed = split(text)[1]
    except ValueError as error:
        return [str(error)]
    found = []
    seen = set()
    for previous, name in zip([None] + listed, listed):
        if name in seen:
            found.append(f"{name} is listed twice")
        elif previous is not None and name < previous:
            found.append(f"{name} comes before {previous}")
        seen.add(name)
    return found


def sort_text(text: str) -> str:
    header, listed = split(text)
    return "\n".join(header + sorted(set(listed))) + "\n"


def found(strings_output: bytes, switches) -> list:
    """The switches equal to a whole line of `strings` output, as `grep -x` matches lines."""
    lines = set(strings_output.split(b"\n"))
    return sorted(name for name in set(switches) if name.encode() in lines)


def check(binary: str = None, strings_file: str = None, path: Path = LIST) -> int:
    switches = names(path)
    if not switches:
        print(f"{path}: no DEBUG switches listed", file=sys.stderr)
        return 2
    if strings_file:
        output = Path(strings_file).read_bytes()
    else:
        # The whole output is read, so a match early in it can't be lost to a closed pipe, as with `strings | grep -q`
        # under pipefail.
        run = subprocess.run(["strings", binary], stdout=subprocess.PIPE)
        if run.returncode:
            print(f"strings failed on {binary} (exit {run.returncode})", file=sys.stderr)
            return 2
        output = run.stdout
    hits = found(output, switches)
    if hits:
        print(MESSAGE, file=sys.stderr)
        for name in hits:
            print(f"  {name}", file=sys.stderr)
        return 1
    return 0


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--list", type=Path, default=LIST, help="the list of switches (default: debug_switches.txt)")
    commands = parser.add_subparsers(dest="command", required=True)
    checking = commands.add_parser("check", help="fail when a switch is a line of the binary's strings")
    source = checking.add_mutually_exclusive_group(required=True)
    source.add_argument("binary", nargs="?", help="the Release binary, e.g. Persona.app/Persona")
    source.add_argument("--strings", dest="strings_file", help="saved `strings` output to read instead")
    commands.add_parser("sort", help="sort the list and drop duplicates, keeping its header")
    args = parser.parse_args(argv)
    try:
        if args.command == "sort":
            text = args.list.read_text(encoding="utf-8")
            args.list.write_text(sort_text(text), encoding="utf-8")
            return 0
        return check(args.binary, args.strings_file, args.list)
    except ValueError as error:
        print(f"{args.list}: {error}", file=sys.stderr)
        return 2
    except OSError as error:
        print(error, file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
