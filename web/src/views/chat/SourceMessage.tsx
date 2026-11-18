import { useLayoutEffect, useRef, useState } from "react";
import { t } from "../../i18n";
import { isRelayContext, sourceLabel } from "../../lib/message-source";
import { parseUserText } from "../../lib/turns";
import type { UserMessage } from "../../lib/turns";
import { cx } from "../../ui/atoms";
import { Markdown } from "../../ui/Markdown";
import { Icon } from "../../ui/icons";
import { useChatContext } from "./chat-context";
import { useExpanded } from "./expanded";
import { useFullText } from "./FullText";
import { keepLineBreaks } from "./Messages";
import { useReveal } from "./search-reveal";
import { AttachedPhotos } from "./AttachedPhotos";
import { FileCards } from "./FileCards";
import { Thumb } from "./Steps";
import { ContextGroup } from "./ContextGroup";

/** Relayed words and automatic inputs are sources in one chat, never participants or user bubbles. */
type SourceProps = { msg: UserMessage; mid: string; sid: string };

export function SourceMessage(props: SourceProps) {
  return props.msg.source?.kind === "scheduled_task" ? <ScheduledTaskMessage {...props} /> : <RelayedSourceMessage {...props} />;
}

/** A Codex-style automation input: two lines that cannot scroll, with the rest behind a disclosure. */
function ScheduledTaskMessage({ msg, mid, sid }: SourceProps) {
  const { scope } = useChatContext();
  const [open, setOpen] = useExpanded(`${scope}source:${msg.id}`);
  const item = msg.rawInput?.find((part) => part.id === msg.id);
  const full = useFullText(item, mid, sid);
  const text = item ? full.text : msg.text;
  const preview = useRef<HTMLDivElement>(null);
  const [overflow, setOverflow] = useState<boolean>();
  useLayoutEffect(() => {
    const el = preview.current;
    if (!el || open) return;
    const measure = () => {
      if (el.clientHeight > 0) setOverflow(el.scrollHeight > el.clientHeight + 1);
    };
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [text, open]);
  const long = open || !!full.more || (overflow ?? (text.length > 90 || text.split("\n").length > 2));
  useReveal(scope, [msg.id], () => {
    if (open) return;
    setOpen(true);
    return () => setOpen(false);
  });
  return (
    <div className="chat-scheduled-source" data-message-source="scheduled_task" data-items={msg.id}>
      <span className="flex max-w-full items-center gap-1 text-right text-[length:var(--chat-secondary)] text-ink-3">
        <Icon name="clock" size={13} className="shrink-0" />
        {sourceLabel(msg.source!)}
      </span>
      <div className="min-w-0 rounded-card bg-neutral-bubble px-4 py-2.5 text-ink">
        <div ref={preview} className="chat-scheduled-body" data-collapsed={!open || undefined} data-source-folded={!open && long || undefined}>{text}</div>
        {open && full.more}
        {long && <button type="button" className="chat-source-toggle mt-1.5 inline-flex items-center gap-1" onClick={() => setOpen((value) => !value)} aria-expanded={open}>
          {t(open ? "chat-segmentview.collapse" : "chat-messages.show_more")}
          <Icon name="chevron" size={13} className={open ? "rotate-180" : undefined} />
        </button>}
      </div>
    </div>
  );
}

function RelayedSourceMessage({ msg, mid, sid }: SourceProps) {
  const { scope } = useChatContext();
  const [open, setOpen] = useExpanded(`${scope}source:${msg.id}`);
  const [measuredLong, setMeasuredLong] = useState(false);
  const full = useFullText(msg.injected ? undefined : msg.rawInput?.find((item) => item.kind === "text" && item.full_size), mid, sid);
  // a scheduled task's prompt stays out of the main conversation while background rounds fold (promptHidden)
  const text = msg.injected ? (msg.promptHidden ? "" : msg.injected.detail) || msg.injected.line : full.text ? parseUserText(full.text).text : msg.text;
  const long = measuredLong || text.length > 180 || text.split("\n").length > 3;
  useReveal(scope, [msg.id], () => {
    if (open) return;
    setOpen(true);
    return () => setOpen(false);
  });
  const label = sourceLabel(msg.source!);
  const contexts = (full.text ? parseUserText(full.text).contexts : msg.contexts).filter((context) => context.kind !== "message-source" && !isRelayContext(context));
  return (
    <div className="chat-source" data-message-source={msg.source!.kind} data-source-conversation={msg.source!.conversation_id} data-items={[msg.id, ...msg.images.map((item) => item.id)].join(" ")}>
      {!msg.continuedSource && <span className="chat-source-label" title={label}>@{label}</span>}
      <div className={cx("chat-source-body", long && !open && "chat-source-folded")} data-source-folded={long && !open || undefined} ref={(el) => {
        if (!el || open) return;
        const line = parseFloat(getComputedStyle(el).lineHeight);
        if (line && el.scrollHeight > line * 3.5 && !measuredLong) setMeasuredLong(true);
      }}>
        <Markdown text={keepLineBreaks(text)} mid={mid} small />
      </div>
      {long && <button type="button" className="chat-source-toggle" onClick={() => setOpen((value) => !value)} aria-expanded={open}>
        {t(open ? "chat-segmentview.collapse" : "chat-segmentview.expand")}
      </button>}
      {contexts.length > 0 && <ContextGroup contexts={contexts} />}
      {msg.images.length > 0 && <div className="flex flex-wrap gap-1.5">{msg.images.map((image) => <Thumb key={image.id} item={image} mid={mid} sid={sid} />)}</div>}
      {msg.photos && <AttachedPhotos paths={msg.photos} mid={mid} sid={sid} />}
      {msg.files && <FileCards files={msg.files} mid={mid} />}
      {!msg.injected && full.more}
    </div>
  );
}
