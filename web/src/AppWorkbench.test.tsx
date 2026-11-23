// @vitest-environment jsdom
// The Workbench layout in the shell (#13439): off by default the shell draws the usual sidebar and page, with no trace of
// the layout; on and wide it draws the project sidebar and frames a session's conversation with the changes and the
// terminal drawer, listing a thread that needs you under its project; flipping the switch changes it at once with no
// reload; a narrow window keeps the usual shell; the toggles and the usual chords (⌘B, ⌘J) keep their flags.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const env = vi.hoisted(() => ({ narrow: false, git: true }));
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
});
vi.hoisted(() => {
  // one list per query, as the browser gives, whose `matches` follows the window's width at the time it is read
  const lists = new Map<string, MediaQueryList>();
  window.matchMedia = ((query: string) => {
    if (!lists.has(query)) lists.set(query, { get matches() { return query.includes("max-width: 1023px") ? env.narrow : false; }, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false } as MediaQueryList);
    return lists.get(query)!;
  }) as typeof window.matchMedia;
});
vi.mock("./views/Sidebar", () => ({ Sidebar: () => <div data-usual-sidebar />, SidebarRail: () => null }));
vi.mock("./views/chat/ChatView", () => ({ ChatView: ({ terminalAvailable }: { terminalAvailable?: boolean }) => <div data-chat data-terminal-available={String(terminalAvailable)} /> }));
vi.mock("./views/session/ReviewPanel", () => ({ ReviewPanel: ({ sid }: { sid: string }) => <div data-review={sid} /> }));
vi.mock("./views/session/TerminalPanel", () => ({ TerminalPanel: ({ sid }: { sid: string }) => <div data-terminal={sid} /> }));
vi.mock("./views/Palette", () => ({ Palette: () => null }));
vi.mock("./views/NewAgent", () => ({ NewAgent: () => null }));
vi.mock("./views/NewWorktreeHost", () => ({ NewWorktreeHost: () => null }));
vi.mock("./views/SidePanel", () => ({ SidePanel: () => null }));
vi.mock("./views/ShortcutHelp", () => ({ ShortcutHelp: () => null }));
// the hub's features arrive with its hello: this stands in for the one that says it has Git review
vi.mock("./lib/store", async (load) => {
  const actual = await load<typeof import("./lib/store")>();
  return { ...actual, connect: () => {}, disconnect: () => {}, hubHas: (features: string[], feature: string, unknown?: boolean) => (feature === "git_review" ? env.git : actual.hubHas(features, feature, unknown)) };
});
import { refresh, reset } from "./lib/store";
import { setToken } from "./lib/api";
import { readWorkbench, readWorkbenchLayout, WORKBENCH_LAYOUT_DEFAULT, writeWorkbench, writeWorkbenchLayout } from "./lib/labs-workbench";
import { runShortcut } from "./lib/shortcuts";
import { contextCommands } from "./lib/palette";
import type { Machine, Session } from "./lib/types";
import { App } from "./App";
import { t } from "./i18n";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const machines: Machine[] = [{ id: "mac", name: "MacBook", online: true }];
const row = (session_id: string, cwd: string, extra: Partial<Session> = {}): Session => ({ machine_id: "mac", session_id, title: `Task ${session_id}`, cwd, status: "idle", state: "idle", ...extra });
const sessions: Session[] = [
  row("s1", "/code/app", { state: "working", status: "working" }),
  row("s2", "/code/app", { state: "needs_you", status: "blocked" }),
  row("s3", "/code/api"),
];
const answers: Record<string, unknown> = { "/api/v1/sessions": { sessions }, "/api/v1/machines": { machines } };

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); });
};
// the layout's chunks load on first use: wait for what they draw
const until = async (sel: string) => {
  for (let i = 0; i < 200 && !host.querySelector(sel); i++) await act(async () => { await new Promise((r) => setTimeout(r, 10)); });
};
const open = async (path: string) => {
  history.replaceState(null, "", path);
  await act(() => refresh());
  act(() => root.render(<App />));
  await flush();
  if (readWorkbench().value && window.matchMedia("(max-width: 1023px)").matches === false) {
    await until("[data-workbench-sidebar]");
    if (path.startsWith("/s/")) await until("[data-workbench-thread-frame]");
  }
};
const q = (sel: string) => host.querySelector<HTMLElement>(sel);

beforeEach(() => {
  localStorage.clear();
  writeWorkbenchLayout(WORKBENCH_LAYOUT_DEFAULT);
  localStorage.clear();
  env.narrow = false;
  env.git = true;
  setToken("tok");
  vi.stubGlobal("fetch", vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(answers[new URL(url, "http://x").pathname] ?? {}), { headers: { "Content-Type": "application/json" } }))));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.unstubAllGlobals();
});

