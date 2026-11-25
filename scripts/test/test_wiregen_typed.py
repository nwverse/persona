"""The wire generator's opt-in typed keywords (#15397): x-enum "closed" with x-unknown "count", x-id, enum-keyed maps
made structs, x-decode "strict", x-opaque and the Rust target, in every language, from
scripts/test/fixtures/wiregen/typed.schema.json. Where a toolchain is on this machine the generated code is also
compiled and run (Go: go vet and go test; Rust: cargo test --offline; TypeScript: tsc --noEmit; Swift: swiftc -parse;
Kotlin is never compiled here, it needs the Android build); a missing one is reported as a skip, not a pass.

No existing schema uses these keywords yet, so scripts/gen/extension_schema.py --check (test_extension_schema.py,
test_wire_schema.py) is what proves their outputs did not change."""

import importlib.util
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("wiregen_typed_under_test", ROOT / "scripts/gen/extension_schema.py")
GEN = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = GEN
SPEC.loader.exec_module(GEN)

FIXTURES = ROOT / "scripts/test/fixtures/wiregen"
FIXTURE = "typed.schema.json"
DEFECTS = "TypedWire"


def fixture_defs():
    return GEN.load(FIXTURES, [FIXTURE])


def generate(language, defs=None):
    defs = fixture_defs() if defs is None else defs
    header = ["// Generated for scripts/test/test_wiregen_typed.py."]
    if language == "go":
        return GEN.gofmt(GEN.gen_go(defs, header=header, package="typed", package_doc="", registry="typedDefs",
                                    defects=DEFECTS))
    if language == "ts":
        return GEN.gen_ts(defs, header=header, defects=DEFECTS)
    if language == "arkts":
        return GEN.gen_arkts(defs, header=header, defects=DEFECTS)
    if language == "swift":
        return GEN.gen_swift(defs, header=header, prefix="", defects=DEFECTS)
    if language == "kotlin":
        return GEN.gen_kotlin(defs, header=header, prefix="", package="persona.test", defects=DEFECTS)
    if language == "rust":
        return GEN.gen_rust(defs, header=header, defects=DEFECTS)
    raise ValueError(language)


def definitions(schemas):
    with tempfile.TemporaryDirectory() as tmp:
        path = Path(tmp) / "example.schema.json"
        path.write_text(json.dumps({"$defs": schemas}))
        return GEN.load(Path(tmp), [path.name])


def run(cmd, cwd, env=None):
    r = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, env=env)
    return r.returncode, r.stdout + r.stderr


