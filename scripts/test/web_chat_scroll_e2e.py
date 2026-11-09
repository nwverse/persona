#!/usr/bin/env python3
"""End-to-end check that a web chat opens at its newest message and stays there (#1340): first open, reload, switching
from the sidebar, content that keeps landing after the first paint (images, cards), and the composer growing under the
list. A reader who scrolled up is not pulled back.

The race behind #1340: the chat pins itself to the end, and when more content lands before that scroll's event fires,
the event sees the end far away and used to take it as the reader leaving. The check makes content land in a burst
(one piece per task, faster than a frame), like images decoding one after another.

Needs a running web page against the fake hub with the long conversations, and Playwright for Python (it drives the
installed Chrome, or the browser PERSONA_E2E_CHROMIUM points at):

    MOCK_TASKS=1 MOCK_LONG_ASSISTANT=1 MOCK_LONG_HISTORY=1 PORT=18792 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18792 npx vite --port 5292 --strictPort &)
    python3 scripts/test/web_chat_scroll_e2e.py http://127.0.0.1:5292 [--hub http://127.0.0.1:18792] [--shots <dir>]

Finished exchanges before the last two skip drawing while off screen (#2485): in the agent chat, what is on screen
holds still while they are drawn on the way up, the browser's find reaches their words, and a rail jump and a search
jump land on their row and stay there.

Prints one line per check and a verdict; exits 1 on the first failure. --shots saves light / dark / narrow screenshots.
--hub turns on the fake hub's paging and a slow network for one more reload (then resets it).
"""
import argparse
import json
import os
import sys
import urllib.request

from playwright.sync_api import expect, sync_playwright

SCROLL = "[data-chat-scroll]"
# The agent chat with fifteen earlier exchanges (MOCK_LONG_HISTORY).
AGENT = ("macbook", "a1f3c2d4-0000-4000-8000-000000000001")

METRICS = """() => {
  const s = document.querySelector('[data-chat-scroll]');
  if (!s || !s.firstElementChild || s.firstElementChild.childElementCount < 2) return null;
  return { gap: s.scrollHeight - s.scrollTop - s.clientHeight, top: s.scrollTop, height: s.scrollHeight, view: s.clientHeight };
}"""

# Content lands in a burst below the newest message: one piece per task, each before the next frame.
BURST = """async (n) => {
  const s = document.querySelector('[data-chat-scroll]');
  for (let i = 0; i < n; i++) {
    await new Promise((r) => setTimeout(r, 7));
    const d = document.createElement('div');
    d.style.height = '120px';
    d.setAttribute('data-e2e-burst', '');
    s.firstElementChild.appendChild(d);
  }
  await new Promise((r) => setTimeout(r, 600));
}"""

# The composer's text box: the textarea outside the list (cards in the list have their own).
COMPOSER = """() => [...document.querySelectorAll('main textarea')].filter((t) => !t.closest('[data-chat-scroll]')).pop()"""


def ok(msg):
    print("ok  " + msg, flush=True)


def fail(msg):
    print("FAIL " + msg, flush=True)
    sys.exit(1)


def settled(page, what, timeout=8000):
    """Waits until the list has content and its size holds still for half a second; returns its metrics."""
    last, still, waited = None, 0, 0
    while waited < timeout:
        m = page.evaluate(METRICS)
        if m and last and abs(m["height"] - last["height"]) < 1 and abs(m["top"] - last["top"]) < 1:
            still += 100
            if still >= 500:
                return m
        else:
            still = 0
        last = m
        page.wait_for_timeout(100)
        waited += 100
    fail(f"{what}: the list did not settle ({last})")


def at_end(page, what, short_ok=False):
    m = settled(page, what)
    if m["height"] <= m["view"] + 40 and not short_ok:
        fail(f"{what}: the conversation is too short to tell ({m})")
    if m["gap"] > 2:
        fail(f"{what}: {round(m['gap'])} px above the newest message ({m})")
    ok(f"{what}: at the newest message")
    return m


def burst_stays(page, what):
    for _ in range(3):
        page.evaluate(BURST, 6)
    at_end(page, what + ", content landing in bursts")


