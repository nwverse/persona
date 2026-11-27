import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { alertText, droppedOn, moved, scheduleText, sparkPoints, trendValues, watchTone, type Watch, type WatchDisplay } from "../../lib/watches";
import { WatchCard } from "./WatchCard";

const NOW = Date.parse("2026-11-20T09:00:00Z");

function card(display: WatchDisplay, latest?: Watch["latest"], extra: Partial<Watch> = {}): Watch {
  return {
    id: "w_1", title: "Open orders", question: "How many orders wait in the Garden app shop?", display, unit: display === "number" || display === "trend" ? "orders" : undefined,
    schedule: { repeat: "daily", time: "08:30" }, state: "active", task_id: "watch-w_1", latest,
    history: [3, 5, 4, 8].map((n, i) => ({ at: `2026-11-1${i + 6}T08:30:00Z`, number: n })), ...extra,
  };
}

describe("watch card", () => {
  const kinds: [WatchDisplay, Watch["latest"], Watch["latest"]][] = [
    ["number", { at: "2026-11-20T08:30:00Z", number: 8, text: "8 orders waiting" }, { at: "2026-11-20T08:30:00Z", number: 31, text: "31 orders waiting", alert: true }],
    ["trend", { at: "2026-11-20T08:30:00Z", number: 8, text: "Up from 4" }, { at: "2026-11-20T08:30:00Z", number: 31, text: "Spike", alert: true }],
    ["status", { at: "2026-11-20T08:30:00Z", status: "ok", text: "Docs site is up" }, { at: "2026-11-20T08:30:00Z", status: "bad", text: "Docs site is down", alert: true }],
    ["table", { at: "2026-11-20T08:30:00Z", text: "2 of 3 done", table: { columns: ["Step", "State"], rows: [["Docs", "done"], ["Store page", "open"]] } },
      { at: "2026-11-20T08:30:00Z", text: "Blocked", alert: true, table: { columns: ["Step", "State"], rows: [["Review", "blocked"]] } }],
  ];
  for (const [display, normal, alert] of kinds) {
    it(`draws ${display} empty, normal and alert`, () => {
      const empty = renderToStaticMarkup(<WatchCard w={card(display)} now={NOW} />);
      expect(empty).toContain("data-watch-empty");
      expect(empty).toContain('data-watch-tone="empty"');

      const ok = renderToStaticMarkup(<WatchCard w={card(display, normal)} now={NOW} />);
      expect(ok).not.toContain("data-watch-empty");
      expect(ok).toContain(normal!.text!);
      expect(ok).not.toContain("ring-red");
      if (display === "trend") expect(ok).toContain("data-watch-spark");
      if (display === "table") expect(ok).toContain("<table");
      if (display === "number") expect(ok).toContain(">8<");

      const bad = renderToStaticMarkup(<WatchCard w={card(display, alert)} now={NOW} />);
      expect(bad).toContain('data-watch-tone="alert"');
      expect(bad).toContain("ring-red");
    });
  }

  it("draws a draft with its preview and the two choices", () => {
    const html = renderToStaticMarkup(<WatchCard w={card("number", undefined, { state: "draft", task_id: undefined, alert: { when: "above", value: 20 } })} now={NOW} onConfirm={() => {}} onDiscard={() => {}} />);
    expect(html).toContain("border-dashed");
    expect(html).toContain(alertText({ when: "above", value: 20 }, "orders"));
    expect(html).toContain("<button");
  });
});

describe("watch helpers", () => {
  it("reorders by moving and by dropping", () => {
    expect(moved(["a", "b", "c"], "c", -1)).toEqual(["a", "c", "b"]);
    expect(moved(["a", "b"], "a", -1)).toEqual(["a", "b"]);
    expect(droppedOn(["a", "b", "c"], "a", "c")).toEqual(["b", "c", "a"]);
    expect(droppedOn(["a", "b", "c"], "c", "a")).toEqual(["c", "a", "b"]);
  });

  it("writes the schedule back as the editor takes it", () => {
    expect(scheduleText(card("number"))).toBe("08:30");
    expect(scheduleText(card("number", undefined, { schedule: { repeat: "interval", every_minutes: 120 } }))).toBe("2h");
    expect(scheduleText(card("number", undefined, { schedule: { repeat: "cron", cron: "0 9 * * 1-5" } }))).toBe("0 9 * * 1-5");
  });

  it("tones and trends", () => {
    expect(watchTone(card("status", { at: "x", status: "warn", text: "?" }))).toBe("warn");
    expect(trendValues(card("trend"))).toEqual([3, 5, 4, 8]);
    expect(sparkPoints([1, 1], 10, 10).split(" ")).toHaveLength(2);
    expect(sparkPoints([], 10, 10)).toBe("");
  });
});
