"""A migration already on the base branch cannot be edited, deleted or renamed.

Comment-only and whitespace-only edits pass. A new file may be added. The cases build a
throwaway repository; they do not touch this checkout's migrations.
"""
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import migration_freeze

ROOT = Path(__file__).resolve().parents[2]
HUB = "server/internal/db/migrations/hub/0001_init.sql"
MANAGER = "server/internal/db/migrations/manager/0002_settings.sql"
ORIGINAL = "CREATE TABLE devices (\n    id int\n);\n"
OTHER = "-- the manager's settings\nCREATE TABLE settings (id int);\n"


class NormalizeTests(unittest.TestCase):
    def test_comments_blank_lines_and_indentation_match(self):
        edited = "-- added\nCREATE TABLE devices (id int);\n\n"
        self.assertEqual(migration_freeze.normalize_sql(ORIGINAL), migration_freeze.normalize_sql(edited))
        crlf = ORIGINAL.replace("\n", "\r\n")
        self.assertEqual(migration_freeze.normalize_sql(ORIGINAL), migration_freeze.normalize_sql(crlf))
        block = "CREATE TABLE devices (/* key */ id int);"
        self.assertEqual(migration_freeze.normalize_sql(ORIGINAL), migration_freeze.normalize_sql(block))

    def test_whitespace_inside_a_literal_is_kept(self):
        self.assertNotEqual(
            migration_freeze.normalize_sql("SELECT 'a b';"),
            migration_freeze.normalize_sql("SELECT 'a  b';"))
        self.assertNotEqual(
            migration_freeze.normalize_sql("SELECT 'a--b';"),
            migration_freeze.normalize_sql("SELECT 'a--c';"))
        self.assertEqual(
            migration_freeze.normalize_sql("SELECT 'a''b';"),
            migration_freeze.normalize_sql("SELECT 'a''b' ;"))

    def test_reindenting_a_dollar_quoted_body_matches(self):
        spaced = "DO $$\nBEGIN\n    INSERT INTO t VALUES ('a b');\nEND $$;\n"
        flat = "DO $$ BEGIN INSERT INTO t VALUES ('a b'); END $$;"
        self.assertEqual(migration_freeze.normalize_sql(spaced), migration_freeze.normalize_sql(flat))
        changed = "DO $$ BEGIN INSERT INTO t VALUES ('a  b'); END $$;"
        self.assertNotEqual(migration_freeze.normalize_sql(spaced), migration_freeze.normalize_sql(changed))

    def test_commenting_out_the_statement_is_an_edit(self):
        self.assertNotEqual(
            migration_freeze.normalize_sql(ORIGINAL),
            migration_freeze.normalize_sql("/* " + ORIGINAL + " */\n"))

    def test_a_real_sql_change_does_not_match(self):
        self.assertNotEqual(
            migration_freeze.normalize_sql(ORIGINAL),
            migration_freeze.normalize_sql("CREATE TABLE devices (id text);\n"))


class CompareTests(unittest.TestCase):
    def test_reports_edits_deletions_and_renames_but_not_additions_or_comments(self):
        base = {HUB: ORIGINAL, MANAGER: OTHER}
        self.assertEqual(migration_freeze.frozen_changes(base, dict(base)), [])
        commented = {HUB: "-- note\n" + ORIGINAL, MANAGER: OTHER}
        self.assertEqual(migration_freeze.frozen_changes(base, commented), [])
        added = dict(base)
        added["server/internal/db/migrations/hub/0009_extra.sql"] = "CREATE TABLE extra (id int);\n"
        self.assertEqual(migration_freeze.frozen_changes(base, added), [])
        edited = dict(base)
        edited[HUB] = "CREATE TABLE devices (id text);\n"
        self.assertEqual(migration_freeze.frozen_changes(base, edited), [(HUB, "edited")])
        renamed = {MANAGER: OTHER, "server/internal/db/migrations/hub/0001_init_v2.sql": ORIGINAL}
        self.assertEqual(migration_freeze.frozen_changes(base, renamed), [(HUB, "deleted")])
        self.assertEqual(migration_freeze.frozen_changes(base, {MANAGER: OTHER}), [(HUB, "deleted")])

    def test_a_missing_copy_wins_over_an_edited_one(self):
        base = {HUB: ORIGINAL}
        index = {HUB: "CREATE TABLE devices (id text);\n"}
        worktree = {}
        self.assertEqual(migration_freeze.changes_against(base, [index, worktree]), [(HUB, "deleted")])

    def test_messages_tell_the_author_to_add_a_migration(self):
        edited = migration_freeze.describe(HUB, "edited")
        deleted = migration_freeze.describe(HUB, "deleted")
        self.assertEqual(edited.count("\n"), 0)
        self.assertIn("was edited", edited)
        self.assertIn("was deleted or renamed", deleted)
        self.assertIn("add a new migration instead", edited)
        self.assertIn("add a new migration instead", deleted)
        self.assertTrue(edited.startswith("migration freeze: " + HUB))


