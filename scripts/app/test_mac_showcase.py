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
            self.assertEqual(scenes, [{"name": "b", "title": "b", "bottom": False, "review": False, "images": [], "scrollAll": False},
                                      {"name": "a", "title": "A", "bottom": False, "review": False, "images": [], "scrollAll": False}])
            self.assertEqual(set(hashes), {"a", "b"})

    def test_each_native_page_has_an_acknowledged_close_in_both_appearances(self):
        selected = [{"name": "button"}, {"name": "code-review-v5"}]
        valid = ["showcase/button", "showcase/code-review-v5"] * 2
        self.assertTrue(RUNNER.native_closes(valid, selected))
        for value in (None, "closed", valid[:3], valid + [valid[0]], [None], [{}]):
            self.assertFalse(RUNNER.native_closes(value, selected))

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

    def test_images_require_real_decode_completion_except_reserved_failure_fixture(self):
        scene = RUNNER.scenes(["row"])[0][0]
        self.assertEqual(len(scene["images"]), 3)
        self.assertNotIn("https://example.invalid/none.png", scene["images"])
        self.assertEqual(RUNNER.image_urls({"avatar": {"url": "https://cdn.example.com/a.png"},
                                          "thumbnail": {"url": "http://example.com/b.png"}}), ["https://cdn.example.com/a.png"])
        rows = [{"sample": "row", "appearance": appearance, "position": position,
                 "file": appearance + position, "loadedImages": scene["images"]}
                for appearance in ("light", "dark") for position in ("top", "bottom")]
        self.assertTrue(RUNNER.complete_captures(rows, [scene]))
        rows[0]["loadedImages"] = scene["images"][:2]
        self.assertFalse(RUNNER.complete_captures(rows, [scene]))

    def test_scroll_tiles_must_overlap_and_reach_the_actual_end(self):
        scene = {"name": "list-more", "bottom": False, "scrollAll": True}
        rows = [{"sample": scene["name"], "appearance": appearance, "position": position,
                 "file": appearance + position, "scroll": {"offset": offset, "viewport": 900, "end": 1400}}
                for appearance in ("light", "dark") for position, offset in (("top", 0), ("middle-1", 675), ("middle-2", 1350), ("bottom", 1400))]
        self.assertTrue(RUNNER.complete_captures(rows, [scene]))
        for invalid in (rows[:-1], [*rows[:1], *rows[2:]], []):
            self.assertFalse(RUNNER.complete_captures(invalid, [scene]))
        rows[1]["scroll"]["offset"] = 800
        self.assertFalse(RUNNER.complete_captures(rows, [scene]))
        rows[1]["scroll"]["offset"] = 675
        rows[-1]["scroll"]["offset"] = 1390
        self.assertFalse(RUNNER.complete_captures(rows, [scene]))
        rows[-1]["scroll"]["offset"] = float("nan")
        self.assertFalse(RUNNER.complete_captures(rows, [scene]))

    def test_omitted_overview_and_list_pages_request_complete_scrolling(self):
        scenes, _ = RUNNER.scenes(sorted(RUNNER.SCROLL_ALL_PAGES))
        self.assertTrue(all(scene["scrollAll"] for scene in scenes))

    def test_empty_duplicate_and_unknown_selections_are_rejected(self):
        for names in ([], ["button", "button"], ["missing"], ["../button"]):
            with self.subTest(names=names), self.assertRaises(ValueError):
                RUNNER.scenes(names)


if __name__ == "__main__":
    unittest.main()
