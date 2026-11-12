// Picking messages to forward at once (#3838; iOS MessagePick.swift): Forward to's Select messages… starts it with the
// message it was opened for picked. Each message with a Forward is then a checkbox (views/chat/MessageActions.tsx), and a
// bar in the composer's place (views/chat/PickBar.tsx) says how many are picked and forwards them; Cancel leaves the chat
// as it was. One per chat (ChatView), read with useSyncExternalStore where it shows: a pick draws that message and the
// bar again, not the chat. No DOM here.
import { FORWARD_MOST, type ForwardPick } from "./forward";

export interface MessagePick {
  subscribe: (listener: () => void) => () => void;
  /** Picking now. */
  on: () => boolean;
  /** The message `id` (ForwardPick.itemId) is picked. */
  has: (id: string) => boolean;
  count: () => number;
  /** The picked messages, in the order they were picked (inChatOrder puts them in the chat's). */
  picks: () => ForwardPick[];
  /** Starts picking, with `first` picked. */
  start: (first: ForwardPick) => void;
  /** Picks `p`, or unpicks it; false when that would pick more than FORWARD_MOST: nothing changes but `refusals`. */
  toggle: (p: ForwardPick) => boolean;
  /** How many picks were refused so far (the bar says so as it changes). */
  refusals: () => number;
  /** Stops picking; nothing stays picked. */
  end: () => void;
}

export function messagePick(): MessagePick {
  let on = false;
  let refused = 0;
  let picked = new Map<string, ForwardPick>();
  const listeners = new Set<() => void>();
  const changed = () => listeners.forEach((l) => l());
  return {
    subscribe: (l) => {
      listeners.add(l);
      return () => void listeners.delete(l);
    },
    on: () => on,
    has: (id) => picked.has(id),
    count: () => picked.size,
    picks: () => [...picked.values()],
    start: (first) => {
      on = true;
      picked = new Map([[first.itemId, first]]);
      changed();
    },
    toggle: (p) => {
      if (!on) return false;
      const next = new Map(picked);
      if (next.has(p.itemId)) next.delete(p.itemId);
      else if (next.size >= FORWARD_MOST) {
        refused++;
        changed();
        return false;
      } else next.set(p.itemId, p);
      picked = next;
      changed();
      return true;
    },
    refusals: () => refused,
    end: () => {
      if (!on) return;
      on = false;
      picked = new Map();
      changed();
    },
  };
}
