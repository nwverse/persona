import { afterEach, describe, expect, it } from "vitest";
import {
  aggregateSessionTokens,
  buildSessionInsights,
  compactActionSupport,
  formatHitRate,
  latestTurnDurationMs,
  summarizeSessionPerformance,
} from "./session-insights";
import { clearRateHistories, emptyRateHistory, recordTokens, saveRateHistory } from "./token-rate";
import type { Item, Session } from "./types";

const baseSession: Session = {
  machine_id: "m1",
  session_id: "s1",
  title: "Test",
  cwd: "/code",
  status: "idle",
  agent: "claude",
};

const item = (partial: Partial<Item> & Pick<Item, "id" | "seq">): Item => ({
  role: "assistant",
  kind: "text",
  ...partial,
});

describe("aggregateSessionTokens", () => {
  it("returns null for empty or zero-usage items", () => {
    expect(aggregateSessionTokens([])).toBeNull();
    expect(aggregateSessionTokens([
      item({ id: "1", seq: 1, text: "hi" }),
      item({ id: "2", seq: 2, usage: [{ in: 0, out: -5, cache_read: Number.NaN, cache_write: Number.POSITIVE_INFINITY }] }),
    ])).toBeNull();
  });

  it("sums direct and folded Item.Usage and computes cache hit rate", () => {
    const items: Item[] = [
      item({
        id: "1",
        seq: 1,
        usage: [{ model: "claude-sonnet-4-5-20250929", in: 1000, out: 500, cache_read: 8000, cache_write: 1000 }],
      }),
      item({
        id: "2",
        seq: 2,
        kind: "fold",
        fold: {
          items: 2,
          tools: 1,
          thoughts: 1,
          through_seq: 3,
          usage: [{ model: "anthropic/claude-sonnet-4-5", in: 500, out: 300, cache_read: 9000, cache_write: 500 }],
        },
      }),
    ];

    const summary = aggregateSessionTokens(items);
    expect(summary).not.toBeNull();
    expect(summary!.input).toBe(1500);
    expect(summary!.output).toBe(800);
    expect(summary!.cacheRead).toBe(17000);
    expect(summary!.cacheWrite).toBe(1500);
    expect(summary!.total).toBe(20800);
    expect(summary!.hasCache).toBe(true);
    // 17000 / (1500 + 17000 + 1500) = 17000 / 20000 = 0.85
    expect(summary!.cacheHitRate).toBeCloseTo(0.85, 6);
    expect(summary!.models).toHaveLength(1);
    expect(summary!.models[0]).toMatchObject({
      model: "claude-sonnet-4-5",
      label: "Sonnet 4.5",
      total: 20800,
      hasCache: true,
    });
  });

  it("omits cache metrics honestly when no cache tokens exist", () => {
    const summary = aggregateSessionTokens([
      item({ id: "1", seq: 1, usage: [{ model: "gpt-5.4", in: 2000, out: 600 }] }),
    ]);
    expect(summary).not.toBeNull();
    expect(summary!.hasCache).toBe(false);
    expect(summary!.cacheHitRate).toBeNull();
    expect(summary!.models[0].hasCache).toBe(false);
    expect(summary!.models[0].cacheHitRate).toBeNull();
  });

  it("reports 0% cache hit rate when cache_write occurred with zero cache_read", () => {
    const summary = aggregateSessionTokens([
      item({ id: "1", seq: 1, usage: [{ model: "claude-opus-4-5", in: 1000, out: 200, cache_write: 4000 }] }),
    ]);
    expect(summary).not.toBeNull();
    expect(summary!.hasCache).toBe(true);
    expect(summary!.cacheHitRate).toBe(0);
  });

  it("deduplicates subagent reports by (id, model, fast) and sorts multi-model breakdown by total desc", () => {
    const items: Item[] = [
      item({
        id: "1",
        seq: 1,
        model: "claude-haiku-4-5-20251001",
        // direct entry without u.model inherits item.model
        usage: [{ in: 400, out: 100 }],
      }),
      item({
        id: "2",
        seq: 2,
        usage: [
          { id: "sub-1", model: "claude-opus-4-5", in: 1000, out: 500, cache_read: 3000 },
          { id: "sub-1", model: "claude-opus-4-5", in: 1200, out: 400, cache_read: 5000 },
        ],
      }),
      item({
        id: "3",
        seq: 3,
        usage: [
          // duplicate report on a later item takes max per field, not sum
          { id: "sub-1", model: "claude-opus-4-5", in: 900, out: 700, cache_read: 5000 },
          { model: "claude-sonnet-4-5", in: 2000, out: 800, cache_read: 1000 },
        ],
      }),
    ];

    const summary = aggregateSessionTokens(items)!;
    // sub-1 deduplicated: in=1200, out=700, cache_read=5000 => 6900
    // sonnet: 3800; haiku: 500
    expect(summary.models.map((m) => [m.model, m.total])).toEqual([
      ["claude-opus-4-5", 6900],
      ["claude-sonnet-4-5", 3800],
      ["claude-haiku-4-5", 500],
    ]);
    expect(summary.total).toBe(6900 + 3800 + 500);
  });

  it("keeps normal-speed and fast-speed usage on the same model in separate buckets (matching turn-cost.modelTokens)", () => {
    const items: Item[] = [
      item({
        id: "1",
        seq: 1,
        usage: [
          { model: "claude-opus-4-5", in: 1000, out: 500, cache_read: 4000 },
          { model: "claude-opus-4-5", fast: true, in: 600, out: 400, cache_read: 1000 },
          { id: "sub-1", model: "claude-opus-4-5", in: 500, out: 200 },
          { id: "sub-1", model: "claude-opus-4-5", fast: true, in: 300, out: 100, cache_read: 600 },
        ],
      }),
      item({
        id: "2",
        seq: 2,
        usage: [
          // duplicate sub-1 reports deduplicate within each speed bucket independently
          { id: "sub-1", model: "claude-opus-4-5", in: 700, out: 200 },
          { id: "sub-1", model: "claude-opus-4-5", fast: true, in: 300, out: 200, cache_read: 600 },
        ],
      }),
    ];

    const summary = aggregateSessionTokens(items)!;
    // normal: direct (1000, 500, 4000) + sub-1 (700, 200, 0) = in 1700, out 700, read 4000 => 6400
    // fast:   direct (600, 400, 1000)  + sub-1 (300, 200, 600) = in 900, out 600, read 1600 => 3100
    expect(summary.models).toHaveLength(2);
    expect(summary.models[0]).toMatchObject({
      model: "claude-opus-4-5",
      input: 1700,
      output: 700,
      cacheRead: 4000,
      total: 6400,
    });
    expect(summary.models[0].fast).toBeUndefined();
    expect(summary.models[1]).toMatchObject({
      model: "claude-opus-4-5",
      fast: true,
      input: 900,
      output: 600,
      cacheRead: 1600,
      total: 3100,
    });
    expect(summary.total).toBe(9500);
  });
});

