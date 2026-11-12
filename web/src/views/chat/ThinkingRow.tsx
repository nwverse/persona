// A thought in a work group, as cindy draws it (WorkGroupBlock.tsx ThinkingActivityRow / ExpandedThinkingRow, #5155):
// one row in the tool rows' chrome, the sparkle in the status slot and its words in italic on one line, `**strong**`
// and backtick code read as such (ThinkingText.tsx); it opens in place to the whole thought when they do not fit or
// have a line break. A thought without words is no row at all (Claude Code mostly writes thoughts without words:
// "Thought for 1s" rows say nothing). Drawn only inside an opened summary, so WorkGroup loads it lazily: off the first
// screen, with the emphasis parser.
import { useLayoutEffect, useRef, useState } from "react";
import { thinkingPlainText } from "../../lib/thinking-text";
import type { Item } from "../../lib/types";
import { cx } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { CHEVRON_SLOT, ROW_CHROME, ROW_HOVER, STATUS_SLOT } from "./ActionRow";
import { useExpanded } from "./expanded";
import { useReveal } from "./search-reveal";
import { ThinkingText } from "./ThinkingText";

const NONE: string[] = [];

export function ThinkingRow({ item, scope = "" }: { item: Item; scope?: string }) {
  const raw = item.text?.trim() ?? "";
  const words = raw.replace(/\s+/g, " ");
  const lineBreak = /[\r\n]/.test(raw);
  const [open, setOpen] = useExpanded(`${scope}thinking:${item.id}`);
  const [overflows, setOverflows] = useState(false);
  const text = useRef<HTMLSpanElement>(null);
  // whether one line holds it: measured on the line itself, again whenever its width changes
  useLayoutEffect(() => {
    const el = text.current;
    if (open || !el) return;
    const measure = () => setOverflows(el.scrollWidth > el.clientWidth + 1);
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const watch = new ResizeObserver(measure);
    watch.observe(el);
    return () => watch.disconnect();
  }, [words, open]);
  const opens = open || lineBreak || overflows;
  // a search hit in its words (search-reveal.ts): it opens
  useReveal(scope, words ? [item.id] : NONE, () => {
    if (open) return;
    setOpen(true);
    return () => setOpen(false);
  });
  if (!words) return null;
  return (
    <div className="flex min-w-0 flex-col" data-thinking="words" data-items={item.id}>
      <button type="button" disabled={!opens} aria-expanded={opens ? open : undefined} onClick={() => setOpen((o) => !o)} className={cx(ROW_CHROME, open ? "items-start" : "items-center", opens ? ROW_HOVER : "cursor-default")}>
        <span aria-hidden className={STATUS_SLOT}>
          <Icon name="spark" size={13} />
        </span>
        <span ref={text} title={open ? undefined : thinkingPlainText(words)} className={cx("min-w-0 flex-1 text-[length:var(--chat-secondary)] text-thinking-ink italic", open ? "whitespace-pre-wrap [overflow-wrap:anywhere]" : "truncate")}>
          <ThinkingText content={open ? raw : words} />
        </span>
        <span aria-hidden className={CHEVRON_SLOT}>
          {opens && <Icon name="chevronRight" size={13} stroke={2.2} className="transition-transform duration-150 motion-reduce:transition-none" style={{ transform: open ? "rotate(90deg)" : "none" }} />}
        </span>
      </button>
    </div>
  );
}
