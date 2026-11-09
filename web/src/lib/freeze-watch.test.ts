import { describe, expect, it } from "vitest";
import { FREEZE_MS } from "./error-reports";
import { classifyFreeze, startFreezeWatch, type FreezeEntry, type FreezeHit, type FreezeObserver, type FreezeObserverCtor } from "./freeze-watch";

const ORIGIN = "https://hub.example";

function fakeDocument(initial = "visible") {
  let visibilityState = initial;
  const listeners = new Map<string, Set<() => void>>();
  const emit = (type: string) => {
    for (const fn of [...(listeners.get(type) ?? [])]) fn();
  };
  return {
    get visibilityState() {
      return visibilityState;
    },
    addEventListener(type: string, fn: () => void) {
      let set = listeners.get(type);
      if (!set) listeners.set(type, (set = new Set()));
      set.add(fn);
    },
    removeEventListener(type: string, fn: () => void) {
      listeners.get(type)?.delete(fn);
    },
    hide() {
      visibilityState = "hidden";
      emit("visibilitychange");
    },
    show() {
      visibilityState = "visible";
      emit("visibilitychange");
    },
    /** The property updates before the event is delivered. */
    freeze() {
      emit("freeze");
    },
    resume() {
      emit("resume");
    },
    listenerCount() {
      return [...listeners.values()].reduce((n, set) => n + set.size, 0);
    },
  };
}

function setup(over: { types?: readonly string[]; throwOn?: string; visibility?: string; enabled?: boolean; interactiveAt?: () => number | null; wall?: () => number; timeOrigin?: () => number } = {}) {
  const created: Fake[] = [];
  class Fake implements FreezeObserver {
    static supportedEntryTypes: readonly string[] = over.types ?? ["long-animation-frame", "longtask"];
    opts: { type: string; buffered?: boolean } | null = null;
    constructor(private cb: (list: { getEntries(): FreezeEntry[] }) => void) {
      created.push(this);
    }
    observe(opts: { type: string; buffered?: boolean }) {
      if (over.throwOn === opts.type) throw new Error("unsupported");
      this.opts = opts;
    }
    disconnect() {
      this.opts = null;
    }
    emit(entries: FreezeEntry[]) {
      this.cb({ getEntries: () => entries });
    }
  }
  const doc = fakeDocument(over.visibility ?? "visible");
  const hits: FreezeHit[] = [];
  let enabled = over.enabled ?? true;
  let accept = true;
  let clock = 0;
  const subs = new Set<() => void>();
  const stop = startFreezeWatch({
    report: (hit) => {
      hits.push(hit);
      return accept;
    },
    enabled: () => enabled,
    subscribe: (listener) => {
      subs.add(listener);
      return () => subs.delete(listener);
    },
    origin: ORIGIN,
    now: () => clock,
    wall: over.wall,
    timeOrigin: over.timeOrigin,
    interactiveAt: over.interactiveAt,
    document: doc,
    Observer: Fake as unknown as FreezeObserverCtor,
  });
  const active = () => created.filter((observer) => observer.opts);
  return {
    doc,
    hits,
    created,
    active,
    stop,
    setEnabled(on: boolean) {
      enabled = on;
      for (const listener of [...subs]) listener();
    },
    setAccept(on: boolean) {
      accept = on;
    },
    at(ms: number) {
      clock = ms;
    },
  };
}

const long = (start: number, duration: number, extra: Partial<FreezeEntry> = {}): FreezeEntry => ({ startTime: start, duration, ...extra });

