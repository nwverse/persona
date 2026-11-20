#!/usr/bin/env python3
"""Synthetic dispatch acceptance hub; no production data or extra wire fields.

PORT=18912 python3 apple/PersonaUITests/Fixtures/dispatch_hub.py
Use PERSONA_MOCK_HUB=http://127.0.0.1:18912 for DispatchedTaskCardTests.
"""
import copy
import importlib.util
import os
from pathlib import Path

os.environ["MOCK_TASKS"] = "1"
os.environ["MOCK_TASKS_NO_ASKS"] = "1"
# The conversation and its dispatched Agents belong to the same assistant in this fixture.
os.environ.setdefault("MOCK_MANAGERS", "1")
root = Path(__file__).resolve().parents[3]
spec = importlib.util.spec_from_file_location("dispatch_mock", root / "scripts/mock/mock_hub.py")
hub = importlib.util.module_from_spec(spec)
spec.loader.exec_module(hub)

# One standalone task with an Agent, and one still starting. The other two are the plan's tasks.
todos = {task["id"]: copy.deepcopy(task) for task in hub.TODOS_INIT}
widget = todos["todo_task_widget"]
widget.pop("plan_id", None)
starting = copy.deepcopy(widget)
starting.update(id="todo_dispatch_starting", title="检查启动状态", agents=[], created_at=hub.ago(minutes=1))
failed = dict(starting, id="todo_dispatch_failed", title="校对测试清单", start_error="connection closed")
plan = copy.deepcopy(hub.TASK_PLANS["plan_mock_1"])
plan["tasks"] = [copy.deepcopy(hub.TASK_PLANS["plan_mock_1"]["tasks"][1]),
                 copy.deepcopy(hub.TASK_PLANS["plan_mock_2"]["tasks"][0])]
plan["tasks"].append(dict(plan["tasks"][0], title="未启动的计划任务", todo_id=None, error="当前没有可用的电脑。"))
for task in plan["tasks"]:
    if task.get("todo_id"):
        todos[task["todo_id"]]["plan_id"] = plan["id"]
hub.TODOS_INIT[:] = [widget, todos["todo_task_tests"], todos["todo_task_release"], starting, failed]
hub.TODOS[:] = copy.deepcopy(hub.TODOS_INIT)
hub.TASK_PLANS.clear()
hub.TASK_PLANS[plan["id"]] = plan
hub.TASK_FEED[hub.ASK1] = [
    hub._feed("dispatch_one", 5.8, "task", widget["id"]),
    hub._feed("dispatch_many", 3.8, "plan", plan["id"]),
    hub._feed("dispatch_many_one", 3.7, "task", "todo_task_tests"),
    hub._feed("dispatch_many_two", 3.6, "task", "todo_task_release"),
    hub._feed("dispatch_starting", 1.8, "task", starting["id"]),
    hub._feed("dispatch_failed", 1.7, "task", failed["id"]),
]
hub.ITEMS[hub.ASK1] = [
    hub.it("user", "text", 6, text="修一下小组件崩溃。"),
    hub.it("assistant", "text", 5.9, text="已派出修复任务。"),
    hub.it("user", "text", 4, text="再补测试和发布说明。"),
    hub.it("assistant", "text", 3.9, text="已分别派出两项工作；另一项没有启动成功。"),
    hub.it("user", "text", 2, text="检查一下启动状态。"),
    hub.it("assistant", "text", 1.9, text="任务已创建，Agent 正在启动。"),
    hub.it("user", "text", 1.5, text="任务状态已更新。", injected={
        "kind": "task_notice", "state": "done", "title": todos["todo_task_release"]["title"],
        "todo_id": "todo_task_release", "line": "任务「整理发布说明」做完了",
    }),
]

source_items = list(hub.ITEMS[hub.ASK1])
widget_session = next(s for s in hub.SESSIONS if s["machine_id"] == "macbook" and s["session_id"] == hub.WIDGET)
studio_widget = dict(widget_session, machine_id="studio", title="工作室检查小组件", state="needs_you",
                     status="blocked", pending_interactions=1)


# Unlike /mock/say (subscribed conversations), the source can receive new messages while the Agent is in front.
class DispatchHandler(hub.H):
    def do_POST(self):
        if self.path == "/fixture/handoff/reset":
            hub.ITEMS[hub.ASK1] = list(source_items)
            hub.TODOS[:] = copy.deepcopy(hub.TODOS_INIT)
            hub.SESSIONS[:] = [s for s in hub.SESSIONS if s is not studio_widget]
            return self.send_json({"reset": True})
        if self.path == "/fixture/handoff/multi":
            task = next(t for t in hub.TODOS if t["id"] == widget["id"])
            if studio_widget not in hub.SESSIONS:
                hub.SESSIONS.append(studio_widget)
                task["agents"].append(dict(task["agents"][0], machine_id="studio", title=studio_widget["title"]))
            return self.send_json({"agents": len(task["agents"])})
        if self.path == "/fixture/handoff/append":
            items = hub.ITEMS[hub.ASK1]
            line = hub.it("assistant", "text", 0, text="新到的测试消息。 " * 52)
            hub.ITEMS[hub.ASK1] = items + [line]
            hub.broadcast(dict(type="items", machine_id="server", session_id=hub.ASK1,
                               items=[line], **hub.span([line], items[-1]["seq"])))
            return self.send_json({"appended": line["id"]})
        super().do_POST()


if __name__ == "__main__":
    server = hub.ThreadingHTTPServer(("127.0.0.1", hub.PORT), DispatchHandler)
    server.daemon_threads = True
    hub.ENDPOINTS.append(hub.endpoint("127.0.0.1", hub.PORT, "http", "lan"))
    print(f"Dispatch fixture ready on port {hub.PORT}", flush=True)
    try:
        server.serve_forever()
    finally:
        server.server_close()
