"""Partner hubs in the mock hub (docs/server/partner-hubs.md, #13248), a domain of scripts/mock/mock_domains.py: what
Settings › This hub › Partner hubs reads and changes. MOCK_PARTNER_HUBS=1 lists the partner_hubs feature and two partners,
one connected and one offline that this hub does not dial, with "Ask me before sending" off for the second; =empty has
none yet; =noissue cannot show a code (no TLS here). Every change pushes {"type":"partner_hubs"}. GET /mock/partner_hubs
shows the list, the prefs and what the page asked (?reset=1 puts the first list back). Everything is made up.
"""
import os
import secrets
from datetime import datetime, timedelta, timezone
from urllib.parse import parse_qs, quote, urlparse

MODE = os.environ.get("MOCK_PARTNER_HUBS", "")
HUB = None  # mock_hub.py's globals (mock_domains.Hub), from register
FP = "bW9jay1wYXJ0bmVyLWh1Yi1maW5nZXJwcmludC0wMDE"  # a made-up base64url pin
OWN_FP = "bW9jay10aGlzLWh1Yi1maW5nZXJwcmludC0wMDAwMDA"
STATE = {"hubs": [], "prefs": {}, "log": []}


def _iso(t):
    return t.isoformat(timespec="seconds").replace("+00:00", "Z")


def _first_list():
    if MODE == "empty":
        return []
    return [
        {"hub_id": "7c1e9a52-4d3b-4f6a-9e21-0a5b6c7d8e01", "name": "家里", "fp": FP, "state": "connected", "dial": "auto",
         "since": HUB.ago(hours=3), "last_seen": HUB.ago(minutes=1), "dialed_by": "us", "rtt_ms": 38,
         "created_at": HUB.ago(days=6)},
        {"hub_id": "2f6b0d84-8e5a-4c19-b7d3-5e4f3a2b1c02", "name": "工作室", "fp": FP, "state": "offline", "dial": "never",
         "last_seen": HUB.ago(hours=5), "dialed_by": "", "created_at": HUB.ago(days=2)},
    ]


def reset():
    STATE["hubs"] = _first_list()
    STATE["prefs"] = {} if MODE == "empty" else {"2f6b0d84-8e5a-4c19-b7d3-5e4f3a2b1c02": False}
    STATE["log"] = []


def changed():
    HUB.broadcast({"type": "partner_hubs"})


def _find(hub_id):
    return next((x for x in STATE["hubs"] if x["hub_id"] == hub_id), None)


def _json(req):
    try:
        body = req.json()
    except ValueError:
        return None
    return body if isinstance(body, dict) else None


def list_route(h, req):
    """GET /api/v1/partner-hubs."""
    return h.send_json({"enabled": True, "member": False, "can_issue": MODE != "noissue", "hubs": STATE["hubs"]})


def code_route(h, req):
    """POST /api/v1/partner-hubs/code: a one-time code for ten minutes on the real clock (the mock's NOW is fixed)."""
    STATE["log"].append("POST /api/v1/partner-hubs/code")
    if MODE == "noissue":
        return h.send_json({"error": "这台 hub 没有 TLS 端口，别的 hub 连不进来（设置 public.tls_listen）", "code": "no_tls"}, 409)
    code = "persona://partner?hub=%s&name=%s&fp=%s&tls=18662&url=%s&code=jh_%s" % (
        HUB.HUB_ID, quote("演示 hub"), OWN_FP, quote("https://hub.example.com:18662", safe=""), secrets.token_hex(32))
    return h.send_json({"code": code, "expires_at": _iso(datetime.now(timezone.utc) + timedelta(minutes=10))})


