// The shared composer grows to six lines, then scrolls. Phones also offer the existing full-screen editor.
// Very short visible viewports leave room for the toolbar instead of forcing the entire six-line box on screen.

/** Where the compact composer applies: a phone-width screen, or a touch screen narrower than the sidebar breakpoint
 *  (a phone on its side). Desktop layouts keep the box they have. */
export const COMPACT_COMPOSER = "(max-width: 639px), (hover: none) and (max-width: 1023px)";

/** Reuse the dock's input share so a keyboard does not prematurely cap the draft at three lines. */
const GROW_SHARE = 0.35;
const MIN_LINES = 1;
const MAX_LINES = 6;

/** Six lines when they fit; fewer only when the visible viewport cannot accommodate the box and toolbar. */
export function growLines(visible: number, lineHeight: number): number {
  if (!(visible > 0) || !(lineHeight > 0)) return MAX_LINES;
  return Math.min(MAX_LINES, Math.max(MIN_LINES, Math.floor((visible * GROW_SHARE) / lineHeight)));
}

/** The draft's wrapped lines, from the text's height (the box's scrollHeight less its vertical padding). */
export function lineCount(contentHeight: number, lineHeight: number): number {
  if (!(lineHeight > 0)) return 1;
  return Math.max(1, Math.round(contentHeight / lineHeight));
}

/** Expand shows once the draft has more lines than the box shows. With the button the text wraps narrower (more
 *  lines), so whichever state it lands in holds: it never flips back by itself. */
export const offersExpand = (lines: number, limit: number) => lines > limit;
