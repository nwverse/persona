// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { initI18n } from "../i18n";
import { ApiError, setPairedToken, setToken } from "../lib/api";
import { chooseManager, MANAGER_HEADER, resetManagerChoice, setRosterDefault, settleRosterDefault } from "../lib/manager-choice";
import type { SavedTodoView, SavedTodoViewsDocument } from "../lib/saved-todo-views";
import { getState } from "../lib/store";
import type { Todo } from "../lib/todos";

vi.hoisted(() => {
  window.matchMedia = ((query: string) => ({
    matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {},
    addListener() {}, removeListener() {}, dispatchEvent: () => false,
  })) as typeof window.matchMedia;
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
});
const transport = vi.hoisted(() => ({
  api: vi.fn(),
  events: new Set<(event: Record<string, unknown>) => void>(),
}));
vi.mock("../lib/api", async (original) => ({
  ...(await original<typeof import("../lib/api")>()),
  api: transport.api,
}));

vi.mock("../lib/store", async (original) => {
  const store = await original<typeof import("../lib/store")>();
  return {
    ...store,
    onHubEvent: (type: string, listener: (event: Record<string, unknown>) => void) => {
      if (type !== "saved_todo_views") return store.onHubEvent(type, listener);
      transport.events.add(listener);
      return () => { transport.events.delete(listener); };
    },
  };
});

const { TodosView } = await import("./Todos");
await import("./todos/SavedTodoViewsDialog");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const path = "/assistant/saved-todo-views";
const instant = "2026-11-21T09:00:00Z";
const work = { id: "view_" + "a".repeat(24), name: "Domain work",
  criteria: { text: "domain", due_window: "today", section_sort: "priority" }, created_at: instant, updated_at: instant,
} as const;
const saved = (revision: string, views: SavedTodoView[] = [work]): SavedTodoViewsDocument => ({ schema_version: 1, revision, can_write: true, views });
const originalState = { ...getState() };
const makeTodo = (id: string, title: string, due?: string): Todo => ({
  id, title, due_date: due, created_at: instant, updated_at: instant, done_at: null, agents: [],
});
let host: HTMLDivElement;
let root: Root;
let server = saved("1");
let write: (opts: { json: unknown }) => unknown | Promise<unknown>;
let fetchViews: () => unknown | Promise<unknown>;

const openListMenu = () => act(async () => host.querySelector<HTMLElement>('[data-testid="todos-filter"] button')!.click());
const menu = (label: string) => [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].find((el) => el.textContent?.trim() === label);
const dialog = () => document.querySelector<HTMLElement>('[role="dialog"]')!;
const clickMenu = async (label: string) => {
  await openListMenu();
  await act(async () => menu(label)!.click());
  await vi.waitFor(() => expect(dialog()).not.toBeNull());
};
const button = (name: string) => [...dialog().querySelectorAll<HTMLButtonElement>("button")].find((el) =>
  (el.getAttribute("aria-label") ?? el.textContent?.trim()) === name)!;
const click = (name: string) => act(async () => button(name).click());
const viewName = () => dialog().querySelector<HTMLInputElement>("input")!;
const setViewName = (value: string) => act(async () => {
  const input = viewName();
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value);
  input.dispatchEvent(new Event("input", { bubbles: true }));
});
const reads = () => transport.api.mock.calls.filter(([url, init]) => url === path && init?.method !== "POST");
const posts = () => transport.api.mock.calls.filter(([url, init]) => url === path && init?.method === "POST");
const rows = () => [...host.querySelectorAll<HTMLElement>("[data-testid=todo-triage-row]")].map((row) => row.textContent);
const render = () => act(async () => root.render(<TodosView selectedId="later" />));

