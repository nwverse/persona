"""The To-dos view as a hub with the redesign's stage 1 serves it (#10226, #10465; proposal
docs/design/proposals/todos-redesign.md 3.3, 3.4, 3.7 and 6), for mock_hub.py's MOCK_TODO_VIEW: the design prototype's
to-dos (T01 to T14 of docs/design/prototypes/todos/data/todos.js) as wire rows, each with its `view`, so an app's
screenshot lines up with the V1 frame it draws.

MOCK_TODO_VIEW=1      features todo_view and todo_places. GET /api/v1/todos: every row with `view`, the list's
                      `counts`, ETag W/"t1-…" and 304 for it; GET /api/v1/todos/{id} → {"todo": row + view}; GET
                      /api/v1/todos/places → the places and the counts; PUT / DELETE …/someday and PUT …/restored as
                      the hub answers them. Today's writes (POST, PATCH, DELETE, …/seen, …/retry) stay mock_hub.py's,
                      and the views follow them.
MOCK_TODO_VIEW=old    a hub from before the view: the same to-dos with neither `view` nor `counts`, 405 for the item
                      and places routes (PATCH and DELETE /api/v1/todos/{id} match their path, as
                      docs/protocol/protocol.md says), 404 for someday and restored.
MOCK_TODO_VIEW=late   as 1, except the hello names todo_view and todo_places only from the second connection on, as a
                      hub whose manager reconnects after it: once an app has connected, the mock closes every socket
                      three times, MOCK_TODO_VIEW_LATE_SECONDS (default 5) apart, as a restarting hub does, so the
                      hellos read: without, with, without, with. The routes answer as under 1 throughout.
MOCK_TODO_MANAGERS=2  the Weather App's to-dos are the second manager's (the first other one the roster lists, which
                      has the MacBook where their runs are): every row is stamped with its manager, the counts are
                      added up and the places merged as the hub merges them (server/internal/hub/todo_view.go).
GET /mock/churn?todos=1[&n=20&ms=3000]  n times, ms apart, the newsletter's working line moves on and `todos` is
                      pushed, for measuring a push's cost on the list (/mock/churned counts the rounds).
GET /mock/todo-view   the mode, whether the hello names the view now, the late phase and the managers.

Time: the moment the mock starts plays the prototype's now (Wednesday 18 November 2026, 10:14), and every time is
placed from it as the prototype places it, so a row the prototype shows as 16 minutes old is 16 minutes old when the
mock starts, and a due day keeps its distance in days from the start day (in this machine's zone).

The views: a row whose state comes from runs, asks or parks (T01, T04 to T07, T09, T10, T13, and T02's Widget and
Release notes steps) keeps the prototype's view while the facts that placed it are as they were (open, the same runs
linked); everything else follows the hub's rules for what the mock's writes change (spec/fixtures/todo-view/README.md):
a failed start, a closed to-do (Just finished until seen or for 24 hours, then Done), Someday, seen, a to-do with an
Agent linked (Starting) or none (To do), and a parent led by its most urgent open step (T02). Ranks keep the
prototype's order; a row that moved into a section, or is new, comes first in it, newest first, as the prototype's
Data.view re-ranks. Standard library only; every name and number is made up, as the prototype's are.
"""
import copy
import hashlib
import json
import os
import sys
import threading
import time
from datetime import date, datetime, timedelta, timezone
from urllib.parse import parse_qs, unquote, urlparse

MODE = os.environ.get("MOCK_TODO_VIEW", "")
MODES = ("1", "old", "late")
TWO_MANAGERS = os.environ.get("MOCK_TODO_MANAGERS") == "2"
LATE_SECONDS = float(os.environ.get("MOCK_TODO_VIEW_LATE_SECONDS") or 5)

PROTO_NOW = datetime(2026, 11, 18, 10, 14)  # todos.js NOW
NO_DATE = datetime(9999, 12, 31, tzinfo=timezone.utc)  # db.SomedayNoDate: Someday with no date
SECTIONS = ("needs_you", "can_wait", "working", "waiting", "todo", "just_finished", "someday", "done")
VIEW_KEYS = ("phase", "section", "reason", "line", "since", "next_change_at", "rank", "seen", "inactive", "can_wait",
             "asks", "steps")  # TodoSummary's order
WEATHER = "/Users/me/code/weather-app"
NEWSLETTER = ["writing part 2 of 3", "writing part 3 of 3", "reading it through once more"]  # /mock/churn?todos=1

