// @vitest-environment jsdom
// Side sessions in the sidebar's complete Agents list (#7755, after the paged list's #5935): a Claude desktop app's side
// chat or /btw question (`parent_agent_id`) is drawn under the session it was started from in every grouping's sections,
// By repository included, and Pinned keeps the order the user chose. "Side sessions (N)" folds them; the choice is kept
// per parent and follows the list's Collapse all / Expand all, a folded side session that needs the user stays a row,
// a search or filter opens the fold, and no count changes.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { t } from "../i18n";
import { DEFAULT_DIMENSION_FILTERS } from "../lib/agent-chatlist-filter";
import { setAgentListFilters } from "../lib/agent-list-filters";
import { resetFoldAll } from "../lib/fold-all";
import { setSidebarCollapsed } from "../lib/layout";
import { setAgentsFolded } from "../lib/sidebar-api";
import { refresh, reset } from "../lib/store";
import type { AgentPins } from "../lib/pins";
import type { Session } from "../lib/types";
import { Sidebar } from "./Sidebar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const at = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString();
// A review started on the Mac two days ago, and two sessions the desktop app started from it since: a /btw question
// that waits for the user and a side chat. Another session in another folder is no relation of theirs.
const desktop = { machine_id: "mac", space_id: "", space_name: "" } as const;
const review: Session = { ...desktop, session_id: "review", title: "Review the PR", cwd: "/code/weather", status: "idle", state: "idle", updated_at: at(2880) };
const btw: Session = { ...desktop, session_id: "btw", title: "Can it merge now", cwd: "/code/weather", status: "blocked", state: "needs_you", pending_interactions: 1, updated_at: at(4), parent_agent_id: "review" };
const side: Session = { ...desktop, session_id: "side", title: "Side chat", cwd: "/code/weather", status: "idle", state: "idle", updated_at: at(9), parent_agent_id: "review" };
const other: Session = { ...desktop, session_id: "other", title: "Tidy the notes", cwd: "/code/notes", status: "idle", state: "idle", updated_at: at(60) };
const sessions = [btw, side, other, review];
const TITLES = sessions.map((s) => s.title!);

let host: HTMLDivElement;
let root: Root;
let pins: AgentPins;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};
// past a fold's motion (Collapse settles after 450 ms at most)
const settle = async () => {
  await act(async () => { await new Promise((r) => setTimeout(r, 500)); });
  await flush();
};
const block = () => host.querySelector<HTMLElement>("[data-sidebar-agents]")!;
/** The agent rows in the list's order: which session, drawn a level in, and in view (not in a folded part). */
const rows = (within: ParentNode = block()) => [...within.querySelectorAll<HTMLAnchorElement>('a[href^="/s/"]')].map((a) => ({
  title: TITLES.find((x) => a.textContent?.includes(x)) ?? "",
  nested: Boolean(a.closest("[data-nested-session]")),
  shown: !a.closest("[inert]"),
})).filter((r) => r.title);
const shown = () => rows().filter((r) => r.shown).map((r) => `${r.nested ? "  " : ""}${r.title}`);
/** The review's group as drawn: the other session goes wherever a grouping puts it (By repository, its folder first). */
const group = () => shown().filter((x) => x !== other.title);
const folds = () => [...block().querySelectorAll<HTMLButtonElement>("[data-side-fold] button")];
const foldLabel = t("sidebar.side_sessions", { count: 2 });
/** Every group heading open, so the rows are drawn (By status starts Seen folded). */
const openGroups = async () => {
  for (const h of block().querySelectorAll<HTMLButtonElement>("[data-sidebar-group-heading] button[aria-expanded='false'], [data-tree-branch-row] button[aria-expanded='false']")) {
    if (!h.closest("[data-sidebar-agents-controls]")) act(() => h.click());
  }
  await settle();
};
const render = async (grouping: string) => {
  localStorage.setItem("persona.agentGrouping", grouping);
  act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
  await flush();
  await openGroups();
};
/** What the list counts: every group's number on its heading and the quick filters' numbers. */
const counts = () => [...block().querySelectorAll("[data-sidebar-group-heading], [data-tree-branch-row], [role=radiogroup] button")]
  .filter((el) => !el.closest("[inert]") || el.matches("[data-sidebar-group-heading]"))
  .map((el) => (el.getAttribute("aria-label") ?? el.textContent ?? "").replace(/\s+/g, " ").trim());

beforeEach(async () => {
  localStorage.clear();
  resetFoldAll();
  setSidebarCollapsed(false);
  setAgentListFilters(DEFAULT_DIMENSION_FILTERS);
  history.replaceState(null, "", "/");
  pins = { spaces: [], sessions: [] };
  vi.stubGlobal("fetch", vi.fn((url: string) => {
    const path = new URL(url, "http://x").pathname;
    const body = path === "/api/v1/sessions" ? { sessions } : path === "/api/v1/machines" ? { machines: [{ id: "mac", name: "Work Mac", online: true }] }
      : path === "/api/v1/pins" ? pins : path === "/api/v1/todos" ? { todos: [] } : {};
    return Promise.resolve(new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } }));
  }));
  await refresh();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  setAgentListFilters(DEFAULT_DIMENSION_FILTERS);
  reset();
  vi.unstubAllGlobals();
});

