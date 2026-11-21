// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((media: string) => ({ media, matches: false, addEventListener() {}, removeEventListener() {} })) as typeof window.matchMedia;
});
import { t } from "../i18n";
import { loadPages } from "../lib/lazy-page";
import { setSidebarTerminals } from "../lib/sidebar-terminals";
import { connect, reset } from "../lib/store";
import { REFRESH_MS } from "../lib/terminal-panes";
import type { PaneDTO, PanesResponse } from "../lib/terminals";
import type { Session } from "../lib/types";
import { Sidebar } from "./Sidebar";
import { HistoryBrowser } from "./agents/HistoryBrowser";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class Socket {
  static last: Socket;
  static OPEN = 1;
  readyState = 1;
  onopen?: () => void;
  onmessage?: (event: { data: string }) => void;
  constructor() { Socket.last = this; }
  send() {}
  close() { this.readyState = 3; }
  push(value: unknown) { this.onmessage?.({ data: JSON.stringify(value) }); }
}
const machine = { id: "mac", name: "Studio", online: true };
const tab = { id: "w1:t1", workspace_id: "w1", label: "Review changes" };
const plain: PaneDTO = { pane: "w1:p1", terminal: "pane:w1:p1", tab_id: tab.id, workspace_id: "w1", cwd: "/demo", title: "zsh", foreground: "zsh" };
const layout = (panes: PaneDTO[]): PanesResponse => ({ hosts: [{ kind: "herdr", stream: true, control: true, new_tab: true,
  workspaces: [{ id: "w1", label: "Project" }], tabs: panes.length ? [tab] : [], panes }] });
const agent = (agent: string, session_id: string): Session => ({ machine_id: machine.id, session_id, agent, title: session_id, cwd: "/demo", status: "working", state: "working",
  host: "herdr", space_kind: "herdr", space_id: "w1", space_name: "Project", slot_id: tab.id, slot_name: tab.label, pane_id: plain.pane, updated_at: "2026-11-16T12:00:00Z" });
const json = (value: unknown) => new Response(JSON.stringify(value), { headers: { "Content-Type": "application/json" } });
let root: Root, host: HTMLElement;
const links = () => [...host.querySelectorAll<HTMLAnchorElement>("[data-sidebar-agents] a")];
const terminal = () => links().find(a => a.pathname === "/terminal/mac/pane%3Aw1%3Ap1");
const flush = async () => { for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); }); };
beforeAll(() => loadPages(), 60_000);
beforeEach(() => {
  reset(); localStorage.clear(); localStorage.setItem("persona.agentGrouping", "machine");
  history.replaceState(null, "", "/overview");
  vi.useFakeTimers();
  vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
  vi.stubGlobal("WebSocket", Socket);
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount()); host.remove(); reset(); vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks();
});

