import importlib.util
from pathlib import Path
import tempfile
import subprocess
import json
import os
import sys
import unittest

SOURCE = Path(__file__).resolve().parents[1] / "ci" / "prompt_language_ratchet.py"


class PromptLanguageRatchetTests(unittest.TestCase):
    def setUp(self):
        spec = importlib.util.spec_from_file_location("prompt_language_ratchet", SOURCE)
        self.ratchet = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.ratchet)

    def scan(self, files):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name, text in files.items():
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(text)
            return self.ratchet.scan(root)

    def test_language_conditions_and_includes(self):
        got = self.scan({
            "agents/old.md": "用中文写",
            "agents/localized.md": '{{if .Language}}Write in {{.Language}}{{else}}用中文写{{end}}',
            "voice/full.md": '{{if eq .Lang "en"}}Hello{{else}}你好{{end}}',
            "kb/output.md": '{{template "kb/language" .}}\n总结记录',
            "kb/language.md": '{{if .Language}}{{.Language}}{{else}}中文{{end}}',
            "agents/comment.md": '{{/* if .Language */}}\n用中文写',
            "agents/data.md": 'The field is .Language. 中文',
            "agents/quoted.md": '{{if eq .Title ".Language"}}中文{{end}}',
            "agents/else_if.md": '{{if .Handled}}Done{{else if eq .Language "English"}}Hello{{else}}你好{{end}}',
            "agents/else_other.md": '{{if .Handled}}Done{{else if .Quiet}}安静{{end}}',
            "agents/plain.md": "English instructions",
        })
        self.assertEqual(got, {"agents": ["agents/comment.md", "agents/data.md", "agents/else_other.md", "agents/old.md", "agents/quoted.md"]})

    def test_same_count_cannot_hide_new_file(self):
        old = {"agents": ["agents/old.md"]}
        new = {"agents": ["agents/new.md"]}
        self.assertTrue(self.ratchet.increases(new, old))
        with self.assertRaises(ValueError):
            self.ratchet.lowered(new, old)

    def test_lower_only_and_new_directory(self):
        old = {"agents": ["agents/a.md", "agents/b.md"]}
        new = {"agents": ["agents/a.md"]}
        self.assertEqual(self.ratchet.lowered(new, old), new)
        self.assertTrue(self.ratchet.increases({"new": ["new/a.md"]}, old))
        self.assertEqual(self.ratchet.lowered({}, old), {})

    def test_cycle_does_not_grant_localization(self):
        got = self.scan({"a/a.md": '{{template "a/b" .}}中文', "a/b.md": '{{template "a/a" .}}中文'})
        self.assertEqual(got, {"a": ["a/a.md", "a/b.md"]})

    def test_baseline_counts_and_paths_are_checked(self):
        with self.assertRaises(ValueError):
            self.ratchet.decode_baseline({"agents": {"count": 2, "files": ["agents/a.md"]}})
        with self.assertRaises(ValueError):
            self.ratchet.decode_baseline({"agents": {"count": 1, "files": ["other/a.md"]}})

    def test_cli_refuses_raised_baseline_and_update(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            script = root / "scripts/ci/prompt_language_ratchet.py"
            script.parent.mkdir(parents=True)
            script.write_text(SOURCE.read_text())
            prompts = root / "server/internal/prompts/agents"
            prompts.mkdir(parents=True)
            (prompts / "old.md").write_text("中文")
            baseline = root / "scripts/ci/prompt_language_baseline.json"
            baseline.write_text(json.dumps({"agents": {"count": 1, "files": ["agents/old.md"]}}))
            def git(*args):
                subprocess.run(["git", *args], cwd=root, check=True, capture_output=True)
            git("init")
            git("add", ".")
            git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.com", "-c", "core.hooksPath=/dev/null", "commit", "-m", "baseline")
            def run(*args):
                return subprocess.run([sys.executable, str(script), "--base", "HEAD", *args], cwd=root, capture_output=True, text=True)
            self.assertEqual(run().returncode, 0)
            (prompts / "new.md").write_text("中文")
            self.assertNotEqual(run("--update").returncode, 0)
            baseline.write_text(json.dumps({"agents": {"count": 2, "files": ["agents/new.md", "agents/old.md"]}}))
            self.assertNotEqual(run().returncode, 0)
            self.assertNotEqual(run("--update").returncode, 0)

    def test_a_base_baseline_that_cannot_be_fetched_fails_instead_of_passing(self):
        # CI's guards checkout is blobless (#12753): a branch that changed the baseline has the base's copy fetched
        # on first read. A failed fetch must fail the check, not read as "no baseline at the base" and pass.
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            source = work / "source"
            script = source / "scripts/ci/prompt_language_ratchet.py"
            script.parent.mkdir(parents=True)
            script.write_text(SOURCE.read_text())
            prompts = source / "server/internal/prompts/agents"
            prompts.mkdir(parents=True)
            (prompts / "old.md").write_text("中文")
            baseline = source / "scripts/ci/prompt_language_baseline.json"
            baseline.write_text(json.dumps({"agents": {"count": 1, "files": ["agents/old.md"]}}))

            def git(*args, cwd=source):
                subprocess.run(["git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.com",
                                "-c", "core.hooksPath=/dev/null", *args], cwd=cwd, check=True, capture_output=True,
                               timeout=60)
            git("init", "-q", "-b", "main")
            git("add", ".")
            git("commit", "-q", "-m", "baseline")
            git("checkout", "-q", "-b", "raise")
            (prompts / "new.md").write_text("中文")
            baseline.write_text(json.dumps({"agents": {"count": 2, "files": ["agents/new.md", "agents/old.md"]}}))
            git("add", ".")
            git("commit", "-q", "-m", "raise")
            origin = work / "origin.git"
            git("clone", "-q", "--bare", str(source), str(origin), cwd=work)
            git("symbolic-ref", "HEAD", "refs/heads/main", cwd=origin)
            git("config", "uploadpack.allowFilter", "true", cwd=origin)
            clone = work / "clone"
            git("clone", "-q", "--filter=blob:none", "--branch", "raise", origin.as_uri(), str(clone), cwd=work)
            git("remote", "set-url", "origin", (work / "gone.git").as_uri(), cwd=clone)

            env = {key: value for key, value in os.environ.items() if key != "PERSONA_CI_SOURCE_ROOT"}

            def run():
                return subprocess.run([sys.executable, str(clone / "scripts/ci/prompt_language_ratchet.py"),
                                       "--base", "origin/main"], cwd=clone, env=env, capture_output=True, text=True,
                                      timeout=120)
            offline = run()
            self.assertEqual(offline.returncode, 1, offline.stdout)
            self.assertIn("cannot read origin/main:scripts/ci/prompt_language_baseline.json", offline.stdout)
            git("remote", "set-url", "origin", origin.as_uri(), cwd=clone)
            online = run()
            self.assertEqual(online.returncode, 1, online.stdout)
            self.assertIn("new unlocalized prompts: agents/new.md", online.stdout)


if __name__ == "__main__":
    unittest.main()
