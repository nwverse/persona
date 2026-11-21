"""Filing sessions under a direction and reading one direction (#11391, #11516): the hub's `direction_sessions`.

MOCK_DIRECTION_SESSIONS=1 (with MOCK_LEADERS=1) lists the feature, answers
`PUT /api/v1/assistant/sessions/{mid}/{sid}/direction` and `GET /api/v1/assistant/directions/{id}`, and adds the live
filed sessions to the tree (`sessions`), as docs/protocol/protocol.md describes them. The first leader ("Persona 产品")
starts with a session of every standing (docs/assistant/unified-entry.md "Where a session stands"): two chats and an
Agent filed by the owner, an Agent its leader opened, one stopped on a connected computer (`offline`), and one each
`computer_offline`, `list_unknown`, `not_listed` and `computer_removed`. Two Agents join SESSIONS for it.
MOCK_DIRECTION_SESSIONS=missing lists the feature but answers the routes 404 without their codes, as a manager that
reconnected at an older version does. Unset: a hub without the feature (the routes are not there).

GET /mock/direction_sessions?… changes how it answers from now on (each optional; GET /mock/reset puts all back):
  routes=missing|on       the routes answer 404 without their codes / as above
  feature=off|on          leave `direction_sessions` out of the hello and /api/v1/health / list it
  refuse=<code>           the next PUT is refused with that code (full_access_required, forbidden, session_not_found,
                          direction_full, direction_supervised_task, direction_unavailable, …), nothing changed
  read=fail|gone|ok       the single read answers 503 direction_unavailable / 404 direction_not_found / as it is
  delay=<seconds>         the single read waits that long first (the page draws from the tree meanwhile)
  sessions_unavailable=1  the single read says the placed sessions could not be read
  ended=<id>|""           the single read of that direction has `ended_at`
The answer is the book as it stands: {"book", "options"}. A lost answer is POST /mock/fault {"fault":"lost_receipt"}.
"""
import json
import os
import threading
import time
from datetime import datetime, timedelta, timezone

MODE = os.environ.get("MOCK_DIRECTION_SESSIONS", "")
FEATURE = "direction_sessions"
MAX_PER_DIRECTION = 50

# The hub's sentences for its refusals, in the request's language (server/internal/i18n/locales, shortened).
SENTENCES = {  # i18n-ignore: the hub's own refusal sentences, mock data
    "full_access_required": ("Only a device with full access can do this.", "只有完全访问权限的设备才能这样做。",
                             "フルアクセスのデバイスだけがこれを行えます。"),
    "forbidden": ("This device can't make changes.", "这台设备不能做更改。", "このデバイスでは変更できません。"),
    "session_not_found": ("That session isn't on its computer's list.", "这个会话不在它电脑的列表里。",
                          "そのセッションはコンピューターの一覧にありません。"),
    "machine_not_found": ("That computer isn't one of yours.", "这台电脑不是你的电脑。", "そのコンピューターはあなたのものではありません。"),
    "direction_not_found": ("That no longer exists.", "它已经不在了。", "それはもうありません。"),
    "direction_ended": ("That has ended.", "它已经结束了。", "それは終了しています。"),
    "direction_full": ("It already holds 50 sessions.", "那里已经放了 50 个会话。", "すでに 50 件のセッションがあります。"),
    "direction_supervised_task": ("This Agent follows its task, so it stays with it.", "这个 Agent 跟着它的任务走，不能单独移动。",
                                  "この Agent はタスクに従うため、移動できません。"),
    "direction_main_conversation": ("The main chat stays where it is.", "主对话不能移动。", "メインチャットは移動できません。"),
    "direction_leader_conversation": ("A pinned chat can't be put under another.", "常驻对话不能放到别处。",
                                      "常駐のチャットは他に入れられません。"),
    "direction_unavailable": ("Couldn't save that right now. Try again later.", "现在保存不了，请稍后再试。",
                              "いまは保存できません。あとでもう一度お試しください。"),
}
STATUS = {"full_access_required": 403, "forbidden": 403, "session_not_found": 404, "machine_not_found": 404,
          "direction_not_found": 404, "direction_unavailable": 503}


