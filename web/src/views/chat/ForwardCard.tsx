// A forwarded message as a card (#3899; iOS QuoteViews.swift QuoteBarRow / QuoteBlock with the forward arrow): over the
// composer, the message about to go with the line saying which computer it comes from and where it goes, × to drop it;
// at the top of a sent bubble, "Forwarded from Claude · chat", the computer and time and the first lines, a click opening
// the original chat at that message (or the whole text, when this page does not have that chat). Drawn as the quote's
// bar and card (Quote.tsx) with the forward arrow before the heading, so a forward reads as a quote of another chat.
// Several of one chat next to each other (#3838) are one bar and one card, with a line for each message.
import { Suspense, useLayoutEffect, useRef, useState } from "react";
import { t } from "../../i18n";
import { forwardGroupHeading, forwardHeading, forwardLine, forwardMeta, forwardOrigin, forwardRuns, forwardSummary, jumpWhenOpen } from "../../lib/forward";
import { removeContext, useContexts, type ContextRef } from "../../lib/links";
import { navigate } from "../../lib/router";
import { getSession, getState } from "../../lib/store";
import type { Context } from "../../lib/turns";
import { Icon } from "../../ui/icons";
import { Markdown } from "../../ui/Markdown";
import { lazyPage } from "../../lib/lazy-page";
import { ForwardBar } from "./ForwardBar";

// several of one chat over the box (#3838): Forward to, which puts them there, has loaded it by then
const ForwardGroupBar = lazyPage(() => import("./ForwardGroupBar").then((m) => m.ForwardGroupBar));

/** The forwards waiting above a composer's input (lib/links.ts addContext), several of one chat as one bar (#3838);
 *  other contexts are ContextChips'. */
export function ForwardBars({ draftKey, onRemove }: { draftKey: string; onRemove?: () => void }) {
  const forwards = useContexts(draftKey).filter((r) => r.kind === "forward");
  if (!forwards.length) return null;
  const drop = (rs: ContextRef[]) => {
    rs.forEach((r) => removeContext(draftKey, r.id));
    onRemove?.();
  };
  return (
    <div className="flex flex-col gap-1.5 px-0.5 pt-0.5" data-forwards>
      {forwardRuns(forwards).map((run) => {
        const rs = run.map((i) => forwards[i]);
        return rs.length > 1 ? (
          <Suspense key={rs[0].id} fallback={null}>
            <ForwardGroupBar forwards={rs} onRemove={() => drop(rs)} />
          </Suspense>
        ) : (
          <ForwardBar key={rs[0].id} forward={rs[0]} lines={forwards.length === 1 ? 2 : 1} onRemove={() => drop(rs)} />
        );
      })}
    </div>
  );
}

/** Opens the chat a forward came from, at the message, when this page has that chat; false otherwise. */
export function openForwarded(c: Pick<Context, "url" | "attrs">): boolean {
  const o = forwardOrigin(c.url ?? "", c.attrs?.msg);
  if (!o) return false;
  const st = getState();
  const main = st.assistant?.machine_id === o.machine && st.assistant?.main === o.session;
  const session = getSession(o.machine, o.session);
  if (!main && !session) return false;
  if (o.item) jumpWhenOpen(o.machine, o.session, o.item);
  if (main) navigate({ name: "persona" });
  else if (session?.assistant) navigate({ name: "persona", sid: o.session });
  else navigate({ name: "session", mid: o.machine, sid: o.session, view: "chat" });
  return true;
}

/** A sent forward at the top of its bubble: the arrow and heading, the computer and time, four lines (iOS QuoteBlock.forwardLines); a click
 *  opens the original (openForwarded), or, when this page does not have that chat, the whole text in place. */
