import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { clock } from "../../lib/format";
import { segments } from "../../lib/turns";
import type { Item } from "../../lib/types";
import { LatestMessageTime } from "./latest-message-time";
import { MessageActions } from "./MessageActions";
import { SegmentView } from "./SegmentView";

// A message's send time (#5285): a hover shows it for any message, and the chat's last message keeps it on screen.
const ts = new Date().toISOString();
const earlier = new Date(Date.now() - 60_000).toISOString();

const reply = (latest?: string) =>
  renderToStaticMarkup(
    <LatestMessageTime.Provider value={latest}>
      <MessageActions text="回复" ts={ts} itemId="u1#1">
        <p>回复</p>
      </MessageActions>
    </LatestMessageTime.Provider>,
  );

const own = (latest?: string) =>
  renderToStaticMarkup(
    <LatestMessageTime.Provider value={latest}>
      <MessageActions text="我的消息" isUser ts={ts} side messageId="u1">
        <p>我的消息</p>
      </MessageActions>
    </LatestMessageTime.Provider>,
  );

const count = (html: string, s: string) => html.split(s).length - 1;

describe("message send time", () => {
  it("an earlier reply shows its time only in the hover bar", () => {
    const html = reply(earlier);
    expect(html).not.toContain("data-latest-time");
    expect(count(html, `>${clock(ts)}<`)).toBe(1);
    expect(html).toContain("opacity-0");
  });

  it("the last reply keeps its time outside the hover bar, once", () => {
    const html = reply(ts);
    expect(html).toContain("data-latest-time");
    expect(count(html, `>${clock(ts)}<`)).toBe(1);
  });

  it("your own message shows its time in the side hover bar", () => {
    const html = own(earlier);
    expect(count(html, `>${clock(ts)}<`)).toBe(1);
    expect(html).not.toContain("data-latest-time");
  });

  it("your own last message leaves the time to the row under the bubble", () => {
    expect(count(own(ts), `>${clock(ts)}<`)).toBe(0);
    const item: Item = { id: "u1#0", seq: 1, role: "user", kind: "text", text: "看一下 CI", client_msg_id: "c1", ts };
    const seg = segments([item])[0];
    const view = (latest?: string) =>
      renderToStaticMarkup(
        <LatestMessageTime.Provider value={latest}>
          <SegmentView seg={seg} live={false} mid="m" sid="s" expandFold={async () => {}} />
        </LatestMessageTime.Provider>,
      );
    expect(view(ts)).toContain("data-latest-time");
    expect(view(earlier)).not.toContain("data-latest-time");
  });

  it("outside a chat transcript nothing is pinned", () => {
    expect(reply(undefined)).not.toContain("data-latest-time");
  });
});
