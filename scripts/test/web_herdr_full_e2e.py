#!/usr/bin/env python3
"""End-to-end check of the terminal's "Full view" (herdr's own client in the browser, docs/integrations/terminal.md "Full herdr UI")
against the fake hub's made-up herdr client, and screenshots of both surfaces and of the switch between them.

Checks, one line each: a terminal opens as "Full view" by default and the sidebar / tab bar / panes are drawn; what is
acknowledged and what the page answers when herdr asks about the colour scheme; typing and the key bar reach the client;
a copy the client makes (OSC 52) lands on the browser's clipboard; the header's switch goes to "Single pane" and back, the client
is let go on the way (herdr_close) and the choice is kept; Settings chooses the default; reduced motion cuts the transition;
resizing the panel is told to the client; a client that ends can be reconnected; a hub or machine that cannot show it
falls back to "Single pane" and says why; a hub without the feature has no switch.

Needs the fake hub with terminals and the full client (MOCK_TERMINAL_FULL=1), a web page against it and Playwright for
Python (the installed Chrome, or the browser PERSONA_E2E_CHROMIUM points at):

    MOCK_TERMINAL_STREAM=1 MOCK_TERMINAL_CONTROL=1 MOCK_TERMINAL_PANES=1 MOCK_TERMINAL_FULL=1 PORT=18860 python3 scripts/mock/mock_hub.py &
    MOCK_TERMINAL_STREAM=1 MOCK_TERMINAL_CONTROL=1 MOCK_TERMINAL_PANES=1 MOCK_TERMINAL_FULL=1 MOCK_TERMINAL_FULL_END_AFTER=2 PORT=18862 python3 scripts/mock/mock_hub.py &
    MOCK_TERMINAL_STREAM=1 MOCK_TERMINAL_CONTROL=1 MOCK_TERMINAL_PANES=1 MOCK_TERMINAL_FULL=1 MOCK_TERMINAL_FULL_REFUSE=unsupported PORT=18863 python3 scripts/mock/mock_hub.py &
    MOCK_TERMINAL_STREAM=1 MOCK_TERMINAL_CONTROL=1 MOCK_TERMINAL_PANES=1 PORT=18864 python3 scripts/mock/mock_hub.py &
    for pair in 18861:18860 18865:18862 18866:18863 18867:18864; do (cd web && PERSONA_HUB_URL=http://127.0.0.1:${pair#*:} npx vite --port ${pair%:*} --strictPort &); done
    python3 scripts/test/web_herdr_full_e2e.py --base 18861:18860 --ended 18865:18862 --refused 18866:18863 --old 18867:18864 [--shots <dir>]

(each page is told which hub it talks to by its own Vite proxy, so each argument is a pair, page port : hub port.)
Prints one line per check and a verdict; exits 1 on the first failure.
"""
import argparse
import json
import os
import time
import urllib.request

from playwright.sync_api import expect, sync_playwright

SHELL = "pane%3Aw1%3Ap2"  # a shell of the "persona" workspace on the fake hub's first machine
HOST = "127.0.0.1"
MODE_KEYS = ("persona.terminal.mode", "persona.terminal.defaultMode")


def ok(msg):
    print("ok  ", msg, flush=True)


HUBS = {}  # page port -> its hub's port


def log(page_port):
    """What the fake hub was told about full views, in order."""
    with urllib.request.urlopen(f"http://{HOST}:{HUBS[page_port]}/mock/herdr_client") as r:
        return json.load(r)["log"]


def sent(page_port, kind, since=0):
    return [m for m in log(page_port)[since:] if m.get("type") == kind]


def fresh(browser, port, viewport, scheme="light", touch=False, reduced=False):
    ctx = browser.new_context(
        viewport=viewport,
        color_scheme=scheme,
        reduced_motion="reduce" if reduced else "no-preference",
        device_scale_factor=2,
        has_touch=touch,
        is_mobile=touch,
        permissions=["clipboard-read", "clipboard-write"],
    )
    ctx.add_init_script(f"localStorage.setItem('persona.token', 'mock'); localStorage.setItem('persona.theme', {scheme!r})")
    page = ctx.new_page()
    page.set_default_timeout(10000)
    return ctx, page


