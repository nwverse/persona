"""The sent-as-user ledger as the hub answers it (GET /api/v1/connectors/sent, docs/extensions/connectors.md "Sent as the user",
docs/protocol/protocol.md "Connector API"), for mock_hub.py's MOCK_SENT (docs/web/web.md "Settings", scripts/test/web_sent_e2e.py).

MOCK_SENT=1        about seventy made-up rows, newest first: mail, GitHub, Lark and an extension's connector, all four ways
                   through the gate (a rule, "always allow" in the chat, the card on the phone with the assistant answering
                   some of them, the extension sending on its own), and one text of exactly 4000 characters
MOCK_SENT=empty    a ledger with nothing in it
MOCK_SENT=nodb     503 no_database: a hub running without a database
MOCK_SENT=forbidden 403: a device without full access
MOCK_SENT=old      404 with no code: a hub from before the ledger (Go's mux answers an unknown path in plain text)
GET /mock/sent?mode=<one of them> switches a running mock to that state (no mode: says the current one) and forgets recalls

Recall (#3219, extmgr/sent_recall_route.go): `can_recall` is true on a row with a message_ref sent within a day (the
hub's default window), row 54 is already recalled, and POST /api/v1/connectors/sent/{id}/recall with {"confirm": true}
takes back row 66 (the mock remembers it until the next /mock/sent?mode=) and answers row 60 with 502 recall_refused.

`before` / `limit` / `connector` as the hub reads them (extmgr/sent_ledger.go sentRoutes, db.SentAsUsers): rows newest
first by id, those with an id below `before`, at most `limit` (default 50, at most 200, 400 below 1), `next` the id to
ask for the page after this one and null on the last. Nothing here is real: the names are placeholders, the machine
and conversation ids are the ones mock_hub.py's fixtures have so a row's chat link opens something. Standard library
only, no state but the recalls: mock_hub.py calls route(); test_sent_ledger_mock.py pins the shapes.
"""
import json
import os
from datetime import datetime, timedelta, timezone

MODES = ("1", "empty", "nodb", "forbidden", "old")
SIZE = 70  # rows MOCK_SENT=1 holds
LONG_AT = 3  # the template (and the row, 0 = newest) whose text is as long as the ledger keeps: once, on the first page
TEXT_MOST = 4000  # connector.SentTextMost
DEFAULT_LIMIT, MOST_LIMIT = 50, 200  # db.SentAsUserDefault, db.SentAsUserMost

_START = datetime.now(timezone.utc)

# mock_hub.py's fixtures: the assistant's chats are on "server", an agent's on "macbook"
ASSISTANT_CHAT = {"machine_id": "server", "session_id": "b2e4d6f8-0000-4000-8000-000000000001"}
AGENT_CHAT = {"machine_id": "macbook", "session_id": "a1f3c2d4-0000-4000-8000-000000000001"}


_now = {"mode": None}  # GET /mock/sent?mode=…: what a running mock answers instead of MOCK_SENT
_recalled = {}  # row id -> recalled_at, from POST …/recall since the last mode switch
RECALL_WINDOW = 24 * 60  # minutes: extmgr sentRecallHours
RECALLED_ID, REFUSED_ID = 54, 60  # already recalled; the other side refuses


def mode():
    """MOCK_SENT (or what /mock/sent set since), or "" when it is not set (or not one of MODES): the route then answers nothing."""
    v = _now["mode"] if _now["mode"] is not None else os.environ.get("MOCK_SENT", "")
    return v if v in MODES else ""


def _long_text():
    """Exactly TEXT_MOST characters, in paragraphs, as the ledger keeps a long send."""
    out = []
    while len("\n\n".join(out)) < TEXT_MOST:
        out.append("Paragraph %d. This is a long update that goes on for a while, so the details can show where the ledger cuts it." % (len(out) + 1))
    return "\n\n".join(out)[:TEXT_MOST]


