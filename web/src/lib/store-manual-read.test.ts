// @vitest-environment jsdom
// Manual mark as read (#7356): with the hub's switch on, viewing a conversation or an Agent leaves it Unread (no seen
// at all, so the hub and the other devices keep it too); only Mark as read clears it, says so to the hub (manual), is
// held seen here until the hub records it, and a new turn after it is Unread again. Off, viewing marks it as before
// (store-seen.test.ts).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "./api";
import { connect, getSession, getState, markRead, markSeen, reset, viewSession as openView } from "./store";
import { manualRead, setManualReadLocal } from "./manual-read";
import { SEEN_CONFIRM_MS } from "./seen-outbox";
import { stateOf } from "./format";
import type { Session } from "./types";

vi.mock("./api", async (original) => ({ ...await original<typeof import("./api")>(), api: vi.fn() }));

class Socket {
  static OPEN = 1;
  static all: Socket[] = [];
  readyState = 0;
  sent: string[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onclose: ((ev: { code: number }) => void) | null = null;
  constructor() { Socket.all.push(this); }
  send(s: string) { this.sent.push(s); }
  close() { this.readyState = 3; }
  open() { this.readyState = 1; this.onopen?.(); }
  push(msg: Record<string, unknown>) { this.onmessage?.({ data: JSON.stringify(msg) }); }
  seen() { return this.sent.map((s) => JSON.parse(s)).filter((m) => m.type === "seen"); }
}
const sock = () => Socket.all[Socket.all.length - 1];

const T0 = "2026-01-01T00:00:00Z";
const unread = (patch: Partial<Session> = {}): Session => ({
  machine_id: "mac", session_id: "s1", title: "Fix the tests", cwd: "/code", status: "done", state: "unread",
  updated_at: "2025-12-31T23:59:00Z", seen_at: "0001-01-01T00:00:00Z", ...patch,
});
const seenPosts = () => vi.mocked(api).mock.calls.filter(([path]) => String(path).endsWith("/seen"));
let visibility: DocumentVisibilityState = "visible";
Object.defineProperty(document, "visibilityState", { configurable: true, get: () => visibility });
const toFront = () => { visibility = "visible"; document.dispatchEvent(new Event("visibilitychange")); };
// every view a test opens is closed after it: the registry of chats on screen lives as long as the module
const opened: Array<() => void> = [];
const viewSession = (mid: string, sid: string) => {
  const release = openView(mid, sid);
  let done = false;
  const once = () => { if (!done) { done = true; release(); } };
  opened.push(once);
  return once;
};

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date(T0));
  Socket.all = [];
  visibility = "visible";
  vi.stubGlobal("WebSocket", Socket);
  vi.stubGlobal("location", { protocol: "https:", host: "hub.example.com" });
  vi.stubGlobal("localStorage", { getItem: () => "tok", setItem: () => {}, removeItem: () => {} });
  reset();
  // the hub's switch as each test set it (the store reads it again after every hello, lib/read-setting.ts)
  hubManual = undefined;
  vi.mocked(api).mockReset().mockImplementation(async (path: string) =>
    path === "/read-settings" ? { manual: hubManual ?? manualRead(), can_write: true } : {});
});
afterEach(() => {
  setManualReadLocal(false);
  opened.splice(0).forEach((release) => release());
  reset();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

/** A connected page with the hub's hello, the machine's list pushed (a hub with sessions_delta). */
function online(rows: Session[] = [unread()]) {
  connect();
  sock().open();
  sock().push({ type: "hello", features: ["sessions_delta"], event_stream: "e", event_seq: 1 });
  sock().push({ type: "sessions", machine_id: "mac", sessions: rows });
  return sock();
}
const shown = () => stateOf(getSession("mac", "s1")!);

let hubManual: boolean | undefined;
const unreadCount = () => getState().sessions.mac.filter((s) => stateOf(s) === "unread").length;

describe("manual mark as read off (the default)", () => {
  it("marks a viewed chat seen, as before, without saying it was the user's", () => {
    const s = online([unread(), unread({ session_id: "s2" })]);
    viewSession("mac", "s1");
    expect(shown()).toBe("idle");
    expect(unreadCount()).toBe(1);
    expect(s.seen()).toEqual([{ type: "seen", machine_id: "mac", session_id: "s1" }]);
  });
});

describe("manual mark as read on", () => {
  beforeEach(() => setManualReadLocal(true));

  it("leaves a viewed chat Unread and tells the hub nothing", () => {
    const s = online([unread(), unread({ session_id: "s2" })]);
    viewSession("mac", "s1");
    toFront(); // the page coming back to the front marks nothing either
    expect(shown()).toBe("unread");
    expect(unreadCount()).toBe(2);
    expect(s.seen()).toEqual([]);
    vi.advanceTimersByTime(SEEN_CONFIRM_MS + 10);
    expect(seenPosts()).toEqual([]);
  });

  it("leaves a turn that ends while the chat is on screen Unread", () => {
    const s = online([unread({ state: "working", status: "working" })]);
    viewSession("mac", "s1");
    s.push({ type: "sessions_delta", machine_id: "mac", upsert: [unread({ updated_at: "2026-01-01T00:01:00Z" })], remove: [] });
    expect(shown()).toBe("unread");
    expect(s.seen()).toEqual([]);
  });

  it("clears it on Mark as read, held against a stale list, and again harmlessly", () => {
    const s = online([unread(), unread({ session_id: "s2" })]);
    viewSession("mac", "s1");
    markRead("mac", "s1");
    expect(shown()).toBe("idle");
    expect(unreadCount()).toBe(1);
    expect(s.seen()).toEqual([{ type: "seen", machine_id: "mac", session_id: "s1", manual: true }]);
    // a list the hub made before it took the mark: still shown read
    s.push({ type: "sessions", machine_id: "mac", sessions: [unread(), unread({ session_id: "s2" })] });
    expect(shown()).toBe("idle");
    markRead("mac", "s1");
    expect(shown()).toBe("idle");
    expect(s.seen().every((m) => m.manual === true)).toBe(true);
  });

  it("goes over HTTP with manual when the socket's mark is not taken", () => {
    online();
    markRead("mac", "s1");
    vi.advanceTimersByTime(SEEN_CONFIRM_MS + 10);
    expect(seenPosts()).toEqual([["/sessions/mac/s1/seen", expect.objectContaining({ method: "POST", json: { manual: true } })]]);
  });

  it("is Unread again after a new turn, and viewing it still does not clear it", () => {
    const s = online();
    viewSession("mac", "s1");
    markRead("mac", "s1");
    s.push({ type: "sessions", machine_id: "mac", sessions: [unread({ state: "idle", seen_at: "2026-01-01T00:00:01Z" })] });
    expect(getState().seen).toEqual({});
    s.sent = [];
    s.push({ type: "sessions", machine_id: "mac", sessions: [unread({ updated_at: "2026-01-01T00:05:00Z", seen_at: "2026-01-01T00:00:01Z" })] });
    expect(shown()).toBe("unread");
    expect(s.seen()).toEqual([]);
  });

  it("says manual for Mark all as read", () => {
    const s = online([unread(), unread({ session_id: "s2" })]);
    markSeen(getState().sessions.mac);
    expect(unreadCount()).toBe(0);
    expect(s.seen().map((m) => m.manual)).toEqual([true, true]);
  });
});

describe("turning the switch", () => {
  it("on: a look the hub has not recorded no longer holds the chat read", () => {
    online([unread(), unread({ session_id: "s2" })]);
    viewSession("mac", "s1");
    markRead("mac", "s2");
    expect(unreadCount()).toBe(0);
    setManualReadLocal(true);
    expect(shown()).toBe("unread");
    expect(stateOf(getSession("mac", "s2")!)).toBe("idle"); // the user's own mark stays
  });

  it("off: the chats on screen are seen, as opening them would", () => {
    setManualReadLocal(true);
    const s = online();
    viewSession("mac", "s1");
    expect(shown()).toBe("unread");
    setManualReadLocal(false);
    expect(shown()).toBe("idle");
    expect(s.seen()).toEqual([{ type: "seen", machine_id: "mac", session_id: "s1" }]);
  });
});

describe("a hello", () => {
  it("reads the hub's switch, so a reload or another device's change is followed", async () => {
    hubManual = true; // turned on elsewhere while this page was away
    vi.useRealTimers();
    online();
    await vi.waitFor(() => expect(manualRead()).toBe(true));
    viewSession("mac", "s1");
    expect(shown()).toBe("unread");
  });
});