def _iso(t):
    return t.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _now():
    return datetime.now(timezone.utc)


def register(d):
    d.help("MOCK_DIRECTION_SESSIONS=1|missing  filing sessions under a direction and the single read (#11516; "
           "with MOCK_LEADERS=1); missing: listed, but the routes answer 404 without their codes; "
           "GET /mock/direction_sessions?routes=&feature=&refuse=&read=&delay=&sessions_unavailable=&ended=")
    if MODE not in ("1", "missing"):
        # A hub without the feature has neither route (the mock's catch-all would answer the write 200).
        def absent(h, req):
            if req.match.groupdict().get("id") in ("tree", "offers"):
                return False
            return h.send_json({"error": "not found"}, 404)
        d.route("PUT", r"/api/v1/assistant/sessions/(?P<mid>[^/]+)/(?P<sid>[^/]+)/direction", absent)
        d.route("GET", r"/api/v1/assistant/directions/(?P<id>[^/]+)", absent)
        return
    hub = d.hub
    lock = threading.Lock()
    leaders = list(hub.MOCK_LEADERS)
    first = leaders[0] if leaders else ""
    computer = "macbook"
    # Two Agents of the first direction's: one the owner filed, at work; one its leader opened, waiting on you.
    extra = [  # i18n-ignore: mock data
        hub.sess(computer, "dirs0001-0000-4000-8000-000000000001", "整理首页改版的设计稿", "/Users/me/code/site", "working",
                 "working", "site", 4, "1", 1, 2, "正在对比三个方案的间距。"),
        hub.sess("studio", "dirs0002-0000-4000-8000-000000000002", "写新版引导页的文案", "/Users/me/code/site", "blocked",
                 "needs_you", "site", 4, "2", 2, 6, "两个标题你更喜欢哪个？", pending=1),
    ]
    hub.SESSIONS.extend(extra)
    chats = [c["session_id"] for c in hub.ASSISTANT
             if c["session_id"] not in leaders and c["session_id"] != hub.ASSISTANT_CONFIG.get("main")
             and c.get("space") != "health"]
    ago = lambda **kw: _iso(_now() - timedelta(**kw))  # noqa: E731

    def initial_book():
        if not first:
            return {}
        book = {
            f"{computer}/{extra[0]['session_id']}": {"direction": first, "title": extra[0]["title"], "at": ago(hours=2)},
            f"{computer}/a1f3c2d4-0000-4000-8000-000000000007": {"direction": first, "title": "菜谱网站加搜索",  # i18n-ignore: mock data
                                                                 "at": ago(days=1)},
            "nas/dirs0005-0000-4000-8000-000000000005": {"direction": first, "title": "备份照片到 NAS", "at": ago(days=2)},  # i18n-ignore: mock data
            "server/dirs0006-0000-4000-8000-000000000006": {"direction": first, "title": "看一下服务器的磁盘",  # i18n-ignore: mock data
                                                            "at": ago(hours=5), "standing": "list_unknown"},
            f"{computer}/dirs0007-0000-4000-8000-000000000007": {"direction": first, "title": "旧版落地页的截图",  # i18n-ignore: mock data
                                                                 "at": ago(days=3), "standing": "not_listed"},
            "old-laptop/dirs0008-0000-4000-8000-000000000008": {"direction": first, "title": "旧电脑上的草稿",  # i18n-ignore: mock data
                                                                "at": ago(days=12), "standing": "computer_removed"},
        }
        for i, sid in enumerate(chats[1:3]):  # two everyday chats the owner put here
            title = next((c.get("title", "") for c in hub.ASSISTANT if c["session_id"] == sid), "")
            book[f"{hub.ASSISTANT_CONFIG.get('machine_id') or 'server'}/{sid}"] = {"direction": first, "title": title,
                                                                                  "at": ago(hours=20 + i)}
        return book

    def initial_origins():
        # what the first leader opened: the session the owner did not file
        return {f"studio/{extra[1]['session_id']}": {"leader": first, "title": extra[1]["title"], "at": ago(minutes=30)}} if first else {}

    state = {"book": initial_book(), "origins": initial_origins(), "removed": {}, "options": {}}

    def options():
        return state["options"]

    def missing():
        return MODE == "missing" or options().get("routes") == "missing"

    d.feature(FEATURE, when=lambda: options().get("feature") != "off")

    def lang(h):
        al = (h.headers.get("Accept-Language") or "").lower()
        return 1 if al.startswith("zh") else 2 if al.startswith("ja") else 0

    def refuse(h, code, status=None):
        words = SENTENCES.get(code, ("Refused.", "被拒绝了。", "拒否されました。"))[lang(h)]
        return h.send_json({"error": words, "code": code}, status or STATUS.get(code, 409))

    def leaders_now():
        return list(hub.MOCK_LEADERS)

    def session_row(machine, sid):
        for s in hub.SESSIONS:
            if s["machine_id"] == machine and s["session_id"] == sid:
                return s
        for c in hub.ASSISTANT:
            if c["session_id"] == sid and (c.get("machine_id") or hub.ASSISTANT_CONFIG.get("machine_id")) == machine:
                return c
        return None

    def online(machine):
        m = next((x for x in hub.MACHINES if (x.get("id") or x.get("machine_id")) == machine), None)
        return None if m is None else bool(m.get("online"))

    def standing(key, entry):
        machine, sid = key.split("/", 1)
        if entry.get("standing"):
            return entry["standing"]
        up = online(machine)
        if up is None:
            return "computer_removed"
        if not up:
            return "computer_offline"
        row = session_row(machine, sid)
        return (row or {}).get("state") or ("not_listed" if row is None else "idle")

    def followers():
        out = set()
        for t in hub.TODOS:
            if t.get("done_at") or not (t.get("supervision") or {}).get("supervisor"):
                continue
            for a in t.get("agents", []):
                if not a.get("released_at"):
                    out.add(f"{a['machine_id']}/{a['session_id']}")
        return out

    def placements():
        """Each placed session's direction, source, title and time: the owner's live filings first, then what a leader
        opened, unless the owner set that one on its own."""
        out = {}
        follow = followers()
        for key, e in state["book"].items():
            if e.get("direction") and key not in follow:
                out[key] = dict(e, source="owner")
        for key, o in state["origins"].items():
            if key in state["book"] or key in follow:
                continue
            out[key] = {"direction": o["leader"], "title": o["title"], "at": o["at"], "source": "leader"}
        return out

    def placed(direction):
        rows = []
        for key, e in placements().items():
            if e["direction"] != direction or key.split("/", 1)[1] == direction:
                continue
            machine, sid = key.split("/", 1)
            rows.append({"machine_id": machine, "session_id": sid, "title": e.get("title", ""), "source": e["source"],
                         "at": e.get("at", ""), "state": standing(key, e)})
        rows.sort(key=lambda r: r["at"], reverse=True)
        return rows

    def tree(h, req):
        if os.environ.get("MOCK_LEGACY_HUB") or hub.assistant_for_manager(h) is not None:
            return False  # the hub's own answer (another manager's page has no tree)
        body = hub.mock_direction_tree()
        if missing() or options().get("feature") == "off":
            return h.send_json(body)
        in_tasks = {f"{a['machine_id']}/{a['session_id']}" for dd in body["directions"] for t in dd["tasks"]
                    for a in _walk(t["agents"])}
        with lock:
            for dd in body["directions"]:
                if dd.get("secretary"):
                    continue
                live = [r for r in placed(dd["id"]) if r["state"] in ("working", "needs_you")
                        and f"{r['machine_id']}/{r['session_id']}" not in in_tasks]
                if live:
                    dd["sessions"] = live
        return h.send_json(body)

    def read(h, req):
        did = req.match["id"]
        if did in ("tree", "offers"):
            return False
        if hub.assistant_for_manager(h) is not None:
            return False
        if missing():
            return h.send_json({"error": "not found"}, 404)
        opts = options()
        if opts.get("delay"):
            time.sleep(float(opts["delay"]))
        if opts.get("read") == "fail":
            return refuse(h, "direction_unavailable", 503)
        if opts.get("read") == "gone" or (did not in leaders_now() and did != opts.get("ended")):
            return refuse(h, "direction_not_found", 404)
        with lock:
            rows = [] if opts.get("sessions_unavailable") else placed(did)
        out = {"id": did, "name": hub.MOCK_DIRECTION_NAMES.get(did, ""), "notes": hub.MOCK_DIRECTION_NOTES.get(did, ""),
               "sessions": rows, "ended_tasks": [], "more": False}
        if opts.get("sessions_unavailable"):
            out["sessions_unavailable"] = True
        if opts.get("ended") == did:
            out["ended_at"] = ago(minutes=1)
        return h.send_json(out)

    def file(h, req):
        if hub.assistant_for_manager(h) is not None:
            return False
        if missing():
            return h.send_json({"error": "not found"}, 404)
        if hub.is_read_only(h):
            return refuse(h, "forbidden", 403)
        code = options().pop("refuse", None)
        if code:
            return refuse(h, code)
        try:
            body = json.loads(req.body or b"{}")
        except ValueError:
            return refuse(h, "bad_request", 400)
        target = body["direction"] if "direction" in body else ""
        if target is not None and not isinstance(target, str):
            return refuse(h, "bad_request", 400)
        machine, sid = req.match["mid"], req.match["sid"]
        key = f"{machine}/{sid}"
        with lock:
            book = state["book"]
            before = book.get(key)
            previous = before.get("direction") if before else None
            if sid == hub.ASSISTANT_CONFIG.get("main"):
                return refuse(h, "direction_main_conversation")
            if sid in leaders_now():
                return refuse(h, "direction_leader_conversation")
            if key in followers():
                return refuse(h, "direction_supervised_task")
            if target and target not in leaders_now():
                return refuse(h, "direction_not_found", 404)
            if target is None:  # taken out of the book: the placement before the first filing comes back
                if before is not None:
                    state["removed"][key] = before
                    del book[key]
                return h.send_json({"direction": None, "previous": previous, "changed": before is not None})
            if online(machine) is None:
                return refuse(h, "machine_not_found", 404)
            undo = state["removed"].pop(key, None)
            if target and session_row(machine, sid) is None and not (undo and undo.get("direction") == target) \
                    and not (before and before.get("direction") == target):
                return refuse(h, "session_not_found", 404)
            if target and previous != target and sum(1 for e in book.values() if e.get("direction") == target) >= MAX_PER_DIRECTION:
                return refuse(h, "direction_full")
            row = session_row(machine, sid) or {}
            entry = undo if undo and undo.get("direction") == target else {
                "direction": target, "title": (before or {}).get("title") or row.get("title", ""), "at": _iso(_now())}
            if before and before.get("standing") and "standing" not in entry:
                entry["standing"] = before["standing"]
            book[key] = entry
        hub.broadcast(hub.from_default({"type": "directions"}))
        return h.send_json({"direction": target, "previous": previous, "changed": previous != target})

    def control(h, req):
        opts = options()
        for name in ("routes", "feature", "refuse", "read", "delay", "sessions_unavailable", "ended"):
            if name in req.query:
                value = req.query[name][0]
                if value in ("", "on", "ok", "0") and name != "ended":
                    opts.pop(name, None)
                else:
                    opts[name] = value
        if "ended" in req.query and not req.query["ended"][0]:
            opts.pop("ended", None)
        return h.send_json({"book": state["book"], "options": opts})

    def reset():
        state.update(book=initial_book(), origins=initial_origins(), removed={}, options={})

    d.route("GET", r"/api/v1/assistant/directions/tree", tree)
    d.route("GET", r"/api/v1/assistant/directions/(?P<id>[^/]+)", read)
    d.route("PUT", r"/api/v1/assistant/sessions/(?P<mid>[^/]+)/(?P<sid>[^/]+)/direction", file)
    d.route("GET", r"/mock/direction_sessions", control)
    d.on_reset(reset)


def _walk(agents):
    for a in agents:
        yield a
        yield from _walk(a.get("children") or [])

