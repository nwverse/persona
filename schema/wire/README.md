# Hub wire schemas

JSON Schemas for the wire shapes of domains the shared fixtures (`spec/fixtures/`) already pin (#7891 stage 5, #9117).
Each `<domain>.schema.json` generates the clients' types with `scripts/gen/extension_schema.py`, the same generator and
subset as `schema/extension/README.md`; the file's `x-out` names the generated TypeScript (`ts`), Swift (`swift`), Go
(`go`) and Kotlin (`kotlin`) files it wants. Swift and Kotlin types carry no prefix. Go and Kotlin take the package of
the directory they are written to (the generator checks that the files already there say the same); a Go file gets no
package doc and names its registry of definitions after the schema (`todoViewDefs`), since it shares its package with
hand-written code. Android and Go are generated only for a schema whose `x-out` names them.

```sh
python3 scripts/gen/extension_schema.py           # after changing a schema
python3 scripts/gen/extension_schema.py --check   # compare only (also part of scripts/ci/guards.sh)
python3 scripts/test/test_wire_schema.py          # --check, fixture samples hold to the schema, no hand-written copy
```

| File | Domain | Generated |
| --- | --- | --- |
| `recents.schema.json` | `RecentSetup`, New Agent's recent setups (`spec/fixtures/recents`) | `web/src/lib/recents-wire.ts`, `apple/Packages/PersonaWire/Sources/PersonaWire/RecentsWire.swift` |
| `todo-view.schema.json` | To-dos (#10226): a row's view (`TodoSummary`) and its asks (`OwnerAsk`), the list's counts (`TodoCounts`) and places (`TodoPlaces`) (`spec/fixtures/todo-view`, `spec/fixtures/todo-counts`); a to-do's conversation in rounds (`TodoTimeline`), its results (`TodoResult`) and what changed since the owner looked (`TodoRecap`) (#10465, `spec/fixtures/todo-rounds`), and what `GET /api/v1/todos/{id}` carries of them beside the row (`TodoItemDetail`); a decision push's `todo` (`TodoDecidePush`, #10465, `spec/fixtures/todo-push`) | `web/src/lib/todo-view-wire.ts`, `apple/Packages/PersonaWire/Sources/PersonaWire/TodoViewWire.swift`, `server/internal/assistantwire/todo_view_gen.go`, `android/core/contracts/src/main/kotlin/persona/core/contracts/generated/TodoViewWire.kt` |

`x-out` may also name `arkts` (HarmonyOS: the TypeScript output, without what ArkTS lacks) and `rust` (serde types;
the crate that includes the file needs `serde` with `derive` and `serde_json`, and its `mod` line `#[rustfmt::skip]`,
since the generator writes the file without rustfmt). No schema here asks for Rust yet. The generator is
`scripts/gen/wiregen/`, one module per language (`model.py` reads the schemas; `go.py`, `ts.py`, `arkts.py`,
`swift.py`, `kotlin.py`, `rust.py`); `scripts/gen/extension_schema.py` stays the command.

## Typed keywords (opt-in)

The keywords below turn the open shapes the generator writes by default into closed types (#15397, the typed core
plan's 2.1–2.5). Each is off unless a schema names it, so a schema without them generates the same bytes as before;
`scripts/test/test_wiregen_typed.py` checks every one in every language against
`scripts/test/fixtures/wiregen/typed.schema.json` and compiles the output where the toolchain is on the machine. A
misused keyword stops the generator.

| Keyword | Where | Generates |
| --- | --- | --- |
| `"x-enum": "closed"` | a string `enum` definition | A real enum with an explicit unknown that keeps the raw value: Go `type Phase string` with its constants, the ordered `Phases []Phase` and `Valid()` (an unknown value keeps its string and is not valid); Swift `enum Phase { case needsYou, …; case unknown(String) }` with `init(rawValue:)`, `rawValue` and hand-written `Codable`; Kotlin `sealed interface Phase` of `data object`s plus `Unknown(raw)`, `Phase.of(raw)` and a string serializer; TypeScript and ArkTS the literal union without `(string & {})`, plus `UnknownPhase` and `parsePhase(raw)`; Rust `enum Phase { …, Unknown(String) }` with `parse`, `as_str` and serde impls. A field of the enum has the enum's type; a required one defaults to the unknown `""` where a language defaults. A value whose name would be the type's own (`unknown`, `all`) takes a `Value` suffix. Every value must make a name (letters and digits, a letter first). |
| `"x-unknown": "count"` | with `"x-enum": "closed"` | Decoding a value the enum does not know also calls the file's defect hook, named after the schema (`todo-view` → `TodoViewWire`): Go `var TodoViewWireUnknownEnum func(enum, raw string)`, Swift `TodoViewWireDefects.unknownEnum`, Kotlin `TodoViewWireDefects.unknownEnum`, TypeScript and ArkTS `TodoViewWireDefects.unknownEnum` (called from `parseX`), Rust `static TODO_VIEW_WIRE_UNKNOWN_ENUM: OnceLock<fn(&str, &str)>`. Unset, nothing is called; set it once at start. |
| `"x-id": "<Name>"` | a string definition, repeating its name; or a string property naming such a definition | An ID type: Go `type SessionID string`; Swift `struct SessionID: RawRepresentable, Codable, Hashable, Sendable` encoding as the bare string; Kotlin `@JvmInline value class SessionID(val raw: String)`; TypeScript `string & { readonly __brand: "SessionID" }` with the constructor `sessionID(raw)` (ArkTS: a plain `string` alias, it has no intersection types); Rust `#[serde(transparent)] struct SessionID(pub String)`. Fields referencing it have that type. |
| `propertyNames: {"$ref": <closed enum>}` with every value in `required` | a map property | A struct with one field per key, named `<Owner><Field>` (`TodoCounts.sections` → `TodoCountsSections`), in place of `map[string]int` / `[String: Int]`. A map keyed by an open enum, or that does not require every key, stays a map. |
| `"x-decode": "strict"` | an object definition, or the file's root (then every object in it) | Decoding a missing required field or a mistyped one fails: Swift `try c.decode` / `try c.decodeIfPresent` in place of `try?` with a default, Kotlin required fields without defaults, Rust required fields without `#[serde(default)]`. A closed enum's unknown value still decodes, to its unknown. Go and TypeScript decode as before (Go's `encoding/json` already refuses a mistyped field). Not with `additionalProperties`. |
| `"x-opaque": "<reason>"` | a definition of free JSON (no `type`, or an object without `properties`) | JSON that stays open on purpose, with its reason in the doc comment: Go `type ToolInput json.RawMessage` with pass-through `MarshalJSON` / `UnmarshalJSON`; Swift `typealias ToolInput = JSONValue`, Kotlin `= JsonElement`, TypeScript `= unknown`, Rust `= serde_json::Value`. The reason is required; on an inline property the keyword is refused (name a definition). |

Every sample a fixture holds for a definition must name exactly the schema's fields, at any depth, and every definition
must be reached by some sample (`test_wire_schema.py`). The clients' tests write each sample back through the generated
type and compare: Apple (`apple/PersonaTests/RecentSetupsTests.swift`, `apple/PersonaTests/TodoViewWireTests.swift`) the
bytes, Go (`server/internal/assistantwire/todo_view_gen_test.go`) and Kotlin
(`android/core/contracts/src/test/kotlin/persona/core/contracts/TodoViewWireTest.kt`) the JSON values; Go also holds
what it writes to the schema with `server/internal/jsonsubset`, as the tests of the code that builds these values
should, since a nil slice or map writes `null`. A sample writes an optional boolean only when it is true and leaves an
empty list or map out, as Go's `omitempty` does, and a time as Go writes it.

Hand-written copies of a generated type may not return: the test fails when a tracked file declares one of a schema's
names outside its generated files, in every language its `x-out` names and in those its `x-guard` adds (`arkts`,
`rust`: languages a copy could be written in before they are generated, or never are). `x-reserved` lists names a later
stage will define, guarded from now on. The rule is per schema, so `recents.schema.json` guards TypeScript and Swift
only (its Kotlin and Rust copies predate it). A decoder that reads the JSON key by key declares no type (HarmonyOS's
`Wire.text(obj, 'key')`), so `x-guard-keys` lists keys and values distinctive enough that the test also fails on a
quoted one in a Swift, Kotlin or ArkTS source the schema guards, outside its generated files and the tests. A key goes
there only while no client source quotes it for another reason; TypeScript and Go are not scanned for keys, since plain
code there compares the generated types' values.

## IDs and AgentRef (`ids.schema.json`, #15397)

`ids.schema.json` defines the first batch of contract IDs with `x-id` (`MachineID`, `SessionID`, `InteractionID`,
`TodoID`, `ConversationID`, `HubID`, `DeviceID`, `ExtensionID`, `ClientMsgID`, `InboxItemID`, `ConnectorID`,
`SubscriptionID`) and `AgentRef{machine, session}`, the one shape for "points at an Agent" (strict decode). Go's output is
the contract package `server/internal/wireids`. Core's opaque `agentref` types keep their names; the guard above skips
`server/internal/agentref` for those three names only (`OPAQUE_COUNTERPARTS` in `test_wire_schema.py`).

Follow-up, not built: each wire schema loads on its own, so another schema cannot `$ref` `ids.schema.json` yet and
declares its own `x-id` definitions meanwhile. Cross-schema `$ref`, and wiring the generated Rust file
(`native/app-core/crates/persona-core/src/ids_wire.rs`, written but not yet a `mod`) into `persona-core`, are left for P5.
