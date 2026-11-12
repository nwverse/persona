import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import android_adaptive_shots as shots


class AssistantStackProbeTest(unittest.TestCase):
    def stack(self, **changes):
        value = {
            "fieldVersion": 1,
            "session": "session",
            "pid": 42,
            "updatedAtNanos": 100,
            "route": "inbox/mail",
            "selectedKey": "mail-item",
            "pages": {
                "Mail": {
                    "updatedAtNanos": 100,
                    "composer": {"draftHmac": "hash", "focused": True},
                }
            },
        }
        value.update(changes)
        return json.dumps(value)

    def test_decodes_real_route_and_selected_key(self):
        probe = shots.decode_stack_probe(self.stack(), 42, 100)
        self.assertEqual("inbox/mail", probe["route"])
        self.assertEqual("mail-item", probe["selectedKey"])

    def test_rejects_stale_or_invalid_navigation(self):
        with self.assertRaises(shots.AdaptiveFailure):
            shots.decode_stack_probe(self.stack(updatedAtNanos=1), 42, shots.MAX_PROBE_AGE_NANOS + 2)
        with self.assertRaises(shots.AdaptiveFailure):
            shots.decode_stack_probe(self.stack(route=""), 42, 100)
        with self.assertRaises(shots.AdaptiveFailure):
            shots.decode_stack_probe(self.stack(selectedKey=""), 42, 100)
        with self.assertRaises(shots.AdaptiveFailure):
            shots.decode_stack_probe(self.stack(pid=7), 42, 100)

    def test_merges_draft_pages_without_mutating_list_evidence(self):
        base = {"pages": {"Inbox": {"updatedAtNanos": 100, "list": {"firstVisibleKey": "item"}}}}
        stack = shots.decode_stack_probe(self.stack(), 42, 100)
        merged = shots.merge_stack_pages(base, stack)
        self.assertEqual("item", merged["pages"]["Inbox"]["list"]["firstVisibleKey"])
        self.assertEqual("hash", merged["pages"]["Mail"]["composer"]["draftHmac"])
        self.assertNotIn("Mail", base["pages"])


if __name__ == "__main__":
    unittest.main()