# What a row says, cycled through. (connector, action, target, text, summary, origin, let_through, message_ref)
def _templates():
    rule = lambda connector, action, **extra: {"kind": "rule", "rule": {"connector": connector, "action": action, **extra, "decision": "allow"}}
    card = lambda n, **extra: {"kind": "card", "interaction_id": "ix-demo-%d" % n, **extra}
    return [
        ("mail", "mail_send", {"to": ["ann@example.com"], "subject": "Re: Design review moved to Tuesday"},
         "Hi Ann,\n\nTuesday afternoon works for me. I'll bring the updated mockups.\n\nThanks", "Send an email to ann@example.com, subject: Re: Design review moved to Tuesday",
         ASSISTANT_CHAT, card(1), ""),
        ("github", "github_comment", {"ref": "example-org/app#412"},
         "Thanks for the report. This is fixed in the next release.\nLet me know if it still happens after updating.", "github_comment example-org/app#412",
         AGENT_CHAT, rule("github", "github_comment"), ""),
        ("lark", "POST /im/v1/conversations/oc_demo01/messages",
         {"command": "lark-api", "method": "POST", "path": "/im/v1/conversations/oc_demo01/messages", "conversation": "oc_demo01"},
         "Got it, I'll send the numbers tonight.", "POST /im/v1/conversations/oc_demo01/messages",
         ASSISTANT_CHAT, {"kind": "conversation_always"}, ""),
        ("mail", "mail_send", {"to": ["bob@example.com", "team@example.com"], "cc": ["ann@example.com"], "subject": "Quarterly summary (long)"},
         _long_text(), "Send an email to bob@example.com, team@example.com, subject: Quarterly summary (long)", ASSISTANT_CHAT, card(2), ""),
        ("examplechat", "sent.record", {"chat": "general", "thread": "t-8841"},
         "Standup notes are in the shared doc.\nI'll post the action items after lunch.", "examplechat: chat general, thread t-8841",
         {"extension": "examplechat", **ASSISTANT_CHAT}, {"kind": "auto", "setting": "auto_reply"}, "msg-8841"),
        ("mail", "mail_send", {"to": ["bob@example.com"], "subject": "Invoice 1042"},
         "Hi Bob,\n\nHere is the invoice we talked about. Payment terms are unchanged.\n\nThanks", "Send an email to bob@example.com, subject: Invoice 1042",
         ASSISTANT_CHAT, card(3, answered_by="assistant"), ""),
        ("github", "github_review", {"ref": "example-org/app#398", "event": "COMMENT"},
         "Looks good overall. One question about the retry path: should it back off?", "Review (comment) on example-org/app#398",
         AGENT_CHAT, card(4), ""),
        ("lark", "POST /im/v1/messages", {"command": "lark-api", "method": "POST", "path": "/im/v1/messages"},
         "Reminder: the review starts in ten minutes.", "POST /im/v1/messages",
         {"machine_id": "macbook"}, rule("lark", "POST /im/v1/messages", match={"receive_id_type": "chat_id"}), ""),
        ("mail", "mail_send", {"to": ["bob@example.com"], "subject": "Out of office"},
         "", "Send an email to bob@example.com, subject: Out of office", {}, card(5), ""),
        ("examplechat", "chat_post", {"arg_keys": ["body", "id"]},
         "Moved the meeting to 3pm.", "Post a message in the chat", AGENT_CHAT, card(6), ""),
        ("examplechat", "sent.record", {"chat": "random"},
         "Friday cake is confirmed.", "examplechat: chat random", {"extension": "examplechat"}, {"kind": "auto"}, "msg-9017"),
        ("github", "github_comment", {"ref": "example-org/docs#77"},
         "Merged. Thanks for the quick turnaround!", "Comment on example-org/docs#77",
         {}, card(7, answered_by="assistant"), ""),
    ]


def rows(now=None):
    """The ledger's rows, newest first, ids SIZE down to 1: row i is a template cycled, sent some minutes before now."""
    now = now or _START
    tpl = _templates()
    out = []
    for i in range(SIZE):
        k = i % len(tpl)
        connector, action, target, text, summary, origin, through, ref = tpl[0 if k == LONG_AT and i != LONG_AT else k]
        minutes = 3 + i * 37 + i * i * 9  # a few minutes ago, out to about a month
        at = now - timedelta(minutes=minutes)
        recalled = _recalled.get(SIZE - i) or (_iso(at + timedelta(minutes=5)) if SIZE - i == RECALLED_ID else None)
        out.append({
            "id": SIZE - i, "at": _iso(at), "connector": connector,
            "action": action, "target": target, "text": text, "summary": summary, "origin": origin, "let_through": through,
            **({"message_ref": ref} if ref else {}), **({"recalled_at": recalled} if recalled else {}),
            "can_recall": bool(ref) and not recalled and minutes < RECALL_WINDOW,
        })
    return out


def _iso(t):
    return t.isoformat().replace("+00:00", "Z")


