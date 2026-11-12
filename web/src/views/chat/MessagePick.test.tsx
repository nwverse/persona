// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import { FORWARD_MOST, type ForwardPick } from "../../lib/forward";
import { messagePick, type MessagePick } from "../../lib/message-pick";
import { ChatActionsContext, type ChatActions } from "./chat-actions";
import { MessageActions } from "./MessageActions";
import { PickBar } from "./PickBar";

// Picking messages to forward at once (#3838): each message with a Forward is a checkbox while the chat picks (its
// actions off), the bar counts them, Forward is off with none, and Cancel or Escape leave the chat as it was.

const toast = vi.hoisted(() => vi.fn());
vi.mock("../../ui/atoms", async (original) => ({ ...(await original<typeof import("../../ui/atoms")>()), toast }));
vi.hoisted(() => {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia;
});

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
let pick: MessagePick;
const chat = (): ChatActions => ({ draftKey: "m/s", canCompose: true, speaker: "Agent", title: "Network tests", forward: () => {}, pick, images: () => [] });

beforeEach(async () => {
  await initI18n("en");
  toast.mockClear();
  pick = messagePick();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  act(() => root.unmount());
  host.remove();
  await initI18n("zh-Hans");
});

const p = (itemId: string): ForwardPick => ({ itemId, text: `words of ${itemId}`, isUser: false });

function renderChat(withBar = false, onForward: (picks: ForwardPick[]) => void = () => {}) {
  act(() =>
    root.render(
      <ChatActionsContext.Provider value={chat()}>
        <MessageActions text="How many times does it retry?" isUser side messageId="u#1">
          <p>How many times does it retry?</p>
        </MessageActions>
        <MessageActions text="Three times." itemId="r#2">
          <a href="#somewhere">Three times.</a>
        </MessageActions>
        <MessageActions text="Still streaming">
          <p>Still streaming</p>
        </MessageActions>
        {withBar && <PickBar pick={pick} items={() => [{ id: "u#1" }, { id: "r#2" }]} onForward={onForward} />}
      </ChatActionsContext.Provider>,
    ),
  );
}

const boxes = () => [...host.querySelectorAll<HTMLElement>('[role="checkbox"]')];

describe("picking messages in a chat", () => {
  it("draws nothing new until picking starts", () => {
    renderChat();
    expect(boxes()).toHaveLength(0);
    expect(host.querySelector("[data-pick]")).toBeNull();
  });

  it("makes each message with a Forward a checkbox, the first one picked, and a click picks or unpicks it", () => {
    renderChat();
    act(() => pick.start(p("u#1")));
    // the streaming reply has no Forward, so it is not one to pick
    expect(boxes().map((b) => [b.getAttribute("aria-checked"), b.dataset.pick])).toEqual([
      ["true", "on"],
      ["false", "off"],
    ]);
    act(() => boxes()[1].click());
    expect(pick.picks().map((x) => x.itemId)).toEqual(["u#1", "r#2"]);
    expect(boxes()[1].getAttribute("aria-checked")).toBe("true");
    act(() => boxes()[0].click());
    expect(pick.picks().map((x) => x.itemId)).toEqual(["r#2"]);
    // what is in a message does not take the click while picking (a link in a reply)
    expect(host.querySelector("a")?.closest("[inert]")).not.toBeNull();
    // the space bar picks too
    act(() => boxes()[0].dispatchEvent(new KeyboardEvent("keydown", { key: " ", bubbles: true })));
    expect(pick.has("u#1")).toBe(true);
  });

  it("opens no menu while picking", () => {
    renderChat();
    const rightClick = () => act(() => host.querySelector("p")!.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 4, clientY: 4 })));
    rightClick();
    expect(document.querySelector('[role="menu"]')).not.toBeNull(); // as it does when not picking
    act(() => document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    act(() => pick.start(p("u#1")));
    rightClick();
    expect(document.querySelector('[role="menu"]')).toBeNull();
  });

  it(`refuses more than ${FORWARD_MOST}, the bar saying so`, () => {
    renderChat(true);
    act(() => pick.start(p("0")));
    act(() => {
      for (let i = 1; i < FORWARD_MOST; i++) pick.toggle(p(`x${i}`));
    });
    act(() => boxes()[1].click());
    expect(pick.has("r#2")).toBe(false);
    expect(toast).toHaveBeenCalledWith(`Up to ${FORWARD_MOST} messages at a time`, "error");
  });

  it("ends as it was: no checkbox once picking stops", () => {
    renderChat();
    act(() => pick.start(p("u#1")));
    act(() => pick.end());
    expect(boxes()).toHaveLength(0);
  });
});

describe("the pick bar", () => {
  const button = (id: string) => host.querySelector<HTMLButtonElement>(`[data-testid="${id}"]`)!;

  it("counts the picked messages and forwards them; Forward is off with none", () => {
    const onForward = vi.fn();
    renderChat(true, onForward);
    act(() => pick.start(p("u#1")));
    expect(host.querySelector("[data-pick-count]")?.textContent).toBe("1 selected");
    expect(button("pick-forward").textContent).toContain("Forward 1");
    act(() => boxes()[1].click());
    expect(host.querySelector("[data-pick-count]")?.textContent).toBe("2 selected");
    act(() => button("pick-forward").click());
    // in the chat's order
    expect(onForward.mock.calls[0][0].map((x: ForwardPick) => x.itemId)).toEqual(["u#1", "r#2"]);
    act(() => boxes()[0].click());
    act(() => boxes()[1].click());
    expect(button("pick-forward").disabled).toBe(true);
  });

  it.each([
    ["zh-Hans", "已选 2 条", "转发 2 条"],
    ["ja", "2件を選択", "2件を転送"],
  ])("speaks %s", async (lang, count, forward) => {
    await initI18n(lang);
    renderChat(true);
    act(() => pick.start(p("u#1")));
    act(() => boxes()[1].click());
    expect(host.querySelector("[data-pick-count]")?.textContent).toBe(count);
    expect(button("pick-forward").textContent).toContain(forward);
  });

  it("ends picking with Cancel or Escape", () => {
    renderChat(true);
    act(() => pick.start(p("u#1")));
    act(() => button("pick-cancel").click());
    expect(pick.on()).toBe(false);
    act(() => pick.start(p("u#1")));
    act(() => window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })));
    expect(pick.on()).toBe(false);
  });
});
