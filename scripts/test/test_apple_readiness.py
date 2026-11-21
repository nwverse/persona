"""Execute the CI preparation block with owned fake devices and real result validation."""
import json
import os
from pathlib import Path
import subprocess
import textwrap
import unittest

import test_sim_boot
from test_sim_boot import FAKE_XCRUN, NEW, PHONE, ROOT, WATCH

REGULAR_UDID = "44444444-4444-4444-4444-444444444444"
REGULAR_SUITE = "AboutVersionRegularVisualTests"

SCRIPT = ROOT / "scripts/ci/apple.sh"

FAKE_XCODE = textwrap.dedent('''\
    #!/usr/bin/env python3
    import json, os, signal, sys
    from pathlib import Path
    args = sys.argv[1:]
    root = Path(os.environ["FAKE"])
    with (root / "xcode-calls.jsonl").open("a") as log:
        log.write(json.dumps(args) + "\\n")
    with (root / "xcode-env.jsonl").open("a") as log:
        log.write(json.dumps({key: os.environ[key] for key in ("TEST_RUNNER_PERSONA_SHOTS",) if key in os.environ}) + "\\n")
    if "-only-testing:PersonaTests/AboutVersionRegularVisualTests" in args:
        assert "-only-testing:PersonaTests/AboutVersionRegularVisualTests" in args, args
        assert os.environ["TEST_RUNNER_PERSONA_SHOTS"].endswith("/regular-shots")
        assert args[args.index("-destination") + 1] == "platform=iOS Simulator,id=44444444-4444-4444-4444-444444444444"
        assert args[-1] == "test-without-building"
        assert args.count("-only-testing:PersonaTests/AboutVersionRegularVisualTests") == 1
        if os.environ.get("REGULAR_STATUS", "0") != "0":
            sys.exit(int(os.environ["REGULAR_STATUS"]))
        (root / "regular-suite-started").write_text("yes")
        bundle = Path(args[args.index("-resultBundlePath") + 1])
        bundle.mkdir()
        tests = [] if os.environ.get("REGULAR_EMPTY") == "1" else [{"nodeType": "Test Case",
            "nodeIdentifier": "PersonaTests/AboutVersionRegularVisualTests/theSharedRowMatchesTheNativeControlAtRegularWidth()",
            "result": os.environ.get("REGULAR_RESULT", "Passed")}]
        (bundle / "tests.json").write_text(json.dumps({"testNodes": tests}))
        sys.exit(0)
    if args[-1] == "build-for-testing":
        assert args[args.index("-destination") + 1] == "generic/platform=iOS Simulator"
        status = int(os.environ.get("BUILD_STATUS", "0"))
        if status == 0:
            # The product the CallKit check reads (#9290): one Mach-O, known by its magic number.
            app = Path(args[args.index("-derivedDataPath") + 1]) / "Build/Products/Debug-iphonesimulator/Persona.app"
            app.mkdir(parents=True, exist_ok=True)
            (app / "Persona").write_bytes(b"\\xcf\\xfa\\xed\\xfe" + bytes(28))
        sys.exit(status)
    regular_probe = args[args.index("-resultBundlePath") + 1].endswith("/ios-regular-readiness.xcresult")
    expected_udid = "44444444-4444-4444-4444-444444444444" if regular_probe else os.environ["OWNED_UDID"]
    assert args[args.index("-destination") + 1] == "platform=iOS Simulator,id=" + expected_udid
    probe = "-only-testing:PersonaTests/TypeScaleTests" in args
    if probe:
        probe_device = expected_udid
        # Xcode manages boot, installation and test-host launch, without simctl bootstatus.
        state = json.loads((root / "state.json").read_text())
        device = next(d for devices in state["devices"].values() for d in devices
                      if d["udid"] == probe_device)
        device["state"] = "Booted"
        (root / "state.json").write_text(json.dumps(state))
        bundle = Path(args[args.index("-resultBundlePath") + 1])
        (root / "probe.pid").write_text(str(os.getpid()))
        if bundle.name == "ios-regular-readiness.xcresult":
            (root / "regular-probe-started").write_text("yes")
            if os.environ.get("REGULAR_PROBE_HANG") == "1":
                def stop(*_):
                    (root / "regular-probe.stopped").write_text("terminated")
                    sys.exit(143)
                signal.signal(signal.SIGTERM, stop)
                signal.pause()
            if os.environ.get("REGULAR_PROBE_STATUS", "0") != "0":
                sys.exit(int(os.environ["REGULAR_PROBE_STATUS"]))
        if os.environ.get("PROBE_HANG") == "1":
            def stop(*_):
                (root / "probe.stopped").write_text("terminated")
                sys.exit(143)
            signal.signal(signal.SIGTERM, stop)
            signal.pause()
        if os.environ.get("PROBE_STATUS", "0") != "0":
            sys.exit(int(os.environ["PROBE_STATUS"]))
        bundle = Path(args[args.index("-resultBundlePath") + 1])
        bundle.mkdir()
        status = os.environ.get("RESULT_STATUS", "Passed")
        suite = os.environ.get("RESULT_SUITE", "TypeScaleTests")
        tests = [] if status == "empty" else [{"nodeType": "Test Case",
            "nodeIdentifier": "PersonaTests/" + suite + "/usesTextTokens()", "result": status}]
        (bundle / "tests.json").write_text(json.dumps({"testNodes": tests}))
        # Xcode manages the test destination; success does not keep it booted for simctl privacy.
        state = json.loads((root / "state.json").read_text())
        device = next(d for devices in state["devices"].values() for d in devices
                      if d["udid"] == probe_device)
        device["state"] = os.environ.get("POST_PROBE_STATE", "Shutdown")
        device["isAvailable"] = os.environ.get("POST_PROBE_AVAILABLE", "1") == "1"
        (root / "state.json").write_text(json.dumps(state))
    else:
        tests = [] if os.environ.get("FULL_RESULT_EMPTY") == "1" else [{"nodeType": "Test Case",
            "nodeIdentifier": "PersonaTests/" + suite + "/usesTextTokens()",
            "result": os.environ.get("FULL_RESULT", "Passed")} for suite in os.environ.get("FULL_RESULT_SUITE", "TypeScaleTests").split(",")]
        (root / "full-tests.json").write_text(json.dumps({"testNodes": tests}))
        (root / "full-suite-started").write_text("yes")
        sys.exit(int(os.environ.get("FULL_STATUS", "0")))
    ''')

