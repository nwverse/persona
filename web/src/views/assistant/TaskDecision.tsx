import { taskMeter, type TaskCardMetadata } from "../../lib/task-cards";
import { backgroundWorkLine, type BackgroundWork } from "../../lib/background-work";
import { usageLimitLabel } from "../../lib/usage-limit";
import { stateName } from "../../lib/state-labels";
import { StatusMark } from "../../ui/StatusMark";
import { shownCloseout } from "../../lib/closeout";
import { agentRowKey, agentRowLabel, agentRowMark, agentRowTone, todoChipState, todoChipLabel } from "../../lib/task-row-status";
import { plainMarkdown } from "../../lib/plain-markdown";
import { Icon } from "../../ui/icons";
import type { draftTasks } from "./Tasks";
// Task-review decisions use the existing row and draft actions, including legacy reviews without an acceptance ask.
// Dismissal is round-bound and persisted by the manager; Later only defers this browser session, as on reply drafts.
import { openTask } from "../../lib/handoff";
import { useRef, useState } from "react";
import { t } from "../../i18n";
import { api, credentialOwner, enc, errorText } from "../../lib/api";
import { decisionLaterKey, decisionRound, taskAwaitsDecision } from "../../lib/assistant-entries";
import { askRow, later, field, startInFlight, useFeed, type FeedItem } from "../../lib/feed";
import { defaultManager, MANAGER_HEADER, shownManager } from "../../lib/manager-choice";
import { getState, putTodo, refreshTodos, taskSessionLookup, useStore } from "../../lib/store";
import { latestReview, parkedLine, progressLine } from "../../lib/supervision";
import { askOptions, openAsk, interruption, orphanedLine, stoppedReason, waitingLine, TODO_STATUS_INFO, type Todo, type TodoStatus } from "../../lib/todos";
import { Button, toast, cx, useNow } from "../../ui/atoms";
import { AcceptRow } from "./Brief";
import { BriefingMark } from "./BriefingMark";
import { DecisionRow } from "./DecisionRow";
import { FailedTaskRow, waitsLabel } from "./FailedTaskRow";
import RuleSuggestionLine from "./RuleSuggestionLine";
import type { RuleCard } from "../../lib/rule-suggestion";

type Review = NonNullable<Todo["decision_review"]>;

