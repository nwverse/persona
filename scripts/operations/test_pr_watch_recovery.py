"""Real-Git recovery tests using only temporary watcher-owned directories."""
import os
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import pr_watch_common
import pr_watch_recovery


class RecoveryIntegrationTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="pr-watch-recovery-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.checkout = self.root / "checkout"
        self.checkout.mkdir()
        self.state = self.root / "state.json"
        self.worktrees = pr_watch_recovery.owned_root(self.state)
        self.worktrees.mkdir()
        pr_watch_recovery.atomic_leases(self.state, {"version": 1, "prs": {}, "worktree_leases": {}})
        env = dict(os.environ, GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull)
        subprocess.run(["git", "init", "-q"], cwd=self.checkout, env=env, check=True, capture_output=True)

    def test_owned_path_rejects_external_and_symlinked_directories(self):
        (self.root / "outside").mkdir()
        for path in (self.root, self.root / "outside", self.worktrees, self.root / "state.json"):
            with self.subTest(path=path):
                if not path.exists():
                    path.touch()
                try:
                    pr_watch_recovery.owned_path(self.state, path)
                except pr_watch_common.WatchError:
                    pass
                else:
                    self.fail("accepted unsafe cleanup path: %s" % path)
        link = self.root / "link"
        link.symlink_to(self.worktrees, target_is_directory=True)
        with self.assertRaises(pr_watch_common.WatchError):
            pr_watch_recovery.owned_path(self.state, link / "entry")

    def test_disk_pressure_reclaims_only_inactive_watcher_worktrees(self):
        stale = self.worktrees / "persona-7-old"
        stale.mkdir()
        (stale / "file").write_text("old")
        (stale / ".git").write_text("gitdir: " + str(self.checkout / ".git/worktrees/persona-7-old") + "\n")
        os.utime(stale, (1, 1))
        active = self.worktrees / "persona-8-active"
        active.mkdir()
        outside = self.root / "outside"
        outside.mkdir()
        pr_watch_recovery.atomic_leases(self.state, {"version": 1, "prs": {}, "worktree_leases": {
            str(stale): {"active": False, "heartbeat": 1},
            str(active): {"active": True, "heartbeat": 1},
        }})
        def fake_run(argv, **kwargs):
            code = 1 if argv[:3] == ["git", "worktree", "remove"] else 0
            return subprocess.CompletedProcess(argv, code, "", "stale registration" if code else "")
        with patch.object(pr_watch_recovery, "run", side_effect=fake_run):
            self.assertTrue(pr_watch_recovery.recover_worktree_failure(
                self.checkout, self.state, "fatal: Out of diskspace", protected=[active]))
        self.assertFalse(stale.exists())
        self.assertTrue(active.exists())
        self.assertTrue(outside.exists())

    def test_unregistered_and_active_worktrees_are_never_bulk_deleted(self):
        stale = self.worktrees / "persona-7-old"
        stale.mkdir()
        os.utime(stale, (1, 1))
        active = self.worktrees / "persona-8-active"
        active.mkdir()
        pr_watch_recovery.atomic_leases(self.state, {"worktree_leases": {
            str(active): {"active": True, "heartbeat": 1}}})
        self.assertFalse(pr_watch_recovery.recover_worktree_failure(
            self.checkout, self.state, "fatal: no space left", protected=[active]))
        self.assertTrue(stale.exists())
        self.assertTrue(active.exists())

    def test_registered_worktree_is_not_recursively_deleted_after_a_remove_failure(self):
        registered = self.worktrees / "persona-7-live"
        registered.mkdir()
        (registered / ".git").write_text("gitdir: " + str(self.checkout / ".git/worktrees/persona-7-live") + "\n")
        registration = self.checkout / ".git/worktrees/persona-7-live"
        registration.parent.mkdir(parents=True)
        registration.mkdir()
        with patch.object(pr_watch_recovery, "run", return_value=subprocess.CompletedProcess([], 1, "", "registered")):
            with self.assertRaises(pr_watch_common.WatchError):
                pr_watch_recovery.remove_worktree(self.checkout, self.state, registered)
        self.assertTrue(registered.exists())
        self.assertTrue(registration.exists())

    def test_missing_worktree_registration_is_pruned_before_fallback_cleanup(self):
        broken = self.worktrees / "persona-7-broken"
        broken.mkdir()
        (broken / "file").write_text("orphan")
        with patch.object(pr_watch_recovery, "run", side_effect=[
                subprocess.CompletedProcess([], 128, "", "not a working tree"),
                subprocess.CompletedProcess([], 0, "", ""),
                subprocess.CompletedProcess([], 0, "", "")]):
            pr_watch_recovery.remove_worktree(self.checkout, self.state, broken)
        self.assertFalse(broken.exists())

    def test_only_empty_loose_objects_and_old_temporary_packs_are_removed(self):
        loose = self.checkout / ".git/objects/ab"
        loose.mkdir(parents=True)
        empty = loose / ("c" * 38)
        empty.touch()
        nonempty = loose / ("d" * 38)
        nonempty.write_bytes(b"do-not-remove")
        pack = self.checkout / ".git/objects/pack"
        pack.mkdir(exist_ok=True)
        temporary = pack / "tmp_pack_test"
        temporary.touch()
        old = 1
        os.utime(temporary, (old, old))
        self.assertEqual(pr_watch_recovery.recover_object_debris(self.checkout), 2)
        self.assertFalse(empty.exists())
        self.assertTrue(nonempty.exists())
        self.assertFalse(temporary.exists())
        with self.assertRaises(pr_watch_common.WatchError):
            pr_watch_recovery.recover_object_debris(self.worktrees)

    def test_rename_refusal_can_be_resolved_from_the_exact_remote_ref(self):
        with patch.object(pr_watch_common, "run", side_effect=[
                subprocess.CompletedProcess([], 2, "", "exhaustive rename detection was skipped"),
                subprocess.CompletedProcess([], 0, "a" * 40 + "\trefs/heads/main\n", ""),
                subprocess.CompletedProcess([], 0, "", "")]) as execute:
            self.assertEqual(pr_watch_common.checked(["git", "fetch", "-q", "origin", "refs/heads/main"]),
                             "a" * 40 + "\n")
        self.assertEqual(execute.call_args_list[1].args[0],
                         ["git", "ls-remote", "origin", "refs/heads/main"])
        with patch.object(pr_watch_common, "run", side_effect=[
                subprocess.CompletedProcess([], 2, "", "rename refused"),
                subprocess.CompletedProcess([], 0, "b" * 40 + "\trefs/pull/7/head\n", ""),
                subprocess.CompletedProcess([], 0, "", "")]) as execute:
            self.assertEqual(pr_watch_common.checked(
                ["git", "fetch", "-q", "origin", "+refs/pull/7/head:refs/pr-watch/heads/7"]),
                "b" * 40 + "\n")
        self.assertEqual(execute.call_args_list[1].args[0],
                         ["git", "ls-remote", "origin", "refs/pull/7/head"])
        with patch.object(pr_watch_common, "run", return_value=subprocess.CompletedProcess([], 128, "", "fatal: corrupt")):
            with self.assertRaises(pr_watch_common.WatchError):
                pr_watch_common.checked(["git", "fetch", "-q", "origin", "refs/heads/main"])

    def test_repair_commit_message_has_no_tool_attribution(self):
        source = (HERE / "pr_watch_repair.py").read_text()
        self.assertNotIn("Co-Authored-By:", source)
        self.assertNotIn("Kimi", source)
        self.assertNotIn("Moonshot", source)


if __name__ == "__main__":
    unittest.main()
