import io
import json
import sys
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from unittest import mock

import release_candidate
import release_pr


class ReleaseCandidateTests(unittest.TestCase):
    def pull(self, number=42, title="release: v1.2.0", channel="release", state="open", **changes):
        ref_ver = title.removeprefix("release: v") if isinstance(title, str) else "unknown"
        value = {
            "number": number,
            "title": title,
            "state": state,
            "draft": False,
            "base": {"ref": "main"},
            "head": {"ref": "release/" + ref_ver + "-r3",
                     "repo": {"full_name": "nwverse/persona"}},
            "user": {"login": "chyroc"},
            "html_url": "https://example.invalid/pull/" + str(number),
        }
        value.update(changes)
        return value

    def test_one_existing_candidate_is_reused_including_a_retry_suffix(self):
        for state in ("open", "OPEN"):
            with self.subTest(state=state):
                existing = self.pull(state=state)
                self.assertIs(release_candidate.active_candidate([existing], "nwverse/persona", "release"), existing)
        beta = self.pull(number=55, title="release: v1.2.0-beta.4", state="open")
        self.assertIs(release_candidate.active_candidate([beta], "nwverse/persona", "beta"), beta)
        self.assertIsNone(release_candidate.active_candidate([], "nwverse/persona", "release"))

    def test_other_channel_and_non_release_prs_do_not_own_the_candidate(self):
        self.assertIsNone(release_candidate.active_candidate([
            self.pull(number=1, title="release: v1.2.0-beta.4"),
            self.pull(number=2, title="feat: an ordinary change"),
            self.pull(number=3, state="closed"),
            self.pull(number=4, state="CLOSED"),
        ], "nwverse/persona", "release"))

    def test_multiple_candidates_need_an_explicit_superseding_decision(self):
        for state in ("open", "OPEN"):
            with self.subTest(state=state):
                with self.assertRaisesRegex(release_pr.ReleaseError, "#42, #43"):
                    release_candidate.active_candidate(
                        [self.pull(42, state=state), self.pull(43, state=state)],
                        "nwverse/persona",
                        "release",
                    )

    def test_draft_fork_deleted_fork_and_other_authors_cannot_supply_a_release_candidate(self):
        for changes in (
            {"draft": True},
            {"user": {"login": "other"}},
            {"user": None},
            {"user": "chyroc"},
            {"head": {"ref": "patch", "repo": {"full_name": "fork/persona"}}},
            {"head": {"ref": "deleted-fork-branch", "repo": None}},
            {"head": {"ref": "bad-repo-type", "repo": "nwverse/persona"}},
            {"head": None},
            {"head": "main"},
            {"base": {"ref": "another-branch"}},
            {"base": None},
            {"base": "main"},
            {"title": None},
        ):
            with self.subTest(changes=changes):
                self.assertIsNone(release_candidate.active_candidate([
                    self.pull(**changes)
                ], "nwverse/persona", "release"))

    def test_invalid_candidate_number_raises_release_error(self):
        for bad_number in (True, False, 0, -1, "42", None):
            with self.subTest(bad_number=bad_number):
                with self.assertRaisesRegex(release_pr.ReleaseError, "valid PR number"):
                    release_candidate.active_candidate([self.pull(number=bad_number)], "nwverse/persona", "release")

    def test_deleted_fork_pr_does_not_crash_detection_of_active_rest_candidate(self):
        deleted_fork = self.pull(number=10, title="feat: external contribution", head={"ref": "patch", "repo": None})
        active = self.pull(number=42, state="open")
        self.assertIs(
            release_candidate.active_candidate([deleted_fork, active], "nwverse/persona", "release"),
            active,
        )

    def test_non_object_pull_entry_raises_release_error(self):
        with self.assertRaisesRegex(release_pr.ReleaseError, "object"):
            release_candidate.active_candidate(["not-an-object"], "nwverse/persona", "release")

    def test_main_prints_rest_candidate_empty_when_none_and_rejects_invalid_html_url(self):
        with tempfile.TemporaryDirectory() as tmp:
            pulls_path = Path(tmp) / "pulls.json"
            pulls_path.write_text(json.dumps([]), encoding="utf-8")
            empty_out = io.StringIO()
            with mock.patch.object(sys, "argv", [
                "release_candidate.py", "--repository", "nwverse/persona",
                "--channel", "release", "--pulls", str(pulls_path),
            ]), redirect_stdout(empty_out):
                release_candidate.main()
            self.assertEqual(empty_out.getvalue(), "")

            pulls_path.write_text(json.dumps([self.pull(number=99, state="open")]), encoding="utf-8")
            stdout = io.StringIO()
            with mock.patch.object(sys, "argv", [
                "release_candidate.py", "--repository", "nwverse/persona",
                "--channel", "release", "--pulls", str(pulls_path),
            ]), redirect_stdout(stdout):
                release_candidate.main()
            self.assertEqual(stdout.getvalue().splitlines(), ["99", "https://example.invalid/pull/99"])

            for bad_url in (None, "", "   "):
                with self.subTest(bad_url=bad_url):
                    bad_pull = self.pull(number=100, state="open")
                    if bad_url is None:
                        bad_pull.pop("html_url")
                    else:
                        bad_pull["html_url"] = bad_url
                    pulls_path.write_text(json.dumps([bad_pull]), encoding="utf-8")
                    with mock.patch.object(sys, "argv", [
                        "release_candidate.py", "--repository", "nwverse/persona",
                        "--channel", "release", "--pulls", str(pulls_path),
                    ]):
                        with self.assertRaises(SystemExit):
                            release_candidate.main()


if __name__ == "__main__":
    unittest.main()