def screen_text(page):
    """The text xterm shows, row by row, of the surface on show (not of a terminal kept hidden under it)."""
    return page.evaluate(
        """() => [...document.querySelectorAll('[data-surface="shown"] .xterm-rows')]
            .filter((el) => !el.closest('.invisible, [inert]'))
            .flatMap((el) => [...el.children].map((r) => r.textContent)).join('\\n')"""
    )


def wait_text(page, needle, what, timeout=10000):
    deadline = time.time() + timeout / 1000
    while time.time() < deadline:
        if needle in screen_text(page):
            return
        time.sleep(0.1)
    raise AssertionError(f"{what}: {needle!r} not in\n{screen_text(page)}")


def radio(page, name):
    return page.get_by_role("radio", name=name, exact=True)


def open_terminal(page, base):
    page.goto(f"{base}/terminal/server/{SHELL}")


def mode_is(page, name):
    expect(radio(page, name)).to_have_attribute("aria-checked", "true")


def shot(page, shots, name):
    if shots:
        page.screenshot(path=os.path.join(shots, name))


def transition_shot(page, shots, name):
    """Catches the middle of the switch (a screenshot takes about as long as the switch): taken right after a click, it
    counts when both surfaces were still on the page just after it; the other way is tried when it was too late."""
    for _ in range(10):
        target = "单窗格" if radio(page, "单窗格").get_attribute("aria-checked") == "false" else "完整界面"
        radio(page, target).click()
        page.screenshot(path=os.path.join(shots, name), scale="css")
        both = page.locator("[data-surface]").count() == 2
        page.wait_for_function("document.querySelectorAll('[data-surface]').length === 1")
        if both:
            return True
        page.wait_for_timeout(300)
    return False


