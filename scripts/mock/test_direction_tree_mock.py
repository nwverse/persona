"""Tests mock_hub's GET /api/v1/assistant/directions/tree (#4352): the leaders' open to-dos, the secretary's group last;
and its `direction_sessions` (#11516, scripts/mock/direction_sessions_mock.py): the tree's live filed sessions, the
single read with every standing, the filing write and its Undo, and the switch for a hub without the routes."""
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


def put(base, path, body, lang=""):
    headers = {"Authorization": "Bearer mock", "Content-Type": "application/json"}
    if lang:
        headers["Accept-Language"] = lang
    req = Request(base + path, data=json.dumps(body).encode(), headers=headers, method="PUT")
    try:
        with urlopen(req, timeout=3) as response:
            return response.status, json.load(response)
    except HTTPError as e:
        return e.code, json.load(e)


def file_path(machine, session):
    return f"/api/v1/assistant/sessions/{machine}/{session}/direction"


RECIPE = "a1f3c2d4-0000-4000-8000-000000000007"  # stopped on a connected computer, no to-do
WIDGET = "a1f3c2d4-0000-4000-8000-000000000001"  # an Agent of a to-do the first leader supervises


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

    def test_an_older_hub_has_none(self):
        base, process, log = start({"MOCK_LEGACY_HUB": "1"})
        self.addCleanup(stop, process, log)
        with self.assertRaises(HTTPError) as err:
            get(base, "/api/v1/assistant/directions/tree")
        self.assertEqual(err.exception.code, 404)


class DirectionSessionsMockTests(unittest.TestCase):
    def start(self, mode="1"):
        base, process, log = start({"MOCK_LEADERS": "1", "MOCK_DIRECTION_SESSIONS": mode})
        self.addCleanup(stop, process, log)
        return base

    def test_off_lists_no_feature_and_no_sessions(self):
        base, process, log = start({"MOCK_LEADERS": "1"})
        self.addCleanup(stop, process, log)
        self.assertNotIn("direction_sessions", get(base, "/api/v1/health").get("features") or [])
        tree = get(base, "/api/v1/assistant/directions/tree")["directions"]
        self.assertFalse(any("sessions" in d for d in tree))
        status, _ = put(base, file_path("macbook", RECIPE), {"direction": tree[0]["id"]})
        self.assertEqual(status, 404)

    def test_the_tree_lists_only_live_filed_sessions_and_the_read_every_standing(self):
        base = self.start()
        self.assertIn("direction_sessions", get(base, "/api/v1/health")["features"])
        tree = get(base, "/api/v1/assistant/directions/tree")["directions"]
        first = tree[0]
        live = first["sessions"]
        self.assertTrue(live)
        self.assertTrue(all(s["state"] in ("working", "needs_you") for s in live))
        self.assertEqual({s["source"] for s in live}, {"owner", "leader"})
        in_tasks = {a["session_id"] for t in first["tasks"] for a in t["agents"]}
        self.assertFalse(in_tasks & {s["session_id"] for s in live}, "an Agent of a to-do is not repeated")
        self.assertNotIn("sessions", tree[-1], "the secretary group lists none")
        read = get(base, "/api/v1/assistant/directions/" + first["id"])
        states = {s["state"] for s in read["sessions"]}
        for standing in ("computer_offline", "list_unknown", "not_listed", "computer_removed", "offline", "working",
                         "needs_you"):
            self.assertIn(standing, states)
        self.assertGreater(len(read["sessions"]), 5, "more than five, for Show All N")
        self.assertNotIn("sessions_unavailable", read)

    def test_a_move_and_its_undo_restore_what_was_there(self):
        base = self.start()
        tree = get(base, "/api/v1/assistant/directions/tree")["directions"]
        first, second = tree[0]["id"], tree[1]["id"]
        status, answer = put(base, file_path("macbook", RECIPE), {"direction": second})
        self.assertEqual((status, answer), (200, {"direction": second, "previous": first, "changed": True}))
        status, again = put(base, file_path("macbook", RECIPE), {"direction": second})
        self.assertEqual(again, {"direction": second, "previous": second, "changed": False}, "the same move again")
        status, undo = put(base, file_path("macbook", RECIPE), {"direction": first})
        self.assertEqual(undo["direction"], first)
        placed = {s["session_id"] for s in get(base, "/api/v1/assistant/directions/" + first)["sessions"]}
        self.assertIn(RECIPE, placed)
        # a first filing's Undo writes null: the placement before it (none) comes back
        chat = "b2e4d6f8-0000-4000-8000-000000000002"
        status, filed = put(base, file_path("server", chat), {"direction": second})
        self.assertEqual(filed["previous"], None)
        status, undone = put(base, file_path("server", chat), {"direction": None})
        self.assertEqual((status, undone["direction"], undone["changed"]), (200, None, True))
        self.assertNotIn(chat, {s["session_id"] for s in get(base, "/api/v1/assistant/directions/" + second)["sessions"]})

    def test_refusals_change_nothing_and_say_why_in_the_requests_language(self):
        base = self.start()
        first = get(base, "/api/v1/assistant/directions/tree")["directions"][0]["id"]
        status, refused = put(base, file_path("macbook", WIDGET), {"direction": first}, lang="ja")
        self.assertEqual((status, refused["code"]), (409, "direction_supervised_task"))
        self.assertRegex(refused["error"], r"[\u3040-\u30ff]")
        get(base, "/mock/direction_sessions?refuse=direction_full")
        status, full = put(base, file_path("macbook", RECIPE), {"direction": ""})
        self.assertEqual((status, full["code"]), (409, "direction_full"))
        status, ok = put(base, file_path("macbook", RECIPE), {"direction": ""})
        self.assertEqual(status, 200, "a refusal is for the next write only")

    def test_missing_routes_answer_404_without_their_codes(self):
        base = self.start("missing")
        self.assertIn("direction_sessions", get(base, "/api/v1/health")["features"])
        tree = get(base, "/api/v1/assistant/directions/tree")["directions"]
        self.assertFalse(any("sessions" in d for d in tree))
        status, body = put(base, file_path("macbook", RECIPE), {"direction": ""})
        self.assertEqual(status, 404)
        self.assertNotIn(body.get("code"), ("session_not_found", "machine_not_found", "direction_not_found"))

    def test_the_reads_failures(self):
        base = self.start()
        first = get(base, "/api/v1/assistant/directions/tree")["directions"][0]["id"]
        get(base, "/mock/direction_sessions?sessions_unavailable=1&ended=" + first)
        read = get(base, "/api/v1/assistant/directions/" + first)
        self.assertEqual((read["sessions"], read["sessions_unavailable"]), ([], True))
        self.assertIn("ended_at", read)
        get(base, "/mock/direction_sessions?read=fail")
        with self.assertRaises(HTTPError) as err:
            get(base, "/api/v1/assistant/directions/" + first)
        self.assertEqual(err.exception.code, 503)
        get(base, "/mock/reset")
        self.assertNotIn("sessions_unavailable", get(base, "/api/v1/assistant/directions/" + first))


if __name__ == "__main__":
    unittest.main()
