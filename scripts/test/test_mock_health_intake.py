"""The screenshot hub's intake routes mirror the client contract, including idempotent writes and tombstones."""
import json
from pathlib import Path
import sys
import unittest
import uuid
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "mock"))
import mock_hub as hub
import health_intake_mock as intake


class Handler:
    def __init__(self, headers=None):
        self.headers = headers or {}
        self.result = None
        self.status = None

    def send_json(self, result, status=200):
        self.result, self.status = result, status


class HealthIntakeMockTests(unittest.TestCase):
    path = "/api/v1/assistant/health/intake"

    def setUp(self):
        intake.HEALTH_INTAKE_ENABLED = True
        intake.health_intake_reset()
        self.events = []
        self.broadcast = patch.object(hub, "broadcast", self.events.append)
        self.broadcast.start()
        self.addCleanup(self.broadcast.stop)

    def request(self, method="GET", suffix="", body=None, query=None, headers=None):
        handler = Handler(headers)
        self.assertTrue(intake.health_intake_route(handler, method, self.path + suffix,
                                              {"space": ["health"]} if query is None else query,
                                              json.dumps(body).encode() if body is not None else b""))
        return handler

    def test_snapshot_has_goals_records_and_zero_filled_days(self):
        response = self.request(query={"space": ["health"], "days": ["30"]})
        self.assertEqual(response.status, 200)
        self.assertEqual(response.result["water"]["today"], 750)
        self.assertEqual(response.result["protein"]["today"], 36)
        self.assertEqual(response.result["water"]["goal"], 2000)
        self.assertEqual(len(response.result["days"]), 30)
        self.assertEqual(response.result["days"][0]["water"], 0)
        self.assertEqual(response.result["days"][-1]["water"], 750)

    def test_changes_pages_use_exclusive_sequences_and_keep_tombstones(self):
        first = self.request(suffix="/changes", query={"space": ["health"], "limit": ["2"]}).result
        self.assertEqual([e["seq"] for e in first["changes"]], [1, 2])
        self.assertEqual(first["next"], 2)
        self.assertTrue(first["has_more"])
        identity = first["changes"][0]["id"]
        self.assertEqual(self.request("DELETE", "/" + identity).status, 200)
        tail = self.request(suffix="/changes", query={"space": ["health"], "after": ["2"]}).result
        self.assertEqual([e["seq"] for e in tail["changes"]], [3, 4])
        self.assertIn("deleted_at", tail["changes"][-1])
        empty = self.request(suffix="/changes", query={"space": ["health"], "after": ["4"]}).result
        self.assertEqual(empty, {"changes": [], "next": 4, "has_more": False})

    def test_detail_pages_use_exported_gauges_settings_and_action_toasts(self):
        for kind, color in [("water", "blue"), ("protein", "amber")]:
            body = {"context": {"locale": "en", "ui_version": 11}, "state": json.dumps({"kind": kind})}
            initial = self.request("POST", "/ui", body).result
            self.assertEqual(initial["view"]["title"], kind.title())
            self.assertEqual(initial["view"]["sections"][0]["items"][0]["tiles"][0]["gauge"]["color"], color)
            body["state"] = initial["view"]["state"]
            body["action"] = "drink" if kind == "water" else "log"
            acted = self.request("POST", "/ui", body).result
            self.assertEqual(acted["view"], initial["view"])
            self.assertTrue(acted["toast"])
            body["action"] = "settings"
            settings = self.request("POST", "/ui", body).result["view"]
            self.assertTrue(json.loads(settings["state"])["edit"])
            self.assertEqual(settings["sections"][0]["items"][0]["name"], "goal_ml" if kind == "water" else "goal_g")
        legacy = self.request("POST", "/ui", {"context": {"locale": "zh-Hans", "ui_version": 4}}).result
        self.assertEqual(legacy["view"]["title"], "喝水")
        self.assertEqual(legacy["view"]["sections"][0]["items"][0]["type"], "row")

    def test_default_cup_retry_and_tombstone_are_idempotent(self):
        draft = {"id": str(uuid.uuid4()), "kind": "water"}
        first = self.request("POST", body=draft).result
        self.assertEqual(first["entry"]["amount"], 250)
        self.assertEqual(first["today"]["water"], 1000)
        retry = self.request("POST", body=dict(draft, amount=500)).result
        self.assertEqual(first, retry)
        deleted = self.request("DELETE", "/" + draft["id"]).result
        self.assertEqual(deleted["today"]["water"], 750)
        sequence = intake.HEALTH_INTAKE_ENTRIES[draft["id"]]["seq"]
        self.assertEqual(self.request("DELETE", "/" + draft["id"]).result, deleted)
        self.assertEqual(intake.HEALTH_INTAKE_ENTRIES[draft["id"]]["seq"], sequence)
        tombstone = self.request("POST", body=draft).result
        self.assertIn("deleted_at", tombstone["entry"])
        self.assertEqual(tombstone["today"]["water"], 750)
        self.assertTrue(self.events)
        self.assertTrue(all(event == {"type": "health_intake"} for event in self.events))

    def test_protein_and_water_accept_one_decimal(self):
        for kind, amount, total in [("protein", 12.3, 48.3), ("water", 125.5, 875.5)]:
            result = self.request("POST", body={"id": str(uuid.uuid4()), "kind": kind, "amount": amount})
            self.assertEqual(result.status, 200)
            self.assertEqual(result.result["today"][kind], total)

    def test_wrong_space_manager_and_old_hub_are_refused(self):
        self.assertEqual(self.request(query={}).status, 400)
        self.assertEqual(self.request(headers={"X-Persona-Manager": "another-manager"}).status, 400)
        intake.HEALTH_INTAKE_ENABLED = False
        self.assertNotIn("health_intake", hub.features())
        self.assertEqual(self.request().status, 403)

    def test_invalid_values_and_missing_delete_have_explicit_errors(self):
        for kind, amount in [("water", 0), ("water", 3000.1), ("water", True), ("protein", 200.1), ("protein", 0.01)]:
            result = self.request("POST", body={"id": str(uuid.uuid4()), "kind": kind, "amount": amount})
            self.assertEqual(result.status, 400)
            self.assertTrue(result.result["error"])
        self.assertEqual(self.request("DELETE", "/" + str(uuid.uuid4())).status, 404)


    def test_import_keeps_seed_records_and_publishes_changes_once(self):
        before = self.request().result
        self.assertEqual(before["water_extension"], "pending")
        imported = self.request("POST", "/import")
        self.assertEqual(imported.status, 200)
        self.assertEqual(imported.result["imported"], 2)
        self.assertTrue(imported.result["extension_disabled"])
        self.assertEqual(imported.result["settings"]["water"]["interval_minutes"], 90)
        self.assertTrue(imported.result["settings"]["water"]["remind"])
        after = self.request().result
        self.assertNotIn("water_extension", after)
        self.assertEqual(after["water"]["today"], 1200)
        self.assertEqual(after["water"]["goal"], 2400)
        self.assertEqual(after["protein"], before["protein"])
        self.assertEqual(len(after["days"]), 7)
        changes = self.request(suffix="/changes", query={"space": ["health"], "after": ["3"], "limit": ["1"]}).result
        self.assertEqual([e["amount"] for e in changes["changes"]], [200])
        self.assertEqual(changes["next"], 4)
        self.assertTrue(changes["has_more"])
        tail = self.request(suffix="/changes", query={"space": ["health"], "after": ["4"]}).result
        self.assertEqual([e["amount"] for e in tail["changes"]], [250])
        self.assertEqual(tail["next"], 5)
        self.assertFalse(tail["has_more"])
        retry = self.request("POST", "/import").result
        self.assertEqual(retry["imported"], 0)
        self.assertEqual(retry["settings"], imported.result["settings"])
        self.assertEqual(self.request().result, after)
        self.assertEqual(self.events, [{"type": "health_intake"}])
        identity = changes["changes"][0]["id"]
        self.assertEqual(self.request("DELETE", "/" + identity).status, 200)
        self.assertEqual(self.request("POST", "/import").result["imported"], 0)
        self.assertEqual(self.request().result["water"]["today"], 1000)
        tombstones = self.request(suffix="/changes", query={"space": ["health"], "after": ["5"]}).result
        self.assertEqual(tombstones["changes"][0]["id"], identity)
        self.assertIn("deleted_at", tombstones["changes"][0])

    def test_none_and_migrated_start_without_a_pending_card(self):
        for mode, status in (("none", 404), ("migrated", 200)):
            with self.subTest(mode=mode), patch.object(intake, "HEALTH_WATER_EXTENSION_MODE", mode):
                intake.health_intake_reset()
                self.assertNotIn("water_extension", self.request().result)
                imported = self.request("POST", "/import")
                self.assertEqual(imported.status, status)
                if mode == "migrated":
                    self.assertEqual(imported.result["imported"], 0)
                    self.assertTrue(imported.result["extension_disabled"])
                self.assertEqual(self.request().result["water"]["today"], 750)

    def test_reset_restores_pending_settings_and_import_records(self):
        import mock_domains
        registry = mock_domains.Registry(vars(hub))
        with patch.dict("os.environ", {"MOCK_WATER_EXTENSION": "pending"}):
            intake.register(mock_domains.Domain(registry, "health_intake_mock"))
        for reset in (lambda: intake.health_intake_route(Handler(), "POST", "/mock/health-intake", {"reset": ["1"]}),
                      registry.reset):
            with self.subTest(reset=reset):
                self.assertEqual(self.request("POST", "/import").result["imported"], 2)
                reset()
                restored = self.request().result
                self.assertEqual(restored["water_extension"], "pending")
                self.assertEqual(restored["water"]["today"], 750)
                self.assertEqual(restored["water"]["goal"], 2000)
                self.assertEqual(intake.HEALTH_INTAKE_SETTINGS["water"]["interval_minutes"], 60)
                changes = self.request(suffix="/changes").result
                self.assertEqual([e["seq"] for e in changes["changes"]], [1, 2, 3])

    def test_import_keeps_space_manager_and_feature_guards(self):
        self.assertEqual(self.request("POST", "/import", query={}).status, 400)
        self.assertEqual(self.request("POST", "/import", headers={"X-Persona-Manager": "another-manager"}).status, 400)
        intake.HEALTH_INTAKE_ENABLED = False
        self.assertEqual(self.request("POST", "/import").status, 403)
        self.assertEqual(len(intake.HEALTH_INTAKE_ENTRIES), 3)
        self.assertEqual(self.events, [])