FAKE_XCRUN_WRAPPER = textwrap.dedent('''\
    #!/usr/bin/env python3
    import json, os, signal, sys
    from pathlib import Path
    args = sys.argv[1:]
    root = Path(os.environ["FAKE"])
    if args[:1] == ["xcresulttool"]:
        (root / "result.pid").write_text(str(os.getpid()))
        if os.environ.get("REGULAR_PROBE_HANG") == "1" or os.environ.get("RESULT_HANG") == "1":
            def stop(*_):
                (root / "result.stopped").write_text("terminated")
                sys.exit(143)
            signal.signal(signal.SIGTERM, stop)
            signal.pause()
        if args[1:3] == ["test-results", "tests"] or args[2:4] == ["test-results", "tests"]:
            bundle = Path(args[args.index("--path") + 1])
            if bundle.name == "ios-unit.xcresult":
                print((root / "full-tests.json").read_text())
            else:
                print((bundle / "tests.json").read_text())
        else:
            bundle = Path(args[args.index("--path") + 1])
            print((bundle / "tests.json").read_text())
    elif args[:2] == ["simctl", "privacy"]:
        assert args[2:] == [os.environ["OWNED_UDID"], "grant", "reminders", "org.example.persona.ci"], args
        (root / "privacy-attempted").write_text("yes")
        state = json.loads((root / "state.json").read_text())
        device = next(d for devices in state["devices"].values() for d in devices
                      if d["udid"] == args[2])
        if device["state"] != "Booted":
            print("SimError 405: Unable to lookup in current state: " + device["state"], file=sys.stderr)
            sys.exit(149)
        if os.environ.get("PRIVACY_STATUS", "0") != "0":
            sys.exit(int(os.environ["PRIVACY_STATUS"]))
        (root / "privacy-granted").write_text("yes")
    else:
        if args[:2] == ["simctl", "boot"] and os.environ.get("BOOT_HANG") == "1":
            assert args[2:] == [os.environ["OWNED_UDID"]], args
            def stop(*_):
                (root / "boot.stopped").write_text("terminated")
                sys.exit(143)
            signal.signal(signal.SIGTERM, stop)
            signal.pause()
        os.execv(str(root / "bin/fake-simctl"), ["xcrun", *args])
    ''')


