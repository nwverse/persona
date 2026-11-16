// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";

const loading = vi.hoisted(() => {
  let release!: () => void;
  const ready = new Promise<void>((done) => { release = done; });
  return { ready, release, started: vi.fn(), copied: vi.fn(async () => true) };
});
vi.mock("./ReplyBar", async (original) => {
  loading.started();
  await loading.ready;
  return original<typeof import("./ReplyBar")>();
});
vi.mock("../../ui/atoms", async (original) => ({
  ...await original<typeof import("../../ui/atoms")>(),
  copyText: loading.copied,
}));
vi.mock("../../ui/media-query", async (original) => ({
  ...await original<typeof import("../../ui/media-query")>(),
  isNoHover: () => true,
}));

import { initI18n, t } from "../../i18n";
import { MessageActions } from "./MessageActions";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

it("loads message menus and text selection only on intent, preserving the message, copy and dialog exit", async () => {
  await initI18n("en");
  const text = "First **line**\n\nSecond line.";
  const host = document.createElement("div");
  document.body.append(host);
  const root = createRoot(host);
  const animation = Object.getOwnPropertyDescriptor(Element.prototype, "animate");
  Object.defineProperty(Element.prototype, "animate", {
    configurable: true,
    value: () => ({ finished: new Promise(() => {}), cancel() {} }) as unknown as Animation,
  });
  const item = (label: string) => [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')]
    .find((button) => button.textContent?.trim() === label)!;
  const open = async () => {
    await act(async () => host.querySelector("[data-original]")!.dispatchEvent(
      new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 20, clientY: 24 }),
    ));
  };
  const settle = async () => {
    await act(async () => { await vi.dynamicImportSettled(); });
    await act(async () => { await vi.dynamicImportSettled(); });
  };
  try {
    await act(async () => root.render(<MessageActions text={text} isUser side><p data-original>{text}</p></MessageActions>));
    expect(loading.started).not.toHaveBeenCalled();
    await open();
    await vi.waitFor(() => expect(loading.started).toHaveBeenCalledTimes(1));
    expect(host.querySelector("[data-original]")?.textContent).toBe(text);
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    loading.release();
    await settle();
    await act(async () => item(t("chat-messageactions.select_text")).click());
    await settle();
    const dialog = () => document.querySelector('[role="dialog"]')!;
    expect(dialog().textContent).toContain(text);
    const copy = [...dialog().querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === t("chat-messageactions.copy_all"))!;
    await act(async () => copy.click());
    expect(loading.copied).toHaveBeenCalledExactlyOnceWith(text);
    expect(document.querySelector("[data-closing]")).not.toBeNull();
    await vi.waitFor(() => expect(document.querySelector('[role="dialog"]')).toBeNull());
    expect(host.querySelector("[data-original]")?.textContent).toBe(text);
    expect(loading.started).toHaveBeenCalledTimes(1);
  } finally {
    loading.release();
    act(() => root.unmount());
    host.remove();
    if (animation) Object.defineProperty(Element.prototype, "animate", animation);
    else Reflect.deleteProperty(Element.prototype, "animate");
  }
});
