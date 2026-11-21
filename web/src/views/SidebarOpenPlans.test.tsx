// @vitest-environment jsdom
// Sidebar indicators for open pending task plans from scheduled routines and other Persona conversations (#12254).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { t } from "../i18n";
import { getFeedState, type TaskPlan } from "../lib/feed";
import { connect, refresh, reset } from "../lib/store";
import type { Session } from "../lib/types";
import { NavGrid, Sidebar, SidebarRail, useNavPages } from "./Sidebar";
import { PagesTally } from "./SidebarRail";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

class FakeSocket {
  static last: FakeSocket | null = null;
  readyState = 1;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  constructor() { FakeSocket.last = this; }
  send() {}
  close() { this.readyState = 3; }
}

const at = "2026-11-10T08:00:00Z";
const conv = (session_id: string, title: string, origin = "user"): Session => ({
  machine_id: "hub", session_id, title, cwd: "/persona", status: "idle", state: "idle", assistant: true, origin, updated_at: at,
});

const mainChat = conv("main", "Persona");
const routineChat = conv("routine-1", "Nightly audit", "task");
const sideChat = conv("side-1", "Release prep", "user");

const routinePlan: TaskPlan = {
  id: "plan-routine-sidebar",
  conversation: "routine-1",
  state: "open",
  created_at: at,
  tasks: [{ title: "Clean old artifacts", prompt: "Clean", cwd: "/code", machine: "hub", schedule: "sched-1", schedule_title: "Nightly audit" }],
};
const sidePlan: TaskPlan = {
  id: "plan-side-sidebar",
  conversation: "side-1",
  state: "open",
  created_at: "2026-11-10T08:05:00Z",
  tasks: [{ title: "Publish release", prompt: "Publish", cwd: "/code", machine: "hub" }],
};
const healthPlan: TaskPlan = {
  id: "plan-health-sidebar",
  conversation: "health-1",
  state: "open",
  space: "health",
  created_at: "2026-11-10T08:06:00Z",
  tasks: [{ title: "Private health task", prompt: "Check", cwd: "/code", machine: "hub" }],
};

let openPlansPayload: TaskPlan[];
let host: HTMLDivElement;
let root: Root;

const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};

function DrawerPages() {
  const { pages } = useNavPages();
  return <NavGrid pages={pages} activeKey="home" go={() => () => {}} />;
}

function FoldedPagesTally() {
  const { pages } = useNavPages();
  return <PagesTally pages={pages} />;
}

beforeEach(() => {
  localStorage.clear();
  openPlansPayload = [];
  vi.stubGlobal("WebSocket", FakeSocket);
  vi.stubGlobal("fetch", vi.fn((url: string) => {
    const path = new URL(url, "http://x").pathname;
    const body = path === "/api/v1/assistant"
      ? { machine_id: "hub", main: "main", conversations: [mainChat, routineChat, sideChat], open_plans: openPlansPayload }
      : path === "/api/v1/machines"
        ? { machines: [{ id: "hub", name: "Hub", online: true }] }
        : {};
    return Promise.resolve(new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } }));
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  for (const id of Object.keys(getFeedState().plans)) delete getFeedState().plans[id];
  vi.unstubAllGlobals();
});

