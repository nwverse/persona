// The hub's view of a to-do read in the web's own words (#10226; docs/web/web.md "Developing Persona"): which of today's
// sections (lib/todo-triage.ts) a row's `view` stands for, and which differences between the two the redesign decided
// (docs/design/proposals/todos-redesign.md 6.3; docs/assistant/todo-status-sop.md "The hub's view"), so the shadow
// (view-shadow.ts) reports only the ones nothing explains. Pure: no state, no clock of its own.

import { TODO_LIST_SECTIONS, TODO_REASONS, type TodoSummary } from "../../../lib/todo-view-wire";
import type { Triage, TriageRule, TriageSection } from "../../../lib/todo-triage";
import type { SessionLookup, Todo } from "../../../lib/todos";
import { stateOf } from "../../../lib/format";
import { usageLimitAgent } from "../../../lib/usage-limit";

const SECTIONS: ReadonlySet<string> = new Set(TODO_LIST_SECTIONS);
const REASONS: ReadonlySet<string> = new Set(TODO_REASONS);

/** A view this web can read: a row with a newer hub's section or reason is drawn as if it had none, and counted apart. */
export function knownView(view: TodoSummary | undefined): view is TodoSummary {
  return !!view && SECTIONS.has(view.section) && REASONS.has(view.reason);
}

/** Today's section for each of the view's: Waiting is the web's Parked (a parked hold and an open PR wait there, 6.3
 *  rows 8 and 10), Can wait is listed under Needs you, Someday is open work, and To do is Other open, or Shelved while
 *  it carries the Inactive mark. */
const LEGACY: Record<(typeof TODO_LIST_SECTIONS)[number], TriageSection> = {
  needs_you: "waiting",
  can_wait: "waiting",
  working: "working",
  waiting: "parked",
  todo: "other",
  just_finished: "just_done",
  someday: "other",
  done: "done",
};

export function legacyOf(view: TodoSummary): TriageSection | undefined {
  if (view.section === "todo" && view.inactive) return "shelved";
  return LEGACY[view.section as keyof typeof LEGACY];
}

/** What the to-do's runs say that today's rules read differently from the hub's, from the same sessions. */
export interface RunFacts {
  /** A linked run the session lookup does not have: the web reads it as offline, the hub may list it. */
  missing: boolean;
  /** A run stopped on a usage limit that has not reset yet. */
  usageLimit: boolean;
  /** A run whose turn ended blocked on another Agent or an outside event. */
  waits: boolean;
  /** A run whose moment's model failure the hub retries ("Continue"). */
  transient: boolean;
}

export function runFacts(todo: Todo, lookup: SessionLookup | undefined, now: number): RunFacts {
  const out: RunFacts = { missing: false, usageLimit: false, waits: false, transient: false };
  for (const a of todo.agents ?? []) {
    if (a.released_at || a.start_error) continue;
    const s = lookup?.(a.machine_id, a.session_id);
    if (!s) {
      out.missing = true;
      continue;
    }
    if (usageLimitAgent(s, now)) out.usageLimit = true;
    const st = stateOf(s);
    if (s.work?.phase !== "blocked" || st === "needs_you" || st === "unread") continue;
    if (s.work.on === "agent" || s.work.on === "external") out.waits = true;
    if (s.work.on === "transient") out.transient = true;
  }
  return out;
}

function ms(at: string | null | undefined): number {
  const v = at ? Date.parse(at) : NaN;
  return Number.isNaN(v) ? 0 : v;
}

/** The restore the shadow gives today's triage: this browser's own, the hub's (`restored_at`), and a Someday that has
 *  passed, which the hub counts as activity too, so both read the same moves. */
export function restoredAt(todo: Todo, local: number, now: number): number {
  const back = ms(todo.someday_until);
  return Math.max(local, ms(todo.restored_at), back && back <= now ? back : 0);
}

/** A difference the redesign decided, by the 6.3 row (or decision) that changed today's rule. */
export type ByDesign =
  | "failed_start"
  | "steps"
  | "run_waits"
  | "usage_limit"
  | "awaits_reply"
  | "finished_unseen"
  | "finished_recent"
  | "finished_seen"
  | "someday"
  | "inactive";

const FINISHED_DONE: ReadonlySet<TriageRule> = new Set(["seen", "accepted", "deployed"]);
const AT_REST: ReadonlySet<TriageRule> = new Set(["rest", "stale"]);