describe("latestTurnDurationMs", () => {
  it("computes duration of the latest turn including fold.until", () => {
    const items: Item[] = [
      { id: "u1", seq: 1, role: "user", kind: "text", ts: "2026-11-20T10:00:00Z", text: "first" },
      { id: "a1", seq: 2, role: "assistant", kind: "text", ts: "2026-11-20T10:00:05Z", text: "ok" },
      { id: "u2", seq: 3, role: "user", kind: "text", ts: "2026-11-20T10:01:00Z", text: "second" },
      {
        id: "f1",
        seq: 4,
        role: "assistant",
        kind: "fold",
        ts: "2026-11-20T10:01:02Z",
        fold: { items: 2, tools: 1, thoughts: 0, through_seq: 5, until: "2026-11-20T10:01:18Z" },
      },
    ];
    expect(latestTurnDurationMs(items)).toBe(18_000);
  });

  it("falls back to previous completed turn if latest user message has no reply yet", () => {
    const items: Item[] = [
      { id: "u1", seq: 1, role: "user", kind: "text", ts: "2026-11-20T10:00:00Z", text: "first" },
      { id: "a1", seq: 2, role: "assistant", kind: "text", ts: "2026-11-20T10:00:12Z", text: "done" },
      { id: "u2", seq: 3, role: "user", kind: "text", ts: "2026-11-20T10:02:00Z", text: "waiting" },
    ];
    expect(latestTurnDurationMs(items)).toBe(12_000);
  });

  it("returns null when timestamps are absent or non-increasing", () => {
    expect(latestTurnDurationMs([])).toBeNull();
    expect(latestTurnDurationMs([
      { id: "u1", seq: 1, role: "user", kind: "text", ts: "2026-11-20T10:00:00Z" },
      { id: "a1", seq: 2, role: "assistant", kind: "text", ts: "2026-11-20T10:00:00Z" },
    ])).toBeNull();
  });
});