function hub(mode: "paged" | "pinned" | "legacy", omitFromRoot?: string) {
  let revision = 1;
  let active: Session[] = [], ended: Session[] = [], inventory: PaneDTO[] = [];
  const snapshots = new Map<string, Session[]>();
  const requests: URL[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: string) => {
    const url = new URL(input, "http://example.test");
    requests.push(url);
    if (url.pathname.endsWith("/machines")) return json({ machines: [machine] });
    if (url.pathname.endsWith("/pins")) return json({ sessions: mode === "pinned" ? ["mac/unrelated"] : [], spaces: [] });
    if (url.pathname.endsWith("/panes")) return json(layout(inventory));
    if (url.pathname.endsWith("/sessions")) {
      const q = url.searchParams;
      const rows = [...active, ...ended];
      if (q.has("counts")) return json({ revision: String(revision), total: rows.length, state: { working: active.length, offline: ended.length }, machine: { mac: rows.length }, group: {}, project: {} });
      const scoped = (items: Session[]) => q.get("agents_only") === "1" && !q.has("machine_id") ? items.filter(s => s.session_id !== omitFromRoot) : items;
      const listed = scoped(rows);
      const previous = scoped(snapshots.get(q.get("since") ?? "") ?? []);
      const remove = previous.filter(s => !listed.some(row => row.session_id === s.session_id)).map(s => JSON.stringify([s.machine_id, s.session_id]));
      return json({ sessions: listed.filter(s => !previous.includes(s)), revision: String(revision), remove, has_more: false, total: listed.length });
    }
    return json({});
  }));
  const sidebar = <Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />;
  return {
    requests,
    set(rows: Session[], past: Session[], panes: PaneDTO[]) {
      revision++; active = rows; ended = past; inventory = panes;
      snapshots.set(String(revision), [...rows, ...past]);
    },
    async open() {
      act(() => {
        connect(); Socket.last.onopen?.();
        Socket.last.push({ type: "hello", features: mode === "legacy" ? [] : ["sessions_list", "sessions_lookup", "sessions_agents_only"] });
      });
      await flush();
      act(() => root.render(sidebar));
      await flush();
    },
    async refresh() {
      act(() => Socket.last.push(mode === "legacy" ? { type: "sessions", machine_id: machine.id, sessions: [...active, ...ended] } : { type: "sessions_list", revision: String(revision) }));
      await act(() => vi.advanceTimersByTimeAsync(REFRESH_MS));
      await flush();
    },
    async history() {
      act(() => root.render(<HistoryBrowser />));
      await flush();
    },
  };
}

it.each([
  ["codex", "paged"], ["claude", "paged"], ["codex", "pinned"], ["claude", "pinned"],
] as const)("keeps the tab of an exited %s addressable with the %s sidebar, then opens only the replacement Agent", async (kind, mode) => {
  const before = agent(kind, "old-session"), after = agent(kind, "new-session");
  const stopped: Session = { ...before, state: "offline", status: "offline", pane_id: undefined, slot_id: undefined, space_id: undefined, capabilities: { read: true, terminal: true },
    ...(kind === "codex" && { source_kind: "codex_history" }) };
  const f = hub(mode, after.session_id);
  f.set([before], [], [{ ...plain, agent: kind, session_id: before.session_id }]);
  await f.open();
  expect(Boolean(host.querySelector("[data-paged-sessions]"))).toBe(mode === "paged");
  expect(links().find(a => a.textContent?.includes(before.title!))).toBeDefined();
  expect(f.requests.some(u => u.pathname.endsWith("/panes"))).toBe(false);

  act(() => setSidebarTerminals(true));
  await flush();
  f.set([], [stopped], [{ ...plain, agent_session: before.session_id }]);
  await f.refresh();
  expect(terminal()?.textContent).toBe(tab.label);
  expect(terminal()?.title).toContain(t("lib-terminals.finished", { title: before.session_id.slice(0, 8) }));
  expect(terminal()?.title).toContain(plain.foreground);
  if (mode === "paged") expect(links().some(a => a.textContent?.includes(before.title!))).toBe(false);

  // The session list can already identify the replacement while the previous pane snapshot is still being served.
  f.set([after], [stopped], [{ ...plain, agent_session: before.session_id }]);
  await f.refresh();
  expect(links().filter(a => a.textContent?.includes(after.title!))).toHaveLength(1);
  expect(terminal()).toBeUndefined();

  // A positive session ID in the pane list is authoritative even when the host's process label lags.
  f.set([after], [stopped], [{ ...plain, session_id: after.session_id }]);
  await f.refresh();
  expect(terminal()).toBeUndefined();
  const replacement = links().find(a => a.textContent?.includes(after.title!));
  expect(replacement?.pathname).toBe(mode === "paged" ? "/s/mac/new-session" : "/t/mac/w1/w1%3At1");
  if (mode === "paged") expect(links().some(a => a.textContent?.includes(before.title!))).toBe(false);
  f.set([], [stopped], []);
  await f.refresh();
  expect(terminal()).toBeUndefined();
  if (mode === "paged") expect(host.querySelector('[data-session-group="mac"]:not([hidden])')).toBeNull();
  expect(f.requests.some(u => u.pathname.endsWith("/sessions") && (!u.search || u.searchParams.get("summary") === "0"))).toBe(mode === "pinned");

  await f.history();
  const historical = host.querySelector<HTMLAnchorElement>('[data-history-row="old-session"] a');
  expect(historical).not.toBeNull();
  act(() => historical!.click());
  expect(location.pathname).toContain(before.session_id);
  expect(location.search).toContain("work");
});

