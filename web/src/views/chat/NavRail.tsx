// The question navigation rail (#1739, docs/web/web.md "Question rail"): in a wide chat with several of your questions
// loaded, a slim column of ticks in the margin beside the chat — one tick per question. Hovering (or focusing) a tick
// previews the question and the start of its answer, clicking jumps to that turn, and the tick of the turn being read
// is the long one. It never shows on a narrow chat area, a short conversation or a cramped viewport, so those look
// exactly as before; which turns become ticks and where they fit is lib/nav-rail.ts (pure, tested there).
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { NAV_RAIL_COLUMN, NAV_RAIL_MIN_GUTTER, NAV_RAIL_MIN_HEIGHT, NAV_RAIL_MIN_TICKS, hasNavRailRoom, pickActiveTick, planNavRailTicks, type NavRailTick } from "../../lib/nav-rail";
import { cx } from "../../ui/atoms";
import { t } from "../../i18n";

/** The rail's vertical padding (inside the chat area's height). */
const PAD_Y = 24;
/** How far under the scroller's top a line counts as read (past the jump's landing offset, so a jump that just landed
 *  keeps its own tick as the active one). */
const ACTIVE_FUDGE = 40;
/** Pointer activity this close to the chat's left edge wakes the rail (the ticks sit in that margin). */
const WAKE_GUTTER = 48;
/** Quiet for this long and the rail dims (it stays clickable); scrolling, the pointer or the focus wakes it. */
const IDLE_MS = 2000;
/** A clicked tick shows as active until the scroll lands (or this passes) even if the geometry has not caught up. */
const PENDING_MS = 3000;

/** The label a tick stands for: its question's words, or a count when it is only attachments. */
function labelOf(tick: NavRailTick): string {
  return tick.preview || (tick.attachmentsOnly ? t("chat-navrail.attachments", { count: tick.attachmentsOnly }) : "");
}

