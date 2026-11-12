// Ported from cindy's runningTokenRateHistory tests (apps/desktop/src/renderer/__tests__), on the status line's count
// over time instead of cindy's generation-only clock: two rises at most GAP_MS apart are generation, a longer gap is not.
import { describe, expect, it } from "vitest";
import { clearRateHistories, emptyRateHistory, formatRate, FRESH_MS, GAP_MS, loadRateHistory, recordTokens, saveRateHistory, shownRate, turnAverage, type RateHistory } from "./token-rate";

/** Readings of one turn: [tokens, ms] pairs, the page's clock equal to the node's. */
function feed(h: RateHistory, readings: [number, number][], turn: string | null = "t1"): RateHistory {
  return readings.reduce((acc, [tokens, at]) => recordTokens(acc, { turn, tokens, at, now: at }), h);
}
const begin = () => feed(emptyRateHistory(), [[0, 0]]);

describe("the speed history", () => {
  it("measures each stretch between readings, not the turn's running average", () => {
    const first = feed(begin(), [[100, 1000]]);
    const next = feed(first, [[150, 2000]]);
    expect(next.samples.map((s) => s.rate)).toEqual([100, 50]);
    expect(next.peak).toBe(100);
    expect(next.latestRate).toBe(50);
    // the same reading again changes nothing, and is the same history
    expect(recordTokens(next, { turn: "t1", tokens: 150, at: 2000, now: 2500 })).toBe(next);
  });

  it("takes a page's first reading in the middle of a turn only as where to measure from", () => {
    const first = feed(emptyRateHistory(), [[1000, 10_000]]);
    expect(first.samples).toEqual([]);
    expect(first.latestRate).toBeNull();
    expect(feed(first, [[1100, 12_000]]).samples[0].rate).toBe(50);
  });

  it("counts no time between rises further apart than GAP_MS (a tool, a wait) and measures again after them", () => {
    const first = feed(begin(), [[100, 1000]]);
    const afterTool = feed(first, [[200, 1000 + GAP_MS + 1]]);
    expect(afterTool.samples).toEqual(first.samples);
    expect(afterTool.genMs).toBe(1000);
    const next = feed(afterTool, [[260, 2000 + GAP_MS + 1]]);
    expect(next.samples.map((s) => s.rate)).toEqual([100, 60]);
    expect(next.samples.map((s) => s.durationMs)).toEqual([1000, 2000]);
    // a gap of exactly GAP_MS is still one stretch
    expect(feed(begin(), [[90, GAP_MS]]).samples.map((s) => s.rate)).toEqual([30]);
  });

  it("starts the counts again for a new turn or a count that went back, keeping the chart", () => {
    const first = feed(begin(), [[100, 1000]]);
    const newTurn = feed(first, [[0, 5000]], "t2");
    expect(newTurn.samples).toEqual(first.samples);
    expect(newTurn.latestRate).toBeNull();
    expect(newTurn.peak).toBe(0);
    expect(newTurn.genMs).toBe(0);
    const back = feed(first, [[50, 1500]]);
    expect(back.samples).toEqual(first.samples);
    expect(feed(back, [[100, 2500]]).samples.map((s) => s.rate)).toEqual([100, 50]);
  });

  it("does not turn a count corrected with no time between into a spike", () => {
    const first = feed(begin(), [[100, 1000]]);
    const correction = feed(first, [[150, 1000]]);
    expect(correction.samples).toEqual(first.samples);
    expect(correction.peak).toBe(100);
    expect(feed(correction, [[200, 2000]]).samples.at(-1)?.rate).toBe(50);
  });

  it("gathers readings into a second of generation before it is a sample, losing no tokens", () => {
    let h = begin();
    for (let n = 1; n < 4; n++) {
      h = feed(h, [[n * 30, n * 300]]);
      expect(h.samples).toEqual([]);
    }
    h = feed(h, [[120, 1200]]);
    expect(h.samples.map((s) => s.rate)).toEqual([100]);
    expect(h.samples[0].durationMs).toBe(1200);
  });

  it("keeps 60 samples for the chart and the turn's peak", () => {
    let h = feed(begin(), [[1000, 1000]]);
    for (let n = 2; n <= 200; n++) h = feed(h, [[1000 + n * 10, n * 1000]]);
    expect(h.samples).toHaveLength(60);
    expect(h.peak).toBe(1000);
    expect(h.samples[0].durationMs).toBe(141_000);
  });

  it("records the reading that ends the turn with the turn's own counts, and the next turn goes on along the chart", () => {
    const active = feed(begin(), [[100, 1000]]);
    const end = feed(active, [[150, 2000]], null);
    expect(end.turn).toBe("t1");
    expect(end.samples.map((s) => s.rate)).toEqual([100, 50]);
    const next = feed(feed(end, [[0, 9000]], "t2"), [[40, 10_000]], "t2");
    expect(next.samples.map((s) => s.rate)).toEqual([100, 50, 40]);
    expect(next.samples.map((s) => s.durationMs)).toEqual([1000, 2000, 3000]);
    expect(next.latestRate).toBe(40);
    expect(next.peak).toBe(40);
  });

  it("ignores readings without a count", () => {
    const h = begin();
    expect(recordTokens(h, { turn: "t1", tokens: undefined, at: 1000, now: 1000 })).toBe(h);
    expect(recordTokens(h, { turn: "t1", tokens: Number.NaN, at: 1000, now: 1000 })).toBe(h);
  });
});

