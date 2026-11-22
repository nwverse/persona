// How the web shows botmux sessions (docs/protocol/protocol.md "BotMux messages"), switched by the sidebar's botmux chip
// (#10764): "decode" shows Lark messages and replies, "all" shows raw transcripts, and "hide" hides BotMux sessions.
import { useSyncExternalStore } from "react";

export type BotmuxMode = "decode" | "all" | "hide";

export const BOTMUX_MODE_KEY = "persona.agentBotmuxMode";
export const LEGACY_BOTMUX_HIDDEN_KEY = "persona.agentHideBotmux";

/** The stored mode; the default where there is no storage (a server render) or it cannot be read. */
export function readBotmuxMode(store?: Storage): BotmuxMode {
  try {
    const storage = store ?? globalThis.localStorage;
    const mode = storage?.getItem(BOTMUX_MODE_KEY);
    if (mode === "all" || mode === "hide" || mode === "decode") return mode;
    return storage?.getItem(LEGACY_BOTMUX_HIDDEN_KEY) === "1" ? "hide" : "decode";
  } catch {
    return "decode";
  }
}

const BOTMUX_MODE_EVENT = "persona-botmux-mode";

const subscribe = (changed: () => void) => {
  window.addEventListener(BOTMUX_MODE_EVENT, changed);
  window.addEventListener("storage", changed);
  return () => {
    window.removeEventListener(BOTMUX_MODE_EVENT, changed);
    window.removeEventListener("storage", changed);
  };
};

export const getBotmuxMode = (): BotmuxMode => readBotmuxMode();

export function setBotmuxMode(next: BotmuxMode) {
  if (next === getBotmuxMode()) return;
  try {
    globalThis.localStorage.setItem(BOTMUX_MODE_KEY, next);
  } catch {
    return;
  }
  window.dispatchEvent(new Event(BOTMUX_MODE_EVENT));
}

export function useBotmuxMode(): BotmuxMode {
  return useSyncExternalStore(subscribe, getBotmuxMode, getBotmuxMode);
}
