"""CI lanes, check-fast and the PR watcher agree on what a change affects, outside the listed differences (#11026).

The drift test plans a probe set of tracked paths with check-fast (--dry-run --each) and compares each path's checks
with CI's lanes (scripts/ci/paths.py) and the watcher's (scripts/operations/pr_watch_verify.py). The probes are derived,
never listed: up to two tracked files for every pattern of CI's lanes, the first file of every top-level directory and
of each directory below it, every tracked path check-fast.sh names, and every tracked path outside server/ and docs/
that a Go test or a runtime package names (the files check-fast's reader searches find). A new mapping in one of the
three that the others miss fails here; scripts/ci/affected_drift.py prints the same comparison for every tracked file.
"""
import fnmatch
from pathlib import Path
import re
import shutil
import subprocess
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import affected_drift  # noqa: E402
import paths  # noqa: E402
import pr_watch_verify  # noqa: E402

ROOT = Path(__file__).resolve().parents[2]
_NAMED = re.compile(r"(?<![\w/.-])((?:\.github|android|apple|deploy|desktop|docs|harmony|integrations|native|schema|"
                    r"scripts|sdk|server|site|spec|web)/[\w./{},*-]+)")


def _braces(pattern):
    """A bash case label's {a,b} parts spelled out: scripts/{gen,test}/x -> scripts/gen/x, scripts/test/x."""
    match = re.search(r"\{([^{}]*)\}", pattern)
    if not match:
        return [pattern]
    return [expanded for part in match.group(1).split(",")
            for expanded in _braces(pattern[:match.start()] + part + pattern[match.end():])]


def probes(tracked):
    """The paths the drift test compares (module docstring)."""
    found = set()
    patterns = [p for lane, lane_patterns in paths.FILTERS.items() if lane != "scripts" for p in lane_patterns]
    patterns += paths.SHARED
    names = _NAMED.findall((ROOT / "scripts/test/check-fast.sh").read_text())
    patterns += [expanded.rstrip(".,;)") for name in names for expanded in _braces(name)]
    for pattern in patterns:
        matches = [path for path in tracked if fnmatch.fnmatchcase(path, pattern)]
        found.update(matches[:1] + matches[-1:])
    named = subprocess.check_output(["git", "grep", "-ohE", r"[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)+", "--",
                                     "server/*_test.go", "integrations/*", "sdk/ts/*"], cwd=ROOT, text=True, timeout=120)
    named = {re.sub(r"^(\.\./)+", "", name) for name in named.split()}
    found.update(path for path in tracked if path in named and not path.startswith(("server/", "docs/")))
    seen = set()
    for path in tracked:
        parts = path.split("/")
        for depth in (1, 2):
            key = "/".join(parts[:depth]) if len(parts) > depth else path
            if key not in seen:
                seen.add(key)
                found.add(path)
    return sorted(found)


class LaneComparisonTests(unittest.TestCase):
    def test_check_fast_lines_name_the_lanes_they_run_part_of(self):
        lines = ["  queued: privacy scan", "  queued: web", "  queued: server go test ./internal/hub/...",
                 "  queued: integrations/runtime-sdk", "  queued: scripts/test/test_doc_paths.py"]
        self.assertEqual({"web", "server", "runtimes"}, affected_drift.fast_lanes(lines))
        self.assertEqual({"server"}, affected_drift.fast_lanes(["  queued: sdk/go go test"]))
        self.assertEqual({"runtimes"}, affected_drift.fast_lanes(["  queued: sdk/ts (bun)"]))
        # A SKIP or INFO note runs nothing, and the privacy scan and the scripts suites are the scripts lane's.
        self.assertEqual(set(), affected_drift.fast_lanes(["SKIP  apple/** builds and tests: need a Mac",
                                                           "INFO  CI lanes this change selects that check-fast does "
                                                           "not run: android", "  queued: privacy scan"]))

    def test_a_lane_only_one_side_runs_is_a_disagreement_unless_listed(self):
        found = affected_drift.compare("web/src/App.tsx", set())
        self.assertIn(("web", "ci", None), found)
        found = affected_drift.compare("site/worker.js", {"server"})
        self.assertIn(("server", "fast", None), found)
        self.assertEqual([], [row for row in affected_drift.compare("web/src/App.tsx", {"web"})
                              if row[2] is None and row[0] == "web"])

    def test_listed_differences_say_why(self):
        reason = affected_drift.allowed("android", "ci", "android/app/build.gradle.kts")
        self.assertIn("Gradle", reason)
        self.assertIsNone(affected_drift.allowed("web", "ci", "web/src/App.tsx"))
        for lane, side, patterns, reason in affected_drift.ALLOWED:
            self.assertIn(lane, affected_drift.LANES)
            self.assertIn(side, ("ci", "fast", "watcher"))
            self.assertTrue(patterns and reason, lane)


