import { describe, expect, it } from "vitest";
import { autoConversations, autoOrigin, matchesOrigin, parseOriginFilter } from "./conversation-origins";
import type { Session } from "./types";

const conv = (id: string, extra: Partial<Session> = {}) => ({ machine_id: "hub", session_id: id, title: id, cwd: "", status: "idle", ...extra }) as Session;

describe("conversation origins (docs/protocol/protocol.md「Origin of assistant chats」)", () => {
  it("counts none, user and channel as the user's own and anything else as automatic", () => {
    expect(autoOrigin(conv("a"))).toBeUndefined();
    expect(autoOrigin(conv("a", { origin: "user" }))).toBeUndefined();
    expect(autoOrigin(conv("a", { origin: "channel" }))).toBeUndefined();
    expect(autoOrigin(conv("a", { origin: "inbox" }))).toBe("inbox");
    expect(autoOrigin(conv("a", { origin: "lark_agent" }))).toBe("lark_agent");
    expect(autoOrigin(conv("a", { origin: "something_new" }))).toBe("other");
  });

  it("tallies the automatic ones by origin, in the sidebar's order, with what runs and waits", () => {
    const list = [
      conv("main", { origin: "task" }),
      conv("t1", { origin: "task", state: "working" }),
      conv("i1", { origin: "inbox" }),
      conv("i2", { origin: "inbox", state: "needs_you" }),
      conv("i3", { origin: "inbox", archived: true, state: "needs_you" }),
      conv("x", { origin: "future" }),
      conv("mine"),
    ];
    const { total, groups } = autoConversations(list, "main");
    expect(total).toEqual({ count: 4, working: 1, needsYou: 1 });
    expect(groups).toEqual([
      { origin: "inbox", count: 2, working: 0, needsYou: 1 },
      { origin: "task", count: 1, working: 1, needsYou: 0 },
      { origin: "other", count: 1, working: 0, needsYou: 0 },
    ]);
  });

  it("filters 全部对话 by an address's filter", () => {
    expect(parseOriginFilter(undefined)).toBe("all");
    expect(parseOriginFilter("bogus")).toBe("all");
    expect(parseOriginFilter("mine")).toBe("mine");
    expect(parseOriginFilter("inbox")).toBe("inbox");
    expect(matchesOrigin(conv("a", { origin: "inbox" }), "mine")).toBe(false);
    expect(matchesOrigin(conv("a", { origin: "channel" }), "mine")).toBe(true);
    expect(matchesOrigin(conv("a", { origin: "inbox" }), "inbox")).toBe(true);
    expect(matchesOrigin(conv("a", { origin: "task" }), "inbox")).toBe(false);
    expect(matchesOrigin(conv("a", { origin: "task" }), "all")).toBe(true);
  });
});
