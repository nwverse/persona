// @vitest-environment jsdom
// The complete Agents list with hundreds of Agents (the sidebar and the phone's Agents page): a folded group mounts no
// rows, an open long group draws a bounded number of them (its first rows, the parts near the view and the part
// holding the open row), and a group heading's height comes from a ResizeObserver, never from a layout read on mount.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

type Observed = { callback: (entries: unknown[]) => void; targets: Set<Element> };
const { intersections, sizes } = vi.hoisted(() => {
  const intersections: Observed[] = [];
  const sizes: Observed[] = [];
  const observer = (list: Observed[]) => class {
    o: Observed;
    constructor(callback: (entries: unknown[]) => void) { this.o = { callback, targets: new Set() }; list.push(this.o); }
    observe(el: Element) { this.o.targets.add(el); }
    unobserve(el: Element) { this.o.targets.delete(el); }
    disconnect() { this.o.targets.clear(); }
  };
  globalThis.ResizeObserver = observer(sizes) as unknown as typeof ResizeObserver;
  globalThis.IntersectionObserver = observer(intersections) as unknown as typeof IntersectionObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
  return { intersections, sizes };
});
import { buildTodoTree } from "../lib/todo-tree";
import type { Machine, Session } from "../lib/types";
import { SESSION_LIST_METRICS } from "../ui/list-metrics";
import { Section, SidebarGroup } from "./SidebarSection";
import { TodoTreeView } from "./SidebarTree";
import { WindowedRows } from "./WindowedRows";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { windowHead: HEAD, windowPart: PART } = SESSION_LIST_METRICS;
const at = new Date(Date.now() - 3 * 86_400_000).toISOString();
const machines: Machine[] = [{ id: "m1", name: "Mac", online: true }];
// 400 Agents on no to-do, all done and seen, as the audit's hub had them
const many: Session[] = Array.from({ length: 400 }, (_, i) => ({ machine_id: "m1", session_id: `s${i}`, title: `Session ${i}`, cwd: "/code/app", status: "idle", state: "idle", updated_at: at }));
const stable = { reg: () => () => {}, hover: () => {}, go: () => () => {}, onRowMenu: () => {} };

let host: HTMLDivElement;
let root: Root;
const settle = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};
const agentLinks = () => [...host.querySelectorAll<HTMLAnchorElement>('a[href^="/s/"]')];
const parts = () => [...host.querySelectorAll<HTMLElement>("[data-rows-part]")];
/** The list observer reports part `i` near the view (or not), as the browser would on a scroll. */
const near = (i: number, isIntersecting: boolean) => {
  const o = intersections.find((x) => [...x.targets].some((el) => (el as HTMLElement).dataset.rowsPart === String(i)))!;
  const target = [...o.targets].find((el) => (el as HTMLElement).dataset.rowsPart === String(i))!;
  act(() => o.callback([{ target, isIntersecting }]));
};
/** The size observer watching `el` reports its height. */
const resized = (el: Element, blockSize: number) => {
  const o = sizes.find((x) => x.targets.has(el))!;
  act(() => o.callback([{ target: el, borderBoxSize: [{ blockSize, inlineSize: 300 }] }]));
};

