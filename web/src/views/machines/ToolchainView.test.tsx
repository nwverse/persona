// @vitest-environment jsdom
// Regression test for review #6004294744 (comment 4651711398): the Toolchain view is strictly opt-in on click,
// never persisted in localStorage, and never auto-restored on subsequent visits to Machines.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import type { Machine } from "../../lib/types";

const storage = vi.hoisted(() => new Map<string, string>());
vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  g.localStorage = {
    getItem: (k: string) => storage.get(k) ?? null,
    setItem: (k: string, v: string) => void storage.set(k, v),
    removeItem: (k: string) => void storage.delete(k),
  };
});

const fake = vi.hoisted(() => ({ machines: [] as Machine[], features: [] as string[] }));
vi.mock("../../lib/store", async (importOriginal) => {
  const real = await importOriginal<typeof import("../../lib/store")>();
  return {
    ...real,
    useStore: <T,>(select: (s: ReturnType<typeof real.getState>) => T) =>
      select({ ...real.getState(), loaded: true, machines: fake.machines, features: fake.features }),
  };
});

const { Machines } = await import("../Machines");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const json = (v: unknown, status = 200) =>
  new Response(JSON.stringify(v), { status, headers: { "Content-Type": "application/json" } });

let host: HTMLDivElement;
let root: Root;

const flush = async () => {
  for (let i = 0; i < 8; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
};

beforeEach(async () => {
  await initI18n("en");
  storage.clear();
  fake.machines = [{ id: "dev-box", name: "Dev Box", online: true }];
  fake.features = [];
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("Machines toolchain view opt-in persistence", () => {
  it("never restores toolchain from storage and does not persist it when selected", async () => {
    const requested: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        requested.push(url);
        if (url.includes("/toolchain")) {
          return Promise.resolve(
            json({
              collected_at: "2026-11-21T17:00:00Z",
              cached: false,
              cache_ttl_sec: 60,
              duration_ms: 12,
              os: "linux",
              arch: "amd64",
              tools: [],
              agent_clis: [],
              environment: {},
            }),
          );
        }
        if (url.includes("/metrics")) return Promise.resolve(json({ machines: [] }));
        if (url.includes("/launchers/health")) return Promise.resolve(json({ records: [] }));
        return Promise.resolve(json({ ok: true }));
      }),
    );

    // Pre-seed storage with "toolchain": Machines must ignore it and default to Cards without probing.
    storage.set("persona.machines.view", "toolchain");

    act(() => root.render(<Machines onToggleSidebar={() => {}} />));
    await flush();

    expect(requested.some((u) => u.includes("/toolchain"))).toBe(false);
    expect(host.querySelector('[data-testid="toolchain-panel"]')).toBeNull();

    // Selecting Capabilities persists "capabilities".
    const capBtn = [...host.querySelectorAll("button")].find((b) => b.textContent?.trim() === "Capabilities");
    expect(capBtn).toBeTruthy();
    act(() => capBtn!.click());
    await flush();
    expect(storage.get("persona.machines.view")).toBe("capabilities");

    // Clicking Toolchain triggers the probe on demand, but leaves storage at "capabilities".
    const tcBtn = [...host.querySelectorAll("button")].find((b) => b.textContent?.trim() === "Toolchain");
    expect(tcBtn).toBeTruthy();
    act(() => tcBtn!.click());
    await flush();

    expect(requested.some((u) => u.includes("/api/v1/machines/dev-box/toolchain"))).toBe(true);
    expect(storage.get("persona.machines.view")).toBe("capabilities");
  });
});
