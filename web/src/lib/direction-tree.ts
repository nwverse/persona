// The direction tree (#4352, docs/assistant/unified-entry.md "Direction tree"): each current leader conversation (a
// "direction", shown as 常驻 / Pinned) with the open tasks it supervises, each task with its state and its agents, and
// last the secretary's group (tasks with no leader). The hub builds it once (GET /api/v1/assistant/directions/tree); the
// sidebar, the To-dos page and the Agents page all draw this one tree and never group or rank the tasks themselves.
// Asked on the socket's hello and again on every {"type":"directions"} push; a hub without the route (404) answers
// null, and the screens keep what they showed before (flat direction rows, no grouping by direction).
import { useEffect, useSyncExternalStore } from "react";
import { t } from "../i18n";
import { api, ApiError } from "./api";
import { onHubEvent } from "./store";
import type { AgentMutes } from "./agent-groups";
import { agentTreeState, buildTodoTree, type TodoTree, type TreeAgent, type TreeProject, type TreeState, type TreeTodo, worstTreeState } from "./todo-tree";
import type { AgentState, Machine, Session } from "./types";

export const DIRECTION_TREE_PATH = "/assistant/directions/tree";

/** A task's state in the tree, as the hub decides it (one rule, in this precedence). */
export type DirectionTaskState = "needs_you" | "review" | "stuck" | "working";

export const DIRECTION_TASK_STATES: readonly DirectionTaskState[] = ["needs_you", "review", "stuck", "working"];

export interface DirectionCounts {
  working: number;
  needs_you: number;
  stuck: number;
  review: number;
}

export interface DirectionAgent {
  machine_id: string;
  session_id: string;
  title: string;
  agent?: string;
  /** The session's state as session rows carry it ("" when the hub did not find the session). */
  state: string;
  children?: DirectionAgent[];
}

export interface DirectionTask {
  id: string;
  title: string;
  state: DirectionTaskState;
  agents?: DirectionAgent[];
}

export interface Direction {
  /** The leader conversation's id; the main conversation's for the secretary's group. */
  id: string;
  /** The direction's name ("" for the secretary's group). */
  name: string;
  secretary?: boolean;
  counts: DirectionCounts;
  tasks?: DirectionTask[];
}

export interface DirectionTree {
  directions: Direction[];
}

/** The tree as the hub sent it, with anything missing or malformed made safe to draw. */
export function normalizeTree(raw: unknown): DirectionTree {
  const list = (raw as { directions?: unknown })?.directions;
  if (!Array.isArray(list)) return { directions: [] };
  const agent = (a: DirectionAgent): DirectionAgent => ({ ...a, title: a.title ?? "", state: a.state ?? "", children: Array.isArray(a.children) ? a.children.filter(validAgent).map(agent) : [] });
  return {
    directions: list
      .filter((d): d is Direction => !!d && typeof d.id === "string")
      .map((d) => ({
        id: d.id,
        name: typeof d.name === "string" ? d.name : "",
        ...(d.secretary ? { secretary: true } : {}),
        counts: normalizeCounts(d.counts),
        tasks: (Array.isArray(d.tasks) ? d.tasks : [])
          .filter((td): td is DirectionTask => !!td && typeof td.id === "string")
          .map((td) => ({ id: td.id, title: td.title ?? "", state: DIRECTION_TASK_STATES.includes(td.state) ? td.state : "working", agents: (Array.isArray(td.agents) ? td.agents : []).filter(validAgent).map(agent) })),
      })),
  };
}

/** Each count a finite number, 0 for anything else the hub sent. */
function normalizeCounts(raw: unknown): DirectionCounts {
  const c = (raw ?? {}) as Partial<Record<keyof DirectionCounts, unknown>>;
  const n = (v: unknown) => (typeof v === "number" && Number.isFinite(v) ? v : 0);
  return { working: n(c.working), needs_you: n(c.needs_you), stuck: n(c.stuck), review: n(c.review) };
}

function validAgent(a: unknown): a is DirectionAgent {
  return !!a && typeof (a as DirectionAgent).machine_id === "string" && typeof (a as DirectionAgent).session_id === "string";
}

/** The directions to show as such: the current leaders (never the secretary's group). */
export function leaderDirections(tree: DirectionTree | null | undefined): Direction[] {
  return tree?.directions.filter((d) => !d.secretary) ?? [];
}

