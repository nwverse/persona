import { describe, expect, it } from "vitest";
import { fileChange, recordedChange, visibleHunks } from "./session-changes";

const path = "/project/rates.ts";
function patch(diff: string) {
  return recordedChange({ tool_name: "Edit", tool_input: { file_path: path, diff } }, { outcome: { status: "ok" } }, path);
}
function replace(before: string, after: string, all = false) {
  return recordedChange({ tool_name: "Edit", tool_input: { file_path: path, old_string: before, new_string: after, replace_all: all } }, {}, path);
}

describe("a file across the selected edits", () => {
  it("composes shifted and repeated edits with the original and final coordinates", () => {
    const net = fileChange([patch("@@ -9,2 +9,3 @@ work\n same\n+inserted\n-old\n+middle"), patch("@@ -11 +11 @@\n-middle\n+final")]);
    expect(net.complete).toBe(true);
    expect(net.regions).toMatchObject([{ oldStart: 9, newStart: 9, positioned: true, ops: [
      { kind: "same", text: "same" }, { kind: "del", text: "old" }, { kind: "add", text: "inserted" }, { kind: "add", text: "final" },
    ] }]);
    expect([net.add, net.del]).toEqual([2, 1]);
  });
  it("cancels a reverted edit and keeps a distant hunk in place", () => {
    const net = fileChange([patch("@@ -20 +20 @@\n-old\n+new"), patch("@@ -20 +20 @@\n-new\n+old\n@@ -53 +53 @@\n-x\n+y")]);
    const h = visibleHunks(net.regions, 3);
    expect(h).toHaveLength(1);
    expect([h[0].old_start, h[0].new_start, h[0].lines]).toEqual([53, 53, ["-x", "+y"]]);
    expect([net.add, net.del]).toEqual([1, 1]);
  });
  it("merges legacy snippets without inventing a file position or a whole-file overwrite", () => {
    const net = fileChange([replace("first value", "next value"), replace("next value", "last value")]);
    expect(visibleHunks(net.regions, 3)).toMatchObject([{ positioned: false, lines: ["-first value", "+last value"] }]);
    expect(fileChange([replace("x", "y", true)]).complete).toBe(false);
    const write = recordedChange({tool_name:"Write",tool_input:{file_path:path,content:"overwritten"}}, {}, path);
    expect(fileChange([write]).complete).toBe(false);
  });
  it("preserves scope on a verified base, including unchanged lines that can be expanded", () => {
    const previous = Array.from({ length: 14 }, (_, i) => "line " + (i + 1)).join("\n") + "\n";
    const net = fileChange([replace("line 8", "new line 8")], previous);
    expect(net.complete).toBe(true);
    expect(visibleHunks(net.regions, 2)).toMatchObject([{ old_start: 6, new_start: 6, moreBefore: true, moreAfter: true }]);
    expect(visibleHunks(net.regions, 16)[0].lines).toContain(" line 14");
  });
  it("uses Git's hunk and heading only when the working-tree diff yields the same final bytes", () => {
    const git = [{ old_start: 1, new_start: 1, header: "original()", lines: [" a", "-old", "+final", " z"] }];
    const source = "a\nold\nz\n";
    const net = fileChange([replace("old", "middle"), replace("middle", "final")], source, git);
    expect(visibleHunks(net.regions, 3, net.gitHunks)[0]).toMatchObject(git[0]);
    const later = [{ ...git[0], lines: [...git[0].lines, "+unrelated"] }];
    const historical = fileChange([replace("old", "final")], source, later);
    expect(historical.gitHunks).toBeUndefined();
    expect(visibleHunks(historical.regions, 3)[0].lines).not.toContain("+unrelated");
  });
  it("distinguishes a missing final newline from a file line on both sides", () => {
    const marker = "\\ No newline at end of file";
    const add = patch("@@ -1 +1 @@\n-x\n" + marker + "\n+x");
    const remove = patch("@@ -1 +1 @@\n-x\n+x\n" + marker);
    for (const start of [undefined, "x"]) {
      const net = fileChange([add], start);
      expect([net.add, net.del]).toEqual([1, 1]);
      expect(visibleHunks(net.regions, 3)[0].lines).toEqual(["-x", marker, "+x"]);
      expect(fileChange([add, remove], start).regions).toEqual([]);
    }
  });
  it("shows disconnected legacy excerpts without assigning them separate file lines or totals", () => {
    const result = fileChange([replace("blue", "green"), replace("jump", "rest")]);
    expect(result.complete).toBe(false);
    expect(visibleHunks(result.regions, 3).map((h) => [h.positioned, h.lines])).toEqual([[false, ["-blue", "+green"]], [false, ["-jump", "+rest"]]]);
  });
  it("expands one captured hunk edge without opening the other edge or distant hunk", () => {
    const start = Array.from({ length: 100 }, (_, i) => "line " + String(i + 1).padStart(3, "0")).join("\n") + "\n";
    const net = fileChange([replace("line 014", "changed 14"), replace("line 080", "changed 80")], start);
    const [first, second] = visibleHunks(net.regions, 3);
    const expanded = visibleHunks(net.regions, 3, undefined, { [first.afterKey]: 20 });
    expect(expanded[0].old_start).toBe(first.old_start);
    expect(expanded[0].lines).toContain(" line 036");
    expect(expanded[1]).toEqual(second);
  });
  it("locates a successful legacy substring edit inside a previously recorded hunk", () => {
    const base = patch("@@ -18,2 +18,2 @@ color\n label =\n-'red'\n+'blue'");
    const net = fileChange([base, replace("blue", "green")]);
    expect(visibleHunks(net.regions, 3)).toMatchObject([{ old_start: 18, new_start: 18, lines: [" label =", "-'red'", "+'green'"] }]);
    expect(fileChange([base, replace("elsewhere", "green")]).complete).toBe(false);
    const eof = patch("@@ -18 +18 @@\n-old\n\\ No newline at end of file\n+middle\n\\ No newline at end of file");
    expect(fileChange([eof, replace("middle\n", "no")]).complete).toBe(false);
    expect(visibleHunks(fileChange([eof, replace("middle", "final")]).regions, 3)[0].lines)
      .toEqual(["-old", "\\ No newline at end of file", "+final", "\\ No newline at end of file"]);
  });
  it("refuses clipped or contradictory history and malformed hunk coordinates", () => {
    expect(fileChange([patch("@@ -23 +23 @@\n-a\n+b"), patch("@@ -23 +23 @@\n-unrelated\n+c")]).complete).toBe(false);
    expect(fileChange([patch("@@ -1,12 +1 @@\n-a\n+b")]).complete).toBe(false);
    const cut = recordedChange({tool_name:"Edit",tool_input:{file_path:path,old_string:"a",new_string:"b"}},
      {outcome:{status:"ok",patch_cut:true,files:[{path}],patch:"@@ -1 +1 @@\n-a\n+b"}}, path);
    expect(fileChange([cut]).complete).toBe(false);
  });
});
