"""An explicit release PR is the only new CI publication input."""
import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parent))
import release_pr as release

COMMIT = "a" * 40
OTHER = "b" * 40


class ReleasePRTests(unittest.TestCase):
    def pr(self, **changes):
        value = {"state": "OPEN", "isDraft": False, "baseRefName": "main", "headRefOid": COMMIT,
                 "headRepository": {"nameWithOwner": "nwverse/persona"}, "author": {"login": "chyroc"},
                 "title": "release: v0.9.2-beta.2"}
        value.update(changes)
        return value

    def test_only_an_admin_same_repository_release_pr_can_publish(self):
        self.assertEqual(release.verify_pr(self.pr(), "nwverse/persona", True), "0.9.2-beta.2")
        for change in ({"state": "MERGED"}, {"isDraft": True}, {"baseRefName": "other"},
                       {"headRepository": {"nameWithOwner": "fork/persona"}}, {"author": {"login": "other"}},
                       {"title": "feat: a release-looking title"}, {"title": "release: latest"}):
            with self.subTest(change=change), self.assertRaises(release.ReleaseError):
                release.verify_pr(self.pr(**change), "nwverse/persona", True)

    def test_stable_and_beta_titles_cannot_swap_channels(self):
        self.assertEqual(release.verify_pr(self.pr(title="release: v0.9.2"), "nwverse/persona", False), "0.9.2")
        for title, beta in (("release: v0.9.2", True), ("release: v0.9.2-beta.2", False),
                            ("release: v0.9.2-rc.1", False), ("release: v0.9.2-beta.2 extra", True)):
            with self.subTest(title=title), self.assertRaises(release.ReleaseError):
                release.verify_pr(self.pr(title=title), "nwverse/persona", beta)

    def test_version_allocation_never_reuses_an_immutable_tag(self):
        def remote(command, **kwargs):
            return ("a" * 40 + "\trefs/tags/v0.9.1\n" + "b" * 40 + "\trefs/tags/v0.9.1-beta.1\n"
                    + "c" * 40 + "\trefs/tags/v0.9.1-beta.2\n")
        with patch.object(release, "tags", return_value={"v0.9.1": "a" * 40, "v0.9.1-beta.1": "b" * 40, "v0.9.1-beta.2": "c" * 40}):
            self.assertEqual(release.next_version("nwverse/persona", True), "0.9.1-beta.3")
            self.assertEqual(release.next_version("nwverse/persona", False, "0.9.2"), "0.9.2")
        with patch.object(release, "tags", return_value={"v0.9.2": "a" * 40}):
            with self.assertRaises(release.ReleaseError):
                release.next_version("nwverse/persona", False, "0.9.2")

    def test_plan_binds_the_merge_result_not_just_a_pr_head(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "release.json"
            with patch.object(release.subprocess, "check_output", return_value="1794790000\n"):
                data = release.write_plan(path, "nwverse/persona", "beta", "0.9.1-beta.3", COMMIT, 12, OTHER)
            self.assertEqual(data["tag"], "v0.9.1-beta.3")
            self.assertEqual(data["merge_commit"], OTHER)
            self.assertEqual(release.load_plan(path, "nwverse/persona", "beta", COMMIT)["pr"], 12)
            for commit in (OTHER, "not-a-sha"):
                with self.subTest(commit=commit), self.assertRaises(release.ReleaseError):
                    release.load_plan(path, "nwverse/persona", "beta", commit)
            with self.assertRaises(release.ReleaseError):
                release.load_plan(path, "nwverse/persona", "release", COMMIT)

    def test_artifact_receipt_hashes_the_exact_files_and_rejects_a_missing_platform(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            artifacts = root / "artifacts"
            artifacts.mkdir()
            (artifacts / "latest.json").write_text('{"version":"0.9.1-beta.3"}')
            (artifacts / "Persona-test.apk").write_bytes(b"signed fixture")
            plan = root / "release.json"
            release.write_plan(plan, "nwverse/persona", "beta", "0.9.1-beta.3", COMMIT, 12, COMMIT, timestamp=1794790000)
            data = release.verify_artifacts(plan, "nwverse/persona", "beta", COMMIT, (artifacts,))
            self.assertEqual(len(data["artifacts"]), 2)
            self.assertNotIn("release.json", {item["name"] for item in data["artifacts"]})
            with self.assertRaises((release.ReleaseError, OSError)):
                release.verify_artifacts(plan, "nwverse/persona", "beta", COMMIT, (root / "missing",))


    def test_merged_pr_and_actor_must_match_the_actual_merge_commit(self):
        pr = self.pr(state="MERGED", mergeCommit={"oid": COMMIT}, mergedBy={"login": "chyroc"}, title="release: v0.9.2-beta.2")
        self.assertEqual(release.verify_merged_pr(pr, "nwverse/persona", "beta", COMMIT, "chyroc"), "0.9.2-beta.2")
        for change in ({"state": "OPEN"}, {"mergeCommit": {"oid": OTHER}}, {"mergedBy": {"login": "other"}},
                       {"author": {"login": "other"}}, {"headRepository": {"nameWithOwner": "fork/persona"}}):
            with self.subTest(change=change), self.assertRaises(release.ReleaseError):
                release.verify_merged_pr(self.pr(**{**pr, **change}), "nwverse/persona", "beta", COMMIT, "chyroc")
        self.assertEqual(release.verify_merged_pr({**pr, "mergedBy": None}, "nwverse/persona", "beta", COMMIT, "chyroc"), "0.9.2-beta.2")
        self.assertEqual(release.verify_merged_pr({**pr, "mergedBy": {"login": "github-actions[bot]"}}, "nwverse/persona", "beta", COMMIT, "chyroc"), "0.9.2-beta.2")
        with self.assertRaises(release.ReleaseError):
            release.verify_merged_pr(pr, "nwverse/persona", "beta", COMMIT, "other")

    def test_cli_web_receipt_binds_the_same_manifest_and_every_download(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            downloads = root / "downloads"
            downloads.mkdir()
            plan_path = root / "plan.json"
            release.write_plan(plan_path, "nwverse/persona", "beta", "0.9.1-beta.3", COMMIT, 12, COMMIT, timestamp=1794790000)
            manifest = {"version": "0.9.1-beta.3", "commit": COMMIT, "build": release.load_plan(plan_path, "nwverse/persona", "beta", COMMIT)["build"],
                        "channel": "beta", "files": {"linux-amd64": {"name": "personad-linux-amd64.gz"}},
                        "links": {"windows-amd64": {"name": "persona-link-windows-amd64.exe.gz"}},
                        "nodes": {"persona-node": {"windows-amd64": {"name": "persona-node-windows-amd64.exe.gz"}}}}
            (downloads / "latest.json").write_text(json.dumps(manifest))
            for name in ("personad-linux-amd64.gz", "persona-link-windows-amd64.exe.gz", "persona-node-windows-amd64.exe.gz"):
                (downloads / name).write_bytes(name.encode())
            data = release.bind_cli_web(plan_path, downloads)
            self.assertEqual(len(data["artifacts"]), 4)
            release.verify_artifacts(plan_path, "nwverse/persona", "beta", COMMIT)
            android = root / "android"
            android.mkdir()
            (android / "Persona-test.apk").write_bytes(b"signed fixture")
            (android / "android.json").write_text('{"version":"0.9.1-beta.3"}')
            data = release.verify_artifacts(plan_path, "nwverse/persona", "beta", COMMIT, (android,))
            self.assertEqual(len(data["artifacts"]), 6)
            self.assertIn("android.json", {item["name"] for item in data["artifacts"]})
            (downloads / "personad-linux-amd64.gz").write_bytes(b"changed")
            with self.assertRaises(release.ReleaseError):
                release.verify_artifacts(plan_path, "nwverse/persona", "beta", COMMIT)
            (downloads / "persona-node-windows-amd64.exe.gz").unlink()
            with self.assertRaises(release.ReleaseError):
                release.bind_cli_web(plan_path, downloads)


    def test_schedule_skips_the_same_source_and_selects_stable_version_by_change(self):
        with patch.object(release, "tags", return_value={"v0.9.1": "a" * 40, "v0.9.1-beta.1": "b" * 40}):
            self.assertEqual(release.schedule_version("nwverse/persona", "beta", "v0.9.1", COMMIT, COMMIT, []), None)
            self.assertEqual(release.schedule_version("nwverse/persona", "beta", "v0.9.1", COMMIT, OTHER, []), "0.9.1-beta.2")
            self.assertEqual(release.schedule_version("nwverse/persona", "release", "v0.9.1", COMMIT, COMMIT, []), None)
            self.assertEqual(release.schedule_version("nwverse/persona", "release", "v0.9.1", COMMIT, OTHER, ["fix: repair a bug"]), "0.9.2")
            self.assertEqual(release.schedule_version("nwverse/persona", "release", "v0.9.1", COMMIT, OTHER, ["feat(ui): a feature"]), "0.10.0")


if __name__ == "__main__":
    unittest.main()
