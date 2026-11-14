// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { api } from "../../lib/api";
import { loadPages } from "../../lib/lazy-page";
import { SessionList, type ListCounts } from "../../lib/session-list";
import { setAllFolded } from "../../lib/sidebar-api";
import { SESSION_LIST_METRICS } from "../../ui/list-metrics";
import { PagedSessionList } from "./PagedSessionList";

vi.mock("../../lib/api", async (original) => ({ ...await original<typeof import("../../lib/api")>(), api: vi.fn() }));
vi.mock("../../lib/store", () => ({ onHubEvent: () => () => {} }));
// jsdom lays nothing out, so the virtualizer would draw no row: draw every item, in order
vi.mock("@tanstack/react-virtual", () => ({
  useVirtualizer: ({ count, getItemKey }: { count: number; getItemKey: (i: number) => string }) => ({
    getVirtualItems: () => Array.from({ length: count }, (_, index) => ({ index, key: getItemKey(index), start: index * 32 })),
    getTotalSize: () => count * 32,
    measureElement: () => {},
  }),
}));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
// the sections' headings measure themselves with one (jsdom has none)
globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;

type Row = { id: string; updated_at: string; state: string; parent?: string };
const NOW = Date.UTC(2026, 10, 13, 12);
const HOUR = 3_600_000;
const row = (id: string, hoursAgo: number, state = "idle", parent?: string): Row =>
  ({ id, updated_at: new Date(NOW - hoursAgo * HOUR).toISOString(), state, parent });
// newest first, as the hub sends them: two side chats of "main" (one waiting on the user), main itself two days old
const rows = [row("btw-1", 1, "idle", "main"), row("other", 2), row("btw-2", 5, "needs_you", "main"), row("main", 48), row("old", 200)];
const identity = (r: Row) => r.id;
const compare = (a: Row, b: Row) => Date.parse(b.updated_at) - Date.parse(a.updated_at);
const activeAt = (r: Row) => Date.parse(r.updated_at);
const needsYou = (r: Row) => r.state === "needs_you";
const parentOf = (r: Row) => r.parent;
// one computer, so Browse by Computer draws one section of them all
const counts: ListCounts = { total: 5, state: { needs_you: 1, idle: 4 }, machine: { mac: 5 }, project: {}, group: {}, revision: "r" };

let host: HTMLDivElement;
let root: Root;
let list: SessionList<Row>;

beforeEach(() => {
  vi.useFakeTimers({ now: NOW });
  localStorage.clear();
  vi.mocked(api).mockImplementation((path) => {
    const params = new URL(path, "https://example.com").searchParams;
    return Promise.resolve(params.has("counts") ? counts
      : { sessions: rows, remove: [], has_more: false, total: 5, revision: "r" }) as ReturnType<typeof api>;
  });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  list = new SessionList<Row>("/sessions", identity, compare);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.body.innerHTML = "";
  vi.useRealTimers();
});

async function show(withParents = true) {
  await list.load();
  await act(async () => root.render(<PagedSessionList root={list} path="/sessions" identity={identity} compare={compare} event="sessions_list"
    activeAt={activeAt} needsYou={needsYou} parentOf={withParents ? parentOf : undefined} renderRow={(r) => <span>{r.id}</span>} />));
}
const shown = () => [...host.querySelectorAll("[data-session-row]")].map((el) => el.textContent);
const nested = () => [...host.querySelectorAll("[data-nested-session]")].map((el) => el.textContent);
const total = () => host.querySelector("[data-session-total]")?.textContent;
const chip = (state: string) => host.querySelector(`[role="radiogroup"] button[aria-label^="${state}"]`)?.getAttribute("aria-label");

describe("side sessions in the paged list (#5935)", () => {
  it("draws each under its parent, a level in, the group where its newest member was", async () => {
    await show();
    expect(shown()).toEqual(["main", "btw-1", "btw-2", "other", "old"]);
    expect(nested()).toEqual(["btw-1", "btw-2"]);
  });

  it("keeps every row and count: nesting only moves rows", async () => {
    await show(false);
    const flat = { total: total(), needs: chip(t("lib-state-labels.needs_you")), rows: [...shown()].sort() };
    expect(nested()).toEqual([]);
    act(() => root.unmount());
    root = createRoot(host);
    await show();
    expect(total()).toBe(flat.total);
    expect(total()).toBe(t("paged-agents.count", { count: 5 }));
    expect(chip(t("lib-state-labels.needs_you"))).toBe(flat.needs);
    expect(flat.needs).toBe(`${t("lib-state-labels.needs_you")} 1`);
    expect([...shown()].sort()).toEqual(flat.rows);
    // the side session that waits on the user is a row of its own, in view without opening anything
    expect(shown()).toContain("btw-2");
  });

  it("never splits a group with Earlier: the group is as recent as its newest member", async () => {
    localStorage.setItem("persona.agentActiveWindow", "1d");
    await show();
    // main is two days old, but its side chat moved an hour ago: the group stays in the recent part
    expect(shown()).toEqual(["main", "btw-1", "btw-2", "other"]);
    const header = host.querySelector<HTMLElement>("[data-earlier] button");
    expect(header?.textContent).toBe(`${t("paged-agents.earlier")}1`);
  });
});

