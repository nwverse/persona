// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { beforeEach, beforeAll, afterEach, expect, it, vi } from "vitest";
import { TaskPromptEditor } from "./TaskPromptEditor";
import { blankTask, type Task } from "../../lib/routines";
import { loadPages } from "../../lib/lazy-page";
import { splitSkillDraft } from "../../lib/skill-mentions";
import { t } from "../../i18n";
import { storeDraft } from "../../lib/drafts";
import { HISTORY_PREFIX, recordSent } from "../../lib/send-history";
import { reloadSavedPrompts, resetSavedPrompts } from "../../lib/saved-prompts";

const api = vi.hoisted(() => vi.fn());
vi.mock("../../lib/api", async (original) => ({ ...(await original<typeof import("../../lib/api")>()), api }));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root, host: HTMLDivElement;
const execution = { machine_id: "current", cwd: "/current", launcher: "claude", agent: "claude" };
const catalog = { catalog_version: 1, complete: true, skills: [{ id: "opaque", name: "Index receipts", source: "project" }] };
const draft: Task = { ...blankTask(), prompt: "", cwd: "/current", execution_machine_id: "current" };
const contextPath = "/assistant/tasks/input-context";
const field = () => host.querySelector("textarea")!;
const option = (text: string) => [...host.querySelectorAll('[role="option"]')].find((row) => row.textContent?.includes(text));
// Flush the request debounce and queued React work before reading the next rendered state.
async function settle() {
  await act(async () => { await vi.advanceTimersByTimeAsync(200); });
}
const render = (task: Task) => act(() => root.render(<TaskPromptEditor draft={task} saved={null} disabled={false} onChange={() => {}} />));
function type(text: string) {
  act(() => {
    const el = field();
    el.focus();
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(el, text);
    el.setSelectionRange(text.length, text.length);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

beforeAll(async () => { await loadPages(); await loadPages(); });
beforeEach(() => {
  vi.useFakeTimers();
  Element.prototype.scrollIntoView ??= () => {};
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  api.mockReset();
});
afterEach(() => { act(() => root.unmount()); host.remove(); vi.useRealTimers(); });

it("ignores an abandoned execution context and requests only the new task directory", async () => {
  let resolveOld!: (value: unknown) => void;
  api.mockImplementation(async (path: string, options) => {
    if (path === contextPath && options.json.cwd === "/old") return new Promise((resolve) => { resolveOld = resolve; });
    if (path === contextPath) return { execution, catalog, task_input: true };
    if (path.includes("/fs?")) return { path: "/current", entries: [{ name: "current.txt", dir: false }], truncated: false };
    return {};
  });
  render({ ...draft, cwd: "/old", execution_machine_id: "old" });
  await settle();
  expect(resolveOld).toBeTypeOf("function");
  render(draft);
  const plus = () => host.querySelector<HTMLButtonElement>("[data-composer-plus]")!;
  await settle();
  expect(plus().disabled).toBe(false);
  await act(async () => resolveOld({ execution: { ...execution, machine_id: "old", cwd: "/old" }, task_input: true }));
  act(() => plus().click());
  await settle();
  expect(option("current.txt")).toBeDefined();
  const files = api.mock.calls.map(([path]) => path as string).filter((path) => path.includes("/fs?"));
  expect(files).toEqual(["/machines/current/fs?path=%2Fcurrent&cwd=%2Fcurrent&task_input=1"]);
});

it("selects a task-scoped slash skill with the keyboard and preserves prose and identity while editing", async () => {
  api.mockImplementation(async (path: string, options) => {
    if (path === contextPath) return { execution, task_input: true, catalog: options.json.catalog ? catalog : undefined };
    return {};
  });
  let raw = "";
  function Form() {
    const [task, setTask] = useState<Task>({ ...draft, repeat: "daily", fresh_each_run: true });
    raw = task.prompt;
    return <TaskPromptEditor draft={task} saved={null} disabled={false}
      onChange={(prompt, prompt_selections) => setTask({ ...task, prompt, prompt_selections })} />;
  }
  act(() => root.render(<Form />));
  await settle();
  expect(host.querySelector<HTMLButtonElement>("[data-composer-plus]")?.disabled).toBe(false);
  type("/Index");
  await settle();
  expect(option("Index receipts")).toBeDefined();
  act(() => { field().dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })); });
  expect(field().value).toBe("");
  expect(splitSkillDraft(raw, true).skills[0].attrs?.ref).toBe("opaque");
  type("  Review tomorrow\n");
  expect(field().value).toBe("  Review tomorrow\n");
  expect(splitSkillDraft(raw, true).text).toBe(field().value);
  const preview = api.mock.calls.filter(([path]) => path === contextPath);
  expect(preview.map(([, options]) => options.json.catalog ?? false)).toEqual([false, true]);
  expect(preview[1][1].json).toMatchObject({ cwd: "/current", repeat: "daily", fresh_each_run: true });
  expect(preview[1][1].json).not.toHaveProperty("id");
});

