"""clitest: a small harness for UI tests written against the runtime CLI (scripts/app/personactl.py) instead of XCUITest.

A case is a function taking a `T`. `run(cases, setup, runs, launch)` launches the app once, calls `setup(t)` before each
case (no relaunch: the mock is reset and the app moved back to where the case starts), times every case, prints one line
per run and a summary, and exits non-zero when any run failed. A failed run leaves the screen as it was then in
CLITEST_OUT (default <tmp>/clitest)/<time>/<case>-<run>/: `dump`. docs/operations/runtime-cli.md "UI tests written with the CLI".
"""
import base64
import json
import os
import subprocess
import sys
import tempfile
import time
import traceback
import urllib.request

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))
from personactl import Ctl, CtlError  # noqa: E402,F401

# Match XCUITest's launchPinned(): pin the style and exercise the app's default navigation.
# Compact iPhone screens use the shell; regular-width screens keep their sidebar.
PINNED = ["-ui.style", os.environ.get("PERSONA_UI_STYLE", "glass")]


class Fail(Exception):
    pass


def mock(c, path):
    """GET <hub>/mock/<path> on scripts/mock/mock_hub.py (reset, seen, churn…), as JSON."""
    with urllib.request.urlopen(c.hub.rstrip("/") + "/mock/" + path, timeout=10) as r:
        return json.load(r)


def allow_alerts(c, wait=4):
    """A system alert over the app (the notification prompt an earlier `xcodebuild test` leaves) answered with "Allow".
    System alerts are not in the app's own tree: found through `idb ui describe-all`."""
    deadline = time.time() + wait
    while time.time() < deadline:
        if c.call("state").get("appState") == "active":
            return
        out = subprocess.run(["idb", "ui", "describe-all", "--udid", c.udid], capture_output=True, text=True).stdout
        try:
            items = json.loads(out)
        except ValueError:
            items = []
        for e in items:
            if e.get("AXLabel") in ("允许", "Allow") and e.get("frame"):
                f = e["frame"]
                c._idb("tap", round(f["x"] + f["width"] / 2), round(f["y"] + f["height"] / 2))
                break
        time.sleep(0.5)


class T:
    def __init__(self, ctl):
        self.c = ctl

    def find(self, **f):
        return self.c.call("find", **f)["element"]

    def see(self, what, timeout=10, **f):
        try:
            return self.c.call("wait", timeout=timeout, **f)["element"]
        except CtlError:
            raise Fail(f"{what} not found ({f})")

    def gone(self, what, timeout=10, **f):
        try:
            self.c.call("wait", timeout=timeout, gone=True, **f)
        except CtlError:
            raise Fail(f"{what}: still there ({f})")

    def label(self, **f):
        e = self.find(**f)
        if not e:
            raise Fail(f"no element {f}")
        return e["label"]

    def tap(self, what, **f):
        self.see(what, **f)
        return self.c.tap(**f)

    def settle(self):
        self.c.call("settle", timeout=3, stable=2, pixels=600)

    def reach(self, what, **f):
        """Swipes up until the element is where a touch reaches it, as XCUITest's reach (the list builds rows lazily:
        one below the fold is not in the tree yet)."""
        for _ in range(8):
            e = self.find(**f)
            if e and e["visible"]:
                return e
            self.c.swipe("up", distance=300, duration=0.5)
            self.c.call("settle", timeout=2, stable=2, every=100)
        return self.see(what, timeout=2, **f)

    def until(self, what, test, timeout=10, got=None):
        deadline = time.time() + timeout
        while time.time() < deadline:
            if test():
                return
            time.sleep(0.2)
        if not test():
            raise Fail(what + (f" (now {got()!r})" if got else ""))

    def eq(self, a, b, what=""):
        if a != b:
            raise Fail(f"{what}: {a!r} != {b!r}")

    def true(self, v, what):
        if not v:
            raise Fail(what)


def to_agents(t):
    """The Agents list at its root, on a new view tree; a chat still open is closed with its back button."""
    t.c.call("navigate", scene="agents")
    for _ in range(4):
        if t.find(id="agents.filter.all"):
            return
        if t.find(label="返回", trait="button", visibleOnly=True):  # not a hidden page's: tap acts only on a reachable one
            t.c.tap(label="返回", trait="button")
        t.settle()
    t.see("the Agents list", timeout=5, id="agents.filter.all")


