// The configuration contract as this client reads and checks it (contract.ts): every kind, mirrored validation (Unicode
// scalar lengths, safe-range numbers with decimal bounds, duplicate choices), unknown kinds and versions shown but never
// written, and the API's error body.
import { describe, expect, it } from "vitest";
import example from "./fixtures/all-types.json";
import { checkValue, ConfigurationError, fieldValue, parseDocument, parseField, sameValue, scalarLength, type KnownField } from "./contract";

const field = (raw: Record<string, unknown>) => {
  const f = parseField(raw);
  if (!f || f.kind === "unsupported") throw new Error(`not a known field: ${JSON.stringify(f)}`);
  return f as KnownField;
};

describe("a document", () => {
  it("reads every kind of the example, and the kind it doesn't know as unsupported", () => {
    const { doc, writable } = parseDocument(example);
    expect(writable).toBe(true);
    const kinds = doc.groups.flatMap((g) => g.fields.map((f) => `${f.id}:${f.kind}`));
    expect(kinds).toEqual(["enabled:toggle", "nickname:text", "note:text", "locked:toggle", "paused:toggle", "mode:select", "size:select", "region:select", "days:multiselect", "limit:number", "ratio:number", "future:unsupported"]);
    expect(doc.groups[1].fields.at(-1)).toMatchObject({ kind: "unsupported", reason: "kind", title: "Accent from a newer version" });
    expect(doc.notices).toEqual({ mode: { kind: "info", message: "Manual mode asks before each run." } });
  });

  it("is shown but not writable in a schema version it doesn't know", () => {
    const { doc, writable } = parseDocument({ ...example, schema_version: 2 });
    expect(writable).toBe(false);
    expect(doc.title).toBe("Example settings");
  });

  it("still reads a newer version whose groups it can't use", () => {
    const { doc, writable } = parseDocument({ schema_version: 2, id: "x", revision: "1", title: "Later", sections: [] });
    expect(writable).toBe(false);
    expect(doc.groups).toEqual([]);
  });

  it.each([
    ["not an object", null],
    ["no id", { ...example, id: 1 }],
    ["no revision", { ...example, revision: undefined }],
    ["no values", { ...example, values: undefined }],
    ["a field id twice", { ...example, groups: [example.groups[0], { ...example.groups[1], fields: [example.groups[0].fields[0]] }] }],
  ])("is refused when it is %s", (_, raw) => {
    expect(() => parseDocument(raw)).toThrow(ConfigurationError);
    try {
      parseDocument(raw);
    } catch (e) {
      expect((e as ConfigurationError).code).toBe("invalid_document");
    }
  });

  it("gives a field's value only when it is a valid one, leaving the rest alone", () => {
    const raw = { ...example, values: { ...example.values, size: "xl", limit: 1.5 } };
    const { doc } = parseDocument(raw);
    const byId = (id: string) => doc.groups.flatMap((g) => g.fields).find((f) => f.id === id) as KnownField;
    expect(fieldValue(doc, byId("size"))).toBeUndefined();
    expect(fieldValue(doc, byId("limit"))).toBeUndefined();
    expect(fieldValue(doc, byId("mode"))).toBe("manual");
    expect(doc.values.size).toBe("xl"); // kept as it came: nothing deletes it
  });
});

describe("field definitions", () => {
  it.each([
    ["a select without options", { id: "a", kind: "select", title: "A", default: "x", select: { options: [] } }],
    ["a select whose default isn't an option", { id: "a", kind: "select", title: "A", default: "z", select: { options: [{ value: "x", title: "X" }] } }],
    ["options with the same value", { id: "a", kind: "select", title: "A", default: "x", select: { options: [{ value: "x", title: "X" }, { value: "x", title: "Y" }] } }],
    ["a toggle whose default is a string", { id: "a", kind: "toggle", title: "A", default: "true" }],
    ["a number with a step of 0", { id: "a", kind: "number", title: "A", default: 1, number: { step: 0 } }],
    ["a number bound past the safe range", { id: "a", kind: "number", title: "A", default: 1, number: { maximum: 2 ** 60 } }],
    ["a minimum above the maximum", { id: "a", kind: "number", title: "A", default: 1, number: { minimum: 5, maximum: 2 } }],
    ["min_length above max_length", { id: "a", kind: "text", title: "A", default: "", text: { min_length: 3, max_length: 2 } }],
    ["a fractional length", { id: "a", kind: "text", title: "A", default: "", text: { max_length: 2.5 } }],
    ["details that aren't title and description", { id: "a", kind: "toggle", title: "A", default: true, details: [{ title: "x" }] }],
  ])("marks %s as unsupported", (_, raw) => {
    expect(parseField(raw)).toMatchObject({ kind: "unsupported", reason: "definition" });
  });

  it("drops a field with no id or title (nothing to show it by)", () => {
    expect(parseField({ kind: "toggle", title: "A", default: true })).toBeNull();
    expect(parseField({ id: "a", kind: "toggle", default: true })).toBeNull();
  });
});

