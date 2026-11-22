// @vitest-environment jsdom
// A bubble measures its height (a forced layout: its line height, its scroll height, its fold's scroll height) only
// when its text could run past BUBBLE_MAX_LINES in some bubble: opening a long chat measured every question's bubble.
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { segments } from "../../lib/turns";
import { UserBubble } from "./SegmentView";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
afterEach(() => vi.restoreAllMocks());

it.each([
  ["a short question", "看一下 CI 为什么红了", false],
  ["a long one", "一行很长的话".repeat(30), true],
])("%s", (_, text, measured) => {
  const read = vi.spyOn(window, "getComputedStyle");
  const height = vi.spyOn(Element.prototype, "scrollHeight", "get");
  const host = document.createElement("div");
  const root = createRoot(host);
  act(() => root.render(<UserBubble msg={segments([{ id: "u1", seq: 1, role: "user", kind: "text", text }])[0].user!} mid="m" sid="s" />));
  const bubble = (el: unknown) => (el as Element).hasAttribute("data-bubble-text");
  expect(read.mock.calls.some(([el]) => bubble(el)) || height.mock.contexts.some(bubble)).toBe(measured);
  act(() => root.unmount());
});
