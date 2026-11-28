// A to-do's token usage and its estimated cost (#12936): what every Agent session linked to it used, its subagents
// included, as the hub sums it from what each computer reads of its own records (GET /api/v1/todos/usage for the
// cards, GET /api/v1/todos/{id}/usage for the detail). The amount is an estimate at public prices, never a bill.
// Cards ask together: the ids wanted in one tick go out as one request, and an answer is kept for a minute.

import { useEffect, useSyncExternalStore } from "react";
import { api, enc } from "./api";
import { compactTokens } from "./tokens";
import { formatCost } from "./turn-cost";

export interface TodoUsageTokens {
  in: number;
  cache_write: number;
  cache_read: number;
  out: number;
  total: number;
}

export interface TodoUsageSummary {
  tokens: TodoUsageTokens;
  usd: number;
  stale?: boolean;
  sessions: number;
  found: number;
}

export interface TodoUsagePart {
  machine_id?: string;
  session_id?: string;
  subagent?: string;
  title?: string;
  kind?: string;
  agent?: string;
  found: boolean;
  stale?: boolean;
  updated_at?: string;
  tokens: TodoUsageTokens;
  usd: number;
  subagents?: TodoUsagePart[];
}

export interface TodoUsageModel {
  model: string;
  tokens: TodoUsageTokens;
  usd: number;
  price: { input: number; cache_write: number; cache_read: number; output: number };
  price_from: "override" | "catalog" | "default";
}

export interface TodoUsageDetail extends TodoUsageSummary {
  todo_id: string;
  estimate: boolean;
  parts: TodoUsagePart[];
  models: TodoUsageModel[];
}

const KEEP_MS = 60_000;

type Entry = { at: number; value: TodoUsageSummary | null };
const cache = new Map<string, Entry>();
const listeners = new Set<() => void>();
let wanted = new Set<string>();
let timer: ReturnType<typeof setTimeout> | undefined;
let version = 0;

function notify() {
  version++;
  for (const l of listeners) l();
}

async function flush() {
  timer = undefined;
  const ids = [...wanted];
  wanted = new Set();
  for (let i = 0; i < ids.length; i += 100) {
    const batch = ids.slice(i, i + 100);
    try {
      const r = await api<{ todos?: Record<string, TodoUsageSummary> }>(`/todos/usage?ids=${batch.map(enc).join(",")}`);
      const now = Date.now();
      for (const id of batch) cache.set(id, { at: now, value: r.todos?.[id] ?? null });
    } catch {
      // an older hub or a failed read: no line, and asked again after KEEP_MS
      const now = Date.now();
      for (const id of batch) if (!cache.has(id)) cache.set(id, { at: now, value: null });
    }
  }
  notify();
}

function want(id: string) {
  const e = cache.get(id);
  if (e && Date.now() - e.at < KEEP_MS) return;
  if (e) e.at = Date.now(); // keep the old value while the new one is fetched, and ask once
  wanted.add(id);
  timer ??= setTimeout(() => void flush(), 0);
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

/** A to-do's total for its card; undefined until known, or when it has no Agents or used nothing. */
export function useTodoUsage(id: string, hasAgents: boolean): TodoUsageSummary | undefined {
  useSyncExternalStore(subscribe, () => version, () => version);
  useEffect(() => {
    if (hasAgents) want(id);
  }, [id, hasAgents]);
  if (!hasAgents) return undefined;
  const v = cache.get(id)?.value;
  return v && v.tokens.total > 0 ? v : undefined;
}

/** The detail of a to-do's usage, by session, subagent and model. */
export async function loadTodoUsage(id: string): Promise<TodoUsageDetail> {
  return api<TodoUsageDetail>(`/todos/${enc(id)}/usage`);
}

/** An amount as the reply bar writes one (turn-cost.ts formatCost): two decimals, "<$0.01" under a cent. */
export const formatUSD = formatCost;

/** A token count as the cards write it (12.3M). */
export const formatUsageTokens = compactTokens;

/** Tests only: forget every answer. */
export function resetTodoUsageForTest() {
  cache.clear();
  wanted = new Set();
  if (timer) clearTimeout(timer);
  timer = undefined;
  notify();
}
