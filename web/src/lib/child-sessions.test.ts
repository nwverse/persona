import { describe, expect, it } from "vitest";
import { foldedChildren, foldedState, foldSessions, isFoldedIn, listedKeys, requestHome, runningChildren } from "./child-sessions";
import type { Session } from "./types";

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

describe("foldSessions", () => {
  it("returns the list itself when nothing folds", () => {
    const rows = [row("a"), row("b")];
    expect(foldSessions(rows)).toBe(rows);
  });
  it("leaves out children of a listed session and shows the parent folded", () => {
    const parent = row("p", { state: "idle", children: { total: 2, working: 1 } });
    const rows = [parent, row("c1", { folded_under: "p", state: "working" }), row("c2", { folded_under: "p" }), row("x")];
    const out = foldSessions(rows);
    expect(out.map((s) => s.session_id)).toEqual(["p", "x"]);
    expect(out[0].state).toBe("working");
    expect(out[1]).toBe(rows[3]);
  });
  it("keeps a child whose parent the list does not hold, and a parent on another machine is not its parent", () => {
    const rows = [row("c", { folded_under: "gone" }), row("p", { machine_id: "other" }), row("d", { folded_under: "p" })];
    expect(foldSessions(rows).map((s) => s.session_id)).toEqual(["c", "p", "d"]);
  });
  it("copies no parent whose state does not change", () => {
    const parent = row("p", { state: "unread", children: { total: 1, working: 1 } });
    const out = foldSessions([parent, row("c", { folded_under: "p" })]);
    expect(out[0]).toBe(parent);
  });
});

describe("helpers", () => {
  it("isFoldedIn checks the machine and the parent", () => {
    const keys = listedKeys([row("p")]);
    expect(isFoldedIn(row("c", { folded_under: "p" }), keys)).toBe(true);
    expect(isFoldedIn(row("c", { folded_under: "p", machine_id: "n" }), keys)).toBe(false);
    expect(isFoldedIn(row("c"), keys)).toBe(false);
  });
  it("foldedChildren lists a session's children oldest first", () => {
    const rows = [row("c2", { folded_under: "p", started_at: "2026-11-12T00:05:00Z" }), row("c1", { folded_under: "p", started_at: "2026-11-12T00:01:00Z" }), row("x", { folded_under: "q" })];
    expect(foldedChildren(rows, "m", "p").map((s) => s.session_id)).toEqual(["c1", "c2"]);
  });
  it("requestHome is the parent's page for a child's request", () => {
    expect(requestHome({ session_id: "c", folded_under: "p" })).toBe("p");
    expect(requestHome({ session_id: "c" })).toBe("c");
  });
  it("runningChildren counts working children", () => {
    expect(runningChildren(row("p", { children: { total: 3, working: 2 } }))).toBe(2);
    expect(runningChildren(row("p"))).toBe(0);
  });
});
