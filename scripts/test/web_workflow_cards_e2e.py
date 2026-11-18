#!/usr/bin/env python3
"""End-to-end check of the web's workflow run cards (#3434): in an agent's chat each Workflow call that launched a run is
the run's card (head, where it is now or what it was for, a progress bar in parts, one block per phase); the card stays
out of the folded work while the hub lists its run, running or ended (an ended one folded in place to its head and
summary), a call whose run the hub no longer lists stays a plain row; a long run (185 agents, the hub lists 60) shows a
block's last 12 rows with "更早的 n 个没有列出" and "还有 n 个" above them; a phase with failed agents has a red ✕; counts
roll when they change and do not move on the first draw nor with reduced motion; the running bar says
"后台 workflow · <phase> · N/M+ · 还有 n 个" from the run's start, its count whole at 390 wide in English and Japanese too;
the session overview uses the same workflow cards; the sidebar row's count. Light / dark × 1280 / 390.

    MOCK_WORKFLOW_CARDS=1 PORT=18772 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18772 npx vite --port 5182 --strictPort &)
    python3 scripts/test/web_workflow_cards_e2e.py http://127.0.0.1:5182 --hub http://127.0.0.1:18772 [--shots <dir>]

The mock hub's GET /mock/workflow?step=0..3 moves the small run on (an agent done and a new one, the next phase,
completed) and pushes the session. Prints one line per check and a verdict; exits 1 on the first failure.
"""
import argparse
import json
import os
import sys
import urllib.request

from playwright.sync_api import expect, sync_playwright

CHAT = "/s/macbook/a1f3c2d4-0000-4000-8000-0000000000e1"
SIZES = {"wide": {"width": 1280, "height": 860}, "narrow": {"width": 390, "height": 844}}
OVERFLOW = "() => document.documentElement.scrollWidth - document.documentElement.clientWidth"
# each part of a card's bar: its transform and whether a width was set on it
PARTS = """(card) => [...card.querySelectorAll('[data-workflow-progress] [data-part]')].map((p) => ({
  part: p.dataset.part, transform: p.style.transform, width: p.style.width, transition: getComputedStyle(p).transitionProperty }))"""


def ok(msg):
    print("ok  " + msg, flush=True)


def fail(msg):
    print("FAIL " + msg, flush=True)
    sys.exit(1)


def step(hub, n):
    with urllib.request.urlopen(f"{hub}/mock/workflow?step={n}", timeout=10) as r:
        return json.load(r)


def center(el):
    """Brings an element to the middle of the window (not to its top, under the sticky work-group titles)."""
    el.evaluate("(e) => e.scrollIntoView({ block: 'center' })")
    el.page.wait_for_timeout(250)


def in_open(card):
    return not card.evaluate("(el) => Boolean(el.closest('[data-work-group]'))")


