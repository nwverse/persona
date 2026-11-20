// Back (the browser's button, a phone's back gesture) closes what is open over the page before it leaves the page
// (#1996): the drawer, a full-screen side panel, the picture viewer, a bottom sheet. Opening one pushes an entry with
// the same URL; going back pops it and closes the overlay; closing it from the page takes its entry back off, with the
// entries of whatever is open over it, in one Back (a file open in the side panel goes with the panel, useBackStack).
import { useEffect, useRef } from "react";

const KEY = "personaOverlay";
const DEPTH = "personaOverlayDepth";
/** The address the overlay opened on: the entry under its own has it (see settleOverlayEntry). */
const AT = "personaOverlayAt";
let next = 0;
// Entries closed from the page come off a tick later, all in one Back: down to depth `to`, the lowest asked for. An
// overlay that opens again before then (React's StrictMode runs every effect twice in development; a sheet swapped for
// another) takes the lowest of those entries instead of racing an async Back that would pop the new one and close it at
// once. Once that Back is `going`, it stays here until it lands: closes it takes off meanwhile (the effects of the
// views over the overlay) do not go back again, and one under where it lands (`then`) goes on from there.
type Unwind = { to: number; at: string; going: boolean; then?: number };
let pending: Unwind | null = null;

/** For a navigation: whether the current entry is an open overlay's, which the new page should replace instead of
 *  stacking on (else Back from the new page lands on a dead copy of the old one). The navigation's replaceState takes
 *  the overlay's marker with it, so the overlay's close finds nothing of it to take off. */
export function takeOverlayEntry(): boolean {
  if (!onOverlayEntry(history.state)) return false;
  if (pending && !pending.going) pending = null;
  return true;
}

/** The state an overlay's entry carries (kept apart from whatever the page's state is). */
export function overlayState(base: unknown, id: string): Record<string, unknown> {
  return { ...(base && typeof base === "object" ? (base as Record<string, unknown>) : {}), [KEY]: id };
}

/** Whether `state` is an open overlay's entry (any overlay's). */
export function onOverlayEntry(state: unknown = typeof history !== "undefined" ? history.state : null): boolean {
  return Boolean(state && typeof state === "object" && typeof (state as Record<string, unknown>)[KEY] === "string");
}

/** How many overlay entries are stacked at `state`; entries written before the depth field count as one. */
export function overlayDepth(state: unknown = typeof history !== "undefined" ? history.state : null): number {
  if (!onOverlayEntry(state)) return 0;
  const n = (state as Record<string, unknown>)[DEPTH];
  return typeof n === "number" && Number.isSafeInteger(n) && n > 0 ? n : 1;
}

/** Whether `state` is the entry the overlay `id` pushed. */
export function isOverlayEntry(state: unknown, id: string): boolean {
  return Boolean(state && typeof state === "object" && (state as Record<string, unknown>)[KEY] === id);
}

/** Removes only overlay metadata, preserving the page's other history state. */
function clearOverlayMarkers(state: Record<string, unknown>) {
  const rest = { ...state };
  delete rest[KEY];
  delete rest[AT];
  delete rest[DEPTH];
  history.replaceState(Object.keys(rest).length ? rest : null, "", location.href);
}

/** Takes the entries over depth `to` off, a tick from now (see `pending`). */
function takeOff(to: number, at: string) {
  if (pending) {
    if (pending.to <= to) return; // the Back already takes this one off
    if (pending.going) pending.then = Math.min(pending.then ?? to, to);
    else pending.to = to;
    return;
  }
  const p: Unwind = { to, at, going: false };
  pending = p;
  setTimeout(() => {
    if (pending !== p) return; // reopened or taken by a navigation
    const n = overlayDepth(history.state) - p.to;
    if (n <= 0 || location.href !== p.at) {
      pending = null;
      return;
    }
    p.going = true;
    afterBack(() => {
      if (pending !== p) return;
      pending = null;
      if (p.then !== undefined) takeOff(p.then, p.at);
    });
    if (n === 1) history.back();
    else history.go(-n);
  }, 0);
}

