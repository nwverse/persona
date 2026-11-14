#!/usr/bin/env python3
"""Device check for ordinary link reopening (android/feature/chat/LINKS.md, "Reopening a link").

    android_link_reopen_check.py serve [--host ADDRESS] [--port PORT] [--log FILE]
    android_link_reopen_check.py reopen --serial SERIAL --base URL --log FILE
    android_link_reopen_check.py cookie --serial SERIAL --base URL --log FILE

`serve` runs the test pages on this computer and logs every request with a timestamp. Bind it to an address the phone can reach
that is not loopback (`adb reverse` would turn it into a forwarded port, a different code path); with no --host it uses this
computer's outbound address. It prints the links to send, one per message, so the chat shows them.

`reopen` and `cookie` drive the open card through `adb` and read the page back with `uiautomator dump`, and judge only what can be
observed: the page's own `history.length` and load number (the server request that produced it), and the server log's
timestamps. They do not judge renderer process counts, the phone's loopback traffic, or how many requests a reopen makes
(a cache can answer a load without one). The UI language must be English, or pass the labels.
"""
import argparse
import http.server
import json
from pathlib import Path
import re
import socket
import socketserver
import subprocess
import sys
import threading
import time
import xml.etree.ElementTree as ET

PAGE = """<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width"><title>Page {name}</title>
<body><h1>Page {name}</h1><p id=state>page={name} load={load} history=loading</p><p><a href="{other}">Go to {other_name}</a></p>
<script>document.getElementById('state').textContent='page={name} load={load} history='+history.length;</script>"""
NO_STORE = {"Cache-Control": "no-store"}


class Pages(http.server.BaseHTTPRequestHandler):
    """/a and /b show their load number and history.length; /login sets a cookie; /whoami says whether it came back."""
    sequence = 0
    lock = threading.Lock()
    log_path = None

    def log_message(self, *args):
        pass

    def reply(self, status, body, headers=()):
        data = body.encode()
        self.send_response(status)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(data)))
        for key, value in {**NO_STORE, **dict(headers)}.items():
            self.send_header(key, value)
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        cls = type(self)
        with cls.lock:
            cls.sequence += 1
            load = cls.sequence
            path = self.path.split("?", 1)[0]
            cookie = "session=kept" in (self.headers.get("Cookie") or "")
            if cls.log_path:
                with open(cls.log_path, "a") as stream:
                    stream.write(json.dumps({"seq": load, "t": time.time(), "path": path, "cookie": cookie}) + "\n")
        if path == "/a":
            self.reply(200, PAGE.format(name="a", load=load, other="/b", other_name="B"))
        elif path == "/b":
            self.reply(200, PAGE.format(name="b", load=load, other="/a", other_name="A"))
        elif path == "/login":
            self.reply(302, "", {"Location": "/a", "Set-Cookie": "session=kept; Max-Age=86400; Path=/"})
        elif path == "/whoami":
            self.reply(200, f"<!doctype html><meta charset=utf-8><body><p>whoami load={load} cookie={'yes' if cookie else 'no'}</p>")
        else:
            self.reply(404, "not found")


