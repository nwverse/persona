#!/usr/bin/env python3
import argparse
import fnmatch
import json
import os
from pathlib import Path
import subprocess

FILTERS = {
    "server": ["server/**", "sdk/go/**", "schema/**", "spec/**", "integrations/**", "docs/assistant/prompts.md", "docs/protocol/**", "apple/**", "android/**", "scripts/test/server_test_env.py", "scripts/ci/server.sh", "scripts/ci/postgres.py", "scripts/ci/gofmt.py", ".github/workflows/server-test.yml"],
    "web": ["web/**", "schema/extension/**", "spec/fixtures/injected/**", "spec/fixtures/sync/**", "scripts/mock/inbox_events.json", "scripts/ci/web.sh", ".github/workflows/web-test.yml"],
    "android": ["android/**", "schema/**", "spec/**", "scripts/app/*android*", "scripts/ci/android.sh", ".github/workflows/android-test.yml"],
    "apple": ["apple/**", "schema/**", "spec/**", "scripts/app/sim-boot.sh", "scripts/i18n/**", "scripts/ci/apple*", ".github/workflows/apple-test.yml"],
    "runtimes": ["integrations/**", "sdk/ts/**", "schema/**", "spec/**", "scripts/ci/runtimes.sh", ".github/workflows/runtimes-test.yml"],
    "scripts": ["**"],
    "image": ["Dockerfile", ".dockerignore", "deploy/docker/**", "server/**", "sdk/go/**", "web/**", "integrations/**", "schema/extension/**", "spec/fixtures/injected/**", "scripts/ci/hub-image.sh", ".github/workflows/hub-image.yml"],
}
SHARED = [".github/workflows/ci.yml", "scripts/ci/common.sh", "scripts/ci/run.py", "scripts/ci/paths.py", "scripts/ci/test_ci.py"]


def select(paths, all_platforms=False):
    shared = all_platforms or any(fnmatch.fnmatchcase(path, pattern) for path in paths for pattern in SHARED)
    return {platform: shared or any(fnmatch.fnmatchcase(path, pattern) for path in paths for pattern in patterns)
            for platform, patterns in FILTERS.items()}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base")
    parser.add_argument("--all", action="store_true")
    args = parser.parse_args()
    paths = []
    if not args.all:
        base = args.base
        if not base:
            event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
            base = event["pull_request"]["base"]["sha"] if "pull_request" in event else event["merge_group"]["base_sha"]
        raw = subprocess.check_output(["git", "diff", "--name-only", "--no-renames", "-z", f"{base}...HEAD"], timeout=30)
        paths = raw.decode().split("\0")[:-1]
    selected = select(paths, args.all)
    print(json.dumps(selected, sort_keys=True))
    if os.environ.get("GITHUB_OUTPUT"):
        with open(os.environ["GITHUB_OUTPUT"], "a") as output:
            for platform, needed in selected.items():
                output.write(f"{platform}={str(needed).lower()}\n")


if __name__ == "__main__":
    main()