// #7755: "Side sessions (N)" under the parent folds its side sessions, kept per parent, reached by fold-all, opened by a
// search; a folded one that needs the user stays a row, and no count changes.
const visible = () => [...host.querySelectorAll("[data-session-row]")].filter((el) => !el.closest("[inert]")).map((el) => `${el.querySelector("[data-nested-session]") ? "  " : ""}${el.textContent}`);
const fold = () => host.querySelector<HTMLButtonElement>("[data-side-fold] button");

describe("folding side sessions in the paged list (#7755)", () => {
  it("draws the fold under the parent, open by default", async () => {
    await show();
    expect(fold()?.textContent).toBe(t("sidebar.side_sessions", { count: 2 }));
    expect(fold()?.getAttribute("aria-expanded")).toBe("true");
    // the fold sits between the parent and its side sessions
    const group = host.querySelector("[data-side-group]")!;
    expect([...group.querySelectorAll("[data-session-row], [data-side-fold]")].map((el) => el.matches("[data-side-fold]") ? "fold" : el.textContent)).toEqual(["main", "fold", "btw-1", "btw-2"]);
    // the fold and the side sessions are one block a level in
    expect(group.querySelector("[data-side-sessions]")?.textContent).toBe(`${fold()!.textContent}btw-1btw-2`);
    expect(visible()).toEqual(["main", "  btw-1", "  btw-2", "other", "old"]);
  });

  it("folds only the side sessions, keeps the one that needs you, and changes no count", async () => {
    await show();
    const before = { total: total(), needs: chip(t("lib-state-labels.needs_you")) };
    act(() => fold()!.click());
    expect(fold()?.getAttribute("aria-expanded")).toBe("false");
    expect(visible()).toEqual(["main", "  btw-2", "other", "old"]);
    expect({ total: total(), needs: chip(t("lib-state-labels.needs_you")) }).toEqual(before);
    expect(localStorage.getItem("fold:side-sessions:main")).toBe("1");
    // drawn again, the fold is as it was left
    act(() => root.unmount());
    root = createRoot(host);
    await show();
    expect(fold()?.getAttribute("aria-expanded")).toBe("false");
    expect(visible()).toEqual(["main", "  btw-2", "other", "old"]);
  });

  it("follows Collapse all and Expand all", async () => {
    await show();
    act(() => setAllFolded(true));
    expect(fold()?.getAttribute("aria-expanded")).toBe("false");
    expect(visible()).toEqual(["main", "  btw-2", "other", "old"]);
    act(() => setAllFolded(false));
    expect(fold()?.getAttribute("aria-expanded")).toBe("true");
    expect(visible()).toEqual(["main", "  btw-1", "  btw-2", "other", "old"]);
  });

  it("opens the fold while a search is on the list, without changing the kept choice", async () => {
    localStorage.setItem("fold:side-sessions:main", "1");
    await show();
    expect(visible()).not.toContain("  btw-1");
    const field = host.querySelector<HTMLInputElement>("input")!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(field, "btw");
      field.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => { vi.advanceTimersByTime(SESSION_LIST_METRICS.searchDelay + 10); });
    for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
    expect(fold()?.getAttribute("aria-expanded")).toBe("true");
    expect(visible()).toContain("  btw-1");
    expect(localStorage.getItem("fold:side-sessions:main")).toBe("1");
  });
});

describe("folding side sessions in a Browse by section (#7755)", () => {
  beforeAll(() => loadPages(), 60_000);

  it("nests and folds them as the flat list does", async () => {
    localStorage.setItem("persona.agentBrowseBy", "machine");
    await show();
    for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
    const section = host.querySelector("[data-session-group]")!;
    expect(section).toBeTruthy();
    expect(visible()).toEqual(["main", "  btw-1", "  btw-2", "other", "old"]);
    expect(fold()?.textContent).toBe(t("sidebar.side_sessions", { count: 2 }));
    act(() => fold()!.click());
    expect(visible()).toEqual(["main", "  btw-2", "other", "old"]);
    // the section's count is the hub's either way
    expect(section.querySelector("[data-sidebar-group-heading]")?.textContent).toContain("5");
    expect(localStorage.getItem("fold:side-sessions:main")).toBe("1");
  });
});
