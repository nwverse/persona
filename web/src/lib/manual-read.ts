// Manual mark as read (#7356): the hub's switch as this page last knew it, small and free of the store so the store and
// the first screen can read it (lib/read-setting.ts, loaded after the hub's hello, keeps it in step with the hub). Off,
// opening a conversation or an Agent marks it seen as always; on, only the user's Mark as read does. The last answer is
// kept in localStorage, so a reload does not mark the chat it reopens before the hub has answered again.
import { useSyncExternalStore } from "react";

const KEY = "persona.manualRead";

function stored(): boolean {
  try {
    return typeof localStorage !== "undefined" && localStorage.getItem(KEY) === "1";
  } catch {
    return false;
  }
}

let manual = stored();
const listeners = new Set<(on: boolean) => void>();

export function manualRead(): boolean {
  return manual;
}

export function setManualReadLocal(on: boolean) {
  if (on === manual) return;
  manual = on;
  try {
    if (on) localStorage.setItem(KEY, "1");
    else localStorage.removeItem(KEY);
  } catch {
    // private mode: this page still knows it
  }
  listeners.forEach((l) => l(on));
}

/** Told each time the switch changes, with its new value. */
export function onManualReadChange(l: (on: boolean) => void): () => void {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}

const subscribe = (l: () => void) => onManualReadChange(l);

/** The switch, for a view that shows Mark as read only while it is on. */
export function useManualRead(): boolean {
  return useSyncExternalStore(subscribe, manualRead, manualRead);
}