def pair_route(h, req):
    """POST /api/v1/partner-hubs {code}: the hub the code names, connecting; its own code and one that does not read refused."""
    body = _json(req) or {}
    raw = str(body.get("code") or "")
    STATE["log"].append("POST /api/v1/partner-hubs")
    url = urlparse(raw)
    q = {k: v[0] for k, v in parse_qs(url.query).items()}
    if url.scheme != "persona" or url.netloc != "partner" or not q.get("hub") or not q.get("fp") or not q.get("code", "").startswith("jh_"):
        return h.send_json({"error": "配对码读不出来", "code": "bad_code"}, 400)
    if q["hub"] == HUB.HUB_ID:
        return h.send_json({"error": "这是这台 hub 自己的配对码", "code": "own_code"}, 409)
    if q["code"].endswith("dead"):
        return h.send_json({"error": "对方 hub 拒绝了这个配对码", "code": "code_refused"}, 401)
    if _find(q["hub"]):  # a pairing is never replaced: unpair first
        return h.send_json({"error": "这台 hub 已经和那台 hub 配对过了：先解除配对，再重新配对", "code": "already_paired"}, 409)
    view = {"hub_id": q["hub"], "name": q.get("name") or q["hub"], "fp": q["fp"], "state": "connecting", "dial": "auto",
            "dialed_by": "", "created_at": _iso(datetime.now(timezone.utc))}
    STATE["hubs"] = STATE["hubs"] + [view]
    changed()
    return h.send_json(view)


def hub_route(h, req):
    """PATCH /api/v1/partner-hubs/{hub_id} {dial} and DELETE /api/v1/partner-hubs/{hub_id}."""
    hub_id = req.match["id"]
    found = _find(hub_id)
    STATE["log"].append("%s /api/v1/partner-hubs/%s" % (req.method, hub_id))
    if not found:
        return h.send_json({"error": "没有这个协作 hub", "code": "not_found"}, 404)
    if req.method == "DELETE":
        STATE["hubs"] = [x for x in STATE["hubs"] if x["hub_id"] != hub_id]
        STATE["prefs"].pop(hub_id, None)
        changed()
        return h.send_json({"ok": True})
    body = _json(req) or {}
    if body.get("dial") not in ("auto", "never"):
        return h.send_json({"error": "dial 只能是 auto 或 never", "code": "bad_request"}, 400)
    found["dial"] = body["dial"]
    if found["state"] != "connected":
        found["state"] = "connecting" if found["dial"] == "auto" else "offline"
    changed()
    return h.send_json(found)


def prefs_route(h, req):
    """GET /api/v1/partner-messages/prefs: the stored rows only (none: ask first)."""
    return h.send_json({"prefs": [{"hub_id": k, "confirm": v} for k, v in STATE["prefs"].items()]})


def pref_route(h, req):
    """PUT /api/v1/partner-messages/prefs/{hub_id} {confirm}."""
    body = _json(req)
    hub_id = req.match["id"]
    STATE["log"].append("PUT /api/v1/partner-messages/prefs/%s" % hub_id)
    if body is None or not isinstance(body.get("confirm"), bool):
        return h.send_json({"error": "confirm 要是 true 或 false", "code": "bad_request"}, 400)
    STATE["prefs"][hub_id] = body["confirm"]
    return h.send_json({"hub_id": hub_id, "confirm": body["confirm"]})


def mock_route(h, req):
    """GET /mock/partner_hubs[?reset=1]."""
    if (req.query.get("reset") or [""])[0]:
        reset()
        changed()
    return h.send_json({"hubs": STATE["hubs"], "prefs": STATE["prefs"], "log": STATE["log"]})


def register(d):
    global HUB
    HUB = d.hub
    d.help("MOCK_PARTNER_HUBS=1|empty|noissue  Settings › This hub › Partner hubs: two partners (connected, offline), none yet, "
           "or no code to show (#13248); GET /mock/partner_hubs")
    if not MODE:
        return
    reset()
    d.feature("partner_hubs")
    d.route("GET", r"/api/v1/partner-hubs", list_route)
    d.route("POST", r"/api/v1/partner-hubs", pair_route)
    d.route("POST", r"/api/v1/partner-hubs/code", code_route)
    d.route(["PATCH", "DELETE"], r"/api/v1/partner-hubs/(?P<id>[^/]+)", hub_route)
    d.route("GET", r"/api/v1/partner-messages/prefs", prefs_route)
    d.route("PUT", r"/api/v1/partner-messages/prefs/(?P<id>[^/]+)", pref_route)
    d.route("GET", "/mock/partner_hubs", mock_route)
    d.on_reset(reset)