/** The decided differences, first that holds: each names the view it expects and the web's (section, rule) it replaces.
 *  Parked in Waiting and Can wait under Needs you need no entry: the mapping above files them where the web does. */
export const BY_DESIGN: readonly { why: ByDesign; holds: (local: Triage, view: TodoSummary, runs: RunFacts) => boolean }[] = [
  // D4, row 1: a failed start stays open, Needs you for 48 h from the failure, then To do; the web counts its 48 h from
  // the last activity and files it in Done after them
  { why: "failed_start", holds: (l, v) => l.rule === "failed_start" && (v.reason === "failed_start" || v.section === "todo") },
  // 3.4 "Steps roll up": the row carries its most urgent step's state (its line names the step); the web reads it alone
  { why: "steps", holds: (_l, v) => v.line?.args?.step !== undefined },
  // D11: a run that waits on another Agent or an outside event leaves Working; a moment's model failure the hub retries
  // is Working (background), which the web reads as cut off
  {
    why: "run_waits",
    holds: (l, v, r) => (l.rule === "agent_running" && r.waits && v.section !== "working") || (l.rule === "agent_asks" && r.transient && v.section === "working"),
  },
  // row 10: a usage limit waits for its reset, after finished work (row 9); the web has no rule for it and files the
  // to-do at rest, an unread run's finish included
  { why: "usage_limit", holds: (l, v, r) => r.usageLimit && AT_REST.has(l.rule) && (v.reason === "usage_limit" || v.reason === "finished_unseen") },
  // row 4: a run that ended on a plain-text question is Can wait, not finished work
  { why: "awaits_reply", holds: (l, v) => v.reason === "awaits_reply" && (l.rule === "finished_unseen" || l.rule === "seen" || AT_REST.has(l.rule)) },
  // row 9: accepted or deployed work nobody has seen stays in Just finished however long (the web: Done after 24 h)
  { why: "finished_unseen", holds: (l, v) => v.reason === "finished_unseen" && (l.rule === "accepted" || l.rule === "deployed") && l.section === "done" },
  // row 9: finished work stays in Just finished for 24 h after it was seen (the web: Done once seen)
  { why: "finished_recent", holds: (l, v) => v.reason === "finished_recent" && FINISHED_DONE.has(l.rule) && l.section === "done" },
  // row 11, rule 9a: finished work seen over 24 h ago and still open is a To do to tick, never Done
  { why: "finished_seen", holds: (l, v) => v.reason === "finished_seen" && FINISHED_DONE.has(l.rule) && l.section === "done" },
  // row 12 (D7): Someday is the hub's alone; the web may shelve a to-do put off for long
  { why: "someday", holds: (l, v) => v.section === "someday" && AT_REST.has(l.rule) },
  // row 13: Inactive is a mark the hub sets (its restores, a computer that does not answer, the owner's zone for a due
  // day), not the web's Shelved section
  { why: "inactive", holds: (l, v) => v.section === "todo" && AT_REST.has(l.rule) },
];

/** How a row's view compares with today's triage of the same row. */
export type ViewVerdict =
  | { kind: "same" }
  | { kind: "by_design"; why: ByDesign }
  /** A section or reason this web does not know: a newer hub's. */
  | { kind: "unknown" }
  /** The web lacks a run's session (outside the Agents window), so it reads the run as offline. */
  | { kind: "client_gap" }
  /** The view changes by time alone within BOUNDARY_MS, or already has (`next_change_at`): the hub sends no new list
   *  for that, and the web read its clock a moment later. */
  | { kind: "boundary" }
  | { kind: "mismatch" };

export const BOUNDARY_MS = 2 * 60_000;

function outdated(view: TodoSummary, now: number): boolean {
  const at = ms(view.next_change_at);
  return at > 0 && at <= now + BOUNDARY_MS;
}

export function classify(local: Triage, view: TodoSummary, runs: RunFacts, now: number): ViewVerdict {
  if (!knownView(view)) return { kind: "unknown" };
  if (legacyOf(view) === local.section) return { kind: "same" };
  const hit = BY_DESIGN.find((d) => d.holds(local, view, runs));
  if (hit) return { kind: "by_design", why: hit.why };
  if (runs.missing) return { kind: "client_gap" };
  if (outdated(view, now)) return { kind: "boundary" };
  return { kind: "mismatch" };
}
