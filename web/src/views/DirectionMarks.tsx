// The direction tree's two marks (#4352), the same on the sidebar, the To-dos page and the Agents page: a direction's
// "Doing N · Needs you N" and a task's state word. Text, so a state never rides on color alone.
import { memo } from "react";
import { t } from "../i18n";
import { DIRECTION_STATE_TONE, directionCountsText, directionStateName, type DirectionCounts, type DirectionTaskState } from "../lib/direction-tree";
import { cx } from "../ui/atoms";

/** "在做 N · 等你 N" from the hub's counts; "等你 N" in the needs-you tone while it is above zero. */
export const DirectionCountsText = memo(function DirectionCountsText({ counts, className }: { counts: DirectionCounts; className?: string }) {
  const label = directionCountsText(counts);
  return (
    <span title={label} data-direction-counts className={cx("shrink-0 whitespace-nowrap text-[11px] text-ink-3 tabular-nums", className)}>
      <span className="sr-only">{label}</span>
      <span aria-hidden>
        {t("lib-state-labels.direction_working_n", { count: counts.working })}
        {" · "}
        <span className={cx(counts.needs_you > 0 && "font-semibold text-attention")}>{t("lib-state-labels.direction_needs_you_n", { count: counts.needs_you })}</span>
      </span>
    </span>
  );
});

/** A task's state word in its tone: 等你 / 待验收 / 卡住 / 在做. */
export const DirectionStateText = memo(function DirectionStateText({ state }: { state: DirectionTaskState }) {
  return <span data-task-state={state} className={cx("shrink-0 text-[11px] font-semibold whitespace-nowrap", DIRECTION_STATE_TONE[state])}>{directionStateName(state)}</span>;
});
