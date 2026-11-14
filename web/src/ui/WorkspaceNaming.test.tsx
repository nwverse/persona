// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
vi.hoisted(() => { window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {} })) as typeof window.matchMedia; });
import { initI18n } from "../i18n";
import { credentialOwner, setToken } from "../lib/api";
import { WorkspaceNaming, WorkspaceNamingPreview } from "./WorkspaceNaming";
import type { WorkspaceNamingPreview as Preview } from "../lib/workspace-naming";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
type Call = { path: string; init: RequestInit; answer: (body: unknown, status?: number) => void };
let root: Root, host: HTMLDivElement, calls: Call[];
const defaults = { base_directory: "{home}/workspace", task_directory_template: "task-{task-id}", branch_template: "persona/{task-id}" };
const settings = { manager: "assistant", machine_id: "box", workspace_naming_version: "workspace-naming/v1", workspace_rules: {}, effective_workspace_rules: defaults };
const button = (label: string) => [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label)!;
const click = (label: string) => act(async () => { expect(button(label), label).toBeTruthy(); button(label).click(); });
const answer = (call: Call, body: unknown, status = 200) => act(async () => call.answer(body, status));
const render = (manager = "assistant", todoId?: string) => act(async () => root.render(<WorkspaceNaming manager={manager} owner={credentialOwner()} todoId={todoId} />));
const type = (value: string) => act(async () => {
  const node = document.querySelectorAll("input")[1];
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(node, value);
  node.dispatchEvent(new Event("input", { bubbles: true }));
});
beforeEach(async () => {
  await initI18n("en"); setToken("synthetic-naming-session"); calls = [];
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  vi.stubGlobal("fetch", vi.fn((url: RequestInfo | URL, init: RequestInit = {}) => new Promise<Response>((resolve) => calls.push({ path: String(url).split("/api/v1")[1], init, answer: (body, status = 200) => resolve(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })) }))));
});
afterEach(async () => { act(() => root.unmount()); host.remove(); vi.unstubAllGlobals(); setToken(""); await initI18n("zh-Hans"); });

it("loads blank defaults on demand and clears a custom override with null", async () => {
  await render(); expect(calls).toHaveLength(0); await click("Workspace naming"); await answer(calls[0], settings);
  const inputs = [...document.querySelectorAll("input")];
  expect(inputs.map((node) => node.value)).toEqual(["", "", ""]);
  expect(inputs.map((node) => node.placeholder)).toEqual(Object.values(defaults).map((value) => `System default: ${value}`));
  expect(document.querySelector("input[aria-invalid=true]")).toBeNull();
  expect(document.body.textContent).not.toMatch(/Inherited|Restore inheritance|Use inherited|Override/);
  expect(document.body.textContent).toContain("Use {home}");
  expect(document.querySelectorAll("details")).toHaveLength(1);
  await type("custom/{task-id}"); await click("Save"); const save = calls.at(-1)!;
  expect(new Headers(save.init.headers).get("X-Persona-Manager")).toBe("assistant");
  expect(JSON.parse(save.init.body as string)).toEqual({ workspace_rules: { base_directory: null, task_directory_template: "custom/{task-id}", branch_template: null } });
  await answer(save, { ...settings, workspace_rules: { task_directory_template: "custom/{task-id}" } });
  expect(button("Save").disabled).toBe(true);
  await type(""); expect(document.querySelectorAll("input")[1].value).toBe("");
  expect(button("Save").disabled).toBe(false);
  await click("Preview naming");
  expect(JSON.parse(calls.at(-1)!.init.body as string).workspace_rules.task_directory_template).toBeNull();
  await answer(calls.at(-1)!, { status: "incomplete", sample: true, diagnostics: [] });
  await click("Save");
  expect(JSON.parse(calls.at(-1)!.init.body as string)).toEqual({ workspace_rules: { base_directory: null, task_directory_template: null, branch_template: null } });
  await answer(calls.at(-1)!, settings); expect(button("Save").disabled).toBe(true);
  await act(async () => root.render(<p>Another page</p>));
  await render(); await click("Workspace naming"); await answer(calls.at(-1)!, settings);
  expect(document.querySelectorAll("input")[1].value).toBe("");
  expect(document.querySelectorAll("input")[1].placeholder).toBe(`System default: ${defaults.task_directory_template}`);
});

