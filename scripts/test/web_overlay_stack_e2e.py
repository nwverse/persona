#!/usr/bin/env python3
"""Phone Back with nested Files/Git views, the Assistant's trays and live overlays (#11643).

    MOCK_GIT_WRITE=1 MOCK_FILES=1 MOCK_SPLIT_TABS=1 PORT=18884 python3 scripts/mock/mock_hub.py
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18884 npx vite --port 18885 --strictPort)
    uv run --no-project --with playwright==1.58.0 python3 scripts/test/web_overlay_stack_e2e.py http://127.0.0.1:18885
"""
import argparse
import os
from pathlib import Path
from urllib.parse import parse_qs, urlsplit

from playwright.sync_api import expect, sync_playwright

from web_mobile_input_e2e import CLAUDE, open_pane

CWD = "/home/me/code/weather"
MAIN = "b2e4d6f8-0000-4000-8000-000000000001"  # ASK1, the mock assistant’s main chat
OPEN = "async (p) => (await import('/src/lib/panel.ts')).openPanel(p)"
NAVIGATE = "async ([p, replace]) => (await import('/src/lib/router.ts')).navigate(p, replace)"
DEPTH = "history.state?.personaOverlayDepth || 0"


def depth(page, value):
    try:
        page.wait_for_function(f"({DEPTH}) === {value}", timeout=5_000)
    except Exception as error:
        raise AssertionError(f"expected {value} overlay entries at {page.url}, found {page.evaluate(DEPTH)}") from error


def picture(page, shots, label):
    if shots:
        if label.startswith("tray-back"):
            expect(page.get_by_text("今天有什么需要我处理的？", exact=True)).to_be_visible(timeout=15_000)
        page.evaluate("""async () => {
            await document.fonts.ready;
            await Promise.allSettled(document.getAnimations().filter(a => Number.isFinite(a.effect?.getComputedTiming().endTime)).map(a => a.finished));
        }""")
        page.screenshot(path=shots / (label + ".png"))


def files_and_git(page, base, shots, theme):
    open_pane(page, base, cards=False)
    chat = page.url
    page.locator("main header").get_by_role("button", name="更多操作", exact=True).click()
    page.get_by_role("menuitem", name="文件").or_(page.get_by_role("menuitemcheckbox", name="文件")).click()
    files = page.locator("aside[aria-label=文件]")
    files.locator("button[title='/home/me/code']").click()
    files.locator(f"button[title='{CWD}']").click()
    readme = files.locator(f"button[title='{CWD}/README.md']")
    readme.click()
    depth(page, 2)
    page.go_back()
    expect(readme).to_be_visible()
    depth(page, 1)
    picture(page, shots, "files-back-" + theme)
    page.go_back()
    expect(files).to_have_count(0)
    assert page.url == chat

    panel = {"mid": "server", "sid": CLAUDE, "cwd": CWD}
    page.evaluate(OPEN, {**panel, "kind": "git"})
    git = page.locator("aside[aria-label=Git]")
    commit = git.get_by_role("button").filter(has_text="Show the city name").first
    for title in ("Sources/App/main.swift", "分支：切换或新建", "用 git stash 收起来的改动有几份"):
        target = git.locator(f"button[title='{title}']")
        target.click()
        depth(page, 2)
        page.go_back()
        expect(target).to_be_visible()
        depth(page, 1)
    commit.click()
    changed = git.locator("button[title$='.swift']:visible").first
    changed.click()
    depth(page, 3)
    page.go_back()
    expect(changed).to_be_visible()
    depth(page, 2)
    picture(page, shots, "git-back-" + theme)
    page.go_back()
    expect(commit).to_be_visible()
    page.go_back()
    expect(git).to_have_count(0)
    assert page.url == chat

    page.evaluate(OPEN, {**panel, "kind": "files", "file": CWD + "/README.md"})
    depth(page, 2)
    page.evaluate(NAVIGATE, ["/settings", False])
    page.wait_for_url("**/settings")
    expect(files).to_have_count(0)
    page.go_back()
    page.wait_for_url(chat)
    expect(files.get_by_role("button", name="返回", exact=True)).to_be_visible()
    depth(page, 2)
    files.get_by_role("button", name="关闭面板").last.click()
    depth(page, 0)
    assert page.url == chat
    # The rendered Markdown viewer follows relative links as another preview. Seven such links exceed the old cap.
    def linked_file(route):
        path = parse_qs(urlsplit(route.request.url).query).get("path", [""])[0]
        if not path.startswith(CWD + "/level-"):
            return route.fallback()
        index = int(Path(path).stem.removeprefix("level-"))
        text = f"# Level {index}\n\n[Next level](level-{index + 1}.md)"
        route.fulfill(json={"path": path, "name": Path(path).name, "text": text, "size": len(text), "mime": "text/markdown", "is_dir": False, "truncated": False})

    page.route("**/machines/server/file?**", linked_file)
    page.evaluate(OPEN, {**panel, "kind": "files", "file": CWD + "/level-1.md"})
    for index in range(1, 8):
        expect(files.get_by_role("heading", name=f"Level {index}")).to_be_visible()
        files.get_by_role("link", name="Next level").click()
    depth(page, 9)
    page.reload()
    page.locator("main header").get_by_role("button", name="更多操作", exact=True).wait_for()
    expect(files).to_have_count(0)
    depth(page, 0)
    page.go_back()
    page.wait_for_url("**/overview")
    print(f"ok  {theme} Files/Git, file/commit/diff/branches/stashes, direct preview Close and nine-level reload", flush=True)