def run(browser, base, hub, token, size, theme, shots, reduce):
    tag = f"{size}/{theme}{'/reduced' if reduce else ''}"
    step(hub, 0)
    ctx = browser.new_context(viewport=SIZES[size], device_scale_factor=2, has_touch=size == "narrow", color_scheme=theme,
                              locale="zh-CN", reduced_motion="reduce" if reduce else "no-preference")
    page = ctx.new_page()
    page.set_default_timeout(15000)
    errors = []
    page.on("pageerror", lambda e: errors.append(str(e)))
    page.goto(base + "/overview?token=" + token)
    page.wait_for_load_state("networkidle")
    page.evaluate(f"localStorage.setItem('persona.theme', '{theme}')")
    prefix = f"workflow-cards-{size}-{theme}{'-reduced' if reduce else ''}"

    def shot(name, el=None):
        if not shots:
            return
        path = os.path.join(shots, f"{prefix}-{name}.png")
        (el or page).screenshot(path=path)

    page.goto(base + CHAT)
    main = page.locator("main")
    small = main.locator('[data-workflow-call][data-tool-use="toolu_wf_run"]')
    long = main.locator('[data-workflow-call][data-tool-use="toolu_wf_long"]')
    # the hub folded the turns' work; the chat fetches the folds holding a running run's call
    expect(small).to_be_visible()
    expect(long).to_be_visible()
    for card in (small, long):
        if not in_open(card):
            fail(f"{tag}: a running run's card is folded into the work")
    ok(f"{tag}: the running runs' cards are in the open, out of the folded work")

    expect(small.locator("[data-workflow-counts]")).to_have_text("6/9+")
    expect(small.locator("[data-workflow-status]")).to_have_text("在跑")
    expect(small.locator("[data-workflow-now]")).to_contain_text("当前 Verify：verify:")
    expect(small.locator('[data-phase="Verify"][data-phase-current]')).to_have_count(1)
    expect(small.locator('[data-phase="Verify"] [data-phase-count]')).to_have_text("2/5+")
    expect(small.locator('[data-phase="Scan"] [data-phase-count]')).to_have_text("4/4")
    expect(small.locator('[data-phase="Scan"] [data-phase-agents]')).to_have_count(0)  # all back: folded
    expect(small.locator('[data-phase="Verify"] [data-agent]')).to_have_count(5)
    if size == "wide":
        expect(small.locator('[data-phase="Verify"] [data-agent-detail]').first).to_be_visible()
    else:
        expect(small.locator('[data-phase="Verify"] [data-agent-detail]').first).to_be_hidden()
    parts = small.evaluate(PARTS)
    if [p["part"] for p in parts] != ["done", "running", "failed", "stopped"] or any(p["width"] for p in parts) \
            or not all("scaleX" in p["transform"] for p in parts) or any("width" in p["transition"] for p in parts):
        fail(f"{tag}: the bar's parts should move by transform: scaleX, never width: {parts}")
    ok(f"{tag}: the running card: 6/9+, Verify lit at 2/5+, Scan folded at 4/4, the bar by scaleX")
    center(small)
    shot("running", small)
    shot("running-page")

    # the running bar above the composer: what runs in the background, timed from the run's start
    bar = main.locator('[data-running-background="workflow"]')
    expect(bar).to_be_visible()
    expect(bar.locator("[data-running-verb]")).to_have_text("后台 workflow · Verify · 6/9+ · 还有 1 个")
    elapsed = bar.locator("[data-running-elapsed]").inner_text()
    started = page.evaluate("""async () => { const r = await fetch('/api/v1/sessions', { headers: { Authorization: 'Bearer ' + (localStorage.getItem('persona.token') || 'mock') } });
      const all = (await r.json()).sessions; const s = all.find((x) => x.session_id === '%s');
      return (Date.now() - Date.parse(s.workflows[0].started_at)) / 60000; }""" % CHAT.split("/")[-1])
    minutes = int(elapsed.split(" 分")[0]) if " 分" in elapsed else -1
    if abs(minutes - int(started)) > 1:
        fail(f"{tag}: the running bar should count from the run's start ({started:.1f} min): {elapsed}")
    ok(f"{tag}: the running bar says the background workflow, its phase and count, from the run's start")

    # the long run: 185 agents, the hub lists the newest 60 (Audit #126–180): Audit, cut into by that cap, shows its last
    # 12 rows (#169–180, the running ones among them) with what is hidden and what is not listed above them
    expect(long.locator("[data-workflow-counts]")).to_have_text("169/185+")
    audit = long.locator('[data-phase="Audit"]')
    expect(audit.locator("[data-phase-count]")).to_have_text("169/180+")
    expect(audit.locator("[data-agent]")).to_have_count(12)
    expect(audit.locator("[data-agent]").first).to_have_attribute("data-agent", "p169")
    expect(audit.locator("[data-phase-unlisted]")).to_have_text("更早的 125 个没有列出")
    expect(audit.locator("[data-phase-more]")).to_have_text("还有 43 个")
    order = audit.evaluate("""(b) => [...b.querySelectorAll('[data-phase-agents] > li')].slice(0, 3).map((li) =>
      li.dataset.agent || (li.hasAttribute('data-phase-unlisted') ? 'unlisted' : 'more'))""")
    if order != ["unlisted", "more", "p169"]:
        fail(f"{tag}: the long block should start with what is not listed, then 还有 n 个, then its rows: {order}")
    if page.evaluate(OVERFLOW) > 0:
        fail(f"{tag}: the page scrolls sideways")
    ok(f"{tag}: the long run: Audit's last 12 rows, 更早的 125 个没有列出 and 还有 43 个 above them")
    if shots:
        # the whole card in one picture: a window tall enough for it, then back
        tall = int(long.bounding_box()["height"]) + 420
        page.set_viewport_size({"width": SIZES[size]["width"], "height": max(tall, SIZES[size]["height"])})
        page.wait_for_timeout(300)
        center(long)
        shot("long", long)
        page.set_viewport_size(SIZES[size])
        page.wait_for_timeout(200)
    audit.locator("[data-phase-more]").click()
    expect(audit.locator("[data-agent]")).to_have_count(55)  # the 43 it said, and the 12
    expect(audit.locator("[data-agent]").first).to_have_attribute("data-agent", "p126")
    expect(audit.locator("[data-phase-unlisted]")).to_be_visible()
    audit.locator("[data-phase-fewer]").click()
    expect(audit.locator("[data-agent]")).to_have_count(12)
    ok(f"{tag}: 还有 43 个 opens exactly those, 收起 folds them back")

    # a call whose run is over a day old: the row it always was
    old_turn = main.locator("[data-segment]", has_text="上次的文案审计再跑一遍")
    old_turn.locator('[data-work-group] > button[aria-expanded="false"]').first.click()
    gone = old_turn.locator('[data-tool-use="toolu_wf_gone"]')
    expect(gone).to_have_count(1)
    if gone.get_attribute("data-action-row") != "Workflow":
        fail(f"{tag}: an unlisted run's call should stay a plain row")
    ok(f"{tag}: a call with no run listed is the plain row")

    # ended runs stay in the open, folded to their head and what they were for (the hub folded their turns' work: the
    # folds holding their calls were fetched too)
    for prompt, call, status, name in (("把设置页的文案过一遍", "toolu_wf_done", "completed", "done"),
                                       ("小组件的快照对比也跑一遍", "toolu_wf_failed", "failed", "failed")):
        seg = main.locator("[data-segment]", has_text=prompt)
        card = seg.locator(f'[data-workflow-call="{status}"][data-tool-use="{call}"]')
        expect(card).to_be_visible()
        if not in_open(card):
            fail(f"{tag}: the {name} run's card should stay out of the folded work")
        expect(card.locator("button[aria-expanded]").first).to_have_attribute("aria-expanded", "false")
        expect(card.locator("[data-workflow-summary]")).to_be_visible()
        if status == "failed":
            expect(card.locator("[data-workflow-error]")).to_contain_text("2 agents failed")
            expect(card.locator("[data-workflow-counts]")).to_have_text("4/6")
        else:
            expect(card.locator("[data-workflow-counts]")).to_have_text("8/8")
        center(card)
        shot(f"{name}-folded", card)
        card.locator("button[aria-expanded]").first.click()
        expect(card.locator("[data-workflow-phases]")).to_be_visible()
        if status == "failed":
            expect(card.locator('[data-phase="Snapshot"] [data-phase-mark]')).to_have_attribute("data-phase-mark", "done_failed")
            # open, the error is shown whole (one line only while folded)
            if card.locator("[data-workflow-error]").evaluate("(e) => e.scrollWidth > e.clientWidth + 1"):
                fail(f"{tag}: the open card's error is still cut off")
        page.wait_for_timeout(400)
        center(card)
        shot(f"{name}-open", card)
        ok(f"{tag}: the {name} run's card in the open: folded to its head and summary, opens to its phases")

    # a change: counts roll (not with reduced motion), a new agent fades in, then the phase moves on, then it completes
    center(small)
    shot("change-before", small)
    step(hub, 1)
    if reduce:
        expect(small.locator("[data-workflow-counts]")).to_have_text("8/10+")
        if small.locator("[data-rolling], [data-fading]").count():
            fail(f"{tag}: something moved with reduced motion")
        ok(f"{tag}: reduced motion: the new count at once, nothing rolls")
    else:
        expect(small.locator("[data-workflow-counts] [data-rolling]")).to_have_count(1, timeout=3000)
        page.wait_for_timeout(60)
        shot("change-mid", small)
        expect(small.locator("[data-workflow-counts]")).to_have_text("8/10+")
        expect(small.locator("[data-workflow-counts] [data-rolling]")).to_have_count(0)
        ok(f"{tag}: the count rolls to 8/10+ and comes to rest")
    expect(small.locator('[data-phase="Verify"] [data-agent]')).to_have_count(6)
    page.wait_for_timeout(500)
    shot("change-after", small)
    step(hub, 2)
    expect(small.locator('[data-phase="Report"][data-phase-current]')).to_have_count(1)
    expect(small.locator('[data-phase="Verify"]')).to_have_attribute("data-phase-state", "done")
    page.wait_for_timeout(600)
    shot("phase-next", small)
    ok(f"{tag}: the phase moves on to Report, Verify gets its ✓")
    step(hub, 3)
    # ended, it folds itself in place to its head and summary, where the reader was looking
    expect(small).to_have_attribute("data-workflow-call", "completed")
    if not in_open(small):
        fail(f"{tag}: an ended run's card should stay in the open")
    expect(small.locator("[data-workflow-status]")).to_have_text("完成")
    expect(small.locator("button[aria-expanded]").first).to_have_attribute("aria-expanded", "false")
    expect(small.locator("[data-workflow-counts]")).to_have_text("11/11")
    expect(small.locator("[data-workflow-summary]")).to_be_visible()
    page.wait_for_timeout(600)
    center(small)
    shot("completed", small)
    shot("completed-page")
    ok(f"{tag}: completed in place: the chip says 完成, the + goes, the card folds to its head and summary")

    # The session overview uses the same cards and can jump to the chat.
    overview = page.locator("[data-session-overview-trigger]").first
    expect(overview).to_be_visible()
    overview.click()
    panel = page.locator("[data-session-overview]")
    expect(panel.locator("[data-workflow-card]")).to_have_count(4)
    expect(panel.locator('[data-workflow-card="failed"]')).to_contain_text("2 agents failed")
    expect(panel.locator('[data-workflow-card="running"] [data-phase]').first).to_be_visible()
    page.wait_for_timeout(350)
    shot("overview")
    panel.locator('[data-workflow-card="failed"] [data-workflow-jump]').click()
    expect(panel).to_be_visible()
    expect(main.locator('[data-tool-use="toolu_wf_failed"]')).to_be_in_viewport()
    overview.click()
    expect(panel).to_have_count(0)
    ok(f"{tag}: the overview stays open when 在对话中查看 brings the call into view")

    if size == "wide":
        row = page.locator("nav a[aria-current=page], aside a[aria-current=page]").first
        expect(row.locator("[data-workflow-line]")).to_contain_text("· parity-audit · Verify")
        ok(f"{tag}: the sidebar row says the run it waits for")
        shot("sidebar", row)
    if page.evaluate(OVERFLOW) > 0:
        fail(f"{tag}: the page scrolls sideways")
    if errors:
        fail(f"{tag}: page errors: {errors[:3]}")
    ctx.close()


