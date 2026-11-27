// @vitest-environment jsdom
// Overview by project (#13019): a card per top-level project of the tree, with the same counts as the project's node
// panel on the knowledge map (ProjectHome), the empty state, and Temporary and Unsorted with the way to the sort sheet.
// The data made up.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n, t } from "../../i18n";
import { setToken } from "../../lib/api";
import { nodeId, treeOf, type TreeEntry } from "../../lib/kb-tree";
import { type MapNode } from "../../lib/kb-map";
import { setRosterDefault } from "../../lib/manager-choice";
import type { Todo } from "../../lib/todos";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const fake = vi.hoisted(() => ({ todos: [] as unknown[] }));
vi.mock("../../lib/store", async (importOriginal) => {
  const real = await importOriginal<typeof import("../../lib/store")>();
  return { ...real, useStore: <T,>(select: (s: ReturnType<typeof real.getState>) => T) => select({ ...real.getState(), loaded: true, todos: fake.todos as Todo[] }) };
});
vi.mock("../todos/SortTodos", () => ({ default: ({ onClose }: { onClose: () => void }) => <div data-sort-sheet=""><button type="button" onClick={onClose}>close</button></div> }));

const { default: ProjectOverview } = await import("./ProjectOverview");
const { ProjectHome } = await import("../knowledge/ProjectTree");

type Request = { url: string; init: RequestInit; resolve: (value: Response) => void; done?: boolean };
let requests: Request[];
let host: HTMLDivElement;
let root: Root;
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
/** Answers every open request whose path ends so. */
const answerAll = async (path: string, value: (r: Request) => unknown) => {
  for (const r of requests.filter((x) => !x.done && x.url.split("?")[0].endsWith(path))) {
    r.done = true;
    await act(async () => r.resolve(json(value(r))));
  }
};

const y = new Date(Date.now() - 86_400_000);
const yesterday = `${y.getFullYear()}-${String(y.getMonth() + 1).padStart(2, "0")}-${String(y.getDate()).padStart(2, "0")}`;
const p = (id: string, name: string, extra: Partial<TreeEntry> = {}): TreeEntry => ({ kind: "projects", id, name, relevance: 0, extra: "short · active", ...extra });
const items = [
  p("garden-app", "Garden app", { one_line: "Plan and water a small garden", milestones: [{ id: "m1", title: "Public beta", target: "2026-12-15", state: "open" }, { id: "m2", title: "Alpha", target: "2026-10-01", state: "accepted" }] }),
  p("seed-planner", "Seed planner", { parent: "garden-app" }),
  p("watering", "Watering reminders", { parent: "garden-app" }),
  p("rain", "Rain skips", { parent: "watering" }),
  p("docs-site", "Docs site"),
];
const td = (id: string, extra: Partial<Todo> = {}) => ({ id, title: `To-do ${id}`, created_at: "2026-11-20T10:00:00Z", ...extra });
const todos = [
  td("a", { project: "garden-app" }),
  td("b", { project: "seed-planner" }),
  td("c", { project: "rain" }),
  td("d", { project: "rain", done_at: "2026-11-21T10:00:00Z" }),
  td("e", { project: "docs-site" }),
  td("t1", { temporary: true, title: "Buy printer paper" }),
  td("u1", { title: "Think about the team offsite" }),
  td("u2", { title: "Try the new icon set" }),
];
const detail = (r: Request) => {
  const slug = decodeURIComponent(r.url.split("/kb/projects/")[1].split("?")[0]);
  const log = slug === "garden-app" ? [`- ${yesterday} 09:40 · Persona · Beta build sent to 20 testers`, `- ${yesterday} 17:05 · Persona · Two crash reports, fix planned`] : [];
  return { detail: { kind: "projects", id: slug, log, meta: {} } };
};

