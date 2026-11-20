#!/usr/bin/env python3
"""Replay synthetic project evidence captured from the real Go list, detail and owner update handlers."""
import copy
import importlib.util
import json
import os
import threading
from pathlib import Path
from urllib.parse import urlsplit

os.environ.setdefault("PORT", "19465")
root = Path(__file__).resolve().parents[3]
spec = importlib.util.spec_from_file_location("project_evidence_mock", root / "scripts/mock/mock_hub.py")
mock = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mock)

PROJECT = "community-garden"
SECOND = "garden-verification"
EMPTY = "empty-garden"
OLDER = "sample-archive"
CHAT = "garden-review"
LIST = "/api/v1/kb/projects"
fixtures = root / "apple/PersonaTests/Fixtures"
lock = threading.RLock()
states = {}
writes = []


def captured(name):
    return json.loads((fixtures / ("garden-project-" + name + ".json")).read_text())


ROWS = captured("list")["items"]
GARDEN = captured("detail")["detail"]
REFERENCE = captured("detail-reference-only")["detail"]
VERIFICATION = captured("detail-verification")["detail"]
EMPTY_ROW = captured("empty-list")["items"][0]
EMPTY_FULL = captured("empty-detail")["detail"]
EMPTY_REFERENCE = captured("empty-detail-reference-only")["detail"]
ARCHIVE = {"kind": "projects", "id": OLDER, "name": "Archive index", "relevance": 0}


def current(owner):
    state = states.setdefault((owner, PROJECT), {"mode": "full", "urls": GARDEN["meta"]["source_urls"]})
    detail = copy.deepcopy(REFERENCE if state["mode"] == "reference-only" else GARDEN)
    detail["meta"]["source_urls"] = state["urls"]
    linked = [source for source in detail["sources"] if source.get("refs")]
    detail["sources"] = [{"id": url, "url": url} for url in state["urls"]] + linked
    return detail


class Handler(mock.H):
    def do_GET(self):
        path = urlsplit(self.path).path
        owner = self.headers.get("X-Persona-Manager", "")
        with lock:
            if path == "/mock/project-evidence/reset":
                states.clear()
                writes.clear()
                return self.send_json({"fixture": PROJECT})
            if path == "/mock/project-evidence/writes":
                return self.send_json({"writes": copy.deepcopy(writes)})
            if path == LIST:
                rows = copy.deepcopy(ROWS)
                row = next(item for item in rows if item["id"] == PROJECT)
                value = current(owner)
                for key in ("project_mode", "freshness", "sources"):
                    if key in value:
                        row[key] = value[key]
                empty = copy.deepcopy(EMPTY_ROW)
                if states.get((owner, EMPTY)) == "reference-only":
                    empty.update(project_mode="reference-only", freshness=EMPTY_REFERENCE["freshness"])
                return self.send_json({"items": rows + [empty, ARCHIVE]})
            if path == LIST + "/" + PROJECT:
                return self.send_json({"detail": current(owner)})
            if path == LIST + "/" + SECOND:
                return self.send_json({"detail": VERIFICATION})
            if path == LIST + "/" + EMPTY:
                return self.send_json({"detail": EMPTY_REFERENCE if states.get((owner, EMPTY)) == "reference-only" else EMPTY_FULL})
            if path == LIST + "/" + OLDER:
                return self.send_json({"detail": {"kind": "projects", "id": OLDER, "path": "projects/" + OLDER,
                                                  "meta": {"name": ARCHIVE["name"], "pinned": "Archive note."}}})
            if path == "/api/v1/kb/chats/" + CHAT:
                return self.send_json({"detail": {"kind": "chats", "id": CHAT, "path": "chats/" + CHAT,
                                                  "meta": {"name": "Planting plan review"}, "summary": "Source delivery history."}})
        return super().do_GET()

    def do_PUT(self):
        path = urlsplit(self.path).path
        if path not in (LIST + "/" + PROJECT, LIST + "/" + EMPTY):
            return super().do_PUT()
        project = path.rsplit("/", 1)[1]
        body = json.loads(self.body())
        owner = self.headers.get("X-Persona-Manager", "")
        with lock:
            if set(body) - {"project_mode", "source_urls"} or body.get("project_mode", "full") not in ("full", "reference-only"):
                return self.send_json({"error": "Invalid project update"}, 400)
            for raw in body.get("source_urls", []):
                url = urlsplit(raw)
                if url.scheme not in ("http", "https") or not url.hostname or url.username is not None or raw.endswith("/rejected"):
                    return self.send_json({"error": "Invalid source URL"}, 400)
            if project == EMPTY:
                if set(body) != {"project_mode"}:
                    return self.send_json({"error": "Unexpected empty project update"}, 400)
                states[owner, EMPTY] = body["project_mode"]
            else:
                state = states[owner, PROJECT]
                if "project_mode" in body:
                    state["mode"] = body["project_mode"]
                if "source_urls" in body:
                    state["urls"] = body["source_urls"]
            writes.append({"manager": owner, "body": body})
            return self.send_json({"id": project})


if __name__ == "__main__":
    with mock.ThreadingHTTPServer(("127.0.0.1", mock.PORT), Handler) as server:
        server.daemon_threads = True
        mock.ENDPOINTS.append(mock.endpoint("127.0.0.1", mock.PORT, "http", "lan"))
        server.serve_forever()
