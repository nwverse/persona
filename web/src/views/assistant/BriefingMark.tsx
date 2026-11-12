// A decision held for the briefing (#4930, #5803, docs/assistant/todos.md "Decisions that can wait"): its supervisor
// said it can wait, so nothing was pushed for it and it comes with the next briefing; the to-do's ask says
// `for_briefing` meanwhile. Without the mark a held decision looks like any other in Needs you, and the owner cannot
// tell why it did not ring. On the decision's card the mark has its meaning written out beside it (a touch screen has
// no hover); the To-dos page draws its own on a row and on the to-do's page (views/Todos HeldForBriefing).
import { t } from "../../i18n";
import { Icon } from "../../ui/icons";

export function BriefingMark() {
  return (
    <p data-for-briefing="caption" className="flex min-w-0 items-start gap-1.5 text-xs text-ink-2 [overflow-wrap:anywhere]">
      <Icon name="clock" size={13} className="mt-0.5 shrink-0" />
      <span>
        <span className="font-medium">{t("assistant-decisionrow.for_briefing")}</span>
        {t("assistant-decisionrow.separator")}
        {t("assistant-decisionrow.for_briefing_hint")}
      </span>
    </p>
  );
}