export function ForwardBlock({ c }: { c: Context }) {
  const clampRef = useRef<HTMLSpanElement>(null);
  const [open, setOpen] = useState(false);
  const [long, setLong] = useState(false);
  const heading = forwardHeading(c.attrs);
  const meta = forwardMeta(c.attrs);
  const summary = forwardSummary(c.text);
  useLayoutEffect(() => {
    const el = clampRef.current;
    if (el && !open) setLong(el.scrollHeight > el.clientHeight + 1);
  }, [summary, open]);
  const go = () => {
    if (!openForwarded(c)) setOpen(true);
  };
  return (
    <div className="flex min-w-0 gap-2 rounded-[8px] bg-ink/[0.05] py-1.5 pr-2.5 pl-2 whitespace-normal" data-forward>
      <span aria-hidden className="w-[3px] shrink-0 self-stretch rounded-full bg-ink/20" />
      <div className="flex min-w-0 flex-1 flex-col items-start gap-0.5">
        <button
          type="button"
          onClick={go}
          title={t("chat-forward.open_original")}
          aria-label={[heading, meta, summary].filter(Boolean).join(": ")}
          // a grid track of minmax(0, auto): the heading's one line never widens the bubble past the chat; it truncates
          className="grid w-full min-w-0 grid-cols-[minmax(0,auto)] gap-0.5 rounded-[6px] text-left"
        >
          <span className="flex max-w-full min-w-0 items-center gap-1 text-[12px] font-medium whitespace-nowrap text-ink-3">
            <Icon name="forward" size={12} className="shrink-0" />
            <span className="min-w-0 truncate">{heading}</span>
          </span>
          {meta && <span className="max-w-full truncate text-[11.5px] text-ink-3">{meta}</span>}
          {!open && (
            <span ref={clampRef} className="line-clamp-4 text-[13px] leading-[1.55] whitespace-pre-line text-ink-2 [overflow-wrap:anywhere]">
              {summary}
            </span>
          )}
        </button>
        {open && (
          <div className="max-h-[320px] w-full overflow-y-auto text-ink-2 [overflow-wrap:anywhere]" data-scroll-box>
            <Markdown text={c.text} small compact />
          </div>
        )}
        {(long || open) && (
          <button
            type="button"
            onClick={() => setOpen((o) => !o)}
            aria-expanded={open}
            className="-ml-1 rounded-[6px] px-1 text-[12px] font-medium whitespace-nowrap text-ink-3 transition-colors hover:bg-hover-2 hover:text-ink-2"
          >
            {open ? t("chat-forward.collapse") : t("chat-forward.show_all")}
          </button>
        )}
      </div>
    </div>
  );
}

/** The messages of a group a bubble shows before "2 more" (iOS ForwardGroupBlock). */
const GROUP_SHOWN = 3;

/** Several forwards of one chat at the top of a bubble as one card (#3838; iOS ForwardGroupBlock): the arrow and "3
 *  forwarded messages · chat", then each message on up to two lines ("Claude: Three times…"), a click opening it in its
 *  chat (or its whole text in place, when this page does not have that chat); past GROUP_SHOWN, "2 more" shows the rest. */
export function ForwardGroupBlock({ group }: { group: Context[] }) {
  const [all, setAll] = useState(false);
  const heading = forwardGroupHeading(group);
  const shown = all ? group : group.slice(0, GROUP_SHOWN);
  const more = group.length - shown.length;
  return (
    <div className="flex min-w-0 gap-2 rounded-[8px] bg-ink/[0.05] py-1.5 pr-2.5 pl-2 whitespace-normal" data-forward data-forward-group={group.length}>
      <span aria-hidden className="w-[3px] shrink-0 self-stretch rounded-full bg-ink/20" />
      <div className="flex min-w-0 flex-1 flex-col items-start gap-1">
        <span className="flex max-w-full min-w-0 items-center gap-1 text-[12px] font-medium whitespace-nowrap text-ink-3">
          <Icon name="forward" size={12} className="shrink-0" />
          <span className="min-w-0 truncate">{heading}</span>
        </span>
        {shown.map((c, i) => (
          <GroupLine key={i} c={c} />
        ))}
        {more > 0 && (
          <button
            type="button"
            onClick={() => setAll(true)}
            className="-ml-1 rounded-[6px] px-1 text-[12px] font-medium whitespace-nowrap text-ink-3 transition-colors hover:bg-hover-2 hover:text-ink-2"
          >
            {t("chat-forward.more", { count: more })}
          </button>
        )}
      </div>
    </div>
  );
}

/** One message of a group card: its line, a click opening the original (openForwarded) or its whole text here. */
function GroupLine({ c }: { c: Context }) {
  const [open, setOpen] = useState(false);
  const line = forwardLine(c);
  const meta = forwardMeta(c.attrs);
  return (
    <div className="flex w-full min-w-0 flex-col items-start gap-0.5">
      <button
        type="button"
        onClick={() => {
          if (!openForwarded(c)) setOpen((o) => !o);
        }}
        title={[t("chat-forward.open_original"), meta].filter(Boolean).join(" · ")}
        aria-label={[line, meta].filter(Boolean).join(" · ")}
        aria-expanded={open || undefined}
        className="grid w-full min-w-0 grid-cols-[minmax(0,auto)] rounded-[6px] text-left"
      >
        <span className="line-clamp-2 text-[13px] leading-[1.55] text-ink-2 [overflow-wrap:anywhere]">{line}</span>
      </button>
      {open && (
        <div className="max-h-[320px] w-full overflow-y-auto text-ink-2 [overflow-wrap:anywhere]" data-scroll-box>
          <Markdown text={c.text} small compact />
        </div>
      )}
    </div>
  );
}
