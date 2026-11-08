// A frozen page: the main thread blocked for 5 seconds or more (docs/web/web.md "Error reports").
// One anr warning per page load, through the error-report pipeline, and only while the caller says the hub is
// advertising error_reports. long-animation-frame where the browser has it, otherwise longtask; with neither, nothing.
// A hidden tab is not a freeze, and neither is an entry that overlaps the page being hidden or frozen (visibilitychange,
// freeze / resume): a background tab, or the clock jumping while the machine sleeps, must not be reported. Script
// attribution is a URL path only.

import type { ErrorFrame } from "./extension-wire";
import { FREEZE_MS, scriptFrame } from "./error-reports";

const MAX_SCRIPTS = 8;

export type FreezeMechanism = "long-animation-frame" | "longtask";

export interface FreezeScript {
  sourceURL?: string;
  sourceFunctionName?: string;
  duration?: number;
}

/** A long task's frame attribution. containerId and containerName are never read: they are page content. */
export interface FreezeAttribution {
  containerSrc?: string;
}

export interface FreezeEntry {
  startTime: number;
  duration: number;
  scripts?: FreezeScript[];
  attribution?: FreezeAttribution[];
}

export interface FreezeHit {
  durationMs: number;
  mechanism: FreezeMechanism;
  frames: ErrorFrame[];
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
  return frames.slice(-MAX_SCRIPTS);
}

/** The entry is one freeze, or null when it is too short, the page is not in front, or it overlaps a gap. */
export function classifyFreeze(
  entry: FreezeEntry,
  opts: { mechanism: FreezeMechanism; origin: string; foreground: boolean; gaps: readonly FreezeGap[] },
): FreezeHit | null {
  if (!opts.foreground) return null;
  if (!Number.isFinite(entry.startTime) || !Number.isFinite(entry.duration) || entry.duration < FREEZE_MS) return null;
  if (freezeOverlaps(entry.startTime, entry.duration, opts.gaps)) return null;
  return { durationMs: entry.duration, mechanism: opts.mechanism, frames: freezeFrames(entry, opts.mechanism, opts.origin) };
}

interface RawEntry {
  startTime: number;
  duration: number;
  scripts?: FreezeScript[];
  attribution?: FreezeAttribution[];
}

function readEntry(raw: RawEntry): FreezeEntry {
  return {
    startTime: raw.startTime,
    duration: raw.duration,
    // Only the fields a report may carry. An invoker, a character offset, an element id or a name stays out.
    scripts: raw.scripts?.map((script) => ({
      sourceURL: typeof script.sourceURL === "string" ? script.sourceURL : undefined,
      sourceFunctionName: typeof script.sourceFunctionName === "string" ? script.sourceFunctionName : undefined,
      duration: typeof script.duration === "number" ? script.duration : undefined,
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
export function startFreezeWatch(opts: FreezeWatchOptions): () => void {
  const found = opts.document ?? browserDocument();
  if (!found) return () => {};
  const doc: FreezeDocument = found;
  const origin = opts.origin ?? pageOrigin();
  const now = opts.now ?? monoNow;
  const Observer = opts.Observer === undefined ? browserObserver() : opts.Observer;

  let hidden = doc.visibilityState !== "visible";
  let frozen = false;
  // Already hidden as the watch starts: the gap reaches back to navigation, so a buffered entry from this hidden
  // stretch is not a freeze. Visible: no gap until the page actually goes away.
  let openFrom: number | null = hidden ? 0 : null;
  const gaps: FreezeGap[] = [];
  const skipped = new Set<string>();
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

  function teardown() {
    reported = true;
    disconnectObserver();
    removeListeners();
    if (typeof unsubscribe === "function") unsubscribe();
    unsubscribe = undefined;
  }

  function onEntries(raws: RawEntry[], type: FreezeMechanism) {
    try {
      if (reported || !opts.enabled()) return;
      for (const raw of raws) {
        if (reported || !opts.enabled()) return;
        const entry = readEntry(raw);
        const id = `${type}:${entry.startTime}:${entry.duration}`;
        if (skipped.has(id)) continue;
        const hit = classifyFreeze(entry, { mechanism: type, origin, foreground: !hidden && !frozen, gaps: allGaps() });
        if (!hit) continue;
        let accepted = false;
        try {
          accepted = opts.report(hit) === true;
        } catch {
          accepted = false;
        }
        if (accepted) {
          teardown();
          return;
        }
        skipped.add(id);
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
  return teardown;
}