HELP = ["MOCK_TODO_VIEW=1|old|late  the To-dos view from the design prototype's to-dos (#10465, "
        "scripts/mock/todo_view_mock.py): rows with view, counts and an ETag, the item and places routes, Someday and "
        "restore; old: a hub from before it (405); late: the hello names it only after a reconnect",
        "MOCK_TODO_MANAGERS=2  with MOCK_TODO_VIEW: the Weather App's to-dos are a second manager's (stamped rows, "
        "counts added up, places merged)"]

_hub = {}  # mock_hub.py's globals (install)
_state = {"phase": 0, "churn": 0, "bases": {}, "homes": {}, "order": [], "owners": {}, "legacy": {}}


def stamp(t):
    return t.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def parse(s):
    try:
        return datetime.fromisoformat(str(s).replace("Z", "+00:00")) if s else None
    except ValueError:
        return None


def line(key, **args):
    """A view's line: a key and its string arguments, an empty one left out."""
    args = {k: v for k, v in args.items() if v}
    return dict({"key": key}, **({"args": args} if args else {}))


class Clock:
    """The prototype's times from the mock's start: at(-2, 9, 12) is Monday 9:12, the start playing Wednesday 10:14."""

    def __init__(self, start):
        self.start = start

    def at(self, day, hour, minute):
        return stamp(self.start + timedelta(days=day, hours=hour - PROTO_NOW.hour, minutes=minute - PROTO_NOW.minute))

    def day(self, iso):
        n = (date.fromisoformat(iso) - PROTO_NOW.date()).days
        return (self.start.astimezone().date() + timedelta(days=n)).isoformat()


# ---------- the prototype's to-dos as the hub lists them ----------

