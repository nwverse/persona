"""The link reopening device helper: its test server and the way it reads a uiautomator dump (no device or adb involved)."""
import importlib.util
import json
from pathlib import Path
import tempfile
import threading
import unittest
import urllib.error
import urllib.request

SPEC = importlib.util.spec_from_file_location("link_reopen", Path(__file__).with_name("android_link_reopen_check.py"))
check = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(check)

DUMP = """<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<hierarchy rotation="0"><node text="" content-desc="Close page" bounds="[900,100][1000,200]" />
<node text="page=a load=7 history=1" content-desc="" bounds="[0,300][1080,400]" />
<node text="http://192.0.2.10:8000/a" content-desc="" bounds="[100,1000][700,1100]" /></hierarchy>"""


class ServerTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.log = str(Path(self.tmp.name) / "requests.jsonl")
        self.server = check.make_server("127.0.0.1", 0, self.log)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.base = f"http://127.0.0.1:{self.server.server_address[1]}"

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.tmp.cleanup()

    def get(self, path, cookie=None):
        request = urllib.request.Request(self.base + path, headers={"Cookie": cookie} if cookie else {})
        with urllib.request.urlopen(request) as response:
            return response.read().decode(), response

    def test_every_request_is_logged_with_a_sequence_a_time_and_the_cookie(self):
        self.get("/a")
        self.get("/whoami", "session=kept")
        entries = check.read_log(self.log)
        self.assertEqual([(entry["seq"], entry["path"], entry["cookie"]) for entry in entries], [(1, "/a", False), (2, "/whoami", True)])
        self.assertLessEqual(entries[0]["t"], entries[1]["t"])

    def test_a_page_carries_the_load_number_of_the_request_that_made_it_and_is_never_cached(self):
        first, response = self.get("/a")
        second, _ = self.get("/a")
        self.assertIn("load=1 ", first)
        self.assertIn("load=2 ", second)
        self.assertEqual(response.headers["Cache-Control"], "no-store")

    def test_whoami_reports_the_cookie_only_when_it_arrives(self):
        self.assertIn("cookie=no", self.get("/whoami")[0])
        self.assertIn("cookie=yes", self.get("/whoami", "session=kept")[0])

    def test_login_sets_the_cookie_and_goes_to_a(self):
        class NoRedirect(urllib.request.HTTPRedirectHandler):
            def redirect_request(self, *args, **kwargs):
                return None
        try:
            urllib.request.build_opener(NoRedirect).open(self.base + "/login")
        except urllib.error.HTTPError as error:
            self.assertEqual(error.code, 302)
            self.assertEqual(error.headers["Location"], "/a")
            self.assertIn("session=kept", error.headers["Set-Cookie"])
        else:
            self.fail("login should redirect")


class DumpTest(unittest.TestCase):
    def test_nodes_are_found_by_text_or_description_and_tapped_at_their_center(self):
        nodes = check.parse_nodes(DUMP)
        self.assertEqual(check.center(check.find(nodes, desc="Close page")["bounds"]), (950, 150))
        self.assertEqual(check.center(check.find(nodes, text="192.0.2.10:8000/a")["bounds"]), (400, 1050))
        self.assertIsNone(check.find(nodes, desc="Missing"))

    def test_the_page_state_is_read_from_its_state_line_only(self):
        self.assertEqual(check.page_state(check.parse_nodes(DUMP)), ("a", 7, 1))
        self.assertIsNone(check.page_state([{"text": "history=1", "desc": "", "bounds": (0, 0, 1, 1)}]))


if __name__ == "__main__":
    unittest.main()
