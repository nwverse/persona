"""The web Starred page's loopback fixture. Separate assistants get separate collections."""
import os
import re
import threading
import unicodedata
import uuid
from urllib.parse import unquote


BASE = "/api/v1/assistant/starred"
COLLECTIONS = BASE + "/collections"
SOURCE_FIELDS = ("machine_id", "session_id", "item_id", "conversation", "source_fingerprint")
POSITION = "line900#0"
LONG_USER = "fixture-long-user#0"
LONG_USER_VISIBLE = "User-visible long note " + "🐈" * 6500
VERSIONS = {"original": ("Original words from the reused line", "a" * 32),
            "replacement": ("Replacement words from the reused line", "b" * 32)}


class Collections:
    """One manager's fixture state, read and written under the same lock as its starred snapshots."""
    def __init__(self):
        self.revision = 0
        self.names = {}
        self.assignments = {}

    def view(self, stars, can_write):
        return {
            "revision": self.revision,
            "can_write": can_write,
            "collections": [{"id": key, "name": name} for key, name in self.names.items()],
            "assignments": {row["id"]: self.assignments[row["id"]] for row in stars if row["id"] in self.assignments},
        }

    def restore(self, stars, star_id, collection):
        if not isinstance(collection, str) or collection not in self.names:
            return
        active = {row["id"] for row in stars}
        self.assignments = {star: target for star, target in self.assignments.items() if star in active}
        self.assignments[star_id] = collection
        self.revision += 1

    def change(self, req, stars):
        """Return the wire result, status and whether this valid write sends an invalidation."""
        def rejected(code, message, status):
            return {"error": message, "code": code}, status, False

        valid_ids = {row["id"] for row in stars}
        names = self.names.copy()
        assignments = {star: collection for star, collection in self.assignments.items() if star in valid_ids}
        if req.method == "DELETE":
            revisions = req.query.get("revision", [])
            revision = revisions[0] if len(revisions) == 1 and not req.query.keys() - {"revision", "manager"} else ""
            payload = {"revision": int(revision)} if re.fullmatch(r"0|[1-9][0-9]{0,15}", revision) else {}
        else:
            try:
                payload = req.json()
            except ValueError:
                payload = {}
        if not isinstance(payload, dict) or type(payload.get("revision")) is not int or not 0 <= payload["revision"] < (1 << 53) - 1:
            return rejected("invalid_request", "A collection change needs a revision.", 400)
        if payload["revision"] != self.revision:
            return rejected("conflict", "The starred collections changed. Reload and try again.", 409)

        if req.path == COLLECTIONS + "/assignments":
            star_ids, collection = payload.get("star_ids"), payload.get("collection_id")
            if (not isinstance(star_ids, list) or not 1 <= len(star_ids) <= 300 or
                    not all(isinstance(x, str) and x for x in star_ids) or len(set(star_ids)) != len(star_ids)):
                return rejected("invalid_request", "Choose saved messages to move.", 400)
            if "collection_id" not in payload or (collection is not None and (not isinstance(collection, str) or not re.fullmatch(r"sc[0-9a-f]{32}", collection))):
                return rejected("invalid_request", "Choose a collection or Unfiled.", 400)
            if collection is not None and collection not in names:
                return rejected("collection_not_found", "That collection is gone.", 404)
            if not valid_ids.issuperset(star_ids):
                return rejected("star_not_found", "A saved message is gone.", 404)
            for star in star_ids:
                if collection is None:
                    assignments.pop(star, None)
                else:
                    assignments[star] = collection
        else:
            identifier = None if req.method == "POST" else req.match.group(1)
            if identifier is not None:
                if not re.fullmatch(r"sc[0-9a-f]{32}", identifier):
                    return rejected("invalid_request", "Invalid collection identifier.", 400)
                if identifier not in names:
                    return rejected("collection_not_found", "That collection is gone.", 404)
            if req.method == "DELETE":
                del names[identifier]
                assignments = {star: collection for star, collection in assignments.items() if collection != identifier}
            else:
                name = payload.get("name")
                if (not isinstance(name, str) or not (name := name.strip()) or len(name) > 64 or
                        any(unicodedata.category(char) == "Cc" for char in name)):
                    return rejected("invalid_request", "Enter a collection name up to 64 characters.", 400)
                if any(key != identifier and previous.casefold() == name.casefold() for key, previous in names.items()):
                    return rejected("duplicate_name", "A collection already uses that name.", 409)
                if identifier is None:
                    if len(names) >= 50:
                        return rejected("limit_reached", "The collection limit is 50.", 400)
                    identifier = "sc" + uuid.uuid4().hex
                names[identifier] = name

        changed = names != self.names or assignments != self.assignments
        if changed:
            self.names, self.assignments = names, assignments
            self.revision += 1
        return self.view(stars, True), 200, changed


