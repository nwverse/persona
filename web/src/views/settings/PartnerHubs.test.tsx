// @vitest-environment jsdom
// Settings › This hub › Partner hubs (docs/server/partner-hubs.md): only on a hub with the partner_hubs feature; the list
// with each partner's state, its two switches (each showing what the hub answered, not what was asked) and Unpair confirmed
// in the row; the empty state; Show my code (countdown, fingerprint) and Pair… (a pasted code checked before it is sent,
// the hub's refusal in the dialog); and the partner_hubs push and an unresumed hello reading the list again.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

type Call = { path: string; method: string; json?: unknown; resolve: (v: unknown) => void; reject: (e: unknown) => void };
const calls: Call[] = [];
vi.mock("../../lib/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/api")>()),
  api: (path: string, init?: { method?: string; json?: unknown }) =>
    new Promise((resolve, reject) => calls.push({ path, method: init?.method ?? "GET", json: init?.json, resolve, reject })),
}));
const listeners = new Map<string, (m: Record<string, unknown>) => void>();
let features: string[] = ["partner_hubs"];
vi.mock("../../lib/store", async (orig) => {
  const real = await orig<typeof import("../../lib/store")>();
  return {
    ...real,
    useStore: <T,>(select: (s: ReturnType<typeof real.getState>) => T): T => select({ ...real.getState(), features }),
    onHubEvent: (type: string, l: (m: Record<string, unknown>) => void) => (listeners.set(type, l), () => listeners.delete(type)),
  };
});
const toast = vi.fn();
vi.mock("../../ui/atoms", async (original) => ({ ...(await original<typeof import("../../ui/atoms")>()), toast: (...args: unknown[]) => toast(...args) }));

const { ApiError, setToken } = await import("../../lib/api");
const { initI18n } = await import("../../i18n");
const { PartnerHubs } = await import("./PartnerHubs");

const FP = "Q2hlY2tUaGlzRmluZ2VycHJpbnRBZ2FpbnN0VGhlSHVi";
const CODE = `persona://partner?hub=hub-d&name=Studio&fp=${FP}&tls=18662&url=https%3A%2F%2Fhub.example.com%3A18662&code=jh_${"c0ffee00".repeat(8)}`;
const ago = (h: number) => new Date(Date.now() - h * 3600_000).toISOString();
const view = (extra: Record<string, unknown> = {}) => ({
  enabled: true, member: false, can_issue: true,
  hubs: [
    { hub_id: "hub-b", name: "家里", fp: FP, state: "connected", dial: "auto", since: ago(2), dialed_by: "us", created_at: ago(48) },
    { hub_id: "hub-c", name: "朋友的 hub", fp: FP, state: "refused", dial: "never", last_seen: ago(30), error: "对方不再认这台 hub 的凭据", created_at: ago(72) },
  ],
  ...extra,
});
const PREFS = { prefs: [{ hub_id: "hub-c", confirm: false }] };

let root: Root;
let host: HTMLDivElement;
beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  calls.length = 0;
  toast.mockClear();
  listeners.clear();
  features = ["partner_hubs"];
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  act(() => root.unmount());
  host.remove();
  setToken("");
  document.body.innerHTML = "";
  await initI18n("zh-Hans");
});

const render = () => act(async () => root.render(<PartnerHubs />));
const answer = (c: Call | undefined, v: unknown) => act(async () => c!.resolve(v));
const refuse = (c: Call | undefined, e: unknown) => act(async () => c!.reject(e));
const last = (method: string, path: string) => calls.filter((c) => c.method === method && c.path === path).at(-1);
const shown = async (list = view(), prefs: unknown = PREFS) => {
  await render();
  await answer(last("GET", "/partner-hubs"), list);
  await answer(last("GET", "/partner-messages/prefs"), prefs);
};
const text = () => document.body.textContent ?? "";
const button = (label: string) => [...document.body.querySelectorAll("button")].find((b) => b.textContent?.trim() === label);
const click = (el: Element | undefined) => act(async () => (el as HTMLElement).click());
const row = (id: string) => host.querySelector(`[data-partner-hub="${id}"]`)!;
const switches = (id: string) => [...row(id).querySelectorAll<HTMLButtonElement>('[role="switch"]')];
const checked = (el: HTMLButtonElement) => el.getAttribute("aria-checked") === "true";
const type = (value: string) =>
  act(async () => {
    const field = document.body.querySelector("textarea")!;
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(field, value);
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });

