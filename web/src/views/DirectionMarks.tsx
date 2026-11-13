// The direction tree's two marks (#4352) on the To-dos page and the Agents page: a direction's counts, in the words and
// the shape of the sidebar's 常驻 rows (lib/direction-names.ts countsLabel: the non-zero parts), and a task's state word.
// Text, so a state never rides on color alone.
import { memo } from "react";
import { t } from "../i18n";
import { countsLabel } from "../lib/direction-names";
import { DIRECTION_STATE_TONE, directionStateName, type DirectionCounts, type DirectionTaskState } from "../lib/direction-tree";
import { cx } from "../ui/atoms";
import { StatusMark } from "../ui/StatusMark";

/** A direction's busy and attention marks on the To-dos page and the Agents page (#6626): one busy expression only —
 *  the same spinning arc a conversation row draws while a live Agent runs; the working count is never spelled out.
 *  The "等你 N" attention count stays as words. The full "在做 N · 等你 N" phrase still titles the element and is spoken
 *  to screen readers, so the numbers remain reachable without a second visible expression. `working` is the hub tree's
 *  live-session flag; absent on an older hub, fall back to the task count so the mark never regresses. */
export const DirectionCountsText = memo(function DirectionCountsText({ counts, working, className }: { counts: DirectionCounts; working?: boolean; className?: string }) {
  const label = countsLabel(counts, t);
  const busy = working ?? (counts.working > 0);
  const needsYou = counts.needs_you > 0 ? t("sidebar.leader_needs_you", { count: counts.needs_you }) : "";
  // The live-session flag alone must still draw the arc even when no count is non-zero: the guard is the mark, not the
  // words. Nothing to say and nothing spinning is the only empty case.
  if (!label && !busy) return null;
  return (
    <span title={label} data-direction-counts className={cx("flex shrink-0 items-center gap-1 whitespace-nowrap text-[11px] text-ink-3 tabular-nums", className)}>
      {label && <span className="sr-only">{label}</span>}
      <span aria-hidden className="flex items-center gap-1">
        {busy && <StatusMark state="working" size="sm" label={false} />}
        {needsYou && <span className="font-semibold text-attention">{needsYou}</span>}
      </span>
    </span>
  );
});

/** A task's state word in its tone: 等你 / 待验收 / 卡住 / 在做. */
export const DirectionStateText = memo(function DirectionStateText({ state }: { state: DirectionTaskState }) {
  return <span data-task-state={state} className={cx("shrink-0 text-[11px] font-semibold whitespace-nowrap", DIRECTION_STATE_TONE[state])}>{directionStateName(state)}</span>;
});
