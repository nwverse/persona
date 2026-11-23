"""Overview's watch cards (#13020, docs/assistant/watch-cards.md) with fictional data, MOCK_WATCH_CARDS.

MOCK_WATCH_CARDS=1 fills the wall with one card of each display kind (number, status light, table, trend), one of them
tripping its alert, a paused one, one not checked yet and a draft waiting for the owner; =empty starts with none.
The routes change the cards in memory like the hub does (confirm puts a draft first, order, edit, pause, delete)."""
import copy
import datetime
import os
import threading


BASE = "/api/v1/assistant/watches"
MODE = os.environ.get("MOCK_WATCH_CARDS", "")


def _ago(**kw):
    t = datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(**kw)
    return t.isoformat(timespec="seconds").replace("+00:00", "Z")


def _soon(**kw):
    t = datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(**kw)
    return t.isoformat(timespec="seconds").replace("+00:00", "Z")


def _history(values):
    return [{"at": _ago(days=len(values) - i), "number": v} for i, v in enumerate(values)]


def fixtures():
    if MODE == "empty":
        return []
    return [
        {"id": "w_orders", "title": "Open orders", "question": "How many orders are waiting in the Garden app shop?", "display": "trend",
         "unit": "orders", "schedule": {"repeat": "daily", "time": "08:30"}, "alert": {"when": "above", "value": 20}, "project": "garden-app",
         "project_name": "Garden app", "state": "active", "task_id": "watch-w_orders",
         "latest": {"at": _ago(hours=1), "number": 27, "text": "27 waiting, up from 12 yesterday", "alert": True},
         "history": _history([9, 11, 8, 12, 14, 12, 27]), "next_run": _soon(hours=23), "last_run": _ago(hours=1), "last_status": "ok"},
        {"id": "w_uptime", "title": "Docs site", "question": "Is the Docs site up and serving the latest build?", "display": "status",
         "schedule": {"repeat": "interval", "every_minutes": 120}, "alert": {"when": "bad"}, "project": "docs-site", "project_name": "Docs site",
         "state": "active", "task_id": "watch-w_uptime", "latest": {"at": _ago(minutes=40), "status": "ok", "text": "Up, build from this morning"},
         "history": [], "next_run": _soon(minutes=80)},
        {"id": "w_launch", "title": "Q4 launch checklist", "question": "Which Q4 launch steps are still open?", "display": "table",
         "schedule": {"repeat": "cron", "cron": "0 9 * * 1-5"}, "cron_text": "At 09:00, Monday through Friday", "project": "q4-launch",
         "project_name": "Q4 launch", "state": "active", "task_id": "watch-w_launch",
         "latest": {"at": _ago(hours=3), "status": "warn", "text": "2 of 5 steps open",
                    "table": {"columns": ["Step", "Owner", "State"], "rows": [["Store listing", "Design", "open"], ["Release notes", "Docs", "open"],
                                                                         ["Beta feedback", "Support", "done"], ["Pricing page", "Web", "done"], ["Press kit", "Marketing", "done"]]}}},
        {"id": "w_crashes", "title": "Crash-free sessions", "question": "What share of Garden app sessions were crash-free yesterday?", "display": "number",
         "unit": "%", "schedule": {"repeat": "daily", "time": "07:00"}, "alert": {"when": "below", "value": 99}, "project": "garden-app",
         "project_name": "Garden app", "state": "active", "task_id": "watch-w_crashes",
         "latest": {"at": _ago(hours=2), "number": 99.6, "text": "Steady over the week"}, "history": _history([99.4, 99.5, 99.6])},
        {"id": "w_reviews", "title": "New store reviews", "question": "How many new one-star reviews came in?", "display": "number",
         "schedule": {"repeat": "daily", "time": "18:00"}, "state": "active", "task_id": "watch-w_reviews", "next_run": _soon(hours=6)},
        {"id": "w_backup", "title": "Nightly backup", "question": "Did last night's backup finish?", "display": "status",
         "schedule": {"repeat": "daily", "time": "06:00"}, "state": "active", "paused": True, "task_id": "watch-w_backup",
         "latest": {"at": _ago(days=3), "status": "ok", "text": "Finished in 12 minutes"}},
        {"id": "w_draft", "title": "Newsletter sign-ups", "question": "How many people signed up for the newsletter today?", "display": "trend",
         "schedule": {"repeat": "daily", "time": "21:00"}, "alert": {"when": "change"}, "state": "draft"},
    ]


def register(d):
    d.help("MOCK_WATCH_CARDS=1|empty  Overview watch cards with fictional data (#13020)")
    if MODE not in ("1", "empty"):
        return
    hub = d.hub
    lock = threading.Lock()
    book = {"cards": fixtures()}

    def changed():
        hub.broadcast({"type": "assistant_watches"})

    def find(cid):
        return next((c for c in book["cards"] if c["id"] == cid), None)

    def listing(h, req):
        project = req.query.get("project", [""])[0]
        with lock:
            cards = [c for c in book["cards"] if not project or (c.get("project") == project and c["state"] == "active")]
            h.send_json({"watches": copy.deepcopy(cards)})
        return True

    def confirm(h, req):
        with lock:
            c = find(req.match["id"])
            if not c:
                h.send_json({"error": "That watch card no longer exists."}, 404)
                return True
            c.update(state="active", task_id="watch-" + c["id"])
            book["cards"] = [c] + [x for x in book["cards"] if x is not c]
            h.send_json({"watch": copy.deepcopy(c)})
        changed()
        return True

    def order(h, req):
        ids = (req.json() or {}).get("ids", [])
        with lock:
            first = [c for i in ids for c in book["cards"] if c["id"] == i]
            book["cards"] = first + [c for c in book["cards"] if c not in first]
        h.send_json({"ok": True})
        changed()
        return True

    def change(h, req):
        with lock:
            c = find(req.match["id"])
            if not c:
                h.send_json({"error": "That watch card no longer exists."}, 404)
                return True
            if req.method == "DELETE":
                book["cards"].remove(c)
                h.send_json({"ok": True})
            else:
                body = req.json() or {}
                for k in ("title", "question", "display", "unit", "project", "paused"):
                    if k in body:
                        c[k] = body[k]
                if "alert" in body:
                    c["alert"] = body["alert"]
                h.send_json({"watch": copy.deepcopy(c)})
        changed()
        return True

    d.route("GET", BASE, listing)
    d.route("PUT", BASE + "/order", order)
    d.route("POST", BASE + r"/(?P<id>[^/]+)/confirm", confirm)
    d.route(("PUT", "DELETE"), BASE + r"/(?P<id>[^/]+)", change)
    d.on_reset(lambda: book.update(cards=fixtures()))
