// @vitest-environment jsdom
// Manual mark as read (#7356): with the switch on, an Unread chat's header has a Mark as read button (a real button,
// named for screen readers, reached by Tab) that marks it read as the user's own; off, or once read, there is none.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Item, Session } from "../../lib/types";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  HTMLElement.prototype.scrollTo = () => {};
  g.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  g.IntersectionObserver = class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } };
});

const captured = vi.hoisted(() => ({
  session: { machine_id: "hub", session_id: "c1", title: "Conversation", cwd: "/code", status: "done", state: "unread" } as Session,
  send: undefined as undefined | ((out: { text: string; images: [] }) => Promise<boolean>),
  views: [] as string[],
  released: [] as string[],
  read: [] as string[],
}));

vi.mock("./Composer", async (importOriginal) => ({ ...await importOriginal<typeof import("./Composer")>(), Composer: ({ onSend }: { onSend: NonNullable<typeof captured.send> }) => { captured.send = onSend; return null; } }));
const transcript = vi.hoisted(() => ({ items: [] as Item[], loading: false, error: "", hasMore: false, streaming: "" }));
vi.mock("./useTranscript", () => ({ useTranscript: () => transcript }));
vi.mock("../../lib/store", async (importOriginal) => {
  const store = await importOriginal<typeof import("../../lib/store")>();
  return {
    ...store,
    getSession: () => captured.session,
    useStore: <T,>(select: (s: ReturnType<typeof store.getState>) => T) => select({ ...store.getState(), sessions: { hub: [captured.session] } }),
    markRead: (mid: string, sid: string) => captured.read.push(`${mid}/${sid}`),
    viewSession: (mid: string, sid: string) => {
      captured.views.push(`${mid}/${sid}`);
      return () => captured.released.push(`${mid}/${sid}`);
    },
  };
});
vi.mock("../../lib/panel", async (importOriginal) => ({ ...await importOriginal<typeof import("../../lib/panel")>(), usePanel: () => null }));
vi.mock("../../lib/router", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/router")>(),
  useRoute: () => ({ name: "session", mid: "hub", id: "c1" }),
}));
vi.mock("../assistant/Persona", () => ({ usePersona: () => ({ name: "Persona" }) }));
vi.mock("../assistant/AssistantChat", () => ({
  AssistantFace: () => null,
  CompactionStrip: () => null,
  useAssistantMenu: () => ({ items: [], dialogs: null }),
}));
vi.mock("../assistant/Tasks", () => ({
  DayLine: () => null,
  FeedLineView: () => null,
  QuickResultCard: () => null,
  RouteChip: () => null,
  AssistantActionBar: () => null,
  TaskDock: () => null,
  TaskQuestions: () => null,
}));

const { ChatView } = await import("./ChatView");
const { setManualReadLocal } = await import("../../lib/manual-read");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  transcript.items = [];
  captured.send = undefined;
  captured.views = [];
  captured.released = [];
  captured.read = [];
  captured.session = { ...captured.session, state: "unread", status: "done" };
  localStorage.clear();
  vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response("{}", { status: 200 }))));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  setManualReadLocal(false);
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

const button = () => host.querySelector<HTMLButtonElement>('[data-testid="chat-mark-read"]');

describe("Mark as read in the chat's header", () => {
  it("is not there with the switch off (opening the chat reads it)", () => {
    act(() => root.render(<ChatView mid="hub" sid="c1" inPage />));
    expect(button()).toBeNull();
    expect(captured.views).toEqual(["hub/c1"]);
  });

  it("marks an Unread chat read as the user's own, from a keyboard-reachable named button", () => {
    setManualReadLocal(true);
    act(() => root.render(<ChatView mid="hub" sid="c1" inPage />));
    const b = button();
    expect(b?.tagName).toBe("BUTTON");
    expect(b?.getAttribute("aria-label")).toBeTruthy();
    expect(b?.tabIndex).not.toBe(-1);
    act(() => b!.click());
    expect(captured.read).toEqual(["hub/c1"]);
  });

  it("is gone once the chat is read, and back when a new turn makes it Unread", () => {
    setManualReadLocal(true);
    captured.session = { ...captured.session, state: "idle", status: "done" };
    act(() => root.render(<ChatView mid="hub" sid="c1" inPage />));
    expect(button()).toBeNull();
    captured.session = { ...captured.session, state: "unread", status: "done" };
    act(() => root.render(<ChatView mid="hub" sid="c1" inPage />));
    expect(button()).not.toBeNull();
  });
});