describe("with the switch off", () => {
  it("draws the usual sidebar and page, with nothing of the layout", async () => {
    await open("/s/mac/s1");
    expect(q("[data-usual-sidebar]")).not.toBeNull();
    expect(q("[data-chat]")?.dataset.terminalAvailable).toBe("undefined");
    expect(q("[data-workbench-sidebar]")).toBeNull();
    expect(q("[data-workbench-thread-frame]")).toBeNull();
    expect(q("[data-workbench-diff]")).toBeNull();
  });
});

describe("with the switch on", () => {
  beforeEach(() => void writeWorkbench(true));

  it("draws the project sidebar, folder names and states, and a thread that needs you under its project", async () => {
    await open("/s/mac/s1");
    const bar = q("[data-workbench-sidebar]")!;
    expect(bar).not.toBeNull();
    expect(q("[data-usual-sidebar]")).toBeNull();
    const projects = [...bar.querySelectorAll("[data-workbench-project]")];
    // the project with a thread that needs you goes first, and says so on its header
    expect(projects.map((p) => p.getAttribute("aria-label"))).toEqual(["app", "api"]);
    expect(projects[0].textContent).toContain("Task s2");
    expect(projects[0].querySelector("[data-count-badge]")?.textContent).toBe("1");
    // folder names, never a path or an id
    expect(bar.textContent).not.toContain("/code");
    expect(bar.textContent).not.toContain("s1/");
    expect(bar.querySelector('[data-workbench-persona]')).not.toBeNull();
    const open1 = bar.querySelector<HTMLAnchorElement>('a[href="/s/mac/s1"]')!;
    expect(open1.getAttribute("aria-current")).toBe("page");
  });

  it("frames the conversation with the changes on the right and the chat's own terminal switch off", async () => {
    await open("/s/mac/s1");
    expect(q("[data-workbench-thread-frame]")).not.toBeNull();
    expect(q("[data-chat]")?.dataset.terminalAvailable).toBe("false");
    expect(q("[data-workbench-diff] [data-review='s1']")).not.toBeNull();
    expect(q("[data-workbench-drawer]")).toBeNull();
  });

  it("says so in the changes region when the hub has no Git review", async () => {
    env.git = false;
    await open("/s/mac/s1");
    expect(q("[data-workbench-diff]")?.textContent).toContain(t("workbench.diff_unavailable"));
    expect(q("[data-review]")).toBeNull();
  });

  it("opens and closes the terminal drawer and the changes with the toggles, keeping the flags", async () => {
    await open("/s/mac/s1");
    const toggle = (label: string) => host.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`)!;
    act(() => toggle(t("workbench.toggle_terminal")).click());
    await until("[data-workbench-drawer] [data-terminal]");
    expect(q("[data-workbench-drawer] [data-terminal='s1']")).not.toBeNull();
    expect(readWorkbenchLayout().drawer).toBe(true);
    act(() => toggle(t("workbench.toggle_diff")).click());
    await flush();
    expect(q("[data-workbench-diff]")).toBeNull();
    expect(readWorkbenchLayout().diff).toBe(false);
  });

  it("keeps ⌘B and ⌘J on the changes and the terminal, and adds palette commands", async () => {
    await open("/s/mac/s1");
    expect(runShortcut("terminal-chat")).toBe(true);
    await flush();
    expect(readWorkbenchLayout().drawer).toBe(true);
    expect(runShortcut("right-panel")).toBe(true);
    await flush();
    expect(readWorkbenchLayout().diff).toBe(false);
    const ids = contextCommands({ route: { name: "session", mid: "mac", sid: "s1" }, pane: { mid: "mac", sid: "s1" } }).map((c) => c.id);
    expect(ids).toEqual(expect.arrayContaining(["workbench:diff", "workbench:terminal", "workbench:new-thread"]));
  });

  it("draws other pages as usual inside the layout's sidebar", async () => {
    await open("/settings");
    expect(q("[data-workbench-sidebar]")).not.toBeNull();
    expect(q("[data-workbench-thread-frame]")).toBeNull();
  });

});

describe("switching and window width", () => {
  it("switches on and off at once with no reload, the open page staying", async () => {
    await open("/s/mac/s1");
    expect(q("[data-workbench-sidebar]")).toBeNull();
    const path = location.pathname;
    act(() => void writeWorkbench(true));
    await until("[data-workbench-sidebar]");
    expect(q("[data-workbench-sidebar]")).not.toBeNull();
    act(() => void writeWorkbench(false));
    await flush();
    expect(q("[data-workbench-sidebar]")).toBeNull();
    expect(q("[data-usual-sidebar]")).not.toBeNull();
    expect(location.pathname).toBe(path);
  });

  it("keeps the usual shell in a narrow window", async () => {
    writeWorkbench(true);
    env.narrow = true;
    await open("/s/mac/s1");
    expect(q("[data-workbench-sidebar]")).toBeNull();
    expect(q("[data-workbench-thread-frame]")).toBeNull();
    expect(q("[data-usual-sidebar]")).not.toBeNull();
  });
});
