// The hub's `notice` feed line (docs/protocol/protocol.md「Tasks and the chat feed」, #4305): no ref, its text written by the hub in the
// user's language. Nothing to say: no line.
import { describe, expect, it } from "vitest";
import { feedNotice, type FeedItem } from "./feed";

const item = (kind: string, body?: Record<string, unknown>): FeedItem =>
  ({ id: "f1", conversation: "c1", at: "2026-11-06T08:00:00Z", kind, body }) as FeedItem;

describe("feedNotice", () => {
  it("is the hub's text", () => {
    expect(feedNotice(item("notice", { text: "电脑和模型接好了，定时任务恢复运行，这次是「早报」" }))).toBe("电脑和模型接好了，定时任务恢复运行，这次是「早报」");
  });
  it("is nothing without words", () => {
    expect(feedNotice(item("notice", { text: "  \n " }))).toBeUndefined();
    expect(feedNotice(item("notice"))).toBeUndefined();
    expect(feedNotice(item("notice", { text: 3 }))).toBeUndefined();
  });
  it("is only a notice's", () => {
    expect(feedNotice(item("routed", { text: "a message to a task" }))).toBeUndefined();
  });
});
