#!/usr/bin/env python3
"""How the CI lanes, check-fast and the PR watcher map a changed path, compared path by path (#11026).

Three places decide what a change affects: scripts/ci/paths.py picks CI's lanes, scripts/test/check-fast.sh picks the
local fast checks, and the PR watcher (scripts/operations/pr_watch_verify.py) picks the lanes it verifies a PR with.
The watcher reads paths.py itself; check-fast keeps its own map in bash. This compares the three for one path at a
time, lane by lane, and names every disagreement that ALLOWED does not explain.

A check-fast check counts for a CI lane when it runs a fast part of that lane's work (FAST_LANES): the web check for
web, Go tests and builds for server, the integration and sdk/ts tests for runtimes. The lanes check-fast never runs
(android, apple, core, desktop, image) it names instead, from paths.py (--lanes-for). The scripts lane is CI's "**",
so every path selects it, and the privacy scan and the scripts unittests always run.

    python3 scripts/ci/affected_drift.py                 # every tracked file: a summary by top-level area
    python3 scripts/ci/affected_drift.py --table out.tsv # also the per-path table
    python3 scripts/ci/affected_drift.py path ...        # only these paths

check-fast plans with stub go, node and bun on PATH, so the plan does not depend on what this machine has installed.
"""
import argparse
from collections import Counter, defaultdict
import fnmatch
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts/ci"))
sys.path.insert(0, str(ROOT / "scripts/operations"))
import paths  # noqa: E402
import pr_watch_verify  # noqa: E402

# CI's platform lanes. scripts is "**" in CI, so it is left out of the comparison.
LANES = ("server", "web", "android", "apple", "core", "runtimes", "desktop", "image")

# The check-fast lines that cover a lane: a queued check that runs a fast part of the lane's work. A SKIP or INFO line
# runs nothing, so it covers nothing.
FAST_LANES = (
    ("web", re.compile(r"  queued: web$")),
    ("server", re.compile(r"  queued: (server go test |server go build |server repo-wide guards$|sdk/go go test$)")),
    ("runtimes", re.compile(r"  queued: (integrations/|sdk/ts \(bun\)$)")),
)

# The differences that are meant, each with the reason. A row is (lane, side, patterns, reason): side "ci" means CI
# selects the lane and check-fast runs no part of it, "fast" the other way round, "watcher" that the watcher does not
# run a lane CI selects. A path is explained when it matches one of the patterns (fnmatch: * matches / too).
_NAMED = "check-fast names it instead (the apple SKIP line, or the INFO line of the CI lanes it does not run)"
ALLOWED = (
    # Lanes check-fast never runs: they need a toolchain or a build that takes minutes.
    ("android", "ci", ("**",), "check-fast never runs Gradle; " + _NAMED),
    ("apple", "ci", ("**",), "check-fast never runs Xcode; " + _NAMED),
    ("core", "ci", ("**",), "check-fast never runs cargo; " + _NAMED),
    ("desktop", "ci", ("**",), "check-fast never installs Electron; " + _NAMED),
    ("image", "ci", ("**",), "check-fast never builds the hub image (CI builds it for releases)"),
    ("image", "watcher", ("**",), "the watcher never builds the hub image (CI builds it for releases)"),
    ("apple", "watcher", ("**",), "the watcher has no Mac; it records a policy line for the apple lane instead"),
    # CI watches whole directories that tests outside the lane's tree read; check-fast runs the tests that name the
    # changed file.
    ("server", "ci", tuple(paths.SERVER_CLIENT_INPUTS),
     "CI runs the server lane for every client contract source; check-fast runs the Go tests that name the file whole"),
    ("server", "ci", ("spec/**",),
     "CI runs the server lane for all of spec/; check-fast runs the Go tests that read the changed fixture"),
    ("server", "ci", ("docs/protocol/**", "docs/assistant/prompts.md"),
     "CI runs the server lane for the protocol docs and the prompt catalogue; check-fast runs the Go tests that "
     "mention the changed doc"),
    ("server", "fast", ("docs/**",),
     "check-fast runs the Go tests that mention a changed doc outside a comment line, in a message or an example "
     "too; CI's server lane watches the docs Go tests read"),
    ("server", "fast", ("apple/Persona/Views/Machines/MachinesView.swift",
                        "android/feature/machines/src/main/kotlin/persona/feature/machines/MachineConcern.kt"),
     "internal/hub's wire samples name these client decoders as the samples' consumers without reading them; "
     "check-fast runs internal/hub for them, CI's server lane does not watch them"),
    ("runtimes", "ci", ("schema/**", "spec/**"),
     "CI runs the runtimes lane for all fixtures and schemas; check-fast runs the runtime packages that name the path"),
    # A lane's own runner and workflow: CI runs the lane, check-fast the runner's unittests.
    ("server", "ci", ("scripts/ci/*", "scripts/test/go_trimpath.sh", ".github/workflows/*"),
     "the lane's own runner and workflow: their unittests run instead"),
    ("web", "ci", ("scripts/ci/web.sh", ".github/workflows/*"), "the lane's own runner and workflow"),
    ("runtimes", "ci", ("scripts/ci/runtimes.sh", ".github/workflows/*"), "the lane's own runner and workflow"),
    # CI's selection itself changed: CI runs every lane.
    ("server", "ci", tuple(paths.SHARED), "CI's selection itself: every lane runs"),
    ("web", "ci", tuple(paths.SHARED), "CI's selection itself: every lane runs"),
    ("runtimes", "ci", tuple(paths.SHARED), "CI's selection itself: every lane runs"),
)


