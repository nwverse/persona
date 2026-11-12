"""Tests mock_hub's suggested rules and the decision held for the briefing (MOCK_RULE_SUGGESTIONS, #5802, #5803)."""
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

ROUTE = "/api/v1/assistant/rule-suggestions/"


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


def call(base, path, method="GET", language="en"):
    request = Request(base + path, data=b"" if method == "POST" else None, method=method,
                      headers={"Authorization": "Bearer mock", "Content-Type": "application/json", "Accept-Language": language})
    with urlopen(request, timeout=3) as response:
        return json.load(response)


def cards(base, language="en"):
    """The suggestion each kind of card carries now: the open plan's, the merge decision's, the question card's."""
    main = call(base, "/api/v1/assistant", language=language)["main"]
    plans = call(base, "/api/v1/assistant/conversations/" + main + "/feed", language=language)["plans"]
    todos = {t["id"]: t for t in call(base, "/api/v1/todos", language=language)["todos"]}
    asks = {i["id"]: i for i in call(base, "/api/v1/interactions", language=language)["interactions"]}
    return {"plan": plans["plan_mock_3"].get("suggestion"), "decision": todos["todo_task_merge"]["ask"].get("suggestion"),
            "question": asks["ix_tests"].get("suggestion"), "todos": todos, "plans": plans}