def prototype(c, origin):
    """(rows, views, legacy asks, sessions): the to-dos in Data.todos's order, then their steps; the views of the rows
    the prototype places by runs, asks or parks; T01's decision as today's list carries it (`ask`); the runs'
    sessions."""
    rows = []

    def todo(tid, title, created, updated, **kw):
        t = {"id": tid, "title": title, "notes": "", "done_at": None, "created_at": created, "updated_at": updated,
             "spaces": [], "agents": [], "quiet": False}
        t.update(kw)
        rows.append(t)
        return t

    def sid(n):
        return "e1a7c3d5-0000-4000-8000-%012d" % (10465000 + n)

    def agent(n, title, linked):
        return {"machine_id": "macbook", "session_id": sid(n), "linked_at": linked, "released_at": None, "title": title,
                "cwd": WEATHER, "agent": "claude", "start_error": "", "source": "spawned", "via": ""}

    asked, deadline = c.at(0, 10, 11), c.at(2, 18, 0)
    options = [{"label": "Ryokan Hanami", "leads_to": "¥58k a night, 4 min from Gion, late check-in"},
               {"label": "Kaede", "leads_to": "¥52k a night"}, {"label": "Tsuki", "leads_to": "¥59k a night"}]
    todo("t01", "Plan the Kyoto trip", c.at(-2, 9, 12), c.at(0, 10, 11), origin=origin, project="kyoto",
         due_date=c.day("2027-04-03"), notes="Two of us. Ryokan near Gion, under ¥60k a night.")
    todo("t02", "Release Weather App 1.3", c.at(-1, 9, 40), c.at(0, 10, 12), origin=origin, project="weather",
         due_date=c.day("2026-11-20"))
    todo("t03", "Sort last year’s photos", c.at(0, 9, 50), c.at(0, 9, 58), origin=origin, project="home",
         done_at=c.at(0, 9, 58), start_error="Desk Mac is offline",
         notes="Group by trip, delete blurry duplicates, keep the originals.")
    todo("t13", "Renew the car insurance", c.at(-1, 16, 0), c.at(0, 9, 14), origin=origin, project="home")
    todo("t05", "Draft the school newsletter", c.at(-1, 20, 0), c.at(0, 10, 13), origin=origin, project="home",
         due_date=c.day("2026-11-19"))
    todo("t06", "Get quotes for the garden gate", c.at(0, 9, 30), c.at(0, 10, 11), origin=origin, project="home")
    todo("t07", "Book the dentist", c.at(0, 9, 40), c.at(0, 10, 6), origin=origin, project="home")
    todo("t08", "Fix widget refresh crash", c.at(-1, 11, 0), c.at(0, 8, 55), origin=origin, project="weather",
         done_at=c.at(0, 8, 55), done_by="auto", done_reason="Merged and live",
         delivery={"state": "live", "at": c.at(0, 8, 55)})
    todo("t04", "Fix login crash for SSO users", c.at(-2, 14, 5), c.at(0, 9, 30), origin=origin, project="weather",
         priority="high", agents=[agent(4, "Login fix", c.at(-2, 14, 5))], stage="review",
         pr_url="https://github.com/example/weather-app/pull/812")
    todo("t09", "Update the privacy policy page", c.at(-2, 16, 0), c.at(-1, 15, 0), origin=origin, project="weather")
    todo("t10", "Keep an eye on the app size after 1.3", c.at(-1, 17, 0), c.at(-1, 17, 5), origin=origin,
         project="weather", kind="concern")
    todo("t11", "Renew passport", c.at(0, 9, 5), c.at(0, 9, 5), due_date=c.day("2026-11-20"),
         notes="Old one expires in May. Photo booth at the post office.")
    todo("t12", "Buy groceries", c.at(-2, 18, 0), c.at(-2, 18, 30), project="home")
    todo("t14", "Learn to bake sourdough", c.at(-3, 20, 0), c.at(-3, 20, 0), someday_until=stamp(NO_DATE))
    # Steps: the to-dos whose part_of names their parent (proposal 3.4 "Steps roll up"), only where the prototype's list
    # counts them on line 3 (T01, T02, T12); T04's and T05's steps on their pages are their runs' plans, not to-dos.
    for n, (title, done_at, by) in enumerate([("Choose dates", c.at(-2, 9, 12), ""),
                                              ("Book flights", c.at(-2, 18, 40), "auto"), ("Pick a ryokan", None, ""),
                                              ("Get JR passes", None, ""), ("Plan each day", None, "")], 1):
        todo("t01-s%d" % n, title, c.at(-2, 9, 15), done_at or c.at(-2, 9, 15), part_of="t01", done_at=done_at,
             **({"done_by": by} if by else {}))
    todo("t02-widget", "Widget", c.at(-1, 9, 41), c.at(0, 10, 12), part_of="t02",
         agents=[agent(1, "Widget", c.at(0, 10, 6))])
    todo("t02-notes", "Release notes", c.at(-1, 9, 41), c.at(0, 10, 14), part_of="t02",
         agents=[agent(2, "Release notes", c.at(0, 10, 11))])
    todo("t02-shots", "Screenshots", c.at(-1, 9, 41), c.at(-1, 9, 52), part_of="t02", done_at=c.at(-1, 9, 52),
         done_by="auto", agents=[agent(3, "Screenshots", c.at(-1, 9, 40))])
    for n, title in enumerate(["Milk", "Eggs", "Miso", "Tofu"]):
        bought = c.at(-2, 18, 30) if n == 0 else None
        todo("t12-" + title.lower(), title, c.at(-2, 18, 0), bought or c.at(-2, 18, 0), part_of="t12", done_at=bought)

    widget_since = c.at(0, 10, 12)
    widget_ask = {"id": "run:macbook/%s@%s" % (sid(1), widget_since), "kind": "plan", "home": "run",
                  "run": {"machine_id": "macbook", "session_id": sid(1), "title": "Widget"},
                  "text": "Plan awaiting approval"}
    views = {
        "t01": {"phase": "needs_you", "section": "needs_you", "reason": "owner_decides",
                "line": line("decision", text="Which ryokan?", recommend="Ryokan Hanami"), "since": asked,
                "asks": [{"id": "todo:decision@" + asked, "kind": "decision", "home": "todo", "round": asked,
                          "text": "Which ryokan?", "recommend": "Ryokan Hanami", "options": options,
                          "default_at": deadline}]},
        "t13": {"phase": "needs_you", "section": "can_wait", "reason": "awaits_reply", "can_wait": True,
                "line": line("awaits_reply", run="Persona", text="Do you want roadside cover? It adds ¥4,800 a year."),
                "since": c.at(0, 9, 14)},
        "t05": {"phase": "working", "section": "working", "reason": "persona_working",
                "line": line("persona_working", text=NEWSLETTER[0]), "since": c.at(0, 10, 2)},
        "t06": {"phase": "working", "section": "working", "reason": "persona_working",
                "line": line("persona_working", text="comparing 2 quotes"), "since": c.at(0, 10, 11)},
        "t07": {"phase": "ready", "section": "just_finished", "reason": "finished_unseen", "line": line("finished"),
                "since": c.at(0, 10, 6)},
        "t04": {"phase": "waiting", "section": "waiting", "reason": "pr_open", "line": line("pr_open")},
        "t09": {"phase": "waiting", "section": "waiting", "reason": "someone_else",
                "line": line("parked", reason="A reviewer to merge"), "since": c.at(-1, 15, 0)},
        "t10": {"phase": "waiting", "section": "waiting", "reason": "parked",
                "line": line("parked", reason="Watching the next 3 builds", until=c.at(2, 9, 0)),
                "since": c.at(-1, 17, 5)},
        "t02-widget": {"phase": "needs_you", "section": "needs_you", "reason": "agent_asks",
                       "line": line("agent_asks", run="Widget", text="Plan awaiting approval"), "since": widget_since,
                       "asks": [widget_ask]},
        "t02-notes": {"phase": "working", "section": "working", "reason": "agent_running",
                      "line": line("working", run="Release notes", text="drafting What’s New"),
                      "since": c.at(0, 10, 11)},
    }
    legacy = {"t01": {"outcome": "decide", "verdict": "🤔 Needs your decision: Which ryokan?",
                      "recommend": "Ryokan Hanami", "asked_at": asked, "question": "Which ryokan?", "options": options,
                      "deadline": deadline, "default": "taken",
                      "responses": ["decide_yourself", "not_what_i_asked", "explain_first"]}}
    # The runs' sessions on the MacBook: (id, title, status, state, last activity, state since, last text, cards)
    sessions = [(sid(1), "Widget", "blocked", "needs_you", widget_since, widget_since,
                 "Here is the plan: a timeline provider, small and medium layouts and snapshot tests. About 2 h.", 1),
                (sid(2), "Release notes", "working", "working", c.at(0, 10, 14), c.at(0, 10, 11),
                 "Drafting What’s New for 1.3.", 0),
                (sid(3), "Screenshots", "idle", "idle", c.at(-1, 9, 52), c.at(-1, 9, 52),
                 "Made 6 screenshots for the App Store page.", 0),
                (sid(4), "Login fix", "idle", "idle", c.at(-2, 14, 43), c.at(-2, 14, 43),
                 "Found it: the token refresh races the SSO redirect. The change is ready to review.", 0)]
    return rows, views, legacy, sessions


