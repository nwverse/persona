# Bounded tool inputs

The hub (`server/internal/transcript/clip.go` `clipJSON`) and runtime SDK (`integrations/runtime-sdk/lib/items.js`
`clipInput`) read these cases. They cover the third pass over tool-input objects: after the two existing string passes,
arrays lose tail items longest-first, then objects lose trailing fields widest-first, until the encoded object is at most
24,576 UTF-8 bytes. All data is made up.

## Rules

| Rule | Cases |
| --- | --- |
| Thousands of short values cannot keep an object over budget. | `short_values` |
| Nested arrays are detached correctly, and surviving values retain their order and types. | `nested_arrays`, `object_array`, `todo_array`, `question_array` |
| Object fields are dropped only after no attached array can be shortened; the root marker itself is never removed. | `wide_object`, `root_wide_object`, `escaped_root_object`, `single_long_key` |
| The two string passes still run first. | `mixed_strings` |
| The marker is included in the exact 24,576-byte bound. | `one_byte_over`, `exact_budget` |
| An input at the budget is unchanged, and a user field is never overwritten by the marker. | `at_budget`, `marker_collision` |

The marker is one collision-safe top-level string field, normally `zz_persona_clipped`. Its value names the direct array
and object-field removals and ends with the existing cut note. No marker value is inserted into an array. The `zz_` prefix
keeps the field after ordinary argument names in sorted-key subject fallbacks. Specialized clients ignore this unknown
root field; generic argument and Raw Input views retain and show it.

## Format

`{"version": 1, "cases": [...]}`. Each case has `input`, a compact generated JSON value, and `expected` assertions. The
test consumers expand these directives recursively:

- `{"$repeat": N, "value": V}`: an array of N copies of V.
- `{"$fields": N, "key": "prefix_", "pad": 4, "value": V}`: an object with zero-padded indexes in its keys.
- `{"$index": 0}`: the innermost repeat index as a string; a positive value zero-pads to that width.
- `{"$string": "x", "count": N}`: a string repeated N times.
- `{"$single_field": K, "value": V}`: an object whose one expanded key K maps to V.
- `{"$pad_object_to": N, "value": {...}}`: add an ASCII `pad` field so the compact object is exactly N bytes.

`expected.check` selects structural assertions in both consumers. `input_bytes` and `output_bytes`, when present, are
exact. `remaining`, `array_items`, `object_fields`, `key_prefix`, and `marker_key` pin exact values where shown. `marker` records whether
structural clipping must leave a marker. Go and JavaScript each measure their own emitted JSON; these cases use ASCII
content where exact sizes are shared. Existing encoder differences for U+2028/U+2029 remain covered by the SDK tests.

## Checks

- Go: `cd server && go test ./internal/transcript -run TestClipInputSharedCases`
- Runtime SDK: `cd integrations/runtime-sdk && node --test test/input-clip.test.js`

To add a case, define its generated input and independent structural expectations here, then implement the same named
check in both consumers. Do not generate expected values from the implementation under test.
