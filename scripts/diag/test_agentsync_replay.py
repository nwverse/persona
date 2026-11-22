import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


class AgentSyncReplayTests(unittest.TestCase):
    def replay(self, *updates):
        script = Path(__file__).with_name("agentsync_replay.py")
        with tempfile.TemporaryDirectory() as directory:
            recording = Path(directory) / "push.jsonl"
            recording.write_text("".join(
                json.dumps({"t": time, "msg": {"type": "revs", "machine_id": "fixture", "revs": revisions}}) + "\n"
                for time, revisions in updates
            ))
            return subprocess.run([sys.executable, "-B", str(script), str(recording)], capture_output=True,
                                  text=True, timeout=10)

    def test_zero_span_keeps_counts_without_rates(self):
        for initial, changed in [({"a": 1}, {"a": 2}), ({"a": 1, "b": 1}, {"a": 2, "b": 2})]:
            with self.subTest(changes=len(changed)):
                result = self.replay((0, initial), (30000, changed))
                self.assertEqual(result.returncode, 0, result.stderr)
                count = len(changed)
                self.assertEqual(result.stdout, f"{count} transcript changes over 0.0 min in {count} sessions "
                                                f"(0 while working): pulls {count} before, {count} after "
                                                "(per-minute rates unavailable)\n")

    def test_positive_span_keeps_per_minute_rates(self):
        result = self.replay((0, {"a": 1}), (30000, {"a": 2}), (90000, {"a": 3}))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "2 transcript changes over 1.0 min in 1 sessions (0 while working): "
                                        "pulls 2.0/min before, 2.0/min after\n")

    def test_no_changes_keeps_existing_error(self):
        result = self.replay((0, {"a": 1}))
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stderr, "no transcript changes in the recording\n")


if __name__ == "__main__":
    unittest.main()
