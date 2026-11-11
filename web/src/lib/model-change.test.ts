import { describe, expect, it } from "vitest";
import { markModelChanges, replyModel, replyModelName } from "./model-change";
import { chatLines, type ChatLine } from "./turns";
import type { Item } from "./types";

const OPUS = "claude-opus-5-5";
const SONNET = "claude-sonnet-4-5-20250929";

const user = (seq: number, text = "next", extra: Partial<Item> = {}): Item => ({ id: `u${seq}#0`, seq, role: "user", kind: "text", text, ...extra });
const said = (seq: number, model?: string, text = "an answer"): Item => ({ id: `a${seq}#0`, seq, role: "assistant", kind: "text", text, ...(model ? { model } : {}) });
const call = (seq: number, model?: string): Item => ({ id: `t${seq}#0`, seq, role: "assistant", kind: "tool_use", tool_use_id: `call${seq}`, tool_name: "Bash", tool_input: { command: "ls" }, ...(model ? { model } : {}) });
const result = (seq: number): Item => ({ id: `r${seq}#0`, seq, role: "user", kind: "tool_result", tool_use_id: `call${seq - 1}`, text: "ok" });

/** Each line as the test reads it: a segment's user text (or "-") and its mark, other lines by kind. */
function marks(lines: ChatLine[]): string[] {
  return lines.map((l) => (l.kind === "segment" ? `${l.seg.user?.text ?? "-"}${l.seg.modelChange ? ` [${l.seg.modelChange.from} → ${l.seg.modelChange.to}]` : ""}` : l.kind));
}

describe("replyModelName", () => {
  it("names Claude's models as Claude Code does and keeps other ids readable", () => {
    expect(replyModelName(OPUS)).toBe("Opus 5.5");
    expect(replyModelName(SONNET)).toBe("Sonnet 4.5");
    expect(replyModelName("claude-opus-5-5[1m]")).toBe("Opus 5.5 1M");
    expect(replyModelName("gpt-6.1-sol")).toBe("6.1 Sol"); // as the model lists name a raw GPT id
    expect(replyModelName("glm-5.1")).toBe("glm-5.1");
    expect(replyModelName("vendor/some-model")).toBe("vendor/some-model");
  });
});

describe("replyModel", () => {
  it("is the answer's model, else the latest step's that names one", () => {
    const [withAnswer, steps] = chatLines([user(1, "a"), call(2, OPUS), result(3), said(4, SONNET), user(5, "b"), call(6, OPUS), result(7), call(8)]);
    expect(withAnswer.kind === "segment" && replyModel(withAnswer.seg)).toBe(SONNET);
    expect(steps.kind === "segment" && replyModel(steps.seg)).toBe(OPUS);
  });
});

describe("model change divider placement", () => {
  it("marks the exchange whose reply came from another model than the previous visible reply", () => {
    const lines = chatLines([user(1, "first"), said(2, OPUS), user(3, "second"), said(4, OPUS), user(5, "third"), said(6, SONNET), user(7, "fourth"), said(8, SONNET)]);
    expect(marks(lines)).toEqual(["first", "second", `third [${OPUS} → ${SONNET}]`, "fourth"]);
  });

  it("puts nothing where a model is unknown, and compares across it with the last known one", () => {
    // older nodes send no model; Claude Code's own lines (an API error) name none
    const lines = chatLines([user(1, "first"), said(2, OPUS), user(3, "second"), said(4), user(5, "third"), said(6, SONNET)]);
    expect(marks(lines)).toEqual(["first", "second", `third [${OPUS} → ${SONNET}]`]);
    expect(marks(chatLines([user(1, "first"), said(2), user(3, "second"), said(4, SONNET)]))).toEqual(["first", "second"]);
  });

  it("never marks inside an exchange: one mark at most, before the reply, and no new lines", () => {
    // a refusal fallback moves the turn to another model halfway: the exchange is marked once, by its answer's model
    const items = [user(1, "first"), said(2, OPUS), user(3, "second"), call(4, OPUS), result(5), call(6, SONNET), result(7), said(8, SONNET)];
    const lines = chatLines(items);
    expect(marks(lines)).toEqual(["first", `second [${OPUS} → ${SONNET}]`]);
    expect(lines).toHaveLength(chatLines(items.map(({ model: _, ...it }) => it)).length);
  });

  it("treats a dated id and its alias as one model", () => {
    expect(marks(chatLines([user(1, "first"), said(2, OPUS), user(3, "second"), said(4, `${OPUS}-20260101`)]))).toEqual(["first", "second"]);
  });

  it("skips folded rounds: they are no visible reply", () => {
    const supervise = { injected: { kind: "system", state: "supervise", line: "Review a task" } } as Partial<Item>;
    const lines = chatLines([user(1, "first"), said(2, OPUS), user(3, "check", supervise), said(4, "claude-haiku-4-5"), user(5, "second"), said(6, OPUS)]);
    expect(marks(lines)).toEqual(["first", "supervision", "second"]);
  });

  it("marks the live exchange as soon as the new model's first step is in", () => {
    const lines = chatLines([user(1, "first"), said(2, OPUS), user(3, "second"), call(4, SONNET)], { live: true });
    expect(marks(lines)).toEqual(["first", `second [${OPUS} → ${SONNET}]`]);
  });

  it("leaves the lines as they are when no model changes", () => {
    const lines = chatLines([user(1, "first"), said(2, OPUS), user(3, "second"), said(4, OPUS)]);
    const again = markModelChanges(lines);
    expect(again.every((l, i) => l === lines[i])).toBe(true);
  });
});
