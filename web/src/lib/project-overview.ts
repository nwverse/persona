// Overview's "By project" (#13019, docs/web/web.md "Overview by project"): one card per top-level project of the project
// tree (lib/kb-tree.ts), with only what the tree's node and its detail panel already show, counted here once for both
// (views/overview/ByProject.tsx and views/knowledge/ProjectTree.tsx ProjectHome), plus the to-dos in no project: the
// ones marked Temporary and the unsorted ones (lib/todo-sort.ts). Pure: the list, the parents and the to-dos in.
import { below, milestoneFlag, treeOf, type Milestone, type TreeEntry } from "./kb-tree";
import { isUnsorted } from "./todo-sort";
import type { Todo } from "./todos";

type TodoFields = Pick<Todo, "id" | "done_at" | "project" | "temporary" | "kind">;

/** An open to-do as the project counts it: not done (cancelled ones are closed too) and not a concern. */
export const openTodo = (td: Pick<Todo, "done_at" | "kind">) => !td.done_at && td.kind !== "concern";

/** What a project's card and its node panel both show: the projects directly under it, its milestones and their flag,
 *  and the open to-dos on it or on any project below it (a to-do's `project` is the project's slug). */
export interface ProjectFacts {
  children: TreeEntry[];
  milestones: Milestone[];
  flag: ReturnType<typeof milestoneFlag>;
  /** open to-dos on the project itself */
  ownTodos: number;
  /** open to-dos on it and its subtree */
  openTodos: number;
}

/** The facts of one project, from the tree's list and parents (as drawn) and the to-dos. */
export function projectFacts(list: TreeEntry[], parents: Map<string, string>, todos: readonly TodoFields[], id: string): ProjectFacts {
  const e = list.find((x) => x.id === id);
  const children = list.filter((x) => parents.get(x.id) === id).sort((a, b) => a.name.localeCompare(b.name));
  const subtree = below(parents, id);
  let ownTodos = 0;
  let openTodos = 0;
  for (const td of todos) {
    if (!td.project || !openTodo(td)) continue;
    if (td.project === id) ownTodos++;
    if (td.project === id || subtree.has(td.project)) openTodos++;
  }
  const milestones = e?.milestones ?? [];
  return { children, milestones, flag: milestoneFlag(milestones), ownTodos, openTodos };
}

/** The top-level projects of the tree, in the tree's order (by name; merged and "not a project" ones left out). */
export function topProjects(list: TreeEntry[], parents: Map<string, string>): TreeEntry[] {
  return treeOf(list).live.filter((e) => !parents.has(e.id)).sort((a, b) => a.name.localeCompare(b.name));
}

/** The to-dos in no project: the ones marked Temporary (open, newest first) and the unsorted ones (the sort sheet's rule). */
export function looseTodos<T extends TodoFields & { created_at?: string }>(todos: readonly T[]): { temporary: T[]; unsorted: T[] } {
  const newest = (a: T, b: T) => (b.created_at ?? "").localeCompare(a.created_at ?? "");
  const temporary = todos.filter((td) => td.temporary && !td.project && openTodo(td)).sort(newest);
  const unsorted = todos.filter((td) => isUnsorted(td)).sort(newest);
  return { temporary, unsorted };
}