/** The secretary's group, when it is to be shown: there is at least one leader and it holds at least one task. */
export function shownSecretary(tree: DirectionTree | null | undefined): Direction | undefined {
  if (!tree || leaderDirections(tree).length === 0) return undefined;
  const s = tree.directions.find((d) => d.secretary);
  return s && (s.tasks?.length ?? 0) > 0 ? s : undefined;
}

/** Whether the tree has something to group by: a hub that sends it and at least one leader. */
export function groupsByDirection(tree: DirectionTree | null | undefined): tree is DirectionTree {
  return leaderDirections(tree).length > 0;
}

/** A direction's title: its name; 书记官 for the secretary's group; the id as a last resort (a leader with no name yet). */
export function directionTitle(d: Direction, fallback?: string): string {
  if (d.secretary) return t("lib-todo-tree.secretary");
  return d.name || fallback || d.id;
}

/** A task state's word (在做 / 等你 / 卡住 / 待验收). */
export function directionStateName(s: DirectionTaskState): string {
  switch (s) {
    case "needs_you":
      return t("lib-state-labels.direction_needs_you");
    case "review":
      return t("assistant-tasks.review_pending");
    case "stuck":
      return t("lib-state-labels.direction_stuck");
    default:
      return t("lib-state-labels.direction_working");
  }
}

/** A task state's text color: needs-you, working, and caution for stuck and review (Tailwind token classes). */
export const DIRECTION_STATE_TONE: Record<DirectionTaskState, string> = { needs_you: "text-attention", working: "text-orange", stuck: "text-orange", review: "text-orange" };

/** What a direction row says under or beside its name: "在做 N · 等你 N". */
export function directionCountsText(c: DirectionCounts): string {
  return t("lib-state-labels.direction_counts", { working: c.working, needs_you: c.needs_you });
}

/** Every agent of a task, its children included, depth first. */
export function taskAgents(td: DirectionTask): DirectionAgent[] {
  const out: DirectionAgent[] = [];
  const walk = (list: readonly DirectionAgent[] | undefined) => list?.forEach((a) => (out.push(a), walk(a.children)));
  walk(td.agents);
  return out;
}

/** The "machine/session" key of an agent. */
export const agentRef = (a: Pick<DirectionAgent, "machine_id" | "session_id">) => `${a.machine_id}/${a.session_id}`;

// ── the store: one tree for the page ─────────────────────────

/** undefined: not asked yet (or asking the first time); null: no tree (an older hub, or the first read failed). */
type Snapshot = DirectionTree | null | undefined;

const listeners = new Set<() => void>();
let tree: Snapshot = undefined;
let inFlight = false;
let again = false;
let generation = 0;
let unhook: (() => void) | null = null;

function set(next: Snapshot) {
  if (next === tree) return;
  tree = next;
  listeners.forEach((l) => l());
}

/** Ask the hub for the tree; a push while a read is out asks once more after it. */
export function refreshDirectionTree(): Promise<void> {
  if (inFlight) {
    again = true;
    return Promise.resolve();
  }
  inFlight = true;
  const n = generation;
  return api<unknown>(DIRECTION_TREE_PATH)
    .then(
      (res) => {
        if (n === generation) set(normalizeTree(res));
      },
      // an older hub: no tree, and the screens stay as they were. Any other failure keeps the last tree; with none yet
      // it counts as no tree too, so no screen waits on a read that failed (the next hello or push asks again)
      (e) => {
        if (n !== generation) return;
        if ((e instanceof ApiError && e.status === 404) || tree === undefined) set(null);
      },
    )
    .finally(() => {
      if (n !== generation) return;
      inFlight = false;
      if (again) {
        again = false;
        void refreshDirectionTree();
      }
    });
}

/** Start keeping the tree: read it now, on every hello and on every directions push (once for the page). */
export function watchDirectionTree() {
  if (unhook) return;
  const offPush = onHubEvent("directions", () => void refreshDirectionTree());
  const offHello = onHubEvent("hello", () => void refreshDirectionTree());
  unhook = () => {
    offPush();
    offHello();
  };
  void refreshDirectionTree();
}

/** Forget the tree and stop listening (another login, tests). */
export function resetDirectionTree() {
  generation++;
  inFlight = false;
  again = false;
  unhook?.();
  unhook = null;
  tree = undefined;
}

const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => void listeners.delete(l);
};
const snapshot = () => tree;

/** Whether the last tree read has a leader to group by (the grouping shortcut offers "By pinned" then). */
export function directionTreeGroups(): boolean {
  return groupsByDirection(tree);
}

