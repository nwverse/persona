import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ChatActionsContext, liveValue, type ChatActions } from "./chat-actions";
import { MessageActions, moreActions, rewindActions } from "./MessageActions";

const base: ChatActions = { draftKey: "m/s", canCompose: true, speaker: "Persona", title: "Persona", images: () => [] };

const ts = new Date(Date.now() - 5 * 60_000).toISOString();

function render(actions: ChatActions, itemId?: string, bar = true) {
  return renderToStaticMarkup(
    <ChatActionsContext.Provider value={actions}>
      <MessageActions text="回复" ts={ts} itemId={itemId} bar={bar}>
        <p>回复</p>
      </MessageActions>
    </ChatActionsContext.Provider>,
  );
}

const labels = (html: string) => [...html.matchAll(/aria-label="([^"]+)"/g)].map((m) => m[1]);

// cindy's bar under a turn's last reply (#5314): Copy message, Start a new session (#2137) where the session can fork,
// More message actions, then the time.
describe("the reply bar", () => {
  it("is in cindy's order, the time last with the exact time as its tooltip", () => {
    const html = render({ ...base, fork: () => {} }, "u1#0");
    expect(labels(html)).toEqual(["复制消息", "开启一个新会话", "更多消息操作"]);
    expect(html).toContain("data-reply-bar");
    expect(html.indexOf('aria-label="更多消息操作"')).toBeLessThan(html.indexOf("<time"));
    expect(html).toMatch(/<time datetime="[^"]+" title="\d{4}-\d\d-\d\d \d\d:\d\d:\d\d"[^>]*>5 分钟前<\/time>/i);
  });
  it("says chat, not session, in a Persona conversation", () => {
    expect(labels(render({ ...base, fork: () => {}, assistant: true }, "u1#0"))).toContain("开启一个新对话");
  });
  it("has no Start a new session when the session cannot fork, or the reply is not a line of its own", () => {
    expect(labels(render(base, "u1#0"))).toEqual(["复制消息", "更多消息操作"]);
    expect(labels(render({ ...base, fork: () => {} }))).toEqual(["复制消息", "更多消息操作"]);
  });
  it("has no ⋯ when there is nothing to put in it", () => {
    expect(labels(render({ ...base, canCompose: false }, "u1#0"))).toEqual(["复制消息"]);
  });
  it("is not there without `bar` (narration, words still streaming): the menus only", () => {
    const html = render({ ...base, fork: () => {} }, "u1#0", false);
    expect(html).not.toContain("data-reply-bar");
    expect(labels(html)).toEqual([]);
    expect(html).toContain("<p>回复</p>");
  });
});

describe("moreActions", () => {
  it("branches off at the reply it is on", () => {
    const got: string[] = [];
    const [fork] = moreActions("u1#0", (id) => got.push(id));
    expect(fork?.label).toBe("开启一个新会话");
    expect(moreActions("u1#0", () => {}, true)[0]?.label).toBe("开启一个新对话");
    fork?.onSelect();
    expect(got).toEqual(["u1#0"]);
  });
  it("offers nothing for a line the node cannot find", () => {
    expect(moreActions("line12#0", () => {})).toEqual([]);
    expect(moreActions("u1", () => {})).toEqual([]);
    expect(moreActions(undefined, () => {})).toEqual([]);
  });
});

// "Rewind to here" (#2436): on your messages, "Conversation only" / "Code and conversation", the second asking again.
describe("rewindActions", () => {
  const run = async () => {};
  it("has the two choices, the second a danger that asks again", () => {
    const [rewind] = rewindActions("u1#0", "继续", { block: "", run });
    expect(rewind.label).toBe("回退到这里");
    expect(rewind.disabled).toBe(false);
    const [conv, code] = rewind.children as { label: string; danger?: boolean; confirm?: string; inline?: boolean }[];
    expect([conv.label, code.label]).toEqual(["只回退对话", "回退代码和对话"]);
    expect(conv.danger).toBeUndefined();
    expect(conv.confirm).toBeUndefined();
    expect(code.danger).toBe(true);
    expect(code.confirm).toBe("确认回退代码");
    expect(conv.inline && code.inline).toBe(true);
  });
  it("runs the choice it is given, with the message", async () => {
    const got: unknown[] = [];
    const [rewind] = rewindActions("u1#0", "继续", { block: "", run: async (...a) => void got.push(a) });
    const [conv, code] = rewind.children as { onSelect: () => Promise<unknown> }[];
    await conv.onSelect();
    await code.onSelect();
    expect(got).toEqual([["u1#0", "继续", "conversation"], ["u1#0", "继续", "code_and_conversation"]]);
  });
  it("is off with the reason while the session cannot", () => {
    const [rewind] = rewindActions("u1#0", "继续", { block: "工作中", run });
    expect(rewind.disabled).toBe(true);
    expect(rewind.hint).toBe("工作中");
  });
  it("is not there without a message, words or a session that rewinds", () => {
    expect(rewindActions(undefined, "继续", { block: "", run })).toEqual([]);
    expect(rewindActions("u1#0", "  ", { block: "", run })).toEqual([]);
    expect(rewindActions("u1#0", "继续", undefined)).toEqual([]);
  });
});

describe("MessageActions on your own message", () => {
  // read-only: the bar beside your bubble has nothing else for its ⋯ (Forward and Use as context go there when it can send)
  const mine = (actions: ChatActions, messageId?: string) =>
    renderToStaticMarkup(
      <ChatActionsContext.Provider value={{ ...actions, canCompose: false }}>
        <MessageActions text="继续" isUser side messageId={messageId}>
          <p>继续</p>
        </MessageActions>
      </ChatActionsContext.Provider>,
    );
  it("has ⋯ for 回退到这里 when the chat can rewind, even while it is off", () => {
    expect(mine({ ...base, rewind: { block: liveValue(""), run: async () => {} } }, "u1#0")).toContain('aria-label="更多"');
    expect(mine({ ...base, rewind: { block: liveValue("工作中"), run: async () => {} } }, "u1#0")).toContain('aria-label="更多"');
  });
  it("has none for a chat that cannot, or a message without an id", () => {
    expect(mine(base, "u1#0")).not.toContain('aria-label="更多"');
    expect(mine({ ...base, rewind: { block: liveValue(""), run: async () => {} } })).not.toContain('aria-label="更多"');
  });
});
