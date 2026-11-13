#!/usr/bin/env python3
"""Verify the synthetic Assistant-tray fixture without opening sockets."""

import copy
import unittest

import assistant_trays as fixture


class Recorder:
    def send_json(self, value, status=200):
        self.value, self.status = copy.deepcopy(value), status


class AssistantTrayFixtureTests(unittest.TestCase):
    def setUp(self):
        fixture.install()

    def test_progress_has_four_stable_slots_and_keeps_the_audit(self):
        handler = Recorder()
        path = f"/api/v1/assistant/conversations/{fixture.hub.ASK1}/feed"
        self.assertTrue(fixture.tasks_area(handler, "GET", path, {}))
        self.assertEqual(4, len(handler.value["task_cards"]))
        self.assertEqual(4, len({card["id"] for card in handler.value["task_cards"]}))
        self.assertEqual(6, len(handler.value["items"]))
        self.assertEqual("accepted", handler.value["task_cards"][-1]["state"])
        for index, card in enumerate(handler.value["task_cards"]):
            self.assertEqual(f"plan:plan_tray:{index}", card["id"])
            self.assertEqual(index, card["index"])
            self.assertEqual(f"todo_tray_{index}", card["todo_id"])

    def test_clear_removes_decisions_and_drafts_not_progress(self):
        before = [todo["id"] for todo in fixture.hub.TODOS]
        fixture.clear()
        self.assertEqual([], fixture.hub.INTERACTIONS)
        self.assertFalse(any(item.get("suggestion") for item in fixture.hub.INBOX))
        self.assertEqual(before, [todo["id"] for todo in fixture.hub.TODOS])
        self.assertFalse(any(todo["supervision"]["state"] == "needs_user" for todo in fixture.hub.TODOS))
        self.assertFalse(any(session.get("pending_interactions") for session in fixture.hub.SESSIONS))

    def test_background_is_structurally_marked_and_names_are_synthetic(self):
        self.assertTrue(all(machine["name"].startswith("Computer ") for machine in fixture.hub.MACHINES))
        items = fixture.hub.ITEMS[fixture.hub.ASK1]
        self.assertTrue(any(item.get("source", {}).get("kind") == "supervision" for item in items))
        self.assertTrue(any(item["role"] == "system" for item in items))
        for todo in fixture.hub.TODOS:
            report = todo["supervision"]["progress"]
            self.assertLessEqual(report["step"], report["steps"])
            self.assertGreater(report["steps"], 0)


if __name__ == "__main__":
    unittest.main()