beforeEach(() => {
  localStorage.clear();
  host = document.createElement("div");
  host.setAttribute("data-row-scroller", "");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("a long list of Agents", () => {
  it("mounts no rows under a folded group", async () => {
    localStorage.setItem("fold:todo:none", "1");
    const tree = buildTodoTree(many, machines, [], { spaces: [], sessions: [] }, Date.now());
    act(() => root.render(<TodoTreeView tree={tree} machines={machines} activeKey="" {...stable} />));
    await settle();
    expect(host.querySelector('[data-tree-branch="todo:none"] button[aria-expanded]')!.getAttribute("aria-expanded")).toBe("false");
    // before, all 400 were there under the fold, inert
    expect(agentLinks()).toHaveLength(0);
    // opened, its rows come
    act(() => host.querySelector<HTMLButtonElement>('[data-tree-branch="todo:none"] button[aria-expanded]')!.click());
    await settle();
    expect(agentLinks().length).toBeGreaterThan(0);
  });

  it("draws a bounded number of an open group's rows, and keeps the rest's place", async () => {
    localStorage.setItem("fold:todo:none", "0");
    const tree = buildTodoTree(many, machines, [], { spaces: [], sessions: [] }, Date.now());
    act(() => root.render(<TodoTreeView tree={tree} machines={machines} activeKey="" {...stable} />));
    await settle();
    // the first rows, a part each for the rest: before, all 400 rows were drawn at once
    expect(agentLinks()).toHaveLength(HEAD);
    expect(agentLinks()[0]!.textContent).toContain("Session 0");
    expect(parts()).toHaveLength(1 + Math.ceil((400 - HEAD) / PART));
    expect(parts().slice(1).every((p) => p.childElementCount === 0 && Number.parseFloat(p.style.height) > 0)).toBe(true);
  });

  it("draws the open Agent's row and the parts beside it wherever it is", async () => {
    localStorage.setItem("fold:todo:none", "0");
    const tree = buildTodoTree(many, machines, [], { spaces: [], sessions: [] }, Date.now());
    act(() => root.render(<TodoTreeView tree={tree} machines={machines} activeKey="s:m1/s300" {...stable} />));
    await settle();
    const shown = agentLinks().map((a) => a.getAttribute("href"));
    expect(shown).toContain("/s/m1/s300");
    expect(shown.length).toBeLessThanOrEqual(HEAD + 3 * PART);
    // a herdr Agent's page moves on to its tab's address, which no row here holds: its row stays for the second
    // scroll to it
    act(() => root.render(<TodoTreeView tree={tree} machines={machines} activeKey="t:m1/w1/t1" {...stable} />));
    await settle();
    expect(agentLinks().map((a) => a.getAttribute("href"))).toContain("/s/m1/s300");
  });

  it("keeps the part holding the keyboard's focus and the next one drawn", async () => {
    localStorage.setItem("fold:todo:none", "0");
    const tree = buildTodoTree(many, machines, [], { spaces: [], sessions: [] }, Date.now());
    act(() => root.render(<TodoTreeView tree={tree} machines={machines} activeKey="" {...stable} />));
    await settle();
    // Tab reaches the last of the first rows: the next part is there for the next Tab
    const last = agentLinks()[HEAD - 1]!;
    act(() => last.focus());
    expect(agentLinks()).toHaveLength(HEAD + PART);
    expect(document.activeElement).toBe(last);
  });
});

describe("WindowedRows", () => {
  const rows = (n: number) => Array.from({ length: n }, (_, i) => i);
  const render = (i: number) => <p key={i} data-row={i}>row {i}</p>;
  const drawn = () => [...host.querySelectorAll<HTMLElement>("[data-row]")].map((el) => Number(el.dataset.row));

  it("draws a short list as it always was", () => {
    act(() => root.render(<WindowedRows items={rows(HEAD + PART)} render={render} />));
    expect(drawn()).toHaveLength(HEAD + PART);
    expect(parts()).toHaveLength(0);
  });

  it("draws the first rows and an empty box for the rest while its chunk is on its way", () => {
    act(() => root.render(<WindowedRows items={rows(400)} render={render} />));
    expect(drawn()).toEqual(rows(HEAD));
    expect(host.lastElementChild?.getAttribute("aria-hidden")).toBe("true");
  });

  it("draws a part while it is near the view, and keeps its height once it is not", async () => {
    act(() => root.render(<WindowedRows items={rows(400)} render={render} />));
    await settle();
    expect(drawn()).toEqual(rows(HEAD));
    near(3, true);
    const third = parts()[3]!;
    expect(drawn()).toEqual([...rows(HEAD), ...rows(PART).map((i) => HEAD + 2 * PART + i)]);
    resized(third, 517);
    near(3, false);
    expect(drawn()).toEqual(rows(HEAD));
    expect(parts()[3]!.style.height).toBe("517px");
  });

  it("gives a part never drawn the first rows' average height", async () => {
    act(() => root.render(<WindowedRows items={rows(400)} render={render} />));
    await settle();
    resized(parts()[0]!, HEAD * 31);
    expect(parts()[5]!.style.height).toBe(`${PART * 31}px`);
  });
});

describe("a group heading", () => {
  it("takes its height from the observer, with no layout read when it mounts", () => {
    const read = vi.spyOn(HTMLElement.prototype, "getBoundingClientRect");
    act(() => root.render(
      <SidebarGroup heading={<span>Agents</span>}>
        <Section title="Not on a to-do" folded={false} onToggle={() => {}}><p>row</p></Section>
      </SidebarGroup>,
    ));
    expect(read).not.toHaveBeenCalled();
    const [outer, inner] = [...host.querySelectorAll<HTMLElement>("[data-sidebar-group-heading]")];
    expect(inner!.style.top).toBe("calc(var(--sidebar-sticky-top, 0px) + 0px)");
    // the browser's layout reports the outer heading's height: the nested heading sticks under it
    resized(outer!, 36);
    expect(inner!.style.top).toBe("calc(var(--sidebar-sticky-top, 0px) + 36px)");
    read.mockRestore();
  });
});
