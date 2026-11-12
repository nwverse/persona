// The goal an Agent works toward (docs/integrations/agent-goal.md, iOS GoalBar / GoalSheet).
import { useState } from "react";
import { t } from "../../i18n";
import { goalHeadline, goalStatusLabel, goalTone, goalUsage } from "../../lib/goal";
import type { GoalInfo } from "../../lib/types";
import { cx, Dialog } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

const goalChipTone = {
  active: "bg-accent-tint text-accent-ink",
  done: "bg-green-tint text-green",
  stopped: "bg-orange-tint text-orange",
} as const;
const goalTextTone = { active: "text-accent-ink", done: "text-green", stopped: "text-orange" } as const;

/** The goal it works toward (docs/integrations/agent-goal.md, iOS GoalBar / GoalSheet): ◎ and its status in the chat's header (the
 *  first line too where there is room); a click shows all of it, the latest check's reason and what it has spent. */
export function GoalChip({ goal }: { goal: GoalInfo }) {
  const [open, setOpen] = useState(false);
  const status = goalStatusLabel(goal);
  const headline = goalHeadline(goal);
  const usage = goalUsage(goal);
  const tone = goalChipTone[goalTone(goal)];
  const textTone = goalTextTone[goalTone(goal)];
  return (
    <>
      <button type="button" onClick={() => setOpen(true)} aria-label={t("agent-goal.chip_label", { status, headline })} title={t("agent-goal.chip_title", { status, headline })}
        className="touch-hit flex h-7 min-w-0 shrink items-center rounded-[8px] px-1 transition-colors hover:bg-hover">
        <span className={cx("flex h-5.5 min-w-0 items-center gap-1 rounded-full px-2 text-[11.5px] font-medium whitespace-nowrap", tone)}>
          <Icon name="target" size={12} stroke={2.2} className="shrink-0" />
          <span className="hidden max-w-[220px] truncate @min-[800px]:inline">{headline}</span>
          <span className="shrink-0 @max-[640px]:hidden @min-[800px]:before:content-['·_']">{status}</span>
        </span>
      </button>
      <Dialog open={open} onClose={() => setOpen(false)} title={t("agent-goal.title")} width={520}>
        <div className="flex flex-col gap-3">
          <div className={cx("flex items-center gap-1.5 text-[12.5px] font-semibold", textTone)}>
            <Icon name="target" size={14} stroke={2.2} className="shrink-0" />
            <span className="whitespace-nowrap">{status}</span>
            {usage && <span className="truncate font-normal text-ink-3">{usage}</span>}
          </div>
          <p className="text-[13.5px] leading-relaxed whitespace-pre-wrap text-ink select-text">{goal.objective}</p>
          {goal.reason && (
            <div className="flex flex-col gap-1 rounded-card bg-stripe-bg px-3.5 py-3 shadow-hairline">
              <span className="text-[11.5px] font-semibold text-ink-3">{t(goal.status === "complete" ? "agent-goal.reason_met" : "agent-goal.reason_check")}</span>
              <p className="text-[12.5px] leading-relaxed whitespace-pre-wrap text-ink-2 select-text">{goal.reason}</p>
            </div>
          )}
          <p className="text-[12px] text-ink-3">{t("agent-goal.hint")}</p>
        </div>
      </Dialog>
    </>
  );
}
