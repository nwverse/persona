import { describe, expect, it } from "vitest";
import { buildBlocks, chatLines, parseUserText, segments } from "./turns";
import type { Item } from "./types";

// #4315: the node's <persona-view-errors> feedback before an assistant message is for the model. An older hub gave it
// as a user item of its own (the line's block #0) or at the start of the message; the bubble showed its raw tags.
const FEEDBACK = "<persona-view-errors>\nThe view v1 failed its check.\n</persona-view-errors>";
const item = (id: string, seq: number, role: string, text: string, extra: Partial<Item> = {}): Item =>
  ({ id, seq, ts: "2026-01-02T03:04:05Z", role, kind: "text", text, ...extra }) as Item;

describe("view feedback is not the user's words (#4315)", () => {
  it("parseUserText drops it", () => {
    expect(parseUserText(`${FEEDBACK}\nAdd a bakery stop.`).text).toBe("Add a bakery stop.");
  });

  it("an item holding only the feedback adds nothing to the message", () => {
    const s = segments([
      item("l5#0", 5, "user", FEEDBACK, { client_msg_id: "m1" }),
      item("l5#1", 5, "user", "Add a bakery stop.", { client_msg_id: "m1" }),
      item("l6#0", 6, "assistant", "Bakery stop added."),
    ]);
    const users = s.filter((x) => x.user).map((x) => x.user!.text);
    expect(users).toEqual(["Add a bakery stop."]);
    expect(s.at(-1)?.answer?.text ?? s.at(-1)?.steps.length).toBeTruthy();
  });

  it("a line of only the feedback shows no bubble", () => {
    const s = segments([item("l5#0", 5, "user", FEEDBACK), item("l6#0", 6, "assistant", "ok")]);
    expect(s.filter((x) => x.user).length).toBe(0);
  });
});


describe("the turn builders skip a feedback-only item (#4315)", () => {
  const items = [
    item("l5#0", 5, "user", FEEDBACK, { client_msg_id: "m1" }),
    item("l5#1", 5, "user", "Add a bakery stop.", { client_msg_id: "m1" }),
    item("l6#0", 6, "assistant", "Bakery stop added."),
  ];
  it("buildBlocks keeps only the message as the turn's input", () => {
    const turns = buildBlocks(items).flatMap((b) => (b.kind === "turn" ? [b.turn] : []));
    expect(turns.flatMap((t) => t.users.map((u) => u.id))).toEqual(["l5#1"]);
  });
  it("chatLines draws no line with the raw tags", () => {
    expect(JSON.stringify(chatLines(items))).not.toContain("persona-view-errors");
  });
});
