import { describe, expect, it } from "vitest";
import { TODO_LIST_SECTIONS, type TodoSummary } from "../../../lib/todo-view-wire";
import type { Triage, TriageRule, TriageSection } from "../../../lib/todo-triage";
import { makeSessionLookup, type Todo } from "../../../lib/todos";
import type { Session } from "../../../lib/types";
import { BY_DESIGN, BOUNDARY_MS, classify, knownView, legacyOf, restoredAt, runFacts, type ByDesign, type RunFacts } from "./view-legacy";

const NOW = Date.parse("2026-11-10T12:00:00Z");
const HOUR = 3_600_000;
const local = (section: TriageSection, rule: TriageRule, last = NOW - HOUR): Triage => ({ section, rule, last });
const view = (section: string, reason: string, more: Partial<TodoSummary> = {}): TodoSummary => ({ phase: "todo", section, reason, rank: 0, ...more });
const runs = (more: Partial<RunFacts> = {}): RunFacts => ({ missing: false, usageLimit: false, waits: false, transient: false, ...more });

describe("legacyOf", () => {
  it("files every view section where today's page draws it", () => {
    const got = Object.fromEntries(TODO_LIST_SECTIONS.map((s) => [s, legacyOf(view(s, "rest"))]));
    expect(got).toEqual({
      needs_you: "waiting",
      can_wait: "waiting",
      working: "working",
      waiting: "parked",
      todo: "other",
      just_finished: "just_done",
      someday: "other",
      done: "done",
    });
    expect(legacyOf(view("todo", "rest", { inactive: "2026-11-08T12:00:00Z" }))).toBe("shelved");
  });

  it("reads only sections and reasons this web knows", () => {
    expect(knownView(view("todo", "rest"))).toBe(true);
    expect(knownView(view("later", "rest"))).toBe(false);
    expect(knownView(view("todo", "a_newer_reason"))).toBe(false);
    expect(knownView(undefined)).toBe(false);
    expect(classify(local("other", "rest"), view("todo", "a_newer_reason"), runs(), NOW)).toEqual({ kind: "unknown" });
  });
});

describe("the decided differences: one case each, and its look-alike that is not one", () => {
  const cases: { why: ByDesign; local: Triage; view: TodoSummary; runs?: RunFacts; not: { local: Triage; view: TodoSummary; runs?: RunFacts } }[] = [
    // row 1: the web files a failed start in Done after 48 h; the hub keeps it open in To do
    { why: "failed_start", local: local("done", "failed_start"), view: view("todo", "rest", { inactive: "2026-11-08T00:00:00Z" }), not: { local: local("done", "closed"), view: view("todo", "rest") } },
    // 3.4: the row carries its step's decision
    { why: "steps", local: local("shelved", "stale"), view: view("needs_you", "owner_decides", { line: { key: "decision", args: { step: "Book the ryokan" } } }), not: { local: local("shelved", "stale"), view: view("needs_you", "owner_decides", { line: { key: "decision" } }) } },
    // D11: a run that waits on another Agent is Waiting, not Working
    { why: "run_waits", local: local("working", "agent_running"), view: view("waiting", "someone_else"), runs: runs({ waits: true }), not: { local: local("working", "agent_running"), view: view("waiting", "someone_else") } },
    // D11: a model failure the hub retries is Working, not cut off
    { why: "run_waits", local: local("waiting", "agent_asks"), view: view("working", "background"), runs: runs({ transient: true }), not: { local: local("waiting", "agent_asks"), view: view("working", "background") } },
    // row 10: a usage limit waits for its reset
    { why: "usage_limit", local: local("other", "rest"), view: view("waiting", "usage_limit"), runs: runs({ usageLimit: true }), not: { local: local("other", "rest"), view: view("waiting", "usage_limit") } },
    // row 4: a run that ended on a question in its text can wait
    { why: "awaits_reply", local: local("just_done", "finished_unseen"), view: view("can_wait", "awaits_reply"), not: { local: local("just_done", "finished_unseen"), view: view("can_wait", "can_wait") } },
    // row 9: accepted work nobody has seen stays Just finished
    { why: "finished_unseen", local: local("done", "accepted"), view: view("just_finished", "finished_unseen"), not: { local: local("done", "closed"), view: view("just_finished", "finished_unseen") } },
    // row 9: 24 h in Just finished after it was seen
    { why: "finished_recent", local: local("done", "seen"), view: view("just_finished", "finished_recent"), not: { local: local("done", "closed"), view: view("just_finished", "finished_recent") } },
    // row 11, rule 9a: seen over a day ago and open is a To do
    { why: "finished_seen", local: local("done", "seen"), view: view("todo", "finished_seen"), not: { local: local("done", "closed"), view: view("todo", "finished_seen") } },
    // row 12: Someday, which the web may shelve
    { why: "someday", local: local("shelved", "stale"), view: view("someday", "deferred"), not: { local: local("just_done", "finished_unseen"), view: view("someday", "deferred") } },
    // row 13: the Inactive mark the hub withholds (a restore of its own, a computer that does not answer)
    { why: "inactive", local: local("shelved", "stale"), view: view("todo", "rest"), not: { local: local("working", "agent_running"), view: view("todo", "rest") } },
  ];

  for (const c of cases) {
    it(`${c.why}: ${c.local.section}/${c.local.rule} → ${c.view.section}/${c.view.reason}`, () => {
      expect(classify(c.local, c.view, c.runs ?? runs(), NOW)).toEqual({ kind: "by_design", why: c.why });
      expect(classify(c.not.local, c.not.view, c.not.runs ?? runs(), NOW)).toEqual({ kind: "mismatch" });
    });
  }

  it("covers every entry of the table", () => {
    expect([...new Set(cases.map((c) => c.why))].sort()).toEqual(BY_DESIGN.map((d) => d.why).sort());
  });

  it("reads Parked in Waiting and Can wait under Needs you as the same", () => {
    expect(classify(local("parked", "parked"), view("waiting", "parked"), runs(), NOW)).toEqual({ kind: "same" });
    expect(classify(local("parked", "pr_open"), view("waiting", "pr_open"), runs(), NOW)).toEqual({ kind: "same" });
    expect(classify(local("waiting", "owner_decides"), view("can_wait", "can_wait"), runs(), NOW)).toEqual({ kind: "same" });
  });
});

