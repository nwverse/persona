import type { TodoBoardProjection } from "../../lib/todos";
import { api, credentialOwner } from "../../lib/api";
import { chosenManager, shownManager, stampedHeaders } from "../../lib/manager-choice";
import { currentRoster, multiManager, subscribeRoster } from "../../lib/managers";
import { applyScopedTodoBoard, getState, subscribe } from "../../lib/store";

let requested: { manager: string; source: TodoBoardProjection } | undefined;

/** The merged list contains all managers' totals. The shown assistant's counts need its own projection. */
async function refreshScopedTodoBoard(manager: string, source: TodoBoardProjection) {
  const request = requested = { manager, source };
  const owner = credentialOwner();
  try {
    const answer = await api<{ board?: unknown }>("/todos", { query: { closed_limit: "50" }, headers: stampedHeaders(manager) });
    if (requested !== request) return;
    if (getState().todoBoard === source && shownManager() === manager && credentialOwner() === owner) {
      applyScopedTodoBoard({ manager, board: validTodoBoard(answer?.board) });
    }
  } catch {
    // Keep the previous projection for this assistant on a transient failure.
  }
}

// This module first loads when the hub sends a board. Follow the assistant profile and roster as they arrive.
function syncScopedBoard() {
  const { assistant, todoBoard } = getState();
  const manager = shownManager();
  if (!todoBoard || assistant?.todos_v2 !== true || (assistant.manager && assistant.manager !== manager)) {
    requested = undefined;
    return;
  }
  if (requested?.source === todoBoard && requested.manager === manager) return;
  const roster = currentRoster();
  if (multiManager(roster) || (!roster && chosenManager())) void refreshScopedTodoBoard(manager, todoBoard);
}

subscribe(syncScopedBoard);
subscribeRoster(syncScopedBoard);

const isCount = (n: unknown): n is number => typeof n === "number" && Number.isSafeInteger(n) && n >= 0;
const areIds = (ids: unknown): boolean => Array.isArray(ids) && ids.every((id) => typeof id === "string");

export function validTodoBoard(raw: unknown): TodoBoardProjection | null {
  if (!raw || typeof raw !== "object") return null;
  const b = raw as Partial<TodoBoardProjection>;
  const c = b.counts;
  if (!c || ![c.needs_you, c.in_progress, c.pending_review, c.waiting, c.someday, c.done, c.open_total].every(isCount)) return null;
  if (![c.can_wait, c.todo, c.cancelled].every((n) => n === undefined || isCount(n))) return null;
  if (!Array.isArray(b.status_groups) || !b.status_groups.every((g) =>
    g && typeof g.key === "string" && isCount(g.total_count) && isCount(g.visible_count) &&
    Array.isArray(g.entries) && g.entries.every((e) =>
      e && typeof e.todo_id === "string" && (e.folded_count === undefined || isCount(e.folded_count)) &&
      (e.folded_child_ids == null || areIds(e.folded_child_ids)) &&
      (e.surfaced_child_ids == null || areIds(e.surfaced_child_ids))),
  )) return null;
  if (!Array.isArray(b.direction_groups) || !b.direction_groups.every((g) => {
    const c = g?.counts;
    return c && typeof g.direction_id === "string" && (g.label == null || typeof g.label === "string") &&
      areIds(g.todo_ids) && [c.needs_you, c.in_progress, c.pending_review, c.waiting, c.done, c.total].every(isCount);
  })) return null;
  if (!Array.isArray(b.sidebar_tree) || !b.sidebar_tree.every((n) =>
    n && typeof n.todo_id === "string" && typeof n.section === "string" && typeof n.item_type === "string" &&
    (n.direction_id == null || typeof n.direction_id === "string") &&
    [n.folded_count, n.needs_you_count, n.active_count].every(isCount) &&
    (n.child_ids == null || areIds(n.child_ids)) && (n.surfaced_child_ids == null || areIds(n.surfaced_child_ids)),
  )) return null;
  return b as TodoBoardProjection;
}
