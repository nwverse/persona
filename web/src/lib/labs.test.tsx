// @vitest-environment jsdom
// Settings › Labs' registry (lib/labs.ts): one definition per entry, validated reads and writes, the saved WebGL switch
// from before Labs still read, values from another version kept, and a browser that doesn't keep a write reported.
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { booleanLab, choiceLab, decodeLab, encodeLab, isLabId, isLabValue, LAB_IDS, LABS, readLab, useLab, writeLab, type LabDefinition } from "./labs";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const KEY = "persona.terminal.webgl";

beforeEach(() => localStorage.clear());
afterEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
});

describe("the definitions", () => {
  it("are real features only: the terminal renderer, kept in this browser, for views opened after a change", () => {
    expect(LAB_IDS).toEqual(["terminal-renderer"]);
    expect(LABS["terminal-renderer"]).toMatchObject({ kind: "choice", key: KEY, default: "standard", scope: "browser", activation: "new-views", issue: 9877 });
  });

  it.each(LAB_IDS)("%s: one key of its own, a default among its options, options that store apart", (id) => {
    const def: LabDefinition = LABS[id];
    expect(LAB_IDS.filter((other) => (LABS[other] as LabDefinition).key === def.key)).toEqual([id]);
    if (def.kind !== "choice") return;
    const values = def.options.map((o) => o.value);
    const raws = def.options.map((o) => o.raw ?? o.value);
    expect(values).toContain(def.default);
    expect(new Set(values).size).toBe(values.length);
    expect(new Set(raws).size).toBe(raws.length);
  });

  it("knows its ids and values and nothing else", () => {
    expect(isLabId("terminal-renderer")).toBe(true);
    expect(isLabId("constructor")).toBe(false);
    expect(isLabId("terminal-webgl")).toBe(false);
    expect(isLabValue("terminal-renderer", "webgl")).toBe(true);
    expect(isLabValue("terminal-renderer", "canvas")).toBe(false);
    expect(isLabValue("terminal-renderer", true)).toBe(false);
  });
});

describe("both kinds of entry", () => {
  const flag = booleanLab({ key: "persona.labs.test-flag", default: false, scope: "browser", activation: "new-views", issue: 1 });
  const onByDefault = booleanLab({ key: "persona.labs.test-on", default: true, scope: "browser", activation: "new-views", issue: 1 });
  const choice = choiceLab({ key: "persona.labs.test-choice", options: [{ value: "stable" }, { value: "next" }], default: "stable", scope: "browser", activation: "new-views", issue: 1 });

  it("a boolean stores nothing for its default, 1 / 0 otherwise, and refuses anything else", () => {
    expect(encodeLab(flag, false)).toBeNull();
    expect(encodeLab(flag, true)).toBe("1");
    expect(encodeLab(onByDefault, true)).toBeNull();
    expect(encodeLab(onByDefault, false)).toBe("0");
    expect(encodeLab(flag, "1")).toBeUndefined();
    expect(decodeLab(flag, "1")).toBe(true);
    expect(decodeLab(flag, "0")).toBe(false);
    expect(decodeLab(flag, "true")).toBeUndefined();
  });

  it("a choice stores nothing for its default and the option's value (or its raw) otherwise", () => {
    expect(encodeLab(choice, "stable")).toBeNull();
    expect(encodeLab(choice, "next")).toBe("next");
    expect(encodeLab(choice, "other")).toBeUndefined();
    expect(decodeLab(choice, "next")).toBe("next");
    expect(decodeLab(choice, "NEXT")).toBeUndefined();
    expect(encodeLab(LABS["terminal-renderer"], "webgl")).toBe("1");
  });
});

describe("reading", () => {
  it("is the default with nothing saved", () => {
    expect(readLab("terminal-renderer")).toEqual({ value: "standard", source: "default" });
  });

  it("reads the Terminal settings WebGL switch saved before Labs", () => {
    localStorage.setItem(KEY, "1");
    expect(readLab("terminal-renderer")).toEqual({ value: "webgl", source: "saved" });
  });

  it.each(["0", "true", "webgl", ""])("takes %j (another version's, or hand-written) as the default and leaves it in place", (raw) => {
    localStorage.setItem(KEY, raw);
    expect(readLab("terminal-renderer")).toEqual({ value: "standard", source: "invalid" });
    expect(localStorage.getItem(KEY)).toBe(raw);
  });

  it("is the default, said so, when the browser won't let the page read its storage", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("denied", "SecurityError");
    });
    expect(readLab("terminal-renderer")).toEqual({ value: "standard", source: "unreadable" });
  });
});

describe("writing", () => {
  it("keeps WebGL as the key's old 1, and Standard (the default) as no key", () => {
    expect(writeLab("terminal-renderer", "webgl")).toEqual({ ok: true });
    expect(localStorage.getItem(KEY)).toBe("1");
    expect(writeLab("terminal-renderer", "standard")).toEqual({ ok: true });
    expect(localStorage.getItem(KEY)).toBeNull();
  });

  it("replaces a value it didn't recognize once a choice is made", () => {
    localStorage.setItem(KEY, "webgl2");
    expect(writeLab("terminal-renderer", "standard")).toEqual({ ok: true });
    expect(readLab("terminal-renderer")).toEqual({ value: "standard", source: "default" });
  });

  it("refuses a value the entry doesn't offer, and an unknown entry, without touching storage", () => {
    localStorage.setItem(KEY, "1");
    expect(writeLab("terminal-renderer", "canvas" as never)).toEqual({ ok: false, reason: "invalid" });
    expect(writeLab("terminal-webgl" as never, "1" as never)).toEqual({ ok: false, reason: "invalid" });
    expect(localStorage.getItem(KEY)).toBe("1");
  });

  it("reports a browser that throws on the write (storage off or full) and keeps the old value", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("full", "QuotaExceededError");
    });
    expect(writeLab("terminal-renderer", "webgl")).toEqual({ ok: false, reason: "storage" });
    expect(readLab("terminal-renderer").value).toBe("standard");
  });

  it("reports a write that doesn't read back", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {});
    expect(writeLab("terminal-renderer", "webgl")).toEqual({ ok: false, reason: "storage" });
  });
});

describe("useLab", () => {
  function Probe({ seen }: { seen: string[] }) {
    const s = useLab("terminal-renderer");
    seen.push(`${s.value}/${s.source}`);
    return null;
  }

  it("follows writes here and changes made in another tab", () => {
    const seen: string[] = [];
    const host = document.createElement("div");
    const root = createRoot(host);
    act(() => root.render(<Probe seen={seen} />));
    expect(seen.at(-1)).toBe("standard/default");
    act(() => void writeLab("terminal-renderer", "webgl"));
    expect(seen.at(-1)).toBe("webgl/saved");
    act(() => {
      localStorage.removeItem(KEY); // another tab chose Standard
      window.dispatchEvent(new StorageEvent("storage", { key: KEY }));
    });
    expect(seen.at(-1)).toBe("standard/default");
    const renders = seen.length;
    act(() => window.dispatchEvent(new StorageEvent("storage", { key: "persona.unrelated" })));
    expect(seen.length).toBe(renders);
    act(() => root.unmount());
  });
});
