// @vitest-environment jsdom
// "Hand to AI" with several managers (docs/assistant/managers.md "Clients"): the item goes to its manager's main
// conversation by default (#1194), or to a new conversation with that manager, and the conversation's next requests go
// there too; an item stamped with the default (a hub with one manager stamps every item so) sends no header at all.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../../lib/router", () => ({ navigate: vi.fn() }));
vi.mock("../../ui/atoms", async (orig) => ({ ...(await orig<typeof import("../../ui/atoms")>()), toast: vi.fn() }));

import { reloadInbox, resetInboxForTests, type InboxItem } from "../../lib/inbox";
import { chooseManager, chosenManager, managerHeaderFor, resetManagerChoice, setRosterDefault } from "../../lib/manager-choice";
import { navigate } from "../../lib/router";
import { HandOffDialog } from "./InboxDialogs";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const item = (manager: string): InboxItem => ({
  id: "lark:f1:1", manager, source: "lark", kind: "dm", account: "feishu", account_name: "Lark", title: "Example", preview: "text", count: 1,
  updated_at: "2026-11-01T09:00:00Z", tier: "action", lark: { feed_id: "f1", is_group: false },
});

let host: HTMLDivElement;
let root: Root;
const calls: { url: string; method: string; header: string | null; body: unknown }[] = [];
// what GET /assistant answers, by the manager it names (none: the default's)
let mains: Record<string, string | undefined>;