# `otool -arch all -L <file>` for the CallKit check (#9290): libSystem, and CallKit as well with OTOOL_CALLKIT=1.
FAKE_OTOOL = textwrap.dedent('''\
    #!/usr/bin/env python3
    import os, sys
    from pathlib import Path
    with (Path(os.environ["FAKE"]) / "otool-calls").open("a") as log:
        log.write(" ".join(sys.argv[1:]) + "\\n")
    print(sys.argv[-1] + ":")
    print("\\t/usr/lib/libSystem.B.dylib (compatibility version 1.0.0, current version 1.0.0)")
    if os.environ.get("OTOOL_CALLKIT") == "1":
        print("\\t/System/Library/Frameworks/CallKit.framework/CallKit (compatibility version 1.0.0, current version 1.0.0)")
    ''')


FAKE_CLEANUP_SIMCTL = '''#!/usr/bin/env python3
import os
import sys
from pathlib import Path
with (Path(os.environ["FAKE"]) / "cleanup-calls").open("a") as log:
    log.write(" ".join(sys.argv[1:]) + "\\n")
if sys.argv[1:] == ["simctl", "list", "devices", "-j"]:
    print('{"devices":{"runtime":[{"udid":"44444444-4444-4444-4444-444444444444","name":"owned regular"}]}}')
'''


