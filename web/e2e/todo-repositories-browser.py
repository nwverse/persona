#!/usr/bin/env python3
"""Browser evidence for a to-do's **Repositories and work** section (Web).

Input for Browser Use, from the repository root:

    MOCK_TODO_LIST=1 PORT=18961 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18961 npx vite --port 5961 --strictPort &)
    REPOSITORY_WEB=http://127.0.0.1:5961 REPOSITORY_HUB=http://127.0.0.1:18961 browser-use < web/e2e/todo-repositories-browser.py

Only the association store's replies are synthetic: the page, the to-do list, its rows and the assistant's repository
catalog come from the mock hub, and `GET/PUT /todos/{id}/repositories` is answered in the browser by an invented store,
so no Agent, repository or Git checkout is involved. Checks, in English, light and dark at 1280 and 390 px, plus Large
text at 390: the section draws saved repositories and their PRs as links without an editor; the legacy `pr_url` is not
repeated beside it; **Manage links** opens the dialog; another repository's URL is refused inline and a valid one is
added with Enter; a revision conflict keeps the draft and **Discard edits and reload** takes the store's newest value;
Save sends the revisions it was shown; Escape closes the dialog and returns the focus; a hub without the route shows only
the legacy link. Screenshots stay outside Git (`REPOSITORY_EVIDENCE_DIR`, default `/tmp/persona-todo-repositories-evidence`).
"""

import json
import os
import time
import urllib.request
from pathlib import Path

WEB = os.environ.get("REPOSITORY_WEB", "http://127.0.0.1:5961").rstrip("/")
HUB = os.environ.get("REPOSITORY_HUB", "http://127.0.0.1:18961").rstrip("/")
EVIDENCE = Path(os.environ.get("REPOSITORY_EVIDENCE_DIR", "/tmp/persona-todo-repositories-evidence"))
EVIDENCE.mkdir(parents=True, exist_ok=True)

IDENTITY = {"provider": "github", "instance_url": "https://github.com", "path": "example/web", "canonical_url": "https://github.com/example/web"}
CATALOG = {"manager": "assistant", "catalog_revision": 4, "repositories": [
    {**IDENTITY, "id": "web", "name": "Web app", "verified_at": "2026-01-01T00:00:00Z"},
    {"provider": "github", "instance_url": "https://github.com", "path": "example/docs",
     "canonical_url": "https://github.com/example/docs", "id": "docs", "name": "Docs", "verified_at": "2026-01-01T00:00:00Z"},
]}


def hub_todos():
    # A loopback hub is reached directly: a proxy from the shell's environment refuses it.
    request = urllib.request.Request(f"{HUB}/api/v1/todos")
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(request, timeout=10) as response:
        return json.load(response)["todos"]


TODOS = hub_todos()
TODO = next((item for item in TODOS if item.get("pr_url")), TODOS[0] if TODOS else None)
assert TODO, "the mock hub lists no to-do (start it with MOCK_TODO_LIST=1)"
TODO_ID, LEGACY_PR = TODO["id"], TODO.get("pr_url", "")


def fixture_source(legacy=False):
    """The whole page's fetch shim, installed before any page script so the section's first read finds it."""
    return """(() => {
  const state = window.repoFixture = {calls: [], revision: 2, numbers: [3211, 3212], catalogRevision: 4, failNextSave: null, legacy: %(legacy)s};
  const identity = %(identity)s, catalog = %(catalog)s, todoId = %(todo)s;
  const set = () => ({schema_version: 1, manager: "assistant", todo_id: todoId, revision: state.revision, repositories: [{
    repo_id: 'web', requested_ref: {kind: 'default'},
    source_snapshot: {identity, clone_url: identity.canonical_url, catalog_revision: 4, transport_origin: 'canonical_default'},
    links: state.numbers.map((n) => ({kind: 'pull_request', number: String(n), canonical_url: identity.canonical_url + '/pull/' + n})),
    catalog_state: 'current'}]});
  const reply = (body, status=200) => new Response(JSON.stringify(body), {status, headers: {'Content-Type': 'application/json'}});
  const original = window.fetch.bind(window);
  window.fetch = async (url, init={}) => {
    const path = String(url).split('/api/v1')[1], method = (init.method || 'GET').toUpperCase();
    if (!path || !path.includes('/repositories')) return original(url, init);
    if (state.legacy) return reply({code: 'not_found', error: 'Not Found'}, 404);
    const manager = new Headers(init.headers).get('X-Persona-Manager');
    if (path.endsWith('/assistant/repositories')) return reply({...catalog, manager});
    if (method === 'GET') return reply(set());
    const body = init.body ? JSON.parse(init.body) : {};
    state.calls.push({method, body, shownRevision: state.revision});
    if (state.failNextSave) { const failure = state.failNextSave; state.failNextSave = null; return reply(failure, 409); }
    if (body.expected_revision !== state.revision) return reply({code: 'task_repositories_revision_conflict', error: 'changed'}, 409);
    if (body.expected_catalog_revision !== state.catalogRevision) return reply({code: 'task_repositories_catalog_conflict', error: 'catalog changed'}, 409);
    state.revision += 1;
    state.numbers = body.repositories.flatMap((row) => (row.links || []).map((link) => Number(String(link.number || link.url).split('/').pop())));
    return reply(set());
  };
  return true;
})()""" % {"legacy": "true" if legacy else "false", "identity": json.dumps(IDENTITY), "catalog": json.dumps(CATALOG), "todo": json.dumps(TODO_ID)}