describe("设置 › 这台 hub › 协作 hub", () => {
  it("没有 partner_hubs 功能的 hub 上不出现，也不读任何东西", async () => {
    features = ["managers"];
    await render();
    expect(host.innerHTML).toBe("");
    expect(calls).toEqual([]);
  });

  it("列出每个协作 hub：状态、时间、出错原因和两个开关", async () => {
    await shown();
    expect(calls.map((c) => `${c.method} ${c.path}`)).toEqual(["GET /partner-hubs", "GET /partner-messages/prefs"]);
    expect(host.querySelector('[data-settings-group="partner-hubs"]')).not.toBeNull();
    expect(row("hub-b").textContent).toContain("家里");
    expect(row("hub-b").textContent).toContain("已连接");
    expect(row("hub-b").textContent).toContain("2小时前连上");
    expect(row("hub-c").textContent).toContain("连不上");
    expect(row("hub-c").textContent).toContain("对方不再认这台 hub 的凭据");
    // Ask me before sending: on without a stored value, as stored otherwise; Connect: dial auto / never
    expect(switches("hub-b").map(checked)).toEqual([true, true]);
    expect(switches("hub-c").map(checked)).toEqual([false, false]);
    expect(switches("hub-b")[0].getAttribute("aria-label")).toBe("发送前问我");
    expect(button("我的配对码")?.disabled).toBe(false);
    expect(button("配对…")).toBeDefined();
  });

  it("还没有协作 hub 时用一句话说明", async () => {
    await shown(view({ hubs: [] }), { prefs: [] });
    expect(text()).toContain("和另一个 hub 配对后，你的助手就能给它的助手发消息。");
    expect(host.querySelector("[data-partner-hub]")).toBeNull();
  });

  it("别的 hub 连不进来时，「我的配对码」不能点，并说明怎么办", async () => {
    await shown(view({ can_issue: false }));
    expect(button("我的配对码")?.disabled).toBe(true);
    expect(text()).toContain("请在另一个 hub 上显示配对码，再在这里配对");
  });

  it("互联组的成员 hub：说明不能配对，没有按钮", async () => {
    await shown(view({ member: true, hubs: [] }), { prefs: [] });
    expect(text()).toContain("已加入互联的 hub 组");
    expect(button("配对…")).toBeUndefined();
  });

  it("读不到列表时显示原因和重试", async () => {
    await render();
    await refuse(last("GET", "/partner-hubs"), new ApiError(500, "boom", { error: "boom" }));
    expect(text()).toContain("没能读取协作 hub");
    await click(button("重试"));
    expect(calls.filter((c) => c.path === "/partner-hubs")).toHaveLength(2);
  });

  it("「发送前问我」存到默认助手，只显示它答复的值", async () => {
    await shown();
    await click(switches("hub-b")[0]);
    const put = last("PUT", "/partner-messages/prefs/hub-b");
    expect(put?.json).toEqual({ confirm: false });
    // while it saves: still what the manager said, and not pressable twice
    expect(checked(switches("hub-b")[0])).toBe(true);
    expect(switches("hub-b")[0].disabled).toBe(true);
    await answer(put, { hub_id: "hub-b", confirm: false });
    expect(checked(switches("hub-b")[0])).toBe(false);
    expect(switches("hub-b")[0].disabled).toBe(false);
    // and it reads anew: the manager pushes nothing for its prefs, and a read already out may predate the change
    expect(calls.filter((c) => c.path === "/partner-messages/prefs")).toHaveLength(2);
    // the manager answered off: it shows off
    await click(switches("hub-b")[0]);
    await answer(last("PUT", "/partner-messages/prefs/hub-b"), { hub_id: "hub-b", confirm: false });
    expect(checked(switches("hub-b")[0])).toBe(false);
    // a refusal: unchanged, and said
    await click(switches("hub-b")[0]);
    expect(last("PUT", "/partner-messages/prefs/hub-b")?.json).toEqual({ confirm: true });
    await refuse(last("PUT", "/partner-messages/prefs/hub-b"), new ApiError(500, "boom", { error: "boom" }));
    expect(checked(switches("hub-b")[0])).toBe(false);
    expect(toast).toHaveBeenCalledWith(expect.stringContaining("没能保存"), "error");
  });

  it("读不到默认助手的设置时，开关不能点并说明", async () => {
    await render();
    await answer(last("GET", "/partner-hubs"), view());
    await refuse(last("GET", "/partner-messages/prefs"), new ApiError(404, "not found", {}));
    expect(switches("hub-b")[0].disabled).toBe(true);
    expect(row("hub-b").textContent).toContain("读不到这项设置");
    // the other switch is the hub's and still works
    expect(switches("hub-b")[1].disabled).toBe(false);
  });

  it("「主动连接这个 hub」改的是 hub 的拨号设置，显示 hub 答复的状态", async () => {
    await shown();
    await click(switches("hub-b")[1]);
    const patch = last("PATCH", "/partner-hubs/hub-b");
    expect(patch?.json).toEqual({ dial: "never" });
    expect(checked(switches("hub-b")[1])).toBe(true);
    await answer(patch, { ...view().hubs[0], dial: "never" });
    expect(checked(switches("hub-b")[1])).toBe(false);
    await click(switches("hub-c")[1]);
    expect(last("PATCH", "/partner-hubs/hub-c")?.json).toEqual({ dial: "auto" });
    const member = "这台 hub 已加入互联的 hub 组，不能有协作 hub";
    await refuse(last("PATCH", "/partner-hubs/hub-c"), new ApiError(409, member, { error: member, code: "member_hub" }));
    expect(checked(switches("hub-c")[1])).toBe(false);
    expect(toast).toHaveBeenCalledWith(`没能保存：${member}`, "error");
  });

  it("解除配对先在这一行确认，再删除", async () => {
    await shown();
    await click([...row("hub-c").querySelectorAll("button")].find((b) => b.textContent === "解除配对"));
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    expect([...row("hub-c").querySelectorAll("button")].map((b) => b.textContent)).toEqual(expect.arrayContaining(["取消", "解除配对"]));
    await click([...row("hub-c").querySelectorAll("button")].find((b) => b.textContent === "取消"));
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    await click([...row("hub-c").querySelectorAll("button")].find((b) => b.textContent === "解除配对"));
    await click([...row("hub-c").querySelectorAll("button")].filter((b) => b.textContent === "解除配对").at(-1));
    await answer(last("DELETE", "/partner-hubs/hub-c"), { ok: true });
    expect(host.querySelector('[data-partner-hub="hub-c"]')).toBeNull();
    expect(toast).toHaveBeenCalledWith("已和「朋友的 hub」解除配对", "ok");
  });

  it("「我的配对码」显示一次性配对码、倒计时和指纹", async () => {
    await shown();
    await click(button("我的配对码"));
    expect(text()).toContain("正在生成…");
    await answer(last("POST", "/partner-hubs/code"), { code: CODE, expires_at: new Date(Date.now() + 600_000).toISOString() });
    const dialog = document.body.querySelector("[data-partner-code-dialog]")!;
    expect(dialog.textContent).toContain(CODE);
    expect(dialog.textContent).toMatch(/(10:00|9:59) 后失效/);
    expect(dialog.textContent).toContain(FP);
    expect(dialog.querySelector('button[aria-label="复制"]')).not.toBeNull();
  });

  it("再打开「我的配对码」时从头生成，不先露出上一个；生成失败可以重试", async () => {
    await shown();
    await click(button("我的配对码"));
    await answer(last("POST", "/partner-hubs/code"), { code: CODE, expires_at: new Date(Date.now() + 600_000).toISOString() });
    await click(button("完成"));
    await click(button("我的配对码"));
    // the open dialog: the one that just closed stays in the page while it leaves
    const open = () => document.body.querySelector('[role="dialog"][aria-modal="true"] [data-partner-code-dialog]')!;
    expect(open().textContent).not.toContain(CODE);
    expect(open().textContent).toContain("正在生成…");
    expect(calls.filter((c) => c.method === "POST" && c.path === "/partner-hubs/code")).toHaveLength(2);
    await refuse(last("POST", "/partner-hubs/code"), new ApiError(503, "数据库没有回应", { error: "数据库没有回应", code: "no_database" }));
    expect(open().querySelector('[role="alert"]')?.textContent).toContain("数据库没有回应");
    await click([...open().querySelectorAll("button")].find((b) => b.textContent?.trim() === "重试"));
    expect(calls.filter((c) => c.method === "POST" && c.path === "/partner-hubs/code")).toHaveLength(3);
    await answer(last("POST", "/partner-hubs/code"), { code: CODE, expires_at: new Date(Date.now() + 600_000).toISOString() });
    expect(open().textContent).toContain(CODE);
    expect(open().querySelector('[role="alert"]')).toBeNull();
  });

  it("关掉对话框后才答复的配对：用提示说结果，不留到下次打开", async () => {
    await shown();
    await click(button("配对…"));
    await type(CODE);
    await click(button("配对"));
    await click(button("取消"));
    await refuse(last("POST", "/partner-hubs"), new ApiError(401, "refused", { error: "refused", code: "code_refused" }));
    expect(toast).toHaveBeenCalledWith(expect.stringContaining("已经用过或失效"), "error");
    await click(button("配对…"));
    expect(document.body.querySelector('[role="dialog"][aria-modal="true"] [role="alert"]')).toBeNull();
    expect(button("配对")?.disabled).toBe(true);
  });

  it("「配对…」先检查粘贴的配对码，显示对方的名称和指纹，再配对", async () => {
    await shown();
    await click(button("配对…"));
    expect(button("配对")?.disabled).toBe(true);
    await type("https://example.com/not-a-code");
    expect(text()).toContain("这不是协作 hub 的配对码");
    expect(button("配对")?.disabled).toBe(true);
    await type(`  ${CODE}\n`);
    expect(text()).not.toContain("这不是协作 hub 的配对码");
    const facts = document.body.querySelector("[data-partner-code-facts]")!;
    expect(facts.textContent).toContain("Studio");
    expect(facts.textContent).toContain(FP);
    expect(button("配对")?.disabled).toBe(false);
    await click(button("配对"));
    expect(last("POST", "/partner-hubs")?.json).toEqual({ code: CODE });
    // the hub's refusal stays in the dialog, in plain words
    await refuse(last("POST", "/partner-hubs"), new ApiError(409, "own", { error: "own", code: "own_code" }));
    expect(document.body.querySelector('[role="alert"]')?.textContent).toContain("这台 hub 自己的配对码");
    await click(button("配对"));
    await answer(last("POST", "/partner-hubs"), { hub_id: "hub-d", name: "Studio", fp: FP, state: "connecting", dial: "auto", created_at: ago(0) });
    expect(toast).toHaveBeenCalledWith("已和「Studio」配对", "ok");
    expect(document.body.querySelector("textarea")).toBeNull();
    // and the list is read again
    expect(calls.filter((c) => c.method === "GET" && c.path === "/partner-hubs")).toHaveLength(2);
  });

  it("hub 推送 partner_hubs 或没能续上的重连时重新读取", async () => {
    await shown();
    await act(async () => listeners.get("partner_hubs")!({ type: "partner_hubs" }));
    expect(calls.filter((c) => c.path === "/partner-hubs")).toHaveLength(2);
    await act(async () => listeners.get("hello")!({ type: "hello", resumed: true }));
    expect(calls.filter((c) => c.path === "/partner-hubs")).toHaveLength(2);
    await act(async () => listeners.get("hello")!({ type: "hello" }));
    expect(calls.filter((c) => c.path === "/partner-hubs")).toHaveLength(3);
    // only the newest read's answer counts
    await answer(calls.filter((c) => c.path === "/partner-hubs")[2], view({ hubs: [view().hubs[1]] }));
    await answer(calls.filter((c) => c.path === "/partner-hubs")[1], view());
    expect(host.querySelector('[data-partner-hub="hub-b"]')).toBeNull();
  });
});
