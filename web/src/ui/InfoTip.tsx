// An ⓘ beside a title that says, in a sentence or two, what the thing it names holds (#6458; docs/design/DESIGN_SYSTEM.md
// 3.8 "Info tips"). The words stay out of the list until asked for: the pointer resting on the ⓘ, keyboard focus or a
// tap shows them in a dark tip under it (above it when there is no room); a tap or a click keeps it until a press
// elsewhere, Escape or another tap. The tip is drawn over the page (a portal), so a list that clips or scrolls never
// cuts it off, and the ⓘ names it for a screen reader (aria-describedby while shown).
import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { t } from "../i18n";
import { cx } from "./atoms";
import { Icon } from "./icons";
import { placeMenu } from "./overlay";

/** How long the pointer rests on the ⓘ before the tip shows, so a pointer passing over the list doesn't flash it. */
const HOVER_MS = 250;
/** The tip's width at most, in px. */
const TIP_W = 260;

export function InfoTip({ name, text, className }: { name: string; text: string; className?: string }) {
  const [hover, setHover] = useState(false);
  const [pinned, setPinned] = useState(false);
  const [place, setPlace] = useState<{ left: number; top: number } | null>(null);
  const button = useRef<HTMLButtonElement>(null);
  const tip = useRef<HTMLDivElement>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const id = useId();
  const open = hover || pinned;

  useEffect(() => () => clearTimeout(timer.current), []);
  useEffect(() => {
    if (!open) return;
    const down = (e: PointerEvent) => {
      const at = e.target as Node;
      if (button.current?.contains(at) || tip.current?.contains(at)) return;
      setPinned(false);
      setHover(false);
    };
    const key = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      setPinned(false);
      setHover(false);
    };
    const away = () => {
      setPinned(false);
      setHover(false);
    };
    addEventListener("pointerdown", down, true);
    addEventListener("keydown", key);
    // the list scrolling under it would leave the tip behind
    addEventListener("scroll", away, true);
    return () => {
      removeEventListener("pointerdown", down, true);
      removeEventListener("keydown", key);
      removeEventListener("scroll", away, true);
    };
  }, [open]);
  useLayoutEffect(() => {
    if (!open || !button.current || !tip.current) return;
    const r = button.current.getBoundingClientRect();
    const { left, top } = placeMenu(r, tip.current.offsetHeight, { width: TIP_W, align: "left", side: "bottom", screenW: innerWidth, screenH: innerHeight, gap: 4 });
    setPlace({ left: Math.max(8, Math.min(left - 8, innerWidth - tip.current.offsetWidth - 8)), top });
  }, [open, text]);

  const rest = (on: boolean) => {
    clearTimeout(timer.current);
    if (on) timer.current = setTimeout(() => setHover(true), HOVER_MS);
    else setHover(false);
  };
  return (
    <>
      <button
        ref={button}
        type="button"
        aria-label={t("ui-atoms.about", { name })}
        // no aria-expanded: the sidebar reads that on a group heading's buttons as the group's fold (GroupFoldButton)
        data-pinned={pinned || undefined}
        aria-describedby={open ? id : undefined}
        onPointerEnter={(e) => e.pointerType === "mouse" && rest(true)}
        onPointerLeave={(e) => e.pointerType === "mouse" && rest(false)}
        onFocus={(e) => e.currentTarget.matches(":focus-visible") && setHover(true)}
        onBlur={() => rest(false)}
        onClick={() => {
          clearTimeout(timer.current);
          setPinned((p) => !p);
          setHover(false);
        }}
        className={cx("touch-hit relative z-10 flex size-4 shrink-0 items-center justify-center rounded-full text-ink-3 transition-colors hover:text-ink-2 data-pinned:text-ink-2", className)}
      >
        <Icon name="info" size={11} stroke={2} />
      </button>
      {open &&
        createPortal(
          <div
            ref={tip}
            id={id}
            role="tooltip"
            style={{ left: place?.left ?? -9999, top: place?.top ?? -9999, maxWidth: TIP_W }}
            className="fixed z-[75] w-max rounded-[10px] border border-tooltip-border bg-tooltip-bg px-3 py-2 text-[12px] leading-[1.5] text-tooltip-fg shadow-overlay"
          >
            {text}
          </div>,
          document.body,
        )}
    </>
  );
}