beforeEach(async () => {
  await initI18n("en");
  localStorage.clear();
  resetManagerChoice();
  setToken("synthetic-todo-owner");
  settleRosterDefault();
  localStorage.setItem("persona.todos.filter", "all");
  localStorage.setItem("persona.todos.sort", "due");
  server = saved("1");
  write = () => { throw new Error("unexpected write"); };
  fetchViews = () => server;
  transport.api.mockReset();
  transport.api.mockImplementation(async (url, opts) => {
    if (url !== path) return {};
    return opts?.method === "POST" ? write(opts) : fetchViews();
  });
  const day = new Date();
  const today = `${day.getFullYear()}-${String(day.getMonth() + 1).padStart(2, "0")}-${String(day.getDate()).padStart(2, "0")}`;
  Object.assign(getState(), {
    todos: [makeTodo("domain", "Renew the domain", today), makeTodo("later", "Plan the offsite")],
    todosLoaded: true, todosError: null, todosSupported: true, sessions: {}, interactions: [], answered: {},
  });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  resetManagerChoice();
  Object.assign(getState(), originalState);
});

it("applies and clears a saved view without changing the selected detail, overview counts or stored preferences", async () => {
  await render();
  const summary = host.querySelector('[data-testid="todos-summary"]')?.textContent;
  expect(rows()).toEqual(expect.arrayContaining([expect.stringContaining("Plan the offsite"), expect.stringContaining("Renew the domain")]));
  await clickMenu("Saved views");
  await click("Apply Domain work");
  expect(rows()).toEqual([expect.stringContaining("Renew the domain")]);
  expect([...host.querySelectorAll<HTMLInputElement>('[data-testid="todos-detail-column"] input')].map((input) => input.value))
    .toContain("Plan the offsite");
  expect(host.querySelector('[data-testid="todos-summary"]')?.textContent).toBe(summary);
  expect(localStorage.getItem("persona.todos.filter")).toBe("all");
  expect(localStorage.getItem("persona.todos.sort")).toBe("due");
  expect(host.querySelector('[data-testid="todos-filter"]')?.getAttribute("data-sort")).toBe("priority");

  await openListMenu();
  await act(async () => menu("Clear saved view")!.click());
  expect(rows()).toEqual(expect.arrayContaining([expect.stringContaining("Plan the offsite")]));
  expect(host.querySelector('[data-testid="todos-filter"]')?.getAttribute("data-sort")).toBe("due");
});

it("keeps a stale save draft and reloads only after an explicit request", async () => {
  await render();
  await clickMenu("Save current view");
  await setViewName("Release planning");
  const readsBefore = reads().length;
  write = () => {
    server = saved("2");
    throw new ApiError(409, "Conflict", { code: "conflict" });
  };
  await click("Save view");
  expect(posts()).toHaveLength(1);
  expect(reads()).toHaveLength(readsBefore);
  expect(viewName().value).toBe("Release planning");
  expect(button("Save view").disabled).toBe(true);

  await click("Reload saved views");
  expect(reads()).toHaveLength(readsBefore + 1);
  expect(viewName().value).toBe("Release planning");
  write = () => saved("3");
  await click("Save view");
  expect(posts().at(-1)?.[1]).toMatchObject({ headers: { [MANAGER_HEADER]: "assistant" }, json: {
    revision: "2", op: "add", name: "Release planning",
    criteria: { text: "", due_window: "all", section_sort: "due" },
  } });
});

it.each([
  { field: "name", concurrent: { ...work, criteria: { text: "incident", due_window: "week", section_sort: "default" } },
    expected: { name: "Release queue", criteria: { text: "incident", due_window: "week", section_sort: "default" } } },
  { field: "filters", concurrent: { ...work, name: "Operations" },
    expected: { name: "Operations", criteria: { text: "", due_window: "all", section_sort: "due" } } },
] as const)("keeps a $field-only edit and adopts the other device's change", async ({ field, concurrent, expected }) => {
  await render();
  await clickMenu("Saved views");
  await click("Edit Domain work");
  if (field === "name") await setViewName("Release queue");
  else await click("Use current filters");
  const before = reads().length;
  write = () => {
    server = saved("2", [concurrent]);
    throw new ApiError(409, "Conflict", { code: "conflict" });
  };

  await click("Save view");
  expect(posts()).toHaveLength(1);
  expect(reads()).toHaveLength(before);
  expect(button("Save view").disabled).toBe(true);
  await click("Reload saved views");
  expect(viewName().value).toBe(expected.name);
  expect(posts()).toHaveLength(1);

  write = () => saved("3", [{ ...work, ...expected }]);
  await click("Save view");
  expect(posts().at(-1)?.[1].json).toEqual({ revision: "2", op: "update", id: work.id, ...expected });
});