describe("buildSessionInsights & performance", () => {
  afterEach(() => {
    clearRateHistories();
  });

  it("aggregates context, tokens, rate history, and reserved optional slots", () => {
    let h = emptyRateHistory();
    h = recordTokens(h, { turn: "t1", tokens: 50, at: 1000, now: 1000 });
    h = recordTokens(h, { turn: "t1", tokens: 100, at: 3000, now: 3000 });
    h = recordTokens(h, { turn: "t1", tokens: 200, at: 5000, now: 5000 });
    h = recordTokens(h, { turn: "t1", tokens: 350, at: 7000, now: 7000 }); // 250 tok / 4s = 62.5 tok/s
    saveRateHistory("m1/s1", h);

    const items: Item[] = [
      { id: "u1", seq: 1, role: "user", kind: "text", ts: "2026-11-20T10:00:00Z" },
      {
        id: "a1",
        seq: 2,
        role: "assistant",
        kind: "text",
        ts: "2026-11-20T10:00:09Z",
        usage: [{ model: "claude-opus-4-5", in: 500, out: 350, cache_read: 4500 }],
      },
    ];

    const insights = buildSessionInsights(
      { ...baseSession, context_tokens: 78_000, context_window: 200_000 },
      items,
      {
        ttftMs: 420,
        breakdown: { segments: [{ key: "system", label: "System", tokens: 12_000 }] },
      },
    );

    expect(insights.context).toMatchObject({ used: 39, left: 61, tokens: 78_000, window: 200_000 });
    expect(insights.tokens?.total).toBe(5350);
    expect(insights.tokens?.cacheHitRate).toBeCloseTo(0.9, 6);
    expect(insights.performance).toMatchObject({
      latestTurnDurationMs: 9000,
      latestTokPerSec: 62.5,
      avgTokPerSec: 62.5,
      peakTokPerSec: 62.5,
      ttftMs: 420,
    });
    expect(insights.breakdown?.segments).toEqual([{ key: "system", label: "System", tokens: 12_000 }]);
  });

  it("omits performance and breakdown when data is unavailable", () => {
    const insights = buildSessionInsights(baseSession, []);
    expect(insights.context).toBeNull();
    expect(insights.tokens).toBeNull();
    expect(insights.performance).toBeNull();
    expect(insights.breakdown).toBeUndefined();
    expect(summarizeSessionPerformance([], null)).toBeNull();
  });
});

describe("formatHitRate", () => {
  it("formats whole and decimal percentages and returns null for null/NaN", () => {
    expect(formatHitRate(null)).toBeNull();
    expect(formatHitRate(Number.NaN)).toBeNull();
    expect(formatHitRate(0)).toBe("0%");
    expect(formatHitRate(0.85)).toBe("85%");
    expect(formatHitRate(0.824)).toBe("82.4%");
    expect(formatHitRate(1.2)).toBe("100%");
  });
});

describe("compactActionSupport", () => {
  it("supports assistant chats only when hub exposes assistant_compact", () => {
    const asst: Session = { ...baseSession, assistant: true };
    expect(compactActionSupport(asst, { assistant: true, hubSupportsAssistantCompact: true })).toEqual({
      supported: true,
      mode: "assistant",
      running: false,
    });
    expect(compactActionSupport(
      { ...asst, compaction: { state: "running", started_at: "2026-11-20T10:00:00Z" } },
      { assistant: true, hubSupportsAssistantCompact: true },
    )).toEqual({
      supported: true,
      mode: "assistant",
      running: true,
    });
    expect(compactActionSupport(asst, { assistant: true, hubSupportsAssistantCompact: false })).toEqual({
      supported: false,
      mode: null,
      running: false,
    });
  });

  it("supports agent chats only for terminal-command agents with send capability and callback", () => {
    expect(compactActionSupport(baseSession, { hasSendCompact: true })).toEqual({
      supported: true,
      mode: "agent",
      running: false,
    });
    expect(compactActionSupport({ ...baseSession, source_kind: "antigravity" }, { hasSendCompact: true }).supported).toBe(false);
    expect(compactActionSupport({ ...baseSession, capabilities: { send: false } }, { hasSendCompact: true }).supported).toBe(false);
    expect(compactActionSupport(baseSession, { hasSendCompact: false }).supported).toBe(false);
    expect(compactActionSupport(baseSession, { hasSendCompact: true, blocked: true }).supported).toBe(false);
    expect(compactActionSupport(baseSession, { hasSendCompact: true, routed: true }).supported).toBe(false);
    expect(compactActionSupport({ ...baseSession, assistant: true }, { assistant: true, hubSupportsAssistantCompact: true, blocked: true }).supported).toBe(false);
    expect(compactActionSupport({ ...baseSession, assistant: true }, { assistant: true, hubSupportsAssistantCompact: true, routed: true }).supported).toBe(false);
  });
});
