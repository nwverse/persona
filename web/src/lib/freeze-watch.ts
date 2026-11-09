// A frozen page: the main thread blocked for 5 seconds or more (docs/web/web.md "Error reports").
// One anr warning per page load, through the error-report pipeline, and only while the caller says the hub is
// advertising error_reports. long-animation-frame where the browser has it, otherwise longtask; with neither, nothing.
// A hidden tab is not a freeze, and neither is an entry that overlaps the page being hidden or frozen (visibilitychange,
// freeze / resume). The first load (before DOMContentLoaded), time a script spent paused (a debugger breakpoint, a
// modal, a synchronous request) and a wall-clock jump the monotonic clock did not see (sleep) are not a freeze either.
// Script attribution is a URL path only.

import type { ErrorFrame } from "./extension-wire";
import { FREEZE_MS, scriptFrame } from "./error-reports";

const MAX_SCRIPTS = 8;

export type FreezeMechanism = "long-animation-frame" | "longtask";

export interface FreezeScript {
  sourceURL?: string;
  sourceFunctionName?: string;
  duration?: number;
  /** Time the script spent paused: a debugger breakpoint, a modal, a synchronous request. Not a freeze. */
  pauseDuration?: number;
}

/** A long task's frame attribution. containerId and containerName are never read: they are page content. */
export interface FreezeAttribution {
  containerSrc?: string;
}

export interface FreezeEntry {
  startTime: number;
  duration: number;
  /** Long-animation-frame only: time the main thread was actually blocked. A throttled frame can have a large `duration` and a tiny one of these. */
  blockingDuration?: number;
  scripts?: FreezeScript[];
  attribution?: FreezeAttribution[];
}

export interface FreezeHit {
  durationMs: number;
  mechanism: FreezeMechanism;
  frames: ErrorFrame[];
  /** Wall time the block started, so a buffered entry is not stamped when the callback runs. */
  startedAt?: number;
}

/** A stretch of performance.now() while the page was hidden or frozen. `end` null: still in it. */
export interface FreezeGap {
  start: number;
  end: number | null;
}

export function freezeOverlaps(start: number, duration: number, gaps: readonly FreezeGap[]): boolean {
  if (!Number.isFinite(start) || !Number.isFinite(duration) || duration < 0) return true;
  const end = start + duration;
  return gaps.some((g) => start < (g.end ?? Number.POSITIVE_INFINITY) && end > g.start);
}

/** Script frames for one entry, innermost last. The longest script is last, so the hub fingerprints by it. */
export function freezeFrames(entry: FreezeEntry, mechanism: FreezeMechanism, origin: string): ErrorFrame[] {
  const frames: ErrorFrame[] = [];
  if (mechanism === "long-animation-frame") {
    const ranked = (entry.scripts ?? [])
      .map((script, index) => ({ script, index }))
      .filter(({ script }) => typeof script.sourceURL === "string" && script.sourceURL.trim() !== "")
      .sort((a, b) => (a.script.duration ?? 0) - (b.script.duration ?? 0) || a.index - b.index);
    for (const { script } of ranked) {
      const frame = scriptFrame(script.sourceURL ?? "", origin, script.sourceFunctionName);
      if (frame) frames.push(frame);
    }
  } else {
    for (const item of entry.attribution ?? []) {
      const frame = scriptFrame(item.containerSrc ?? "", origin);
      if (frame) frames.push(frame);
    }
  }
  return capFrames(frames);
}

/** At most MAX_SCRIPTS, longest last, but never drop every in-app script: the hub fingerprints by those. */
function capFrames(frames: ErrorFrame[]): ErrorFrame[] {
  if (frames.length <= MAX_SCRIPTS) return frames;
  const keep = frames.slice(-MAX_SCRIPTS);
  if (keep.some((frame) => frame.in_app)) return keep;
  const inApp = [...frames].reverse().find((frame) => frame.in_app);
  if (!inApp) return keep;
  return [...keep.slice(1), inApp];
}

