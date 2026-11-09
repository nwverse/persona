// Auto flow (#2648): the board's optional second view. The manual columns stay what they are; these lanes are
// computed from the agents a card has, so a card moves as its agents change, and nothing is persisted for them.
// Needs you → Working → To review → Idle; reviewing and clearing a card is the existing Archive action.
import { stateOf } from "./format";
import type { BoardCard } from "./workspace-board";
import type { Session } from "./types";

// To-do cards use the task board's status mapping so both automatic views share one classification.
export { autoTodoLaneOf } from "./task-board";
export type { AutoTodoLane } from "./task-board";

export type AutoLane = "needs_you" | "working" | "review" | "idle";
export const AUTO_LANES: readonly AutoLane[] = ["needs_you", "working", "review", "idle"];

/** A card's lane: the most urgent thing any of its agents says. No agents, or only offline ones: idle. */
export function autoLaneOf(sessions: readonly Session[]): AutoLane {
  if (sessions.some((s) => stateOf(s) === "needs_you")) return "needs_you";
  if (sessions.some((s) => stateOf(s) === "working")) return "working";
  if (sessions.some((s) => stateOf(s) !== "offline")) return "review";
  return "idle";
}

/** Latest agent activity, for ordering; cards without any go last. */
export function autoActivity(sessions: readonly Session[]): number {
  let latest = 0;
  for (const s of sessions) {
    const at = Date.parse(s.updated_at ?? s.state_since ?? "");
    if (Number.isFinite(at) && at > latest) latest = at;
  }
  return latest;
}

/** The session to open from a card: the one needing you, else a working one, else the most recently active. */
export function autoFocusSession(sessions: readonly Session[]): Session | undefined {
  const needy = sessions.filter((s) => stateOf(s) === "needs_you");
  const working = sessions.filter((s) => stateOf(s) === "working");
  const pool = needy.length ? needy : working.length ? working : sessions;
  return [...pool].sort((a, b) => autoActivity([b]) - autoActivity([a]))[0];
}

/** One lane's cards: most recent agent activity first, then pinned, then title so the order is stable. */
export function autoSort(cards: readonly BoardCard[], sessionsOf: (card: BoardCard) => readonly Session[]): BoardCard[] {
  return [...cards].sort((a, b) => {
    const activity = autoActivity(sessionsOf(b)) - autoActivity(sessionsOf(a));
    if (activity) return activity;
    if (a.pinned !== b.pinned) return a.pinned ? -1 : 1;
    return a.title.localeCompare(b.title) || a.id.localeCompare(b.id);
  });
}

/** The lanes and their cards for the auto view, after the board's own search/machine/archived filters. */
export function autoLanes(
  cards: readonly BoardCard[],
  sessionsOf: (card: BoardCard) => readonly Session[],
): { lane: AutoLane; cards: BoardCard[] }[] {
  return AUTO_LANES.map((lane) => ({
    lane,
    cards: autoSort(cards.filter((c) => autoLaneOf(sessionsOf(c)) === lane), sessionsOf),
  }));
}
