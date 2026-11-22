// The folding rows load when the Needs you tray opens. Its bar registers Back immediately.
import { useEffect, useRef } from "react";
import { t } from "../../i18n";
import { needsYouBySupervisor, type NeedsYouItem } from "../../lib/assistant-entries";
import { field } from "../../lib/feed";
import { sessionTitle } from "../../lib/format";
import { openAgent, openTask } from "../../lib/handoff";
import { interactionKicker } from "../../lib/row-line";
import { useStore } from "../../lib/store";
import type { Interaction } from "../../lib/types";
import { conversationTitle } from "./conversations";
import { stateName } from "../../lib/state-labels";
import { openAsk } from "../../lib/todos";
import { cx } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { StatusMark } from "../../ui/StatusMark";
import { cardLine } from "../chat/dock";
import { isDecision, NeedsYouBody } from "./Tasks";

/** What a Needs you row says folded (#5569): its mark, the title and, under it, what it waits for, with the kind at the
 *  end. None for the rows that are one line already (an Agent with no question, a task that only waits, a failed start). */
function foldedRow(item: NeedsYouItem): { title: string; line?: string; kicker: string } | undefined {
  if (item.kind === "plan") {
    const first = item.plan.tasks[0]?.title ?? t("assistant-tasks.plan_single");
    return { title: item.plan.tasks.length > 1 ? t("assistant-tasks.needs_more", { title: first, count: item.plan.tasks.length - 1 }) : first, kicker: t("assistant-tasks.plan_waiting") };
  }
  if (item.kind === "message") return { title: [field(item.item, "sender"), field(item.item, "chat")].filter(Boolean).join(" · ") || t("assistant-tasks.task_fallback"), line: field(item.item, "text"), kicker: stateName("needs_you") };
  if (item.kind === "agent") return item.asks.length ? { title: sessionTitle(item.session), line: cardLine(item.asks[0]), kicker: interactionKicker(item.asks[0]) } : undefined;
  if (item.asks.length) return { title: item.todo.title, line: cardLine(item.asks[0]), kicker: interactionKicker(item.asks[0]) };
  const ask = openAsk(item.todo);
  // a decision held for the briefing says so folded too (#5803): why it did not ring
  if (isDecision(item.todo) || ask) return { title: item.todo.title, line: ask?.verdict, kicker: ask?.for_briefing === true ? t("todos.ask_held") : stateName("needs_you") };
  return undefined;
}

/** A Needs you row. Open the folded line to answer in place; its expanded title opens the asking Agent (or the task
 *  for a task-level decision), while the chevron folds it again. */
