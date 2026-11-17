// @vitest-environment node
// The showcase's own mock data and sources (configuration-showcase-data.mjs): every scenario, in every client language,
// builds documents the real contract parses with valid effective values, and each in-memory writer commits, refuses or
// fails exactly as its scenario says. This is fixture verification, not production behavior.
import { describe, expect, it } from "vitest";
import { buildShowcase, SCENARIO_IDS, shell } from "./configuration-showcase-data.mjs";
import { CONFIGURATION_VERSION, fieldValue, parseDocument } from "../src/lib/configuration/contract.ts";

const LANGS = ["en", "zh-Hans", "ja"];
const FAST = { latency: { read: 0, save: 0 } };
const moduleByKey = (showcase, key) => showcase.modules.find((module) => module.key === key);
const updateFor = (doc, more) => ({ schema_version: CONFIGURATION_VERSION, id: doc.id, revision: doc.revision, ...more });

describe("the showcase in every language", () => {
  it("names every scenario and every page string", () => {
    for (const lang of LANGS) {
      const strings = shell(lang);
      for (const key of ["title", "fixtureProblem"]) expect(strings[key].trim(), `${lang} ${key}`).not.toBe("");
    }
    expect(SCENARIO_IDS).toEqual(["default", "slow", "load-fail", "reject", "conflict", "unconfirmed", "contract", "empty", "unsupported"]);
  });

  it.each(LANGS.flatMap((lang) => SCENARIO_IDS.map((id) => [lang, id])))("%s %s: every document parses with valid effective values", async (lang, id) => {
    const showcase = buildShowcase(id, lang, FAST);
    expect(showcase.scenario).toBe(id);
    expect(showcase.modules.length).toBeGreaterThan(0);
    const keys = new Set();
    const ids = new Set();
    for (const module of showcase.modules) {
      expect(keys.has(module.key), module.key).toBe(false);
      keys.add(module.key);
      const { doc, writable } = parseDocument(module.document);
      expect(ids.has(doc.id), doc.id).toBe(false);
      ids.add(doc.id);
      // Only the newer-version document is deliberately not writable.
      expect(writable).toBe(module.key !== "showcase-newer-version");
      for (const group of doc.groups) {
        for (const field of group.fields) {
          if (field.kind !== "unsupported") expect(fieldValue(doc, field), `${module.key}:${field.id}`).toBeDefined();
        }
      }
      if (id === "load-fail" && module.key === "showcase-terminal") {
        await expect(module.source.read()).rejects.toMatchObject({ code: "configuration_unavailable" });
        expect((await module.source.read()).id).toBe(doc.id);
      } else {
        expect((await module.source.read()).id).toBe(doc.id);
      }
    }
  });

  it("falls back to the full showcase for an unknown scenario, and takes overview as its URL alias", () => {
    expect(buildShowcase("no-such-scenario", "en", FAST).scenario).toBe("default");
    const overview = buildShowcase("overview", "en", FAST);
    expect(overview.scenario).toBe("default");
    expect(overview.modules.map((module) => module.key)).toEqual(buildShowcase("default", "en", FAST).modules.map((module) => module.key));
  });

  it("keeps the product view to three genuine modules and focuses each failure on its module", () => {
    const keys = (id) => buildShowcase(id, "en", FAST).modules.map((module) => module.key);
    expect(keys("default")).toEqual(["showcase-terminal", "showcase-digest", "showcase-editor"]);
    expect(keys("overview")).toEqual(keys("default"));
    expect(keys("slow")).toEqual(keys("default"));
    expect(keys("load-fail")).toEqual(["showcase-terminal"]);
    for (const id of ["reject", "conflict", "unconfirmed"]) expect(keys(id), id).toEqual(["showcase-digest"]);
    expect(keys("contract")).toEqual(["showcase-shared"]);
  });

  it("covers every control kind and select presentation across the full showcase", () => {
    const kinds = new Set();
    const presentations = new Set();
    for (const module of buildShowcase("default", "en", FAST).modules) {
      for (const group of parseDocument(module.document).doc.groups) {
        for (const field of group.fields) {
          kinds.add(field.kind);
          if (field.kind === "select") presentations.add(field.select.presentation ?? "list");
        }
      }
    }
    expect([...kinds].sort()).toEqual(["multiselect", "number", "select", "text", "toggle"]);
    expect([...presentations].sort()).toEqual(["dropdown", "list", "segmented"]);
  });

  it("keeps the shared all-types example unchanged in its own coverage scenario", () => {
    const shared = moduleByKey(buildShowcase("contract", "en", FAST), "showcase-shared");
    expect(shared.document.id).toBe("example-preferences");
    expect(shared.document.revision).toBe("revision-1");
    const { doc } = parseDocument(shared.document);
    expect(doc.groups[0].fields.map((field) => `${field.id}:${field.kind}`)).toEqual([
      "compact:toggle",
      "label:text",
      "notes:text",
      "layout:select",
      "accent:select",
      "motion:select",
      "sections:multiselect",
      "interval:number",
      "managed:toggle",
    ]);
  });

  it("shows a newer field read-only and a newer version as not writable", () => {
    const showcase = buildShowcase("unsupported", "en", FAST);
    const kind = parseDocument(moduleByKey(showcase, "showcase-newer-kind").document);
    expect(kind.writable).toBe(true);
    expect(kind.doc.groups[0].fields.at(-1)).toMatchObject({ id: "palette", kind: "unsupported", reason: "kind" });
    const version = parseDocument(moduleByKey(showcase, "showcase-newer-version").document);
    expect(version.writable).toBe(false);
  });

  it("renders a valid document with no groups as the empty form", () => {
    const showcase = buildShowcase("empty", "zh-Hans", FAST);
    const { doc } = parseDocument(showcase.modules[0].document);
    expect(doc.groups).toEqual([]);
    expect(doc.values).toEqual({});
  });
});

