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
            self.assertEqual(scenes, [{"name": "b", "title": "b", "bottom": False}, {"name": "a", "title": "A", "bottom": False}])
            self.assertEqual(set(hashes), {"a", "b"})

    def test_long_pages_also_request_bottom_captures(self):
        scenes, _ = RUNNER.scenes()
        by_name = {scene["name"]: scene for scene in scenes}
        for name in RUNNER.BOTTOM_PAGES:
            self.assertTrue(by_name[name]["bottom"])
        self.assertFalse(by_name["button"]["bottom"])
        self.assertFalse(by_name["widget-small"]["bottom"])

    def test_empty_duplicate_and_unknown_selections_are_rejected(self):
        for names in ([], ["button", "button"], ["missing"], ["../button"]):
            with self.subTest(names=names), self.assertRaises(ValueError):
                RUNNER.scenes(names)


if __name__ == "__main__":
    unittest.main()