beforeEach(async () => {
  await initI18n("en");
  localStorage.clear();
  setToken("synthetic-owner-a");
  setRosterDefault("assistant");
  fake.todos = todos;
  requests = [];
  vi.stubGlobal("fetch", vi.fn((url: string, init: RequestInit) => new Promise<Response>((resolve) => requests.push({ url, init: init ?? {}, resolve }))));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

async function renderOverview(list: TreeEntry[] = items) {
  await act(async () => root.render(<ProjectOverview />));
  await answerAll("/kb/projects", () => ({ items: list }));
  await answerAll("/kb/projects/garden-app", detail);
  await answerAll("/kb/projects/docs-site", detail);
}
const card = (id: string) => host.querySelector(`[data-project-card="${id}"]`) as HTMLElement;

describe("Overview by project", () => {
  it("draws one card per top-level project with its sub-projects, milestones, open to-dos and latest note", async () => {
    await renderOverview();
    expect([...host.querySelectorAll("[data-project-card]")].map((c) => c.getAttribute("data-project-card"))).toEqual(["docs-site", "garden-app"]);
    const garden = card("garden-app");
    expect(garden.querySelector("[data-card-todos]")?.getAttribute("data-card-todos")).toBe("3");
    expect(garden.querySelector("[data-card-children]")?.textContent).toContain("Seed planner, Watering reminders");
    expect(garden.querySelector("[data-card-milestones]")?.textContent).toContain("Public beta");
    expect(garden.querySelector("[data-card-milestones]")?.textContent).toContain(t("lib-kb-tree.state_accepted"));
    expect(garden.querySelector("[data-card-note]")?.textContent).toContain("Two crash reports, fix planned");
    expect(card("docs-site").querySelector("[data-card-note]")?.textContent).toContain(t("overview-projects.no_yesterday"));
    // the card opens the project tree with the project selected
    expect(garden.querySelector("a")?.getAttribute("href")).toMatch(/\/kb\/map\/garden-app$/);
  });

  it("counts as the project's node panel on the map does", async () => {
    await renderOverview();
    const overview = new Map([...host.querySelectorAll("[data-project-card]")].map((c) => [
      c.getAttribute("data-project-card")!,
      { todos: c.querySelector("[data-card-todos]")!.getAttribute("data-card-todos"), children: c.querySelector("[data-card-children]")!.getAttribute("data-card-children"), milestones: c.querySelector("[data-card-milestones]")?.getAttribute("data-card-milestones") ?? "0" },
    ]));
    act(() => root.unmount());
    const { parents } = treeOf(items);
    const data = { list: items, parents, error: "", refresh: () => {}, place: () => {} };
    for (const [slug, want] of overview) {
      root = createRoot(host);
      const e = items.find((x) => x.id === slug)!;
      const node: MapNode = { id: nodeId(slug), title: e.name, kind: "pnode", tone: "accent", kids: [], project: e };
      await act(async () => root.render(<ProjectHome node={node} data={data} onOpen={() => {}} onMenu={() => {}} milestoneAsk={0} />));
      await answerAll(`/kb/projects/${slug}`, (r) => ({ ...detail(r), tree: { ancestors: [], children: [], milestones: e.milestones ?? [], depth: 1 } }));
      const panel = host.querySelector(`[data-project-home="${slug}"]`)!;
      expect(panel.querySelector("[data-project-todos]")?.getAttribute("data-project-todos")).toBe(want.todos);
      expect(panel.querySelector("[data-project-children]")?.getAttribute("data-project-children")).toBe(want.children);
      expect(panel.querySelector("[data-milestones]")?.getAttribute("data-milestones")).toBe(want.milestones);
      act(() => root.unmount());
    }
    root = createRoot(host);
  });

  it("says there are no projects yet, and still shows Temporary and Unsorted", async () => {
    await renderOverview([]);
    expect(host.querySelector("[data-project-overview-empty]")?.textContent).toContain(t("overview-projects.empty_title"));
    expect(host.querySelector('[data-loose="temporary"]')?.textContent).toContain("Buy printer paper");
    const unsorted = host.querySelector('[data-loose="unsorted"]')!;
    expect(unsorted.textContent).toContain("Think about the team offsite");
    expect(unsorted.textContent).toContain("Try the new icon set");
    expect(unsorted.textContent).not.toContain("Buy printer paper");
  });

  it("opens the sort sheet from Unsorted", async () => {
    await renderOverview();
    const sort = host.querySelector("[data-overview-sort]") as HTMLButtonElement;
    expect(sort.textContent).toBe(t("overview-projects.sort", { count: 2 }));
    await act(async () => sort.click());
    await vi.waitFor(() => expect(host.querySelector("[data-sort-sheet]")).toBeTruthy());
    await act(async () => (host.querySelector("[data-sort-sheet] button") as HTMLButtonElement).click());
    expect(host.querySelector("[data-sort-sheet]")).toBeNull();
  });

  it("has no Sort link and says so when every open to-do has a project", async () => {
    fake.todos = todos.filter((x) => x.project);
    await renderOverview();
    expect(host.querySelector("[data-overview-sort]")).toBeNull();
    expect(host.querySelector('[data-loose="unsorted"]')?.textContent).toContain(t("overview-projects.unsorted_empty"));
  });
});
