// @vitest-environment jsdom
// The Workbench sidebar (#13439): Persona's row first, projects by folder name, state words and the needs-you count in
// the rows, a project's "+" starting a thread in it, folding kept, and rows that are links.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
const opened = vi.hoisted(() => ({ presets: [] as unknown[] }));
vi.mock("../../lib/new-agent", async (load) => ({ ...(await load<typeof import("../../lib/new-agent")>()), openNewAgent: (p: unknown) => void opened.presets.push(p) }));
import { reset, upsertSession } from "../../lib/store";
import { readWorkbenchLayout, WORKBENCH_LAYOUT_DEFAULT, writeWorkbenchLayout } from "../../lib/labs-workbench";
import { STATE_LABEL } from "../../lib/state-labels";
import type { Session } from "../../lib/types";
import { t } from "../../i18n";
import { WorkbenchSidebar } from "./WorkbenchSidebar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const row = (session_id: string, cwd: string, extra: Partial<Session> = {}): Session => ({ machine_id: "mac", session_id, title: `Task ${session_id}`, cwd, status: "idle", state: "idle", ...extra });
let host: HTMLDivElement;
let root: Root;
const render = (route: Parameters<typeof WorkbenchSidebar>[0]["route"] = { name: "home" }) => act(() => root.render(<WorkbenchSidebar route={route} onSearch={() => {}} onNewAgent={() => {}} />));

beforeEach(() => {
  localStorage.clear();
  writeWorkbenchLayout(WORKBENCH_LAYOUT_DEFAULT);
  opened.presets = [];
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  act(() => {
    upsertSession(row("w", "/code/app", { state: "working", status: "working" }));
    upsertSession(row("n", "/code/app", { state: "needs_you", status: "blocked" }));
    upsertSession(row("i", "/code/api"));
  });
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
});

describe("the Workbench sidebar", () => {
  it("lists Persona's row first, then projects by folder name with plain state words", () => {
    render();
    const nav = host.querySelector("nav")!;
    expect(nav.firstElementChild?.hasAttribute("data-workbench-persona")).toBe(true);
    expect([...host.querySelectorAll("[data-workbench-project]")].map((p) => p.getAttribute("aria-label"))).toEqual(["app", "api"]);
    const rows = [...host.querySelectorAll("[data-workbench-thread]")].map((a) => a.textContent);
    expect(rows[0]).toContain("Task n");
    expect(rows[0]).toContain(STATE_LABEL.needs_you);
    expect(rows[1]).toContain(STATE_LABEL.working);
    expect(host.textContent).not.toContain("/code");
  });

  it("shows the needs-you count on the project, spoken as the sidebar's own words", () => {
    render();
    const badge = host.querySelector("[data-workbench-project] [data-count-badge]")!;
    expect(badge.textContent).toBe("1");
  });

  it("makes each thread a link to its page and marks the open one", () => {
    render({ name: "session", mid: "mac", sid: "w" });
    const open = host.querySelector<HTMLAnchorElement>('a[href="/s/mac/w"]')!;
    expect(open.getAttribute("aria-current")).toBe("page");
    expect(host.querySelector('a[href="/s/mac/i"]')?.getAttribute("aria-current")).toBeNull();
  });

  it("starts a thread in a project from its plus, on its computer and in its folder", () => {
    render();
    const plus = host.querySelector<HTMLButtonElement>(`button[aria-label="${t("workbench.new_thread_in", { name: "api" })}"]`)!;
    act(() => plus.click());
    expect(opened.presets).toEqual([{ machineId: "mac", cwd: "/code/api" }]);
  });

  it("keeps a folded project folded, but never hides one that needs you or holds the open thread", () => {
    act(() => writeWorkbenchLayout({ folded: ["mac\n/code/app", "mac\n/code/api"] }));
    render({ name: "home" });
    const labels = () => [...host.querySelectorAll("[data-workbench-project]")].map((p) => [p.getAttribute("aria-label"), p.querySelectorAll("[data-workbench-thread]").length]);
    // "app" needs you: open; "api" is folded
    expect(labels()).toEqual([["app", 2], ["api", 0]]);
    render({ name: "session", mid: "mac", sid: "i" });
    expect(labels()).toEqual([["app", 2], ["api", 1]]);
  });

  it("folds and unfolds a project, kept", () => {
    render();
    const toggle = () => host.querySelectorAll<HTMLButtonElement>("[data-workbench-project] button[aria-expanded]")[1];
    act(() => toggle().click());
    expect(readWorkbenchLayout().folded).toEqual(["mac\n/code/api"]);
    expect(host.querySelectorAll("[data-workbench-project]")[1].querySelectorAll("[data-workbench-thread]")).toHaveLength(0);
    act(() => toggle().click());
    expect(readWorkbenchLayout().folded).toEqual([]);
  });
});
