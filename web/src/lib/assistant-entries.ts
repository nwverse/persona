import { backgroundWork, taskBackgroundWork, type BackgroundWork } from "./background-work";
import { cardDismissed, cardFiled, cardSent, startFailure, type FeedItem, type TaskPlan } from "./feed";
import { cardBucket, taskCardSlots, type TaskCardMetadata } from "./task-cards";
import { acceptanceStands, decisionDismissed, linkedParkInteraction, parkInteractionID, reviewState, withoutParkInteraction } from "./supervision";
import { STOP_FOLLOWUP_MS, taskExecution } from "./task-execution";
import { finishedAt, JUST_DONE_MS, supersededFailedStarts, triage } from "./todo-triage";
import { awaitsReview, dockAgents, heldDelivery, makeSessionLookup, openAsk, todoRank, todoStatus, withInteractionLookup, type SessionLookup, type Todo, type TodoStatus } from "./todos";
import { byTime } from "./times";
import { homeKey } from "./folded-rows";
import type { Interaction, Session } from "./types";

/**
 * The Assistant's entries above the composer (docs/assistant/unified-entry.md "Needs you and In progress"):
 * Needs you lists the user's turn, In progress active work and external waits, including ready PRs.
 * Each thing has one home and one count; a reported finish goes in uncounted Just finished, not a peer review tray.
 * Finished lists the last 24 hours of finished work (#10922).
 * An unfinished stop needs follow-up after a full hour, even if the old page's inactivity rule would shelve it.
 * A task is one row whatever its agents: they are not listed on their own. In the main
 * conversation an Agent that belongs to no task (opened by hand) is a row of its own.
 */
export type NeedsYouItem =
  /** A task whose agent asks something (`asks`, oldest first), or that waits for the user's decision.
   *  `countsOpen` says whether the task contributes to the badge; an unattended stopped task counts even after
   *  the legacy page's inactivity window. */
  | { kind: "task"; key: string; todo: Todo; status: TodoStatus; asks: Interaction[]; countsOpen: boolean }
  /** An Agent that belongs to no task, asking something. */
  | { kind: "agent"; key: string; session: Session; asks: Interaction[] }
  /** A plan waiting for Start. */
  | { kind: "plan"; key: string; plan: TaskPlan }
  /** A message card waiting for Send. */
  | { kind: "message"; key: string; item: FeedItem };

export type RunningItem =
  /** A dispatched task not finished and not waiting on the user. `countsOpen`: see NeedsYouItem. */
  | { kind: "task"; key: string; card: TaskCardMetadata; todo: Todo; status: TodoStatus; activity?: BackgroundWork; countsOpen: boolean }
  /** A working Agent that belongs to no task. */
  | { kind: "agent"; key: string; session: Session; activity?: BackgroundWork };

/** A task the conversation handed out that finished in the last 24 hours (Finished, #10922). `fresh`: the owner has not
 *  opened it since it finished (the To-dos page's Just finished), so it is drawn as new; seeing it changes only that.
 *  `at`: when it finished (ms, `finishedAt`). */
export interface FinishedItem { kind: "task"; key: string; card: TaskCardMetadata; todo: Todo; status: TodoStatus; fresh: boolean; at: number }

export interface AssistantEntries {
  needsYou: NeedsYouItem[];
  running: RunningItem[];
  /** Open tasks with no progress for 48 hours and nothing running (the To-dos page's shelved, #3331): folded under
   *  In progress, not counted. */
  shelved: RunningItem[];
  /** Explicit holds and fresh ready PR waits: the Waiting subsection of In progress. */
  parked: RunningItem[];
  /** Reported finishes awaiting acceptance, uncounted under In progress. */
  justFinished: RunningItem[];
  /** Handed-out tasks that finished in the last 24 hours (JUST_DONE_MS, the To-dos page's Just finished window), seen or
   *  not, newest finished first: the Finished entry (#10922). How a task ended is told here, not by a card in the chat
   *  (#8237). Not open work: its count is its rows, and opening one marks it seen without taking it out. Until #10922
   *  only the unseen ones were listed, as a group under In progress that went as soon as one was opened. */
  finished: FinishedItem[];
}

export interface AssistantEntriesInput {
  conversation: string;
  /** The main conversation's id: it watches every task and the Agents opened by hand. */
  main?: string;
  todos: Todo[];
  /** Every session, as the lists show them (answered cards applied). */
  sessions: Session[];
  /** Pending cards. */
  interactions: Interaction[];
  plans: Record<string, TaskPlan>;
  /** The conversation's feed. */
  feed: FeedItem[];
  /** The hub's task card slots for the conversation (`task_cards`). */
  cards?: TaskCardMetadata[];
  /** Message cards and task-decision rounds the user put off locally (`later`). */
  deferred?: Record<string, unknown>;
  /** Looks up a task's agents; defaults to `sessions`. */
  lookup?: SessionLookup;
  now?: number;
  /** Tasks the user brought back from shelved, by id: when (ms). */
  restored?: ReadonlyMap<string, number>;
}

