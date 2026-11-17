// @vitest-environment jsdom
// The generic configuration form (ConfigurationForm.tsx) over the synthetic all-types document: every control with its
// name for assistive technology and its keys, choices written at once and saved only once committed, typed text saved
// explicitly and kept through failures and conflicts, resets, read-only / disabled / unknown fields, a version it can't
// write, and a source replaced while its answer is still coming.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { formatList, t } from "../../../i18n";
import example from "../../../lib/configuration/fixtures/all-types.json";
import { ConfigurationError, type ConfigurationSource, type ConfigurationUpdate } from "../../../lib/configuration/contract";
import { ConfigurationForm } from "./ConfigurationForm";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

type Doc = typeof example;
const clone = (d: unknown): Doc => JSON.parse(JSON.stringify(d));

/** An in-memory source: commits like a writer, or answers with `fail` once. */
function memory(start: unknown = example, key = "example") {
  let doc = clone(start);
  const updates: ConfigurationUpdate[] = [];
  const state = { fail: null as unknown, hold: null as null | ((go: () => void) => void) };
  const source: ConfigurationSource = {
    key,
    read: async () => clone(doc),
    update: async (u) => {
      updates.push(u);
      if (state.hold) await new Promise<void>((go) => state.hold!(go));
      if (state.fail) {
        const f = state.fail;
        state.fail = null;
        throw f;
      }
      if (u.revision !== doc.revision) throw new ConfigurationError("conflict");
      const next = clone(doc);
      const fields = (next.groups as { fields: { id: string; default: unknown }[] }[]).flatMap((g) => g.fields);
      Object.assign(next.values, u.changes ?? {});
      for (const id of u.reset ?? []) (next.values as Record<string, unknown>)[id] = fields.find((f) => f.id === id)!.default;
      next.revision = `${doc.revision}+`;
      doc = next;
      return clone(doc);
    },
  };
  return { source, updates, state, set: (d: Doc) => (doc = d) };
}

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const settle = async () => {
  for (let i = 0; i < 4; i++) await act(async () => {});
};
async function show(source: ConfigurationSource) {
  act(() => root.render(<ConfigurationForm source={source} />));
  await settle();
}
const row = (id: string) => host.querySelector<HTMLElement>(`[data-configuration-field="${id}"]`)!;
const buttonIn = (el: Element, text: string) => [...el.querySelectorAll("button")].find((b) => b.textContent === text)!;
const labelled = (el: Element) => {
  const id = el.getAttribute("aria-labelledby");
  return id ? document.getElementById(id)?.textContent : el.getAttribute("aria-label");
};
function type(el: HTMLInputElement | HTMLTextAreaElement, value: string) {
  const set = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el), "value")!.set!;
  act(() => {
    set.call(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
const key = (el: Element, k: string) => act(() => void el.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true })));

describe("drawing every kind", () => {
  it("shows the document's title, groups, fields, descriptions, notices and details as given", async () => {
    const m = memory();
    await show(m.source);
    expect(host.querySelector("h2")?.textContent).toBe("Example settings");
    expect(host.textContent).toContain("Basics");
    expect(host.textContent).toContain("Switches and text.");
    expect(row("mode").textContent).toContain("Manual mode asks before each run.");
    const dl = row("limit").querySelector("dl")!;
    expect([...dl.querySelectorAll("dt")].map((d) => d.textContent)).toEqual([t("settings-configuration.default"), "Takes effect"]);
    expect([...dl.querySelectorAll("dd")].map((d) => d.textContent)).toEqual(["10", "From the next run."]);
    expect(row("days").querySelector("dd")?.textContent).toBe(formatList(["Monday", "Wednesday"]));
  });

  it("names every control by its field's title", async () => {
    await show(memory().source);
    expect(row("enabled").querySelector('[role="switch"]')?.getAttribute("aria-label")).toBe("Enable the example");
    expect(labelled(row("nickname").querySelector("input")!)).toBe("Nickname");
    expect(labelled(row("note").querySelector("textarea")!)).toBe("Note");
    expect(row("mode").querySelector('[role="radiogroup"]')?.getAttribute("aria-label")).toBe("Mode");
    expect(row("size").querySelector('[role="radiogroup"]')?.getAttribute("aria-label")).toBe("Size");
    expect(labelled(row("region").querySelector("select")!)).toBe("Region");
    expect(labelled(row("days").querySelector('[role="group"]')!)).toBe("Days");
    expect(labelled(row("limit").querySelector("input")!)).toBe("Daily limit");
  });

  it("shows read-only, disabled and unknown fields without a way to change them", async () => {
    await show(memory().source);
    expect((row("locked").querySelector('[role="switch"]') as HTMLButtonElement).disabled).toBe(true);
    expect(row("locked").textContent).toContain(t("settings-configuration.read_only"));
    expect((row("paused").querySelector('[role="switch"]') as HTMLButtonElement).disabled).toBe(true);
    expect(row("paused").textContent).toContain("Available once the example is set up.");
    expect(row("future").textContent).toContain(t("settings-configuration.unsupported_kind"));
    expect(row("future").querySelector("button, input, select, textarea")).toBeNull();
  });
});

