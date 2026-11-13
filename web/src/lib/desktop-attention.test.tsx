// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useAttentionSignals } from "./attention-signals";
import type { Interaction } from "./types";

const sendMock = vi.hoisted(() => vi.fn());
vi.mock("./store", () => ({
  useStore: (select: (s: { loaded: boolean; interactions: Interaction[] }) => unknown) => select({ loaded: true, interactions: [] }),
  getState: () => ({ mutes: { spaces: [], sessions: [], allowed: [] }, interactions: [] }),
  onHello: (fn: () => void) => { hello = fn; return () => { hello = undefined; }; },
  send: sendMock,
  appSocketReady: () => true,
}));
vi.mock("./sessions", () => ({ useCounts: () => ({ badge: 0 }), useAgentSessions: () => [] }));
vi.mock("./notify", async (original) => ({ ...(await original<typeof import("./notify")>()), systemNotifyActive: () => true }));

let hello: (() => void) | undefined;
let inactive: (() => void) | undefined;

function Probe() {
  useAttentionSignals("Persona", undefined);
  return null;
}

describe("desktop attention lifecycle", () => {
  let root: Root;
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    vi.stubGlobal("window", Object.assign(window, { personaDesktop: {
      version: 1, platform: "linux", deviceName: "Desktop",
      notificationPermission: () => "granted", desktopPresence: () => ({ active: true, notifies: true }),
      notify: () => true, closeNotification: vi.fn(), clearNotifications: vi.fn(), setBadge: vi.fn(),
      onOpenNotification: () => () => {},
      onPresenceChange: (callback: (presence: { active: boolean; notifies: boolean }) => void) => {
        inactive = () => callback({ active: false, notifies: true });
        return () => { inactive = undefined; };
      },
    } }));
    hello = undefined;
    inactive = undefined;
    root = createRoot(document.createElement("div"));
  });
  afterEach(() => {
    act(() => root.unmount());
    vi.unstubAllGlobals();
    vi.clearAllMocks();
  });
  it("registers on authenticated hello and disconnects on lock, then stops listening", async () => {
    await act(async () => root.render(<Probe />));
    await act(async () => hello?.());
    const { send } = await import("./store");
    expect(send).toHaveBeenCalledExactlyOnceWith({ type: "register_desktop", device_name: "Desktop" });
    await act(async () => inactive?.());
    expect(sendMock).toHaveBeenNthCalledWith(2, { type: "unregister_desktop" });
    await act(async () => root.unmount());
    expect(hello).toBeUndefined();
    expect(inactive).toBeUndefined();
  });
});