class Model(unittest.TestCase):
    def test_the_fixture_loads_each_keyword(self):
        defs = fixture_defs()
        self.assertEqual(defs["SessionID"].kind, "id")
        self.assertEqual(defs["ToolInput"].kind, "opaque")
        self.assertIn("tool's own arguments", defs["ToolInput"].reason)
        self.assertTrue(defs["Phase"].closed and defs["Phase"].count_unknown)
        self.assertTrue(defs["Section"].closed and not defs["Section"].count_unknown)
        self.assertFalse(defs["Legacy"].closed)
        self.assertTrue(defs["View"].strict)
        self.assertFalse(defs["Lenient"].strict)
        fields = {f.name: f.type for f in defs["View"].fields}
        self.assertEqual(fields["counts"].ref, "ViewCounts")
        self.assertEqual([f.name for f in defs["ViewCounts"].fields], ["now", "later"])
        self.assertTrue(all(f.required for f in defs["ViewCounts"].fields))
        self.assertEqual(fields["loose"].kind, "map")  # not every key required
        self.assertEqual(fields["related"].of.ref, "SessionID")
        self.assertEqual({f.name: f.type.ref for f in defs["Ref"].fields}, {"machine": "MachineID", "session": "SessionID"})

    def test_an_open_enum_keyed_map_stays_a_map(self):
        defs = definitions({
            "Section": {"type": "string", "enum": ["a", "b"]},
            "Counts": {"type": "object", "properties": {"by": {
                "type": "object", "propertyNames": {"$ref": "#/$defs/Section"},
                "additionalProperties": {"type": "integer"}, "required": ["a", "b"]}}},
        })
        self.assertEqual(defs["Counts"].fields[0].type.kind, "map")
        self.assertNotIn("CountsBy", defs)

    def test_a_file_wide_strict_decode_reaches_every_object(self):
        with tempfile.TemporaryDirectory() as tmp:
            (Path(tmp) / "s.schema.json").write_text(json.dumps({"x-decode": "strict", "$defs": {
                "A": {"type": "object", "properties": {"n": {"type": "integer"}}, "required": ["n"]},
                "B": {"type": "object", "properties": {"n": {"type": "integer"}}, "x-decode": "strict"},
                "E": {"type": "string", "enum": ["x"]}}}))
            defs = GEN.load(Path(tmp), ["s.schema.json"])
        self.assertTrue(defs["A"].strict and defs["B"].strict)

    def test_misuse_is_refused_not_guessed(self):
        cases = {
            "repeats its name": {"X": {"type": "string", "x-id": "Y"}},
            "x-id goes on a string": {"X": {"type": "integer", "x-id": "X"}},
            "takes the reason": {"X": {"x-opaque": " "}},
            "free JSON": {"X": {"type": "object", "properties": {"a": {"type": "string"}}, "x-opaque": "why"}},
            "on a definition": {"X": {"type": "object", "properties": {"a": {"x-opaque": "why"}}}},
            "not a string definition with that x-id": {
                "X": {"type": "object", "properties": {"a": {"type": "string", "x-id": "Missing"}}}},
            "go on a string enum": {"X": {"type": "string", "x-unknown": "count"}},
            "x-enum 'open'": {"X": {"type": "string", "enum": ["a"], "x-enum": "open"}},
            "x-unknown 'drop'": {"X": {"type": "string", "enum": ["a"], "x-enum": "closed", "x-unknown": "drop"}},
            "make a name": {"X": {"type": "string", "enum": ["1st"], "x-enum": "closed"}},
            "same name": {"X": {"type": "string", "enum": ["a_b", "a-b"], "x-enum": "closed"}},
            "on an object": {"X": {"type": "string", "enum": ["a"], "x-decode": "strict"}},
            "additionalProperties is not": {"X": {"type": "object", "additionalProperties": True, "x-decode": "strict"}},
        }
        for message, schemas in cases.items():
            with self.subTest(message), self.assertRaisesRegex(GEN.SchemaError, message):
                definitions(schemas)


