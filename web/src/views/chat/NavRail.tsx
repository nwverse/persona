// The chat's left-gutter user-input navigator. Only this small component updates while scrolling.
// cached content offsets are rebuilt on transcript/layout changes, never once per tick on a scroll frame.
import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { activeNavRailOffset, navRailMark, NAV_RAIL_MIN_HEIGHT, planNavRailTicks, type NavRailTick } from "../../lib/nav-rail";
import { useFinePointer } from "../../lib/viewport";
import { NAV_RAIL_METRICS as M } from "../../ui/nav-rail-metrics";
import { cx } from "../../ui/atoms";
import { t } from "../../i18n";
import type { QuestionMark, QuestionState } from "../../lib/question-states";

/** A question's state as its tick draws it (#13021): read keeps the plain rail's colors. */
const STATE_TICK: Record<QuestionState, string> = {
  pending: "bg-ink-3",
  running: "bg-accent animate-pulse busy-motion", // a busy indicator: slower, not frozen, under reduced motion (lib/busy-motion.ts)
  unread: "bg-ink",
  read: "",
};

const stateWord = (state: QuestionState) => t(`chat-navrail.state_${state}`);

/** Throttle observer-driven re-measurements so streaming DOM growth does not force full layout every frame. */
export const NAV_RAIL_OBSERVER_MS = 250;

function labelOf(tick: NavRailTick): string {
  if (tick.preview) return tick.preview;
  if (tick.attachmentsOnly) return t("chat-navrail.attachments", { count: tick.attachmentsOnly });
  return !tick.answerExcerpt && tick.ordinal ? t("chat-navrail.input", { index: tick.ordinal }) : "";
}

/** Where the chat's line wrappers (ChatLineView's `[data-line]` boxes) are: the content element's own children, or the
 *  scroller's when it has no content element. A measure reads those children instead of querying the whole transcript
 *  subtree, which grows with every message's markup. */
function lineParent(scroller: HTMLElement): Element {
  for (const el of scroller.children) if (el instanceof HTMLElement && el.dataset.chatContent !== undefined) return el;
  return scroller;
}

const EMPTY_TRACKED: ReadonlySet<string> = new Set();

function isTrackedLine(node: Node, trackedIds: ReadonlySet<string>): boolean {
  return node instanceof HTMLElement && node.dataset.line !== undefined && trackedIds.has(node.dataset.line);
}

function isAfter(lastLine: HTMLElement, node: Node, parent: Element): boolean {
  if (node.parentNode !== parent) return false;
  const pos = lastLine.compareDocumentPosition(node);
  return (pos & Node.DOCUMENT_POSITION_DISCONNECTED) === 0 && (pos & Node.DOCUMENT_POSITION_FOLLOWING) !== 0;
}

/** True when every mutation is strictly inside the last measured tick's body or in untracked tail siblings after it
 *  (including streaming assistant `[data-line]` replies below the last user tick), so no tracked tick offset can shift. */
function isTailOnlyMutation(
  records: MutationRecord[],
  parent: Element,
  lastLine: HTMLElement | null,
  trackedIds: ReadonlySet<string>,
): boolean {
  if (!lastLine || lastLine.parentElement !== parent) return false;
  for (const record of records) {
    if (record.target === parent) {
      for (const node of record.addedNodes) if (isTrackedLine(node, trackedIds)) return false;
      for (const node of record.removedNodes) if (isTrackedLine(node, trackedIds)) return false;
      const prev = record.previousSibling;
      if (!prev) return false;
      if (prev !== lastLine && !isAfter(lastLine, prev, parent)) return false;
      continue;
    }
    let child: Node | null = record.target;
    while (child && child.parentNode !== parent) child = child.parentNode;
    if (!child) return false;
    if (child === lastLine) {
      // Replacing the line wrapper's direct child swaps the measured row element itself.
      if (record.target === lastLine) return false;
      continue;
    }
    if (isTrackedLine(child, trackedIds)) return false;
    if (!isAfter(lastLine, child, parent)) return false;
  }
  return true;
}

