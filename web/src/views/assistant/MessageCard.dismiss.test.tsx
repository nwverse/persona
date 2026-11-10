// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => vi.fn());
const toast = vi.hoisted(() => vi.fn());
vi.mock("../../lib/api", async (orig) => ({ ...(await orig<typeof import("../../lib/api")>()), api }));
vi.mock("../../ui/atoms", async (orig) => ({ ...(await orig<typeof import("../../ui/atoms")>()), toast }));
import { ApiError } from "../../lib/api";
import { assistantEntries, messageWaits } from "../../lib/assistant-entries";
import { cardDismissed, dismissMessage, getFeedState, later, loadFeed, sendMessage, undoDismissMessage, useFeed, type FeedItem } from "../../lib/feed";
import { chooseManager, noteConversation, resetManagerChoice } from "../../lib/manager-choice";
import { reset } from "../../lib/store";
import { FeedLineView } from "./Tasks";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root;
let host: HTMLDivElement;
const draft: FeedItem = { id: "demo-draft", conversation: "demo-main", at: "2026-11-10T08:00:00Z", kind: "message", ref: "demo-inbox", body: { draft: "The synthetic checks are ready.", text: "Are the checks ready?" } };
const dismissed: FeedItem = { ...draft, body: { ...draft.body, dismissed_at: "2026-11-10T08:01:00Z", dismissal_id: "demo-receipt" } };
const needs = () => assistantEntries({ conversation: draft.conversation, todos: [], sessions: [], interactions: [], plans: {}, feed: getFeedState().feeds[draft.conversation] ?? [], deferred: getFeedState().later }).needsYou;
const button = (name: string) => [...host.querySelectorAll("button")].find((b) => b.textContent?.trim() === name)!;
function Pending() {
  const feed = useFeed((s) => s.feeds[draft.conversation] ?? []);
  return <>{feed.filter((it) => messageWaits(it)).map((item) => <FeedLineView key={item.id} item={item} />)}</>;
}

beforeEach(() => {
  api.mockReset();
  toast.mockReset();
  reset();
  resetManagerChoice();
  Object.assign(getFeedState(), { feeds: { [draft.conversation]: [draft] }, later: {}, busy: {} });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  resetManagerChoice();
  vi.restoreAllMocks();
});

