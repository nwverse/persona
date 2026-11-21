// Phone overlays have one same-address history entry per visible level (#1996, #11643).
import { useEffect, useRef } from "react";

const KEY = "personaOverlay";
const DEPTH = "personaOverlayDepth";
const AT = "personaOverlayAt";
const BACK_MS = 500;

type State = Record<string, unknown>;
type Close = (navigating: boolean) => void;
type Slot = { id: string; close?: Close };
let next = 0;
let slots: Slot[] = [];
let timer: ReturnType<typeof setTimeout> | undefined;
let moving = false;
let navigation: (() => void) | undefined;
let replaced: { url: string; origin: string; state: State | null } | undefined;

/** State belonging to the page survives overlay pushes and closes. */
export function overlayState(base: unknown, id: string): State {
  return { ...(base && typeof base === "object" ? base : {}), [KEY]: id };
}
export function onOverlayEntry(state: unknown = typeof history === "undefined" ? null : history.state): boolean {
  return typeof (state as State | null)?.[KEY] === "string";
}
export function isOverlayEntry(state: unknown, id: string): boolean {
  return (state as State | null)?.[KEY] === id;
}
export function overlayDepth(state: unknown = typeof history === "undefined" ? null : history.state): number {
  if (!onOverlayEntry(state)) return 0;
  const n = (state as State)[DEPTH];
  return typeof n === "number" && Number.isSafeInteger(n) && n > 0 ? n : 1;
}
function pageState(state: State | null): State | null {
  const rest = { ...state };
  delete rest[KEY];
  delete rest[DEPTH];
  delete rest[AT];
  return Object.keys(rest).length ? rest : null;
}

function go(n: number) {
  if (n === 1) history.back();
  else history.go(-n);
}

function sweep() {
  clearTimeout(timer);
  timer = undefined;
  if (moving || !slots.length || !isOverlayEntry(history.state, slots.at(-1)!.id)) return;
  let n = 0;
  while (n < slots.length && !slots[slots.length - 1 - n].close) n++;
  if (n) {
    moving = true;
    go(n);
  }
}

function forget() {
  removeEventListener("popstate", landed, true);
  clearTimeout(timer);
  slots = [];
  timer = undefined;
  moving = false;
  replaced = undefined;
  navigation = undefined;
}

function landed(event?: Event) {
  const depth = overlayDepth();
  // An Inbox return can traverse both overlays and pages at once. A user Back only removes levels above its target.
  const removed = slots.splice(depth && slots[depth - 1]?.id === history.state[KEY] ? depth : 0);
  const target = navigation;
  const redirect = replaced;
  navigation = undefined;
  moving = false;
  if (redirect && (slots.length || location.href === redirect.origin)) {
    const state = history.state;
    history.replaceState(depth ? { ...redirect.state, [KEY]: state[KEY], [AT]: state[AT], [DEPTH]: depth } : redirect.state, "", redirect.url);
  }
  if (!slots.length) forget();
  if (target) event?.stopImmediatePropagation();
  for (const slot of removed.reverse()) slot.close?.(Boolean(target));
  if (target) target();
  else sweep(); // Dismissed lower levels become removable when there is no live overlay above them.
}

/** Opening again during the same commit reuses the first vacated position in the top suffix (including StrictMode). */
export function openOverlay(onClose: Close): () => void {
  if (moving) {
    let cancelled = false;
    let close: (() => void) | undefined;
    afterBack(() => { if (!cancelled) close = openOverlay(onClose); });
    return () => { cancelled = true; close?.(); };
  }
  if (slots.length && !isOverlayEntry(history.state, slots.at(-1)!.id)) forget();
  let i = slots.length;
  while (i && !slots[i - 1].close) i--;
  const slot = slots[i] ?? { id: `o${++next}` };
  if (i === slots.length) {
    if (!i) addEventListener("popstate", landed, true);
    slots.push(slot);
    const current = history.state;
    history.pushState({ ...overlayState(current, slot.id), [DEPTH]: overlayDepth(current) + 1, [AT]: current?.[AT] ?? location.href }, "", location.href);
  }
  slot.close = onClose;
  return () => {
    if (slot.close !== onClose || !slots.includes(slot)) return;
    slot.close = undefined;
    // Sibling cleanups and remounts in the same React commit settle before choosing how far to go.
    timer ??= setTimeout(sweep, 0);
  };
}

/** A single entry can be replaced immediately. From deeper stacks return to the real origin and push the destination:
 * this also drops the old overlay entries from Forward. A replace under open layers follows them back to the base. */
export function navigateOverlay(to: string, state: State | null, replace: boolean, notify: () => void): boolean {
  const depth = overlayDepth();
  if (!depth) return false;
  // Forward can revisit a stored stack whose UI was already closed. Its return still needs a listener.
  addEventListener("popstate", landed, true);
  if (replace) {
    const st = history.state;
    replaced = { url: to, origin: st[AT] ?? location.href, state: pageState(state ?? st) };
    history.replaceState({ ...replaced.state, [KEY]: st[KEY], [DEPTH]: depth, [AT]: st[AT] }, "", to);
    notify();
  } else if (depth === 1 && !moving && !replaced) {
    const removed = slots;
    forget();
    history.replaceState(state, "", to);
    for (const slot of removed.reverse()) slot.close?.(true);
    notify();
  } else {
    // A close can already be traversing history. Finish on its landing and consume whatever layers are left there.
    navigation = () => {
      if (!navigateOverlay(to, state, false, notify)) {
        history.pushState(state, "", to);
        notify();
      }
    };
    if (!moving) {
      moving = true;
      go(depth);
    }
  }
  return true;
}

/** Return to an earlier page even if an outside press is already taking a menu off the item. */
export function backPastOverlays(pages: number) {
  clearTimeout(timer);
  timer = undefined;
  if (moving) {
    navigation = () => backPastOverlays(pages);
    return;
  }
  moving = slots.length > 0;
  go(pages + overlayDepth());
}

/** Runs once after this traversal, or on timeout if the browser has no entry to return to. */
export function afterBack(then: (popped: boolean) => void, ms = BACK_MS): void {
  const timer = setTimeout(() => (removeEventListener("popstate", pop), then(false)), ms);
  const pop = () => (clearTimeout(timer), removeEventListener("popstate", pop), then(true));
  addEventListener("popstate", pop);
}

/** A reload restores the entries but no overlays. Depth allows a single traversal; legacy markers may need another.
 * A replace under the stack belongs at the base, including its page state. A restored tab can have no reachable base. */
export async function settleOverlayEntry(ms = BACK_MS): Promise<void> {
  const first = history.state;
  if (!onOverlayEntry(first)) return;
  const url = location.href;
  const moved = first[AT] !== undefined && first[AT] !== url;
  while (onOverlayEntry()) {
    const id = history.state[KEY];
    const popped = await new Promise<boolean>((done) => {
      afterBack(done, ms);
      go(overlayDepth());
    });
    if (!popped || isOverlayEntry(history.state, id)) break;
  }
  if (moved || onOverlayEntry()) history.replaceState(pageState(moved ? first : history.state), "", moved ? url : location.href);
}

export function useCloseOnBack(open: boolean, onClose: Close, enabled = true): void {
  const close = useRef(onClose);
  close.current = onClose;
  useEffect(() => (open && enabled ? openOverlay((navigating) => close.current(navigating)) : undefined), [open, enabled]);
}
