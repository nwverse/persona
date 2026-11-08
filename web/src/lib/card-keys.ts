// A waiting card's own keyboard (docs/web/web.md "Keyboard shortcuts"): while a card has keyboard focus, digits 1–9
// pick its options, a bare Enter runs its main action and Esc goes back to the composer — never an answer. This file
// is the part with no DOM of its own: whose key it is, and where focus goes. The cards hang these on their root
// (useCardKeys in views/chat/Interactions.tsx); the shortcut that hands the card the keyboard is "focus-card"
// (lib/shortcuts.ts, lib/use-shortcuts.ts).
import { composing } from "./send-history";

/** What a key press on a card is: Esc back to the composer, a digit picking an option, Enter running the main action;
 *  null when the key is someone else's. */
export type CardKeyAction = { kind: "esc" } | { kind: "digit"; digit: number } | { kind: "enter" };

/** Elements that type: their keys are the text's, never the card's. */
const EDITABLE = "input, textarea, select, [contenteditable=''], [contenteditable='true']";

/** Controls with their own keyboard semantics: an Enter on one of them is that control's (a focused Deny button's
 *  Enter denies), not the card's main action. */
const INTERACTIVE = "button, a[href], summary, [role='button'], [role='link'], [role^='menuitem'], [role='option'], [role='tab'], [role='radio'], [role='checkbox'], [role='switch']";

export interface CardKeyEvent {
  key: string;
  isComposing?: boolean;
  keyCode?: number;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
  target: EventTarget | null;
}

/**
 * What a keydown inside a waiting card is for. `root` is the card's own element (the card's handler only sees its
 * subtree, so this is a guard, not the boundary). Yields — null — to: an input method composing (the Enter or digit
 * that picks a candidate is not ours), any chord (the shortcuts' Mod keys, and a browser or system key), and typing
 * in an editable element anywhere in the card. Enter on a control that activates itself (a button, an option row) is
 * left to it: that is the focused action, not the main one. Esc is the card's wherever in it focus sits, with no
 * modifier held.
 */
export function cardKeyAction(e: CardKeyEvent, root: Element | null): CardKeyAction | null {
  if (composing(e)) return null;
  if (e.ctrlKey || e.metaKey || e.altKey) return null;
  const el = e.target instanceof Element ? e.target : null;
  if (!el || !root?.contains(el)) return null;
  if (e.key === "Escape") return { kind: "esc" };
  if (e.shiftKey) return null;
  if (el.closest(EDITABLE) || (el as HTMLElement).isContentEditable) return null;
  if (/^[1-9]$/.test(e.key)) return { kind: "digit", digit: Number(e.key) };
  if (e.key === "Enter") return el === root || !el.closest(INTERACTIVE) ? { kind: "enter" } : null;
  return null;
}

/** The chat's composer on this page, if there is one; the card's Esc hands focus back to it. */
export function focusComposer(): boolean {
  const el = [...document.querySelectorAll<HTMLTextAreaElement>("main textarea:not(.xterm-helper-textarea)")].find((t) => !t.disabled && t.getClientRects().length > 0);
  if (!el) return false;
  el.focus();
  return true;
}

/** The first waiting card on the page that is showing (what the "focus-card" shortcut gives the keyboard). */
export function focusWaitingCard(): boolean {
  const el = [...document.querySelectorAll<HTMLElement>("[data-card-keys]")].find((e) => e.getClientRects().length > 0);
  if (!el) return false;
  el.focus();
  return true;
}
