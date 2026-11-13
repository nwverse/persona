// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Machine } from "../lib/types";
import type { HostWorkspace } from "../lib/workspace-catalog";

const state = vi.hoisted(() => ({ loaded: true, features: [] as string[], machines: [] as Machine[], workspaces: [] as HostWorkspace[], desktopSource: true }));
const launched = vi.hoisted(() => [] as Record<string, unknown>[]);
vi.mock("../lib/current-assistant", () => ({ useScopedMachines: () => state.machines }));
vi.mock("../lib/store", () => ({ getState: () => state, useStore: (select: (s: typeof state) => unknown) => select(state), hubHas: () => false }));
vi.mock("react-dom", async (original) => ({ ...(await original<typeof import("react-dom")>()), createPortal: (children: React.ReactNode) => children }));
vi.mock("../lib/new-agent", async (original) => ({ ...(await original<typeof import("../lib/new-agent")>()), launchAgent: (input: Record<string, unknown>) => { launched.push(input); return "local-1"; } }));
vi.mock("../lib/workspace-catalog", async (original) => {
  const real = await original<typeof import("../lib/workspace-catalog")>();
  return { ...real, useWorkspaceCatalog: () => ({ ...real.workspaceCatalogOwner(), workspaces: state.workspaces }) };
});
import { NewAgent } from "./NewAgent";
import { resetModelListsForTests } from "../lib/session-models";
import { t } from "../i18n";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root;
let host: HTMLDivElement;
const flush = () => act(async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); });
const button = (key: string) => [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent === t(key))!;
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const open = async (launcher: string, cwd = "", desktopProjectId?: string) => {
  act(() => root.render(<NewAgent open preset={{ machineId: "pc", cwd, launcher, desktopProjectId }} onClose={() => {}} />));
  await flush();
};
const type = (selector: string, value: string) => act(() => {
  const el = host.querySelector<HTMLInputElement | HTMLTextAreaElement>(selector)!;
  const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(proto, "value")!.set!.call(el, value);
  el.dispatchEvent(new Event("input", { bubbles: true }));
});

beforeEach(() => {
  state.machines = [{ id: "pc", name: "Computer", online: true }];
  state.desktopSource = true;
  state.workspaces = [];
  launched.length = 0; localStorage.clear(); resetModelListsForTests();
  localStorage.setItem("new.machine", "pc");
  localStorage.setItem("new.mode.pc", "plan");
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes("/model-config/purposes/")) return json({}, 404);
    if (url.includes("/models")) return json({ agent: url.includes("codex-app") ? "codex" : "claude", models: [], locked: "Settings follow the desktop app." });
    if (url.includes("/launchers")) return json({ launchers: [{ name: "claude-desktop", agent: "claude", source: state.desktopSource ? "claude_desktop" : undefined }, { name: "codex-app", agent: "codex", source: state.desktopSource ? "codex_app" : undefined }, { name: "claude", agent: "claude" }] });
    return json({ path: "C:\\Projects", parent: "C:\\", entries: [] });
  }));
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); vi.unstubAllGlobals(); });

