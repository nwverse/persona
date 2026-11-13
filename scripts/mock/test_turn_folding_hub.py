import os
from pathlib import Path
import runpy
import unittest
from unittest.mock import patch

import turn_folding_hub


class TurnFoldingFixtureTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with patch.dict(os.environ, {}, clear=True):
            cls.mock = runpy.run_path(str(Path(__file__).with_name("mock_hub.py")))
            turn_folding_hub.configure(cls.mock)

    def test_compacted_history_retains_successful_review_evidence_inside_server_fold(self):
        rows = self.mock["ITEMS"][self.mock["ASK3"]]
        self.assertEqual(sum(row["kind"] == "tool_use" for row in rows), 4)
        self.assertEqual(sum(bool(row.get("compact")) for row in rows), 1)
        self.assertEqual(sum(bool(row.get("is_error")) for row in rows), 1)
        self.assertNotIn("supervisionUser", rows[-1])
        folded = self.mock["H"].do_GET.__globals__["fold_work"](rows)
        work = next(row for row in folded if row["kind"] == "fold")
        self.assertEqual(work["fold"]["tools"], 2)
        self.assertTrue(work["fold"]["supervision_user"])
        self.assertEqual(folded[-1]["text"], rows[-1]["text"])

    def test_running_and_completed_states_keep_all_narration_and_calls(self):
        for key, status in [("ASK1", "working"), ("ASK2", "idle")]:
            sid = self.mock[key]
            rows = self.mock["ITEMS"][sid]
            session = next(s for s in self.mock["ASSISTANT"] if s["session_id"] == sid)
            self.assertEqual(session["status"], status)
            self.assertEqual(sum(row["kind"] == "tool_use" for row in rows), 4)
            self.assertEqual(sum(row["kind"] == "text" and row["role"] == "assistant" for row in rows), 3)
            self.assertEqual([row["seq"] for row in rows], list(range(1, len(rows) + 1)))


if __name__ == "__main__":
    unittest.main()