# ---------- the view of one to-do ----------

def closed(t):
    return bool(t.get("done_at")) and not t.get("start_error")


def live_agents(t):
    return [a for a in t.get("agents") or [] if not a.get("released_at") and not a.get("dismissed_at")]


def facts_key(t):
    """What placed a row the prototype places: open or not, a start error, the runs linked."""
    return closed(t), bool(t.get("start_error")), tuple(sorted(a["session_id"] for a in live_agents(t)))


def failed_start(t, done, now):
    """Row 1 for 48 hours from the failure (a retry ask only with an origin: retryTask refuses the others), then a To
    do that goes Inactive."""
    end = done + timedelta(hours=48)
    if now < end:
        v = {"phase": "needs_you", "section": "needs_you", "reason": "failed_start",
             "line": line("failed_start", error=t["start_error"]), "since": stamp(done), "next_change_at": stamp(end)}
        if t.get("origin"):
            v["asks"] = [{"id": "todo:retry@" + t["done_at"], "kind": "retry", "home": "todo"}]
        return v
    last = max(x for x in (done, parse(t.get("updated_at")), parse(t.get("restored_at"))) if x)
    v = {"phase": "todo", "section": "todo", "reason": "rest"}
    if now >= last + timedelta(hours=48):
        v["inactive"] = stamp(last)
    else:
        v["next_change_at"] = stamp(last + timedelta(hours=48))
    return v


def closed_view(t, done, now):
    """Rows 6 and 7: Just finished until seen or for 24 hours, then Done."""
    sup = t.get("supervision") or {}
    if t.get("cancelled_at") or (t.get("done_by") == "review" and sup.get("state") == "cancelled"):
        reason, said = "canceled", line("canceled", reason=(t.get("cancelled_reason") or "").split("\n")[0])
    elif t.get("kind") == "concern":
        reason, said = "reported", line("reported")
    else:
        by = t.get("done_by") if t.get("done_by") in ("auto", "review") else ""
        reason, said = "completed", line("completed", by=by)
    seen_at = parse(t.get("seen_at"))
    seen = bool(seen_at and seen_at >= done)
    end = done + timedelta(hours=24)
    if seen or now >= end:
        return dict({"phase": "done", "section": "done", "reason": reason, "line": said, "since": stamp(done)},
                    **({"seen": True} if seen else {}))
    return {"phase": "done", "section": "just_finished", "reason": "closed_unseen", "line": said, "since": stamp(done),
            "next_change_at": stamp(end)}


