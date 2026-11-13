import { describe, expect, it } from "vitest";
import { t } from "../i18n";
import { agentRowKey, agentRowLabel, agentRowMark, todoChipState } from "./task-row-status";
import type { Supervision } from "./supervision";
import type { Session } from "./types";

const session = (state: Session["state"]): Session => ({ machine_id: "m", session_id: "s", title: "", cwd: "/", state, status: state === "offline" ? "offline" : "idle", updated_at: "2026-11-13T00:00:00Z" });
const parked: Supervision = { state: "parked", supervisor: "c", reviews: [], parked: { reason: "Waiting on review", until: "2026-11-20T00:00:00Z", waiting_on: "someone_else", count: 1 } };

describe("a task row's status column says only what its Agent is doing (#6492)", () => {
  it("reads the Agent's own state, not the to-do's", () => {
    // a parked or follow-up to-do: the column says what its lead session is, stopped or idle
    expect(agentRowKey({ live: false, status: "follow_up", lead: session("offline"), todo: true, agents: 1 })).toBe("stopped");
    expect(agentRowKey({ live: false, status: "follow_up", lead: session("idle"), todo: true, agents: 1 })).toBe("idle");
    expect(agentRowKey({ live: false, status: "done", lead: undefined, todo: true, agents: 1 })).toBe("stopped");
    expect(agentRowKey({ live: false, status: "follow_up", lead: session("unread"), todo: true, agents: 1 })).toBe("unread");
    // the states that are the Agent's own stay as they are
    expect(agentRowKey({ live: false, status: "needs_you", lead: session("needs_you"), todo: true, agents: 1 })).toBe("needs_you");
    expect(agentRowKey({ live: false, status: "usage_limit", todo: true, agents: 1 })).toBe("usage_limit");
    expect(agentRowKey({ live: true, status: "follow_up", lead: session("idle"), todo: true, agents: 1 })).toBe("working");
    // nothing started yet: a to-do whose Agent is coming up, or a plan card without a to-do
    expect(agentRowKey({ live: false, status: "not_started", todo: true, agents: 0 })).toBe("starting");
    expect(agentRowKey({ live: false, status: "accepted", todo: false, agents: 0 })).toBe("not_started");
  });

  it("draws every key with the Agents' own marks and names stopped and idle apart", () => {
    expect(agentRowMark("stopped")).toBe("offline");
    expect(agentRowMark("idle")).toBe("idle");
    expect(agentRowMark("working")).toBe("working");
    expect(agentRowLabel("stopped")).toBe(t("assistant-tasks.agent_stopped"));
    expect(agentRowLabel("idle")).toBe(t("assistant-tasks.agent_idle"));
    expect(agentRowLabel("stopped")).not.toBe(t("assistant-tasks.review_parked"));
  });
});

describe("the to-do's own state is a chip of its own (#6492)", () => {
  it("names parked, waiting to merge, accepted and waiting to start; nothing for a to-do with nothing to add", () => {
    expect(todoChipState({ toStart: false, status: "follow_up", done: false, todo: { done_at: null, supervision: parked } })).toBe(t("assistant-tasks.review_parked"));
    expect(todoChipState({ toStart: false, status: "follow_up", done: false, stage: "merge", pr: "https://example.com/pr/1", todo: { done_at: null } })).toBe(t("assistant-tasks.todo_waiting_merge"));
    expect(todoChipState({ toStart: false, status: "follow_up", done: false, stage: "merge", todo: { done_at: null } })).toBeUndefined();
    expect(todoChipState({ toStart: true, status: "not_started", done: false })).toBe(t("assistant-tasks.plan_waiting"));
    expect(todoChipState({ toStart: false, status: "accepted", done: true })).toBe(t("assistant-tasks.review_accepted"));
    expect(todoChipState({ toStart: false, status: "working", done: false, todo: { done_at: null } })).toBeUndefined();
  });
});
