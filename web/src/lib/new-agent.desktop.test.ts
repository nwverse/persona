import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./store", () => {
  const state = { launches: {} as Record<string, import("./new-agent").AgentLaunch> };
  return { getState: () => state, putLaunch: (launch: import("./new-agent").AgentLaunch) => { state.launches[launch.id] = launch; }, upsertSession: vi.fn(), isOpen: () => true, onHubEvent: () => () => {} };
});
vi.mock("./router", () => ({ href: (r: { mid: string; sid: string }) => `/s/${r.mid}/${r.sid}`, navigate: vi.fn() }));
import { deliverLaunch, launchAgent } from "./new-agent";
import { getState, upsertSession } from "./store";

const session = { session_id: "native-session", title: "Sample", cwd: "C:\\Projects\\Sample", status: "idle", agent: "claude", source_kind: "claude_desktop" };
const input = { machineId: "desktop", cwd: session.cwd, launcher: "claude-desktop", text: "Check this project", attachments: [] };
const response = (body: unknown, status = 200) => ({ ok: status < 400, status, text: async () => JSON.stringify(body) });

// Module loading is asynchronous even when fake time has not advanced.
async function advanceDelivery(ms: number) {
  await vi.advanceTimersByTimeAsync(0);
  await vi.dynamicImportSettled();
  await vi.advanceTimersByTimeAsync(ms);
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal("localStorage", { getItem: () => "test" });
  vi.stubGlobal("location", { pathname: "/other" });
  getState().launches = {};
  vi.clearAllMocks();
});
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

describe("desktop creation and recovery", () => {
  it.each(["claude-desktop", "codex-app"])("requests the synchronous startup budget for %s", async (launcher) => {
    const fetcher = vi.fn().mockResolvedValueOnce(response({ session: { ...session, machine_id: "desktop" } }));
    vi.stubGlobal("fetch", fetcher);
    launchAgent({ ...input, launcher, text: "" });
    await advanceDelivery(0);
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toMatchObject({ launcher, async: false });
  });

  it("preserves an explicit native project on Codex's prompt creation", async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(response({ session: { ...session, machine_id: "desktop", agent: "codex" } }));
    vi.stubGlobal("fetch", fetcher);
    launchAgent({ ...input, launcher: "codex-app", inlinePrompt: true, desktopProjectId: "codex-project:home:second" });
    await advanceDelivery(0);
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toMatchObject({ desktop_project_id: "codex-project:home:second", prompt: input.text });
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("preserves the exact native Claude environment on session creation", async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(response({ session: { ...session, machine_id: "desktop" } }));
    vi.stubGlobal("fetch", fetcher);
    launchAgent({ ...input, text: "", desktopProjectId: "claude-environment:account:saved" });
    await advanceDelivery(0);
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toMatchObject({ desktop_project_id: "claude-environment:account:saved", launcher: "claude-desktop", cwd: session.cwd });
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it.each(["none", "unknown"])("retains a created session when first-message delivery is %s and never creates again", async (delivery) => {
    const fetcher = vi.fn().mockResolvedValueOnce(response({ code: "desktop_start_incomplete", error: "Desktop import did not complete", delivered: "unknown", created: true, session_id: session.session_id, session, first_message_delivered: delivery }, 409))
      .mockResolvedValueOnce(response({}));
    vi.stubGlobal("fetch", fetcher);
    const id = launchAgent(input);
    await advanceDelivery(0);
    const recovered = { ...session, machine_id: "desktop" };
    expect(getState().launches[id]).toMatchObject({ phase: "failed", session: recovered, uncertain: delivery === "unknown" });
    expect(upsertSession).toHaveBeenCalledWith(recovered);
    expect(fetcher).toHaveBeenCalledTimes(1);
    await deliverLaunch(id);
    expect(fetcher.mock.calls.filter((c) => c[0] === "/api/v1/sessions")).toHaveLength(1);
    expect(fetcher.mock.calls[1][0]).toBe("/api/v1/sessions/desktop/native-session/messages");
    expect(getState().launches[id].phase).toBe("ready");
  });

  it("waits for a setup-only acknowledgement when retrying an empty created Claude session", async () => {
    let finishSetup!: (r: ReturnType<typeof response>) => void;
    const fetcher = vi.fn().mockResolvedValueOnce(response({ code: "desktop_start_incomplete", error: "Import failed", delivered: "unknown", created: true, session_id: session.session_id, session, first_message_delivered: "none" }, 409))
      .mockImplementationOnce(() => new Promise((resolve) => { finishSetup = resolve; }));
    vi.stubGlobal("fetch", fetcher);
    const id = launchAgent({ ...input, text: "" });
    await advanceDelivery(0);
    expect(getState().launches[id]).toMatchObject({ phase: "failed", desktopSetupIncomplete: true });
    const recovery = deliverLaunch(id);
    await advanceDelivery(0);
    expect(getState().launches[id].phase).toBe("creating");
    expect(JSON.parse(fetcher.mock.calls[1][1].body)).toMatchObject({ launcher: "claude-desktop", resume_session_id: session.session_id, prompt: "", async: false });
    expect(fetcher.mock.calls[1][0]).toBe("/api/v1/sessions");
    finishSetup(response({ session: { ...session, machine_id: "desktop" } }));
    await recovery;
    expect(getState().launches[id]).toMatchObject({ phase: "ready", desktopSetupIncomplete: false });
    expect(fetcher).toHaveBeenCalledTimes(2);
  });
});
