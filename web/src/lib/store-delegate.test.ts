// The socket's side of the assistant answering questions first: a card it is still looking at is replaced by id, a
// question it answered stays with what it answered and why, and a changed answer follows it.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { connect, dropDelegated, flushPushes, getState, noteReanswer, reset } from "./store";
import type { Interaction } from "./types";

class FakeSocket {
  static all: FakeSocket[] = [];
  readyState = 0;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onclose: ((ev: { code: number }) => void) | null = null;
  constructor() {
    FakeSocket.all.push(this);
  }
  send() {}
  close() {
    this.readyState = 3;
  }
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
  push(msg: Record<string, unknown>) {
    this.onmessage?.({ data: JSON.stringify(msg) });
    flushPushes();
  }
}

beforeEach(() => {
  vi.useFakeTimers();
  FakeSocket.all = [];
  vi.stubGlobal("WebSocket", FakeSocket);
  vi.stubGlobal("location", { protocol: "http:", host: "hub" });
  vi.stubGlobal("localStorage", { getItem: () => "tok", setItem: () => {}, removeItem: () => {} });
  vi.stubGlobal("window", { setTimeout: (f: () => void, ms: number) => setTimeout(f, ms), clearTimeout: (t: number) => clearTimeout(t) });
  vi.stubGlobal("fetch", vi.fn().mockImplementation(() => Promise.resolve(new Response("{}", { status: 200 }))));
});

afterEach(() => {
  reset();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function online() {
  connect();
  const s = FakeSocket.all[FakeSocket.all.length - 1];
  s.open();
  s.push({ type: "hello", server_version: "v1", event_stream: "s1", event_seq: 1 });
  return s;
}

const question: Interaction = {
  id: "ix1",
  machine_id: "m1",
  session_id: "s1",
  kind: "question",
  questions: [{ question: "Which driver?", header: "", multi_select: false, options: [{ label: "pgx", description: "" }, { label: "lib/pq", description: "" }] }],
  created_at: "2025-01-01T00:00:00Z",
};

describe("questions the assistant answers first", () => {
  it("replaces the card it was looking at by id when it leaves the question to the user", () => {
    const s = online();
    s.push({ type: "interaction", interaction: { ...question, delegating: true } });
    expect(getState().interactions).toEqual([{ ...question, delegating: true }]);
    s.push({ type: "interaction", interaction: { ...question, delegate: { recommend: "pgx", reason: "Needs your call" } } });
    expect(getState().interactions).toEqual([{ ...question, delegate: { recommend: "pgx", reason: "Needs your call" } }]);
  });

  it("keeps a question the assistant answered, with its answers and reason, and follows a changed answer", () => {
    const s = online();
    s.push({ type: "interaction", interaction: { ...question, delegating: true } });
    s.push({ type: "interaction_resolved", id: "ix1", answered_by: "assistant", reason: "The repo uses pgx", answers: { "Which driver?": "pgx" } });
    expect(getState().interactions).toEqual([]);
    expect(getState().delegated.ix1).toMatchObject({ ix: { ...question, delegating: true }, answers: { "Which driver?": "pgx" }, reason: "The repo uses pgx" });
    expect(getState().delegated.ix1.at).toBeGreaterThan(0);

    s.push({ type: "interaction_reanswered", id: "ix1", resolution: { answers: { "Which driver?": "lib/pq" } } });
    expect(getState().reanswered.ix1.answers).toEqual({ "Which driver?": "lib/pq" });

    dropDelegated("ix1");
    expect(getState().delegated.ix1).toBeUndefined();
    expect(getState().reanswered.ix1).toBeDefined(); // the session's history still shows the change
  });

  it("does not keep a card the user answered, or one it never saw", () => {
    const s = online();
    s.push({ type: "interaction", interaction: { ...question, delegating: true } });
    s.push({ type: "interaction_resolved", id: "ix1" });
    s.push({ type: "interaction_resolved", id: "gone", answered_by: "assistant", reason: "r", answers: {} });
    expect(getState().delegated).toEqual({});
  });

  it("notes this page's own change of answer, and a login forgets them all", () => {
    const s = online();
    s.push({ type: "interaction", interaction: question });
    s.push({ type: "interaction_resolved", id: "ix1", answered_by: "assistant", reason: "r", answers: { "Which driver?": "pgx" } });
    noteReanswer("ix1", { "Which driver?": "lib/pq" });
    expect(getState().reanswered.ix1.answers).toEqual({ "Which driver?": "lib/pq" });
    noteReanswer("ix1", { "Which port?": "5433" }); // the hub sends only what changed
    expect(getState().reanswered.ix1.answers).toEqual({ "Which driver?": "lib/pq", "Which port?": "5433" });
    reset();
    expect(getState().delegated).toEqual({});
    expect(getState().reanswered).toEqual({});
  });
});