describe("side sessions in the complete list (#7755)", () => {
  // every grouping draws them under their parent, a level in, after its fold; By status's sections are the states, so
  // the side session that needs the user is in Needs you, not with its parent
  for (const [grouping, expected] of [
    ["repo", ["Review the PR", "  Can it merge now", "  Side chat", "Tidy the notes"]],
    ["space", ["Review the PR", "  Can it merge now", "  Side chat", "Tidy the notes"]],
    ["machine", ["Review the PR", "  Can it merge now", "  Side chat", "Tidy the notes"]],
    ["todo", ["Review the PR", "  Can it merge now", "  Side chat", "Tidy the notes"]],
    ["state", ["Can it merge now", "Review the PR", "  Side chat", "Tidy the notes"]],
  ] as const) {
    it(`nests them under their parent By ${grouping}`, async () => {
      await render(grouping);
      const order = shown();
      expect([...order].map((x) => x.trim()).sort()).toEqual([...TITLES].sort());
      // the group in the list's order; the other session wherever its grouping puts it
      const group = order.filter((x) => x !== "Tidy the notes");
      expect(group).toEqual(expected.filter((x) => x !== "Tidy the notes"));
      expect(order.filter((x) => x.startsWith("  "))).toEqual(expected.filter((x) => x.startsWith("  ")));
      expect(folds().map((b) => b.textContent)).toEqual([t("sidebar.side_sessions", { count: expected.filter((x) => x.startsWith("  ")).length })]);
      expect(folds()[0]!.getAttribute("aria-expanded")).toBe("true");
    });
  }

  it("nests them in the chat list's rows too", async () => {
    localStorage.setItem("persona.agentChatList", "1");
    await render("repo");
    expect(group()).toEqual(["Review the PR", "  Can it merge now", "  Side chat"]);
    expect(folds()).toHaveLength(1);
  });

  it("keeps Pinned in the order the user chose, nesting nothing there", async () => {
    pins = { spaces: [], sessions: ["mac/side", "mac/review", "mac/btw"] };
    await refresh();
    await render("repo");
    // the innermost group whose heading says Pinned
    const pinned = [...block().querySelectorAll<HTMLElement>("[data-sidebar-group]")].filter((g) => g.querySelector(":scope > [data-sidebar-group-heading]")?.textContent?.includes(t("sidebar.pinned_section"))).pop()!;
    expect(pinned).toBeTruthy();
    expect(rows(pinned).map((r) => `${r.nested ? "  " : ""}${r.title}`)).toEqual(["Side chat", "Review the PR", "Can it merge now"]);
    expect(folds()).toHaveLength(0);
  });

  it("folds only the side sessions, keeps the one that needs you, and keeps the choice", async () => {
    await render("repo");
    const before = counts();
    // the folder's count and the quick filters' (Needs you 1) are among them
    expect(before.some((x) => /weather.*3/.test(x))).toBe(true);
    expect(before.length).toBeGreaterThan(3);
    act(() => folds()[0]!.click());
    await settle();
    expect(folds()[0]!.getAttribute("aria-expanded")).toBe("false");
    expect(folds()[0]!.textContent).toBe(foldLabel);
    // the side chat is folded away; the question that waits for you stays a row of its own, a level in
    expect(group()).toEqual(["Review the PR", "  Can it merge now"]);
    expect(shown()).toContain(other.title);
    // folding hides rows, not sessions: every count on the list is the same
    expect(counts()).toEqual(before);
    expect(localStorage.getItem('fold:side-sessions:["mac","review"]')).toBe("1");
    // drawn again (another grouping and back), the fold is as it was left
    act(() => root.unmount());
    root = createRoot(host);
    await render("space");
    expect(folds()[0]!.getAttribute("aria-expanded")).toBe("false");
    expect(group()).toEqual(["Review the PR", "  Can it merge now"]);
    act(() => folds()[0]!.click());
    await settle();
    expect(group()).toEqual(["Review the PR", "  Can it merge now", "  Side chat"]);
  });

  it("follows the list's Collapse all and Expand all", async () => {
    await render("repo");
    act(() => setAgentsFolded(true));
    await settle();
    expect(localStorage.getItem('fold:side-sessions:["mac","review"]')).toBe("1");
    // the groups folded with it; opened by hand, the side sessions are still folded but for the one that needs you
    await openGroups();
    expect(folds().map((b) => b.getAttribute("aria-expanded"))).toEqual(["false"]);
    expect(group()).toEqual(["Review the PR", "  Can it merge now"]);
    act(() => setAgentsFolded(false));
    await settle();
    expect(folds().map((b) => b.getAttribute("aria-expanded"))).toEqual(["true"]);
    expect(group()).toEqual(["Review the PR", "  Can it merge now", "  Side chat"]);
  });

  it("Expand all opens a fold that is not drawn now", () => {
    localStorage.setItem('fold:side-sessions:["mac","review"]', "1");
    setAgentsFolded(false);
    expect(localStorage.getItem('fold:side-sessions:["mac","review"]')).toBe("0");
  });

  it("opens the fold while a search or a filter is on the list, and keeps the kept choice", async () => {
    await render("repo");
    act(() => folds()[0]!.click());
    await settle();
    expect(group()).not.toContain("  Side chat");
    // ⌘K's words put on the list (#6589): what they find is in view
    act(() => setAgentListFilters({ ...DEFAULT_DIMENSION_FILTERS, query: "weather" }));
    await settle();
    expect(folds()[0]!.getAttribute("aria-expanded")).toBe("true");
    expect(shown()).toEqual(["Review the PR", "  Can it merge now", "  Side chat"]);
    expect(localStorage.getItem('fold:side-sessions:["mac","review"]')).toBe("1");
  });
});
