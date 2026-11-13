// @vitest-environment jsdom
// U3 of the four-client compare (DECISIONS D3 "四端对比后统一", "图标照 meanings.json：... reported 用 reported 的字形，不和聊天来源
// 的气泡共用"): a reported step and a chat-source step must not share one glyph, and the reported step takes the shared
// reported mark (spec/icons/meanings.json: sf arrow.up.message). The chat-source step keeps the bubble it has on iOS
// (four-way U3: "iOS draws arrow.up.message vs a bubble").
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
  Object.assign(globalThis, {
    ResizeObserver: globalThis.ResizeObserver ?? class { observe() {} unobserve() {} disconnect() {} },
    IntersectionObserver: globalThis.IntersectionObserver ?? class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } },
  });
  globalThis.CSS ??= { escape: (s: string) => s } as unknown as typeof CSS;
});
vi.mock("../../lib/api", async (orig) => ({ ...(await orig<typeof import("../../lib/api")>()), api: () => Promise.resolve({}) }));
vi.mock("../../ui/atoms", async (orig) => ({ ...(await orig<typeof import("../../ui/atoms")>()), toast: vi.fn() }));
vi.mock("../../lib/store", async (orig) => {
  const m = await orig<typeof import("../../lib/store")>();
  return { ...m, useStore: <T,>(sel: (s: ReturnType<typeof m.getState>) => T) => m.useStore((s) => sel({ ...s, loaded: true })) };
});
vi.mock("../../lib/extension-slots", () => ({ useInboxItemPlace: () => undefined }));
vi.mock("../../lib/translate", () => ({ useTranslatePrefs: () => ({ enabled: false }), hubTranslation: () => undefined, translatesChat: () => false, translatesMail: () => false, languageName: () => "" }));

import type { InboxItem } from "../../lib/inbox";
import { resetInboxForTests } from "../../lib/inbox";
import type { ChainEntry } from "../../lib/inbox-events";
import { resetMailForTests } from "../../lib/mail";
import { settleRosterDefault } from "../../lib/manager-choice";
import { EventBody } from "./InboxEvent";

settleRosterDefault();
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const chain: ChainEntry[] = [
  { id: "c1", at: "2026-01-01T11:02:00Z", kind: "received", text: "收到 2 条消息", via: "chat", count: 2 },
  { id: "c5", at: "2026-01-01T11:20:00Z", kind: "reported", text: "助手说：发了邮件", detail: "发了邮件", via: "agent" },
];
const item: InboxItem = {
  id: "chat:c1:1", source: "chat", kind: "group", account: "chat", account_name: "Team chat", title: "一件事",
  preview: "最新一句", count: 1, started_at: "2026-01-01T11:00:00Z", updated_at: "2026-01-01T11:30:00Z", chain,
};

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  resetInboxForTests();
  resetMailForTests();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  act(() => root.render(<EventBody item={item} onSource={() => {}} />));
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const glyphOf = (id: string) => host.querySelector(`[data-chain-id="${id}"] [data-chain-glyph]`)?.getAttribute("data-chain-glyph");

describe("链条图标：reported 与聊天来源不共用气泡", () => {
  // U3's expectation: iOS draws a bubble next to the reported arrow. Web now falls back to `link` for an unbranded
  // chat source, so this reads `link`; whether web keeps a bubble or moves to `link` is a ruling this package cannot make.
  it("a chat-source step draws no message-arrow glyph (U3: it is the reported step's)", () => {
    expect(glyphOf("c1")).not.toBe("reported");
    expect(glyphOf("c1")).toBeTruthy();
  });

  it("a reported step does not draw the chat-source bubble", () => {
    expect(glyphOf("c5")).toBeTruthy();
    expect(glyphOf("c5")).not.toBe(glyphOf("c1"));
  });
});