def bar_in(browser, base, hub, token, locale, shots):
    """The running bar at 390 wide in another language: the words before the count give way, the count stays whole."""
    step(hub, 0)
    ctx = browser.new_context(viewport=SIZES["narrow"], device_scale_factor=2, has_touch=True, color_scheme="light", locale=locale)
    page = ctx.new_page()
    page.set_default_timeout(15000)
    page.goto(base + "/overview?token=" + token)
    page.wait_for_load_state("networkidle")
    page.goto(base + CHAT)
    bar = page.locator('main [data-running-background="workflow"]')
    expect(bar).to_be_visible()
    expect(bar.locator("[data-running-counts]")).to_contain_text("6/9+")
    cut = bar.evaluate("""(b) => {
      const verb = b.querySelector('[data-running-verb]').getBoundingClientRect();
      return ['[data-running-counts]', '[data-running-more]'].map((s) => {
        const e = b.querySelector(s), r = e.getBoundingClientRect();
        return { s, cut: e.scrollWidth > e.clientWidth + 1 || r.right > verb.right + 0.5 || r.left < verb.left - 0.5 };
      }).filter((x) => x.cut).map((x) => x.s);
    }""")
    if cut:
        fail(f"narrow/{locale}: the running bar cuts {cut}")
    if shots:
        page.screenshot(path=os.path.join(shots, f"workflow-cards-narrow-light-{locale}-page.png"))
        bar.screenshot(path=os.path.join(shots, f"workflow-cards-narrow-light-{locale}-bar.png"))
    ok(f"narrow/{locale}: the running bar keeps the count and the other runs whole: {bar.locator('[data-running-verb]').inner_text()}")
    ctx.close()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--hub", required=True, help="the fake hub (MOCK_WORKFLOW_CARDS=1), for /mock/workflow")
    ap.add_argument("--shots")
    ap.add_argument("--token", default="mock")
    ap.add_argument("--only", choices=list(SIZES), help="one width only")
    a = ap.parse_args()
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    with sync_playwright() as p:
        browser = p.chromium.launch(channel="chrome")
        try:
            for size in [a.only] if a.only else SIZES:
                for theme in ("light", "dark"):
                    run(browser, a.base.rstrip("/"), a.hub.rstrip("/"), a.token, size, theme, a.shots, False)
            run(browser, a.base.rstrip("/"), a.hub.rstrip("/"), a.token, a.only or "wide", "light", a.shots, True)
            if a.only != "wide":
                for locale in ("en-US", "ja-JP"):
                    bar_in(browser, a.base.rstrip("/"), a.hub.rstrip("/"), a.token, locale, a.shots)
        finally:
            step(a.hub.rstrip("/"), 0)
            browser.close()
    print("PASS web workflow cards e2e")


if __name__ == "__main__":
    main()
