import { t } from "../../i18n";
import { loopCadence, loopNext } from "../../lib/tasks";
import type { Session } from "../../lib/types";
import { backgroundCount } from "../../lib/workflows";
import { cx, IconButton, useNow } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

/** Before the title while a /loop drives the Agent (#5421, cindy's schedule badge): the loop's mark; its cadence and next
 *  wake-up on hover; a click opens the Background tasks panel, where the loop is and stops. */
export function LoopMark({ loop, onOpen }: { loop: NonNullable<Session["loop"]>; onOpen: () => void }) {
  const now = useNow(30_000);
  const hint = [t("chat-chatview.loop_mark"), loopCadence(loop), loopNext(loop, now)].filter(Boolean).join(" · ");
  return (
    <button type="button" onClick={onOpen} aria-label={hint} title={hint} data-loop-mark className="mr-1 flex size-4 shrink-0 items-center justify-center rounded-[4px] text-ink-3 transition-colors hover:text-ink">
      <Icon name="loop" size={13} />
    </button>
  );
}


export { TabViewSwitch } from "../../ui/TabViewSwitch";
export { StoppedAgentLine } from "./StoppedAgentLine";

export interface AgentHeaderPanelsProps {
  overview?: { id: string; open: boolean; toggle: () => void };
  team?: { open: boolean; needs: boolean; onToggle: () => void };
  sides?: { count: number; open: boolean; needs: boolean; onToggle: () => void };
  session?: Session;
}

/** Controls for panels attached to an Agent. The main conversation does not load their markup or count its background work. */
export function AgentHeaderPanels({ overview, team, sides, session }: AgentHeaderPanelsProps) {
  const running = session ? backgroundCount(session) : 0;
  const sideLabel = sides ? [t("chat-chatview.side_sessions_count", { count: sides.count }), sides.needs ? t("chat-chatview.side_sessions_needs_you") : ""].filter(Boolean).join(" · ") : "";
  return (
    <>
      {overview && (
        <button
          type="button"
          data-session-overview-trigger
          aria-label={t("chat-chatview.session_overview")}
          aria-pressed={overview.open}
          aria-description={running > 0 ? t("chat-chatview.background_running", { count: running }) : undefined}
          aria-controls={overview.open ? overview.id : undefined}
          title={t("chat-chatview.session_overview")}
          onClick={overview.toggle}
          className={cx("touch-hit relative flex size-7 shrink-0 items-center justify-center rounded-control transition-colors hover:bg-hover hover:text-ink", overview.open ? "bg-hover text-ink" : "text-ink-3")}
        >
          <Icon name="list" size={15} />
          {running > 0 && !overview.open && (
            <span aria-hidden data-background-count className="pointer-events-none absolute -top-1 -right-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-field px-1 text-[10px] font-semibold text-ink-2 tabular-nums shadow-[0_0_0_2px_var(--page)]">{running}</span>
          )}
        </button>
      )}
      {team && (
        <span className="relative flex shrink-0">
          <IconButton icon="users" label={team.needs && !team.open ? t("chat-chatview.team_needs_you") : t("chat-chatview.team")} onClick={team.onToggle} active={team.open} />
          {team.needs && !team.open && <span aria-hidden className="pointer-events-none absolute top-0.5 right-0.5 size-2 rounded-full bg-attention shadow-[0_0_0_2px_var(--page)]" />}
        </span>
      )}
      {sides && (
        // its side sessions (#7944): the mark and how many, on one line; the attention dot while one needs you
        <span className="relative flex shrink-0">
          <button
            type="button"
            aria-label={sideLabel}
            title={sideLabel}
            aria-expanded={sides.open}
            data-side-sessions={sides.count}
            onClick={sides.onToggle}
            className={cx(
              "touch-hit flex h-7 shrink-0 items-center gap-1 rounded-[8px] px-1.5 text-label leading-none font-medium whitespace-nowrap tabular-nums transition-[background-color,color,transform] duration-150 active:scale-[0.94]",
              sides.open ? "bg-hover text-ink" : "text-ink-3 hover:bg-hover hover:text-ink",
            )}
          >
            <Icon name="sideSessions" size={15} />
            <span>{sides.count}</span>
          </button>
          {sides.needs && <span aria-hidden data-side-sessions-needs className="pointer-events-none absolute top-0.5 right-0.5 size-2 rounded-full bg-attention shadow-[0_0_0_2px_var(--page)]" />}
        </span>
      )}
    </>
  );
}