export function TaskDecision({ todo, rowKey, hideTitle = false }: { todo: Todo; rowKey?: string; hideTitle?: boolean }) {
  const ask = openAsk(todo);
  const review = latestReview(todo.supervision);
  const round = decisionRound(todo);
  const admission = useRef(false);
  const [busy, setBusy] = useState(false);
  const [dismissing, setDismissing] = useState(false);
  const owner = credentialOwner();
  const manager = todo.manager || defaultManager();
  const selection = shownManager();
  const current = () => owner === credentialOwner() && selection === shownManager();
  const open = () => openTask(todo.id);
  const apply = (state: Review) => {
    if (!current()) return;
    const now = getState().todos.find((t) => t.id === todo.id && (t.manager || defaultManager()) === manager);
    // An old write cannot hide a newer review that arrived while it was pending.
    if (now && decisionRound(now) === round) putTodo({ ...now, decision_review: state });
    void refreshTodos();
  };
  const write = (undo?: string) => api<Review>(`/todos/${enc(todo.id)}/decision-${undo ? "undo-dismiss" : "dismiss"}`, {
    method: "POST", json: { review_at: round, ...(undo ? { dismissal_id: undo } : {}) },
    headers: { [MANAGER_HEADER]: manager }, isAuthContextCurrent: current,
  });
  const dismiss = async () => {
    if (admission.current || !round || !current()) return;
    admission.current = true;
    setBusy(true);
    setDismissing(true);
    const deferredKey = decisionLaterKey(todo);
    later(deferredKey);
    try {
      const state = await write();
      if (!current()) return;
      apply(state);
      later(deferredKey, false);
      toast(t("assistant-decisionrow.dismissed"), "ok", {
        label: t("assistant-task-actions.undo_dismiss"),
        run: async () => {
          if (!current() || !state.dismissal_id) return;
          try {
            const restored = await write(state.dismissal_id);
            apply(restored);
          } catch (error) {
            if (current()) toast(t("assistant-decisionrow.undo_failed", { error: errorText(error) }), "error");
          }
        },
      });
    } catch (error) {
      if (current()) {
        later(deferredKey, false);
        toast(t("assistant-task-actions.dismiss_failed", { error: errorText(error) }), "error");
      }
    } finally {
      admission.current = false;
      setBusy(false);
      setDismissing(false);
    }
  };
  const defer = () => {
    if (admission.current || !current()) return;
    later(decisionLaterKey(todo));
  };
  const buttons = <>
    <Button className="min-h-11" disabled={busy} onClick={defer}>{t("assistant-task-actions.later")}</Button>
    <Button className="min-h-11" disabled={busy || !round} busy={dismissing} onClick={dismiss}>{t("assistant-task-actions.dismiss_draft")}</Button>
  </>;
  const controls = <div data-decision-actions className={`${ask && askOptions(ask).length ? "" : "sticky -bottom-2.5 "}-mx-3 -mb-2.5 mt-1 flex items-center gap-2 border-t border-line bg-field px-3 py-2.5`}>{buttons}</div>;
  // a decision that offers a go-ahead the owner gave three times carries a suggested rule: its line goes under the
  // row (#5802; always mounted here), answered through the to-do's manager as the row's own answers are
  // (kept by the decision's round: what Always made is not said under the to-do's next decision)
  const rule = { id: todo.id + (ask?.asked_at ?? ""), suggestion: ask?.suggestion, headers: { [MANAGER_HEADER]: manager } };
  if (ask && askOptions(ask).length) return <><DecisionRow todo={todo} ask={ask} rowKey={rowKey} hideTitle={hideTitle} admission={admission} onBusyChange={setBusy} blocked={busy} footer={controls} /><RuleSuggestionLine of={rule} /></>;
  return <><div data-needs-row={rowKey} className="flex min-w-0 flex-col gap-1.5 rounded-control bg-field px-3 py-2.5">
    {ask ? <><AcceptRow stacked hideTitle={hideTitle} admission={admission} onBusyChange={setBusy} blocked={busy} secondary={<span data-decision-actions className="contents">{buttons}</span>} row={askRow(todo, ask)} />{ask.for_briefing && <BriefingMark />}</> : <>
      <div className="flex min-w-0 items-center gap-2 text-sm"><button type="button" onClick={open} className="min-w-0 flex-1 truncate text-left font-medium hover:underline">{todo.title}</button><span className="shrink-0 text-xs text-ink-2">{t("assistant-tasks.review_needs_user")}</span></div>
      {(review?.reason || review?.note) && <p data-decision-legacy className="whitespace-pre-wrap text-sm select-text [overflow-wrap:anywhere]">{review.reason || review.note}</p>}
      {review?.recommend && <p className="text-xs text-ink-2">{t("assistant-brief.recommend", { option: review.recommend })}</p>}
      {review?.options?.length ? <ul className="text-xs text-ink-2">{review.options.map((option) => <li key={option.label}>{option.label}{option.leads_to ? t("assistant-decisionrow.leads_to", { leads: option.leads_to }) : ""}</li>)}</ul> : null}
      <p className="text-xs text-ink-2">{t("assistant-decisionrow.missing_options")}</p>
      <div><Button disabled={busy} onClick={open}>{t("assistant-decisionrow.another")}</Button></div>
    </>}
    {!ask && controls}
  </div><RuleSuggestionLine of={rule} /></>;
}

export type NeedsYouPartProps =
  /** A task's decision in the tray. */
  | { decision: Todo; rowKey?: string; hideTitle?: boolean }
  /** A task whose Agent did not start, in the tray (#10365). */
  | { failed: Todo; rowKey: string; countsOpenAttr: { "data-counts-open"?: string } }
  /** The suggested rule of a plan card or a passed-up question, under it. */
  | { rule: RuleCard };

/** What views/assistant/Tasks loads, with the first card that draws one and not with the first screen
 *  (startup-preload's budget): a decision's row, a failed start's row (#10365), or a suggested rule's line (#5802). One
 *  chunk behind one component, so the first screen names one file for all. */
