// @vitest-environment jsdom
// The rail's own drawing rules (lib/nav-rail.test.ts) are pure; these pin what the component does around them: a chat
// below the tick minimum, and a touch screen (no precise pointer), measure nothing (mounting draws no extra frame, and
// scrolling a long touch chat does no layout work — the panes-renders regression), while a wide chat with enough
// questions draws a tick per question after its first measure.
import { act, Profiler } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NAV_RAIL_MIN_TICKS, type NavRailTick } from "../../lib/nav-rail";

// `matches` is read live: viewport.ts caches the MediaQueryList, so a later test can flip the pointer without a new list
const finePointer = vi.hoisted(() => {
  const state = { matches: true };
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = (query: string) => ({
    get matches() {
      return query === "(any-pointer: fine)" ? state.matches : true;
    },
    addEventListener() {},
    removeEventListener() {},
  });
  g.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  return state;
});

const { NavRail } = await import("./NavRail");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const tick = (i: number): NavRailTick => ({ id: `seg:${i}`, preview: `question ${i}` });
// jsdom reports every box as 0: a scroller wide and tall enough for the rail needs real numbers
const scrollerWith = (width: number, height: number) => {
  const el = document.createElement("div");
  Object.defineProperty(el, "clientWidth", { value: width });
  Object.defineProperty(el, "clientHeight", { value: height });
  return el;
};
// the rail measures on requestAnimationFrame, which jsdom runs as a ~16 ms timer
const settle = async () => {
  for (let i = 0; i < 3; i++) await act(async () => { await new Promise((r) => setTimeout(r, 20)); });
};

let host: HTMLDivElement;
let root: Root;
let commits: number;
beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  commits = 0;
});
afterEach(() => {
  finePointer.matches = true;
  act(() => root.unmount());
  host.remove();
});

describe("NavRail", () => {
  it("draws a tick per question in a wide chat once measured", async () => {
    const sc = scrollerWith(1200, 600);
    act(() => root.render(
      <Profiler id="rail" onRender={() => commits++}>
        <NavRail ticks={[tick(1), tick(2), tick(3), tick(4)]} scroller={sc} onJump={() => {}} reset="mac/s1" />
      </Profiler>,
    ));
    await settle();
    const buttons = [...host.querySelectorAll("[data-nav-rail] button[data-tick]")];
    expect(buttons).toHaveLength(4);
    expect(commits).toBeGreaterThan(1); // the mount, then the measure that draws it
  });

  it("measures nothing for a chat below the tick minimum", async () => {
    const sc = scrollerWith(1200, 600);
    act(() => root.render(
      <Profiler id="rail" onRender={() => commits++}>
        <NavRail ticks={Array.from({ length: NAV_RAIL_MIN_TICKS - 1 }, (_, i) => tick(i + 1))} scroller={sc} onJump={() => {}} reset="mac/s1" />
      </Profiler>,
    ));
    const mounted = commits;
    await settle();
    expect(commits).toBe(mounted); // no measure state, no redraw — whatever the scroller's size
    expect(host.querySelector("[data-nav-rail]")).toBeNull();
  });

  it("measures nothing on a touch screen, including while the chat scrolls", async () => {
    finePointer.matches = false;
    let reads = 0;
    const sc = document.createElement("div");
    Object.defineProperty(sc, "clientWidth", { get() { reads++; return 1200; } });
    Object.defineProperty(sc, "clientHeight", { get() { reads++; return 600; } });
    const boxes = vi.spyOn(sc, "getBoundingClientRect");
    const lines = vi.spyOn(sc, "querySelectorAll");
    act(() => root.render(
      <Profiler id="rail" onRender={() => commits++}>
        <NavRail ticks={Array.from({ length: 8 }, (_, i) => tick(i + 1))} scroller={sc} onJump={() => {}} reset="mac/s1" />
      </Profiler>,
    ));
    await settle();
    const mounted = commits;
    sc.dispatchEvent(new Event("scroll"));
    await settle();
    expect(host.querySelector("[data-nav-rail]")).toBeNull();
    expect(commits).toBe(mounted); // scrolling writes no state
    expect(reads).toBe(0); // no clientWidth / clientHeight
    expect(boxes).not.toHaveBeenCalled();
    expect(lines).not.toHaveBeenCalled();
  });
});