class Languages(unittest.TestCase):
    """What each keyword turns into, language by language (2.1, 2.3, 2.4, 2.5 of the typed core plan)."""

    def test_go(self):
        go = generate("go")
        # x-enum closed + x-unknown count
        self.assertIn("type Phase string", go)
        self.assertRegex(go, r'PhaseNeedsYou\s+Phase = "needs_you"')
        self.assertIn("var Phases = []Phase{PhaseNeedsYou,", go)
        self.assertIn("func (v Phase) Valid() bool {", go)
        self.assertIn("func (v *Phase) UnmarshalJSON(data []byte) error {", go)
        self.assertIn('TypedWireUnknownEnum("Phase", raw)', go)
        self.assertIn("var TypedWireUnknownEnum func(enum, raw string)", go)
        self.assertNotIn("func (v *Section) UnmarshalJSON", go)  # closed, not counted
        self.assertIn("var Legacies = []string{", go)  # the open enum as before
        # x-id
        self.assertIn("type SessionID string", go)
        self.assertRegex(go, r'Session SessionID `json:"session"`')
        self.assertRegex(go, r'Related\s+\[\]SessionID\s+`json:"related,omitempty"`')
        # enum-keyed map → struct, and the map that stays one
        self.assertRegex(go, r'Counts ViewCounts `json:"counts"`')
        self.assertRegex(go, r'Now\s+int `json:"now"`')
        self.assertRegex(go, r'Loose\s+map\[string\]int')
        # x-opaque
        self.assertIn("type ToolInput json.RawMessage", go)
        self.assertIn("func (v ToolInput) MarshalJSON() ([]byte, error)", go)
        self.assertIn("It stays opaque: a tool's own arguments", go)
        # a union's field of a closed enum keeps the type
        self.assertRegex(go, r'Phase\s+Phase\s+`json:"phase,omitempty"`')

    def test_typescript_and_arkts(self):
        for flavor in ("ts", "arkts"):
            with self.subTest(flavor):
                ts = generate(flavor)
                self.assertIn('export type Phase = "needs_you" | "working" | "done" | "error" | "idle" | "unknown";', ts)
                self.assertNotIn('"unknown" | (string & {})', ts)
                self.assertIn('export type Legacy = "a" | "b" | (string & {});', ts)  # open: as before
                self.assertIn("export function parsePhase(raw: string): Phase | UnknownPhase {", ts)
                self.assertIn('hook("Phase", raw);', ts)
                self.assertIn("export class TypedWireDefects {", ts)
                self.assertNotIn('hook("Section"', ts)
                self.assertIn("export function sessionID(raw: string): SessionID {", ts)
                self.assertIn("counts: ViewCounts;", ts)
                self.assertIn("loose?: Record<string, number>;", ts)
                self.assertIn("export type ToolInput = unknown;", ts)
                self.assertIn("related?: SessionID[];", ts)
        self.assertIn('export type SessionID = string & { readonly __brand: "SessionID" };', generate("ts"))
        self.assertIn("export type SessionID = string;", generate("arkts"))  # ArkTS has no intersection types

    def test_swift(self):
        swift = generate("swift")
        self.assertIn("public nonisolated enum Phase: Codable, Hashable, Sendable {", swift)
        self.assertIn("    case needsYou", swift)
        self.assertIn("    case unknownValue", swift)  # the value "unknown" beside case unknown(String)
        self.assertIn("    case unknown(String)", swift)
        self.assertIn('if case .unknown = self { TypedWireDefects.unknownEnum?("Phase", raw) }', swift)
        self.assertIn("public nonisolated(unsafe) static var unknownEnum:", swift)
        self.assertIn('    public static let all = ["a", "b"]', swift)  # the open enum as before
        self.assertIn("public nonisolated struct SessionID: RawRepresentable, Codable, Hashable, Sendable", swift)
        self.assertIn("public var counts: ViewCounts", swift)
        self.assertIn("public typealias ToolInput = JSONValue", swift)
        # x-decode strict throws; the lenient struct still defaults, a closed enum to .unknown("")
        self.assertIn("self.phase = try c.decode(Phase.self, forKey: .phase)", swift)
        self.assertIn("self.previous = try c.decodeIfPresent(Phase.self, forKey: .previous)", swift)
        self.assertIn('self.phase = (try? c.decodeIfPresent(Phase.self, forKey: .phase)) ?? .unknown("")', swift)
        self.assertIn('self.id = (try? c.decodeIfPresent(SessionID.self, forKey: .id)) ?? SessionID("")', swift)

    def test_kotlin(self):
        kotlin = generate("kotlin")
        self.assertIn("sealed interface Phase {", kotlin)
        self.assertIn('data object NeedsYou : Phase { override val raw: String = "needs_you" }', kotlin)
        self.assertIn('data object UnknownValue : Phase { override val raw: String = "unknown" }', kotlin)
        self.assertIn("data class Unknown(override val raw: String) : Phase", kotlin)
        self.assertIn("object PhaseSerializer : KSerializer<Phase> {", kotlin)
        self.assertIn('TypedWireDefects.unknownEnum?.invoke("Phase", raw)', kotlin)
        self.assertIn("import kotlinx.serialization.descriptors.PrimitiveKind", kotlin)
        self.assertIn("@JvmInline\nvalue class SessionID(val raw: String) {", kotlin)
        self.assertIn("typealias ToolInput = JsonElement", kotlin)
        self.assertIn("val counts: ViewCounts,", kotlin)  # strict: no default
        self.assertIn("val phase: Phase,", kotlin)
        self.assertIn('val phase: Phase = Phase.Unknown(""),', kotlin)  # lenient
        self.assertIn('val id: SessionID = SessionID(""),', kotlin)
        self.assertIn('    const val A = "a"', kotlin)  # the open enum as before

    def test_rust(self):
        rust = generate("rust")
        self.assertIn("use serde::{Deserialize, Serialize};", rust)
        self.assertIn("pub enum Phase {", rust)
        self.assertIn("    UnknownValue,", rust)
        self.assertIn("    Unknown(String),", rust)
        self.assertIn("pub static TYPED_WIRE_UNKNOWN_ENUM: std::sync::OnceLock<fn(&str, &str)>", rust)
        self.assertIn("#[serde(transparent)]\npub struct SessionID(pub String);", rust)
        self.assertIn("pub type ToolInput = serde_json::Value;", rust)
        self.assertIn("pub counts: ViewCounts,", rust)
        self.assertIn("pub loose: Option<std::collections::BTreeMap<String, i64>>,", rust)
        self.assertIn("pub type Legacy = String;", rust)
        self.assertIn("pub enum Event {", rust)
        self.assertIn("Unknown { r#type: String, raw: serde_json::Value },", rust)
        # x-decode strict: View's required fields have no serde default; Lenient's do
        view = rust[rust.index("pub struct View {"):rust.index("pub struct Lenient {")]
        lenient = rust[rust.index("pub struct Lenient {"):]
        self.assertNotIn("#[serde(default)]\n    pub phase", view)
        self.assertIn("#[serde(default)]\n    pub phase: Phase,", lenient)

    def test_a_file_of_ids_only_generates_no_registry(self):
        defs = definitions({"SessionID": {"type": "string", "x-id": "SessionID"},
                            "MachineID": {"type": "string", "x-id": "MachineID"}})
        go = GEN.gofmt(GEN.gen_go(defs, header=["// A test."], package="ids", package_doc="", registry="idsDefs"))
        self.assertIn("type SessionID string", go)
        self.assertNotIn("idsDefs", go)
        self.assertNotIn("import", go)

    def test_the_wire_command_routes_rust_and_names_the_hook_after_the_schema(self):
        with tempfile.TemporaryDirectory() as tmp:
            doc = json.loads((FIXTURES / FIXTURE).read_text())
            doc["x-out"] = {"rust": "out/typed.rs", "ts": "out/typed.ts"}
            (Path(tmp) / "agent-view.schema.json").write_text(json.dumps(doc))
            with patch.object(GEN, "WIRE_DIR", Path(tmp)):
                files = GEN.wire_outputs()
        rust, ts = files[GEN.ROOT / "out/typed.rs"], files[GEN.ROOT / "out/typed.ts"]
        self.assertIn("AGENT_VIEW_WIRE_UNKNOWN_ENUM", rust)
        self.assertIn("export class AgentViewWireDefects {", ts)
        self.assertTrue(rust.startswith("// Generated by scripts/gen/extension_schema.py from schema/wire/"))


