// Pure aggregation helper for the lazy Session Insights popover (#12541).
// Aggregates existing protocol data only: Session context window, Item.Usage (including Fold.usage and
// deduplicated subagent reports), item timestamps for latest turn duration, and in-memory token-rate history.
// Unavailable metrics are returned as null so callers omit them honestly rather than displaying fake zeros.
import { contextUsage, type ContextUsage } from "./context-usage";
import { sessionCan } from "./format";
import { bareModel, replyModelName } from "./model-change";
import { loadRateHistory, turnAverage, type RateHistory } from "./token-rate";
import type { Item, Session, TokenUsage } from "./types";

/** Reserved slot for future protocol context-window category breakdown (e.g. system / tools / messages). */
export interface ContextBreakdownSegment {
  key: string;
  label?: string;
  tokens: number;
}

export interface ContextBreakdownSlot {
  segments: ContextBreakdownSegment[];
}

export interface SessionModelUsage {
  /** Normalized bare model identifier, or "" when the transcript did not record a model. */
  model: string;
  /** True for fast-speed requests, kept distinct from normal-speed usage on the same model (matching turn-cost.modelTokens). */
  fast?: boolean;
  /** Human-readable display label for the model, or "" when unknown. */
  label: string;
  /** Non-cached input tokens. */
  input: number;
  /** Output tokens. */
  output: number;
  cacheRead: number;
  cacheWrite: number;
  total: number;
  /** True when this model reported any cache_read or cache_write tokens. */
  hasCache: boolean;
  /** Cache read / (input + cacheRead + cacheWrite) in [0, 1], or null when unavailable. */
  cacheHitRate: number | null;
}

export interface SessionTokenSummary {
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  total: number;
  /** True when the session reported any cache_read or cache_write tokens. */
  hasCache: boolean;
  /** Cache read / (input + cacheRead + cacheWrite) in [0, 1], or null when unavailable. */
  cacheHitRate: number | null;
  /** Per-model breakdown sorted by total tokens descending (stable on ties). */
  models: SessionModelUsage[];
}

export interface SessionPerformanceSummary {
  /** Duration of the latest turn in ms, derived from item timestamps. */
  latestTurnDurationMs: number | null;
  /** Latest measured output speed in tok/s. */
  latestTokPerSec: number | null;
  /** Average measured output speed in tok/s. */
  avgTokPerSec: number | null;
  /** Peak measured output speed in tok/s. */
  peakTokPerSec: number | null;
  /** Reserved optional slot for future Time-To-First-Token (ms) metric. */
  ttftMs?: number | null;
}

export interface SessionInsights {
  context: ContextUsage | null;
  tokens: SessionTokenSummary | null;
  performance: SessionPerformanceSummary | null;
  /** Reserved optional slot for future ContextBreakdown data. */
  breakdown?: ContextBreakdownSlot | null;
}

export interface SessionInsightsOptions {
  /** Override the in-memory token-rate history (defaults to loadRateHistory for the session). */
  rateHistory?: RateHistory | null;
  /** Reserved optional slot for future context breakdown data. */
  breakdown?: ContextBreakdownSlot | null;
  /** Reserved optional slot for future TTFT (ms) data. */
  ttftMs?: number | null;
}

export interface CompactSupport {
  supported: boolean;
  mode: "assistant" | "agent" | null;
  running: boolean;
}

interface RawBucket {
  model: string;
  fast: boolean;
  order: number;
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
}

const positive = (v: unknown): number => (typeof v === "number" && Number.isFinite(v) && v > 0 ? v : 0);

function tsMs(ts?: string): number | null {
  if (!ts) return null;
  const ms = Date.parse(ts);
  return Number.isFinite(ms) ? ms : null;
}

function hitRateOf(input: number, cacheRead: number, cacheWrite: number): { hasCache: boolean; cacheHitRate: number | null } {
  const hasCache = cacheRead > 0 || cacheWrite > 0;
  if (!hasCache) return { hasCache: false, cacheHitRate: null };
  const prompt = input + cacheRead + cacheWrite;
  return { hasCache: true, cacheHitRate: prompt > 0 ? cacheRead / prompt : null };
}

/**
 * Aggregates cumulative token and cache usage across all loaded items of a session.
 * Deduplicates subagent reports (entries with a non-empty `id`) by `(id, model, fast)` using the max per field,
 * matching `lib/turn-cost.ts`, and returns null when no token usage exists.
 */