def dump(c, d, why, trace):
    """What there was to see when a run failed, in directory `d`: why.txt (the reason and where the script stopped),
    snapshot.png, tree.json, state.json. Re-running is the only other way to look, and a failure 1 time in 3 may not
    come back (`the row is gone`). What the app cannot answer (it crashed, 5 s each) is written to dump-errors.txt."""
    d = os.path.abspath(d)  # the app writes snapshot.png: a relative path would be the app's own directory
    os.makedirs(d, exist_ok=True)
    with open(os.path.join(d, "why.txt"), "w") as f:
        f.write(why + "\n\n" + trace)
    errors = []

    def snapshot():
        if not c.via_hub:  # the simulator's files are the Mac's: the app writes it
            return c.call("snapshot", _timeout=5, path=os.path.join(d, "snapshot.png"))
        with open(os.path.join(d, "snapshot.png"), "wb") as f:
            f.write(base64.b64decode(c.call("snapshot", _timeout=5)["png"]))

    def save(name, value):
        with open(os.path.join(d, name), "w") as f:
            json.dump(value, f, ensure_ascii=False, indent=1)

    steps = {"snapshot.png": snapshot,
             "tree.json": lambda: save("tree.json", c.call("tree", _timeout=5)["elements"]),
             "state.json": lambda: save("state.json", c.call("state", _timeout=5))}
    for name, do in steps.items():
        try:
            do()
        except Exception as e:  # whatever goes wrong here, the run goes on to the next case
            errors.append(f"{name}: {e}")
    if errors:
        with open(os.path.join(d, "dump-errors.txt"), "w") as f:
            f.write("\n".join(errors) + "\n")
    return d


def run(cases, setup, runs, launch):
    """`launch(ctl)` once, then every case `runs` times, each after `setup(t)`. CLITEST_LOG: one tab-separated line per
    run appended there (case, run, ok, seconds, why)."""
    c = Ctl()
    t = T(c)
    t0 = time.time()
    dumps = os.path.join(os.environ.get("CLITEST_OUT") or os.path.join(tempfile.gettempdir(), "clitest"),
                         time.strftime("%Y%m%d-%H%M%S"))  # a run of its own: nothing left from an earlier one
    launch(c)
    allow_alerts(c)
    out = []
    for r in range(runs):
        for case in cases:
            s = time.time()
            ok, why, trace, saved = True, "", "", ""
            try:
                setup(t)
                case(t)
            except (Fail, CtlError) as e:
                ok, why, trace = False, str(e), traceback.format_exc()
            except Exception as e:  # a bug in the script itself
                ok, why, trace = False, "".join(traceback.format_exception_only(type(e), e)).strip(), traceback.format_exc()
            d = time.time() - s
            if not ok:
                saved = "\t" + dump(c, os.path.join(dumps, f"{case.__name__}-{r + 1}"), why, trace)
            out.append((case.__name__, r + 1, ok, d, why))
            print(f"{case.__name__}\trun {r + 1}\t{'PASS' if ok else 'FAIL'}\t{d:.1f} s\t{why}{saved}", flush=True)
    print(f"total {time.time() - t0:.1f} s")
    for case in cases:
        rows = [o for o in out if o[0] == case.__name__]
        ts = sorted(o[3] for o in rows)
        print(f"SUMMARY {case.__name__}: {sum(o[2] for o in rows)}/{len(rows)} passed, "
              f"{ts[0]:.1f} / {ts[len(ts) // 2]:.1f} / {ts[-1]:.1f} s (min / median / max)")
    if os.environ.get("CLITEST_LOG"):
        with open(os.environ["CLITEST_LOG"], "a") as f:
            for o in out:
                f.write("\t".join(map(str, o)) + "\n")
    return all(o[2] for o in out)


def main(cases, setup, launch):
    """`<script> [runs] [case …]`: the named cases (all without names), `runs` times (1)."""
    runs = int(sys.argv[1]) if len(sys.argv) > 1 else 1
    pick = sys.argv[2:]
    chosen = [c for c in cases if not pick or c.__name__ in pick]
    if not chosen:
        sys.exit(f"no such case: {pick} (cases: {[c.__name__ for c in cases]})")
    sys.exit(0 if run(chosen, setup, runs, launch) else 1)
