// Settings › Labs (#9877, docs/web/web.md "Labs"): the one place a trial feature or a choice between an old and a new
// implementation is defined, validated, kept and read. A feature never adds its own switch for a trial; it adds an entry to
// LABS and reads it through readLab / useLab.
//
// Scope: every entry here is kept by this browser (localStorage), exactly like the other per-browser settings. It never
// reaches the hub, another browser, another device or another user, and it grants nothing: a lab only picks between code
// paths the page already has and is already allowed to run. An entry that would need the hub (an instance- or user-wide
// trial) is a different kind and needs a hub-side definition and its permission check first; this registry does not do it.
//
// Storage: each entry keeps its own key, the one it had before Labs existed where there was one, so a saved choice keeps
// working and an older build reads the same value back (rollback needs no migration). The default writes nothing. A value
// this build does not recognize (written by a newer build, or by hand) reads as the default and is left in place until the
// user chooses again, so going back and forth between versions never loses it.
//
// Lifecycle: see docs/web/web.md "Labs" ("Adding, graduating and removing an entry").
import { useSyncExternalStore } from "react";

/** Where a lab's value is kept and who it affects. Only "browser" exists today (see the header). */
export type LabScope = "browser";

/** When a change takes effect. "new-views": what is already open keeps the value it opened with; the next one opened
 *  (or the page after a reload) uses the new one. */
export type LabActivation = "new-views";

interface LabBase {
  /** The localStorage key. Kept forever for an entry that had a key before Labs, so old saved choices still read. */
  key: string;
  scope: LabScope;
  activation: LabActivation;
  /** The issue that added it: where its graduation or removal is decided. */
  issue: number;
}

export interface BooleanLab extends LabBase {
  kind: "boolean";
  default: boolean;
}

export interface LabOption<V extends string = string> {
  value: V;
  /** What is stored for it, when that differs from `value` (a key that held "1" before Labs). */
  raw?: string;
}

export interface ChoiceLab<V extends string = string> extends LabBase {
  kind: "choice";
  options: readonly LabOption<V>[];
  default: V;
}

export type LabDefinition = BooleanLab | ChoiceLab;

/** An implementation choice (stable / new). Its first option is normally the default. */
export function choiceLab<const V extends string>(def: Omit<ChoiceLab<V>, "kind">): ChoiceLab<V> {
  return { kind: "choice", ...def };
}

/** A trial feature that is on or off. No entry uses it yet: entries are real features only, never demos. */
export function booleanLab(def: Omit<BooleanLab, "kind">): BooleanLab {
  return { kind: "boolean", ...def };
}

/** Every lab, in the order Settings › Labs shows them. */
export const LABS = {
  /** Single pane terminals draw with xterm's default DOM renderer, or with WebGL (@xterm/addon-webgl). The key and its
   *  "1" are the Terminal settings switch's from before Labs (docs/integrations/terminal.md). */
  "terminal-renderer": choiceLab({
    key: "persona.terminal.webgl",
    options: [{ value: "standard" }, { value: "webgl", raw: "1" }],
    default: "standard",
    scope: "browser",
    activation: "new-views",
    issue: 9877,
  }),
} satisfies Record<string, LabDefinition>;

export type LabId = keyof typeof LABS;
export const LAB_IDS = Object.keys(LABS) as LabId[];

type Def<I extends LabId> = (typeof LABS)[I];
export type LabValue<I extends LabId> = Def<I> extends ChoiceLab<infer V> ? V : boolean;

/** Where the value read came from. "invalid": something this build does not recognize was saved (left in place);
 *  "unreadable": this browser does not let the page read its storage. Both read as the default. */
export type LabSource = "default" | "saved" | "invalid" | "unreadable";

export interface LabState<V> {
  value: V;
  source: LabSource;
}

export type LabWrite = { ok: true } | { ok: false; reason: "invalid" | "storage" };

export function isLabId(id: string): id is LabId {
  return Object.hasOwn(LABS, id);
}

function definition(id: LabId): LabDefinition {
  return LABS[id] as LabDefinition;
}