it("shows a shell-only machine without reading an empty group of Agents and removes a closed tab", async () => {
  const f = hub("paged");
  setSidebarTerminals(true);
  f.set([], [], []);
  await f.open();
  expect(host.querySelector('[data-session-group="mac"]')).toBeNull();
  f.set([], [], [plain]);
  await f.refresh();
  expect(terminal()?.textContent).toBe(tab.label);
  expect(f.requests.some(u => u.searchParams.get("machine_id") === "mac")).toBe(false);
  f.set([], [], []);
  await f.refresh();
  expect(terminal()).toBeUndefined();
  expect(host.querySelector('[data-session-group="mac"]')).toBeNull();
});

it.each([false, true])("retains keyboard focus when a machine is folded and reopened (terminals %s)", async enabled => {
  const f = hub("paged");
  setSidebarTerminals(enabled);
  const current = agent("codex", "session");
  f.set([current], [], [{ ...plain, agent: current.agent, session_id: current.session_id }]);
  await f.open();
  const button = host.querySelector<HTMLButtonElement>('[data-session-group="mac"] button[aria-expanded]')!;
  button.focus();
  for (const open of ["false", "true"]) {
    act(() => button.click());
    await flush();
    expect(document.activeElement).toBe(button);
    expect(button.getAttribute("aria-expanded")).toBe(open);
  }
});

it("keeps an unrelated offline session in By machine when a known Agent leaves a plain, unmarked shell", async () => {
  const f = hub("paged");
  setSidebarTerminals(true);
  const before = agent("codex", "old-session");
  const stopped: Session = { ...before, status: "offline", state: "offline", source_kind: "codex_history", pane_id: undefined, space_id: undefined, slot_id: undefined };
  const unrelated = { ...stopped, session_id: "other-session", title: "Other history" };
  f.set([before], [unrelated], [{ ...plain, agent: "codex", session_id: before.session_id }]);
  await f.open();
  f.set([], [stopped, unrelated], [plain]);
  await f.refresh();
  const group = host.querySelector('[data-session-group="mac"]')!;
  expect(group.querySelector('[title="' + t("paged-agents.count", { count: 1 }) + '"]')).not.toBeNull();
  expect(terminal()?.textContent).toBe(tab.label);
  expect(links().some(a => a.pathname === "/s/mac/old-session")).toBe(false);
  expect(links().some(a => a.pathname === "/s/mac/other-session")).toBe(true);
  await f.history();
  expect(host.querySelector('[data-history-row="old-session"] a')).not.toBeNull();
});

it("keeps a shell beside an Agent in a split tab addressable as its own pane", async () => {
  const other: PaneDTO = { ...plain, pane: "w1:p2", terminal: "pane:w1:p2", agent: "claude", session_id: "sibling" };
  const f = hub("legacy");
  setSidebarTerminals(true);
  f.set([{ ...agent("claude", "sibling"), pane_id: other.pane }], [], [plain, other]);
  await f.open();
  const shell = terminal();
  expect(shell?.textContent).toBe(tab.label + " · " + plain.title);
  const menu = shell?.closest(".group\\/row")?.querySelector<HTMLButtonElement>("[aria-haspopup=menu]");
  act(() => menu?.click());
  await flush();
  const actions = document.querySelector('[role="menu"]');
  expect(actions?.textContent).toContain(t("lib-node-actions.close_pane"));
  expect(actions?.textContent).not.toContain(t("lib-node-actions.close_tab"));
});
