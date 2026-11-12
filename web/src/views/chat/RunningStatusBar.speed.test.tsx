// @vitest-environment jsdom
// The running bar's speed and its end (#5312, cindy's RunningStatusBar and RunningTokenRatePopover): the latest speed
// replaces the tokens once one is measured and goes while the count stands still, the speed opens the history card on
// hover and pins it on a click (✕ and Esc close it), a pinned card outlives the turn, the history is kept per
// conversation, and a finished turn says Done before the bar fades.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});

import type { SessionLive } from "../../lib/types";
import { clearRateHistories } from "../../lib/token-rate";
import { FADE_OUT_MS, LINGER_MS } from "../../ui/motion";
import { RunningStatusView } from "./RunningStatusBar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = Date.parse("2026-11-12T10:00:00Z");
let box: HTMLDivElement;
let root: Root;
beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(T0);
  clearRateHistories();
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
});
afterEach(() => {
  act(() => root.unmount());
  box.remove();
  vi.useRealTimers();
});

const line = (s: number, tokens: number): SessionLive => ({ verb: "Frosting", elapsed_s: s, tokens_down: tokens, at: new Date(T0 + s * 1000).toISOString() });
const bar = (live: SessionLive | null, working = true, key = "m/s") => <RunningStatusView live={live} working={working} startedAt="2026-11-12T09:59:59Z" speedKey={key} />;
const draw = (ui: React.ReactElement) => act(() => root.render(ui));
const tick = (ms: number) => act(async () => { await vi.advanceTimersByTimeAsync(ms); });
/** Readings one second apart, 50 tokens each (after the first, `line(1, 50)`, the rise at 2 s is where measuring starts). */
async function run(from: number, to: number, key = "m/s") {
  for (let s = from; s <= to; s++) {
    vi.setSystemTime(T0 + s * 1000);
    draw(bar(line(s, s * 50), true, key));
  }
}
const q = (sel: string) => document.querySelector<HTMLElement>(sel);
const speed = () => q("[data-running-speed]");
const card = () => q("[data-speed-card]");
const pointer = (type: string, el: Element) => act(() => {
  const e = new MouseEvent(type, { bubbles: true, relatedTarget: document.body });
  Object.defineProperty(e, "pointerType", { value: "mouse" });
  el.dispatchEvent(e);
});
const click = (el: Element) => act(() => el.dispatchEvent(new MouseEvent("click", { bubbles: true })));
const key = (k: string) => act(() => document.body.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true })));
/** The speed button and its card load once an Agent's turn runs. */
const loaded = () => act(async () => { await import("./RunningSpeedCard"); await vi.advanceTimersByTimeAsync(0); });

