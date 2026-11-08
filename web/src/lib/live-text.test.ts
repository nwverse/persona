import { describe, expect, it } from "vitest";
import { landedWords, unlandedLive, unlandedText, type Landed } from "./live-text";
import type { Item } from "./types";

const at = (seq: number, role: Item["role"], kind: Item["kind"], text?: string, more: Partial<Item> = {}): Item =>
  ({ id: `i${seq}`, seq, role, kind, text, ...more }) as Item;
const whole = (text: string): Landed => ({ text, size: 0 });

describe("the live text against the transcript", () => {
  it("keeps what the transcript does not have, as it came", () => {
    expect(unlandedText("Line 1.\nLine 2.\n", [])).toBe("Line 1.\nLine 2.\n");
    expect(unlandedText("\n\nLine 1.\n", [])).toBe("\n\nLine 1.\n");
    expect(unlandedText("\n \n", [])).toBe("");
  });

  it("drops what an item already holds, the final batch's last line too", () => {
    const words = [whole("Line 1.\nLine 2.\nLine 3.")];
    expect(unlandedText("Line 1.\nLine 2.\n", words)).toBe("");
    expect(unlandedText("Line 1.\nLine 2.\nLine 3.", words)).toBe("");
  });

  it("shows only what goes past the blocks it begins with", () => {
    expect(unlandedText("Block one.\n\nBlock two.\n", [whole("Block one.")])).toBe("Block two.\n");
    expect(unlandedText("Block one.Block two.", [whole("Block one."), whole("Block two.")])).toBe("");
  });

  it("takes an item off only where a word ends", () => {
    expect(unlandedText("好的，测试已通过。\n", [whole("好的")])).toBe("好的，测试已通过。\n");
    expect(unlandedText("OK, the tests pass.\n", [whole("OK")])).toBe("OK, the tests pass.\n");
  });

  it("counts a cut item as its whole block: what goes past it still shows", () => {
    const long = "Line of a long reply.\n".repeat(30);
    expect(unlandedText(long, [{ text: long.slice(0, 40), size: long.length - 1 }])).toBe("");
    expect(unlandedText(long + "\nAnother block.\n", [{ text: long.slice(0, 40), size: long.length }])).toBe("Another block.\n");
    const wide = "测试结果：全部通过。\n".repeat(20); // its size counts bytes, not characters
    expect(unlandedText(wide + "下一段。\n", [{ text: wide.slice(0, 12), size: new TextEncoder().encode(wide).length }])).toBe("下一段。\n");
  });

  it("leaves the next message alone", () => {
    expect(unlandedText("Mountain 1.\n", [whole("Sea 1.\nSea 2.")])).toBe("Mountain 1.\n");
  });

  it("takes only the agent's latest message, a cut item without its note", () => {
    const items = [
      at(1, "user", "text", "Earlier question"),
      at(2, "assistant", "text", "Earlier answer."),
      at(3, "user", "text", "Write about the sea, then list the folder."),
      at(4, "assistant", "thinking", "Plan."),
      at(5, "assistant", "text", "Sea 1.\nSea 2."),
      at(6, "assistant", "tool_use", undefined, { tool_use_id: "t1", tool_name: "Bash" }),
      at(7, "user", "tool_result", "total 0", { tool_use_id: "t1" }),
      at(8, "assistant", "text", "A long reply\n…（已截断，共 40000 字节）", { full_size: 40000 }), // i18n-ignore: the hub's note
      at(9, "assistant", "text", "And a second block."),
    ];
    expect(landedWords(items)).toEqual([{ text: "A long reply", size: 40000 }, whole("And a second block.")]);
    // words followed by a tool's result are an earlier message's: never the live one's
    expect(landedWords(items.slice(0, 7))).toEqual([]);
    expect(landedWords(items.slice(0, 6))).toEqual([whole("Sea 1.\nSea 2.")]);
    expect(landedWords([...items, at(10, "system", "notice", "Background task done", { wake: true })])).toEqual([]);
  });

  it("does not let the message before, which begins alike, take the next one", () => {
    const items = [
      at(1, "user", "text", "Rename them and run the tests."),
      at(2, "assistant", "text", "I'll update the names.\nThen I'll run the tests."),
      at(3, "assistant", "tool_use", undefined, { tool_use_id: "t1", tool_name: "Bash" }),
      at(4, "user", "tool_result", "ok", { tool_use_id: "t1" }),
    ];
    expect(unlandedText("I'll update the names.\n", landedWords(items))).toBe("I'll update the names.\n");
    expect(unlandedLive("I'll update the names.\n", items)).toBe("I'll update the names.\n");
  });

  it("drops a whole copy of the message before, come late, and nothing else of it", () => {
    const before = "I'll update the names.\nThen I'll run the tests.";
    const items = [
      at(1, "user", "text", "Rename them and run the tests."),
      at(2, "assistant", "text", before),
      at(3, "assistant", "tool_use", undefined, { tool_use_id: "t1", tool_name: "Bash" }),
      at(4, "user", "tool_result", "ok", { tool_use_id: "t1" }),
    ];
    expect(unlandedLive(before, items)).toBe("");
    expect(unlandedLive("Running the tests now.\n", items)).toBe("Running the tests now.\n");
    // the turn before's last words are not this turn's
    expect(unlandedLive("Rename them and run the tests.", items.slice(0, 1))).toBe("Rename them and run the tests.");
    expect(unlandedLive("Done.", [at(1, "assistant", "text", "Done."), at(2, "user", "text", "Again?"), at(3, "assistant", "thinking", "…")])).toBe("Done.");
  });
});
