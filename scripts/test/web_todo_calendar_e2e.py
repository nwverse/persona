#!/usr/bin/env python3
"""End-to-end check of the to-dos' calendar on the web (#2029, docs/assistant/todos.md "Calendar"): List | Calendar
switches and is kept in the address, the week / month / day show the mock's to-dos (due today at 10:00, 16:00 and two
at 18:00), a block opens the to-do beside the calendar, a click on the empty axis makes a to-do due there, a to-do
dragged to another time, onto the All day row or to another day of the month is saved there (#2045; a mouse at desktop
width, a held touch at a phone's; Escape and a failed save put it back), and the bar under the calendar goes back to the
list. On a phone's width the week is the day. The page runs in zh-CN, as the other
web e2e checks, so the selectors use the Chinese labels.

Needs a running web page against the fake hub, and Playwright for Python:

    PORT=18899 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18899 npx vite --port 5899 --strictPort &)
    python3 scripts/test/web_todo_calendar_e2e.py http://127.0.0.1:5899 --hub http://127.0.0.1:18899 [--shots <dir>]

Prints one line per check and a verdict; exits 1 on the first failure. Runs light and dark, at desktop and phone
widths; --shots saves each state there. --channel chrome / msedge uses an installed browser instead of Playwright's
own Chromium.
"""
import argparse
import json
import os
import sys
import time
import urllib.request

from playwright.sync_api import expect, sync_playwright

SIZES = {"wide": {"width": 1280, "height": 860}, "narrow": {"width": 390, "height": 844}}
HOUR = 48  # px an hour on the axis (views/TodoCalendar.tsx)


def ok(msg):
    print("ok  " + msg, flush=True)


