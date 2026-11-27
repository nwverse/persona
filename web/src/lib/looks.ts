// Lean looks (#16898): three pages a template can make leaner, for people who use Persona mainly as a console for their
// Agents (docs/web/web.md "Templates"). Kept in this browser only, like the phone navigation (lib/phone-nav.ts): each
// look's default stores nothing, so the page is exactly what it was before this existed, and other tabs follow at once.
//   - main_chat: `plain` keeps the main conversation to questions and replies (views/chat/ChatView.tsx): no tray chips,
//     permission pill, context meter, finished supervision rounds or briefing cards;
//   - agents_view: `active` lists only the sessions running now, by computer (views/Sidebar.tsx AgentsBlock);
//   - settings_view: `short` shows a few Settings tabs and puts the rest under More settings (views/Settings.tsx);
//   - sidebar_view: `agents` keeps the sidebar (and the phone's drawer) to the main chat and the Agents, without the
//     Pages and Persona chats sections (views/Sidebar.tsx); Settings stays in the assistant menu, ⌘, and the tab bar.
// Templates set them (lib/templates.ts, the `browser` part) and the built-in Default puts them back.
import { useSyncExternalStore } from "react";

export const LOOKS = {
  main_chat: ["full", "plain"],
  agents_view: ["full", "active"],
  settings_view: ["full", "short"],
  sidebar_view: ["full", "agents"],
} as const;

export type LookName = keyof typeof LOOKS;
export type LookMode<N extends LookName = LookName> = (typeof LOOKS)[N][number];
export const LOOK_NAMES = Object.keys(LOOKS) as LookName[];

const KEYS: Record<LookName, string> = {
  main_chat: "persona.look.main-chat",
  agents_view: "persona.look.agents",
  settings_view: "persona.look.settings",
  sidebar_view: "persona.look.sidebar",
};

const listeners = new Set<() => void>();
const notify = () => listeners.forEach((l) => l());
const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => void listeners.delete(l);
};

const defaultOf = <N extends LookName>(name: N): LookMode<N> => LOOKS[name][0];

export function isLookMode<N extends LookName>(name: N, v: unknown): v is LookMode<N> {
  return (LOOKS[name] as readonly unknown[]).includes(v);
}

export function look<N extends LookName>(name: N): LookMode<N> {
  try {
    const v = localStorage.getItem(KEYS[name]);
    return isLookMode(name, v) ? v : defaultOf(name);
  } catch {
    return defaultOf(name);
  }
}

export function setLook<N extends LookName>(name: N, mode: LookMode<N>) {
  try {
    if (mode === defaultOf(name)) localStorage.removeItem(KEYS[name]);
    else localStorage.setItem(KEYS[name], mode);
  } catch {
    // not kept: the next page starts with the default
  }
  notify();
}

// Another tab of this browser changed one (or its storage was cleared): this one follows.
if (typeof window !== "undefined")
  addEventListener("storage", (e) => {
    if (e.key === null || Object.values(KEYS).includes(e.key)) notify();
  });

/** The look `name` is in, following changes; the default on a server render. */
export function useLook<N extends LookName>(name: N): LookMode<N> {
  const get = () => look(name);
  return useSyncExternalStore(subscribe, get, () => defaultOf(name));
}

export const usePlainMainChat = () => useLook("main_chat") === "plain";
export const useActiveAgentsView = () => useLook("agents_view") === "active";
export const useShortSettings = () => useLook("settings_view") === "short";
export const useAgentsSidebar = () => useLook("sidebar_view") === "agents";
