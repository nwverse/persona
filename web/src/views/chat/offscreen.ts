// Finished exchanges off screen are not drawn (content-visibility: auto, `.seg-cv` in styles.css; docs/web/web.md
// "Long chats"): each takes a guessed height (SegmentView segmentHeight) until it is first drawn, then its own. The
// chat sets `overflow-anchor: none` (#1124), so the browser does not keep the view still when one above it changes:
// ChatView's ResizeObserver does, before paint, from what these work out.

export interface SizeChange {
  /** Its top on screen now (px). */
  top: number;
  was: number;
  now: number;
}

/** The exchanges among `entries` whose height changed since the last time (`heights`, kept up to date here). The
 *  first size of one just watched only counts from then on. */
export function segmentChanges(entries: readonly ResizeObserverEntry[], heights: WeakMap<Element, number>): SizeChange[] {
  const out: SizeChange[] = [];
  for (const e of entries) {
    const el = e.target;
    if (!el.hasAttribute("data-segment")) continue;
    const now = e.borderBoxSize?.[0]?.blockSize ?? e.contentRect.height;
    const was = heights.get(el);
    heights.set(el, now);
    if (was !== undefined && el.isConnected && Math.abs(now - was) >= 0.5) out.push({ top: el.getBoundingClientRect().top, was, now });
  }
  return out;
}

/** How far what is on screen moved down (px) when exchanges that start above the view's top (`viewTop`) changed
 *  height: their ends moved, and what follows them. One first drawn as the view's top reaches it keeps its end where it
 *  was, so the rows under it stay put; one that starts on screen moves only what is under it, as before. Any order. */
export function shiftAbove(changes: readonly SizeChange[], viewTop: number): number {
  let shift = 0;
  for (const c of [...changes].sort((a, b) => a.top - b.top)) {
    // its top before: the ones above it that changed moved it by `shift`
    if (c.top - shift >= viewTop - 0.5) break;
    shift += c.now - c.was;
  }
  return shift;
}
