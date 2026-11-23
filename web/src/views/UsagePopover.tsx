// The account windows and error details behind a click on the sidebar's usage bars (#10082): these are not needed
// for the cold first screen, so the overlay and its long strings load on intent.
import { useEffect, useState } from "react";
import { t as tr } from "../i18n";
import { sendDraft } from "../lib/drafts";
import { navigate } from "../lib/router";
import { getState } from "../lib/store";
import { balanceEmpty, balanceText } from "../lib/usage-balance";
import { accountPace, groupByProvider, isBalanceAccount, isWarn, planNote, providerTimeline, providerTitle, remainingText, resetLabel, resetPinnedUsageAccounts, sampleAge, sortedWindows, sourceNames, TIMELINE_TICKS_HOURS, timelinePosition, togglePinnedUsageAccount, usePinnedUsageAccounts, windowKey, type PaceState, type UnconnectedProvider, type UsageAccount, type UsageWindow } from "../lib/usage-windows";
import { cx, EASE } from "../ui/atoms";
import { Icon } from "../ui/icons";
import { Meter } from "./UsageBars";

const PACE_TEXT: Record<PaceState, string> = { over: "text-orange", under: "text-green", on: "text-ink-3" };
const paceLabel = (s: PaceState) => (s === "over" ? tr("usage.pace_over") : s === "under" ? tr("usage.pace_under") : tr("usage.pace_on"));
const shown = (p: number) => `${Math.round(p)}%`;

function problemText(a: UsageAccount): string {
  switch (a.problem) {
    case "rejected":
      return tr("usage.problem_rejected");
    case "unreachable":
      return tr("usage.problem_unreachable");
    case "failed":
      return tr("usage.problem_failed");
    case "error":
      return tr("usage.problem_error");
    default:
      return "";
  }
}

/** Why a balance could not be read, or why the amount shown is the last one the manager had. */
function balanceProblemText(a: UsageAccount): string {
  if (!isBalanceAccount(a)) return "";
  switch (a.problem) {
    case "rejected":
      // A refused key after an earlier good read keeps the amount: say it is the last known one, not a fresh number.
      return a.balance ? tr("usage.balance_rejected_kept") : tr("usage.balance_rejected");
    case "unreachable":
      return a.balance ? tr("usage.balance_unreachable_kept") : tr("usage.balance_unreachable");
    case "failed":
      return a.balance ? tr("usage.balance_failed_kept") : tr("usage.balance_failed");
    default:
      return "";
  }
}

/** Why a provider has no bars, in plain words (#12411). Only the built-in subscriptions can be read on a computer, so
 *  they have not been read yet; any other launcher (one run through Claude Code against another provider included)
 *  can only be read through its relay, which is the case connectPrompt hands the assistant too. */
function connectHint(p: UnconnectedProvider): string {
  if (p.agent === "claude" && p.id === "claude") return tr("usage.connect_hint_claude");
  if (p.agent === "agy" || p.agent === "antigravity" || (p.agent === "codex" && p.id === "codex")) return tr("usage.connect_hint_pending", { name: p.title });
  return tr("usage.connect_hint_other");
}

function connectPrompt(p: UnconnectedProvider): string {
  if (p.agent === "claude" && p.id === "claude") return tr("usage.connect_prompt_claude", { id: p.id });
  if (p.agent === "agy" || p.agent === "antigravity") return tr("usage.connect_prompt_agy", { id: p.id });
  return tr("usage.connect_prompt_other", { name: p.title, id: p.id });
}

