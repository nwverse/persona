// @vitest-environment jsdom
// Safari picks an input-method candidate with a keydown whose isComposing is false and keyCode is 229 (#4667, #4937):
// a menu's keys and its rename input leave that one to the input method.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Menu } from "./atoms";

let root: Root;
let host: HTMLDivElement;

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  window.matchMedia = ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const key = (el: EventTarget, k: string, safari: boolean) =>
  act(async () => {
    el.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true, ...(safari ? { keyCode: 229, isComposing: false } : {}) }));
  });

describe("Safari's candidate keys in a menu", () => {
  it("do not move or choose a row", async () => {
    const onSelect = vi.fn();
    act(() => root.render(<Menu anchor={null} onClose={() => {}} items={[{ key: "a", label: "文件", onSelect }, { key: "b", label: "终端", onSelect: () => {} }]} />));
    await key(document.body, "ArrowDown", true);
    await key(document.body, "Enter", true);
    expect(onSelect).not.toHaveBeenCalled();
    await key(document.body, "ArrowDown", false);
    await key(document.body, "Enter", false);
    expect(onSelect).toHaveBeenCalledOnce();
  });

  it("do not commit a half-typed rename", async () => {
    const commit = vi.fn(async () => {});
    act(() => root.render(<Menu anchor={null} onClose={() => {}} items={[{ key: "r", label: "重命名…", onSelect: () => {}, rename: { value: "旧名字", commit } }]} />));
    const row = [...document.querySelectorAll("button")].find((b) => b.textContent?.includes("重命名"))!;
    act(() => row.click());
    const input = document.querySelector<HTMLInputElement>("input")!;
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "新名字");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await key(input, "Enter", true);
    expect(commit).not.toHaveBeenCalled();
    await key(input, "Enter", false);
    expect(commit).toHaveBeenCalledExactlyOnceWith("新名字");
  });

  it("passes a file name with its trailing whitespace", async () => {
    const commit = vi.fn(async () => {});
    act(() => root.render(<Menu anchor={null} onClose={() => {}} items={[{ key: "r", label: "Rename", onSelect: () => {}, rename: { value: "report", preserveWhitespace: true, commit } }]} />));
    const row = [...document.querySelectorAll("button")].find((b) => b.textContent?.includes("Rename"))!;
    act(() => row.click());
    const input = document.querySelector<HTMLInputElement>("input")!;
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "report\u00a0");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await key(input, "Enter", false);
    expect(commit).toHaveBeenCalledExactlyOnceWith("report\u00a0");
  });
});
