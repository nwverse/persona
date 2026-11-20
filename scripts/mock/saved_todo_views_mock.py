"""Saved To-do views and deterministic sample tasks for a browser run."""

import datetime
import os
import re
import threading
import uuid


PATH = "/api/v1/assistant/saved-todo-views"
DUE = {"all", "no_due", "today", "week", "month"}
SORT = {"default", "due", "priority"}


def _now():
    return datetime.datetime.now(datetime.timezone.utc)


def _sample_tasks():
    now = _now()
    today = now.date()
    later = today + datetime.timedelta(days=8)
    stamp = now.isoformat()
    return [
        {"id": "saved-view-review-late", "title": "Review metrics", "notes": "", "due_at": f"{today}T20:00:00Z",
         "created_at": stamp, "updated_at": stamp},
        {"id": "saved-view-review-early", "title": "Review the changelog", "notes": "", "due_at": f"{today}T10:00:00Z",
         "created_at": stamp, "updated_at": stamp},
        {"id": "saved-view-review-future", "title": "Review travel plans", "notes": "", "due_at": f"{later}T10:00:00Z",
         "created_at": stamp, "updated_at": stamp},
        {"id": "saved-view-unscheduled", "title": "Draft an outline", "notes": "", "created_at": stamp, "updated_at": stamp},
    ]


class Book:
    def __init__(self):
        self.revision = 0
        self.views = []

    def view(self):
        return {"schema_version": 1, "revision": str(self.revision), "views": list(self.views), "can_write": True}

    def change(self, body):
        if not isinstance(body, dict) or not isinstance(body.get("revision"), str) or not re.fullmatch(r"0|[1-9][0-9]*", body["revision"]):
            return {"error": "Supply the saved views revision.", "code": "invalid_request"}, 400, False
        op = body.get("op")
        fields = {
            "add": {"op", "revision", "name", "criteria"},
            "update": {"op", "revision", "id", "name", "criteria"},
            "delete": {"op", "revision", "id"},
        }
        if not isinstance(op, str) or op not in fields or set(body) != fields[op]:
            return {"error": "Invalid saved view action.", "code": "invalid_request"}, 400, False
        if str(self.revision) != body["revision"]:
            return {"error": "Saved views changed. Reload before saving.", "code": "conflict"}, 409, False
        current = next((view for view in self.views if view["id"] == body.get("id")), None)
        if op != "add" and current is None:
            return {"error": "Saved view not found.", "code": "view_not_found"}, 404, False
        if op == "delete":
            self.views.remove(current)
        else:
            raw_name, criteria = body["name"], body["criteria"]
            name = raw_name.strip() if isinstance(raw_name, str) else ""
            if (not name or len(name) > 64 or not isinstance(criteria, dict) or
                set(criteria) != {"text", "due_window", "section_sort"} or
                not all(isinstance(value, str) for value in criteria.values()) or
                criteria["due_window"] not in DUE or criteria["section_sort"] not in SORT or
                len(criteria["text"].strip().encode("utf-8")) > 1024):
                return {"error": "Invalid name or filters.", "code": "invalid_request"}, 400, False
            criteria = dict(criteria, text=criteria["text"].strip())
            if any(view is not current and view["name"].casefold() == name.casefold() for view in self.views):
                return {"error": "A saved view already has that name.", "code": "duplicate_name"}, 409, False
            if op == "add" and len(self.views) >= 50:
                return {"error": "Too many saved views.", "code": "limit_reached"}, 400, False
            if current and current["name"] == name and current["criteria"] == criteria:
                return self.view(), 200, False
            now = _now().isoformat()
            if current is None:
                self.views.append({"id": "view_" + uuid.uuid4().hex[:24], "name": name, "criteria": criteria,
                                   "created_at": now, "updated_at": now})
            else:
                index = self.views.index(current)
                self.views[index] = dict(current, name=name, criteria=criteria, updated_at=now)
        self.revision += 1
        return self.view(), 200, True


def register(d):
    d.help("MOCK_SAVED_TODO_VIEWS=1|legacy  Enable the browser's named To-do view fixture")
    mode = os.environ.get("MOCK_SAVED_TODO_VIEWS", "")
    if mode not in ("1", "legacy"):
        return
    hub = d.hub
    books = {}
    lock = threading.Lock()

    def selected(h):
        other = hub.other_manager(h)
        if other is None:
            return hub.MANAGERS["default"]
        manager, problem = other
        if problem:
            h.send_json(*problem)
            return None
        return manager

    def dispatch(h, req):
        manager = selected(h)
        if manager is None:
            return True
        if req.path == "/api/v1/todos":
            h.send_json({"todos": _sample_tasks()})
            return True
        if hub.is_read_only(h):
            h.send_json({"error": "Full credentials are required."}, 403)
            return True
        if mode == "legacy":
            h.send_json({"error": "Not found"}, 404)
            return True
        changed = False
        with lock:
            book = books.setdefault(manager, Book())
            if req.path == "/mock/saved-todo-views/reset":
                books[manager] = Book()
                response, status = {"ok": True}, 200
            elif req.method == "GET":
                response, status = book.view(), 200
            else:
                try:
                    response, status, changed = book.change(req.json())
                except (ValueError, UnicodeError):
                    response, status = {"error": "Invalid saved view."}, 400
        if changed:
            event = {"type": "saved_todo_views", "revision": response["revision"]}
            if manager != "assistant":
                event["manager"] = manager
            hub.broadcast(event)
        h.send_json(response, status)
        return True

    d.route(("GET",), "/api/v1/todos", dispatch)
    d.route(("GET", "POST"), PATH, dispatch)
    d.route(("POST",), "/mock/saved-todo-views/reset", dispatch)
