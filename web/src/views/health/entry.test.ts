// Where entering Health lands (#4329): the latest health conversation, else the new conversation page.
import { describe, expect, it } from "vitest";
import { healthEntry } from "./entry";
import { href, parse } from "../../lib/router";
import type { Session } from "../../lib/types";

const s = (session_id: string, updated_at: string, archived = false) => ({ machine_id: "m", session_id, updated_at, archived, space: "health" }) as Session;

describe("entering Health", () => {
  it("opens the latest conversation by last activity", () => {
    const list = [s("old", "2026-11-01T08:00:00Z"), s("latest", "2026-11-11T08:00:00Z"), s("mid", "2026-11-05T08:00:00Z")];
    expect(healthEntry(list)).toEqual({ kind: "open", sid: "latest" });
  });

  it("skips an archived one, however recent", () => {
    expect(healthEntry([s("gone", "2026-11-11T09:00:00Z", true), s("kept", "2026-11-10T08:00:00Z")])).toEqual({ kind: "open", sid: "kept" });
  });

  it("opens the new conversation page when there is none", () => {
    expect(healthEntry([])).toEqual({ kind: "new" });
    expect(healthEntry([s("gone", "2026-11-11T09:00:00Z", true)])).toEqual({ kind: "new" });
  });

  it("waits for the list, and opens the new conversation page when it can't be read", () => {
    expect(healthEntry(null)).toEqual({ kind: "wait" });
    expect(healthEntry(null, "hub away")).toEqual({ kind: "new" });
  });

  it("gives the new conversation page its own address", () => {
    expect(parse("/health/new")).toEqual({ name: "health", fresh: true });
    expect(href({ name: "health", fresh: true })).toBe("/health/new");
    expect(parse("/health")).toEqual({ name: "health", sid: undefined });
    expect(href({ name: "health" })).toBe("/health");
    expect(parse("/health/h1")).toEqual({ name: "health", sid: "h1" });
  });
});
