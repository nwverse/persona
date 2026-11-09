// The task board (iOS TaskBoardSheet): one conversation's tasks and the agents its dock shows, by where they are —
// Needs you / Awaiting review / Failed to start / Working / Finished / Stopped / Done, what the user must act on first (#4896). Pure, so the dock's
// All N and the board count the same things.
import { awaitsReview, type DockAgent, type Todo, type TodoStatus } from "./todos";
import { byTime } from "./times";

export type BoardSectionKey = "waiting" | "review" | "failed" | "working" | "unread" | "stopped" | "done";

export interface BoardRow {
  todo: Todo;
  status: TodoStatus;
}

export interface BoardSection {
  key: BoardSectionKey;
  rows: BoardRow[];
  agents: DockAgent[];
}

export type AutoTodoLane = "needs_you" | "working" | "review" | "idle";

/** Which section a task is in: by its status, but a supervised one awaiting the user's review in Awaiting review, not with the
 *  stopped or finished-unseen ones (#4896). The web keeps Interrupted (interrupted) apart; it waits for the user like Needs you. */
export function boardSectionOf({ todo, status }: BoardRow): BoardSectionKey {
  if (awaitsReview(todo, status)) return "review";
  switch (status) {
    case "needs_you":
    case "interrupted":
    case "usage_limit":
      return "waiting";
    case "working":
    case "not_started":
      return "working";
    case "unread":
      return "unread";
    case "follow_up":
      return "stopped";
    case "done":
      return "done";
    case "failed":
      return "failed";
  }
}

/** The workflow board's four automatic lanes reuse the task board's sections:
 * waiting and failed need the user, working includes tasks that have not started yet, review includes finished-unseen
 * and supervised awaiting-review tasks, and stopped tasks are idle. Done tasks have no automatic lane. */
export function autoTodoLaneOf(todo: Todo, status: TodoStatus): AutoTodoLane | undefined {
  switch (boardSectionOf({ todo, status })) {
    case "waiting":
    case "failed":
      return "needs_you";
    case "working":
      return "working";
    case "review":
    case "unread":
      return "review";
    case "stopped":
      return "idle";
    case "done":
      return undefined;
  }
}

export const BOARD_ORDER: BoardSectionKey[] = ["waiting", "review", "failed", "working", "unread", "stopped", "done"];

/** The board's sections that have something, in order; tasks newest first, agents waiting ones under "Needs you", the rest
 *  under "Working" (as the dock orders them). */
export function boardSections(rows: BoardRow[], agents: DockAgent[]): BoardSection[] {
  const by = new Map<BoardSectionKey, BoardSection>(BOARD_ORDER.map((key) => [key, { key, rows: [], agents: [] }]));
  const newest = [...rows].sort((a, b) => byTime(b.todo.created_at, a.todo.created_at));
  for (const r of newest) by.get(boardSectionOf(r))!.rows.push(r);
  for (const a of agents) by.get(a.state === "needs_you" ? "waiting" : "working")!.agents.push(a);
  return BOARD_ORDER.map((k) => by.get(k)!).filter((s) => s.rows.length > 0 || s.agents.length > 0);
}
