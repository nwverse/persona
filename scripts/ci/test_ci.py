import json
import os
from pathlib import Path
import subprocess
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import paths
import required


class PathTests(unittest.TestCase):
    def test_docs_do_not_start_macos(self):
        selected = paths.select(["docs/operations/testing.md"])
        self.assertTrue(selected["scripts"])
        self.assertFalse(selected["apple"])
        self.assertFalse(selected["android"])
        self.assertFalse(selected["web"])
        self.assertFalse(selected["server"])
        self.assertTrue(paths.select(["docs/assistant/prompts.md"])["server"])
        self.assertTrue(paths.select(["docs/protocol/protocol.md"])["server"])

    def test_platform_files_and_shared_inputs(self):
        for platform, filename in [("server", "server/internal/hub/hub.go"), ("web", "web/src/App.tsx"), ("android", "android/app/build.gradle.kts"), ("apple", "apple/Shared/Hub.swift"), ("runtimes", "sdk/ts/src/index.ts")]:
            self.assertTrue(paths.select([filename])[platform])
        self.assertTrue(paths.select(["scripts/mock/inbox_events.json"])["web"])
        self.assertTrue(paths.select(["schema/extension/example.json"])["apple"])

    def test_source_authoring_sdk_and_gate_paths_select_runtime_ci(self):
        for filename in ["sdk/ts/scripts/check-source-bundle.mjs", "sdk/ts/scripts/source-authoring-worker.mjs",
                         "sdk/ts/scripts/source-authoring-tests.mjs", "sdk/ts/README.md", "scripts/ci/runtimes.sh"]:
            self.assertTrue(paths.select([filename])["runtimes"], filename)

    def test_infrastructure_and_dispatch_run_all(self):
        self.assertTrue(all(paths.select([".github/workflows/ci.yml"]).values()))
        self.assertTrue(all(paths.select([], True).values()))

    def test_deleted_and_renamed_old_paths_are_relevant(self):
        self.assertTrue(paths.select(["apple/Old.swift", "docs/archive/new.md"])["apple"])


class RequiredTests(unittest.TestCase):
    def needs(self):
        outputs = {name: "false" for name in ("server", "web", "android", "apple", "runtimes", "scripts")}
        outputs["scripts"] = "true"
        needs = {name: {"result": "skipped"} for name in outputs}
        needs["scripts"] = {"result": "success"}
        needs["changes"] = {"result": "success", "outputs": outputs}
        return needs

    def test_only_unselected_checks_can_skip(self):
        self.assertEqual(required.failures(self.needs()), [])
        needs = self.needs()
        needs["scripts"]["result"] = "skipped"
        self.assertTrue(required.failures(needs))

    def test_failed_cancelled_or_missing_results_fail(self):
        for result in ("failure", "cancelled", None):
            needs = self.needs()
            needs["scripts"]["result"] = result
            self.assertTrue(required.failures(needs))
        needs = self.needs()
        del needs["changes"]["outputs"]["apple"]
        self.assertTrue(required.failures(needs))

    def test_guard_failure_never_passes(self):
        needs = self.needs()
        needs["changes"]["result"] = "failure"
        self.assertTrue(required.failures(needs))


class RunnerTests(unittest.TestCase):
    def invoke(self, command, timeout=5):
        return subprocess.run([sys.executable, str(Path(__file__).with_name("run.py")), "--label", "fixture", "--timeout", str(timeout), "--", sys.executable, "-c", command], capture_output=True, text=True, timeout=10)

    def test_success_and_failure(self):
        self.assertEqual(self.invoke("pass").returncode, 0)
        result = self.invoke("raise SystemExit(7)")
        self.assertEqual(result.returncode, 7)
        self.assertIn("FAIL fixture", result.stdout)

    def test_timeout_is_not_a_pass(self):
        result = self.invoke("import time; time.sleep(3)", 0.1)
        self.assertEqual(result.returncode, 124)
        self.assertIn("TIMEOUT fixture", result.stdout)


if __name__ == "__main__":
    unittest.main()