export default function NeedsYouParts(props: NeedsYouPartProps) {
  if ("decision" in props) return <TaskDecision todo={props.decision} rowKey={props.rowKey} hideTitle={props.hideTitle} />;
  if ("failed" in props) return <FailedTaskRow todo={props.failed} rowKey={props.rowKey} countsOpenAttr={props.countsOpenAttr} />;
  return <RuleSuggestionLine of={props.rule} />;
}

const EMPTY_FEED: FeedItem[] = [];

/** A task in Needs you that only waits (#8237): cut off by the model service, with why and Continue (its receipt card's
 *  until then); waiting for review, with what it handed in; else what it waits for. The title opens the task. */
export function WaitingTaskRow({ rowKey, todo, status, countsOpenAttr, tasks }: { rowKey?: string; todo: Todo; status: TodoStatus; countsOpenAttr: { "data-counts-open"?: string }; tasks: typeof draftTasks }) {
  const task = tasks.useTaskNow(todo);
  const retrying = tasks.useRetrying(todo.id);
  const summary = useFeed((s) => {
    const done = (todo.origin ? s.feeds[todo.origin] ?? EMPTY_FEED : EMPTY_FEED).filter((it) => it.kind === "receipt" && it.ref === todo.id && field(it, "state") !== "failed").at(-1);
    return done ? field(done, "summary") : undefined;
  });
  const cutOff = status === "interrupted";
  const cut = cutOff ? interruption(task?.lead) : undefined;
  const canContinue = cutOff && (cut ? cut.canContinue : true);
  const unattended = !cutOff && !taskAwaitsDecision(todo);
  const stopped = unattended ? stoppedReason(todo, status) : undefined;
  const closeout = shownCloseout(task?.lead);
  const agentKey = task?.lead ? agentRowKey({ live: false, status, lead: task.lead, todo: true, starting: false }) : "unfinished";
  const chip = todoChipState({ toStart: false, status, done: !!todo.done_at, stage: todo.stage, pr: todo.pr_url, todo });
  const line = cut ? cut.reason : unattended
    ? (stopped !== undefined ? stopped ? t("assistant-tasks.progress_stopped_reason", { reason: stopped }) : t("assistant-tasks.progress_stopped")
      : closeout?.waiting || closeout?.missing || tasks.progressOf(task?.lead)?.text || task?.lead?.last_text || t("assistant-task-details.tray_unattended"))
    : todo.supervision?.state === "pending_review" ? (summary && plainMarkdown(summary)) || tasks.progressOf(task?.lead)?.text : undefined;
  return (
    <div data-needs-row={rowKey} {...countsOpenAttr} data-waiting-task={todo.id} data-needs-reason={unattended ? "stopped_unfinished" : undefined} className={cx("flex flex-col gap-1.5 rounded-control px-3 py-2 text-sm", cutOff ? "bg-danger-wash" : "bg-field")}>
      <div className="flex min-w-0 items-center gap-2">
        <Icon name={cutOff ? "info" : "hand"} className={cx("shrink-0", cutOff ? "text-red" : "text-attention")} />
        <button type="button" onClick={() => openTask(todo.id)} className="min-w-0 flex-1 truncate text-left hover:underline">{todo.title}</button>
        <span className={cx("shrink-0 text-xs", cutOff ? "text-ink" : "text-ink-2")}>{waitsLabel(todo, status)}</span>
      </div>
      {unattended && <div className="pl-6 text-xs text-ink-2">{t("assistant-task-details.tray_unattended")}</div>}
      {unattended && <div className="flex min-w-0 flex-col gap-1 break-words pl-6 text-xs text-ink-2">
        <span data-task-status={agentKey}>{agentRowLabel(agentKey, task?.lead)}</span>
        {chip && <span data-task-todo-state>{todoChipLabel(chip)}</span>}
      </div>}
      {line && <div className={cx("truncate pl-6 text-xs", cutOff ? "text-ink" : "text-ink-2")}>{line}</div>}
      {canContinue && (
        <div className="flex items-center gap-4 pl-6 text-[12.5px]">
          <button type="button" data-continue-task={todo.id} disabled={retrying} onClick={() => tasks.retryTaskNow(todo, "continue")} className="touch-hit inline-flex items-center gap-1 whitespace-nowrap font-semibold text-ink hover:underline disabled:opacity-50 disabled:hover:no-underline">
            <Icon name="retry" size={12} />
            {retrying ? t("assistant-tasks.retrying") : t("assistant-tasks.continue")}
          </button>
        </div>
      )}
    </div>
  );
}