describe("sidebar indicators for open pending plans (#12254)", () => {
  it("surfaces routine and side-chat pending plans across expanded sidebar, collapsed sections, rail, and drawer, and clears on resolution", async () => {
    openPlansPayload = [routinePlan];
    await act(async () => {
      connect();
      await refresh();
    });

    // Expanded sidebar: Scheduled tasks badge, Main chat badge, and All Persona chats badge (routine chat is not in Recent).
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();

    const routinesLink = host.querySelector<HTMLAnchorElement>('a[href="/routines"]')!;
    const expectedLabel = t("sidebar.open_plans_badge", { count: 1 });
    expect(routinesLink.title).toBe(expectedLabel);
    expect(routinesLink.textContent).toContain("1");
    expect(routinesLink.querySelector(".sr-only")?.textContent).toBe(expectedLabel);

    const mainRow = host.querySelector<HTMLAnchorElement>('a[title="Persona 的主对话"]')!;
    expect(mainRow.textContent).toContain("1");

    const allChatsLink = host.querySelector<HTMLAnchorElement>('a[href="/history"]')!;
    expect(allChatsLink.title).toBe(expectedLabel);
    expect([...allChatsLink.querySelectorAll(".sr-only")].map((el) => el.textContent)).toContain(expectedLabel);

    // Live task_plan event for a side conversation surfaces on that row, while health-space plans stay hidden.
    act(() => {
      FakeSocket.last!.onmessage?.({ data: JSON.stringify({ type: "task_plan", plan: sidePlan }) });
      FakeSocket.last!.onmessage?.({ data: JSON.stringify({ type: "task_plan", plan: healthPlan }) });
    });
    await flush();

    const sideRow = host.querySelector<HTMLAnchorElement>('a[href="/persona/side-1"]')!;
    expect(sideRow.querySelector('[data-state="needs_you"]')).not.toBeNull();
    expect(sideRow.textContent).toContain("1");
    expect(mainRow.textContent).toContain("2");
    // Scheduled tasks still counts only the 1 routine plan, not the side or health plan.
    expect(routinesLink.title).toBe(t("sidebar.open_plans_badge", { count: 1 }));

    // Collapsed Persona chats section still surfaces the combined non-main open plans count (routine + side = 2).
    localStorage.setItem("fold:persona", "1");
    act(() => root.render(<Sidebar key="folded" route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    const foldedBadge = host.querySelector<HTMLElement>("[data-persona-plans-badge]")!;
    expect(foldedBadge).not.toBeNull();
    expect(foldedBadge.title).toBe(t("sidebar.open_plans_badge", { count: 2 }));
    expect(foldedBadge.querySelector("[aria-hidden]")?.textContent).toBe("2");

    // Collapsed Pages tally, SidebarRail, and mobile NavGrid all surface the Scheduled tasks badge/dot.
    act(() => root.render(<FoldedPagesTally />));
    await flush();
    expect(host.querySelector(".sr-only")?.textContent).toContain(expectedLabel);

    act(() => root.render(<SidebarRail route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    expect(host.querySelector<HTMLAnchorElement>('a[href="/routines"]')!.getAttribute("aria-label")).toContain(expectedLabel);
    expect(host.querySelector<HTMLAnchorElement>('a[href="/"]')!.getAttribute("aria-label")).toBe("Persona 2");

    act(() => root.render(<DrawerPages />));
    await flush();
    expect(host.querySelector<HTMLAnchorElement>('a[href="/routines"]')!.getAttribute("aria-label")).toContain(expectedLabel);

    // Resolving both plans via live events clears all badges cleanly (both folded and expanded).
    act(() => {
      FakeSocket.last!.onmessage?.({ data: JSON.stringify({ type: "task_plan", plan: { ...routinePlan, state: "started" } }) });
      FakeSocket.last!.onmessage?.({ data: JSON.stringify({ type: "task_plan", plan: { ...sidePlan, state: "dismissed" } }) });
    });
    act(() => root.render(<Sidebar key="cleared-folded" route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    expect(host.querySelector("[data-persona-plans-badge]")).toBeNull();
    localStorage.removeItem("fold:persona");
    act(() => root.render(<Sidebar key="cleared" route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    expect(host.querySelector("[data-persona-plans-badge]")).toBeNull();
    expect(host.querySelector<HTMLAnchorElement>('a[href="/routines"]')!.title).toBe("");
    expect(host.querySelector<HTMLAnchorElement>('a[href="/routines"]')!.textContent).toBe(t("sidebar.nav_routines"));
    expect(host.querySelector<HTMLAnchorElement>('a[href="/history"]')!.title).toBe("");
  });

  it("shows a badge on collapsed Recent subheading when a recent conversation has an open plan", async () => {
    localStorage.setItem("fold:recent-chats", "1");
    openPlansPayload = [sidePlan];
    await act(async () => {
      await refresh();
    });
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    const subBadge = host.querySelector<HTMLElement>("[data-subheading-badge]")!;
    expect(subBadge).not.toBeNull();
    expect(subBadge.querySelector("[aria-hidden]")?.textContent).toBe("1");
    expect(subBadge.title).toBe(t("sidebar.open_plans_badge", { count: 1 }));
  });
});