def js(expression):
    result = cdp("Runtime.evaluate", expression=expression, returnByValue=True, awaitPromise=True)
    if result.get("exceptionDetails"):
        raise AssertionError(f"page script failed: {result['exceptionDetails'].get('text')}: {expression[:120]}")
    return result["result"].get("value")


def eventually(expression, seconds=10):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if js(expression):
            return
        time.sleep(0.05)
    raise AssertionError(f"never true: {expression}")


def settle():
    js("new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)))")
    eventually("document.getAnimations().every(a=>a.playState!=='running'||a.effect?.getTiming().iterations===Infinity)")


def shot(name):
    settle()
    capture_screenshot(str(EVIDENCE / f"{name}.png"))


def press(name, role="button"):
    """A real pointer press on the control with this accessible name, at its own box (so it also takes the focus, as a
    press does for a person). The box is checked to be the one the pointer reaches before it is clicked."""
    nodes = cdp("Accessibility.getFullAXTree")["nodes"]
    node = next((n for n in nodes if n.get("role", {}).get("value") == role and n.get("name", {}).get("value") == name), None)
    assert node, f"no {role} named {name}"
    backend = node["backendDOMNodeId"]
    cdp("DOM.scrollIntoViewIfNeeded", backendNodeId=backend)
    deadline, previous = time.monotonic() + 8, None
    while time.monotonic() < deadline:
        box = cdp("DOM.getBoxModel", backendNodeId=backend)["model"]["content"]
        x, y = sum(box[0::2]) / 4, sum(box[1::2]) / 4
        clear = js(f"document.elementFromPoint({x},{y})?.closest('[aria-label={json.dumps(name)}], button, a') !== null")
        if box == previous and clear:
            click_at_xy(x, y)
            return
        previous = box
        time.sleep(0.05)
    raise AssertionError(f"control stayed covered or moving: {name}")


def check(condition, message):
    assert condition, message
    print(f"ok  {message}")


def type_url(value):
    js("""(() => { const el = document.querySelector('[role=dialog] input[type=url]');
      if (!el) throw new Error('no link field');
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(el, %s);
      el.dispatchEvent(new Event('input', {bubbles: true})); })()""" % json.dumps(value))


def open_todo(theme, width, legacy=False, large=False):
    # The page is opened first and reloaded: Page.addScriptToEvaluateOnNewDocument only reaches documents created after
    # it, not the tab's first one.
    new_tab(f"{WEB}/?token=mock")
    wait_for_load()
    js("localStorage.setItem('persona.token','mock'); localStorage.setItem('persona.language','en'); "
       "localStorage.setItem('persona.todos.view','list'); localStorage.setItem('persona.theme',%s); true" % json.dumps(theme))
    js("localStorage.setItem('persona.text-size', %s); true" % json.dumps("large" if large else "standard"))
    new_tab(f"{WEB}/todos?id={TODO_ID}")
    wait_for_load()
    cdp("Page.enable")
    cdp("Page.addScriptToEvaluateOnNewDocument", source=fixture_source(legacy=legacy))
    cdp("Page.reload")
    wait_for_load()
    eventually("!!window.repoFixture")
    cdp("Emulation.setDeviceMetricsOverride", width=width, height=1100 if width > 600 else 1700, deviceScaleFactor=1, mobile=width <= 600)
    eventually("!!document.querySelector('[data-todo-repositories]')")
    eventually("!document.querySelector('[data-todo-repositories]').innerText.includes('Loading')")
    if large:
        check(js("document.documentElement.dataset.textSize") == "large", "the page is at the Large text size")
    settle()