def call(hub, token, method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(hub + path, data=data, method=method,
                                 headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json"})
    return json.load(urllib.request.urlopen(req, timeout=10))


def todos(hub, token):
    return call(hub, token, "GET", "/api/v1/todos")["todos"]


def until(check, what):
    deadline = time.time() + 10
    while time.time() < deadline:
        if check():
            return
        time.sleep(0.2)
    raise AssertionError("timed out: " + what)


def local_time(page, iso):
    """An ISO moment as the page's clock reads it: "HH:MM"."""
    return page.evaluate("(s) => { const d = new Date(s); return String(d.getHours()).padStart(2, '0') + ':' + String(d.getMinutes()).padStart(2, '0') }", iso)


def today_plus(page, days):
    """A day as the hub writes due_date, `days` after the page's today."""
    return page.evaluate("(n) => { const d = new Date(); d.setDate(d.getDate() + n); return d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0') }", days)


def drag(page, cdp, size, frm, to, hold=True, between=None):
    """Drags from frm to to (x, y): a mouse at desktop width, a held touch (CDP touch events) at a phone's. between runs
    with the pointer at `to`, before it lets go."""
    if size == "wide":
        page.mouse.move(*frm)
        page.mouse.down()
        for i in range(1, 9):
            page.mouse.move(frm[0] + (to[0] - frm[0]) * i / 8, frm[1] + (to[1] - frm[1]) * i / 8)
        if between:
            between()
        page.mouse.up()
        return
    def touch(kind, x, y):
        points = [] if kind == "touchEnd" else [{"x": x, "y": y, "id": 1}]
        cdp.send("Input.dispatchTouchEvent", {"type": kind, "touchPoints": points})
    touch("touchStart", *frm)
    if hold:
        page.wait_for_timeout(600)  # held still: a drag, not a swipe
    for i in range(1, 9):
        touch("touchMove", frm[0] + (to[0] - frm[0]) * i / 8, frm[1] + (to[1] - frm[1]) * i / 8)
    if between:
        between()
    touch("touchEnd", *to)


def drag_checks(page, hub, token, size, theme, shot, tag):
    """#2045: on the day's axis, onto the All day row, in the month; Escape and a failed save leave it where it was."""
    expect(page.get_by_role("dialog")).to_have_count(0)  # New to-do has closed
    cdp = page.context.new_cdp_session(page)
    original = next(t for t in todos(hub, token) if t["id"] == "todo_weather")["due_at"]
    weather = page.locator('[data-testid="todo-calendar-entry"][data-todo-id="todo_weather"]')
    ghost = page.locator('[data-testid="todo-calendar-drop"]')
    page.evaluate(f"document.querySelector('[data-testid=\"todo-calendar-axis\"] .overflow-y-auto').scrollTop = {9 * HOUR}")
    column = page.locator('[data-testid="todo-calendar-column"]').first
    box, col = weather.bounding_box(), column.bounding_box()
    frm = (box["x"] + box["width"] / 2, box["y"] + 10)
    to = (frm[0], col["y"] + 14.25 * HOUR + 10 + 4)  # its top edge just past 14:15

    def seen():
        expect(ghost).to_be_visible()
        expect(page.locator('[data-testid="todo-calendar-drop-time"]')).to_have_text("14:15")
        shot("drag")
    drag(page, cdp, size, frm, to, between=seen)
    until(lambda: local_time(page, next(t for t in todos(hub, token) if t["id"] == "todo_weather")["due_at"]) == "14:15", "due at 14:15 on the hub")
    expect(ghost).to_have_count(0)
    expect(weather).to_be_visible()
    assert abs(weather.bounding_box()["y"] - (col["y"] + 14.25 * HOUR + 1)) < 3, weather.bounding_box()
    assert "id=todo_weather" not in page.url, "the drag did not open the to-do: " + page.url
    expect(page.get_by_role("dialog")).to_have_count(0)  # nor offer a new one where it ended
    ok(f"{tag}: a to-do dragged on the axis is due at the quarter hour it lands on")

    # a touch that moves at once is the page's: it scrolls, nothing moves
    if size == "narrow":
        box = weather.bounding_box()
        drag(page, cdp, size, (box["x"] + 20, box["y"] + 10), (box["x"] + 20, box["y"] - 3 * HOUR), hold=False)
        page.wait_for_timeout(300)
        assert local_time(page, next(t for t in todos(hub, token) if t["id"] == "todo_weather")["due_at"]) == "14:15"
        ok(f"{tag}: a swipe that starts on a to-do still scrolls")
        page.evaluate(f"document.querySelector('[data-testid=\"todo-calendar-axis\"] .overflow-y-auto').scrollTop = {12 * HOUR}")

    # a failed save says so and puts it back
    page.route("**/api/v1/todos/todo_weather", lambda route: route.fulfill(status=500, content_type="application/json", body='{"error": "the hub could not save it"}')
               if route.request.method == "PATCH" else route.fallback())
    box, col = weather.bounding_box(), column.bounding_box()
    drag(page, cdp, size, (box["x"] + box["width"] / 2, box["y"] + 10), (box["x"] + box["width"] / 2, col["y"] + 17 * HOUR + 10))
    expect(page.get_by_text("没能改期")).to_be_visible()
    shot("drag-failed")
    expect(weather).to_be_visible()
    assert abs(weather.bounding_box()["y"] - (col["y"] + 14.25 * HOUR + 1)) < 3, "back where it was"
    page.unroute("**/api/v1/todos/todo_weather")
    ok(f"{tag}: a failed save says so and the to-do goes back")

    if size == "wide":  # Escape (a keyboard's)
        box = weather.bounding_box()
        drag(page, cdp, size, (box["x"] + 20, box["y"] + 10), (box["x"] + 20, box["y"] + 3 * HOUR), between=lambda: page.keyboard.press("Escape"))
        page.wait_for_timeout(300)
        assert local_time(page, next(t for t in todos(hub, token) if t["id"] == "todo_weather")["due_at"]) == "14:15"
        assert "id=todo_weather" not in page.url, page.url
        ok(f"{tag}: Escape puts it back")

    # onto the All day row: due that day
    all_day = page.locator('[data-testid="todo-calendar-all-day"]').first.bounding_box()
    box = weather.bounding_box()
    drag(page, cdp, size, (box["x"] + 20, box["y"] + 10), (all_day["x"] + all_day["width"] / 2, all_day["y"] + all_day["height"] / 2))
    until(lambda: next(t for t in todos(hub, token) if t["id"] == "todo_weather").get("due_date") == today_plus(page, 0), "due today, all day")
    ok(f"{tag}: onto the All day row it is due that day")

    # the month: onto another day, as a whole
    page.get_by_role("radio", name="月", exact=True).click()
    expect(page.locator('[data-testid="todo-calendar-month"]')).to_be_visible()
    target = today_plus(page, 2)
    cells = page.locator('[data-testid="todo-calendar-month-day"]')
    stamp = page.evaluate("(k) => { const [y, m, d] = k.split('-').map(Number); return new Date(y, m - 1, d).getTime() }", target)
    cell = page.locator(f'[data-testid="todo-calendar-month-day"][data-day="{stamp}"]')
    if cell.count() == 0:  # the month ends before it: the day before instead
        target = today_plus(page, -2)
        stamp = page.evaluate("(k) => { const [y, m, d] = k.split('-').map(Number); return new Date(y, m - 1, d).getTime() }", target)
        cell = page.locator(f'[data-testid="todo-calendar-month-day"][data-day="{stamp}"]')
    assert cells.count() >= 28
    chip = page.locator('[data-testid="todo-calendar-month"] [data-testid="todo-calendar-entry"][data-todo-id="todo_weather"]')
    box, dest = chip.bounding_box(), cell.bounding_box()

    def over():
        expect(cell.locator('[data-testid="todo-calendar-drop"]')).to_be_visible()
        shot("drag-month")
    drag(page, cdp, size, (box["x"] + box["width"] / 2, box["y"] + box["height"] / 2), (dest["x"] + dest["width"] / 2, dest["y"] + dest["height"] - 8), between=over)
    until(lambda: next(t for t in todos(hub, token) if t["id"] == "todo_weather").get("due_date") == target, "due on the day it was dropped on")
    expect(cell.locator('[data-testid="todo-calendar-entry"][data-todo-id="todo_weather"]')).to_be_visible()
    ok(f"{tag}: in the month it moves to the day it is dropped on")

    call(hub, token, "PATCH", "/api/v1/todos/todo_weather", {"due_at": original})
    page.get_by_role("radio", name="日", exact=True).click()
    expect(page.locator('[data-testid="todo-calendar"]')).to_have_attribute("data-mode", "day")
    cdp.detach()


def run(browser, base, hub, token, size, theme, shots):
    tag = f"{size}/{theme}"
    ctx = browser.new_context(viewport=SIZES[size], device_scale_factor=2, has_touch=size == "narrow", locale="zh-CN",
                              color_scheme=theme)
    page = ctx.new_page()
    page.set_default_timeout(20000)
    page.goto(base + "/overview?token=" + token)
    page.wait_for_function("localStorage.getItem('persona.token')")
    page.evaluate(f"localStorage.setItem('persona.theme', '{theme}'); localStorage.removeItem('persona.todos.view'); localStorage.removeItem('persona.todos.calendar.mode')")

    def shot(name):
        if shots:
            page.wait_for_timeout(300)
            page.screenshot(path=os.path.join(shots, f"todo-calendar-{size}-{theme}-{name}.png"))

    # List | Calendar: the address keeps the calendar, its mode and its day.
    page.goto(base + "/todos")
    page.get_by_role("radio", name="日历", exact=True).click()
    cal = page.locator('[data-testid="todo-calendar"]')
    expect(cal).to_be_visible()
    assert "view=calendar" in page.url, page.url
    # a phone's width has no week: the day instead
    expect(cal).to_have_attribute("data-mode", "week" if size == "wide" else "day")
    weather = page.locator('[data-testid="todo-calendar-entry"][data-todo-id="todo_weather"]')
    blog = page.locator('[data-testid="todo-calendar-entry"][data-todo-id="todo_blog"]')
    expect(weather).to_be_visible()
    expect(blog).to_be_visible()
    shot("week" if size == "wide" else "day")
    ok(f"{tag}: the calendar shows today's to-dos, the two at 18:00 side by side")

    page.get_by_role("radio", name="月", exact=True).click()
    expect(page.locator('[data-testid="todo-calendar-month"]')).to_be_visible()
    assert "mode=month" in page.url, page.url
    shot("month")
    page.get_by_role("radio", name="日", exact=True).click()
    expect(cal).to_have_attribute("data-mode", "day")
    if size == "wide":
        shot("day")
    ok(f"{tag}: month and day")

    # A block opens its to-do beside the calendar (on a phone: instead of it); closing comes back.
    page.locator('[data-testid="todo-calendar-entry"][data-todo-id="todo_weather"]').click()
    detail = page.locator('[data-testid="todo-calendar-detail"]')
    expect(detail).to_be_visible()
    expect(detail.get_by_role("textbox").first).to_have_value("天气 App 发 1.3 版")  # the title, edited in place
    assert "id=todo_weather" in page.url, page.url
    shot("detail")
    if size == "wide":
        detail.get_by_role("button", name="关闭").click()
    else:
        detail.get_by_role("button", name="返回日历").click()
    expect(detail).to_be_hidden()
    ok(f"{tag}: a block opens its to-do")

    # A click on the empty axis at 13:00 makes a to-do due then.
    title = f"日历里记的事 {size} {theme}"
    page.evaluate(f"document.querySelector('[data-testid=\"todo-calendar-axis\"] .overflow-y-auto').scrollTop = {12 * HOUR}")
    column = page.locator('[data-testid="todo-calendar-column"]').first
    box = column.bounding_box()
    column.click(position={"x": box["width"] * 0.8, "y": 13 * HOUR + 6})
    dialog = page.get_by_role("dialog")
    expect(dialog.locator('[data-testid="todo-new-due-time"]')).to_have_value("13:00")
    dialog.locator("input[type=text]").fill(title)
    shot("add")
    dialog.get_by_role("button", name="创建").click()
    until(lambda: any(t["title"] == title and t.get("due_at") for t in todos(hub, token)), "the new to-do due at 13:00 on the hub")
    new = next(t for t in todos(hub, token) if t["title"] == title)
    expect(page.locator(f'[data-testid="todo-calendar-entry"][data-todo-id="{new["id"]}"]')).to_be_visible()
    call(hub, token, "DELETE", f"/api/v1/todos/{new['id']}")
    ok(f"{tag}: a click on the axis makes a to-do due there")

    drag_checks(page, hub, token, size, theme, shot, tag)

    # Those without a due date: the bar goes back to the list.
    page.locator('[data-testid="todo-calendar-undated"]').click()
    expect(page.locator('[data-testid="todos-list-column"]')).to_be_visible()
    assert "view=calendar" not in page.url, page.url
    ok(f"{tag}: the bar under the calendar goes back to the list")
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