def finished(t, v, now):
    """Row 9 with seen: finished_recent for 24 hours after the owner saw it, then To do (row 11)."""
    seen_at, since = parse(t.get("seen_at")), parse(v.get("since"))
    if not seen_at or (since and seen_at < since):
        return v
    end = seen_at + timedelta(hours=24)
    if now < end:
        return dict(v, reason="finished_recent", seen=True, next_change_at=stamp(end))
    return {"phase": "todo", "section": "todo", "reason": "finished_seen",
            "line": line("finished_seen", at=v.get("since")), "since": v.get("since")}


def someday(t, v, now):
    """Row 12: put off, unless a row above it holds the to-do."""
    until = parse(t.get("someday_until"))
    if not until or until <= now or v["phase"] != "todo":
        return v
    dated = until < NO_DATE
    return dict({"phase": "someday", "section": "someday", "reason": "deferred",
                 "line": line("someday", until=stamp(until) if dated else "")},
                **({"next_change_at": stamp(until)} if dated else {}))


def own_view(t, now):
    """The to-do's view from its own facts, before its steps."""
    done = parse(t.get("done_at"))
    if done and t.get("start_error"):
        return someday(t, failed_start(t, done, now), now)
    if done:
        return closed_view(t, done, now)
    base = _state["bases"].get(t["id"])
    if base and facts_key(t) == base["facts"]:
        v = copy.deepcopy(base["view"])
        if t["id"] == "t05":
            v["line"] = line("persona_working", text=NEWSLETTER[_state["churn"] % len(NEWSLETTER)])
        if v["phase"] == "ready":
            v = finished(t, v, now)
    elif live_agents(t):
        a = max(live_agents(t), key=lambda x: x.get("linked_at") or "")
        v = {"phase": "working", "section": "working", "reason": "starting",
             "line": line("starting", run=a.get("title")), "since": a.get("linked_at")}
    else:
        v = {"phase": "todo", "section": "todo", "reason": "rest"}
    return someday(t, v, now)


LEADS = ("needs_you", "working", "waiting", "can_wait")


def roll_up(v, steps):
    """A parent led by its most urgent open step when that is ahead of its own section (proposal 3.4 "Steps roll
    up"): the step's state and line (with `step`), its own and its steps' asks, the first window of either."""
    at = {s: i for i, s in enumerate(SECTIONS)}

    def ahead(sv):
        if sv["section"] == "can_wait":
            return v["section"] in ("todo", "just_finished", "someday")
        return sv["section"] in LEADS and at[sv["section"]] < at[v["section"]]

    lead = min(((s, sv) for s, sv in steps if ahead(sv)), default=None,
               key=lambda x: (LEADS.index(x[1]["section"]), x[1].get("since") or "", x[0]["created_at"],
                              x[0]["id"]))
    out = dict(v)
    if lead:
        s, sv = lead
        out = {k: sv[k] for k in ("phase", "section", "reason", "since", "can_wait") if k in sv}
        if "line" in sv:
            out["line"] = line(sv["line"]["key"], **dict(sv["line"].get("args", {}), step=s["title"]))
    windows = [x for x in [v.get("next_change_at")] + [sv.get("next_change_at") for _, sv in steps] if x]
    out.pop("next_change_at", None)
    if windows:
        out["next_change_at"] = min(windows, key=parse)
    out.pop("asks", None)
    if out["section"] in ("needs_you", "can_wait"):
        parts = [(None, v)] + list(steps)
        asking = [(s, sv) for s, sv in parts if sv.get("asks") and sv["section"] in ("needs_you", "can_wait")]
        first = [p for p in asking if p[0] is (lead[0] if lead else None)]
        rest = sorted((p for p in asking if p not in first),
                      key=lambda p: (p[1]["section"] != "needs_you", p[1].get("since") or "", p[0] is not None))
        asks = []
        for s, sv in first + rest:
            asks += [a if s is None else dict(a, id="step:%s/%s" % (s["id"], a["id"])) for a in sv["asks"]]
        out["asks"] = asks
    return out


def place(rows, now):
    """Every row's view at now, steps rolled up into their parents; ranks are rank's."""
    own = {t["id"]: own_view(t, now) for t in rows}
    kids = {}
    for t in rows:
        if t.get("part_of") and t["part_of"] != t["id"] and t["part_of"] in own:
            kids.setdefault(t["part_of"], []).append(t)
    views = {}
    for t in rows:
        v, steps = own[t["id"]], kids.get(t["id"], [])
        if steps and v["phase"] != "done":
            v = roll_up(v, [(s, own[s["id"]]) for s in steps if not closed(s)])
        if steps:
            v = dict(v, steps={"done": sum(1 for s in steps if closed(s)), "total": len(steps)})
        views[t["id"]] = v
    return views