it("keeps failed drafts and refreshes saved values without discarding local edits", async () => {
  await render(); await click("Workspace naming"); await answer(calls[0], settings);
  await type("draft/{task-id}"); await click("Save"); await answer(calls.at(-1)!, { error: "unavailable" }, 503);
  expect(document.querySelectorAll("input")[1].value).toBe("draft/{task-id}");
  await click("Reload saved settings"); await answer(calls.at(-1)!, { ...settings, workspace_rules: { branch_template: "other/{task-id}" } });
  expect(document.querySelectorAll("input")[1].value).toBe("draft/{task-id}");
  expect(button("Save").disabled).toBe(false);
});

it("ignores old preview and settings responses after edits or manager changes", async () => {
  await render(); await click("Workspace naming"); await answer(calls[0], settings);
  await click("Preview naming"); const pending = calls.at(-1)!;
  await type("changed/{task-id}");
  await answer(pending, { status: "invalid", sample: true, diagnostics: [{ code: "branch_invalid", field: "branch_template", severity: "error" }] });
  expect(document.body.textContent).not.toContain("The generated branch name is invalid");
  await render("other"); await click("Workspace naming"); const old = calls.at(-1)!;
  await render("another"); await answer(old, settings); expect(document.querySelector("input")).toBeNull();
});

it("shows field validation without suggesting a connection problem", async () => {
  await render(); await click("Workspace naming"); await answer(calls[0], settings);
  await type("../unsafe"); await click("Preview naming");
  await answer(calls.at(-1)!, { code: "workspace_naming_path_invalid", field: "task_directory_template", error: "invalid" }, 400);
  expect(document.querySelectorAll("input")[1].getAttribute("aria-invalid")).toBe("true");
  expect(document.body.textContent).toContain("Check the highlighted fields");
  expect(document.body.textContent).not.toContain("Check its connection and version");
});

it("keeps a blank default draft across collapse without saving the fallback as an override", async () => {
  await render(); await click("Workspace naming"); await answer(calls[0], settings);
  expect([...document.querySelectorAll("details")].every((node) => !node.open)).toBe(true);
  await type("custom/{task-id}"); await click("Workspace naming");
  expect(document.querySelector("input")).toBeNull();
  await click("Workspace naming"); expect(calls).toHaveLength(1);
  expect(document.querySelectorAll("input")[1].value).toBe("custom/{task-id}");
  await type("");
  expect(document.querySelectorAll("input")[1].value).toBe("");
  expect(button("Save").disabled).toBe(true);
});

it("shows the effective assistant fallback for tasks and clears only the task override", async () => {
  await render("assistant", "todo_example"); await click("Workspace naming");
  const fallback = { ...defaults, task_directory_template: "assistant/{task-id}" };
  await answer(calls[0], { ...settings, effective_workspace_rules: fallback });
  await answer(calls.find((c) => c.path.endsWith("/workspace-rules"))!, { todo_id: "todo_example", revision: 4, workspace_rules: { task_directory_template: "custom/{task-id}", branch_template: "task/{task-id}" } });
  await answer(calls.find((c) => c.path.endsWith("/repositories"))!, { schema_version: 1, manager: "assistant", todo_id: "todo_example", revision: 0, repositories: [] });
  const field = document.querySelectorAll("input")[1];
  expect(field.value).toBe("custom/{task-id}"); expect(field.placeholder).toBe("Assistant default: assistant/{task-id}");
  expect(document.body.textContent).not.toContain("System default");
  await type(""); await click("Save");
  const save = calls.at(-1)!;
  expect(save.path).toBe("/todos/todo_example/workspace-rules");
  expect(JSON.parse(save.init.body as string)).toEqual({ expected_revision: 4, workspace_rules: { base_directory: null, task_directory_template: null, branch_template: "task/{task-id}" } });
  await answer(save, { todo_id: "todo_example", revision: 5, workspace_rules: { branch_template: "task/{task-id}" } });
  expect(field.value).toBe(""); expect(button("Save").disabled).toBe(true);
  await click("Preview naming"); expect(JSON.parse(calls.at(-1)!.init.body as string).workspace_rules.task_directory_template).toBeNull();
});

