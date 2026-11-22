#!/usr/bin/env python3
"""How often the app pulls chats in the background (AgentSync), over a recording of a real hub's pushes
(server/cmd/pushrec, docs/operations/testing.md "Push replay"): before the working-agent gap (a pull per transcript change, merged
over the 300 ms plan) and after (a working agent's chat at most every AgentSync.workingGap, at once when not working).
Prints counts only; the recording stays on the machine it was made on.

    python3 scripts/diag/agentsync_replay.py ~/push.jsonl
"""
import json
import sys

GAP, PLAN = 60.0, 0.3


def main(path):
    rev, status, events = {}, {}, []  # events: (time, session, working then) per transcript change
    for line in open(path):
        r = json.loads(line)
        t, m = r["t"] / 1000, r["msg"]
        if m.get("type") == "sessions":
            for s in m.get("sessions") or []:
                k = (m["machine_id"], s["session_id"])
                status[k] = s.get("status")
                if s.get("rev") is not None and rev.get(k) != s["rev"]:
                    if k in rev:
                        events.append((t, k, s.get("status") == "working"))
                    rev[k] = s["rev"]
        elif m.get("type") == "revs":
            for sid, v in (m.get("revs") or {}).items():
                k = (m["machine_id"], sid)
                if rev.get(k) != v:
                    if k in rev:
                        events.append((t, k, status.get(k) == "working"))
                    rev[k] = v
    if not events:
        sys.exit("no transcript changes in the recording")
    mins = (events[-1][0] - events[0][0]) / 60
    before, last = 0, {}
    for t, k, _ in events:
        if t - last.get(k, -1e9) > PLAN:
            before, last[k] = before + 1, t
    after, pulled, held = 0, {}, {}
    for t, k, working in events:
        for hk, at in list(held.items()):  # held chats whose turn came
            if at <= t:
                after, pulled[hk] = after + 1, at
                del held[hk]
        if k in held:
            continue
        lp = pulled.get(k)
        if working and lp is not None and t - lp < GAP:
            held[k] = lp + GAP
        elif t - pulled.get(k, -1e9) > PLAN:
            after, pulled[k] = after + 1, t
    after += len(held)
    if mins > 0:
        pulls = "pulls %.1f/min before, %.1f/min after" % (before / mins, after / mins)
    else:
        pulls = "pulls %d before, %d after (per-minute rates unavailable)" % (before, after)
    print("%d transcript changes over %.1f min in %d sessions (%d while working): %s"
          % (len(events), mins, len({k for _, k, _ in events}), sum(1 for e in events if e[2]), pulls))


if __name__ == "__main__":
    main(sys.argv[1])
