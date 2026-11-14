#!/usr/bin/env python3
import argparse
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import time


WINDOWS = os.name == "nt"


def signal_tree(process, hard):
    """Stops the command and what it started: its process group, or on Windows (no process groups here) its tree."""
    if WINDOWS:
        subprocess.run(["taskkill", "/T", "/F", "/PID", str(process.pid)], capture_output=True, check=False)
    else:
        os.killpg(process.pid, signal.SIGKILL if hard else signal.SIGTERM)


def run(label, seconds, command):
    started = time.monotonic()
    print(f"::group::{label}", flush=True)
    if WINDOWS:
        # CreateProcess does not look up npm.cmd for "npm" as a shell does: resolve the name through PATHEXT first.
        command = [shutil.which(command[0]) or command[0], *command[1:]]
    try:
        process = subprocess.Popen(command, start_new_session=True)
    except OSError as error:
        print(f"::endgroup::\nFAIL {label} (could not start command: {error})", flush=True)
        return 127
    def stop(signum, frame):
        signal_tree(process, False)
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            signal_tree(process, True)
            process.wait()
        print(f"INTERRUPTED {label}", flush=True)
        raise SystemExit(128 + signum)
    for signum in (signal.SIGINT, signal.SIGTERM):
        signal.signal(signum, stop)
    try:
        code = process.wait(timeout=seconds)
        verdict = "PASS" if code == 0 else "FAIL"
    except subprocess.TimeoutExpired:
        signal_tree(process, False)
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            signal_tree(process, True)
            process.wait()
        code, verdict = 124, "TIMEOUT"
    elapsed = time.monotonic() - started
    print(f"::endgroup::\n{verdict} {label} ({elapsed:.1f}s, exit {code})", flush=True)
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with Path(summary).open("a") as output:
            output.write(f"- **{verdict}** {label}: {elapsed:.1f}s\n")
    return code if code >= 0 else 128 - code


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--label", required=True)
    parser.add_argument("--timeout", type=float, required=True)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command or args.timeout <= 0:
        parser.error("a command and positive timeout are required")
    sys.exit(run(args.label, args.timeout, command))
