// @vitest-environment jsdom
// cindy's bar under a turn's last reply (#5314): shown by the pointer or keyboard focus, lingering after the pointer
// leaves, kept by an open ⋯; Copy message turns into ✓ until the next hover; the time only ticks under the pointer.
// SegmentView gives it to a finished turn's last reply only: never narration in a work summary, nor words still coming.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import type { Segment } from "../../lib/turns";
import type { Item } from "../../lib/types";
import { REPLY_BAR_LINGER_MS } from "../../ui/motion";
import { ChatActionsContext, type ChatActions } from "./chat-actions";
import { MessageActions } from "./MessageActions";
import { Answer, SegmentView } from "./SegmentView";
import { StepView, type WorkProps } from "./WorkGroup";

const copied = vi.hoisted(() => vi.fn(async (..._: unknown[]) => true));
vi.mock("../../ui/atoms", async (original) => ({ ...(await original<typeof import("../../ui/atoms")>()), copyText: copied }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const chat: ChatActions = { draftKey: "m/s", canCompose: true, speaker: "Agent", title: "Fix login", forward: () => {}, fork: () => {}, images: () => [] };
let host: HTMLDivElement;
let root: Root;

beforeEach(async () => {
  await initI18n("zh-Hans");
  copied.mockClear();
  vi.useFakeTimers({ now: new Date("2026-11-12T10:00:00Z") });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.useRealTimers();
});

/** The reply with the bar, once its code is in (it loads after the first screen). */
async function draw({ actions = chat, itemId = "r1#0" as string | null } = {}) {
  await act(async () => root.render(
    <ChatActionsContext.Provider value={actions}>
      <MessageActions text="All done." ts="2026-11-12T09:59:01Z" itemId={itemId ?? undefined} bar>
        <p data-message>All done.</p>
      </MessageActions>
    </ChatActionsContext.Provider>,
  ));
  // React tries the lazy bar again on a timer, which the fake clock holds: let it run
  await act(async () => {
    await vi.dynamicImportSettled();
    await vi.advanceTimersByTimeAsync(0);
  });
  expect(host.querySelector('[data-reply-bar="loading"]')).toBeNull();
}

const bar = () => host.querySelector<HTMLElement>("[data-reply-bar]")!;
const shown = () => bar().classList.contains("opacity-100");
const button = (label: string) => host.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`)!;
const labels = () => [...host.querySelectorAll("[data-reply-bar] button")].map((b) => b.getAttribute("aria-label"));
// the bar watches the reply it is in (MessageActions' element)
const enter = () => act(() => void bar().parentElement!.dispatchEvent(new MouseEvent("mouseenter")));
const leave = () => act(() => void bar().parentElement!.dispatchEvent(new MouseEvent("mouseleave")));
const wait = (ms: number) => act(() => void vi.advanceTimersByTime(ms));

describe("the reply bar", () => {
  it("is in cindy's order: Copy message, Start a new session, More message actions, then the time", async () => {
    await draw();
    expect(labels()).toEqual(["复制消息", "开启一个新会话", "更多消息操作"]);
    expect(bar().lastElementChild?.tagName).toBe("TIME");
  });

  it("says chat, not session, in a Persona conversation", async () => {
    await draw({ actions: { ...chat, assistant: true } });
    expect(labels()).toEqual(["复制消息", "开启一个新对话", "更多消息操作"]);
  });

  it("has no Start a new session where the session cannot branch, or for words that are not a line of their own", async () => {
    await draw({ actions: { ...chat, fork: undefined } });
    expect(labels()).toEqual(["复制消息", "更多消息操作"]);
    await draw({ itemId: "line12#0" });
    expect(labels()).toEqual(["复制消息", "更多消息操作"]);
  });

  it("has no ⋯ with nothing to put in it (a chat that cannot send, words not in the transcript)", async () => {
    await draw({ actions: { ...chat, canCompose: false }, itemId: null });
    expect(labels()).toEqual(["复制消息"]);
  });

  it("shows on hover, stays a moment after the pointer leaves, and a quick way back keeps it", async () => {
    await draw();
    expect(shown()).toBe(false);
    expect(bar().className).toContain("has-[:focus-visible]:opacity-100");
    enter();
    expect(shown()).toBe(true);
    leave();
    wait(REPLY_BAR_LINGER_MS - 50);
    expect(shown()).toBe(true);
    enter();
    wait(REPLY_BAR_LINGER_MS);
    expect(shown()).toBe(true);
    leave();
    wait(REPLY_BAR_LINGER_MS);
    expect(shown()).toBe(false);
    expect(bar().className).toContain("pointer-events-none");
  });

  it("stays while its ⋯ menu is open, which holds Quote, Forward and Use as context", async () => {
    await draw();
    enter();
    act(() => button("更多消息操作").click());
    expect([...document.querySelectorAll('[role="menuitem"]')].map((item) => item.textContent?.trim())).toEqual(["引用", "转发", "作为上下文"]);
    leave();
    wait(REPLY_BAR_LINGER_MS * 2);
    expect(shown()).toBe(true);
    act(() => button("更多消息操作").click());
    expect(shown()).toBe(false);
  });

  it("copies without a toast and shows ✓ until the pointer comes back", async () => {
    const writeText = vi.fn(async () => {});
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
    try {
      await draw();
      enter();
      await act(async () => button("复制消息").click());
      expect(writeText).toHaveBeenCalledExactlyOnceWith("All done.");
      expect(copied).not.toHaveBeenCalled(); // copyText, which says "Copied"
      const check = () => button("复制消息").innerHTML.includes("M20 6L9 17l-5-5");
      expect(check()).toBe(true);
      leave();
      wait(REPLY_BAR_LINGER_MS * 2);
      expect(check()).toBe(true); // it fades out as it was
      enter();
      expect(check()).toBe(false);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("copies the old way, which says so, without the async clipboard (plain http)", async () => {
    await draw();
    enter();
    await act(async () => button("复制消息").click());
    expect(copied).toHaveBeenCalledExactlyOnceWith("All done.");
    expect(button("复制消息").innerHTML).toContain("M20 6L9 17l-5-5");
  });

  it("says when, the exact time as its tooltip, and keeps no timer while the pointer is elsewhere", async () => {
    await draw();
    const time = () => host.querySelector("time")!;
    expect(time().textContent).toBe("刚刚");
    expect(time().title).toMatch(/^2026-11-12 \d\d:59:01$/);
    wait(REPLY_BAR_LINGER_MS);
    expect(vi.getTimerCount()).toBe(0);
    wait(5 * 60_000);
    expect(time().textContent).toBe("刚刚");
    enter();
    expect(time().textContent).toBe("5 分钟前");
    wait(60_000);
    expect(time().textContent).toBe("6 分钟前");
    leave();
    wait(REPLY_BAR_LINGER_MS);
    expect(vi.getTimerCount()).toBe(0);
  });
});

const T = "2026-11-12T09:00:00Z";
const text = (id: string, seq: number, words: string): Item => ({ id, seq, ts: T, role: "assistant", kind: "text", text: words });
const exchange = (extra: Partial<Segment> = {}): Segment => ({
  id: "u1#0",
  user: { id: "u1#0", text: "Look at it", images: [], contexts: [], ts: T },
  steps: [{ kind: "text", id: "a2#0", item: text("a2#0", 2, "Let me look.") }, { kind: "tool", id: "t3#0", use: { id: "t3#0", seq: 3, role: "assistant", kind: "tool_use", tool_use_id: "c3", tool_name: "Read", tool_input: { file_path: "/a.ts" }, ts: T }, images: [] }],
  answer: text("a4#0", 4, "It is fine."),
  tools: 1,
  thoughts: 0,
  replies: 1,
  folds: [],
  start: T,
  ...extra,
});
const segment = (seg: Segment, live = false, streaming?: string) =>
  renderToStaticMarkup(
    <ChatActionsContext.Provider value={chat}>
      <SegmentView seg={seg} live={live} streaming={streaming} mid="m" sid="s" expandFold={async () => {}} />
    </ChatActionsContext.Provider>,
  );
const bars = (html: string) => html.split("data-reply-bar").length - 1;

describe("which replies have the bar", () => {
  it("a finished turn's last reply, under its words", () => {
    const html = segment(exchange());
    expect(bars(html)).toBe(1);
    expect(html.indexOf("It is fine.")).toBeLessThan(html.indexOf("data-reply-bar"));
  });

  it("not narration inside an opened work summary", () => {
    const props: WorkProps = { mid: "m", sid: "s", scope: "t:", expandFold: async () => {}, agentStatus: () => "completed" };
    const html = renderToStaticMarkup(
      <ChatActionsContext.Provider value={chat}>
        <StepView step={{ kind: "text", id: "a2#0", item: text("a2#0", 2, "Let me look.") }} p={props} />
      </ChatActionsContext.Provider>,
    );
    expect(html).toContain("Let me look.");
    expect(html).toContain("group/msg"); // its right-click menu is still there
    expect(bars(html)).toBe(0);
  });

  it("not while the turn runs, nor words still streaming", () => {
    expect(bars(segment(exchange(), true))).toBe(0);
    expect(bars(segment(exchange({ answer: undefined }), true, "Still writ"))).toBe(0);
    expect(bars(renderToStaticMarkup(<Answer streaming="Still writ" mid="m" sid="s" />))).toBe(0);
  });

  it("a reply cut short says so above its words, once the turn is over", () => {
    const html = segment(exchange({ cut: true }));
    expect(html).toContain(">此回复未完成，内容仅供参考。<");
    expect(html.indexOf("此回复未完成")).toBeLessThan(html.indexOf("It is fine."));
    expect(segment(exchange())).not.toContain("此回复未完成");
    expect(segment(exchange({ cut: true }), true)).not.toContain("此回复未完成");
  });
});
