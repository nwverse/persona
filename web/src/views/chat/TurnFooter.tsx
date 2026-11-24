// Subtle settled-turn footer summarizing duration, tool/thought counts, and a full-reply copy action.
import { memo, useEffect, useMemo, useRef, useState } from "react";
import { t } from "../../i18n";
import type { Segment } from "../../lib/turns";
import { copyText, cx } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

export interface TurnFooterProps {
  seg: Segment;
}

export interface TurnSummaryMetrics {
  durationSec: number | null;
  toolCount: number;
  thoughtCount: number;
  fullReplyText: string;
}

/** Format a settled turn's elapsed seconds as `Xs` or `Xm Ys` when >= 60s. */
export function formatTurnDuration(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  const rem = s % 60;
  return `${m}m ${rem}s`;
}

/** Compute duration, tool/thought totals, and joined assistant reply text for a settled turn segment. */
export function turnSummaryMetrics(seg: Segment): TurnSummaryMetrics {
  const startStr = seg.user?.ts ?? seg.start;
  const startMs = startStr ? Date.parse(startStr) : Number.NaN;

  let endMs = seg.end ? Date.parse(seg.end) : Number.NaN;
  if (!Number.isFinite(endMs) && seg.answer?.ts) {
    endMs = Date.parse(seg.answer.ts);
  }
  if (!Number.isFinite(endMs)) {
    for (let i = seg.steps.length - 1; i >= 0; i--) {
      const st = seg.steps[i];
      const ts =
        st.kind === "tool"
          ? (st.result?.ts ?? st.use.ts)
          : st.kind === "fold"
            ? (st.item.fold?.until ?? st.item.ts)
            : st.item.ts;
      if (ts) {
        const parsed = Date.parse(ts);
        if (Number.isFinite(parsed)) {
          endMs = parsed;
          break;
        }
      }
    }
  }

  const durationSec =
    Number.isFinite(startMs) && Number.isFinite(endMs) && endMs >= startMs
      ? Math.round((endMs - startMs) / 1000)
      : null;

  let stepTools = 0;
  let stepThoughts = 0;
  const replyParts: string[] = [];

  for (const st of seg.steps) {
    if (st.kind === "tool") stepTools++;
    else if (st.kind === "thinking" && st.item.text?.trim()) stepThoughts++;
    else if (st.kind === "text" && st.item.text?.trim()) replyParts.push(st.item.text.trim());
  }
  if (seg.answer?.text?.trim()) {
    replyParts.push(seg.answer.text.trim());
  }

  return {
    durationSec,
    toolCount: Math.max(seg.tools, stepTools),
    thoughtCount: Math.max(seg.thoughts, stepThoughts),
    fullReplyText: replyParts.join("\n\n"),
  };
}

export const TurnFooter = memo(function TurnFooter({ seg }: TurnFooterProps) {
  const metrics = useMemo(() => turnSummaryMetrics(seg), [seg]);
  const [copied, setCopied] = useState(false);
  const timer = useRef<number | null>(null);
  useEffect(() => () => { if (timer.current !== null) clearTimeout(timer.current); }, []);

  const { durationSec, toolCount, thoughtCount, fullReplyText } = metrics;
  if (toolCount === 0 && thoughtCount === 0 && (durationSec === null || durationSec < 2)) {
    return null;
  }

  const textBlockCount =
    seg.steps.filter((st) => st.kind === "text" && Boolean(st.item.text?.trim())).length +
    (seg.answer?.text?.trim() ? 1 : 0);
  const showCopy = Boolean(fullReplyText) && (textBlockCount > 1 || seg.replies > 0 || toolCount > 0);

  const onCopy = () => {
    void copyText(fullReplyText);
    setCopied(true);
    if (timer.current !== null) clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setCopied(false), 1500);
  };

  return (
    <div
      className="flex flex-wrap items-center gap-2.5 pt-0.5 text-[11.5px] text-ink-3 tabular-nums"
      data-chat-find-skip="true"
      data-turn-footer
    >
      {durationSec !== null && durationSec >= 1 && (
        <span className="inline-flex items-center gap-1" data-turn-duration>
          <Icon name="clock" size={12} className="shrink-0 opacity-80" />
          {t("chat-turnfooter.duration", { duration: formatTurnDuration(durationSec) })}
        </span>
      )}
      {toolCount > 0 && (
        <span className="inline-flex items-center" data-turn-tools>
          {t("chat-turnfooter.tools", { count: toolCount })}
        </span>
      )}
      {thoughtCount > 0 && (
        <span className="inline-flex items-center" data-turn-thoughts>
          {t("chat-turnfooter.thoughts", { count: thoughtCount })}
        </span>
      )}
      {showCopy && (
        <button
          type="button"
          onClick={onCopy}
          aria-label={copied ? t("chat-turnfooter.copied") : t("chat-turnfooter.copy_reply")}
          className={cx(
            "inline-flex items-center gap-1 rounded-[5px] px-1.5 py-0.5 text-[11.5px] transition-colors hover:bg-hover-2",
            copied ? "text-green" : "text-ink-3 hover:text-ink-2",
          )}
          data-turn-copy
        >
          <Icon name={copied ? "check" : "copy"} size={11} className={copied ? "text-green" : undefined} />
          {copied ? t("chat-turnfooter.copied") : t("chat-turnfooter.copy_reply")}
        </button>
      )}
    </div>
  );
});