/** Opens one overlay's history entry: Back calls `onClose`. Returns the close (from the page) that takes it off. */
export function openOverlay(onClose: () => void): () => void {
  const id = `o${++next}`;
  const top = overlayDepth(history.state);
  let depth: number;
  if (pending && !pending.going && pending.to < top) {
    // an entry closed this tick, not off yet: this one takes it (the lowest, as the overlays reopen in their order)
    depth = ++pending.to;
    if (depth === top) {
      pending = null;
      history.replaceState(overlayState(history.state, id), "", location.href);
    }
  } else {
    if (pending && !pending.going) pending = null; // nothing over its depth left to take off
    depth = top + 1;
    history.pushState({ ...overlayState(history.state, id), [AT]: location.href, [DEPTH]: depth }, "", location.href);
  }
  // where it opened: a replace that moves the page under it (an old link turned into its new one, a delete going
  // home) keeps the marker, so Back still closes it; closing it from the page then must not go back from the new page
  const at = location.href;
  // the entry on top is its own, or one of an overlay opened over it (stacked on it, on the same address)
  const under = (st: unknown) => isOverlayEntry(st, id) || (overlayDepth(st) >= depth && (st as Record<string, unknown>)[AT] === at);
  let popped = false;
  const onPop = () => {
    // the entry under ours came back: ours is gone, the overlay goes with it
    if (!under(history.state)) {
      popped = true;
      removeEventListener("popstate", onPop);
      onClose();
    }
  };
  addEventListener("popstate", onPop);
  return () => {
    removeEventListener("popstate", onPop);
    const st = history.state;
    if (popped || !under(st)) return; // popped, or a navigation went on top: leave it
    if (location.href !== at) {
      // the page moved under it: take its marker off this entry and stay on the new page
      if (isOverlayEntry(st, id)) clearOverlayMarkers(st);
      return;
    }
    takeOff(depth - 1, at);
  };
}

/** How long one Back may take to land (its popstate) before what waits for it goes on anyway. */
const BACK_MS = 500;

/** Runs `then` once the next Back lands (its popstate: true), or after `ms` without one (false). */
export function afterBack(then: (popped: boolean) => void, ms = BACK_MS): void {
  const timer = setTimeout(() => (removeEventListener("popstate", pop), then(false)), ms);
  const pop = () => (clearTimeout(timer), removeEventListener("popstate", pop), then(true));
  addEventListener("popstate", pop);
}

/** For the page's first load, before anything draws: an entry that still carries an overlay's marker is one the last
 *  load left behind (a reload with the side panel open, #5134). The overlay is gone, so its entry would only make the
 *  first Back do nothing. On the address it opened on, go back off it (more than once for overlays stacked on each
 *  other); on another (the page moved under it), take the marker off and stay. `ms`: how long one Back may take. */
export async function settleOverlayEntry(ms = BACK_MS): Promise<void> {
  const marked = () => {
    const st = history.state as Record<string, unknown> | null;
    return onOverlayEntry(st) ? st : null;
  };
  for (let i = 0; i < 5; i++) {
    const st = marked();
    if (!st) return;
    if (st[AT] !== location.href) break;
    const popped = await new Promise<boolean>((done) => {
      afterBack(done, ms);
      history.back();
    });
    if (!popped) break; // nothing under it (a new tab's only entry): keep the entry, without the marker
  }
  const st = marked();
  if (!st) return;
  clearOverlayMarkers(st);
}

/** While `open` (and `enabled`, e.g. only on a narrow screen), Back closes it through `onClose`. A navigation away while
 *  it is open leaves its entry under the new page; closing then does not go back (that would undo the navigation). */
export function useCloseOnBack(open: boolean, onClose: () => void, enabled = true): void {
  const close = useRef(onClose);
  close.current = onClose;
  useEffect(() => (open && enabled ? openOverlay(() => close.current()) : undefined), [open, enabled]);
}

/** Views opened inside an overlay, one over another (a file in the Files panel, a diff in Git): `depth` of them. While
 *  `enabled` (a narrow screen) each has an entry over the overlay's, so Back steps out of the top one (`popTo(i)` keeps
 *  the first i) before it closes the overlay; closing the overlay takes them off with its own, in one Back. */
export function useBackStack(depth: number, popTo: (n: number) => void, enabled = true): void {
  const pop = useRef(popTo);
  pop.current = popTo;
  const levels = useRef<(() => void)[]>([]);
  const want = enabled ? depth : 0;
  useEffect(() => {
    // once this commit's effects have run: the overlay the views are in (its effect runs after its children's) has
    // pushed its own entry by then, so theirs go over it even when it mounts with one open (a panel restored on a file)
    let current = true;
    queueMicrotask(() => {
      if (!current) return;
      const open = levels.current;
      for (const close of open.splice(want)) close();
      while (open.length < want) {
        const i = open.length;
        open.push(
          openOverlay(() => {
            open.length = Math.min(open.length, i);
            pop.current(i);
          }),
        );
      }
    });
    return () => {
      current = false;
    };
  }, [want]);
  useEffect(
    () => () => {
      for (const close of levels.current.splice(0)) close();
    },
    [],
  );
}
