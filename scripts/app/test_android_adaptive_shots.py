import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import android_adaptive_shots as shots


APP_ID = "com.example.persona.shots"
TASK_DUMP = """
Task{abcdef #42 I=com.example.persona.shots/persona.android.MainActivity}
  mBounds=Rect(0, 0 - 1200, 960)
  CurrentConfiguration: {1.0 320dpi sw600dp w600dp h480dp}
  ActivityRecord{fedcba com.example.persona.shots/persona.android.MainActivity t42}
topResumedActivity=ActivityRecord{fedcba com.example.persona.shots/persona.android.MainActivity t42}
"""
OTHER_TASK_DUMP = TASK_DUMP.replace(APP_ID, "com.example.other")


class FakeAdb:
    def __init__(self):
        self.serial = "emulator-test"
        self.commands = []
        self.ui_failures = 0
        self.png_frames = []
        self.ui_text = ""
        self.window = {
            "widthDp": 600, "heightDp": 480, "widthPx": 1200, "heightPx": 960,
            "densityDpi": 320, "orientation": "landscape", "taskId": 42, "bounds": (0, 0, 1200, 960),
            "evidence": "task-configuration",
        }

    def run(self, arguments, timeout=20, binary=False, check=True):
        self.commands.append(list(arguments))
        if binary:
            if self.png_frames:
                return self.png_frames.pop(0)
            return shots.PNG_SIGNATURE + b"frame"
        return b"" if binary else ""

    def shell(self, *arguments, timeout=20, check=True):
        self.commands.append(list(arguments))
        if arguments[:2] == ("wm", "size"):
            return "Physical size: 2560x1600\n"
        if arguments[:2] == ("wm", "density"):
            return "Physical density: 320\n"
        if arguments[:1] == ("pidof",):
            return "42\n"
        if arguments[:1] == ("getprop",):
            return "en-US\n"
        if arguments[:2] == ("dumpsys", "activity"):
            return TASK_DUMP
        if arguments[:2] == ("dumpsys", "window"):
            return "mCurrentRotation=1\n"
        if arguments[:2] == ("dumpsys", "device_state"):
            return "mCommittedState=OPENED\n"
        if arguments[:2] == ("uiautomator", "dump"):
            if self.ui_failures:
                self.ui_failures -= 1
                raise shots.AdaptiveFailure("null root")
            return "UI hierchary dumped to: %s" % arguments[2]
        if arguments[:1] == ("cat",) and arguments[1] == shots.UI_PATH:
            return '<hierarchy><node resource-id="field" text="%s" content-desc="" class="EditText" bounds="[0,0][10,10]" focused="true" /></hierarchy>' % self.ui_text
        if arguments[:2] == ("cat", "/proc/uptime"):
            return "100.00 100.00\n"
        if arguments[:2] == ("run-as", APP_ID) and "cat" in arguments and "shell-position.json" in arguments:
            return '{"tab":"Agents","chats":{"Agents":{"machineId":"machine","sessionId":"session"}}}'
        if arguments[:2] == ("run-as", APP_ID) and "shell-position.json" in arguments:
            return "no_backup/hub-data/scope/shell-position.json\n"
        return ""


def scenario(steps=None):
    return {
        "id": "test-scene", "manifestId": "adaptive-test", "description": "test",
        "requires": {"fields": []}, "steps": steps or [{"step": "launch"}, {"step": "capture", "name": "one", "state": "flat"}],
    }


def make_runner(adb, output, document=None, reset=False):
    environment = {
        "APP_ID": APP_ID, "MOCK_ADDRESS": "http://127.0.0.1:19000", "SERIAL": adb.serial,
        "SHOTS_SETTLE": "0", "SHOTS_STYLE": "glass",
    }
    with mock.patch.dict(os.environ, environment, clear=False):
        return shots.AdaptiveRunner(adb, document or scenario(), output, "light", reset=reset)