class WatcherLaneTests(unittest.TestCase):
    def test_the_watcher_runs_cis_lanes_with_the_scripts_lane_narrowed(self):
        names = ["server/internal/hub/hub.go"]
        self.assertEqual(["server"], pr_watch_verify.watcher_lanes(paths.select(names), names))
        names = ["scripts/ci/gofmt.py"]
        self.assertEqual(["server", "scripts"], pr_watch_verify.watcher_lanes(paths.select(names), names))
        # CI's apple and image lanes are not among the watcher's.
        names = ["apple/Shared/Models.swift"]
        self.assertNotIn("apple", pr_watch_verify.watcher_lanes(paths.select(names), names))

    def test_a_test_only_pr_runs_its_tests_instead_of_the_web_scripts_and_server_lanes(self):
        names = ["server/internal/hub/hub_test.go", "web/src/App.test.tsx"]
        # The desktop lane loads the web client, so a web test still runs it.
        self.assertEqual(["desktop"], pr_watch_verify.watcher_lanes(paths.select(names), names, focused=True))
        names = ["android/app/src/test/FooTest.kt"]
        self.assertEqual(["android"], pr_watch_verify.watcher_lanes(paths.select(names), names, focused=True))


class ResolverCommandLineTests(unittest.TestCase):
    """The paths.py commands check-fast reads: the lanes of a change and the web plan of each path."""

    def run_paths(self, flag, text):
        return subprocess.run([sys.executable, str(ROOT / "scripts/ci/paths.py"), flag], input=text, cwd=ROOT,
                              capture_output=True, text=True, timeout=120, check=True).stdout

    def test_lanes_for_prints_the_lanes_ci_runs(self):
        self.assertEqual("server android apple core scripts\n",
                         self.run_paths("--lanes-for", "apple/Shared/Models.swift\nnative/app-core/Cargo.toml\n"))
        self.assertEqual("scripts\n", self.run_paths("--lanes-for", "docs/example.md\n"))

    def test_the_web_plan_of_each_path_matches_its_own_web_plan(self):
        listed = ["web/src/App.tsx", "server/go.mod", "web/package.json", "spec/fixtures/agent-names/names.json"]
        lines = self.run_paths("--web-plan-each", "".join(p + "\n" for p in listed)).splitlines()
        self.assertEqual(len(listed), len(lines))
        for path, line in zip(listed, lines):
            mode, reason, _ = paths.web_plan([path])
            self.assertEqual(f"{mode} {reason}", line, path)


@unittest.skipUnless(shutil.which("timeout") or shutil.which("gtimeout"), "check-fast.sh needs GNU timeout")
class DriftTests(unittest.TestCase):
    def test_ci_check_fast_and_the_watcher_agree_outside_the_listed_differences(self):
        tracked = affected_drift.tracked()
        listed = probes(tracked)
        self.assertGreater(len(listed), 100)
        rows = affected_drift.measure(listed)
        gaps = ["%s: %s %s" % (path, side, lane) for path, _, _, _, found in rows
                for lane, side, reason in found if reason is None]
        self.assertEqual([], gaps, "\n".join(affected_drift.summary(rows)))

    def test_the_probes_cover_every_lane_pattern_that_matches_a_file(self):
        tracked = affected_drift.tracked()
        listed = set(probes(tracked))
        for lane, patterns in paths.FILTERS.items():
            if lane == "scripts":
                continue
            for pattern in patterns:
                if any(fnmatch.fnmatchcase(path, pattern) for path in tracked):
                    self.assertTrue(any(fnmatch.fnmatchcase(path, pattern) for path in listed), (lane, pattern))


if __name__ == "__main__":
    unittest.main()