def rank(rows, views):
    """Ranks within each section of one manager's list: the prototype's order, a row that moved there (or is new)
    first, newest first."""
    order = {tid: i for i, tid in enumerate(_state["order"])}
    by = {}
    for t in rows:
        by.setdefault(views[t["id"]]["section"], []).append(t)
    for section, ts in by.items():
        def key(t):
            if _state["homes"].get(t["id"]) == section and t["id"] in order:
                return 0, 0.0, order[t["id"]], ""
            moment = parse(views[t["id"]].get("since") or t.get("done_at") or t.get("created_at"))
            return -1, -(moment.timestamp() if moment else 0.0), 0, t["id"]
        for i, t in enumerate(sorted(ts, key=key)):
            views[t["id"]]["rank"] = i
    for tid, v in views.items():
        views[tid] = {k: v[k] for k in VIEW_KEYS if k in v}
    return views


# ---------- counts and places (server/internal/assistant/todo/projection/list.go) ----------

def carried(t, open_ids):
    return bool(t.get("part_of")) and t["part_of"] != t["id"] and t["part_of"] in open_ids


def counts(rows, views):
    out = {"open": 0, "needs_you": 0, "in_progress": 0, "sections": {s: 0 for s in sorted(SECTIONS)}}
    open_ids = {t["id"] for t in rows if views[t["id"]]["phase"] != "done"}
    for t in rows:
        if carried(t, open_ids):
            continue
        v = views[t["id"]]
        out["sections"][v["section"]] = out["sections"].get(v["section"], 0) + 1
        out["open"] += v["phase"] != "done" and v["section"] != "someday"
        out["needs_you"] += v["section"] == "needs_you" and not t.get("quiet")
        out["in_progress"] += v["section"] in ("working", "waiting")
    return out


def places(rows, views, total):
    titles = {p.get("id"): p.get("name") for p in _hub["KB"].get("projects", [])}
    out = [{"id": "all", "kind": "all", "title": "", "needs_you": total["needs_you"]},
           {"id": "today", "kind": "today", "title": "", "needs_you": 0},
           {"id": "upcoming", "kind": "upcoming", "title": "", "needs_you": 0}]
    open_ids = {t["id"] for t in rows if views[t["id"]]["phase"] != "done"}
    need = {}
    for t in rows:
        v = views[t["id"]]
        if carried(t, open_ids) or not t.get("project") or v["phase"] == "done" or v["section"] == "someday":
            continue
        need[t["project"]] = need.get(t["project"], 0) + (v["section"] == "needs_you" and not t.get("quiet"))
    for slug in sorted(need):
        out.append({"id": "project:" + slug, "kind": "project", "title": titles.get(slug) or slug,
                    "project": slug, "needs_you": need[slug]})
    return out


def add_up(objs):
    """The hub's sumMembers: integers added up member by member, objects key by key."""
    out = {}
    for o in objs:
        for k, v in o.items():
            if k in out:
                continue
            if isinstance(v, dict):
                out[k] = add_up([x[k] for x in objs if isinstance(x.get(k), dict)])
            elif isinstance(v, int) and not isinstance(v, bool):
                out[k] = sum(x.get(k, 0) for x in objs if isinstance(x.get(k), int))
            else:
                out[k] = v
    return out


# ---------- the answers ----------

def managers():
    """The managers whose to-dos the list merges, the default's first; one unless MOCK_TODO_MANAGERS=2."""
    roster = _hub["MANAGERS"]
    first = roster["default"]
    others = sorted(m["id"] for m in roster["list"]
                    if m["id"] != first and m.get("online") and not m.get("installing"))
    return [first] + others[:1] if TWO_MANAGERS else [first]


def owner(t, ids):
    if len(ids) < 2:
        return ids[0]
    tid = t.get("part_of") or t["id"]
    return ids[1] if _state["owners"].get(tid) == "weather" else ids[0]


def answers(now):
    """Each manager's answer as it sends it: [(manager, rows with views, counts, places)]."""
    rows = sorted(_hub["sorted_todos"](), key=lambda t: t["id"])
    rows.sort(key=lambda t: t.get("created_at") or "", reverse=True)  # newest first; equal times by id
    ids = managers()
    out = []
    for mid in ids:
        mine = [t for t in rows if owner(t, ids) == mid]
        views = rank(mine, place(mine, now))
        total = counts(mine, views)
        for t in mine:
            base = _state["bases"].get(t["id"])
            legacy = _state["legacy"].get(t["id"])
            if legacy and "ask" not in t and base and facts_key(t) == base["facts"]:
                t["ask"] = copy.deepcopy(legacy)
            if MODE != "old":
                t["view"] = views[t["id"]]
        out.append((mid, mine, total, places(mine, views, total)))
    return out


