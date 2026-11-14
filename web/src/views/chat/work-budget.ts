// An opened work summary draws its steps a screenful at a time (audit item 90): a long run's fold (650 commands with
// notes between, about 8,000 elements) mounted in one commit froze a phone for most of a second after the tap. The
// outermost open group keeps a budget of steps: the first screenful is drawn with the tap, and the rest follows in
// batches while the page is idle, each its own interruptible render, so the next tap or keystroke goes first. Nested
// groups draw what their outer group's budget leaves them. A live group draws everything (its newest step must show at
// once), and a search hit inside a group draws everything at once, so the row it names is there to land on.
import { startTransition, useEffect, useState } from "react";
import type { WorkNode } from "../../lib/work-groups";
import { whenIdle } from "../../ui/stream/idle";

/** Steps drawn with the tap: more than a tall window's worth of rows. */
export const FIRST_STEPS = 60;
/** Steps each idle batch adds: one batch stays well under a long task on a slow phone. */
export const STEP_BATCH = 60;
/** The longest a batch waits for an idle moment (a busy page still gets the rest). */
const BATCH_WAIT_MS = 250;

const weights = new WeakMap<WorkNode, number>();

/** What drawing a node costs, in steps: a group's header and every step under it, nested groups' too. */
export function stepWeight(n: WorkNode): number {
  if (n.kind !== "group") return 1;
  let w = weights.get(n);
  if (w === undefined) {
    w = 1;
    for (const c of n.children) w += stepWeight(c);
    weights.set(n, w);
  }
  return w;
}

/** The nodes a `limit` of steps reaches, and what each group among them may draw of its own (undefined: all of it). */
export function withinBudget(nodes: WorkNode[], limit: number | undefined): { shown: WorkNode[]; limits: Map<WorkNode, number> | null } {
  if (limit === undefined) return { shown: nodes, limits: null };
  const shown: WorkNode[] = [];
  const limits = new Map<WorkNode, number>();
  let left = limit;
  for (const n of nodes) {
    if (left <= 0) break;
    const w = stepWeight(n);
    shown.push(n);
    // a group the budget only reaches into: its header and as many of its steps as are left
    if (n.kind === "group" && w > left) limits.set(n, left - 1);
    left -= w;
  }
  return { shown, limits };
}

/**
 * The outermost open group's budget: how many steps it draws now (undefined: all of them), and `showAll` for a search
 * hit. It starts at the first screenful each time the group opens and grows by a batch whenever the page is idle until
 * it covers every step, those a fetched fold brings in later too. `live`: everything, and it stays that way after the
 * run ends while the group is open (what was drawn is never taken back).
 */
export function useStepBudget(open: boolean, total: number, live: boolean): { drawn: number | undefined; showAll: () => void } {
  const [budget, setBudget] = useState(live ? Infinity : FIRST_STEPS);
  // a search hit asked for every step: until the group closes (the hit may open it in the same update)
  const [all, setAll] = useState(false);
  const [was, setWas] = useState(open);
  if (was !== open) {
    // adjusted while rendering: the frame the group opens in draws only the first screenful
    setWas(open);
    if (open) setBudget(live ? Infinity : FIRST_STEPS);
    else setAll(false);
  } else if (open && live && budget !== Infinity) setBudget(Infinity);
  const whole = all || budget >= total;
  useEffect(() => {
    if (!open || whole) return;
    return whenIdle(() => startTransition(() => setBudget((b) => b + STEP_BATCH)), BATCH_WAIT_MS);
  }, [open, whole, budget]);
  return { drawn: open && !whole ? budget : undefined, showAll: () => setAll(true) };
}
