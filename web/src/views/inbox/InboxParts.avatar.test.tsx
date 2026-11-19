// @vitest-environment jsdom
// Feishu items stored before the extension rewrote media paths keep the gateway's /agent/v1/media/ avatar: the browser
// got the web app's HTML for it and showed a broken image (#1353).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { InboxItem } from "../../lib/inbox";
import { avatarSrc } from "../../lib/connectors";
import { ItemAvatar, Monogram } from "./InboxParts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root;
let host: HTMLDivElement;

beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const larkItem = (avatar: string) => ({ id: "lark:chat:1", source: "lark", title: "Review group", avatar, lark: { feed_id: "chat", is_group: true } }) as unknown as InboxItem;

describe("inbox avatars", () => {
  it("loads a gateway avatar path through the hub", () => {
    act(() => root.render(<ItemAvatar it={larkItem("/agent/v1/media/default-avatar_1")} />));
    const src = host.querySelector("img:not([class*='size-'])")?.getAttribute("src") ?? "";
    expect(src).toContain("/api/v1/lark/media/default-avatar_1");
    expect(src).not.toContain("/agent/v1/");
  });

  // A connector's thread picture (#10923): the avatars route's path the hub listed, asked at a size it draws; the letter
  // without one or when it does not load; the source badge stays.
  const telegramItem = (avatar_url?: string) =>
    ({ id: "telegram:dm1", source: "telegram", title: "Example user", sender: "Example user", account_name: "Telegram",
       connector: { thread: "u1", label: "DM", ...(avatar_url ? { avatar_url } : {}) } }) as unknown as InboxItem;
  const face = () => [...host.querySelectorAll("img")].find((img) => img.getAttribute("src")?.includes("/api/v1/avatars/"));

  it("draws a connector row's picture from the avatars route", () => {
    act(() => root.render(<ItemAvatar it={telegramItem("/api/v1/avatars/telegram/YS51MS41MDE")} />));
    const src = face()?.getAttribute("src") ?? "";
    expect(src).toMatch(/^\/api\/v1\/avatars\/telegram\/YS51MS41MDE\?size=(64|128)/);
    expect(host.querySelector("[title='Telegram']")).not.toBeNull();
  });

  it("asks for the smallest size the route draws that covers the pixels drawn", () => {
    expect(avatarSrc("/api/v1/avatars/t/x", 40, 1)).toBe("/api/v1/avatars/t/x?size=64");
    expect(avatarSrc("/api/v1/avatars/t/x", 40, 2)).toBe("/api/v1/avatars/t/x?size=128");
    expect(avatarSrc("/api/v1/avatars/t/x", 36, 3)).toBe("/api/v1/avatars/t/x?size=128");
    expect(avatarSrc("/api/v1/avatars/t/x", 400, 3)).toBe("/api/v1/avatars/t/x?size=512");
    expect(avatarSrc(undefined, 40)).toBeUndefined();
  });

  it("keeps a connector row's letter without a picture, and when it does not load", () => {
    act(() => root.render(<ItemAvatar it={telegramItem()} />));
    expect(face()).toBeUndefined();
    expect(host.textContent).toContain("E");
    act(() => root.render(<ItemAvatar it={telegramItem("/api/v1/avatars/telegram/YS51Lmdvbmu")} />));
    act(() => face()!.dispatchEvent(new Event("error")));
    expect(face()).toBeUndefined();
    expect(host.textContent).toContain("E");
  });

  it("shows the letter when the picture does not load, and tries a new one", () => {
    act(() => root.render(<Monogram name="Review group" src="/api/v1/lark/media/gone" />));
    const img = host.querySelector("img");
    expect(img).not.toBeNull();
    act(() => img!.dispatchEvent(new Event("error")));
    expect(host.querySelector("img")).toBeNull();
    expect(host.textContent).toBe("R");
    act(() => root.render(<Monogram name="Review group" src="/api/v1/lark/media/other" />));
    expect(host.querySelector("img")).not.toBeNull();
  });
});
