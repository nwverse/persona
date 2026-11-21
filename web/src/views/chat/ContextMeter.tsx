// A session's context & usage meter (#2430, moved from the header to the usage row under the composer in #5056,
// enabled for Assistant chats with lazy Session Insights popover in #12541): shows context window share when known
// (or compact cumulative token count when only Item.Usage is available), and opens the lazy Session Insights popover
// on click. "Show details in Agent rows" does not gate it. Nothing renders while both window and token usage are unknown.
import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { t } from "../../i18n";
import { contextHint, contextLabel, contextTier, contextTooltip, contextUsage, type ContextTier } from "../../lib/context-usage";
import { compactTokens } from "../../lib/tokens";
import { cx, EASE } from "../../ui/atoms";
import type { Item, Session } from "../../lib/types";

const SessionInsightsPopover = lazy(() => import("./SessionInsightsPopover").then((m) => ({ default: m.SessionInsightsPopover })));

const TIER_BAR: Record<ContextTier, string> = { ok: "bg-accent", warn: "bg-orange", danger: "bg-orange" };
const TIER_TEXT: Record<ContextTier, string> = { ok: "", warn: "font-medium text-orange", danger: "font-medium text-orange" };

/** Lightweight cumulative token total from loaded items (deduplicating subagent reports) so the L1 pill can show
 *  even before the lazy SessionInsightsPopover chunk is fetched. */
function sessionTokenTotal(items?: readonly Item[]): number {
  if (!items?.length) return 0;
  let sum = 0;
  const seen = new Map<string, number>();
  for (const it of items) {
    for (const list of [it.usage, it.fold?.usage]) {
      if (!list?.length) continue;
      for (const u of list) {
        const n = (u.in ?? 0) + (u.out ?? 0) + (u.cache_read ?? 0) + (u.cache_write ?? 0);
        if (!(n > 0)) continue;
        if (u.id) {
          const k = `${u.id}\n${u.model ?? ""}\n${u.fast ? 1 : 0}`;
          seen.set(k, Math.max(seen.get(k) ?? 0, n));
        } else {
          sum += n;
        }
      }
    }
  }
  for (const n of seen.values()) sum += n;
  return sum;
}

/** True when any loaded item carries positive token usage. */
export function hasSessionTokenUsage(items?: readonly Item[]): boolean {
  if (!items?.length) return false;
  for (const it of items) {
    for (const list of [it.usage, it.fold?.usage]) {
      if (!list?.length) continue;
      for (const u of list) {
        if ((u.in ?? 0) + (u.out ?? 0) + (u.cache_read ?? 0) + (u.cache_write ?? 0) > 0) return true;
      }
    }
  }
  return false;
}

export interface ContextMeterProps {
  session: Session;
  compact?: boolean;
  items?: readonly Item[];
  assistant?: boolean;
  onCompact?: () => boolean | void | Promise<boolean | void | unknown>;
  compactBlocked?: boolean;
}

export function ContextMeter({ session, compact = false, items, assistant, onCompact, compactBlocked }: ContextMeterProps) {
  const buttonRef = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const close = useCallback(() => setOpen(false), []);
  const usage = contextUsage(session);
  const tokenTotal = useMemo(() => (usage ? 0 : sessionTokenTotal(items)), [usage, items]);
  const known = usage !== null || tokenTotal > 0;
  // close if the window and usage go unknown (e.g. a /clear) while open
  useEffect(() => {
    if (!known) setOpen(false);
  }, [known]);
  if (!known) return null;

  const tier = usage ? contextTier(usage) : "ok";
  const tip = usage ? contextTooltip(usage) : t("chat-runningstatusbar.token_count", { tokens: compactTokens(tokenTotal) });
  const description = usage ? contextHint(usage) : undefined;
  const title = usage ? [tip, contextHint(usage)].join("\n") : tip;

  return (
    <>
      <button
        ref={buttonRef}
        type="button"
        onClick={() => setOpen((was) => !was)}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={tip}
        aria-description={description}
        title={open ? undefined : title}
        className={cx("touch-hit -mx-1.5 flex shrink-0 items-center gap-1.5 rounded-chip px-1.5 text-submeta whitespace-nowrap text-ink-3 transition-colors", open && "bg-hover")}
      >
        {usage ? (
          <>
            {!compact && (
              <span
                className="block h-1 w-10 overflow-hidden rounded-full bg-hover-2"
                role="progressbar"
                aria-label={tip}
                aria-valuenow={usage.used}
                aria-valuemin={0}
                aria-valuemax={100}
              >
                <span className={cx("block h-full rounded-full motion-safe:transition-[width] motion-safe:duration-300", TIER_BAR[tier])} style={{ width: `${usage.used}%`, transitionTimingFunction: EASE }} />
              </span>
            )}
            <span className={cx("tabular-nums", TIER_TEXT[tier])}>{contextLabel(usage)}</span>
          </>
        ) : (
          <span className="tabular-nums" data-context-tokens>{tip}</span>
        )}
      </button>
      {open && (
        <Suspense fallback={null}>
          <SessionInsightsPopover
            anchor={buttonRef}
            session={session}
            items={items}
            assistant={assistant}
            onCompact={onCompact}
            compactBlocked={compactBlocked}
            onClose={close}
          />
        </Suspense>
      )}
    </>
  );
}