describe("values", () => {
  it("toggle: a boolean", () => {
    const f = field({ id: "t", kind: "toggle", title: "T", default: false });
    expect(checkValue(f, true)).toBeNull();
    expect(checkValue(f, "true")).toBe("invalid_type");
    expect(checkValue(f, null)).toBe("invalid_type");
  });

  it("text: counts Unicode scalars, not UTF-16 units", () => {
    const f = field({ id: "x", kind: "text", title: "X", default: "ab", text: { min_length: 2, max_length: 3 } });
    expect(scalarLength("👋👋👋")).toBe(3);
    expect("👋👋👋".length).toBe(6);
    expect(checkValue(f, "👋👋👋")).toBeNull();
    expect(checkValue(f, "世界")).toBeNull();
    expect(checkValue(f, "👋")).toBe("too_short");
    expect(checkValue(f, "abcd")).toBe("too_long");
    expect(checkValue(f, 12)).toBe("invalid_type");
  });

  it("select: one of its options", () => {
    const f = field({ id: "s", kind: "select", title: "S", default: "a", select: { options: [{ value: "a", title: "A" }, { value: "b", title: "B" }] } });
    expect(checkValue(f, "b")).toBeNull();
    expect(checkValue(f, "c")).toBe("invalid_choice");
    expect(checkValue(f, ["a"])).toBe("invalid_type");
  });

  it("multiselect: known, distinct choices within its counts", () => {
    const f = field({ id: "m", kind: "multiselect", title: "M", default: ["a"], multiselect: { min_selected: 1, max_selected: 2, options: [{ value: "a", title: "A" }, { value: "b", title: "B" }, { value: "c", title: "C" }] } });
    expect(checkValue(f, ["a", "b"])).toBeNull();
    expect(checkValue(f, ["a", "a"])).toBe("duplicate");
    expect(checkValue(f, ["z"])).toBe("invalid_choice");
    expect(checkValue(f, [])).toBe("too_few_selected");
    expect(checkValue(f, ["a", "b", "c"])).toBe("too_many_selected");
    expect(checkValue(f, "a")).toBe("invalid_type");
  });

  it("number: finite, in the safe integer range, an integer when asked, inside decimal bounds", () => {
    const n = field({ id: "n", kind: "number", title: "N", default: 0.5, number: { minimum: 0.25, maximum: 1.75 } });
    expect(checkValue(n, 0.25)).toBeNull();
    expect(checkValue(n, 1.75)).toBeNull();
    expect(checkValue(n, 0.2)).toBe("out_of_range");
    expect(checkValue(n, Number.NaN)).toBe("out_of_range");
    expect(checkValue(n, Number.POSITIVE_INFINITY)).toBe("out_of_range");
    expect(checkValue(n, "1")).toBe("invalid_type");
    const i = field({ id: "i", kind: "number", title: "I", default: 1, number: { integer: true } });
    expect(checkValue(i, 1.5)).toBe("not_integer");
    expect(checkValue(i, Number.MAX_SAFE_INTEGER)).toBeNull();
    expect(checkValue(i, Number.MAX_SAFE_INTEGER + 2)).toBe("out_of_range");
  });

  it("compares a multiselect's choices in any order", () => {
    expect(sameValue(["a", "b"], ["b", "a"])).toBe(true);
    expect(sameValue(["a"], ["a", "b"])).toBe(false);
    expect(sameValue(1, 1)).toBe(true);
  });
});

describe("the error body", () => {
  it("reads error, code and field_errors", () => {
    const e = ConfigurationError.fromBody({ error: "Nope", code: "invalid_update", field_errors: [{ id: "a", code: "too_long" }, { id: "b", code: "custom", message: "Pick another" }, { bad: true }] });
    expect(e.code).toBe("invalid_update");
    expect(e.message).toBe("Nope");
    expect(e.fieldErrors).toEqual([{ id: "a", code: "too_long" }, { id: "b", code: "custom", message: "Pick another" }]);
  });

  it("falls back to a generic code for anything else", () => {
    expect(ConfigurationError.fromBody("<html>").code).toBe("failed");
    expect(ConfigurationError.fromBody(undefined, "conflict").code).toBe("conflict");
  });
});
