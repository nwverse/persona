#!/usr/bin/env python3
"""End-to-end check of a scheduled task's "Push when done" in the web (#2570, docs/assistant/assistant.md "Push after running"): the editor shows
"Every time" / "Alerts only" when the hub has the task_push feature; picking the second hides "Continue the original chat", says what it
means, and saves push "asked"; the list marks the task; picking "Every time" again saves "".

    PORT=18832 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18832 npx vite --port 5332 --strictPort &)
    python3 scripts/test/web_task_push_e2e.py http://127.0.0.1:5332 --hub http://127.0.0.1:18832 [--shots <dir>]

Prints one line per check and a verdict; exits 1 on the first failure. Runs light and dark, at desktop and phone
widths; --shots saves the editor and the list for each.
"""
import argparse
import json
import os
import sys
import urllib.request

from playwright.sync_api import expect, sync_playwright

SIZES = {"wide": {"width": 1280, "height": 900}, "narrow": {"width": 390, "height": 844}}
TASK = "Agent 巡检"  # the mock hub's interval task scheduled from a conversation (fu_patrol)


def ok(msg):
    print("ok  " + msg, flush=True)


def fail(msg):
    print("FAIL " + msg, flush=True)
    sys.exit(1)


def hub_task(hub, token, tid):
    req = urllib.request.Request(hub + "/api/v1/assistant/tasks", headers={"Authorization": "Bearer " + token})
    with urllib.request.urlopen(req, timeout=10) as r:
        return next(t for t in json.load(r)["tasks"] if t["id"] == tid)


def case(browser, base, hub, token, size, theme, shots):
    tag = f"{size}/{theme}"
    urllib.request.urlopen(hub + "/mock/schedule/reset", timeout=10).read()
    ctx = browser.new_context(viewport=SIZES[size], device_scale_factor=2, has_touch=size == "narrow")
    page = ctx.new_page()
    page.set_default_timeout(15000)
    page.goto(base + "/overview?token=" + token)
    page.wait_for_function("localStorage.getItem('persona.token')")
    page.evaluate(f"localStorage.setItem('persona.theme', '{theme}')")
    page.goto(base + "/routines")
    if page.locator('[role="button"]', has_text=TASK).filter(has_text="只在提醒时推送").count():
        fail(f"{tag}: {TASK} is marked before it was switched")
    page.get_by_text(TASK, exact=True).first.click()
    group = page.get_by_role("radiogroup", name="跑完推送")
    expect(group).to_be_visible()
    every = group.get_by_role("radio", name="每次")
    asked = group.get_by_role("radio", name="只在它自己提醒时")
    expect(every).to_have_attribute("aria-checked", "true")
    ok(f"{tag}: the editor offers 跑完推送, 每次 by default")

    asked.click()
    expect(asked).to_have_attribute("aria-checked", "true")
    expect(page.get_by_text("只有这次运行判断你需要知道")).to_be_visible()
    if page.get_by_role("switch", name="接着原来的对话").count():
        fail(f"{tag}: 接着原来的对话 still shows for a task that pushes only what it sends")
    ok(f"{tag}: 只在它自己提醒时 says what it means and hides 接着原来的对话")
    page.get_by_text("只有这次运行判断你需要知道").scroll_into_view_if_needed()
    page.wait_for_timeout(300)
    if shots:
        page.screenshot(path=os.path.join(shots, f"task-push-{size}-{theme}-editor.png"))

    page.get_by_role("button", name="保存").click()
    page.wait_for_timeout(800)
    t = hub_task(hub, token, "fu_patrol")
    if t.get("push") != "asked" or t.get("session_id"):
        fail(f"{tag}: saved {t.get('push')!r}, session {t.get('session_id')!r}")
    ok(f"{tag}: saved push asked, in its own conversation")
    page.keyboard.press("Escape")
    page.wait_for_timeout(400)
    # Its own row: the mock hub has another task that already pushes only when it alerts ("project patrol").
    expect(page.locator('[role="button"]', has_text=TASK).filter(has_text="只在提醒时推送").first).to_be_visible()
    ok(f"{tag}: the list marks it")
    if shots:
        page.screenshot(path=os.path.join(shots, f"task-push-{size}-{theme}-list.png"))

    page.get_by_text(TASK, exact=True).first.click()
    page.get_by_role("radiogroup", name="跑完推送").get_by_role("radio", name="每次").click()
    page.get_by_role("button", name="保存").click()
    page.wait_for_timeout(800)
    if "push" in hub_task(hub, token, "fu_patrol"):
        fail(f"{tag}: switching back to 每次 did not save")
    ok(f"{tag}: 每次 again saves the default")
    ctx.close()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--hub", required=True)
    ap.add_argument("--token", default="mock")
    ap.add_argument("--shots")
    a = ap.parse_args()
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    with sync_playwright() as pw:
        browser = pw.chromium.launch(channel="chrome")
        try:
            for size in SIZES:
                for theme in ("light", "dark"):
                    case(browser, a.base.rstrip("/"), a.hub.rstrip("/"), a.token, size, theme, a.shots)
        finally:
            browser.close()
    print("PASS")


if __name__ == "__main__":
    main()