class Server(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True


def outbound_address():
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as probe:
        probe.connect(("192.0.2.1", 9))  # a documentation address: nothing is sent
        return probe.getsockname()[0]


def make_server(host, port, log_path):
    handler = type("Handler", (Pages,), {"sequence": 0, "lock": threading.Lock(), "log_path": log_path})
    return Server((host, port), handler)


def read_log(path):
    if not Path(path).exists():
        return []
    return [json.loads(line) for line in Path(path).read_text().splitlines() if line.strip()]


# What uiautomator dump gives: nodes with text, content-desc and bounds "[l,t][r,b]".
def parse_nodes(xml_text):
    nodes = []
    for node in ET.fromstring(xml_text).iter("node"):
        match = re.fullmatch(r"\[(\d+),(\d+)\]\[(\d+),(\d+)\]", node.get("bounds", ""))
        if match:
            nodes.append({"text": node.get("text", ""), "desc": node.get("content-desc", ""),
                          "bounds": tuple(int(group) for group in match.groups())})
    return nodes


def center(bounds):
    left, top, right, bottom = bounds
    return (left + right) // 2, (top + bottom) // 2


def find(nodes, *, text=None, desc=None):
    for node in nodes:
        if text is not None and text in node["text"]:
            return node
        if desc is not None and node["desc"] == desc:
            return node
    return None


STATE = re.compile(r"page=(\w+) load=(\d+) history=(\d+)")


def page_state(nodes):
    """(page, load, history) from the test page's state line, or None while it is not on screen."""
    for node in nodes:
        match = STATE.search(node["text"])
        if match:
            return match.group(1), int(match.group(2)), int(match.group(3))
    return None


class Device:
    def __init__(self, serial):
        self.serial = serial

    def adb(self, *args):
        return subprocess.run(["adb", "-s", self.serial, *args], check=True, capture_output=True, text=True).stdout

    def nodes(self):
        out = self.adb("exec-out", "uiautomator", "dump", "/dev/tty")
        return parse_nodes(out[out.index("<?xml"):out.rindex("</hierarchy>") + len("</hierarchy>")])

    def tap(self, node):
        x, y = center(node["bounds"])
        self.adb("shell", "input", "tap", str(x), str(y))

    def wait(self, ready, what, timeout=20):
        end = time.monotonic() + timeout
        while time.monotonic() < end:
            found = ready(self.nodes())
            if found:
                return found
            time.sleep(0.5)
        raise SystemExit(f"timed out waiting for {what}")

    def tap_when_present(self, what, **target):
        self.tap(self.wait(lambda nodes: find(nodes, **target), what))


def open_link(device, base, path):
    device.tap_when_present(f"the message link {path}", text=base + path)
    return time.time()


def close_card(device, label):
    device.tap_when_present("the close button", desc=label)
    # The card is gone once the page's state line is.
    device.wait(lambda nodes: page_state(nodes) is None and find(nodes, desc=label) is None, "the card to close")
    return time.time()


def reopen(device, base, log, close_label, settle):
    timeline, failures, notes = {}, [], []
    timeline["first_open"] = open_link(device, base, "/a")
    _, first_load, first_history = device.wait(page_state, "page A")
    device.tap_when_present("the link to B", text="Go to B")
    device.wait(lambda nodes: (state := page_state(nodes)) and state[0] == "b" and state, "page B")
    timeline["closed"] = close_card(device, close_label)
    time.sleep(settle)
    timeline["second_open"] = open_link(device, base, "/a")
    _, second_load, second_history = device.wait(page_state, "page A again")
    entries = read_log(log)
    produced = next((entry for entry in entries if entry["seq"] == second_load), None)
    if first_history != 1:
        failures.append(f"first opening showed history.length {first_history}, expected 1")
    if second_history != 1:
        failures.append(f"the reopened page has history.length {second_history}: it kept the in-page history")
    if second_load == first_load:
        failures.append("the reopened page is the load the first opening showed, not a new one")
    if produced is None or produced["path"] != "/a":
        failures.append(f"load {second_load} is not a logged /a request")
    else:
        if produced["t"] < timeline["closed"] - 1:
            failures.append("the reopened page was requested before the card closed")
        # A device that prerenders also loads a prerendered page again when it is opened, so the time of this request
        # relative to the tap is reported, not judged.
        notes.append("the reopened page's request came %s the tap" % ("after" if produced["t"] > timeline["second_open"] else "before"))
    if any(entry["path"] == "/b" and entry["t"] > timeline["closed"] for entry in entries):
        failures.append("page B was requested after the card closed")
    return dict(timeline=timeline, first=dict(load=first_load, history=first_history),
                reopened=dict(load=second_load, history=second_history, requested_at=produced and produced["t"]),
                notes=notes, failures=failures)


def cookie(device, base, log, close_label, settle):
    failures, answers = [], []
    open_link(device, base, "/login")
    device.wait(page_state, "page A after login")
    close_card(device, close_label)
    for round_ in range(2):
        time.sleep(settle)
        open_link(device, base, "/whoami")
        node = device.wait(lambda nodes: find(nodes, text="whoami load="), "the whoami page")
        answers.append(node["text"])
        if "cookie=yes" not in node["text"]:
            failures.append(f"whoami opening {round_ + 1} did not carry the cookie: {node['text']}")
        close_card(device, close_label)
    served = [entry for entry in read_log(log) if entry["path"] == "/whoami"]
    return dict(answers=answers, whoami_requests=served, failures=failures)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    parser.add_argument("command", choices=["serve", "reopen", "cookie"])
    parser.add_argument("--host")
    parser.add_argument("--port", type=int, default=0)
    parser.add_argument("--log", default="link-reopen-requests.jsonl")
    parser.add_argument("--serial")
    parser.add_argument("--base", help="the server's address as the phone sees it, e.g. http://<address>:<port>")
    parser.add_argument("--close-label", default="Close page")
    parser.add_argument("--settle", type=float, default=3.0, help="seconds between closing and reopening")
    args = parser.parse_args(argv)
    if args.command == "serve":
        host = args.host or outbound_address()
        server = make_server(host, args.port, args.log)
        base = f"http://{host}:{server.server_address[1]}"
        print(f"Serving {base}; requests are logged to {args.log}")
        print("Send each of these as its own message (one link per message, so a tap lands on the right link):", flush=True)
        for path in ("/a", "/login", "/whoami"):
            print(f"  {base}{path}", flush=True)
        try:
            server.serve_forever()
        except KeyboardInterrupt:
            pass
        return 0
    if not (args.serial and args.base):
        parser.error("--serial and --base are required")
    device = Device(args.serial)
    run = reopen if args.command == "reopen" else cookie
    report = run(device, args.base.rstrip("/"), args.log, args.close_label, args.settle)
    print(json.dumps(report, indent=2))
    print(f"android_link_reopen_check: {'FAIL' if report['failures'] else 'PASS'} {args.command}")
    return 1 if report["failures"] else 0


if __name__ == "__main__":
    sys.exit(main())
