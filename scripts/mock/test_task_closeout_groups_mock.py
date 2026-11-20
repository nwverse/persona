"""Deterministic native UI inputs: an external wait is not execution or supervisor acceptance."""
import socket
import unittest

from test_mock_hub_mesh import Mock


class TaskCloseoutGroupsMockTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.hub = Mock(port, MOCK_TASKS="1", MOCK_TASKS_NO_ASKS="1", MOCK_TASK_CLOSEOUT_GROUPS="1")
        cls.addClassCleanup(cls.hub.stop)

    def test_closeouts_have_six_unaccepted_tasks(self):
        todos = self.hub.get("/api/v1/todos")["todos"]
        self.assertEqual(len(todos), 6)
        self.assertTrue(all(not t.get("done_at") for t in todos))
        self.assertEqual({t["supervision"]["state"] for t in todos}, {"needs_user", "", "pending_review", "rework", "parked"})

    def test_explicit_park_and_unparked_wait_share_a_blocked_external_closeout(self):
        sessions = {s["session_id"]: s for s in self.hub.get("/api/v1/sessions")["sessions"]}
        todos = {t["id"]: t for t in self.hub.get("/api/v1/todos")["todos"]}
        for name in ("external", "parked"):
            work = sessions["taskbar-" + name]["work"]
            self.assertEqual((work["phase"], work["on"], work["closeout"]["status"]), ("blocked", "external", "blocked"))
            self.assertIn("maintainer", work["closeout"]["waiting"])
        self.assertNotIn("parked", todos["taskbar-external"]["supervision"])
        self.assertEqual(todos["taskbar-parked"]["supervision"]["parked"]["waiting_on"], "someone_else")


if __name__ == "__main__":
    unittest.main()