it("keeps a cleared draft after a failed save and saved-baseline reload, then retries the removal", async () => {
  const custom = { ...settings, workspace_rules: { task_directory_template: "custom/{task-id}" } };
  await render(); await click("Workspace naming"); await answer(calls[0], custom);
  await type(""); await click("Save"); await answer(calls.at(-1)!, { error: "unavailable" }, 503);
  expect(document.querySelectorAll("input")[1].value).toBe("");
  await click("Reload saved settings"); await answer(calls.at(-1)!, custom);
  expect(document.querySelectorAll("input")[1].value).toBe(""); expect(button("Save").disabled).toBe(false);
  await click("Save"); expect(JSON.parse(calls.at(-1)!.init.body as string).workspace_rules.task_directory_template).toBeNull();
  await answer(calls.at(-1)!, settings); expect(button("Save").disabled).toBe(true);
});

const rootPath = "/home/example/workspace/task-todo_example";
const named = (layout: "root" | "container", repositories: Array<[string, string, string]>, timezone = "UTC"): Preview => ({
  status: "complete", sample: true, diagnostics: [], plan: {
    version: "workspace-naming/v1", digest: "digest", base_path: "/home/example/workspace", root_path: rootPath, task_slug: "example-task",
    node: { home: "/home/example" },
    source: { manager: "assistant", todo_id: "todo_example", title: "Example task", machine_id: "box", planned_at: "2026-11-14T06:45:00Z", timezone, rules: {}, layout,
      repository_snapshot: { schema_version: 1, manager: "assistant", todo_id: "todo_example", revision: 0, digest: "snapshot", repositories: [] } },
    repositories: repositories.map(([repo_id, relative_path, task_branch]) => ({ repo_id, requested_ref: { kind: "default" }, relative_path, absolute_path: relative_path === "." ? rootPath : `${rootPath}/${relative_path}`, task_branch })),
  },
});
const showPreview = (result: Preview) => act(async () => root.render(<WorkspaceNamingPreview result={result} />));

it("shows the root once with a labeled branch and no routine metadata", async () => {
  await showPreview(named("root", [["web", ".", "persona/todo_example"]], "Asia/Shanghai"));
  const text = document.body.textContent ?? "";
  expect(document.querySelector("[data-naming-root]")?.textContent).toBe(rootPath);
  expect(text.split(rootPath).length - 1).toBe(1);
  expect(document.querySelector("[data-naming-repo]")).toBeNull();
  expect(document.querySelector("[data-naming-branch]")?.textContent).toBe("persona/todo_example");
  expect(document.querySelector("dl")).toBeNull();
  expect(document.querySelector("details")).toBeNull();
  expect(text).toContain("Example preview");
  expect(text).not.toContain("Names only");
  expect(text).not.toContain("UTC");
  expect(text).not.toContain("Asia/Shanghai");
});

it("maps repository folders to branches without repeating the root or absolute paths", async () => {
  await showPreview(named("container", [["web", "web", "persona/todo_example-web"], ["api", "api", "persona/todo_example-api"]]));
  const rows = [...document.querySelectorAll("[data-naming-repo]")].map((row) => [row.querySelector("[data-naming-folder]")?.textContent, row.querySelector("[data-naming-branch]")?.textContent]);
  expect(rows).toEqual([["web", "persona/todo_example-web"], ["api", "persona/todo_example-api"]]);
  const text = document.body.textContent ?? "";
  expect(text.split(rootPath).length - 1).toBe(1);
  expect(text).not.toContain(`${rootPath}/web`);
  expect(text).not.toContain(`${rootPath}/api`);
});

it("keeps diagnostics and preview errors visible without a plan", async () => {
  await showPreview({ ...named("root", []), status: "invalid", plan: undefined, diagnostics: [{ code: "path_invalid", field: "task_directory_template", severity: "error" }] });
  expect(document.body.textContent).toContain("The folder template contains an unsafe path");
  await act(async () => root.render(<WorkspaceNamingPreview error="Couldn’t preview names on this computer." />));
  expect(document.querySelector("[role=alert]")?.textContent).toBe("Couldn’t preview names on this computer.");
});
