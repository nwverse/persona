"""mock_hub.py's MOCK_SENT ledger answers as the hub does (docs/extensions/connectors.md "Sent as the user", docs/protocol/protocol.md
"Connector API", server/internal/extmgr/sent_ledger.go): the rows' shape, the paging, the connector filter, the
refusals. Run: python3 -m unittest scripts/mock/test_sent_ledger_mock.py (or from scripts/mock)."""
import os
import sys
import unittest
from datetime import datetime, timedelta, timezone
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import sent_ledger_mock as m  # noqa: E402

ROW_KEYS = {"id", "at", "connector", "action", "target", "text", "summary", "origin", "let_through", "message_ref", "recalled_at", "can_recall"}


class Fake:
    """The part of mock_hub.py's handler route() uses."""

    def __init__(self):
        self.json = None
        self.status = None
        self.headers = {}
        self.body = b""

        class W:
            def write(w, b):
                self.body += b
        self.wfile = W()

    def send_json(self, obj, status=200):
        self.json, self.status = obj, status

    def send_response(self, code):
        self.status = code

    def send_header(self, k, v):
        self.headers[k] = v

    def end_headers(self):
        pass


def ask(mode, query=None, path="/api/v1/connectors/sent", method="GET"):
    env = {"MOCK_SENT": mode} if mode is not None else {}
    with mock.patch.dict(os.environ, env, clear=False):
        if mode is None:
            os.environ.pop("MOCK_SENT", None)
        h = Fake()
        return m.route(h, method, path, query or {}), h


class Rows(unittest.TestCase):
    def test_seventy_rows_newest_first_by_id(self):
        r = m.rows()
        self.assertEqual([x["id"] for x in r], list(range(70, 0, -1)))
        ats = [datetime.fromisoformat(x["at"].replace("Z", "+00:00")) for x in r]
        self.assertEqual(ats, sorted(ats, reverse=True))
        self.assertLess(datetime.now(timezone.utc) - ats[-1], timedelta(days=90))

    def test_row_shape_is_the_ledger_s(self):
        for x in m.rows():
            self.assertLessEqual(set(x), ROW_KEYS)
            self.assertTrue({"id", "at", "connector", "action", "target", "text", "summary", "origin", "let_through"} <= set(x))
            for k in ("target", "origin", "let_through"):
                self.assertIsInstance(x[k], dict)
            self.assertLessEqual(len(x["text"]), m.TEXT_MOST)
            self.assertNotIn("\n", x["summary"])

    def test_all_four_ways_through_the_gate(self):
        r = m.rows()
        kinds = {x["let_through"]["kind"] for x in r}
        self.assertEqual(kinds, {"rule", "conversation_always", "card", "auto"})
        self.assertTrue(any(x["let_through"].get("answered_by") == "assistant" for x in r))
        self.assertTrue(any(x["let_through"]["kind"] == "auto" and x["let_through"].get("setting") for x in r))
        self.assertTrue(any(x["let_through"]["kind"] == "rule" and x["let_through"]["rule"]["decision"] == "allow" for x in r))
        # an extension's own record names the extension; the gate's own rows never do
        for x in r:
            self.assertEqual("extension" in x["origin"], x["action"] == "sent.record")
        self.assertTrue(any(x["origin"] == {} for x in r))

    def test_one_text_of_exactly_4000_characters_on_the_first_page(self):
        r = m.rows()
        long = [x for x in r if len(x["text"]) == m.TEXT_MOST]
        self.assertEqual(len(long), 1)
        self.assertIn(long[0], r[:m.DEFAULT_LIMIT])

    def test_a_row_with_no_text_and_connectors_for_each_shape(self):
        r = m.rows()
        self.assertTrue(any(x["text"] == "" and x["summary"] for x in r))
        self.assertTrue({"mail", "github", "lark", "examplechat"} <= {x["connector"] for x in r})
        self.assertTrue(any("arg_keys" in x["target"] for x in r))


class Paging(unittest.TestCase):
    def test_pages_of_fifty_by_next(self):
        r = m.rows()
        first = m.page(r)
        self.assertEqual(len(first["items"]), 50)
        self.assertEqual(first["next"], 21)  # the last row's id
        second = m.page(r, before=first["next"])
        self.assertEqual([x["id"] for x in second["items"]], list(range(20, 0, -1)))
        self.assertIsNone(second["next"])

    def test_a_page_that_ends_exactly_at_the_last_row_has_no_next(self):
        self.assertIsNone(m.page(m.rows(), limit=70)["next"])
        self.assertEqual(m.page(m.rows(), limit=69)["next"], 2)

    def test_connector_filter_pages_over_its_own_rows(self):
        r = m.rows()
        mails = [x for x in r if x["connector"] == "mail"]
        got = m.page(r, limit=10, connector="mail")
        self.assertEqual(got["items"], mails[:10])
        self.assertEqual(got["next"], mails[9]["id"])
        self.assertEqual(m.page(r, before=got["next"], limit=100, connector="mail")["items"], mails[10:])
        self.assertEqual(m.page(r, connector="nope"), {"items": [], "next": None})


