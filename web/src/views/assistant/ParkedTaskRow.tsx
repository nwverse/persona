import { openTask } from "../../lib/handoff";
import { openMergePRs } from "../../lib/todo-triage";
import type { Todo } from "../../lib/todos";
import { Icon } from "../../ui/icons";
import { TaskWaitInfo } from "./TaskWaitInfo";

/** Same row contract as the separate grouping slice; usable in today's progress and PR trays without that slice. */
export function ParkedTaskRow({ todo, countsOpen }: { todo: Todo; countsOpen: boolean }) {
  const prs = openMergePRs(todo, Date.now());
  return <div data-parked-task-row={todo.id} data-counts-open={countsOpen ? "true" : "false"}
    data-pr-waiting-row={prs.length ? todo.id : undefined} className="flex items-start gap-2 px-3 py-2.5">
    <span data-task-mark="waiting" className="mt-0.5 shrink-0 text-ink-3"><Icon name={prs.length ? "pullRequest" : "clock"} /></span>
    <div className="min-w-0 flex-1">
      <button type="button" data-pr-task-link={prs.length ? todo.id : undefined} onClick={() => openTask(todo.id)}
        className="block max-w-full truncate text-left text-sm font-medium hover:underline pointer-coarse:min-h-11 pointer-coarse:min-w-11">{todo.title}</button>
      <TaskWaitInfo todo={todo} merges={prs} />
    </div>
  </div>;
}