describe("choices", () => {
  it("a switch, a list, a segmented choice, a dropdown and checks each write that field only", async () => {
    const m = memory();
    await show(m.source);
    act(() => (row("enabled").querySelector('[role="switch"]') as HTMLButtonElement).click());
    await settle();
    act(() => buttonIn(row("size"), "Large").click());
    await settle();
    act(() => [...row("mode").querySelectorAll<HTMLButtonElement>('[role="radio"]')][0].click());
    await settle();
    const select = row("region").querySelector("select")!;
    act(() => {
      select.value = "ap";
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await settle();
    act(() => [...row("days").querySelectorAll<HTMLButtonElement>('[role="checkbox"]')][2].click());
    await settle();
    expect(m.updates.map((u) => u.changes)).toEqual([{ enabled: false }, { size: "l" }, { mode: "auto" }, { region: "ap" }, { days: ["mon", "wed", "fri"] }]);
    expect(m.updates.map((u) => u.revision)).toEqual(["r1", "r1+", "r1++", "r1+++", "r1++++"]);
    expect(row("days").textContent).toContain(t("settings-configuration.saved"));
  });

  it("shows Saving… (not Saved) while the source hasn't answered", async () => {
    const m = memory();
    let go = () => {};
    m.state.hold = (g) => (go = g);
    await show(m.source);
    act(() => buttonIn(row("size"), "Small").click());
    await settle();
    expect(row("size").textContent).toContain(t("settings-configuration.saving"));
    expect(row("size").textContent).not.toContain(t("settings-configuration.saved"));
    m.state.hold = null;
    go();
    await settle();
    expect(row("size").textContent).toContain(t("settings-configuration.saved"));
  });

  it("arrow keys move a list's choice", async () => {
    const m = memory();
    await show(m.source);
    const group = row("mode").querySelector('[role="radiogroup"]')!;
    key(group, "ArrowDown"); // from Manual (the second) around to Automatic
    await settle();
    expect(m.updates.at(-1)?.changes).toEqual({ mode: "auto" });
  });

  it("refuses unchecking the last check (at least one) without asking the source", async () => {
    const m = memory({ ...clone(example), values: { ...example.values, days: ["mon"] } });
    await show(m.source);
    act(() => [...row("days").querySelectorAll<HTMLButtonElement>('[role="checkbox"]')][0].click());
    await settle();
    expect(m.updates).toHaveLength(0);
    expect(row("days").querySelector('[role="alert"]')?.textContent).toBe(t("settings-configuration.error_too_few_selected"));
  });
});

describe("typed text", () => {
  it("Save appears once it differs; Enter saves a single line; Escape puts it back", async () => {
    const m = memory();
    await show(m.source);
    const input = row("nickname").querySelector("input")!;
    expect(buttonIn(row("nickname"), t("settings-configuration.save"))).toBeUndefined();
    type(input, "Sam");
    expect(buttonIn(row("nickname"), t("settings-configuration.save"))).toBeDefined();
    key(input, "Escape");
    expect(input.value).toBe("");
    type(input, "Sam 👋");
    expect(row("nickname").textContent).toContain(t("settings-configuration.count", { n: 5, max: 20 }));
    key(input, "Enter");
    await settle();
    expect(m.updates.at(-1)?.changes).toEqual({ nickname: "Sam 👋" });
    expect(row("nickname").textContent).toContain(t("settings-configuration.saved"));
  });

  it("keeps the typed text, and says why, when saving fails", async () => {
    const m = memory();
    await show(m.source);
    const area = row("note").querySelector("textarea")!;
    type(area, "Line one\nline two");
    m.state.fail = new ConfigurationError("storage_failed", "The disk is full");
    act(() => buttonIn(row("note"), t("settings-configuration.save")).click());
    await settle();
    expect(area.value).toBe("Line one\nline two");
    expect(row("note").querySelector('[role="alert"]')?.textContent).toBe("The disk is full");
  });

  it("a conflict keeps the text and offers Reload, which keeps it too", async () => {
    const m = memory();
    await show(m.source);
    m.set({ ...clone(example), revision: "elsewhere", values: { ...example.values, nickname: "Theirs" } });
    const input = row("nickname").querySelector("input")!;
    type(input, "Mine");
    key(input, "Enter");
    await settle();
    const alert = host.querySelector('[role="alert"]')!;
    expect(alert.textContent).toBe(t("settings-configuration.error_conflict"));
    act(() => buttonIn(host, t("settings-configuration.reload")).click());
    await settle();
    expect(host.textContent).not.toContain(t("settings-configuration.error_conflict"));
    expect(row("nickname").querySelector("input")!.value).toBe("Mine");
    act(() => buttonIn(row("nickname"), t("settings-configuration.cancel")).click());
    expect(row("nickname").querySelector("input")!.value).toBe("Theirs");
  });

  it("a number: arrows step it, and a value out of range is said before sending", async () => {
    const m = memory();
    await show(m.source);
    const input = row("limit").querySelector("input")!;
    key(input, "ArrowUp");
    expect(input.value).toBe("30");
    type(input, "500");
    key(input, "Enter");
    expect(m.updates).toHaveLength(0);
    expect(row("limit").querySelector('[role="alert"]')?.textContent).toBe(t("settings-configuration.error_out_of_range"));
    type(input, "40");
    key(input, "Enter");
    await settle();
    expect(m.updates.at(-1)?.changes).toEqual({ limit: 40 });
  });
});

describe("resetting", () => {
  it("offers Use default only where the value differs, and sends reset", async () => {
    const m = memory();
    await show(m.source);
    expect(buttonIn(row("size"), t("settings-configuration.reset"))).toBeUndefined();
    expect(buttonIn(row("locked"), t("settings-configuration.reset"))).toBeUndefined();
    act(() => buttonIn(row("limit"), t("settings-configuration.reset")).click());
    await settle();
    expect(m.updates.at(-1)).toMatchObject({ reset: ["limit"] });
    expect(row("limit").querySelector("input")!.value).toBe("10");
  });
});

describe("what can't be read or written", () => {
  it("a version it doesn't know is shown with every control off", async () => {
    await show(memory({ ...clone(example), schema_version: 2 }).source);
    expect(host.textContent).toContain(t("settings-configuration.unsupported_version"));
    expect((row("enabled").querySelector('[role="switch"]') as HTMLButtonElement).disabled).toBe(true);
    expect(row("nickname").querySelector("input")!.disabled).toBe(true);
  });

  it("a read that fails says why and has Try again", async () => {
    let fail = true;
    const source: ConfigurationSource = { key: "x", read: async () => { if (fail) throw new ConfigurationError("not_found"); return clone(example); }, update: async () => clone(example) };
    await show(source);
    expect(host.textContent).toContain(t("settings-configuration.error_not_found"));
    fail = false;
    act(() => buttonIn(host, t("settings-configuration.retry")).click());
    await settle();
    expect(host.querySelector("h2")?.textContent).toBe("Example settings");
  });
});

describe("another source", () => {
  it("starts over for another key, and the first source's late answer is ignored", async () => {
    let release = (_: unknown) => {};
    const slow: ConfigurationSource = { key: "slow", read: () => new Promise((r) => (release = r)), update: async () => clone(example) };
    await show(slow);
    const other = memory({ ...clone(example), title: "Other settings" }, "other");
    await show(other.source);
    expect(host.querySelector("h2")?.textContent).toBe("Other settings");
    release({ ...clone(example), title: "Too late" });
    await settle();
    expect(host.querySelector("h2")?.textContent).toBe("Other settings");
  });
});
