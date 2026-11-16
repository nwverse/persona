# Hub wire schemas

JSON Schemas for the wire shapes of domains the shared fixtures (`spec/fixtures/`) already pin (#7891 stage 5, #9117).
Each `<domain>.schema.json` generates the clients' types with `scripts/gen/extension_schema.py`, the same generator and
subset as `schema/extension/README.md`; the file's `x-out` names the generated TypeScript (`ts`) and Swift (`swift`)
files. Swift types carry no prefix. Android is not generated.

```sh
python3 scripts/gen/extension_schema.py           # after changing a schema
python3 scripts/gen/extension_schema.py --check   # compare only (also part of scripts/ci/guards.sh)
python3 scripts/test/test_wire_schema.py          # --check, fixture samples hold to the schema, no hand-written copy
```

| File | Domain | Generated |
| --- | --- | --- |
| `recents.schema.json` | `RecentSetup`, New Agent's recent setups (`spec/fixtures/recents`) | `web/src/lib/recents-wire.ts`, `apple/Packages/PersonaWire/Sources/PersonaWire/RecentsWire.swift` |

Every sample a fixture holds for a definition must name exactly the schema's fields (`test_wire_schema.py`), and the
Apple tests write each sample back through the generated type and compare the bytes. Hand-written copies of a generated
type may not return: the test fails when another file declares one of the schema's names.
