// @vitest-environment jsdom
// Through the real sidebar and REST client: a personal agent's ⋯ / right click offers "Remove from list", which posts
// the hide, takes the row out at once and keeps it out once the hub's list no longer has it.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {} })) as typeof window.matchMedia;
});
import { initI18n, t } from "../i18n";
import { refreshAgentSessions, resetAgents, type AgentSession } from "../lib/agents";
import { reset } from "../lib/store";
import { Sidebar } from "./Sidebar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const keep: AgentSession = { id: "example:keep", source: "example", sessionKey: "keep", provider: "example", title: "Kept agent", canSend: true, unreadCount: 0, rankTimeMs: 2 };
const gone: AgentSession = { id: "example:gone", source: "example", sessionKey: "gone", provider: "example", title: "Removed agent", canSend: true, unreadCount: 3, rankTimeMs: 1 };
const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
let host: HTMLDivElement, root: Root;
let listed: AgentSession[];
let posts: string[];
let hideStatus: number;
const flush = async () => { for (let i = 0; i < 10; i++) await act(async () => { await Promise.resolve(); }); };
const rowOf = (title: string) => [...host.querySelectorAll<HTMLAnchorElement>("aside a")].find((a) => a.textContent?.includes(title));

beforeEach(async () => {
  await initI18n("en");
  localStorage.clear();
  resetAgents();
  listed = [keep, gone];
  posts = [];
  hideStatus = 200;
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    const path = new URL(url, "https://example.invalid").pathname;
    if (init?.method === "POST") {
      posts.push(path);
      if (path.endsWith("/hide")) {
        if (hideStatus !== 200) return response({ error: "gateway refused" }, hideStatus);
        listed = listed.filter((s) => s.id !== gone.id);
      }
      return response({ ok: true });
    }
    if (path === "/api/v1/agents/sessions") return response({ sessions: listed });
    if (path === "/api/v1/agents") return response({ sources: [{ source: "example", enabled: true }] });
    return response({});
  });
  history.replaceState(null, "", "/");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  await act(async () => { await refreshAgentSessions(); });
  await act(async () => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
  await flush();
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.body.querySelectorAll("[role=menu]").forEach((m) => m.remove());
  reset();
  resetAgents();
  vi.unstubAllGlobals();
});

const removeFromMenu = async (title: string) => {
  const row = rowOf(title)!;
  await act(async () => row.closest("div")!.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true })));
  await flush();
  const item = [...document.querySelectorAll<HTMLElement>("[role=menuitem]")].find((b) => b.textContent?.includes(t("lib-agents.remove")));
  expect(item).toBeTruthy();
  await act(async () => item!.click());
  await flush();
};

describe("removing a personal agent from the sidebar", () => {
  it("posts the hide and the row leaves the list", async () => {
    expect(rowOf("Removed agent")).toBeTruthy();
    await removeFromMenu("Removed agent");
    expect(posts).toEqual(["/api/v1/agents/sessions/example%3Agone/hide"]);
    expect(rowOf("Removed agent")).toBeUndefined();
    expect(rowOf("Kept agent")).toBeTruthy();
  });

  it("keeps the row when the hub refuses", async () => {
    hideStatus = 404;
    await removeFromMenu("Removed agent");
    expect(rowOf("Removed agent")).toBeTruthy();
  });
});
