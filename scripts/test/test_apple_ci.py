"""Keep minimum-Xcode platform preparation ahead of destination validation."""

from pathlib import Path
import sys
import unittest


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts/ci/apple.sh"
sys.path.insert(0, str(ROOT / "scripts/ci"))
import apple_runtime


class AppleCIPreparationTests(unittest.TestCase):
    def test_matching_runtime_is_prepared_before_device_build(self):
        script = SCRIPT.read_text()
        self.assertLess(
            script.index('Download matching $platform 26.0 simulator runtime'),
            script.index("Unsigned iOS and embedded watch/extensions build"),
        )

    def test_runtime_remains_pinned_to_minimum_compiler(self):
        script = SCRIPT.read_text()
        self.assertIn('for platform in iOS watchOS; do', script)
        self.assertIn('xcodebuild -downloadPlatform "$platform" -architectureVariant universal', script)
        self.assertIn('--platform "$platform"', script)
        self.assertNotIn("-buildVersion 26.0", script)
        self.assertIn('--profile stock --runtime "$4"', script)
        self.assertIn('"$OUTPUT/simulator.log" "$IOS_RUNTIME"', script)
        self.assertIn("FAIL select Xcode 26.0.x explicitly", script)


class AppleRuntimeTests(unittest.TestCase):
    def runtime(self, version, available=True, name=None, platform="iOS"):
        return {"version": version, "isAvailable": available,
                "name": name or platform + " " + version,
                "identifier": "com.apple.CoreSimulator.SimRuntime." + platform + "-" + version.replace(".", "-")}

    def test_exact_and_patch_versions_are_compatible(self):
        for version in ("26.0", "26.0.1"):
            runtime = self.runtime(version)
            self.assertEqual(apple_runtime.select([runtime]), runtime["identifier"])

    def test_newer_minors_unavailable_beta_and_other_platforms_are_rejected(self):
        for runtime in (self.runtime("26.1"), self.runtime("26.5"),
                        self.runtime("26.0", available=False),
                        self.runtime("26.0", name="iOS 26.0 beta"),
                        self.runtime("26.0", platform="watchOS")):
            with self.subTest(runtime=runtime):
                self.assertIsNone(apple_runtime.select([runtime]))
        self.assertIsNone(apple_runtime.select([]))

    def test_watch_runtime_is_selected_separately_from_ios(self):
        ios = self.runtime("26.0.1")
        watch = self.runtime("26.0", platform="watchOS")
        self.assertEqual(apple_runtime.select([ios, watch], "watchOS"), watch["identifier"])
        self.assertIsNone(apple_runtime.select([ios], "watchOS"))
        self.assertIsNone(apple_runtime.select([self.runtime("26.2", platform="watchOS")], "watchOS"))

    def test_highest_compatible_patch_wins_without_selecting_newer_minor(self):
        expected = self.runtime("26.0.10")
        self.assertEqual(apple_runtime.select([self.runtime("26.0"), self.runtime("26.0.2"),
                                               self.runtime("26.5"), expected]), expected["identifier"])


if __name__ == "__main__":
    unittest.main()
