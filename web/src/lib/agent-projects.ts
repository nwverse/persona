// The Agents list By project (knowledge-base project): an Agent sits under the project of the open to-do it works on
// (session → to-do → `project` slug), rolled up to the heading the Overview board shows for it: the nearest project
// pinned to Overview above it (meta.json overview_pinned), else its top-level project (lib/project-overview.ts
// overviewProjects, the same subtree rule). Inside a project the Agents are split by state; the ones that need the user
// are lifted out of every project into one strip at the top, so they are never folded away. Agents on no to-do, on a
// to-do in no project or marked Temporary go under Unassigned, always last. Pure: sessions, to-dos and projects in.
import { awaitsReply, justFinished, type AgentMutes } from "./agent-groups";
import { backgroundWaiting, stateOf } from "./format";
import { sortTodos, type Todo } from "./todos";
import type { Session } from "./types";

/** A knowledge-base project as this list needs it (lib/todo-projects.ts TodoProject). */
export interface AgentProject {
  slug: string;
  name: string;
  /** the project it shows under on the project tree; none at the top */
  parent?: string;
  /** pinned to Overview by the user */
  pinned?: boolean;
}

/** The sections inside a project, in this order; the ones that need the user are in the strip above every project. */
export type ProjectSectionKey = "working" | "waiting" | "just_finished" | "idle";
export const PROJECT_SECTIONS: readonly ProjectSectionKey[] = ["working", "waiting", "just_finished", "idle"];

export interface ProjectSection { key: ProjectSectionKey; sessions: Session[] }

export interface ProjectAgentGroup {
  /** the heading project's slug; "" for Unassigned */
  key: string;
  name: string;
  /** Unassigned: the Agents in no project */
  rest?: boolean;
  /** how many of its Agents need the user (they are listed in the strip, not here) */
  needs: number;
  /** every Agent it holds, those that need the user included */
  count: number;
  sections: ProjectSection[];
  /** nothing in it needs the user, works or just finished: folded at first */
  quiet: boolean;
}

export interface ProjectAgentRow {
  /** what the row is called: the to-do's title when it is the to-do's only Agent here, else the session's own short title */
  title: string;
  /** the sub-project's name when the Agent's project is below the heading's */
  sub?: string;
  /** the heading project's name (none for Unassigned): the Needs you strip names it on the row */
  head?: string;
}

export interface ProjectAgentList {
  /** every Agent that needs the user, across projects */
  needsYou: Session[];
  groups: ProjectAgentGroup[];
  /** the row's title and sub-project, by `machine/session` */
  rows: Map<string, ProjectAgentRow>;
}

const keyOf = (s: Pick<Session, "machine_id" | "session_id">) => `${s.machine_id}/${s.session_id}`;

/** Which section an Agent that does not need the user is in. */
export function projectSection(s: Session, mutes: AgentMutes): ProjectSectionKey {
  const st = stateOf(s);
  if (st === "working") return backgroundWaiting(s) ? "waiting" : "working";
  if (st === "unread" || justFinished(s, mutes)) return "just_finished";
  if (awaitsReply(s, mutes)) return "waiting";
  return "idle";
}

/** The heading each project shows under: the nearest pinned project at or above it when the user pinned any, else its
 *  top-level project. A project the list does not have is its own heading. */
export function projectHeads(projects: readonly AgentProject[]): (slug: string) => string {
  const bySlug = new Map(projects.map((p) => [p.slug, p]));
  const pinning = projects.some((p) => p.pinned);
  const memo = new Map<string, string>();
  return (slug) => {
    const known = memo.get(slug);
    if (known !== undefined) return known;
    let head = slug;
    let pinnedHead: string | undefined;
    const seen = new Set<string>();
    for (let at: string | undefined = slug; at && bySlug.has(at) && !seen.has(at); at = bySlug.get(at)!.parent) {
      seen.add(at);
      head = at;
      if (pinning && !pinnedHead && bySlug.get(at)!.pinned) pinnedHead = at;
    }
    const out = pinnedHead ?? head;
    memo.set(slug, out);
    return out;
  };
}