describe("what the bar shows of it", () => {
  it("shows the latest speed only while it is fresh and the count moves", () => {
    const h = feed(begin(), [[100, 1000]]);
    expect(shownRate(h, 1000)).toBe(100);
    expect(shownRate(h, 1000 + GAP_MS)).toBe(100);
    // the count stood still past GAP_MS: a tool runs, so there is no speed now
    expect(shownRate(h, 1000 + GAP_MS + 1)).toBeNull();
    // still moving but every rise too far apart to measure: the last sample goes stale
    const stale = { ...h, movedAt: 1000 + FRESH_MS };
    expect(shownRate(stale, 1000 + FRESH_MS)).toBeNull();
    expect(shownRate(begin(), 0)).toBeNull();
  });

  it("averages the turn over its measured generation only", () => {
    expect(turnAverage(begin())).toBeNull();
    const h = feed(begin(), [[100, 1000], [300, 1000 + GAP_MS + 1], [400, 2000 + GAP_MS + 1]]);
    // 100 tokens in the first second, 100 in the last: the gap's 200 tokens and time count for nothing
    expect(turnAverage(h)).toBe(100);
  });

  it("writes a speed as cindy's formatRecentOutputTokenRate does", () => {
    expect(formatRate(0)).toBe("0");
    expect(formatRate(0.05)).toBe("<0.1");
    expect(formatRate(4.25)).toBe("4.3");
    expect(formatRate(42)).toBe("42");
    expect(formatRate(99.96)).toBe("100");
    expect(formatRate(120.4)).toBe("120");
  });
});

describe("the per-conversation cache", () => {
  it("brings back a history that has samples, and only one that has", () => {
    clearRateHistories();
    saveRateHistory("a", emptyRateHistory());
    expect(loadRateHistory("a")).toBeNull();
    const h = feed(begin(), [[100, 1000]]);
    saveRateHistory("a", h);
    expect(loadRateHistory("a")?.samples.map((s) => s.rate)).toEqual([100]);
  });

  it("drops the least recently used conversation past 20", () => {
    clearRateHistories();
    const h = feed(begin(), [[100, 1000]]);
    for (let n = 0; n < 20; n++) saveRateHistory(`c${n}`, h);
    expect(loadRateHistory("c0")).not.toBeNull(); // read: now the most recent
    saveRateHistory("c20", h);
    expect(loadRateHistory("c1")).toBeNull();
    expect(loadRateHistory("c0")).not.toBeNull();
    expect(loadRateHistory("c20")).not.toBeNull();
    clearRateHistories();
  });
});