def main_checks(browser, base_port, shots):
    base = f"http://{HOST}:{base_port}"
    ctx, page = fresh(browser, base_port, {"width": 1500, "height": 900})
    start = len(log(base_port))
    open_terminal(page, base)

    # ── the default is "Full view" and it is herdr's own screen ──
    mode_is(page, "完整界面")
    wait_text(page, "spaces", "the sidebar")
    wait_text(page, "1 main", "the tab bar")
    wait_text(page, "me@host ~/persona $", "the shell pane")
    ok("a terminal opens as 完整界面 by default: sidebar, tab bar and panes are drawn")
    opens = sent(base_port, "herdr_open", start)
    assert len(opens) == 1 and opens[0]["machine_id"] == "server" and opens[0]["session_id"] == "pane:w1:p2" and opens[0]["focus"] is True, opens
    assert opens[0]["cols"] > 60 and opens[0]["rows"] > 10, opens
    ok(f"herdr_open names the machine, the pane to show and a {opens[0]['cols']}×{opens[0]['rows']} grid")
    shot(page, shots, "full-wide-light.png")

    # the page acknowledges what xterm parsed (cumulative bytes), and answers what the client asked about the colours
    deadline = time.time() + 5
    while time.time() < deadline and not sent(base_port, "herdr_ack", start):
        time.sleep(0.1)
    acks = [m["bytes"] for m in sent(base_port, "herdr_ack", start)]
    assert acks and all(b > 0 for b in acks) and acks == sorted(acks), acks
    ok(f"herdr_ack carries the bytes parsed so far ({acks[-1]}), never going back")
    replies = "".join(m.get("data", "") for m in sent(base_port, "herdr_input", start))
    assert "\x1b[?997;2n" in replies, repr(replies)  # light: CSI ? 997 ; 2 n, the answer to CSI ? 996 n
    ok("the page answers CSI ? 996 n with the colour scheme (light = 2)")

    # ── typing and the key bar reach the client ──
    page.locator('[data-surface="shown"] .xterm').click()
    page.keyboard.type("echo hi")
    wait_text(page, "$ echo hi", "typing in the shell pane")
    typed = "".join(m.get("data", "") for m in sent(base_port, "herdr_input", start))
    assert "echo hi" in typed, repr(typed)
    ok("what is typed goes to the client as herdr_input and shows in its pane")

    # ── a copy made by herdr (OSC 52) reaches the clipboard ──
    page.evaluate("navigator.clipboard.writeText('before')")
    page.keyboard.press("Control+y")
    deadline = time.time() + 5
    got = ""
    while time.time() < deadline:
        got = page.evaluate("navigator.clipboard.readText()")
        if got != "before":
            break
        time.sleep(0.1)
    assert got == "copied by herdr", got
    ok("a copy the client makes (OSC 52) is on the browser's clipboard")

    # ── the switch: "Single pane" and back, the client let go on the way ──
    n = len(log(base_port))
    radio(page, "单窗格").click()
    page.wait_for_timeout(60)  # mid-transition: both surfaces are on the page
    both = page.locator("[data-surface]").count()
    assert both == 2, both
    mode_is(page, "单窗格")
    page.wait_for_function("document.querySelectorAll('[data-surface]').length === 1")
    assert page.evaluate("localStorage.getItem('persona.terminal.mode')") == "pane"
    assert len(sent(base_port, "herdr_close", n)) == 1, log(base_port)[n:]
    expect(page.locator('[data-surface="shown"] .xterm')).to_be_visible()
    ok("the switch slides 单窗格 in over 完整界面, lets the client go (herdr_close) and remembers the choice")
    shot(page, shots, "pane-wide-light.png")

    n = len(log(base_port))
    radio(page, "完整界面").click()
    wait_text(page, "spaces", "the sidebar again")
    assert len(sent(base_port, "herdr_open", n)) == 1
    assert page.evaluate("localStorage.getItem('persona.terminal.mode')") == "full"
    ok("and back: a new client is opened on a clean screen")

    # ── the shortcut ──
    page.keyboard.press("Control+Alt+f")
    mode_is(page, "单窗格")
    page.keyboard.press("Control+Alt+f")
    mode_is(page, "完整界面")
    wait_text(page, "spaces", "the client after the shortcut")
    ok("Ctrl+Alt+F toggles the two")

    # ── resize is told ──
    n = len(log(base_port))
    page.set_viewport_size({"width": 1100, "height": 700})
    deadline = time.time() + 5
    while time.time() < deadline and not sent(base_port, "herdr_resize", n):
        time.sleep(0.1)
    sizes = sent(base_port, "herdr_resize", n)
    assert sizes and sizes[-1]["cols"] < opens[0]["cols"], (sizes, opens[0])
    ok(f"resizing the panel tells the client its new size ({sizes[-1]['cols']}×{sizes[-1]['rows']})")
    page.set_viewport_size({"width": 1500, "height": 900})

    # ── leaving the page lets the client go ──
    n = len(log(base_port))
    page.get_by_role("link", name="设置").first.click()
    deadline = time.time() + 5
    while time.time() < deadline and not sent(base_port, "herdr_close", n):
        time.sleep(0.1)
    assert sent(base_port, "herdr_close", n), log(base_port)[n:]
    ok("leaving the terminal for another page lets the client go (herdr_close)")
    page.goto(f"{base}/terminal/server/{SHELL}")
    wait_text(page, "spaces", "a client to drop")
    n = len(log(base_port))
    page.goto(f"{base}/settings")  # the connection itself goes: the hub lets go of what the page had open
    deadline = time.time() + 5
    while time.time() < deadline and not sent(base_port, "herdr_dropped", n):
        time.sleep(0.1)
    assert sent(base_port, "herdr_dropped", n), log(base_port)[n:]
    ok("closing the page drops its client with the connection")

    # ── Settings chooses the default ──
    expect(page.get_by_role("radio", name="完整界面", exact=True)).to_have_attribute("aria-checked", "true")
    page.get_by_role("radio", name="单窗格", exact=True).click()
    assert page.evaluate("localStorage.getItem('persona.terminal.defaultMode')") == "pane"
    page.evaluate("localStorage.removeItem('persona.terminal.mode')")
    page.evaluate("window.sessionStorage.clear()")
    page.reload()  # a new page life: no terminal has chosen its own mode
    open_terminal(page, base)
    mode_is(page, "单窗格")
    ok("设置 › 终端界面 › 单窗格: a terminal opens as a single pane")
    page.goto(f"{base}/settings")
    page.get_by_role("radio", name="记住上次", exact=True).click()
    page.goto(f"{base}/terminal/server/{SHELL}")
    mode_is(page, "完整界面")  # nothing remembered yet: "Full view"
    wait_text(page, "spaces", "full before a choice is remembered")
    radio(page, "单窗格").click()
    page.wait_for_function("document.querySelectorAll('[data-surface]').length === 1")
    page.reload()
    mode_is(page, "单窗格")
    ok("设置 › 记住上次: the last choice of the switch is where the next terminal starts")
    page.goto(f"{base}/settings")
    page.get_by_role("radio", name="完整界面", exact=True).click()
    assert page.evaluate("localStorage.getItem('persona.terminal.defaultMode')") is None
    ok("choosing 完整界面 in 设置 is the default again, and writes nothing")
    ctx.close()

    # ── dark, and a phone ──
    for scheme in ("dark", "light"):
        for name, vp, touch in (("wide", {"width": 1500, "height": 900}, False), ("390", {"width": 390, "height": 844}, True)):
            ctx, page = fresh(browser, base_port, vp, scheme, touch)
            open_terminal(page, base)
            mode_is(page, "完整界面")
            wait_text(page, "me@host ~/persona $", f"{scheme} {name}")
            if name == "390":
                assert "spaces" not in screen_text(page)  # herdr's single-column layout at a phone's width
                n = len(log(base_port))
                page.get_by_role("button", name="Esc", exact=True).click()
                page.get_by_role("button", name="Tab", exact=True).click()
                page.wait_for_timeout(200)
                keys = [m.get("data") for m in sent(base_port, "herdr_input", n)]
                assert keys == ["\x1b", "\t"], keys
                ok("on a phone's width: herdr's single-column layout, and the key bar sends Esc and Tab to the client")
            shot(page, shots, f"full-{name}-{scheme}.png")
            if shots:
                assert transition_shot(page, shots, f"transition-{name}-{scheme}.png"), "never caught the switch in the middle"
            if radio(page, "完整界面").get_attribute("aria-checked") == "true":
                radio(page, "单窗格").click()
                page.wait_for_function("document.querySelectorAll('[data-surface]').length === 1")
            shot(page, shots, f"pane-{name}-{scheme}.png")
            ctx.close()
    ok("screenshots: full, single pane and mid-transition, wide and 390px, dark and light")

    # ── reduced motion ──
    ctx, page = fresh(browser, base_port, {"width": 1500, "height": 900}, reduced=True)
    open_terminal(page, base)
    mode_is(page, "完整界面")
    wait_text(page, "spaces", "reduced motion")
    radio(page, "单窗格").click()
    page.wait_for_function("document.querySelectorAll('[data-surface]').length === 1", timeout=300)
    ok("with reduced motion the other surface is gone at once (no slide kept on the page)")
    ctx.close()