# The dock against the chat: the chat's bottom edge, the dock's top and bottom edges, the window.
DOCK = """() => {
  const s = document.querySelector('[data-chat-scroll]').getBoundingClientRect();
  const d = document.querySelector('[data-chat-dock]').getBoundingClientRect();
  return { chat: s.bottom, top: d.top, bottom: d.bottom, win: innerHeight };
}"""


def composer_grows(page, what):
    box = page.evaluate_handle(COMPOSER).as_element()
    if not box:
        fail(f"{what}: no composer")
    box.fill("一\n二\n三\n四\n五\n六")
    at_end(page, what + ", composer grown to six lines")
    # past the box's cap (#5375): typed key by key, each one measures the box again
    box.fill("")
    box.click()
    for i in range(24):
        page.keyboard.type(f"第 {i + 1} 行，很长的一段输入内容")
        page.keyboard.press("Shift+Enter")
    at_end(page, what + ", composer typed past its cap")
    d = page.evaluate(DOCK)
    if d["top"] < d["chat"] - 1 or d["bottom"] > d["win"] + 1:
        fail(f"{what}: the dock covers the chat or leaves the window ({d})")
    ok(f"{what}: the dock sits under the chat, inside the window")
    box.fill("")
    page.wait_for_timeout(300)


def scrolled_up_stays(page, what):
    sc = page.locator(SCROLL)
    sc.hover()
    page.mouse.wheel(0, -900)
    m = settled(page, what + " scrolled up")
    if m["gap"] < 400:
        fail(f"{what}: the wheel did not move the list up ({m})")
    page.evaluate(BURST, 6)
    after = settled(page, what + " scrolled up, then content")
    if abs(after["top"] - m["top"]) > 2:
        fail(f"{what}: a reader who scrolled up was moved from {m['top']} to {after['top']}")
    ok(f"{what}: scrolled up, content landing does not pull the reader back")
    down = page.locator("[data-jump-pill]")  # "Jump to bottom" ("New messages" once something came in)
    down.wait_for(state="visible")
    down.click()
    at_end(page, what + ", 跳到底部")


OFFSCREEN = """() => {
  const cv = [...document.querySelectorAll('[data-chat-scroll] section.seg-cv')];
  return { cv: cv.length, skipped: cv.filter((s) => !s.firstElementChild?.checkVisibility({ contentVisibilityAuto: true })).length };
}"""

# Scrolls up by script, a step a frame; per frame, how far the row at the view's middle moved beyond the step.
DRIFT = """async () => {
  const sc = document.querySelector('[data-chat-scroll]');
  const r = sc.getBoundingClientRect();
  const frame = () => new Promise((f) => requestAnimationFrame(() => f()));
  const probe = () => {
    let el = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
    while (el && el !== sc && el.getBoundingClientRect().height < 4) el = el.parentElement;
    return el;
  };
  let worst = 0, frames = 0;
  while (sc.scrollTop > 0 && frames < 100) {
    const el = probe(), top = el.getBoundingClientRect().top, was = sc.scrollTop;
    sc.scrollTop -= 120;
    const step = was - sc.scrollTop;
    await frame();
    await frame();
    if (el.isConnected) worst = Math.max(worst, Math.abs(el.getBoundingClientRect().top - top - step));
    frames++;
  }
  return { worst, frames };
}"""

# Where a row sits under the view's top, frame by frame until it has held still for 30 frames (or 4 s).
HOLDS = """async (sel) => {
  const sc = document.querySelector('[data-chat-scroll]');
  const frame = () => new Promise((f) => requestAnimationFrame(() => f()));
  const tops = [];
  for (let i = 0, still = 0; i < 240 && still < 30; i++) {
    await frame();
    const el = typeof sel === 'string' ? document.querySelector(sel) : null;
    const top = el ? el.getBoundingClientRect().top - sc.getBoundingClientRect().top : NaN;
    still = tops.length && Math.abs(top - tops[tops.length - 1]) < 0.5 ? still + 1 : 0;
    tops.push(top);
  }
  const last = tops.slice(-30);
  return { top: tops[tops.length - 1], spread: Math.max(...last) - Math.min(...last), view: sc.clientHeight };
}"""


