// A smooth jump the chat drives itself, frame by frame (docs/web/web.md "Long chats"). The browser's own smooth scroll
// aims once, at where the row was when it set off: exchanges drawn for the first time on the way (content-visibility)
// then move the row, and setting scrollTop to keep the view still stops it. This one asks `aim()` each frame. Its pace
// is Chromium's own (measured): √distance × 15 ms, at most 1.5 s, easing out.

export function glideMs(distance: number): number {
  return Math.min(1500, 15 * Math.sqrt(Math.abs(distance)));
}

/** Moves `el` to `aim()`, read again each frame; `done` once there. Returns a stop. */
export function glide(el: HTMLElement, aim: () => number, done: () => void): () => void {
  const d0 = aim() - el.scrollTop;
  const ms = glideMs(d0);
  const t0 = performance.now();
  let id = 0;
  const step = () => {
    const p = ms > 0 ? Math.min(1, (performance.now() - t0) / ms) : 1;
    el.scrollTop = aim() - d0 * (1 - p) ** 3;
    if (p < 1) id = requestAnimationFrame(step);
    else done();
  };
  id = requestAnimationFrame(step);
  return () => cancelAnimationFrame(id);
}
