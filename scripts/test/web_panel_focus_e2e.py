#!/usr/bin/env python3
"""End-to-end check that opening a task in the side panel does not push the page up (#1282).

The bug: in a task's side panel, a plan waiting for approval with a question and a permission under it stood taller
than the panel; the composer's focus then scrolled the page's overflow-hidden root up to show it (about 80-150 px),
and the panel's header went off the top. The panel's dock now folds its cards and keeps to DOCK_MAX as a narrow
screen's does, and the composer's focus does not scroll its ancestors.

Needs a running web page against the fake hub and Playwright for Python:

    MOCK_TASKS=1 MOCK_TASKS_DOCK=many PORT=18793 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18793 npx vite --port 5293 --strictPort &)
    python3 scripts/test/web_panel_focus_e2e.py http://127.0.0.1:5293 [--size 1280x800] [--shots <dir>]

Opens every pill of the main conversation's task dock in turn, light and dark, and checks that no overflow-hidden
element got scrolled and the panel's header is in the window. Prints one line per pill and a verdict; exits 1 on the
first failure.
"""
import argparse
import sys

from playwright.sync_api import sync_playwright

# The largest scrollTop of an element that does not scroll on its own: the page pushed up when it is above 0.
PUSHED = """() => {
  let worst = 0, who = '';
  for (const el of document.querySelectorAll('*')) {
    const s = getComputedStyle(el);
    if ((s.overflowY === 'hidden' || s.overflowY === 'clip') && el.scrollTop > worst) {
      worst = el.scrollTop; who = el.tagName + '.' + String(el.className).slice(0, 60);
    }
  }
  return { worst, who, doc: document.scrollingElement.scrollTop };
}"""


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--token", default="mock")
    ap.add_argument("--size", default="1280x800")
    ap.add_argument("--shots")
    ap.add_argument("--executable", help="a Chromium to drive instead of Playwright's own")
    a = ap.parse_args()
    w, h = (int(x) for x in a.size.split("x"))
    with sync_playwright() as p:
        browser = p.chromium.launch(executable_path=a.executable) if a.executable else p.chromium.launch()
        for theme in ("light", "dark"):
            ctx = browser.new_context(viewport={"width": w, "height": h}, color_scheme=theme)
            ctx.add_init_script(f"localStorage.setItem('persona.theme', '{theme}')")
            page = ctx.new_page()
            page.goto(a.base + "/?token=" + a.token)
            page.wait_for_selector('[data-testid="task-dock-board"]', timeout=20000)
            page.wait_for_timeout(800)
            pills = page.locator('[data-testid="task-dock-board"] + * > button')
            for i in range(pills.count()):
                if pills.count() <= i:
                    break
                label = pills.nth(i).inner_text().replace("\n", " ")[:30]
                pills.nth(i).click()
                page.wait_for_timeout(900)
                r = page.evaluate(PUSHED)
                if a.shots:
                    page.screenshot(path=f"{a.shots}/panel-{theme}-{w}-{i}.png")
                ok = r["worst"] == 0 and r["doc"] == 0
                print(f"{'ok  ' if ok else 'FAIL'} {theme} {w}x{h} [{label}]: pushed {r['worst']} px {r['who']}")
                if not ok:
                    print("FAIL: opening a task in the side panel pushed the page up")
                    return 1
                page.keyboard.press("Escape")
                page.wait_for_timeout(300)
            ctx.close()
        browser.close()
    print("PASS: no task's panel pushes the page up")
    return 0


if __name__ == "__main__":
    sys.exit(main())