class HealthItemsMockTests(unittest.TestCase):
    """What Health tracks (#14716): the whole list at a revision, 409 with the current list, one message per save."""
    path = "/api/v1/assistant/health/items"

    def setUp(self):
        intake.HEALTH_INTAKE_ENABLED = True
        intake.HEALTH_ITEMS_MODE = "existing"
        intake.health_items_reset()
        self.addCleanup(intake.health_items_reset)
        self.events = []
        self.broadcast = patch.object(hub, "broadcast", self.events.append)
        self.broadcast.start()
        self.addCleanup(self.broadcast.stop)

    def request(self, method="GET", body=None, headers=None, path=None, query=None):
        handler = Handler(headers)
        self.assertTrue(intake.health_items_route(handler, method, path or self.path, query or {"space": ["health"]},
                                                  json.dumps(body).encode() if body is not None else b""))
        return handler

    def test_defaults_follow_the_owner(self):
        got = self.request().result
        self.assertEqual([c["id"] for c in got["catalog"]], ["water", "protein", "activity", "trends"])
        self.assertEqual((got["items"], got["revision"], got["saved"]), (["water", "protein", "activity", "trends"], 0, False))
        self.request(path="/mock/health-items", query={"mode": ["fresh"]})
        self.assertEqual(self.request().result["items"], ["activity", "trends"])

    def test_save_bumps_the_revision_once_and_says_so(self):
        saved = self.request("PUT", {"items": ["protein", "activity", "water", "trends"], "revision": 0})
        self.assertEqual((saved.status, saved.result["revision"], saved.result["saved"]), (200, 1, True))
        self.assertEqual(self.events, [{"type": "health_items", "revision": 1}])
        self.assertEqual(self.request().result["items"], ["protein", "activity", "water", "trends"])

    def test_a_stale_revision_gets_the_current_list(self):
        self.request("PUT", {"items": [], "revision": 0})
        stale = self.request("PUT", {"items": ["water"], "revision": 0})
        self.assertEqual((stale.status, stale.result["items"], stale.result["revision"]), (409, [], 1))
        self.assertEqual(len(self.events), 1)

    def test_invalid_lists_and_other_managers_are_refused(self):
        for body in ({"items": ["water", "water"], "revision": 0}, {"items": ["steps"], "revision": 0},
                     {"items": ["water"]}, {"items": None, "revision": 0}, {"items": [], "revision": -1},
                     {"items": [], "revision": 0, "extra": 1}):
            with self.subTest(body=body):
                self.assertEqual(self.request("PUT", body).status, 400)
        self.assertEqual(self.request(headers={"X-Persona-Manager": "work"}).status, 400)
        self.assertEqual(self.events, [])

    def test_off_has_no_route_and_intake_has_no_items(self):
        self.request(path="/mock/health-items", query={"mode": ["off"]})
        self.assertEqual(self.request().status, 404)
        view = Handler()
        intake.health_intake_route(view, "GET", "/api/v1/assistant/health/intake", {"space": ["health"]})
        self.assertNotIn("items", view.result)
        self.request(path="/mock/health-items", query={"mode": ["existing"]})
        intake.health_intake_route(view, "GET", "/api/v1/assistant/health/intake", {"space": ["health"]})
        self.assertEqual(view.result["items"], ["water", "protein", "activity", "trends"])