/** Task states that wait on the user: an agent asks, or its turn broke off on an error (a failed start is below). */
const WAITS_ON_YOU: ReadonlySet<TodoStatus> = new Set(["needs_you", "interrupted"]);

const sessionKey = (mid: string, sid: string) => `${mid}/${sid}`;

/** A task's status when some of its Agents ask under another task (`own`: its status from its own Agents alone): it
 *  does not wait on the user for them, so a Needs you that only those bring is the task at work instead. */
function withNeeds(all: TodoStatus, own: TodoStatus): TodoStatus {
  return all === "needs_you" && own !== "needs_you" ? (own === "not_started" ? "working" : own) : all;
}

/** A task's status, failed too when only its plan says the start failed (a hub from before #1077). */
export function entryStatus(todo: Todo, lookup: SessionLookup, plans: Record<string, TaskPlan>): TodoStatus {
  const st = todoStatus(todo, lookup);
  return st === "not_started" && startFailure(todo, plans) ? "failed" : st;
}

/** The task waits for the user's decision: an open ask (a decided one only while the task is open), or its supervisor's
 *  `needs_user` on an open task. A `pending_review` is its supervisor's to do, however long it has waited (#8678). */
export function taskAwaitsDecision(todo: Todo): boolean {
  const ask = openAsk(todo);
  if (ask) return !(todo.done_at && ask.outcome === "decide");
  if (todo.done_at) return false;
  return todo.supervision?.state === "needs_user";
}

/** The message card still waits for Send: not sent, filed, dismissed, or put off. */
export function messageWaits(it: FeedItem, deferred: Record<string, unknown> = {}): boolean {
  return it.kind === "message" && !cardSent(it) && !cardFiled(it) && !cardDismissed(it) && !deferred[it.id];
}

/** The review round, not the task id alone: a later decision must remain visible. */
export function decisionRound(todo: Todo): string {
  return todo.supervision?.reviews?.at(-1)?.at || todo.decision_review?.review_at || todo.ask?.asked_at || "";
}

export function decisionLaterKey(todo: Todo): string {
  return `decision:${todo.manager || ""}:${todo.id}:${decisionRound(todo)}`;
}