describe("New Agent in a desktop application", () => {
  it("allows a Claude temporary session and keeps its own permissions", async () => {
    await open("claude-desktop");
    expect(host.textContent).toContain(t("newagent.temporary_folder"));
    expect(host.textContent).not.toContain(t("newagent.permission_mode"));
    expect(button("newagent.start").disabled).toBe(false);
    act(() => button("newagent.start").click());
    expect(launched).toEqual([expect.objectContaining({ launcher: "claude-desktop", cwd: "", permissionMode: undefined, host: undefined, workspaceId: undefined })]);
    expect(localStorage.getItem("new.mode.pc")).toBe("plan");
  });

  it("requires the first message for Codex even when the temporary folder is selected", async () => {
    await open("codex-app");
    expect(button("newagent.start").disabled).toBe(true);
    const area = host.querySelector("textarea")!;
    act(() => area.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", ctrlKey: true, bubbles: true })));
    expect(launched).toEqual([]);
    type("textarea", "Check the project");
    expect(button("newagent.start").disabled).toBe(false);
    act(() => button("newagent.start").click());
    expect(launched).toEqual([expect.objectContaining({ launcher: "codex-app", cwd: "", inlinePrompt: true, agentType: "codex", text: "Check the project" })]);
  });

  it("passes a chosen Windows project directory to its desktop launcher", async () => {
    await open("claude-desktop", "C:\\Projects\\Sample");
    act(() => button("newagent.start").click());
    expect(launched).toEqual([expect.objectContaining({ launcher: "claude-desktop", cwd: "C:\\Projects\\Sample", workspaceId: undefined, host: undefined })]);
  });

  it.each([["codex-app", "codex-project:home:second"], ["claude-desktop", "claude-environment:account:second"]])("keeps the exact native project for %s when projects share a directory", async (launcher, desktopProjectId) => {
    await open(launcher, "C:\\Projects\\Sample", desktopProjectId);
    type("textarea", "Check this project");
    act(() => button("newagent.start").click());
    expect(launched).toEqual([expect.objectContaining({ desktopProjectId, launcher, workspaceId: undefined })]);
  });

  it.each([["codex-app", "codex_app", "codex-project:home:second"], ["claude-desktop", "claude_desktop", "claude-environment:account:second"]])("keeps %s project identity when another catalog root is selected", async (launcher, source, desktopProjectId) => {
    state.workspaces = ["C:\\Projects\\Sample", "D:\\Docs"].map((cwd) => ({ id: cwd, machine_id: "pc", source, source_id: desktopProjectId, title: "Saved project", cwd }));
    await open(launcher, "C:\\Projects\\Sample", desktopProjectId);
    type(`input[placeholder="${t("newagent.desktop_folder_placeholder")}"]`, "d:/docs/pages");
    type("textarea", "Check this project");
    expect(host.textContent).not.toContain(t("newagent.desktop_project_detached"));
    act(() => button("newagent.start").click());
    expect(launched).toEqual([expect.objectContaining({ desktopProjectId, cwd: "d:/docs/pages" })]);
  });

  it.each([["codex-app", "codex-project:home:second"], ["claude-desktop", "claude-environment:account:second"]])("clears %s project identity when a temporary session is selected", async (launcher, desktopProjectId) => {
    await open(launcher, "C:\\Projects\\Sample", desktopProjectId);
    act(() => button("newagent.temporary_folder").click());
    type("textarea", "Check a new task");
    expect(host.textContent).toContain(t("newagent.desktop_project_detached"));
    act(() => button("newagent.start").click());
    expect(launched).toEqual([expect.objectContaining({ desktopProjectId: undefined, cwd: "" })]);
  });

  it.each([["codex-app", "codex-project:home:second"], ["claude-desktop", "claude-environment:account:second"]])("clears %s project identity when a directory outside its roots is selected", async (launcher, desktopProjectId) => {
    await open(launcher, "C:\\Projects\\Sample", desktopProjectId);
    type(`input[placeholder="${t("newagent.desktop_folder_placeholder")}"]`, "D:\\Other");
    type("textarea", "Check a new task");
    expect(host.textContent).toContain(t("newagent.desktop_project_detached"));
    act(() => button("newagent.start").click());
    expect(launched).toEqual([expect.objectContaining({ desktopProjectId: undefined, cwd: "D:\\Other" })]);
  });

  it("honors an explicitly temporary preset over the remembered computer and folder", async () => {
    state.machines.push({ id: "other", name: "Other", online: true });
    localStorage.setItem("new.machine", "other");
    localStorage.setItem("new.cwd.pc", "C:\\Projects\\Remembered");
    await open("claude-desktop");
    act(() => button("newagent.start").click());
    expect(launched).toEqual([expect.objectContaining({ machineId: "pc", cwd: "", launcher: "claude-desktop" })]);
  });

  it("refuses a terminal alias with the same name as the requested desktop app", async () => {
    state.desktopSource = false;
    await open("claude-desktop", "C:\\Projects\\Sample");
    expect(host.querySelector('[role="alert"]')?.textContent).toContain(t("newagent.desktop_unavailable", { app: "Claude" }));
    expect(button("newagent.start").disabled).toBe(true);
    act(() => button("newagent.start").click());
    expect(launched).toEqual([]);
  });
});
