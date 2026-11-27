// @vitest-environment jsdom
// #13019: Overview's two layouts. By default (Classic layout off) what waits on the owner, the watch cards, then the
// projects (views/overview/ProjectOverview.tsx) in place of the activity sections; with Settings › General › Overview's
// Classic layout on, the sections in columns exactly as before. The data made up.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { classicOverview, setClassicOverview } from "../lib/overview-layout";
import type { Todo } from "../lib/todos";
import type { Machine, Session } from "../lib/types";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const fake = vi.hoisted(() => ({ sessions: [] as unknown[], todos: [] as unknown[] }));

vi.mock("../lib/store", async (importOriginal) => {
  const real = await importOriginal<typeof import("../lib/store")>();
  const mac: Machine = { id: "mac", name: "Mac", online: true };
  return { ...real, useStore: <T,>(select: (s: ReturnType<typeof real.getState>) => T) => select({ ...real.getState(), loaded: true, machines: [mac], interactions: [], todos: fake.todos as Todo[], answered: {} }) };
});
vi.mock("../lib/sessions", async (importOriginal) => {
  const real = await importOriginal<typeof import("../lib/sessions")>();
  const sessions = () => fake.sessions as Session[];
  return { ...real, useAgentSessions: sessions, useCounts: () => ({ ...real.stateCounts(sessions()), interactions: 0 }), useAgentListStatus: () => ({ status: "loaded", whole: true }) };
});
vi.mock("./extensions/ExtensionSlots", () => ({ OverviewCards: () => null, OverviewArrangeButton: () => null }));
vi.mock("./watch/WatchWall", () => ({ default: () => <section data-watch-wall="" /> }));
vi.mock("./overview/ProjectOverview", () => ({ default: () => <section data-project-overview="" /> }));

const { Home } = await import("./Home");
const { t, initI18n } = await import("../i18n");

const agent = (sid: string, o: Partial<Session>): Session =>
  ({ machine_id: "mac", session_id: sid, title: sid, cwd: "/code", status: "idle", state: "idle", agent: "claude", updated_at: "2026-11-19T08:00:00Z", ...o }) as Session;

let host: HTMLDivElement;
let root: Root;
beforeEach(async () => {
  await initI18n("en");
  localStorage.clear();
  fake.sessions = [agent("busy", { state: "working", status: "working" }), agent("done", { state: "unread" })];
  fake.todos = [{ id: "td1", title: "Write the store text", created_at: "2026-11-19T07:00:00Z" }];
  host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const headings = () => [...host.querySelectorAll("h2")].map((h) => h.textContent);
async function render() {
  await act(async () => root.render(<Home onNewAgent={() => {}} onToggleSidebar={() => {}} />));
  // the lazy chunks
  await vi.waitFor(() => expect(host.querySelector("[data-watch-wall]")).toBeTruthy());
}

describe("Overview's layout", () => {
  it("shows the watch cards, then the projects, and no activity sections by default", async () => {
    expect(classicOverview()).toBe(false);
    await render();
    await vi.waitFor(() => expect(host.querySelector("[data-project-overview]")).toBeTruthy());
    const wall = host.querySelector("[data-watch-wall]")!;
    const projects = host.querySelector("[data-project-overview]")!;
    expect(wall).toBeTruthy();
    expect(projects).toBeTruthy();
    expect(wall.compareDocumentPosition(projects) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(headings()).not.toContain(t("home.tasks"));
    expect(headings()).not.toContain(t("home.computers"));
    expect(headings()).not.toContain(t("lib-state-labels.working"));
  });

  it("draws the classic sections, and no projects, with Classic layout on", async () => {
    setClassicOverview(true);
    await render();
    expect(host.querySelector("[data-project-overview]")).toBeNull();
    expect(host.querySelector("[data-watch-wall]")).toBeTruthy();
    expect(headings()).toContain(t("home.tasks"));
    expect(headings()).toContain(t("home.computers"));
    // and back: the switch applies at once
    await act(async () => setClassicOverview(false));
    await vi.waitFor(() => expect(host.querySelector("[data-project-overview]")).toBeTruthy());
    expect(headings()).not.toContain(t("home.tasks"));
  });
});
