// The running bar's generation speed (cindy's runningTokenRateHistory and formatRecentOutputTokenRate, adapted; the
// owner asked on 2026-11-12 that the bar follow cindy). cindy reads a generation-only clock from its host; Persona has
// only the count Claude's status line shows ("↓ 1.2k tokens", read about once a second, `SessionLive.tokens_down`), so
// generation time is worked out from when that count moves: two rises at most GAP_MS apart are generation, and a longer
// gap (a tool running, a wait) counts for nothing and starts the measuring again. Every speed is a count over a time
// that both moved; nothing is guessed, and nothing shows before there is a measurement (docs/design/PRINCIPLES.md 5).
import { useEffect, useState } from "react";

export interface RateSample {
  /** Measured generation time up to this sample, across turns: the chart's x axis. */
  durationMs: number;
  rate: number;
}

export interface RateHistory {
  /** The turn the counts belong to (its start); a new one starts the counts and the peak again, not the chart. */
  turn: string | null;
  /** The last count seen and when (the node's clock when it says, else this page's). */
  last: { tokens: number; at: number } | null;
  /** Generation since the last sample, not a second long yet. */
  window: { tokens: number; ms: number };
  /** This turn's measured generation: the turn average. */
  genTokens: number;
  genMs: number;
  samples: RateSample[];
  /** This turn's highest sample. */
  peak: number;
  latestRate: number | null;
  /** When the latest sample was taken, and when the count last rose (this page's clock). */
  latestAt?: number;
  movedAt?: number;
}

/** Two rises of the count at most this far apart are one stretch of generation; the speed also hides once the count
 *  has stood still this long (cindy hides it while its host says generation paused). */
export const GAP_MS = 3000;
/** A sample covers at least this much generation (a burst of a few milliseconds is no speed). */
const MIN_SAMPLE_MS = 1000;
/** The latest speed stays the latest for this long (cindy's RATE_SAMPLE_FRESH_MS). */
export const FRESH_MS = 60_000;
const MAX_SAMPLES = 60;
const NO_WINDOW = { tokens: 0, ms: 0 };

export function emptyRateHistory(): RateHistory {
  return { turn: null, last: null, window: NO_WINDOW, genTokens: 0, genMs: 0, samples: [], peak: 0, latestRate: null };
}

/** One reading of the count. `turn` null (the turn is over) keeps the one before, as cindy's terminal report does. The
 *  same history comes back when nothing changed, so a render that reads it does not render again. */
export function recordTokens(h: RateHistory, input: { turn: string | null; tokens: number | undefined; at: number; now: number }): RateHistory {
  const turn = input.turn ?? h.turn;
  // a new turn: its counts start from nothing; the chart goes on after the last one's samples
  const cur: RateHistory = turn === h.turn ? h : { ...h, turn, last: null, window: NO_WINDOW, genTokens: 0, genMs: 0, peak: 0, latestRate: null, latestAt: undefined, movedAt: undefined };
  const { tokens, at, now } = input;
  if (tokens === undefined || !Number.isFinite(tokens) || tokens < 0 || !Number.isFinite(at)) return cur;
  const last = cur.last;
  // the first reading (a page opened in the middle of a turn) or a count that went back: only where to measure from
  if (!last || tokens < last.tokens) return { ...cur, last: { tokens, at }, window: NO_WINDOW };
  // a reading with the same count says nothing of when the next tokens came
  if (tokens === last.tokens) return cur;
  const gap = at - last.at;
  const moved = { ...cur, last: { tokens, at }, movedAt: now };
  // a long gap is not generation (the tokens came at some unknown moment in it), and a count corrected at the same
  // moment has no time to divide by: measure again from here
  if (gap <= 0 || gap > GAP_MS) return { ...moved, window: NO_WINDOW };
  const delta = tokens - last.tokens;
  const window = { tokens: cur.window.tokens + delta, ms: cur.window.ms + gap };
  const next = { ...moved, genTokens: cur.genTokens + delta, genMs: cur.genMs + gap };
  if (window.ms < MIN_SAMPLE_MS) return { ...next, window };
  const rate = (window.tokens * 1000) / window.ms;
  const samples = [...cur.samples.slice(-(MAX_SAMPLES - 1)), { durationMs: (cur.samples.at(-1)?.durationMs ?? 0) + window.ms, rate }];
  return { ...next, window: NO_WINDOW, samples, peak: Math.max(cur.peak, rate), latestRate: rate, latestAt: now };
}

/** The latest speed to show at `now`: while it is fresh and the count still moves, else null. */
export function shownRate(h: RateHistory, now: number): number | null {
  if (h.latestRate === null || h.latestAt === undefined || h.movedAt === undefined) return null;
  return now - h.latestAt < FRESH_MS && now - h.movedAt <= GAP_MS ? h.latestRate : null;
}

/** This turn's measured tokens over its measured generation time, once there is a second of it. */
export function turnAverage(h: RateHistory): number | null {
  return h.genMs >= MIN_SAMPLE_MS ? (h.genTokens * 1000) / h.genMs : null;
}

/** cindy's formatRecentOutputTokenRate: `0`, `<0.1`, `4.2`, `42`, `120` (no ".0"; whole from 100). */
export function formatRate(rate: number): string {
  if (rate === 0) return "0";
  if (!Number.isFinite(rate) || rate < 0) return "";
  if (rate < 0.1) return "<0.1";
  return rate >= 100 ? rate.toFixed(0) : rate.toFixed(1).replace(/\.0$/, "");
}

// Kept per conversation while the page is open (cindy's in-process cache): switching to another chat and back keeps
// the chart. Never written anywhere; a reload starts empty.
const MAX_CACHED = 20;
const cache = new Map<string, RateHistory>();

export function loadRateHistory(key: string): RateHistory | null {
  const h = cache.get(key);
  if (!h) return null;
  // the Map iterates in insertion order: what is read moves to the end, the least recently used is first
  cache.delete(key);
  cache.set(key, h);
  return h;
}

export function saveRateHistory(key: string, h: RateHistory): void {
  if (!h.samples.length) return; // no chart to bring back
  cache.delete(key);
  cache.set(key, h);
  while (cache.size > MAX_CACHED) cache.delete(cache.keys().next().value!);
}

export function clearRateHistories(): void {
  cache.clear();
}

/** The speed history of the conversation `key`, fed with each status line while `on` (an Agent's own turn: not in the
 *  Persona conversation, not while only background work goes on). `turn` is the turn's start. */
export function useRateHistory(key: string, turn: string | undefined, tokens: number | undefined, at: string | undefined, on: boolean): RateHistory {
  const [state, setState] = useState(() => ({ key, h: loadRateHistory(key) ?? emptyRateHistory() }));
  // another conversation in the same bar: its own history
  const cur = state.key === key ? state : { key, h: loadRateHistory(key) ?? emptyRateHistory() };
  if (cur !== state) setState(cur);
  useEffect(() => {
    if (!on) return;
    const now = Date.now();
    const node = at ? Date.parse(at) : NaN;
    setState((s) => {
      const h = recordTokens(s.h, { turn: turn ?? null, tokens, at: Number.isFinite(node) ? node : now, now });
      return h === s.h ? s : { key: s.key, h };
    });
    // a reading is a new `at` (or, without one, a new count)
  }, [on, turn, tokens, at]);
  useEffect(() => saveRateHistory(cur.key, cur.h), [cur.key, cur.h]);
  return cur.h;
}