/** Prompt openers that say nothing about the work: tags, a quoted block's marker, "Implement the following plan:". */
const OPENER = /^(?:<[^>]+>\s*|[#>*\-\s]+|\((?:[^)]{0,40})\)\s*|（[^）]{0,40}）\s*)+/;
/** About 24 CJK characters or 40 Latin ones on a sidebar row. */
const TITLE_WIDTH = 40;
const wide = (c: string) => /[\u1100-\u115F\u2E80-\uA4CF\uAC00-\uD7A3\uF900-\uFAFF\uFE30-\uFE4F\uFF00-\uFF60\uFFE0-\uFFE6]/.test(c);

/** A session title cut to what a row has room for: its first line without the prompt's opener, up to its first sentence
 *  end when that leaves enough, and an ellipsis past about 40 columns (a CJK character counts two). */
export function shortTitle(title: string): string {
  const first = title.split(/\r?\n/).map((l) => l.trim().replace(OPENER, "").trim()).find(Boolean) ?? "";
  const sentence = first.match(/^(.{4,}?)[。！？!?；;]|^(.{8,}?)[.:：](?:\s|$)/u);
  const text = (sentence ? sentence[1] ?? sentence[2] : first).trim();
  let width = 0;
  let out = "";
  for (const c of text) {
    width += wide(c) ? 2 : 1;
    if (width > TITLE_WIDTH) return `${out.trimEnd()}…`;
    out += c;
  }
  return out;
}

/** The open to-do each Agent works on: its own (pushed with the session) when open, else the first open to-do that
 *  lists it and has not let it go (lib/todos.ts buildTodoAgentGroups' rule). */
function homes(sessions: readonly Session[], open: readonly Todo[]): Map<string, Todo> {
  const byId = new Map(open.map((td) => [td.id, td]));
  const listed = new Map<string, Todo>();
  for (const td of open) {
    for (const a of td.agents ?? []) {
      const k = `${a.machine_id}/${a.session_id}`;
      if (!a.released_at && !listed.has(k)) listed.set(k, td);
    }
  }
  const out = new Map<string, Todo>();
  for (const s of sessions) {
    const own = s.todo?.id ? byId.get(s.todo.id) : undefined;
    const home = own ?? listed.get(keyOf(s));
    if (home) out.set(keyOf(s), home);
  }
  return out;
}

/** The list By project. `sessions`: the live Agents to place, in the order rows are drawn. */
export function buildProjectAgentList(sessions: readonly Session[], todos: readonly Todo[], projects: readonly AgentProject[], o: { mutes?: AgentMutes; unassigned: string }): ProjectAgentList {
  const mutes = o.mutes ?? { spaces: [], sessions: [] };
  const open = sortTodos(todos.filter((td) => !td.done_at));
  const home = homes(sessions, open);
  const head = projectHeads(projects);
  const names = new Map(projects.map((p) => [p.slug, p.name]));
  const perTodo = new Map<string, number>();
  for (const td of home.values()) perTodo.set(td.id, (perTodo.get(td.id) ?? 0) + 1);

  const needsYou: Session[] = [];
  const rows = new Map<string, ProjectAgentRow>();
  const groups = new Map<string, ProjectAgentGroup>();
  const groupOf = (key: string): ProjectAgentGroup => {
    let g = groups.get(key);
    if (!g) {
      g = { key, name: key ? names.get(key) ?? key : o.unassigned, rest: key ? undefined : true, needs: 0, count: 0, sections: [], quiet: true };
      groups.set(key, g);
    }
    return g;
  };
  for (const s of sessions) {
    const td = home.get(keyOf(s));
    const project = td && !td.temporary ? td.project ?? "" : "";
    const top = project ? head(project) : "";
    const row: ProjectAgentRow = { title: td && perTodo.get(td.id) === 1 ? shortTitle(td.title) : shortTitle(s.title || "") };
    if (project && top !== project) row.sub = names.get(project) ?? project;
    if (top) row.head = names.get(top) ?? top;
    rows.set(keyOf(s), row);
    const g = groupOf(top);
    g.count++;
    if (stateOf(s) === "needs_you") {
      g.needs++;
      g.quiet = false;
      needsYou.push(s);
      continue;
    }
    const section = projectSection(s, mutes);
    if (section !== "idle") g.quiet = false;
    let sec = g.sections.find((x) => x.key === section);
    if (!sec) {
      sec = { key: section, sessions: [] };
      g.sections.push(sec);
    }
    sec.sessions.push(s);
  }
  for (const g of groups.values()) g.sections.sort((a, b) => PROJECT_SECTIONS.indexOf(a.key) - PROJECT_SECTIONS.indexOf(b.key));
  // the board's order: by name, which counts and activity never change (#11801); Unassigned last
  const ordered = [...groups.values()].sort((a, b) => Number(Boolean(a.rest)) - Number(Boolean(b.rest)) || a.name.localeCompare(b.name) || a.key.localeCompare(b.key));
  return { needsYou, groups: ordered, rows };
}
