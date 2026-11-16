import { t } from "../../i18n";
import { openTask } from "../../lib/handoff";
import { parkedLine } from "../../lib/supervision";
import { openMergePRs, prBlockers } from "../../lib/todo-triage";
import type { Todo } from "../../lib/todos";
import { Icon } from "../../ui/icons";
import { TaskMark } from "./Tasks";

/** Loaded when Parked opens, not on the first screen. A hold never borrows an Agent's finished-turn check. */
export function ParkedTaskRow({ todo, countsOpen }: { todo: Todo; countsOpen: boolean }) {
  const prs = openMergePRs(todo, Date.now());
  return <div data-parked-task-row={todo.id} data-counts-open={countsOpen ? "true" : "false"}>
    {prs.length ? (
      <div data-pr-waiting-row={todo.id} className="flex items-start gap-2 px-3 py-2.5">
        <Icon name="pullRequest" size={14} className="mt-0.5 shrink-0 text-accent-ink" />
        <div className="min-w-0 flex-1">
          <button type="button" data-pr-task-link={todo.id} onClick={() => openTask(todo.id)} className="block max-w-full truncate text-left text-sm font-medium hover:underline">{todo.title}</button>
          {prs.map((facts) => <div key={facts.url} data-pr-waiting-url={facts.url}>
            <a href={facts.url} target="_blank" rel="noreferrer noopener" title={facts.url} className="block max-w-full truncate text-xs text-accent-ink hover:underline">{facts.url}</a>
            <div className="truncate text-xs text-ink-2">{prBlockers(todo, facts).map((key) => t(`assistant-tasks.pr_blocker_${key}`)).join(" · ")}</div>
          </div>)}
        </div>
      </div>
    ) : (
      <button type="button" onClick={() => openTask(todo.id)} className="flex w-full min-w-0 items-center gap-3 px-3 py-2.5 text-left hover:bg-hover">
        <TaskMark status="waiting" />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-medium">{todo.title}</span>
          <span className="block truncate text-xs text-ink-2">{parkedLine(todo)}</span>
        </span>
        <span className="shrink-0 text-xs text-ink-2">{t("assistant-tasks.review_parked")}</span>
      </button>
    )}
  </div>;
}
