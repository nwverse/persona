#!/usr/bin/env python3
"""End-to-end check of the focus sync between herdr's own client (the terminal's "Full view") and the page
(docs/integrations/terminal.md "Focus synchronization"), against the fake hub's made-up herdr client.

Checks, one line each: choosing another session of the machine in the sidebar moves herdr's focus (one herdr_focus)
without closing or reopening the client or rebuilding xterm, and the focus coming back does not move the page; a focus
change made in this view (the hub says so) moves the page to that pane's address (an agent's page in the terminal
view, a plain shell on the "Terminal" page), the sidebar highlights and shows the row, the same client and screen stay, and the
page sends no focus back; a change made elsewhere (the computer's window, another browser) leaves the page where it is and
shows a small chip that follows it when tapped; going to another machine opens that machine's client and lets the first
go; switching to the conversation and back keeps the client; a hub without the feature sends no herdr_focus.

Needs two fake hubs and two web pages (ports below are this lane's; pick your own):

    MOCK_TERMINAL_STREAM=1 MOCK_TERMINAL_CONTROL=1 MOCK_TERMINAL_PANES=1 MOCK_TERMINAL_FULL=1 PORT=19000 python3 scripts/mock/mock_hub.py &
    MOCK_TERMINAL_STREAM=1 MOCK_TERMINAL_CONTROL=1 MOCK_TERMINAL_PANES=1 MOCK_TERMINAL_FULL=1 MOCK_TERMINAL_FULL_NOFOCUS=1 PORT=19002 python3 scripts/mock/mock_hub.py &
    for pair in 19001:19000 19003:19002; do (cd web && PERSONA_HUB_URL=http://127.0.0.1:${pair#*:} npx vite --port ${pair%:*} --strictPort &); done
    python3 scripts/test/web_herdr_focus_e2e.py --base 19001:19000 --old 19003:19002 [--shots <dir>]

(each page is told which hub it talks to by its own Vite proxy, so each argument is a pair, page port : hub port.)
Playwright for Python and a Chrome (PERSONA_E2E_CHROMIUM points at the binary, else the installed Chrome). Prints one line per check
and a verdict; exits 1 on the first failure.
"""
import argparse
import json
import os
import time
import urllib.request

from playwright.sync_api import expect, sync_playwright

HOST = "127.0.0.1"
A = "a1f3c2d4-0000-4000-8000-0000000000"  # the fake hub's agents of the machine "macbook": 01 (w1:p1), 02 (w1:p2), 06 (w1:p3), 08 (w1:p4)
S1, S2, S3, S4 = A + "01", A + "02", A + "06", A + "08"
STUDIO = A + "03"  # an agent of the machine "studio" (w2:p1)
ROW = {S1: "/t/macbook/w1/w1%3At1", S2: "/t/macbook/w1/w1%3At2", S3: "/t/macbook/w1/w1%3At3", S4: "/t/macbook/w1/w1%3At4", STUDIO: "/t/studio/w2/w2%3At1"}
SEEN = {f"macbook/{S1}": "term", f"macbook/{S2}": "term", f"macbook/{S3}": "term", f"studio/{STUDIO}": "term"}
HUBS = {}  # page port -> its hub's port


def ok(msg):
    print("ok  ", msg, flush=True)


def log(port):
    with urllib.request.urlopen(f"http://{HOST}:{HUBS[port]}/mock/herdr_client") as r:
        return json.load(r)["log"]


def sent(port, kind, since=0):
    return [m for m in log(port)[since:] if m.get("type") == kind]


def focus(port, **q):
    """Makes herdr's focus move as the node would tell the views (the fake hub's /mock/herdr_focus)."""
    qs = "&".join(f"{k}={v}" for k, v in q.items())
    with urllib.request.urlopen(f"http://{HOST}:{HUBS[port]}/mock/herdr_focus?{qs}") as r:
        return json.load(r)["told"]


def fresh(browser, port, viewport, scheme="light"):
    ctx = browser.new_context(viewport=viewport, color_scheme=scheme, locale="zh-CN", permissions=["clipboard-read", "clipboard-write"])
    ctx.add_init_script(
        f"localStorage.setItem('persona.token', 'mock'); localStorage.setItem('persona.theme', {scheme!r}); localStorage.setItem('persona.agentGrouping', 'machine'); localStorage.setItem('persona.sessionView', {json.dumps(json.dumps(SEEN))})"
    )
    page = ctx.new_page()
    page.set_default_timeout(10000)
    return ctx, page


def open_session(page, base, sid, mid="macbook"):
    page.goto(f"{base}/s/{mid}/{sid}?view=term")
    page.wait_for_selector("[data-herdr-full] .xterm")
    page.wait_for_function("document.querySelector('[data-herdr-full]') && !document.querySelector('[data-herdr-full]').textContent.includes('正在打开')")
    page.wait_for_timeout(600)


