// The bar in the composer's place while a chat picks messages to forward at once (#3838; iOS PickBar, and Inbox's Select
// bar, views/inbox/InboxList.tsx): Cancel, how many are picked, and Forward N, off while none is. Escape cancels too.
// Cancel leaves the chat as it was: the composer comes back with its draft. A pick past FORWARD_MOST is refused with a
// toast from here. Its own chunk: only picking draws it.
import { useEffect, useRef, useSyncExternalStore } from "react";
import { t } from "../../i18n";
import { FORWARD_MOST, type ForwardPick } from "../../lib/forward";
import type { MessagePick } from "../../lib/message-pick";
import { Button, toast } from "../../ui/atoms";

/** `picks` in the chat's order (several.json "three_then_a_note": the blocks go as the chat has them, not as they were
 *  picked): by where each message is in `items`, the transcript; one it no longer holds goes after, as it was picked. */
export function inChatOrder(picks: readonly ForwardPick[], items: readonly { id: string }[]): ForwardPick[] {
  const at = new Map<string, number>();
  items.forEach((it, i) => {
    if (!at.has(it.id)) at.set(it.id, i);
  });
  const place = (p: ForwardPick) => at.get(p.itemId) ?? Number.MAX_SAFE_INTEGER;
  return picks
    .map((p, i) => ({ p, i }))
    .sort((a, b) => place(a.p) - place(b.p) || a.i - b.i)
    .map((x) => x.p);
}

/** `items`: the chat's transcript as it is when Forward is pressed; `onForward` gets the picked messages in its order. */
export function PickBar({ pick, items, onForward }: { pick: MessagePick; items: () => readonly { id: string }[]; onForward: (picks: ForwardPick[]) => void }) {
  const count = useSyncExternalStore(pick.subscribe, pick.count, pick.count);
  const refusals = useSyncExternalStore(pick.subscribe, pick.refusals, pick.refusals);
  const told = useRef(refusals);
  useEffect(() => {
    if (refusals === told.current) return;
    told.current = refusals;
    toast(t("chat-forwardto.at_most", { count: FORWARD_MOST }), "error");
  }, [refusals]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      // a dialog or menu over the chat takes its own Escape
      if (e.key !== "Escape" || e.defaultPrevented || document.querySelector("[role=dialog], [role=menu]")) return;
      pick.end();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [pick]);
  return (
    <div role="toolbar" aria-label={t("chat-forwardto.selection")} data-pick-bar className="flex shrink-0 items-center gap-2 rounded-[14px] bg-surface p-2 shadow-card">
      <Button tone="quiet" onClick={pick.end} testId="pick-cancel">
        {t("common.cancel")}
      </Button>
      <span className="min-w-0 flex-1 truncate text-center text-[12.5px] text-ink-3 tabular-nums" aria-live="polite" data-pick-count>
        {t("chat-forwardto.selected", { count })}
      </span>
      <Button tone="primary" icon="forward" disabled={!count} onClick={() => onForward(inChatOrder(pick.picks(), items()))} testId="pick-forward">
        {t("chat-forwardto.forward_count", { count })}
      </Button>
    </div>
  );
}
