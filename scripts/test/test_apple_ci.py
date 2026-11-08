"""Keep minimum-Xcode platform preparation ahead of destination validation."""

from pathlib import Path
import plistlib
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts/ci/apple.sh"
sys.path.insert(0, str(ROOT / "scripts/ci"))
import apple_runtime
import apple_entitlements


class AppleCIPreparationTests(unittest.TestCase):
    def test_matching_runtime_is_prepared_before_device_build(self):
        script = SCRIPT.read_text()
        self.assertLess(
            script.index('Download matching $platform 26.0 simulator runtime'),
            script.index("Unsigned iOS and embedded watch/extensions build"),
        )

    def test_existing_integration_exclusions_use_exact_swift_testing_selectors(self):
        script = SCRIPT.read_text()
        block = script.split('IOS_EXCLUSIONS=(', 1)[1].split('\n  )', 1)[0]
        actual = {line.strip().strip('"') for line in block.splitlines() if line.strip()}
        self.assertEqual(actual, {
            '-skip-testing:PersonaTests/OldHubCompatibilityIntegrationTests',
            '-skip-testing:PersonaTests/NotificationServiceTests/opensWhatTheRelayDelivered()',
            '-skip-testing:PersonaTests/VideoFileTests/nativeVideoPlaysSeeksAndCleansUp()',
            '-skip-testing:PersonaTests/VideoFileResourceTests/realHTTPReadsMiddleTailAndRejectsChangedRevision()',
        })
        self.assertIn('-skip-testing:PersonaTests/AssistantHTMLSecurityTests/aRealInfiniteLoopCanBeDiscardedWithoutBlockingANewCard()', script)

    def test_reminder_round_trip_is_enabled_with_owned_simulator_permission(self):
        script = SCRIPT.read_text()
        self.assertIn('xcrun simctl privacy "$SIM" grant reminders "$CI_BUNDLE_ID"', script)
        self.assertIn('export TEST_RUNNER_PERSONA_EVENTKIT_ROUNDTRIP=1', script)
        self.assertLess(script.index('Grant Reminders access'), script.index('iOS offline unit and design/token tests'))
        self.assertNotIn('skip-testing:PersonaTests/PlaceReminderTests', script)

    def test_mac_profile_preparation_keeps_unsigned_build_and_signed_tests(self):
        script = SCRIPT.read_text()
        prepare = script.index('Prepare profile-free Mac offline test entitlements')
        self.assertLess(script.index('Unsigned macOS app and notification extension build'), prepare)
        self.assertLess(prepare, script.index('macOS offline unit tests'))
        self.assertIn('"$SOURCE/apple/Mac/Resources/PersonaMac.entitlements"', script)
        self.assertIn('"$SOURCE/apple/PersonaNotify/PersonaMacNotify.entitlements"', script)
        self.assertIn('TEST_SIGNING=(CODE_SIGNING_ALLOWED=YES CODE_SIGN_IDENTITY=-', script)
        self.assertIn('"${TEST_SIGNING[@]}"', script.split("ci_run 'macOS offline unit tests'", 1)[1])
        self.assertNotIn('ENABLE_HARDENED_RUNTIME=NO', script)

    def test_runtime_remains_pinned_to_minimum_compiler(self):
        script = SCRIPT.read_text()
        self.assertIn('for platform in iOS watchOS; do', script)
        self.assertIn('xcodebuild -downloadPlatform "$platform" -architectureVariant universal', script)
        self.assertIn('--platform "$platform"', script)
        self.assertNotIn("-buildVersion 26.0", script)
        self.assertIn('--profile stock --runtime "$4"', script)
        self.assertIn('"$OUTPUT/simulator.log" "$IOS_RUNTIME"', script)
        self.assertIn("FAIL select Xcode 26.0.x explicitly", script)


class AppleOfflineEntitlementsTests(unittest.TestCase):
    def test_only_profile_authorized_permissions_are_removed(self):
        original = {
            'com.apple.security.app-sandbox': True,
            'com.apple.security.network.client': True,
            'com.apple.security.get-task-allow': True,
            'com.apple.developer.unrelated': ['preserve this'],
            'com.apple.developer.aps-environment': 'development',
            'com.apple.developer.usernotifications.time-sensitive': True,
            'keychain-access-groups': ['org.example.persona.ci.push'],
        }
        prepared = apple_entitlements.offline_entitlements(original)
        self.assertEqual(original.keys() - prepared.keys(), apple_entitlements.PROFILE_ENTITLEMENTS)
        for key, value in prepared.items():
            self.assertEqual(value, original[key])
        self.assertIn('keychain-access-groups', original)

    def test_missing_or_disabled_sandbox_is_rejected(self):
        for sandbox in (None, False, 'true'):
            with self.subTest(sandbox=sandbox), self.assertRaises(ValueError):
                apple_entitlements.offline_entitlements({'com.apple.security.app-sandbox': sandbox})

    def test_all_snapshot_inputs_are_validated_before_any_write(self):
        with tempfile.TemporaryDirectory() as directory:
            first = Path(directory) / 'host.entitlements'
            second = Path(directory) / 'extension.entitlements'
            original = plistlib.dumps({'com.apple.security.app-sandbox': True,
                                       'keychain-access-groups': ['org.example.persona.ci']})
            first.write_bytes(original)
            second.write_bytes(plistlib.dumps({}))
            with self.assertRaises(ValueError):
                apple_entitlements.prepare([first, second])
            self.assertEqual(first.read_bytes(), original)
            second.write_bytes(original)
            apple_entitlements.prepare([first, second])
            for path in (first, second):
                self.assertEqual(plistlib.loads(path.read_bytes()), {'com.apple.security.app-sandbox': True})


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
