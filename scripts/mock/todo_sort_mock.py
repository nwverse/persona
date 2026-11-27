"""Sorting to-dos into knowledge-base projects in the mock hub (server/internal/assistant/todo_sort.go and
todo_sort_batches.go, #13026), a domain of scripts/mock/mock_domains.py. With MOCK_TODO_SORT=1 the to-dos and the
knowledge base's projects are replaced by a small fictional set (Garden app, Q4 launch, Docs site), most of the to-dos
in no project, and the hub answers POST /api/v1/todo-sort/suggest with fixed suggestions, POST …/apply as one batch,
GET …/batches and POST …/batches/{id}/undo (skipping a to-do moved since). scripts/test/web_todo_sort_e2e.py drives it.
"""
import os
import secrets

import mock_domains

HELP = "MOCK_TODO_SORT=1  fictional to-dos in no project, three projects and the sort routes (#13026)"
HUB = None
MODE = os.environ.get("MOCK_TODO_SORT", "")
BATCHES = []  # newest last

PROJECTS = [
    ("garden-app", "Garden app", "A small app that reminds you to water the plants", "long · active"),
    ("q4-launch", "Q4 launch", "Everything for the fourth-quarter release", "short · active"),
    ("docs-site", "Docs site", "The public documentation site", "long · active"),
]

# id, title, project, suggestion (project | "temporary" | ""), confidence, reason
TODOS = [
    ("todo_s1", "Water the Garden app seedling screen", "", "garden-app", 0.92, "Its title names Garden app"),
    ("todo_s2", "Draft the launch plan checklist", "", "q4-launch", 0.85, "Its title names launch plan, an alias of Q4 launch"),
    ("todo_s3", "Fix broken links in the search page", "", "docs-site", 0.8, "Its Agent works in ~/src/docs-site, a folder of Docs site"),
    ("todo_s4", "Buy printer paper", "", "temporary", 0.88, "A one-off errand"),
    ("todo_s5", "Write the release notes", "", "q4-launch", 0.66, "The notes mention the release"),
    ("todo_s6", "Try the new watering icon set", "", "garden-app", 0.45, "Icons might be for the garden app"),
    ("todo_s7", "Think about the team offsite", "", "", 0, ""),
    ("todo_s8", "Add the plant photo upload", "garden-app", "", 0, ""),
    ("todo_s9", "Review the style guide", "docs-site", "", 0, ""),
]


def todo(tid, title, project, i):
    return {"id": tid, "title": title, "notes": "", "done_at": None, "created_at": HUB.ago(hours=i + 1),
            "updated_at": HUB.ago(minutes=i * 7 + 3), "spaces": [], "agents": [], **({"project": project} if project else {})}


def reset():
    if not MODE:
        return
    BATCHES.clear()
    HUB.TODOS[:] = [todo(tid, title, project, i) for i, (tid, title, project, *_rest) in enumerate(TODOS)]
    HUB.KB["projects"][:] = [{"kind": "projects", "id": slug, "name": name, "one_line": line, "importance": "P2", "relevance": 60,
                              "tags": [], "updated": HUB.ago(days=i), "extra": extra} for i, (slug, name, line, extra) in enumerate(PROJECTS)]


def unsorted(t):
    return not t.get("done_at") and not t.get("project") and not t.get("temporary") and t.get("kind") != "concern"


def find(tid):
    return next((t for t in HUB.TODOS if t["id"] == tid), None)


