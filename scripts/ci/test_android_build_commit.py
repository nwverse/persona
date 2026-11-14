"""The Android build commit is passed explicitly by every script that builds an APK (About shows it)."""
from pathlib import Path
import re
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]
CI = (ROOT / "scripts/ci/android.sh").read_text()
LOCAL = (ROOT / "scripts/app/android.sh").read_text()
GRADLE = (ROOT / "android/app/build.gradle.kts").read_text()
SHA = "0123456789abcdef0123456789abcdef01234567"


def resolved(env):
    """Run the CI script's own assignment with only [env] set and return what it resolves to."""
    line = next(line for line in CI.splitlines() if line.startswith("BUILD_COMMIT="))
    done = subprocess.run(["bash", "-c", line + '\nprintf %s "$BUILD_COMMIT"'], env=env, capture_output=True, text=True, check=True)
    return done.stdout


class CiBuildCommitTest(unittest.TestCase):
    def test_explicit_value_wins_over_the_runner_sha(self):
        self.assertEqual(resolved({"PERSONA_BUILD_COMMIT": "abcdef1", "GITHUB_SHA": SHA}), "abcdef1")

    def test_runner_sha_is_the_fallback(self):
        self.assertEqual(resolved({"GITHUB_SHA": SHA}), SHA)

    def test_nothing_set_or_empty_means_unknown(self):
        self.assertEqual(resolved({}), "unknown")
        self.assertEqual(resolved({"PERSONA_BUILD_COMMIT": "", "GITHUB_SHA": ""}), "unknown")

    def test_gradle_gets_the_value_and_the_script_never_asks_git(self):
        self.assertIn('-PpersonaBuildCommit="$BUILD_COMMIT"', CI)
        self.assertNotRegex(CI, r"git\s+(-C\s+\S+\s+)?(rev-parse|describe|log)")
        self.assertNotIn(".config/persona", CI.split("BUILD_COMMIT=", 1)[1].split("\n", 1)[0])


class LocalBuildCommitTest(unittest.TestCase):
    def test_only_the_release_command_injects_the_repository_head(self):
        release = LOCAL.split("\n  release)", 1)[1].split("\n  install)", 1)[0]
        self.assertIn("-PpersonaBuildCommit=", release)
        self.assertIn("rev-parse HEAD", release)
        # An unreadable repository says unknown; an earlier build's value is never reused.
        self.assertIn("BUILD_COMMIT=unknown", release)
        self.assertEqual(LOCAL.count("-PpersonaBuildCommit="), 1)
        for plain in ("test)", "lint)", "apk)"):
            line = next(line for line in LOCAL.splitlines() if line.strip().startswith(plain))
            self.assertNotIn("personaBuildCommit", line)


class GradleBuildCommitTest(unittest.TestCase):
    def test_gradle_reads_only_the_property_then_the_environment_and_never_git(self):
        block = GRADLE.split("val buildCommit", 1)[1].split("}.lowercase()", 1)[0]
        self.assertLess(block.index('gradleProperty("personaBuildCommit")'), block.index('environmentVariable("PERSONA_BUILD_COMMIT")'))
        self.assertIn('?: "unknown"', block)
        self.assertNotRegex(GRADLE, r"rev-parse|ProcessBuilder|exec\(|\.git\b")
        self.assertIn('Regex("[0-9a-fA-F]{7,64}")', block)

    def test_the_commit_is_a_generated_resource_and_not_build_config_or_manifest(self):
        self.assertIn('resValue("string", "persona_build_commit", buildCommit)', GRADLE)
        self.assertNotIn("buildConfig", GRADLE)
        self.assertNotIn("buildConfigField", GRADLE)
        self.assertNotIn("persona_build_commit", (ROOT / "android/app/src/main/AndroidManifest.xml").read_text())

    def test_the_settings_module_reads_the_same_resource_name(self):
        reader = (ROOT / "android/feature/settings/src/main/kotlin/persona/feature/settings/AboutBuildCommit.kt").read_text()
        self.assertIn('BUILD_COMMIT_RESOURCE = "persona_build_commit"', reader)

    def test_the_other_lanes_files_carry_no_build_commit(self):
        devices = (ROOT / "android/feature/settings/src/main/kotlin/persona/feature/settings/DeviceSettingsPages.kt").read_text()
        page = devices.split("internal fun DevicesPage(", 1)[1].split("internal fun AboutSettingsPage", 1)[0]
        self.assertNotIn("BuildCommit", page)


if __name__ == "__main__":
    unittest.main()