describe("the running bar's speed (#5312)", () => {
  it("shows the latest speed or the tokens, never both, and drops the speed once the count stands still", async () => {
    draw(bar(line(1, 50)));
    await loaded();
    expect(q("[data-running-rate]")).toBeNull();
    expect(speed()).toBeNull();
    await run(2, 3);
    expect(q("[data-running-rate]")?.textContent).toBe("50 tok/s");
    expect(q("[data-running-tokens]")).toBeNull();
    expect(speed()?.textContent).toBe("3s·50 tok/s");
    // a tool runs: the count holds, so after GAP_MS there is no speed, only the tokens
    await tick(4000);
    expect(q("[data-running-rate]")).toBeNull();
    expect(q("[data-running-tokens]")).not.toBeNull();
    expect(speed()).toBeNull();
  });

  it("opens the history on hover, pins it on a click with a ✕, and closes it with ✕ or Esc", async () => {
    draw(bar(line(1, 50)));
    await loaded();
    await run(2, 4);
    pointer("pointerover", speed()!);
    expect(card()).toBeNull();
    await tick(500);
    await loaded();
    expect(card()?.dataset.speedCard).toBe("hover");
    expect(card()?.getAttribute("role")).toBe("tooltip");
    expect(q("[data-speed-latest]")?.textContent).toBe("50");
    expect(q("[data-speed-average]")?.textContent).toBe("50 tok/s");
    expect(q("[data-speed-output]")?.textContent).toBe("200 tok");
    expect(q("[data-speed-peak]")?.textContent).toBe("50 tok/s");
    expect(q("[data-speed-chart]")?.dataset.speedChart).toBe("2");
    expect(q("[data-speed-close]")).toBeNull();
    click(speed()!);
    expect(card()?.dataset.speedCard).toBe("pinned");
    expect(card()?.getAttribute("role")).toBe("dialog");
    // pinned, the pointer leaving does not close it
    pointer("pointerout", speed()!);
    await tick(200);
    expect(card()?.dataset.speedCard).toBe("pinned");
    key("Escape");
    expect(card()).toBeNull();
    click(speed()!);
    expect(card()?.dataset.speedCard).toBe("pinned");
    click(q("[data-speed-close]")!);
    expect(card()).toBeNull();
    // closed by hand: the same hover does not bring it back
    await tick(600);
    expect(card()).toBeNull();
  });

  it("keeps a pinned card and its button after the turn, until it is closed", async () => {
    draw(bar(line(1, 50)));
    await loaded();
    await run(2, 3);
    click(speed()!);
    await loaded();
    expect(card()?.dataset.speedCard).toBe("pinned");
    draw(bar(null, false));
    await tick(LINGER_MS + FADE_OUT_MS + 50);
    expect(q('[data-running-status="off"]')).not.toBeNull();
    expect(speed()).not.toBeNull();
    expect(card()).not.toBeNull();
    click(q("[data-speed-close]")!);
    expect(q("[data-running-status]")).toBeNull();
  });

  it("keeps the history per conversation while the page is open", async () => {
    draw(bar(line(1, 50)));
    await loaded();
    await run(2, 4);
    draw(<></>);
    draw(bar(line(5, 250), true, "m/other"));
    expect(speed()).toBeNull();
    draw(<></>);
    // back, the count moving again: the speed measured before is still the latest (under a minute old), until the
    // next measurement replaces it
    vi.setSystemTime(T0 + 30_000);
    draw(bar(line(30, 2000)));
    expect(q("[data-running-rate]")?.textContent).toBe("50 tok/s");
    vi.setSystemTime(T0 + 31_000);
    draw(bar(line(31, 2040)));
    click(speed()!);
    await loaded();
    expect(q("[data-speed-chart]")?.dataset.speedChart).toBe("3");
    expect(q("[data-speed-latest]")?.textContent).toBe("40");
  });

  it("times readings by when they arrive: the node's whole seconds are too coarse", async () => {
    // every reading says the same second: read by it, no stretch could be measured
    const coarse = (s: number): SessionLive => ({ ...line(s, s * 50), at: "2026-11-12T10:00:00Z" });
    draw(bar(coarse(1)));
    await loaded();
    for (let s = 2; s <= 3; s++) {
      vi.setSystemTime(T0 + s * 1000);
      draw(bar(coarse(s)));
    }
    expect(q("[data-running-rate]")?.textContent).toBe("50 tok/s");
  });
});

describe("the end of a turn", () => {
  it("says Done with a check for the linger second, then fades and goes", async () => {
    draw(bar(line(3, 150)));
    expect(q("[data-running-done]")).toBeNull();
    draw(bar(null, false));
    const done = q("[data-running-done]");
    expect(done?.textContent).toBe("已完成");
    expect(done?.className).toContain("status-done");
    expect(q('[data-running-status="linger"]')).not.toBeNull();
    await tick(LINGER_MS);
    expect(q('[data-running-status="fading"]')).not.toBeNull();
    await tick(FADE_OUT_MS);
    expect(q("[data-running-status]")).toBeNull();
  });

  it("says no Done after background subagents or a workflow", async () => {
    draw(<RunningStatusView live={null} working subtasks />);
    draw(<RunningStatusView live={null} working={false} subtasks />);
    expect(q("[data-running-done]")).toBeNull();
    expect(q("[data-running-background]")?.dataset.runningBackground).toBe("subtasks");
  });
});