/** Every live account, window by window, plus active providers without usage data yet. */
export function UsagePopover({
  accounts,
  unconnected = [],
  now: fixed,
  onClose,
}: {
  accounts: UsageAccount[];
  unconnected?: readonly UnconnectedProvider[];
  now?: number;
  onClose?: () => void;
}) {
  const pinnedIds = usePinnedUsageAccounts();
  const pinnedSet = new Set(pinnedIds);
  const livePinnedCount = accounts.reduce((n, a) => n + (pinnedSet.has(a.id) ? 1 : 0), 0);
  const [tick, setTick] = useState(() => Date.now());
  useEffect(() => {
    if (fixed !== undefined) return;
    const t = setInterval(() => setTick(Date.now()), 1000);
    return () => clearInterval(t);
  }, [fixed]);
  const now = fixed ?? tick;
  const askAssistant = (p: UnconnectedProvider) => {
    sendDraft("persona-new", { type: "replace", text: connectPrompt(p) });
    onClose?.();
    const onStarter = typeof location !== "undefined" && (location.pathname === "/persona/new" || (location.pathname === "/persona" && !getState().assistant?.main));
    if (!onStarter) navigate("/persona/new");
  };
  return (
    <div role="dialog" aria-label={tr("usage.title")} className="absolute bottom-full left-2.5 right-2.5 z-20 mb-1.5 max-h-[60vh] overflow-y-auto rounded-card bg-surface p-3 shadow-overlay" style={{ animation: `pop-in 160ms ${EASE} both`, transformOrigin: "bottom left" }}>
      <div className="flex flex-col gap-3">
        {pinnedIds.length > 0 && accounts.length > 0 && (
          <div data-usage-pinned-bar className="flex items-center justify-between gap-2 border-b border-line pb-2 text-submeta">
            <span className="min-w-0 truncate text-ink-3">
              {livePinnedCount > 0 ? tr("usage.pinned_summary", { count: livePinnedCount }) : tr("usage.pinned_fallback")}
            </span>
            <button
              type="button"
              data-usage-reset-pins
              onClick={() => resetPinnedUsageAccounts()}
              className="shrink-0 font-medium whitespace-nowrap text-accent-ink hover:underline"
            >
              {tr("usage.reset_pinned")}
            </button>
          </div>
        )}
        {groupByProvider(accounts).map((g) => (
          <section key={g.provider} data-usage-provider={g.provider} aria-label={providerTitle(g.provider)} className="flex flex-col gap-3 rounded-control border border-line p-2.5">
            <header className="flex items-baseline gap-2">
              <h3 className="min-w-0 flex-1 truncate text-meta font-semibold text-ink">{providerTitle(g.provider)}</h3>
              <span className="shrink-0 whitespace-nowrap text-submeta text-ink-3">{tr("usage.provider_accounts", { count: g.accounts.length })}</span>
              {g.sampledAt && <span className="shrink-0 whitespace-nowrap text-submeta text-ink-3">{sampleAge(g.sampledAt, now)}</span>}
            </header>
            {g.accounts.map((a, i) => (
              <div key={a.id} className={cx("flex flex-col", i > 0 && "border-t border-line pt-2.5")}>
                <AccountSection a={a} now={now} pinned={pinnedSet.has(a.id)} />
              </div>
            ))}
            <ProviderTimeline accounts={g.accounts} now={now} />
          </section>
        ))}
        {unconnected.map((p, i) => (
          <section key={`provider:${p.id}`} aria-label={p.title} className={cx("flex flex-col gap-1 text-xs", (accounts.length > 0 || i > 0) && "border-t border-line pt-2.5")}>
            <div className="flex items-baseline gap-2">
              <h3 className="min-w-0 flex-1 truncate font-medium text-ink">{p.title}</h3>
              <button
                type="button"
                data-usage-connect={p.id}
                onClick={() => askAssistant(p)}
                className="shrink-0 font-medium whitespace-nowrap text-accent-ink hover:underline"
              >
                {tr("usage.connect_action")}
              </button>
            </div>
            <div className="leading-snug text-ink-3">{connectHint(p)}</div>
          </section>
        ))}
      </div>
    </div>
  );
}

