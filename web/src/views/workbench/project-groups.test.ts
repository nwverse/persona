// The Workbench sidebar's grouping (#13439): projects by repository folder across computers and worktrees, the ones that
// need you first, counts equal to the sidebar's own state counts, sessions without a folder in "Other", same-named
// projects told apart in plain words, assistant and hidden rows left out, and a deterministic order.
import { describe, expect, it } from "vitest";
import { stateCounts } from "../../lib/sessions";
import type { Machine, Session } from "../../lib/types";
import { flatThreads, folderName, groupByProject, sortThreads } from "./project-groups";

const machines: Machine[] = [{ id: "mac", name: "MacBook", online: true }, { id: "box", name: "Linux box", online: true }];
const row = (sid: string, cwd: string, extra: Partial<Session> = {}): Session => ({ machine_id: "mac", session_id: sid, title: `T ${sid}`, cwd, state: "idle", status: "idle", updated_at: "2026-11-01T10:00:00Z", ...extra });
const needs = (sid: string, cwd: string, extra: Partial<Session> = {}) => row(sid, cwd, { state: "needs_you", status: "blocked", ...extra });
const group = (list: Session[]) => groupByProject(list, machines, "Other");

describe("groupByProject children", () => {
  it("folds a child under its listed parent: no row, no count of its own, the parent carries the wait", () => {
    const parent = row("p", "/code/app");
    const child = needs("c", "/code/app", { folded_under: "p", folded_as: "worker" });
    const [p] = group([parent, child]);
    expect(p.threads.map((s) => s.session_id)).toEqual(["p"]);
    expect(p.needs).toBeLessThanOrEqual(1);
  });
});

describe("groupByProject", () => {
  it("groups by the repository's folder name, worktrees of one repository together", () => {
    const projects = group([row("a", "/code/app"), row("b", "/code/app/.worktrees/feat"), row("c", "/code/api")]);
    expect(projects.map((p) => [p.label, p.threads.map((s) => s.session_id).sort()])).toEqual(expect.arrayContaining([["app", ["a", "b"]], ["api", ["c"]]]));
    expect(projects).toHaveLength(2);
  });

  it("labels a project by its folder name only, never a path", () => {
    const [p] = group([row("a", "/home/me/secret/place/app")]);
    expect(p.label).toBe("app");
    expect(p.label).not.toContain("/");
  });

  it("uses the host's repository root when the session carries one", () => {
    const [p] = group([row("a", "/data/trees/x1", { repo_root: "/code/app" })]);
    expect(p.label).toBe("app");
    expect(p.cwd).toBe("/code/app");
  });

  it("puts the projects with a thread needing you first, then the most recently changed", () => {
    const projects = group([
      row("old", "/code/old", { updated_at: "2026-10-01T00:00:00Z" }),
      row("new", "/code/new", { updated_at: "2026-11-05T00:00:00Z" }),
      needs("n", "/code/wait", { updated_at: "2026-09-01T00:00:00Z" }),
    ]);
    expect(projects.map((p) => p.label)).toEqual(["wait", "new", "old"]);
    expect(projects[0].needs).toBe(1);
  });

  it("counts needs-you per project so the total equals the sidebar's own count", () => {
    const all = [needs("a", "/code/x"), needs("b", "/code/y"), row("c", "/code/y"), row("d", "")];
    const projects = group(all);
    expect(projects.reduce((n, p) => n + p.needs, 0)).toBe(stateCounts(all).needs);
  });

  it("sends sessions with no folder to Other, last", () => {
    const projects = group([row("a", ""), needs("b", "/code/x"), row("c", "/code/z", { updated_at: "2026-01-01T00:00:00Z" })]);
    expect(projects.at(-1)).toMatchObject({ label: "Other", cwd: "", machineId: "" });
    expect(projects.at(-1)!.threads.map((s) => s.session_id)).toEqual(["a"]);
  });

  it("tells same-named projects apart by the computer, else by the folder holding them", () => {
    const apart = group([row("a", "/code/app"), row("b", "/code/app", { machine_id: "box" })]);
    expect(apart.map((p) => p.label).sort()).toEqual(["app · Linux box", "app · MacBook"]);
    const same = group([row("a", "/work/one/app"), row("b", "/work/two/app")]);
    expect(same.map((p) => p.label).sort()).toEqual(["app · one", "app · two"]);
  });

  it("leaves assistant conversations and rows hidden by a directory out", () => {
    const projects = group([row("a", "/code/x", { assistant: true }), row("b", "/code/x", { hidden_by_directory: true }), row("c", "/code/x")]);
    expect(flatThreads(projects).map((s) => s.session_id)).toEqual(["c"]);
  });

  it("orders threads needing you, then working, then the rest, newest first, and is deterministic", () => {
    const list = [
      row("idle-new", "/c/x", { updated_at: "2026-11-09T00:00:00Z" }),
      row("work", "/c/x", { state: "working", status: "working", updated_at: "2026-11-01T00:00:00Z" }),
      needs("ask", "/c/x", { updated_at: "2026-10-01T00:00:00Z" }),
      row("idle-old", "/c/x", { updated_at: "2026-10-05T00:00:00Z" }),
    ];
    expect(sortThreads(list).map((s) => s.session_id)).toEqual(["ask", "work", "idle-new", "idle-old"]);
    expect(flatThreads(group(list))).toEqual(flatThreads(group([...list].reverse())));
  });
});

describe("folderName", () => {
  it("keeps only the last folder of a Windows or POSIX path", () => {
    expect(folderName("C:\\Users\\me\\code\\Weather")).toBe("Weather");
    expect(folderName("D:\\work\\Notes\\")).toBe("Notes");
    expect(folderName("/home/me/code/weather")).toBe("weather");
    expect(folderName("weather")).toBe("weather");
  });
});
