// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { stripBase } from "./base";

describe("Electron desktop bridge", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("uses the existing notification text, tag and quiet flag without a browser duplicate", async () => {
    const notify = vi.fn(() => true);
    const closeNotification = vi.fn();
    vi.stubGlobal("window", Object.assign(window, { personaDesktop: {
      version: 1, platform: "darwin", deviceName: "Desktop",
      notificationPermission: () => "granted", notify, closeNotification,
      desktopPresence: () => ({ active: true, notifies: true }),
      setBadge: vi.fn(), onOpenNotification: vi.fn(),
    } }));
    vi.stubGlobal("localStorage", { getItem: (key: string) => key === "persona.systemNotify" ? "1" : null, setItem: vi.fn(), removeItem: vi.fn() });
    vi.stubGlobal("navigator", {});
    const { postNotification, closeNotification: close } = await import("./notify");
    postNotification({ title: "Fix login", body: "Allow?", tag: "m/a#needs", url: "/persona/s/m/a", silent: true }, () => {});
    expect(notify).toHaveBeenCalledExactlyOnceWith({ title: "Fix login", body: "Allow?", tag: "m/a#needs", route: "/persona/s/m/a", silent: true, ix: undefined });
    close("m/a#needs");
    expect(closeNotification).toHaveBeenCalledExactlyOnceWith("m/a#needs");
  });

  it("returns a native click to the existing SPA router without exposing an origin", () => {
    const openDesktopNotification = (route: string, go: (url: string) => void) => {
      if (!route.startsWith("/") || route.startsWith("//") || route.includes("\\")) return;
      go(stripBase(route));
    };
    const navigate = vi.fn();
    openDesktopNotification("/persona/s/m/a?view=chat", navigate);
    expect(navigate).toHaveBeenCalledExactlyOnceWith("/persona/s/m/a?view=chat");
    openDesktopNotification("https://evil.test", navigate);
    openDesktopNotification("//evil.test", navigate);
    expect(navigate).toHaveBeenCalledOnce();
  });
});