function AccountSection({ a, now, pinned }: { a: UsageAccount; now: number; pinned: boolean }) {
  const from = sourceNames(a);
  const plan = planNote(a);
  // A balance's failure is about the provider's balance route, not a usage gateway's stored windows (#11825).
  const problem = isBalanceAccount(a) ? balanceProblemText(a) : problemText(a);
  return (
    <section aria-label={a.name} className={cx("flex flex-col gap-2", a.stale && "opacity-70")}>
      <header className="flex flex-col gap-0.5">
        <div className="flex items-center gap-1.5">
          <h3 className="min-w-0 flex-1 truncate text-label font-medium text-ink">{a.name}</h3>
          <span className="shrink-0 whitespace-nowrap text-submeta text-ink-3">{a.stale ? tr("usage.stale_age", { age: sampleAge(a.sampled_at, now) }) : sampleAge(a.sampled_at, now)}</span>
          <button
            type="button"
            data-usage-pin={a.id}
            aria-pressed={pinned}
            aria-label={pinned ? tr("usage.unpin_account", { name: a.name }) : tr("usage.pin_account", { name: a.name })}
            title={pinned ? tr("usage.unpin_title") : tr("usage.pin_title")}
            onClick={() => togglePinnedUsageAccount(a.id)}
            className={cx(
              "inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-control transition-colors",
              pinned ? "bg-hover-2 text-accent-ink" : "text-ink-3 hover:bg-hover hover:text-ink",
            )}
          >
            <Icon name="pin" size={12} />
          </button>
        </div>
        {(from.length > 0 || plan) && (
          <div data-usage-source className="flex min-w-0 items-baseline gap-1.5 text-submeta text-ink-3">
            {plan && <span className="shrink-0 whitespace-nowrap">{plan}</span>}
            {plan && from.length > 0 && <span aria-hidden>·</span>}
            <span className="min-w-0 truncate" title={tr("usage.sources_title", { sources: from.join(" · ") })}>{from.join(" · ")}</span>
          </div>
        )}
        {problem && <div className="text-submeta leading-snug text-orange">{problem}</div>}
      </header>
      {a.balance && (
        <div data-usage-balance className="flex items-baseline justify-between gap-2 rounded-control bg-inset px-2.5 py-2">
          <span className="text-xs text-ink-3">{tr("usage.balance_label")}</span>
          <span className={cx("text-sm font-medium tabular-nums", balanceEmpty(a.balance) ? "text-orange" : "text-ink")}>{balanceText(a.balance)}</span>
        </div>
      )}
      {!a.balance && isBalanceAccount(a) && <div data-usage-balance-unavailable className="text-xs text-orange">{tr("usage.balance_unavailable")}</div>}
      {sortedWindows(a).map((w) => (
        <WindowRow key={windowKey(w)} name={a.name} w={w} stale={Boolean(a.stale)} now={now} />
      ))}
    </section>
  );
}

const TIMELINE_NAME_W = "3.5rem";
const LANE_COLOR = { short: "var(--hue-teal)", long: "var(--hue-blue)" } as const;

/** One reset timeline for a whole provider card (#13173): a shared axis (now, 6h, 12h, 1d, 3d, 5d, 7d; the first day
 *  takes 45% of the width), one row per account in the card's order, a capsule per window from the now line to its
 *  reset with a hollow end dot and the time left. Geometry is `providerTimeline` / `timelinePosition`. */