def trays(page, base, shots, theme):
    page.goto(base + "/inbox?token=mock")
    page.wait_for_selector("aside", state="attached")
    page.evaluate(NAVIGATE, ["/", False])
    entries = page.locator("[data-assistant-actionbar] [data-tray-entry]")
    tray = page.locator("section.assistant-action-tray")
    expect(entries.first).to_be_visible()
    # Same notification dispatched by feed.ts when a new task arrives; no prior interaction with the document.
    page.evaluate("sid => dispatchEvent(new CustomEvent('persona:task-pending', {detail: sid}))", MAIN)
    expect(tray).to_be_visible()
    depth(page, 1)
    count = page.evaluate("history.length")
    entries.nth(1).tap()
    tray.get_by_role("button", name="放大", exact=True).tap()
    expect(tray).to_have_attribute("data-expanded", "true")
    assert page.evaluate("history.length") == count
    page.go_back()
    expect(tray).to_have_count(0)
    depth(page, 0)
    assert urlsplit(page.url).path == "/"
    picture(page, shots, "tray-back-" + theme)
    page.go_back()
    page.wait_for_url("**/inbox")
    page.evaluate(NAVIGATE, ["/", False])
    entries.first.tap()
    tray.get_by_role("button", name="关闭", exact=True).tap()
    depth(page, 0)
    entries.first.tap()
    tray.get_by_role("button", name="全部", exact=True).tap()
    depth(page, 2)
    # The desktop dialog no longer owns Back at 800px, while the narrow tray still does.
    page.set_viewport_size({"width": 800, "height": 390})
    depth(page, 1)
    page.set_viewport_size({"width": 390, "height": 844})
    depth(page, 2)
    page.evaluate(NAVIGATE, ["/?back-check=1", True])
    depth(page, 2)
    page.go_back()
    expect(page.get_by_role("dialog")).to_have_count(0)
    expect(tray).to_be_visible()
    assert urlsplit(page.url).query == "back-check=1"
    tray.get_by_role("button", name="全部", exact=True).tap()
    depth(page, 2)
    page.evaluate(NAVIGATE, ["/settings", False])
    page.wait_for_url("**/settings")
    expect(tray).to_have_count(0)
    page.go_back()
    page.wait_for_url("**/?back-check=1")
    depth(page, 0)
    page.go_back()
    page.wait_for_url("**/inbox")
    print(f"ok  {theme} unsolicited, switched, expanded and closed tray; rotation, stacked replace and navigation", flush=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("base")
    parser.add_argument("--shots", type=Path)
    parser.add_argument("--large", action="store_true", help="use the largest text size in Appearance")
    args = parser.parse_args()
    if args.shots:
        args.shots.mkdir(parents=True, exist_ok=True)
    with sync_playwright() as playwright:
        with playwright.chromium.launch(executable_path=os.environ.get("PERSONA_E2E_CHROMIUM") or None) as browser:
            for width, height, theme in ((390, 844, "light"), (360, 740, "dark")):
                for scenario in (files_and_git, trays):
                    with browser.new_context(viewport={"width": width, "height": height}, locale="zh-CN", color_scheme=theme, is_mobile=True, has_touch=True, device_scale_factor=2) as context:
                        if args.large:
                            context.add_init_script("localStorage.setItem('persona.text-size', 'large')")
                        page = context.new_page()
                        page.set_default_timeout(12_000)
                        scenario(page, args.base.rstrip("/"), args.shots, theme + ("-large" if args.large else ""))
    print("PASS web_overlay_stack_e2e", flush=True)


if __name__ == "__main__":
    main()
