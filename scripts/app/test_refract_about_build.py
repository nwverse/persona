import unittest
import tempfile
from pathlib import Path
from unittest import mock

import refract_about_build as build


class AboutBuildConsumerTest(unittest.TestCase):
    def test_lock_and_production_exclusion(self):
        lock = build.validate_consumer_lock()
        self.assertEqual(build.EXPECTED["commit"], lock["commit"])
        self.assertEqual(build.EXPECTED["mappingSchemaVersion"], 1)
        self.assertEqual(build.SOURCE.parent, build.MAPPING.parent)
        self.assertNotEqual(build.SOURCE, build.source_lock.SOURCE)

    def test_plain_string_fallback_is_rejected(self):
        text = build.GENERATED.read_text(encoding="utf-8")
        with mock.patch.object(build, "GENERATED") as generated:
            generated.read_text.return_value = text.replace(
                "stringResource(R.string.settings_not_provided)", '"settings.not.provided"')
            with self.assertRaisesRegex(ValueError, "optional/native/token/localization"):
                build.validate_consumer_lock()

    def test_check_generates_twice_without_patching_the_reviewed_file(self):
        original = build.GENERATED.read_bytes()
        with tempfile.TemporaryDirectory() as temporary:
            cli = Path(temporary) / "refract"
            cli.touch()
            calls = []

            def generate(command, **kwargs):
                calls.append(command)
                if "--version" in command:
                    return mock.Mock(returncode=0, stdout=f"refract 0.1.0 ({build.EXPECTED['commit']})\n")
                output = Path(command[command.index("--android-out") + 1]) / "AboutBuildRow.kt"
                output.parent.mkdir(parents=True, exist_ok=True)
                output.write_bytes(original)
                return mock.Mock(returncode=0)

            with mock.patch.object(build, "validate_consumer_lock", return_value=dict(build.EXPECTED)), \
                    mock.patch.object(build.subprocess, "run", side_effect=generate), \
                    mock.patch.object(build.sys, "argv", ["refract_about_build.py", "check", "--cli", str(cli)]):
                self.assertEqual(build.main(), 0)
        self.assertEqual(2, sum("--version" not in command for command in calls))
        self.assertEqual(build.GENERATED.read_bytes(), original)


if __name__ == "__main__":
    unittest.main()
