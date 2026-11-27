// The plain main chat (#16898, lib/looks.ts `main_chat: "plain"`): what the main conversation leaves out so it reads as
// questions and replies. Rounds the owner did not start (supervision, another conversation's relay, a scheduled task)
// go once they are over; a live one stays, so the chat still says it is at work. Briefings, the day's summary and inbox
// suggestions go; everything that waits on the owner (plans to start, decisions, live cards, urgent calls) stays.
import type { FeedItem, FeedKind } from "./feed";
import type { ChatLine } from "./turns";

export const PLAIN_HIDDEN_FEED: ReadonlySet<FeedKind> = new Set<FeedKind>(["brief", "today", "inbox_suggestion"]);

export function plainChatLines(lines: ChatLine[]): ChatLine[] {
  const kept = lines.filter((l) => l.kind !== "supervision" || l.live);
  return kept.length === lines.length ? lines : kept;
}

export function plainFeed(feed: FeedItem[]): FeedItem[] {
  const kept = feed.filter((item) => !PLAIN_HIDDEN_FEED.has(item.kind));
  return kept.length === feed.length ? feed : kept;
}
