// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { beforeEach, afterEach, expect, it, vi } from "vitest";
vi.hoisted(() => {
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, addEventListener() {}, removeEventListener() {} })) as typeof window.matchMedia;
  // a long group is drawn a part at a time only where the browser has both observers (WindowedRows)
  const inert = class { observe() {} unobserve() {} disconnect() {} };
  globalThis.ResizeObserver ??= inert as unknown as typeof ResizeObserver;
  globalThis.IntersectionObserver ??= inert as unknown as typeof IntersectionObserver;
});
import type { Session } from "../lib/types";
import { setAllFolded } from "../lib/sidebar-api";
import { FoldScope } from "../lib/use-folded";
import { nestAgents, type TreeTodo } from "../lib/todo-tree";
import { buildMachineTree } from "../lib/herdr-tree";
import { href } from "../lib/router";
import { AgentTreeRows, MachineSection, TreeTodoRows } from "./SidebarTree";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement;
let root: Root;
const session = (id: string, patch: Partial<Session> = {}): Session => ({ machine_id: "demo", session_id: id, title: id, cwd: "/code/app", status: "idle", state: "idle", updated_at: "2026-11-11T12:00:00Z", host: "orca", pane_id: `orca:tab:${id}`, ...patch });
const sessions = [session("parent"), session("worker", { parent_agent_id: "parent" }), session("nested", { parent_agent_id: "worker", state: "needs_you", status: "blocked" })];
const show = (activeKey = "") => act(() => root.render(<FoldScope value="agents"><AgentTreeRows sessions={sessions} activeKey={activeKey} scope="demo-group" row={(s) => <a href={`/s/${s.machine_id}/${s.session_id}`}>{s.title}</a>} /></FoldScope>));
beforeEach(() => { localStorage.clear(); host = document.createElement("div"); document.body.append(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); });

it("shows each original session once at its reported depth and leaves its link intact", () => {
  show();
  const parent = host.querySelector('[data-agent-parent="s:demo/parent"]')!;
  const worker = parent.querySelector('[data-agent-parent="s:demo/worker"]')!;
  expect(worker.querySelector('a[href="/s/demo/nested"]')).not.toBeNull();
  expect(host.querySelectorAll("a")).toHaveLength(3);
  expect(parent.querySelector("button")!.getAttribute("aria-expanded")).toBe("true");
});

it("folds repeatedly, counts hidden attention, follows fold-all, and reveals an opened descendant", () => {
  show();
  const parent = host.querySelector('[data-agent-parent="s:demo/parent"]')!;
  const button = parent.querySelector("button")!;
  act(() => button.click());
  expect(button.getAttribute("aria-expanded")).toBe("false");
  expect(parent.textContent).toContain("1");
  expect(parent.querySelector("[inert]")).not.toBeNull();
  act(() => button.click());
  expect(button.getAttribute("aria-expanded")).toBe("true");
  act(() => setAllFolded(true, "agents"));
  expect([...host.querySelectorAll("button[aria-expanded]")].every((b) => b.getAttribute("aria-expanded") === "false")).toBe(true);
  show("s:demo/nested");
  expect([...host.querySelectorAll("button[aria-expanded]")].every((b) => b.getAttribute("aria-expanded") === "true")).toBe(true);
  act(() => setAllFolded(true, "agents"));
  act(() => setAllFolded(false, "agents"));
  expect(button.getAttribute("aria-expanded")).toBe("true");
});

it("keeps a pinned direction's row keys, context and nested folds separate from the Agents tree", () => {
  const registered = new Set<string>();
  const agents = nestAgents(sessions, undefined, Date.now());
  const td: TreeTodo = { key: "direction-task", name: "Example task", state: "needs_you", agents, sessions, folded: false };
  const render = () => act(() => root.render(<>
    <section data-copy="agents"><AgentTreeRows sessions={sessions} activeKey="" scope="demo-group" row={(s) => <a href={`/s/${s.machine_id}/${s.session_id}`}>{s.title}</a>} /></section>
    <section data-copy="direction"><TreeTodoRows td={td} machineLabel={() => "Example computer"} noContext keyPrefix="d:example:" reg={(key) => { registered.add(key); return () => {}; }} hover={() => {}} activeKey="" go={() => (event) => event.preventDefault()} /></section>
  </>));
  render();
  const copy = host.querySelector('[data-copy="direction"]')!;
  const direction = copy.querySelector('[data-agent-parent="s:demo/parent"] > div > button')!;
  const list = host.querySelector('[data-copy="agents"] [data-agent-parent="s:demo/parent"] > div > button')!;
  expect(registered).toEqual(new Set(sessions.map((s) => `d:example:s:demo/${s.session_id}`)));
  expect(copy.textContent).not.toContain("Example computer");
  expect(copy.querySelectorAll('a[href$="/nested"]')).toHaveLength(1);
  act(() => (direction as HTMLButtonElement).click());
  expect(direction.getAttribute("aria-expanded")).toBe("false");
  expect(list.getAttribute("aria-expanded")).toBe("true");
  expect(localStorage.getItem(`fold:d:example:${agents[0].key}:children`)).toBe("1");
  act(() => root.render(null));
  render();
  expect(host.querySelector('[data-copy="direction"] [data-agent-parent="s:demo/parent"] > div > button')!.getAttribute("aria-expanded")).toBe("false");
  expect(host.querySelector('[data-copy="agents"] [data-agent-parent="s:demo/parent"] > div > button')!.getAttribute("aria-expanded")).toBe("true");
});

