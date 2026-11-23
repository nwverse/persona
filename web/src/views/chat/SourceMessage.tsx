import { lazy, Suspense, useState } from "react";
import { t } from "../../i18n";
import { isRelayContext, sourceLabel } from "../../lib/message-source";
import { parseUserText } from "../../lib/turns";
import type { UserMessage } from "../../lib/turns";
import { cx } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { Markdown } from "../../ui/Markdown";
import { useChatContext } from "./chat-context";
import { useExpanded } from "./expanded";
import { useFullText } from "./FullText";
import { keepLineBreaks } from "./Messages";
import { useReveal } from "./search-reveal";
import { AttachedPhotos } from "./AttachedPhotos";
import { FileCards } from "./FileCards";
import { Thumb } from "./Steps";
import { ContextGroup } from "./ContextGroup";

export type SourceProps = { msg: UserMessage; mid: string; sid: string };

// Keep the scheduled-task disclosure off the startup path for chats without one.
const ScheduledTaskMessage = lazy(() => import("./ScheduledTaskMessage"));

/** Relayed words and automatic inputs are sources in one chat, never participants or user bubbles. */
export function SourceMessage(props: SourceProps) {
  if (props.msg.source?.kind === "scheduled_task") {
    return <Suspense fallback={<div className="h-24" aria-hidden="true" />}><ScheduledTaskMessage {...props} /></Suspense>;
  }
  return <RelayedSourceMessage {...props} />;
}

/** A partner hub's words as it wrote them: the manager writes the `<` of every tag start in them as `&lt;` so the model
 *  never reads a tag there (prompts.PlainWords); shown as Markdown, which never renders raw HTML, they get it back, so
 *  `Vec<String>` in a code span reads as written. */
export function partnerWords(text: string): string {
  return text.replace(/&lt;(?=\s*[/\\]*\s*\p{L})/gu, "<");
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
  // another hub's assistant asks: its hub's name with the hub mark, and once a line that it is not the user's request
  const hub = msg.source!.kind === "hub";
  const contexts = (full.text ? parseUserText(full.text).contexts : msg.contexts).filter((context) => context.kind !== "message-source" && !isRelayContext(context));
  return (
    <div className="chat-source" data-message-source={msg.source!.kind} data-source-conversation={msg.source!.conversation_id} data-source-hub={hub ? msg.source!.hub_id : undefined} data-items={[msg.id, ...msg.images.map((item) => item.id)].join(" ")}>
      {!msg.continuedSource && (hub ? (
        <span className="chat-source-label flex items-center gap-1" title={label}>
          <Icon name="hub" size={12} className="shrink-0" />
          <span className="truncate">@{label}</span>
        </span>
      ) : <span className="chat-source-label" title={label}>@{label}</span>)}
      {!msg.continuedSource && hub && <span className="chat-source-note">{t("chat-messages.source_hub_note")}</span>}
      <div className={cx("chat-source-body", long && !open && "chat-source-folded")} data-source-folded={long && !open || undefined} ref={(el) => {
        if (!el || open) return;
        const line = parseFloat(getComputedStyle(el).lineHeight);
        if (line && el.scrollHeight > line * 3.5 && !measuredLong) setMeasuredLong(true);
      }}>
        <Markdown text={keepLineBreaks(hub ? partnerWords(text) : text)} mid={mid} small />
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
