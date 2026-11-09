// The page's side of the terminal's focus policy: reads the facts lib/terminal-details.ts `shouldRefocus` decides on.
import { EDITABLE } from "../../../lib/shortcuts";
import type { FocusFacts } from "../../../lib/terminal-details";

const OVERLAYS = '[role="dialog"], [role="alertdialog"], [role="menu"], [aria-modal="true"]';

/** Text selected on the page inside `root` (the rows the page draws, the panel's own text). */
export function selectionInside(root: HTMLElement): boolean {
  const sel = window.getSelection();
  return Boolean(sel && !sel.isCollapsed && sel.anchorNode && root.contains(sel.anchorNode));
}

/** What is true of the page right now for a terminal panel in `root`. `selected`: xterm has a selection. */
export function focusFacts(root: HTMLElement, driving: boolean, selected: boolean): FocusFacts {
  const active = document.activeElement as HTMLElement | null;
  const own = Boolean(active?.classList.contains("xterm-helper-textarea"));
  return {
    driving,
    // a kept terminal stays mounted under the shown one, `inert` and invisible (views/terminal/TerminalPage.tsx)
    visible:
      document.visibilityState === "visible" &&
      !root.closest("[inert]") &&
      root.getClientRects().length > 0 &&
      getComputedStyle(root).visibility !== "hidden",
    selection: selected || selectionInside(root),
    overlayOpen: Boolean(document.querySelector(OVERLAYS)),
    otherInput: Boolean(active && !own && (active.matches(EDITABLE) || active.closest('[role="search"]'))), // the find bar counts whole
    outside: Boolean(active && active !== document.body && !root.contains(active)),
  };
}