def tracked(root=ROOT):
    return subprocess.check_output(["git", "ls-files"], cwd=root, text=True, timeout=60).splitlines()


def ci_lanes(path):
    return {lane for lane, on in paths.select([path]).items() if on and lane in LANES}


def watcher_lanes(path):
    """The lanes the watcher runs for a PR that changes only this path (not a test-only test/fix PR)."""
    return set(pr_watch_verify.watcher_lanes(paths.select([path]), [path])) & set(LANES)


def _stub_tools(directory):
    """go, node (v22) and bun that answer the version checks and do nothing else."""
    for name, body in (("go", ""), ("bun", ""), ("node", 'echo 22\n')):
        tool = Path(directory, name)
        tool.write_text("#!/bin/sh\n" + body)
        tool.chmod(0o755)


def check_fast_plans(path_list, root=ROOT):
    """{path: [check-fast's plan lines for that path alone]} from one check-fast --dry-run --each."""
    with tempfile.TemporaryDirectory(prefix="affected-drift-") as temporary:
        _stub_tools(temporary)
        env = dict(os.environ, PATH=temporary + os.pathsep + os.environ.get("PATH", ""))
        result = subprocess.run(["bash", str(root / "scripts/test/check-fast.sh"), "--dry-run", "--changed-from", "-",
                                 "--each"], input="".join(p + "\n" for p in path_list), cwd=root, env=env,
                                capture_output=True, text=True, timeout=3600)
    if result.returncode:
        raise RuntimeError("check-fast --each failed (%d): %s" % (result.returncode, result.stderr[-2000:]))
    plans, current = {}, None
    for line in result.stdout.splitlines():
        if line.startswith("== "):
            current = line[3:]
            plans[current] = []
        elif current is not None:
            plans[current].append(line)
    return plans


def fast_lanes(lines):
    return {lane for lane, pattern in FAST_LANES for line in lines if pattern.match(line)}


def allowed(lane, side, path):
    for row_lane, row_side, patterns, reason in ALLOWED:
        if row_lane == lane and row_side == side and any(fnmatch.fnmatchcase(path, p) for p in patterns):
            return reason
    return None


def compare(path, fast):
    """The disagreements of one path: (lane, side, reason or None) for each lane where the three differ."""
    ci, watcher = ci_lanes(path), watcher_lanes(path)
    found = []
    for lane in LANES:
        if lane in ci and lane not in fast:
            found.append((lane, "ci", allowed(lane, "ci", path)))
        if lane in fast and lane not in ci:
            found.append((lane, "fast", allowed(lane, "fast", path)))
        if lane in ci and lane not in watcher:
            found.append((lane, "watcher", allowed(lane, "watcher", path)))
    return found


def area(path):
    parts = path.split("/")
    return parts[0] if len(parts) == 1 else parts[0] + "/"


def measure(path_list, root=ROOT):
    """[(path, CI lanes, check-fast lanes, watcher lanes, disagreements)] for each path."""
    plans = check_fast_plans(path_list, root)
    rows = []
    for path in path_list:
        fast = fast_lanes(plans.get(path, []))
        rows.append((path, ci_lanes(path), fast, watcher_lanes(path), compare(path, fast)))
    return rows


def summary(rows):
    """Lines: the disagreements counted by side, lane and top-level area, then the unexplained ones by area."""
    explained, gaps = Counter(), defaultdict(list)
    for path, _, _, _, found in rows:
        for lane, side, reason in found:
            if reason:
                explained[(side, lane, area(path), reason)] += 1
            else:
                gaps[(side, lane, area(path))].append(path)
    lines = ["%d path(s) compared" % len(rows), "", "Intended differences (side, lane, area: paths):"]
    for (side, lane, where, reason), count in sorted(explained.items()):
        lines.append("  %s %s %s: %d (%s)" % (side, lane, where, count, reason))
    lines += ["", "Unexplained (side, lane, area: paths, examples):"]
    for (side, lane, where), found in sorted(gaps.items()):
        lines.append("  %s %s %s: %d, e.g. %s" % (side, lane, where, len(found), ", ".join(found[:3])))
    if not gaps:
        lines.append("  none")
    return lines


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("paths", nargs="*", help="paths to compare (default: every tracked file)")
    parser.add_argument("--table", help="write the per-path table (tab-separated) here")
    args = parser.parse_args(argv)
    if not shutil.which("timeout") and not shutil.which("gtimeout"):
        parser.error("check-fast.sh needs GNU timeout")
    rows = measure(args.paths or tracked())
    if args.table:
        with open(args.table, "w") as out:
            out.write("path\tci\tcheck-fast\twatcher\tdisagreements\n")
            for path, ci, fast, watcher, found in rows:
                out.write("\t".join([path, ",".join(sorted(ci)), ",".join(sorted(fast)), ",".join(sorted(watcher)),
                                     "; ".join("%s %s%s" % (side, lane, "" if reason else " (gap)")
                                               for lane, side, reason in found)]) + "\n")
    print("\n".join(summary(rows)))
    return 1 if any(reason is None for row in rows for _, _, reason in row[4]) else 0


if __name__ == "__main__":
    sys.exit(main())
