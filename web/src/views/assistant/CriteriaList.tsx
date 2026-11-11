import { t } from "../../i18n";
import type { CheckMark, ChecklistEntry } from "../../lib/supervision";
import { cx } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

const MARK: Record<CheckMark, { icon: "check" | "x" | "clock" | "minus"; className: string; label: () => string }> = {
  met: { icon: "check", className: "text-green", label: () => t("assistant-tasks.review_met") },
  missing: { icon: "x", className: "text-red", label: () => t("assistant-tasks.review_missing") },
  open: { icon: "clock", className: "text-ink-3", label: () => t("assistant-tasks.review_open") },
  // Added after the acceptance, which still stands (#3156): nothing waits on it.
  unchecked: { icon: "minus", className: "text-ink-3", label: () => t("assistant-tasks.review_unchecked") },
};

/** Who supplied the latest evidence, not an independent task acceptance. */
function criterionReporter(by: string): string {
  if (by.startsWith("agent:")) return t("assistant-tasks.criterion_agent", { id: by.slice(6) });
  if (by.startsWith("supervisor:")) return t("assistant-tasks.criterion_supervisor", { id: by.slice(11) });
  if (by === "judge") return t("assistant-tasks.criterion_judge");
  return by;
}

/** Shared by the task detail and disclosures, loaded only when the evidence is visible. */
export function CriteriaList({ list }: { list: ChecklistEntry[] }) {
  return (
    <ul className="mt-2 flex min-w-0 flex-col gap-1.5">
      {list.map((c) => {
        const m = MARK[c.mark];
        return (
          <li key={c.criterion} className="flex min-w-0 items-start gap-2 text-[13px]">
            <span role="img" aria-label={m.label()} title={m.label()} className={cx("mt-0.5 shrink-0", m.className)}>
              <Icon name={m.icon} size={13} stroke={2.4} />
            </span>
            <span className="flex min-w-0 flex-col [overflow-wrap:anywhere]">
              <span className="text-ink">{c.criterion}</span>
              {c.evidence && <span className="whitespace-pre-wrap text-[12px] text-ink-3 select-text">{c.evidence}</span>}
              {(c.at || c.by) && (
                <span className="flex flex-wrap gap-x-1.5 text-[11px] text-ink-3">
                  {c.by && <span>{criterionReporter(c.by)}</span>}
                  {c.at && <time dateTime={c.at} title={c.at}>{new Date(c.at).toLocaleString()}</time>}
                </span>
              )}
            </span>
          </li>
        );
      })}
    </ul>
  );
}
