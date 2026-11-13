import { describe, expect, it } from "vitest";
import { buildRepoMachineTree, sessionWorkspace, showsWorktreeLevel, type RepoCatalogOptions, type RepoWorktree } from "./agent-repos";
import type { HostWorkspace } from "./workspace-catalog";
import type { Machine, Session } from "./types";

const machines: Machine[] = [{ id: "machine-a", name: "Computer A", online: true }, { id: "machine-b", name: "Computer B", online: true }];
const workspace = (id: string, patch: Partial<HostWorkspace> = {}): HostWorkspace => ({ id: `qualified-${id}`, machine_id: "machine-a", source: "orca", source_id: id, title: id, cwd: `/code/${id}`, project_id: "project-a", project_name: "Project A", ...patch });
const session = (id: string, workspaceId: string, patch: Partial<Session> = {}): Session => ({ session_id: id, machine_id: "machine-a", title: id, cwd: "/elsewhere", state: "idle", status: "idle", space_kind: "orca", space_id: workspaceId, ...patch });
const options = (workspaces: HostWorkspace[], patch: Partial<RepoCatalogOptions> = {}): RepoCatalogOptions => ({ owner: "credential-a", manager: "manager-a", workspaces, ...patch });
const flatten = (rows: RepoWorktree[]): RepoWorktree[] => rows.flatMap((w) => [w, ...flatten(w.children)]);
const rows = (all: Session[], opts: RepoCatalogOptions) => buildRepoMachineTree(all, machines, opts).flatMap((m) => m.repos.flatMap((r) => flatten(r.worktrees)));
const desktops = [["codex_app", "codex-project:home"], ["claude_desktop", "claude-environment:account"]] as const;

