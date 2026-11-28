// Transcript format for visible BotMux sessions, switched by the sidebar's botmux chip (#10764).
import { useSyncExternalStore } from "react";

export type BotmuxMode = "decode" | "all";

export const BOTMUX_MODE_KEY = "persona.agentBotmuxMode";
export const LEGACY_BOTMUX_HIDDEN_KEY = "persona.agentHideBotmux";

/** BotMux's control line for "the replies already went to Lark" (the hub's transcript.BotmuxSentinel): last words that
 *  are only this are not drawn while decoding; words around it, and raw mode, keep it as written. */
export const BOTMUX_SENTINEL = "BOTMUX_NOTHING_TO_SEND";

/** The stored mode; the default where there is no storage (a server render) or it cannot be read. */
export function readBotmuxMode(store?: Storage): BotmuxMode {
  try {
    const storage = store ?? globalThis.localStorage;
    const mode = storage?.getItem(BOTMUX_MODE_KEY);
    return mode === "all" ? "all" : "decode";
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
