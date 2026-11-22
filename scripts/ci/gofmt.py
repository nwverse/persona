#!/usr/bin/env python3
from pathlib import Path
import subprocess
import sys

files = sorted(str(path) for root in sys.argv[1:] for path in Path(root).rglob("*.go"))
failed = False
for offset in range(0, len(files), 100):
    result = subprocess.run(["gofmt", "-l", *files[offset:offset + 100]], capture_output=True, text=True, timeout=30)
    if result.stdout or result.returncode:
        # gofmt -l prints bare file names; say what they are, so a failure summary has text to show.
        for name in result.stdout.splitlines():
            print(f"gofmt: needs formatting (run gofmt -w): {name}")
        print(result.stderr, end="")
        failed = True
sys.exit(1 if failed else 0)
