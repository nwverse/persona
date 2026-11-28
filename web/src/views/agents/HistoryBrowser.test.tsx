// @vitest-environment jsdom
import { act, useEffect } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { refreshAgentsDisplay, resetAgentsDisplay, saveAgentsDisplay } from "../../lib/agents-display";
import type { Session } from "../../lib/types";
import { HistoryBrowser } from "./HistoryBrowser";
import { clearResume } from "../../lib/agent-resume";
import { getState, applyCompleteSessions, reset } from "../../lib/store";
import { navigate } from "../../lib/router";
const env = vi.hoisted(() => ({ rows: [] as Session[], features: [] as string[], search: vi.fn(), more: vi.fn(), state: { query: "", hits: [] as { session: Session }[], loaded: true, loading: false, loadingMore: false, error: "", moreError: "", hasMore: false } }));
vi.mock("../../lib/sessions", () => ({ useFullAgentSessions: () => ({ sessions: env.rows, loaded: true, error: "" }), retryFullAgentSessions: vi.fn() }));
vi.mock("../../lib/current-assistant", () => ({ useScoped: (rows: Session[]) => rows, useScope: () => ({ several: false, id: "main", has: () => true }), useScopedMachines: () => [{ id: "one", name: "Computer one", online: true }, { id: "two", name: "Computer two", online: true }] }));
vi.mock("../../lib/store", async original => ({ ...await original<typeof import("../../lib/store")>(), rememberSessionRow: vi.fn() }));
vi.mock("../../lib/runtime-search", () => ({ RUNTIME_SEARCH_FEATURE: "sessions_search", RuntimeSearchModel: class { subscribe = () => () => {}; snapshot = () => env.state; search = env.search; retry = vi.fn(); more = env.more; dispose = vi.fn(); } }));
vi.mock("../../lib/router", async original => ({ ...await original<typeof import("../../lib/router")>(), navigate: vi.fn() }));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement, root: Root;
const row = (session_id: string, extra: Partial<Session> = {}) => ({ machine_id: "one", session_id, title: session_id, status: "offline", state: "offline", updated_at: "2026-11-16T12:00:00Z", ...extra } as Session);
async function render() {
  await act(async () => {
    Object.assign(getState(), { features: env.features, conn: "online" });
    applyCompleteSessions(env.rows);
    root.render(<HistoryBrowser />);
  });
}
beforeEach(() => {
  env.rows = [row("resume", { agent: "codex", capabilities: { read: true, terminal: true }, space_id: "old", slot_id: "old-tab" }), row("readonly", { agent: "claude", machine_id: "two" }), row("live", { state: "working", status: "working" }), row("waiting", { state: "needs_you" })];
  localStorage.clear(); env.features = []; env.state = { ...env.state, query: "", hits: [], hasMore: false }; vi.clearAllMocks();
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); clearResume("one", "resume"); reset(); host.remove(); resetAgentsDisplay(); vi.unstubAllGlobals(); vi.restoreAllMocks(); vi.useRealTimers(); });
const ids = () => [...host.querySelectorAll("[data-history-row]")].map(el => el.getAttribute("data-history-row"));
describe("HistoryBrowser", () => {
  it("opens on stopped rows, counts only filtered rows, and preserves read-only navigation", async () => {
    await render(); expect(ids()).toEqual(["resume", "readonly"]); expect(host.querySelector("[data-history-count]")?.textContent).toBe("2");
    const agent = host.querySelector<HTMLSelectElement>(`select[aria-label="${t("session-history.agent")}"]`)!;
    await act(async () => { agent.value = "codex"; agent.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(ids()).toEqual(["resume"]); expect(host.querySelector("[data-history-count]")?.textContent).toBe("1");
  });
  const resumeButton = (title = "resume") => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("session-history.resume_named", { title })}"]`)!;
  const live = (extra: Partial<Session> = {}) => row("resume", {
    state: "idle", status: "idle", space_id: "new-space", slot_id: "new-tab", ...extra,
  });
  function hub(sessions: () => Session[], reply?: () => Promise<Response>) {
    const starts = vi.fn(reply ?? (async () => Response.json({})));
    const reads = vi.fn((url: URL) => {
      const mid = url.searchParams.get("machine_id"), sid = url.searchParams.get("session_id");
      const rows = sessions().filter(s => !sid || (s.machine_id === mid && s.session_id === sid));
      return Response.json({ sessions: rows, total: rows.length, revision: "current", remove: [], has_more: false });
    });
    const fetch = vi.fn((path: string, init: RequestInit) =>
      init.method === "POST" ? starts() : reads(new URL(path, location.origin)));
    vi.stubGlobal("fetch", fetch);
    return { fetch, starts, reads };
  }
  it.each([false, true])("opens only the resumed tab after success (snapshot first = %s)", async snapshotFirst => {
    let accept!: (response: Response) => void;
    const { fetch, starts } = hub(() => env.rows, () => new Promise(resolve => { accept = resolve; }));
    await render();
    await act(async () => { resumeButton().click(); resumeButton().click(); });
    const [url, init] = fetch.mock.calls[0]!;
    expect([url, init.method, JSON.parse(String(init.body))]).toEqual([
      "/api/v1/sessions", "POST", { machine_id: "one", resume_session_id: "resume" },
    ]);
    const publish = async () => {
      env.rows = [live(), env.rows[1]!];
      await render();
      expect(ids()).toEqual(["readonly"]);
    };
    if (snapshotFirst) await publish();
    expect(navigate).not.toHaveBeenCalled();
    await act(async () => accept(Response.json({})));
    if (!snapshotFirst) {
      expect(navigate).not.toHaveBeenCalled();
      expect(resumeButton().disabled).toBe(true);
      await publish();
    }
    expect(starts).toHaveBeenCalledTimes(1);
    expect(vi.mocked(navigate).mock.calls).toEqual([[{ name: "tab", mid: "one", sid: "resume", space: "new-space", slot: "new-tab" }]]);
  });

  it.each([false, true])("follows the resumed Agent when the shell resolves the previous tab (same render = %s)", async sameRender => {
    const router = await vi.importActual<typeof import("../../lib/router")>("../../lib/router");
    const original = location.href;
    vi.mocked(navigate).mockImplementation(router.navigate);
    try {
      router.navigate({ name: "session", mid: "one", sid: "already-open", work: true });
      let accept!: (response: Response) => void;
      const { starts } = hub(() => env.rows, () => new Promise(resolve => { accept = resolve; }));
      function Shell({ detailArrived }: { detailArrived: boolean }) {
        useEffect(() => {
          if (detailArrived) router.navigate({ name: "tab", mid: "one", space: "old", slot: "retained", sid: "already-open", work: true }, true);
        }, [detailArrived]);
        return <HistoryBrowser />;
      }
      await act(async () => root.render(<Shell detailArrived={false} />));
      await act(async () => resumeButton().click());
      if (!sameRender) await act(async () => root.render(<Shell detailArrived />));
      await act(async () => accept(Response.json({})));
      env.rows = [live()];
      await act(async () => root.render(<Shell detailArrived />));
      expect(starts).toHaveBeenCalledTimes(1);
      expect(location.pathname + location.search).toBe("/t/one/new-space/new-tab/resume");
    } finally {
      vi.mocked(navigate).mockReset();
      history.replaceState(null, "", original);
    }
  });

  it("keeps a refused resume in History and shows the error", async () => {
    const { starts } = hub(() => [], async () => Response.json({ error: "Resume refused" }, { status: 409 }));
    await render();
    await act(async () => resumeButton().click());
    expect(starts).toHaveBeenCalledTimes(1);
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("Resume refused");
    expect(resumeButton().disabled).toBe(false);
    expect(navigate).not.toHaveBeenCalled();
  });

  it("ignores a live session on another computer with the same session ID", async () => {
    hub(() => env.rows);
    await render();
    await act(async () => resumeButton().click());
    env.rows = [env.rows[0]!, live({ machine_id: "two", slot_id: "unrelated-tab" })];
    await render();
    expect(navigate).not.toHaveBeenCalled();
    env.rows = [live(), env.rows[1]!];
    await render();
    expect(vi.mocked(navigate).mock.calls).toEqual([[{ name: "tab", mid: "one", sid: "resume", space: "new-space", slot: "new-tab" }]]);
  });

  it.each([
    { status: "idle", space_id: undefined, slot_id: undefined },
    { status: "unknown", state: "unknown", pane_id: "herdr-core:live", space_id: "dir:/test", slot_id: undefined },
  ] satisfies Partial<Session>[])("opens the session when the full lookup reports $status with no tab", async fields => {
    env.features = ["sessions_lookup"];
    const { reads } = hub(() => [live(fields)]);
    await render();
    await act(async () => resumeButton().click());
    expect(reads).toHaveBeenCalledTimes(1);
    expect(Object.fromEntries(reads.mock.calls[0]![0].searchParams)).toMatchObject({ machine_id: "one", session_id: "resume", summary: "0" });
    expect(vi.mocked(navigate).mock.calls).toEqual([[{ name: "session", mid: "one", sid: "resume" }]]);
  });
  it("routes conversation search through the existing model, filters live hits and reconciles resumed results", async () => {
    env.features = ["sessions_search"]; env.state = { ...env.state, query: "needle", hits: [{ session: row("resume") }, { session: row("live", { state: "working" }) }], hasMore: true };
    await render(); const input = host.querySelector("input")!;
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "needle"); input.dispatchEvent(new Event("input", { bubbles: true })); });
    expect(env.search).toHaveBeenLastCalledWith("needle", { manager: undefined }); expect(ids()).toEqual(["resume"]);
    expect(host.textContent).toContain(t("paged-agents.more"));
    env.rows = env.rows.map(s => s.session_id === "resume" ? { ...s, state: "idle", status: "idle" } : s); await render(); expect(ids()).toEqual([]);
  });
  it("follows the acknowledged hub preference while History stays open", async () => {
    env.rows = [row("bot", { pane_id: "botmux:example/topic" }), row("ordinary")];
    vi.stubGlobal("fetch", vi.fn(async (_url, init) => {
      const showBotmux = init?.method === "PUT" ? JSON.parse(String(init.body)).show_botmux : true;
      return Response.json({ show_botmux: showBotmux, configured: true });
    }));
    await render();
    expect(ids()).toEqual(["ordinary"]);
    await act(async () => { await refreshAgentsDisplay(); });
    expect(ids()).toHaveLength(2);
    await act(async () => { await saveAgentsDisplay(false); });
    expect(ids()).toEqual(["ordinary"]);
  });
  it("drops removed identities from an older search window", async () => {
    env.features = ["sessions_search"]; env.state = { ...env.state, query: "needle", hits: [{ session: row("removed") }] };
    await render(); const input = host.querySelector("input")!;
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "needle"); input.dispatchEvent(new Event("input", { bubbles: true })); });
    expect(ids()).toEqual([]);
  });

});