export function ProgressTaskRow({ card, conversation, activity, countsOpen, fresh = false, onOpen, tasks }: { card: TaskCardMetadata; conversation: string; activity?: BackgroundWork; countsOpen: boolean; fresh?: boolean; onOpen?: () => void; tasks: typeof draftTasks }) {
  const todo = tasks.useTodo(card.todo_id);
  const task = tasks.useTaskNow(todo);
  const plans = useFeed((s) => s.plans);
  const feed = useFeed((s) => s.feeds[conversation] ?? EMPTY_FEED);
  const planned = card.plan_id !== undefined && card.index !== undefined ? plans[card.plan_id]?.tasks[card.index] : undefined;
  const title = todo?.title ?? planned?.title ?? t("assistant-tasks.task_fallback");
  const accepted = !!todo?.done_at && todo.supervision?.state === "accepted";
  let state = task?.status ?? (card.state === "followup" ? "follow_up" : card.state);
  if (todo && accepted) state = "accepted";
  else if (todo?.done_at && todo.supervision?.state === "cancelled") state = "cancelled";
  else if (!todo && card.plan_id && plans[card.plan_id]?.state === "dismissed") state = "cancelled";
  const finished = state === "done" || state === "accepted";
  const clock = useNow(30_000, state === "usage_limit"); // the limit's grace running out
  const blocked = ["needs_you", "interrupted", "usage_limit", "failed"].includes(state) || (!finished && todo?.supervision?.progress?.kind === "blocked");
  const meter = taskMeter(todo, task?.lead);
  const report = feed.find((it) => it.id === card.report_id);
  const receipt = feed.find((it) => it.id === card.receipt_id);
  const pr = todo?.pr_url ?? (receipt && field(receipt, "pr"));
  const prNumber = pr ? /\/(\d+)\/?$/.exec(pr)?.[1] : undefined;
  const toStart = !todo && !!card.plan_id && plans[card.plan_id]?.state === "open";
  // Starting only while an Agent is being started for it; a to-do nobody dispatched says Not started (#7552)
  const starting = !todo || startInFlight(todo, plans);
  const liveWork = !finished && state !== "cancelled" ? activity : undefined;
  const liveLine = backgroundWorkLine(liveWork);
  // parked by its supervisor (#3804): the row says so, until when and why, ahead of what its agent last did
  const parked = liveWork || finished || state === "cancelled" ? undefined : parkedLine(todo);
  // the status column is the Agent's own state, drawn with the Agents' marks; the to-do's own state is its chip (#6492)
  const agentKey = agentRowKey({ live: !!liveWork, status: state, lead: task?.lead, todo: !!todo, starting });
  const rowIsFinished = !countsOpen && finished;
  const isNew = fresh && !countsOpen;
  const chip = todoChipState({ toStart, status: state, done: finished, stage: todo?.stage, pr, todo });
  const ask = useStore((s) => tasks.taskAsk(todo ? tasks.taskInteraction(todo, s.interactions, task?.lead) : undefined));
  // what a waiting task waits for (#3274): its agent's own line for it
  const waits = useStore((s) => (todo && state === "waiting" ? waitingLine(todo, taskSessionLookup(s)) : undefined));
  // stopped and not done: the hub's completion check says why (#3180); an agent's open question or waiting line says more
  const why = ask || waits ? undefined : stoppedReason(todo, state);
  const stopped = why === undefined ? undefined : why ? t("assistant-tasks.progress_stopped_reason", { reason: why }) : t("assistant-tasks.progress_stopped");
  // nobody is working on it (#3618): said ahead of why it stopped and of its agent's last report
  const orphan = todo && !ask ? orphanedLine(todo) : undefined;
  const nobody = orphan && (orphan.reason ? `${orphan.lead} · ${orphan.reason}` : orphan.lead);
  // a usage limit says what stopped it and when it goes on (the status word on the right says only that it did)
  const limit = state === "usage_limit" ? usageLimitLabel(task?.lead, clock) : undefined;
  const closeout = shownCloseout(task?.lead);
  const detail = finished || state === "cancelled"
    ? latestReview(todo?.supervision)?.note ?? todo?.done_reason ?? (receipt && field(receipt, "summary"))
    : limit ?? liveLine ?? parked ?? (closeout?.status === "blocked" ? closeout.waiting : undefined) ?? waits ?? nobody ?? stopped ?? progressLine(todo?.supervision) ?? ask ?? task?.lead?.plan?.current ?? tasks.progressOf(task?.lead)?.text ?? (report && field(report, "text"));
  return (
    <button type="button" data-task-slot={card.id} data-counts-open={countsOpen ? "true" : "false"} disabled={!todo && !card.plan_id} onClick={() => {
      onOpen?.();
      if (card.todo_id) openTask(card.todo_id);
      else window.dispatchEvent(new CustomEvent("persona:task-start", { detail: conversation }));
    }} className="grid w-full min-w-0 grid-cols-[1rem_minmax(0,1fr)_1rem] items-center gap-x-3 gap-y-0.5 px-3 py-2.5 text-left hover:bg-hover disabled:cursor-default sm:flex sm:gap-3">
      <span data-task-mark={isNew ? "just_finished" : rowIsFinished ? "done" : agentKey} className="col-start-1 row-start-1 flex w-4 shrink-0 justify-center">
        {isNew ? <StatusMark state="idle" finished size="sm" /> : rowIsFinished ? <tasks.TaskMark status="done" size={8} /> : <StatusMark state={agentRowMark(agentKey)} size="sm" />}
      </span>
      <div data-task-content className="col-start-2 row-start-1 min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-1.5">
          <span className="min-w-0 truncate text-sm font-semibold" title={title}>{title}</span>
          {chip && <span data-task-todo-state className="max-w-1/2 shrink-0 truncate rounded-full bg-field px-1.5 text-xs text-ink-2">{todoChipLabel(chip)}</span>}
        </div>
        <tasks.BackgroundWorkDetail activity={liveWork} detail={detail} pr={prNumber ? t("assistant-tasks.progress_pr", { number: prNumber }) : undefined} />
        {!finished && state !== "cancelled" && meter && <TaskProgressMeter value={meter.value} total={meter.total} blocked={blocked} title={title} />}
      </div>
      {/* the status in its own right-aligned column (the rows' states scan down it), then the row's end: › where it opens
          something, ✓ once finished, ⓘ for a usage limit (#5569) */}
      <span data-task-status={isNew ? "just_finished" : rowIsFinished ? "done" : agentKey} className={cx("col-start-2 row-start-2 min-w-0 break-words text-xs sm:max-w-[40%] sm:shrink-0 sm:text-right", isNew ? "text-green" : rowIsFinished ? "text-ink-2" : agentRowTone(agentKey))}>
        {isNew ? stateName("just_finished") : rowIsFinished ? (accepted ? t("assistant-tasks.review_accepted") : TODO_STATUS_INFO.done.label) : agentRowLabel(agentKey, task?.lead)}
      </span>
      <span className="col-start-3 row-start-1 flex w-4 shrink-0 justify-center">
        {finished ? <Icon name="check" className="text-green" /> : state === "usage_limit" ? <Icon name="info" className="text-orange" /> : <Icon name="chevronRight" className="text-ink-3" />}
      </span>
    </button>
  );
}

/** An honest count meter with a readable value and a larger hover target on its task row. */
function TaskProgressMeter({ value, total, blocked, title }: { value: number; total: number; blocked: boolean | undefined; title: string }) {
  const label = t("assistant-tasks.progress_count", { value, total });
  return (
    <div className="mt-1.5 flex items-center gap-2" title={label}>
      <div role="progressbar" aria-label={title} aria-valuemin={0} aria-valuemax={total} aria-valuenow={value} aria-valuetext={label} className="task-count-meter min-w-0 flex-1 overflow-hidden rounded-full bg-field">
        <span className={cx("block h-full origin-left rounded-full", blocked ? "bg-orange" : "bg-accent")} style={{ width: `${value / total * 100}%` }} />
      </div>
      <span className="shrink-0 text-xs text-ink-2 tabular-nums">{label}</span>
    </div>
  );
}