it("keeps task typing and saved prompt selection out of chat drafts and send history", async () => {
  const chat = "chat-fixture";
  const reusable = { id: "saved-review", name: "fixture-review", text: "Review the synthetic notes." };
  const existing = { ...draft, id: "job", prompt: "Original task prompt" };
  const chatStorage = () => Object.fromEntries(Object.keys(localStorage)
    .filter((key) => key.startsWith("draft:") || key.startsWith(HISTORY_PREFIX))
    .map((key) => [key, localStorage.getItem(key)]));
  localStorage.clear();
  storeDraft(chat, "Unsent chat draft");
  recordSent(chat, "Previously sent chat message");
  const before = chatStorage();
  expect(before).toHaveProperty("draft:chat-fixture", "Unsent chat draft");
  expect(before[HISTORY_PREFIX + chat]).toContain("Previously sent chat message");
  api.mockImplementation(async (path) => {
    if (path === contextPath) return { execution, task_input: true };
    if (path === "/assistant/saved-prompts") return { prompts: [reusable] };
    throw new Error("Unexpected request");
  });
  function Form() {
    const [task, setTask] = useState(existing);
    return <TaskPromptEditor draft={task} saved={existing} disabled={false}
      onChange={(prompt, prompt_selections) => setTask({ ...task, prompt, prompt_selections })} />;
  }
  const open = () => act(() => root.render(<Form />));
  const flush = () => act(async () => { await vi.advanceTimersByTimeAsync(400); });
  try {
    await act(async () => { await reloadSavedPrompts(); });
    open();
    type("Typed only in the task");
    await flush();
    expect(field().value).toBe("Typed only in the task");
    expect(chatStorage()).toEqual(before);

    type(`/${reusable.name}`);
    await flush();
    expect(option(reusable.name)).toBeDefined();
    act(() => field().dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })));
    expect(field().value).toBe(reusable.text);
    act(() => window.dispatchEvent(new Event("pagehide")));
    expect(chatStorage()).toEqual(before);

    act(() => root.render(null));
    expect(chatStorage()).toEqual(before);
    open();
    await flush();
    expect(field().value).toBe(existing.prompt);
    expect(chatStorage()).toEqual(before);
  } finally {
    act(() => { root.render(null); resetSavedPrompts(); });
    localStorage.clear();
  }
});

it("retains a saved prompt when the execution computer is unavailable", async () => {
  api.mockRejectedValue(new Error("Computer unavailable"));
  const literal = '<persona-context kind="skill" ref="example">Example</persona-context>\n\nKeep my note';
  const task = { ...draft, id: "existing", prompt: literal };
  const changed = vi.fn();
  act(() => root.render(<TaskPromptEditor draft={task} saved={task} disabled={false} onChange={changed} />));
  await settle();
  expect(field().value).toBe(literal);
  expect(host.querySelector("[role=alert]")?.textContent).toContain("Computer unavailable");
  expect(host.querySelector<HTMLButtonElement>("[data-composer-plus]")!.disabled).toBe(true);
  expect(changed).not.toHaveBeenCalled();
});

it.each([false, undefined])("keeps text and quick prompts usable when the selected computer reports task input %s", async (taskInput) => {
  const inputError = "Update this computer to select files and skills.";
  api.mockImplementation(async (path) => path === contextPath ? { execution, task_input: taskInput, input_error: inputError } : {});
  function Form() {
    const [task, setTask] = useState(draft);
    return <TaskPromptEditor draft={task} saved={null} disabled={false}
      onChange={(prompt, prompt_selections) => setTask({ ...task, prompt, prompt_selections })} />;
  }
  act(() => root.render(<Form />));
  await settle();
  expect(host.querySelector("[role=alert]")?.textContent).toBe(inputError);
  expect(host.querySelector<HTMLButtonElement>("[data-composer-plus]")!.disabled).toBe(true);
  type("plain @");
  await settle();
  expect(field().value).toBe("plain @");
  expect(host.querySelector("[role=listbox]")).toBeNull();
  const command = t("chat-composer.cmd_report_word");
  type(command);
  await settle();
  expect(option(command.slice(1))).toBeDefined();
  act(() => field().dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })));
  expect(field().value).toBe(t("chat-composer.cmd_report_text"));
  expect(api.mock.calls.filter(([path]) => path === contextPath)).toHaveLength(1);
  expect(api.mock.calls.some(([path]) => String(path).includes("/fs?"))).toBe(false);
});

it("retires the picker if the computer loses support while opening its catalog", async () => {
  const inputError = "This computer no longer supports task selections.";
  api.mockImplementation(async (path, options) => {
    if (path !== contextPath) return {};
    return options.json.catalog ? { execution, task_input: false, input_error: inputError } : { execution, task_input: true };
  });
  render({ ...draft, prompt: "/Index" });
  await settle();
  expect(host.querySelector("[role=alert]")?.textContent).toBe(inputError);
  expect(host.querySelector<HTMLButtonElement>("[data-composer-plus]")!.disabled).toBe(true);
  expect(field().value).toBe("/Index");
});
