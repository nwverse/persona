// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { BOTMUX_MODE_KEY, botmuxSentinelOnly, getBotmuxMode, readBotmuxMode, setBotmuxMode } from "./botmux-mode";

afterEach(() => localStorage.clear());

describe("botmux mode", () => {
  it("decodes by default", () => {
    expect(readBotmuxMode()).toBe("decode");
  });
  it("uses decoded format for an old hidden preference", () => {
    localStorage.setItem("persona.agentHideBotmux", "1");
    localStorage.setItem(BOTMUX_MODE_KEY, "hide");
    expect(readBotmuxMode()).toBe("decode");
  });
  it("persists the mode without removing visibility migration input", () => {
    localStorage.setItem("persona.agentHideBotmux", "1");
    setBotmuxMode("all");
    expect(getBotmuxMode()).toBe("all");
    expect(localStorage.getItem(BOTMUX_MODE_KEY)).toBe("all");
    setBotmuxMode("decode");
    expect(localStorage.getItem(BOTMUX_MODE_KEY)).toBe("decode");
    expect(localStorage.getItem("persona.agentHideBotmux")).toBe("1");
  });
  it("falls back when the localStorage getter throws", () => {
    const getter = vi.spyOn(globalThis, "localStorage", "get").mockImplementation(() => {
      throw new DOMException("Storage unavailable", "SecurityError");
    });
    try {
      expect(readBotmuxMode()).toBe("decode");
    } finally {
      getter.mockRestore();
    }
  });
});

describe("botmuxSentinelOnly (the hub's transcript.WithoutBotmuxSentinel is empty)", () => {
  it("is the control line alone, its blank space and repeats included", () => {
    expect(botmuxSentinelOnly("BOTMUX_NOTHING_TO_SEND")).toBe(true);
    expect(botmuxSentinelOnly("  BOTMUX_NOTHING_TO_SEND \n\n")).toBe(true);
    expect(botmuxSentinelOnly("BOTMUX_NOTHING_TO_SEND\nBOTMUX_NOTHING_TO_SEND")).toBe(true);
  });
  it("is not words that mention it or stand beside it", () => {
    expect(botmuxSentinelOnly("Done.\nBOTMUX_NOTHING_TO_SEND")).toBe(false);
    expect(botmuxSentinelOnly("The agent ends with BOTMUX_NOTHING_TO_SEND here.")).toBe(false);
    expect(botmuxSentinelOnly("`BOTMUX_NOTHING_TO_SEND`")).toBe(false);
    expect(botmuxSentinelOnly("")).toBe(false);
    expect(botmuxSentinelOnly(undefined)).toBe(false);
  });
});
