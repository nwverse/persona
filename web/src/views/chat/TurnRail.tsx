import { useCallback, useEffect, useMemo, useState } from "react";
import { t } from "../../i18n";
import { BUSY_MOTION } from "../../lib/busy-motion";
import { cx } from "../../ui/atoms";
import { scrollBehavior } from "../../ui/stream/motion";

export interface TurnRailItem {
  id: string; // matches [data-turn-id="<id>"] in scroller
  prompt: string; // user prompt text
  reply?: string; // optional assistant reply preview
  working?: boolean; // true if this turn is currently running
}

export interface TurnRailProps {
  items: TurnRailItem[];
  scrollerRef: React.RefObject<HTMLElement | null>;
  activeId?: string | null;
  onJump?: (id: string) => void;
}

export const MAX_RAIL_TURNS = 30;

export interface TurnRect {
  id: string;
  top: number;
  bottom: number;
}

/** Pure helper: picks the turn closest to the upper third of the scroller viewport. */
export function pickActiveTurnId(
  rects: readonly TurnRect[],
  viewportTop: number,
  viewportBottom: number,
): string | null {
  if (rects.length === 0) return null;
  const anchorY = viewportTop + (viewportBottom - viewportTop) / 3;
  const visible = rects.filter((r) => r.bottom >= viewportTop && r.top <= viewportBottom);
  const candidates = visible.length > 0 ? visible : rects;

  let bestId = candidates[0].id;
  let bestDist = Infinity;
  let bestTopDist = Infinity;

  for (const r of candidates) {
    // Distance from anchorY to the turn's vertical span [top, bottom] (0 if it covers anchorY).
    const dist = anchorY < r.top ? r.top - anchorY : anchorY > r.bottom ? anchorY - r.bottom : 0;
    const topDist = Math.abs(r.top - anchorY);
    if (dist < bestDist || (dist === bestDist && topDist < bestTopDist)) {
      bestDist = dist;
      bestTopDist = topDist;
      bestId = r.id;
    }
  }
  return bestId;
}

/** Pure helper: slices the latest MAX_RAIL_TURNS items along with their 1-based starting turn index. */
export function sliceRailItems(items: readonly TurnRailItem[]): {
  visible: TurnRailItem[];
  startIndex: number;
} {
  const offset = Math.max(0, items.length - MAX_RAIL_TURNS);
  return { visible: items.slice(offset), startIndex: offset + 1 };
}

export function TurnRail({ items, scrollerRef, activeId, onJump }: TurnRailProps) {
  const { visible, startIndex } = useMemo(() => sliceRailItems(items), [items]);
  const [trackedId, setTrackedId] = useState<string | null>(null);
  const [hoveredId, setHoveredId] = useState<string | null>(null);
  const [focusedId, setFocusedId] = useState<string | null>(null);

  const visibleIdSet = useMemo(() => new Set(visible.map((it) => it.id)), [visible]);

  useEffect(() => {
    if (activeId !== undefined || visible.length < 2) return;
    const scroller = scrollerRef.current;
    if (!scroller) return;

    let raf = 0;
    const measure = () => {
      raf = 0;
      const sRect = scroller.getBoundingClientRect();
      const nodes = scroller.querySelectorAll<HTMLElement>("[data-turn-id]");
      const rects: TurnRect[] = [];
      for (const el of nodes) {
        const id = el.getAttribute("data-turn-id");
        if (!id || !visibleIdSet.has(id)) continue;
        const r = el.getBoundingClientRect();
        rects.push({ id, top: r.top, bottom: r.bottom });
      }
      const next = pickActiveTurnId(rects, sRect.top, sRect.bottom);
      if (next !== null) setTrackedId(next);
    };

    const onScroll = () => {
      if (!raf) raf = requestAnimationFrame(measure);
    };

    measure();
    scroller.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("resize", onScroll, { passive: true });
    return () => {
      if (raf) cancelAnimationFrame(raf);
      scroller.removeEventListener("scroll", onScroll);
      window.removeEventListener("resize", onScroll);
    };
  }, [activeId, scrollerRef, visible.length, visibleIdSet]);

  const handleJump = useCallback(
    (id: string) => {
      const scroller = scrollerRef.current;
      if (scroller) {
        const nodes = scroller.querySelectorAll<HTMLElement>("[data-turn-id]");
        for (const el of nodes) {
          if (el.getAttribute("data-turn-id") === id) {
            el.scrollIntoView({
              behavior: scrollBehavior(),
              block: "start",
            });
            break;
          }
        }
      }
      if (activeId === undefined) setTrackedId(id);
      onJump?.(id);
    },
    [activeId, onJump, scrollerRef],
  );

  const handleWheel = useCallback(
    (e: React.WheelEvent<HTMLElement>) => {
      const scroller = scrollerRef.current;
      if (scroller) {
        scroller.scrollTop += e.deltaY;
      }
    },
    [scrollerRef],
  );

  if (items.length < 2) return null;

  const currentActiveId = activeId !== undefined ? activeId : (trackedId ?? visible[visible.length - 1]?.id ?? null);
  const previewId = hoveredId ?? focusedId;

  return (
    <nav
      aria-label={t("chat-turnrail.nav_aria")}
      data-chat-find-skip="true"
      onWheel={handleWheel}
      onMouseLeave={() => setHoveredId(null)}
      className="hidden lg:flex absolute right-2 top-1/2 -translate-y-1/2 z-20 flex-col items-end gap-0.5 py-1.5"
    >
      {visible.map((item, idx) => {
        const turnNumber = startIndex + idx;
        const isActive = item.id === currentActiveId;
        const showPreview = item.id === previewId;

        return (
          <div key={item.id} className="relative flex items-center">
            {showPreview && (
              <div
                role="tooltip"
                className="pointer-events-none absolute right-full mr-2.5 top-1/2 -translate-y-1/2 w-64 rounded-[10px] border border-line bg-surface/95 backdrop-blur-md p-2.5 shadow-overlay"
              >
                <div className="flex items-start gap-1.5">
                  <span className="shrink-0 font-mono text-[10.5px] font-medium text-ink-3 tabular-nums pt-[2px]">
                    #{turnNumber}
                  </span>
                  <p className="line-clamp-2 text-[12.5px] font-medium leading-snug text-ink">
                    {item.prompt}
                  </p>
                </div>
                {item.reply && (
                  <p className="line-clamp-2 text-[11.5px] leading-snug text-ink-3 mt-1">
                    {item.reply}
                  </p>
                )}
              </div>
            )}
            <button
              type="button"
              aria-label={t("chat-turnrail.jump_to_turn", { turn: turnNumber, prompt: item.prompt.slice(0, 40) })}
              aria-current={isActive ? "step" : undefined}
              onClick={() => handleJump(item.id)}
              onMouseEnter={() => setHoveredId(item.id)}
              onFocus={() => setFocusedId(item.id)}
              onBlur={() => setFocusedId((cur) => (cur === item.id ? null : cur))}
              className="group/tick flex h-3 w-7 cursor-pointer items-center justify-end pr-0.5 outline-none focus-visible:ring-1 focus-visible:ring-accent rounded-full"
            >
              <span
                className={cx(
                  "block h-[3px] rounded-full transition-all",
                  isActive
                    ? "w-5 bg-ink"
                    : item.working
                      ? cx("w-4 bg-accent animate-pulse", BUSY_MOTION)
                      : "w-3 bg-ink-3/45 group-hover/tick:w-4 group-hover/tick:bg-ink-2",
                )}
              />
            </button>
          </div>
        );
      })}
    </nav>
  );
}
