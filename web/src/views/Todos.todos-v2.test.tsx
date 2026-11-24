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
  sharedBlockers = [],
  todosV2,
  assistantManager,
  grouping = "status",
  selectedId,
}: {
  todos?: Todo[];
  sharedBlockers?: unknown[];
  todosV2?: boolean;
  assistantManager?: string;
  grouping?: "status" | "folder";
  selectedId?: string;
} = {}) => {
  localStorage.setItem("persona.todos.grouping", grouping);
  seed({
    todos,
    sharedBlockers,
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

describe("TodosView goal folding and blocker surfacing under todos_v2 (#14661)", () => {
  const goalTodos: Todo[] = [
    // Top-level personal goal
    {
      id: "goal_ship",
      title: "Launch Q4 release",
      audience: "human",
      created_at: iso(8 * HOUR),
      updated_at: iso(2 * HOUR),
      view: view("working", "agent_running", "working", 0),
    },
    // Non-urgent child task: working
    {
      id: "child_work",
      title: "Build release artifacts",
      part_of: "goal_ship",
      audience: "autonomous",
      agents: [{ machine_id: "m1", session_id: "s-work", linked_at: iso(2 * HOUR) }],
      created_at: iso(6 * HOUR),
      updated_at: iso(1 * HOUR),
      view: view("working", "agent_running", "working", 1),
    },
    // Non-urgent child task: completed
    {
      id: "child_done",
      title: "Freeze release branch",
      part_of: "goal_ship",
      audience: "autonomous",
      created_at: iso(7 * HOUR),
      updated_at: iso(3 * HOUR),
      done_at: iso(3 * HOUR),
      view: view("done", "closed", "done", 0),
    },
    // Urgent child task: needs_you (blocker)
    {
      id: "child_blocker",
      title: "Approve production cutover",
      part_of: "goal_ship",
      audience: "autonomous",
      supervision: { supervisor: "main", state: "needs_user" },
      ask: { verdict: "Ready to cut over?", asked_at: iso(1 * HOUR), outcome: "decide", recommend: "Proceed" },
      created_at: iso(5 * HOUR),
      updated_at: iso(1 * HOUR),
      view: view("needs_you", "owner_decides", "needs_you", 0),
    },
    // Standalone top-level personal to-do
    {
      id: "solo_todo",
      title: "Send release announcement",
      audience: "human",
      created_at: iso(4 * HOUR),
      updated_at: iso(2 * HOUR),
      view: view("todo", "rest", "todo", 0),
    },
  ];

  it("keeps legacy flat DOM and counts pixel-identical when todos_v2 is false or omitted", async () => {
    await render({ todos: goalTodos, todosV2: undefined, selectedId: "goal_ship" });
    expect(host.querySelector("[data-testid=todo-goal-toggle]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-parent-goal]")).toBeNull();
    const legacyHtml = host.querySelector("[data-testid=todos-list-column]")?.innerHTML;
    const legacyIds = visibleTodoIds();
    expect(legacyIds).toEqual(["child_blocker", "goal_ship", "child_work", "solo_todo", "child_done"]);

    await act(async () => root.unmount());
    root = createRoot(host);
    await render({ todos: goalTodos, todosV2: false, selectedId: "goal_ship" });
    expect(host.querySelector("[data-testid=todo-goal-toggle]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-parent-goal]")).toBeNull();
    expect(host.querySelector("[data-testid=todos-list-column]")?.innerHTML).toBe(legacyHtml);
  });

  it("folds non-urgent children, shows done/total progress & inline expand without opening detail, and surfaces needs_you blocker at top", async () => {
    await render({ todos: goalTodos, todosV2: true, selectedId: "solo_todo" });

    // Non-urgent children (child_work, child_done) are folded; blocker surfaces in waiting (`needs_you`)
    expect(visibleTodoIds()).toEqual(["child_blocker", "goal_ship", "solo_todo"]);

    // Waiting section holds the surfaced blocker with parent goal hint
    const waitingSec = host.querySelector<HTMLElement>('[data-testid="todo-section"][data-section="waiting"]');
    expect(waitingSec?.querySelector('[data-todo="child_blocker"]')).toBeTruthy();
    expect(waitingSec?.querySelector('[data-todo="child_blocker"] [data-testid="todo-parent-goal"]')?.textContent).toContain("Launch Q4 release");

    // Parent goal row shows compact 1/3 progress, blocked indicator, and accessible toggle
    const parentRow = host.querySelector<HTMLElement>('[data-todo="goal_ship"]')!;
    const toggle = parentRow.querySelector<HTMLButtonElement>('[data-testid="todo-goal-toggle"]')!;
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(toggle.className).toContain("touch-hit");
    expect(parentRow.querySelector('[data-testid="todo-goal-progress"]')?.textContent).toBe("1/3");
    expect(parentRow.querySelector('[data-testid="todo-goal-blocked"]')?.textContent).toBe(t("todos.goal_blocked", { count: 1 }));
    expect(host.querySelector('[data-testid="todo-goal-children"]')).toBeNull();

    // Clicking toggle expands inline children WITHOUT changing the selected detail (solo_todo stays selected)
    await act(async () => toggle.click());
    await flush();
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(host.querySelector('[data-todo="solo_todo"]')?.getAttribute("aria-current")).toBe("true");
    expect(parentRow.getAttribute("aria-current")).toBeNull();

    const childrenBox = host.querySelector<HTMLElement>('[data-testid="todo-goal-children"][data-parent="goal_ship"]')!;
    expect(childrenBox).toBeTruthy();
    const childIds = [...childrenBox.querySelectorAll<HTMLElement>("[data-todo]")].map((el) => el.dataset.todo);
    expect(childIds).toEqual(["child_blocker", "child_work", "child_done"]);

    // Clicking toggle again collapses children
    await act(async () => toggle.click());
    await flush();
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(host.querySelector('[data-testid="todo-goal-children"]')).toBeNull();
  });

  it("interoperates with All / To-dos / Tasks filter and reverts immediately when todos_v2 is switched off", async () => {
    await render({ todos: goalTodos, todosV2: true });
    expect(visibleTodoIds()).toEqual(["child_blocker", "goal_ship", "solo_todo"]);

    // Under Tasks filter, personal parent goal_ship is excluded -> its task children stay visible as top-level rows
    await clickKind(t("todos.kind_filter_tasks"));
    expect(visibleTodoIds()).toEqual(["child_blocker", "child_work", "child_done"]);
    expect(host.querySelector('[data-testid="todo-parent-goal"]')).toBeNull();
    expect(host.querySelector('[data-testid="todo-goal-toggle"]')).toBeNull();

    // Under To-dos filter, task children are excluded -> personal parent renders without a fold toggle
    await clickKind(t("todos.kind_filter_todos"));
    expect(visibleTodoIds()).toEqual(["goal_ship", "solo_todo"]);
    expect(host.querySelector('[data-testid="todo-goal-toggle"]')).toBeNull();

    // Back to All -> goal folding resumes
    await clickKind(t("todos.kind_filter_all"));
    expect(visibleTodoIds()).toEqual(["child_blocker", "goal_ship", "solo_todo"]);

    // Switching todos_v2 off reverts immediately to flat list with zero migration
    await act(async () => patchAssistant({ todos_v2: false }));
    await flush();
    expect(host.querySelector('[data-testid="todo-goal-toggle"]')).toBeNull();
    expect(host.querySelector('[data-testid="todo-parent-goal"]')).toBeNull();
    expect(visibleTodoIds()).toEqual(["child_blocker", "goal_ship", "child_work", "solo_todo", "child_done"]);
  });

  it("gates the detail view Target Repo / Reference Repo matrix on todos_v2 and reverts immediately when switched off", async () => {
    const repoSet = {
      schema_version: 1,
      manager: "assistant",
      todo_id: "task_agent",
      revision: 1,
      repositories: [
        {
          repo_id: "docs",
          requested_ref: { kind: "branch", value: "docs/v2" },
          source_snapshot: {
            identity: { provider: "github", instance_url: "https://github.com", path: "example/docs", canonical_url: "https://github.com/example/docs" },
            clone_url: "https://github.com/example/docs",
            transport_origin: "canonical_default",
            catalog_revision: 1,
          },
          links: [{ kind: "issue", number: "5", canonical_url: "https://github.com/example/docs/issues/5", role: "reference" }],
        },
        {
          repo_id: "web",
          requested_ref: { kind: "default" },
          source_snapshot: {
            identity: { provider: "github", instance_url: "https://github.com", path: "example/web", canonical_url: "https://github.com/example/web" },
            clone_url: "https://github.com/example/web",
            transport_origin: "canonical_default",
            catalog_revision: 1,
          },
          links: [{ kind: "pull_request", number: "12", canonical_url: "https://github.com/example/web/pull/12", role: "delivery" }],
        },
      ],
    };
    api.mockImplementation(async (path: string) => {
      if (path.includes("/kb")) return { items: kbProjects };
      if (path === "/todos/task_agent/repositories") return repoSet;
      return {};
    });

    await render({ todos: sampleTodos, todosV2: true, selectedId: "task_agent" });
    const matrix = host.querySelector("[data-repo-matrix]");
    expect(matrix).toBeTruthy();
    const rows = [...matrix!.querySelectorAll<HTMLElement>("[data-repository-row]")];
    expect(rows.map((r) => r.dataset.repoRole)).toEqual(["target", "reference"]);
    expect(matrix!.querySelector('[data-repo-role-badge="target"]')?.textContent).toBe(t("task-repositories.role_target"));
    expect(matrix!.querySelector('[data-repo-role-badge="reference"]')?.textContent).toBe(t("task-repositories.role_reference"));

    await act(async () => patchAssistant({ todos_v2: false }));
    await flush();
    expect(host.querySelector("[data-repo-matrix]")).toBeNull();
    expect(host.querySelector("[data-repo-role-badge]")).toBeNull();
  });
});

describe("TodosView shared external blockers strip under todos_v2 (#14728)", () => {
  const sampleBlockers = [
    {
      key: "pull_request:https://github.com/example/core/pull/42",
      kind: "pull_request",
      label: "example/core#42",
      url: "https://github.com/example/core/pull/42",
      todo_ids: ["task_agent", "task_supervised"],
      count: 2,
    },
    {
      key: "target:staging deploy",
      kind: "external_target",
      label: "staging deploy",
      todo_ids: ["personal_proj", "task_agent"],
      count: 2,
    },
  ];

  const stripEl = () => host.querySelector<HTMLElement>("[data-testid=todos-shared-blockers]");
  const chips = () => [...host.querySelectorAll<HTMLButtonElement>("[data-testid=shared-blocker-chip]")];

  it("renders no shared blockers DOM when todos_v2 is false or omitted, even if shared_blockers exists", async () => {
    await render({ todosV2: undefined, sharedBlockers: sampleBlockers });
    expect(stripEl()).toBeNull();
    const legacyHtml = host.querySelector("[data-testid=todos-list-column]")?.innerHTML;

    await act(async () => root.unmount());
    root = createRoot(host);
    await render({ todosV2: false, sharedBlockers: sampleBlockers });
    expect(stripEl()).toBeNull();
    expect(host.querySelector("[data-testid=todos-list-column]")?.innerHTML).toBe(legacyHtml);
  });

  it("renders nothing under todos_v2=true when shared_blockers is empty or absent", async () => {
    await render({ todosV2: true, sharedBlockers: [] });
    expect(stripEl()).toBeNull();
  });

  it("renders blocker labels, counts, and safe external links, and filters tasks on click with toggle and clear", async () => {
    await render({ todosV2: true, sharedBlockers: sampleBlockers });
    expect(stripEl()).toBeTruthy();

    const list = chips();
    expect(list).toHaveLength(2);
    expect(list[0].textContent).toContain("example/core#42");
    expect(list[0].textContent).toContain(t("todos.shared_blockers_count", { count: 2 }));
    expect(list[0].getAttribute("aria-pressed")).toBe("false");

    // External link renders only for valid http(s) URL, never nested inside the filter button
    const links = [...host.querySelectorAll<HTMLAnchorElement>("[data-testid=shared-blocker-link]")];
    expect(links).toHaveLength(1);
    expect(links[0].getAttribute("href")).toBe("https://github.com/example/core/pull/42");
    expect(links[0].getAttribute("target")).toBe("_blank");
    expect(links[0].getAttribute("rel")).toBe("noopener noreferrer");
    expect(links[0].closest("button")).toBeNull();
    expect(host.querySelector("[data-testid=shared-blockers-clear]")).toBeNull();

    // Click first blocker -> filters list to its todo_ids
    await act(async () => list[0].click());
    await flush();
    expect(list[0].getAttribute("aria-pressed")).toBe("true");
    expect(visibleTodoIds()).toEqual(["task_supervised", "task_agent"]);
    expect(host.querySelector("[data-testid=shared-blockers-clear]")).toBeTruthy();

    // Click same blocker again -> toggles off and restores all rows
    await act(async () => list[0].click());
    await flush();
    expect(list[0].getAttribute("aria-pressed")).toBe("false");
    expect(visibleTodoIds()).toEqual(["task_supervised", "task_agent", "personal_plain", "personal_proj", "task_done"]);

    // Click second blocker, then click Clear filter button -> restores all rows
    await act(async () => chips()[1].click());
    await flush();
    expect(visibleTodoIds()).toEqual(["task_agent", "personal_proj"]);
    await act(async () => host.querySelector<HTMLButtonElement>("[data-testid=shared-blockers-clear]")!.click());
    await flush();
    expect(visibleTodoIds()).toEqual(["task_supervised", "task_agent", "personal_plain", "personal_proj", "task_done"]);
  });

  it("auto-clears selection when the selected blocker disappears after refresh, and reverts when todos_v2 is switched off", async () => {
    await render({ todosV2: true, sharedBlockers: sampleBlockers });
    await act(async () => chips()[0].click());
    await flush();
    expect(visibleTodoIds()).toEqual(["task_supervised", "task_agent"]);

    // Refresh drops the selected blocker -> automatically exits filter instead of deadlocking empty
    await act(async () => {
      seed({ sharedBlockers: [sampleBlockers[1]] });
      root.render(<TodosView />);
    });
    await flush();
    expect(visibleTodoIds()).toEqual(["task_supervised", "task_agent", "personal_plain", "personal_proj", "task_done"]);
    expect(host.querySelector("[data-testid=shared-blockers-clear]")).toBeNull();

    // Re-select remaining blocker, then turn todos_v2 off -> immediate rollback
    await act(async () => chips()[0].click());
    await flush();
    expect(visibleTodoIds()).toEqual(["task_agent", "personal_proj"]);

    await act(async () => patchAssistant({ todos_v2: false }));
    await flush();
    expect(stripEl()).toBeNull();
    expect(visibleTodoIds()).toEqual(["task_supervised", "task_agent", "personal_plain", "personal_proj", "task_done"]);
  });
});
