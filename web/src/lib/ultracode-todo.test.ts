import { describe, expect, it } from "vitest";
import { todoUltracode } from "./ultracode";
import { makeSessionLookup, type Todo } from "./todos";
import type { Session } from "./types";

const session = (over: Partial<Session> = {}): Session =>
  ({ machine_id: "m1", session_id: "s1", title: "fix", cwd: "/work/app", agent: "claude", status: "working", state: "working", ...over }) as Session;

const todo = (over: Partial<Todo> = {}): Todo => ({
  id: "t1",
  title: "fix the widget",
  created_at: "2026-11-08T00:00:00Z",
  updated_at: "2026-11-08T00:00:00Z",
  agents: [{ machine_id: "m1", session_id: "s1", linked_at: "2026-11-08T00:00:00Z" }],
  ...over,
});

describe("the ultracode mark on a to-do", () => {
  it("follows its agents as their Agent list rows do", () => {
    expect(todoUltracode(todo(), makeSessionLookup([session({ ultracode: true })]))).toBe(true);
    expect(todoUltracode(todo(), makeSessionLookup([session({ ultracode: false })]))).toBe(false);
    expect(todoUltracode(todo(), makeSessionLookup([session()]))).toBe(false); // unknown is not on
    // not on an offline agent: the node keeps the last value it saw after the process is gone
    expect(todoUltracode(todo(), makeSessionLookup([session({ ultracode: true, status: "offline", state: "offline" })]))).toBe(false);
    // not from an agent the to-do let go
    const released = todo({ agents: [{ machine_id: "m1", session_id: "s1", linked_at: "x", released_at: "y" }, { machine_id: "m1", session_id: "s2", linked_at: "x" }] });
    expect(todoUltracode(released, makeSessionLookup([session({ ultracode: true }), session({ session_id: "s2" })]))).toBe(false);
  });

  it("is on when any of its agents runs with it", () => {
    const two = todo({ agents: [{ machine_id: "m1", session_id: "s1", linked_at: "x" }, { machine_id: "m1", session_id: "s2", linked_at: "x" }] });
    expect(todoUltracode(two, makeSessionLookup([session(), session({ session_id: "s2", ultracode: true })]))).toBe(true);
  });

  it("says what the plan asks for before any agent started, and the agent's own once one has", () => {
    const waiting = todo({ agents: [], plan_id: "p1" });
    expect(todoUltracode(waiting, makeSessionLookup([]), { ultracode: true })).toBe(true);
    expect(todoUltracode(waiting, makeSessionLookup([]), { ultracode: false })).toBe(false);
    expect(todoUltracode(waiting, makeSessionLookup([]))).toBe(false);
    expect(todoUltracode(todo(), makeSessionLookup([session({ ultracode: false })]), { ultracode: true })).toBe(false);
  });

  it("does not go back to the plan's choice once its agents were moved to another to-do or taken out", () => {
    const emptied = todo({ agents: [{ machine_id: "m1", session_id: "s1", linked_at: "x", released_at: "y" }], plan_id: "p1" });
    expect(todoUltracode(emptied, makeSessionLookup([session({ ultracode: true })]), { ultracode: true })).toBe(false);
  });

  it("never shows on a to-do that is done or did not start", () => {
    expect(todoUltracode(todo({ done_at: "2026-11-08T01:00:00Z" }), makeSessionLookup([session({ ultracode: true })]))).toBe(false);
    expect(todoUltracode(todo({ agents: [], start_error: "ultracode_unsupported" }), makeSessionLookup([]), { ultracode: true })).toBe(false);
  });
});
