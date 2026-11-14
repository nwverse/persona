// @vitest-environment jsdom
// An empty kind says where its entries come from, in plain words: recordings from the phone, events from a connected
// calendar, the archived kinds from Lark and email chats; never "the hub" or "compacting".
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import en from "../../i18n/locales/en/knowledge-kblist.json";
import ja from "../../i18n/locales/ja/knowledge-kblist.json";
import zh from "../../i18n/locales/zh-Hans/knowledge-kblist.json";
import type { KBKind } from "../../lib/kb";
import { lists, searches } from "./cache";
import { ListPane } from "./KBList";

vi.mock("../../lib/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/api")>()),
  api: () => Promise.resolve({ items: [], tags: [] }),
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
const emptyText = async (kind: KBKind) => {
  await act(async () => root.render(<ListPane kind={kind} detailOpen={false} reloadKey={0} onToggleSidebar={() => {}} onOpen={() => {}} />));
  return [...host.querySelectorAll("p")].map((p) => p.textContent ?? "");
};

beforeEach(() => {
  lists.clear();
  searches.clear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  lists.clear();
  searches.clear();
});

describe("an empty kind", () => {
  it("says recordings come from the phone", async () => {
    const [title, body] = await emptyText("recordings");
    expect(title).toBe("还没有录音");
    expect(body).toBe(zh.empty_recordings_body);
    expect(body).toContain("手机");
  });

  it("says events come from a connected calendar", async () => {
    const [title, body] = await emptyText("events");
    expect(title).toBe("还没有日程");
    expect(body).toBe(zh.empty_events_body);
    expect(body).toContain("日历");
  });

  it("keeps the archived chats' sentence for the kinds that come from them", async () => {
    const [, body] = await emptyText("docs");
    expect(body).toBe(zh.empty_body);
    expect(body).not.toBe(zh.empty_recordings_body);
  });

  it("uses no engineering words in any language", () => {
    for (const catalog of [en, zh, ja]) {
      for (const text of [catalog.empty_body, catalog.empty_events_body, catalog.empty_recordings_body]) {
        expect(text).not.toMatch(/\bhub\b|ハブ|compact|压缩|圧縮/i);
      }
    }
  });
});
