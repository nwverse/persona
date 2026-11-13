// @vitest-environment jsdom
// The direction mark's one busy expression (#6626): a spinner driven by the hub's live-session flag, never a spelled
// working count; the needs-you count stays as words and the full phrase survives for assistive tech.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n } from "../i18n";
import type { DirectionCounts } from "../lib/direction-tree";
import { DirectionCountsText } from "./DirectionMarks";

vi.hoisted(() => {
  window.matchMedia = ((media: string) => ({
    matches: false, media, onchange: null,
    addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false,
  })) as typeof window.matchMedia;
});

let host: HTMLDivElement;
let root: Root;

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const counts = (patch: Partial<DirectionCounts> = {}): DirectionCounts => ({ working: 0, needs_you: 0, stuck: 0, review: 0, ...patch });

beforeEach(async () => {
  await initI18n("zh-Hans");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});

const render = async (element: React.ReactNode) => {
  await act(async () => root.render(element));
};

describe("DirectionCountsText", () => {
  it("renders nothing when no count is non-zero and nothing runs", async () => {
    await render(<DirectionCountsText counts={counts()} />);
    expect(host.querySelector("[data-direction-counts]")).toBeNull();
  });

  it("still draws the arc from the live flag when every count is zero (#6626 defensive case)", async () => {
    await render(<DirectionCountsText counts={counts()} working />);
    const mark = host.querySelector("[data-direction-counts]")!;
    expect(mark).not.toBeNull();
    expect(mark.querySelector('[data-shape="arc"][data-state="working"]')).not.toBeNull();
    expect(mark.querySelector('[aria-hidden="true"]')!.textContent).toBe("");
  });

  it("shows the spinner and the needs-you words but never a visible working count", async () => {
    await render(<DirectionCountsText counts={counts({ working: 2, needs_you: 1 })} working />);
    const mark = host.querySelector("[data-direction-counts]")!;
    expect(mark.querySelector('[data-shape="arc"][data-state="working"]')).not.toBeNull();
    const visible = mark.querySelector('[aria-hidden="true"]')!;
    expect(visible.textContent).not.toContain("在做");
    expect(visible.textContent).toContain("等你 1");
    // the full phrase stays reachable for screen readers and the title tooltip
    expect(mark.querySelector(".sr-only")!.textContent).toContain("在做 2");
    expect(mark.getAttribute("title")).toContain("在做 2");
  });

  it("falls back to the task count for the arc when the live flag is absent (older hub)", async () => {
    await render(<DirectionCountsText counts={counts({ working: 1 })} />);
    expect(host.querySelector('[data-shape="arc"][data-state="working"]')).not.toBeNull();
  });

  it("does not spin on an explicit false even with a triaged working task count", async () => {
    await render(<DirectionCountsText counts={counts({ working: 1 })} working={false} />);
    expect(host.querySelector("[data-direction-counts] [data-shape='arc']")).toBeNull();
  });
});
