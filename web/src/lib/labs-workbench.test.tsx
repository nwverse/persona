// @vitest-environment jsdom
// The Workbench layout's switch (labs-workbench.ts) and Labs source (labs-workbench-source.ts) (#13439): off by default, on kept as "1" and off as no key, a value from
// another version read as off and left in place, a browser that won't keep the write reported with nothing changed,
// listeners told on a write here and on another tab's, and the layout flags with their defaults.
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../i18n";
import { ConfigurationError, parseDocument, type ConfigurationDocument } from "./configuration/contract";
import {
  readWorkbench, readWorkbenchLayout, subscribeWorkbench, useWorkbench, writeWorkbench, writeWorkbenchLayout,
  WORKBENCH_KEY, WORKBENCH_LAYOUT_DEFAULT, WORKBENCH_LAYOUT_KEY,
} from "./labs-workbench";
import { workbenchLabsSource } from "./labs-workbench-source";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const source = workbenchLabsSource();
const read = async () => (await source.read()) as ConfigurationDocument;
const upd = (doc: ConfigurationDocument, more: Record<string, unknown>) => source.update({ schema_version: 1, id: doc.id, revision: doc.revision, ...more });

beforeEach(() => {
  localStorage.clear();
  writeWorkbenchLayout(WORKBENCH_LAYOUT_DEFAULT);
  localStorage.clear();
});
afterEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
});

describe("the switch", () => {
  it("is off by default and writes nothing", () => {
    expect(readWorkbench()).toEqual({ value: false, source: "default", raw: null });
    expect(localStorage.getItem(WORKBENCH_KEY)).toBeNull();
  });

  it("keeps on as 1 and off as no key, and tells listeners", () => {
    const heard = vi.fn();
    const off = subscribeWorkbench(heard);
    expect(writeWorkbench(true)).toEqual({ ok: true });
    expect(localStorage.getItem(WORKBENCH_KEY)).toBe("1");
    expect(readWorkbench()).toEqual({ value: true, source: "saved", raw: "1" });
    expect(writeWorkbench(false)).toEqual({ ok: true });
    expect(localStorage.getItem(WORKBENCH_KEY)).toBeNull();
    expect(heard).toHaveBeenCalledTimes(2);
    off();
    writeWorkbench(true);
    expect(heard).toHaveBeenCalledTimes(2);
  });

  it("reads a value it doesn't recognize as off and leaves it in place", () => {
    localStorage.setItem(WORKBENCH_KEY, "yes");
    expect(readWorkbench()).toEqual({ value: false, source: "invalid", raw: "yes" });
    expect(localStorage.getItem(WORKBENCH_KEY)).toBe("yes");
  });

  it("reports a browser that does not read or keep it", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("denied", "SecurityError");
    });
    expect(readWorkbench().source).toBe("unreadable");
    vi.restoreAllMocks();
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {});
    expect(writeWorkbench(true)).toEqual({ ok: false, reason: "storage" });
  });

  it("follows another tab's change", () => {
    const heard = vi.fn();
    const off = subscribeWorkbench(heard);
    window.dispatchEvent(new StorageEvent("storage", { key: WORKBENCH_KEY }));
    window.dispatchEvent(new StorageEvent("storage", { key: "unrelated" }));
    expect(heard).toHaveBeenCalledTimes(1);
    off();
  });

  it("re-renders a reader at once when it flips, with no reload", () => {
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    const Probe = () => <span>{useWorkbench() ? "on" : "off"}</span>;
    act(() => root.render(<Probe />));
    expect(host.textContent).toBe("off");
    act(() => void writeWorkbench(true));
    expect(host.textContent).toBe("on");
    act(() => void writeWorkbench(false));
    expect(host.textContent).toBe("off");
    act(() => root.unmount());
    host.remove();
  });
});

describe("the layout flags", () => {
  it("default to the changes open and the drawer shut", () => {
    expect(readWorkbenchLayout()).toEqual({ diff: true, drawer: false, drawerHeight: 0, diffWidth: 0, folded: [] });
  });

  it("keep a patch, the rest as it was, and read the same object while nothing changes", () => {
    writeWorkbenchLayout({ drawer: true, folded: ["a"] });
    const first = readWorkbenchLayout();
    expect(first).toMatchObject({ diff: true, drawer: true, folded: ["a"] });
    expect(readWorkbenchLayout()).toBe(first);
    expect(JSON.parse(localStorage.getItem(WORKBENCH_LAYOUT_KEY)!)).toMatchObject({ drawer: true });
  });

  it("read garbage as the defaults", () => {
    localStorage.setItem(WORKBENCH_LAYOUT_KEY, "{not json");
    expect(readWorkbenchLayout()).toEqual(WORKBENCH_LAYOUT_DEFAULT);
    localStorage.setItem(WORKBENCH_LAYOUT_KEY, JSON.stringify({ diff: "x", drawerHeight: -3, folded: [1, "k"] }));
    expect(readWorkbenchLayout()).toEqual({ ...WORKBENCH_LAYOUT_DEFAULT, folded: ["k"] });
  });
});

describe("the Labs document", () => {
  it("is a valid document: one toggle, off by default, with scope, activation and rollback", async () => {
    const doc = await read();
    expect(parseDocument(doc).writable).toBe(true);
    expect(source.key).toBe("labs:workbench");
    expect(doc.groups[0].fields[0]).toMatchObject({ id: "enabled", kind: "toggle", default: false });
    expect(doc.groups[0].fields[0].details?.map((d) => d.title)).toEqual([t("settings-labs.scope"), t("settings-labs.activation"), t("settings-labs.rollback")]);
    expect(doc.values).toEqual({ enabled: false });
    expect(doc.revision).toBe("default");
    expect(doc.notices).toBeUndefined();
  });

  it("writes the choice, answers with the document as committed and follows the stored value", async () => {
    const doc = await read();
    const next = (await upd(doc, { changes: { enabled: true } })) as ConfigurationDocument;
    expect(next.values).toEqual({ enabled: true });
    expect(localStorage.getItem(WORKBENCH_KEY)).toBe("1");
    expect(next.revision).toBe("saved:1");
    const reset = (await upd(next, { reset: ["enabled"] })) as ConfigurationDocument;
    expect(reset.values).toEqual({ enabled: false });
    expect(localStorage.getItem(WORKBENCH_KEY)).toBeNull();
  });

  it("refuses an update made against an old revision", async () => {
    const doc = await read();
    writeWorkbench(true);
    await expect(upd(doc, { changes: { enabled: false } })).rejects.toBeInstanceOf(ConfigurationError);
    expect(localStorage.getItem(WORKBENCH_KEY)).toBe("1");
  });

  it("names a value it doesn't recognize, and a browser that doesn't keep the write", async () => {
    localStorage.setItem(WORKBENCH_KEY, "nope");
    const doc = await read();
    expect(doc.notices).toEqual({ enabled: { kind: "warning", message: t("settings-labs.invalid") } });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {});
    await expect(upd(doc, { changes: { enabled: true } })).rejects.toMatchObject({ code: "storage_failed" });
  });
});