class AdaptiveShotsTest(unittest.TestCase):
    def test_steps_never_restart_and_each_theme_may_clear_once(self):
        document = scenario([{"step": "launch"}, {"step": "open", "tab": "Agents"},
                             {"step": "rotate"}, {"step": "capture", "name": "one", "state": "flat"}])
        for reset in (False, True):
            adb = FakeAdb()
            with tempfile.TemporaryDirectory() as output:
                runner = make_runner(adb, output, document, reset=reset)
                runner.wait_ready = mock.Mock()
                runner.rotate = mock.Mock()
                runner.capture = mock.Mock(side_effect=lambda step, index: setattr(runner, "screenshots", 1))
                runner.cleanup = mock.Mock()
                runner.run()
            flattened = [str(item) for command in adb.commands for item in command]
            self.assertNotIn("force-stop", flattened)
            clears = [command for command in adb.commands if command[:2] == ["pm", "clear"]]
            self.assertEqual(1 if reset else 0, len(clears))
            if clears:
                clear_index = adb.commands.index(clears[0])
                self.assertTrue(all(command[:1] == ["wm"] for command in adb.commands[:clear_index]))

    def test_transforms_execute_before_bounded_readback(self):
        for method, step, expected in (
            ("rotate", {"orientation": "landscape"}, ["emu", "rotate"]),
            ("posture", {"value": 2}, ["emu", "posture", "2"]),
            ("wm-size", {"size": "1200x960", "density": 320}, ["wm", "size", "1200x960"]),
            ("freeform-resize", {"widthDp": 600, "heightDp": 480}, ["am", "task", "resize", "42", "0", "0", "1200", "960"]),
        ):
            with self.subTest(method=method), tempfile.TemporaryDirectory() as output:
                adb = FakeAdb()
                runner = make_runner(adb, output)
                runner.read_window = mock.Mock(return_value=adb.window)
                runner.device_state = mock.Mock(return_value="HALF_OPENED")
                with mock.patch.object(shots, "poll", side_effect=shots.AdaptiveFailure("TIMEOUT condition")) as waiting:
                    with self.assertRaisesRegex(shots.AdaptiveFailure, "TIMEOUT"):
                        getattr(runner, method.replace("-", "_"))(step)
                waiting.assert_called_once()
                self.assertIn(expected, adb.commands)

    def test_poll_timeout_is_nonzero_verdict(self):
        with self.assertRaisesRegex(shots.AdaptiveFailure, "TIMEOUT example"):
            shots.poll("example", lambda: None, timeout=0)

    def test_current_task_must_be_persona(self):
        task = shots.parse_persona_task(TASK_DUMP, APP_ID)
        self.assertEqual(42, task.task_id)
        self.assertEqual((0, 0, 1200, 960), task.bounds)
        self.assertEqual((600, 600, 480), task.configuration_dp)
        with self.assertRaisesRegex(shots.AdaptiveFailure, "not Persona"):
            shots.parse_persona_task(OTHER_TASK_DUMP, APP_ID)
        with self.assertRaisesRegex(shots.AdaptiveFailure, "resumed"):
            shots.parse_persona_task(TASK_DUMP.replace("topResumedActivity", "otherActivity"), APP_ID)

    def test_freeform_right_bottom_coordinates_match_boundary_table(self):
        for width, height, right, bottom in ((599, 480, 1198, 960), (600, 480, 1200, 960),
                                             (759, 400, 1518, 800), (760, 400, 1520, 800),
                                             (839, 480, 1678, 960), (840, 480, 1680, 960)):
            with self.subTest(width=width, height=height), tempfile.TemporaryDirectory() as output:
                adb = FakeAdb()
                runner = make_runner(adb, output)
                target = dict(adb.window, widthDp=width, heightDp=height, widthPx=right, heightPx=bottom)
                runner.read_window = mock.Mock(side_effect=[adb.window, target])
                runner.freeform_resize({"widthDp": width, "heightDp": height})
                self.assertIn(["am", "task", "resize", "42", "0", "0", str(right), str(bottom)], adb.commands)

    def test_ui_dump_deletes_old_xml_and_retries_at_most_five_times(self):
        adb = FakeAdb()
        adb.ui_failures = 4
        with tempfile.TemporaryDirectory() as output, mock.patch.object(shots.time, "sleep") as sleeping:
            runner = make_runner(adb, output)
            self.assertTrue(runner.ui_tree())
        self.assertEqual(4, sleeping.call_count)
        sleeping.assert_has_calls([mock.call(0.5)] * 4)
        self.assertEqual(5, adb.commands.count(["rm", "-f", shots.UI_PATH]))
        adb.ui_failures = 5
        with self.assertRaisesRegex(shots.AdaptiveFailure, "after 5 fresh attempts"):
            runner.ui_tree()
        self.assertEqual(10, adb.commands.count(["rm", "-f", shots.UI_PATH]))
        with self.assertRaises(shots.AdaptiveFailure):
            shots.parse_ui_nodes(b"<hierarchy />")

    def test_png_warning_is_stripped_and_bad_frames_fail(self):
        self.assertEqual(shots.PNG_SIGNATURE + b"data", shots.strip_png(b"warning\n" + shots.PNG_SIGNATURE + b"data"))
        for data in (b"", b"not a png"):
            with self.subTest(data=data), self.assertRaises(shots.AdaptiveFailure):
                shots.strip_png(data)

    def test_stable_frame_timeout_does_not_save_last_frame(self):
        adb = FakeAdb()
        adb.png_frames = [shots.PNG_SIGNATURE + bytes([index]) for index in range(5)]
        with tempfile.TemporaryDirectory() as output:
            runner = make_runner(adb, output)
            with mock.patch.object(shots.time, "monotonic", side_effect=[0, 1, 2, 3, 21]), \
                 mock.patch.object(shots.time, "sleep"):
                with self.assertRaisesRegex(shots.AdaptiveFailure, "TIMEOUT stable"):
                    runner.stable_png()
            self.assertEqual([], list(Path(output).glob("*.png")))

    def test_missing_required_probe_field_and_pid_change_fail(self):
        with tempfile.TemporaryDirectory() as output:
            adb = FakeAdb()
            document = scenario()
            document["requires"] = {"page": "chat", "fields": ["draftHmac"]}
            runner = make_runner(adb, output, document)
            runner.read_probe = mock.Mock(return_value={"session": "session", "pages": {}})
            runner.page_evidence = mock.Mock(return_value={"updatedAtNanos": 99_000_000_000})
            runner.elapsed_nanos = mock.Mock(return_value=100_000_000_000)
            with self.assertRaisesRegex(shots.AdaptiveFailure, "draftHmac"):
                runner.collect("capture", "flat", 42)
            runner.initial_pid = 42
            runner.pid = mock.Mock(return_value=43)
            with self.assertRaisesRegex(shots.AdaptiveFailure, "PID changed"):
                runner.check_pid()

    def test_sidecar_is_complete_and_contains_no_draft_or_hmac_key(self):
        with tempfile.TemporaryDirectory() as output:
            adb = FakeAdb()
            runner = make_runner(adb, output)
            runner.stable_png = mock.Mock(return_value=shots.PNG_SIGNATURE + b"image")
            runner.collect = mock.Mock(return_value={
                "scenario": "test-scene", "step": "one", "state": "flat", "serial": adb.serial,
                "pidBefore": 42, "pidAfter": 42, "initialPid": 42, "window": adb.window,
                "widthDp": 600, "heightDp": 480, "orientation": "landscape", "deviceState": "OPENED",
                "postureEvidenceType": "none", "style": "glass", "theme": "light", "locale": "en-US",
                "route": "Agents/chat", "selectedKey": "machine/session", "firstVisibleKey": None,
                "firstVisibleOffsetPx": None, "draftHmac": "a" * 64, "selection": {"start": 0, "end": 4},
                "focused": True, "focusedNode": {"resource-id": "field"}, "attachments": [],
                "contentBounds": None, "hingeBounds": None, "probePages": {}, "columns": None, "detailAgentsExpanded": None,
                "assertions": [], "timestamp": "2026-01-01T00:00:00+00:00",
            })
            runner.capture({"name": "one", "state": "flat"}, 0)
            sidecar = Path(output, "test-scene", "one-flat-glass-light.json")
            data = json.loads(sidecar.read_text())
            for key in ("scenario", "step", "serial", "pidBefore", "pidAfter", "window", "orientation",
                        "deviceState", "postureEvidenceType", "style", "theme", "locale", "route", "selectedKey",
                        "firstVisibleKey", "firstVisibleOffsetPx", "draftHmac", "selection", "focused",
                        "focusedNode", "attachments", "contentBounds", "hingeBounds", "probePages", "columns",
                        "detailAgentsExpanded", "assertions", "timestamp", "pngSha256"):
                self.assertIn(key, data)
            self.assertNotIn("secret draft", sidecar.read_text())
            self.assertNotIn("hmacKey", sidecar.read_text())
            self.assertTrue(sidecar.with_suffix(".png").read_bytes().startswith(shots.PNG_SIGNATURE))

    def test_multiple_probe_pages_and_per_step_requirements(self):
        with tempfile.TemporaryDirectory() as output:
            adb = FakeAdb()
            document = scenario()
            document["requires"] = {"fields": [], "pages": {
                "settings": {"page": "Settings", "fields": ["firstVisibleKey", "contentBounds", "hingeBounds"]},
                "composer": {"page": "chat", "fields": ["draftHmac"]},
            }}
            runner = make_runner(adb, output, document)
            runner.read_probe = mock.Mock(return_value={"session": "session", "pages": {}})
            runner.elapsed_nanos = mock.Mock(return_value=100_000_000_000)
            pages = {
                "Settings": {
                    "updatedAtNanos": 99_000_000_000,
                    "list": {"firstVisibleKey": "notifications", "firstVisibleOffsetPx": 12},
                    "bounds": {"content": {"left": 0, "top": 0, "right": 600, "bottom": 400},
                               "hinge": {"left": 0, "top": 400, "right": 600, "bottom": 440}},
                },
                "chat": {
                    "updatedAtNanos": 99_000_000_000,
                    "composer": {"draftHmac": "a" * 64, "selection": {"start": 0, "end": 1},
                                 "focused": True, "attachments": []},
                },
            }
            runner.page_evidence = mock.Mock(side_effect=lambda probe, name, now, visible=(): pages[name])
            snapshot = runner.collect("capture", "tabletop", 42)
            self.assertEqual({"settings", "composer"}, set(snapshot["probePages"]))
            self.assertEqual("notifications", snapshot["probePages"]["settings"]["firstVisibleKey"])
            self.assertEqual("a" * 64, snapshot["probePages"]["composer"]["draftHmac"])
            local = runner.collect("capture", "tabletop", 42,
                                   {"page": "Settings", "fields": ["firstVisibleKey", "hingeBounds"]})
            self.assertEqual("notifications", local["firstVisibleKey"])
            self.assertEqual(400, local["hingeBounds"]["top"])

    def test_hinge_bounds_assertion_rejects_overlap_and_missing_rectangles(self):
        content = {"left": 0, "top": 0, "right": 600, "bottom": 400}
        touching = {"left": 0, "top": 400, "right": 600, "bottom": 440}
        overlap = {"left": 0, "top": 399, "right": 600, "bottom": 440}
        self.assertFalse(shots.rectangles_overlap(content, touching))
        self.assertTrue(shots.rectangles_overlap(content, overlap))
        with tempfile.TemporaryDirectory() as output:
            runner = make_runner(FakeAdb(), output)
            runner._step_pid_before = 42
            runner.collect = mock.Mock(return_value={"contentBounds": content, "hingeBounds": touching})
            runner.check_assertions({"checks": {"contentBounds": {"notOverlaps": "hingeBounds"}}})
            runner.collect = mock.Mock(return_value={"contentBounds": content, "hingeBounds": overlap})
            with self.assertRaisesRegex(shots.AdaptiveFailure, "notOverlaps"):
                runner.check_assertions({"checks": {"contentBounds": {"notOverlaps": "hingeBounds"}}})
            runner.collect = mock.Mock(return_value={"contentBounds": content, "hingeBounds": None})
            with self.assertRaises(shots.AdaptiveFailure):
                runner.check_assertions({"checks": {"contentBounds": {"notOverlaps": "hingeBounds"}}})

    def test_machine_columns_detail_expansion_and_compact_dsl(self):
        compact = shots.normalize_scenario({"id": "machines", "description": "test", "requires": {"fields": []}, "steps": [
            {"launch": {"name": "initial"}}, {"open": {"page": "machines"}},
            {"wm-size": {"width": 1080, "height": 2400, "density": 420}},
            {"assert": {"pid": {"unchanged": True}, "columns": {"equals": 2}}},
            {"posture": {"state": 2}}, {"rotate": {"direction": "landscape"}},
            {"capture": {"name": "grid"}},
        ]})
        self.assertEqual("1080x2400", compact["steps"][2]["size"])
        self.assertEqual(2, compact["steps"][4]["value"])
        self.assertEqual("landscape", compact["steps"][5]["orientation"])
        self.assertEqual(2, compact["steps"][3]["checks"]["columns"]["equals"])
        self.assertEqual("capture", compact["steps"][6]["state"])
        shots.validate_scenario(compact)
        with tempfile.TemporaryDirectory() as output:
            adb = FakeAdb()
            adb.ui_text = "Show less"
            document = scenario()
            document["requires"] = {"page": "Machines", "fields": ["firstVisibleKey", "columns", "detailAgentsExpanded"]}
            runner = make_runner(adb, output, document)
            runner.read_probe = mock.Mock(return_value={"session": "session", "pages": {}})
            runner.elapsed_nanos = mock.Mock(return_value=100_000_000_000)
            runner.page_evidence = mock.Mock(return_value={
                "updatedAtNanos": 99_000_000_000,
                "list": {"firstVisibleKey": "machine-row|15:concern-pending|15:concern-offline", "firstVisibleOffsetPx": 0},
            })
            runner.note_open({"page": "machines"})
            snapshot = runner.collect("capture", "grid", 42)
            self.assertEqual(2, snapshot["columns"])
            self.assertTrue(snapshot["detailAgentsExpanded"])
            self.assertEqual("machines", snapshot["route"])
            self.assertIsNone(snapshot["selectedKey"])
            self.assertEqual(0, shots.path_value(snapshot, "firstVisibleOffset"))
            runner.transformed = True
            runner.note_open({"page": "machine:concern-needs"})
            self.assertEqual("concern-needs", runner.selected_key_override)
            with self.assertRaisesRegex(shots.AdaptiveFailure, "same page"):
                runner.note_open({"page": "machine:concern-needs"})

    def test_cleanup_targets_only_owned_device_resources(self):
        with tempfile.TemporaryDirectory() as output:
            adb = FakeAdb()
            runner = make_runner(adb, output)
            runner.size_changed = True
            runner.density_changed = True
            runner.posture_changed = True
            runner.cleanup()
            flattened = [str(item) for command in adb.commands for item in command]
            self.assertNotIn("pkill", flattened)
            self.assertNotIn("kill", flattened)
            self.assertIn(["rm", "-f", shots.UI_PATH], adb.commands)
            self.assertIn(["run-as", APP_ID, "rm", "-f", shots.PROBE_PATH], adb.commands)
            self.assertIn(["wm", "size", "reset"], adb.commands)
            self.assertIn(["wm", "density", "reset"], adb.commands)
            self.assertIn(["emu", "posture", "3"], adb.commands)

    def test_scenario_validation_and_ascii_input_boundaries(self):
        document = scenario()
        document["manifestId"] = "r1-adaptive-window"
        self.assertIs(document, shots.validate_scenario(document))
        invalid = scenario([{"step": "launch", "tab": "Agents"}, {"step": "rotate"}, {"step": "open", "tab": "Agents"}])
        with self.assertRaisesRegex(shots.AdaptiveFailure, "cannot restore"):
            shots.validate_scenario(invalid)
        self.assertEqual("hello%sworld", shots.ascii_input_argument("hello world"))
        for text in ("中文", "line\nfeed", ""):
            with self.subTest(text=text), self.assertRaises(shots.AdaptiveFailure):
                shots.ascii_input_argument(text)


if __name__ == "__main__":
    unittest.main()