/** What a value is stored as; null: nothing (the default). Undefined: not a value of this lab. */
export function encodeLab(def: LabDefinition, value: unknown): string | null | undefined {
  if (def.kind === "boolean") {
    if (typeof value !== "boolean") return undefined;
    return value === def.default ? null : value ? "1" : "0";
  }
  const option = def.options.find((o) => o.value === value);
  if (!option) return undefined;
  return option.value === def.default ? null : (option.raw ?? option.value);
}

/** The value a stored string means; undefined: not one this build recognizes. */
export function decodeLab(def: LabDefinition, raw: string): string | boolean | undefined {
  if (def.kind === "boolean") return raw === "1" ? true : raw === "0" ? false : undefined;
  return def.options.find((o) => (o.raw ?? o.value) === raw)?.value;
}

/** Whether `value` is one of the lab's values (a choice's option, or a boolean). */
export function isLabValue<I extends LabId>(id: I, value: unknown): value is LabValue<I> {
  return isLabId(id) && encodeLab(definition(id), value) !== undefined;
}

const UNREADABLE = Symbol("unreadable");

function rawOf(id: LabId): string | null | typeof UNREADABLE {
  try {
    return localStorage.getItem(definition(id).key);
  } catch {
    return UNREADABLE;
  }
}

/** The state a stored string reads as. */
export function parseLab<I extends LabId>(id: I, raw: string | null | typeof UNREADABLE): LabState<LabValue<I>> {
  const def = definition(id);
  const fallback = def.default as LabValue<I>;
  if (raw === UNREADABLE) return { value: fallback, source: "unreadable" };
  if (raw === null) return { value: fallback, source: "default" };
  const value = decodeLab(def, raw);
  return value === undefined ? { value: fallback, source: "invalid" } : { value: value as LabValue<I>, source: "saved" };
}

/** The lab's current value and where it came from. Read it once where the feature starts (a view opening), not on every
 *  render of something already running: see LabActivation. */
export function readLab<I extends LabId>(id: I): LabState<LabValue<I>> {
  return parseLab(id, rawOf(id));
}

const listeners = new Set<() => void>();

/** Keeps a value. The default removes the key. A value the lab does not offer is refused, and a browser that does not
 *  keep it (storage off, full, or the write not read back) is reported, never taken as saved. */
export function writeLab<I extends LabId>(id: I, value: LabValue<I>): LabWrite {
  if (!isLabId(id)) return { ok: false, reason: "invalid" };
  const def = definition(id);
  const raw = encodeLab(def, value);
  if (raw === undefined) return { ok: false, reason: "invalid" };
  try {
    if (raw === null) localStorage.removeItem(def.key);
    else localStorage.setItem(def.key, raw);
    if (localStorage.getItem(def.key) !== raw) return { ok: false, reason: "storage" };
  } catch {
    return { ok: false, reason: "storage" };
  }
  listeners.forEach((l) => l());
  return { ok: true };
}

const KEYS = new Set(LAB_IDS.map((id) => definition(id).key));

function subscribe(l: () => void) {
  listeners.add(l);
  // another tab of this browser changed one (the same browser-wide value)
  const storage = (e: StorageEvent) => {
    if (e.key === null || KEYS.has(e.key)) l();
  };
  if (typeof window !== "undefined") window.addEventListener("storage", storage);
  return () => {
    listeners.delete(l);
    if (typeof window !== "undefined") window.removeEventListener("storage", storage);
  };
}

// useSyncExternalStore needs the same object while nothing changed: one state per lab and stored string.
const cache = new Map<LabId, { raw: string | null | typeof UNREADABLE; state: LabState<unknown> }>();

function snapshot<I extends LabId>(id: I): LabState<LabValue<I>> {
  const raw = rawOf(id);
  const hit = cache.get(id);
  if (hit && hit.raw === raw) return hit.state as LabState<LabValue<I>>;
  const state = parseLab(id, raw);
  cache.set(id, { raw, state });
  return state;
}

/** The lab's saved state for a settings page: follows writeLab here and changes from another tab. A feature already
 *  running keeps what it read when it started (readLab); this is for showing and changing the choice. */
export function useLab<I extends LabId>(id: I): LabState<LabValue<I>> {
  return useSyncExternalStore(
    subscribe,
    () => snapshot(id),
    () => ({ value: definition(id).default as LabValue<I>, source: "default" as const }),
  );
}

export type TerminalRenderer = LabValue<"terminal-renderer">;
