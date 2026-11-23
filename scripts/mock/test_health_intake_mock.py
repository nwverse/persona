"""The migration card's mock uses only synthetic data and clears pending after import."""
import unittest

from test_readiness_mock import Hub


class HealthIntakeMigrationTests(unittest.TestCase):
    def test_import_clears_pending_and_adds_once(self):
        h = Hub(MOCK_HEALTH_INTAKE="1")
        self.addCleanup(h.stop)
        code, before = h.call("GET", "/api/v1/assistant/health/intake?space=health")
        self.assertEqual(code, 200)
        self.assertEqual(before["water_extension"], "pending")
        self.assertEqual(before["water"]["today"], 0)
        self.assertEqual(len(before["days"]), 7)
        code, imported = h.call("POST", "/api/v1/assistant/health/intake/import?space=health")
        self.assertEqual(code, 200)
        self.assertEqual(imported["imported"], 2)
        self.assertTrue(imported["extension_disabled"])
        self.assertEqual(imported["settings"]["water"]["interval_minutes"], 90)
        code, after = h.call("GET", "/api/v1/assistant/health/intake")
        self.assertEqual(code, 200)
        self.assertNotIn("water_extension", after)
        self.assertEqual(after["water"]["today"], 450)
        code, retry = h.call("POST", "/api/v1/assistant/health/intake/import")
        self.assertEqual(code, 200)
        self.assertEqual(retry["imported"], 0)
        self.assertEqual(retry["settings"], imported["settings"])

    def test_no_pending_card(self):
        h = Hub(MOCK_HEALTH_INTAKE="1", MOCK_WATER_EXTENSION="none")
        self.addCleanup(h.stop)
        code, result = h.call("GET", "/api/v1/assistant/health/intake")
        self.assertEqual(code, 200)
        self.assertNotIn("water_extension", result)
        self.assertEqual(h.call("POST", "/api/v1/assistant/health/intake/import")[0], 404)


if __name__ == "__main__":
    unittest.main()
