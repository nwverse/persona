"""Check native layout on an API 26 phone with the binding homepage or a stable Machines list open."""

import argparse
import json
import re
import subprocess
import time
import uuid


def check(device, scene):
    tree_path = f"/data/local/tmp/persona-form-{uuid.uuid4().hex}.json"

    def shell(*args):
        return subprocess.check_output(["hdc", "-t", device, "shell", *args], text=True, timeout=15)

    def snapshot():
        shell("uitest", "dumpLayout", "-b", "com.example.persona.harmony", "-p", tree_path)
        nodes = []

        def walk(node):
            attrs = node.get("attributes", {})
            bounds = tuple(map(int, re.findall(r"-?\d+", attrs.get("bounds", ""))))
            if len(bounds) == 4 and bounds[0] < bounds[2] and bounds[1] < bounds[3]:
                nodes.append((attrs, bounds))
            for child in node.get("children", []):
                walk(child)

        walk(json.loads(shell("cat", tree_path)))
        return nodes

    def by_id(nodes, key):
        return next((bounds for attrs, bounds in nodes if attrs.get("id") == key), None)

    def wait(key, ready=lambda nodes: True):
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            nodes = snapshot()
            if by_id(nodes, key) and ready(nodes):
                return nodes
            time.sleep(0.1)
        raise AssertionError(f"Expected control {key}")

    def tap(bounds):
        left, top, right, bottom = bounds
        shell("uitest", "uiInput", "click", str((left + right) // 2), str((top + bottom) // 2))

    def agent_round_trip():
        rows = wait("machines.detail")
        candidates = [attrs["id"] for attrs, bounds in rows
                      if attrs.get("id", "").startswith("machines.agent.") and bounds[3] - bounds[1] > 40]
        if not candidates:
            raise AssertionError("Open a machine detail with a visible Agent conversation")
        metric = next((attrs["id"] for attrs, _ in rows if attrs.get("id", "").startswith("machines.metrics.")), None)
        keys = ["machines.detail", candidates[0]] + ([metric] if metric else [])

        def identity(nodes, key):
            return next((attrs.get("accessibilityId") for attrs, _ in nodes if attrs.get("id") == key), None)

        def populated(nodes):
            if not metric or not (area := by_id(nodes, metric)):
                return set()
            return {attrs.get("text", "").split(" ")[0] for attrs, bounds in nodes if attrs.get("type") == "Text"
                    and area[0] <= bounds[0] and area[1] <= bounds[1] and bounds[2] <= area[2] and bounds[3] <= area[3]
                    and attrs.get("text") and "—" not in attrs["text"]}

        instances = [identity(rows, key) for key in keys]
        positions = [by_id(rows, key) for key in keys]
        measured = populated(rows)
        for agent in [candidates[0], candidates[-1]]:
            tap(by_id(wait(agent), agent))
            wait("chat.transcript")
            shell("uitest", "uiInput", "keyEvent", "Back")
            returned = wait("machines.detail")
            if [identity(returned, key) for key in keys] != instances:
                raise AssertionError("Returning recreated the machine detail or its rows")
            if [by_id(returned, key) for key in keys] != positions or not measured.issubset(populated(returned)):
                raise AssertionError("Returning moved the rows or dropped a previously available metric")
        print("agent: both conversations opened, original machine nodes, geometry and readings retained on return")

    def directory():
        nodes = wait("machines.list")
        left, top, right, bottom = by_id(nodes, "machines.list")
        # A row clipped by the bottom of the screen changes its visible bounds as the pull moves it.
        cards = [attrs["id"] for attrs, bounds in nodes if attrs.get("id", "").startswith("machines.card.")
                 and top <= bounds[1] and bounds[3] <= bottom - (bottom - top) // 8]
        key = next((card for card in cards if by_id(nodes, card.replace("machines.card.", "machines.metrics."))),
                   cards[0] if cards else "machines.empty")
        initial = by_id(nodes, key)
        if not initial:
            raise AssertionError("Wait for the computer list to finish loading")
        add_enabled = any(attrs.get("id") == "machines.add" and attrs.get("enabled") == "true" for attrs, _ in nodes)

        def verify_list(pulling=False):
            for _ in range(4):
                rows = wait("machines.list")
                bounds = by_id(rows, key)
                if not bounds or abs((bounds[3] - bounds[1]) - (initial[3] - initial[1])) > 1:
                    raise AssertionError(f"Directory card height changed: {initial} -> {bounds}")
                if not pulling and abs(bounds[1] - initial[1]) > 1:
                    raise AssertionError(f"Directory card shifted: {initial} -> {bounds}")
                if add_enabled and not any(attrs.get("id") == "machines.add" and attrs.get("enabled") == "true" for attrs, _ in rows):
                    raise AssertionError("Refreshing disabled Add Computer")

        x, height = (left + right) // 2, bottom - top
        def swipe(distance):
            y = top + height // 5
            shell("uitest", "uiInput", "swipe", str(x), str(y), str(x), str(y + distance), "1000")

        swipe(height // 60)
        wait(key, lambda rows: abs(by_id(rows, key)[1] - initial[1]) <= 1)
        swipe(height // 3)
        verify_list(pulling=True)
        wait(key, lambda rows: abs(by_id(rows, key)[1] - initial[1]) <= 1)
        verify_list()
        if cards:
            tap(by_id(wait(key), key))
            try:
                wait("machines.detail")
            finally:
                if by_id(snapshot(), "machines.detail"):
                    shell("uitest", "uiInput", "keyEvent", "Back")
            verify_list()
        print("machines: card height stable during pull, original position restored after release and navigation")

    entry = "welcome.other" if scene == "pair" else "machines.add"
    panel = "pair.content" if scene == "pair" else "machines.add.form"
    anchors = ["pair.heading"] if scene == "pair" else ["machines.add.hint", "machines.add.name", "machines.add.generate"]
    opened = False
    try:
        if scene == "agent":
            agent_round_trip()
            return
        if scene == "machines":
            directory()
            return
        nodes = wait(entry)
        if by_id(nodes, panel):
            raise AssertionError("Start with the sheet closed")
        tap(by_id(nodes, entry))
        nodes = wait(anchors[0])
        opened = True
        top, bottom = by_id(nodes, panel)[1::2]
        baseline = [by_id(nodes, anchor)[1] for anchor in anchors]
        if not 0 <= baseline[0] - top < (bottom - top) / 8:
            raise AssertionError("First row is too far from the sheet header")
        for i, label in enumerate(["Linux", "Windows", "Linux", "Mac"]):
            if scene == "pair":
                tap(by_id(nodes, "pair.switch-method"))
                nodes = wait("pair.heading", lambda rows: bool(by_id(rows, "pair.code")) == (i % 2 == 0))
            else:
                left, top, right, bottom = by_id(nodes, "machines.add.system")
                target = next(bounds for attrs, bounds in nodes if attrs.get("text") == label
                              and left <= (bounds[0] + bounds[2]) / 2 <= right
                              and top <= (bounds[1] + bounds[3]) / 2 <= bottom)
                tap(target)
                nodes = wait("machines.add.system")
            positions = [by_id(nodes, anchor)[1] for anchor in anchors]
            if any(abs(old - new) > 1 for old, new in zip(baseline, positions)):
                raise AssertionError(f"{scene} anchors moved: {baseline} -> {positions}")
        print(f"{scene}: four switches, anchor positions stable within one physical pixel")
    finally:
        try:
            if opened and by_id(snapshot(), panel):
                shell("uitest", "uiInput", "keyEvent", "Back")
        finally:
            shell("rm", "-f", tree_path)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("device", help="the authorized hdc target")
    parser.add_argument("scene", choices=["machines", "agent", "computer", "pair"])
    args = parser.parse_args()
    check(args.device, args.scene)
