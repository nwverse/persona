// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { connect, reset, savePins } from "../lib/store";
import { cycleGrouping } from "../lib/sidebar-api";
import type { AgentPins } from "../lib/pins";
import type { Session } from "../lib/types";
import { Sidebar } from "./Sidebar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class FakeSocket {
  static OPEN = 1;
  static all: FakeSocket[] = [];
  readyState = 0;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  constructor() { FakeSocket.all.push(this); }
  send() {}
  close() { this.readyState = 3; }
  open() { this.readyState = 1; this.onopen?.(); }
  push(msg: Record<string, unknown>) { this.onmessage?.({ data: JSON.stringify(msg) }); }
}
const row = (id: string): Session => ({ machine_id: "computer", session_id: id, title: id, cwd: "/code/demo", status: "working", state: "working", updated_at: "2026-01-01T00:00:00Z", space_id: "project", space_name: "Demo project" });
const first = row("First page agent"), tail = row("Beyond first page");
const emptyPins: AgentPins = { sessions: [], spaces: [] };
const json = (body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } }));
let host: HTMLDivElement, root: Root;
const flush = async () => { for (let i = 0; i < 10; i++) await act(async () => { await Promise.resolve(); }); };
const grouping = () => host.querySelector('[role="radiogroup"][aria-label="Agent 分组"]');

async function render(paged: boolean, pins = emptyPins, saved?: string) {
  if (saved) localStorage.setItem("persona.agentGrouping", saved);
  const fetch = vi.fn((input: string) => {
    const url = new URL(input, "http://hub.example.com");
    if (url.pathname.endsWith("/sessions") && !url.pathname.includes("/agents/")) {
      if (url.searchParams.has("counts")) return json({ total: 2, state: { working: 2 }, machine: { computer: 2 }, group: {}, project: {}, revision: "r1" });
      // The complete fallback's pages (#3759): every row, in two frozen pages; the bounded list's summary pages.
      if (url.searchParams.get("summary") === "0" && !url.searchParams.has("session_id")) {
        if (url.searchParams.has("since")) return json({ sessions: [], remove: [], has_more: false, revision: "r1", total: 2 });
        return url.searchParams.get("cursor") === "next"
          ? json({ sessions: [tail], remove: [], has_more: false, revision: "r1", total: 2 })
          : json({ sessions: [first], remove: [], has_more: true, next_cursor: "next", revision: "r1", total: 2 });
      }
      if (url.search) return json({ sessions: [first], remove: [], has_more: true, next_cursor: "next", revision: "r1", total: 2 });
      return json({ sessions: [first, tail] });
    }
    if (url.pathname.endsWith("/pins")) return json(pins);
    if (url.pathname.endsWith("/machines")) return json({ machines: [{ id: "computer", name: "Demo computer", online: true }] });
    if (url.pathname.endsWith("/todos")) return json({ todos: [] });
    return json({});
  });
  vi.stubGlobal("fetch", fetch);
  act(() => {
    connect();
    const socket = FakeSocket.all.at(-1)!;
    socket.open();
    socket.push({ type: "hello", features: paged ? ["sessions_list", "sessions_lookup"] : [] });
  });
  await flush();
  act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
  await flush();
  return fetch;
}

beforeEach(() => {
  localStorage.clear();
  reset();
  FakeSocket.all = [];
  vi.stubGlobal("WebSocket", FakeSocket);
  history.replaceState(null, "", "/");
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount()); host.remove(); reset(); vi.unstubAllGlobals();
});

for (const paged of [false, true]) {
  describe(`sidebar with paging ${paged ? "supported" : "unavailable"}`, () => {
    it("keeps session pins beyond the first page in their own section", async () => {
      await render(paged, { sessions: ["computer/Beyond first page"], spaces: [] });
      expect(grouping()).not.toBeNull();
      expect(host.textContent).toContain("置顶");
      expect(host.textContent).toContain(tail.title);
      expect(host.querySelectorAll('a[href*="Beyond%20first%20page"]')).toHaveLength(1);
    });
    it("keeps pinned workspaces", async () => {
      await render(paged, { sessions: [], spaces: ["computer/Demo project"] });
      expect(grouping()).not.toBeNull();
      expect(host.textContent).toContain(tail.title);
    });
    it.each(["machine", "space", "repo", "state", "todo"])("honors saved %s grouping", async (saved) => {
      await render(paged, emptyPins, saved);
      expect(grouping()).not.toBeNull();
      expect(grouping()!.querySelector('[aria-checked="true"]')).not.toBeNull();
      expect(host.textContent).toContain(tail.title);
      expect(localStorage.getItem("persona.agentGrouping")).toBe(saved);
    });
    it("uses paging only when supported and no pins or grouping were chosen", async () => {
      const fetch = await render(paged);
      expect(Boolean(grouping())).toBe(!paged);
      const completeReads = fetch.mock.calls.filter(([url]) => url.endsWith("/api/v1/sessions"));
      expect(completeReads.length > 0).toBe(!paged);
    });
  });
}

it("reacts to pins saved while the paged list is mounted, then releases the fallback", async () => {
  await render(true);
  expect(grouping()).toBeNull();
  await act(async () => savePins({ sessions: ["computer/Beyond first page"], spaces: [] }));
  await flush();
  expect(grouping()).not.toBeNull();
  expect(host.textContent).toContain(tail.title);
  await act(async () => savePins(emptyPins));
  await flush();
  expect(grouping()).toBeNull();
});

it("honors the grouping shortcut while the paged list is mounted", async () => {
  await render(true);
  act(() => { cycleGrouping(); });
  await flush();
  expect(grouping()).not.toBeNull();
  expect(localStorage.getItem("persona.agentGrouping")).toBe("machine");
  expect(host.textContent).toContain(tail.title);
});
