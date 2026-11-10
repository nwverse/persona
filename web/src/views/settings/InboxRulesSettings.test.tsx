// @vitest-environment jsdom
// Settings › Inbox rules (docs/assistant/inbox.md "Corrections and learned rules"): the rules one a line, edited, removed
// and added, saved whole; a read that fails shows no list to save over the hub's, and a failed save puts the list back.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

type Pending = { path: string; method: string; json?: unknown; resolve: (v: unknown) => void; reject: (e: unknown) => void };
const pending: Pending[] = [];
vi.mock("../../lib/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/api")>()),
  api: (path: string, init?: { method?: string; json?: unknown }) =>
    new Promise((resolve, reject) => pending.push({ path, method: init?.method ?? "GET", json: init?.json, resolve, reject })),
}));
const toast = vi.fn();
vi.mock("../../ui/atoms", async (orig) => ({ ...(await orig<typeof import("../../ui/atoms")>()), toast: (...a: unknown[]) => toast(...a) }));

const { InboxRulesSettings } = await import("./InboxRulesSettings");

let root: Root;
let host: HTMLDivElement;
beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  pending.length = 0;
  toast.mockClear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const render = () => act(async () => root.render(<InboxRulesSettings />));
const rules = () => [...host.querySelectorAll("[data-rule] p")].map((p) => p.textContent);
const button = (label: string) => [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.getAttribute("aria-label") === label || b.textContent === label);
const answer = async (i: number, v: unknown) => act(async () => pending[i].resolve(v));
const click = async (b: HTMLButtonElement | undefined) => act(async () => b!.click());
const type = async (text: string) => act(async () => {
  const input = host.querySelector("input")!;
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, text);
  input.dispatchEvent(new Event("input", { bubbles: true }));
});

describe("设置 › 收件箱规则", () => {
  it("lists the rules; edits, removes and adds one, saving the whole list", async () => {
    await render();
    expect(pending[0].path).toBe("/inbox/rules");
    await answer(0, { rules: ["发版通知算知悉即可", "示例商城的邮件直接折叠"] });
    expect(rules()).toEqual(["发版通知算知悉即可", "示例商城的邮件直接折叠"]);

    await click(button("编辑"));
    await type("发版通知需要我");
    await act(async () => host.querySelector("form")!.requestSubmit());
    expect([pending[1].method, pending[1].json]).toEqual(["PUT", { rules: ["发版通知需要我", "示例商城的邮件直接折叠"] }]);
    await answer(1, { rules: ["发版通知需要我", "示例商城的邮件直接折叠"] });

    await click([...host.querySelectorAll<HTMLButtonElement>("button")].filter((b) => b.getAttribute("aria-label") === "删除")[1]);
    expect(pending[2].json).toEqual({ rules: ["发版通知需要我"] });
    await answer(2, { rules: ["发版通知需要我"] });

    await click(button("添加规则"));
    await type("周报邮件算知悉即可");
    await act(async () => host.querySelector("form")!.requestSubmit());
    expect(pending[3].json).toEqual({ rules: ["发版通知需要我", "周报邮件算知悉即可"] });
    await act(async () => pending[3].reject(new Error("boom")));
    expect(rules()).toEqual(["发版通知需要我"]);
    expect(toast.mock.lastCall?.[0]).toMatch(/^规则没有保存/);
  });

  it("a failed read shows no list, only why and Retry", async () => {
    await render();
    await act(async () => pending[0].reject(new Error("offline")));
    expect(rules()).toEqual([]);
    expect(button("添加规则")).toBeUndefined();
    await click(button("重试"));
    expect(pending).toHaveLength(2);
  });
});
