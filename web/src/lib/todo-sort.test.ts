import { describe, expect, it } from "vitest";
import { chosenMoves, initialPicks, isUnsorted, sureness, TEMPORARY, type SortSuggestion } from "./todo-sort";

const item = (p: Partial<SortSuggestion>): SortSuggestion => ({ todo: "t", title: "T", project: "", confidence: 0, preselect: false, ...p });

describe("todo sort", () => {
  it("counts open to-dos in no project and not Temporary as unsorted, as the hub does", () => {
    expect(isUnsorted({ project: "" })).toBe(true);
    expect(isUnsorted({ project: "garden-app" })).toBe(false);
    expect(isUnsorted({ temporary: true })).toBe(false);
    expect(isUnsorted({ done_at: "2026-11-01T00:00:00Z" })).toBe(false);
    expect(isUnsorted({ kind: "concern" })).toBe(false);
  });

  it("ticks the sure suggestions only, and writes the ticked ones with a target", () => {
    const items = [
      item({ todo: "a", project: "garden-app", confidence: 0.9, preselect: true }),
      item({ todo: "b", temporary: true, confidence: 0.8, preselect: true }),
      item({ todo: "c", project: "docs-site", confidence: 0.4 }),
      item({ todo: "d" }),
    ];
    const picks = initialPicks(items);
    expect(picks.get("a")).toEqual({ on: true, target: "garden-app" });
    expect(picks.get("b")).toEqual({ on: true, target: TEMPORARY });
    expect(picks.get("c")).toEqual({ on: false, target: "docs-site" });
    expect(picks.get("d")).toEqual({ on: false, target: "" });
    picks.set("c", { on: true, target: "q4-launch" }); // the owner changed it
    picks.set("d", { on: true, target: "" }); // ticked with no target: nothing to write
    expect(chosenMoves(items, picks)).toEqual([
      { id: "a", project: "garden-app" },
      { id: "b", temporary: true },
      { id: "c", project: "q4-launch" },
    ]);
  });

  it("names how sure", () => {
    expect([sureness(0.85), sureness(0.6), sureness(0.3)]).toEqual(["sure", "fairly", "unsure"]);
  });
});
