#!/usr/bin/env python3
"""Capture shared showcase snapshots through the real Mac app without activating it.

Build PersonaMac separately through xcode-slot, then:
  python3 scripts/app/mac-showcase.py --app /tmp/build/Build/Products/Debug/Persona.app \
      --output /tmp/mac-showcase-glass --port 17490

No production hub, keychain credentials or image hosting is used. The actual Mac
sidebar/page renderer draws either auxiliary mock HTTP or an installed extension
on a dedicated loopback test hub. The capture manifest records that distinction.
Long shared pages include a second capture after scrolling to the bottom.
"""
from __future__ import annotations

import argparse
from collections import Counter
import hashlib
import json
import math
import os
from pathlib import Path
import re
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[2]
SNAPSHOTS = ROOT / "schema/extension/examples/showcase"
BOTTOM_PAGES = {"row", "field", "chart", "stat_grid", "table", "code-browse", "code-review", "code-review-v5"}
SCROLL_ALL_PAGES = {"home", "list", "list-filter", "list-more", "list-search", "code-overview"}


def image_urls(value):
    found = set()
    if isinstance(value, dict):
        for field in ("avatar", "thumbnail"):
            image = value.get(field)
            if isinstance(image, dict) and isinstance(image.get("url"), str):
                url = urllib.parse.urlparse(image["url"])
                if url.scheme == "https" and url.hostname and not url.hostname.endswith(".invalid"):
                    found.add(image["url"])
        for child in value.values():
            found.update(image_urls(child))
    elif isinstance(value, list):
        for child in value:
            found.update(image_urls(child))
    return sorted(found)


def scenes(names=None, snapshots=SNAPSHOTS):
    files = {file.stem: file for file in sorted(snapshots.glob("*.json"))}
    selected = sorted(files) if names is None else names
    if not selected or len(selected) != len(set(selected)) or any(name not in files for name in selected):
        raise ValueError("Select unique snapshot names from the shared catalog")
    result, hashes = [], {}
    for name in selected:
        data = files[name].read_bytes()
        view = json.loads(data)
        result.append({"name": name, "title": view.get("title") or name, "bottom": name in BOTTOM_PAGES,
                       "review": view.get("review") is not None, "images": image_urls(view),
                       "scrollAll": name in SCROLL_ALL_PAGES})
        hashes[name] = hashlib.sha256(data).hexdigest()
    return result, hashes


def native_revision(value):
    return isinstance(value, str) and re.fullmatch(r"stream:[1-9][0-9]*", value) is not None


def native_closes(value, selected):
    return (isinstance(value, list) and all(isinstance(page, str) for page in value)
            and Counter(value) == Counter({"showcase/" + scene["name"]: 2 for scene in selected}))


def complete_captures(captures, selected):
    if not isinstance(captures, list) or not captures:
        return False
    expected = {(scene["name"], appearance) for scene in selected for appearance in ("light", "dark")}
    if {(c.get("sample"), c.get("appearance")) for c in captures if isinstance(c, dict)} != expected:
        return False
    if any(not isinstance(c, dict) for c in captures):
        return False
    for scene in selected:
        for appearance in ("light", "dark"):
            rows = [c for c in captures if c.get("sample") == scene["name"] and c.get("appearance") == appearance]
            positions = [c.get("position") for c in rows]
            if any(not set(scene.get("images", [])).issubset(c.get("loadedImages") or []) for c in rows):
                return False
            if not scene.get("scrollAll"):
                if positions != (["top", "bottom"] if scene["bottom"] else ["top"]):
                    return False
                continue
            if positions == ["top"] and rows[0].get("scroll") is None:
                continue
            if len(rows) < 2 or len(rows) > 65 or positions != ["top", *[f"middle-{i}" for i in range(1, len(rows) - 1)], "bottom"]:
                return False
            metrics = [c.get("scroll") for c in rows]
            if any(not isinstance(s, dict) or any(type(s.get(key)) not in (int, float) or not math.isfinite(s[key]) for key in ("offset", "viewport", "end"))
                   or s["viewport"] <= 0 or s["end"] <= 0 for s in metrics):
                return False
            if metrics[0]["offset"] > 1 or metrics[-1]["offset"] < metrics[-1]["end"] - 1:
                return False
            if any(b["offset"] <= a["offset"] or b["offset"] - a["offset"] > a["viewport"] * 0.8 + 1 for a, b in zip(metrics, metrics[1:])):
                return False
    return len({c.get("file") for c in captures}) == len(captures)


def capture_plan(hub, output, selected, requires_native=False):
    return {"hub": hub, "output": output.as_uri(), "requiresNative": requires_native, "extensionShowcase": selected}


def stop(process):
    if process is not None and process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)