def stamped(mid, row):
    return dict({"manager": mid}, **row) if _hub["_stamps"]() else row


def list_answer(now):
    got = answers(now)
    obj = {"todos": [stamped(mid, t) for mid, rows, _, _ in got for t in rows]}
    if MODE != "old":
        obj["counts"] = add_up([c for _, _, c, _ in got])
    return obj


def places_answer(now):
    """GET /api/v1/todos/places as the hub merges it (mergeTodoPlaces): each fixed place once, its needs_you added
    up, then every manager's projects stamped with it, the default's first; the counts added up."""
    got = answers(now)
    fixed, own = [], []
    for mid, _, _, pl in got:
        for p in pl:
            if p["kind"] == "project":
                own.append(dict({"manager": mid}, **p))
                continue
            same = next((f for f in fixed if f["id"] == p["id"]), None)
            if same:
                same["needs_you"] += p["needs_you"]
            else:
                fixed.append(dict(p))
    return {"places": fixed + own, "counts": add_up([c for _, _, c, _ in got])}


def tag_matches(values, tag):
    opaque = tag[2:] if tag.startswith("W/") else tag
    for v in values:
        for t in v.split(","):
            t = t.strip()
            if t == "*" or (t[2:] if t.startswith("W/") else t) == opaque:
                return True
    return False


def send_list(h):
    """GET /api/v1/todos with the hub's validator (#9590): ETag W/"t1-<128 bits of the body's SHA-256>", 304 for
    it."""
    body = json.dumps(list_answer(datetime.now(timezone.utc)), ensure_ascii=False).encode()
    tag = 'W/"t1-' + hashlib.sha256(body).hexdigest()[:32] + '"'
    if tag_matches(h.headers.get_all("If-None-Match") or [], tag):
        h.send_response(304)
        h.send_header("ETag", tag)
        h.end_headers()
        return True
    h.send_response(200)
    h.send_header("Content-Type", "application/json; charset=utf-8")
    h.send_header("Content-Length", str(len(body)))
    h.send_header("Cache-Control", "no-store")
    h.send_header("ETag", tag)
    h.end_headers()
    h.wfile.write(body)
    return True


def mark(h, tid, what):
    """PUT / DELETE …/someday and PUT …/restored (server/internal/assistant/todo_marks.go): {"todo": Todo}; neither
    is an edit, so updated_at stays."""
    if _hub["is_read_only"](h):
        return h.send_json({"error": "read-only role cannot modify todos"}, 403)
    t = next((x for x in _hub["TODOS"] if x["id"] == tid), None)
    if not t:
        return h.send_json({"error": "todo not found", "code": "todo_not_found"}, 404)
    now = datetime.now(timezone.utc)
    if what == "restored":
        t["restored_at"] = stamp(now)
    elif h.command == "DELETE":
        t.pop("someday_until", None)
    else:
        try:
            raw = json.loads(getattr(h, "last_body", b"") or b"{}").get("until")
        except (ValueError, AttributeError):
            return h.send_json({"error": "invalid body"}, 400)
        until = parse(raw) if raw else NO_DATE
        if until is None or until.tzinfo is None or until <= now or until > NO_DATE:
            return h.send_json({"error": "Pick a time ahead, no later than the year 9999",
                                "code": "someday_until"}, 400)
        if closed(t):
            return h.send_json({"error": "Only an open to-do can be put off to Someday", "code": "todo_closed"}, 409)
        t["someday_until"] = stamp(until)
    _hub["push_todos"]()
    return h.send_json({"todo": _hub["format_todo"](t)})