describe("classifyFreeze", () => {
  it("needs 5 seconds in the foreground, clear of a gap", () => {
    const base = { mechanism: "longtask" as const, origin: ORIGIN, foreground: true, gaps: [] as { start: number; end: number | null }[] };
    expect(classifyFreeze(long(0, FREEZE_MS - 1), base)).toBeNull();
    expect(classifyFreeze(long(0, FREEZE_MS), base)?.durationMs).toBe(FREEZE_MS);
    expect(classifyFreeze(long(0, 9_000), { ...base, foreground: false })).toBeNull();
    expect(classifyFreeze(long(0, 9_000), { ...base, gaps: [{ start: 1_000, end: 2_000 }] })).toBeNull();
    expect(classifyFreeze(long(2_000, FREEZE_MS), { ...base, gaps: [{ start: 0, end: 2_000 }] })?.durationMs).toBe(FREEZE_MS);
    expect(classifyFreeze(long(0, Number.NaN), base)).toBeNull();
  });
  it("drops the first load, a debugger pause and a sleep, and keeps the block that remains", () => {
    const base = { mechanism: "long-animation-frame" as const, origin: ORIGIN, foreground: true, gaps: [] as { start: number; end: number | null }[] };
    expect(classifyFreeze(long(0, 8_000), { ...base, interactiveAt: 0 })).toBeNull();
    expect(classifyFreeze(long(10, 8_000), { ...base, interactiveAt: 1_000 })).toBeNull();
    expect(classifyFreeze(long(1_000, FREEZE_MS), { ...base, interactiveAt: 1_000 })?.durationMs).toBe(FREEZE_MS);
    const paused = long(2_000, 12_000, { scripts: [{ sourceURL: `${ORIGIN}/assets/a.js`, pauseDuration: 8_000, sourceFunctionName: "onClick" }] });
    expect(classifyFreeze(paused, { ...base, interactiveAt: 1 })).toBeNull();
    expect(classifyFreeze(long(2_000, 7_000, { scripts: [{ sourceURL: `${ORIGIN}/assets/a.js`, pauseDuration: 1_000 }] }), { ...base, interactiveAt: 1 })?.durationMs).toBe(6_000);
    expect(classifyFreeze(long(2_000, 21_000), { ...base, sleepMs: 20_000 })).toBeNull();
    expect(classifyFreeze(long(2_000, 26_000), { ...base, sleepMs: 20_000 })?.durationMs).toBe(6_000);
    // the frame waited; the thread did not block
    expect(classifyFreeze({ ...long(2_000, 8_000), blockingDuration: 20 }, base)).toBeNull();
    expect(classifyFreeze({ ...long(2_000, 8_000), blockingDuration: 6_000 }, base)?.durationMs).toBe(6_000);
  });
});

