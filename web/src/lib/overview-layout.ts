// Which Overview this browser shows (#13019, docs/web/web.md "Overview by project"): by default the project view
// (watch cards, By project, Temporary and Unsorted, the question summary); with Settings › General › Overview's
// "Classic layout" on, the sections in two and three columns as before (Needs you, tasks, Working, Unread, computers,
// views/OverviewBody.tsx). Off unless turned on; kept per browser, as the other display switches of General.
import { useSyncExternalStore } from "react";

const KEY = "persona.overviewClassic";
const listeners = new Set<() => void>();

/** Whether this browser shows the classic Overview: off unless turned on. */
export function classicOverview(): boolean {
  try {
    return localStorage.getItem(KEY) === "1";
  } catch {
    return false;
  }
}

export function setClassicOverview(on: boolean): void {
  try {
    if (on) localStorage.setItem(KEY, "1");
    else localStorage.removeItem(KEY);
  } catch {
    // storage unavailable: nothing is kept, the default (off) stays
  }
  listeners.forEach((l) => l());
}

// this tab's changes only (a change in another tab shows at its next load): fewer bytes on the first screen
const subscribe = (l: () => void) => (listeners.add(l), () => void listeners.delete(l));

export const useClassicOverview = (): boolean => useSyncExternalStore(subscribe, classicOverview, classicOverview);