/** Time the entry's scripts spent paused. A number only; anything else is ignored. */
function pausedMs(entry: FreezeEntry): number {
  let paused = 0;
  for (const script of entry.scripts ?? []) {
    if (typeof script.pauseDuration === "number" && Number.isFinite(script.pauseDuration) && script.pauseDuration > 0) paused += script.pauseDuration;
  }
  return paused;
}

/** The entry is one freeze, or null when it is too short, still the first load, not in front, paused, asleep, or overlapping a gap.
 *  `interactiveAt`: DOMContentLoaded (`0` = not yet). Omitted or null: no first-load filter (the timing is unavailable).
 *  `sleepMs`: how far the wall clock jumped ahead of the monotonic clock since the previous look. */
export function classifyFreeze(
  entry: FreezeEntry,
  opts: {
    mechanism: FreezeMechanism;
    origin: string;
    foreground: boolean;
    gaps: readonly FreezeGap[];
    interactiveAt?: number | null;
    sleepMs?: number;
  },
): FreezeHit | null {
  if (!opts.foreground) return null;
  if (!Number.isFinite(entry.startTime) || !Number.isFinite(entry.duration) || entry.duration < 0) return null;
  const started = opts.interactiveAt;
  if (started === 0 || (typeof started === "number" && started > 0 && entry.startTime < started)) return null;
  const sleep = typeof opts.sleepMs === "number" && Number.isFinite(opts.sleepMs) && opts.sleepMs > 0 ? opts.sleepMs : 0;
  // A long animation frame's duration includes time the thread was waiting, not blocked. blockingDuration is the block.
  const span = opts.mechanism === "long-animation-frame" && typeof entry.blockingDuration === "number" && Number.isFinite(entry.blockingDuration)
    ? entry.blockingDuration
    : entry.duration;
  const blocked = span - pausedMs(entry) - sleep;
  if (!(blocked >= FREEZE_MS)) return null;
  if (freezeOverlaps(entry.startTime, entry.duration, opts.gaps)) return null;
  return { durationMs: blocked, mechanism: opts.mechanism, frames: freezeFrames(entry, opts.mechanism, opts.origin) };
}

interface RawEntry {
  startTime: number;
  duration: number;
  blockingDuration?: number;
  scripts?: FreezeScript[];
  attribution?: FreezeAttribution[];
}

function readEntry(raw: RawEntry): FreezeEntry {
  return {
    startTime: raw.startTime,
    duration: raw.duration,
    blockingDuration: typeof raw.blockingDuration === "number" ? raw.blockingDuration : undefined,
    // Only the fields a report may carry. An invoker, a character offset, an element id or a name stays out.
    scripts: raw.scripts?.map((script) => ({
      sourceURL: typeof script.sourceURL === "string" ? script.sourceURL : undefined,
      sourceFunctionName: typeof script.sourceFunctionName === "string" ? script.sourceFunctionName : undefined,
      duration: typeof script.duration === "number" ? script.duration : undefined,
      pauseDuration: typeof script.pauseDuration === "number" ? script.pauseDuration : undefined,
    })),
    attribution: raw.attribution?.map((item) => ({ containerSrc: typeof item.containerSrc === "string" ? item.containerSrc : undefined })),
  };
}

export interface FreezeObserver {
  observe(options: { type: string; buffered?: boolean }): void;
  disconnect(): void;
}

export interface FreezeObserverCtor {
  new (callback: (list: { getEntries(): RawEntry[] }) => void): FreezeObserver;
  readonly supportedEntryTypes: readonly string[];
}

export interface FreezeDocument {
  visibilityState: string;
  addEventListener(type: string, listener: () => void): void;
  removeEventListener(type: string, listener: () => void): void;
}