def mark_xterm(page):
    """Tags the xterm element: a rebuilt terminal would not carry it."""
    page.evaluate("document.querySelector('[data-herdr-full] .xterm').dataset.same = '1'")


def same_xterm(page):
    return page.evaluate("document.querySelector('[data-herdr-full] .xterm')?.dataset.same") == "1"


def row(page, sid):
    return page.locator(f"a[href='{ROW[sid]}']").first


def current(page, sid):
    return row(page, sid).get_attribute("aria-current") == "page"


def in_list(page, sid):
    """Whether the sidebar row is inside the part of the list that shows (the list scrolls to the open row)."""
    return page.evaluate(
        """(href) => {
            const a = document.querySelector(`a[href='${href}']`);
            let p = a.parentElement;
            while (p && !['auto', 'scroll'].includes(getComputedStyle(p).overflowY)) p = p.parentElement;
            const r = a.getBoundingClientRect(), b = p.getBoundingClientRect();
            return r.top >= b.top - 1 && r.bottom <= b.bottom + 1;
        }""",
        ROW[sid],
    )


def counts(port, since):
    return {k: len(sent(port, k, since)) for k in ("herdr_open", "herdr_close", "herdr_focus")}


def shot(page, shots, name):
    if shots:
        page.screenshot(path=os.path.join(shots, name))


def main_checks(browser, port, shots):
    base = f"http://{HOST}:{port}"
    ctx, page = fresh(browser, port, {"width": 1500, "height": 560})  # short, so the sidebar's list has rows below the fold
    start = len(log(port))
    open_session(page, base, S1)
    mark_xterm(page)
    assert counts(port, start) == {"herdr_open": 1, "herdr_close": 0, "herdr_focus": 0}, counts(port, start)
    opens = sent(port, "herdr_open", start)
    assert opens[0]["session_id"] == S1 and opens[0]["machine_id"] == "macbook", opens
    ok("the page of an agent in the terminal view opens one client on that agent")

    # ── page → herdr: another session of the machine, chosen in the sidebar ──
    t0 = time.time()
    row(page, S2).click()
    page.wait_for_function(f"location.pathname.endsWith('{S2}')")
    page.wait_for_function("document.querySelector('[data-herdr-full] .xterm') !== null")
    focused = []
    for _ in range(30):
        focused = sent(port, "herdr_focus", start)
        if focused:
            break
        page.wait_for_timeout(20)
    dt = time.time() - t0
    assert [m.get("session_id") for m in focused] == [S2], focused
    assert focused[0]["id"] == opens[0]["id"], "the focus is asked for through the open view"
    page.wait_for_timeout(700)  # the echo comes back as the page's own
    assert counts(port, start) == {"herdr_open": 1, "herdr_close": 0, "herdr_focus": 1}, counts(port, start)
    assert same_xterm(page), "xterm was rebuilt"
    assert page.url.endswith(S2), page.url
    assert current(page, S2) and not current(page, S1)
    ok(f"choosing another session in the sidebar sends one herdr_focus ({dt * 1000:.0f} ms), keeps the client and the screen, and the echo moves nothing")

    # ── herdr → page: a change made in this view ──
    start2 = len(log(port))
    assert focus(port, pane="w1:p3", session=S3, source="client", mine=1) == 1
    page.wait_for_function(f"location.pathname.endsWith('{S3}')")
    page.wait_for_timeout(500)
    assert "view=term" in page.url, page.url
    assert current(page, S3) and not current(page, S2), "the sidebar did not highlight the pane herdr moved to"
    assert in_list(page, S3), "the sidebar row is out of sight"
    assert counts(port, start2) == {"herdr_open": 0, "herdr_close": 0, "herdr_focus": 0}, counts(port, start2)
    assert same_xterm(page), "xterm was rebuilt by the follow"
    ok("a change made in this view moves the page to that agent's terminal view, highlights its row, and sends no focus back (client and screen kept)")
    shot(page, shots, "focus-follow-light.png")

    # the follow did not leave the page unable to move herdr on its own: choosing a session still sends a focus
    row(page, S1).click()
    page.wait_for_function(f"location.pathname.endsWith('{S1}')")
    page.wait_for_timeout(600)
    assert [m.get("session_id") for m in sent(port, "herdr_focus", start2)] == [S1], sent(port, "herdr_focus", start2)
    ok("after following, a sidebar choice moves herdr again")

    # a plain shell: the "Terminal" page, by its address
    start3 = len(log(port))
    focus(port, pane="w1:p9", source="client", mine=1)
    page.wait_for_function("location.pathname === '/terminal/macbook/pane%3Aw1%3Ap9'")
    page.wait_for_timeout(500)
    assert counts(port, start3) == {"herdr_open": 0, "herdr_close": 0, "herdr_focus": 0}, counts(port, start3)
    assert same_xterm(page)
    ok("a plain shell goes to the 终端 page by its pane address with the same client, and sends nothing back")

    # ── changes made elsewhere do not drag the page ──
    here = page.url
    start4 = len(log(port))
    assert focus(port, pane="w1:p4", session=S4, source="other", mine=0) == 1
    page.wait_for_timeout(500)
    assert page.url == here, "a change made on the computer moved the page"
    chip = page.get_by_role("button", name="焦点在别处")
    expect(chip).to_be_visible()
    focus(port, pane="w1:p3", session=S3, source="client", mine=0)  # another browser's view
    page.wait_for_timeout(400)
    expect(chip).to_be_visible()
    assert page.url == here, "a change made in another browser moved the page"
    assert counts(port, start4) == {"herdr_open": 0, "herdr_close": 0, "herdr_focus": 0}
    shot(page, shots, "focus-elsewhere-light.png")
    chip.click()  # follows the latest change
    page.wait_for_function(f"location.pathname.endsWith('{S3}')")
    page.wait_for_timeout(500)
    expect(page.get_by_role("button", name="焦点在别处")).to_have_count(0)
    assert counts(port, start4) == {"herdr_open": 0, "herdr_close": 0, "herdr_focus": 0} and same_xterm(page)
    ok("changes made elsewhere leave the page alone and show a chip; tapping it follows, with no focus sent back")

    # ── the conversation and back: the client stays ──
    start5 = len(log(port))
    page.get_by_role("radio", name="对话", exact=True).click() if page.get_by_role("radio", name="对话", exact=True).count() else page.get_by_text("对话", exact=True).first.click()
    page.wait_for_timeout(300)
    page.get_by_role("radio", name="终端", exact=True).click() if page.get_by_role("radio", name="终端", exact=True).count() else page.get_by_text("终端", exact=True).first.click()
    page.wait_for_timeout(1800)
    assert counts(port, start5)["herdr_open"] == 0 and counts(port, start5)["herdr_close"] == 0, counts(port, start5)
    assert same_xterm(page)
    ok("the conversation and back within a moment keeps the client")

    # ── another machine: its own client, the first one goes ──
    start6 = len(log(port))
    row(page, STUDIO).click()
    page.wait_for_function(f"location.pathname.endsWith('{STUDIO}')")
    page.wait_for_function("document.querySelector('[data-herdr-full] .xterm') !== null")
    page.wait_for_timeout(3500)
    c = counts(port, start6)
    assert c["herdr_open"] == 1 and c["herdr_close"] == 1, c
    assert sent(port, "herdr_open", start6)[0]["machine_id"] == "studio"
    ok("another machine opens its own client and lets the first one go (one client per machine)")
    ctx.close()


