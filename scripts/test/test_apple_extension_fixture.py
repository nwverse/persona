#!/usr/bin/env python3
"""Pure contract checks for the isolated Apple acceptance extension builder."""
import importlib.util
import json
from pathlib import Path
import unittest

SPEC = importlib.util.spec_from_file_location("apple_extension_fixture", Path(__file__).with_name("apple_extension_fixture.py"))
BUILDER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(BUILDER)


class AppleExtensionFixtureTests(unittest.TestCase):
    def setUp(self):
        self.snapshots = {file.stem: json.loads(file.read_bytes()) for file in
                          (BUILDER.ROOT / "schema/extension/examples/showcase").glob("*.json")}
        self.manifest = BUILDER.manifest(self.snapshots)

    def test_every_shared_snapshot_is_a_native_navigation_contribution(self):
        pages = {page["id"]: page for page in self.manifest["contributes"]["ui"]}
        self.assertGreaterEqual(len(self.snapshots), 35)
        for name, view in self.snapshots.items():
            self.assertEqual(pages[name]["placement"], "nav_page")
            self.assertEqual(pages[name]["title"], view.get("title") or name)

    def test_all_widget_families_use_existing_shared_data(self):
        widget = next(page for page in self.manifest["contributes"]["ui"] if page["placement"] == "widget")
        self.assertEqual(len(widget["families"]), 6)
        for family in widget["families"]:
            self.assertIn("widget-" + family, self.snapshots)
        self.assertEqual(widget["opens"], "showcase")

    def test_shortcut_fixture_covers_all_supported_parameter_kinds(self):
        shortcuts = self.manifest["contributes"]["shortcuts"]
        self.assertEqual({parameter["kind"] for shortcut in shortcuts for parameter in shortcut.get("params", [])},
                         {"text", "number", "choice", "toggle"})
        self.assertIn("shortcut-count", self.snapshots)

    def test_process_fixture_is_a_distinct_supported_runtime_not_a_source_bypass(self):
        process = BUILDER.manifest(self.snapshots, runtime="process")
        runtime = process["runtime"]
        self.assertEqual(runtime["mode"], "process")
        self.assertEqual(runtime["command"], {"linux-amd64": "bin/showcase-linux-amd64"})
        self.assertNotIn("source", runtime)
        self.assertNotIn("env", runtime)
        self.assertNotIn("sandbox", runtime)
        self.assertEqual(process["contributes"], self.manifest["contributes"])
        self.assertEqual(runtime["resources"], self.manifest["runtime"]["resources"])
        self.assertEqual(self.manifest["runtime"]["mode"], "deno")
        with self.assertRaises(ValueError):
            BUILDER.manifest(self.snapshots, runtime="unrestricted")

    def test_fixture_does_not_opt_out_of_source_validation_or_sandboxing(self):
        runtime = self.manifest["runtime"]
        self.assertEqual(runtime["mode"], "deno")
        self.assertEqual(set(runtime), {"mode", "source", "resources"})
        self.assertNotIn("permissions", self.manifest)
        self.assertNotIn("env", runtime)
        self.assertNotIn("sandbox", runtime)
        self.assertEqual(runtime["source"]["bundle"], "bundle/main.mjs")


if __name__ == "__main__":
    unittest.main()
