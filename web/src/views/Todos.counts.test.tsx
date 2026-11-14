// @vitest-environment jsdom
// Audit items 12 and 83: the To-dos page's header counts. One definition in every grouping: Not done is the open count
// the sidebar's badge shows (lib/todo-triage countsAsOpen), Done the to-dos in the Done section, and the folders keep
// what the status sections show open while their Done fold holds the Done section's, so switching grouping changes no
// number and each one matches a section's. A failed start waiting on the owner is open in every grouping, not done in
// the folders. No counts while the list is still loading. The hub is faked; the data made up.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Todo } from "../lib/todos";

vi.hoisted(() => {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia;
});

const api = vi.fn(async (_path: string, _init?: unknown): Promise<unknown> => ({}));
vi.mock("../lib/api", async (orig) => ({
  ...(await orig<typeof import("../lib/api")>()),
  api: (path: string, init?: unknown) => api(path, init),
}));

const { getState, refreshTodos } = await import("../lib/store");
const { TodosView } = await import("./Todos");
const { triageTodos } = await import("../lib/todo-triage");
const { t } = await import("../i18n");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};
const iso = (agoMs: number) => new Date(Date.now() - agoMs).toISOString();
const DAY = 86_400_000;
const todos = [
  // the user's own, open: Other open
  { id: "own", title: "Book the dentist", created_at: iso(DAY), updated_at: iso(DAY) },
  // a task whose agent never started, closed by the hub with why: Needs you, open
  { id: "failed", title: "Rotate the keys", origin: "conv", created_at: iso(2 * 3_600_000), updated_at: iso(3_600_000), done_at: iso(3_600_000), start_error: "machine offline", agents: [] },
  // closed an hour ago: Just finished, closed
  { id: "fresh", title: "Water the plants", created_at: iso(DAY), updated_at: iso(3_600_000), done_at: iso(3_600_000) },
  // closed days ago: Done
  { id: "old1", title: "Pay the rent", created_at: iso(5 * DAY), updated_at: iso(3 * DAY), done_at: iso(3 * DAY) },
  { id: "old2", title: "Renew the library card", created_at: iso(6 * DAY), updated_at: iso(4 * DAY), done_at: iso(4 * DAY) },
] as unknown as Todo[];
const seed = (patch: Record<string, unknown>) => Object.assign(getState() as unknown as Record<string, unknown>, patch);
const before = { ...getState() };

beforeEach(() => {
  api.mockReset();
  api.mockImplementation(async () => ({}));
  localStorage.clear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  document.body.innerHTML = "";
  localStorage.clear();
  seed(before);
});

const render = async (grouping?: "status" | "folder") => {
  if (grouping) localStorage.setItem("persona.todos.grouping", grouping);
  seed({ todos, todosLoaded: true, sessions: {}, interactions: [], answered: {} });
  await act(async () => root.render(<TodosView />));
  await flush();
};
const summary = () => host.querySelector("[data-testid=todos-summary]")?.textContent;
const expected = t("todos.summary_triage", { open: 2, parked: 0, shelved: 0, done: 2 });

describe("the To-dos header's counts", () => {
  it("count open as the sidebar's badge does and done as the Done section holds", async () => {
    await render("status");
    // the sidebar's badge: lib/todo-triage's open count, without quiet tasks (views/SidebarParts.tsx)
    expect(triageTodos(todos, undefined, Date.now(), new Map(), { quiet: false }).open).toBe(2);
    expect(summary()).toBe(expected);
    expect(host.querySelector("[data-testid=todo-section][data-section=done]")?.getAttribute("data-todos")).toBe("2");
  });

  it("stay the same by folder, whose Done fold holds the Done section and whose folders keep a failed start open", async () => {
    await render("folder");
    expect(summary()).toBe(expected);
    expect(host.textContent).toContain(t("todos.done_section", { count: 2 }));
    const failed = host.querySelector<HTMLElement>("[data-todo=failed]")!;
    expect(failed.closest("[data-testid=todo-group]")).toBeTruthy();
    expect(failed.querySelector(".line-through")).toBeNull();
    expect(failed.querySelector("button")?.getAttribute("aria-label")).toBe(t("todos.complete_todo"));
    // closed an hour ago, it is still worth a look in its folder; days ago it is in the fold
    expect(host.querySelector("[data-todo=fresh]")?.closest("[data-testid=todo-group]")).toBeTruthy();
    expect(host.querySelector("[data-todo=old1]")).toBeNull();
  });

  it("wait for the list: none while it loads, then the counts", async () => {
    seed({ todos: [], todosLoaded: false, todosError: null, sessions: {}, interactions: [], answered: {} });
    await act(async () => root.render(<TodosView />));
    await flush();
    expect(host.querySelector("[data-testid=todos-loading]")).toBeTruthy();
    expect(host.querySelector("header")?.textContent).not.toContain(t("todos.summary_triage", { open: 0, parked: 0, shelved: 0, done: 0 }));
    expect(summary()).toBeUndefined();
    api.mockImplementation(async (path) => (path === "/todos" ? { todos } : {}));
    await act(async () => { await refreshTodos(); });
    await flush();
    expect(summary()).toBe(expected);
  });
});
