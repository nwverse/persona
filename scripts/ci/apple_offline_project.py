#!/usr/bin/env python3
"""Materialize an offline-only graph from a fully resolved XcodeGen JSON spec.

The ordinary project remains unchanged. This offline project keeps the real
Persona host and every PersonaTests source/selector, but excludes UI-test and
unreferenced Watch/Share/Vision/About targets instead of compiling them again.
"""

import argparse
from copy import deepcopy
import json
from pathlib import Path


SCHEME = "PersonaOfflineUnitTests"
EXCLUDED = {
    "PersonaWatch", "PersonaWatchWidgets", "PersonaShare",
    "PersonaVision", "PersonaVisionWidgets", "PersonaVisionNotify", "PersonaVisionShare",
    "PersonaUITests", "AboutVersionAccessibilityHost", "AboutVersionAccessibilityUITests",
}


def filtered(project):
    """Keep the canonical host, packages and test target; drop only offline-unused targets."""
    if set(project["schemes"]) != {
        "Persona", SCHEME, "AboutVersionVisualTests", "AboutVersionAccessibilityHost", "PersonaWatch", "PersonaVision",
    }:
        raise ValueError("the canonical project schemes changed; regenerate and review the offline graph")
    if set(project["targets"]) - set(project["schemes"]) != {
        "PersonaTests", "PersonaWidgets", "PersonaNotify", "PersonaWatchWidgets",
        "PersonaVisionWidgets", "PersonaVisionNotify", "PersonaVisionShare", "PersonaShare", "PersonaUITests",
        "AboutVersionAccessibilityUITests",
    }:
        raise ValueError("the canonical project targets changed; regenerate and review the offline graph")
    result = deepcopy(project)
    result["name"] = SCHEME
    result.pop("include", None)
    result["schemes"] = {SCHEME: deepcopy(project["schemes"][SCHEME])}
    # A top-level include merges identical test-action arrays, so XcodeGen's
    # resolved JSON repeats this one test entry. Deduplicate only that exact
    # scheme bookkeeping; command-line suite selectors remain untouched.
    action = result["schemes"][SCHEME]
    for key in ("build", "test"):
        if key not in action:
            continue
        if key == "build" and action[key]["targets"].get("PersonaTests") == ["test", "test"]:
            action[key]["targets"]["PersonaTests"] = ["test"]
        if key == "test" and action[key]["targets"] == ["PersonaTests", "PersonaTests"]:
            action[key]["targets"] = ["PersonaTests"]
    result["targets"] = {name: deepcopy(target) for name, target in project["targets"].items()
                         if name not in EXCLUDED}
    if set(result["targets"]) != {"Persona", "PersonaTests", "PersonaWidgets", "PersonaNotify"}:
        raise ValueError("offline graph must retain only the real iPhone host, its extensions and unit tests")
    host = result["targets"]["Persona"]
    host["dependencies"] = [dependency for dependency in host["dependencies"]
                            if dependency.get("target") not in EXCLUDED]
    if any(dependency.get("target") not in result["targets"] for dependency in host["dependencies"]
           if "target" in dependency):
        raise ValueError("offline host has an unresolved target dependency")
    tests = result["targets"]["PersonaTests"]
    if not any(dependency == {"target": "Persona"} for dependency in tests["dependencies"]):
        raise ValueError("offline tests must retain the real Persona host dependency")
    settings = tests["settings"]["base"]
    if (settings["TEST_HOST"] != "$(BUILT_PRODUCTS_DIR)/Persona.app/Persona"
            or settings["BUNDLE_LOADER"] != "$(TEST_HOST)"):
        raise ValueError("offline tests must retain the real Persona host and bundle loader")
    scheme = result["schemes"][SCHEME]
    if scheme["build"]["targets"] != {"Persona": "all", "PersonaTests": ["test"]}:
        raise ValueError("offline build must retain its canonical host and unit-test entries")
    if scheme["test"]["targets"] != ["PersonaTests"]:
        raise ValueError("offline test selection must remain PersonaTests")
    if "PersonaUITests" in json.dumps(result):
        raise ValueError("offline graph must not contain the UI-test target")
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("dump", type=Path, help="xcodegen dump --type json of the canonical whole project")
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    result = filtered(json.loads(args.dump.read_text()))
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + "\n")


if __name__ == "__main__":
    main()