export function NavRail({ ticks, total, scroller, onJump, reset, marks }: {
  ticks: NavRailTick[];
  total?: number;
  scroller: HTMLDivElement | null;
  onJump: (id: string) => void | Promise<void>;
  reset: string;
  /** Each question's state by line id in Persona chats. A follow-up's tick is shorter. These marks also allow touch
   *  navigation when the gutter fits the rail. */
  marks?: ReadonlyMap<string, QuestionMark>;
}) {
  const finePointer = useFinePointer();
  /** A touch screen's rail: only with question states (the plain rail stays off without a precise pointer). */
  const touch = !finePointer && Boolean(marks);
  const enabled = finePointer || touch;
  const markOf = (tick: NavRailTick | undefined) => (tick ? marks?.get(tick.lineId ?? tick.id) : undefined);
  const [height, setHeight] = useState(0);
  const [active, setActive] = useState<string | null>(null);
  const [pending, setPending] = useState<string | null>(null);
  const [preview, setPreview] = useState<string | null>(null);
  const [windowCenter, setWindowCenter] = useState<string | null>(null);
  const [announcement, setAnnouncement] = useState("");
  const card = useRef<HTMLDivElement>(null);
  const frame = useRef(0);
  const jumpRun = useRef(0);
  const hovered = useRef(false);
  const focused = useRef(false);
  const drag = useRef<{ pointer: number; ids: string[]; pitch: number; top: number } | null>(null);
  const selected = useRef<string | null>(null);
  const previewOrigin = useRef<"pointer" | "keyboard" | "drag" | null>(null);
  const suppressClick = useRef(false);
  const list = useRef(ticks);
  list.current = ticks;
  const anchorList = useRef<NavRailTick[]>([]);
  const dirty = useRef(true);
  const checkResize = useRef(false);
  const pendingObserver = useRef<"mutation" | "height" | null>(null);
  const lastContentWidth = useRef<number | null>(null);
  const lastTick = useRef<{ id: string; line: HTMLElement; row: HTMLElement; top: number } | null>(null);
  const trackedIds = useRef<ReadonlySet<string>>(EMPTY_TRACKED);
  const observerTimer = useRef(0);
  const lastObserverRun = useRef(-Infinity);
  const offsets = useRef<{ index: number; top: number }[]>([]);
  const previewId = useId();

  const resetInteraction = useCallback(() => {
    hovered.current = focused.current = false;
    drag.current = null;
    selected.current = null;
    previewOrigin.current = null;
    suppressClick.current = false;
    setPreview(null);
    setWindowCenter(null);
    setAnnouncement("");
  }, []);

  const measure = useCallback(() => {
    frame.current = 0;
    if (!scroller || !enabled || !list.current.length) return;
    if (!dirty.current && checkResize.current) {
      checkResize.current = false;
      const tail = lastTick.current;
      const last = list.current[list.current.length - 1];
      const lastId = last?.lineId ?? last?.id;
      const content = lineParent(scroller);
      if (
        !tail || tail.id !== lastId || tail.line.parentElement !== content || tail.line.firstElementChild !== tail.row
        || Math.abs((tail.row.getBoundingClientRect().top - scroller.getBoundingClientRect().top + scroller.scrollTop) - tail.top) >= 0.5
      ) {
        dirty.current = true;
      }
    }
    if (dirty.current) {
      dirty.current = false;
      checkResize.current = false;
      pendingObserver.current = null;
      if (observerTimer.current) {
        clearTimeout(observerTimer.current);
        observerTimer.current = 0;
      }
      const content = lineParent(scroller);
      const availableHeight = scroller.clientHeight - M.padding * 2;
      // The centered column's outer margin is the available gutter. clientWidth excludes the right scrollbar.
      const gutter = (scroller.clientWidth - content.clientWidth) / 2;
      if (gutter < M.edgeGap + M.width + M.contentGap || availableHeight < NAV_RAIL_MIN_HEIGHT) {
        offsets.current = [];
        lastTick.current = null;
        trackedIds.current = EMPTY_TRACKED;
        setHeight(0);
        setActive(null);
        resetInteraction();
        return;
      }
      setHeight(availableHeight);
      const rows = new Map<string, { line: HTMLElement; row: HTMLElement }>();
      for (const el of content.children) {
        const child = el.firstElementChild;
        if (el instanceof HTMLElement && el.dataset.line !== undefined && child instanceof HTMLElement) rows.set(el.dataset.line, { line: el, row: child });
      }
      const top = scroller.getBoundingClientRect().top;
      let nextLast: { id: string; line: HTMLElement; row: HTMLElement; top: number } | null = null;
      const tracked = new Set<string>();
      const last = list.current[list.current.length - 1];
      const lastId = last?.lineId ?? last?.id;
      offsets.current = list.current.flatMap((tick, index) => {
        const key = tick.lineId ?? tick.id;
        tracked.add(key);
        const found = rows.get(key);
        if (!found) return [];
        const offsetTop = found.row.getBoundingClientRect().top - top + scroller.scrollTop;
        if (key === lastId) nextLast = { id: key, line: found.line, row: found.row, top: offsetTop };
        return [{ index, top: offsetTop }];
      });
      trackedIds.current = tracked;
      lastTick.current = nextLast;
    }
    const index = activeNavRailOffset(offsets.current, scroller.scrollTop + M.activeOffset);
    setActive(index === null ? null : list.current[index]?.id ?? null);
  }, [scroller, enabled, resetInteraction]);

  const schedule = useCallback(() => {
    if (!frame.current) frame.current = requestAnimationFrame(measure);
  }, [measure]);

  useLayoutEffect(() => {
    const previous = anchorList.current;
    anchorList.current = ticks;
    // Streaming and indexed previews can update the text without moving any anchor.
    if (previous.length === ticks.length && previous.every((tick, i) => tick.id === ticks[i].id && tick.lineId === ticks[i].lineId)) return;
    dirty.current = true;
    schedule();
  }, [ticks, schedule]);

  const hasTicks = ticks.length > 0;
  useEffect(() => {
    if (!scroller || !enabled || !hasTicks) return;
    const parent = lineParent(scroller);
    const content = parent === scroller ? scroller.firstElementChild : parent;
    const runObserver = () => {
      const pending = pendingObserver.current;
      pendingObserver.current = null;
      if (!pending) return;
      lastObserverRun.current = performance.now();
      if (pending === "mutation") dirty.current = true;
      else checkResize.current = true;
      schedule();
    };
    const armObserver = (immediate: boolean) => {
      const elapsed = performance.now() - lastObserverRun.current;
      if (immediate && elapsed >= NAV_RAIL_OBSERVER_MS) {
        if (observerTimer.current) {
          clearTimeout(observerTimer.current);
          observerTimer.current = 0;
        }
        runObserver();
        return;
      }
      if (observerTimer.current) return;
      const wait = immediate ? Math.max(0, NAV_RAIL_OBSERVER_MS - elapsed) : NAV_RAIL_OBSERVER_MS;
      observerTimer.current = window.setTimeout(() => {
        observerTimer.current = 0;
        runObserver();
      }, wait);
    };
    const onResize: ResizeObserverCallback = (entries) => {
      let structural = !entries.length;
      for (const entry of entries) {
        if (entry.target === scroller) structural = true;
        if (entry.target === content) {
          const width = entry.contentRect?.width;
          // Compare ResizeObserver content-box widths. clientWidth also includes the transcript's padding.
          if (width === undefined || lastContentWidth.current === null || Math.abs(width - lastContentWidth.current) >= 0.5) structural = true;
          lastContentWidth.current = width ?? null;
        }
      }
      if (structural) {
        dirty.current = true;
        schedule();
        return;
      }
      if (offsets.current.length) {
        if (pendingObserver.current !== "mutation") pendingObserver.current = "height";
        armObserver(false);
      }
    };
    const onMutation: MutationCallback = (records) => {
      if (!trackedIds.current.size) return;
      if (isTailOnlyMutation(records, parent, lastTick.current?.line ?? null, trackedIds.current)) return;
      pendingObserver.current = "mutation";
      armObserver(true);
    };
    const scroll = () => { if (offsets.current.length) schedule(); };
    scroller.addEventListener("scroll", scroll, { passive: true });
    const resize = new ResizeObserver(onResize);
    resize.observe(scroller);
    if (content) resize.observe(content);
    // Disclosure and equal-height replacements can change anchors without changing the outer content height.
    const mutations = new MutationObserver(onMutation);
    if (content) mutations.observe(content, { childList: true, subtree: true });
    schedule();
    return () => {
      scroller.removeEventListener("scroll", scroll);
      lastContentWidth.current = null;
      lastTick.current = null;
      trackedIds.current = EMPTY_TRACKED;
      checkResize.current = false;
      pendingObserver.current = null;
      resize.disconnect();
      mutations.disconnect();
      clearTimeout(observerTimer.current);
      observerTimer.current = 0;
      if (frame.current) cancelAnimationFrame(frame.current);
      frame.current = 0;
    };
  }, [scroller, enabled, hasTicks, schedule]);

  useLayoutEffect(() => {
    resetInteraction();
    jumpRun.current++;
    setActive(null);
    setPending(null);
    setHeight(0);
    lastTick.current = null;
    trackedIds.current = EMPTY_TRACKED;
    checkResize.current = false;
    dirty.current = true;
    schedule();
  }, [reset, finePointer, schedule, resetInteraction]);

  useEffect(() => () => {
    clearTimeout(observerTimer.current);
    observerTimer.current = 0;
    jumpRun.current++;
    if (frame.current) cancelAnimationFrame(frame.current);
    frame.current = 0;
  }, []);

  const indexOf = (id: string | null) => id === null ? -1 : ticks.findIndex((tick) => tick.id === id);
  const marked = pending ?? active;
  const center = indexOf(windowCenter ?? marked);
  const plan = planNavRailTicks(ticks.length, height, center < 0 ? ticks.length - 1 : center);
  const shown = ticks.slice(plan.start, plan.start + plan.count);
  const previewIndex = indexOf(preview);
  const previewTick = previewIndex >= plan.start && previewIndex < plan.start + plan.count ? ticks[previewIndex] : undefined;
  const currentIndex = indexOf(marked);
  const focusedMark = preview ?? pending;
  const focusedIndex = previewIndex >= 0 ? previewIndex : indexOf(pending);
  const focusTarget = previewTick?.id ?? (shown.some((tick) => tick.id === active) ? active : shown.at(-1)?.id);
  const previewMark = markOf(previewTick);
  const previewTitle = previewTick && labelOf(previewTick);
  const railHeight = height + M.padding * 2;
  const ticksTop = (railHeight - plan.count * plan.pitch) / 2;

  useLayoutEffect(() => {
    if (!card.current || !previewTick) return;
    const y = ticksTop + (previewIndex - plan.start + 0.5) * plan.pitch;
    const cardHeight = card.current.offsetHeight;
    card.current.style.top = `${Math.max(0, Math.min(y - cardHeight / 2, railHeight - cardHeight))}px`;
  }, [previewTick, previewIndex, previewMark, railHeight, ticksTop, plan.start, plan.pitch]);

  const dismissPreview = () => {
    previewOrigin.current = null;
    selected.current = null;
    setPreview(null);
  };
  const pick = (id: string, origin: "pointer" | "keyboard" | "drag" = "pointer") => {
    previewOrigin.current = origin;
    selected.current = id;
    setPreview(id);
  };
  const jump = async (id: string) => {
    if (!list.current.some((tick) => tick.id === id)) return;
    const run = ++jumpRun.current;
    setPending(id);
    const result = onJump(id);
    if (!result) {
      setPending(null);
      return;
    }
    try {
      await result;
    } finally {
      if (run === jumpRun.current) setPending(null);
    }
  };
  const pickY = (y: number) => {
    const bounds = drag.current;
    if (!bounds?.ids.length) return;
    const i = Math.max(0, Math.min(bounds.ids.length - 1, Math.floor((y - bounds.top) / bounds.pitch)));
    const id = bounds.ids[i];
    if (list.current.some((tick) => tick.id === id)) pick(id, "drag");
  };

  if (!enabled || !shown.length) return null;

  return (
    <nav
      aria-label={t("chat-navrail.rail")}
      aria-description={t("chat-navrail.keys")}
      aria-describedby={previewTick ? previewId : undefined}
      data-nav-rail
      data-nav-total={total ?? ticks.length}
      data-nav-source={total === undefined ? "loaded" : "server"}
      tabIndex={0}
      data-nav-touch={touch || undefined}
      className="group/nav pointer-events-none absolute inset-y-0 z-10 flex flex-col justify-center focus-visible:outline-none"
      style={{ left: M.edgeGap, width: M.width, touchAction: touch ? "none" : undefined }}
      onMouseEnter={() => {
        if (touch) return;
        hovered.current = true;
        if (!drag.current) setWindowCenter(preview ?? active);
      }}
      onMouseLeave={() => {
        if (touch) return;
        hovered.current = false;
        if (!drag.current) {
          if (previewOrigin.current === "pointer") dismissPreview();
          if (!focused.current) setWindowCenter(null);
        }
      }}
      onFocus={() => {
        focused.current = true;
        if (preview !== null || drag.current) return;
        const id = active ?? shown[shown.length - 1].id;
        pick(id, "keyboard");
        setWindowCenter(id);
      }}
      onBlur={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) {
          focused.current = false;
          if (!drag.current) {
            if (hovered.current) previewOrigin.current = "pointer";
            else {
              dismissPreview();
              setWindowCenter(null);
            }
          }
        }
      }}
      onKeyDown={(e) => {
        if (e.nativeEvent.isComposing || e.altKey || e.ctrlKey || e.metaKey) return;
        const old = indexOf(preview ?? active);
        let next: number | null = null;
        if (e.key === "ArrowDown" || e.key === "ArrowRight") next = Math.min(ticks.length - 1, Math.max(0, old + 1));
        if (e.key === "ArrowUp" || e.key === "ArrowLeft") next = Math.max(0, old - 1);
        if (e.key === "Home") next = 0;
        if (e.key === "End") next = ticks.length - 1;
        if (next !== null) {
          e.preventDefault();
          e.stopPropagation();
          pick(ticks[next].id, "keyboard");
          setWindowCenter(ticks[next].id);
          const tick = ticks[next];
          const m = markOf(tick);
          const title = labelOf(tick);
          const answer = tick.answerExcerpt ?? "";
          const said = t("chat-navrail.selection", { index: tick.ordinal ?? next + 1, count: total ?? ticks.length, preview: title || answer, answer: title ? answer : "" });
          setAnnouncement(m ? `${said} ${stateWord(m.state)}` : said);
        } else if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          e.stopPropagation();
          const id = preview ?? active ?? shown[shown.length - 1].id;
          jump(id);
        } else if (e.key === "Escape") {
          e.preventDefault();
          e.stopPropagation();
          drag.current = null;
          dismissPreview();
          setWindowCenter(null);
        }
      }}
      onPointerDown={(e) => {
        if (e.button !== 0 || card.current?.contains(e.target as Node)) return;
        e.preventDefault();
        const rect = e.currentTarget.getBoundingClientRect();
        drag.current = { pointer: e.pointerId, ids: shown.map((tick) => tick.id), pitch: plan.pitch, top: rect.top + ticksTop };
        setWindowCenter(windowCenter ?? marked ?? shown[shown.length - 1].id);
        suppressClick.current = true;
        e.currentTarget.setPointerCapture(e.pointerId);
        pickY(e.clientY);
      }}
      onPointerMove={(e) => { if (drag.current?.pointer === e.pointerId) pickY(e.clientY); }}
      onPointerUp={(e) => {
        if (drag.current?.pointer !== e.pointerId) return;
        pickY(e.clientY);
        drag.current = null;
        e.currentTarget.releasePointerCapture(e.pointerId);
        if (selected.current) jump(selected.current);
        previewOrigin.current = "pointer";
        if (!hovered.current) {
          dismissPreview();
          if (!focused.current) setWindowCenter(null);
        }
      }}
      onPointerCancel={() => { drag.current = null; suppressClick.current = false; dismissPreview(); setWindowCenter(null); }}
      onLostPointerCapture={() => { if (drag.current) { drag.current = null; dismissPreview(); setWindowCenter(null); } }}
      onWheel={(e) => {
        if (plan.hidden) {
          if (!e.deltaY) return;
          // Adjacent wheel windows share a tick so every position can be passed in either direction.
          const step = Math.max(1, plan.count - 1);
          const i = Math.max(0, Math.min(ticks.length - 1, (center < 0 ? ticks.length - 1 : center) + Math.sign(e.deltaY) * step));
          setWindowCenter(ticks[i].id);
          pick(ticks[i].id);
        } else if (scroller) {
          const unit = e.deltaMode === 1 ? M.tickPitch : e.deltaMode === 2 ? railHeight : 1;
          scroller.scrollBy({ top: e.deltaY * unit, left: e.deltaX * unit });
        }
      }}
    >
      {pending && (
        <span role="status" data-nav-loading className="pointer-events-none absolute left-full top-0 whitespace-nowrap rounded-nav-preview border border-line bg-surface px-2 py-1 text-sm text-ink shadow-nav-preview">
          {t("chat-navrail.loading_target")}
        </span>
      )}
      <span className="sr-only" aria-live="polite" aria-atomic="true">{announcement}</span>
      {shown.map((tick, k) => {
        const i = plan.start + k;
        const isCurrent = tick.id === active;
        const isPreview = tick.id === focusedMark;
        const emphasized = isCurrent || tick.id === pending;
        const mark = navRailMark(i, ticks.length, currentIndex < 0 ? null : currentIndex, focusedIndex < 0 ? null : focusedIndex);
        const q = markOf(tick);
        const label = t("chat-navrail.jump", { index: tick.ordinal ?? i + 1, preview: labelOf(tick) || tick.answerExcerpt || "" });
        const stateTone = q && !emphasized && !isPreview ? STATE_TICK[q.state] : "";
        return (
          <button
            key={tick.id}
            type="button"
            data-tick={i}
            tabIndex={-1}
            aria-label={q ? t(q.follows ? "chat-navrail.jump_follow_state" : "chat-navrail.jump_state", { label, state: stateWord(q.state) }) : label}
            data-question-state={q?.state}
            data-question-follow={q?.follows ? "" : undefined}
            aria-current={isCurrent ? "true" : undefined}
            aria-describedby={tick.id === preview ? previewId : undefined}
            className={cx("pointer-events-auto relative flex shrink-0 items-center", tick.id === focusTarget && "rounded-control group-focus-visible/nav:outline group-focus-visible/nav:-outline-offset-1 group-focus-visible/nav:outline-accent")}
            style={{ height: plan.pitch, paddingLeft: M.tickInset }}
            onMouseEnter={() => { if (!touch) pick(tick.id); }}
            onClick={() => {
              if (suppressClick.current) { suppressClick.current = false; return; }
              jump(tick.id);
            }}
          >
            <span
              aria-hidden
              data-nav-mark={mark.state}
              className={cx("shrink-0 rounded-full transition-[width,height,background-color,opacity] duration-150 motion-reduce:transition-none", stateTone || (emphasized ? "bg-ink" : isPreview ? "bg-ink-2" : "bg-ink-3"))}
              style={{
                // a follow-up's tick is shorter, under the question it follows
                width: q?.follows ? mark.width * M.followWidth : mark.width,
                height: mark.height,
                opacity: emphasized || isPreview ? 1 : q?.state === "pending" ? mark.opacity * M.pendingOpacity : q?.state === "running" || q?.state === "unread" ? Math.max(mark.opacity, M.stateOpacity) : mark.opacity,
              }}
            />
            {q?.state === "unread" && <span aria-hidden data-question-dot className="absolute left-0 top-1/2 -translate-y-1/2 rounded-full bg-red" style={{ width: M.dotSize, height: M.dotSize }} />}
          </button>
        );
      })}
      {previewTick && (
        <div
          ref={card}
          id={previewId}
          data-nav-preview
          data-nav-rail-preview
          className="pointer-events-auto absolute z-20"
          // Padding connects the card's hit box to the rail without a timer or an unhoverable gap.
          style={{ left: M.width, width: M.previewWidth + M.previewGap, paddingLeft: M.previewGap }}
        >
          <div data-nav-preview-surface className="overflow-hidden rounded-nav-preview border border-line bg-surface shadow-nav-preview" style={{ maxHeight: railHeight, padding: M.previewPadding }}>
            {(previewTitle || previewMark) && (
              <div className="flex items-baseline gap-2">
                {previewTitle && <p data-nav-preview-title className="min-w-0 flex-1 truncate text-sm font-semibold text-ink">{previewTitle}</p>}
                {previewMark && <p data-nav-preview-state className="max-w-1/2 shrink-0 truncate text-xs text-ink-3">{previewMark.follows ? t("chat-navrail.follow_state", { state: stateWord(previewMark.state) }) : stateWord(previewMark.state)}</p>}
              </div>
            )}
            {previewTick.answerExcerpt && <p data-nav-preview-excerpt className={cx("line-clamp-3 break-words text-sm leading-relaxed text-ink-2", (previewTitle || previewMark) && "mt-2")}>{previewTick.answerExcerpt}</p>}
          </div>
        </div>
      )}
    </nav>
  );
}
