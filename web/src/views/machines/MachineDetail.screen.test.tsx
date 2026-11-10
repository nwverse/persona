// @vitest-environment jsdom
// Machine detail, live screen (#3247): one "View screen" link in the top bar while Screen Sharing answers, whatever Cua Driver's
// state; "Computer control" keeps no link of its own, and says how to turn Screen Sharing on while it is off.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NO_SCREEN_SHARING } from "../../lib/computer";
import type { Machine } from "../../lib/types";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  const kept = new Map<string, string>();
  g.localStorage = { getItem: (k: string) => kept.get(k) ?? null, setItem: (k: string, v: string) => void kept.set(k, v), removeItem: (k: string) => void kept.delete(k) };
});

const fake = vi.hoisted(() => ({ machines: [] as Machine[] }));
vi.mock("../../lib/store", async (importOriginal) => {
  const real = await importOriginal<typeof import("../../lib/store")>();
  return { ...real, useStore: <T,>(select: (s: ReturnType<typeof real.getState>) => T) => select({ ...real.getState(), loaded: true, machines: fake.machines }) };
});

const { MachineDetail } = await import("./MachineDetail");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};
const screenLinks = () => [...host.querySelectorAll<HTMLAnchorElement>('a[href="/screen/mac"]')];
const section = () => host.querySelector('[data-testid="computer-section"]')!;

// What the node reports while Cua Driver's daemon is not running (computer.status: permissions unknown, the hub's note).
const NOTE = "Cua Driver 没在运行，助手要用时会自动打开它"; // i18n-ignore: the hub's note, shown as it comes
const daemonDown = { enabled: true, installed: true, daemon: false, ready: false, accessibility: "unknown", screen_recording: "unknown", note: NOTE };
const mac = (computer: Machine["computer"], extra: Partial<Machine> = {}) => ({ id: "mac", name: "Mac", online: true, os: "darwin", computer, ...extra }) as Machine;

async function open(m: Machine) {
  fake.machines = [m];
  act(() => root.render(<MachineDetail mid="mac" onToggleSidebar={() => {}} />));
  await flush();
}

beforeEach(() => {
  // every read fails: the page keeps the machine list's copy
  vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response(JSON.stringify({ error: "not found" }), { status: 404 }))));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

describe("MachineDetail live screen", () => {
  it("offers the screen in the top bar while Cua Driver is not running", async () => {
    await open(mac({ ...daemonDown, screen_sharing: true }));
    expect(section().textContent).toContain(NOTE);
    const links = screenLinks();
    expect(links).toHaveLength(1);
    expect(links[0]!.closest("header")).not.toBeNull();
    expect(links[0]!.textContent).toBe("查看屏幕");
    expect(links[0]!.target).toBe("_blank");
    expect(section().textContent).not.toContain(NO_SCREEN_SHARING);
  });

  it("keeps the one entry when Cua Driver is ready: the section has no VNC button", async () => {
    await open(mac({ enabled: true, installed: true, daemon: true, ready: true, accessibility: "granted", screen_recording: "granted", screen_sharing: true }));
    expect(screenLinks()).toHaveLength(1);
    expect(section().textContent).not.toContain("VNC");
  });

  it("says how to turn on Screen Sharing while it is off, with no link", async () => {
    await open(mac({ ...daemonDown, screen_sharing: false }));
    expect(screenLinks()).toHaveLength(0);
    expect(section().textContent).toContain(NO_SCREEN_SHARING);
  });

  it("offers the screen through ego while Computer control is off", async () => {
    await open(mac({ ...daemonDown, enabled: false, screen_sharing: true }, { browser: { enabled: true, installed: true, set_up: true, ready: true, screen_sharing: true } }));
    expect(screenLinks()).toHaveLength(1);
  });

  it("offers nothing while the Mac is offline", async () => {
    await open(mac({ ...daemonDown, screen_sharing: true }, { online: false }));
    expect(screenLinks()).toHaveLength(0);
  });
});
