import { useCallback, useEffect, useState } from "react";
import { t } from "../../i18n";

export type ChatFontScale = "compact" | "default" | "large";

export interface ChatFontOption {
  value: ChatFontScale;
  label: string;
  bodyPx: number;
  secondaryPx: number;
}

export const CHAT_FONT_OPTIONS: readonly ChatFontOption[] = [
  { value: "compact", label: t("chat-font.compact"), bodyPx: 14, secondaryPx: 13 },
  { value: "default", label: t("chat-font.default"), bodyPx: 15, secondaryPx: 14 },
  { value: "large", label: t("chat-font.large"), bodyPx: 17, secondaryPx: 15.5 },
];

export const CHAT_FONT_STORAGE_KEY = "persona.chatFontScale";

const listeners = new Set<(scale: ChatFontScale) => void>();

function isChatFontScale(value: unknown): value is ChatFontScale {
  return value === "compact" || value === "default" || value === "large";
}

export function readChatFontScale(): ChatFontScale {
  try {
    if (typeof localStorage === "undefined") return "default";
    const raw = localStorage.getItem(CHAT_FONT_STORAGE_KEY);
    return isChatFontScale(raw) ? raw : "default";
  } catch {
    return "default";
  }
}

export function writeChatFontScale(scale: ChatFontScale): void {
  try {
    if (typeof localStorage === "undefined") return;
    localStorage.setItem(CHAT_FONT_STORAGE_KEY, scale);
  } catch {
    // Ignore storage errors (SSR / private browsing / quota).
  }
}

export function applyChatFontScale(scale: ChatFontScale): void {
  if (typeof document === "undefined" || !document.documentElement) return;
  const style = document.documentElement.style;
  if (scale === "default") {
    style.removeProperty("--chat-body");
    style.removeProperty("--chat-secondary");
    return;
  }
  const opt = CHAT_FONT_OPTIONS.find((item) => item.value === scale);
  if (!opt) return;
  style.setProperty("--chat-body", `${opt.bodyPx}px`);
  style.setProperty("--chat-secondary", `${opt.secondaryPx}px`);
}

export function setChatFontScale(next: ChatFontScale): void {
  const valid: ChatFontScale = isChatFontScale(next) ? next : "default";
  writeChatFontScale(valid);
  applyChatFontScale(valid);
  for (const listener of listeners) {
    listener(valid);
  }
}

export function subscribeChatFontScale(listener: (scale: ChatFontScale) => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function useChatFontScale(): [ChatFontScale, (next: ChatFontScale) => void] {
  const [scale, setScale] = useState<ChatFontScale>(() => readChatFontScale());

  useEffect(() => {
    const current = readChatFontScale();
    setScale(current);
    applyChatFontScale(current);
    return subscribeChatFontScale(setScale);
  }, []);

  const update = useCallback((next: ChatFontScale) => {
    setChatFontScale(next);
  }, []);

  return [scale, update];
}
