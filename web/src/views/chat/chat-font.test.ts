import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  applyChatFontScale,
  CHAT_FONT_OPTIONS,
  CHAT_FONT_STORAGE_KEY,
  readChatFontScale,
  setChatFontScale,
  subscribeChatFontScale,
  writeChatFontScale,
} from "./chat-font";

function createMemoryStorage() {
  const map = new Map<string, string>();
  return {
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => {
      map.set(key, value);
    },
    removeItem: (key: string) => {
      map.delete(key);
    },
    clear: () => {
      map.clear();
    },
  };
}

function createFakeDocument() {
  const props = new Map<string, string>();
  return {
    documentElement: {
      style: {
        setProperty: (name: string, value: string) => {
          props.set(name, value);
        },
        removeProperty: (name: string) => {
          props.delete(name);
          return "";
        },
        getPropertyValue: (name: string) => props.get(name) ?? "",
      },
    },
  };
}

describe("chat-font", () => {
  beforeEach(() => {
    vi.stubGlobal("localStorage", createMemoryStorage());
    vi.stubGlobal("document", createFakeDocument());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("defines compact, default, and large options with expected px sizes", () => {
    expect(CHAT_FONT_OPTIONS).toEqual([
      { value: "compact", label: "紧凑", bodyPx: 14, secondaryPx: 13 },
      { value: "default", label: "标准", bodyPx: 15, secondaryPx: 14 },
      { value: "large", label: "舒适", bodyPx: 17, secondaryPx: 15.5 },
    ]);
  });

  it("reads default when storage is empty or invalid, and persists valid values", () => {
    expect(readChatFontScale()).toBe("default");

    localStorage.setItem(CHAT_FONT_STORAGE_KEY, "huge");
    expect(readChatFontScale()).toBe("default");

    writeChatFontScale("large");
    expect(localStorage.getItem(CHAT_FONT_STORAGE_KEY)).toBe("large");
    expect(readChatFontScale()).toBe("large");
  });

  it("handles localStorage failures gracefully", () => {
    vi.stubGlobal("localStorage", {
      getItem: () => {
        throw new Error("denied");
      },
      setItem: () => {
        throw new Error("denied");
      },
    });
    expect(readChatFontScale()).toBe("default");
    expect(() => writeChatFontScale("compact")).not.toThrow();
  });

  it("applies CSS custom properties for compact/large and clears overrides for default", () => {
    applyChatFontScale("compact");
    expect(document.documentElement.style.getPropertyValue("--chat-body")).toBe("14px");
    expect(document.documentElement.style.getPropertyValue("--chat-secondary")).toBe("13px");

    applyChatFontScale("large");
    expect(document.documentElement.style.getPropertyValue("--chat-body")).toBe("17px");
    expect(document.documentElement.style.getPropertyValue("--chat-secondary")).toBe("15.5px");

    applyChatFontScale("default");
    expect(document.documentElement.style.getPropertyValue("--chat-body")).toBe("");
    expect(document.documentElement.style.getPropertyValue("--chat-secondary")).toBe("");
  });

  it("notifies subscribed listeners and updates storage and DOM on setChatFontScale", () => {
    const received: string[] = [];
    const unsubscribe = subscribeChatFontScale((next) => received.push(next));

    setChatFontScale("large");
    expect(received).toEqual(["large"]);
    expect(readChatFontScale()).toBe("large");
    expect(document.documentElement.style.getPropertyValue("--chat-body")).toBe("17px");

    setChatFontScale("default");
    expect(received).toEqual(["large", "default"]);
    expect(document.documentElement.style.getPropertyValue("--chat-body")).toBe("");

    unsubscribe();
    setChatFontScale("compact");
    expect(received).toEqual(["large", "default"]);
  });

  it("is safe in SSR environments without document or localStorage", () => {
    vi.stubGlobal("document", undefined);
    vi.stubGlobal("localStorage", undefined);
    expect(readChatFontScale()).toBe("default");
    expect(() => applyChatFontScale("large")).not.toThrow();
    expect(() => setChatFontScale("compact")).not.toThrow();
  });
});
