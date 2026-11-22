// @vitest-environment jsdom
// Exercise the real sidebar and REST client: failures must be readable without hover and recover in place.
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

const example: AgentSession = { id: "example:agent", source: "example", sessionKey: "agent", provider: "example", title: "Example personal agent", canSend: true, unreadCount: 0, rankTimeMs: 1 };
const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
let host: HTMLDivElement, root: Root;
let sessions: () => Promise<Response>;
let calls: number;
const flush = async () => { for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); }); };
const retry = () => [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === t("sidebar.retry"));
const open = async () => {
  // A cold failure now hides this optional entry; retry UI belongs to previously loaded conversations.
  const next = sessions;
  sessions = async () => response({ sessions: [example] });
  await act(async () => { await refreshAgentSessions(); });
  sessions = next;
  calls = 0;
  await act(async () => { await refreshAgentSessions(); });
  await act(async () => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
  await flush();
};

beforeEach(async () => {
  await initI18n("en");
  localStorage.clear();
  resetAgents();
  calls = 0;
  sessions = async () => response({ error: "Example gateway is unavailable" }, 503);
  vi.stubGlobal("fetch", async (url: string) => {
    const path = new URL(url, "https://example.invalid").pathname;
    if (path === "/api/v1/agents/sessions") { calls++; return sessions(); }
    if (path === "/api/v1/agents") return response({ sources: [{ source: "example", enabled: true }] });
    return response({});
  });
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

describe("personal agents load failures in the sidebar", () => {
  it("shows the reason in the page and retries once while the request is pending", async () => {
    await open();
    expect(host.textContent).toContain("Example gateway is unavailable");
    expect(retry()).toBeTruthy();

    let answer!: (value: Response) => void;
    sessions = () => new Promise<Response>((resolve) => { answer = resolve; });
    const button = retry()!;
    await act(async () => button.click());
    await flush();
    expect(button.disabled).toBe(true);
    expect(button.textContent).toBe(t("sidebar.loading"));
    expect(host.textContent).toContain("Example gateway is unavailable");
    await act(async () => { button.click(); button.click(); });
    expect(calls).toBe(2);

    await act(async () => answer(response({ sessions: [example] })));
    await flush();
    expect(host.textContent).not.toContain("Example gateway is unavailable");
    expect(retry()).toBeUndefined();
    expect(host.querySelector('a[href*="example%3Aagent"]')?.textContent).toContain(example.title);
    expect(calls).toBe(2);
  });

  it("keeps a failed retry available and uses the shared network error explanation", async () => {
    await open();
    expect(retry()).toBeTruthy();
    sessions = async () => { throw new TypeError("Failed to fetch"); };
    await act(async () => retry()!.click());
    await flush();
    expect(host.textContent).toContain(t("lib-api.cant_reach_hub"));
    expect(host.textContent).not.toContain("Failed to fetch");
    expect(retry()?.disabled).toBe(false);

    sessions = async () => response({ sessions: [example] });
    await act(async () => retry()!.click());
    await flush();
    expect(host.textContent).toContain(example.title);
    expect(host.textContent).not.toContain(t("lib-api.cant_reach_hub"));
    expect(calls).toBe(3);
  });

  it("keeps previously loaded agents visible on failure and preserves the successful empty state", async () => {
    sessions = async () => response({ sessions: [example] });
    await open();
    sessions = async () => response({ error: "Example refresh failed" }, 503);
    await act(async () => { await refreshAgentSessions(); });
    await flush();
    expect(host.textContent).toContain(example.title);
    expect(host.textContent).toContain("Example refresh failed");

    sessions = async () => response({ sessions: [] });
    await act(async () => retry()!.click());
    await flush();
    expect(host.textContent).not.toContain(example.title);
    expect(host.textContent).not.toContain(t("sidebar.personal_agents"));
    expect(retry()).toBeUndefined();
  });
});
