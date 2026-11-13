// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
vi.hoisted(() => { window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {} })) as typeof window.matchMedia; });
import { initI18n } from "../i18n";
import { credentialOwner, setToken } from "../lib/api";
import { WorkspaceNaming } from "./WorkspaceNaming";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
type Call = { path: string; init: RequestInit; answer: (body: unknown, status?: number) => void };
let root: Root, host: HTMLDivElement, calls: Call[];
const defaults = { base_directory: "{home}/workspace", task_directory_template: "task-{task-id}", branch_template: "persona/{task-id}" };
const settings = { manager: "assistant", machine_id: "box", workspace_naming_version: "workspace-naming/v1", workspace_rules: {}, effective_workspace_rules: defaults };
const button = (label: string) => [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label)!;
const click = (label: string) => act(async () => { expect(button(label), label).toBeTruthy(); button(label).click(); });
const answer = (call: Call, body: unknown, status = 200) => act(async () => call.answer(body, status));
const render = (manager = "assistant") => act(async () => root.render(<WorkspaceNaming manager={manager} owner={credentialOwner()} />));
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

it("loads on demand, persists only the explicit naming layer and resets inheritance", async () => {
  await render(); expect(calls).toHaveLength(0); await click("Workspace naming"); await answer(calls[0], settings);
  await type("custom/{task-id}"); await click("Save"); const save = calls.at(-1)!;
  expect(new Headers(save.init.headers).get("X-Persona-Manager")).toBe("assistant");
  expect(JSON.parse(save.init.body as string)).toEqual({ workspace_rules: { base_directory: null, task_directory_template: "custom/{task-id}", branch_template: null } });
  await answer(save, { ...settings, workspace_rules: { task_directory_template: "custom/{task-id}" } });
  expect(button("Save").disabled).toBe(true);
  await click("Restore inheritance"); expect(document.querySelectorAll("input")[1].value).toBe(defaults.task_directory_template);
  await click("Save"); await answer(calls.at(-1)!, settings); expect(button("Save").disabled).toBe(true);
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
