// @vitest-environment jsdom
// Conversation tasks on a phone (#11511): under 640 px the header's button gives way, so the title keeps its room, and
// ⋯ offers the panel instead; from 640 px the button is there and ⋯ is as it was.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Item, Session } from "../../lib/types";

// a phone (under 640 px, touch) or a desktop (a mouse), as the test says
const media = vi.hoisted(() => {
  const state = { phone: false };
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = (query: string) => ({
    media: query,
    get matches() { return state.phone ? /max-width: (639|767|1023)px|hover: none|pointer: coarse/.test(query) : /pointer: *fine|hover: *hover/.test(query); },
    addEventListener() {},
    removeEventListener() {},
  });
  g.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  g.IntersectionObserver = class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } };
  return state;
});

const transcript = vi.hoisted(() => ({ items: [] as Item[], loading: false, error: "", hasMore: false, streaming: "" }));
vi.mock("./useTranscript", () => ({ useTranscript: () => transcript }));
vi.mock("../assistant/Persona", () => ({ usePersona: () => ({ name: "Persona" }) }));

const { ChatView } = await import("./ChatView");
const { reset, upsertSession } = await import("../../lib/store");
const { closePanel, getPanel } = await import("../../lib/panel");
const { t } = await import("../../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const TASKS = t("sidepanel.conversation_tasks");
const agent = { machine_id: "mac", session_id: "s1", title: "Checks", cwd: "/code", status: "idle", state: "idle" } as Session;
const main = { machine_id: "hub", session_id: "c1", title: "Persona", status: "idle", state: "idle", assistant: true, agent: "claude" } as Session;
const reply = { id: "i1", seq: 1, role: "assistant", kind: "text", text: "Done.", ts: "2026-11-06T00:00:00Z" } as Item;

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};
const header = () => host.querySelector("header")!;
const tasksButton = () => header().querySelector<HTMLButtonElement>(`button[aria-label="${TASKS}"]`);
const menuRows = () => [...document.querySelectorAll<HTMLElement>('[role="menu"] [role^="menuitem"]')];
const tasksRow = () => menuRows().find((r) => r.textContent?.includes(TASKS));
const openMore = async () => {
  act(() => header().querySelector<HTMLButtonElement>(`button[aria-label="${t("chat-chatview.more_actions")}"]`)!.click());
  await flush();
  expect(menuRows().length).toBeGreaterThan(0);
};

beforeEach(() => {
  localStorage.clear();
  Object.assign(transcript, { items: [reply], hasMore: false });
  vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } })));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  act(() => closePanel());
  host.remove();
  reset();
  vi.unstubAllGlobals();
});

describe("conversation tasks in the header", () => {
  it("on a phone, the button gives way and ⋯ opens the panel", async () => {
    media.phone = true;
    upsertSession(agent);
    act(() => root.render(<ChatView mid="mac" sid="s1" inPage />));
    await flush();
    expect(tasksButton()!.className).toMatch(/(^| )hidden( |$)/);
    expect(tasksButton()!.className).toContain("sm:flex");
    await openMore();
    const row = tasksRow()!;
    expect(row).toBeDefined();
    act(() => row.click());
    await flush();
    expect(getPanel()).toMatchObject({ kind: "conversation-tasks", mid: "mac", sid: "s1" });
  });

  it("on a phone, the main conversation's menu (its title) has it too", async () => {
    media.phone = true;
    upsertSession(main);
    act(() => root.render(<ChatView mid="hub" sid="c1" assistant mainCache={{ scope: "test" }} onToggleSidebar={() => {}} />));
    await flush();
    expect(tasksButton()!.className).toContain("sm:flex");
    act(() => header().querySelector<HTMLButtonElement>("[data-assistant-name-menu]")!.click());
    await flush();
    act(() => tasksRow()!.click());
    await flush();
    expect(getPanel()).toMatchObject({ kind: "conversation-tasks", mid: "hub", sid: "c1" });
  });

  it("from 640 px, the button opens it and ⋯ has no row for it", async () => {
    media.phone = false;
    upsertSession(agent);
    act(() => root.render(<ChatView mid="mac" sid="s1" inPage />));
    await flush();
    await openMore();
    expect(tasksRow()).toBeUndefined();
    act(() => tasksButton()!.click());
    await flush();
    expect(getPanel()).toMatchObject({ kind: "conversation-tasks", mid: "mac", sid: "s1" });
  });
});

