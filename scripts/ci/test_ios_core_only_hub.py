#!/usr/bin/env python3
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("ios_core_only_hub", ROOT / "scripts/ci/ios-core-only-hub.py")
DRIVER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(DRIVER)


class IOSCoreOnlyHubProofTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.shots = Path(self.temp.name)
        self.addCleanup(self.temp.cleanup)

    def proof(self, edition, run_id="run"):
        full = edition == "full"
        entries = {name: full for name in (
            "sidebar-health", "settings-live-screen", "settings-meetings", "settings-health", "settings-mail",
            "connector-lark", "connector-lark-bot", "channel-feishu", "voice-extension", "composer-menu-meeting")}
        entries["sidebar-meeting-recording"] = True
        captures = [f"{edition}-{look}-{page}.png" for look in ("light", "dark") for page in (
            "assistant", "agents", "inbox", "todos", "drawer", "settings", "advanced", "connectors", "channels", "devices")]
        for name in captures:
            (self.shots / name).write_bytes(b"png")
        return {
            "schema": 1, "run_id": run_id, "edition": edition,
            "device_credential_issued": True, "current_device_confirmed": True, "paired_devices": 1,
            "core_reads": {"sessions": 0, "inbox_items": 0, "todos": 0},
            "registry": {"active": sorted(["health", "screen-share", "voice"] if full else []),
                         "off_by_hub": [] if full else ["health", "screen-share", "voice"]},
            "module_flags": ["modules"] + [f"module.{name}" for name in DRIVER.smoke.module_ids()] if full else ["modules"],
            "pages": {"agents": "loaded", "inbox": "loaded empty", "todos": "loaded empty"},
            "entries": entries, "app_alive_at_end": True, "screenshots": captures,
        }

    def test_accepts_both_real_editions(self):
        for edition in ("core-only", "full"):
            with self.subTest(edition=edition):
                DRIVER.validate_proof(self.proof(edition), edition, "run", self.shots)

    def test_rejects_another_run_or_a_missing_core_page(self):
        with self.assertRaises(DRIVER.Failed):
            DRIVER.validate_proof(self.proof("core-only", "other"), "core-only", "run", self.shots)
        proof = self.proof("core-only")
        proof["pages"]["inbox"] = "not read"
        with self.assertRaises(DRIVER.Failed):
            DRIVER.validate_proof(proof, "core-only", "run", self.shots)

    def test_module_assertions_are_not_vacuous(self):
        for edition in ("core-only", "full"):
            proof = self.proof(edition)
            proof["entries"]["sidebar-health"] = not (edition == "full")
            with self.subTest(edition=edition), self.assertRaises(DRIVER.Failed):
                DRIVER.validate_proof(proof, edition, "run", self.shots)
        proof = self.proof("core-only")
        proof["module_flags"].append("module.voice")
        with self.assertRaises(DRIVER.Failed):
            DRIVER.validate_proof(proof, "core-only", "run", self.shots)
        proof = self.proof("core-only")
        proof["entries"]["connector-lark"] = True
        with self.assertRaises(DRIVER.Failed):
            DRIVER.validate_proof(proof, "core-only", "run", self.shots)

    def test_rejects_missing_captures_and_unproven_device_exchange(self):
        proof = self.proof("full")
        (self.shots / proof["screenshots"][0]).unlink()
        with self.assertRaises(DRIVER.Failed):
            DRIVER.validate_proof(proof, "full", "run", self.shots)
        proof = self.proof("full")
        proof["device_credential_issued"] = False
        with self.assertRaises(DRIVER.Failed):
            DRIVER.validate_proof(proof, "full", "run", self.shots)

    def test_composer_ui_test_is_opt_in_and_uses_the_real_xctest_gesture(self):
        source = (ROOT / "apple/PersonaUITests/CoreOnlyHubLiveUITests.swift").read_text()
        self.assertIn("throw XCTSkip", source)
        self.assertIn("app.launchPinned()", source)
        self.assertIn("addButton.press(forDuration:", source)
        self.assertIn('NSPredicate(format: "enabled == true")', source)

    def test_machine_fixture_never_starts_a_local_node(self):
        source = (ROOT / "scripts/ci/ios-core-only-hub.py").read_text()
        self.assertIn('"kind": "link"', source)
        self.assertNotIn('"kind": "local"', source)

    def test_proof_contains_no_credentials(self):
        encoded = json.dumps(self.proof("core-only"))
        self.assertNotIn("pairing_token", encoded)
        self.assertNotIn("jp_", encoded)


if __name__ == "__main__":
    unittest.main()
