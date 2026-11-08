#!/usr/bin/env python3
"""Pure plan validation for the real-app Mac showcase capture runner."""
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location("mac_showcase", Path(__file__).with_name("mac-showcase.py"))
RUNNER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(RUNNER)


class ShowcasePlanTests(unittest.TestCase):
    def test_every_shared_snapshot_has_a_capture_and_original_hash(self):
        scenes, hashes = RUNNER.scenes()
        files = sorted(RUNNER.SNAPSHOTS.glob("*.json"), key=lambda file: file.stem)
        self.assertEqual([scene["name"] for scene in scenes], [file.stem for file in files])
        self.assertGreaterEqual(len(files), 35)
        for file, scene in zip(files, scenes):
            original = file.read_bytes()
            self.assertEqual(hashes[file.stem], hashlib.sha256(original).hexdigest())
            self.assertEqual(scene["title"], json.loads(original).get("title") or file.stem)

    def test_subset_preserves_selection_order_and_fallback_titles(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "a.json").write_text('{"title":"A","sections":[]}')
            (root / "b.json").write_text('{"sections":[]}')
            scenes, hashes = RUNNER.scenes(["b", "a"], snapshots=root)
            self.assertEqual(scenes, [{"name": "b", "title": "b", "bottom": False, "review": False},
                                      {"name": "a", "title": "A", "bottom": False, "review": False}])
            self.assertEqual(set(hashes), {"a", "b"})

    def test_root_review_controls_are_required_by_the_capture_plan(self):
        scenes, _ = RUNNER.scenes(["code-review-v5", "code-review", "button"])
        self.assertEqual([scene["review"] for scene in scenes], [True, False, False])

    def test_long_pages_also_request_bottom_captures(self):
        scenes, _ = RUNNER.scenes()
        by_name = {scene["name"]: scene for scene in scenes}
        for name in RUNNER.BOTTOM_PAGES:
            self.assertTrue(by_name[name]["bottom"])
        self.assertFalse(by_name["button"]["bottom"])
        self.assertFalse(by_name["widget-small"]["bottom"])

    def test_installed_hub_plan_requires_positive_native_readiness(self):
        scenes, _ = RUNNER.scenes(["button"])
        output = Path("/tmp/test-captures")
        plan = RUNNER.capture_plan("http://127.0.0.1:17490", output, scenes, requires_native=True)
        self.assertIs(plan["requiresNative"], True)
        self.assertEqual(plan["extensionShowcase"], scenes)
        self.assertIs(RUNNER.capture_plan(plan["hub"], output, scenes)["requiresNative"], False)

    def test_retained_legacy_or_zero_revision_is_not_native_readiness(self):
        for value in ("stream:1", "stream:42"):
            self.assertTrue(RUNNER.native_revision(value))
        for value in (None, "legacy", "stream:0", "stream:01", "stream:1:visible=false", "stream:١"):
            self.assertFalse(RUNNER.native_revision(value))

    def test_empty_duplicate_and_unknown_selections_are_rejected(self):
        for names in ([], ["button", "button"], ["missing"], ["../button"]):
            with self.subTest(names=names), self.assertRaises(ValueError):
                RUNNER.scenes(names)


if __name__ == "__main__":
    unittest.main()