GO_TEST = """package typed

import (
\t"encoding/json"
\t"testing"
)

func TestClosedEnumKeepsTheUnknownValueAndCountsIt(t *testing.T) {
\tvar got []string
\tTypedWireUnknownEnum = func(enum, raw string) { got = append(got, enum+"="+raw) }
\tdefer func() { TypedWireUnknownEnum = nil }()
\tin := `{"phase":"resting","id":"s1"}`
\tvar v Lenient
\tif err := json.Unmarshal([]byte(in), &v); err != nil {
\t\tt.Fatal(err)
\t}
\tif v.Phase.Valid() || v.Phase != "resting" || v.ID != SessionID("s1") || !PhaseDone.Valid() {
\t\tt.Fatalf("%+v", v)
\t}
\tif len(got) != 1 || got[0] != "Phase=resting" {
\t\tt.Fatalf("hook: %v", got)
\t}
\tout, _ := json.Marshal(v)
\tif string(out) != in {
\t\tt.Fatalf("%s", out)
\t}
}

func TestOpaqueIDsAndKeyedStructRoundTrip(t *testing.T) {
\tin := `{"ref":{"machine":"m","session":"s"},"phase":"done","counts":{"now":1,"later":2},"rank":3,"input":{"a":[1,2]},"related":["x"]}`
\tvar v View
\tif err := json.Unmarshal([]byte(in), &v); err != nil {
\t\tt.Fatal(err)
\t}
\tif v.Counts.Later != 2 || v.Phase != PhaseDone || v.Ref.Machine != "m" || string(v.Input) != `{"a":[1,2]}` {
\t\tt.Fatalf("%+v", v)
\t}
\tout, _ := json.Marshal(v)
\tif string(out) != in {
\t\tt.Fatalf("%s", out)
\t}
}
"""

