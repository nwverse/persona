import { describe, expect, it } from "vitest";
import { requestRoute } from "./card-focus";
import { childRollup, foldedChildren, foldedState, isFoldedIn, listedKeys, requestHome, requestHomeMachine, runningChildren } from "./child-sessions";
import { setSideFold } from "./side-fold";
import type { Interaction, Session } from "./types";

const row = (sid: string, extra: Partial<Session> = {}): Session =>
  ({ machine_id: "m", session_id: sid, title: sid, cwd: "/w", status: "idle", state: "idle", updated_at: "2026-11-12T00:00:00Z", ...extra }) as Session;

describe("foldedState", () => {
  it("is the own state without children", () => {
    expect(foldedState(row("a", { state: "unread" }))).toBe("unread");
  });
  it("needs you when the session or a child does", () => {
    expect(foldedState(row("a", { state: "working", children: { total: 2, needs_you: 1 } }))).toBe("needs_you");
    expect(foldedState(row("a", { state: "needs_you", children: { total: 1 } }))).toBe("needs_you");
  });
  it("works while a child works, unless a reply waits to be read or answered", () => {
    expect(foldedState(row("a", { state: "idle", children: { total: 1, working: 1 } }))).toBe("working");
    expect(foldedState(row("a", { state: "offline", children: { total: 1, working: 1 } }))).toBe("working");
    expect(foldedState(row("a", { state: "unread", children: { total: 1, working: 1 } }))).toBe("unread");
    expect(foldedState(row("a", { state: "idle", awaits_reply: true, children: { total: 1, working: 1 } }))).toBe("idle");
  });
});

describe("helpers", () => {
  it("isFoldedIn checks the machine and the parent, including cross-machine folded_under_machine_id", () => {
    const keys = listedKeys([row("p")]);
    expect(isFoldedIn(row("c", { folded_under: "p" }), keys)).toBe(true);
    expect(isFoldedIn(row("c", { folded_under: "p", machine_id: "n" }), keys)).toBe(false);
    expect(isFoldedIn(row("c", { folded_under: "p", machine_id: "n", folded_under_machine_id: "m" }), keys)).toBe(true);
    expect(isFoldedIn(row("c"), keys)).toBe(false);
  });
  it("foldedChildren lists same-machine, cross-machine, and archived-parent orphan children oldest first", () => {
    const rows = [
      row("c2", { folded_under: "p", started_at: "2026-11-12T00:05:00Z" }),
      row("c1", { folded_under: "p", started_at: "2026-11-12T00:01:00Z" }),
      row("c3", { machine_id: "box", folded_under: "p", folded_under_machine_id: "m", started_at: "2026-11-12T00:03:00Z" }),
      row("orphan-archived", { machine_id: "box", is_subagent: true, parent_machine_id: "m", parent_session_id: "p", started_at: "2026-11-12T00:02:00Z" }),
      row("x", { folded_under: "q" }),
    ];
    expect(foldedChildren(rows, "m", "p").map((s) => s.session_id)).toEqual(["c1", "orphan-archived", "c3", "c2"]);
  });
  it("requestHome, requestHomeMachine, and requestRoute resolve cross-machine parents", () => {
    expect(requestHome({ session_id: "c", folded_under: "p" })).toBe("p");
    expect(requestHome({ session_id: "c" })).toBe("c");
    const remoteIx = { id: "ix1", machine_id: "box", session_id: "c", folded_under: "p", folded_under_machine_id: "m", kind: "permission" } as Interaction;
    expect(requestHomeMachine(remoteIx)).toBe("m");
    expect(requestRoute(remoteIx)).toEqual({ name: "session", mid: "m", sid: "p" });
  });
  it("runningChildren counts working children", () => {
    expect(runningChildren(row("p", { children: { total: 3, working: 2 } }))).toBe(2);
    expect(runningChildren(row("p"))).toBe(0);
  });
  it("childRollup prioritizes needs_you > running > null and excludes side sessions", () => {
    expect(childRollup(row("p"))).toBeNull();
    expect(childRollup(row("p", { children: { total: 4, working: 0, needs_you: 0 } }))).toBeNull();
    expect(childRollup(row("p", { children: { total: 3, working: 2, needs_you: 1 } }))).toEqual({ kind: "needs_you", count: 1 });
    expect(childRollup(row("p", { children: { total: 3, working: 2 } }))).toEqual({ kind: "running", count: 2 });
    expect(childRollup(row("p", { subagents: [{ id: "a", session_id: "p@a", running: true }, { id: "b", session_id: "p@b", running: false }] }))).toEqual({ kind: "running", count: 1 });
    expect(childRollup(row("p", { waiting: false, running_subagents: 3, children: { total: 2, working: 2 } }))).toEqual({ kind: "running", count: 3 });
    setSideFold(true);
    try {
      expect(childRollup(row("p", { children: { total: 1, needs_you: 1, sides: 1, sides_needs_you: 1 } }))).toBeNull();
      expect(childRollup(row("p", { children: { total: 2, working: 2, sides: 1, sides_working: 1 } }))).toEqual({ kind: "running", count: 1 });
    } finally {
      setSideFold(false);
    }
  });
});
