// @vitest-environment jsdom
// An agent row's structured card carries the source details removed from the row itself (#9769).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../i18n";
import { sessionAgentName } from "../lib/agent-kind";
import { sourceName } from "../lib/agent-groups";
import { modelShort } from "../lib/context-usage";
import { ago } from "../lib/format";
import type { Session } from "../lib/types";
import { TIP_DELAY } from "./TodoRequestGraphTip";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  const kept = new Map<string, string>();
  g.localStorage = { getItem: (k: string) => kept.get(k) ?? null, setItem: (k: string, v: string) => void kept.set(k, v), removeItem: (k: string) => void kept.delete(k) };
});
vi.mock("./assistant/Persona", async (importOriginal) => ({ ...(await importOriginal<typeof import("./assistant/Persona")>()), PersonaAvatar: () => null }));

const { SessionRow } = await import("./Sidebar");
const NOW = new Date("2026-11-17T12:00:00Z");
const terminal: Session = {
  machine_id: "mac", session_id: "s1", title: "Release work", cwd: "/code", status: "working", state: "working",
  updated_at: "2026-11-17T11:59:00Z", pane_id: "w1:p1", slot_id: "w1:t2", slot_name: "Release Review",
  agent: "claude", model: "claude-opus-5-5",
};
const desktop: Session = {
  ...terminal, session_id: "s2", title: "Desktop work", pane_id: undefined, slot_id: undefined, slot_name: undefined,
  host: "paseo", agent: "paseo", launcher: "cursor", model: "claude-sonnet-5-5",
};
const runtime: Session = {
  ...terminal, session_id: "s3", title: "Runtime work", pane_id: undefined, slot_id: undefined, slot_name: undefined,
  source_kind: "runtime", source_instance_id: "custom-engine", agent: "custom", agent_label: "Custom Agent", model: "model-x",
};

let host: HTMLDivElement;
let root: Root;
const tip = () => document.querySelector<HTMLElement>("[data-session-details]");
const fields = () => Object.fromEntries([...tip()!.querySelectorAll("dl > div")].map((row) => [row.querySelector("dt")!.textContent, row.querySelector("dd")!.textContent]));
const enter = async (el: Element) => {
  act(() => { el.dispatchEvent(new PointerEvent("pointerover", { bubbles: true, pointerType: "mouse", relatedTarget: document.body })); });
  await act(() => vi.dynamicImportSettled());
};
const leave = (el: Element) => act(() => { el.dispatchEvent(new PointerEvent("pointerout", { bubbles: true, pointerType: "mouse", relatedTarget: document.body })); });
const settle = async () => { for (let i = 0; i < 20 && !tip(); i++) await act(async () => { await Promise.resolve(); }); };

function show(s: Session) {
  act(() => root.render(<SessionRow s={s} k={`s:${s.machine_id}/${s.session_id}`} reg={() => () => {}} hover={() => {}} active={false}
    to={{ name: "session", mid: s.machine_id, sid: s.session_id }} go={() => () => {}} />));
  return host.querySelector<HTMLAnchorElement>("a")!;
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(NOW);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.querySelectorAll("[data-graph-tip]").forEach((el) => el.remove());
  vi.useRealTimers();
});

