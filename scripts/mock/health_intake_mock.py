"""Synthetic Health intake and water migration states for the phone's card (#13636)."""
import copy
from datetime import timedelta
import os
import threading


def register(d):
    d.help("MOCK_HEALTH_INTAKE=1  Health intake and water migration card (#13636); "
           "MOCK_WATER_EXTENSION=pending|none|migrated selects its initial state")
    if os.environ.get("MOCK_HEALTH_INTAKE") != "1":
        return
    mode = os.environ.get("MOCK_WATER_EXTENSION", "pending")
    lock = threading.Lock()
    state = {}
    now = d.hub.NOW

    def reset():
        with lock:
            state.update(pending=mode == "pending", records=[], settings={
                "water": {"goal_ml": 2000, "cup_ml": 250, "remind": False, "start": "09:00", "end": "21:00",
                          "interval_minutes": 60, "weekends": True, "stop_when_done": True},
                "protein": {"goal_g": 60},
            })

    def read(handler, req):
        try:
            days = int(req.query.get("days", ["7"])[0])
            if not 1 <= days <= 366:
                raise ValueError()
        except ValueError:
            return handler.send_json({"error": "invalid days", "code": "bad_request"}, 400)
        with lock:
            total = sum(r["amount"] for r in state["records"])
            history = [{"date": (now.date() - timedelta(days=i)).isoformat(),
                        "water": total if i == 0 else 0, "protein": 0} for i in range(days - 1, -1, -1)]
            view = {"water": {"today": total, "goal": state["settings"]["water"]["goal_ml"],
                              "entries": copy.deepcopy(state["records"])},
                    "protein": {"today": 0, "goal": 60, "entries": []}, "days": history, "zone": "UTC"}
            if state["pending"]:
                view["water_extension"] = "pending"
        return handler.send_json(view)

    def migrate(handler, req):
        if mode == "none":
            return handler.send_json({"error": "water extension is not installed", "code": "water_import_failed"}, 404)
        with lock:
            imported = 0
            if state["pending"]:
                for n, amount in enumerate((200, 250), 1):
                    state["records"].append({"id": "00000000-0000-5000-8000-%012d" % n,
                        "kind": "water", "amount": amount, "at": now.isoformat(), "note": "",
                        "seq": n, "created_at": now.isoformat()})
                imported = 2
                state["pending"] = False
                state["settings"]["water"].update(goal_ml=2400, interval_minutes=90, remind=True)
            result = {"imported": imported, "extension_disabled": True, "settings": copy.deepcopy(state["settings"])}
        return handler.send_json(result)

    reset()
    d.feature("health_intake")
    d.route("GET", "/api/v1/assistant/health/intake", read)
    d.route("POST", "/api/v1/assistant/health/intake/import", migrate)
    d.on_reset(reset)
