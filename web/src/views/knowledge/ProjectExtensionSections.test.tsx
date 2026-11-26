// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import { setToken } from "../../lib/api";
import { EXTENSIONS_CHANGED } from "../../lib/extension-prompts";
import { resetManagerChoice, setRosterDefault } from "../../lib/manager-choice";
import { navigate, parse, routePath } from "../../lib/router";
import { getState } from "../../lib/store";
import { ProjectExtensionSections } from "./ProjectExtensionSections";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const notes = { extension: "quicknotes", extension_name: "Quick notes", id: "notes", title: "Project notes", placement: "project_section" };
const base = "/kb/projects/atlas";
const direct = base + "?extension=quicknotes&ui=notes";
const view = { sections: [{ items: [{ type: "text", text: "First project note" }] }] };
let root: Root;
let host: HTMLDivElement;
let listed = [notes];
let calls: { url: string; options: RequestInit }[];
let features: string[];
const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const drain = async () => {
  await act(async () => { await vi.dynamicImportSettled(); });
  await act(async () => { await Promise.resolve(); });
};
const show = async (path: string) => {
  navigate(path, true);
  await act(async () => root.render(<ProjectExtensionSections project="atlas" manager="writer" />));
  await drain();
};
const renders = () => calls.filter(c => c.options.method === "POST");
beforeEach(async () => {
  await initI18n("en");
  resetManagerChoice();
  setToken("synthetic-project-section-test");
  setRosterDefault("writer");
  features = getState().features;
  getState().features = ["extension_ui_slots", "extensions"];
  calls = [];
  listed = [notes];
  vi.stubGlobal("fetch", vi.fn(async (url: string, options: RequestInit = {}) => {
    calls.push({ url, options });
    return response(options.method === "POST" ? { view } : { ui: listed });
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  getState().features = features;
  resetManagerChoice();
  setToken("");
  vi.unstubAllGlobals();
});

it("lists declared sections without rendering providers; opening a link renders only that manager's project", async () => {
  await show(base);
  const entry = host.querySelector<HTMLAnchorElement>("a")!;
  expect(entry.getAttribute("href")).toBe(direct);
  expect(renders()).toHaveLength(0);
  expect(calls[0].url).toContain("placement=project_section");
  expect(new Headers(calls[0].options.headers).get("X-Persona-Manager")).toBe("writer");
  await act(async () => entry.click());
  await drain();
  expect(host.textContent).toContain("First project note");
  const render = renders()[0];
  expect(render.url).toContain("/quicknotes/-/ui/notes");
  expect(new Headers(render.options.headers).get("X-Persona-Manager")).toBe("writer");
  const { context } = JSON.parse(String(render.options.body));
  expect(context.project_id).toBe("atlas");
  expect(context).not.toHaveProperty("item");
  expect(context).not.toHaveProperty("assistant");
  expect(context).not.toHaveProperty("summary");
  expect(location.search).toBe("?extension=quicknotes&ui=notes");
  listed = [];
  act(() => window.dispatchEvent(new Event(EXTENSIONS_CHANGED)));
  await drain();
  expect(location.pathname + location.search).toBe(base);
  expect(host.textContent).not.toContain("First project note");
});

it("opens a bookmarked section after the live list resolves; absence returns to its project", async () => {
  await show(direct);
  expect(host.textContent).toContain("First project note");
  await act(async () => navigate(base + "?extension=another&ui=notes"));
  await drain();
  expect(location.pathname + location.search).toBe(base);
  expect(renders().every(c => c.url.includes("/quicknotes/"))).toBe(true);
});

it("round-trips encoded project addresses and ignores incomplete or non-project section queries", () => {
  const route = parse("/kb/projects/%E9%A1%B9%E7%9B%AE", "?extension=notebook&ui=notes");
  expect(route).toEqual({ name: "kb", kind: "projects", id: "项目", section: { extension: "notebook", ui: "notes" } });
  expect(routePath(route)).toBe("/kb/projects/%E9%A1%B9%E7%9B%AE?extension=notebook&ui=notes");
  expect(parse(base, "?extension=quicknotes")).not.toHaveProperty("section");
  expect(parse("/kb/people/member", "?extension=quicknotes&ui=notes")).not.toHaveProperty("section");
});