describe("what explains a difference outside the table", () => {
  const working = view("working", "agent_running", { next_change_at: new Date(NOW + 10 * HOUR).toISOString() });

  it("a run the web has no session of", () => {
    expect(classify(local("other", "rest"), working, runs({ missing: true }), NOW)).toEqual({ kind: "client_gap" });
  });

  it("a view that time alone changes within two minutes, or already has", () => {
    const at = (ms: number) => view("working", "agent_running", { next_change_at: new Date(NOW + ms).toISOString() });
    expect(classify(local("other", "rest"), at(BOUNDARY_MS - 1000), runs(), NOW)).toEqual({ kind: "boundary" });
    expect(classify(local("other", "rest"), at(-HOUR), runs(), NOW)).toEqual({ kind: "boundary" });
    expect(classify(local("other", "rest"), at(BOUNDARY_MS + 1000), runs(), NOW)).toEqual({ kind: "mismatch" });
    expect(classify(local("other", "rest"), working, runs(), NOW)).toEqual({ kind: "mismatch" });
  });
});

describe("runFacts", () => {
  const todo = (sessionIds: string[]): Todo => ({
    id: "todo_r1",
    title: "Compare the hotel prices",
    created_at: "2026-11-10T08:00:00Z",
    updated_at: "2026-11-10T08:00:00Z",
    agents: sessionIds.map((sid) => ({ machine_id: "m1", session_id: sid, linked_at: "2026-11-10T08:00:00Z" })),
  });
  const session = (sid: string, more: Partial<Session>): Session => ({ machine_id: "m1", session_id: sid, state: "idle", status: "idle", ...more }) as Session;

  it("reads each linked run's session", () => {
    const lookup = makeSessionLookup([
      session("s-wait", { work: { phase: "blocked", on: "agent" } }),
      session("s-retry", { work: { phase: "blocked", on: "transient", wake_at: "2026-11-10T12:00:10Z" } }),
      session("s-limit", { turn_error: "limit", turn_error_kind: "usage_limit", turn_error_resets_at: "2026-11-10T13:00:00Z" }),
    ]);
    expect(runFacts(todo(["s-wait", "s-retry", "s-limit"]), lookup, NOW)).toEqual({ missing: false, usageLimit: true, waits: true, transient: true });
    expect(runFacts(todo(["s-gone"]), lookup, NOW)).toEqual({ missing: true, usageLimit: false, waits: false, transient: false });
  });

  it("leaves out released runs and a run that waits on the owner", () => {
    const lookup = makeSessionLookup([session("s-ask", { state: "needs_you", work: { phase: "blocked", on: "agent" } })]);
    const released = { ...todo(["s-gone"]), agents: [{ machine_id: "m1", session_id: "s-gone", linked_at: "2026-11-10T08:00:00Z", released_at: "2026-11-10T09:00:00Z" }] };
    expect(runFacts(released, lookup, NOW).missing).toBe(false);
    expect(runFacts(todo(["s-ask"]), lookup, NOW).waits).toBe(false);
  });
});

describe("restoredAt", () => {
  const todo = (more: Partial<Todo>): Todo => ({ id: "todo_x1", title: "Renew the library card", created_at: "2026-10-01T08:00:00Z", updated_at: "2026-10-01T08:00:00Z", ...more });

  it("takes the latest of this browser's restore, the hub's and a Someday that has passed", () => {
    expect(restoredAt(todo({}), 0, NOW)).toBe(0);
    expect(restoredAt(todo({ restored_at: "2026-11-09T08:00:00Z" }), NOW - 2 * HOUR, NOW)).toBe(NOW - 2 * HOUR);
    expect(restoredAt(todo({ restored_at: "2026-11-10T11:30:00Z" }), NOW - 2 * HOUR, NOW)).toBe(Date.parse("2026-11-10T11:30:00Z"));
    expect(restoredAt(todo({ someday_until: "2026-11-10T10:00:00Z" }), 0, NOW)).toBe(Date.parse("2026-11-10T10:00:00Z"));
    expect(restoredAt(todo({ someday_until: "2026-11-11T10:00:00Z" }), 0, NOW)).toBe(0);
    expect(restoredAt(todo({ someday_until: "9999-12-31T00:00:00Z" }), 0, NOW)).toBe(0);
  });
});