beforeEach(() => {
  calls.length = 0;
  mains = { assistant: "main-a", "m-1": "main-m1" };
  resetManagerChoice();
  resetInboxForTests();
  setRosterDefault("assistant");
  vi.mocked(navigate).mockClear();
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    const header = new Headers(init?.headers).get("X-Persona-Manager");
    calls.push({ url, method: init?.method ?? "GET", header, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    const main = mains[header ?? "assistant"];
    const body = url.endsWith("/assistant/conversations") ? { session: { machine_id: "mac", session_id: "s-new" } }
      : url.endsWith("/assistant") ? { machine_id: "mac", conversations: [], ...(main ? { main } : {}) }
      : url.includes("/inbox?") ? { items: [item("assistant")] }
      : url.includes("/inbox/") ? item("assistant") : {};
    return { ok: true, status: 200, text: async () => JSON.stringify(body), headers: new Headers() };
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  resetManagerChoice();
  resetInboxForTests();
  vi.unstubAllGlobals();
});

const pick = (label: string) => {
  const radio = [...document.body.querySelectorAll('[role="radio"]')].find((b) => b.textContent?.includes(label)) as HTMLButtonElement;
  act(() => radio.click());
};
const send = async (done: (c: (typeof calls)[number]) => boolean) => {
  const button = [...document.body.querySelectorAll("button")].find((b) => b.textContent?.trim() === "发送")!;
  await act(async () => button.click());
  await vi.waitFor(() => expect(calls.some(done)).toBe(true));
};
const started = () => calls.find((c) => c.url.endsWith("/assistant/conversations"))!;
const sentTo = (sid: string) => (c: (typeof calls)[number]) => c.method === "POST" && c.url === `/api/v1/sessions/mac/${sid}/messages`;

describe("Hand to AI: Persona's main conversation", () => {
  it("is picked by default and checked", () => {
    act(() => root.render(<HandOffDialog item={item("assistant")} onClose={() => {}} />));
    const radios = [...document.body.querySelectorAll('[role="radio"]')];
    expect(radios[0].textContent).toContain("Persona（主对话）");
    expect(radios[0].getAttribute("aria-checked")).toBe("true");
    expect(radios[1].textContent).toContain("Persona（新对话）");
  });

  it("sends the item to the default's main conversation and opens it, starting nothing", async () => {
    act(() => root.render(<HandOffDialog item={item("assistant")} onClose={() => {}} />));
    await send(sentTo("main-a"));
    const message = calls.find(sentTo("main-a"))!;
    expect((message.body as { text: string }).text).toContain('kind="context" title="');
    expect(calls.some((c) => c.url.endsWith("/assistant/conversations"))).toBe(false);
    expect(calls.every((c) => c.header === null)).toBe(true);
    expect(navigate).toHaveBeenCalledWith({ name: "persona", sid: "main-a" });
    expect(chosenManager()).toBeNull();
  });

  it("files the item as done only once the message got there, when asked to", async () => {
    await reloadInbox();
    const fetcher = vi.mocked(fetch);
    const answer = fetcher.getMockImplementation()!;
    let deliver!: () => void;
    const delivered = new Promise<void>((r) => (deliver = r));
    fetcher.mockImplementation(async (url, init) => {
      const reply = await answer(url, init);
      if (String(url) === "/api/v1/sessions/mac/main-a/messages") await delivered;
      return reply;
    });
    act(() => root.render(<HandOffDialog item={item("assistant")} onClose={() => {}} />));
    await send(sentTo("main-a"));
    await act(async () => {});
    expect(calls.some((c) => c.url.endsWith("/done"))).toBe(false);
    deliver();
    await vi.waitFor(() => expect(calls.some((c) => c.url === "/api/v1/inbox/lark:f1:1/done")).toBe(true));
  });

  it("leaves the item open when the message didn't get there", async () => {
    await reloadInbox();
    const fetcher = vi.mocked(fetch);
    const answer = fetcher.getMockImplementation()!;
    fetcher.mockImplementation(async (url, init) => {
      const reply = await answer(url, init);
      return String(url) === "/api/v1/sessions/mac/main-a/messages"
        ? ({ ok: false, status: 409, text: async () => JSON.stringify({ code: "refused", error: "Refused" }), headers: new Headers() } as Response)
        : reply;
    });
    act(() => root.render(<HandOffDialog item={item("assistant")} onClose={() => {}} />));
    await send(sentTo("main-a"));
    await vi.waitFor(() => expect(navigate).toHaveBeenCalledWith({ name: "persona", sid: "main-a" }));
    await act(async () => {});
    expect(calls.some((c) => c.url.endsWith("/done"))).toBe(false);
  });

  it("sends the default's item to the default's main conversation while the page shows another manager", async () => {
    chooseManager("m-1");
    act(() => root.render(<HandOffDialog item={item("assistant")} onClose={() => {}} />));
    await send(sentTo("main-a"));
    expect(calls.some((c) => c.url === "/api/v1/assistant" && c.header === "assistant")).toBe(true);
    expect(navigate).toHaveBeenCalledWith({ name: "persona", sid: "main-a" });
    expect(chosenManager()).toBeNull();
  });

  it("sends another manager's item to that manager's main conversation and shows its page", async () => {
    act(() => root.render(<HandOffDialog item={item("m-1")} onClose={() => {}} />));
    await send(sentTo("main-m1"));
    expect(calls.find((c) => c.url === "/api/v1/assistant")).toMatchObject({ header: "m-1" });
    expect(navigate).toHaveBeenCalledWith({ name: "persona", sid: "main-m1" });
    expect(chosenManager()).toBe("m-1");
    expect(managerHeaderFor("/assistant/conversations/main-m1/feed")).toBe("m-1");
  });

  it("starts the manager's main conversation with the item when it has none", async () => {
    mains["m-1"] = undefined;
    act(() => root.render(<HandOffDialog item={item("m-1")} onClose={() => {}} />));
    await send((c) => c.url.endsWith("/assistant/conversations"));
    expect(started()).toMatchObject({ method: "POST", header: "m-1", body: { main: true } });
    expect((started().body as { prompt: string }).prompt).toContain('kind="context" title="');
    await vi.waitFor(() => expect(navigate).toHaveBeenCalledWith({ name: "persona", sid: "s-new" }));
    expect(managerHeaderFor("/assistant/conversations/s-new/feed")).toBe("m-1");
  });

  it("stays open with the reason when the manager can't be read, moving nothing", async () => {
    const fetcher = vi.mocked(fetch);
    const answer = fetcher.getMockImplementation()!;
    fetcher.mockImplementation(async (url, init) => {
      if (!String(url).endsWith("/assistant")) return answer(url, init);
      calls.push({ url: String(url), method: "GET", header: new Headers(init?.headers).get("X-Persona-Manager"), body: undefined });
      return { ok: false, status: 503, text: async () => JSON.stringify({ code: "assistant_offline", error: "Assistant temporarily unavailable" }), headers: new Headers() } as Response;
    });
    const onClose = vi.fn();
    act(() => root.render(<HandOffDialog item={item("m-1")} onClose={onClose} />));
    await send((c) => c.url === "/api/v1/assistant");
    await vi.waitFor(() => expect(document.body.textContent).toContain("Assistant temporarily unavailable"));
    expect(onClose).not.toHaveBeenCalled();
    expect(navigate).not.toHaveBeenCalled();
    expect(chosenManager()).toBeNull();
    expect(calls.some((c) => c.url.includes("/messages") || c.url.endsWith("/assistant/conversations"))).toBe(false);
  });

  it("stays open with the reason when the manager's main conversation can't start, moving nothing", async () => {
    mains["m-1"] = undefined;
    const fetcher = vi.mocked(fetch);
    const answer = fetcher.getMockImplementation()!;
    fetcher.mockImplementation(async (url, init) => {
      if (!String(url).endsWith("/assistant/conversations")) return answer(url, init);
      calls.push({ url: String(url), method: "POST", header: new Headers(init?.headers).get("X-Persona-Manager"), body: undefined });
      return { ok: false, status: 409, text: async () => JSON.stringify({ code: "refused", error: "Can't start a conversation" }), headers: new Headers() } as Response;
    });
    const onClose = vi.fn();
    act(() => root.render(<HandOffDialog item={item("m-1")} onClose={onClose} />));
    await send((c) => c.url.endsWith("/assistant/conversations"));
    await vi.waitFor(() => expect(document.body.textContent).toContain("Can't start a conversation"));
    expect(onClose).not.toHaveBeenCalled();
    expect(navigate).not.toHaveBeenCalled();
    expect(chosenManager()).toBeNull();
  });
});

describe("Hand to AI: a new conversation", () => {
  it("names nothing for an item stamped with the default and nothing picked (one manager)", async () => {
    act(() => root.render(<HandOffDialog item={item("assistant")} onClose={() => {}} />));
    pick("Persona（新对话）");
    await send((c) => c.url.endsWith("/assistant/conversations"));
    expect(calls.every((c) => c.header === null)).toBe(true);
    expect(started().body).toEqual({ prompt: expect.any(String) });
    expect(chosenManager()).toBeNull();
  });

  it("starts with the item's manager, shows its page and sends the conversation's requests there", async () => {
    act(() => root.render(<HandOffDialog item={item("m-1")} onClose={() => {}} />));
    pick("Persona（新对话）");
    await send((c) => c.url.endsWith("/assistant/conversations"));
    expect(calls[0]).toMatchObject({ url: "/api/v1/inbox/lark:f1:1", header: "m-1" });
    expect(started().header).toBe("m-1");
    expect(chosenManager()).toBe("m-1");
    expect(managerHeaderFor("/assistant/conversations/s-new/feed")).toBe("m-1");
  });
});