describe("catalog-backed By repo projection", () => {
  it("preserves three empty ancestors, a standalone empty workspace, metadata and zero Agent counts", () => {
    const catalog = [workspace("root", { is_main: true }), workspace("middle", { parent_source_id: "root" }), workspace("leaf", { parent_source_id: "middle", workspace_kind: "folder", head: "abc" }), workspace("standalone")];
    const tree = buildRepoMachineTree([], machines, options(catalog));
    expect(tree).toHaveLength(1);
    const repo = tree[0].repos[0];
    expect(repo.count).toBe(0);
    expect(flatten(repo.worktrees).map((w) => w.name)).toEqual(["root", "middle", "leaf", "standalone"]);
    expect(rows([], options(catalog)).find((w) => w.name === "leaf")?.workspace).toBe(catalog[2]);
    expect(repo.worktrees[0].childStates).toEqual({ needs_you: 0, working: 0, done: 0 });
    expect(showsWorktreeLevel(buildRepoMachineTree([], machines, options([catalog[0]]))[0].repos[0])).toBe(true);
  });

  it("follows a same-host parent in another project and retains the child's own project/main metadata", () => {
    const catalog = [workspace("root"), workspace("child", { parent_source_id: "root", project_id: "project-b", project_name: "Project B", is_main: true })];
    const tree = buildRepoMachineTree([session("agent", "child")], machines, options(catalog));
    expect(tree[0].repos).toHaveLength(1);
    expect(tree[0].repos[0].count).toBe(1);
    const root = tree[0].repos[0].worktrees[0];
    expect(root.children[0].workspace?.project_name).toBe("Project B");
    expect(root.children[0].main).toBe(true);
    expect(root.sessions).toEqual([]);
    expect(root.tree.map((s) => s.session_id)).toEqual(["agent"]);
  });

  it("never merges equal paths or equal source IDs across machines, hosts or owners", () => {
    const catalog = [workspace("shared", { cwd: "/code/shared" }), workspace("shared", { cwd: "/code/shared", source: "herdr" }), workspace("shared", { machine_id: "machine-b", cwd: "/code/shared" })];
    const agents = [session("same", "shared"), session("same", "shared", { space_kind: "herdr", workspace_id: "shared" }), session("same", "shared", { machine_id: "machine-b" })];
    const actual = rows(agents, options(catalog));
    expect(new Set(actual.map((w) => w.key)).size).toBe(3);
    expect(actual.every((w) => w.sessions.length === 1)).toBe(true);
    expect(rows(agents, options(catalog, { owner: "credential-b" })).every((w) => !actual.some((a) => a.key === w.key))).toBe(true);
    expect(rows(agents, options(catalog, { manager: "manager-b" })).every((w) => !actual.some((a) => a.key === w.key))).toBe(true);
  });

  it("uses explicit placement ahead of directory inference and refuses a foreign host's binding", () => {
    const catalog = [workspace("a", { cwd: "/code/a" }), workspace("b", { cwd: "/code/b" })];
    expect(sessionWorkspace(session("agent", "b", { cwd: "/code/a", workspace_id: "b" }), catalog)?.source_id).toBe("b");
    expect(sessionWorkspace(session("foreign", "b", { cwd: "/code/a", space_kind: "herdr", workspace_id: "b" }), catalog)).toBeUndefined();
    expect(sessionWorkspace(session("unknown", "missing", { cwd: "/code/a" }), catalog)).toBeUndefined();
    expect(sessionWorkspace(session("other-manager", "a", { manager: "manager-b" }), catalog, "manager-a")).toBeUndefined();
  });

  it.each(desktops)("keeps each %s project root visible and assigns Agents to the deepest matching root", (source, prefix) => {
    const project = `${prefix}:project-a`;
    const root = workspace("root", { source, source_id: project, cwd: "C:\\Projects\\App" });
    const child = { ...root, id: "child-root", title: "Child", cwd: "C:\\Projects\\App\\Packages\\Child" };
    const docs = { ...root, id: "docs-root", title: "Docs", cwd: "D:\\Docs" };
    const agents = [
      session("main", project, { space_kind: source, cwd: "c:/projects/app/src" }),
      session("child", project, { space_kind: source, cwd: "c:/projects/app/packages/child/src" }),
      session("docs", project, { space_kind: source, cwd: "d:/docs/pages" }),
    ];
    const actual = rows(agents, options([root, child, docs]));
    expect(actual).toHaveLength(3);
    expect(new Set(actual.map((w) => w.key)).size).toBe(3);
    for (const [w, id] of [[root, "main"], [child, "child"], [docs, "docs"]] as const) {
      expect(actual.find((entry) => entry.workspace === w)?.sessions.map((s) => s.session_id)).toEqual([id]);
    }
  });

  it.each(desktops)("uses a %s linked worktree's common Git root when its cwd is outside the project's roots", (source, prefix) => {
    const project = `${prefix}:project-a`;
    const app = workspace("app", { source, source_id: project, cwd: "C:\\Projects\\App" });
    const docs = { ...app, id: "docs-root", cwd: "D:\\Docs" };
    const agent = session("worktree", project, { space_kind: source, cwd: "C:\\Worktrees\\fix", repo_root: "c:/projects/app" });
    expect(sessionWorkspace(agent, [app, docs])).toBe(app);
    expect(sessionWorkspace({ ...agent, cwd: "d:/docs/pages" }, [app, docs])).toBe(docs);
    expect(sessionWorkspace({ ...agent, repo_root: undefined, workspace_cwd: "D:\\Docs" }, [app, docs])).toBe(docs);
    expect(sessionWorkspace({ ...agent, repo_root: undefined }, [app, docs])).toBeUndefined();
  });

  it.each(desktops)("never cross-binds saved %s projects that share a directory", (source, prefix) => {
    const a = workspace("a", { source, source_id: `${prefix}:a`, cwd: "C:\\Projects\\App" });
    const b = { ...a, id: "project-b-root", source_id: `${prefix}:b` };
    const otherRoot = { ...a, id: "project-a-docs", cwd: "D:\\Docs" };
    const agent = session("agent", a.source_id, { space_kind: source, cwd: "c:/projects/app/src" });
    expect(sessionWorkspace(agent, [a, b, otherRoot])).toBe(a);
    expect(sessionWorkspace({ ...agent, space_id: b.source_id }, [a, b, otherRoot])).toBe(b);
    expect(sessionWorkspace(agent, [b])).toBeUndefined();
    expect(sessionWorkspace({ ...agent, cwd: "C:/Unrelated" }, [a, b, otherRoot])).toBeUndefined();
    expect(sessionWorkspace(agent, [a, { ...a, id: "duplicate-root" }, otherRoot])).toBeUndefined();
  });

  it.each(desktops)("matches directory-derived %s placement only when a unique saved project contains its directory", (source, prefix) => {
    const project = workspace("app", { source, source_id: `${prefix}:saved`, cwd: "C:\\Projects\\App" });
    const directoryPrefix = source === "codex_app" ? "codex-project:" : "claude-project:";
    const inferred = session("unassigned", `${directoryPrefix}C:\\Projects\\App`, { space_kind: source, cwd: "c:/projects/app/src" });
    expect(sessionWorkspace(inferred, [project])).toBe(project);
    expect(sessionWorkspace({ ...inferred, cwd: "C:\\Users\\User\\.codex\\worktrees\\fix" }, [project])).toBe(project);
    expect(sessionWorkspace(inferred, [project, { ...project, id: "other", source_id: `${prefix}:other` }])).toBeUndefined();
  });

  it("attributes unbound native Agents only to a unique deepest containing directory and respects worktree markers", () => {
    const a = workspace("a", { cwd: "/code/app" }), b = workspace("b", { cwd: "/code/app/child" });
    const native = session("native", "", { space_kind: undefined, cwd: "/code/app/child/src" });
    expect(sessionWorkspace(native, [a, b])).toBe(b);
    expect(sessionWorkspace(native, [a, b, { ...b, source_id: "other-id" }])).toBeUndefined();
    expect(sessionWorkspace(native, [a, b, { ...b, source: "herdr" }])).toBeUndefined();
    expect(sessionWorkspace({ ...native, space_id: "b" }, [a, b, { ...b, cwd: "/different", source: "herdr" }])).toBeUndefined();
    expect(sessionWorkspace({ ...native, cwd: "/tmp" }, [workspace("root", { cwd: "/" })])?.source_id).toBe("root");
    expect(sessionWorkspace({ ...native, cwd: "/code/app-copy/src" }, [a])).toBeUndefined();
    expect(sessionWorkspace({ ...native, cwd: "/code/app/.worktrees/independent" }, [a])).toBeUndefined();
    expect(sessionWorkspace({ ...native, cwd: "/outside", workspace_cwd: "/code/app/child" }, [a, b])).toBe(b);
    expect(sessionWorkspace({ ...native, machine_id: "machine-b" }, [a, b])).toBeUndefined();
  });

  it("keeps same-host orphans and cycles visible and never resolves parents across hosts or machines", () => {
    const catalog = [workspace("a", { parent_source_id: "b" }), workspace("b", { parent_source_id: "a" }), workspace("self", { parent_source_id: "self" }), workspace("orphan", { parent_source_id: "missing" }), workspace("foreign", { parent_source_id: "b", source: "herdr" }), workspace("elsewhere", { parent_source_id: "a", machine_id: "machine-b" })];
    const actual = rows([], options(catalog));
    expect(actual).toHaveLength(6);
    expect(actual.every((w) => w.children.length === 0)).toBe(true);
  });

  it("state/source filters retain only actual matches and their empty ancestors, including pinned direct navigation", () => {
    const catalog = [workspace("root"), workspace("middle", { parent_source_id: "root" }), workspace("leaf", { parent_source_id: "middle" }), workspace("empty")];
    const pinned = session("pinned", "leaf", { state: "needs_you", status: "needs_you" });
    const actual = rows([], options(catalog, { navigationSessions: [pinned], hideEmpty: true }));
    expect(actual.map((w) => w.name)).toEqual(["root", "middle", "leaf"]);
    expect(actual.every((w) => w.sessions.length === 0)).toBe(true);
    expect(actual[0].directSessions).toEqual([]);
    expect(actual[2].directSessions).toEqual([pinned]);
    expect(rows([], options(catalog, { navigationSessions: [], hideEmpty: true }))).toEqual([]);
  });

  it("search preserves a matching empty workspace path or Agent path without retaining unrelated siblings", () => {
    const catalog = [workspace("root"), workspace("middle", { parent_source_id: "root" }), workspace("leaf", { parent_source_id: "middle" }), workspace("unrelated")];
    expect(rows([], options(catalog, { search: "leaf" })).map((w) => w.name)).toEqual(["root", "middle", "leaf"]);
    expect(rows([session("needle", "leaf")], options(catalog, { search: "needle" })).map((w) => w.name)).toEqual(["root", "middle", "leaf"]);
  });

  it("navigates direct pinned Agents in display order without drawing them or counting them again", () => {
    const catalog = [workspace("root"), workspace("child", { parent_source_id: "root" })];
    const pinned = session("pinned", "root", { state: "needs_you", status: "needs_you" });
    const ordinary = session("ordinary", "root");
    const descendant = session("descendant", "child");
    const actual = rows([ordinary, descendant], options(catalog, { navigationSessions: [ordinary, pinned, descendant] }));
    expect(actual[0].directSessions?.map((s) => s.session_id)).toEqual(["pinned", "ordinary"]);
    expect(actual[0].sessions).toEqual([ordinary]);
    expect(actual[0].tree).toHaveLength(2);
  });

  it("keeps attribution ambiguous when a source is hidden and retains legacy pinned navigation without duplicate counts", () => {
    const a = workspace("a", { cwd: "/code/app" }), b = workspace("b", { cwd: "/code/app", source: "botmux" });
    const native = session("native", "", { space_kind: undefined, cwd: "/code/app" });
    const actual = rows([native], options([a, b], { hiddenSources: ["botmux"], navigationSessions: [native] }));
    expect(actual.find((w) => w.workspace === a)?.sessions).toEqual([]);
    expect(actual.some((w) => w.workspace === b)).toBe(false);
    const pinned = session("pinned", "project::/code/app");
    const legacy = rows([], options([], { navigationSessions: [pinned] }));
    expect(legacy[0].directSessions).toEqual([pinned]);
    expect(legacy[0].sessions).toEqual([]);
    expect(legacy[0].tree).toEqual([]);
  });

  it("retains offline/empty folder workspaces and uses session-only fallback when discovery is absent", () => {
    const offline = workspace("folder", { workspace_kind: "folder", online: false, available: false });
    expect(rows([], options([offline]))[0].target?.workspaceId).toBe("folder");
    const agent = session("legacy", "", { cwd: "/code/legacy", space_kind: undefined });
    expect(buildRepoMachineTree([agent], machines, options([]))).toEqual(buildRepoMachineTree([agent], machines));
  });
});