def run(theme, width, suffix=""):
    where = f"{theme} {width}px{suffix}"
    open_todo(theme, width, large=bool(suffix))
    section = "document.querySelector('[data-todo-repositories]').innerText"
    check("example/web" in js(section), f"{where}: the saved repository is drawn on the page")
    check("#3211" in js(section) and "#3212" in js(section), f"{where}: both saved PRs are links, with no editor open")
    check(js("document.querySelectorAll('[data-todo-repositories] a[href$=\"/pull/3211\"], [data-todo-repositories] a[href$=\"/pull/3212\"]').length") == 2,
          f"{where}: each PR is one link")
    check(js("!document.querySelector('[data-todo-repositories] textarea')"), f"{where}: no multiline URL form")
    if LEGACY_PR:
        # The list still carries the to-do's own PR line; the detail draws the store's links, not that legacy one as well.
        check(js("document.querySelectorAll('[data-testid=todos-detail-column] a[href=%s]').length === 0" % json.dumps(LEGACY_PR)),
              f"{where}: the detail does not draw the legacy PR beside the store's links")
    shot(f"section-{width}-{theme}{suffix}")
    press("Manage links")
    eventually("!!document.querySelector('[role=dialog]')")
    check("Pull request" in js("document.querySelector('[role=dialog]').innerText"), f"{where}: the dialog lists the links as rows")
    shot(f"dialog-{width}-{theme}{suffix}")
    press("Add issue or PR")
    eventually("!!document.querySelector('[role=dialog] input[type=url]')")
    type_url("https://github.com/example/docs/pull/9")
    press("Add issue or PR")
    eventually("!!document.querySelector('[role=dialog] [role=alert]')")
    check("Use an issue or pull request link from this repository." in js("document.querySelector('[role=dialog]').innerText"),
          f"{where}: another repository's URL is refused inline")
    type_url("https://github.com/example/web/pull/3213")
    js("document.querySelector('[role=dialog] input[type=url]').dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true})); true")
    eventually("document.querySelector('[role=dialog]').innerText.includes('#3213')")
    check(True, f"{where}: Enter adds a link of this repository")
    shot(f"dialog-added-{width}-{theme}{suffix}")
    js("window.repoFixture.failNextSave = {code: 'task_repositories_revision_conflict', error: 'changed'}; true")
    press("Save")
    eventually("document.querySelector('[role=dialog] [role=alert]') && document.querySelector('[role=dialog]').innerText.includes('#3213')")
    check("Your edits are kept" in js("document.querySelector('[role=dialog]').innerText"), f"{where}: a revision conflict keeps the draft")
    shot(f"dialog-conflict-{width}-{theme}{suffix}")
    js("window.repoFixture.revision = 9; window.repoFixture.numbers = [3211, 3214]; true")
    press("Discard edits and reload")
    eventually("document.querySelector('[role=dialog]').innerText.includes('#3214')")
    check("#3213" not in js("document.querySelector('[role=dialog]').innerText"), f"{where}: the reload takes the store's newest links, not the stale draft")
    # Nothing to save yet: the draft is now the store's own value, so Save is off until an edit is made.
    check(js("[...document.querySelectorAll('[role=dialog] button')].some((b) => b.innerText.trim() === 'Save' && b.disabled)"), f"{where}: Save is off while the reloaded draft matches the store")
    press("Add issue or PR")
    eventually("!!document.querySelector('[role=dialog] input[type=url]')")
    type_url("https://github.com/example/web/pull/3215")
    js("document.querySelector('[role=dialog] input[type=url]').dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true})); true")
    eventually("document.querySelector('[role=dialog]').innerText.includes('#3215')")
    press("Save")
    eventually("!document.querySelector('[role=dialog]')", seconds=15)
    eventually("document.querySelector('[data-todo-repositories]').innerText.includes('#3215')")
    check(True, f"{where}: the saved set is drawn on the page without reopening the editor")
    saves = js("JSON.stringify(window.repoFixture.calls.filter((c) => c.method === 'PUT'))")
    last = json.loads(saves)[-1]["body"]
    check(last["expected_revision"] == 9 and last["expected_catalog_revision"] == 4, f"{where}: the save sent the revisions it was shown after the reload")
    shot(f"section-saved-{width}-{theme}{suffix}")
    press("Manage links")
    eventually("!!document.querySelector('[role=dialog]')")
    # A real Escape on the page: the dialog's own handler closes it and the focus goes back to the button.
    cdp("Input.dispatchKeyEvent", type="keyDown", key="Escape", code="Escape", windowsVirtualKeyCode=27, nativeVirtualKeyCode=27)
    cdp("Input.dispatchKeyEvent", type="keyUp", key="Escape", code="Escape", windowsVirtualKeyCode=27, nativeVirtualKeyCode=27)
    eventually("!document.querySelector('[role=dialog]')")
    check(js("document.activeElement && document.activeElement.innerText.trim() === 'Manage links'"), f"{where}: Escape closes the dialog and hands the focus back")


def run_legacy(theme="light", width=1280):
    open_todo(theme, width, legacy=True)
    eventually("document.querySelector('[data-todo-repositories]').innerText.includes('Linked before repositories were stored here.')")
    check(js("!!document.querySelector('[data-todo-repositories] a[href=%s]')" % json.dumps(LEGACY_PR)),
          "a hub without the route shows the legacy PR link")
    check(js("!document.querySelector('[data-todo-repositories]').innerText.includes('Manage links')"), "and offers no editor there")
    shot(f"section-legacy-{width}-{theme}")


try:
    for theme in ("light", "dark"):
        for width in (1280, 390):
            run(theme, width)
    run("light", 390, "-large")
    run_legacy()
    print("VERDICT: pass")
except AssertionError as failure:
    print(f"VERDICT: FAIL {failure}")
    sys.exit(1)
