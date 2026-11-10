// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import { setToken } from "../../lib/api";
import { getSession, reset, upsertSession } from "../../lib/store";
import type { Session } from "../../lib/types";
import { ModeChips } from "./SessionDialogs";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const session: Session = {
  machine_id: "m", session_id: "example", title: "Example", cwd: "", status: "working", agent: "codex",
  capabilities: { send: true }, efforts: ["low", "high"], effort: "high",
  service_tiers: [{ id: "default", name: "Standard" }, { id: "priority", name: "Fast" }],
};
let root: Root;
let host: HTMLDivElement;
const fetcher = vi.fn();

beforeEach(async () => {
  await initI18n("en");
  setToken("synthetic-speed");
  reset();
  upsertSession(session);
  fetcher.mockReset().mockResolvedValue(new Response("{}", { status: 200 }));
  vi.stubGlobal("fetch", fetcher);
  vi.stubGlobal("matchMedia", (query: string) => ({ matches: false, media: query, addEventListener() {}, removeEventListener() {} }));
  const css = ((globalThis as { CSS?: Partial<typeof CSS> }).CSS ??= {});
  css.escape ??= (s: string) => s;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  await act(async () => root.render(<ModeChips session={session} effortOnly />));
});

afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

async function openSpeed() {
  await act(async () => host.querySelector<HTMLButtonElement>("button")!.click());
  const row = [...document.querySelectorAll<HTMLElement>("[role=menuitem]")].find((e) => e.textContent?.startsWith("Speed"));
  expect(row).toBeDefined();
  await act(async () => row!.click());
  await act(async () => vi.dynamicImportSettled());
}

it("offers native speeds independently of effort and waits for a native session push", async () => {
  await openSpeed();
  const fast = [...document.querySelectorAll<HTMLElement>("[role=menuitemradio]")].find((e) => e.textContent?.startsWith("Fast"));
  expect(fast?.getAttribute("aria-checked")).toBe("false");
  expect(fast?.textContent).toContain("increased usage or cost");
  await act(async () => fast!.click());
  expect(fetcher).toHaveBeenCalledWith(
    expect.stringContaining("/sessions/m/example/modes"),
    expect.objectContaining({ method: "POST", body: JSON.stringify({ service_tier: "priority" }) }),
  );
  const saved = getSession("m", "example");
  expect(saved?.service_tier).toBeUndefined();
  expect(saved?.effort).toBe("high");
});

it("offers Standard as the reversible choice when Fast is active", async () => {
  await act(async () => root.render(<ModeChips session={{ ...session, service_tier: "priority" }} effortOnly />));
  await openSpeed();
  const choices = [...document.querySelectorAll<HTMLElement>("[role=menuitemradio]")];
  expect(choices.find((e) => e.textContent?.startsWith("Fast"))?.getAttribute("aria-checked")).toBe("true");
  await act(async () => choices.find((e) => e.textContent?.startsWith("Standard"))!.click());
  expect(fetcher).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ body: JSON.stringify({ service_tier: "default" }) }));
});
