import { openTask } from "../../lib/handoff";
import type { RunningItem } from "../../lib/assistant-entries";
import { compareWaitAge } from "../../lib/task-wait";
import { openMergePRs } from "../../lib/todo-triage";
import { mostUrgentAgent, type Todo } from "../../lib/todos";
import { taskSessionLookup, useStore } from "../../lib/store";
import { agentRowKey, agentRowLabel, todoChipLabel, todoChipState } from "../../lib/task-row-status";
import { Icon } from "../../ui/icons";
import { useNow } from "../../ui/atoms";
import { TaskWaitInfo } from "./TaskWaitInfo";

// The Finished tray's rows (#10922) load in this chunk too: one lazy entry for the trays drawn on intent, not one each.
export { AcceptanceTaskRows, FinishedTaskRows } from "./FinishedTaskRows";

/** The tray already loads waiting rows on demand; keep their ordering on that same lazy boundary. */
export function ParkedTaskRows({ items }: { items: readonly RunningItem[] }) {
  const now = useNow(60_000);
  return <>{items.filter((item) => item.kind === "task").sort((a, b) => compareWaitAge(a.todo, b.todo, now))
    .map((item) => <ParkedTaskRow key={item.key} todo={item.todo} countsOpen={item.countsOpen} />)}</>;
}

/** Waiting remains a subsection of In progress, including the hub's named run waits (#10411). */
export function ParkedTaskRow({ todo, countsOpen }: { todo: Todo; countsOpen: boolean }) {
  const prs = openMergePRs(todo, Date.now());
  const lead = useStore((s) => mostUrgentAgent(todo, taskSessionLookup(s))?.session);
  const state = agentRowKey({ live: false, status: "waiting", lead, todo: true, starting: false });
  const chip = todoChipState({ toStart: false, status: "waiting", done: false, stage: todo.stage, pr: todo.pr_url, todo });
  return <div data-parked-task-row={todo.id} data-counts-open={countsOpen ? "true" : "false"}
    data-pr-waiting-row={prs.length ? todo.id : undefined} className="flex items-start gap-2 px-3 py-2.5">
    <span data-task-mark="waiting" className="mt-0.5 shrink-0 text-ink-3"><Icon name={prs.length ? "pullRequest" : "clock"} /></span>
    <div className="min-w-0 flex-1">
      <button type="button" data-pr-task-link={prs.length ? todo.id : undefined} onClick={() => openTask(todo.id)}
        className="block min-w-11 max-w-full truncate text-left text-sm font-medium hover:underline pointer-coarse:min-h-11">{todo.title}</button>
      {lead && <p data-task-status={state} className="break-words text-xs text-ink-2">{agentRowLabel(state, lead)}</p>}
      {chip && <p data-task-todo-state className="break-words text-xs text-ink-2">{todoChipLabel(chip)}</p>}
      {todo.supervision?.state !== "parked" && lead?.work?.wait && <p data-task-run-wait className="break-words text-xs text-ink-2">{lead.work.wait}</p>}
      <TaskWaitInfo todo={todo} merges={prs} />
    </div>
  </div>;
}
