// Switching an ssh computer to Persona Link (#10662): what the machine list says about it (`link_switch`) and the one
// start a page may have in flight for a computer. There is no read route: where a switch stands always comes from the
// machine list (pushed with machine updates; the admin page reads its own). A start posts once and is never retried:
// on an answer it cannot trust, or a refusal, the caller reads that list again and lets it decide.
// Only linkSwitchRunning is read by the first screen (machine-deploy.ts, where it lives); the rest loads with the pages.
import { useSyncExternalStore } from "react";
import type { MachineLinkSwitch } from "./types";

export { linkSwitchRunning } from "./machine-deploy";

/** The steps a switch goes through, in order; `rolling_back` is the way back, not a step. */
export const LINK_SWITCH_STEPS = ["checking", "installing", "connecting"] as const;

/** What the last start here got back when it did not take the hub's answer as the state: `refused`, a 409 (or another
 *  refusal) with its code; `forbidden`, a 403 (a device without full access); `uncertain`, no answer it can trust. */
export type LinkSwitchNotice = { kind: "refused"; code: string } | { kind: "forbidden" } | { kind: "uncertain" };

export interface LinkSwitchAttempt {
  /** pressed, and the hub's answer (and on an unclear one, the list read after it) not back yet */
  starting: boolean;
  /** the 202's state, shown until the machine list is as new */
  accepted?: MachineLinkSwitch;
  notice?: LinkSwitchNotice;
  /** the list's `link_switch` when it was pressed (`linkSwitchKey`) */
  seen: string;
}

// One per computer, for every mount (the detail and a remounted one alike): a second press anywhere waits for this one.
const attempts = new Map<string, LinkSwitchAttempt>();
const listeners = new Set<() => void>();

function put(machineId: string, attempt: LinkSwitchAttempt) {
  attempts.set(machineId, attempt);
  listeners.forEach((l) => l());
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => void listeners.delete(listener);
}

export function useLinkSwitchAttempt(machineId: string): LinkSwitchAttempt | undefined {
  return useSyncExternalStore(subscribe, () => attempts.get(machineId));
}

/** Tests only: forget every start. */
export function resetLinkSwitchAttempts() {
  attempts.clear();
  listeners.forEach((l) => l());
}

/** The fields that say where a switch stands, as one string: whether the list has moved since a start was pressed. */
export function linkSwitchKey(s?: MachineLinkSwitch | null): string {
  return s ? [s.available, s.reason ?? "", s.phase ?? "", s.error ?? "", !!s.rollback_unconfirmed, s.at ?? ""].join("|") : "";
}

const time = (at?: string) => {
  const n = at ? Date.parse(at) : NaN;
  return Number.isFinite(n) ? n : undefined;
};

/** What to show: the list's state, unless a start made here was accepted and the list is not as new yet (both times are
 *  the hub's; without them, until the list moves at all). */
export function shownLinkSwitch(listed: MachineLinkSwitch | undefined, attempt: LinkSwitchAttempt | undefined): MachineLinkSwitch | undefined {
  const accepted = attempt?.accepted;
  if (!accepted || !listed) return listed;
  const a = time(accepted.at);
  const l = time(listed.at);
  const caughtUp = a !== undefined && l !== undefined ? l >= a : linkSwitchKey(listed) !== attempt.seen;
  return caughtUp ? listed : accepted;
}

/** The last start's notice, while the list has not moved since it was pressed (once it has, the list says more). */
export function linkSwitchNotice(listed: MachineLinkSwitch | undefined, attempt: LinkSwitchAttempt | undefined): LinkSwitchNotice | undefined {
  if (!attempt || attempt.starting || !attempt.notice) return undefined;
  return linkSwitchKey(listed) === attempt.seen ? attempt.notice : undefined;
}

const statusOf = (e: unknown) => (typeof e === "object" && e !== null && typeof (e as { status?: unknown }).status === "number" ? (e as { status: number }).status : 0);
const codeOf = (e: unknown) => {
  const code = typeof e === "object" && e !== null ? (e as { code?: unknown }).code : undefined;
  return typeof code === "string" ? code : "";
};

/**
 * Starts a switch of `machineId`, once: `post` is the page's own request (the app's or the admin page's), `refresh`
 * reads its machine list again. A second call while one is in flight, from any mount, does nothing. A 202 shows its
 * state at once; a 403 says so; anything else (a 409, another refusal, a lost or unreadable answer) may mean a switch
 * runs, so it is never posted again: the list is read and its `link_switch` decides.
 */
export async function startLinkSwitch(
  machineId: string,
  listed: MachineLinkSwitch | undefined,
  post: () => Promise<{ link_switch?: MachineLinkSwitch } | undefined>,
  refresh: () => unknown,
): Promise<void> {
  if (attempts.get(machineId)?.starting) return;
  const seen = linkSwitchKey(listed);
  put(machineId, { starting: true, seen });
  let notice: LinkSwitchNotice;
  try {
    const answer = await post();
    const state = answer?.link_switch;
    if (state && typeof state === "object" && typeof state.available === "boolean") {
      put(machineId, { starting: false, accepted: state, seen });
      return;
    }
    notice = { kind: "uncertain" };
  } catch (e) {
    const status = statusOf(e);
    if (status === 403) {
      put(machineId, { starting: false, notice: { kind: "forbidden" }, seen });
      return;
    }
    notice = status === 409 || (status >= 400 && status < 500 && status !== 408 && status !== 429) ? { kind: "refused", code: codeOf(e) } : { kind: "uncertain" };
  }
  try {
    await refresh();
  } catch {
    // the list is pushed again with the next machine update
  }
  put(machineId, { starting: false, notice, seen });
}