class RuleSuggestionsMockTests(unittest.TestCase):
    def serve(self, **env):
        base, process, log = start({"MOCK_TASKS": "1", "MOCK_RULE_SUGGESTIONS": "1", **env})
        self.addCleanup(stop, process, log)
        return base

    def test_each_kind_of_card_carries_its_suggestion(self):
        base = self.serve()
        now = cards(base)
        self.assertEqual(now["plan"]["kind"], "plan_start")
        self.assertEqual(now["plan"]["creates"], "tasks.trust")
        self.assertEqual(now["plan"]["scope"], {"machine_id": "macbook", "machine": "MacBook", "directory": "/Users/me/code/weather"})
        self.assertEqual(now["decision"]["kind"], "standing_authorization")
        self.assertEqual(now["decision"]["scope"]["action"], "merge")
        self.assertEqual(now["decision"]["scope"]["conditions"], ["ci_green", "no_conflicts"])
        self.assertEqual(now["decision"]["scope"]["quote"], "Merge the PR now? → Merge")
        self.assertEqual((now["question"]["kind"], now["question"]["creates"], now["question"]["setting"], now["question"]["scope"]),
                         ("delegate", "", "agents.delegate", {}))
        for card in ("plan", "decision", "question"):
            self.assertTrue(now[card]["id"].startswith("rs_"))
            self.assertEqual(now[card]["count"], 3)
            self.assertIn("3 times", now[card]["text"])
        # only the open plan carries one, and it is never stored with the plan
        self.assertTrue(all("suggestion" not in p for pid, p in now["plans"].items() if pid != "plan_mock_3"))
        self.assertIn("suggestion", call(base, "/api/v1/task-plans/plan_mock_3")["plan"])

    def test_worded_in_the_requests_language(self):
        base = self.serve()
        self.assertIn("这件事你已经同意了 3 次", cards(base, "zh-Hans")["decision"]["text"])
        self.assertEqual(cards(base, "zh-Hans")["decision"]["scope"]["quote"], "现在合并这个 PR 吗？→ 合并")
        self.assertIn("3 回承認しました", cards(base, "ja")["decision"]["text"])
        self.assertEqual(cards(base, "ja")["todos"]["todo_task_merge"]["ask"]["question"], "この PR を今マージしますか？")

    def test_a_decision_is_held_for_the_briefing(self):
        base = self.serve()
        todos = cards(base)["todos"]
        held, pushed = todos["todo_task_copy"]["ask"], todos["todo_task_merge"]["ask"]
        self.assertIs(held["for_briefing"], True)
        self.assertNotIn("for_briefing", pushed)
        self.assertNotIn("suggestion", held)
        for ask in (held, pushed):
            self.assertEqual(ask["outcome"], "decide")
            self.assertGreaterEqual(len(ask["options"]), 2)
            self.assertEqual(ask["options"][0]["label"], ask["recommend"])

    def test_always_makes_the_rule_once_and_the_line_goes(self):
        base = self.serve()
        made = call(base, ROUTE + "rs_mock_merge/always", "POST")
        self.assertEqual((made["ok"], made["kind"], made["creates"]), (True, "standing_authorization", "standing_authorization"))
        self.assertEqual(made["authorization"]["actions"], ["merge"])
        self.assertEqual(made["authorization"]["quote"], "Merge the PR now? → Merge")
        listed = call(base, "/api/v1/assistant/standing-authorizations")
        self.assertEqual([g["id"] for g in listed["authorizations"]], [made["authorization"]["id"]])
        self.assertEqual(listed["revision"], made["revision"])
        self.assertIsNone(cards(base)["decision"])
        self.assertIsNotNone(cards(base)["plan"])  # another suggestion is left alone
        with self.assertRaises(HTTPError) as again:  # two taps make one rule
            call(base, ROUTE + "rs_mock_merge/always", "POST")
        self.assertEqual(again.exception.code, 404)
        # removed where it is listed
        call(base, "/api/v1/assistant/standing-authorizations/" + made["authorization"]["id"], "DELETE")
        self.assertEqual(call(base, "/api/v1/assistant/standing-authorizations")["authorizations"], [])

        trusted = call(base, ROUTE + "rs_mock_plan/always", "POST")
        self.assertEqual((trusted["kind"], trusted["creates"]), ("plan_start", "tasks.trust"))
        self.assertIn(trusted["key"], [k["key"] for k in call(base, "/api/v1/task-trust")["kinds"]])
        self.assertIsNone(cards(base)["plan"])

        delegate = call(base, ROUTE + "rs_mock_delegate/always", "POST")
        self.assertEqual((delegate["kind"], delegate["creates"], delegate["setting"]), ("delegate", "", "agents.delegate"))
        self.assertIsNone(cards(base)["question"])

    def test_not_now_hides_the_line_and_is_the_same_again(self):
        base = self.serve()
        self.assertEqual(call(base, ROUTE + "rs_mock_delegate/not-now", "POST"), {"ok": True})
        self.assertIsNone(cards(base)["question"])
        self.assertEqual(call(base, ROUTE + "rs_mock_delegate/not-now", "POST"), {"ok": True})
        with self.assertRaises(HTTPError) as after:  # answered: Always makes nothing
            call(base, ROUTE + "rs_mock_delegate/always", "POST")
        self.assertEqual(after.exception.code, 404)
        self.assertEqual(call(base, "/mock/rule_suggestions")["answered"], {"rs_mock_delegate": "not-now"})
        call(base, "/mock/rule_suggestions?reset=1")
        self.assertIsNotNone(cards(base)["question"])

    def test_an_unknown_or_stale_suggestion_is_404(self):
        base = self.serve()
        for verb in ("always", "not-now"):
            with self.assertRaises(HTTPError) as unknown:
                call(base, ROUTE + "rs_made_up/" + verb, "POST")
            self.assertEqual(unknown.exception.code, 404)
        stale = self.serve(MOCK_RULE_SUGGESTIONS="stale")
        self.assertIsNotNone(cards(stale)["decision"])
        with self.assertRaises(HTTPError) as gone:
            call(stale, ROUTE + "rs_mock_merge/always", "POST")
        self.assertEqual(gone.exception.code, 404)
        self.assertEqual(json.load(gone.exception)["error"], "This suggestion is no longer available.")
        self.assertEqual(call(stale, "/api/v1/assistant/standing-authorizations")["authorizations"], [])

    def test_without_the_flag_no_card_carries_one(self):
        base, process, log = start({"MOCK_TASKS": "1"})
        self.addCleanup(stop, process, log)
        main = call(base, "/api/v1/assistant")["main"]
        plans = call(base, "/api/v1/assistant/conversations/" + main + "/feed")["plans"]
        self.assertTrue(all("suggestion" not in p for p in plans.values()))
        todos = call(base, "/api/v1/todos")["todos"]
        self.assertNotIn("todo_task_merge", [t["id"] for t in todos])
        self.assertTrue(all("suggestion" not in t.get("ask", {}) and "for_briefing" not in t.get("ask", {}) for t in todos))
        self.assertTrue(all("suggestion" not in i for i in call(base, "/api/v1/interactions")["interactions"]))


if __name__ == "__main__":
    unittest.main()
