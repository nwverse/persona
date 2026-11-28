import { api, errorText } from "../../lib/api";
import { stampedHeaders } from "../../lib/manager-choice";
import type { TodoClosedCursor } from "../../lib/store";
import { isClosedForHistory, type Todo } from "../../lib/todos";

type TodoPageAnswer = {
  todos: Todo[];
  next_cursor?: string;
  has_more: boolean;
};

/** Walk a bounded number of pages per manager. A page with only duplicates may lead to an older one. */
export async function readOlderClosed(
  starts: [string, TodoClosedCursor][],
  currentIds: () => ReadonlySet<string> | undefined,
) {
  const cursors: Record<string, TodoClosedCursor> = {};
  const added = new Map<string, Todo>();
  const errors: string[] = [];
  for (const [manager, start] of starts) {
    let cursor = start.next_cursor;
    for (let page = 0; cursor && page < 3; page++) {
      try {
        const query = new URLSearchParams({ limit: "50", cursor });
        const answer = await api<TodoPageAnswer>(`/todos?${query}`, { headers: stampedHeaders(manager) });
        const known = currentIds();
        if (!known) return;
        const before = added.size;
        for (const todo of answer.todos ?? []) {
          if (isClosedForHistory(todo) && !known.has(todo.id) && !added.has(todo.id)) added.set(todo.id, todo);
        }
        cursor = answer.has_more ? answer.next_cursor || "" : "";
        if (added.size > before) break;
      } catch (reason) {
        if (!currentIds()) return;
        errors.push(errorText(reason));
        break;
      }
    }
    // Keep the last successful cursor after a failure and still attempt the other managers.
    cursors[manager] = { next_cursor: cursor, has_more: Boolean(cursor) };
  }
  return { rows: [...added.values()], cursors, error: errors.length ? errors.join(" · ") : null };
}
