"""schema/wire (#9117, #7891 stage 5): the generated hub wire types match their schemas, and every sample the shared
fixtures hold for them is exactly what the schema describes.

    python3 scripts/test/test_wire_schema.py

The generator's --check covers all generated files (schema/extension and schema/wire), so nobody can edit one by hand.
A sample must name no field the schema lacks and miss no required one: a generated client type would otherwise drop
it or fail on it, changing the bytes it writes back.
"""

import importlib.util
import json
import re
import subprocess
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
WIRE = ROOT / "schema" / "wire"
FIXTURES = ROOT / "spec" / "fixtures"
GENERATOR = ROOT / "scripts" / "gen" / "extension_schema.py"

spec = importlib.util.spec_from_file_location("test_extension_schema", ROOT / "scripts/test/test_extension_schema.py")
ext = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ext)


def cases(name):
    return json.loads((FIXTURES / name).read_text())["cases"]


def recent_setups():
    """(definition, sample) of every setup in recents/recent_setups.json."""
    for c in cases("recents/recent_setups.json"):
        i, e = c.get("input", {}), c.get("expected", {})
        for key in ("before", "entries"):
            for s in i.get(key, []):
                yield "RecentSetup", f"{c['name']}.input.{key}", s
        if "setup" in i:
            yield "RecentSetup", f"{c['name']}.input.setup", i["setup"]
        for s in e.get("after", []):
            yield "RecentSetup", f"{c['name']}.expected.after", s


SAMPLES = {"recents.schema.json": recent_setups}


class WireSchema(unittest.TestCase):
    def test_generated_files_are_current(self):
        r = subprocess.run([sys.executable, str(GENERATOR), "--check"], capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, r.stderr)

    def test_every_schema_has_samples(self):
        self.assertEqual({p.name for p in WIRE.glob("*.schema.json")}, set(SAMPLES))

    def test_samples_are_exactly_the_schema(self):
        for fname, source in SAMPLES.items():
            doc = json.loads((WIRE / fname).read_text())
            v = ext.Validator({fname: doc})
            count = 0
            for definition, where, sample in source():
                count += 1
                schema = doc["$defs"][definition]
                errs = v.errors(schema, sample, fname)
                unknown = sorted(set(sample) - set(schema["properties"]))
                self.assertEqual(errs, [], f"{fname} {where}")
                self.assertEqual(unknown, [], f"{fname} {where}: fields the schema lacks")
            self.assertGreater(count, 0, fname)

    def test_no_hand_written_copy_returns(self):
        # The names a schema defines are declared only by the generated files (the old hand-written types stay gone).
        generated, names = set(), []
        for p in WIRE.glob("*.schema.json"):
            doc = json.loads(p.read_text())
            generated |= {ROOT / f for f in doc["x-out"].values()}
            names += list(doc["$defs"])
        tracked = subprocess.run(["git", "ls-files", "apple", "web/src"], cwd=ROOT, capture_output=True, text=True,
                                 check=True).stdout.split()
        pattern = re.compile(r"^\s*(?:export\s+)?(?:public\s+)?(?:nonisolated\s+)?(?:struct|class|enum|interface|type)\s+("
                             + "|".join(map(re.escape, names)) + r")\b", re.M)
        for rel in tracked:
            path = ROOT / rel
            if path in generated or path.suffix not in (".swift", ".ts", ".tsx"):
                continue
            for m in pattern.finditer(path.read_text(errors="ignore")):
                self.fail(f"{rel} declares {m.group(1)}, which schema/wire generates")

    def test_empty_strings_are_samples(self):
        # "" is a value, not an absent field: a generated type must write it back (Go omitempty would not).
        setups = [s for _, _, s in recent_setups()]
        self.assertTrue(any(s["launcher"] == "" for s in setups))
        self.assertTrue(any(s["mode"] == "" for s in setups))


if __name__ == "__main__":
    unittest.main()