it("does not turn a removed view's draft into a new view on reload", async () => {
  await render();
  await clickMenu("Saved views");
  await click("Edit Domain work");
  await setViewName("Draft retained");
  write = () => {
    server = saved("2", []);
    throw new ApiError(409, "Conflict", { code: "conflict" });
  };

  await click("Save view");
  await click("Reload saved views");
  expect(viewName().value).toBe("Draft retained");
  expect(button("Save view").disabled).toBe(true);
  expect(dialog().textContent).toContain("This view was removed");
  await click("Save view");
  expect(posts()).toHaveLength(1);
});

it("keeps an explicit assistant's draft and temporary view when the roster default changes", async () => {
  await act(async () => chooseManager("team"));
  await render();
  await clickMenu("Saved views");
  await click("Apply Domain work");
  await clickMenu("Save current view");
  await setViewName("Team view draft");
  const before = reads().length;

  await act(async () => setRosterDefault("new-default"));
  expect(reads()).toHaveLength(before);
  expect(viewName().value).toBe("Team view draft");
  expect(host.querySelector('[data-testid="todos-filter"]')?.getAttribute("data-sort")).toBe("priority");
});

it.each(["manager", "credential"])("clears a temporary view when the %s changes", async (kind) => {
  await render();
  await clickMenu("Saved views");
  await click("Apply Domain work");
  expect(rows()).toEqual([expect.stringContaining("Renew the domain")]);

  await act(async () => kind === "manager" ? chooseManager("team") : setPairedToken("synthetic-todo-owner"));
  if (kind === "manager") expect(reads().at(-1)?.[1].headers).toEqual({ [MANAGER_HEADER]: "team" });
  expect(host.querySelector('[data-testid="todos-filter"]')?.getAttribute("data-sort")).toBe("due");
  expect(host.querySelector<HTMLInputElement>('input[type="search"]')?.value ?? "").toBe("");
});

it("reconciles a missed announcement by reading again when opening the library", async () => {
  server = saved("1", []);
  await render();
  expect(reads()).toHaveLength(1);
  server = saved("2");
  await clickMenu("Saved views");
  expect(reads()).toHaveLength(2);
  expect(button("Apply Domain work")).toBeDefined();
});

it("updates an open library only for an event from its captured manager", async () => {
  server = saved("1", []);
  await render();
  await clickMenu("Saved views");
  const before = reads().length;
  server = saved("2");

  await act(async () => transport.events.forEach((notify) => {
    notify({ type: "saved_todo_views", manager: "other", revision: "2" });
  }));
  expect(reads()).toHaveLength(before);
  expect(button("Apply Domain work")).toBeUndefined();

  await act(async () => transport.events.forEach((notify) => notify({ type: "saved_todo_views", revision: "2" })));
  expect(reads()).toHaveLength(before + 1);
  expect(reads().at(-1)?.[1].headers).toEqual({ [MANAGER_HEADER]: "assistant" });
  expect(button("Apply Domain work")).toBeDefined();
});

it("keeps the ordinary filter menu on an older manager", async () => {
  fetchViews = () => { throw new ApiError(404, "Unknown route", {}); };
  await render();
  await openListMenu();
  expect(menu("Saved views")).toBeUndefined();
  expect(menu("Save current view")).toBeUndefined();
  expect(document.querySelector('[role="menuitemradio"]')).not.toBeNull();
});
