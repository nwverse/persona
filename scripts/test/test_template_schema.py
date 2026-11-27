"""schema/template.schema.json (#16772): templates hold to it, and the web app's parser reads the same parts.

    python3 scripts/test/test_template_schema.py

The valid cases are the built-in Default's shape (web/src/lib/templates.ts builtInTemplates), a template of one's own
and a template with every part; the invalid ones must be refused. The validator is the one of test_extension_schema.py.
"""

import json
import re
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from test_extension_schema import ANNOTATIONS, VALIDATED, Validator, unknown_keywords  # noqa: E402

ROOT = Path(__file__).resolve().parents[2]
SCHEMA = ROOT / "schema" / "template.schema.json"
PARSER = ROOT / "web" / "src" / "lib" / "templates.ts"

BASE = {"persona_template": 1, "id": "mine", "name": "Mine"}

VALID = {
    "default": {**BASE, "id": "default", "name": "Default", "description": "As it comes",
                "browser": {"phone_nav": "switch", "sidebar_icons": "monochrome", "main_chat": "full",
                            "agents_view": "full", "settings_view": "full", "sidebar_view": "full", "modules_off": []},
                "navigation": {"pages": None}},
    "tab bar": {**BASE, "id": "my-console", "name": {"en": "My console", "zh-Hans": "我的控制台"},
                      "browser": {"phone_nav": "tabs", "main_chat": "plain", "agents_view": "active",
                                  "settings_view": "short", "sidebar_view": "agents", "modules_off": ["health"]},
                      "navigation": {"pages": {"order": [], "shown": [], "hidden": ["home", "todos"]}}},
    "every part": {**BASE, "navigation": {"start": "p:example/today"},
                   "extensions": {"example-ext": {"enabled": True, "config": {"agent": True}}},
                   "settings": [{"key": "assistant.main_tasks_quiet", "value": False},
                                {"key": "agents.hidden_dirs", "action": "add", "value": "~/scratch"}]},
}

INVALID = {
    "no version": {"id": "mine", "name": "Mine"},
    "another version": {**BASE, "persona_template": 2},
    "upper-case id": {**BASE, "id": "Mine"},
    "empty name": {**BASE, "name": ""},
    "unknown language": {**BASE, "name": {"fr": "Le mien"}},
    "unknown part": {**BASE, "theme": "dark"},
    "unknown phone navigation": {**BASE, "browser": {"phone_nav": "bottom"}},
    "unknown browser look": {**BASE, "browser": {"font": "serif"}},
    "unknown main chat look": {**BASE, "browser": {"main_chat": "lean"}},
    "unknown agents look": {**BASE, "browser": {"agents_view": "all"}},
    "unknown sidebar look": {**BASE, "browser": {"sidebar_view": "none"}},
    "bad start page": {**BASE, "navigation": {"start": "../settings"}},
    "extension secret": {**BASE, "extensions": {"x": {"secrets": {"token": "t"}}}},
    "setting without a value": {**BASE, "settings": [{"key": "k"}]},
    "unknown action": {**BASE, "settings": [{"key": "k", "action": "drop", "value": 1}]},
}


class TemplateSchemaTest(unittest.TestCase):
    def setUp(self):
        self.schema = json.loads(SCHEMA.read_text())
        self.v = Validator({SCHEMA.name: self.schema})

    def errors(self, value):
        return self.v.errors(self.schema, value, SCHEMA.name)

    def test_schema_keywords_are_known(self):
        self.assertEqual(unknown_keywords(self.schema, SCHEMA.name), set(), f"known: {sorted(VALIDATED | ANNOTATIONS)}")

    def test_valid_templates_hold_to_the_schema(self):
        for name, value in VALID.items():
            with self.subTest(case=name):
                self.assertEqual(self.errors(value), [])

    def test_invalid_templates_are_refused(self):
        for name, value in INVALID.items():
            with self.subTest(case=name):
                self.assertNotEqual(self.errors(value), [], f"{name} was accepted")

    def test_the_web_parser_reads_the_schema_parts(self):
        src = PARSER.read_text()
        known = re.search(r"const known = new Set\(\[([^\]]+)\]\)", src)
        self.assertIsNotNone(known, "parseTemplate's list of parts moved")
        parts = set(re.findall(r'"([a-z_]+)"', known.group(1)))
        self.assertEqual(parts, set(self.schema["$defs"]["Template"]["properties"]),
                         "web/src/lib/templates.ts and schema/template.schema.json name different parts")


if __name__ == "__main__":
    unittest.main()
