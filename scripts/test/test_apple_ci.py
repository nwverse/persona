"""Keep minimum-Xcode platform preparation ahead of destination validation."""

from pathlib import Path
import unittest


SCRIPT = Path(__file__).resolve().parents[2] / "scripts/ci/apple.sh"


class AppleCIPreparationTests(unittest.TestCase):
    def test_matching_runtime_is_prepared_before_device_build(self):
        script = SCRIPT.read_text()
        self.assertLess(
            script.index("Download matching iOS 26.0 simulator runtime"),
            script.index("Unsigned iOS and embedded watch/extensions build"),
        )

    def test_runtime_remains_pinned_to_minimum_compiler(self):
        script = SCRIPT.read_text()
        self.assertIn("xcodebuild -downloadPlatform iOS -buildVersion 26.0", script)
        self.assertIn('--profile stock --runtime 26.0', script)
        self.assertIn("FAIL select Xcode 26.0.x explicitly", script)


if __name__ == "__main__":
    unittest.main()
