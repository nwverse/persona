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

    def test_mac_result_does_not_change_required_ios_policy(self):
        for mac_result in ('failure', 'cancelled', 'skipped', None, 'success'):
            needs = self.needs()
            needs['changes']['outputs']['apple'] = 'true'
            needs['apple']['result'] = 'success'
            needs['apple_macos'] = {'result': mac_result}
            self.assertEqual(required.failures(needs), [])
            for ios_result in ('failure', 'cancelled', 'skipped', None):
                needs['apple']['result'] = ios_result
                self.assertTrue(required.failures(needs))

    def test_guard_failure_never_passes(self):
        needs = self.needs()
        needs["changes"]["result"] = "failure"
        self.assertTrue(required.failures(needs))


class AppleWorkflowTests(unittest.TestCase):
    def test_mac_and_ios_are_independent_but_only_ios_blocks(self):
        root = Path(__file__).resolve().parents[2]
        workflow = (root / '.github/workflows/ci.yml').read_text()
        ios = workflow.split('\n  apple:\n', 1)[1].split('\n  apple_macos:\n', 1)[0]
        mac = workflow.split('\n  apple_macos:\n', 1)[1].split('\n  runtimes:\n', 1)[0]
        for block, lane in ((ios, 'ios'), (mac, 'macos')):
            self.assertIn('    needs: changes\n', block)
            self.assertIn("    if: needs.changes.outputs.apple == 'true'", block)
            self.assertIn('    uses: ./.github/workflows/apple-test.yml', block)
            self.assertIn('      lane: ' + lane, block)
        gate = workflow.split('\n  required:\n', 1)[1]
        self.assertIn('needs: [changes, server, web, android, apple, runtimes, scripts]', gate)
        self.assertNotIn('apple_macos', gate)
        self.assertNotIn('continue-on-error:', workflow)

    def test_reusable_lane_uses_quoted_environment_not_script_interpolation(self):
        root = Path(__file__).resolve().parents[2]
        workflow = (root / '.github/workflows/apple-test.yml').read_text()
        self.assertIn('options: [ios, macos]', workflow)
        self.assertIn('APPLE_CI_LANE: ${{ inputs.lane }}', workflow)
        self.assertIn('run: scripts/ci/apple.sh "$APPLE_CI_LANE"', workflow)
        self.assertNotIn('continue-on-error:', workflow)
        script = (root / 'scripts/ci/apple.sh').read_text()
        package_gate = script.split("ci_run 'PersonaKit unit tests'", 1)[0].rsplit('\nif ', 1)[1]
        self.assertIn('[ "$LANE" = ios ]', package_gate)
        for required_batch in ('Unsigned iOS and embedded watch/extensions build',
                               'iOS offline unit and design/token tests',
                               'iOS HTML card tests (separate batch)',
                               'iOS bounded HTML security tests (separate batch)'):
            self.assertIn(required_batch, script)


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
