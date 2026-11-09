// @vitest-environment jsdom
// "New agent"'s presets (#2225): the tiers of the model profile in use this computer can start. Picking one fills the
// launcher, model and effort as picking them by hand would (the model survives the new launcher's list loading); a pick
// by hand unmarks it; what the launcher can't take is said, not silently dropped.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Machine } from "../lib/types";

const state = vi.hoisted(() => ({ loaded: true, features: [] as string[], machines: [] as Machine[] }));
vi.mock("../lib/store", () => ({
  getState: () => state,
  useStore: (selector: (value: typeof state) => unknown) => selector(state),
  hubHas: (features: string[], feature: string, unknown = false) => (features.length ? features.includes(feature) : unknown),
}));
vi.mock("react-dom", async (original) => ({ ...(await original<typeof import("react-dom")>()), createPortal: (children: React.ReactNode) => children }));
const launched = vi.hoisted(() => [] as Record<string, unknown>[]);
vi.mock("../lib/new-agent", async (original) => ({ ...(await original<typeof import("../lib/new-agent")>()), launchAgent: (input: Record<string, unknown>) => { launched.push(input); return "local-1"; } }));

import { NewAgent } from "./NewAgent";
import { Toasts } from "../ui/atoms";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const TIERS = [
  { name: "quick", launcher: "claude" },
  { name: "review", launcher: "fast", model: "m-review", effort: "high", description: "代码审查和设计" },
];
let tiers: unknown = TIERS;
let fastList: unknown = { agent: "claude", models: [{ id: "default", name: "Default", default: true }, { id: "m-review", name: "Review", efforts: ["low", "high"] }] };
let host: HTMLDivElement;
let root: Root;
const flush = () => act(async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); });
const button = (text: string) => [...document.body.querySelectorAll("button")].find((b) => b.textContent?.trim() === text)!;
const hint = () => document.body.querySelector("[data-testid=new-agent-preset-hint]");

beforeEach(() => {
  state.machines = [{ id: "mac", name: "Mac", online: true }];
  launched.length = 0;
  tiers = TIERS;
  fastList = { agent: "claude", models: [{ id: "default", name: "Default", default: true }, { id: "m-review", name: "Review", efforts: ["low", "high"] }] };
  localStorage.clear();
  localStorage.setItem("new.cwd.mac", "/code/app");
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes("/model-config/purposes/")) return json({ error: "not found" }, 404);
    if (url.includes("/launchers/fast/models")) return json(fastList);
    if (url.includes("/launchers/") && url.includes("/models")) return json({ agent: "claude", models: [{ id: "default", name: "Default", default: true }] });
    if (url.includes("/launchers")) {
      return json({ launchers: [{ name: "claude", agent: "claude" }, { name: "fast", agent: "claude" }], tiers, tiers_profile: tiers ? { id: "performance", name: "性能" } : undefined });
    }
    return json({ path: "/code", entries: [] });
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

async function open() {
  act(() => root.render(<><NewAgent open onClose={() => {}} /><Toasts /></>));
  await flush();
}

describe("New agent: presets (#2225)", () => {
  it("fills launcher, model and effort from a preset and starts with them", async () => {
    await open();
    expect(hint()?.textContent).toContain("性能");
    act(() => button("review").click());
    await flush();
    expect(button("review").getAttribute("aria-checked")).toBe("true");
    expect(hint()?.textContent).toBe("代码审查和设计");
    act(() => button("启动").click());
    expect(launched[0]).toMatchObject({ machineId: "mac", launcher: "fast", model: "m-review", effort: "high" });
  });

  it("unmarks the preset when something is picked by hand", async () => {
    await open();
    act(() => button("review").click());
    await flush();
    act(() => button("claude").click());
    await flush();
    expect(button("review").getAttribute("aria-checked")).toBe("false");
    act(() => button("启动").click());
    expect(launched[0]).toMatchObject({ launcher: "claude" });
    expect(launched[0]?.model).toBeUndefined();
  });

  it("says what a launcher fixing its own model can't take, and sends none of it", async () => {
    fastList = { agent: "claude", models: [], locked: "启动器固定了模型参数" };
    await open();
    act(() => button("review").click());
    await flush();
    expect(hint()?.textContent).toContain("m-review");
    act(() => button("启动").click());
    expect(launched[0]).toMatchObject({ launcher: "fast" });
    expect(launched[0]?.model).toBeUndefined();
  });

  it("has no preset row when the hub offers none", async () => {
    tiers = undefined;
    await open();
    expect(hint()).toBeNull();
    expect(document.body.textContent).not.toContain("预设");
  });
});
