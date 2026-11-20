"""Compiler resource observation must not alter the build or export its arguments."""
from pathlib import Path
import json
import subprocess
import sys
import tempfile
import textwrap
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts/ci"))
import apple_build_watch as watch


class CompilerResourceTests(unittest.TestCase):
    def test_only_compiler_names_and_resource_counts_are_recorded(self):
        result = subprocess.CompletedProcess([], 0, "\n".join([
            "  10  2 80.5 123456 /Toolchain/usr/bin/swift-frontend",
            "  11  2  2.0   4000 /Toolchain/usr/bin/swiftc",
            "  12 10  0.0    900 /usr/bin/xcodebuild",
            "  13  2  0.0    800 /usr/bin/clang",
        ]), "")
        parsed = watch.processes(lambda *args, **kwargs: result)
        self.assertEqual(parsed["counts"], {"swift-frontend": 1, "swiftc": 1, "xcodebuild": 1, "clang": 1})
        self.assertEqual(parsed["total_rss_kib"], 129156)
        self.assertNotIn("command", json.dumps(parsed["largest"]))

    def test_bounded_command_rejects_rather_than_truncates_evidence(self):
        result = subprocess.CompletedProcess([], 0, "x" * (watch.MAX_OUTPUT_BYTES + 1), "")
        self.assertEqual(watch.output(["ps"], runner=lambda *args, **kwargs: result),
                         {"command": ["ps"], "unavailable": "output-limit"})

    def test_failures_are_explicit_and_do_not_invent_resource_verdicts(self):
        def unavailable(*args, **kwargs):
            raise OSError("unavailable")
        metadata = watch.metadata(unavailable)
        self.assertTrue(all("unavailable" in item for item in metadata.values()))
        self.assertEqual(watch.processes(unavailable), {"counts": {}, "total_rss_kib": 0, "largest": []})

    def test_observer_stops_and_retains_final_sample(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            stop = root / "stop"
            stop.write_text("")
            with mock.patch.object(watch, "metadata", return_value={}), \
                    mock.patch.object(watch, "sample") as sample:
                watch.observe(root, 1800, stop, monotonic=lambda: 0, sleep=lambda _: None)
            sample.assert_called_once_with(root, "final", mock.ANY)
            self.assertIn("periodic samples", (root / "observer.status.txt").read_text())

    def test_samples_are_compact_and_count_bounded(self):
        def fake_output(command, **kwargs):
            if command == ["memory_pressure"]:
                return subprocess.CompletedProcess(command, 0, "Stats:\nSystem-wide memory free percentage: 4%\n", "")
            if command == ["vm_stat"]:
                return subprocess.CompletedProcess(command, 0, "Pages occupied by compressor: 123456.\n", "")
            if command == ["sysctl", "vm.swapusage"]:
                return subprocess.CompletedProcess(command, 0, "vm.swapusage: total = 1024M used = 900M free = 124M\n", "")
            return subprocess.CompletedProcess(command, 0, "", "")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            record = watch.sample(root, "sample", runner=fake_output)
            self.assertEqual(record["memory_free_percent"], "System-wide memory free percentage: 4%")
            self.assertEqual(record["pages_used_by_compressor"], "Pages occupied by compressor: 123456.")
            self.assertNotIn("memory", record)
            self.assertLess((root / "sample.json").stat().st_size, 1024)
            self.assertLessEqual(watch.MAX_PERIODIC_SAMPLES, 120)
            self.assertLessEqual(watch.MAX_OUTPUT_BYTES, 64 * 1024)

    def test_shell_wrapper_preserves_every_xcodebuild_argument_and_budget(self):
        source = (ROOT / "scripts/ci/apple.sh").read_text()
        self.assertIn("ci_run 'Build iOS offline unit tests' 1800 bash -c", source)
        block = source[source.index("ci_run 'Build iOS offline unit tests'"):source.index("ci_run 'No CallKit in the simulator build'")]
        with tempfile.TemporaryDirectory() as directory:
            fixture = Path(directory)
            (fixture / "python3").write_text(textwrap.dedent('''\
                #!/bin/bash
                if [[ "$*" == *apple_build_watch.py* ]]; then
                  printf '%s\\n' "$*" >> "$FIXTURE_WATCH_ARGS"
                  exit 0
                fi
                exec /usr/bin/python3 "$@"
            '''))
            (fixture / "python3").chmod(0o755)
            (fixture / "xcodebuild").write_text(textwrap.dedent('''\
                #!/bin/bash
                printf '%s\\n' "$*" >> "$FIXTURE_XCODE_ARGS"
            '''))
            (fixture / "xcodebuild").chmod(0o755)
            env = {**__import__('os').environ, "PATH": str(fixture) + ":/usr/bin:/bin",
                   "FIXTURE_WATCH_ARGS": str(fixture / "args"),
                   "FIXTURE_XCODE_ARGS": str(fixture / "xcode-args")}
            command = '''set -euo pipefail
            ci_run() {
              local label="$1" budget="$2"; shift 2
              /usr/bin/python3 "$CI_ROOT/scripts/ci/run.py" --label "$label" --timeout 3 -- "$@"
            }
            CI_ROOT=/fixture-root
            OUTPUT=/fixture-output
            IOS_COMMON=(xcodebuild)
            IOS_UNIT_SELECTION=(-only-testing:PersonaTests)
            CI_ROOT=''' + str(ROOT) + '''
            OUTPUT=''' + str(fixture / "output") + '''
            ''' + block
            result = subprocess.run(["/bin/bash", "-c", command], cwd=ROOT, env=env,
                                    capture_output=True, text=True, timeout=15)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            args = (fixture / "args").read_text()
            self.assertIn(str(fixture / "output/diagnostics/build-resources"), args)
            self.assertIn("--seconds 1800", args)
            self.assertEqual((fixture / "xcode-args").read_text(),
                             "-destination generic/platform=iOS Simulator ARCHS=" + __import__('os').uname().machine +
                             " ONLY_ACTIVE_ARCH=YES -only-testing:PersonaTests build-for-testing\n")
            self.assertNotIn("unbound variable", result.stdout + result.stderr)
        self.assertIn('"$@"\n', block)
        self.assertIn("-destination 'generic/platform=iOS Simulator'", block)
        self.assertIn('ARCHS="$(uname -m)" ONLY_ACTIVE_ARCH=YES', block)
        self.assertIn('"${IOS_UNIT_SELECTION[@]}" build-for-testing', block)
        self.assertIn('wait "$watcher" || true', block)
        self.assertNotIn("xcodebuild -jobs", block)
        self.assertNotIn("SWIFT_", block)


if __name__ == "__main__":
    unittest.main()
