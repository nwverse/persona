"""Exercise the partner hubs mock (scripts/mock/partner_hubs_mock.py: /api/v1/partner-hubs…, /api/v1/partner-messages/prefs…)
without a browser, in each MOCK_PARTNER_HUBS mode."""
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import unittest
from urllib.error import HTTPError, URLError
from urllib.parse import parse_qs, urlparse
from urllib.request import ProxyHandler, Request, build_opener

# The mock is on loopback: a proxy from the environment must not get in the way.
urlopen = build_opener(ProxyHandler({})).open
OTHER = "persona://partner?hub=9d0c2b1a-0000-4000-8000-00000000000d&name=Studio&fp=c3R1ZGlv&tls=18662&code=jh_" + "ab" * 32


class PartnerHubsMockTests(unittest.TestCase):
    mode = "1"

    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.base = f"http://127.0.0.1:{port}"
        cls.log = tempfile.TemporaryFile()
        cls.process = subprocess.Popen(
            ["python3", "-I", str(Path(__file__).with_name("mock_hub.py"))],
            env=dict(os.environ, PORT=str(port), MOCK_PARTNER_HUBS=cls.mode), stdout=cls.log, stderr=cls.log)
        cls.addClassCleanup(cls.stop)
        for _ in range(60):
            if cls.process.poll() is not None:
                raise RuntimeError("mock hub exited before startup")
            try:
                with urlopen(cls.base + "/api/v1/health", timeout=1):
                    return
            except (URLError, TimeoutError):
                time.sleep(0.1)
        raise RuntimeError("mock hub startup timed out")

    @classmethod
    def stop(cls):
        cls.process.terminate()
        try:
            cls.process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            cls.process.kill()
            cls.process.wait(timeout=5)
        cls.log.close()

    def setUp(self):
        with urlopen(self.base + "/mock/reset", timeout=3):
            pass

    def request(self, path, method="GET", body=None):
        request = Request(self.base + path, data=None if body is None else json.dumps(body).encode(), method=method,
                          headers={"Content-Type": "application/json"})
        with urlopen(request, timeout=3) as response:
            return json.load(response)

    def refused(self, path, method="GET", body=None):
        with self.assertRaises(HTTPError) as error:
            self.request(path, method, body)
        with error.exception as refusal:
            return refusal.code, json.load(refusal)

    def features(self):
        return self.request("/api/v1/health").get("features", [])


class WithPartners(PartnerHubsMockTests):
    def test_lists_two_partners_and_the_feature(self):
        self.assertIn("partner_hubs", self.features())
        listed = self.request("/api/v1/partner-hubs")
        self.assertEqual((listed["enabled"], listed["member"], listed["can_issue"]), (True, False, True))
        self.assertEqual([(x["name"], x["state"], x["dial"]) for x in listed["hubs"]], [("家里", "connected", "auto"), ("工作室", "offline", "never")])
        offline = listed["hubs"][1]["hub_id"]
        self.assertEqual(self.request("/api/v1/partner-messages/prefs"), {"prefs": [{"hub_id": offline, "confirm": False}]})

    def test_changes_each_setting(self):
        first, second = (x["hub_id"] for x in self.request("/api/v1/partner-hubs")["hubs"])
        self.assertEqual(self.request(f"/api/v1/partner-messages/prefs/{first}", "PUT", {"confirm": False}), {"hub_id": first, "confirm": False})
        self.assertIn({"hub_id": first, "confirm": False}, self.request("/api/v1/partner-messages/prefs")["prefs"])
        self.assertEqual(self.refused(f"/api/v1/partner-messages/prefs/{first}", "PUT", {"confirm": "no"})[0], 400)
        dialed = self.request(f"/api/v1/partner-hubs/{second}", "PATCH", {"dial": "auto"})
        self.assertEqual((dialed["dial"], dialed["state"]), ("auto", "connecting"))
        self.assertEqual(self.request(f"/api/v1/partner-hubs/{second}", "DELETE"), {"ok": True})
        self.assertEqual([x["hub_id"] for x in self.request("/api/v1/partner-hubs")["hubs"]], [first])
        self.assertEqual(self.refused(f"/api/v1/partner-hubs/{second}", "DELETE")[0], 404)
        log = self.request("/mock/partner_hubs")["log"]
        self.assertEqual(log[-2:], [f"DELETE /api/v1/partner-hubs/{second}", f"DELETE /api/v1/partner-hubs/{second}"])
        self.assertEqual(len(self.request("/mock/partner_hubs?reset=1")["hubs"]), 2)

    def test_makes_a_code_and_pairs_with_another(self):
        made = self.request("/api/v1/partner-hubs/code", "POST")
        q = {k: v[0] for k, v in parse_qs(urlparse(made["code"]).query).items()}
        self.assertTrue(made["code"].startswith("persona://partner?"))
        self.assertRegex(q["code"], r"^jh_[0-9a-f]{64}$")
        self.assertTrue(q["fp"] and q["hub"] and made["expires_at"].endswith("Z"))
        self.assertEqual(self.refused("/api/v1/partner-hubs", "POST", {"code": made["code"]})[1]["code"], "own_code")
        self.assertEqual(self.refused("/api/v1/partner-hubs", "POST", {"code": "persona://peer?hub=x"})[0], 400)
        self.assertEqual(self.refused("/api/v1/partner-hubs", "POST", {"code": OTHER[:-4] + "dead"})[0], 401)
        paired = self.request("/api/v1/partner-hubs", "POST", {"code": OTHER})
        self.assertEqual((paired["name"], paired["state"]), ("Studio", "connecting"))
        self.assertEqual(len(self.request("/api/v1/partner-hubs")["hubs"]), 3)
        # A pairing is never replaced: the same hub again is refused until it is unpaired.
        code, body = self.refused("/api/v1/partner-hubs", "POST", {"code": OTHER})
        self.assertEqual((code, body["code"]), (409, "already_paired"))
        self.assertEqual(len(self.request("/api/v1/partner-hubs")["hubs"]), 3)


class WithoutPartners(PartnerHubsMockTests):
    mode = "empty"

    def test_has_none_yet(self):
        self.assertEqual(self.request("/api/v1/partner-hubs")["hubs"], [])
        self.assertEqual(self.request("/api/v1/partner-messages/prefs"), {"prefs": []})


class WithoutTLS(PartnerHubsMockTests):
    mode = "noissue"

    def test_cannot_show_a_code(self):
        self.assertFalse(self.request("/api/v1/partner-hubs")["can_issue"])
        code, body = self.refused("/api/v1/partner-hubs/code", "POST")
        self.assertEqual((code, body["code"]), (409, "no_tls"))


class SwitchedOff(PartnerHubsMockTests):
    mode = ""

    def test_is_a_hub_without_them(self):
        self.assertNotIn("partner_hubs", self.features())


del PartnerHubsMockTests

if __name__ == "__main__":
    unittest.main()
