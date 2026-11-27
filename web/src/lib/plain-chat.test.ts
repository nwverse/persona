// The plain main chat (#16898, lib/plain-chat.ts): finished background rounds and briefings go, the rest stays.
import { describe, expect, it } from "vitest";
import type { FeedItem, FeedKind } from "./feed";
import { plainChatLines, plainFeed } from "./plain-chat";
import type { ChatLine } from "./turns";

const segment = (id: string): ChatLine => ({ kind: "segment", id, seg: {} as never, live: false });
const round = (id: string, live: boolean): ChatLine => ({ kind: "supervision", id, lines: [segment(id + "-s")], live });
const item = (id: string, kind: FeedKind): FeedItem => ({ id, conversation: "main", at: "2026-01-01T00:00:00Z", kind });

describe("plain main chat", () => {
  it("drops finished background rounds and keeps the exchanges and a live round", () => {
    const lines = [segment("q1"), round("patrol", false), segment("q2"), round("now", true)];
    expect(plainChatLines(lines).map((l) => l.id)).toEqual(["q1", "q2", "now"]);
  });

  it("returns the same list when nothing goes, so memoized rows keep their objects", () => {
    const lines = [segment("q1")];
    expect(plainChatLines(lines)).toBe(lines);
    const feed = [item("p", "plan")];
    expect(plainFeed(feed)).toBe(feed);
  });

  it("drops briefings, the day's summary and inbox suggestions, and keeps what waits on the owner", () => {
    const feed = (["brief", "today", "inbox_suggestion", "plan", "task", "view", "urgent_call", "routed", "answered"] as FeedKind[]).map((k) => item(k, k));
    expect(plainFeed(feed).map((i) => i.kind)).toEqual(["plan", "task", "view", "urgent_call", "routed", "answered"]);
  });
});