RUST_TEST = r"""
#[cfg(test)]
mod tests {
    use super::typed::*;

    fn hook(enum_name: &str, raw: &str) {
        assert_eq!((enum_name, raw), ("Phase", "resting"));
        SEEN.store(true, std::sync::atomic::Ordering::SeqCst);
    }
    static SEEN: std::sync::atomic::AtomicBool = std::sync::atomic::AtomicBool::new(false);

    #[test]
    fn closed_enum_keeps_the_unknown_value_and_counts_it() {
        TYPED_WIRE_UNKNOWN_ENUM.set(hook).ok();
        let v: Lenient = serde_json::from_str(r#"{"phase":"resting","id":"s1"}"#).unwrap();
        assert_eq!(v.phase, Phase::Unknown("resting".into()));
        assert_eq!(v.id, SessionID::new("s1"));
        assert!(SEEN.load(std::sync::atomic::Ordering::SeqCst));
        assert_eq!(serde_json::to_string(&v).unwrap(), r#"{"phase":"resting","id":"s1"}"#);
        assert_eq!(Phase::parse("unknown"), Phase::UnknownValue);
        assert_eq!(Phase::ALL.len(), 6);
    }

    #[test]
    fn strict_objects_refuse_a_missing_field_and_lenient_ones_default() {
        assert!(serde_json::from_str::<View>(r#"{"phase":"done"}"#).is_err());
        let v: Lenient = serde_json::from_str("{}").unwrap();
        assert_eq!(v.phase, Phase::Unknown(String::new()));
    }

    #[test]
    fn unions_keep_what_they_do_not_know() {
        let gone: Event = serde_json::from_str(r#"{"type":"gone","session":"s"}"#).unwrap();
        assert!(matches!(&gone, Event::Gone(g) if g.session.as_str() == "s"));
        assert_eq!(serde_json::to_value(&gone).unwrap(), serde_json::json!({"type": "gone", "session": "s"}));
        let later: Event = serde_json::from_str(r#"{"type":"moved","to":1}"#).unwrap();
        assert_eq!(later.r#type(), "moved");
        assert_eq!(serde_json::to_value(&later).unwrap(), serde_json::json!({"type": "moved", "to": 1}));
    }

    #[test]
    fn opaque_ids_and_keyed_structs_round_trip() {
        let input = serde_json::json!({"ref": {"machine": "m", "session": "s"}, "phase": "done",
            "counts": {"now": 1, "later": 2}, "rank": 3, "input": {"a": [1, 2]}, "related": ["x"]});
        let v: View = serde_json::from_value(input.clone()).unwrap();
        assert_eq!(v.counts.later, 2);
        assert_eq!(serde_json::to_value(&v).unwrap(), input);
    }
}
"""


