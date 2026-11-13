// Transient Inbox view state belongs to a browser history entry, not to the item's URL. The entry carries only an
// identifier; its snapshot lives in this page's memory. Thus a fresh navigation (and a reload) starts clean, while Back
// after another route remounts the Inbox can put back what that entry showed. Updates never write browser history.
import { createContext, useContext, useEffect, useLayoutEffect, useRef, type RefObject } from "react";

const ENTRY = "personaInboxView";
const SETTLE_MS = 1500;
const READER = ["wheel", "touchstart", "pointerdown", "keydown"] as const;

type ChainPlace = { item: string; open: boolean };
type ViewState = {
  query: string;
  toggled: Set<string>;
  scrollTop: number;
  chain: ChainPlace | null;
};

export interface InboxViewMemory {
  /** Whether this mount brought back a snapshot (as opposed to starting a fresh visit). */
  readonly restored: boolean;
  /** The current history entry's identifier; attached on the page's first layout effect. */
  readonly entryId: string | null;
  listQuery(): string;
  setListQuery(query: string): void;
  listToggled(): Set<string>;
  setListToggled(toggled: ReadonlySet<string>): void;
  listScroll(): number;
  setListScroll(top: number): void;
  chainOpen(itemKey: string): boolean;
  setChainOpen(itemKey: string, open: boolean): void;
  /** Attach the view to the entry now current, carrying the view forward on an in-Inbox navigation. */
  attach(): void;
  /** Save outside browser history: navigate has already changed the address when an unmount cleanup runs. */
  detach(): void;
}

const entries = new Map<string, ViewState>();
let nextEntry = 0;

function emptyState(): ViewState {
  return { query: "", toggled: new Set(), scrollTop: 0, chain: null };
}

function copyState(state: ViewState): ViewState {
  return {
    query: state.query,
    toggled: new Set(state.toggled),
    scrollTop: state.scrollTop,
    chain: state.chain ? { ...state.chain } : null,
  };
}

function objectState(state: unknown): Record<string, unknown> {
  return state && typeof state === "object" ? state as Record<string, unknown> : {};
}

function currentEntryId(): string | null {
  if (typeof history === "undefined") return null;
  const id = objectState(history.state)[ENTRY];
  return typeof id === "string" && id ? id : null;
}

class ViewMemory implements InboxViewMemory {
  readonly restored: boolean;
  private id: string | null;
  private state: ViewState;

  constructor(restore: boolean) {
    this.id = restore ? currentEntryId() : null;
    const saved = this.id ? entries.get(this.id) : undefined;
    this.restored = saved !== undefined;
    this.state = saved ? copyState(saved) : emptyState();
  }

  get entryId(): string | null {
    return this.id;
  }

  listQuery(): string {
    return this.state.query;
  }

  setListQuery(query: string): void {
    this.state.query = query;
  }

  listToggled(): Set<string> {
    return new Set(this.state.toggled);
  }

  setListToggled(toggled: ReadonlySet<string>): void {
    this.state.toggled = new Set(toggled);
  }

  listScroll(): number {
    return this.state.scrollTop;
  }

  setListScroll(top: number): void {
    this.state.scrollTop = Number.isFinite(top) ? Math.max(0, top) : 0;
  }

  chainOpen(itemKey: string): boolean {
    return this.state.chain?.item === itemKey && this.state.chain.open;
  }

  setChainOpen(itemKey: string, open: boolean): void {
    this.state.chain = { item: itemKey, open };
  }

  attach(): void {
    if (typeof history === "undefined") return;
    const actual = currentEntryId();
    if (this.id && actual === this.id) {
      entries.set(this.id, this.state);
      return;
    }
    // An in-Inbox push keeps the view on screen. Give its new entry a copy, leaving the previous entry's snapshot alone.
    if (this.id) entries.set(this.id, this.state);
    this.id = actual ?? `i${++nextEntry}`;
    this.state = copyState(this.state);
    if (!actual) history.replaceState({ ...objectState(history.state), [ENTRY]: this.id }, "");
    entries.set(this.id, this.state);
  }

  detach(): void {
    if (this.id) entries.set(this.id, this.state);
  }
}

/** Tests: forget every entry, as a reload does. Entry identifiers already in history are deliberately left alone. */
export function resetInboxViewMemoryForTests(): void {
  entries.clear();
}

/** The page owns entry attachment. `id` and `manager` make an in-Inbox row navigation attach the pushed entry too. */
export function useInboxViewMemory(id?: string, manager?: string): InboxViewMemory {
  const ref = useRef<ViewMemory | null>(null);
  if (!ref.current) ref.current = new ViewMemory(true);
  const memory = ref.current;
  useLayoutEffect(() => {
    memory.attach();
  }, [memory, id, manager]);
  useLayoutEffect(() => () => memory.detach(), [memory]);
  return memory;
}

export const InboxViewMemoryContext = createContext<InboxViewMemory | null>(null);

/** A list or chain under the page shares its memory; standalone component tests get a detached, clean one. */
export function useInboxView(): InboxViewMemory {
  const shared = useContext(InboxViewMemoryContext);
  const own = useRef<ViewMemory | null>(null);
  if (!own.current) own.current = new ViewMemory(false);
  return shared ?? own.current;
}

/** Put a list back where its entry was, retrying while late items grow its content. The reader can take over at once. */
function restoreList(el: HTMLElement, target: number): (() => void) | undefined {
  el.scrollTop = target;
  if (target === 0 || el.scrollTop >= target - 1) return undefined;
  let ro: ResizeObserver | undefined;
  const stop = () => {
    ro?.disconnect();
    ro = undefined;
    clearTimeout(timer);
    for (const event of READER) el.removeEventListener(event, stop);
  };
  const timer = setTimeout(stop, SETTLE_MS);
  for (const event of READER) el.addEventListener(event, stop, { passive: true });
  if (typeof ResizeObserver !== "undefined") {
    ro = new ResizeObserver(() => {
      el.scrollTop = target;
      if (el.scrollTop >= target - 1) stop();
    });
    ro.observe(el.firstElementChild ?? el);
  }
  return stop;
}

/**
 * Keeps the list's own scroller on its history entry. On a returned item, restoring wins over the usual selected-row
 * scrollIntoView. A single-column list may still be hidden then, so revealing the list tries again. Selecting another
 * item resumes the ordinary behavior, without resetting the list that stayed mounted.
 */
export function useInboxListScroll(
  box: RefObject<HTMLElement | null>,
  view: InboxViewMemory,
  selectedKey: string,
  loaded: boolean,
  storeLoaded: boolean,
  showSelected: () => void,
): void {
  const firstSelected = useRef(selectedKey);
  const restoring = useRef(view.restored);
  const show = useRef(showSelected);
  show.current = showSelected;

  useEffect(() => {
    if (selectedKey && selectedKey !== firstSelected.current) restoring.current = false;
    if (selectedKey && !restoring.current) show.current();
  }, [selectedKey, loaded, storeLoaded]);

  useLayoutEffect(() => {
    const el = box.current;
    if (!el || !restoring.current) return;
    return restoreList(el, view.listScroll());
  }, [box, view, selectedKey]);

  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const save = () => view.setListScroll(el.scrollTop);
    el.addEventListener("scroll", save, { passive: true });
    return () => {
      save();
      el.removeEventListener("scroll", save);
    };
  }, [box, view]);
}