describe("the in-memory writers", () => {
  const digest = (scenario, lang = "en") => moduleByKey(buildShowcase(scenario, lang, FAST), "showcase-digest");

  it("commits a change and a reset in memory, without touching another module", async () => {
    const showcase = buildShowcase("default", "zh-Hans", FAST);
    const digestSource = moduleByKey(showcase, "showcase-digest");
    const editorSource = moduleByKey(showcase, "showcase-editor");
    const before = await digestSource.source.read();
    const editorBefore = await editorSource.source.read();
    const next = await digestSource.source.update(updateFor(before, { changes: { topic: "新的主题 🌤" } }));
    expect(next.revision).not.toBe(before.revision);
    expect(next.values.topic).toBe("新的主题 🌤");
    expect((await digestSource.source.read()).values.topic).toBe("新的主题 🌤");
    expect(await editorSource.source.read()).toEqual(editorBefore);
    const reset = await digestSource.source.update(updateFor(next, { reset: ["hour"] }));
    expect(reset.values.hour).toBe(8);
  });

  it("refuses an invalid value through the shared writer", async () => {
    const module = digest("default");
    const doc = await module.source.read();
    const err = await module.source.update(updateFor(doc, { changes: { hour: 24 } })).catch((e) => e);
    expect(err).toMatchObject({ code: "configuration_invalid" });
    expect(err.fieldErrors.map(({ id, code }) => ({ id, code }))).toEqual([{ id: "hour", code: "maximum" }]);
  });

  it("reject: the first topic save names the field, and the next save commits", async () => {
    const module = digest("reject");
    const doc = await module.source.read();
    const err = await module.source.update(updateFor(doc, { changes: { topic: "Another topic" } })).catch((e) => e);
    expect(err).toMatchObject({ code: "configuration_invalid" });
    expect(err.fieldErrors).toHaveLength(1);
    expect(err.fieldErrors[0]).toMatchObject({ id: "topic", code: "taken" });
    expect(err.fieldErrors[0].message).toBeTruthy();
    expect((await module.source.update(updateFor(doc, { changes: { topic: "Another topic" } }))).values.topic).toBe("Another topic");
  });

  it("conflict: the first save conflicts, and Reload's read sees the other writer's value", async () => {
    const module = digest("conflict");
    const first = await module.source.read();
    await expect(module.source.update(updateFor(first, { changes: { topic: "Mine" } }))).rejects.toMatchObject({ code: "configuration_conflict" });
    const after = await module.source.read();
    expect(after.revision).not.toBe(first.revision);
    expect(after.values.topic).toBe("Weekly digest");
    expect((await module.source.update(updateFor(after, { changes: { topic: "Mine" } }))).values.topic).toBe("Mine");
  });

  it("unconfirmed: the first save throws, and a later one commits", async () => {
    const module = digest("unconfirmed");
    const doc = await module.source.read();
    await expect(module.source.update(updateFor(doc, { changes: { topic: "Mine" } }))).rejects.toBeInstanceOf(TypeError);
    expect((await module.source.read()).revision).toBe(doc.revision);
    expect((await module.source.update(updateFor(doc, { changes: { topic: "Mine" } }))).values.topic).toBe("Mine");
  });

  it("load-fail: the first read fails once and the next succeeds", async () => {
    const terminal = moduleByKey(buildShowcase("load-fail", "en", FAST), "showcase-terminal");
    await expect(terminal.source.read()).rejects.toMatchObject({ code: "configuration_unavailable" });
    expect((await terminal.source.read()).id).toBe("showcase-terminal");
  });

  it("gives each page fresh sources, so two pages never share values", async () => {
    const one = moduleByKey(buildShowcase("default", "en", FAST), "showcase-digest");
    const two = moduleByKey(buildShowcase("default", "en", FAST), "showcase-digest");
    const doc = await one.source.read();
    await one.source.update(updateFor(doc, { changes: { topic: "One" } }));
    expect((await two.source.read()).values.topic).toBe("Daily summary 🌤");
  });
});
