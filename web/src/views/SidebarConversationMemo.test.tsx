// @vitest-environment jsdom
// The Persona conversation rows (the main row, the pinned and recent ones) keep their memo across a push that leaves
// their conversation as it was: the menu they share is one function for the sidebar's life. The menu still opens on
// the conversation as it is now.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
// every render of a SessionRow and of the main row reads its draft once: counted per "machine/session"
const draftReads = vi.hoisted(() => new Map<string, number>());
vi.mock("../lib/drafts", async (load) => {
  const real = await load<typeof import("../lib/drafts")>();
  return {
    ...real,
    useLeftDraft: (key: string | null) => {
      if (key) draftReads.set(key, (draftReads.get(key) ?? 0) + 1);
      return real.useLeftDraft(key);
    },
  };
});
import { t } from "../i18n";
import { getState, refresh, reset, upsertSession } from "../lib/store";
import type { Session } from "../lib/types";
import { Sidebar } from "./Sidebar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const conversation = (session_id: string, title: string): Session => ({
  machine_id: "hub", session_id, title, cwd: "/persona", status: "idle", state: "idle", assistant: true, origin: "user", updated_at: "2026-05-01T10:00:00Z",
});
const main = conversation("main", "Persona");
const pinned = conversation("pinned", "Release notes");
const recent = conversation("recent", "Trip plan");
const agent: Session = { machine_id: "mac", session_id: "agent", title: "Build", cwd: "/code", status: "working", state: "working" };
const answers: Record<string, unknown> = {
  "/api/v1/assistant": { machine_id: "hub", online: true, main: "main", conversations: [main, pinned, recent] },
  "/api/v1/sessions": { sessions: [main, pinned, recent, agent] },
  "/api/v1/machines": { machines: [{ id: "hub", name: "Hub", online: true }, { id: "mac", name: "MacBook", online: true }] },
};

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};
const reads = (sid: string) => draftReads.get(sid) ?? 0;
const mainRow = () => host.querySelector<HTMLAnchorElement>(`a[title="${t("sidebar.persona_main")}"]`)!;
const row = (sid: string) => host.querySelector<HTMLAnchorElement>(`a[href="/persona/${sid}"]`)!;
const menu = () => document.querySelector('[role="menu"]');
const push = (mid: string, sid: string, change: Partial<Session>) => {
  act(() => upsertSession({ ...getState().sessions[mid].find((s) => s.session_id === sid)!, ...change }));
};

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("assistant.pinned", '["pinned"]');
  draftReads.clear();
  vi.stubGlobal("fetch", vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(answers[new URL(url, "http://x").pathname] ?? {}), { headers: { "Content-Type": "application/json" } }))));
  history.replaceState(null, "", "/overview");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.unstubAllGlobals();
});

async function renderSidebar() {
  await act(() => refresh());
  act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
  await flush();
  expect(mainRow()).toBeTruthy();
  expect(row("pinned")).toBeTruthy();
  expect(row("recent")).toBeTruthy();
  expect(reads("hub/main")).toBeGreaterThan(0);
  expect(reads("hub/pinned")).toBeGreaterThan(0);
  draftReads.clear();
}

describe("the Persona conversation rows", () => {
  it("keep their render across pushes for an agent", async () => {
    await renderSidebar();
    for (let i = 1; i <= 10; i++) {
      push("mac", "agent", { title: `Build ${i}` });
      await flush();
    }
    expect(host.textContent).toContain("Build 10");
    expect(reads("mac/agent")).toBeGreaterThanOrEqual(10);
    expect(reads("hub/main")).toBe(0);
    expect(reads("hub/pinned")).toBe(0);
    expect(reads("hub/recent")).toBe(0);
  });

  it("render again only for their own conversation", async () => {
    await renderSidebar();
    push("hub", "recent", { title: "Trip plan, day 2" });
    await flush();
    expect(row("recent").textContent).toContain("Trip plan, day 2");
    expect(reads("hub/recent")).toBeGreaterThan(0);
    expect(reads("hub/pinned")).toBe(0);
    expect(reads("hub/main")).toBe(0);

    push("hub", "main", { status: "working", state: "working" });
    await flush();
    expect(mainRow().querySelector('[data-state="working"]')).not.toBeNull();
    expect(reads("hub/main")).toBeGreaterThan(0);
    expect(reads("hub/pinned")).toBe(0);
  });

  it("open the conversation's menu from the ⋯ and on a right click, after pushes too", async () => {
    await renderSidebar();
    push("mac", "agent", { title: "Build 1" });
    push("hub", "pinned", { title: "Release notes v2" });
    await flush();

    // the main row's ⋯: the main conversation's menu (no pin, rename or archive), and closed again on a second click
    const more = mainRow().parentElement!.querySelector<HTMLButtonElement>(`button[aria-label="${t("conversation-menu.actions")}"]`)!;
    act(() => more.click());
    await flush();
    expect(menu()?.textContent).toContain(t("conversation-menu.copy_id"));
    expect(menu()?.textContent).not.toContain(t("conversation-menu.pin"));
    act(() => more.click());
    await flush();
    expect(menu()).toBeNull();

    // a right click on the pinned row: that conversation's menu, as it is now (pinned)
    act(() => row("pinned").dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 10, clientY: 10 })));
    await flush();
    expect(menu()?.textContent).toContain(t("conversation-menu.unpin"));
    expect(menu()?.textContent).toContain(t("conversation-menu.rename"));
  });
});
