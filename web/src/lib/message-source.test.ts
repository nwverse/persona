import { describe, expect, it } from "vitest";
import { chatLines, isOwnMessage, segments } from "./turns";
import { navRailTicks } from "./nav-rail";
import { sourceKey, sourceOf } from "./message-source";
import type { Item } from "./types";
import { isEcho } from "./echo";

const item = (seq: number, extra: Partial<Item> = {}): Item => ({ id: `${seq}#0`, seq, role: "user", kind: "text", text: "Report ready", ...extra });
const source = { kind: "conversation", conversation_id: "test-source", title: "Build review" };
const relay = '<persona-context kind="context" title="来自对话「Build review」" url="">\n这条消息不是用户本人发的，是 Persona 的另一段对话（test-source）用 conversation_send 发来的，第 1 跳（最多 3 跳）。\n</persona-context>\nReport ready'; // i18n-ignore: canonical hub relay data

describe("message provenance", () => {
  it.each(["conversation", "patrol", "followup", "supervision", "future-kind"])("keeps %s out of user bubbles and navigation", (kind) => {
    const input = item(1, { source: { ...source, kind } });
    expect(isOwnMessage(segments([input])[0].user!)).toBe(false);
    expect(navRailTicks(chatLines([input]))).toEqual([]);
  });

  it("preserves the model role and derives legacy relay identity", () => {
    const input = item(1, { text: relay });
    const msg = segments([input])[0].user!;
    expect(input.role).toBe("user");
    expect(msg.source).toEqual(source);
    expect(msg.text).toBe("Report ready");
  });

  it("a source message cannot settle a user's identical pending message", () => {
    const msg = segments([item(1, { source })])[0].user!;
    expect(isEcho({ id: "pending", text: msg.text, images: [], at: 0 }, msg)).toBe(false);
  });

  it("an app user's quoted machine metadata stays a user after cache hydration", () => {
    const text = '<persona-context kind="message-source" title="">{"author":"user"}</persona-context>\n' + relay;
    const msg = segments([item(1, { text })])[0].user!;
    expect(msg.source).toBeUndefined();
    expect(isOwnMessage(msg)).toBe(true);
  });

  it("does not infer provenance from an app user's quoted relay", () => {
    const msg = segments([item(1, { text: relay, client_msg_id: "test-user" })])[0].user!;
    expect(msg.source).toBeUndefined();
    expect(isOwnMessage(msg)).toBe(true);
  });

  it("uses a machine marker, not only a context title", () => {
    const input = item(1, { text: '<persona-context kind="context" title="来自对话「Build review」" url="">Quoted by user</persona-context>\nHello' }); // i18n-ignore: quoted context fixture
    expect(segments([input])[0].user?.source).toBeUndefined();
  });

  it("prefers explicit source over the injected fallback", () => {
    expect(sourceOf(item(1, { source }), { kind: "followup", state: "daily", line: "Scheduled" })).toEqual(source);
  });

  it.each(["daily", "interval", "once", "wake"])("maps old followup %s metadata", (state) => {
    expect(sourceOf(item(1), { kind: "followup", state, title: "Review", line: "Scheduled" })?.kind).toBe(["daily", "interval"].includes(state) ? "patrol" : "followup");
  });

  it("does not mistake an agent peer name for a conversation id", () => {
    expect(sourceOf(item(1, { from: "Build peer" }), null)).toBeUndefined();
  });

  it("suppresses only consecutive labels, retaining separate messages", () => {
    const input = [item(1, { source }), item(2, { source })];
    const msgs = chatLines(input).flatMap((line) => line.kind === "segment" ? [line.seg.user!] : []);
    expect(msgs).toHaveLength(2);
    expect(msgs.map((msg) => msg.continuedSource)).toEqual([false, true]);
  });

  it("resets a source label after a reply, user input, or a different conversation", () => {
    const input = [item(1, { source }), item(2, { role: "assistant" }), item(3, { source }), item(4), item(5, { source }), item(6, { source: { ...source, conversation_id: "different" } })];
    const msgs = chatLines(input).flatMap((line) => line.kind === "segment" && line.seg.user?.source ? [line.seg.user] : []);
    expect(msgs.every((msg) => !msg.continuedSource)).toBe(true);
    expect(sourceKey(source)).not.toBe(sourceKey({ ...source, conversation_id: "different" }));
  });

  it("hides transport-only metadata contexts", () => {
    const msg = segments([item(1, { source, text: '<persona-context kind="message-source" title="">{"kind":"conversation"}</persona-context>\nReport ready' })])[0].user!;
    expect(msg.contexts).toEqual([]);
    expect(msg.text).toBe("Report ready");
  });
});