class Compiles(unittest.TestCase):
    """The generated code builds and behaves, where this machine has the language's toolchain."""

    def test_go_vets_and_runs(self):
        go = shutil.which("go")
        if not go:
            self.skipTest("go is not on PATH")
        with tempfile.TemporaryDirectory() as tmp:
            (Path(tmp) / "go.mod").write_text("module typedcheck\n\ngo 1.24\n")
            (Path(tmp) / "typed_gen.go").write_text(generate("go"))
            (Path(tmp) / "typed_test.go").write_text(GO_TEST)
            env = {**os.environ, "GOFLAGS": "-mod=mod", "GOWORK": "off"}
            for cmd in ([go, "vet", "./..."], [go, "test", "./..."]):
                code, out = run(cmd, tmp, env)
                self.assertEqual(code, 0, out)

    def test_rust_checks_and_runs(self):
        cargo = shutil.which("cargo") or (str(Path.home() / ".cargo/bin/cargo")
                                         if (Path.home() / ".cargo/bin/cargo").exists() else None)
        if not cargo:
            self.skipTest("cargo is not on PATH")
        with tempfile.TemporaryDirectory() as tmp:
            (Path(tmp) / "Cargo.toml").write_text(
                '[package]\nname = "typedcheck"\nversion = "0.0.0"\nedition = "2021"\n\n[dependencies]\n'
                'serde = { version = "1", features = ["derive"] }\nserde_json = "1"\n')
            (Path(tmp) / "src").mkdir()
            (Path(tmp) / "src/typed.rs").write_text(generate("rust"))
            (Path(tmp) / "src/lib.rs").write_text("#[rustfmt::skip]\npub mod typed;\n" + RUST_TEST)
            env = {**os.environ, "RUSTFLAGS": "-D warnings"}
            code, out = run([cargo, "test", "--offline", "--quiet"], tmp, env)
            if code != 0 and ("offline" in out and ("failed to" in out or "no matching package" in out)):
                self.skipTest("serde and serde_json are not in this machine's cargo cache (--offline)")
            self.assertEqual(code, 0, out)

    def test_typescript_type_checks(self):
        tsc = os.environ.get("PERSONA_TSC") or str(ROOT / "web/node_modules/typescript/bin/tsc")
        node = shutil.which("node")
        if not node or not Path(tsc).exists():
            self.skipTest("no TypeScript compiler (web/node_modules or PERSONA_TSC)")
        with tempfile.TemporaryDirectory() as tmp:
            (Path(tmp) / "typed.ts").write_text(generate("ts"))
            (Path(tmp) / "typed_arkts.ts").write_text(generate("arkts"))  # the ArkTS flavour is TypeScript too
            (Path(tmp) / "use.ts").write_text(
                'import { parsePhase, sessionID, type Phase, type SessionID, type View } from "./typed";\n'
                'const p = parsePhase("resting");\n'
                'const known: Phase | null = typeof p === "string" ? p : null;\n'
                'const id: SessionID = sessionID("s");\n'
                '// @ts-expect-error a bare string is not a SessionID\n'
                'const raw: SessionID = "s";\n'
                'function phaseWord(v: View): string {\n'
                '  switch (v.phase) {\n'
                '    case "needs_you": case "working": case "done": case "error": case "idle": case "unknown":\n'
                '      return v.phase;\n'
                '    default: { const never: never = v.phase; return never; }\n'
                '  }\n'
                '}\n'
                'export { known, id, raw, phaseWord };\n')
            code, out = run([node, tsc, "--noEmit", "--strict", "--target", "es2022", "--module", "esnext",
                             "--moduleResolution", "bundler", "typed.ts", "typed_arkts.ts", "use.ts"], tmp)
            self.assertEqual(code, 0, out)

    def test_swift_parses(self):
        swiftc = shutil.which("swiftc")
        if not swiftc:
            self.skipTest("swiftc is not on PATH")
        with tempfile.TemporaryDirectory() as tmp:
            (Path(tmp) / "Typed.swift").write_text(generate("swift"))
            code, out = run([swiftc, "-parse", "Typed.swift"], tmp)
            self.assertEqual(code, 0, out)


if __name__ == "__main__":
    unittest.main()
