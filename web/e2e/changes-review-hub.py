"""Loopback-only Changes review fixture with the normal mock transcript and a real, temporary Git repository.

Run with PORT=<port> python3 web/e2e/changes-review-hub.py. Stop the process to release its temporary checkout.
"""
import hashlib
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
from urllib.parse import parse_qs, urlparse

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "scripts/mock"))
os.environ.update(MOCK_TURN_CHANGES="1", MOCK_REVIEW="1")
import mock_hub as hub

SID = hub.TURN_CHANGES
CWD = "/Users/me/code/weather"
SOURCE = "src/forecast/rain/window.ts"
TEST = "tests/forecast/rain-window.test.ts"
NOTE = "docs/forecast.md"
CACHE = "/fixture/immutable/window.ts"
SOURCE_TEXT = """// Hourly rain window
import type { Forecast, Hour, WindowOptions } from "./forecast";

const defaultWindow = 24;

export function rainWindow(forecast: Forecast, options: WindowOptions): Hour[] {
  const { hourly } = forecast;
  const end = options.until ?? hourly.length;
  const start = Math.max(0, options.start ?? 0);
  const limit = Math.min(end, start + 6);
  const hours = hourly.slice(start, limit);
  return hours.filter((hour) => hour.precipitationProbability >= options.threshold);
}

export function hasRain(hours: Hour[]): boolean {
  return hours.some((hour) => hour.precipitationProbability > 0);
}

export function windowLabel(hours: Hour[]): string {
  return hours.length.toString();
}
"""
BEFORE = "const limit = Math.min(end, start + 6);"
MIDDLE = "const limit = Math.min(end, start + 12);"
FINAL = "const limit = Math.min(end, start + defaultWindow);"
TEST_BEFORE = '  expect(rainWindow(forecast, options)).toHaveLength(6);'
TEST_AFTER = '  expect(rainWindow(forecast, options)).toHaveLength(24);'

def fixture(directory):
    def git(*args):
        return subprocess.check_output(["git", "-C", str(directory), *args], text=True)

    def write(path, body):
        target = directory / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(body)

    git("init", "-q", "-b", "main")
    write(SOURCE, SOURCE_TEXT)
    write(TEST, 'it("uses the full forecast window", () => {\n' + TEST_BEFORE + '\n});\n')
    git("add", "--", SOURCE, TEST)
    git("-c", "user.name=Review Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgSign=false",
        "commit", "-qm", "Original forecast window")
    sha = git("rev-parse", "HEAD").strip()
    base = git("show", "HEAD:" + SOURCE)
    digest = hashlib.sha256(base.encode()).hexdigest()
    write(SOURCE, base.replace(BEFORE, FINAL))
    write(TEST, git("show", "HEAD:" + TEST).replace(TEST_BEFORE, TEST_AFTER))
    # A change made outside the session. The review must not attribute it to the exchange.
    with (directory / TEST).open("a") as stream:
        stream.write("// unrelated working-tree edit\n")

    def native(path):
        hunks = []
        for line in git("diff", "--no-ext-diff", "--no-color", "--unified=3", "HEAD", "--", path).splitlines():
            match = re.match(r"^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@\s*(.*)$", line)
            if match:
                hunks.append(dict(old_start=int(match[1]) + (match[2] == "0"),
                                  new_start=int(match[3]) + (match[4] == "0"), header=match[5], lines=[]))
            elif hunks:
                hunks[-1]["lines"].append(line)
        return hunks

    def say(text, when):
        return hub.it("assistant", "text", when, text=text)

    def edit(name, old, new, line, when, fingerprint=None):
        path = CWD + "/" + name
        pair = hub.tool("Edit", {"file_path": path, "old_string": old, "new_string": new}, "ok", when)
        evidence = {"path": path}
        if fingerprint:
            evidence["before_sha256"] = fingerprint
        pair[1]["outcome"] = {"status": "ok", "files": [evidence],
                              "patch": "@@ -%d +%d @@\n-%s\n+%s" % (line, line, old, new)}
        return pair

    earlier = hub.it("user", "text", 36, text="Add a note about hourly rain.")
    note_path = CWD + "/" + NOTE
    note = hub.tool("Write", {"file_path": note_path, "content": "# Rain windows\n\nUse the hourly forecast.\n"}, "ok", 35)
    note[1]["outcome"] = {"status": "ok", "files": [{"path": note_path, "created": True}]}
    items = [earlier, *note, say("Added the forecast note.", 34),
             hub.it("user", "text", 25, text="Extend the hourly rain window to a full day and update its test.")]
    items += edit(SOURCE, "  " + BEFORE, "  " + MIDDLE, 10, 24, digest)
    items += [say("The first pass takes twelve hours.", 23)]
    items += edit(SOURCE, "  " + MIDDLE, "  " + FINAL, 10, 22)
    items += [say("The widget now uses its configured daily window.", 21)]
    test_base = git("show", "HEAD:" + TEST)
    items += edit(TEST, TEST_BEFORE, TEST_AFTER, 2, 20, hashlib.sha256(test_base.encode()).hexdigest())
    items += [say("The rain window now covers a full day. Its forecast test expects 24 hourly entries.", 19)]
    hub.ITEMS[SID] = items
    for session in hub.SESSIONS:
        if session.get("session_id") == SID:
            session["title"] = "Extend the rain forecast window"
    blobs = {SOURCE: (CACHE, base), TEST: (CACHE + ".test", test_base)}

    class Handler(hub.H):
        def do_GET(self):
            parsed = urlparse(self.path)
            query = parse_qs(parsed.query)
            path = query.get("path", [""])[0]
            route = parsed.path.removeprefix("/api/v1/machines/macbook/")
            if route == "git/review" and path in blobs:
                return self.send_json({"root": CWD, "scope": query.get("scope", ["uncommitted"])[0], "base": {"hash": sha},
                                       "files": [{"path": path, "status": "modified", "hunks": native(path)}]})
            if route == "git/blob" and path in blobs and query.get("rev") == [sha]:
                cache, text = blobs[path]
                return self.send_json({"path": cache, "size": len(text.encode())})
            if route == "file":
                for cache, text in blobs.values():
                    if path == cache:
                        return self.send_json({"path": cache, "name": cache.split("/")[-1], "text": text,
                                               "size": len(text.encode()), "truncated": False, "is_dir": False})
            return super().do_GET()

    with hub.ThreadingHTTPServer(("127.0.0.1", hub.PORT), Handler) as server:
        server.daemon_threads = True
        hub.ENDPOINTS.append(hub.endpoint("127.0.0.1", server.server_port, "http", "lan"))
        print(f"review fixture on 127.0.0.1:{server.server_port}, Git base {sha}", flush=True)
        server.serve_forever()

if __name__ == "__main__":
    with tempfile.TemporaryDirectory(prefix="persona-review-") as temporary:
        fixture(Path(temporary))