def embedded_checks(browser, base_port, shots):
    """An agent's page shows its terminal in place (?view=term, #2445): one header, the panel's controls in a toolbar under it."""
    base = f"http://{HOST}:{base_port}"
    for scheme, vp, touch, name in (("light", {"width": 1500, "height": 900}, False, "wide"), ("dark", {"width": 390, "height": 844}, True, "390")):
        ctx, page = fresh(browser, base_port, vp, scheme, touch)
        page.goto(f"{base}/s/macbook/a1f3c2d4-0000-4000-8000-000000000001?view=term")
        mode_is(page, "完整界面")
        wait_text(page, "me@host ~/persona $", "the agent's terminal view")
        assert page.get_by_role("radiogroup", name="终端界面").count() == 1
        shot(page, shots, f"in-place-{name}-{scheme}.png")
        ctx.close()
    ok("an agent's in-place terminal view: the panel's 单窗格 / 完整界面 switch sits in a toolbar under the page's own header")


def ended_checks(browser, port, shots):
    base = f"http://{HOST}:{port}"
    ctx, page = fresh(browser, port, {"width": 1300, "height": 800})
    open_terminal(page, base)
    wait_text(page, "spaces", "the first client")
    page.get_by_text("herdr client exited: 0").first.wait_for(timeout=8000) if False else None
    wait_text(page, "herdr client exited: 0", "the reason, dim, in the terminal", 8000)
    expect(page.get_by_role("button", name="重新连接", exact=True).first).to_be_visible()
    shot(page, shots, "ended-wide-light.png")
    n = len(log(port))
    page.get_by_role("button", name="重新连接", exact=True).first.click()
    wait_text(page, "spaces", "after reconnecting")
    assert len(sent(port, "herdr_open", n)) == 1
    ok("a client that ends leaves its reason dim in the terminal and a 重新连接 that opens a new one")
    ctx.close()


