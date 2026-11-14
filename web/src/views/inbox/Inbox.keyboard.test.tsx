// @vitest-environment jsdom
// The Inbox by keyboard. Audit item 71: Select and Done are one button, so the focus stays on it as it turns, and comes
// back to it when selecting ends from elsewhere (Esc on a row's tick). Audit item 74: on a narrow page the opened item has
// the list's place, so the focus that went with the row moves to the item; ‹, Esc or Back returns it to that row, and
// Esc goes back only there (a wide page has the list beside the item).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});

const calls = vi.hoisted(() => ({ items: [] as unknown[] }));
vi.mock("../../lib/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/api")>()),
  api: (path: string) => {
    if (path.startsWith("/inbox?")) return Promise.resolve({ items: calls.items });
    if (path === "/mail/accounts") return Promise.resolve({ accounts: [] });
    return Promise.resolve({});
  },
}));
vi.mock("../../lib/store", async (orig) => {
  const m = await orig<typeof import("../../lib/store")>();
  return { ...m, useStore: <T,>(sel: (s: ReturnType<typeof m.getState>) => T) => m.useStore((s) => sel({ ...s, loaded: true })) };
});
vi.mock("../../lib/extension-slots", () => ({ useInboxItemPlace: () => undefined }));
vi.mock("../../lib/translate", () => ({ useTranslatePrefs: () => ({ enabled: false }), hubTranslation: () => undefined }));
vi.mock("./LarkThread", () => ({ LarkThread: () => null, chatOfItem: () => ({}) }));

import { resetInboxForTests, type InboxItem } from "../../lib/inbox";
import { settleRosterDefault } from "../../lib/manager-choice";
import { t } from "../../i18n";
import { Inbox } from "./Inbox";

settleRosterDefault();
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const item = (id: string): InboxItem => ({
  id, source: "lark", kind: "group", account: "feishu", account_name: "Lark", title: `chat ${id}`, preview: "text", count: 1,
  updated_at: "2026-11-09T04:00:00Z", tier: "action", version: `v-${id}`, lark: { feed_id: id, is_group: true },
});

let host: HTMLDivElement;
let root: Root;
// jsdom lays nothing out: what the page draws is what this says (the list hidden while an item has its place: narrow)
let drawn: (el: Element) => boolean;
const flush = async () => {
  for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); });
};
const render = async (id?: string) => {
  act(() => root.render(<Inbox id={id} onToggleSidebar={() => {}} />));
  await flush();
};
const button = (text: string) => [...host.querySelectorAll("button")].find((b) => b.textContent?.trim() === text);
const rowLink = (id: string) => host.querySelector<HTMLAnchorElement>(`[data-item="${id}"] > a`)!;
const list = () => host.querySelector<HTMLElement>(`section[aria-label="${t("inbox-inbox.list")}"]`)!;
const detail = () => host.querySelector<HTMLElement>(`section[aria-label="${t("inbox-inbox.detail")}"]`)!;
const escape = (target: EventTarget = document.activeElement ?? window) =>
  act(() => { target.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })); });
const popped = (go: () => void) =>
  new Promise<void>((done, fail) => {
    const timer = setTimeout(() => fail(new Error("no Back happened")), 1_000);
    addEventListener("popstate", () => (clearTimeout(timer), done()), { once: true });
    go();
  });

beforeEach(async () => {
  calls.items = [item("a1"), item("a2")];
  resetInboxForTests();
  vi.stubGlobal("CSS", { escape: (v: string) => v });
  Element.prototype.scrollIntoView = vi.fn();
  drawn = () => true;
  Object.defineProperty(Element.prototype, "checkVisibility", { configurable: true, value(this: Element) { return drawn(this); } });
  history.pushState(null, "", "/inbox");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  await render();
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
  delete (Element.prototype as { checkVisibility?: unknown }).checkVisibility;
  history.replaceState(null, "", "/");
});

describe("Select / Done keep the focus (audit item 71)", () => {
  it("stays on the one button as Select turns into Done and back", async () => {
    const select = button("选择")!;
    act(() => select.focus());
    await act(async () => select.click());
    await flush();
    expect(document.activeElement?.textContent?.trim()).toBe("完成");
    expect(document.activeElement).toBe(select);
    await act(async () => (document.activeElement as HTMLButtonElement).click());
    await flush();
    expect(document.activeElement?.textContent?.trim()).toBe("选择");
  });

  it("comes back to Select when Esc on a row's tick ends selecting", async () => {
    await act(async () => button("选择")!.click());
    await flush();
    const tick = host.querySelector<HTMLElement>('[data-item="a1"] [role="checkbox"]')!;
    act(() => tick.focus());
    await escape(tick);
    await flush();
    expect(host.querySelector('[role="checkbox"]')).toBeNull();
    expect(document.activeElement?.textContent?.trim()).toBe("选择");
  });
});

describe("the opened item and its row by keyboard on a narrow page (audit item 74)", () => {
  it("moves the focus to the item, and Esc brings it back to the row it was opened from", async () => {
    // narrow: the list is hidden while an item is open, and what was in it with it
    drawn = (el) => !(location.pathname !== "/inbox" && list().contains(el));
    const link = rowLink("a2");
    act(() => link.focus());
    act(() => link.click());
    await render("a2");
    expect(document.activeElement).toBe(detail());

    await popped(() => void escape());
    expect(location.pathname).toBe("/inbox");
    drawn = (el) => !detail().contains(el); // the list has its place back; the item's pane is hidden
    await render();
    expect(document.activeElement).toBe(rowLink("a2"));
  });

  it("leaves Esc to a field, and to a wide page, where the list is beside the item", async () => {
    act(() => rowLink("a1").click());
    await render("a1");
    // wide: the list is drawn beside the item, so Esc does not leave it
    await escape(window);
    expect(location.pathname).toBe("/inbox/a1");
    // narrow, but typing in a field: Esc is the field's
    drawn = (el) => !list().contains(el) && el !== list();
    const field = document.createElement("textarea");
    detail().append(field);
    act(() => field.focus());
    await escape(field);
    expect(location.pathname).toBe("/inbox/a1");
    field.remove();
  });

  it("keeps a focus still in sight: a wide page's row stays focused when it opens its item", async () => {
    const link = rowLink("a1");
    act(() => link.focus());
    act(() => link.click());
    await render("a1");
    expect(document.activeElement).toBe(link);
  });
});
