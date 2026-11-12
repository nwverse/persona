// @vitest-environment jsdom
// The model error a chat stopped on is cindy's banner over the composer, with Retry and ✕, and is not drawn in the chat
// at the same time (#5313). Dismissed (remembered in this browser), followed by anything, or while the Agent works, it
// is the record in the chat, without Retry. Retry puts the banner away at once; the same error again is a new banner.
import { act, useEffect, useReducer } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Item, Session } from "../../lib/types";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  g.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  g.IntersectionObserver = class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } };
});

const IDLE: Session = { machine_id: "hub", session_id: "c1", title: "Conversation", cwd: "/code", status: "idle" };
const captured = vi.hoisted(() => ({ session: undefined as Session | undefined }));

vi.mock("./Composer", async (importOriginal) => ({
  ...await importOriginal<typeof import("./Composer")>(),
  Composer: () => null,
}));
// one transcript for every render; a test swaps its items and draws again
const transcript = vi.hoisted(() => ({ items: [] as unknown[], loading: false, firstRead: true, error: "", hasMore: false, streaming: "" }));
vi.mock("./useTranscript", () => ({ useTranscript: () => transcript }));
vi.mock("../../lib/store", async (importOriginal) => {
  const store = await importOriginal<typeof import("../../lib/store")>();
  return {
    ...store,
    getSession: () => captured.session,
    useStore: <T,>(select: (s: ReturnType<typeof store.getState>) => T) => {
      const [, changed] = useReducer((n: number) => n + 1, 0);
      useEffect(() => store.subscribe(changed), []);
      return select({ ...store.getState(), sessions: { hub: captured.session ? [captured.session] : [] } });
    },
  };
});
vi.mock("../../lib/panel", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/panel")>(),
  usePanel: () => null,
}));
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
const { getState, setOutbox } = await import("../../lib/store");
const { resetOutboxClaimsForTests, resetRestoredForTests } = await import("../../lib/outbox");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const OVERLOADED = "API Error: 529 overloaded";
const msg = (id: string, seq: number, role: "user" | "assistant", text: string, extra: Partial<Item> = {}) =>
  ({ id, seq, role, kind: "text", text, ts: "2026-11-12T08:00:00Z", ...extra }) as Item;
const failedTurn = [msg("u1", 1, "user", "接着把命名改完"), msg("e1", 2, "assistant", OVERLOADED)];

const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { "Content-Type": "application/json" } });
let bodies: { client_msg_id: string; text: string }[];
let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};
const draw = async (assistant = false) => {
  act(() => root.render(<ChatView mid="hub" sid="c1" assistant={assistant} />));
  await flush();
};
const banner = () => host.querySelector<HTMLElement>("[data-chat-dock] [data-error-banner]");
const inChat = () => host.querySelector<HTMLElement>("[data-chat-content] [data-error-notice]");
const button = (scope: Element | null, label: string) => [...(scope?.querySelectorAll("button") ?? [])].find((b) => b.textContent === label);

beforeEach(() => {
  localStorage.clear();
  resetRestoredForTests();
  resetOutboxClaimsForTests();
  for (const key of Object.keys(getState().outbox)) setOutbox(key, []);
  captured.session = IDLE;
  transcript.items = failedTurn;
  bodies = [];
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    if (String(input).endsWith("/messages") && init?.body) bodies.push(JSON.parse(String(init.body)));
    return Promise.resolve(json({}));
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

describe("the error an Agent's chat stopped on", () => {
  it("is the banner over the composer, with Retry and ✕, and not in the chat", async () => {
    await draw();
    expect(banner()).not.toBeNull();
    expect(banner()!.textContent).toContain("重试");
    expect(banner()!.querySelector('[aria-label="关闭错误提示"]')).not.toBeNull();
    expect(inChat()).toBeNull();
    // the question that failed stays in the chat
    expect(host.querySelector("[data-chat-content]")!.textContent).toContain("接着把命名改完");
  });

  it("✕ puts it back in the chat without Retry, and the chat remembers it on this device", async () => {
    await draw();
    act(() => banner()!.querySelector<HTMLButtonElement>('[aria-label="关闭错误提示"]')!.click());
    await flush();
    expect(banner()).toBeNull();
    expect(inChat()).not.toBeNull();
    expect(button(inChat(), "重试")).toBeUndefined();
    act(() => root.unmount());
    root = createRoot(host);
    await draw();
    expect(banner()).toBeNull();
    expect(inChat()).not.toBeNull();
  });

  it("is a record in the chat once anything comes after it", async () => {
    transcript.items = [...failedTurn, msg("u2", 3, "user", "换个思路试试"), msg("a2", 4, "assistant", "好的，换成按模块改名。")];
    await draw();
    expect(banner()).toBeNull();
    expect(inChat()).not.toBeNull();
    expect(button(inChat(), "重试")).toBeUndefined();
  });

  it("is not a banner while the Agent works", async () => {
    captured.session = { ...IDLE, status: "working" };
    await draw();
    expect(banner()).toBeNull();
    expect(inChat()).not.toBeNull();
    expect(button(inChat(), "重试")).toBeUndefined();
  });

  it("Retry sends 继续 and puts the banner away at once; the same error after it is a new banner", async () => {
    await draw();
    await act(async () => button(banner(), "重试")!.click());
    await flush();
    expect(bodies.map((b) => b.text)).toEqual(["继续"]);
    expect(banner()).toBeNull();
    // the echo and the same error again: the line folds with a higher count, a failure to say again
    transcript.items = [...failedTurn, msg("u2", 3, "user", "继续", { client_msg_id: bodies[0].client_msg_id }), msg("e2", 4, "assistant", OVERLOADED)];
    await draw();
    expect(banner()).not.toBeNull();
    expect(inChat()).toBeNull();
  });

  it("leaves the host's failure note to say why the turn stopped: one card over the composer", async () => {
    captured.session = { ...IDLE, failure_reason: "⚠️ API Error: 529 overloaded" };
    await draw();
    expect(host.querySelector('[data-chat-dock] [data-testid="chat-failure"]')).not.toBeNull();
    expect(banner()).toBeNull();
    expect(inChat()).not.toBeNull();
  });
});

describe("the error the Persona conversation stopped on", () => {
  it("is the banner over the composer too, and not in the chat", async () => {
    await draw(true);
    expect(banner()).not.toBeNull();
    expect(banner()!.textContent).toContain("重试");
    expect(inChat()).toBeNull();
    act(() => banner()!.querySelector<HTMLButtonElement>('[aria-label="关闭错误提示"]')!.click());
    await flush();
    expect(banner()).toBeNull();
    expect(inChat()).not.toBeNull();
    expect(button(inChat(), "重试")).toBeUndefined();
  });
});
