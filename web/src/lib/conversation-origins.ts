// Where an assistant conversation comes from (docs/protocol/protocol.md "Origin of assistant chats", #3264): the user's own (the app, a
// channel's /new) stay in the sidebar's "Recent"; the ones the hub opened by itself (the inbox's AI twin, a scheduled task,
// conversation_new, a plugin's /agent…) fold into one "Automatic" row, split by origin. Pure functions, tested without a DOM.
import { t } from "../i18n";
import type { Session } from "./types";

/** The sidebar's groups of automatic conversations, in its order; `other` is any origin this page doesn't know. */
export const AUTO_ORIGINS = ["inbox", "task", "lark_agent", "spawned", "extension", "other"] as const;
export type AutoOrigin = (typeof AUTO_ORIGINS)[number];

/** What "All chats" can be filtered to: everything, the user's own, or one group of automatic ones. */
export type OriginFilter = "all" | "mine" | AutoOrigin;

const USER_ORIGINS = new Set(["", "user", "channel"]);

/** The automatic group a conversation is in; undefined for the user's own (an older hub says nothing: all are). */
export function autoOrigin(s: Pick<Session, "origin">): AutoOrigin | undefined {
  const o = s.origin ?? "";
  if (USER_ORIGINS.has(o)) return undefined;
  return (AUTO_ORIGINS as readonly string[]).includes(o) ? (o as AutoOrigin) : "other";
}

export const isAutoConversation = (s: Pick<Session, "origin">) => autoOrigin(s) !== undefined;

export function originLabel(o: AutoOrigin): string {
  return t(`assistant-conversations.origin_${o}`);
}

/** The filter of a `/history/<filter>` address; anything else is all of them. */
export function parseOriginFilter(v: string | undefined): OriginFilter {
  return v === "mine" || (AUTO_ORIGINS as readonly string[]).includes(v ?? "") ? (v as OriginFilter) : "all";
}

export function matchesOrigin(s: Pick<Session, "origin">, filter: OriginFilter): boolean {
  if (filter === "all") return true;
  const o = autoOrigin(s);
  return filter === "mine" ? o === undefined : o === filter;
}

const working = (s: Session) => s.state === "working" || s.status === "working" || !!s.starting;
const needsYou = (s: Session) => s.state === "needs_you";

/** How a set of conversations stands, for a folded row: how many, how many run, how many wait for the user. */
export interface OriginTally {
  count: number;
  working: number;
  needsYou: number;
}

function tally(list: readonly Session[]): OriginTally {
  return { count: list.length, working: list.filter(working).length, needsYou: list.filter(needsYou).length };
}

/** The automatic conversations (not archived, not the main one) by group, only the groups that have any, in the
 *  sidebar's order, and all of them together. */
export function autoConversations(conversations: readonly Session[], main: string | undefined): { total: OriginTally; groups: (OriginTally & { origin: AutoOrigin })[] } {
  const auto = conversations.filter((c) => c.session_id !== main && !c.archived && isAutoConversation(c));
  const groups = AUTO_ORIGINS.map((origin) => ({ origin, ...tally(auto.filter((c) => autoOrigin(c) === origin)) })).filter((g) => g.count > 0);
  return { total: tally(auto), groups };
}
