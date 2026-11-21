"""Tests mock_hub's GET /api/v1/assistant/directions/tree (#4352): the leaders' open to-dos, the secretary's group last."""
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import unittest
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


def start(env):
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    log = tempfile.TemporaryFile()
    process = subprocess.Popen(["python3", "-I", str(Path(__file__).with_name("mock_hub.py"))],
                               env=dict(os.environ, PORT=str(port), **env), stdout=log, stderr=log)
    base = f"http://127.0.0.1:{port}"
    for _ in range(100):
        if process.poll() is not None:
            raise RuntimeError("mock hub did not start")
        try:
            with urlopen(base + "/api/v1/health", timeout=1):
                return base, process, log
        except (URLError, TimeoutError):
            time.sleep(0.1)
    raise RuntimeError("mock hub start timed out")


def stop(process, log):
    process.terminate()
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)
    log.close()


def get(base, path):
    with urlopen(Request(base + path, headers={"Authorization": "Bearer mock"}), timeout=3) as response:
        return json.load(response)


class DirectionTreeMockTests(unittest.TestCase):
    def test_leaders_tasks_states_and_secretary(self):
        base, process, log = start({"MOCK_LEADERS": "1"})
        self.addCleanup(stop, process, log)
        tree = get(base, "/api/v1/assistant/directions/tree")["directions"]
        leaders = [d["id"] for d in get(base, "/api/v1/assistant/leaders")["leaders"]]
        self.assertEqual([d["id"] for d in tree[:-1]], leaders)
        secretary = tree[-1]
        self.assertTrue(secretary["secretary"])
        self.assertEqual(secretary["name"], "")
        self.assertEqual(secretary["id"], get(base, "/api/v1/assistant")["main"])
        self.assertTrue(secretary["tasks"])
        states = {t["state"] for d in tree for t in d["tasks"]}
        self.assertEqual(states, {"needs_you", "review", "stuck", "working", "planned"})
        order = {"needs_you": 0, "review": 1, "stuck": 2, "working": 3, "planned": 4}
        for d in tree:
            ranks = [order[t["state"]] for t in d["tasks"]]
            self.assertEqual(ranks, sorted(ranks))
            for k in ("working", "needs_you", "stuck", "review", "planned"):
                self.assertEqual(d["counts"][k], sum(1 for t in d["tasks"] if t["state"] == k))
            # running (#6626): an Agent under the direction at work, at any depth, not the working count
            def at_work(agents):
                return any(a["state"] == "working" or at_work(a["children"]) for a in agents)
            self.assertIs(d["running"], any(at_work(t["agents"]) for t in d["tasks"]))
        # a spawned agent sits under the agent it came from, once
        weather = next(t for d in tree for t in d["tasks"] if t["id"] == "todo_weather")
        flat = [a["session_id"] for a in weather["agents"]] + [c["session_id"] for a in weather["agents"] for c in a["children"]]
        self.assertEqual(len(flat), len(set(flat)))
        self.assertTrue(any(a["children"] for a in weather["agents"]))
        # every open to-do is in exactly one group; done ones in none
        todos = get(base, "/api/v1/todos")["todos"]
        listed = [t["id"] for d in tree for t in d["tasks"]]
        self.assertEqual(sorted(listed), sorted(t["id"] for t in todos if not t.get("done_at")))

    def test_without_leaders_only_the_secretary(self):
        base, process, log = start({"MOCK_LEADERS": "0"})
        self.addCleanup(stop, process, log)
        tree = get(base, "/api/v1/assistant/directions/tree")["directions"]
        self.assertEqual(len(tree), 1)
        self.assertTrue(tree[0]["secretary"])

    def test_outside_directions_fixture(self):
        base, process, log = start({"MOCK_OUTSIDE_DIRECTIONS": "1"})
        self.addCleanup(stop, process, log)
        conversation = "b2e4d6f8-0000-4000-8000-000000000002"
        bound = "dir_mock_bound_8236"
        targetless = "dir_mock_targetless_8236"
        order = [conversation, bound, targetless]

        assistant = get(base, "/api/v1/assistant")
        self.assertEqual(assistant["leaders"], order)
        self.assertFalse({bound, targetless} & {c["session_id"] for c in assistant["conversations"]})

        leaders = get(base, "/api/v1/assistant/leaders")["leaders"]
        self.assertEqual([row["id"] for row in leaders], order)
        by_id = {row["id"]: row for row in leaders}
        for sid in (bound, targetless):
            self.assertIs(by_id[sid]["external"], True)
            self.assertEqual(by_id[sid]["title"], by_id[sid]["name"])
        self.assertEqual(by_id[bound]["machine_id"], "server")
        self.assertEqual(by_id[bound]["session_id"], "a1f3c2d4-0000-4000-8000-000000000004")
        self.assertNotIn("machine_id", by_id[targetless])
        self.assertNotIn("session_id", by_id[targetless])
        self.assertNotIn("external", by_id[conversation])

        tree = get(base, "/api/v1/assistant/directions/tree")["directions"]
        self.assertEqual([row["id"] for row in tree[:-1]], order)
        self.assertTrue(tree[-1]["secretary"])
        by_id = {row["id"]: row for row in tree}
        self.assertEqual(by_id[bound]["machine_id"], "server")
        self.assertEqual(by_id[bound]["session_id"], "a1f3c2d4-0000-4000-8000-000000000004")
        self.assertNotIn("machine_id", by_id[targetless])
        self.assertNotIn("session_id", by_id[targetless])
        self.assertEqual(by_id[bound]["counts"],
                         {"working": 1, "needs_you": 2, "stuck": 0, "review": 0, "planned": 0})
        self.assertIs(by_id[bound]["running"], True)
        self.assertEqual(by_id[targetless]["counts"],
                         {"working": 0, "needs_you": 0, "stuck": 0, "review": 0, "planned": 0})
        self.assertIs(by_id[targetless]["running"], False)
        self.assertEqual(by_id[conversation]["counts"]["needs_you"], 1)
        self.assertIn("todo_orders", {task["id"] for task in tree[-1]["tasks"]})
        self.assertNotIn("todo_orders", {task["id"] for task in by_id[bound]["tasks"]})

        sessions = get(base, "/api/v1/sessions")["sessions"]
        target = next(s for s in sessions if s["session_id"] == "a1f3c2d4-0000-4000-8000-000000000004")
        self.assertEqual(target["state"], "idle", "the direction's running mark must not come from its target")

        get(base, "/mock/session_state?mid=macbook&sid=outside-running-0001&state=idle")
        changed_tree = get(base, "/api/v1/assistant/directions/tree")["directions"]
        self.assertFalse(next(row for row in changed_tree if row["id"] == bound)["running"])

        get(base, "/mock/reset")
        reset_tree = get(base, "/api/v1/assistant/directions/tree")["directions"]
        self.assertEqual([row["id"] for row in reset_tree[:-1]], order)
        self.assertTrue(next(row for row in reset_tree if row["id"] == bound)["running"],
                        "reset restores the synthetic running Agent too")
        task_ids = [task["id"] for row in reset_tree for task in row["tasks"]]
        self.assertEqual(len(task_ids), len(set(task_ids)))

    def test_an_older_hub_has_none_with_the_outside_fixture_too(self):
        base, process, log = start({"MOCK_LEGACY_HUB": "1", "MOCK_OUTSIDE_DIRECTIONS": "1"})
        self.addCleanup(stop, process, log)
        with self.assertRaises(HTTPError) as err:
            get(base, "/api/v1/assistant/directions/tree")
        self.assertEqual(err.exception.code, 404)

    def test_an_older_hub_has_none(self):
        base, process, log = start({"MOCK_LEGACY_HUB": "1"})
        self.addCleanup(stop, process, log)
        with self.assertRaises(HTTPError) as err:
            get(base, "/api/v1/assistant/directions/tree")
        self.assertEqual(err.exception.code, 404)


if __name__ == "__main__":
    unittest.main()
