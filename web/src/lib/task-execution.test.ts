import { describe, expect, it } from "vitest";
import { assistantEntries } from "./assistant-entries";
import { agentRowKey, agentRowLabel, agentRowMark } from "./task-row-status";
import { makeSessionLookup, type Todo } from "./todos";
import type { Session } from "./types";
import { STOP_FOLLOWUP_MS, taskExecution } from "./task-execution";

const at = "2026-11-17T08:00:00Z";
const now = Date.parse(at) + STOP_FOLLOWUP_MS;
const todo: Todo = { id: "task", title: "Check the release", origin: "main", created_at: at, updated_at: at,
  agents: [{ machine_id: "box", session_id: "worker", linked_at: at }],
  supervision: { supervisor: "main", state: "pending_review" } };
const agent: Session = { machine_id: "box", session_id: "worker", title: "Release check", cwd: "/example",
  state: "unread", status: "idle", state_since: at, updated_at: at };
const entries = (s = agent, t = todo, clock = now) => assistantEntries({ conversation: "main", main: "main",
  todos: [t], sessions: [s], interactions: [], plans: {}, feed: [], now: clock });
const keys = (rows: { key: string }[]) => rows.map((r) => r.key);

describe("closeout-based entries aligned with #10174", () => {
  it.each(["agent", "external"] as const)("keeps a named %s wait in Waiting rather than escalating its blocked closeout", (on) => {
    const s: Session = { ...agent, state: "idle", work: { phase: "blocked", on, wait: "the release checks finish",
      turn_at: at, closeout: { status: "blocked" } } };
    const got = entries(s, todo, now + STOP_FOLLOWUP_MS);
    expect(keys(got.parked)).toEqual(["task:task"]);
    expect([...got.needsYou, ...got.justFinished, ...got.running]).toEqual([]);
    expect(taskExecution(todo, makeSessionLookup([s])).waiting).toBe(true);
  });
  it.each(["not_done", "blocked", undefined])("keeps a stopped %s turn unfinished, unread or seen", (status) => {
    for (const state of ["unread", "idle"] as const) {
      const got = entries({ ...agent, state, work: status ? { phase: "", closeout: { status, waiting: "the design team" } } : undefined });
      expect(got.justFinished).toEqual([]);
      expect(got.running).toEqual([]);
      expect(got).not.toHaveProperty("review");
      expect(got.needsYou).toEqual([]);
    }
  });
  it("uses the Agent's done closeout, not pending_review alone, for awaiting acceptance", () => {
    const s: Session = { ...agent, work: { phase: "done", closeout: { status: "done" } } };
    for (const state of ["", "pending_review"] as const) {
      const got = entries(s, { ...todo, supervision: { ...todo.supervision!, state } }, now + 24 * STOP_FOLLOWUP_MS);
      expect(keys(got.justFinished)).toEqual(["task:task"]);
      expect(got).not.toHaveProperty("review");
      expect([...got.running, ...got.needsYou]).toEqual([]);
    }
  });
  it("keeps a seen reported finish in Just finished before its acceptance ledger arrives", () => {
    const stopped: Session = { ...agent, state: "idle", work: { phase: "done", closeout: { status: "done" } } };
    const awaiting: Todo = { ...todo, seen_at: new Date(now).toISOString(), supervision: { supervisor: "main", state: "" } };
    const got = entries(stopped, awaiting, now + 1);
    expect(keys(got.justFinished)).toEqual(["task:task"]);
    expect([...got.running, ...got.needsYou]).toEqual([]);
    expect(got.justFinished[0]).toMatchObject({ countsOpen: false });
  });
  it("does not call an unread stopped Agent running when another Agent really runs", () => {
    const second: Session = { ...agent, session_id: "second", state: "working", status: "running" };
    const got = assistantEntries({ conversation: "main", main: "main", todos: [{ ...todo, agents: [...todo.agents!,
      { machine_id: "box", session_id: "second", linked_at: at }] }], sessions: [agent, second], interactions: [], plans: {}, feed: [], now });
    expect(keys(got.running)).toEqual(["task:task"]);
    expect(got).not.toHaveProperty("review");
  });
  it("ignores an old done closeout on a resumed Agent", () => {
    const got = entries({ ...agent, state: "working", work: { phase: "working", closeout: { status: "done" } } });
    expect(keys(got.running)).toEqual(["task:task"]);
    expect(got).not.toHaveProperty("review");
  });
  it("escalates only after the full hour and honors real follow-up, not automatic ledger writes", () => {
    expect(entries(agent, todo, now).needsYou).toEqual([]);
    expect(keys(entries(agent, todo, now + 1).needsYou)).toEqual(["task:task"]);
    const followed = { ...todo, supervision: { ...todo.supervision!, followed_up_at: new Date(now).toISOString() } };
    expect(entries(agent, followed, now + 1).needsYou).toEqual([]);
    expect(entries(agent, followed, now + 1).justFinished).toEqual([]);
    expect(keys(entries(agent, { ...todo, supervision: { ...todo.supervision!, reviews: [
      { at: new Date(now).toISOString(), verdict: "rework" },
    ] } }, now + 1).needsYou)).toEqual([]);
    const automatic = { ...todo, supervision: { ...todo.supervision!, updated_at: new Date(now).toISOString(), reviews: [
      { at: new Date(now).toISOString(), verdict: "rework" as const, by: "reviewer" as const },
    ] } };
    expect(keys(entries(agent, automatic, now + 1).needsYou)).toEqual(["task:task"]);
  });
  it("keeps explicit external parks out of stopped work and age escalation", () => {
    const parked = { ...todo, supervision: { ...todo.supervision!, state: "parked" as const,
      parked: { reason: "Waiting for the maintainer", until: "2026-12-01T00:00:00Z", waiting_on: "someone_else" as const, count: 1 } } };
    const got = entries(agent, parked, now + 48 * STOP_FOLLOWUP_MS);
    expect(keys(got.parked)).toEqual(["task:task"]);
    expect([...got.running, ...got.needsYou]).toEqual([]);
  });
  it("does not let blocked/external waiting override an explicit park", () => {
    const s: Session = { ...agent, state: "idle", work: { phase: "blocked", on: "external", turn_at: at,
      closeout: { status: "blocked", waiting: "the maintainer's re-review" } } };
    const t: Todo = { ...todo, stage: "review", supervision: { ...todo.supervision!, state: "parked",
      parked: { reason: "Waiting for re-review", waiting_on: "someone_else", until: "2026-12-01T00:00:00Z", count: 1 } } };
    expect(keys(entries(s, t, now + 24 * STOP_FOLLOWUP_MS).parked)).toEqual(["task:task"]);
    expect(keys(entries(s, { ...t, supervision: undefined }, now + 1).needsYou)).toEqual(["task:task"]);
    expect(entries(s, { ...t, supervision: undefined }).running).toEqual([]);
    expect(keys(entries({ ...s, background_tasks_count: 1 }, t).running)).toEqual(["task:task"]);
    expect(keys(entries(s, { ...t, ask: { outcome: "decide", asked_at: at, verdict: "Choose a release track" } }).needsYou)).toEqual(["task:task"]);
  });
  it("does not reset the unattended clock on a read receipt or hub restart", () => {
    const s: Session = { ...agent, state: "idle", state_since: new Date(now).toISOString(), updated_at: new Date(now).toISOString(),
      work: { phase: "", turn_at: at, closeout: { status: "not_done" } } };
    expect(taskExecution(todo, makeSessionLookup([s])).unattendedSince).toBe(Date.parse(at));
    expect(keys(entries(s, todo, now + 1).needsYou)).toEqual(["task:task"]);
  });
  it("counts an unattended task in Needs you even when legacy triage would have shelved it", () => {
    const s: Session = { ...agent, state: "idle", work: { phase: "", turn_at: at, closeout: { status: "not_done" } } };
    const t: Todo = { ...todo, supervision: { supervisor: "main", state: "" } };
    expect(entries(s, t, Date.parse(at) + 72 * STOP_FOLLOWUP_MS).needsYou).toMatchObject([{ key: "task:task", countsOpen: true }]);
  });
  it("never brings an accepted task back as stopped or awaiting acceptance", () => {
    const accepted = { ...todo, supervision: { ...todo.supervision!, state: "accepted" as const,
      reviews: [{ at, verdict: "accepted" as const }] } };
    const got = entries(agent, accepted, now + 24 * STOP_FOLLOWUP_MS);
    expect([...got.running, ...got.needsYou]).toEqual([]);
  });
});

describe("Agent rows describe closeouts, never equating unread with finished", () => {
  it.each([["done", "reported_done"], ["not_done", "unfinished"], ["blocked", "blocked"]] as const)("%s has its own row state", (status, expected) => {
    const lead: Session = { ...agent, work: { phase: "", closeout: { status, waiting: "the design team" } } };
    const key = agentRowKey({ live: false, status: "unread", todo: true, starting: false, lead });
    expect(key).toBe(expected);
    expect(agentRowMark(key)).not.toBe("unread");
    expect(agentRowLabel(key, lead)).not.toBe(agentRowLabel("unread"));
    if (status === "blocked") expect(agentRowLabel(key, lead)).toContain("the design team");
  });
  it("does not use the supervisor ledger's updated_at as a send receipt", () => {
    const t = { ...todo, supervision: { ...todo.supervision!, updated_at: new Date(now).toISOString() } };
    expect(taskExecution(t, makeSessionLookup([agent])).unattendedSince).toBe(Date.parse(at));
  });
});
