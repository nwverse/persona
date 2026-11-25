"""The wire types ratchet: a new weak name fails, a move between packages is neutral, a fall passes, --update only lowers.

The cases give the ratchet a counter of their own, so they need no Go; one case runs the real Go counter over a
throwaway tree when Go is on PATH, and one checks this checkout's committed baseline parses.
"""
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import wire_types_ratchet as w

ROOT = Path(__file__).resolve().parents[2]


def baseline(metrics, exceptions=None):
    data = {"metrics": metrics}
    if exceptions:
        data["exceptions"] = exceptions
    return json.dumps(data)


class RatchetTests(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.root = Path(self.dir.name)
        (self.root / "scripts/ci").mkdir(parents=True)
        self.addCleanup(self.dir.cleanup)

    def write(self, metrics, exceptions=None):
        (self.root / w.BASELINE_REL).write_text(baseline(metrics, exceptions))

    def counter(self, counts):
        return lambda root: counts

    def test_the_same_counts_pass(self):
        self.write({"bare-enum-state": {"Session.State": 1}})
        self.assertEqual([], w.check(self.root, counter=self.counter({"bare-enum-state": {"Session.State": 1}})))

    def test_a_new_name_fails_and_says_how_to_fix_it(self):
        self.write({"bare-enum-kind": {"Session.Kind": 1}})
        found = w.check(self.root, counter=self.counter({"bare-enum-kind": {"Session.Kind": 1, "NewThing.Kind": 1}}))
        self.assertEqual(1, len(found))
        self.assertIn("NewThing.Kind (new)", found[0])
        self.assertIn("closed set", found[0])
        self.assertIn("no ignore marker", found[0])

    def test_a_name_that_occurs_more_often_fails(self):
        self.write({"bare-id": {"Item.MachineID": 1}})
        found = w.check(self.root, counter=self.counter({"bare-id": {"Item.MachineID": 2}}))
        self.assertIn("Item.MachineID (1 -> 2)", found[0])

    def test_a_type_moved_between_packages_is_neutral(self):
        # The counter names fields "<Type>.<Field>" with no package, so the same type in another package is the same name.
        self.write({"raw-json": {"Recording.Segments": 1}})
        self.assertEqual([], w.check(self.root, counter=self.counter({"raw-json": {"Recording.Segments": 1}})))

    def test_a_fall_passes_with_a_notice(self):
        self.write({"map-any": {"A.Extra": 1, "B.Extra": 1}})
        notes = []
        found = w.check(self.root, counter=self.counter({"map-any": {"A.Extra": 1}}), notes=notes)
        self.assertEqual([], found)
        self.assertEqual(["map-any B.Extra: 1 -> 0"], notes)

    def test_an_exception_with_a_pr_raises_the_allowance(self):
        self.write({}, {"bare-id": {"Item.OwnerID": {"count": 1, "pr": "#1", "why": "legacy wire"}}})
        self.assertEqual([], w.check(self.root, counter=self.counter({"bare-id": {"Item.OwnerID": 1}})))

    def test_an_exception_without_a_pr_is_refused(self):
        self.write({}, {"bare-id": {"Item.OwnerID": {"count": 1, "why": "legacy wire"}}})
        with self.assertRaises(w.WireTypesError):
            w.check(self.root, counter=self.counter({}))

    def test_a_missing_baseline_is_not_invented(self):
        self.assertIn("baseline missing", w.check(self.root, counter=self.counter({}))[0])
        with self.assertRaises(w.WireTypesError):
            w.update(self.root, counter=self.counter({}))

    def test_update_lowers_and_drops_gone_names(self):
        self.write({"bare-id": {"A.ID": 2, "B.ID": 1}, "map-any": {"C.X": 1}})
        self.assertEqual([], w.update(self.root, counter=self.counter({"bare-id": {"A.ID": 1}})))
        metrics, _ = w.parse_baseline((self.root / w.BASELINE_REL).read_text())
        self.assertEqual({"bare-id": {"A.ID": 1}}, metrics)

    def test_update_refuses_to_raise_or_add(self):
        self.write({"bare-id": {"A.ID": 1}})
        before = (self.root / w.BASELINE_REL).read_text()
        refusals = w.update(self.root, counter=self.counter({"bare-id": {"A.ID": 1, "B.ID": 1}}))
        self.assertIn("B.ID (new)", refusals[0])
        self.assertEqual(before, (self.root / w.BASELINE_REL).read_text())

    def test_the_baseline_may_not_rise_against_the_base(self):
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)
        subprocess.run(["git", "-C", str(self.root), "config", "user.email", "t@example.com"], check=True)
        subprocess.run(["git", "-C", str(self.root), "config", "user.name", "t"], check=True)
        self.write({"bare-id": {"A.ID": 1}})
        subprocess.run(["git", "-C", str(self.root), "add", "-A"], check=True)
        subprocess.run(["git", "-C", str(self.root), "commit", "-qm", "base"], check=True)
        base = subprocess.run(["git", "-C", str(self.root), "rev-parse", "HEAD"], capture_output=True, text=True,
                              check=True).stdout.strip()
        self.write({"bare-id": {"A.ID": 1, "B.ID": 1}})
        counter = self.counter({"bare-id": {"A.ID": 1, "B.ID": 1}})
        found = w.check(self.root, base=base, counter=counter)
        self.assertEqual(1, len(found))
        self.assertIn("B.ID is 1 in the baseline but 0", found[0])
        self.assertEqual([], w.check(self.root, counter=counter))  # no base: the baseline alone holds


@unittest.skipUnless(shutil.which("go"), "go is not on PATH")
class CounterTests(unittest.TestCase):
    def test_the_go_counter_names_fields_without_their_package(self):
        with tempfile.TemporaryDirectory() as tmp:
            server = Path(tmp) / "server"
            for name in ("hub", "assistant", "devices", "extmgr"):
                (server / "internal" / name).mkdir(parents=True)
            for name in ("kbwire", "recordingwire"):
                (server / "internal" / name).mkdir(parents=True)
            (server / "internal/archguard/testdata").mkdir(parents=True)
            (server / "internal/archguard/testdata/contracts.txt").write_text("# c\ninternal/kbwire\ninternal/recordingwire\n")
            src = "package {0}\ntype Clip struct {{ State string }}\n"
            (server / "internal/kbwire/a.go").write_text(src.format("kbwire"))
            before = w.run_counter_in(server)
            (server / "internal/kbwire/a.go").unlink()
            (server / "internal/recordingwire/a.go").write_text(src.format("recordingwire"))
            after = w.run_counter_in(server)
        self.assertEqual({"bare-enum-state": {"Clip.State": 1}}, before)
        self.assertEqual(before, after)


class CommittedBaselineTests(unittest.TestCase):
    def test_the_committed_baseline_parses(self):
        metrics, _ = w.parse_baseline((ROOT / w.BASELINE_REL).read_text(encoding="utf-8"))
        self.assertTrue(metrics["bare-id"])


if __name__ == "__main__":
    unittest.main()
