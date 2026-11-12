// A thought in a work group, as cindy draws it (WorkGroupBlock.tsx ThinkingActivityRow / ExpandedThinkingRow, #5155):
// one row in the tool rows' chrome, the sparkle in the status slot and its words in italic on one line, `**strong**`
// and backtick code read as such (ThinkingText.tsx); it opens in place to the whole thought when they do not fit or
// have a line break. A thought without words is no row at all (Claude Code mostly writes thoughts without words:
// "Thought for 1s" rows say nothing). While the agent is thinking right now (the screen's status line, `live.thinking`)
// the line at the end of the live group says Thinking with three dots and a clock.
import { useLayoutEffect, useRef, useState } from "react";
import type { Item } from "../../lib/types";
import { useStore } from "../../lib/store";
import { workDuration } from "../../lib/work-groups";
import { cx, useNow } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { CHEVRON_SLOT, ROW_CHROME, ROW_HOVER, STATUS_SLOT } from "./ActionRow";
import { useExpanded } from "./expanded";
import { useReveal } from "./search-reveal";
import { ThinkingText } from "./ThinkingText";
import { thinkingPlainText } from "../../lib/thinking-text";
import { t } from "../../i18n";

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

/** The live line: Thinking, three dots, and the time since this line saw the thinking start. */
export function LiveThinking() {
  const [since] = useState(Date.now);
  const now = useNow(500, useStore((s) => s.conn === "online"));
  return (
    <div className="flex w-full min-w-0 items-center gap-1.5 px-2 py-0.5 select-none [@media(pointer:coarse)]:min-h-10" data-thinking="live" aria-live="polite">
      <span className="inline-flex h-[1lh] shrink-0 items-center text-tool-ink">
        <Icon name="spark" size={14} />
      </span>
      <span className="translate-y-[1px] text-[length:var(--chat-secondary)] text-tool-ink">{t("chat-thinkingcard.thinking")}</span>
      <span aria-hidden className="flex translate-y-[2px] items-center gap-[3px]">
        {[0, 200, 400].map((d) => (
          <span key={d} className="size-1 rounded-full bg-tool-ink animate-[breathe_1.2s_ease-in-out_infinite] busy-motion" style={{ animationDelay: `${d}ms` }} />
        ))}
      </span>
      <span className="flex-1" />
      <span className="font-mono text-[12px] text-tool-ink tabular-nums">{workDuration(Math.max(0, now - since))}</span>
    </div>
  );
}