describe("dismiss a task-progress reply draft", () => {
  it("offers one primary Send and secondary Later / Don't handle, with Undo", async () => {
    api.mockResolvedValueOnce({ item: dismissed, dismissal_id: "demo-receipt" });
    act(() => root.render(<Pending />));
    expect(button("发送").className).toContain("bg-accent");
    expect(button("稍后").className).not.toContain("bg-accent");
    expect(button("不处理").className).not.toContain("bg-accent");
    await act(async () => button("不处理").click());
    expect(api.mock.calls).toEqual([["/assistant/feed/demo-draft/dismiss", { method: "POST", json: { draft: draft.body!.draft }, headers: undefined }]]);
    expect(needs()).toHaveLength(0);
    expect(host.querySelector("textarea")).toBeNull();
    expect(toast).toHaveBeenCalledWith("已选择不处理，草稿没有发送", "ok", expect.objectContaining({ label: "撤销" }));
    const undo = toast.mock.calls[0][2];
    api.mockResolvedValueOnce({ item: draft });
    await act(async () => undo.run());
    expect(api.mock.calls[1]).toEqual(["/assistant/feed/demo-draft/undo-dismiss", { method: "POST", json: { dismissal_id: "demo-receipt" }, headers: undefined }]);
    expect(host.querySelector("textarea")).not.toBeNull();
    expect(needs()).toHaveLength(1);
    expect(cardDismissed(getFeedState().feeds[draft.conversation][0])).toBeUndefined();
  });

  it("shares synchronous admission with Send and never sends a dismissed stale card", async () => {
    let finish!: (value: unknown) => void;
    api.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    const first = dismissMessage(draft);
    await Promise.all([sendMessage(draft, "Do not send this"), dismissMessage(draft)]);
    expect(api).toHaveBeenCalledTimes(1);
    expect(getFeedState().busy[draft.id]).toBe(true);
    finish({ item: dismissed, dismissal_id: "demo-receipt" });
    await first;
    await sendMessage(draft, "Do not send this either");
    expect(api).toHaveBeenCalledTimes(1);
    expect(getFeedState().busy[draft.id]).toBe(false);
    expect(cardDismissed(getFeedState().feeds[draft.conversation][0])).toEqual({ at: "2026-11-10T08:01:00Z", id: "demo-receipt" });
  });

  it("does not dismiss while Send is admitted", async () => {
    let finish!: (value: unknown) => void;
    api.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    const sending = sendMessage(draft, "Send this once");
    expect(await dismissMessage(draft)).toBeUndefined();
    expect(api).toHaveBeenCalledTimes(1);
    finish({});
    await sending;
    expect(api.mock.calls[0][0]).toContain("/reply");
  });

  it("keeps the card on failure and permits retry without modifying the task", async () => {
    api.mockRejectedValueOnce(new ApiError(409, "Synthetic stale draft", {}));
    act(() => root.render(<Pending />));
    await act(async () => button("不处理").click());
    expect(needs()).toHaveLength(1);
    expect(button("不处理").disabled).toBe(false);
    expect(toast).toHaveBeenCalledWith(expect.stringContaining("Synthetic stale draft"), "error");
    expect(getFeedState().feeds[draft.conversation][0]).toEqual(draft);
    api.mockResolvedValueOnce({ item: dismissed, dismissal_id: "demo-receipt" });
    await act(async () => button("不处理").click());
    expect(needs()).toHaveLength(0);
    expect(api.mock.calls.every(([path]) => path.endsWith("/dismiss"))).toBe(true);
  });

  it("keeps a dismissed card gone after reload while new same-ref progress is eligible", async () => {
    Object.assign(getFeedState(), { feeds: {} });
    const next: FeedItem = { ...draft, id: "demo-new-progress", at: "2026-11-10T08:02:00Z", body: { ...draft.body, draft: "New progress is ready." } };
    api.mockResolvedValueOnce({ items: [dismissed, next] });
    await loadFeed(draft.conversation);
    expect(needs().map((it) => it.key)).toEqual(["message:demo-new-progress"]);
    later(dismissed.id);
    expect(needs().map((it) => it.key)).toEqual(["message:demo-new-progress"]);
  });

  it("routes Dismiss and Undo to the card's manager even after selecting another assistant", async () => {
    noteConversation(draft.conversation, "demo-owner");
    chooseManager("demo-other");
    api.mockImplementation(async (path: string) => path.endsWith("/undo-dismiss") ? { item: draft } : path.endsWith("/dismiss") ? { item: dismissed, dismissal_id: "demo-receipt" } : {});
    await dismissMessage(draft);
    await undoDismissMessage(draft, "demo-receipt");
    expect(api.mock.calls.filter(([path]) => path.startsWith("/assistant/feed/")).map(([, opts]) => opts.headers)).toEqual([{ "X-Persona-Manager": "demo-owner" }, { "X-Persona-Manager": "demo-owner" }]);
  });

  it("Later hides only this card locally, not a later draft for the same Inbox reference", () => {
    const next = { ...draft, id: "demo-next-local", body: { ...draft.body, draft: "A later progress draft" } };
    Object.assign(getFeedState(), { feeds: { [draft.conversation]: [draft, next] } });
    later(draft.id);
    expect(needs().map((it) => it.key)).toEqual(["message:demo-next-local"]);
    expect(cardDismissed(draft)).toBeUndefined();
    expect(api).not.toHaveBeenCalled();
  });

  it("renders a persisted dismissal as not sent, without an editor or actions", () => {
    act(() => root.render(<FeedLineView item={dismissed} />));
    expect(host.textContent).toContain("已选择不处理，草稿没有发送");
    expect(host.querySelector("textarea")).toBeNull();
    expect(host.querySelector("button")).toBeNull();
    expect(messageWaits(dismissed)).toBe(false);
  });

  it("keeps a newer pending card when Undo is refused; no optimistic resurrection", async () => {
    Object.assign(getFeedState(), { feeds: { [draft.conversation]: [dismissed] } });
    api.mockRejectedValueOnce(new ApiError(409, "Synthetic newer progress", {}));
    await expect(undoDismissMessage(draft, "demo-receipt")).rejects.toThrow("Synthetic newer progress");
    expect(needs()).toHaveLength(0);
    expect(getFeedState().feeds[draft.conversation][0]).toEqual(dismissed);
    expect(getFeedState().busy[draft.id]).toBe(false);
  });
});
