#!/usr/bin/env python3
"""Live check of the terminal's "Full view" against a REAL herdr client (docs/integrations/terminal.md "Full herdr UI"): the fake hub runs
`herdr session attach <name>` in a PTY (MOCK_TERMINAL_FULL_EXEC) and relays its bytes the way the node does; Chrome draws
them in xterm; a mouse selection in herdr's own screen must come out as OSC 52 and land on the browser's clipboard.

Only ever use a throwaway session you made yourself (`herdr --session persona-fullui-test`, then detach): the check
types one `echo` into that session's focused pane and drags the mouse over its output. Stop and delete the session
afterwards (`herdr session stop <name>; herdr session delete <name>`).

    MOCK_TERMINAL_STREAM=1 MOCK_TERMINAL_CONTROL=1 MOCK_TERMINAL_PANES=1 MOCK_TERMINAL_FULL=1 \\
        MOCK_TERMINAL_FULL_EXEC="herdr session attach persona-fullui-test" PORT=18868 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18868 npx vite --port 18869 --strictPort &)
    python3 scripts/test/web_herdr_full_live.py --base http://127.0.0.1:18869 --session persona-fullui-test [--shots <dir>]
"""
import argparse
import json
import os
import subprocess
import time

from playwright.sync_api import sync_playwright

MARK = "LIVE_COPY_MARK"


def ok(msg):
    print("ok  ", msg, flush=True)


def herdr_env(session):
    out = subprocess.run(["herdr", "session", "list", "--json"], capture_output=True, text=True, check=True).stdout
    sock = next(s["socket_path"] for s in json.loads(out)["sessions"] if s["name"] == session)
    env = {k: v for k, v in os.environ.items() if not k.startswith("HERDR_")}
    env["HERDR_SOCKET_PATH"] = sock
    return env


def screen(page):
    return page.evaluate("""() => [...document.querySelectorAll('[data-surface="shown"] .xterm-rows > div')].map(r => r.textContent)""")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", required=True)
    ap.add_argument("--session", required=True)
    ap.add_argument("--shots")
    a = ap.parse_args()
    if a.session == "default":
        raise SystemExit("never the session you work in")
    env = herdr_env(a.session)
    panes = json.loads(subprocess.run(["herdr", "pane", "list"], env=env, capture_output=True, text=True, check=True).stdout)["result"]["panes"]
    pane = next((x for x in panes if x.get("focused")), panes[0])["pane_id"]  # the one herdr shows
    subprocess.run(["herdr", "pane", "run", pane, f"echo {MARK}"], env=env, check=True, capture_output=True)

    with sync_playwright() as p:
        exe = os.environ.get("PERSONA_E2E_CHROMIUM")
        browser = p.chromium.launch(executable_path=exe) if exe else p.chromium.launch(channel="chrome")
        ctx = browser.new_context(viewport={"width": 1500, "height": 900}, device_scale_factor=2, permissions=["clipboard-read", "clipboard-write"])
        ctx.add_init_script("localStorage.setItem('persona.token', 'mock')")
        page = ctx.new_page()
        page.set_default_timeout(15000)
        page.goto(f"{a.base}/terminal/server/pane%3Aw1%3Ap2")
        for needle in ("spaces", MARK):
            deadline = time.time() + 15
            while time.time() < deadline and needle not in "\n".join(screen(page)):
                time.sleep(0.2)
            assert needle in "\n".join(screen(page)), f"{needle!r} not on the screen:\n" + "\n".join(screen(page))
        ok("herdr's own client draws in the browser: its sidebar says 'spaces' and the pane shows the echo")
        if a.shots:
            os.makedirs(a.shots, exist_ok=True)
            page.screenshot(path=os.path.join(a.shots, "live-full-light.png"))

        # where the output line is: the row and column of MARK that is not the command being typed
        rows = screen(page)
        hit = None
        for r, text in enumerate(rows):
            c = text.find(MARK)
            while c >= 0:
                if not text[max(0, c - 5):c].endswith("echo "):
                    hit = (r, c)
                c = text.find(MARK, c + 1)
        assert hit, rows
        box = page.evaluate("""() => { const s = document.querySelector('[data-surface="shown"] .xterm-screen').getBoundingClientRect(); const t = window.__cols; return [s.x, s.y, s.width, s.height]; }""")
        cols = max(len(r) for r in rows)
        cw, ch = box[2] / cols, box[3] / len(rows)
        x0, y = box[0] + (hit[1] + 0.5) * cw, box[1] + (hit[0] + 0.5) * ch
        page.evaluate("navigator.clipboard.writeText('before')")
        page.mouse.move(x0, y)
        page.mouse.down()
        page.mouse.move(x0 + len(MARK) * cw / 2, y, steps=4)
        page.mouse.move(x0 + (len(MARK) - 1) * cw, y, steps=4)
        page.mouse.up()
        got = ""
        deadline = time.time() + 8
        while time.time() < deadline:
            got = page.evaluate("navigator.clipboard.readText()")
            if got != "before":
                break
            time.sleep(0.2)
        assert got.startswith(MARK[:-1]) and got.strip() in (MARK, MARK[:-1]), repr(got)
        ok(f"a selection made in herdr's own screen (mouse drag) reaches the browser's clipboard as {got!r}")
        browser.close()
    print("all passed")


if __name__ == "__main__":
    main()
