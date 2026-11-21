"""Per-computer model overview for Settings > Models."""

import json
import os
from pathlib import Path


def register(domain):
    domain.help("MOCK_MODEL_OVERVIEW=1  per-computer model overview with a configured launcher, fallback and missing inventory (#4664)")
    if os.environ.get("MOCK_MODEL_OVERVIEW") != "1" or os.environ.get("MOCK_LEGACY_HUB"):
        return
    fixture = Path(__file__).resolve().parents[2] / "spec" / "fixtures" / "model-overview" / "overview.json"
    overview = json.loads(fixture.read_text(encoding="utf-8"))
    domain.route("GET", "/api/v1/model-config/overview", lambda hub, req: hub.send_json(overview))
