// A to-do's token usage and estimated cost (#12936): a line on its card, and a section in its detail broken down by
// session, subagent and model. Every amount says it is an estimate at public prices.

import { useEffect, useState } from "react";
import { t } from "../i18n";
import { formatUSD, formatUsageTokens, loadTodoUsage, useTodoUsage, type TodoUsageDetail, type TodoUsagePart, type TodoUsageTokens } from "../lib/todo-usage";
import { cx } from "../ui/atoms";

/** The card's line: its tokens and the estimate; nothing until known or when it used none. */
export function TodoUsageLine({ todoId, hasAgents, className }: { todoId: string; hasAgents: boolean; className?: string }) {
  const u = useTodoUsage(todoId, hasAgents);
  if (!u) return null;
  return (
    <div className={cx("truncate text-[11.5px] text-ink-3", className)} data-testid="todo-usage-line" title={t("todo-usage.estimate_note")}>
      {t("todo-usage.card", { tokens: formatUsageTokens(u.tokens.total), usd: formatUSD(u.usd) })}
    </div>
  );
}

function kinds(tk: TodoUsageTokens): string {
  return t("todo-usage.kinds", {
    input: formatUsageTokens(tk.in),
    write: formatUsageTokens(tk.cache_write),
    read: formatUsageTokens(tk.cache_read),
    output: formatUsageTokens(tk.out),
  });
}

function PartRow({ part, nested }: { part: TodoUsagePart; nested?: boolean }) {
  const title = nested
    ? [part.kind, part.title].filter(Boolean).join(" · ") || t("todo-usage.subagent")
    : part.title || t("todo-usage.session");
  return (
    <div className={cx("flex flex-col gap-0.5", nested && "border-l border-line pl-3")} data-testid={nested ? "todo-usage-subagent" : "todo-usage-session"}>
      <div className="flex items-baseline justify-between gap-3">
        <span className={cx("truncate text-[13px] text-ink", !nested && "font-semibold")}>{title}</span>
        <span className="shrink-0 whitespace-nowrap text-[12px] text-ink-2 tabular-nums">
          {t("todo-usage.amount", { tokens: formatUsageTokens(part.tokens.total), usd: formatUSD(part.usd) })}
        </span>
      </div>
      {part.tokens.total > 0 && <div className="truncate text-[11.5px] text-ink-3 tabular-nums">{kinds(part.tokens)}</div>}
      {!nested && !part.found && <div className="text-[11.5px] text-ink-3">{t("todo-usage.no_records")}</div>}
      {!nested && part.stale && part.found && <div className="text-[11.5px] text-orange">{t("todo-usage.stale")}</div>}
    </div>
  );
}

/** The detail's section: the total, each session with its subagents, each model with its price. */
export function TodoUsageSection({ todoId, hasAgents, version }: { todoId: string; hasAgents: boolean; version?: string }) {
  const [detail, setDetail] = useState<TodoUsageDetail | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    if (!hasAgents) return;
    let live = true;
    setFailed(false);
    loadTodoUsage(todoId).then(
      (d) => live && setDetail(d),
      () => live && setFailed(true),
    );
    return () => {
      live = false;
    };
  }, [todoId, hasAgents, version]);
  useEffect(() => setDetail(null), [todoId]);
  if (!hasAgents || failed || !detail || detail.todo_id !== todoId) return null;
  return (
    <div className="mt-6" data-testid="todo-usage-section">
      <div className="mb-2 text-[11.5px] font-medium tracking-[0.04em] text-ink-3">{t("todo-usage.section")}</div>
      <div className="flex flex-col gap-3 rounded-control border border-line bg-canvas p-3">
        <div className="flex flex-col gap-0.5">
          <div className="flex items-baseline justify-between gap-3">
            <span className="text-[15px] font-semibold text-ink tabular-nums" data-testid="todo-usage-total">
              {t("todo-usage.total", { tokens: formatUsageTokens(detail.tokens.total) })}
            </span>
            <span className="shrink-0 whitespace-nowrap text-[13px] font-medium text-ink tabular-nums" data-testid="todo-usage-usd">
              {t("todo-usage.estimate", { usd: formatUSD(detail.usd) })}
            </span>
          </div>
          <div className="text-[11.5px] text-ink-3 tabular-nums">{kinds(detail.tokens)}</div>
        </div>
        {detail.parts.map((p) => (
          <div key={`${p.machine_id}/${p.session_id}`} className="flex flex-col gap-2">
            <PartRow part={p} />
            {p.subagents?.map((s) => <PartRow key={s.subagent} part={s} nested />)}
          </div>
        ))}
        {detail.models.length > 0 && (
          <div className="flex flex-col gap-0.5 border-t border-line pt-2">
            {detail.models.map((m) => (
              <div key={m.model} className="flex items-baseline justify-between gap-3 text-[11.5px] text-ink-3">
                <span className="truncate">{m.model}</span>
                <span className="shrink-0 whitespace-nowrap tabular-nums">
                  {t(`todo-usage.price_${m.price_from}`, {
                    input: m.price.input, write: m.price.cache_write, read: m.price.cache_read, output: m.price.output,
                  })}
                </span>
              </div>
            ))}
          </div>
        )}
        <div className="text-[11.5px] text-ink-3">{t("todo-usage.estimate_note")}</div>
      </div>
    </div>
  );
}