def page(items, before=0, limit=DEFAULT_LIMIT, connector=""):
    """db.SentAsUsers over items (newest first): the rows below `before` (0: from the newest) of `connector` (""): all),
    at most `limit`; next is the id to ask for the page after this one, None on the last."""
    hit = [r for r in items if (not before or r["id"] < before) and (not connector or r["connector"] == connector)]
    got = hit[:limit]
    return {"items": got, "next": got[-1]["id"] if len(hit) > limit else None}


def _number(v, what, low=1):
    try:
        n = int(v)
    except ValueError:
        n = None
    if n is None or n < low:
        raise ValueError('%s "%s": a number from %d' % (what, v, low))
    return n


def _plain_404(h):
    """What Go's mux says for a path it has no route for: plain text, no code."""
    body = b"404 page not found\n"
    h.send_response(404)
    h.send_header("Content-Type", "text/plain; charset=utf-8")
    h.send_header("Content-Length", str(len(body)))
    h.end_headers()
    h.wfile.write(body)


def _recall(h, m, p, body):
    """POST /api/v1/connectors/sent/{id}/recall as sent_recall_route.go answers it (codes and statuses: recallStatus)."""
    if m == "old":
        return _plain_404(h)
    if m == "forbidden":
        return h.send_json({"error": "a message can only be taken back on your own device", "code": "own_device_only"}, 403)
    if m == "nodb":
        return h.send_json({"error": "no database: there is no ledger", "code": "no_database"}, 503)
    try:
        confirm = json.loads(body or b"{}").get("confirm") is True
    except (ValueError, AttributeError):
        confirm = False
    if not confirm:
        return h.send_json({"error": 'send {"confirm": true} once the user agreed', "code": "confirm_required"}, 400)
    rid = p[len("/api/v1/connectors/sent/"):-len("/recall")]
    row = next((r for r in ([] if m == "empty" else rows()) if str(r["id"]) == rid), None)
    if row is None:
        return h.send_json({"error": "no such row in the ledger", "code": "not_found"}, 404)
    if row.get("recalled_at"):
        return h.send_json({"error": "this message was taken back already", "code": "already_recalled", "item": row}, 409)
    if not row["can_recall"]:
        code = "too_old" if row.get("message_ref") else "not_recallable"
        return h.send_json({"error": "this message can't be taken back", "code": code}, 409)
    if row["id"] == REFUSED_ID:
        return h.send_json({"error": "examplechat: the chat no longer lets you remove this message", "code": "recall_refused"}, 502)
    _recalled[row["id"]] = _iso(datetime.now(timezone.utc))
    h.send_json({"ok": True, "item": dict(row, recalled_at=_recalled[row["id"]], can_recall=False)})


def route(h, method, p, q, body=b""):
    """GET /api/v1/connectors/sent?before=&limit=&connector= (q: the parsed query). True when it answered; False for any
    other request, and for this one when MOCK_SENT is not set. GET /mock/sent?mode=<one of MODES> switches the running
    mock to another state (so one hub can show every state); it says the mode it is in (an unknown one: 400)."""
    if p == "/mock/sent" and method == "GET" and mode():
        want = q.get("mode", [None])[0]
        if want is not None and want not in MODES:
            h.send_json({"error": "mode: one of %s" % ", ".join(MODES), "code": "bad_request"}, 400)
        else:
            if want is not None:
                _now["mode"] = want
                _recalled.clear()
            h.send_json({"mode": mode()})
        return True
    m = mode()
    if m and method == "POST" and p.startswith("/api/v1/connectors/sent/") and p.endswith("/recall"):
        _recall(h, m, p, body)
        return True
    if not m or p != "/api/v1/connectors/sent" or method != "GET":
        return False
    if m == "old":
        _plain_404(h)
    elif m == "forbidden":
        h.send_json({"error": "only a device with full rights reads what was sent as the user", "code": "forbidden"}, 403)
    elif m == "nodb":
        h.send_json({"error": "no database: there is no ledger", "code": "no_database"}, 503)
    else:
        try:
            limit = min(_number(q["limit"][0], "limit"), MOST_LIMIT) if "limit" in q else DEFAULT_LIMIT
            before = _number(q["before"][0], "before") if "before" in q else 0
        except ValueError as e:
            h.send_json({"error": str(e), "code": "bad_request"}, 400)
            return True
        h.send_json(page([] if m == "empty" else rows(), before, limit, q.get("connector", [""])[0]))
    return True
