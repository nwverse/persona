// Developing Persona's shadow of the hub's to-do view (#10226; docs/web/web.md "Developing Persona"): its own chunk,
// started by the recorder (lib/dev-mode.ts startDevMode) and stopped with it. While the hub lists `todo_view` it reads
// every row the list carries with a view through today's triage (lib/todo-triage.ts, the tray's session lookup) and
// compares the two (view-legacy.ts classify). It draws nothing: a summary line at most once a minute, and one line per
// mismatch nothing explains, go to the recorder with ids and enum words only, never a title or any text.
//
// The triage comes through App's module (App.tsx re-exports it), never a value import of lib/todo-triage.ts: the
// recorder can load before App, so such an import would move the triage out of the first screen's chunk into its own.

import { devWord } from "../../../lib/dev-scrub";
import { getState, subscribe, taskSessionLookup } from "../../../lib/store";
import type * as TodoTriage from "../../../lib/todo-triage";
import type { Todo } from "../../../lib/todos";
import { classify, restoredAt, runFacts } from "./view-legacy";
import { viewShadowOn } from "./view-gates";

/** A change of the list, the sessions or the hub's features is compared this long after it, whatever follows. */
export const QUIET_MS = 3_000;
/** A mismatch counts once a run this much later still sees it: a session push the view has not caught up with yet
 *  (D8) or a window just ending settles before then. */
export const RECHECK_MS = 10_000;
/** A run at least this often while nothing changes (time alone moves rows), and a summary line at most this often. */
export const SUMMARY_MS = 60_000;
/** The same mismatch is noted again after this long. */
export const REPEAT_MS = 3_600_000;
/** Rows compared between two idle moments. */
const SLICE = 50;

/** Where lib/todo-restore.ts keeps this browser's restores; view-shadow.test.ts checks the two read alike. */
const RESTORED_KEY = "persona.todos.restored";

/** This browser's own restores (to-do id → when), read as lib/todo-restore.ts writes them. */
export function localRestores(): ReadonlyMap<string, number> {
  const out = new Map<string, number>();
  try {
    const raw = JSON.parse(localStorage.getItem(RESTORED_KEY) ?? "{}") as Record<string, unknown>;
    for (const [id, at] of Object.entries(raw)) if (typeof at === "number" && Number.isFinite(at)) out.set(id, at);
  } catch {
    // none
  }
  return out;
}

type ShadowState = Pick<ReturnType<typeof getState>, "todos" | "features" | "sessions" | "sessionSummaries" | "taskRows">;

export interface ShadowDeps {
  /** Writes one recorder line (lib/dev-mode.ts DevRecorder.note). */
  note: (kind: string, fields: Record<string, unknown>) => void;
  state?: () => ShadowState;
  subscribe?: (listener: () => void) => () => void;
  now?: () => number;
  restores?: () => ReadonlyMap<string, number>;
  /** Resolves when the page is idle: the rows are compared in slices between. */
  idle?: () => Promise<void>;
  /** Today's triage (lib/todo-triage.ts), as App's module re-exports it. */
  triage?: () => Promise<TodayTriage>;
}

type TodayTriage = Pick<typeof TodoTriage, "triage" | "supersededFailedStarts">;

function whenIdle(): Promise<void> {
  return new Promise((done) => (typeof requestIdleCallback === "function" ? requestIdleCallback(() => done(), { timeout: 1000 }) : setTimeout(done, 0)));
}