def refused_checks(browser, port, shots):
    base = f"http://{HOST}:{port}"
    ctx, page = fresh(browser, port, {"width": 1300, "height": 800})
    open_terminal(page, base)
    mode_is(page, "单窗格")
    expect(page.get_by_text("完整界面没能打开")).to_be_visible()
    shot(page, shots, "fallback-wide-light.png")
    ok("a machine that cannot run the client falls back to 单窗格 and says why")
    n = len(sent(port, "herdr_open"))
    page.locator('section[aria-label="窗格"] a[href="/terminal/server/pane%3Aw2%3Ap1"]').click()  # in the page: what it learnt stays
    mode_is(page, "单窗格")
    page.wait_for_timeout(500)
    assert len(sent(port, "herdr_open")) == n, "tried again for another terminal of the same machine"
    ok("and it is not tried again for the machine's other terminals")
    ctx.close()


def old_checks(browser, port):
    base = f"http://{HOST}:{port}"
    ctx, page = fresh(browser, port, {"width": 1300, "height": 800})
    open_terminal(page, base)
    expect(page.locator('section[aria-label="终端"] [role=radio]').first).to_be_visible()
    assert page.get_by_role("radiogroup", name="终端界面").count() == 0
    page.goto(f"{base}/settings")
    assert page.get_by_role("radiogroup", name="终端界面").count() == 0
    ok("a hub without the feature shows no switch and no setting, and the terminal is the single pane it was")
    ctx.close()


def main():
    ap = argparse.ArgumentParser()
    pair = lambda v: tuple(int(x) for x in v.split(":"))
    ap.add_argument("--base", type=pair, required=True, help="page:hub ports; the hub has the made-up full client")
    ap.add_argument("--ended", type=pair, help="page:hub ports; the hub ends each view after a few seconds")
    ap.add_argument("--refused", type=pair, help="page:hub ports; the hub refuses each view as unsupported")
    ap.add_argument("--old", type=pair, help="page:hub ports; the hub has no full client")
    ap.add_argument("--shots")
    a = ap.parse_args()
    for page_port, hub_port in filter(None, (a.base, a.ended, a.refused, a.old)):
        HUBS[page_port] = hub_port
    a.base, a.ended, a.refused, a.old = (x[0] if x else None for x in (a.base, a.ended, a.refused, a.old))
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    with sync_playwright() as p:
        exe = os.environ.get("PERSONA_E2E_CHROMIUM")
        browser = p.chromium.launch(executable_path=exe) if exe else p.chromium.launch(channel="chrome")
        main_checks(browser, a.base, a.shots)
        embedded_checks(browser, a.base, a.shots)
        if a.ended:
            ended_checks(browser, a.ended, a.shots)
        if a.refused:
            refused_checks(browser, a.refused, a.shots)
        if a.old:
            old_checks(browser, a.old)
        browser.close()
    print("all passed")


if __name__ == "__main__":
    main()