export function ProviderTimeline({ accounts, now }: { accounts: UsageAccount[]; now: number }) {
  const rows = providerTimeline(accounts, now);
  if (!rows.length) return null;
  const tickLabel = (h: number) => (h < 24 ? tr("usage.hours", { h }) : tr("usage.days", { d: h / 24 }));
  return (
    <div data-usage-timeline className="border-t border-line pt-2" role="group" aria-label={tr("usage.timeline_title")}>
      <div className="relative h-4" style={{ marginLeft: TIMELINE_NAME_W }} aria-hidden>
        <span className="absolute left-0 top-0 text-submeta font-semibold leading-none text-ink">{tr("usage.timeline_now")}</span>
        {TIMELINE_TICKS_HOURS.map((h, i) => (
          <span key={h} className={cx("absolute top-0 text-submeta leading-none text-ink-3", i === TIMELINE_TICKS_HOURS.length - 1 ? "-translate-x-full" : "-translate-x-1/2")} style={{ left: `${timelinePosition(h * 3600_000) * 100}%` }}>
            {tickLabel(h)}
          </span>
        ))}
      </div>
      <div className="relative" style={{ marginLeft: TIMELINE_NAME_W }}>
        {TIMELINE_TICKS_HOURS.map((h) => (
          <span key={h} aria-hidden className={cx("absolute inset-y-0 w-px bg-line-strong", h === 24 && "opacity-100", h !== 24 && "opacity-60")} style={{ left: `${timelinePosition(h * 3600_000) * 100}%` }} />
        ))}
        <span aria-hidden data-timeline-now className="absolute -top-1 bottom-0 z-[3] w-0.5 rounded-full bg-ink" />
        {rows.map((r, i) => {
          const parts = r.lanes.map((l) => `${l.label} · ${resetLabel(l.resetsAt, now)}`);
          return (
            <div key={r.account.id} data-timeline-row={r.account.id} role="img" aria-label={`${r.account.name}: ${parts.join("; ")}`} title={parts.join("\n")} className={cx("relative h-[30px]", r.account.stale && "opacity-60")}>
              {i % 2 === 0 && <span aria-hidden className="absolute inset-y-0 right-0 rounded-md bg-inset" style={{ left: `-${TIMELINE_NAME_W}` }} />}
              <span className="absolute top-0 z-[1] flex h-[30px] flex-col justify-center truncate pl-1 text-submeta leading-tight text-ink-2" style={{ left: `-${TIMELINE_NAME_W}`, width: "3.4rem" }}>
                {r.account.name}
                {planNote(r.account) && <small className="truncate text-ink-3">{planNote(r.account).split(" ")[0]}</small>}
              </span>
              {r.lanes.map((l) => {
                const color = l.long ? LANE_COLOR.long : LANE_COLOR.short;
                const top = l.long ? 15 : 5;
                const dotTop = l.long ? 14 : 3;
                const flip = l.end > 0.8;
                return (
                  <span key={l.key} data-timeline-lane={l.label}>
                    <span aria-hidden className="absolute left-0 z-[1] rounded" style={{ top, height: l.long ? 9 : 6, width: `${l.end * 100}%`, backgroundImage: `linear-gradient(90deg, transparent, ${color})` }} />
                    <span aria-hidden className="absolute z-[2] size-[11px] -translate-x-1/2 rounded-full border-2 bg-surface" style={{ left: `${l.end * 100}%`, top: dotTop, borderColor: color }} />
                    <span aria-hidden className={cx("absolute z-[2] whitespace-nowrap text-submeta font-semibold leading-none", flip && "text-white")} style={{ top: dotTop - 1, ...(flip ? { right: `calc(${100 - l.end * 100}% + 9px)` } : { left: `calc(${l.end * 100}% + 9px)`, color }) }}>
                      {remainingText(l.remainingMs)}
                    </span>
                  </span>
                );
              })}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function WindowRow({ name, w, stale, now }: { name: string; w: UsageWindow; stale: boolean; now: number }) {
  const p = accountPace({ stale }, w, now);
  return (
    <div className="flex flex-col gap-1">
      {w.model && (
        <span data-usage-model className="truncate text-submeta text-ink-3" title={w.model}>
          {w.model}
        </span>
      )}
      <div className="flex items-baseline gap-2 text-submeta">
        <span className="w-7 shrink-0 text-ink-2">{w.label}</span>
        <span className={cx("shrink-0 tabular-nums", isWarn(w.used_percent) ? "text-orange" : "text-ink")}>{shown(w.used_percent)}</span>
        {p && p.state !== "on" && <span className={cx("shrink-0 whitespace-nowrap", PACE_TEXT[p.state])}>{paceLabel(p.state)}</span>}
        <span className="min-w-0 flex-1 truncate text-right text-ink-3">{resetLabel(w.resets_at, now)}</span>
      </div>
      <Meter used={w.used_percent} expected={p?.expected} protect={w.protect_percent} label={w.model ? tr("usage.window_model_label", { name, window: w.label, model: w.model, percent: shown(w.used_percent) }) : tr("usage.window_used_label", { name, window: w.label, percent: shown(w.used_percent) })} tall />
    </div>
  );
}