class Route(unittest.TestCase):
    def test_not_set_is_not_ours(self):
        handled, h = ask(None)
        self.assertFalse(handled)
        self.assertIsNone(h.status)
        self.assertFalse(ask("")[0])
        self.assertFalse(ask("bogus")[0])

    def test_only_get_of_this_path(self):
        self.assertFalse(ask("1", path="/api/v1/connectors")[0])
        self.assertFalse(ask("1", method="POST")[0])
        self.assertFalse(ask("1", path="/api/v1/connectors/sent/1")[0])

    def test_answers_a_page_and_reads_the_query_as_the_hub_does(self):
        handled, h = ask("1")
        self.assertTrue(handled)
        self.assertEqual((h.status, len(h.json["items"]), h.json["next"]), (200, 50, 21))
        _, h = ask("1", {"before": ["41"], "limit": ["5"], "connector": ["github"]})
        self.assertTrue(all(x["connector"] == "github" and x["id"] < 41 for x in h.json["items"]))
        self.assertEqual(len(h.json["items"]), 5)
        _, h = ask("1", {"limit": ["1000"]})
        self.assertEqual(len(h.json["items"]), 70)  # at most 200: all there are

    def test_a_bad_number_is_a_400_as_the_hub_says(self):
        for q in ({"limit": ["0"]}, {"limit": ["x"]}, {"before": ["0"]}, {"before": ["-3"]}):
            _, h = ask("1", q)
            self.assertEqual(h.status, 400, q)
            self.assertIn("a number from 1", h.json["error"])

    def test_empty(self):
        _, h = ask("empty")
        self.assertEqual((h.status, h.json), (200, {"items": [], "next": None}))

    def test_no_database_is_503_with_its_code(self):
        _, h = ask("nodb")
        self.assertEqual((h.status, h.json["code"]), (503, "no_database"))

    def test_a_device_without_full_access_is_403(self):
        _, h = ask("forbidden")
        self.assertEqual((h.status, h.json["code"]), (403, "forbidden"))

    def test_an_old_hub_is_a_404_in_plain_text_with_no_code(self):
        handled, h = ask("old")
        self.assertTrue(handled)
        self.assertEqual(h.status, 404)
        self.assertIsNone(h.json)
        self.assertEqual(h.headers["Content-Type"], "text/plain; charset=utf-8")
        self.assertEqual(h.body, b"404 page not found\n")

    def test_a_running_mock_switches_state_over_mock_sent(self):
        try:
            _, h = ask("1", path="/mock/sent")
            self.assertEqual(h.json, {"mode": "1"})
            _, h = ask("1", {"mode": ["nodb"]}, path="/mock/sent")
            self.assertEqual(h.json, {"mode": "nodb"})
            _, h = ask("1", {"mode": ["nope"]}, path="/mock/sent")
            self.assertEqual(h.status, 400)
            _, h = ask("1")  # MOCK_SENT says 1, but the switch was made
            self.assertEqual(h.status, 503)
            _, h = ask("1", {"mode": ["1"]}, path="/mock/sent")
            _, h = ask("1")
            self.assertEqual(h.status, 200)
        finally:
            m._now["mode"] = None

    def test_the_switch_is_there_only_when_MOCK_SENT_is(self):
        self.assertFalse(ask(None, path="/mock/sent")[0])
        self.assertFalse(ask("1", method="POST", path="/mock/sent")[0])


def recall(mode, rid, body=b'{"confirm": true}'):
    with mock.patch.dict(os.environ, {"MOCK_SENT": mode}):
        h = Fake()
        return m.route(h, "POST", "/api/v1/connectors/sent/%d/recall" % rid, {}, body), h


class Recall(unittest.TestCase):
    def tearDown(self):
        m._recalled.clear()

    def test_the_first_page_offers_a_recall_and_shows_a_recalled_row(self):
        first = {x["id"]: x for x in m.page(m.rows())["items"]}
        self.assertEqual(sorted(i for i, x in first.items() if x["can_recall"]), [60, 66])
        self.assertTrue(first[54]["recalled_at"])
        self.assertFalse(first[54]["can_recall"])

    def test_a_recall_takes_the_row_back_until_the_mode_switches(self):
        handled, h = recall("1", 66)
        self.assertTrue(handled)
        self.assertEqual((h.status, h.json["item"]["id"], h.json["item"]["can_recall"]), (200, 66, False))
        self.assertTrue(h.json["item"]["recalled_at"])
        _, h = recall("1", 66)
        self.assertEqual((h.status, h.json["code"]), (409, "already_recalled"))
        ask("1", {"mode": ["1"]}, path="/mock/sent")
        m._now["mode"] = None
        self.assertTrue(next(x for x in m.rows() if x["id"] == 66)["can_recall"])

    def test_refusals(self):
        for mode, rid, body, want in (("1", 60, None, (502, "recall_refused")), ("1", 54, None, (409, "already_recalled")),
                                      ("1", 21, None, (409, "not_recallable")), ("1", 99, None, (404, "not_found")),
                                      ("1", 66, b"{}", (400, "confirm_required")), ("forbidden", 66, None, (403, "own_device_only")),
                                      ("nodb", 66, None, (503, "no_database"))):
            _, h = recall(mode, rid, *([body] if body is not None else []))
            self.assertEqual((h.status, h.json["code"]), want, (mode, rid))
        _, h = recall("old", 66)
        self.assertEqual((h.status, h.json), (404, None))


if __name__ == "__main__":
    unittest.main()
