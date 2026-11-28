// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import * as api from "../../lib/api";
import { setBotmuxMode } from "../../lib/botmux-mode";
import { segments } from "../../lib/turns";
import type { Item, Session } from "../../lib/types";
import { AgentStatusLine } from "./AgentStatusLine";
import { SegmentView } from "./SegmentView";

// A botmux session's exchange as the node sends it (docs/protocol/protocol.md "BotMux messages"): the message with
// botmux's blocks, and the `botmux send` call that answered it in Lark.
const raw = "<botmux_reminder>reply with botmux send</botmux_reminder>\n\n<user_message>\nship it\n</user_message>\n\n<sender type=\"user\" open_id=\"ou_example_alex\" name=\"Alex Chen\" />";
const items: Item[] = [
  { id: "u1#0", seq: 1, role: "user", kind: "text", text: raw, botmux: { kind: "message", text: "ship it", sender: "Alex Chen", sender_type: "user" } },
  { id: "a1#0", seq: 2, role: "assistant", kind: "tool_use", tool_name: "Bash", tool_use_id: "t1", tool_input: { command: "botmux send --response-kind final 'shipped v2'" }, botmux: { kind: "reply", text: "shipped v2", final: true } },
  { id: "r1#0", seq: 3, role: "user", kind: "tool_result", tool_use_id: "t1", text: "{\"ok\":true}" },
];

// the botmux pieces load on demand (React.lazy): render, then let them come in
async function render(transcript = items): Promise<string> {
  const host = document.createElement("div");
  const root = createRoot(host);
  await act(async () => root.render(<SegmentView seg={segments(transcript)[0]} live={false} mid="m" sid="s" expandFold={async () => {}} />));
  await act(async () => { await import("./BotmuxMessages"); await new Promise((r) => setTimeout(r, 0)); });
  const html = host.innerHTML;
  act(() => root.unmount());
  return html;
}

afterEach(() => {
  localStorage.clear();
  vi.restoreAllMocks();
});