class AppleReadinessTests(unittest.TestCase):
    def setUp(self):
        self.fixture = test_sim_boot.SimBootTests()
        self.fixture.setUp()
        self.addCleanup(self.fixture.tearDown)
        self.root = self.fixture.dir
        self.fixture.state["bootstatus_hang"] = True
        self.fixture.save()
        for name, body in (("xcodebuild", FAKE_XCODE), ("xcrun", FAKE_XCRUN_WRAPPER),
                           ("fake-simctl", FAKE_XCRUN), ("otool", FAKE_OTOOL)):
            if name == "fake-simctl":
                body = body.replace(
                    'if a[0] == "create":\n',
                    'if a[0] == "create":\n'
                    '    st["next"] = "' + NEW + '" if "iPhone" in a[2] else "' + REGULAR_UDID + '"\n')
            path = self.fixture.bin / name
            path.write_text(body)
            path.chmod(0o755)
        self.state = self.fixture.state
        self.state["next"] = REGULAR_UDID
        self.state["diagnostic_udid"] = NEW
        self.state["devicetypes"].append({"name": "iPad Pro 11-inch (M5)", "identifier": "ipad-regular-fixture"})
        for runtime in self.state["runtimes"]:
            if runtime["identifier"] == "com.apple.CoreSimulator.SimRuntime.iOS-27-0":
                runtime["supportedDeviceTypes"].append({"identifier": "ipad-regular-fixture"})
        self.fixture.state = self.state
        self.fixture.save()
        self.output = self.root / "output"
        self.output.mkdir()
        (self.output / "config.json").write_text(json.dumps({"app": {"bundle_id": "org.example.persona.ci"}}))

    def run_preparation(self, regular_only=False, **options):
        source = SCRIPT.read_text()
        helpers = source[source.index("simulator_udid() {"):source.index("cleanup() {")]
        helpers += source[source.index("result() {"):source.index('if [ "$LANE" = all ]', source.index("test_batch() {"))]
        settings = source.index('  if [ "$LANE" = ios18 ]; then',
                                source.index('if [ "$LANE" != packages ] && [ "$LANE" != ios-build ]; then'))
        helpers += source[settings:source.index('  IOS_COMMON=(', settings)]
        if regular_only:
            start = source.index("  # The same compiled fixture and compatible runtime as #11289")
            block = source[start:source.index('  ci_run \'Verify token baseline', start)]
            owned_udid = REGULAR_UDID
        else:
            start = source.index('  IOS_COMMON=(')
            block = source[start:source.index('  result ios-unit "$SIM" TypeScaleTests IconVocabularyTests', start)]
            ordinary_result = source.index('  result ios-unit "$SIM" TypeScaleTests IconVocabularyTests', start)
            result_end = source.index('\n  fi', ordinary_result) + 3
            block += source[ordinary_result:result_end] + '\n  fi\n'
            block += source[source.index("  # The same compiled fixture and compatible runtime as #11289"):
                            source.index('  ci_run \'Verify token baseline', start)]
            owned_udid = NEW
        shell = '''set -euo pipefail
ci_run() {
  local label="$1" budget="$2"; shift 2
  printf '%s|%s\\n' "$label" "$budget" >> "$FAKE/budgets"
  python3 "$CI_ROOT/scripts/ci/run.py" --label "$label" --timeout "$TEST_STEP_TIMEOUT" -- "$@"
}
XCODE=(xcodebuild)
TEST_SIGNING=(CODE_SIGNING_ALLOWED=YES CODE_SIGN_IDENTITY=- CODE_SIGN_STYLE=Manual DEVELOPMENT_TEAM= PROVISIONING_PROFILE_SPECIFIER=)
''' + helpers + '''
IOS_COMMON=(xcodebuild)
''' + block
        env = dict(os.environ, PATH=str(self.fixture.bin) + os.pathsep + os.environ["PATH"],
                   LANE="ios", CI_ROOT=str(ROOT), SOURCE=str(ROOT), OUTPUT=str(self.output),
                   PERSONA_CONFIG=str(self.output / "config.json"), SIM_NAME="owned readiness fixture",
                   IOS_RUNTIME="com.apple.CoreSimulator.SimRuntime.iOS-27-0", OWNED_UDID=owned_udid,
                   REGULAR_SIM_NAME="owned regular About fixture",
                   FAKE=str(self.root), TEST_STEP_TIMEOUT="10")
        env.update(options)
        env.setdefault("FULL_RESULT_SUITE", "TypeScaleTests,IconVocabularyTests,TokenRatchetTests,StyleSpecTests")
        result = subprocess.run(["bash", "-c", shell], cwd=ROOT, env=env,
                                capture_output=True, text=True, timeout=25)
        calls = self.root / "xcode-calls.jsonl"
        return result, [json.loads(line) for line in calls.read_text().splitlines()] if calls.exists() else []

    def test_xcode_probe_replaces_the_hung_bootstatus_and_preserves_full_selection(self):
        result, calls = self.run_preparation()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(calls), 5)
        self.assertEqual([call[-1] for call in calls], ["build-for-testing"] + ["test-without-building"] * 4)
        selectors = lambda args: [a for a in args if a.startswith(("-only-testing:", "-skip-testing:"))]
        self.assertEqual(selectors(calls[0]), selectors(calls[2]))
        self.assertEqual(selectors(calls[1]), ["-only-testing:PersonaTests/TypeScaleTests"])
        self.assertIn("-only-testing:PersonaTests", calls[2])
        self.assertIn("-skip-testing:PersonaTests/" + REGULAR_SUITE, calls[0])
        self.assertNotIn("-only-testing:PersonaTests/" + REGULAR_SUITE, calls[2])
        self.assertIn("ARCHS=" + os.uname().machine, calls[0])
        self.assertIn("ONLY_ACTIVE_ARCH=YES", calls[0])
        for call in calls[1:]:
            self.assertFalse(any(arg.startswith(("ARCHS=", "ONLY_ACTIVE_ARCH=")) for arg in call))
        for call in calls:
            self.assertIn("CODE_SIGNING_ALLOWED=YES", call)
            self.assertEqual(call[call.index("-derivedDataPath") + 1], str(self.output / "ios-tests"))
        context = json.loads((self.output / "simulator-readiness-context.json").read_text())
        self.assertEqual(context["udid"], NEW)
        self.assertEqual(context["bundle_id"], "org.example.persona.ci")
        self.assertEqual(set(context), {"udid", "bundle_id", "started_at"})
        sim_calls = (self.root / "calls").read_text()
        self.assertNotIn("simctl bootstatus", sim_calls)
        self.assertEqual([line for line in sim_calls.splitlines() if "simctl boot " in line],
                         ["xcrun simctl boot " + NEW])
        self.assertEqual(sim_calls.count("simctl create"), 2)
        self.assertIn('regular About fixture', (self.root / "calls").read_text())
        self.assertIn('com.apple.CoreSimulator.SimRuntime.iOS-27-0', sim_calls)
        self.assertTrue((self.root / "regular-probe-started").exists())
        self.assertTrue((self.root / "regular-suite-started").exists())
        self.assertIn("ios-about-regular.xcresult: 1 test cases, zero failed/skipped", result.stdout)
        self.assertEqual(self.fixture.device(REGULAR_UDID)["state"], "Shutdown")
        self.assertTrue((self.root / "privacy-granted").exists())
        self.assertTrue((self.root / "full-suite-started").exists())
        self.assertEqual(self.fixture.device(PHONE)["state"], "Shutdown")
        self.assertEqual(self.fixture.device(WATCH)["state"], "Shutdown")
        self.assertIn("1 test cases, zero failed/skipped", (self.output / "simulator-readiness.log").read_text())
        self.assertIn("1 test cases, zero failed/skipped", (self.output / "regular-readiness.log").read_text())
        self.assertEqual((self.root / "budgets").read_text().splitlines(), [
            "Build iOS offline unit tests|1800", "No CallKit in the simulator build|300",
            "Create stock iOS 26.0 test simulator|100",
            "Verify hosted iOS test readiness|900", "Grant Reminders access to the owned test app|300",
            "iOS offline unit and design/token tests|1800", "Inspect test results (ios-unit)|120",
            "Create stock regular-width About test simulator|100",
            "Verify hosted regular iPad test readiness|900",
            "iOS regular-width About visual test on real iPad|600",
            "Inspect test results (ios-about-regular)|120",
        ])
        self.assertIn("TEST_RUNNER_PERSONA_SHOTS",
                      (self.root / "xcode-env.jsonl").read_text())

    def test_compact_visual_output_directory_is_run_specific_and_not_a_skip(self):
        result, calls = self.run_preparation()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        expected = str(self.output / "shots")
        environments = [json.loads(line) for line in (self.root / "xcode-env.jsonl").read_text().splitlines()]
        self.assertEqual(environments[:2], [{}, {}])
        self.assertEqual(environments[2:], [{"TEST_RUNNER_PERSONA_SHOTS": expected},
                                           {"TEST_RUNNER_PERSONA_SHOTS": expected},
                                           {"TEST_RUNNER_PERSONA_SHOTS": str(self.output / "regular-shots")}])
        for call in calls:
            self.assertFalse(any(argument.startswith("PERSONA_SHOTS=") for argument in call))
        support = (ROOT / "apple/PersonaTests/AboutVersionRefractTests.swift").read_text()
        self.assertIn('try #require(ProcessInfo.processInfo.environment["PERSONA_SHOTS"])', support)
        self.assertIn("native.pixels == shared.pixels", support)
        self.assertIn("try FileManager.default.createDirectory(at: root", support)
        self.assertNotIn('IOS_UNIT_SELECTION+=(-skip-testing:PersonaTests/AboutVersionCompactVisualTests)',
                         SCRIPT.read_text()[SCRIPT.read_text().index('if [ "$LANE" != packages ] && [ "$LANE" != ios-build ]; then'):SCRIPT.read_text().index('if [ "$LANE" = ios18 ]; then', SCRIPT.read_text().index('IOS_FAILED=0'))])

    def test_cleanup_removes_both_owned_destinations_including_an_unparsed_ipad(self):
        source = SCRIPT.read_text()
        start = source.index("cleanup_device() {")
        block = source[start:source.index("cleanup() {", start)]
        tools = {"xcrun": FAKE_CLEANUP_SIMCTL}
        for name, body in tools.items():
            path = self.fixture.bin / name
            path.write_text(body)
            path.chmod(0o755)
        shell = '''set -euo pipefail
ci_run() { shift 2; "$@"; }
SIM="33333333-3333-3333-3333-333333333333"
SIM_STARTED=1
SIM_NAME="owned phone"
REGULAR_SIM=""
REGULAR_SIM_STARTED=1
REGULAR_SIM_NAME="owned regular"
''' + block + '''
cleanup_device "$SIM" "$SIM_NAME" "$SIM_STARTED"
cleanup_device "$REGULAR_SIM" "$REGULAR_SIM_NAME" "$REGULAR_SIM_STARTED"
'''
        result = subprocess.run(["bash", "-c", shell], cwd=ROOT, text=True, capture_output=True, timeout=10,
                                env=dict(os.environ, PATH=str(self.fixture.bin) + os.pathsep + os.environ["PATH"], FAKE=str(self.root)))
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        calls = (self.root / "cleanup-calls").read_text().splitlines()
        self.assertEqual(calls, [f"simctl shutdown {NEW}", f"simctl delete {NEW}",
                                 "simctl list devices -j", f"simctl shutdown {REGULAR_UDID}",
                                 f"simctl delete {REGULAR_UDID}"])

    def test_regular_about_suite_uses_an_owned_real_ipad_and_strict_result(self):
        result, calls = self.run_preparation(regular_only=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(calls), 2)
        probe, call = calls
        self.assertEqual([a for a in probe if a.startswith(("-only-testing:", "-skip-testing:"))],
                         ["-only-testing:PersonaTests/TypeScaleTests"])
        self.assertEqual(probe[probe.index("-destination") + 1], "platform=iOS Simulator,id=" + REGULAR_UDID)
        self.assertEqual(probe[probe.index("-resultBundlePath") + 1], str(self.output / "ios-regular-readiness.xcresult"))
        self.assertEqual(call[-1], "test-without-building")
        self.assertEqual(call[call.index("-destination") + 1],
                         "platform=iOS Simulator,id=" + REGULAR_UDID)
        self.assertEqual([a for a in call if a.startswith(("-only-testing:", "-skip-testing:"))],
                         ["-only-testing:PersonaTests/" + REGULAR_SUITE])
        self.assertEqual((self.output / "regular-simulator.log").read_text().strip(), REGULAR_UDID)
        self.assertIn('ipad-regular-fixture', (self.root / "calls").read_text())
        self.assertIn('com.apple.CoreSimulator.SimRuntime.iOS-27-0', (self.root / "calls").read_text())
        self.assertIn("1 test cases, zero failed/skipped", result.stdout)
        self.assertTrue((self.root / "regular-probe-started").exists())
        self.assertTrue((self.root / "regular-suite-started").exists())
        self.assertEqual(self.fixture.device(PHONE)["state"], "Shutdown")

    def test_regular_readiness_must_pass_before_the_regular_suite(self):
        for options in ({"REGULAR_PROBE_STATUS": "65"}, {"RESULT_STATUS": "Failed"},
                        {"RESULT_STATUS": "Skipped"}, {"RESULT_STATUS": "empty"},
                        {"RESULT_SUITE": "UnrelatedTests"}):
            with self.subTest(options=options):
                case = AppleReadinessTests()
                case.setUp()
                try:
                    result, calls = case.run_preparation(regular_only=True, **options)
                    self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertEqual(len(calls), 1)
                    self.assertTrue((case.root / "regular-probe-started").exists())
                    self.assertFalse((case.root / "regular-suite-started").exists())
                finally:
                    case.doCleanups()

    def test_regular_readiness_and_result_inspection_share_one_deadline(self):
        for key, marker in (("REGULAR_PROBE_HANG", "regular-probe.stopped"),
                            ("RESULT_HANG", "result.stopped")):
            with self.subTest(phase=key):
                case = AppleReadinessTests()
                case.setUp()
                # The global fixture models a hung legacy bootstatus. This case
                # exercises the separate iPad probe, so its ordinary boot returns.
                case.fixture.state["bootstatus_hang"] = False
                case.fixture.save()
                try:
                    result, calls = case.run_preparation(regular_only=True, TEST_STEP_TIMEOUT="1", **{key: "1"})
                    self.assertEqual(result.returncode, 124, result.stdout + result.stderr)
                    self.assertEqual(len(calls), 1)
                    self.assertEqual((case.root / marker).read_text(), "terminated")
                    self.assertFalse((case.root / "regular-suite-started").exists())
                finally:
                    case.doCleanups()

    def test_regular_about_failure_empty_or_skipped_results_cannot_pass(self):
        for options in ({"REGULAR_STATUS": "7"}, {"REGULAR_EMPTY": "1"}, {"REGULAR_RESULT": "Skipped"}):
            with self.subTest(options=options):
                case = AppleReadinessTests()
                case.setUp()
                try:
                    result, calls = case.run_preparation(regular_only=True, **options)
                    self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertEqual(len(calls), 2)
                finally:
                    case.doCleanups()

    def test_callkit_in_the_built_app_stops_before_creating_any_device(self):
        result, calls = self.run_preparation(OTOOL_CALLKIT="1")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("Persona.app/Persona → /System/Library/Frameworks/CallKit.framework/CallKit",
                      result.stdout + result.stderr)
        self.assertEqual(len(calls), 1)
        app = self.output / "ios-tests/Build/Products/Debug-iphonesimulator/Persona.app"
        self.assertEqual((self.root / "otool-calls").read_text(), "-arch all -L %s\n" % (app / "Persona"))
        self.assertFalse((self.root / "calls").exists())

    def test_build_failure_stops_before_creating_any_device(self):
        result, calls = self.run_preparation(BUILD_STATUS="7")
        self.assertEqual(result.returncode, 7, result.stdout + result.stderr)
        self.assertEqual(len(calls), 1)
        self.assertFalse((self.root / "calls").exists())

    def test_probe_command_failure_stops_before_permissions_and_full_suite(self):
        result, calls = self.run_preparation(PROBE_STATUS="65")
        self.assertEqual(result.returncode, 65, result.stdout + result.stderr)
        self.assertEqual(len(calls), 2)
        self.assertFalse((self.root / "privacy-granted").exists())
        self.assertFalse((self.root / "full-suite-started").exists())

    def test_probe_results_must_be_nonempty_passed_and_from_the_selected_suite(self):
        for options in ({"RESULT_STATUS": "Failed"}, {"RESULT_STATUS": "Skipped"},
                        {"RESULT_STATUS": "empty"}, {"RESULT_SUITE": "UnrelatedTests"}):
            with self.subTest(options=options):
                # Each attempt owns a separate device fixture; no retry of a failed probe.
                case = AppleReadinessTests()
                case.setUp()
                try:
                    result, calls = case.run_preparation(**options)
                    self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertEqual(len(calls), 2)
                    self.assertFalse((case.root / "full-suite-started").exists())
                    self.assertFalse((case.root / "privacy-granted").exists())
                finally:
                    case.doCleanups()

    def test_probe_and_result_inspection_share_one_deadline(self):
        for key, pid_name in (("PROBE_HANG", "probe.pid"), ("RESULT_HANG", "result.pid")):
            with self.subTest(phase=key):
                case = AppleReadinessTests()
                case.setUp()
                try:
                    result, _ = case.run_preparation(**{key: "1", "TEST_STEP_TIMEOUT": "1"})
                    self.assertEqual(result.returncode, 124, result.stdout + result.stderr)
                    self.assertFalse((case.root / "full-suite-started").exists())
                    self.assertTrue((case.root / pid_name).exists())
                    # The deadline terminates the probe/result process, not just its shell.
                    self.assertEqual((case.root / pid_name.replace(".pid", ".stopped")).read_text(), "terminated")
                finally:
                    case.doCleanups()

    def test_probe_leaving_device_booted_does_not_boot_it_again(self):
        result, calls = self.run_preparation(POST_PROBE_STATE="Booted")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(calls), 5)
        self.assertNotIn("simctl boot", (self.root / "calls").read_text())
        self.assertTrue((self.root / "privacy-granted").exists())

    def test_permission_handoff_rejects_unavailable_or_transitional_device(self):
        for options in ({"POST_PROBE_STATE": "Booting"}, {"POST_PROBE_STATE": "Shutting Down"},
                        {"POST_PROBE_AVAILABLE": "0"}):
            with self.subTest(options=options):
                case = AppleReadinessTests()
                case.setUp()
                try:
                    result, calls = case.run_preparation(**options)
                    self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertEqual(len(calls), 2)
                    self.assertNotIn("simctl boot", (case.root / "calls").read_text())
                    self.assertFalse((case.root / "privacy-attempted").exists())
                    self.assertFalse((case.root / "full-suite-started").exists())
                finally:
                    case.doCleanups()

    def test_failed_start_prevents_permission_and_full_suite(self):
        self.fixture.state["boot_rc"] = 7
        self.fixture.save()
        result, calls = self.run_preparation()
        self.assertEqual(result.returncode, 7, result.stdout + result.stderr)
        self.assertEqual(len(calls), 2)
        self.assertFalse((self.root / "privacy-attempted").exists())
        self.assertFalse((self.root / "full-suite-started").exists())
        self.assertEqual((self.root / "calls").read_text().count("simctl boot "), 1)

    def test_permission_failure_is_not_retried_or_allowed_to_start_full_suite(self):
        result, calls = self.run_preparation(PRIVACY_STATUS="149")
        self.assertEqual(result.returncode, 149, result.stdout + result.stderr)
        self.assertEqual(len(calls), 2)
        self.assertTrue((self.root / "privacy-attempted").exists())
        self.assertFalse((self.root / "privacy-granted").exists())
        self.assertFalse((self.root / "full-suite-started").exists())
        self.assertEqual((self.root / "calls").read_text().count("simctl boot "), 1)

    def test_start_is_inside_the_existing_permission_deadline(self):
        result, calls = self.run_preparation(BOOT_HANG="1", TEST_STEP_TIMEOUT="1")
        self.assertEqual(result.returncode, 124, result.stdout + result.stderr)
        self.assertEqual(len(calls), 2)
        self.assertEqual((self.root / "boot.stopped").read_text(), "terminated")
        self.assertFalse((self.root / "privacy-attempted").exists())
        self.assertFalse((self.root / "full-suite-started").exists())
        self.assertEqual((self.root / "budgets").read_text().splitlines()[-1],
                         "Grant Reminders access to the owned test app|300")

    def test_full_suite_failure_is_not_masked_by_successful_readiness(self):
        result, calls = self.run_preparation(FULL_STATUS="7")
        self.assertEqual(result.returncode, 7, result.stdout + result.stderr)
        self.assertEqual(len(calls), 3)


if __name__ == "__main__":
    unittest.main()
