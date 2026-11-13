// Items pushed over the WebSocket after a chat subscribed (not the first page, older pages or an unfolded range): their
// words fade in once (Markdown's `fade`). A store outside React, read per message: each new reply renders its own
// words, not every exchange of the chat (a context handed every reader a new set with each reply).
import { useCallback, useSyncExternalStore } from "react";

type Listener = () => void;

const fresh = new Map<string, Set<string>>(); // chat -> message ids
const listeners = new Map<string, Set<Listener>>(); // chat + message id -> its readers

const chatKey = (mid: string, sid: string) => `${mid}/${sid}`;
const idKey = (chat: string, id: string) => `${chat}\n${id}`;

function notify(chat: string, ids: Iterable<string>) {
  for (const id of ids) listeners.get(idKey(chat, id))?.forEach((l) => l());
}

/** Words that just came in to this chat (lib/turns.ts freshWords). */
export function addFreshWords(mid: string, sid: string, ids: string[]) {
  if (!ids.length) return;
  const chat = chatKey(mid, sid);
  const have = fresh.get(chat) ?? new Set<string>();
  fresh.set(chat, have);
  const added = ids.filter((id) => !have.has(id));
  for (const id of added) have.add(id);
  notify(chat, added);
}

/** The chat was opened again or closed: what came in before does not fade any more. */
export function resetFreshWords(mid: string, sid: string) {
  const chat = chatKey(mid, sid);
  const had = fresh.get(chat);
  if (!had) return;
  fresh.delete(chat);
  notify(chat, had);
}

export function isFreshWords(mid: string, sid: string, id: string) {
  return fresh.get(chatKey(mid, sid))?.has(id) ?? false;
}

/** useSyncExternalStore's subscribe for one message: called only when that message's answer changes. */
export function onFreshWords(mid: string, sid: string, id: string, l: Listener) {
  const k = idKey(chatKey(mid, sid), id);
  const ls = listeners.get(k) ?? new Set<Listener>();
  listeners.set(k, ls);
  ls.add(l);
  return () => {
    ls.delete(l);
    if (!ls.size && listeners.get(k) === ls) listeners.delete(k);
  };
}

const unwatched = () => {};

/** Whether this message's words just came in; renders its reader only when that changes. No id: never fresh. */
export function useFreshWords(mid: string, sid: string, id: string | undefined): boolean {
  const subscribe = useCallback((l: Listener) => (id ? onFreshWords(mid, sid, id, l) : unwatched), [mid, sid, id]);
  const get = () => (id ? isFreshWords(mid, sid, id) : false);
  return useSyncExternalStore(subscribe, get, get);
}
