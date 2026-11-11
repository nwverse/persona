// @vitest-environment jsdom
// A 常驻 row's disclosure (#4352): each leader row (#4610's, with "Doing N · Needs you N") folds open to the tasks the
// hub's direction tree lists under it, each task to its agents and theirs; folded by default, kept per leader. The
// secretary's group has no row of its own (the main chat's row is it). A hub without the tree keeps the plain rows.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { t } from "../i18n";
import { resetDirectionNames } from "../lib/direction-names";
import { resetDirectionTree, type DirectionTree } from "../lib/direction-tree";
import { refresh, reset } from "../lib/store";
import type { Session } from "../lib/types";
import { Sidebar } from "./Sidebar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const conversation = (session_id: string, title: string): Session => ({
  machine_id: "hub", session_id, title, cwd: "/persona", status: "idle", state: "idle", assistant: true, origin: "user", updated_at: "2026-11-10T09:00:00Z",
});
const counts = (working: number, needs_you: number) => ({ working, needs_you, stuck: 0, review: 0 });
const TREE: DirectionTree = {
  directions: [
    {
      id: "lead-a",
      name: "Product",
      counts: counts(1, 1),
      tasks: [
        { id: "t-ask", title: "Pick the icon", state: "needs_you", agents: [{ machine_id: "mac", session_id: "s1", title: "Icon work", state: "needs_you", children: [{ machine_id: "mac", session_id: "s2", title: "Sub agent", state: "working" }] }] },
        { id: "t-run", title: "Write tests", state: "working", agents: [] },
      ],
    },
    { id: "lead-b", name: "Infrastructure", counts: counts(0, 0), tasks: [] },
    { id: "main", name: "", secretary: true, counts: counts(1, 0), tasks: [{ id: "t-own", title: "Own task", state: "stuck", agents: [] }] },
  ],
};
let leaders: string[] | undefined;
let tree: DirectionTree | undefined;

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};
const direction = (id: string) => host.querySelector<HTMLElement>(`[data-direction="${id}"]`);
const disclosure = (id: string) => direction(id)!.querySelector<HTMLButtonElement>("button[aria-expanded]")!;
const taskLinks = () => [...host.querySelectorAll<HTMLAnchorElement>('a[href^="/todos?id="]')].map((a) => a.textContent);

beforeEach(() => {
  localStorage.clear();
  leaders = ["lead-a", "lead-b"];
  tree = TREE;
  vi.stubGlobal("fetch", vi.fn((url: string) => {
    const path = new URL(url, "http://x").pathname;
    if (path === "/api/v1/assistant/directions/tree" && !tree) return Promise.resolve(new Response(JSON.stringify({ error: "not found" }), { status: 404, headers: { "Content-Type": "application/json" } }));
    const body = path === "/api/v1/assistant"
      ? { machine_id: "hub", main: "main", leaders, conversations: [conversation("main", "Persona"), conversation("lead-a", "Growth"), conversation("lead-b", "Payments"), conversation("other", "Trip plan")] }
      : path === "/api/v1/assistant/directions/tree" ? tree
      : path === "/api/v1/assistant/leaders" ? { leaders: [{ id: "lead-a", name: "Product" }, { id: "lead-b", name: "Infrastructure" }] }
      : path === "/api/v1/machines" ? { machines: [{ id: "hub", name: "Hub", online: true }] } : {};
    return Promise.resolve(new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } }));
  }));
  history.replaceState(null, "", "/overview");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  resetDirectionNames();
  resetDirectionTree();
  vi.unstubAllGlobals();
});

async function renderSidebar() {
  await act(() => refresh());
  act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
  await flush();
}

describe("the 常驻 rows' disclosure", () => {
  it("starts folded on #4610's row, with its name and its two counts, and no row for the secretary", async () => {
    await renderSidebar();
    const rows = [...host.querySelectorAll<HTMLElement>("[data-direction]")].map((el) => el.dataset.direction);
    expect(rows).toEqual(["lead-a", "lead-b"]);
    const lead = direction("lead-a")!;
    expect(lead.querySelector('a[href="/persona/lead-a"]')!.textContent).toContain("Product");
    expect(lead.querySelector('a[href="/persona/lead-a"]')!.textContent).toContain("在做 1 · 等你 1");
    expect(host.textContent).not.toContain("书记官");
    expect(disclosure("lead-a").getAttribute("aria-expanded")).toBe("false");
    expect(disclosure("lead-a").getAttribute("aria-label")).toBe(t("sidebar.direction_tasks", { name: "Product" }));
    // a direction with no task has nothing to open
    expect(disclosure("lead-b").disabled).toBe(true);
    expect(host.querySelector("[data-direction] [inert] a[href^='/todos']")).not.toBeNull();
  });

  it("opens to the tasks with their state words, each task to its agents and theirs; the fold is kept", async () => {
    await renderSidebar();
    act(() => disclosure("lead-a").click());
    await flush();
    expect(disclosure("lead-a").getAttribute("aria-expanded")).toBe("true");
    expect(localStorage.getItem("fold:direction:lead-a")).toBe("0");
    const open = direction("lead-a")!;
    expect([...open.querySelectorAll('a[href^="/todos?id="]')].map((a) => a.getAttribute("href"))).toEqual(["/todos?id=t-ask", "/todos?id=t-run"]);
    expect([...open.querySelectorAll("[data-task-state]")].map((el) => el.textContent)).toEqual(["等你", "在做"]);
    const task = open.querySelector<HTMLButtonElement>('button[aria-label="Pick the icon"]')!;
    expect(task.getAttribute("aria-expanded")).toBe("false");
    act(() => task.click());
    await flush();
    expect(open.querySelector('a[href="/s/mac/s1"]')!.textContent).toContain("Icon work");
    const sub = open.querySelector<HTMLButtonElement>(`button[aria-label="${t("lib-todo-tree.spawned_agents", { count: 1 })}"]`)!;
    expect(sub.getAttribute("aria-expanded")).not.toBeNull();
    expect(open.querySelector('a[href="/s/mac/s2"]')).not.toBeNull();
  });

  it("is not there on a hub without the tree: the plain rows of #4610", async () => {
    tree = undefined;
    await renderSidebar();
    expect(host.querySelector("[data-direction]")).toBeNull();
    expect(host.querySelector('a[href="/persona/lead-a"]')!.textContent).toContain("Product");
    expect(taskLinks()).toEqual([]);
  });

  it("leaves the sidebar as it was with no leader, the secretary's tasks and all", async () => {
    leaders = undefined;
    tree = { directions: [TREE.directions[2]] };
    await renderSidebar();
    expect(host.querySelector("[data-direction]")).toBeNull();
    expect(host.textContent).not.toContain("书记官");
    expect(taskLinks()).toEqual([]);
  });
});