export function NavRail({ ticks, scroller, onJump, reset }: { ticks: NavRailTick[]; scroller: HTMLDivElement | null; onJump: (lineId: string) => void; /** mid/sid: another chat, everything starts over. */ reset: string }) {
  const [room, setRoom] = useState(false);
  const [height, setHeight] = useState(0);
  const [active, setActive] = useState<number | null>(null);
  const [pending, setPending] = useState<number | null>(null);
  const [preview, setPreview] = useState<{ i: number; y: number } | null>(null);
  const [idle, setIdle] = useState(false);

  const railRef = useRef<HTMLElement>(null);
  const cardRef = useRef<HTMLDivElement>(null);
  const raf = useRef(0);
  const idleTimer = useRef(0);
  const pendingTimer = useRef(0);
  // the scroller's left edge for the pointer-wake test, refreshed on measure (no layout read per pointermove)
  const leftEdge = useRef(0);
  // what holds the dim off: the pointer resting on the rail, or the focus in it (its preview card would dim with it)
  const hovering = useRef(false);
  const focused = useRef(false);
  const view = useRef(scroller);
  view.current = scroller;
  const list = useRef(ticks);
  list.current = ticks;

  const wake = useCallback(() => {
    setIdle(false);
    clearTimeout(idleTimer.current);
    idleTimer.current = window.setTimeout(() => {
      idleTimer.current = 0;
      // held while the pointer or the focus is on the rail: re-arm instead of dimming under it
      if (hovering.current || focused.current) return wake();
      setIdle(true);
    }, IDLE_MS);
  }, []);

  const measure = useCallback(() => {
    raf.current = 0;
    const sc = view.current;
    if (!sc) return;
    const box = sc.getBoundingClientRect();
    leftEdge.current = box.left;
    const fits = hasNavRailRoom(box.width);
    setRoom(fits);
    setHeight(Math.max(0, sc.clientHeight - PAD_Y * 2));
    const now = list.current;
    if (!fits || now.length < NAV_RAIL_MIN_TICKS) {
      setActive((x) => (x === null ? x : null));
      return;
    }
    // one pass over the drawn lines, then a rect read per tick (all reads, no writes between)
    const byId = new Map<string, HTMLElement>();
    for (const el of sc.querySelectorAll<HTMLElement>("[data-line]")) byId.set(el.dataset.line ?? "", el);
    setActive(pickActiveTick(now.length, box.top + ACTIVE_FUDGE, (i) => byId.get(now[i].id)?.firstElementChild?.getBoundingClientRect().top ?? null));
  }, []);

  const schedule = useCallback(() => {
    if (raf.current === 0) raf.current = requestAnimationFrame(measure);
  }, [measure]);

  // scrolling, resizing and the chat's own changes (a turn answered, history loaded) all re-measure
  useEffect(() => {
    schedule();
  }, [ticks, scroller, schedule]);

  useEffect(() => {
    const sc = view.current;
    if (!sc) return;
    const onScroll = () => {
      wake();
      schedule();
    };
    const onMove = (e: PointerEvent) => {
      if (e.clientX - leftEdge.current <= WAKE_GUTTER) wake();
    };
    sc.addEventListener("scroll", onScroll, { passive: true });
    sc.addEventListener("pointermove", onMove, { passive: true });
    const ro = new ResizeObserver(schedule);
    ro.observe(sc);
    return () => {
      sc.removeEventListener("scroll", onScroll);
      sc.removeEventListener("pointermove", onMove);
      ro.disconnect();
      if (raf.current) cancelAnimationFrame(raf.current);
      raf.current = 0;
    };
  }, [scroller, schedule, wake]);

  // another chat: no leftover active tick, hover or pending jump; show itself once as the where-you-are hint
  useEffect(() => {
    setActive(null);
    setPending(null);
    setPreview(null);
    wake();
    return () => {
      clearTimeout(idleTimer.current);
      clearTimeout(pendingTimer.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- reset only
  }, [reset]);

  // the scroll of a jump landed: the geometry's word takes over from the optimistic tick
  useEffect(() => {
    if (pending === null || pending !== active) return;
    setPending(null);
    clearTimeout(pendingTimer.current);
  }, [pending, active]);

  useEffect(() => () => {
    clearTimeout(idleTimer.current);
    clearTimeout(pendingTimer.current);
    if (raf.current) cancelAnimationFrame(raf.current);
  }, []);

  const jump = (i: number, id: string) => {
    setPending(i);
    clearTimeout(pendingTimer.current);
    pendingTimer.current = window.setTimeout(() => setPending(null), PENDING_MS);
    onJump(id);
  };

  // keep the preview card inside the rail once its height is known (before paint, so it never flashes outside)
  useLayoutEffect(() => {
    const card = cardRef.current;
    const rail = railRef.current;
    if (!card || !rail || !preview) return;
    const h = card.offsetHeight;
    card.style.top = `${Math.max(0, Math.min(preview.y - h / 2, rail.clientHeight - h))}px`;
  }, [preview]);

  if (!room || ticks.length < NAV_RAIL_MIN_TICKS || height < NAV_RAIL_MIN_HEIGHT) return null;

  const plan = planNavRailTicks(ticks.length, height);
  const shown = ticks.slice(plan.start);
  const at = preview?.i ?? -1;
  const current = pending ?? active;
  const pick = (i: number, el: HTMLElement) => {
    const rail = railRef.current;
    if (!rail) return;
    const y = el.getBoundingClientRect().top - rail.getBoundingClientRect().top + el.offsetHeight / 2;
    setPreview({ i, y });
    wake();
  };
  const unpick = () => setPreview(null);

  return (
    <nav
      ref={railRef}
      aria-label={t("chat-navrail.rail")}
      data-nav-rail
      // the container takes no events (the margin stays free for text selection); the ticks and the card do
      className={cx("pointer-events-none absolute inset-y-0 z-10 flex w-8 flex-col justify-center gap-0 py-6 transition-opacity duration-200", idle ? "opacity-35" : "opacity-100")}
      style={{ left: `calc((100% - ${NAV_RAIL_COLUMN}px) / 2 - ${NAV_RAIL_MIN_GUTTER}px)` }}
      onWheel={(e) => view.current?.scrollBy({ top: e.deltaY, left: e.deltaX })}
    >
      {plan.hidden > 0 && (
        <span
          aria-hidden
          className="pointer-events-auto flex items-center justify-end pr-0.5 text-[11px] leading-none text-ink-3"
          style={{ height: plan.pitch }}
          onMouseEnter={(e) => {
            hovering.current = true;
            pick(-1, e.currentTarget);
          }}
          onMouseLeave={() => {
            hovering.current = false;
            unpick();
          }}
        >
          …
        </span>
      )}
      {shown.map((tick, k) => {
        const i = plan.start + k;
        const label = labelOf(tick);
        const isCurrent = i === current;
        const near = at >= 0 ? Math.abs(i - at) : 99;
        return (
          <button
            key={tick.id}
            type="button"
            className="pointer-events-auto flex items-center justify-end pr-0.5"
            style={{ height: plan.pitch }}
            aria-label={t("chat-navrail.jump", { index: i + 1, preview: label })}
            aria-current={isCurrent ? "true" : undefined}
            onClick={() => jump(i, tick.id)}
            onMouseEnter={(e) => {
              hovering.current = true;
              pick(i, e.currentTarget);
            }}
            onMouseLeave={() => {
              hovering.current = false;
              unpick();
            }}
            onFocus={(e) => {
              focused.current = true;
              pick(i, e.currentTarget);
            }}
            onBlur={() => {
              focused.current = false;
              unpick();
            }}
          >
            <span
              className={cx(
                "h-[2px] w-7 origin-right rounded-full transition-[transform,background-color] duration-150 ease-out",
                isCurrent || near === 0 ? "bg-ink" : near === 1 ? "bg-ink-2" : "bg-ink-3",
                near >= 2 && !isCurrent && "opacity-70",
              )}
              style={{ transform: `scaleX(${isCurrent || near === 0 ? 1 : near === 1 ? 0.85 : 0.68})` }}
            />
          </button>
        );
      })}
      {preview && (
        <div ref={cardRef} data-nav-rail-preview className="pointer-events-none absolute top-0 left-10 z-20 w-[300px] max-w-[46vw]">
          <div className="rounded-[10px] border border-line bg-surface px-3 py-2 shadow-overlay">
            <p className="truncate text-[12.5px] font-medium text-ink">
              {preview.i < 0 ? t("chat-navrail.earlier", { count: plan.hidden }) : labelOf(ticks[preview.i])}
            </p>
            {preview.i >= 0 && ticks[preview.i].answerExcerpt && (
              <p className="mt-1 line-clamp-3 text-[12px] leading-relaxed text-ink-2">{ticks[preview.i].answerExcerpt}</p>
            )}
          </div>
        </div>
      )}
    </nav>
  );
}
