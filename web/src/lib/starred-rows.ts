// Chat and sidebar only need the compact index. Load the requests and write
// handling separately from the first screen and never pull snapshot text here.
import { useEffect, useSyncExternalStore } from "react";
import type { Item } from "./types";

/** Transcript location as the chat already holds it. */
export type StarOrigin = {
  mid: string;
  sid: string;
  item: string;
  fingerprint?: string;
  userInputs?: readonly Item[];
  merged?: boolean;
};

export interface StarSource {
  machine_id: string;
  session_id: string;
  item_id: string;
  conversation: "assistant" | "agent";
  source_fingerprint?: string;
}

export interface StarInput extends StarSource {
  role: "user" | "assistant";
  title: string;
  speaker: string;
  source_manager?: string;
  written_at?: string;
  text: string;
  omitted_chars?: number;
}

export interface Star extends StarInput {
  id: string;
  starred_at: string;
}

export const positionalStar = (item: string) => /^line[0-9]+#[0-9]+$/.test(item);
export const starrable = (source?: StarOrigin): source is StarOrigin =>
  !!source && (!positionalStar(source.item) || !!source.fingerprint);

export function starKey(s: StarSource & { id?: string }) {
  const proof = s.source_fingerprint || (s.id && positionalStar(s.item_id) ? `saved:${s.id}` : "");
  return JSON.stringify([s.machine_id, s.session_id, s.item_id, s.conversation, proof]);
}

type StarMark = { id?: string; pending?: "add" | "remove" };
const empty: StarMark = {};
let rows: ReadonlyMap<string, StarMark> = new Map();
let count: number | null | undefined;
let writable = false;
let chunk: Promise<typeof import("./starred")> | undefined;
const listeners = new Set<() => void>();
const subscribe = (listener: () => void) => (listeners.add(listener), () => void listeners.delete(listener));

export function starredModule() {
  chunk ??= import("./starred").catch((error: unknown) => {
    chunk = undefined;
    throw error;
  });
  return chunk;
}

export function setStarredRows(next: ReadonlyMap<string, StarMark>, total: number | null | undefined, canWrite: boolean) {
  rows = new Map([...next].map(([key, mark]) => {
    const previous = rows.get(key);
    return [key, previous && previous.id === mark.id && previous.pending === mark.pending ? previous : mark];
  }));
  count = total;
  writable = canWrite;
  listeners.forEach((l) => l());
}

function start() {
  void starredModule().then((m) => m.startStarred(), () => {});
}

/** Undefined while availability is unknown, null when unavailable, or the confirmed count. */
export function useStarredCount(enabled: boolean) {
  useEffect(() => { if (enabled) start(); }, [enabled]);
  return useSyncExternalStore(subscribe, () => enabled ? count : null, () => undefined);
}

export function useStarMark(source: StarSource | undefined): StarMark {
  const key = source && starKey(source);
  return useSyncExternalStore(subscribe, () => (key && rows.get(key)) || empty, () => empty);
}

export function useStarAccess() {
  return useSyncExternalStore(subscribe, () => writable ? "write" : count != null ? "read" : null, () => null);
}

export const useCanStar = () => useStarAccess() === "write";
