#!/usr/bin/env python3
"""Cross the regular-window boundary and back in quick succession while the Settings sheet is open, in one process.

The Settings sheet's pages swap between the list-detail scene and the single-page scene when a window narrows below the
regular-window width. Done twice within a fraction of a second (a free window's edge dragged across the boundary, or a
display's size and density changed one after the other), that swap crashed Compose's runtime with a movable-content
exception while the sheet's dialog window was open (docs/lessons/android.md). Each round launches the debug build
against a mock hub, opens Settings (and a page when --page), changes the display size to a compact window and its density
to a different regular one right after, and passes only when the same process is still there with no fatal exception.

    scripts/app/android_settings_window_flip.py -s emulator-5604 --mock-port 19001 --rounds 8 --page --out /tmp/flip

Needs a tablet or open-foldable display (at least 600dp wide) and a free local port for the mock hub. Restores the
display it found and stops its mock hub. The samples are few: report the round count with the result, never a rate.
"""
import argparse
import os
import re
import signal
import subprocess
import sys
import time
import xml.etree.ElementTree as ET
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
APP = "com.example.persona.shots"
ENTRY = APP + "/persona.android.debug.DebugEntryActivity"
SESSION = "server/b2e4d6f8-0000-4000-8000-000000000001"


class Device:
    def __init__(self, serial):
        self.serial = serial
        self.adb = os.path.join(os.environ.get("ANDROID_HOME", os.path.expanduser("~/Library/Android/sdk")), "platform-tools", "adb")

    def run(self, *args, timeout=30):
        done = subprocess.run([self.adb, "-s", self.serial, *args], capture_output=True, text=True, timeout=timeout)
        return done.stdout

    def shell(self, *args, timeout=30):
        return self.run("shell", *args, timeout=timeout)

    def pid(self):
        return self.shell("pidof", APP).strip()

    def fatal_count(self):
        return self.run("logcat", "-d").count("FATAL EXCEPTION")

    def find(self, **match):
        """The center of the first node whose attributes equal [match], from a fresh uiautomator dump."""
        for _ in range(5):
            self.shell("uiautomator", "dump", "/sdcard/flip-window.xml")
            xml = self.run("exec-out", "cat", "/sdcard/flip-window.xml")
            try:
                tree = ET.fromstring(xml)
            except ET.ParseError:
                time.sleep(1)
                continue
            for node in tree.iter("node"):
                if all(node.get(key.replace("_", "-")) == value for key, value in match.items()):
                    left, top, right, bottom = map(int, re.findall(r"\d+", node.get("bounds")))
                    return (left + right) // 2, (top + bottom) // 2
            time.sleep(1)
        raise SystemExit("FAIL: no node %r on screen" % (match,))

    def tap(self, **match):
        x, y = self.find(**match)
        self.shell("input", "tap", str(x), str(y))


def display(device):
    size = re.search(r"Physical size: (\d+)x(\d+)", device.shell("wm", "size"))
    density = re.search(r"Physical density: (\d+)", device.shell("wm", "density"))
    return int(size.group(1)), int(size.group(2)), int(density.group(1))


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("-s", "--serial", required=True, help="the emulator to use (emulator-NNNN)")
    parser.add_argument("--mock-port", type=int, required=True, help="a free local port for the mock hub")
    parser.add_argument("--rounds", type=int, default=8)
    parser.add_argument("--page", action="store_true", help="open the Models page inside Settings before the flips")
    parser.add_argument("--flips", type=int, default=1, help="how many size-and-density changes per round")
    parser.add_argument("--out", default="/tmp/settings-window-flip")
    args = parser.parse_args()
    if not args.serial.startswith("emulator-"):
        raise SystemExit("use a dedicated emulator (-s emulator-NNNN)")
    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    device = Device(args.serial)
    width, height, density = display(device)
    if width * 160 / density < 600:
        raise SystemExit("this display is narrower than 600dp: nothing to cross")
    # A display that halves its pixels and then its density: 1280x800dp -> 600x450dp (compact) -> 1200x900dp, as `wm size`
    # then `wm density` do on a 2560x1600 tablet at 320dpi.
    narrow = "%dx%d" % (width * 15 // 32, height * 9 // 16)
    wide_density = density // 2
    mock = subprocess.Popen([sys.executable, "-u", str(ROOT / "scripts/mock/mock_hub.py")], env={**os.environ, "PORT": str(args.mock_port)},
                            stdout=(out / "mock-hub.log").open("w"), stderr=subprocess.STDOUT)
    device.run("reverse", "tcp:%d" % args.mock_port, "tcp:%d" % args.mock_port)
    failures = 0
    try:
        time.sleep(3)
        for number in range(1, args.rounds + 1):
            device.shell("wm", "size", "reset")
            device.shell("wm", "density", "reset")
            device.shell("am", "force-stop", APP)
            device.shell("pm", "clear", APP)
            device.run("logcat", "-c")
            device.shell("am", "start", "-W", "-n", ENTRY, "--es", "debug.mockAddress", "http://127.0.0.1:%d" % args.mock_port,
                         "--es", "debug.openSession", SESSION, "--es", "debug.conversationKind", "assistant",
                         "--ez", "debug.automation", "true", "--es", "debug.style", "glass", "--es", "debug.theme", "light")
            device.tap(content_desc="Settings")
            time.sleep(3)
            if args.page:
                device.tap(text="Models")
                time.sleep(3)
            before = device.pid()
            for _ in range(args.flips):
                device.shell("wm size %s; wm density %d" % (narrow, wide_density))
                time.sleep(1)
                device.shell("wm size reset; wm density reset")
                time.sleep(1)
            time.sleep(4)
            after = device.pid()
            fatal = device.fatal_count()
            verdict = "ok" if before and before == after and fatal == 0 else "CRASH"
            if verdict != "ok":
                failures += 1
                (out / ("round-%d.log" % number)).write_text(device.run("logcat", "-d"))
            print("round %d: pid %s -> %s fatal=%d %s" % (number, before, after, fatal, verdict), flush=True)
    finally:
        device.shell("wm", "density", "reset")
        device.shell("wm", "size", "reset")
        device.run("reverse", "--remove", "tcp:%d" % args.mock_port)
        mock.send_signal(signal.SIGTERM)
        mock.wait(timeout=10)
    print("%s: %d of %d rounds crashed (%d size and density changes each, %s)" % (
        "FAIL" if failures else "PASS", failures, args.rounds, args.flips, "Models page open" if args.page else "Settings list"))
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
