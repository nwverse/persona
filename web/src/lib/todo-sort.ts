// Sorting the unsorted to-dos into knowledge-base project nodes in one go (#13026, docs/assistant/todos.md "Sorting
// into projects"): the hub's suggestions, the owner's picks, one batch written, and its Undo.
import { api } from "./api";
import type { Todo } from "./todos";

/** One to-do with what the hub suggests for it: a project's slug, or Temporary; neither is no suggestion. */
export interface SortSuggestion {
  todo: string;
  title: string;
  detail?: string;
  project: string;
  project_name?: string;
  temporary?: boolean;
  confidence: number;
  reason?: string;
  by?: string;
  preselect: boolean;
}

export interface SortSuggestions {
  items: SortSuggestion[];
  projects: { slug: string; name: string }[];
  /** used, unavailable, failed, or none */
  model: string;
  unsorted: number;
  sorted: number;
}

export interface SortMove {
  id: string;
  project?: string;
  temporary?: boolean;
}

export interface SortApplied {
  batch: string;
  moved: number;
  unchanged: number;
  failed?: number;
}

export interface SortUndone {
  batch: string;
  undone: number;
  skipped: number;
  outcomes: { todo: string; title?: string; outcome: string }[];
}

/** The target a pick names: "" none, TEMPORARY the one-off mark, else a project's slug. */
export const TEMPORARY = "\u0000temporary";

/** Unsorted: open, in no project, not marked Temporary, not a concern; the hub counts by the same rule
 *  (server/internal/assistant/todo_sort.go unsortedTodo). */
export function isUnsorted(td: Pick<Todo, "done_at" | "project" | "temporary" | "kind">): boolean {
  return !td.done_at && !td.project && !td.temporary && td.kind !== "concern";
}

export function loadSortSuggestions(signal?: AbortSignal): Promise<SortSuggestions> {
  return api<SortSuggestions>("/todo-sort/suggest", { method: "POST", json: {}, signal, timeout: 45_000 });
}

export function applySort(items: SortMove[]): Promise<SortApplied> {
  return api<SortApplied>("/todo-sort/apply", { method: "POST", json: { items } });
}

export function undoSort(batch: string): Promise<SortUndone> {
  return api<SortUndone>(`/todo-sort/batches/${encodeURIComponent(batch)}/undo`, { method: "POST", json: {} });
}

/** What the owner chose for one to-do: ticked or not, and where (the suggestion's, or changed). */
export interface SortPick {
  on: boolean;
  target: string;
}

const targetOf = (it: SortSuggestion) => (it.temporary ? TEMPORARY : it.project);

/** A sure suggestion starts ticked; one with no target never is. */
export function initialPicks(items: SortSuggestion[]): Map<string, SortPick> {
  return new Map(items.map((it) => [it.todo, { on: it.preselect && targetOf(it) !== "", target: targetOf(it) }]));
}

/** What is written: the ticked to-dos with a target. */
export function chosenMoves(items: SortSuggestion[], picks: Map<string, SortPick>): SortMove[] {
  return items.flatMap((it) => {
    const p = picks.get(it.todo);
    if (!p?.on || !p.target) return [];
    return [p.target === TEMPORARY ? { id: it.todo, temporary: true } : { id: it.todo, project: p.target }];
  });
}

/** How sure, in words: sure (≥ 0.8), fairly sure (≥ 0.6), unsure. */
export function sureness(c: number): "sure" | "fairly" | "unsure" {
  return c >= 0.8 ? "sure" : c >= 0.6 ? "fairly" : "unsure";
}