describe("an agent row's structured details card", () => {
  it("opens after the pointer rests, names every field, and closes on pointer leave", async () => {
    const anchor = show(terminal);
    await enter(anchor);
    act(() => { vi.advanceTimersByTime(TIP_DELAY - 1); });
    expect(tip()).toBeNull();
    await act(async () => { vi.advanceTimersByTime(1); });
    await settle();
    expect(tip()!.textContent).toContain("Release work");
    expect(fields()).toEqual({
      [t("chat-sessioninfo.machine")]: "mac",
      [t("sidebar.nav_terminal")]: "herdr · Release Review",
      [t("chat-export.agent")]: `${sessionAgentName(terminal)} · ${modelShort(terminal.model)}`,
      [t("chat-sessioninfo.last_activity")]: ago(terminal.updated_at!, NOW.getTime(), "narrow"),
    });
    leave(anchor);
    expect(tip()).toBeNull();
  });

  it.each([
    ["desktop", desktop, `${sourceName("desktop")} · Paseo`],
    ["runtime", runtime, "custom-engine"],
  ])("covers a %s session", async (_kind, session, terminalName) => {
    const anchor = show(session);
    await enter(anchor);
    await act(async () => { vi.advanceTimersByTime(TIP_DELAY); });
    await settle();
    expect(fields()[t("sidebar.nav_terminal")]).toBe(terminalName);
    expect(fields()[t("chat-export.agent")]).toBe(`${sessionAgentName(session)} · ${modelShort(session.model)}`);
  });

  it("opens at once for keyboard focus, links its accessible description, and closes on Escape or blur", async () => {
    const anchor = show(terminal);
    act(() => { document.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true })); anchor.focus(); });
    const tooltip = document.querySelector<HTMLElement>('[role="tooltip"]')!;
    expect(tooltip).not.toBeNull();
    expect(anchor.getAttribute("aria-describedby")).toBe(tooltip.id);
    await settle();
    expect(tooltip.contains(tip())).toBe(true);
    act(() => { window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })); });
    expect(tip()).toBeNull();
    expect(anchor.hasAttribute("aria-describedby")).toBe(false);
    show({ ...terminal, title: "Updated after Escape" });
    await act(() => vi.dynamicImportSettled());
    expect(tip()).toBeNull();
    expect(anchor.hasAttribute("aria-describedby")).toBe(false);
    act(() => anchor.blur());
    act(() => { document.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true })); anchor.focus(); });
    expect(tip()).not.toBeNull();
    act(() => anchor.blur());
    expect(tip()).toBeNull();
  });

  it.each(["scroll", "resize"])("cancels a pending card on %s", async (eventName) => {
    const anchor = show(terminal);
    await enter(anchor);
    await act(async () => { vi.advanceTimersByTime(TIP_DELAY - 1); });
    expect(tip()).toBeNull();
    act(() => { window.dispatchEvent(new Event(eventName)); });
    await act(async () => { vi.advanceTimersByTime(TIP_DELAY * 2); });
    expect(tip()).toBeNull();
    expect(anchor.hasAttribute("aria-describedby")).toBe(false);
  });

  it("keeps a card that is up, with the new content, when the session updates", async () => {
    const anchor = show(terminal);
    await enter(anchor);
    await act(async () => { vi.advanceTimersByTime(TIP_DELAY); });
    await settle();
    expect(tip()!.textContent).toContain("Release work");
    show({ ...terminal, title: "Release work, updated", state: "needs_you" });
    await act(() => vi.dynamicImportSettled());
    // no hide and no second delay: the card is up at once, with what the update brought
    expect(tip()).not.toBeNull();
    expect(tip()!.textContent).toContain("Release work, updated");
    expect(tip()!.textContent).toContain(t("lib-state-labels.needs_you"));
    expect(anchor.hasAttribute("aria-describedby")).toBe(true);
  });

  it.each(["scroll", "resize"])("stays closed after %s when the session updates", async (eventName) => {
    const anchor = show(terminal);
    await enter(anchor);
    await act(async () => { vi.advanceTimersByTime(TIP_DELAY); });
    await settle();
    expect(tip()).not.toBeNull();
    expect(anchor.hasAttribute("aria-describedby")).toBe(true);
    act(() => { window.dispatchEvent(new Event(eventName)); });
    expect(tip()).toBeNull();
    expect(anchor.hasAttribute("aria-describedby")).toBe(false);
    show({ ...terminal, title: `Updated after ${eventName}` });
    await act(() => vi.dynamicImportSettled());
    await act(async () => { vi.advanceTimersByTime(TIP_DELAY * 2); });
    expect(tip()).toBeNull();
    expect(anchor.hasAttribute("aria-describedby")).toBe(false);
  });
});
