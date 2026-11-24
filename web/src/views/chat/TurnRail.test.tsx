import { createRef } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { MAX_RAIL_TURNS, pickActiveTurnId, sliceRailItems, TurnRail, type TurnRailItem } from "./TurnRail";

function makeItems(count: number): TurnRailItem[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `t-${i + 1}`,
    prompt: `Prompt number ${i + 1}`,
    reply: `Reply number ${i + 1}`,
  }));
}

describe("TurnRail", () => {
  it("returns null when fewer than 2 items are provided", () => {
    const ref = createRef<HTMLElement>();
    expect(renderToStaticMarkup(<TurnRail items={[]} scrollerRef={ref} />)).toBe("");
    expect(renderToStaticMarkup(<TurnRail items={makeItems(1)} scrollerRef={ref} />)).toBe("");
  });

  it("renders desktop-only rail with ticks, active state, and working state", () => {
    const ref = createRef<HTMLElement>();
    const items: TurnRailItem[] = [
      { id: "t-1", prompt: "First question", reply: "First answer" },
      { id: "t-2", prompt: "Second question", working: true },
      { id: "t-3", prompt: "Third question" },
    ];
    const html = renderToStaticMarkup(<TurnRail items={items} scrollerRef={ref} activeId="t-1" />);
    expect(html).toContain("hidden lg:flex");
    expect(html).toContain('aria-current="step"');
    expect(html).toContain("w-5 bg-ink");
    expect(html).toContain("w-4 bg-accent animate-pulse");
    expect(html).toContain("w-3 bg-ink-3/45");
    expect(html).toContain("跳转到第 1 轮");
    expect(html).toContain("跳转到第 3 轮");
  });

  it("caps displayed ticks to the latest 30 turns and preserves 1-based turn indices", () => {
    const items = makeItems(35);
    const { visible, startIndex } = sliceRailItems(items);
    expect(visible).toHaveLength(MAX_RAIL_TURNS);
    expect(startIndex).toBe(6);
    expect(visible[0].id).toBe("t-6");
    expect(visible[visible.length - 1].id).toBe("t-35");

    const ref = createRef<HTMLElement>();
    const html = renderToStaticMarkup(<TurnRail items={items} scrollerRef={ref} />);
    expect(html).not.toContain("跳转到第 5 轮");
    expect(html).toContain("跳转到第 6 轮");
    expect(html).toContain("跳转到第 35 轮");
  });
});

describe("pickActiveTurnId", () => {
  it("returns null for empty list", () => {
    expect(pickActiveTurnId([], 0, 600)).toBeNull();
  });

  it("picks the turn spanning or closest to the upper third of the viewport", () => {
    // Viewport: 0..600 -> upper third anchor is y = 200
    const rects = [
      { id: "t-1", top: -100, bottom: 120 },
      { id: "t-2", top: 140, bottom: 320 },
      { id: "t-3", top: 340, bottom: 550 },
    ];
    expect(pickActiveTurnId(rects, 0, 600)).toBe("t-2");
  });

  it("picks the closest visible turn when none directly spans the anchor line", () => {
    // Viewport: 0..600 -> anchor is y = 200
    const rects = [
      { id: "t-1", top: 20, bottom: 100 }, // dist 100
      { id: "t-2", top: 230, bottom: 350 }, // dist 30
    ];
    expect(pickActiveTurnId(rects, 0, 600)).toBe("t-2");
  });

  it("falls back to nearest off-screen turn if nothing intersects viewport", () => {
    const rects = [
      { id: "t-1", top: -500, bottom: -300 },
      { id: "t-2", top: -250, bottom: -50 },
    ];
    expect(pickActiveTurnId(rects, 0, 600)).toBe("t-2");
  });
});
