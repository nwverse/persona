// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { setToken } from "../lib/api";
import type { AutoOrigin } from "../lib/conversation-origins";
import { resetManagerChoice } from "../lib/manager-choice";
import { forgetOpenerIcons } from "../lib/opener-icons";
import { getState } from "../lib/store";
import { emptyTaskIndex } from "../lib/tasks";
import { segments } from "../lib/turns";
import type { Item, Session } from "../lib/types";
import { NO_WORKFLOW_CALLS } from "../lib/workflows";
import { ChatContext } from "../views/chat/chat-context";
import { SegmentView } from "../views/chat/SegmentView";
import { OriginMark } from "./ChannelMark";

vi.mock("../lib/channels", async (original) => ({ ...(await original<typeof import("../lib/channels")>()), useChannels: () => [] }));

// A conversation an extension opened is marked with that extension's own icon (#10358): a third-party extension that
// reuses lark.agent is not drawn as Lark, and without an icon (or the extension) the mark stays as it was. Tests run in
// zh-Hans.
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const icon = (id: string) => `/api/v1/extensions/${id}/-/icon?v=1.0.0`;
type Listed = { id: string; name: string; manager?: string; icon_url?: string };
let installed: Listed[];
let iconStatus: number;
let host: HTMLDivElement, plain: HTMLDivElement, root: Root, plainRoot: Root;
const flush = async () => { for (let i = 0; i < 8; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); }); };
const urls = () => vi.mocked(fetch).mock.calls.map(([url]) => String(url));
/** The extension's own icon in a mark (Lark's tile is a picture too). */
const logo = (el: Element) => el.querySelector<HTMLImageElement>("[data-opener] [data-extension-logo] > img");

// the lazy chunk's module, loaded once up front: a busy run's first import must not outlast the flushes below
beforeAll(async () => { await import("./OpenerTile"); });
beforeEach(() => {
  resetManagerChoice();
  forgetOpenerIcons();
  setToken(`synthetic-${Math.random()}`);
  Object.assign(getState(), { features: ["extensions"] });
  installed = [
    { id: "chat-bridge", name: "Chat Bridge", manager: "assistant", icon_url: icon("chat-bridge") },
    { id: "lark", name: "Lark", manager: "assistant", icon_url: icon("lark") },
    { id: "no-icon", name: "No Icon", manager: "assistant" },
  ];
  iconStatus = 200;
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    if (String(url).includes("/-/icon")) return iconStatus === 200 ? new Response("png", { headers: { "Content-Type": "image/png" } }) : new Response("", { status: iconStatus });
    if (String(url).endsWith("/api/v1/extensions")) return Response.json({ extensions: installed });
    return new Response("", { status: 404 });
  }));
  Object.defineProperty(URL, "createObjectURL", { configurable: true, value: vi.fn(() => "blob:synthetic-icon") });
  Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: vi.fn() });
  host = document.createElement("div"); plain = document.createElement("div");
  document.body.append(host, plain);
  root = createRoot(host); plainRoot = createRoot(plain);
});
afterEach(() => {
  act(() => { root.unmount(); plainRoot.unmount(); });
  host.remove(); plain.remove();
  vi.unstubAllGlobals();
  Object.assign(getState(), { features: [] });
});

/** The mark for a conversation of `origin` opened as `session` says, and today's mark (no opener) beside it. */
async function draw(origin: AutoOrigin, session?: Pick<Session, "opened_by" | "origin_ref" | "manager">) {
  act(() => {
    root.render(<OriginMark origin={origin} size={16} session={session} />);
    plainRoot.render(<OriginMark origin={origin} size={16} />);
  });
  await flush();
  return host.querySelector<HTMLElement>("[data-origin-mark]")!;
}