class GitFreezeTests(unittest.TestCase):
    def setUp(self):
        self.repo = Path(tempfile.mkdtemp(prefix="migration-freeze-"))
        self.addCleanup(self._cleanup)
        self._saved_base = os.environ.pop("CI_BASE_SHA", None)
        self.git("init", "-q", "-b", "main")
        self.git("config", "user.email", "t@example.com")
        self.git("config", "user.name", "t")
        self.write(HUB, ORIGINAL)
        self.write(MANAGER, OTHER)
        self.git("add", "-A")
        self.git("commit", "-q", "-m", "base")
        self.git("update-ref", "refs/remotes/origin/main", "HEAD")
        self.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

    def _cleanup(self):
        if self._saved_base is None:
            os.environ.pop("CI_BASE_SHA", None)
        else:
            os.environ["CI_BASE_SHA"] = self._saved_base
        subprocess.run(["rm", "-rf", str(self.repo)], check=True)

    def write(self, path, text):
        file = self.repo / path
        file.parent.mkdir(parents=True, exist_ok=True)
        file.write_text(text)

    def git(self, *args):
        subprocess.run(["git", "-C", str(self.repo), *args], check=True, capture_output=True, timeout=30)

    def run_check(self, *args):
        code = migration_freeze.main(["--root", str(self.repo), *args])
        return code

    def test_a_clean_tree_passes(self):
        stdout = sys.stdout
        try:
            from io import StringIO
            buf = StringIO()
            sys.stdout = buf
            code = self.run_check()
        finally:
            sys.stdout = stdout
        self.assertEqual(0, code)
        self.assertIn("2 migration(s)", buf.getvalue())

    def test_a_comment_or_reindent_passes_and_a_new_file_passes(self):
        self.write(HUB, "-- added\r\nCREATE TABLE devices (id int);\n")
        self.assertEqual(0, self.run_check())
        self.write("server/internal/db/migrations/hub/0009_extra.sql", "CREATE TABLE extra (id int);\n")
        self.assertEqual(0, self.run_check())

    def test_an_edited_migration_fails(self):
        self.write(HUB, "CREATE TABLE devices (id text);\n")
        stderr = sys.stderr
        try:
            from io import StringIO
            buf = StringIO()
            sys.stderr = buf
            code = self.run_check()
        finally:
            sys.stderr = stderr
        self.assertEqual(1, code)
        self.assertIn("was edited", buf.getvalue())
        self.assertIn("add a new migration instead", buf.getvalue())
        self.assertNotIn(MANAGER, buf.getvalue())

    def test_a_deleted_migration_fails(self):
        (self.repo / HUB).unlink()
        stderr = sys.stderr
        try:
            from io import StringIO
            buf = StringIO()
            sys.stderr = buf
            code = self.run_check()
        finally:
            sys.stderr = stderr
        self.assertEqual(1, code)
        self.assertIn("was deleted or renamed", buf.getvalue())
        self.assertIn(HUB, buf.getvalue())

    def test_a_rename_fails_even_when_the_sql_is_unchanged(self):
        target = "server/internal/db/migrations/hub/0001_devices.sql"
        self.git("mv", HUB, target)
        stderr = sys.stderr
        try:
            from io import StringIO
            buf = StringIO()
            sys.stderr = buf
            code = self.run_check()
        finally:
            sys.stderr = stderr
        self.assertEqual(1, code)
        self.assertIn(HUB, buf.getvalue())
        self.assertIn("was deleted or renamed", buf.getvalue())
        self.assertNotIn("0001_devices.sql was", buf.getvalue())

    def test_a_staged_edit_fails_even_when_the_worktree_was_restored(self):
        self.write(HUB, "CREATE TABLE devices (id text);\n")
        self.git("add", HUB)
        self.write(HUB, ORIGINAL)
        stderr = sys.stderr
        try:
            from io import StringIO
            buf = StringIO()
            sys.stderr = buf
            code = self.run_check()
        finally:
            sys.stderr = stderr
        self.assertEqual(1, code)
        self.assertIn("was edited", buf.getvalue())

    def test_migrations_added_on_the_base_after_the_fork_are_not_required(self):
        self.git("branch", "topic")
        self.write("server/internal/db/migrations/hub/0009_later.sql", "CREATE TABLE later (id int);\n")
        self.git("add", "-A")
        self.git("commit", "-q", "-m", "later")
        self.git("update-ref", "refs/remotes/origin/main", "HEAD")
        self.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
        self.git("checkout", "-q", "topic")
        self.assertEqual(0, self.run_check())

    def test_origin_head_wins_over_a_moved_origin_main(self):
        # HEAD is the rewritten migration. The default branch (origin/HEAD) is still the parent, so the rewrite is
        # not on the base and must fail. origin/main pointing at HEAD would have hidden it.
        self.git("branch", "trunk")
        self.git("update-ref", "refs/remotes/origin/trunk", "HEAD")
        self.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
        self.write(HUB, "CREATE TABLE devices (id text);\n")
        self.git("add", "-A")
        self.git("commit", "-q", "-m", "rewritten")
        self.git("update-ref", "refs/remotes/origin/main", "HEAD")
        self.assertEqual(1, self.run_check())

    def test_ci_base_sha_is_the_ref(self):
        # The tip matches the default branch again, but CI_BASE_SHA is the commit that rewrote the migration. Freezing
        # that commit fails; ignoring it and using origin/main would pass.
        self.write(HUB, "CREATE TABLE devices (id text);\n")
        self.git("add", "-A")
        self.git("commit", "-q", "-m", "edited")
        os.environ["CI_BASE_SHA"] = subprocess.check_output(
            ["git", "-C", str(self.repo), "rev-parse", "HEAD"], text=True, timeout=30).strip()
        self.write(HUB, ORIGINAL)
        self.git("add", "-A")
        self.git("commit", "-q", "-m", "reverted")
        self.assertEqual(1, self.run_check())
        os.environ.pop("CI_BASE_SHA")
        self.assertEqual(0, self.run_check())

    def test_no_base_exits_2(self):
        self.git("update-ref", "-d", "refs/remotes/origin/main")
        self.git("symbolic-ref", "--delete", "refs/remotes/origin/HEAD")
        stderr = sys.stderr
        try:
            from io import StringIO
            buf = StringIO()
            sys.stderr = buf
            code = self.run_check()
        finally:
            sys.stderr = stderr
        self.assertEqual(2, code)
        self.assertIn("no base commit", buf.getvalue())


class WiringTests(unittest.TestCase):
    def test_guards_and_check_fast_run_the_checker(self):
        guards = (ROOT / "scripts/ci/guards.sh").read_text()
        check_fast = (ROOT / "scripts/test/check-fast.sh").read_text()
        self.assertIn("python3 scripts/ci/migration_freeze.py", guards)
        self.assertIn("python3 scripts/ci/migration_freeze.py", check_fast)
        self.assertIn("server/internal/db/migrations/", check_fast)


if __name__ == "__main__":
    unittest.main()
