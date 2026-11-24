import type { PlanTask } from "../../lib/feed";
import type { Todo } from "../../lib/todos";
import { useStore } from "../../lib/store";
import { LiveHandoffCard, type Handoff } from "./Handoff";
import { t } from "../../i18n";
import ContinuationHistory from "./ContinuationHistory";

/** A hub-confirmed send is history even after another Agent takes over. Only the exact current link may supply live
 *  status: an existing working Agent alone is never evidence that this failed dispatch was recovered. */
export function continuationHandoff(task: PlanTask, todos: Todo[]): Handoff | undefined {
  const r = task.continuation_recovery;
  if (!task.error || !r || (r.state !== "sent" && r.state !== "queued") || !r.scope_revision || !r.machine_id || !r.session_id || !r.input_id || !r.sent_at) return undefined;
  const target = task.continues || task.reuse_todo;
  if (!target || target !== r.todo_id || (task.reuse_todo && task.reuse_todo !== target) || (task.todo_id && task.todo_id !== target)) return undefined;
  const todo = todos.find((x) => x.id === target);
  const linked = todo?.agents?.find((a) => a.machine_id === r.machine_id && a.session_id === r.session_id && !a.start_error);
  const agent = linked && !linked.released_at && !linked.dismissed_at;
  return { key: `${r.machine_id}/${r.session_id}`, title: task.title, wake: "continuation", mid: r.machine_id, sid: r.session_id, todo: todo?.id,
    recovery: r.state, recoveryCurrent: Boolean(agent), currentTodo: todo?.id };
}

/** Shared by simplified and full plans. No execution binding is inferred from the target of a failed attempt. */
export function failedContinuationHandoff(task: PlanTask, todos: Todo[], key = "continuation"): Handoff | undefined {
  const recovered = continuationHandoff(task, todos);
  if (recovered) return recovered;
  const target = task.continues || task.reuse_todo;
  if (!task.error || !target || task.todo_id) return undefined;
  const delivery = task.continuation_recovery?.state;
  return { key, title: task.title, wake: "continuation", failure: task.error,
    currentTodo: todos.some((todo) => todo.id === target) ? target : undefined,
    delivery: delivery === "sending" || delivery === "unknown" ? delivery : undefined, deliveryError: task.continuation_recovery?.send_error };
}

export default function ContinuationCard({ handoff: planned }: { handoff: Handoff }) {
  const task = planned.continuationTask!;
  const handoff = useStore((s) => failedContinuationHandoff(task, s.todos, planned.key));
  if (!handoff) return null;
  const shown: Handoff = { ...handoff, detail: (state) => <>
    {handoff.recovery ? <span data-handoff-recovery={handoff.recovery} className="text-label text-ink-2">{t(`assistant-tasks.continuation_${handoff.recovery}`)}</span> : handoff.delivery ? <span data-handoff-delivery={handoff.delivery} className="text-label text-ink-2">{t(`assistant-tasks.continuation_${handoff.delivery}`)}</span> : null}
    <span className="flex min-w-0 flex-wrap items-center gap-1.5">
      {handoff.recovery ? handoff.recoveryCurrent ? <><span className="text-label text-ink-3">{t("assistant-tasks.continuation_current_session")}</span>{state}</> : <span data-handoff-historical-session className="text-label text-ink-3">{t("assistant-tasks.continuation_historical_session")}</span> : <span data-handoff-failure className="min-w-0 break-words text-label text-red">{t("assistant-tasks.continuation_failed_attempt")}</span>}
    </span>
  </>,
  };
  return <div className="flex min-w-0 flex-col gap-1.5"><LiveHandoffCard handoff={shown} /><ContinuationHistory error={task.error!} deliveryError={handoff.deliveryError} todo={handoff.currentTodo} /></div>;
}