export interface FreezeWatchOptions {
  /** Queues the warning. `false`: it was not taken (the feature is off), so a later freeze may still be sent. */
  report: (hit: FreezeHit) => boolean;
  /** The hub's latest hello advertises error_reports. */
  enabled: () => boolean;
  /** Called on every change of that answer, the same way the error reporter hears hellos. Returns an unsubscribe. */
  subscribe?: (listener: () => void) => (() => void) | void;
  origin?: string;
  now?: () => number;
  /** Wall clock, for telling sleep from a freeze. Default `Date.now`. */
  wall?: () => number;
  /** `performance.timeOrigin`: wall time at monotonic 0. Default the browser's. */
  timeOrigin?: () => number;
  /** DOMContentLoaded time, or 0 when the document is not interactive yet. Omit: read the navigation timing. */
  interactiveAt?: () => number | null;
  document?: FreezeDocument;
  /** Null: this browser has no PerformanceObserver. Omit: the page's own. */
  Observer?: FreezeObserverCtor | null;
}

function pageOrigin(): string {
  try {
    return typeof location === "undefined" ? "" : location.origin;
  } catch {
    return "";
  }
}

function monoNow(): number {
  try {
    return typeof performance === "undefined" ? Date.now() : performance.now();
  } catch {
    return Date.now();
  }
}

function browserObserver(): FreezeObserverCtor | null {
  try {
    return typeof PerformanceObserver === "undefined" ? null : (PerformanceObserver as unknown as FreezeObserverCtor);
  } catch {
    return null;
  }
}

/** `0` until DOMContentLoaded, then that event's time. Null when the page has no navigation timing (nothing to filter). */
function browserTimeOrigin(now: () => number, wall: () => number): number {
  try {
    if (typeof performance !== "undefined" && typeof performance.timeOrigin === "number") return performance.timeOrigin;
  } catch {
    // no time origin
  }
  return wall() - now();
}

function browserInteractiveAt(): number | null {
  try {
    const timing = globalThis.performance?.getEntriesByType?.("navigation")[0] as { domContentLoadedEventEnd?: number } | undefined;
    if (!timing || typeof timing.domContentLoadedEventEnd !== "number") return null;
    return timing.domContentLoadedEventEnd;
  } catch {
    return null;
  }
}

function browserDocument(): FreezeDocument | null {
  try {
    return typeof document === "undefined" ? null : (document as unknown as FreezeDocument);
  } catch {
    return null;
  }
}

/**
 * Starts watching this page. Stops itself after the one warning. `stop` ends it sooner (a test, or the page going away).
 * Listening for hidden and frozen starts immediately, so a gap from before the feature was advertised is still known
 * when buffered entries arrive.
 */
export interface FreezeWatchStop {
  (): void;
  /** The queued warning was dropped. Watch again if the feature is on. */
  rearm(): void;
}