def offscreen_exchanges(page, what):
    page.reload()
    at_end(page, what + ", reload")
    s = page.evaluate(OFFSCREEN)
    if s["cv"] < 3 or s["skipped"] < 1:
        fail(f"{what}: earlier exchanges do not skip drawing off screen ({s})")
    ok(f"{what}: {s['skipped']} of {s['cv']} earlier exchanges skip drawing off screen")

    page.locator(SCROLL).hover()
    page.mouse.wheel(0, -200)  # leave the end as a reader does (a script's scroll is not the reader leaving)
    page.wait_for_timeout(800)
    d = page.evaluate(DRIFT)
    if d["worst"] > 1:
        fail(f"{what}: scrolling up, what is on screen moved {d['worst']:.1f} px in a frame ({d})")
    ok(f"{what}: scrolling up, what is on screen moves at most {d['worst']:.1f} px a frame beyond the scroll")

    page.locator("[data-jump-pill]").click()
    at_end(page, what + ", back to the end")
    if not page.evaluate("() => !document.querySelector('[data-chat-scroll] section.seg-cv')?.firstElementChild?.checkVisibility({ contentVisibilityAuto: true })"):
        fail(f"{what}: the first exchange is drawn while off screen")
    if not page.evaluate("() => window.find('第 1 个问题') && String(getSelection()) === '第 1 个问题'"):
        fail(f"{what}: the browser's find does not reach an exchange that skipped drawing")
    page.evaluate("() => getSelection().removeAllRanges()")
    ok(f"{what}: the browser's find reaches an exchange that skipped drawing")

    tick = page.locator("[data-nav-rail] button").nth(1)
    tick.click()
    page.evaluate("""() => {
      const b = [...document.querySelectorAll('[data-chat-scroll] [data-user-bubble]')].find((el) => el.textContent.includes('第 2 个问题'));
      document.querySelectorAll('[data-e2e-row]').forEach((el) => el.removeAttribute('data-e2e-row'));
      b.closest('[data-line] > *').setAttribute('data-e2e-row', '');
    }""")
    h = page.evaluate(HOLDS, "[data-e2e-row]")
    if abs(h["top"] - 12) > 1 or h["spread"] > 1:
        fail(f"{what}: a rail jump left its question {h['top']:.1f} px under the top (12 wanted) or still moving ({h})")
    ok(f"{what}: a rail jump lands its question {h['top']:.1f} px under the top and it stays")

    page.locator("[data-jump-pill]").click()
    at_end(page, what + ", back to the end again")
    page.get_by_role("button", name="更多", exact=True).click()
    page.get_by_role("menuitem", name="会话内搜索").click()
    page.locator("[data-chat-search] input").fill("第 3 个问题")
    page.locator("[data-search-result]").first.click()
    h = page.evaluate(HOLDS, "[data-search-hit]")
    if not (0 <= h["top"] <= h["view"] - 20) or h["spread"] > 1:
        fail(f"{what}: a search jump left its hit out of view or moving ({h})")
    ok(f"{what}: a search jump brings its hit into view and it stays")
    page.locator("[data-jump-pill]").click()
    at_end(page, what + ", back to the end after the search")


def shot(page, shots, name):
    if shots:
        page.wait_for_timeout(300)
        page.screenshot(path=os.path.join(shots, name))


def set_theme(page, theme):
    page.evaluate(f"localStorage.setItem('persona.theme', '{theme}')")


def hub_get(hub, path):
    return urllib.request.urlopen(urllib.request.Request(hub + path, headers={"Authorization": "Bearer mock"}), timeout=10).read()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--hub")
    ap.add_argument("--shots")
    ap.add_argument("--token", default="mock")
    a = ap.parse_args()
    base = a.base.rstrip("/")
    req = urllib.request.Request(base + "/api/v1/assistant", headers={"Authorization": "Bearer " + a.token})
    assistant = json.load(urllib.request.urlopen(req, timeout=10))
    main_id = assistant["main"]
    other = next(c for c in assistant["conversations"] if c["session_id"] != main_id)
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)

    with sync_playwright() as p:
        exe = os.environ.get("PERSONA_E2E_CHROMIUM")
        browser = p.chromium.launch(executable_path=exe) if exe else p.chromium.launch(channel="chrome")
        try:
            wide(browser, base, a, other)
            narrow(browser, base, a)
        finally:
            browser.close()
            if a.hub:
                hub_get(a.hub, "/mock/reset")
    print("PASS web chat scroll e2e")