/** The tree as last read, outside React (tests). */
export const directionTreeNow = snapshot;

/** The hub's direction tree: undefined until it first answers, null on a hub without one. */
export function useDirectionTree(): Snapshot {
  useEffect(() => watchDirectionTree(), []);
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}

// ── the tree as the Agents tree draws it (lib/todo-tree.ts) ───

/** The Agents tree's one-word state for a task's state: needs you, working, and waiting for stuck and review. */
const TREE_STATE: Record<DirectionTaskState, TreeState> = { needs_you: "needs_you", working: "working", stuck: "waiting", review: "waiting" };

/** What a session row needs of an agent the session list does not have (another assistant's, an in-session sub-agent):
 *  its title and the state the hub saw. */
function standIn(a: DirectionAgent): Session {
  const known: readonly string[] = ["needs_you", "working", "unread", "idle", "offline"];
  return { machine_id: a.machine_id, session_id: a.session_id, title: a.title, cwd: "", status: "", state: (known.includes(a.state) ? a.state : "offline") as AgentState, ...(a.agent ? { agent: a.agent } : {}) };
}

/**
 * A task's agents as tree nodes: each found in `sessions` (by "machine/session") once, nested as the hub nested them.
 * `standIns`: an agent the list does not have is drawn from what the tree says of it (the sidebar); otherwise it is
 * left out and its children move up (the Agents page, whose list is filtered). `placed` collects the agents drawn.
 */
export function directionAgentNodes(list: readonly DirectionAgent[] | undefined, sessions: ReadonlyMap<string, Session>, placed: Set<string>, standIns: boolean): TreeAgent[] {
  const out: TreeAgent[] = [];
  for (const a of list ?? []) {
    const ref = agentRef(a);
    const children = directionAgentNodes(a.children, sessions, placed, standIns);
    const s = placed.has(ref) ? undefined : sessions.get(ref) ?? (standIns ? standIn(a) : undefined);
    if (!s) {
      out.push(...children);
      continue;
    }
    placed.add(ref);
    out.push({ key: `s:${ref}`, session: s, state: agentTreeState(s), children, folded: false });
  }
  return out;
}

/** A task as the Agents tree's to-do node, under `keyPrefix` (its fold is kept per key). */
export function directionTaskNode(td: DirectionTask, sessions: ReadonlyMap<string, Session>, placed: Set<string>, { keyPrefix, folded, standIns }: { keyPrefix: string; folded: boolean; standIns: boolean }): TreeTodo {
  const agents = directionAgentNodes(td.agents, sessions, placed, standIns);
  const flat = (n: TreeAgent): Session[] => [n.session, ...n.children.flatMap(flat)];
  return { key: `${keyPrefix}${td.id}`, todoId: td.id, name: td.title, state: TREE_STATE[td.state], taskState: td.state, agents, sessions: agents.flatMap(flat), folded };
}

const keyOf = (s: Session) => `${s.machine_id}/${s.session_id}`;

/**
 * Agents "By pinned" (#4352): pinned direction › task › agent › sub-agent, from the hub's tree, for the sessions listed
 * (live, filtered, unpinned). A task none of whose agents is listed is left out, and so is a direction left with no task.
 * The listed agents the tree does not hold go last, in the "By to-do" tree's own groups for agents on no to-do.
 */
export function buildDirectionAgentTree(tree: DirectionTree, all: Session[], machines: Machine[], mutes?: AgentMutes, now = Date.now()): TodoTree {
  const sessions = new Map(all.map((s) => [keyOf(s), s]));
  const placed = new Set<string>();
  const projects: TreeProject[] = [];
  for (const d of tree.directions) {
    const todos = (d.tasks ?? []).map((td) => directionTaskNode(td, sessions, placed, { keyPrefix: `direction-agents:task:${d.id}:`, folded: false, standIns: false })).filter((td) => td.agents.length > 0);
    if (todos.length === 0) continue;
    projects.push({
      key: `direction-agents:${d.id}`,
      name: directionTitle(d),
      state: worstTreeState(todos.map((td) => td.state)),
      todos,
      sessions: todos.flatMap((td) => td.sessions),
      folded: false,
      direction: { counts: d.counts, secretary: !!d.secretary },
    });
  }
  const rest = all.filter((s) => !placed.has(keyOf(s)));
  return { projects, loose: buildTodoTree(rest, machines, [], mutes, now).loose };
}
