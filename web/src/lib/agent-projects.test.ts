import { describe, expect, it } from "vitest";
import { buildProjectAgentList, projectHeads, shortTitle, type AgentProject } from "./agent-projects";
import { agentGrouping } from "./agent-groups";
import { todoProjectsOf } from "./todo-projects";
import type { Todo } from "./todos";
import type { Session } from "./types";

const session = (id: string, state: Session["state"], extra: Partial<Session> = {}): Session =>
  ({ machine_id: "m1", session_id: id, title: `Session ${id}`, cwd: "/w", status: "", state, ...extra }) as Session;
const todo = (id: string, title: string, extra: Partial<Todo> = {}): Todo => ({ id, title, ...extra }) as Todo;

const projects: AgentProject[] = [
  { slug: "app", name: "Persona app" },
  { slug: "board", name: "Project board", parent: "app" },
  { slug: "trip", name: "Family trip" },
];

describe("Agents By project (knowledge base)", () => {
  it("is the default grouping for a browser that never chose one", () => {
    expect(agentGrouping(null)).toBe("kbproject");
    expect(agentGrouping("todo")).toBe("todo");
  });

  it("reads parents and Overview pins from the project list", () => {
    const list = todoProjectsOf([{ id: "board", name: "Board", parent: "app", overview_pinned: true, extra: "short · active" }]);
    expect(list[0]).toMatchObject({ slug: "board", parent: "app", pinned: true });
  });

  it("rolls a sub-project up to its top-level project, or to the nearest one pinned to Overview", () => {
    expect(projectHeads(projects)("board")).toBe("app");
    expect(projectHeads(projects)("unknown")).toBe("unknown");
    const pinned = projects.map((p) => (p.slug === "board" ? { ...p, pinned: true } : p));
    expect(projectHeads(pinned)("board")).toBe("board");
    // a project with no pinned project above it keeps its top-level heading
    expect(projectHeads(pinned)("trip")).toBe("trip");
  });

  it("puts each Agent under its to-do's project, by state, Needs you on top and Unassigned last", () => {
    const todos = [
      todo("t1", "Agent list by project", { project: "board" }),
      todo("t2", "Plan the trip", { project: "trip" }),
      todo("t3", "One-off", { project: "trip", temporary: true }),
      todo("t4", "Fix the sync", { project: "app" }),
    ];
    const sessions = [
      session("a", "working", { todo: { id: "t1", title: "Agent list by project" } }),
      session("b", "needs_you", { todo: { id: "t2", title: "Plan the trip" } }),
      session("c", "unread", { todo: { id: "t4", title: "Fix the sync" } }),
      session("d", "idle", { todo: { id: "t3", title: "One-off" } }),
      session("e", "idle"),
      // on a to-do through the to-do's own agents list only
      session("f", "idle"),
    ];
    todos[1].agents = [{ machine_id: "m1", session_id: "f" } as NonNullable<Todo["agents"]>[number]];
    const list = buildProjectAgentList(sessions, todos, projects, { unassigned: "Unassigned" });
    expect(list.needsYou.map((s) => s.session_id)).toEqual(["b"]);
    expect(list.groups.map((g) => [g.key, g.count, g.needs, g.quiet])).toEqual([
      ["trip", 2, 1, false],
      ["app", 2, 0, false],
      ["", 2, 0, true],
    ]);
    const app = list.groups.find((g) => g.key === "app")!;
    expect(app.sections.map((x) => [x.key, x.sessions.map((s) => s.session_id)])).toEqual([["working", ["a"]], ["just_finished", ["c"]]]);
    expect(list.rows.get("m1/a")).toEqual({ title: "Agent list by project", sub: "Project board", head: "Persona app" });
    expect(list.groups.at(-1)!.rest).toBe(true);
    expect(list.groups.at(-1)!.name).toBe("Unassigned");
  });

  it("calls a to-do's only Agent by the to-do, and several on one to-do by their own short titles", () => {
    const todos = [todo("t1", "Ship it", { project: "app" })];
    const one = buildProjectAgentList([session("a", "working", { todo: { id: "t1", title: "Ship it" } })], todos, projects, { unassigned: "-" });
    expect(one.rows.get("m1/a")!.title).toBe("Ship it");
    const two = buildProjectAgentList([
      session("a", "working", { todo: { id: "t1", title: "Ship it" }, title: "Write the tests" }),
      session("b", "working", { todo: { id: "t1", title: "Ship it" }, title: "Review the diff" }),
    ], todos, projects, { unassigned: "-" });
    expect([two.rows.get("m1/a")!.title, two.rows.get("m1/b")!.title]).toEqual(["Write the tests", "Review the diff"]);
  });

  it("cuts a prompt opener down to a short title", () => {
    expect(shortTitle("<task>\nFix the login bug. Then also check the logs and report back")).toBe("Fix the login bug");
    expect(shortTitle("（督办：验收没通过）Agent列表按项目分组。用户要求如下")).toBe("Agent列表按项目分组");
    const long = shortTitle("这是一个非常非常长的标题它没有任何标点符号所以必须在大约二十个字之后被截断才对");
    expect(long.endsWith("…")).toBe(true);
    expect(long.length).toBeLessThanOrEqual(21);
  });
});
