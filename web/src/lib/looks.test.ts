// Lean looks (#16898, lib/looks.ts): each look's default stores nothing, an unknown value reads as the default.
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  const kept = new Map<string, string>();
  (globalThis as Record<string, unknown>).localStorage = {
    getItem: (k: string) => kept.get(k) ?? null,
    setItem: (k: string, v: string) => void kept.set(k, v),
    removeItem: (k: string) => void kept.delete(k),
    clear: () => kept.clear(),
    get length() { return kept.size; },
  };
});

import { isLookMode, look, LOOK_NAMES, setLook } from "./looks";

describe("lean looks", () => {
  beforeEach(() => localStorage.clear());

  it("start at their defaults, the page as it always was", () => {
    expect(LOOK_NAMES.map((n) => look(n))).toEqual(["full", "full", "full", "full"]);
  });

  it("keep a lean look in this browser and store nothing for the default", () => {
    setLook("main_chat", "plain");
    setLook("agents_view", "active");
    setLook("settings_view", "short");
    setLook("sidebar_view", "agents");
    expect(LOOK_NAMES.map((n) => look(n))).toEqual(["plain", "active", "short", "agents"]);
    expect(localStorage.length).toBe(4);
    for (const n of LOOK_NAMES) setLook(n, "full");
    expect(localStorage.length).toBe(0);
  });

  it("read an unknown value as the default and know each look's modes", () => {
    localStorage.setItem("persona.look.main-chat", "short");
    expect(look("main_chat")).toBe("full");
    expect(isLookMode("agents_view", "active")).toBe(true);
    expect(isLookMode("agents_view", "plain")).toBe(false);
  });
});
