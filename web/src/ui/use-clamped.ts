// Whether a line-clamped text is cut, for a "Show all" under it (#4715, #4742). Measured once it is laid out, when its
// words change, whenever the element's box changes (a side panel opens, the window narrows, a hidden container shows),
// and once the web fonts are in, as LarkTopicCard's Fold does. While `open` the last answer stays, so "Hide" stays under
// the text. Not after every render: reading the element's size makes the browser lay the page out first, and a list
// of many (the Inbox's draft cards) paid that on every update (audit item 34).
import { useCallback, useLayoutEffect, useState } from "react";

/** `axis` "x" for a one-line `truncate` (cut at its width), "y" (the default) for a `line-clamp` (cut at its height). */
export function useClamped<T extends HTMLElement>(open: boolean, axis: "x" | "y" = "y"): { ref: (el: T | null) => void; cut: boolean } {
  const [el, setEl] = useState<T | null>(null);
  const [cut, setCut] = useState(false);
  const measure = useCallback(() => {
    if (el && !open) setCut(axis === "x" ? el.scrollWidth > el.clientWidth + 1 : el.scrollHeight > el.clientHeight + 1);
  }, [el, open, axis]);
  useLayoutEffect(measure, [measure]);
  useLayoutEffect(() => {
    if (!el) return;
    let live = true;
    void document.fonts?.ready.then(() => live && measure());
    const ro = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(() => measure());
    ro?.observe(el);
    // new words in the same box (a clamped text that grows keeps its height)
    const mo = typeof MutationObserver === "undefined" ? undefined : new MutationObserver(() => measure());
    mo?.observe(el, { childList: true, subtree: true, characterData: true });
    return () => {
      live = false;
      ro?.disconnect();
      mo?.disconnect();
    };
  }, [el, measure]);
  return { ref: setEl, cut };
}