export function aggregateSessionTokens(items: readonly Item[]): SessionTokenSummary | null {
  if (!items.length) return null;

  const reports = new Map<string, { model: string; fast: boolean; input: number; output: number; cacheRead: number; cacheWrite: number }>();
  const models = new Map<string, RawBucket>();
  let nextOrder = 0;

  const ensureModel = (model: string, fast: boolean): RawBucket => {
    const key = `${model}\n${fast ? 1 : 0}`;
    let bucket = models.get(key);
    if (!bucket) {
      bucket = { model, fast, order: nextOrder++, input: 0, output: 0, cacheRead: 0, cacheWrite: 0 };
      models.set(key, bucket);
    }
    return bucket;
  };

  const ingest = (u: TokenUsage, fallbackModel: string) => {
    const inp = positive(u.in);
    const out = positive(u.out);
    const read = positive(u.cache_read);
    const write = positive(u.cache_write);
    if (inp + out + read + write === 0) return;

    const model = u.model ? bareModel(u.model) : u.id ? "" : fallbackModel;
    const fast = Boolean(u.fast);
    ensureModel(model, fast);

    if (u.id) {
      const reportKey = `${u.id}\n${model}\n${fast ? 1 : 0}`;
      const prev = reports.get(reportKey);
      if (prev) {
        prev.input = Math.max(prev.input, inp);
        prev.output = Math.max(prev.output, out);
        prev.cacheRead = Math.max(prev.cacheRead, read);
        prev.cacheWrite = Math.max(prev.cacheWrite, write);
      } else {
        reports.set(reportKey, { model, fast, input: inp, output: out, cacheRead: read, cacheWrite: write });
      }
      return;
    }

    const bucket = ensureModel(model, fast);
    bucket.input += inp;
    bucket.output += out;
    bucket.cacheRead += read;
    bucket.cacheWrite += write;
  };

  for (const item of items) {
    const fallbackModel = item.model ? bareModel(item.model) : "";
    if (item.usage?.length) {
      for (const u of item.usage) ingest(u, fallbackModel);
    }
    if (item.fold?.usage?.length) {
      for (const u of item.fold.usage) ingest(u, fallbackModel);
    }
  }

  for (const r of reports.values()) {
    const bucket = ensureModel(r.model, r.fast);
    bucket.input += r.input;
    bucket.output += r.output;
    bucket.cacheRead += r.cacheRead;
    bucket.cacheWrite += r.cacheWrite;
  }

  const modelRows: (SessionModelUsage & { order: number })[] = [];
  let input = 0;
  let output = 0;
  let cacheRead = 0;
  let cacheWrite = 0;

  for (const b of models.values()) {
    const total = b.input + b.output + b.cacheRead + b.cacheWrite;
    if (total <= 0) continue;
    input += b.input;
    output += b.output;
    cacheRead += b.cacheRead;
    cacheWrite += b.cacheWrite;
    const { hasCache, cacheHitRate } = hitRateOf(b.input, b.cacheRead, b.cacheWrite);
    modelRows.push({
      model: b.model,
      ...(b.fast ? { fast: true } : {}),
      label: b.model ? replyModelName(b.model) : "",
      input: b.input,
      output: b.output,
      cacheRead: b.cacheRead,
      cacheWrite: b.cacheWrite,
      total,
      hasCache,
      cacheHitRate,
      order: b.order,
    });
  }

  const total = input + output + cacheRead + cacheWrite;
  if (total <= 0) return null;

  modelRows.sort((a, b) => b.total - a.total || a.order - b.order);
  const { hasCache, cacheHitRate } = hitRateOf(input, cacheRead, cacheWrite);

  return {
    input,
    output,
    cacheRead,
    cacheWrite,
    total,
    hasCache,
    cacheHitRate,
    models: modelRows.map(({ order: _order, ...row }) => row),
  };
}

/**
 * Estimates the latest turn's duration in milliseconds from loaded transcript items.
 * Splits turns on non-queued user items and measures from the turn's start to its latest assistant/fold timestamp.
 */
export function latestTurnDurationMs(items: readonly Item[]): number | null {
  if (!items.length) return null;

  const turns: { userStart: number | null; replyStart: number | null; replyEnd: number | null }[] = [];
  let current: { userStart: number | null; replyStart: number | null; replyEnd: number | null } | null = null;

  for (const item of items) {
    if (item.role === "user" && !item.queued) {
      current = { userStart: tsMs(item.ts), replyStart: null, replyEnd: null };
      turns.push(current);
      continue;
    }
    if (item.role === "user") continue;
    if (!current) {
      current = { userStart: null, replyStart: null, replyEnd: null };
      turns.push(current);
    }
    const start = tsMs(item.ts);
    const foldEnd = tsMs(item.fold?.until);
    if (start !== null) {
      if (current.replyStart === null || start < current.replyStart) current.replyStart = start;
      if (current.replyEnd === null || start > current.replyEnd) current.replyEnd = start;
    }
    if (foldEnd !== null && (current.replyEnd === null || foldEnd > current.replyEnd)) {
      current.replyEnd = foldEnd;
    }
  }

  for (let i = turns.length - 1; i >= 0; i--) {
    const turn = turns[i];
    if (turn.replyEnd === null) continue;
    const start = turn.userStart ?? turn.replyStart;
    if (start !== null && turn.replyEnd > start) return turn.replyEnd - start;
  }

  return null;
}

