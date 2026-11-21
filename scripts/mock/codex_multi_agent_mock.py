"""Task-plan start receipts and Agent rows for recent and older Codex CLIs on one computer."""
import os
import threading


PLAN_ID = "plan_mock_3"
MACHINE = "studio"
ACTIVE_SESSION = "codex-multi-agent-fixture-confirmed"
LEGACY_SESSION = "codex-multi-agent-fixture-legacy"
WORKFLOW_KIND = "codex_multi_agent_v2"
SKIPPED = "This Codex CLI does not support multi-agent v2. The Agent started without it."


def register(d):
    d.help("MOCK_CODEX_MULTI_AGENT=1 with MOCK_TASKS=1  Mixed Claude/Codex workflow plan and confirmed/skipped start receipts")
    hub = d.hub
    if os.environ.get("MOCK_CODEX_MULTI_AGENT") != "1" or not hub.MOCK_TASKS:
        return
    d.feature("ultracode")
    lock = threading.Lock()
    sessions = {ACTIVE_SESSION, LEGACY_SESSION}

    def reset():
        base = {"machine": MACHINE, "cwd": "/demo/workflows", "permission": "default", "role": "worker", "ultracode": False}
        with lock:
            hub.SESSIONS[:] = [row for row in hub.SESSIONS if row["session_id"] not in sessions]
            for session in sessions:
                hub.ITEMS.pop(session, None)
            plan = hub.TASK_PLANS[PLAN_ID]
            plan.update(state="open", updated_at=hub.ago(), tasks=[
                dict(base, title="Check the release checklist", prompt="Review the checklist", launcher="claude",
                     model="opus", workflow_kind="claude_ultracode"),
                dict(base, title="Review the current build", prompt="Review the build", launcher="codex",
                     model="gpt-5.5", workflow_kind=WORKFLOW_KIND),
                dict(base, title="Review the older build", prompt="Review the build", launcher="codex-legacy",
                     model="gpt-5.5", workflow_kind=WORKFLOW_KIND),
            ])
            machine = next(row for row in hub.MACHINES if row["id"] == MACHINE)
            machine["claude_version"] = "2.1.250"

    def reset_route(h, _req):
        reset()
        h.send_json({"plan_id": PLAN_ID, "conversation": hub.ASK1, "machine": MACHINE,
                     "confirmed": ACTIVE_SESSION, "legacy": LEGACY_SESSION})
        return True

    def start(h, req):
        with lock:
            plan = hub.TASK_PLANS[PLAN_ID]
            if plan["state"] != "open":
                h.send_json({"error": "This plan already started"}, 409)
                return True
            edits = req.json().get("tasks", [{} for _ in plan["tasks"]])
            if (not isinstance(edits, list) or len(edits) != len(plan["tasks"])
                    or any(not isinstance(edit, dict) or ("ultracode" in edit and type(edit["ultracode"]) is not bool)
                           for edit in edits)):
                h.send_json({"error": "Expected one edit per task and boolean workflow choices"}, 400)
                return True
            for task, edit in zip(plan["tasks"], edits):
                task["ultracode"] = edit.get("ultracode", task["ultracode"])
            for index, session_id in enumerate((ACTIVE_SESSION, LEGACY_SESSION), 1):
                task = plan["tasks"][index]
                row = hub.sess(MACHINE, session_id, task["title"], task["cwd"], "working", "working",
                               "Workflow review", 92, task["launcher"], index, 0, "Review in progress", agent="codex",
                               launcher=task["launcher"], effort="high", efforts=["medium", "high"],
                               capabilities={"send": True, "interrupt": True})
                if task["ultracode"]:
                    if session_id == ACTIVE_SESSION:
                        task["workflow_active"] = row["codex_multi_agent_v2"] = True
                    else:
                        task["ultracode"] = False
                        task["workflow_note"] = SKIPPED
                hub.SESSIONS.append(row)
                hub.ITEMS[session_id] = [hub.it("assistant", "text", 0, text="Review in progress")]
            plan.update(state="started", updated_at=hub.ago())
            h.send_json({"plan": plan})
        hub.push_machine(MACHINE)
        return True

    reset()
    d.on_reset(reset)
    d.route("POST", "/mock/codex-multi-agent/reset", reset_route)
    d.route("POST", f"/api/v1/task-plans/{PLAN_ID}/start", start)
