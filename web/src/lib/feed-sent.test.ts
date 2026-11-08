// What Persona sent to its tasks' agents (#1542): only a routed item marked from "assistant" is Persona's; a task's card
// takes in the hand-off written right after it (same task, same conversation), never a later message or the user's.
import { describe, expect, it } from "vitest";
import { displayLines, firstLine, HAND_OFF_MS, sentByPersona, sentTo, shownFeed, takenIn, type FeedItem } from "./feed";

const t0 = Date.parse("2026-11-08T12:00:00Z");
const at = (ms: number) => new Date(t0 + ms).toISOString();
const card = (id: string, ref: string, ms: number, conversation = "c1"): FeedItem => ({ id, conversation, at: at(ms), kind: "task", ref });
const persona = (id: string, ref: string, ms: number, extra: Record<string, unknown> = {}, conversation = "c1"): FeedItem => ({
  id, conversation, at: at(ms), kind: "routed", ref, body: { text: "把内存降下来\n背景：…", from: "assistant", ...extra },
});
const user = (id: string, ref: string, ms: number): FeedItem => ({ id, conversation: "c1", at: at(ms), kind: "routed", ref, body: { text: "顺便看看日志" } });

describe("sent to agents (#1542)", () => {
  it("is Persona's only when the hub marked it", () => {
    expect(sentByPersona(persona("a", "t", 0))).toBe(true);
    expect(sentByPersona(user("b", "t", 0))).toBe(false);
    // an interrupt from a hub before #1542 has no from: drawn as it was
    expect(sentByPersona({ ...user("c", "t", 0), body: { text: "先停", interrupted: true } })).toBe(false);
    expect(sentByPersona({ id: "d", conversation: "c1", at: at(0), kind: "report", ref: "t", body: { from: "assistant" } })).toBe(false);
  });

  it("takes in only the hand-off right after its own card", () => {
    const feed = [
      card("card", "t1", 0),
      persona("handoff", "t1", 8),
      user("said", "t1", 1000), // the user's own word: stays a bubble
      persona("rework", "t1", 600_000), // later: a folded line of its own
      persona("other", "t2", 2000), // another task, no card here
      card("card3", "t3", 2000),
      persona("elsewhere", "t3", 3000, {}, "c2"), // t3's card is in c1, the message in c2
      persona("before", "t4", 4000),
      card("card4", "t4", 5000), // written before its card: not a hand-off
    ];
    expect([...takenIn(feed)]).toEqual(["handoff"]);
    expect(shownFeed(feed).map((i) => i.id)).toEqual(["card", "said", "rework", "other", "card3", "elsewhere", "before", "card4"]);
    expect(sentTo(feed, "t1").map((i) => i.id)).toEqual(["handoff", "rework"]);
    expect(takenIn([card("c", "t", 0), persona("late", "t", HAND_OFF_MS + 1)]).size).toBe(0);
    expect([...takenIn([card("c", "t", 0), persona("edge", "t", HAND_OFF_MS)])]).toEqual(["edge"]);
  });

  it("leaves the hand-off out of the chat's lines", () => {
    const ids = displayLines([], [card("card", "t1", 0), persona("handoff", "t1", 8), persona("rework", "t1", 600_000)])
      .filter((l) => l.kind === "feed")
      .map((l) => l.id);
    expect(ids).toEqual(["feed-card", "feed-rework"]);
  });

  it("folds to the first line with words", () => {
    expect(firstLine("\n\n  把内存降下来，目标是不再被杀掉。  \n\n背景：\n- 一周 14 次")).toBe("把内存降下来，目标是不再被杀掉。");
    expect(firstLine("（督办：验收没通过，第 1 轮返工，最多 2 轮）\r\n逐条验收：")).toBe("（督办：验收没通过，第 1 轮返工，最多 2 轮）");
    expect(firstLine(" \n \n")).toBe("");
  });
});