/** Starts comparing; returns the stop. */
export function startViewShadow(deps: ShadowDeps): () => void {
  const state = deps.state ?? getState;
  const now = deps.now ?? Date.now;
  const restores = deps.restores ?? localRestores;
  const idle = deps.idle ?? whenIdle;
  const today = deps.triage ?? (() => import("../../../App"));
  let stopped = false;
  let running = false;
  let again = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let dueAt = Infinity;
  // mismatches by key: when first seen (not yet confirmed), and when last noted
  const firstSeen = new Map<string, number>();
  const noted = new Map<string, number>();
  let summaryAt = -Infinity;
  let summaryText = "";
  let seenInputs: unknown[] = [];

  /** Runs at `at`, unless a run is due sooner. */
  const runAt = (at: number) => {
    if (stopped || (timer !== undefined && dueAt <= at)) return;
    if (timer !== undefined) clearTimeout(timer);
    dueAt = at;
    timer = setTimeout(fire, Math.max(0, at - now()));
  };
  const fire = () => {
    timer = undefined;
    dueAt = Infinity;
    if (running) {
      again = true;
      return;
    }
    running = true;
    void run()
      .catch(() => {})
      .finally(() => {
        running = false;
        if (stopped) return;
        const unsettled = [...firstSeen.values()].some((at) => now() - at < RECHECK_MS);
        runAt(now() + (again ? QUIET_MS : unsettled ? RECHECK_MS : SUMMARY_MS));
        again = false;
      });
  };

  async function run() {
    const s = state();
    if (!viewShadowOn(s.features)) {
      firstSeen.clear();
      return;
    }
    const { triage, supersededFailedStarts } = await today();
    if (stopped) return;
    const at = now();
    const rows = s.todos.filter((td) => td.view);
    const lookup = taskSessionLookup(s);
    const superseded = supersededFailedStarts(s.todos);
    const local = restores();
    const tally = { compared: 0, same: 0, by_design: {} as Record<string, number>, unknown: 0, client_gap: 0, boundary: 0, unsettled: 0, mismatch: 0 };
    const found = new Map<string, Record<string, string>>();
    const compare = (td: Todo) => {
      const view = td.view!;
      const t = triage(td, lookup, at, restoredAt(td, local.get(td.id) ?? 0, at), superseded.has(td.id));
      const v = classify(t, view, runFacts(td, lookup, at), at);
      tally.compared++;
      if (v.kind === "by_design") tally.by_design[v.why] = (tally.by_design[v.why] ?? 0) + 1;
      else if (v.kind !== "mismatch") tally[v.kind]++;
      else {
        // the hub's words are open and the id is the hub's: each recorded as a code word (anything else reads "other")
        const line = { todo: devWord(td.id), local_section: t.section, local_rule: t.rule, phase: devWord(view.phase), section: devWord(view.section), reason: devWord(view.reason) };
        found.set(Object.values(line).join("|"), line);
      }
    };
    for (let i = 0; i < rows.length; i += SLICE) {
      if (i) await idle();
      if (stopped) return;
      for (const td of rows.slice(i, i + SLICE)) compare(td);
    }
    tally.by_design = Object.fromEntries(Object.entries(tally.by_design).sort(([a], [b]) => a.localeCompare(b)));
    for (const [key, when] of noted) if (at - when >= REPEAT_MS) noted.delete(key);
    // a mismatch counts once a run RECHECK_MS later still sees it; one that went away starts over
    for (const key of firstSeen.keys()) if (!found.has(key)) firstSeen.delete(key);
    for (const [key, line] of found) {
      const first = firstSeen.get(key) ?? at;
      firstSeen.set(key, first);
      if (at - first < RECHECK_MS) {
        tally.unsettled++;
        continue;
      }
      tally.mismatch++;
      if (at - (noted.get(key) ?? -Infinity) < REPEAT_MS) continue;
      noted.set(key, at);
      deps.note("todo_view_mismatch", line);
    }
    const text = JSON.stringify(tally);
    if (text !== summaryText && at - summaryAt >= SUMMARY_MS) {
      summaryText = text;
      summaryAt = at;
      deps.note("todo_view", tally);
    }
  }

  // what a run reads: a new one of these is a change to compare
  const inputs = (s: ShadowState) => [s.todos, s.features, s.sessions, s.sessionSummaries, s.taskRows];
  const unsubscribe = (deps.subscribe ?? subscribe)(() => {
    const next = inputs(state());
    if (next.every((v, i) => v === seenInputs[i])) return;
    seenInputs = next;
    runAt(now() + QUIET_MS);
  });
  seenInputs = inputs(state());
  runAt(now() + QUIET_MS);
  return () => {
    stopped = true;
    unsubscribe();
    if (timer !== undefined) clearTimeout(timer);
    timer = undefined;
  };
}
