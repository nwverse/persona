"""A Working Agent whose background command looks stuck (#13744).

MOCK_BG_STALLED=1: the blog agent's test run has used no CPU and written nothing for hours and still counts as Working
(Session.background_stalled_since; docs/web/web.md "Background commands that look stuck").
"""
import os

HELP = "MOCK_BG_STALLED=1  the blog agent's background test run looks stuck for hours, still Working (#13744)"


def register(d):
    d.help(HELP)
    if os.environ.get("MOCK_BG_STALLED") != "1":
        return
    hub = d.hub
    for s in hub.SESSIONS:
        if s["session_id"] == hub.BLOG:
            s.update(status="done", state="working", state_since=hub.ago(hours=4), seen_at=None, background_working=True,
                     background_waiting=False, background_stalled_since=hub.ago(hours=3, minutes=5),
                     last_text="迁移完成，测试在后台跑着。",
                     background=[{"id": "bq7ci42wa", "kind": "shell", "description": "跑全部测试",
                                  "command": "pnpm test", "started_at": hub.ago(hours=4)}])
