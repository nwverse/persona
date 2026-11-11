// Clearing the main conversation's context in place (#5095), as Cindy's /clear: POST /assistant/main/clear stops a turn
// it is on, archives it as "Old main chat · to <date>" and carries nothing over, so the next message starts a new
// conversation with a fresh context. No confirmation: nothing is deleted (docs/design/DESIGN_SYSTEM.md 4.4, the
// reversible rung). The page stays where it is: the chat empties and the composer stays at the bottom
// (PersonaAssistant's Starter, `cleared`), where the first message starts the new one.
import { useSyncExternalStore } from "react";
import { t } from "../i18n";
import { toast } from "../ui/atoms";
import { api, ApiError, errorText } from "./api";
import type { ComposerCommand } from "./extension-prompts";
import { navigate } from "./router";
import { refreshAssistant } from "./store";

export const CLEAR_MAIN_FEATURE = "assistant_main_clear";
export const CLEAR_MAIN_PATH = "/assistant/main/clear";

/** The main conversation's command list: /clear, worded as an Agent's /clear is. */
export function mainCommands(): ComposerCommand[] {
  return [{ word: "/clear", desc: t("chat-composer.cmd_clear_desc"), text: "/clear" }];
}

/** A message that is the /clear command: the word alone, no pictures (anything more is words for Persona). */
export function isClearCommand(text: string, images: number): boolean {
  return images === 0 && text.trim().toLowerCase() === "/clear";
}

// ── the conversation this tab cleared, while the main one is still to start ──
// The page keeps the conversation's layout for it (an empty chat, the composer at the bottom); a main conversation
// showing again (PersonaAssistant) forgets it, so a later "New main chat" or another device's change shows the
// ordinary start page.

let cleared = "";
const listeners = new Set<() => void>();

function setCleared(sid: string) {
  if (cleared === sid) return;
  cleared = sid;
  listeners.forEach((l) => l());
}

export function forgetMainCleared() {
  setCleared("");
}

export function mainCleared(): boolean {
  return cleared !== "";
}

export function useMainCleared(): boolean {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    mainCleared,
    mainCleared,
  );
}

// ── the action ──

export interface ClearDeps {
  post: (expect: string) => Promise<unknown>;
  refresh: () => Promise<unknown>;
  /** To the main conversation's page (a conversation's own address showed the old one). */
  home: () => void;
  toast: (text: string, tone: "error" | "info") => void;
}

const defaultDeps: ClearDeps = {
  post: (expect) => api(CLEAR_MAIN_PATH, { method: "POST", json: { expect } }),
  refresh: () => refreshAssistant().catch(() => {}),
  home: () => navigate({ name: "persona" }),
  toast: (text, tone) => toast(text, tone),
};

const inFlight = new Set<string>();

/** Clears the main conversation sid (the one this device shows; another device's change since is refused, then shown).
 *  True when it is done or another one shows now, so the /clear typed is used up; false when it failed (a toast says
 *  why), so it goes back into the box. A second clear of the same one while the first is on its way does nothing. */
export async function clearMainContext(sid: string, deps: ClearDeps = defaultDeps): Promise<boolean> {
  if (inFlight.has(sid)) return true;
  inFlight.add(sid);
  // before the request: the hub's push of the change can come before its answer, and the page must not show the
  // centered start page in between
  setCleared(sid);
  try {
    await deps.post(sid);
    // (the refresh also drops the old conversation's cached first page: it is no longer the main one's)
    await deps.refresh();
    deps.home();
    return true;
  } catch (e) {
    forgetMainCleared();
    if (e instanceof ApiError && e.code === "main_changed") {
      // another device replaced or cleared it: show what the hub has now
      await deps.refresh();
      deps.home();
      deps.toast(t("assistant-assistantchat.new_main_elsewhere"), "info");
      return true;
    }
    deps.toast(t("assistant-assistantchat.clear_failed", { error: errorText(e) }), "error");
    return false;
  } finally {
    inFlight.delete(sid);
  }
}