function sampleAverageRate(history: RateHistory): number | null {
  if (!history.samples.length) return null;
  let weightedSum = 0;
  let weightTotal = 0;
  let prevDuration = 0;
  for (const sample of history.samples) {
    if (!Number.isFinite(sample.rate) || sample.rate <= 0) continue;
    const delta = sample.durationMs - prevDuration;
    const weight = delta > 0 ? delta : 1;
    weightedSum += sample.rate * weight;
    weightTotal += weight;
    prevDuration = sample.durationMs;
  }
  return weightTotal > 0 ? weightedSum / weightTotal : null;
}

/** Summarizes performance metrics from transcript timestamps and in-memory token-rate history. */
export function summarizeSessionPerformance(
  items: readonly Item[],
  rateHistory?: RateHistory | null,
  ttftMs?: number | null,
): SessionPerformanceSummary | null {
  const turnMs = latestTurnDurationMs(items);

  let latestTokPerSec: number | null = null;
  let avgTokPerSec: number | null = null;
  let peakTokPerSec: number | null = null;

  if (rateHistory) {
    const latestCandidate = rateHistory.latestRate ?? rateHistory.samples.at(-1)?.rate ?? null;
    latestTokPerSec = latestCandidate !== null && Number.isFinite(latestCandidate) && latestCandidate > 0 ? latestCandidate : null;

    const turnAvg = turnAverage(rateHistory);
    const avgCandidate = turnAvg !== null && Number.isFinite(turnAvg) && turnAvg > 0 ? turnAvg : sampleAverageRate(rateHistory);
    avgTokPerSec = avgCandidate !== null && Number.isFinite(avgCandidate) && avgCandidate > 0 ? avgCandidate : null;

    let maxRate = positive(rateHistory.peak);
    for (const s of rateHistory.samples) {
      if (Number.isFinite(s.rate) && s.rate > maxRate) maxRate = s.rate;
    }
    peakTokPerSec = maxRate > 0 ? maxRate : null;
  }

  const validTtft = ttftMs !== undefined && ttftMs !== null && Number.isFinite(ttftMs) && ttftMs > 0 ? ttftMs : null;

  if (turnMs === null && latestTokPerSec === null && avgTokPerSec === null && peakTokPerSec === null && validTtft === null) {
    return null;
  }

  return {
    latestTurnDurationMs: turnMs,
    latestTokPerSec,
    avgTokPerSec,
    peakTokPerSec,
    ...(validTtft !== null ? { ttftMs: validTtft } : {}),
  };
}

/** Builds the full Session Insights snapshot from existing Session, Item, and token-rate data. */
export function buildSessionInsights(
  session: Session | undefined,
  items: readonly Item[] = [],
  options?: SessionInsightsOptions,
): SessionInsights {
  const history = options && "rateHistory" in options
    ? options.rateHistory
    : session
      ? loadRateHistory(`${session.machine_id}/${session.session_id}`)
      : null;

  const breakdown = options?.breakdown && options.breakdown.segments.length > 0 ? options.breakdown : null;

  return {
    context: contextUsage(session),
    tokens: aggregateSessionTokens(items),
    performance: summarizeSessionPerformance(items, history, options?.ttftMs),
    ...(breakdown ? { breakdown } : {}),
  };
}

/** Formats a [0, 1] cache hit rate as a concise percentage string (e.g. "82%", "82.4%"). */
export function formatHitRate(rate: number | null): string | null {
  if (rate === null || !Number.isFinite(rate)) return null;
  const pct = Math.min(100, Math.max(0, rate * 100));
  if (Math.abs(pct - Math.round(pct)) < 0.05) return `${Math.round(pct)}%`;
  return `${pct.toFixed(1).replace(/\.0$/, "")}%`;
}

/**
 * Determines whether one-click context compaction is supported for the session.
 * - Blocked (setup gate / offline / read-only composer) or routed (active task route): never offered.
 * - Assistant chats: requires hub `assistant_compact` feature (uses POST …/compact).
 * - Agent chats: requires a terminal-command agent (`!source_kind`), send capability, and a send callback.
 */
export function compactActionSupport(
  session: Session | undefined,
  opts: { assistant?: boolean; hubSupportsAssistantCompact?: boolean; hasSendCompact?: boolean; blocked?: boolean; routed?: boolean },
): CompactSupport {
  if (!session || opts.blocked || opts.routed) return { supported: false, mode: null, running: false };
  const isAssistant = Boolean(opts.assistant || session.assistant);
  if (isAssistant) {
    const supported = Boolean(opts.hubSupportsAssistantCompact);
    return { supported, mode: supported ? "assistant" : null, running: session.compaction?.state === "running" };
  }
  const supported = Boolean(!session.source_kind && sessionCan(session, "send") && opts.hasSendCompact);
  return { supported, mode: supported ? "agent" : null, running: false };
}