def wide(browser, base, a, other):
    ctx = browser.new_context(viewport={"width": 1280, "height": 820}, device_scale_factor=2)
    page = ctx.new_page()
    page.set_default_timeout(10000)
    page.goto(base + "/?token=" + a.token)
    set_theme(page, "light")

    # The main conversation: long, with a task dock and a question card under it.
    at_end(page, "wide / first open")
    page.reload()
    at_end(page, "wide / reload")
    burst_stays(page, "wide /")
    composer_grows(page, "wide /")
    scrolled_up_stays(page, "wide /")
    shot(page, a.shots, "wide-main-light.png")
    set_theme(page, "dark")
    page.reload()
    at_end(page, "wide / reload, dark")
    shot(page, a.shots, "wide-main-dark.png")
    set_theme(page, "light")

    # From the sidebar: another Persona conversation, then back to the main one.
    page.locator(f'aside a[href="/persona/{other["session_id"]}"]').click()
    page.wait_for_function(f"location.pathname === '/persona/{other['session_id']}'")
    settled(page, "wide another Persona conversation")
    ok("wide: another Persona conversation opens")
    page.locator('aside a[title="Persona 的主对话"]').click()
    page.wait_for_function("location.pathname === '/'")
    at_end(page, "wide sidebar → Persona")

    # An agent's chat.
    path = f"/s/{AGENT[0]}/{AGENT[1]}"
    link = page.locator(f'aside a[href="{path}"]')
    if link.count():
        link.first.click()
        page.wait_for_function(f"location.pathname === '{path}'")
    else:
        page.goto(base + path)
    at_end(page, "wide agent chat, from the sidebar")
    page.reload()
    at_end(page, "wide agent chat, reload")
    burst_stays(page, "wide agent chat")
    composer_grows(page, "wide agent chat")
    scrolled_up_stays(page, "wide agent chat")
    offscreen_exchanges(page, "wide agent chat")
    if a.shots:
        # without the empty blocks the bursts added
        page.reload()
        at_end(page, "wide agent chat, reload before the screenshot")
    shot(page, a.shots, "wide-agent-light.png")

    # The newest page first, more over a slow network.
    if a.hub:
        hub_get(a.hub, "/mock/paging?on=1")
        hub_get(a.hub, "/mock/delay?s=0.4")
        page.goto(base + "/")
        at_end(page, "wide / over a slow network, paged")
        page.goto(base + path)
        # its newest page (two exchanges) may fit in the view
        at_end(page, "wide agent chat over a slow network, paged", short_ok=True)
        hub_get(a.hub, "/mock/reset")
    ctx.close()


def narrow(browser, base, a):
    for theme in ("light", "dark"):
        ctx = browser.new_context(viewport={"width": 390, "height": 844}, device_scale_factor=2, has_touch=True)
        page = ctx.new_page()
        page.set_default_timeout(10000)
        page.goto(base + "/overview?token=" + a.token)
        # signed in (the link's token traded for this browser's) before leaving, or the next page is the login form
        expect(page.locator("aside")).to_be_attached()
        set_theme(page, theme)
        page.goto(base + "/")
        at_end(page, f"narrow / first open, {theme}")
        if theme == "light":
            page.reload()
            at_end(page, "narrow / reload")
            burst_stays(page, "narrow /")
            page.goto(base + f"/s/{AGENT[0]}/{AGENT[1]}")
            at_end(page, "narrow agent chat")
            burst_stays(page, "narrow agent chat")
            page.goto(base + "/")
            at_end(page, "narrow / again")
        shot(page, a.shots, f"narrow-main-{theme}.png")
        ctx.close()


if __name__ == "__main__":
    main()
