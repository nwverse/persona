// Overview by project's counting (#13019, lib/project-overview.ts): a project's sub-projects, milestones and open
// to-dos on it and its subtree; the top-level projects; Temporary and Unsorted.
import { describe, expect, it } from "vitest";
import { treeOf, type TreeEntry } from "./kb-tree";
import { looseTodos, projectFacts, topProjects } from "./project-overview";
import type { Todo } from "./todos";

const p = (id: string, name: string, extra: Partial<TreeEntry> = {}): TreeEntry => ({ kind: "projects", id, name, relevance: 0, extra: "short · active", ...extra });
const list = [
  p("garden-app", "Garden app", { milestones: [{ id: "m1", title: "Public beta", target: "2026-12-15", state: "open" }] }),
  p("seeds", "Seeds", { parent: "garden-app" }),
  p("watering", "Watering", { parent: "garden-app" }),
  p("rain", "Rain skips", { parent: "watering" }),
  p("docs-site", "Docs site"),
  p("old", "Old merged", { extra: "short · merged" }),
];
const td = (id: string, extra: Partial<Todo> = {}): Todo => ({ id, title: id, created_at: `2026-11-2${id.length % 10}T10:00:00Z`, ...extra }) as Todo;
const todos = [
  td("a", { project: "garden-app" }),
  td("b", { project: "seeds" }),
  td("c", { project: "rain" }),
  td("d", { project: "rain", done_at: "2026-11-20T10:00:00Z" }),
  td("e", { project: "garden-app", kind: "concern" }),
  td("f", { project: "docs-site" }),
  td("g", { temporary: true }),
  td("h"),
  td("i", { done_at: "2026-11-20T10:00:00Z" }),
];

describe("projectFacts", () => {
  const { parents } = treeOf(list);
  it("counts open to-dos on the project and everything below it, not done ones or concerns", () => {
    const f = projectFacts(list, parents, todos, "garden-app");
    expect(f.openTodos).toBe(3);
    expect(f.ownTodos).toBe(1);
    expect(f.children.map((c) => c.name)).toEqual(["Seeds", "Watering"]);
    expect(f.flag).toEqual({ text: "12-15", state: "open" });
  });
  it("counts a sub-project's own subtree only", () => {
    expect(projectFacts(list, parents, todos, "watering")).toMatchObject({ openTodos: 1, ownTodos: 0 });
    expect(projectFacts(list, parents, todos, "docs-site")).toMatchObject({ openTodos: 1, ownTodos: 1, children: [] });
  });
});

describe("topProjects", () => {
  it("lists the live top-level projects by name", () => {
    expect(topProjects(list, treeOf(list).parents).map((e) => e.id)).toEqual(["docs-site", "garden-app"]);
  });
});

describe("looseTodos", () => {
  it("splits the to-dos in no project into Temporary and Unsorted", () => {
    const { temporary, unsorted } = looseTodos(todos);
    expect(temporary.map((x) => x.id)).toEqual(["g"]);
    expect(unsorted.map((x) => x.id)).toEqual(["h"]);
  });
});
