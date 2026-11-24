import { useEffect, useRef, useState } from "react";
import { t } from "../../i18n";
import { BUSY_MOTION } from "../../lib/busy-motion";
import { cx, EASE } from "../../ui/atoms";

export interface ContextRingProps {
  /** Known context tokens (e.g. from last compact pre_tokens or estimated). */
  usedTokens?: number;
  /** Assumed context window (defaults to 200_000). */
  windowTokens?: number;
  /** Live output tokens this turn (from SessionLive.tokens_down). */
  liveTokensDown?: number;
  working?: boolean;
  onCompact?: () => void;
  /** @internal Initial open state (used in tests). */
  defaultOpen?: boolean;
}

const DEFAULT_WINDOW_TOKENS = 200_000;
const RING_RADIUS = 6;
const RING_CIRCUMFERENCE = 2 * Math.PI * RING_RADIUS;

/** Formats token counts compactly (e.g. 850, 12.4k, 200k, 1.2M). */
export function formatTokenCount(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0";
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 100_000) return `${Math.round(n / 1_000)}k`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(Math.round(n));
}

/** Returns clamped [0, 1] context usage ratio, or null when used tokens are unknown. */
export function contextUsageRatio(used?: number, windowTokens = DEFAULT_WINDOW_TOKENS): number | null {
  if (used === undefined || !Number.isFinite(used) || used <= 0) return null;
  if (!Number.isFinite(windowTokens) || windowTokens <= 0) return null;
  return Math.min(1, Math.max(0, used / windowTokens));
}

function toneForRatio(ratio: number | null): { text: string; bar: string } {
  if (ratio === null || ratio < 0.65) return { text: "text-accent", bar: "bg-accent" };
  if (ratio <= 0.85) return { text: "text-orange", bar: "bg-orange" };
  return { text: "text-red", bar: "bg-red" };
}

export function ContextRing({
  usedTokens,
  windowTokens = DEFAULT_WINDOW_TOKENS,
  liveTokensDown,
  working = false,
  onCompact,
  defaultOpen = false,
}: ContextRingProps) {
  const [open, setOpen] = useState(defaultOpen);
  const rootRef = useRef<HTMLDivElement>(null);

  const hasUsed = typeof usedTokens === "number" && Number.isFinite(usedTokens) && usedTokens > 0;
  const hasLive = typeof liveTokensDown === "number" && Number.isFinite(liveTokensDown) && liveTokensDown > 0;

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (e: PointerEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        setOpen(false);
      }
    };
    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open]);

  if (!hasUsed && !hasLive) return null;

  const ratio = hasUsed ? contextUsageRatio(usedTokens, windowTokens) : null;
  const pct = ratio !== null ? Math.round(ratio * 100) : null;
  const tone = toneForRatio(ratio);
  const liveOnly = ratio === null && hasLive;

  // When only live output tokens exist, show a subtle pulsing 28% arc.
  const arcFraction = ratio !== null ? Math.max(ratio, 0.04) : 0.28;
  const dashOffset = RING_CIRCUMFERENCE * (1 - arcFraction);

  const title =
    pct !== null
      ? t("chat-contextring.usage_title", { pct, used: formatTokenCount(usedTokens!), window: formatTokenCount(windowTokens) })
      : t("chat-contextring.live_output", { tokens: formatTokenCount(liveTokensDown!) });

  return (
    <div ref={rootRef} className="relative inline-flex shrink-0">
      <button
        type="button"
        data-native-chat-context-usage="true"
        aria-label={title}
        aria-haspopup="dialog"
        aria-expanded={open}
        title={title}
        onClick={() => setOpen((v) => !v)}
        className={cx(
          "inline-flex size-6 shrink-0 items-center justify-center rounded-[6px] transition-colors hover:bg-hover",
          open && "bg-hover",
          tone.text,
        )}
      >
        <svg
          width={16}
          height={16}
          viewBox="0 0 16 16"
          aria-hidden="true"
          className={cx("-rotate-90", (liveOnly || (working && ratio === null)) && cx("animate-pulse", BUSY_MOTION))}
        >
          <circle
            cx="8"
            cy="8"
            r={RING_RADIUS}
            fill="none"
            stroke="currentColor"
            strokeWidth={1.75}
            opacity="0.2"
          />
          <circle
            cx="8"
            cy="8"
            r={RING_RADIUS}
            fill="none"
            stroke="currentColor"
            strokeWidth={1.75}
            strokeLinecap="round"
            strokeDasharray={RING_CIRCUMFERENCE}
            strokeDashoffset={dashOffset}
            className="transition-[stroke-dashoffset] duration-300"
          />
        </svg>
      </button>

      {open && (
        <div
          role="dialog"
          aria-label={t("chat-contextring.heading")}
          data-chat-find-skip="true"
          className="absolute right-0 bottom-full z-40 mb-2 w-56 rounded-[12px] border border-line-strong bg-surface/95 p-3 shadow-overlay backdrop-blur-md"
          style={{ animation: `pop-in 160ms ${EASE} both`, transformOrigin: "bottom right" }}
        >
          <div className="flex items-center justify-between gap-2">
            <span className="text-[12px] font-medium text-ink">{t("chat-contextring.heading")}</span>
            {pct !== null ? (
              <span className={cx("font-mono text-[12px] font-semibold tabular-nums", tone.text)}>
                {pct}%
              </span>
            ) : working ? (
              <span className="text-[11px] text-ink-3">{t("chat-contextring.generating")}</span>
            ) : null}
          </div>

          {ratio !== null && (
            <div className="mt-2 flex flex-col gap-1.5">
              <div className="h-1.5 w-full overflow-hidden rounded-full bg-hover">
                <div
                  className={cx("h-full rounded-full transition-[width] duration-300", tone.bar)}
                  style={{ width: `${pct}%` }}
                />
              </div>
              <div className="flex items-center justify-between text-[11px] text-ink-3">
                <span>{t("chat-contextring.used_window")}</span>
                <span className="font-mono tabular-nums text-ink-2">
                  {formatTokenCount(usedTokens!)} / {formatTokenCount(windowTokens)}
                </span>
              </div>
            </div>
          )}

          {hasLive && (
            <div className={cx("text-[11.5px] text-ink-2 tabular-nums", ratio !== null ? "mt-2 border-t border-line pt-2" : "mt-2")}>
              {t("chat-contextring.live_output", { tokens: formatTokenCount(liveTokensDown!) })}
            </div>
          )}

          {onCompact && (
            <button
              type="button"
              onClick={() => {
                onCompact();
                setOpen(false);
              }}
              className="mt-2.5 flex w-full items-center justify-center rounded-[7px] bg-hover px-2.5 py-1.5 text-[11.5px] font-medium text-ink transition-colors hover:bg-hover-2"
            >
              {t("chat-contextring.compact_action")}
            </button>
          )}
        </div>
      )}
    </div>
  );
}