export function startFreezeWatch(opts: FreezeWatchOptions): FreezeWatchStop {
  const found = opts.document ?? browserDocument();
  if (!found) return Object.assign(() => {}, { rearm() {} });
  const doc: FreezeDocument = found;
  const origin = opts.origin ?? pageOrigin();
  const now = opts.now ?? monoNow;
  const wallNow = opts.wall ?? (() => Date.now());
  const timeOrigin = opts.timeOrigin ?? (() => browserTimeOrigin(now, wallNow));
  const interactiveAt = opts.interactiveAt ?? browserInteractiveAt;
  const Observer = opts.Observer === undefined ? browserObserver() : opts.Observer;
  // A sleep moves the wall clock and not the monotonic one. The extra is taken off the next entry's duration.
  let lastWall = wallNow();
  let lastMono = now();

  let hidden = doc.visibilityState !== "visible";
  let frozen = false;
  // Already hidden as the watch starts: the gap reaches back to navigation, so a buffered entry from this hidden
  // stretch is not a freeze. Visible: no gap until the page actually goes away.
  let openFrom: number | null = hidden ? 0 : null;
  const gaps: FreezeGap[] = [];
  let reported = false;
  let observer: FreezeObserver | null = null;

  const onVisibility = () => {
    hidden = doc.visibilityState !== "visible";
    sync();
  };
  const onFreeze = () => {
    frozen = true;
    sync();
  };
  const onResume = () => {
    frozen = false;
    sync();
  };

  function sync() {
    const blocked = hidden || frozen;
    const t = now();
    if (blocked) {
      if (openFrom === null) openFrom = t;
    } else if (openFrom !== null) {
      gaps.push({ start: openFrom, end: t });
      openFrom = null;
    }
  }

  function allGaps(): FreezeGap[] {
    return openFrom === null ? gaps : [...gaps, { start: openFrom, end: null }];
  }

  function disconnectObserver() {
    try {
      observer?.disconnect();
    } catch {
      // already gone
    }
    observer = null;
  }

  function removeListeners() {
    doc.removeEventListener("visibilitychange", onVisibility);
    doc.removeEventListener("freeze", onFreeze);
    doc.removeEventListener("resume", onResume);
  }

  let unsubscribe: (() => void) | void = undefined;

  /** One warning is queued. The observer stops, but a drop of that warning (the feature turned off) can rearm. */
  function hold() {
    reported = true;
    disconnectObserver();
  }

  function rearm() {
    if (!reported) return;
    reported = false;
    if (opts.enabled()) connect();
  }

  function teardown() {
    reported = true;
    disconnectObserver();
    removeListeners();
    if (typeof unsubscribe === "function") unsubscribe();
    unsubscribe = undefined;
  }

  function takeSleep(): number {
    const wall = wallNow();
    const mono = now();
    const extra = wall - lastWall - (mono - lastMono);
    lastWall = wall;
    lastMono = mono;
    return extra >= 1_000 ? extra : 0;
  }

  function onEntries(raws: RawEntry[], type: FreezeMechanism) {
    try {
      if (reported || !opts.enabled()) return;
      const sleepMs = takeSleep();
      const started = interactiveAt();
      const originMs = timeOrigin();
      for (const raw of raws) {
        if (reported || !opts.enabled()) return;
        // Shorter than the threshold cannot become one after pauses and sleep are taken off. Skip the copy.
        if (!(typeof raw.duration === "number" && raw.duration >= FREEZE_MS)) continue;
        const entry = readEntry(raw);
        // Hidden is a gap, not "hidden right now": a stall that ended as the tab was hidden still counts.
        const hit = classifyFreeze(entry, { mechanism: type, origin, foreground: !frozen, gaps: allGaps(), interactiveAt: started, sleepMs });
        if (!hit) continue;
        if (Number.isFinite(originMs) && Number.isFinite(entry.startTime)) hit.startedAt = originMs + entry.startTime;
        let accepted = false;
        try {
          accepted = opts.report(hit) === true;
        } catch {
          accepted = false;
        }
        if (accepted) {
          hold();
          return;
        }
      }
    } catch {
      // watching must never become an error of its own
    }
  }

  function connect() {
    if (observer || reported || !Observer) return;
    const types = Observer.supportedEntryTypes ?? [];
    const prefer: FreezeMechanism[] = [];
    if (types.includes("long-animation-frame")) prefer.push("long-animation-frame");
    if (types.includes("longtask")) prefer.push("longtask");
    for (const type of prefer) {
      let inst: FreezeObserver | null = null;
      try {
        inst = new Observer((list) => onEntries(list.getEntries(), type));
        // Assigned first: a callback during observe (buffered entries) must be able to disconnect this one.
        observer = inst;
        inst.observe({ type, buffered: true });
        return;
      } catch {
        try {
          inst?.disconnect();
        } catch {
          // this type is not actually observable
        }
        if (observer === inst) observer = null;
        if (reported) return;
      }
    }
  }

  function setEnabled(on: boolean) {
    if (reported) return;
    if (on) connect();
    else disconnectObserver();
  }

  doc.addEventListener("visibilitychange", onVisibility);
  doc.addEventListener("freeze", onFreeze);
  doc.addEventListener("resume", onResume);
  // A subscriber that calls back before returning can report (and tear down) before this assignment lands.
  unsubscribe = opts.subscribe?.(() => setEnabled(opts.enabled()));
  if (reported) {
    if (typeof unsubscribe === "function") unsubscribe();
    unsubscribe = undefined;
  } else {
    setEnabled(opts.enabled());
  }
  return Object.assign(teardown, { rearm });
}
