// @vitest-environment jsdom
// Unified todos_v2 Labs toggle (#14642): lightweight All / To-dos / Tasks filter and hiding empty Uncategorized
// placeholders strictly gated behind the selected assistant's `todos_v2 === true`, with instant zero-migration rollback.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Todo } from "../lib/todos";
import type { TodoSummary } from "../lib/todo-view-wire";

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

const { getState, patchAssistant, reset } = await import("../lib/store");
const { TodosView } = await import("./Todos");
const { t } = await import("../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const NOW = Date.parse("2026-11-24T12:00:00.000Z");
const HOUR = 3_600_000;
const iso = (agoMs: number) => new Date(NOW - agoMs).toISOString();

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};
const seed = (patch: Record<string, unknown>) => Object.assign(getState() as unknown as Record<string, unknown>, patch);

const view = (section: string, reason: string, phase: string, rank = 0): TodoSummary => ({ phase, section, reason, rank });

const sampleTodos: Todo[] = [
  // Personal to-do without project
  {
    id: "personal_plain",
    title: "Buy coffee beans",
    created_at: iso(5 * HOUR),
    updated_at: iso(4 * HOUR),
    view: view("todo", "rest", "todo", 0),
  },
  // Personal to-do with explicit audience="human" and assigned project
  {
    id: "personal_proj",
    title: "Plan Q4 offsite",
    project: "ops",
    audience: "human",
    created_at: iso(4 * HOUR),
    updated_at: iso(3 * HOUR),
    view: view("todo", "rest", "todo", 1),
  },
  // Agent-backed task with assigned project
  {
    id: "task_agent",
    title: "Fix login timeout",
    project: "persona",
    agents: [{ machine_id: "m1", session_id: "s1", linked_at: iso(2 * HOUR) }],
    created_at: iso(3 * HOUR),
    updated_at: iso(2 * HOUR),
    view: view("working", "agent_running", "working", 0),
  },
  // Supervised task without project
  {
    id: "task_supervised",
    title: "Audit release checklist",
    origin: "conv-main",
    supervision: { supervisor: "main", state: "needs_user" },
    created_at: iso(2 * HOUR),
    updated_at: iso(1 * HOUR),
    view: view("needs_you", "owner_decides", "needs_you", 0),
  },
  // Explicit autonomous task, done
  {
    id: "task_done",
    title: "Run nightly benchmark",
    audience: "autonomous",
    created_at: iso(6 * HOUR),
    updated_at: iso(1 * HOUR),
    done_at: iso(1 * HOUR),
    view: view("done", "closed", "done", 0),
  },
];

const kbProjects = [
  { slug: "persona", name: "Persona Core" },
  { slug: "ops", name: "Operations" },
];

beforeEach(() => {
  reset();
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
  api.mockReset();
  api.mockImplementation(async (path: string) => {
    if (path.includes("/kb")) return { items: kbProjects };
    return {};
  });
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
  reset();
  vi.useRealTimers();
});

const render = async ({
  todos = sampleTodos,
  todosV2,
  assistantManager,
  grouping = "status",
  selectedId,
}: {
  todos?: Todo[];
  todosV2?: boolean;
  assistantManager?: string;
  grouping?: "status" | "folder";
  selectedId?: string;
} = {}) => {
  localStorage.setItem("persona.todos.grouping", grouping);
  seed({
    todos,
    todosLoaded: true,
    sessions: {},
    interactions: [],
    answered: {},
    features: ["todo_view"],
    assistant:
      todosV2 === undefined && assistantManager === undefined
        ? { machine_id: "m1", main: "main", conversations: [] }
        : {
            machine_id: "m1",
            main: "main",
            conversations: [],
            ...(assistantManager !== undefined ? { manager: assistantManager } : {}),
            ...(todosV2 !== undefined ? { todos_v2: todosV2 } : {}),
          },
  });
  await act(async () => root.render(<TodosView selectedId={selectedId} />));
  await flush();
};

const summary = () => host.querySelector("[data-testid=todos-summary]")?.textContent ?? "";
const kindFilterEl = () => host.querySelector<HTMLElement>("[data-testid=todos-kind-filter]");
const visibleTodoIds = () => [...host.querySelectorAll<HTMLElement>("[data-todo]")].map((el) => el.dataset.todo);
const clickKind = async (label: string) => {
  const btn = [...(kindFilterEl()?.querySelectorAll<HTMLButtonElement>("button[role=radio]") ?? [])].find((b) => b.textContent === label);
  expect(btn, `expected kind filter option "${label}"`).toBeTruthy();
  await act(async () => btn!.click());
  await flush();
};