class HealthIntakeMigrationHTTPTests(unittest.TestCase):
    def test_registered_domain_import_and_reset(self):
        from test_readiness_mock import Hub
        h = Hub(MOCK_HEALTH_INTAKE="1")
        self.addCleanup(h.stop)
        path = "/api/v1/assistant/health/intake?space=health"
        code, before = h.call("GET", path)
        self.assertEqual(code, 200)
        self.assertEqual(before["water_extension"], "pending")
        self.assertEqual(h.call("POST", "/api/v1/assistant/health/intake/import")[0], 400)
        code, imported = h.call("POST", "/api/v1/assistant/health/intake/import?space=health")
        self.assertEqual((code, imported["imported"]), (200, 2))
        code, after = h.call("GET", path)
        self.assertNotIn("water_extension", after)
        self.assertEqual(after["water"]["today"], before["water"]["today"] + 450)
        self.assertEqual(h.call("GET", "/mock/reset")[0], 200)
        self.assertEqual(h.call("GET", path)[1]["water_extension"], "pending")

    def test_registered_domain_health_items(self):
        from test_readiness_mock import Hub
        h = Hub(MOCK_HEALTH_ITEMS="fresh")
        self.addCleanup(h.stop)
        path = "/api/v1/assistant/health/items?space=health"
        code, before = h.call("GET", path)
        self.assertEqual((code, before["items"], before["saved"]), (200, ["activity", "trends"], False))
        self.assertIn("health_items", h.call("GET", "/api/v1/health")[1]["features"])
        code, saved = h.call("PUT", path, {"items": ["water", "activity", "trends"], "revision": 0})
        self.assertEqual((code, saved["revision"]), (200, 1))
        self.assertEqual(h.call("PUT", path, {"items": [], "revision": 0})[0], 409)
        self.assertEqual(h.call("GET", "/mock/reset")[0], 200)
        self.assertEqual(h.call("GET", path)[1]["revision"], 0)

    def test_registered_domain_initial_modes(self):
        from test_readiness_mock import Hub
        for mode, expected in (("none", 404), ("migrated", 200)):
            with self.subTest(mode=mode):
                h = Hub(MOCK_WATER_EXTENSION=mode)
                try:
                    code, view = h.call("GET", "/api/v1/assistant/health/intake?space=health")
                    self.assertEqual(code, 200)
                    self.assertNotIn("water_extension", view)
                    code, result = h.call("POST", "/api/v1/assistant/health/intake/import?space=health")
                    self.assertEqual(code, expected)
                    if code == 200:
                        self.assertEqual(result["imported"], 0)
                finally:
                    h.stop()


if __name__ == "__main__":
    unittest.main()
