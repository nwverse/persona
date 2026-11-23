#!/usr/bin/env python3
"""End-to-end check of sorting to-dos into projects on the web (#13026, docs/assistant/todos.md "Sorting into
projects"): the To-dos list says how many open to-dos are in no project, the review sheet shows each suggestion with how
sure and why (the sure ones ticked), Move writes the ticked ones as one batch and the toast's Undo puts them back. The
page runs in English with the mock's fictional data.

Needs a running web page against the fake hub, and Playwright for Python:

    PORT=18911 MOCK_TODO_SORT=1 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18911 npx vite --port 5911 --strictPort &)
    python3 scripts/test/web_todo_sort_e2e.py http://127.0.0.1:5911 --hub http://127.0.0.1:18911 [--shots <dir>]

Prints one line per check and a verdict; exits 1 on the first failure. Runs light and dark, at desktop (1440x900) and
phone (390x844) widths; --shots saves each state there.
"""
import argparse
import json
import os
import sys
import time
import urllib.request

from playwright.sync_api import expect, sync_playwright

SIZES = {"desktop": {"width": 1440, "height": 900}, "phone": {"width": 390, "height": 844}}


def ok(msg):
    print("ok  " + msg, flush=True)


def call(hub, token, method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(hub + path, data=data, method=method,
                                 headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=10) as r:
        return json.load(r)


def todo(hub, token, tid):
    return next(t for t in call(hub, token, "GET", "/api/v1/todos")["todos"] if t["id"] == tid)


def until(check, what):
    deadline = time.time() + 10
    while time.time() < deadline:
        if check():
            return
        time.sleep(0.2)
    raise AssertionError("timed out: " + what)


def run(browser, base, hub, token, size, theme, shots):
    tag = f"{size}/{theme}"
    call(hub, token, "GET", "/mock/reset")
    ctx = browser.new_context(viewport=SIZES[size], device_scale_factor=2, has_touch=size == "phone", locale="en-US",
                              color_scheme=theme)
    page = ctx.new_page()
    page.set_default_timeout(20000)
    page.goto(base + "/overview?token=" + token)
    page.wait_for_function("localStorage.getItem('persona.token')")
    page.evaluate(f"localStorage.setItem('persona.theme', '{theme}'); localStorage.setItem('persona.language', 'en')")

    def shot(name):
        if shots:
            page.wait_for_timeout(400)
            page.screenshot(path=os.path.join(shots, f"todo-sort-{size}-{theme}-{name}.png"))

    page.goto(base + "/todos")
    line = page.locator('[data-testid="todos-unsorted"]')
    expect(line).to_contain_text("7 open to-dos aren't in a project yet")
    shot("list")
    ok(f"{tag}: the list says how many are unsorted")

    page.locator('[data-testid="todos-sort-open"]').click()
    rows = page.locator('[data-testid="todos-sort-row"]')
    expect(rows).to_have_count(6)
    expect(rows.first).to_contain_text("Its title names Garden app")
    expect(rows.first).to_contain_text("92%")
    apply = page.locator('[data-testid="todos-sort-apply"]')
    expect(apply).to_have_text("Move 5")  # the unsure icon set stays unticked
    shot("sheet")
    ok(f"{tag}: the sheet shows the suggestions, the sure ones ticked")

    apply.click()
    until(lambda: todo(hub, token, "todo_s1").get("project") == "garden-app", "moved on the hub")
    assert todo(hub, token, "todo_s4").get("temporary") is True
    undo = page.get_by_role("button", name="Undo")
    expect(undo).to_be_visible()
    expect(line).to_contain_text("2 open to-dos aren't in a project yet")
    shot("moved")
    ok(f"{tag}: Move wrote one batch, the toast offers Undo")

    call(hub, token, "PATCH", "/api/v1/todos/todo_s3", {"project": "garden-app"})  # the owner moved one since
    undo.click()
    until(lambda: "project" not in todo(hub, token, "todo_s1"), "undone on the hub")
    assert todo(hub, token, "todo_s3").get("project") == "garden-app"
    expect(page.get_by_text("Put 4 back; 1 changed since and stayed")).to_be_visible()
    shot("undone")
    ok(f"{tag}: Undo put back what was unchanged and kept the one moved since")
    ctx.close()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--hub", required=True)
    ap.add_argument("--token", default="mock")
    ap.add_argument("--shots")
    ap.add_argument("--channel", default="")
    a = ap.parse_args()
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    with sync_playwright() as p:
        browser = p.chromium.launch(channel=a.channel or None)
        try:
            for size in SIZES:
                for theme in ("light", "dark"):
                    run(browser, a.base, a.hub, a.token, size, theme, a.shots)
        except Exception as e:  # noqa: BLE001 — one line and a verdict, as the other e2e scripts
            print(f"FAIL {e}", flush=True)
            sys.exit(1)
        finally:
            browser.close()
    print("PASS", flush=True)


if __name__ == "__main__":
    main()
