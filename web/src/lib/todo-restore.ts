// Shelved to-dos brought back by hand (#3156, docs/assistant/todo-status-sop.md "Shelving"): the moment of the restore
// counts as activity, so the to-do leaves Shelved and goes back only after another 48 hours without a move. The hub
// keeps nothing about it: a restore is this browser's (localStorage), and nothing of the to-do changes or is deleted.

import { useCallback, useSyncExternalStore } from "react";
import { SHELVE_AFTER_MS } from "./todo-triage";

const KEY = "persona.todos.restored";
const listeners = new Set<() => void>();
let cache: ReadonlyMap<string, number> | undefined;

function read(): ReadonlyMap<string, number> {
  if (cache) return cache;
  const out = new Map<string, number>();
  try {
    const raw = JSON.parse(localStorage.getItem(KEY) ?? "{}") as Record<string, unknown>;
    for (const [id, at] of Object.entries(raw)) if (typeof at === "number" && Number.isFinite(at)) out.set(id, at);
  } catch {
    // a broken value is as none
  }
  cache = out;
  return out;
}

/** Restores to-do id now: kept until it would be shelved again anyway; older restores are dropped as it is written. */
export function restoreTodo(id: string, now = Date.now()): void {
  const next = new Map<string, number>();
  for (const [k, at] of read()) if (now - at < SHELVE_AFTER_MS) next.set(k, at);
  next.set(id, now);
  cache = next;
  try {
    localStorage.setItem(KEY, JSON.stringify(Object.fromEntries(next)));
  } catch {
    // storage full or off: the restore holds for this page only
  }
  for (const l of listeners) l();
}

function subscribe(l: () => void): () => void {
  listeners.add(l);
  const onStorage = (e: StorageEvent) => {
    if (e.key !== KEY) return;
    cache = undefined;
    l();
  };
  window.addEventListener("storage", onStorage);
  return () => {
    listeners.delete(l);
    window.removeEventListener("storage", onStorage);
  };
}

/** The restores (to-do id → when) and the function that restores one. */
export function useRestoredTodos(): [ReadonlyMap<string, number>, (id: string) => void] {
  const map = useSyncExternalStore(subscribe, read, read);
  return [map, useCallback((id: string) => restoreTodo(id), [])];
}

/** For tests: forget the cached value. */
export function resetRestoredCache(): void {
  cache = undefined;
}