describe("startFreezeWatch", () => {
  it("uses long-animation-frame when the browser has it, buffered, and reports one freeze", () => {
    const w = setup();
    expect(w.active()).toHaveLength(1);
    expect(w.active()[0].opts).toEqual({ type: "long-animation-frame", buffered: true });
    w.active()[0].emit([
      long(0, 40),
      long(10, 6_000, {
        scripts: [
          { sourceURL: `${ORIGIN}/assets/short.js?token=t0k`, sourceFunctionName: "small", duration: 100 },
          { sourceURL: "//cdn.example/proto.js", sourceFunctionName: "leaked", duration: 4_000 },
          { sourceURL: "data:text/javascript,secret", sourceFunctionName: "secret", duration: 9_000 },
          Object.assign({ sourceURL: "https://user:s3cret@cdn.example/lib.js?user=ada&token=t0k", sourceFunctionName: "me@example.com", duration: 500 }, { invoker: "https://user:s3cret@cdn.example/on.js", containerId: "s3cret-element" }),
          { sourceURL: `${ORIGIN}/assets/hot.js`, sourceFunctionName: "onClick", duration: 5_000 },
        ],
        attribution: [{ containerSrc: `${ORIGIN}/assets/should-not-use.js` }],
      }),
      long(20, 8_000),
    ]);
    expect(w.hits).toHaveLength(1);
    expect(w.hits[0].mechanism).toBe("long-animation-frame");
    expect(w.hits[0].durationMs).toBe(6_000);
    expect(w.hits[0].frames).toEqual([
      { file: "/assets/short.js", module: "/assets/short.js", function: "small", in_app: true },
      { file: "/lib.js", module: "/lib.js", in_app: false },
      { file: "/assets/hot.js", module: "/assets/hot.js", function: "onClick", in_app: true },
    ]);
    expect(JSON.stringify(w.hits[0])).not.toContain("token");
    expect(JSON.stringify(w.hits[0])).not.toContain("secret");
    expect(JSON.stringify(w.hits[0])).not.toContain("ada");
    expect(JSON.stringify(w.hits[0])).not.toContain("cdn.example");
    expect(JSON.stringify(w.hits[0])).not.toContain("s3cret");
    expect(JSON.stringify(w.hits[0])).not.toContain("invoker");
    expect(JSON.stringify(w.hits[0])).not.toContain("should-not-use");
    expect(JSON.stringify(w.hits[0])).not.toContain("proto");
    expect(JSON.stringify(w.hits[0])).not.toContain("leaked");
    expect(w.active()).toHaveLength(0);
    w.created[0].emit([long(30, 9_000)]);
    expect(w.hits).toHaveLength(1);
    w.stop();
  });

  it("falls back to longtask, and to it when long-animation-frame cannot be observed", () => {
    const only = setup({ types: ["longtask", "mark"] });
    expect(only.active()[0].opts?.type).toBe("longtask");
    only.active()[0].emit([
      long(0, 5_000, {
        attribution: [{ containerSrc: `${ORIGIN}/assets/frame.js?id=secret`, containerId: "secret-element", containerName: "Ada" } as { containerSrc: string }],
        scripts: [{ sourceURL: `${ORIGIN}/assets/not-for-longtask.js`, sourceFunctionName: "render" }],
      }),
    ]);
    expect(only.hits[0].mechanism).toBe("longtask");
    expect(only.hits[0].frames).toEqual([{ file: "/assets/frame.js", module: "/assets/frame.js", in_app: true }]);
    expect(JSON.stringify(only.hits[0])).not.toContain("secret");
    expect(JSON.stringify(only.hits[0])).not.toContain("Ada");
    expect(JSON.stringify(only.hits[0])).not.toContain("not-for-longtask");
    only.stop();

    const fallback = setup({ throwOn: "long-animation-frame" });
    expect(fallback.created).toHaveLength(2);
    expect(fallback.active().map((observer) => observer.opts?.type)).toEqual(["longtask"]);
    fallback.stop();
  });

  it("does not watch while the feature is off, and watches again when it comes on", () => {
    const w = setup({ enabled: false });
    expect(w.created).toHaveLength(0);
    w.setEnabled(true);
    expect(w.active()).toHaveLength(1);
    w.setEnabled(false);
    expect(w.active()).toHaveLength(0);
    w.created[0].emit([long(0, 8_000)]);
    expect(w.hits).toHaveLength(0);
    w.setEnabled(true);
    w.active()[0].emit([long(0, 8_000)]);
    expect(w.hits).toHaveLength(1);
    w.stop();
  });

  it("ignores a hidden tab and a block that overlaps being hidden", () => {
    const w = setup();
    w.doc.hide();
    w.active()[0].emit([long(0, 8_000)]);
    expect(w.hits).toHaveLength(0);
    w.at(1_000);
    w.doc.show();
    // the hidden stretch is [0, 1000]; a task that started inside it is not a freeze
    w.active()[0].emit([long(500, 6_000)]);
    expect(w.hits).toHaveLength(0);
    w.active()[0].emit([long(1_000, FREEZE_MS)]);
    expect(w.hits).toHaveLength(1);
    w.stop();
  });

  it("ignores a gap from the page being frozen, including one that was already hidden", () => {
    const w = setup();
    w.at(50);
    w.doc.freeze();
    w.at(20_000);
    w.doc.resume();
    w.active()[0].emit([long(0, 10_000)]);
    expect(w.hits).toHaveLength(0);
    w.active()[0].emit([long(20_000, FREEZE_MS)]);
    expect(w.hits).toHaveLength(1);
    w.stop();

    const hidden = setup({ visibility: "hidden" });
    expect(hidden.active()).toHaveLength(1);
    hidden.active()[0].emit([long(0, 9_000)]);
    expect(hidden.hits).toHaveLength(0);
    hidden.at(300);
    hidden.doc.show();
    hidden.active()[0].emit([long(0, 9_000)]);
    expect(hidden.hits).toHaveLength(0);
    hidden.active()[0].emit([long(300, FREEZE_MS)]);
    expect(hidden.hits).toHaveLength(1);
    hidden.stop();
  });

  it("does not spend the one report when the pipeline refuses it", () => {
    const w = setup();
    w.setAccept(false);
    w.active()[0].emit([long(0, 6_000), long(10, 7_000)]);
    expect(w.hits).toHaveLength(2);
    expect(w.active()).toHaveLength(1);
    w.setAccept(true);
    // the same entry is not remembered as skipped: a later try, once the pipeline accepts, is the one report
    w.active()[0].emit([long(0, 6_000), long(30, 8_000)]);
    expect(w.hits).toHaveLength(3);
    expect(w.hits[2].durationMs).toBe(6_000);
    expect(w.active()).toHaveLength(0);
    w.stop();
  });

  it("does nothing where the browser has neither entry type, and stop removes the listeners", () => {
    const w = setup({ types: ["mark"] });
    expect(w.created).toHaveLength(0);
    expect(w.doc.listenerCount()).toBeGreaterThan(0);
    w.stop();
    expect(w.doc.listenerCount()).toBe(0);
    w.doc.hide();
    w.setEnabled(true);
    expect(w.created).toHaveLength(0);
  });

  it("disconnects when a buffered entry is delivered during observe", () => {
    const created: { opts: { type: string; buffered?: boolean } | null }[] = [];
    class Sync implements FreezeObserver {
      static supportedEntryTypes = ["long-animation-frame"];
      opts: { type: string; buffered?: boolean } | null = null;
      constructor(private cb: (list: { getEntries(): FreezeEntry[] }) => void) {
        created.push(this);
      }
      observe(opts: { type: string; buffered?: boolean }) {
        this.opts = opts;
        this.cb({ getEntries: () => [long(0, FREEZE_MS)] });
      }
      disconnect() {
        this.opts = null;
      }
    }
    const hits: FreezeHit[] = [];
    const doc = fakeDocument();
    const stop = startFreezeWatch({
      report: (hit) => {
        hits.push(hit);
        return true;
      },
      enabled: () => true,
      origin: ORIGIN,
      now: () => 0,
      document: doc,
      Observer: Sync as unknown as FreezeObserverCtor,
    });
    expect(hits).toHaveLength(1);
    expect(created[0].opts).toBeNull();
    stop();
  });

  it("keeps at most the eight longest scripts, longest last", () => {
    const scripts = Array.from({ length: 10 }, (_, i) => ({
      sourceURL: `${ORIGIN}/assets/s${i}.js`,
      sourceFunctionName: `f${i}`,
      duration: i * 10,
    }));
    const w = setup();
    w.active()[0].emit([long(0, 5_000, { scripts })]);
    expect(w.hits[0].frames.map((frame) => frame.function)).toEqual(["f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9"]);
    w.stop();
  });

  it("does not spend the one report on the first load, a breakpoint or a sleep, then reports a real block", () => {
    let interactive = 0;
    let wall = 1_000_000;
    const w = setup({ interactiveAt: () => interactive, wall: () => wall });
    w.active()[0].emit([long(0, 9_000)]);
    expect(w.hits).toHaveLength(0);
    interactive = 500;
    // started while the document was still loading
    w.active()[0].emit([long(0, 9_000)]);
    // a breakpoint accounts for most of a later long frame
    w.active()[0].emit([long(500, 12_000, { scripts: [{ sourceURL: `${ORIGIN}/assets/app.js`, sourceFunctionName: "onClick", pauseDuration: 8_000 }] })]);
    expect(w.hits).toHaveLength(0);
    wall += 30_000;
    w.active()[0].emit([long(500, 31_000)]);
    expect(w.hits).toHaveLength(0);
    expect(w.active()).toHaveLength(1);
    wall += 5;
    w.at(600);
    w.active()[0].emit([long(600, 5_200, { scripts: [{ sourceURL: `${ORIGIN}/assets/app.js`, sourceFunctionName: "onClick", pauseDuration: 200 }] })]);
    expect(w.hits).toHaveLength(1);
    expect(w.hits[0].durationMs).toBe(5_000);
    // the one report is spent even if the feature flips off and back on
    w.setEnabled(false);
    w.setEnabled(true);
    expect(w.active()).toHaveLength(0);
    w.stop();
  });

  it("keeps a stall that ended as the tab was hidden, and stamps when it started", () => {
    const w = setup({ timeOrigin: () => 1_700_000_000_000 });
    w.at(6_000);
    w.doc.hide();
    w.active()[0].emit([long(0, 6_000)]);
    expect(w.hits).toHaveLength(1);
    expect(w.hits[0].startedAt).toBe(1_700_000_000_000);
    // the hidden stretch starts at 6000, so it does not cover [0, 6000)
    w.stop();
  });

  it("watches again when a queued warning is dropped", () => {
    const w = setup();
    w.active()[0].emit([long(0, 6_000)]);
    expect(w.active()).toHaveLength(0);
    w.stop.rearm();
    expect(w.active()).toHaveLength(1);
    w.active()[0].emit([long(10, 6_000)]);
    expect(w.hits).toHaveLength(2);
    w.stop();
  });
});