function NeedsYouRow({ item, open, onToggle }: { item: NeedsYouItem; open: boolean; onToggle: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  const opened = useRef(open);
  useEffect(() => {
    const row = ref.current;
    const first = open && !opened.current;
    opened.current = open;
    if (!first || !row) return;
    // A card taller than the list shows its top: "nearest" would leave its question and buttons below the fold.
    const reveal = () => {
      const list = row.closest("[data-tray-list]");
      row.scrollIntoView?.({ block: list && row.offsetHeight > list.clientHeight ? "start" : "nearest" });
    };
    reveal();
    // The card's body loads after the row opens (a lazy chunk): align again as the row grows, for a moment, so the
    // finished card is what ends up in view and not the placeholder.
    if (typeof ResizeObserver === "undefined") return;
    const growing = new ResizeObserver(reveal);
    growing.observe(row);
    const stop = window.setTimeout(() => growing.disconnect(), 1000);
    return () => { window.clearTimeout(stop); growing.disconnect(); };
  }, [open]);
  const folded = foldedRow(item);
  // Whether the To-dos page counts this to-do as open (#3331): no attribute on rows that are no to-do. The chip badge
  // is summed from this same flag, so the trays and the header tell one number.
  const countsOpenAttr = item.kind === "task" ? { "data-counts-open": item.countsOpen ? "true" : "false" } : {};
  if (!folded) return <NeedsYouBody item={item} />;
  const openTitle = item.kind === "agent" ? () => openAgent(item.session.machine_id, item.session.session_id)
    : item.kind === "task" ? () => {
      const ask = item.asks[0];
      if (ask) openAgent(ask.machine_id, ask.session_id);
      else openTask(item.todo.id);
    } : undefined;
  const label = <>
    <StatusMark state="needs_you" />
    <span className="min-w-0 flex-1">
      <span className="block truncate font-semibold">{folded.title}</span>
      {!open && folded.line && folded.line !== folded.kicker && <span className="block truncate text-xs text-ink-2">{folded.line}</span>}
    </span>
  </>;
  const chevron = <Icon name="chevronRight" size={14} className="shrink-0 text-ink-3 transition-transform duration-150 motion-reduce:transition-none" style={{ transform: open ? "rotate(90deg)" : "none" }} />;
  const head = open && openTitle ? (
    <div className="flex min-w-0 items-center rounded-control px-3 py-2 text-sm">
      <button type="button" data-needs-title onClick={openTitle} className="flex min-w-0 flex-1 items-center gap-2 text-left hover:underline">
        {label}
      </button>
      <button type="button" aria-expanded={open} aria-label={t("assistant-tasks.summary_collapse")} onClick={onToggle} className="touch-hit ml-2 flex size-6 shrink-0 items-center justify-center rounded-control text-ink-3 hover:bg-hover">
        {chevron}
      </button>
    </div>
  ) : (
    <button
      type="button"
      data-needs-row={open ? undefined : item.key}
      {...countsOpenAttr}
      aria-expanded={open}
      onClick={onToggle}
      className={cx("flex w-full min-w-0 items-center gap-2 rounded-control px-3 py-2 text-left text-sm hover:bg-hover", open ? "" : "bg-field")}
    >
      {label}
      {!open && <span className="max-w-[40%] shrink-0 truncate text-right text-xs text-attention">{folded.kicker}</span>}
      {chevron}
    </button>
  );
  if (!open) return head;
  return (
    <div ref={ref} data-needs-row={item.key} data-needs-open {...countsOpenAttr} className="flex scroll-m-2 flex-col gap-1">
      {head}
      <NeedsYouBody item={item} nested />
    </div>
  );
}

/** What a Needs you row's open state is kept under: its key and the round it asks, so the same task or Agent asking
 *  again comes back folded while a row that only blinks out (hidden for a moment, then restored) keeps its state. */
function openKey(item: NeedsYouItem): string {
  if (item.kind === "task") return `${item.key}@${item.todo.supervision?.reviews?.at(-1)?.at || item.todo.decision_review?.review_at || openAsk(item.todo)?.asked_at || item.asks.map((ix) => ix.id).join(",")}`;
  if (item.kind === "agent") return `${item.key}@${item.asks.map((ix) => ix.id).join(",")}`;
  return item.key;
}

/** One of the conversation's own cards in Needs you (#4883): the card itself is in the chat above the composer, as on
 *  iOS (ActionTrayContent's asks); its row says what it waits for and closes the tray onto it. */
function OwnAskRow({ ix, onOpen }: { ix: Interaction; onOpen: () => void }) {
  return (
    <button type="button" data-own-ask={ix.id} onClick={onOpen} className="flex items-center gap-2 rounded-control bg-field px-3 py-2 text-left text-sm">
      <StatusMark state="needs_you" />
      <span className="min-w-0 flex-1 truncate">{cardLine(ix)}</span>
      <span className="shrink-0 whitespace-nowrap text-xs text-ink-2">{interactionKicker(ix)}</span>
    </button>
  );
}

export function NeedsYouRows({ items, conversation, ownAsks, openOwn, openRows, toggleRow, count }: {
  items: NeedsYouItem[];
  conversation: string;
  ownAsks: Interaction[];
  openOwn: (id: string) => void;
  openRows: ReadonlySet<string>;
  toggleRow: (key: string) => void;
  count: number;
}) {
  const isAsking = (item: NeedsYouItem) => (item.kind === "task" || item.kind === "agent") && item.asks.length > 0;
  // While background rounds fold, rows other conversations supervise are grouped under them (#4314); with the
  // switch off, or every row this conversation's, the tray is as it was.
  const foldBackground = useStore((s) => s.assistant?.main_background_folded) !== false;
  const conversations = useStore((s) => s.assistant?.conversations);
  const groups = foldBackground ? needsYouBySupervisor(items, conversation) : [{ supervisor: conversation, items: items }];
  const own = groups[0].items;
  const asking = own.filter(isAsking);
  const rest = own.filter((item) => !asking.includes(item));
  const supervisorTitle = (id: string) => {
    const row = conversations?.find((c) => c.session_id === id);
    return row ? conversationTitle(row) : t("assistant-tasks.needs_group_another");
  };
  return (
    <div className="flex flex-col gap-2 p-2">
      {asking.map((item) => <NeedsYouRow key={item.key} item={item} open={openRows.has(openKey(item))} onToggle={() => toggleRow(openKey(item))} />)}
      {ownAsks.map((ix) => <OwnAskRow key={ix.id} ix={ix} onOpen={() => openOwn(ix.id)} />)}
      {rest.map((item) => <NeedsYouRow key={item.key} item={item} open={openRows.has(openKey(item))} onToggle={() => toggleRow(openKey(item))} />)}
      {groups.slice(1).map((group) => (
        <section key={group.supervisor} data-needs-supervisor={group.supervisor} aria-label={supervisorTitle(group.supervisor)} className="flex flex-col gap-2">
          <h3 className="truncate px-1 pt-1 text-xs text-ink-2">{supervisorTitle(group.supervisor)}</h3>
          {[...group.items.filter(isAsking), ...group.items.filter((item) => !isAsking(item))].map((item) => <NeedsYouRow key={item.key} item={item} open={openRows.has(openKey(item))} onToggle={() => toggleRow(openKey(item))} />)}
        </section>
      ))}
      {!count && <p className="px-1 py-2 text-sm text-ink-2">{t("assistant-tasks.tray_empty_pending")}</p>}
    </div>
  );
}
