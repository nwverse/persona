// The Finished tray's rows (#10922): drawn when the tray opens, in the chunk the Parked tray's rows load in
// (ParkedTaskRow.tsx re-exports them, so they add no lazy entry of their own), so neither the rows' list, seeing a task
// on opening it, nor the tray's empty line is part of the first screen (startup-preload's FIRST_SCREEN_BUDGET).
import type { FinishedItem, RunningItem } from "../../lib/assistant-entries";
import { getState, taskSessionLookup } from "../../lib/store";
import { setTodoSeen, todoSeenOn } from "../../lib/todo-seen";
import { triage } from "../../lib/todo-triage";
import type { Todo } from "../../lib/todos";
import { t } from "../../i18n";
import { stateName } from "../../lib/state-labels";
import { ProgressTaskRow } from "./Tasks";

/** Opening a Finished row the owner has not seen counts as seeing it (#5707), where the hub keeps seen marks: it stays in
 *  Finished without its Just finished mark (#10922); a failure leaves the mark. */
function seeTrayTodo(todo: Todo) {
  const s = getState();
  if (!todoSeenOn(s.features)) return;
  const tr = triage(todo, taskSessionLookup(s), Date.now());
  if (tr.section === "just_done") void setTodoSeen(todo.id, true, tr.last).catch(() => undefined);
}

/** How a task ended, instead of a card in the chat (#8237): what finished in the last 24 hours, newest first, seen or not;
 *  one nobody opened yet says Just finished (#10922). Its line when nothing is left, the tray kept open past the last. */
export function FinishedTaskRows({ items, conversation }: { items: readonly FinishedItem[]; conversation: string }) {
  if (!items.length) return <p className="px-3 py-3 text-sm text-ink-2">{t("assistant-finishedtaskrows.empty")}</p>;
  return <>{items.map((item) => <ProgressTaskRow key={item.key} card={item.card} conversation={conversation} countsOpen={false} fresh={item.fresh} onOpen={() => seeTrayTodo(item.todo)} />)}</>;
}

/** Reported finishes awaiting acceptance in the uncounted Just finished group. */
export function AcceptanceTaskRows({ items, conversation }: { items: readonly RunningItem[]; conversation: string }) {
  return <>
    <div data-progress-just-finished className="flex w-full items-center gap-2 px-3 py-2 text-xs text-ink-2">
      <span className="min-w-0 flex-1 truncate">{stateName("just_finished")}</span>
      <span className="shrink-0 tabular-nums">{items.length}</span>
    </div>
    {items.map((item) => item.kind === "task" && <ProgressTaskRow key={item.key} card={item.card} conversation={conversation}
      countsOpen={false} onOpen={() => seeTrayTodo(item.todo)} />)}
  </>;
}
