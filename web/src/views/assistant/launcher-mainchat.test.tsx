// @vitest-environment jsdom
// The Persona chat's launcher menu offers only what can run the assistant: a Paseo provider and a managed Codex launcher
// an older hub still lists are left out, and a pick of one is dropped back to the default with a word.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  window.matchMedia ??= ((query: string) => ({
    matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false,
  })) as typeof window.matchMedia;
});
vi.mock("../../lib/launcher-models", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/launcher-models")>()),
  machineLaunchers: vi.fn(async () => ({
    launchers: [
      { name: "claude", source: "zshrc" },
      { name: "gw-claude", source: "jarvis", model: "opus" },
      { name: "cc-relay", source: "paseo", model: "kimi" },
      { name: "codex-gw", source: "jarvis", agent: "codex", model: "gpt" },
    ],
  })),
  launcherModels: vi.fn(async () => ({ models: [] })),
}));

import { AssistantLauncherChip, StarterLauncherChip } from "./AssistantChat";
import { NEW_CONVERSATION, pickedLauncher, pickLauncher } from "../../lib/assistant-chat";
import { getState, reset } from "../../lib/store";
import { Toasts } from "../../ui/atoms";
import type { Session } from "../../lib/types";
import { t } from "../../i18n";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const session = { machine_id: "devbox", session_id: "s-main-chat", assistant: true, agent: "claude" } as unknown as Session;
const key = "devbox/s-main-chat";
let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  pickLauncher(key, undefined);
  pickLauncher(NEW_CONVERSATION, undefined);
  reset();
});

async function openMenu() {
  await act(async () => root.render(<AssistantLauncherChip session={session} />));
  await act(async () => host.querySelector<HTMLButtonElement>(`button[title="${t("assistant-assistantchat.launcher_chip_title")}"]`)!.click());
  return [...document.querySelectorAll<HTMLButtonElement>('[role="menu"] button')].map((b) => b.textContent ?? "");
}

describe("Persona chat launcher menu", () => {
  it("lists only the launchers that run the assistant", async () => {
    const rows = await openMenu();
    expect(rows.some((r) => r.includes("gw-claude"))).toBe(true);
    expect(rows.some((r) => r.startsWith("claude"))).toBe(true);
    expect(rows.some((r) => r.includes("cc-relay"))).toBe(false);
    expect(rows.some((r) => r.includes("codex-gw"))).toBe(false);
  });

  it("drops a pick the menu no longer offers and says so", async () => {
    pickLauncher(key, "cc-relay");
    await openMenu();
    expect(pickedLauncher(key)).toBeUndefined();
    const shown = document.createElement("div");
    document.body.append(shown);
    const toasts = createRoot(shown);
    act(() => toasts.render(<Toasts />));
    expect(document.body.textContent).toContain(t("assistant-assistantchat.pick_dropped", { launcher: "cc-relay" }));
    act(() => toasts.unmount());
    shown.remove();
  });

  it("drops the new-conversation page's pick the menu no longer offers", async () => {
    Object.assign(getState(), { assistant: { machine_id: "devbox", launcher: "claude", conversations: [] } });
    pickLauncher(NEW_CONVERSATION, "codex-gw");
    await act(async () => root.render(<StarterLauncherChip />));
    expect(pickedLauncher(NEW_CONVERSATION)).toBeUndefined();
    act(() => pickLauncher(NEW_CONVERSATION, "gw-claude"));
    await act(async () => root.render(<StarterLauncherChip />));
    expect(pickedLauncher(NEW_CONVERSATION)).toBe("gw-claude");
  });
});