describe("an extension's conversation is marked with the extension that opened it (#10358)", () => {
  it("draws the opener's own icon for a lark_agent conversation another extension opened, named as before", async () => {
    const mark = await draw("lark_agent", { opened_by: "chat-bridge", origin_ref: "chat-bridge" });
    const image = logo(mark);
    expect(mark.querySelector("[data-opener]")?.getAttribute("data-opener")).toBe("chat-bridge");
    expect(image?.getAttribute("src")).toBe("blob:synthetic-icon");
    expect(urls()).toContain(icon("chat-bridge"));
    // until the icon is in, Lark's tile stays under it; then the icon alone, in the tile's size and corner
    expect(image!.className).toContain("opacity-0");
    expect(mark.querySelector(".shadow-hairline")).not.toBeNull();
    act(() => image!.dispatchEvent(new Event("load")));
    expect(mark.querySelector(".shadow-hairline")).toBeNull();
    const tile = mark.querySelector<HTMLElement>("[data-opener]")!;
    expect([tile.style.width, tile.style.height, tile.style.borderRadius]).toEqual(["16px", "16px", "4px"]);
    // the same name, size and tooltip as today's mark
    expect(mark.getAttribute("role")).toBe("img");
    expect(mark.getAttribute("aria-label")).toBe("来自 飞书 /agent");
    expect(mark.getAttribute("title")).toBe("来自 飞书 /agent");
    expect(mark.style.width).toBe("16px");
  });

  it("asks the icon of the manager whose extension it is", async () => {
    installed = [{ id: "chat-bridge", name: "Chat Bridge", manager: "m-2", icon_url: icon("chat-bridge") }];
    await draw("lark_agent", { opened_by: "chat-bridge", manager: "m-2" });
    const call = vi.mocked(fetch).mock.calls.find(([url]) => String(url).includes("/-/icon"));
    expect(new Headers(call?.[1]?.headers).get("X-Persona-Manager")).toBe("m-2");
  });

  it("keeps Lark's mark for a lark_agent conversation whose opener has no icon, is not installed or fails to load", async () => {
    for (const session of [{ opened_by: "no-icon" }, { opened_by: "removed" }, { origin_ref: "removed" }]) {
      const mark = await draw("lark_agent", session);
      expect(mark.outerHTML).toBe(plain.querySelector("[data-origin-mark]")!.outerHTML);
      expect(logo(mark)).toBeNull();
    }
    // another manager's copy is not this conversation's opener
    installed = [{ id: "chat-bridge", name: "Chat Bridge", manager: "m-2", icon_url: icon("chat-bridge") }];
    forgetOpenerIcons();
    expect(logo(await draw("lark_agent", { opened_by: "chat-bridge" }))).toBeNull();
    // an icon the hub does not serve leaves Lark's tile
    installed = [{ id: "chat-bridge", name: "Chat Bridge", manager: "assistant", icon_url: icon("chat-bridge") }];
    forgetOpenerIcons();
    iconStatus = 404;
    const failed = await draw("lark_agent", { opened_by: "chat-bridge" });
    expect(logo(failed)).toBeNull();
    expect(failed.querySelector(".shadow-hairline")).not.toBeNull();
  });

  it("draws the Lark extension's own icon for its /agent, which is Lark's mark", async () => {
    const mark = await draw("lark_agent", { opened_by: "lark" });
    expect(logo(mark)?.getAttribute("src")).toBe("blob:synthetic-icon");
    expect(urls()).toContain(icon("lark"));
    expect(mark.getAttribute("aria-label")).toBe("来自 飞书 /agent");
  });

  it("draws the icon for an extension conversation, and the extensions symbol without one", async () => {
    const withIcon = await draw("extension", { opened_by: "chat-bridge" });
    expect(logo(withIcon)?.getAttribute("src")).toBe("blob:synthetic-icon");
    expect(withIcon.getAttribute("aria-label")).toBe("来自扩展");
    for (const session of [{ opened_by: "no-icon" }, { origin_ref: "removed" }]) {
      const mark = await draw("extension", session);
      expect(mark.outerHTML).toBe(plain.querySelector("[data-origin-mark]")!.outerHTML);
    }
  });

  it("leaves an older conversation with no opener as it was, without reading the extensions", async () => {
    for (const origin of ["lark_agent", "extension"] as const) {
      const mark = await draw(origin, {});
      expect(mark.outerHTML).toBe(plain.querySelector("[data-origin-mark]")!.outerHTML);
    }
    // other origins' refs are not extensions
    const inbox = await draw("inbox", { origin_ref: "chat-bridge" });
    expect(inbox.outerHTML).toBe(plain.querySelector("[data-origin-mark]")!.outerHTML);
    expect(fetch).not.toHaveBeenCalled();
  });

  it("marks the message that opened the conversation as its rows", async () => {
    const msg: Item = { id: "u1#0", seq: 1, role: "user", kind: "text", text: "整理成周报草稿" };
    act(() => root.render(
      <ChatContext.Provider value={{ tasks: emptyTaskIndex(), subagents: [], scope: "", workflowCalls: NO_WORKFLOW_CALLS, opening: { id: "u1#0", origin: "lark_agent", opened_by: "chat-bridge" } }}>
        <SegmentView seg={segments([msg])[0]} live={false} mid="m" sid="s" expandFold={async () => {}} />
      </ChatContext.Provider>,
    ));
    await flush();
    const line = host.querySelector<HTMLElement>("[data-opened-by=lark_agent]")!;
    expect(logo(line)?.getAttribute("src")).toBe("blob:synthetic-icon");
    expect(line.querySelector("[data-origin-mark]")?.getAttribute("aria-hidden")).toBe("true");
    expect(line.textContent).toContain("来自 飞书 /agent");
  });
});
