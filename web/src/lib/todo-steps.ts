// A to-do's steps on the To-dos row and page (#11858): the to-dos whose `part_of` names it, as the hub counts them on
// the row's view (`view.steps`, TodoStepCount; server/internal/assistant/todo/projection/view.go withSteps: the closed
// ones of all, a failed start open unless its work finished in a related to-do). iOS draws the same count ("Steps 2/5").
// The web keeps no count of its own: a row without the view (an older manager) says what it said before.

import { t } from "../i18n";
import type { TodoStepCount } from "./todo-view-wire";
import { acceptedAtRest, type Todo, type TodoStatus } from "./todos";

/** What the status line says about an open to-do's steps, each part in place of or beside today's words. */
export interface StepsWords {
  /** In place of the status word, for a to-do no Agent of its own is on: "Steps 3/9", or "9/9 steps done". */
  label?: string;
  /** In place of "Not handed to an agent yet" beside `label`: "Next step not arranged yet" once every step is closed,
   *  "" (nothing) while some are open, since its steps were handed out. */
  tail?: string;
  /** After the Agent count, for a to-do with an Agent of its own: "Steps 3/9". */
  after?: string;
}

/** The hub's count of an open to-do's steps; undefined when it has none, is closed, or its row has no view. */
export function todoSteps(todo: Todo): TodoStepCount | undefined {
  const steps = todo.view?.steps;
  if (todo.done_at || !steps || !(steps.total > 0)) return undefined;
  return steps;
}

/**
 * What an open to-do's status line says about its steps, from `todoSteps` and its status (todoStatus):
 *  - no Agent of its own (`not_started`): "Steps 3/9" in place of "Not started", nothing in place of "Not handed to an
 *    agent yet"; once every step is closed, "9/9 steps done" and "Next step not arranged yet";
 *  - otherwise its status stays, "Steps 3/9" after its Agent count; so does an accepted one at rest (acceptedAtRest):
 *    its supervisor settled it, so it keeps reading Accepted and nothing claims its next step is unplanned.
 * Undefined when it has no steps the hub counted: the line is as before.
 */
export function stepsWords(todo: Todo, status: TodoStatus): StepsWords | undefined {
  const steps = todoSteps(todo);
  if (!steps) return undefined;
  const count = t("todos.steps_count", { done: steps.done, total: steps.total });
  if (status !== "not_started" || acceptedAtRest(todo, status)) return { after: count };
  if (steps.done >= steps.total) return { label: t("todos.steps_all_done", { done: steps.done, total: steps.total }), tail: t("todos.steps_next_unplanned") };
  return { label: count, tail: "" };
}
