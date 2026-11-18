// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DEV_MODE_KEY } from "../../../lib/dev-probe";
import { resetRestoredCache, restoreTodo } from "../../../lib/todo-restore";
import * as todoTriage from "../../../lib/todo-triage";
import type { TodoSummary } from "../../../lib/todo-view-wire";
import type { Todo } from "../../../lib/todos";
import { localRestores, QUIET_MS, RECHECK_MS, SUMMARY_MS, startViewShadow } from "./view-shadow";

const START = Date.parse("2026-11-10T12:00:00Z");
const ago = (hours: number) => new Date(START - hours * 3_600_000).toISOString();
const view = (section: string, reason: string, more: Partial<TodoSummary> = {}): TodoSummary => ({ phase: "todo", section, reason, rank: 0, ...more });
const todo = (id: string, more: Partial<Todo> = {}): Todo => ({ id, title: "Call the dentist about Tuesday", created_at: ago(2), updated_at: ago(1), ...more });

// today's triage files these at rest (Other open, or Shelved for a task idle for 60 hours); their views say:
const same = todo("todo_same", { view: view("todo", "rest") });
const decided = todo("todo_someday", { origin: "conv_main", created_at: ago(60), updated_at: ago(60), view: view("someday", "deferred", { phase: "someday" }) });
const unexplained = todo("todo_odd", { view: view("working", "agent_running", { phase: "working" }) });

type State = { todos: Todo[]; features: string[]; sessions: Record<string, never>; sessionSummaries: never[]; taskRows: Record<string, never> };

let state: State;
let listeners: Set<() => void>;
let note: ReturnType<typeof vi.fn<(kind: string, fields: Record<string, unknown>) => void>>;
let stop: (() => void) | undefined;

const set = (patch: Partial<State>) => {
  state = { ...state, ...patch };
  for (const l of listeners) l();
};
const start = () => {
  stop = startViewShadow({
    note: (kind, fields) => note(kind, fields),
    state: () => state,
    subscribe: (l) => (listeners.add(l), () => listeners.delete(l)),
    restores: () => new Map(),
    idle: () => Promise.resolve(),
    triage: async () => todoTriage,
  });
};
const lines = (kind: string) => note.mock.calls.filter(([k]) => k === kind).map(([, fields]) => fields as Record<string, unknown>);

beforeEach(() => {
  vi.useFakeTimers({ now: START });
  localStorage.setItem(DEV_MODE_KEY, ago(1));
  state = { todos: [], features: ["todo_view"], sessions: {}, sessionSummaries: [], taskRows: {} };
  listeners = new Set();
  note = vi.fn();
});
afterEach(() => {
  stop?.();
  stop = undefined;
  localStorage.clear();
  resetRestoredCache();
  vi.useRealTimers();
});

describe("the gates", () => {
  it("writes nothing while the Developing Persona switch is off", async () => {
    localStorage.removeItem(DEV_MODE_KEY);
    state.todos = [unexplained];
    start();
    await vi.advanceTimersByTimeAsync(3 * SUMMARY_MS);
    expect(note).not.toHaveBeenCalled();
  });

  it("writes nothing while the hub does not list todo_view, and starts once it does", async () => {
    state = { ...state, features: ["todo_seen"], todos: [same] };
    start();
    await vi.advanceTimersByTimeAsync(3 * SUMMARY_MS);
    expect(note).not.toHaveBeenCalled();
    set({ features: ["todo_seen", "todo_view"] });
    await vi.advanceTimersByTimeAsync(QUIET_MS);
    expect(lines("todo_view")).toHaveLength(1);
  });

  it("stops with the recorder", async () => {
    state.todos = [same];
    start();
    stop?.();
    await vi.advanceTimersByTimeAsync(3 * SUMMARY_MS);
    expect(note).not.toHaveBeenCalled();
  });
});

describe("comparing", () => {
  it("counts each row with a view, and notes a mismatch once a later run still sees it, by ids and enum words only", async () => {
    state.todos = [same, decided, unexplained, todo("todo_plain")];
    start();
    await vi.advanceTimersByTimeAsync(QUIET_MS);
    expect(lines("todo_view")).toEqual([{ compared: 3, same: 1, by_design: { someday: 1 }, unknown: 0, client_gap: 0, boundary: 0, unsettled: 1, mismatch: 0 }]);
    expect(lines("todo_view_mismatch")).toEqual([]);

    await vi.advanceTimersByTimeAsync(RECHECK_MS);
    const mismatch = { todo: "todo_odd", local_section: "other", local_rule: "rest", phase: "working", section: "working", reason: "agent_running" };
    expect(lines("todo_view_mismatch")).toEqual([mismatch]);
    expect(JSON.stringify(note.mock.calls)).not.toContain("dentist");

    // the summary waits out its minute; the same mismatch is not noted again within the hour
    await vi.advanceTimersByTimeAsync(SUMMARY_MS);
    expect(lines("todo_view").at(-1)).toMatchObject({ unsettled: 0, mismatch: 1 });
    expect(lines("todo_view")).toHaveLength(2);
    await vi.advanceTimersByTimeAsync(5 * SUMMARY_MS);
    expect(lines("todo_view_mismatch")).toHaveLength(1);
    expect(lines("todo_view")).toHaveLength(2);
  });

  it("never notes a mismatch that was gone by the next run", async () => {
    state.todos = [unexplained];
    start();
    await vi.advanceTimersByTimeAsync(QUIET_MS);
    expect(lines("todo_view").at(-1)).toMatchObject({ unsettled: 1 });
    // the hub's next list says Other open too
    set({ todos: [{ ...unexplained, view: view("todo", "rest") }] });
    await vi.advanceTimersByTimeAsync(3 * SUMMARY_MS);
    expect(lines("todo_view_mismatch")).toEqual([]);
  });

  it("reads this browser's restores as lib/todo-restore.ts writes them", () => {
    restoreTodo("todo_r1", START - 1000);
    expect(localRestores()).toEqual(new Map([["todo_r1", START - 1000]]));
  });
});
