"""Exercise Overview by project's mock data (#13019: MOCK_KB_TREE with MOCK_TODO_SORT, overview_projects_mock.py) without a browser."""
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import unittest
from urllib.error import HTTPError, URLError
from urllib.request import ProxyHandler, Request, build_opener

# The mock is on loopback: a proxy from the environment must not get in the way.
urlopen = build_opener(ProxyHandler({})).open


class OverviewProjectsMockTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.base = f"http://127.0.0.1:{port}"
        cls.log = tempfile.TemporaryFile()
        cls.process = subprocess.Popen(
            ["python3", "-I", str(Path(__file__).with_name("mock_hub.py"))],
            env=dict(os.environ, PORT=str(port), MOCK_KB_TREE="1", MOCK_TODO_SORT="1"), stdout=cls.log, stderr=cls.log)
        cls.addClassCleanup(cls.stop)
        for _ in range(60):
            if cls.process.poll() is not None:
                raise RuntimeError("mock hub exited before startup")
            try:
                with urlopen(cls.base + "/api/v1/health", timeout=1):
                    return
            except (URLError, TimeoutError):
                time.sleep(0.1)
        raise RuntimeError("mock hub startup timed out")

    @classmethod
    def stop(cls):
        cls.process.terminate()
        try:
            cls.process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            cls.process.kill()
            cls.process.wait(timeout=5)
        cls.log.close()

    def call(self, method, path, body=None):
        data = json.dumps(body).encode() if body is not None else None
        req = Request(self.base + path, data=data, method=method, headers={"Content-Type": "application/json"})
        try:
            with urlopen(req, timeout=5) as r:
                return r.status, json.loads(r.read())
        except HTTPError as e:
            return e.code, json.loads(e.read())

    def test_tree_projects_with_todos(self):
        self.call("GET", "/mock/reset")
        _, listed = self.call("GET", "/api/v1/kb/projects")
        rows = {r["id"]: r for r in listed["items"]}
        # the tree's projects, not the sort's three flat ones
        self.assertEqual(rows["seed-planner"].get("parent"), "garden-app")
        self.assertIn("press-kit", rows)
        _, todos = self.call("GET", "/api/v1/todos")
        by = {t["id"]: t for t in todos["todos"]}
        self.assertEqual(by["todo_p2"]["project"], "watering-reminders")
        self.assertTrue(by["todo_t1"].get("temporary"))
        self.assertTrue(by["todo_p4"].get("done_at"))
        # every project a to-do names is one of the tree's
        self.assertTrue({t["project"] for t in todos["todos"] if t.get("project")} <= set(rows))
        # the sort's unsorted to-dos are still there, and its suggestions name the tree's projects
        self.assertIn("todo_s1", by)
        _, s = self.call("POST", "/api/v1/todo-sort/suggest", {})
        self.assertTrue(all(it["project"] in rows for it in s["items"] if it["project"]))
        self.assertNotIn("todo_t1", [it["todo"] for it in s["items"]])

    def test_reset_puts_them_back(self):
        self.call("POST", "/api/v1/todo-sort/apply", {"items": [{"id": "todo_s1", "project": "garden-app"}]})
        self.call("GET", "/mock/reset")
        _, listed = self.call("GET", "/api/v1/kb/projects")
        self.assertEqual({r["id"]: r for r in listed["items"]}["watering-reminders"].get("parent"), "garden-app")
        _, todos = self.call("GET", "/api/v1/todos")
        ids = [t["id"] for t in todos["todos"]]
        self.assertEqual(len(ids), len(set(ids)))
        self.assertFalse({t["id"]: t for t in todos["todos"]}["todo_s1"].get("project"))


if __name__ == "__main__":
    unittest.main()
