// @vitest-environment jsdom
// A sessions push re-renders the sidebar, but the page rows (NavRow) and the personal agents' rows (AgentRow) take nothing
// from the sessions: they render again only when their own props change (a badge), and they keep their ref, so the
// glide's ResizeObserver is not told to let go of each row and watch it again on every push.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
// every ResizeObserver.observe, by the row's link (its href)
const observed = vi.hoisted(() => new Map<string, number>());
vi.hoisted(() => {
  globalThis.ResizeObserver = class {
    observe(el: Element) {
      const k = el.getAttribute("href") ?? el.getAttribute("aria-label") ?? el.tagName;
      observed.set(k, (observed.get(k) ?? 0) + 1);
    }
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
// NavRow and AgentRow build their link with href() once per render: counted per path
const renders = vi.hoisted(() => new Map<string, number>());
vi.mock("../lib/router", async (load) => {
  const real = await load<typeof import("../lib/router")>();
  return {
    ...real,
    href: (...args: Parameters<typeof real.href>) => {
      const out = real.href(...args);
      renders.set(out, (renders.get(out) ?? 0) + 1);
      return out;
    },
  };
});
import { refreshAgentSessions, resetAgents, type AgentSession } from "../lib/agents";
import { href } from "../lib/router";
import { getState, refresh, reset, upsertSession } from "../lib/store";
import type { Machine, Session } from "../lib/types";
import { Sidebar } from "./Sidebar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const machines: Machine[] = [{ id: "mac", name: "MacBook", online: true }];
const sessions: Session[] = [{ machine_id: "mac", session_id: "s1", title: "Task 1", cwd: "/code", status: "idle", state: "idle" }];
const agents: AgentSession[] = ["a1", "a2", "a3"].map((id, i) => ({ id: `example:${id}`, source: "example", sessionKey: id, provider: "example", title: `Agent ${i + 1}`, canSend: true, unreadCount: 0, rankTimeMs: 0 }));
const answers: Record<string, unknown> = { "/api/v1/sessions": { sessions }, "/api/v1/machines": { machines }, "/api/v1/agents/sessions": { sessions: agents }, "/api/v1/agents": { sources: [{ source: "example", enabled: true }] } };

// built once here: the counting href() would count these calls too
const pagePaths = ["home", "inbox", "settings"].map((name) => href({ name } as Parameters<typeof href>[0]));
const agentPaths = agents.map((a) => href({ name: "agents", id: a.id }));
const homePath = pagePaths[0];
const sum = (m: Map<string, number>, keys: string[]) => keys.reduce((n, k) => n + (m.get(k) ?? 0), 0);

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};

beforeEach(() => {
  localStorage.clear();
  resetAgents();
  vi.stubGlobal("fetch", vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(answers[new URL(url, "http://x").pathname] ?? {}), { headers: { "Content-Type": "application/json" } }))));
  history.replaceState(null, "", "/");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  resetAgents();
  vi.unstubAllGlobals();
});

describe("a sessions push", () => {
  it("re-renders no page row and no personal agent row, and re-observes none of them", async () => {
    await act(() => refresh());
    await act(async () => { await refreshAgentSessions(); });
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    expect(host.textContent).toContain("Agent 3");
    const rows = [...pagePaths, ...agentPaths];
    // the rows are there, each watched once
    for (const p of rows) expect(observed.get(p), p).toBeGreaterThan(0);
    renders.clear();
    observed.clear();

    for (let i = 0; i < 10; i++) {
      act(() => upsertSession({ ...getState().sessions.mac[0], title: `Task 1 (${i})` }));
      await flush();
    }
    expect(host.textContent).toContain("Task 1 (9)");
    const counts = { navRenders: sum(renders, pagePaths), agentRenders: sum(renders, agentPaths), observes: sum(observed, rows) };
    expect(counts).toEqual({ navRenders: 0, agentRenders: 0, observes: 0 });
  });

  it("still re-renders Home when its badge changes", async () => {
    await act(() => refresh());
    act(() => root.render(<Sidebar route={{ name: "inbox" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    const home = () => host.querySelector<HTMLAnchorElement>(`a[href="${homePath}"]`)!;
    expect(home().textContent).not.toMatch(/1/);
    renders.clear();

    act(() => upsertSession({ ...getState().sessions.mac[0], status: "needs_you", state: "needs_you" }));
    await flush();
    expect(renders.get(homePath)).toBeGreaterThan(0);
    expect(home().textContent).toMatch(/1/);
  });
});
