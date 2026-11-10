// Who gets an input when the terminal is herdr's own client (docs/integrations/terminal.md "Full view conflicts", #2700). In the full
// surface the whole terminal area is herdr's: its right-click menus, its prefix key, its mouse selection. Persona keeps
// what herdr does not use, or what needs a modifier herdr does not bind. These are the decisions, as pure functions;
// views/session/terminal/full.ts and FullTerminal.tsx only wire them to events. The single-pane surface is untouched
// (its own rules are lib/terminal-mouse.ts and lib/shortcuts.ts).
import type { Platform } from "./shortcuts";

// ── the mouse ───────────────────────────────────────────

/** What a right click does: herdr's own menu (the report went to it), Persona's terminal menu, or the browser's. */
export type RightClick = "herdr" | "menu" | "browser";

/**
 * herdr takes the mouse (it asks for mouse reporting as soon as it starts) and has menus of its own under the right
 * button (pane, space), so the plain right click is its. Persona's menu is one chord away: Shift, which xterm also
 * keeps for itself (Shift + drag selects locally), as in the single-pane surface. Where herdr does not have the mouse
 * (not up yet, `mouse_capture = false`) nothing conflicts: Persona's menu on the plain click, the browser's with Shift.
 * A touch long press is the phone's text selection.
 */
export function rightClickRoute(e: { shiftKey: boolean }, herdrHasMouse: boolean, pointerType?: string): RightClick {
  if (pointerType === "touch") return "browser";
  if (herdrHasMouse) return e.shiftKey ? "menu" : "herdr";
  return e.shiftKey ? "browser" : "menu";
}

/**
 * Whether a click on a link xterm found in herdr's screen opens it. While herdr has the mouse a plain click is herdr's
 * (focusing a pane, selecting a word — a double click on a URL would otherwise open it twice), so a link takes ⌘ / Ctrl
 * as in other terminals; a finger has no modifier and herdr does not read a tap as anything but a click, so it opens.
 */
export function linkOpens(e: { metaKey: boolean; ctrlKey: boolean }, herdrHasMouse: boolean, pointerType?: string): boolean {
  return !herdrHasMouse || pointerType === "touch" || e.metaKey || e.ctrlKey;
}

// ── the keyboard ────────────────────────────────────────

/**
 * herdr's own keys that are not behind its prefix, written as its config writes them (`herdr --default-config`). In
 * 0.9.5 that is the prefix itself, ctrl+b; every other action is "prefix+…" (bare keys and shift chords after it) or
 * unbound, so Persona's Mod chords (Mod is Ctrl off the Mac) meet it only there. Someone who moved the prefix in
 * herdr's config.toml frees this one and takes another; Persona does not read that file.
 */
export const HERDR_DIRECT_KEYS: readonly string[] = ["ctrl+b"];

export interface KeyLike {
  key: string;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
}

const KEY_NAMES: Record<string, string> = { esc: "escape", enter: "enter", tab: "tab", space: " ", up: "arrowup", down: "arrowdown", left: "arrowleft", right: "arrowright" };

/** Whether a keydown is exactly one of herdr's direct keys ("ctrl+b": Ctrl and the key, no other modifier). */
export function herdrOwnsKey(e: KeyLike, keys: readonly string[] = HERDR_DIRECT_KEYS): boolean {
  const pressed = e.key.toLowerCase();
  return keys.some((spec) => {
    const parts = spec.toLowerCase().split("+");
    const key = parts.pop() ?? "";
    const want = (m: string[]) => parts.some((p) => m.includes(p));
    return (
      pressed === (KEY_NAMES[key] ?? key) &&
      e.ctrlKey === want(["ctrl", "control"]) &&
      e.altKey === want(["alt", "option"]) &&
      e.shiftKey === want(["shift"]) &&
      e.metaKey === want(["cmd", "super", "meta"])
    );
  });
}

/** Whether an event target is inside the full surface (FullTerminal marks its root `data-herdr-full`). */
export function inFullSurface(target: unknown): boolean {
  const el = target as { closest?: (selector: string) => unknown } | null;
  return Boolean(el && typeof el.closest === "function" && el.closest("[data-herdr-full]"));
}

/**
 * Ctrl+V off the Mac. xterm sends ^V for it and cancels the key, so the browser never pastes and a screenshot on the
 * clipboard never reaches the page (⌘V on a Mac and Ctrl+Shift+V do paste, with a `paste` event that carries the files).
 * The full surface takes the key to look at the clipboard first: files are uploaded, anything else is the ^V it has
 * always been — herdr's panes use it (quoted insert, vim's block mode), so a text paste is still Ctrl+Shift+V / the key bar.
 */
export function isCtrlV(e: KeyLike, platform: Platform): boolean {
  return platform === "other" && e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey && e.key.toLowerCase() === "v";
}