def wait_for_fixture(process, hub, identity, timeout=20):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError("The isolated fixture exited before readiness")
        try:
            with urllib.request.urlopen(hub + "/api/v1/health", timeout=1) as response:
                if response.status == 200:
                    health = json.load(response)
                    if health.get("hub_id") != identity or health.get("version") != "mock":
                        raise RuntimeError("The chosen port belongs to another hub")
                    return
        except (urllib.error.URLError, TimeoutError):
            pass
        time.sleep(0.1)
    raise TimeoutError("The isolated fixture did not become ready")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--app", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--hub-config", type=Path, help="A disposable test device credential JSON under /tmp; otherwise use mock HTTP")
    parser.add_argument("--port", type=int, default=17490)
    parser.add_argument("--style", default="glass", choices=["glass", "clay", "island", "lark", "beautiful"])
    parser.add_argument("--pages", help="Comma-separated snapshot names; default: every shared snapshot")
    parser.add_argument("--timeout", type=float, default=1200)
    args = parser.parse_args()
    started = time.monotonic()
    fixture = app = None
    try:
        output = args.output.resolve()
        if output.exists() or not str(output).startswith(("/tmp/", "/private/tmp/")):
            raise ValueError("Output must be a new directory under /tmp")
        binary = args.app.resolve() / "Contents/MacOS/Persona"
        if not binary.is_file():
            raise ValueError("Build PersonaMac first; the app executable does not exist")
        selected, hashes = scenes(args.pages.split(",") if args.pages else None)
        token = "mock"
        if args.hub_config:
            credential_file = args.hub_config.resolve()
            if not str(credential_file).startswith(("/tmp/", "/private/tmp/")):
                raise ValueError("Use a disposable test credential file under /tmp, never production configuration")
            credential = json.loads(credential_file.read_text())
            hub, token = credential["url"].rstrip("/"), credential["token"]
            address = urllib.parse.urlparse(hub)
            if address.scheme != "http" or address.hostname != "127.0.0.1" or address.port is None or address.username or address.password or address.query or address.fragment:
                raise ValueError("The test hub must be reached through a dedicated loopback listener/tunnel")
        else:
            with socket.socket() as probe:
                probe.bind(("127.0.0.1", args.port))
            hub = "http://127.0.0.1:" + str(args.port)
        with tempfile.TemporaryDirectory(prefix="persona-mac-showcase-", dir="/tmp") as temporary:
            directory = Path(temporary)
            plan = directory / "plan.json"
            plan.write_text(json.dumps(capture_plan(hub, output, selected, requires_native=args.hub_config is not None)))
            identity = str(uuid.uuid4())
            inherited = {key: os.environ[key] for key in ("PATH", "HOME", "TMPDIR", "LANG", "LC_ALL") if key in os.environ}
            env = dict(inherited, PORT=str(args.port), MOCK_HUB_ID=identity, MOCK_EXTENSIONS="1", MOCK_EXTENSION_SHOWCASE="1",
                       MOCK_MACHINE_NAMES=json.dumps({"server": "Test computer A", "macbook": "Test computer B", "studio": "Test computer C"}))
            with (directory / "fixture.log").open("wb") as fixture_log, (directory / "app.log").open("wb") as app_log:
                if args.hub_config is None:
                    fixture = subprocess.Popen(["python3", str(ROOT / "scripts/mock/mock_hub.py")], cwd=ROOT,
                                               env=env, stdin=subprocess.DEVNULL, stdout=fixture_log, stderr=subprocess.STDOUT)
                    wait_for_fixture(fixture, hub, identity)
                pair = "persona://pair?url=" + urllib.parse.quote(hub, safe="") + "&token=" + urllib.parse.quote(token, safe="")
                app = subprocess.Popen([str(binary), "-ApplePersistenceIgnoreState", "YES", "-pairURL", pair,
                                        "-macSnapshot", str(plan), "-mac.sidebar.hidden", "YES", "-ui.style", args.style,
                                        "-tab", "settings", "-ui.language", "zh-Hans", "-AppleLanguages", "(zh-Hans)"],
                                       cwd=ROOT, stdin=subprocess.DEVNULL, stdout=app_log, stderr=subprocess.STDOUT)
                try:
                    status = app.wait(timeout=args.timeout)
                finally:
                    stop(app)
                    stop(fixture)
                log = (directory / "app.log").read_text(errors="replace").replace(token, "<test credential>")
                log = log.replace(urllib.parse.quote(token, safe=""), "<test credential>")
                if output.exists():
                    (output / "app.log").write_text(log)
                if status != 0:
                    raise RuntimeError("Mac app failed: " + log[-3000:])
                if not (output / "captures.json").is_file():
                    raise RuntimeError("The app exited without a capture manifest: " + log[-3000:])
                captures = json.loads((output / "captures.json").read_text())
                if args.hub_config and any(not native_revision(capture.get("nativeRevision")) for capture in captures):
                    raise RuntimeError("An installed-extension capture lacks a positive committed native revision")
                if args.hub_config:
                    closed = json.loads((output / "closed-pages.json").read_text())
                    if not native_closes(closed, selected):
                        raise RuntimeError("Every native page must have an acknowledged close in both appearances")
                if not complete_captures(captures, selected):
                    raise RuntimeError("The app did not capture every selected page/appearance, loaded image or overlapping scroll position")
                if any(not (output / capture["file"]).is_file() for capture in captures):
                    raise RuntimeError("A reported capture file is missing")
                (output / "source-sha256.json").write_text(json.dumps(hashes, indent=2, sort_keys=True) + "\n")
                (output / "capture-mode.json").write_text(json.dumps({"backend": "installed-test-extension" if args.hub_config else "auxiliary-mock-http",
                    "style": args.style, "capture": "actual-app-offscreen", "source": "shared-snapshot-inputs",
                    "readiness": "positive-native-committed-revision" if args.hub_config else "rendered-page"}, indent=2) + "\n")
                (output / "app.log").write_text(log)
                print("RESULT Mac showcase: PASS; %d captures; %.1fs; %s" % (len(captures), time.monotonic() - started, output))
                return 0
    except (subprocess.TimeoutExpired, TimeoutError) as error:
        print("RESULT Mac showcase: TIMEOUT; %.1fs; %s" % (time.monotonic() - started, error))
        return 124
    except (OSError, ValueError, RuntimeError) as error:
        print("RESULT Mac showcase: FAIL; %.1fs; %s" % (time.monotonic() - started, error))
        return 1
    finally:
        stop(app)
        stop(fixture)


if __name__ == "__main__":
    raise SystemExit(main())
