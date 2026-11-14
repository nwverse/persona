// Settings › Hub › Unread › Manual mark as read (#7356): the switch is the hub's (GET/PUT /read-settings), so every browser
// and app agrees; off unless turned on. lib/manual-read.ts holds the value the store reads; this keeps it in step with
// the hub. It is not part of the first screen: the store loads it on each hello and asks it to read the switch again.
// A hub from before the switch answers 404: it is off there and cannot be turned on.
import { useSyncExternalStore } from "react";
import { api, ApiError } from "./api";
import { defaultKnown, fromDefaultManager, onDefaultManager } from "./manager-choice";
import { manualRead, onManualReadChange, setManualReadLocal } from "./manual-read";
import { onHubEvent } from "./store";

export interface ReadSetting {
  manual: boolean;
  /** The hub answered: before that the switch shows what this page last knew, and cannot be flipped. */
  known: boolean;
  canWrite: boolean;
  /** False on a hub from before the switch (404): off there, and nothing to turn on. */
  supported: boolean;
}

let state: ReadSetting = { manual: manualRead(), known: false, canWrite: false, supported: true };
let loading: Promise<void> | null = null;
let again = false;
// Every read, write and push, numbered: an answer applies only while nothing newer came after its request.
let rev = 0;
const listeners = new Set<() => void>();

function set(next: ReadSetting) {
  if (next.manual === state.manual && next.known === state.known && next.canWrite === state.canWrite && next.supported === state.supported) return;
  state = next;
  setManualReadLocal(next.manual);
  listeners.forEach((l) => l());
}

export function getReadSetting(): ReadSetting {
  return state;
}

/** Reads the switch again (on every hello). Asked for while a read is out, it reads once more after that one. */
export function refreshReadSetting(): Promise<void> {
  if (loading) {
    again = true;
    return loading;
  }
  loading = (async () => {
    try {
      do {
        again = false;
        await readSwitch();
      } while (again);
    } finally {
      loading = null;
    }
  })();
  return loading;
}

async function readSwitch(): Promise<void> {
  const mine = ++rev;
  try {
    const r = await api<{ manual?: boolean; can_write?: boolean }>("/read-settings");
    if (mine === rev) set({ manual: r.manual === true, known: true, canWrite: r.can_write === true, supported: true });
  } catch (e) {
    if (e instanceof ApiError && e.status === 404 && mine === rev) set({ manual: false, known: true, canWrite: false, supported: false });
    // anything else: keep what we had, the next hello asks again
  }
}

/** Turns manual mark as read on or off for this hub; shown at once, put back (and thrown) when the hub refuses. */
export async function setManualRead(on: boolean): Promise<void> {
  const before = state;
  const mine = ++rev;
  set({ ...state, manual: on });
  try {
    const r = await api<{ manual?: boolean; can_write?: boolean }>("/read-settings", { method: "PUT", json: { manual: on } });
    if (mine === rev) set({ manual: r.manual === true, known: true, canWrite: r.can_write !== false, supported: true });
  } catch (e) {
    if (mine === rev) set(before);
    throw e;
  }
}

// another browser or device changed it; the switch is the default manager's, as the workflow board's
onHubEvent("read_settings", (e) => {
  if (typeof e.manual !== "boolean" || !defaultKnown() || !fromDefaultManager(e)) return;
  rev++;
  set({ ...state, manual: e.manual });
});
onDefaultManager(() => void refreshReadSetting());
// the store's own copy can change only through set; this keeps a test's direct change in view
onManualReadChange((on) => {
  if (on !== state.manual) {
    state = { ...state, manual: on };
    listeners.forEach((l) => l());
  }
});

const subscribe = (l: () => void) => {
  listeners.add(l);
  if (!state.known) void refreshReadSetting();
  return () => {
    listeners.delete(l);
  };
};

export function useReadSetting(): ReadSetting {
  return useSyncExternalStore(subscribe, getReadSetting, getReadSetting);
}

/** Only for tests: back to "not answered yet", off. */
export function resetReadSettingForTest() {
  setManualReadLocal(false);
  state = { manual: false, known: false, canWrite: false, supported: true };
  loading = null;
  again = false;
  rev = 0;
}