describe("TodosView under todos_v2 Labs toggle (#14642)", () => {
  it("renders identically to legacy when todos_v2 is false or omitted by an older Hub", async () => {
    await render({ todosV2: undefined, selectedId: "personal_plain" });
    expect(kindFilterEl()).toBeNull();
    expect(host.querySelector("[data-testid=todo-project-tag]")).toBeNull();
    const legacyHtml = host.querySelector("[data-testid=todos-list-column]")?.innerHTML;
    const legacySummary = summary();
    const legacySelectOption = host.querySelector<HTMLOptionElement>("[data-testid=todo-plan-project] option[value='']")?.textContent;
    expect(legacySelectOption).toBe(t("todos.project_none"));

    await act(async () => root.unmount());
    root = createRoot(host);
    await render({ todosV2: false, selectedId: "personal_plain" });
    expect(kindFilterEl()).toBeNull();
    expect(host.querySelector("[data-testid=todo-project-tag]")).toBeNull();
    expect(host.querySelector("[data-testid=todos-list-column]")?.innerHTML).toBe(legacyHtml);
    expect(summary()).toBe(legacySummary);
  });

  it("shows All / To-dos / Tasks filter under todos_v2=true with consistent counts before view placement", async () => {
    await render({ todosV2: true });
    expect(kindFilterEl()).toBeTruthy();
    expect(summary()).toBe(t("todos.summary_triage", { open: 4, parked: 0, shelved: 0, done: 1, cancelled: 0 }));
    expect(visibleTodoIds()).toEqual(["task_supervised", "task_agent", "personal_plain", "personal_proj", "task_done"]);

    // Filter to personal To-dos only
    await clickKind(t("todos.kind_filter_todos"));
    expect(visibleTodoIds()).toEqual(["personal_plain", "personal_proj"]);
    expect(summary()).toBe(
      t("todos.summary_filtered", {
        filter: t("todos.kind_filter_todos"),
        open: 2,
        parked: 0,
        shelved: 0,
        done: 0,
        cancelled: 0,
      }),
    );

    // Filter to Agent / supervised Tasks only
    await clickKind(t("todos.kind_filter_tasks"));
    expect(visibleTodoIds()).toEqual(["task_supervised", "task_agent", "task_done"]);
    expect(summary()).toBe(
      t("todos.summary_filtered", {
        filter: t("todos.kind_filter_tasks"),
        open: 2,
        parked: 0,
        shelved: 0,
        done: 1,
        cancelled: 0,
      }),
    );

    // Back to All
    await clickKind(t("todos.kind_filter_all"));
    expect(summary()).toBe(t("todos.summary_triage", { open: 4, parked: 0, shelved: 0, done: 1, cancelled: 0 }));
  });

  it("shows assigned project tags while omitting empty Uncategorized placeholders under todos_v2=true", async () => {
    await render({ todosV2: true, selectedId: "personal_plain" });
    // Assigned projects render their tag
    expect(host.querySelector('[data-todo="personal_proj"] [data-testid="todo-project-tag"]')?.textContent).toBe("ops");
    expect(host.querySelector('[data-todo="task_agent"] [data-testid="todo-project-tag"]')?.textContent).toBe("persona");
    // Unassigned rows have no project tag and no Uncategorized / None placeholder
    expect(host.querySelector('[data-todo="personal_plain"] [data-testid="todo-project-tag"]')).toBeNull();
    expect(host.querySelector('[data-todo="task_supervised"] [data-testid="todo-project-tag"]')).toBeNull();
    expect(host.querySelector('[data-todo="personal_plain"]')?.textContent).not.toContain(t("todos.project_none"));
    expect(host.querySelector('[data-todo="personal_plain"]')?.textContent).not.toContain(t("lib-todo-tree.unsorted"));
    // Detail project select leaves empty option blank instead of showing "None / 不归类"
    const emptyOption = host.querySelector<HTMLOptionElement>("[data-testid=todo-plan-project] option[value='']");
    expect(emptyOption?.textContent).toBe("");
    expect(emptyOption?.getAttribute("aria-label")).toBe(t("todos.project_none"));

    // Folder grouping: named project headers remain, while Unsorted header is omitted
    await act(async () => root.unmount());
    root = createRoot(host);
    await render({ todosV2: true, grouping: "folder" });
    const groups = [...host.querySelectorAll<HTMLElement>('[data-testid="todo-group"]')];
    const unsortedGroup = groups.find((el) => el.dataset.group === "todos-project:\u0000");
    expect(unsortedGroup).toBeTruthy();
    expect(unsortedGroup?.querySelector("button[aria-expanded]")).toBeNull();
    expect(unsortedGroup?.textContent).not.toContain(t("lib-todo-tree.unsorted"));
    const namedGroup = groups.find((el) => el.dataset.group === "todos-kb:persona");
    expect(namedGroup?.querySelector("button[aria-expanded]")?.textContent).toContain("persona");
  });

  it("immediately reverts to standard view when todos_v2 is switched off", async () => {
    await render({ todosV2: true });
    await clickKind(t("todos.kind_filter_todos"));
    expect(visibleTodoIds()).toEqual(["personal_plain", "personal_proj"]);
    expect(host.querySelector('[data-todo="personal_proj"] [data-testid="todo-project-tag"]')).toBeTruthy();

    // Disable todos_v2 in store -> immediate rollback without reload or migration
    await act(async () => patchAssistant({ todos_v2: false }));
    await flush();
    expect(kindFilterEl()).toBeNull();
    expect(host.querySelector("[data-testid=todo-project-tag]")).toBeNull();
    expect(summary()).toBe(t("todos.summary_triage", { open: 4, parked: 0, shelved: 0, done: 1, cancelled: 0 }));
    expect(visibleTodoIds()).toEqual(["task_supervised", "task_agent", "personal_plain", "personal_proj", "task_done"]);
  });

  it("stays off when assistant payload belongs to a different manager", async () => {
    await render({ todosV2: true, assistantManager: "other-manager" });
    expect(kindFilterEl()).toBeNull();
    expect(host.querySelector("[data-testid=todo-project-tag]")).toBeNull();
  });
});
