"""License documentation exercises image smoke without enabling PR publication."""

import fnmatch
from pathlib import Path
import unittest

import yaml


ROOT = Path(__file__).resolve().parents[2]


class HubImageWorkflowTests(unittest.TestCase):
    def setUp(self):
        self.workflow = yaml.load(
            (ROOT / ".github/workflows/hub-image.yml").read_text(),
            Loader=yaml.BaseLoader,
        )

    def selected(self, filename):
        patterns = self.workflow["on"]["pull_request"]["paths"]
        return any(fnmatch.fnmatchcase(filename, pattern) for pattern in patterns)

    def test_license_and_both_readmes_trigger_smoke(self):
        for filename in ("LICENSE", "README.md", "README.zh-CN.md"):
            with self.subTest(filename=filename):
                self.assertTrue(self.selected(filename))

    def test_image_inputs_still_trigger_but_unrelated_docs_do_not(self):
        self.assertTrue(self.selected("Dockerfile"))
        self.assertTrue(self.selected("server/cmd/personad/main.go"))
        self.assertFalse(self.selected("docs/operations/testing.md"))

    def test_pr_runs_remain_read_only_and_do_not_publish(self):
        self.assertEqual(self.workflow["permissions"], {"contents": "read"})
        self.assertNotIn("if", self.workflow["jobs"]["smoke"])
        self.assertEqual(
            self.workflow["jobs"]["publish"]["if"],
            "github.event_name != 'pull_request'",
        )


if __name__ == "__main__":
    unittest.main()