def route(h, method, path):
    """The routes above; False for anything else, which mock_hub.py answers."""
    if method == "GET" and path == "/mock/todo-view":
        return h.send_json({"mode": MODE, "listed": listed(), "phase": _state["phase"], "managers": managers(),
                            "churn": _state["churn"]}) or True
    query = parse_qs(urlparse(h.path).query) if path == "/mock/churn" else {}
    if method == "GET" and query.get("todos") == ["1"]:
        n, ms = int(query.get("n", ["20"])[0]), int(query.get("ms", ["3000"])[0])
        _hub["CHURN"]["rounds"] = 0
        threading.Thread(target=churn, args=(n, ms), daemon=True).start()
        return h.send_json({"ok": True}) or True
    if method == "GET" and path == "/mock/reset":
        _state["churn"] = 0
        return False  # and mock_hub.py's reset puts the to-dos back
    parts = path.split("/")  # ['', 'api', 'v1', 'todos', id, what]
    if parts[:4] != ["", "api", "v1", "todos"]:
        return False
    now = datetime.now(timezone.utc)
    if len(parts) == 4:
        return method == "GET" and send_list(h)
    tid = unquote(parts[4])
    if len(parts) == 5 and method == "GET":
        if MODE == "old":  # an older hub: the path matches its PATCH and DELETE routes only
            return h.send_json({"error": "method not allowed"}, 405) or True
        if tid == "places":
            return h.send_json(places_answer(now)) or True
        for _, rows, _, _ in answers(now):
            for t in rows:
                if t["id"] == tid:
                    return h.send_json({"todo": t}) or True
        return h.send_json({"error": "todo not found", "code": "todo_not_found"}, 404) or True
    if len(parts) == 5 and method == "PATCH":
        try:
            reopen = json.loads(getattr(h, "last_body", b"") or b"{}").get("done") is False
        except (ValueError, AttributeError):
            reopen = False
        t = next((x for x in _hub["TODOS"] if x["id"] == tid), None)
        if reopen and t:
            t.pop("someday_until", None)  # reopening takes it out of Someday (db.TodoEdit.Apply)
        return False
    if len(parts) == 6 and (parts[5], method) in (("someday", "PUT"), ("someday", "DELETE"), ("restored", "PUT")):
        if MODE == "old":
            return h.send_json({"error": "not found"}, 404) or True
        return mark(h, tid, parts[5]) or True
    return False


def churn(n, ms):
    for _ in range(n):
        time.sleep(ms / 1000)
        _state["churn"] += 1
        _hub["CHURN"]["rounds"] += 1
        _hub["push_todos"]()


def listed():
    """Whether the hub's hello names todo_view and todo_places now."""
    return MODE == "1" or (MODE == "late" and _state["phase"] in (1, 3))


def late():
    """MOCK_TODO_VIEW=late: once an app has connected, close every socket three times, so its hellos alternate."""
    while not _hub["CLIENTS"]:
        time.sleep(0.1)
    for phase in (1, 2, 3):
        time.sleep(LATE_SECONDS)
        with _hub["FAULT_LOCK"]:
            _state["phase"] = phase
            clients = list(_hub["CLIENTS"].values())
        for c in clients:
            try:
                c["close"]()
            except Exception:  # gone meanwhile
                pass


def install(hub):
    """With MOCK_TODO_VIEW, put the prototype's to-dos, their runs' sessions and their projects in place of the mock's
    own, and the view's features in the hello; mock_hub.py calls it once its other switches have run. False without
    it."""
    global _hub
    if not MODE:
        return False
    if MODE not in MODES:
        sys.exit("mock hub: MOCK_TODO_VIEW must be 1, old or late, not %r" % MODE)
    _hub = hub
    c = Clock(hub["NOW"].replace(second=0, microsecond=0))
    rows, views, legacy, sessions = prototype(c, hub.get("ASK1", ""))
    hub["TODOS_INIT"][:] = rows
    hub["TODOS"][:] = copy.deepcopy(rows)
    for n, (sid, title, status, state, updated, since, last, pending) in enumerate(sessions, 46):
        # in the weather-app space the mock's MacBook already has, a tab each
        hub["SESSIONS"].append(hub["sess"]("macbook", sid, title, WEATHER, status, state, "weather-app", 7, title, n, 0,
                                           last, pending=pending, updated_at=updated, state_since=since))
    hub["KB"].setdefault("projects", []).extend(
        {"kind": "projects", "id": pid, "name": name, "one_line": line_, "importance": "P2", "relevance": 50,
         "updated": c.at(0, 9, 0), "extra": "short · active"}
        for pid, name, line_ in (("kyoto", "Kyoto trip", "Apr 3–9, two of us, a ryokan near Gion"),
                                 ("weather", "Weather App", "1.3 ships Friday with the new forecast view"),
                                 ("home", "Home", "The house, the car and the school")))
    by_id = {t["id"]: t for t in rows}
    _state.update(order=[t["id"] for t in rows], legacy=legacy,
                  owners={t["id"]: t.get("project") for t in rows if not t.get("part_of")},
                  bases={tid: {"view": v, "facts": facts_key(by_id[tid])} for tid, v in views.items()})
    _state["homes"] = {tid: v["section"] for tid, v in place(rows, c.start).items()}

    before = hub["features"]

    def features():
        f = before()
        return f + [x for x in ("todo_view", "todo_places") if x not in f] if listed() else f

    hub["features"] = features
    if MODE == "late":
        threading.Thread(target=late, daemon=True).start()
    return True
