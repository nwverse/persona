// The Workbench layout's Labs switch (#13439, docs/web/web.md "Workbench (Labs)"): an opt-in, per-browser layout of the
// existing web app for people who work with coding Agents (projects with their threads, the conversation, the changes and
// a terminal drawer). This file is the shell's whole eager part: the switch and the layout flags (its Labs source, with the words, is lib/labs-workbench-source.ts, which only Settings loads). The
// layout itself is a lazy chunk (views/workbench/), so it never joins the first screen.
//
// Scope: this browser only (localStorage); nothing reaches the hub. Storage follows lib/labs.ts: on is "1", off writes no
// key; a value this build does not recognize reads as off and is left in place until a choice replaces it.
import { useSyncExternalStore } from "react";
import type { LabState, LabWrite } from "./labs";

export const WORKBENCH_KEY = "persona.workbench";
export const WORKBENCH_LAYOUT_KEY = "persona.workbench.layout";

// ── the switch ───────────────────────────────────────────

/** Whether the layout is on, and where that came from. */
export function readWorkbench(): LabState<boolean> {
  let raw: string | null;
  try {
    raw = localStorage.getItem(WORKBENCH_KEY);
  } catch {
    return { value: false, source: "unreadable", raw: null };
  }
  if (raw === null) return { value: false, source: "default", raw };
  return raw === "1" ? { value: true, source: "saved", raw } : { value: false, source: "invalid", raw };
}

const listeners = new Set<() => void>();
const notify = () => listeners.forEach((l) => l());

/** Keeps the choice (on as "1", off as no key); a browser that does not keep it is reported, never taken as saved. */
export function writeWorkbench(on: boolean): LabWrite {
  if (typeof on !== "boolean") return { ok: false, reason: "invalid" };
  const raw = on ? "1" : null;
  try {
    if (raw === null) localStorage.removeItem(WORKBENCH_KEY);
    else localStorage.setItem(WORKBENCH_KEY, raw);
    if (localStorage.getItem(WORKBENCH_KEY) !== raw) return { ok: false, reason: "storage" };
  } catch {
    return { ok: false, reason: "storage" };
  }
  notify();
  return { ok: true };
}

/** Called when the choice or the layout flags may have changed: a write here, or another tab of this browser. */
export function subscribeWorkbench(l: () => void): () => void {
  listeners.add(l);
  const storage = (e: StorageEvent) => {
    if (e.key === null || e.key === WORKBENCH_KEY || e.key === WORKBENCH_LAYOUT_KEY) l();
  };
  if (typeof window !== "undefined") window.addEventListener("storage", storage);
  return () => {
    listeners.delete(l);
    if (typeof window !== "undefined") window.removeEventListener("storage", storage);
  };
}

/** The switch, live: the shell re-renders when it flips, with no reload. */
export function useWorkbench(): boolean {
  return useSyncExternalStore(subscribeWorkbench, () => readWorkbench().value, () => false);
}

// ── the layout flags ─────────────────────────────────────

export interface WorkbenchLayoutState {
  /** The changes region on the right. */
  diff: boolean;
  /** The terminal drawer at the bottom. */
  drawer: boolean;
  /** The drawer's height in px (0: the default). */
  drawerHeight: number;
  /** The changes region's width in px (0: the default). */
  diffWidth: number;
  /** Project keys folded in the sidebar. */
  folded: string[];
}

export const WORKBENCH_LAYOUT_DEFAULT: WorkbenchLayoutState = { diff: true, drawer: false, drawerHeight: 0, diffWidth: 0, folded: [] };

function parseLayout(raw: string | null): WorkbenchLayoutState {
  if (!raw) return WORKBENCH_LAYOUT_DEFAULT;
  try {
    const v = JSON.parse(raw) as Partial<WorkbenchLayoutState> | null;
    if (!v || typeof v !== "object") return WORKBENCH_LAYOUT_DEFAULT;
    const n = (x: unknown) => (typeof x === "number" && Number.isFinite(x) && x > 0 ? Math.round(x) : 0);
    return {
      diff: typeof v.diff === "boolean" ? v.diff : WORKBENCH_LAYOUT_DEFAULT.diff,
      drawer: typeof v.drawer === "boolean" ? v.drawer : WORKBENCH_LAYOUT_DEFAULT.drawer,
      drawerHeight: n(v.drawerHeight),
      diffWidth: n(v.diffWidth),
      folded: Array.isArray(v.folded) ? v.folded.filter((k): k is string => typeof k === "string") : [],
    };
  } catch {
    return WORKBENCH_LAYOUT_DEFAULT;
  }
}

let cachedRaw: string | null | undefined;
let cachedLayout = WORKBENCH_LAYOUT_DEFAULT;
/** Storage refused a write: this page keeps its own copy from then on. */
let memoryOnly = false;

/** The layout flags as kept (a stable object while nothing changed, for useSyncExternalStore). */
export function readWorkbenchLayout(): WorkbenchLayoutState {
  let raw: string | null = null;
  try {
    raw = localStorage.getItem(WORKBENCH_LAYOUT_KEY);
  } catch {
    // unreadable storage: the defaults
  }
  if (memoryOnly) return cachedLayout;
  if (raw !== cachedRaw) {
    cachedRaw = raw;
    cachedLayout = parseLayout(raw);
  }
  return cachedLayout;
}

export function writeWorkbenchLayout(patch: Partial<WorkbenchLayoutState>): void {
  const next = { ...readWorkbenchLayout(), ...patch };
  try {
    localStorage.setItem(WORKBENCH_LAYOUT_KEY, JSON.stringify(next));
  } catch {
    // not kept: this page's copy follows from here on
    memoryOnly = true;
  }
  cachedRaw = JSON.stringify(next);
  cachedLayout = next;
  notify();
}

export function useWorkbenchLayout(): WorkbenchLayoutState {
  return useSyncExternalStore(subscribeWorkbench, readWorkbenchLayout, () => WORKBENCH_LAYOUT_DEFAULT);
}

// ── the keys the layout re-points ────────────────────────

const keys = new Map<string, () => boolean | void>();

/** While the layout is up, a shortcut the shell already has (⌘B the right panel, ⌘J the terminal) does what the layout
 *  says (lib/use-shortcuts.ts asks first). Returns the way to hand it back; with none taken the shortcut is the usual one. */
export function takeWorkbenchKey(id: string, run: () => boolean | void): () => void {
  keys.set(id, run);
  return () => void (keys.get(id) === run && keys.delete(id));
}

/** The layout's answer to a shortcut, when it has one: undefined means the usual handler runs. */
export function workbenchKey(id: string): (() => boolean | void) | undefined {
  return keys.get(id);
}
