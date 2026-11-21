"""The web Starred page's loopback fixture. Separate assistants get separate collections."""
import os
import re
import threading
import uuid
from urllib.parse import unquote


BASE = "/api/v1/assistant/starred"
SOURCE_FIELDS = ("machine_id", "session_id", "item_id", "conversation", "source_fingerprint")
POSITION = "line900#0"
LONG_USER = "fixture-long-user#0"
LONG_USER_VISIBLE = "User-visible long note " + "🐈" * 6500
VERSIONS = {"original": ("Original words from the reused line", "a" * 32),
            "replacement": ("Replacement words from the reused line", "b" * 32)}


def register(d):
    hub = d.hub
    d.help("MOCK_STARRED=1|old  Enable starred-snapshot routes (1) or simulate an older assistant (old)")
    mode = os.environ.get("MOCK_STARRED", "old")
    if mode == "1":
        d.feature("starred_messages")
    books = {}
    lock = threading.Lock()
    unreadable = False

    def route(h, req):
        if mode != "1":
            h.send_json({"error": "Not found"}, 404)
            return True
        manager = hub.MANAGERS["default"]
        other = hub.other_manager(h)
        if other is not None:
            manager, problem = other
            if problem is not None:
                h.send_json(*problem)
                return True
        changed = False
        with lock:
            stars = books.setdefault(manager, [])
            if req.method == "GET":
                selected = stars[:]
                if session := (req.query.get("session") or [""])[0]:
                    selected = [s for s in selected if f"{s['machine_id']}/{s['session_id']}" == session]
                if req.query.get("index") == ["1"]:
                    refs = [{k: s[k] for k in (*SOURCE_FIELDS, "id") if k in s} for s in selected]
                    body = {"refs": refs, "can_write": True}
                else:
                    body = {"stars": selected, "can_write": True}
            elif req.method == "POST":
                item = req.json()
                key = tuple(item.get(k) for k in SOURCE_FIELDS)
                if not all(key[:4]) or not str(item.get("text", "")).strip():
                    h.send_json({"error": "A star needs a message"}, 400)
                    return True
                star = next((s for s in stars if tuple(s.get(k) for k in SOURCE_FIELDS) == key), None)
                if star is None:
                    star = dict(item, id="sm" + uuid.uuid4().hex, starred_at=hub.ago())
                    stars.insert(0, star)
                    changed = True
                body = {"star": star}
            else:
                identifier = req.match.group(1)
                removed = next((star for star in stars if star["id"] == identifier), None)
                if removed is not None:
                    stars.remove(removed)
                    changed = True
                body = {"removed": removed}
        if changed:
            event = {"type": "assistant_starred"}
            if manager != "assistant":
                event["manager"] = manager
            hub.broadcast(event)
        h.send_json(body)
        return True

    def source(h, req):
        nonlocal unreadable
        phase = req.json().get("phase")
        if mode != "1" or phase not in (*VERSIONS, "reset", "stale", "long-user", "unreadable"):
            h.send_json({"error": "Not found"}, 404)
            return True
        with lock:
            unreadable = phase == "unreadable"
            if unreadable:
                h.send_json({"ok": True})
                return True
            if phase == "stale":
                # Same-size overwrite: the node's regular page is unchanged, but Full can't verify the raw source.
                hub.FULL.pop(POSITION, None)
                h.send_json({"ok": True})
                return True
            items = [item for item in hub.ITEMS[hub.REVIEW] if item["id"] not in (POSITION, LONG_USER)]
            hub.FULL.pop(POSITION, None)
            hub.FULL.pop(LONG_USER, None)
            if phase == "long-user":
                raw = '<persona-context kind="skill-instructions">' + "h" * 25000 + "</persona-context>\n" + LONG_USER_VISIBLE
                size = len(raw.encode())
                cut = raw[:24 * 1024] + f"\n…（已截断，点开看全文，共 {size} 字节）"
                item = hub.it("user", "text", 1, id=LONG_USER, seq=901, text=cut, full_size=size)
                items.append(item)
                hub.FULL[LONG_USER] = {k: v for k, v in item.items() if k != "full_size"} | {"text": raw}
            elif phase != "reset":
                text, proof = VERSIONS[phase]
                item = hub.it("user", "text", 1, id=POSITION, seq=900, text=text, source_fingerprint=proof)
                items.append(item)
                hub.FULL[POSITION] = item
            hub.ITEMS[hub.REVIEW] = items
            hub.HISTORY_NOW["token"] = hub.HISTORY if phase == "reset" else "starred-source-" + phase
        h.send_json({"ok": True})
        return True

    def full_source(h, req):
        if mode != "1":
            return False
        session, item_id = (unquote(part) for part in req.match.groups())
        if session == hub.REVIEW and item_id == POSITION and unreadable:
            h.send_json({"error": "Synthetic transcript read failure"}, 502)
            return True
        if item_id in hub.FULL or re.fullmatch(r"line[0-9]+#[0-9]+", item_id):
            return False  # The normal mock handles saved full values and unverified positions.
        # A real node can return an uncut stable item from the same transcript.
        item = next((value for value in hub.find_items(session) if value["id"] == item_id and not value.get("full_size")), None)
        if item is None:
            return False
        h.send_json(item)
        return True

    d.route(("POST",), "/mock/starred-source", source)
    d.route(("GET",), r"/api/v1/sessions/[^/]+/([^/]+)/items/([^/]+)/full", full_source)
    d.route(("GET", "POST"), BASE, route)
    d.route(("DELETE",), BASE + r"/([^/]+)", route)