def register(d):
    hub = d.hub
    d.help("MOCK_STARRED=1|old  Enable starred-snapshot routes (1) or simulate an older assistant (old)")
    mode = os.environ.get("MOCK_STARRED", "old")
    d.help("MOCK_STARRED_COLLECTIONS=1  Enable the collections API with MOCK_STARRED=1")
    collections_enabled = mode == "1" and os.environ.get("MOCK_STARRED_COLLECTIONS") == "1"
    if mode == "1":
        d.feature("starred_messages")
    books, collections = {}, {}
    lock = threading.Lock()
    unreadable = False

    def selected_manager(h):
        other = hub.other_manager(h)
        if other is None:
            return hub.MANAGERS["default"]
        manager, problem = other
        if problem is not None:
            h.send_json(*problem)
            return None
        return manager

    def invalidate(manager):
        event = {"type": "assistant_starred"}
        if manager != "assistant":
            event["manager"] = manager
        hub.broadcast(event)

    def collection_route(h, req):
        if not collections_enabled:
            h.send_json({"error": "Not found"}, 404)
            return True
        manager = selected_manager(h)
        if manager is None:
            return True
        can_write = not hub.is_read_only(h)
        if req.method != "GET" and not can_write:
            h.send_json({"error": "Full credentials required.", "code": "forbidden"}, 403)
            return True
        with lock:
            stars = books.get(manager, [])
            state = collections.setdefault(manager, Collections())
            if req.method == "GET":
                body, status, changed = state.view(stars, can_write), 200, False
            else:
                body, status, changed = state.change(req, stars)
        if changed:
            invalidate(manager)
        h.send_json(body, status)
        return True

    def route(h, req):
        if mode != "1":
            h.send_json({"error": "Not found"}, 404)
            return True
        manager = selected_manager(h)
        if manager is None:
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
                restore_to = item.pop("collection_id", None)
                key = tuple(item.get(k) for k in SOURCE_FIELDS)
                if not all(key[:4]) or not str(item.get("text", "")).strip():
                    h.send_json({"error": "A star needs a message"}, 400)
                    return True
                star = next((s for s in stars if tuple(s.get(k) for k in SOURCE_FIELDS) == key), None)
                if star is None:
                    star = dict(item, id="sm" + uuid.uuid4().hex, starred_at=hub.ago())
                    stars.insert(0, star)
                    if state := collections.get(manager):
                        state.restore(stars, star["id"], restore_to)
                    changed = True
                body = {"star": star}
            else:
                identifier = req.match.group(1)
                removed = next((star for star in stars if star["id"] == identifier), None)
                if removed is not None:
                    stars.remove(removed)
                    changed = True
                state = collections.get(manager)
                if removed is not None and state is not None and identifier in state.assignments:
                    removed = dict(removed, collection_id=state.assignments[identifier])
                body = {"removed": removed}
        if changed:
            invalidate(manager)
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
    d.route(("GET", "POST"), COLLECTIONS, collection_route)
    d.route(("PATCH",), COLLECTIONS + "/assignments", collection_route)
    d.route(("PATCH", "DELETE"), COLLECTIONS + r"/([^/]+)", collection_route)
    d.route(("GET", "POST"), BASE, route)
    d.route(("DELETE",), BASE + r"/([^/]+)", route)