it("draws a long group a part at a time by its top-level agents, with the open worker's parent drawn", () => {
  const many = Array.from({ length: 300 }, (_, i) => session(`agent-${i}`));
  const long = [...many, session("late-worker", { parent_agent_id: "agent-250" })];
  act(() => root.render(<FoldScope value="agents"><AgentTreeRows sessions={long} activeKey="s:demo/late-worker" scope="long-group" row={(s) => <a href={`/s/${s.machine_id}/${s.session_id}`}>{s.title}</a>} /></FoldScope>));
  const parent = host.querySelector('[data-agent-parent="s:demo/agent-250"]')!;
  expect(parent.querySelector('a[href="/s/demo/late-worker"]')).not.toBeNull();
  expect(parent.querySelector("button")!.getAttribute("aria-expanded")).toBe("true");
  // 300 top-level agents, not 301 rows: the worker is under its parent, and most parts are not drawn
  expect(host.querySelectorAll("a").length).toBeLessThan(long.length);
  expect(host.querySelectorAll('[data-agent-parent]')).toHaveLength(1);
});

it("keeps every Orca tab row, nesting a worker's tab under its coordinator's tab", () => {
  const machines = [{ id: "demo", name: "Example computer", online: true }];
  const pane = (id: string, slot: string, index: number, patch: Partial<Session> = {}) => session(id, { space_kind: "orca", space_id: "w1", space_name: "app", space_index: 0, slot_id: slot, slot_index: index, slot_name: slot, tab_id: slot, ...patch });
  const list = [
    pane("coordinator", "t0", 0),
    pane("worker", "t1", 1, { parent_agent_id: "coordinator" }),
    pane("plain-a", "t2", 2, { pane_index: 0 }),
    pane("plain-b", "t2", 2, { pane_index: 1 }),
  ];
  const [m] = buildMachineTree(machines, list);
  const sp = m!.spaces[0]!;
  expect(sp.tabs.map((t) => t.slotId)).toEqual(["t0", "t1", "t2"]);
  localStorage.setItem(`fold:${sp.foldKey}`, "0");
  act(() => root.render(<FoldScope value="agents"><MachineSection m={m!} machines={machines} activeKey="" mutes={{ spaces: [], sessions: [], allowed: [] }} onSpaceMenu={() => {}} onNodeMenu={() => {}} reg={() => () => {}} hover={() => {}} go={() => () => {}} onRowMenu={() => {}} /></FoldScope>));
  const tabLink = (slot: string) => host.querySelector(`a[href="${href({ name: "tab", mid: "demo", space: "w1", slot })}"]`);
  // every tab keeps its own row and route, the unrelated two-pane tab its "2 agents" fold and menu
  for (const slot of ["t0", "t1", "t2"]) expect(tabLink(slot), slot).not.toBeNull();
  const paneFold = [...host.querySelectorAll("button[aria-expanded]")].find((b) => b.getAttribute("aria-label")?.includes("t2"));
  expect(paneFold?.textContent).toContain("2");
  expect(host.querySelectorAll('a[href$="/plain-a"], a[href$="/plain-b"]')).toHaveLength(2);
  // the worker's tab sits under its coordinator's tab, behind the coordinator's own disclosure
  const coordinator = host.querySelector(`[data-agent-parent="t:${sp.tabs[0]!.key}"]`)!;
  expect(coordinator).not.toBeNull();
  expect(coordinator.contains(tabLink("t1"))).toBe(true);
  expect(coordinator.contains(tabLink("t2"))).toBe(false);
});