describe("botmux sessions in the chat", () => {
  it("decoded (the default): the words written in Lark, the sender as a tag, and the reply sent there", async () => {
    const html = await render();
    expect(html).toContain('data-botmux-message="message"');
    expect(html).toContain("ship it");
    expect(html).toContain("Alex Chen");
    expect(html).not.toContain("&lt;user_message&gt;");
    expect(html).toContain("data-botmux-original");
    expect(html).toContain("data-botmux-reply");
    expect(html).toContain("shipped v2");
  });

  it("loads a paged Original text item and retries a failed request", async () => {
    const pagedItem: Item = {
      id: "u1#1", seq: 1, role: "user", kind: "text",
      text: "Original prefix\n…（已截断，点开看全文，共 90000 字节）", full_size: 90000,
    };
    const completeText = "Original prefix\nThe rest of the transcript";
    let failRequest!: (error: Error) => void;
    const request = new Promise<Item>((_resolve, reject) => { failRequest = reject; });
    const fullItem = vi.spyOn(api, "fullItem").mockReturnValueOnce(request)
      .mockResolvedValueOnce({ ...pagedItem, text: completeText, full_size: undefined });
    const host = document.createElement("div");
    const root = createRoot(host);
    const fullTextButton = () => host.querySelector<HTMLButtonElement>("[data-full-text]");

    try {
      await act(async () => { await import("./BotmuxMessages"); });
      await act(async () => root.render(<SegmentView seg={segments([items[0], pagedItem])[0]} live={false} mid="machine" sid="session" expandFold={async () => {}} />));
      expect(fullTextButton()).toBeNull();
      expect(fullItem).not.toHaveBeenCalled();

      await act(async () => host.querySelector<HTMLButtonElement>("[data-botmux-original]")!.click());
      expect(Array.from(host.querySelectorAll("pre"), (pre) => pre.textContent)).toEqual([raw, "Original prefix"]);
      expect(host.querySelectorAll("[data-full-text]")).toHaveLength(1);
      expect(fullTextButton()?.dataset.fullText).toBe("idle");
      expect(fullItem).not.toHaveBeenCalled();

      await act(async () => fullTextButton()!.click());
      expect(fullItem).toHaveBeenNthCalledWith(1, "machine", "session", pagedItem.id);
      expect(fullTextButton()?.dataset.fullText).toBe("loading");
      expect(fullTextButton()?.disabled).toBe(true);

      await act(async () => failRequest(new Error("Network failure")));
      expect(fullTextButton()?.dataset.fullText).toBe("failed");
      expect(fullTextButton()?.disabled).toBe(false);
      expect(fullTextButton()?.textContent).toContain("重试");

      await act(async () => fullTextButton()!.click());
      expect(fullItem).toHaveBeenNthCalledWith(2, "machine", "session", pagedItem.id);
      expect(Array.from(host.querySelectorAll("pre"), (pre) => pre.textContent)).toEqual([raw, completeText]);
      expect(fullTextButton()).toBeNull();
    } finally {
      act(() => root.unmount());
    }
  });

  it("preserves a single newline in a decoded reply", async () => {
    const html = await render([items[0], { ...items[1], botmux: { kind: "reply", text: "line one\nline two" } }, items[2]]);
    const host = document.createElement("div");
    host.innerHTML = html;
    const lineBreak = host.querySelector("[data-botmux-reply] br");
    expect(lineBreak?.previousSibling?.textContent).toBe("line one");
    expect(lineBreak?.nextSibling?.textContent?.trim()).toBe("line two");
  });

  it("reads a decoded reply line by line without breaking its tables and code blocks", async () => {
    const text = "Title\nField: value\n\n| Field | Meaning |\n| --- | --- |\n| a | b |\n\n```\nline 1\nline 2\n```";
    const html = await render([items[0], { ...items[1], botmux: { kind: "reply", text } }, items[2]]);
    const host = document.createElement("div");
    host.innerHTML = html;
    const reply = host.querySelector("[data-botmux-reply]")!;
    expect(reply.querySelector("p br")?.previousSibling?.textContent).toBe("Title");
    expect(Array.from(reply.querySelectorAll("table td"), (td) => td.textContent)).toEqual(["a", "b"]);
    expect(reply.querySelector("table br")).toBeNull();
    expect(reply.querySelector("pre")?.textContent).toContain("line 1\nline 2");
    expect(reply.querySelector("pre br")).toBeNull();
  });

  it("as they are: the transcript's words, no tags, no reply block", async () => {
    setBotmuxMode("all");
    const html = await render();
    expect(html).not.toContain("data-botmux-message");
    expect(html).not.toContain("data-botmux-reply");
    expect(html).toContain("user_message");
  });

  // A Codex botmux turn that answered with a card (`botmux send --card-file … --response-kind final`) and ended on
  // BotMux's control line: the reply carries no words when BotMux's record has none.
  const card = (text: string, last = "BOTMUX_NOTHING_TO_SEND"): Item[] => [
    items[0],
    { ...items[1], tool_input: { command: "botmux send --card-file /tmp/card.json --response-kind final" }, botmux: { kind: "reply", text, final: true } },
    { ...items[2], text: "{\"success\":true,\"messageId\":\"om_example_card\"}" },
    { id: "m1#0", seq: 4, role: "assistant", kind: "text", text: last },
  ];

  it("decoded: a card reply without words reads as sent, and the bare sentinel is not drawn", async () => {
    const html = await render(card(""));
    expect(html).toContain("data-botmux-reply");
    expect(html).toContain("飞书最终回复");
    expect(html).toContain("data-botmux-no-text");
    expect(html).toContain("已发送非文字内容（如卡片），请在飞书中查看。");
    expect(html).not.toContain("BOTMUX_NOTHING_TO_SEND");
    expect(html).not.toContain("data-latest-text");
  });

  it("decoded: a card reply with words draws them, without the sentinel", async () => {
    const html = await render(card("Cart fields: **extra_interactive_area**"));
    expect(html).toContain("<strong>extra_interactive_area</strong>");
    expect(html).not.toContain("data-botmux-no-text");
    expect(html).not.toContain("BOTMUX_NOTHING_TO_SEND");
  });

  it("decoded: last words beside the sentinel stay as written", async () => {
    const html = await render(card("", "Sent the card.\nBOTMUX_NOTHING_TO_SEND"));
    expect(html).toContain("data-latest-text");
    expect(html).toContain("Sent the card.");
    expect(html).toContain("BOTMUX_NOTHING_TO_SEND");
  });

  it("raw: the sentinel and the transcript stay whole, no reply block", async () => {
    setBotmuxMode("all");
    const html = await render(card(""));
    expect(html).toContain("data-latest-text");
    expect(html).toContain("BOTMUX_NOTHING_TO_SEND");
    expect(html).not.toContain("data-botmux-reply");
  });

  it("another session's bare sentinel is not hidden", async () => {
    const html = await render([{ id: "u9#0", seq: 1, role: "user", kind: "text", text: "explain the marker" }, { id: "m9#0", seq: 2, role: "assistant", kind: "text", text: "BOTMUX_NOTHING_TO_SEND" }]);
    expect(html).toContain("BOTMUX_NOTHING_TO_SEND");
  });

  it("shows a one-line composer notice when botmux falls back to external-event trigger", async () => {
    const host = document.createElement("div");
    const root = createRoot(host);
    const session = { machine_id: "m", session_id: "s", title: "t", cwd: "/tmp", status: "idle", botmux_external_trigger: true } as Session;
    await act(async () => root.render(<AgentStatusLine session={session} />));
    expect(host.innerHTML).toContain('data-testid="botmux-external-trigger-note"');
    expect(host.textContent).toContain("3.59.23");
    act(() => root.unmount());
  });

  it("suppresses errored botmux send replies and renders unknown trigger source generically", async () => {
    const erroredItems: Item[] = [
      { id: "u2#0", seq: 1, role: "user", kind: "text", text: "raw", botmux: { kind: "trigger", text: "cron tick", mentions: ["Alice", "Alice"] } },
      { id: "a2#0", seq: 2, role: "assistant", kind: "tool_use", tool_name: "Bash", tool_use_id: "t2", tool_input: { command: "botmux send 'fail'" }, botmux: { kind: "reply", text: "fail" } },
      { id: "r2#0", seq: 3, role: "user", kind: "tool_result", tool_use_id: "t2", is_error: true, text: "error" },
    ];
    const host = document.createElement("div");
    const root = createRoot(host);
    await act(async () => root.render(<SegmentView seg={segments(erroredItems)[0]} live={false} mid="m" sid="s" expandFold={async () => {}} />));
    await act(async () => { await import("./BotmuxMessages"); await new Promise((r) => setTimeout(r, 0)); });
    const html = host.innerHTML;
    expect(html).toContain("外部触发");
    expect(html).not.toContain("从 Persona 发送");
    expect(html).not.toContain("data-botmux-reply");
    act(() => root.unmount());
  });
});