def suggest(h, req):
    names = {slug: name for slug, name, *_ in PROJECTS}
    rules = {tid: (s, c, r) for tid, _t, _p, s, c, r in TODOS}
    items = []
    for t in HUB.TODOS:
        if not unsorted(t):
            continue
        s, c, r = rules.get(t["id"], ("", 0, ""))
        it = {"todo": t["id"], "title": t["title"], "project": "", "confidence": c, "preselect": bool(s) and c >= 0.6}
        if s == "temporary":
            it.update(temporary=True, reason=r, by="model")
        elif s:
            it.update(project=s, project_name=names[s], reason=r, by="title")
        else:
            it["confidence"] = 0
        items.append(it)
    items.sort(key=lambda it: (not (it["project"] or it.get("temporary")), -it["confidence"]))
    return h.send_json({"items": items, "projects": [{"slug": s, "name": n} for s, n, *_ in PROJECTS], "model": "used",
                        "unsorted": len(items), "sorted": sum(1 for t in HUB.TODOS if not t.get("done_at") and t.get("project"))})


def place(t):
    return {"project": t.get("project", ""), **({"temporary": True} if t.get("temporary") else {})}


def put(t, p):
    t.pop("project", None)
    t.pop("temporary", None)
    if p.get("project"):
        t["project"] = p["project"]
    if p.get("temporary"):
        t["temporary"] = True
    t["updated_at"] = HUB.ago()


def apply(h, req):
    try:
        items = req.json().get("items") or []
    except ValueError:
        return h.send_json({"error": "bad json"}, 400)
    slugs = {s for s, *_ in PROJECTS}
    plan = []
    for i, m in enumerate(items):
        t = find(m.get("id", ""))
        after = {"project": "", "temporary": True} if m.get("temporary") else {"project": m.get("project", "")}
        if t is None or (after["project"] and after["project"] not in slugs):
            return h.send_json({"error": f"item {i + 1}: no such to-do or project"}, 400)
        if place(t) != {k: v for k, v in after.items() if v or k == "project"}:
            plan.append((t, place(t), after))
    res = {"batch": "", "moved": len(plan), "unchanged": len(items) - len(plan)}
    if plan:
        batch = {"id": "tsb_" + secrets.token_hex(6), "at": HUB.ago(), "by": "user", "kind": "sort", "summary": f"moved {len(plan)}",
                 "items": [{"todo": t["id"], "before": b, "after": a} for t, b, a in plan]}
        for t, _b, a in plan:
            put(t, a)
        BATCHES.append(batch)
        res["batch"] = batch["id"]
    return h.send_json(res)


def batches(h, req):
    out = []
    for b in reversed(BATCHES[-20:]):
        out.append({**b, "titles": {it["todo"]: (find(it["todo"]) or {}).get("title", "") for it in b["items"]}})
    return h.send_json({"batches": out})


def undo(h, req):
    b = next((b for b in BATCHES if b["id"] == req.match.group(1)), None)
    if b is None:
        return h.send_json({"error": "There's no such change to undo", "code": "sort_batch_missing"}, 404)
    if b.get("undone_at"):
        return h.send_json({"error": "That change was undone already", "code": "sort_batch_undone"}, 409)
    b["undone_at"] = HUB.ago()
    outcomes, undone = [], 0
    for it in reversed(b["items"]):
        t = find(it["todo"])
        want = {k: v for k, v in it["after"].items() if v or k == "project"}
        if t is None:
            out = "skipped: gone"
        elif place(t) != want:
            out = "skipped: changed since"
        else:
            put(t, it["before"])
            out, undone = "done", undone + 1
        outcomes.insert(0, {"todo": it["todo"], "title": (t or {}).get("title", ""), "outcome": out})
    return h.send_json({"batch": b["id"], "undone": undone, "skipped": len(outcomes) - undone, "outcomes": outcomes})


def register(d):
    global HUB
    HUB = d.hub
    d.help(HELP)
    if not MODE:
        return
    d.route("POST", r"/api/v1/todo-sort/suggest", suggest)
    d.route("POST", r"/api/v1/todo-sort/apply", apply)
    d.route("GET", r"/api/v1/todo-sort/batches", batches)
    d.route("POST", r"/api/v1/todo-sort/batches/([^/]+)/undo", undo)
    d.on_reset(reset)
    reset()