export function assistantEntries(input: AssistantEntriesInput): AssistantEntries {
  const { conversation, main, todos, sessions, interactions, plans, feed } = input;
  const now = input.now ?? Date.now();
  const lookup = withInteractionLookup(input.lookup ?? makeSessionLookup(sessions), interactions);
  const isMain = !!main && main === conversation;
  const byTodo = new Map(todos.map((todo) => [todo.id, todo]));
  // failed starts whose work finished in a related to-do: closed, as the To-dos page has them (#3713)
  const superseded = supersededFailedStarts(todos);
  const asksOf = new Map<string, Interaction[]>();
  for (const ix of [...interactions].sort((a, b) => byTime(a.created_at, b.created_at) || a.id.localeCompare(b.id))) {
    // a child session's card is asked under its parent (#5120): the parent's task or Agent row holds it
    const k = homeKey(ix);
    asksOf.set(k, [...(asksOf.get(k) ?? []), ix]);
  }

  const asking: NeedsYouItem[] = [];
  const deciding: { item: NeedsYouItem; rank: number; at: string }[] = [];
  const open: { item: RunningItem; rank: number; at: string }[] = [];
  const shelved: { item: RunningItem; at: string }[] = [];
  const parked: { item: RunningItem; at: string }[] = [];
  const justFinished: { item: RunningItem; at: number }[] = [];
  const finished: FinishedItem[] = [];
  const folded = new Set<string>();
  const slots = taskCardSlots(conversation, input.cards ?? [], feed, plans, todos, isMain);
  const activeAgents = (todo: Todo) => (todo.done_at ? [] : (todo.agents ?? []).filter((a) => !a.released_at));
  const homeAgents = (todo: Todo) => (todo.done_at ? [] : (todo.agents ?? []).filter((a) => !a.released_at || parkInteractionID(todo)));
  // One home per request (docs/design/DESIGN_SYSTEM.md 4.8): an Agent several of these tasks list asks under one of them,
  // the task the hub ties its session to (`todo`), else the first that lists it. The others neither repeat its cards nor
  // wait on the user for them; they are in progress.
  const listing = new Map<string, string[]>();
  for (const card of slots) {
    const todo = card.todo_id ? byTodo.get(card.todo_id) : undefined;
    if (!todo) continue;
    for (const a of homeAgents(todo)) {
      const k = sessionKey(a.machine_id, a.session_id);
      const ids = listing.get(k) ?? [];
      if (!ids.includes(todo.id)) listing.set(k, [...ids, todo.id]);
    }
  }
  const homeOf = (mid: string, sid: string): string | undefined => {
    const ids = listing.get(sessionKey(mid, sid));
    const tied = lookup(mid, sid)?.todo?.id;
    return tied && ids?.includes(tied) ? tied : ids?.[0];
  };
  for (const card of slots) {
    const sourceTodo = card.todo_id ? byTodo.get(card.todo_id) : undefined;
    // a plan task with no to-do yet is its plan's row (below); a to-do the hub no longer sends is finished
    if (!sourceTodo) continue;
    const candidate = linkedParkInteraction(sourceTodo, interactions);
    const linkedHome = candidate ? homeOf(candidate.machine_id, candidate.session_id) : undefined;
    const todo = linkedHome !== undefined && linkedHome !== sourceTodo.id ? withoutParkInteraction(sourceTodo) : sourceTodo;
    const active = activeAgents(todo);
    const own = active.filter((a) => homeOf(a.machine_id, a.session_id) === todo.id);
    // what the task waits on the user for is its own Agents' (an Agent another task holds asks there)
    const status = own.length < active.length ? withNeeds(entryStatus(todo, lookup, plans), todoStatus({ ...todo, agents: own }, lookup)) : entryStatus(todo, lookup, plans);
    const linked = linkedParkInteraction(todo, interactions);
    const ownAsks = own.flatMap((a) => asksOf.get(sessionKey(a.machine_id, a.session_id)) ?? []);
    const asks = linked && !ownAsks.some((ix) => ix.id === linked.id) ?
      [...ownAsks, linked].sort((a, b) => byTime(a.created_at, b.created_at) || a.id.localeCompare(b.id)) : ownAsks;
    const key = `task:${todo.id}`;
    const bucket = cardBucket(card, todo, plans, lookup, now, input.restored?.get(todo.id) ?? 0, superseded.has(todo.id));
    const countsOpen = bucket === "open";
    // a failed start carries done_at (the hub ends it), yet it is the user's to retry while its bucket says open
    const waits = status === "failed" ? bucket === "open" : !todo.done_at && WAITS_ON_YOU.has(status);
    const decision = taskAwaitsDecision(todo);
    const execution = taskExecution(todo, lookup);
    const settled = !!todo.done_at || acceptanceStands(todo);
    const external = !settled && (execution.waiting || reviewState(todo) !== "pending_review"
      && triage(todo, lookup, now, input.restored?.get(todo.id) ?? 0, superseded.has(todo.id)).section === "parked");
    const unfinished = own.length === active.length && !settled && !!todo.agents?.some((a) => !a.released_at && !a.start_error)
      && !execution.running && !external && status !== "usage_limit" && execution.closeout?.status !== "done";
    const unattended = unfinished && execution.unattendedSince > 0 && now - execution.unattendedSince > STOP_FOLLOWUP_MS;
    const deliveryApproval = heldDelivery(todo)?.unblock === "user";
    if (asks.length) asking.push({ kind: "task", key, todo, status, asks, countsOpen });
    else if (waits || decision || unattended || deliveryApproval) {
      // Only the review decision is hidden; a live Agent question above still has its home.
      if (deliveryApproval || !(status !== "needs_you" && decision && (decisionDismissed(todo) || input.deferred?.[decisionLaterKey(todo)]))) {
        deciding.push({ item: { kind: "task", key, todo, status, asks, countsOpen: countsOpen || unattended }, rank: todoRank(todo, status), at: card.at });
      }
    }
    // #10174: a reported finish awaits acceptance in Just finished, never in a peer acceptance group.
    else if (!settled && !execution.running && !external && execution.closeout?.status === "done") justFinished.push({ item: { kind: "task", key, card, todo, status, countsOpen: false }, at: execution.unattendedSince });
    // A recent unfinished stop remains on To-dos during the supervisor's follow-up window, not among finished work.
    else if (unfinished) {
      for (const agent of active) folded.add(sessionKey(agent.machine_id, agent.session_id));
      continue;
    }
    else if (!external && !todo.done_at && reviewState(todo) === "pending_review" && awaitsReview(todo, status) && !execution.running) justFinished.push({ item: { kind: "task", key, card, todo, status, countsOpen: false }, at: execution.unattendedSince });
    else if (!settled && execution.running) open.push({ item: { kind: "task", key, card, todo, status: "working", activity: taskBackgroundWork(todo, lookup), countsOpen: true }, rank: todoRank(todo, status), at: card.at });
    // Use the To-dos page's wait classification. Live work and owner decisions outrank a hold or an open PR.
    else if (external) parked.push({ item: { kind: "task", key, card, todo, status, countsOpen: countsOpen || execution.waiting }, at: card.at });
    else if (bucket === "open" || reviewState(todo) === "pending_review") open.push({ item: { kind: "task", key, card, todo, status, activity: taskBackgroundWork(todo, lookup), countsOpen }, rank: todoRank(todo, status), at: card.at });
    else if (bucket === "shelved") shelved.push({ item: { kind: "task", key, card, todo, status, countsOpen: false }, at: card.at });
    else {
      // finished work a conversation handed out, for a day after it finished, seen or not (#10922); a failed start a
      // related to-do finished is history, not news
      const tr = triage(todo, lookup, now, input.restored?.get(todo.id) ?? 0, superseded.has(todo.id));
      const ended = finishedAt(todo, tr);
      if ((tr.section === "just_done" || tr.section === "done") && status !== "failed" && todo.origin && ended > 0 && now - ended < JUST_DONE_MS) {
        finished.push({ kind: "task", key, card, todo, status, fresh: tr.section === "just_done", at: ended });
      }
      continue;
    }
    for (const agent of active) folded.add(sessionKey(agent.machine_id, agent.session_id));
    if (linked) folded.add(sessionKey(linked.machine_id, linked.session_id));
  }

  // Agents that belong to no task, the main conversation's only (dockAgents): asking ones wait on the user, working ones run.
  const agentsAsking: NeedsYouItem[] = [];
  const agentsRunning: RunningItem[] = [];
  for (const { session, state } of dockAgents(sessions.map((s) => lookup(s.machine_id, s.session_id) ?? s), todos, conversation, main)) {
    if (folded.has(sessionKey(session.machine_id, session.session_id))) continue;
    const key = `agent:${sessionKey(session.machine_id, session.session_id)}`;
    if (state === "needs_you") agentsAsking.push({ kind: "agent", key, session, asks: asksOf.get(sessionKey(session.machine_id, session.session_id)) ?? [] });
    else agentsRunning.push({ kind: "agent", key, session, activity: backgroundWork(session) });
  }

  const plansWaiting: NeedsYouItem[] = Object.values(plans)
    .filter((plan) => plan.conversation === conversation && plan.state === "open")
    .sort((a, b) => byTime(a.created_at, b.created_at) || a.id.localeCompare(b.id))
    .map((plan) => ({ kind: "plan", key: `plan:${plan.id}`, plan }));
  const messages: NeedsYouItem[] = feed.filter((it) => messageWaits(it, input.deferred)).map((item) => ({ kind: "message", key: `message:${item.id}`, item }));

  const newest = (a: { at: string; rank?: number }, b: { at: string; rank?: number }) => (a.rank ?? 0) - (b.rank ?? 0) || byTime(b.at, a.at);
  const firstAsk = (item: NeedsYouItem) => (item.kind === "task" || item.kind === "agent" ? item.asks[0]?.created_at : undefined);
  return {
    needsYou: [
      ...[...asking, ...agentsAsking].sort((a, b) => byTime(firstAsk(a), firstAsk(b))),
      ...plansWaiting,
      ...deciding.sort(newest).map((row) => row.item),
      ...messages,
    ],
    running: [...open.sort(newest).map((row) => row.item), ...agentsRunning],
    shelved: shelved.sort(newest).map((row) => row.item),
    parked: parked.sort(newest).map((row) => row.item),
    justFinished: justFinished.sort((a, b) => b.at - a.at || a.item.key.localeCompare(b.item.key)).map((row) => row.item),
    // newest finished first; the same moment by id, so every client lists them in one order
    finished: finished.sort((a, b) => b.at - a.at || (a.todo.id < b.todo.id ? -1 : a.todo.id > b.todo.id ? 1 : 0)),
  };
}

/** Needs you's rows by the conversation supervising them (#4314, docs/assistant/assistant.md "Background rounds"): a
 *  task's `origin`, else `conversation` (agents, plans, message cards, hand-made to-dos). This conversation's group comes
 *  first, then the others in the order their first row appears; the rows and their order within a group are as given,
 *  so the entry's count is unchanged. One group when every row is this conversation's. */
export function needsYouBySupervisor(items: readonly NeedsYouItem[], conversation: string): { supervisor: string; items: NeedsYouItem[] }[] {
  const groups = new Map<string, NeedsYouItem[]>([[conversation, []]]);
  for (const item of items) {
    const supervisor = item.kind === "task" && item.todo.origin ? item.todo.origin : conversation;
    const group = groups.get(supervisor);
    if (group) group.push(item);
    else groups.set(supervisor, [item]);
  }
  return [...groups].map(([supervisor, rows]) => ({ supervisor, items: rows }));
}
