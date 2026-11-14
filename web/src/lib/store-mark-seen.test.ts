import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "./api";
import { connect, getState, markSeen, reset, subscribe, upsertSession } from "./store";
import type { Session } from "./types";

vi.mock("./api", async (original) => ({ ...await original<typeof import("./api")>(), api: vi.fn() }));

class Socket {
  static OPEN = 1;
  static last: Socket;
  readyState = 1;
  send = vi.fn();
  constructor() { Socket.last = this; }
  close() { this.readyState = 3; }
}
const row = (session_id: string, patch: Partial<Session> = {}): Session => ({
  machine_id: "demo", session_id, title: session_id, cwd: "/demo", status: "idle", state: "unread", ...patch,
});

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-01-01T00:00:00Z"));
  vi.stubGlobal("WebSocket", Socket);
  vi.stubGlobal("location", { protocol: "https:", host: "hub.example.com" });
  vi.stubGlobal("localStorage", { getItem: () => "mock-token" });
  vi.stubGlobal("window", { setTimeout, clearTimeout });
  reset();
  vi.mocked(api).mockReset().mockResolvedValue({});
});
afterEach(() => { reset(); vi.useRealTimers(); vi.unstubAllGlobals(); });

describe("marking fallback-only sessions seen", () => {
  it("sends each supplied unread identity once and retains only acted-on rows in one update", () => {
    const full = Array.from({ length: 75 }, (_, i) => row(`agent-${i}`));
    const untouched = row("retained");
    upsertSession(untouched);
    const changed = vi.fn();
    const off = subscribe(changed);
    markSeen([full[64], full[70], full[64]]);
    off();

    expect(vi.mocked(api).mock.calls).toEqual([
      ["/sessions/demo/agent-64/seen", { method: "POST", json: { manual: true } }],
      ["/sessions/demo/agent-70/seen", { method: "POST", json: { manual: true } }],
    ]);
    expect(getState().sessions.demo).toEqual([
      untouched,
      { ...full[64], state: "idle", seen_at: "2026-01-01T00:00:00.000Z" },
      { ...full[70], state: "idle", seen_at: "2026-01-01T00:00:00.000Z" },
    ]);
    expect(getState().sessions.demo[0]).toBe(untouched);
    expect(changed).toHaveBeenCalledTimes(1);
  });

  it("uses retained current state and skips agents no longer unread", () => {
    const working = row("working", { state: "working", status: "working" });
    const current = row("changed", { title: "Current title" });
    upsertSession(working);
    upsertSession(current);
    markSeen([row("working"), row("changed"), row("already-seen", { state: "idle" })]);

    expect(vi.mocked(api).mock.calls).toEqual([["/sessions/demo/changed/seen", { method: "POST", json: { manual: true } }]]);
    expect(getState().sessions.demo).toEqual([
      working,
      { ...current, state: "idle", seen_at: "2026-01-01T00:00:00.000Z" },
    ]);
    markSeen([row("changed")]);
    expect(api).toHaveBeenCalledTimes(1);
  });

  it("sends fallback rows over the connected socket and distinguishes composite identities", () => {
    connect();
    const left = row("part/agent", { machine_id: "demo" });
    const right = row("agent", { machine_id: "demo/part" });
    markSeen([left, right, left]);

    expect(Socket.last.send.mock.calls.map(([message]) => JSON.parse(message))).toEqual([
      { type: "seen", machine_id: left.machine_id, session_id: left.session_id, manual: true },
      { type: "seen", machine_id: right.machine_id, session_id: right.session_id, manual: true },
    ]);
    expect(api).not.toHaveBeenCalled();
    expect(getState().sessions.demo[0].state).toBe("idle");
    expect(getState().sessions["demo/part"][0].state).toBe("idle");
  });

  it("does not let an older detail cache suppress a newly unread full snapshot", () => {
    upsertSession(row("updated", { state: "idle", updated_at: "2025-12-30T00:00:00Z", seen_at: "2025-12-30T01:00:00Z" }));
    const latest = row("updated", { title: "Latest result", updated_at: "2025-12-31T00:00:00Z", state_since: "2025-12-31T00:00:01Z" });
    markSeen([latest]);
    expect(vi.mocked(api).mock.calls).toEqual([["/sessions/demo/updated/seen", { method: "POST", json: { manual: true } }]]);
    expect(getState().sessions.demo[0]).toEqual({ ...latest, state: "idle", seen_at: "2026-01-01T00:00:00.000Z" });
  });
});