def look(browser, port, shots):
    """The follow and the chip in dark mode and at a phone's width."""
    base = f"http://{HOST}:{port}"
    for scheme, viewport, tag in (("dark", {"width": 1500, "height": 900}, "dark-wide"), ("light", {"width": 390, "height": 844}, "light-390"), ("dark", {"width": 390, "height": 844}, "dark-390")):
        ctx, page = fresh(browser, port, viewport, scheme)
        open_session(page, base, S1)
        focus(port, pane="w1:p3", session=S3, source="client", mine=1)
        page.wait_for_function(f"location.pathname.endsWith('{S3}')")
        page.wait_for_timeout(600)
        shot(page, shots, f"focus-follow-{tag}.png")
        focus(port, pane="w1:p4", session=S4, source="other", mine=0)
        page.wait_for_timeout(500)
        shot(page, shots, f"focus-elsewhere-{tag}.png")
        ctx.close()
    ok("screenshots: follow and the chip, dark wide, light and dark at 390 px")


def old_hub(browser, port):
    base = f"http://{HOST}:{port}"
    ctx, page = fresh(browser, port, {"width": 1500, "height": 900})
    start = len(log(port))
    open_session(page, base, S1)
    mark_xterm(page)
    row(page, S2).click()
    page.wait_for_function(f"location.pathname.endsWith('{S2}')")
    page.wait_for_timeout(900)
    assert counts(port, start) == {"herdr_open": 1, "herdr_close": 0, "herdr_focus": 0}, counts(port, start)
    assert same_xterm(page)
    ok("a hub without herdr_focus: the client and screen are kept across sessions and no herdr_focus is sent")
    ctx.close()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", required=True, help="page port:hub port")
    ap.add_argument("--old", required=True, help="page port:hub port of a hub without herdr_focus")
    ap.add_argument("--shots")
    a = ap.parse_args()
    for pair in (a.base, a.old):
        page_port, hub_port = pair.split(":")
        HUBS[int(page_port)] = int(hub_port)
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    with sync_playwright() as p:
        exe = os.environ.get("PERSONA_E2E_CHROMIUM")
        browser = p.chromium.launch(executable_path=exe) if exe else p.chromium.launch(channel="chrome")
        main_checks(browser, int(a.base.split(":")[0]), a.shots)
        look(browser, int(a.base.split(":")[0]), a.shots)
        old_hub(browser, int(a.old.split(":")[0]))
        browser.close()
    print("PASS: herdr focus sync")


if __name__ == "__main__":
    main()
