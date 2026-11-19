// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { openNewAgent } from "../../lib/new-agent";
import { navigate } from "../../lib/router";
import { getState, reset } from "../../lib/store";
import type { Session } from "../../lib/types";
import { ContextRestartNotice } from "./ContextRestartNotice";

vi.mock("../../lib/router", async (original) => ({
  ...(await original<typeof import("../../lib/router")>()),
  navigate: vi.fn(),
}));
vi.mock("../../lib/new-agent", async (original) => ({
  ...(await original<typeof import("../../lib/new-agent")>()),
  openNewAgent: vi.fn(),
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const main: Session = { machine_id: "hub", session_id: "main", title: "Main", cwd: "/project", status: "idle" };
let host: HTMLDivElement;
let root: Root;
const notice = () => host.querySelector("[data-context-restart-notice]");
const action = (label: string) => [...notice()!.querySelectorAll("button")].find((button) => button.textContent === label)!;

beforeEach(() => {
  reset();
  vi.clearAllMocks();
  localStorage.clear();
  getState().features = ["assistant_main_new"];
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
});

const show = (s: Session, assistant: boolean, isMain: boolean) =>
  act(async () => root.render(<ContextRestartNotice key={s.session_id} session={s} assistant={assistant} isMain={isMain} />));

describe("sustained context usage notice", () => {
  it("opens the existing new-main dialog with summary carry selected", async () => {
    await show(main, true, true);
    await act(async () => action(t("assistant-assistantchat.new_main_ellipsis")).click());
    await act(async () => vi.dynamicImportSettled());
    const dialog = document.querySelector('[role="dialog"]');
    expect(dialog?.textContent).toContain(t("assistant-assistantchat.new_main_title"));
    expect(dialog?.querySelector('[role="switch"]')?.getAttribute("aria-checked")).toBe("true");
    expect(navigate).not.toHaveBeenCalled();
  });

  it("opens the appropriate new-chat flow for a side chat or Agent", async () => {
    const side = { ...main, session_id: "side" };
    await show(side, true, false);
    await act(async () => action(t("chat-chatview.new_chat")).click());
    expect(navigate).toHaveBeenCalledWith({ name: "persona-new" });
    await show({ ...side, session_id: "agent" }, false, false);
    await act(async () => action(t("chat-contextrestart.new_agent")).click());
    expect(openNewAgent).toHaveBeenCalledWith({ machineId: "hub", cwd: "/project" });
  });

  it("remembers a dismissal across remounts without dismissing another session", async () => {
    await show(main, true, true);
    await act(async () => notice()!.querySelector<HTMLButtonElement>('button[aria-label]')!.click());
    expect(notice()).toBeNull();
    await act(async () => root.render(null));
    await show(main, true, true);
    expect(notice()).toBeNull();
    await show({ ...main, session_id: "side" }, true, false);
    expect(notice()).not.toBeNull();
  });

  it("stays hidden while compacting or when the main action is unavailable", async () => {
    await show({ ...main, compaction: { state: "running", started_at: "2026-01-01T00:00:00Z" } }, true, true);
    expect(notice()).toBeNull();
    getState().features = [];
    await show(main, true, true);
    expect(notice()).toBeNull();
    await show({ ...main, archived: true }, true, false);
    expect(notice()).toBeNull();
  });
});
