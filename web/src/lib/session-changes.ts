// Compose only the successful edits in one transcript scope. Unknown parts of a file stay unknown.
import { diffLines, type Op } from "./compare";
import type { Hunk } from "./code-view";
import { changePath, clipped, editLines } from "./turn-changes";
import type { Item } from "./types";

type Replacement = { before: string; after: string; all: boolean };
export type RecordedChange =
  | { kind: "patch"; hunks: Hunk[]; beforeHash?: string }
  | { kind: "replace"; edits: Replacement[]; beforeHash?: string }
  | { kind: "write"; text: string; created: boolean; beforeHash?: string }
  | { kind: "unknown"; beforeHash?: string };

export interface Region {
  oldStart: number;
  newStart: number;
  positioned: boolean;
  ops: Op[];
  heading?: string;
}
export interface FileChange { regions: Region[]; complete: boolean; add: number; del: number; gitHunks?: Hunk[] }
export interface ChangeHunk extends Hunk { positioned: boolean; moreBefore: boolean; moreAfter: boolean; beforeKey: string; afterKey: string }

const MAX_LINES = 100_000;
const HEADER = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$/;

// A line cannot contain its own separator. A trailing LF inside our line value identifies an
// unterminated final line, including a change affecting only its ending.
function bodyLines(text: string): string[] {
  if (!text) return [];
  if (text.endsWith("\n")) return text.slice(0, -1).split("\n");
  const lines = text.split("\n");
  lines[lines.length - 1] += "\n";
  return lines;
}

function patchLines(lines: string[]): string[] | null {
  const result: string[] = [];
  for (const line of lines) {
    if (line.startsWith("\\")) {
      if (line !== "\\ No newline at end of file" || !result.length || result.at(-1)!.endsWith("\n")) return null;
      result[result.length - 1] += "\n";
    } else result.push(line);
  }
  return result;
}

function readPatch(text: string): Hunk[] | null {
  const hunks: Hunk[] = [];
  let hunk: Hunk | undefined;
  let oldLeft = 0, newLeft = 0;
  for (const line of text.split("\n")) {
    const match = HEADER.exec(line);
    if (match) {
      if (oldLeft || newLeft) return null;
      oldLeft = Number(match[2] ?? 1);
      newLeft = Number(match[4] ?? 1);
      const oldStart = Number(match[1]) + (oldLeft ? 0 : 1);
      const newStart = Number(match[3]) + (newLeft ? 0 : 1);
      if (Math.min(oldStart, newStart) < 1 || Math.max(oldStart + oldLeft, newStart + newLeft) > MAX_LINES) return null;
      hunk = { old_start: oldStart, new_start: newStart, header: match[5].trim() || undefined, lines: [] };
      hunks.push(hunk);
    } else if (hunk && line.startsWith("\\")) {
      if (line !== "\\ No newline at end of file") return null;
      hunk.lines.push(line);
    } else if (hunk && line) {
      if (!["+", "-", " "].includes(line[0])) return null;
      if (line[0] !== "+" && --oldLeft < 0) return null;
      if (line[0] !== "-" && --newLeft < 0) return null;
      hunk.lines.push(line);
    } else if (!hunk && /^@@/.test(line)) return null;
  }
  return hunks.length && !oldLeft && !newLeft ? hunks : null;
}

export function recordedChange(use: Pick<Item, "tool_name" | "tool_input">, result: Pick<Item, "outcome">, path: string, cwd?: string): RecordedChange {
  const input = use.tool_input ?? {};
  const meta = result.outcome;
  const file = meta?.files?.find((f) => changePath(f.path, cwd) === path);
  const object = typeof input.diff === "string" && /^index ([0-9a-f]{40}|[0-9a-f]{64})\.\.[0-9a-f]+(?:\s|$)/m.exec(input.diff);
  const beforeHash = file?.before_sha256 ?? (object ? "git:" + object[1] : undefined);
  const patch = file && !meta?.patch_cut && meta?.patch || (!clipped(input.diff) && typeof input.diff === "string" && input.diff);
  if (patch && /^@@/m.test(patch)) {
    const hunks = readPatch(patch);
    return hunks ? { kind: "patch", hunks, beforeHash } : { kind: "unknown", beforeHash };
  }
  if (clipped(input) || (file && meta?.patch_cut)) return { kind: "unknown", beforeHash };
  if (input.change === "add" && typeof input.diff === "string")
    return { kind: "write", text: input.diff, created: true, beforeHash };
  if (input.change === "delete" && typeof input.diff === "string") {
    const old = editLines(input.diff, "")[0];
    return { kind: "patch", hunks: [{old_start: 1, new_start: 1, lines: old.map((line) => "-" + line)}], beforeHash };
  }
  if (use.tool_name === "Write" && typeof input.content === "string")
    return { kind: "write", text: input.content, created: file?.created === true, beforeHash };
  const rows: unknown[] = use.tool_name === "MultiEdit" && Array.isArray(input.edits) ? input.edits : [input];
  const edits: Replacement[] = [];
  for (const row of rows) {
    if (!row || typeof row !== "object" || !("old_string" in row) || typeof row.old_string !== "string" ||
      !("new_string" in row) || typeof row.new_string !== "string") return { kind: "unknown", beforeHash };
    edits.push({ before: row.old_string, after: row.new_string, all: "replace_all" in row && row.replace_all === true });
  }
  return { kind: "replace", edits, beforeHash };
}

function compare(old: string[], next: string[]): Op[] | null {
  return diffLines(old, next, MAX_LINES, 900);
}

// Original line numbers survive edits. Text replaces a number only for an added line.
// Seeing a later hunk fills an unknown original line, or checks the line left by an earlier call.
class PatchFile {
  original: (string | undefined)[] = [];
  current: (number | string)[] = [];
  headings = new Map<number, string>();

  apply(hunks: Hunk[], whole: boolean): boolean {
    let delta = 0;
    let previousEnd = 0;
    for (const h of hunks) {
      const at = h.old_start - 1 + delta;
      const lines = patchLines(h.lines);
      if (!lines) return false;
      const oldCount = lines.filter((l) => l[0] !== "+").length;
      if (at !== h.new_start - 1 || h.old_start - 1 < previousEnd || at < 0 || at + oldCount > MAX_LINES) return false;
      previousEnd = h.old_start - 1 + oldCount;
      if (whole && at + oldCount > this.current.length) return false;
      while (this.current.length < at + oldCount) {
        this.current.push(this.original.length);
        this.original.push(undefined);
      }
      const replacement: (string | number)[] = [];
      let pos = at;
      for (const line of lines) {
        const text = line.slice(1);
        if (line[0] === "+") { replacement.push(text); continue; }
        const cell = this.current[pos++];
        if (typeof cell === "number" && this.original[cell] === undefined) this.original[cell] = text;
        const known = typeof cell === "number" ? this.original[cell] : cell;
        if (known !== text) return false;
        if (line[0] === " ") replacement.push(cell);
      }
      if (h.header) {
        const origin = this.current[at];
        if (typeof origin === "number") this.headings.set(origin, h.header);
      }
      if (replacement.length > MAX_LINES || this.current.length - oldCount + replacement.length > MAX_LINES) return false;
      this.current = this.current.slice(0, at).concat(replacement, this.current.slice(at + oldCount));
      delta += replacement.length - oldCount;
    }
    return true;
  }

  // A successful non-replace_all Claude Edit requires a unique match. If its entire match is inside
  // lines known from earlier hunks, its absolute position follows from those hunks.
  applyEdit(edit: Replacement): boolean {
    if (edit.all || !edit.before) return false;
    let found: Hunk | undefined;
    for (let start = 0; start < this.current.length; ) {
      const lines: string[] = [];
      let end = start;
      for (; end < this.current.length; end++) {
        const cell = this.current[end];
        const text = typeof cell === "number" ? this.original[cell] : cell;
        if (text === undefined) break;
        lines.push(text);
      }
      const content = lines.map((line) => line.endsWith("\n") ? line.slice(0, -1) : line);
      const known = content.join("\n"), at = known.indexOf(edit.before);
      if (at >= 0) {
        if (found || known.indexOf(edit.before, at + 1) >= 0) return false;
        const first = known.slice(0, at).split("\n").length - 1;
        const last = first + edit.before.split("\n").length - 1;
        const old = lines.slice(first, last + 1);
        const after = replace(content.slice(first, last + 1).join("\n"), edit);
        if (after === undefined) return false;
        const final = bodyLines(after + (old.at(-1)?.endsWith("\n") ? "" : "\n"));
        const marked = (sign: string, values: string[]) => values.flatMap((line) => line.endsWith("\n")
          ? [sign + line.slice(0, -1), "\\ No newline at end of file"] : [sign + line]);
        const coordinate = start + first + 1;
        found = { old_start: coordinate, new_start: coordinate, lines: [...marked("-", old), ...marked("+", final)] };
      }
      start = end + 1;
    }
    return found !== undefined && this.apply([found], false);
  }

  regions(): Region[] | null {
    const regions: Region[] = [];
    let start = 0, from = 0;
    const add = (oldEnd: number, nextEnd: number) => {
      const old = this.original.slice(start, oldEnd);
      if (old.some((v) => v === undefined)) return false;
      const next = this.current.slice(from, nextEnd).map((c) => typeof c === "number" ? this.original[c]! : c);
      const ops = compare(old as string[], next);
      if (!ops) return false;
      if (ops.some((op) => op.kind !== "same")) regions.push({ oldStart: start + 1, newStart: from + 1, positioned: true, ops, heading: this.headings.get(start) });
      return true;
    };
    for (let i = 0; i < this.current.length; i++) {
      const cell = this.current[i];
      if (typeof cell === "number" && this.original[cell] === undefined) {
        if ((start < cell || from < i) && !add(cell, i)) return null;
        start = cell + 1;
        from = i + 1;
      }
    }
    return add(this.original.length, this.current.length) ? regions : null;
  }
}

function replace(text: string, edit: Replacement): string | undefined {
  if (!edit.before || !text.includes(edit.before)) return undefined;
  if (edit.all) return text.split(edit.before).join(edit.after);
  const at = text.indexOf(edit.before);
  if (text.indexOf(edit.before, at + 1) >= 0) return undefined;
  return text.slice(0, at) + edit.after + text.slice(at + edit.before.length);
}

function replay(text: string, change: RecordedChange): string | undefined {
  if (change.kind === "write") return change.created ? (text === "" ? change.text : undefined) : change.text;
  if (change.kind === "replace") {
    for (const edit of change.edits) {
      const next = replace(text, edit);
      if (next === undefined) return undefined;
      text = next;
    }
    return text;
  }
  if (change.kind !== "patch") return undefined;
  const f = new PatchFile();
  const lines = bodyLines(text);
  f.original = lines;
  f.current = lines.map((_, i) => i);
  if (!f.apply(change.hunks, true)) return undefined;
  const final = f.current.map((c) => typeof c === "number" ? lines[c] : c);
  if (final.slice(0, -1).some((line) => line.endsWith("\n"))) return undefined;
  if (!final.length) return "";
  const unterminated = final.at(-1)!.endsWith("\n");
  if (unterminated) final[final.length - 1] = final.at(-1)!.slice(0, -1);
  return final.join("\n") + (unterminated ? "" : "\n");
}

function snippets(changes: RecordedChange[]): Region[] | null {
  const pairs: { before: string; after: string }[] = [];
  for (const c of changes) {
    if (c.kind !== "replace") return null;
    for (const e of c.edits) {
      if (!e.before || e.all) return null;
      const matches = pairs.filter((p) => p.after.includes(e.before));
      if (matches.length > 1) return null;
      if (matches.length) {
        const p = matches[0];
        const next = replace(p.after, e);
        if (next === undefined) return null;
        p.after = next;
      } else {
        // Disconnected excerpts remain unpositioned. A shared line could account for more than one excerpt.
        pairs.push({ before: e.before, after: e.after });
      }
    }
  }
  const regions: Region[] = [];
  for (const p of pairs) {
    const ops = compare(...editLines(p.before, p.after));
    if (!ops) return null;
    if (ops.some((op) => op.kind !== "same")) regions.push({ oldStart: 1, newStart: 1, positioned: false, ops });
  }
  return regions;
}

export function fileChange(changes: RecordedChange[], before?: string, nativeHunks?: Hunk[]): FileChange {
  let gitHunks: Hunk[] | undefined;
  if (before === undefined && changes[0]?.kind === "write" && changes[0].created) before = "";
  let regions: Region[] | null = null;
  if (before !== undefined) {
    let text: string | undefined = before;
    for (const change of changes) {
      text = replay(text, change);
      if (text === undefined) break;
    }
    if (text !== undefined) {
      const ops = compare(bodyLines(before), bodyLines(text));
      // The working tree may include a later session or a manual save. Git supplies the canonical
      // hunk boundaries and headings only when applying its patch gives this exact scope's result.
      if (nativeHunks?.length && replay(before, { kind: "patch", hunks: nativeHunks }) === text) gitHunks = nativeHunks;
      if (ops) regions = ops.some((op) => op.kind !== "same") ? [{ oldStart: 1, newStart: 1, positioned: true, ops }] : [];
    }
  } else if (changes[0]?.kind === "patch") {
    const f = new PatchFile();
    const applied = changes.every((c) => c.kind === "patch" ? f.apply(c.hunks, false) : c.kind === "replace" && c.edits.every((e) => f.applyEdit(e)));
    if (applied) regions = f.regions();
  } else regions = snippets(changes);
  let add = 0, del = 0;
  for (const r of regions ?? []) for (const line of r.ops) {
    if (line.kind === "add") add++;
    if (line.kind === "del") del++;
  }
  return { regions: regions ?? [], complete: regions !== null && changes.length > 0 && !(regions.length > 1 && regions.some((r) => !r.positioned)), add, del, gitHunks };
}

/** Omitted context comes only from lines actually available in this region. Each edge expands separately. */
export function visibleHunks(regions: Region[], context: number, gitHunks?: Hunk[], extra: Record<string, number> = {}): ChangeHunk[] {
  const hunks: ChangeHunk[] = [];
  regions.forEach((region, index) => {
    const ops = region.ops;
    const runs: { from: number; to: number; beforeKey: string; afterKey: string }[] = [];
    for (let i = 0; i < ops.length; i++) {
      if (ops[i].kind === "same") continue;
      const from = Math.max(0, i - context), to = Math.min(ops.length, i + context + 1);
      const key = index + "/" + i;
      const last = runs.at(-1);
      if (last && last.to >= from) {
        last.to = Math.max(last.to, to);
        last.afterKey = key + "/after";
      } else runs.push({ from, to, beforeKey: key + "/before", afterKey: key + "/after" });
    }
    const visible: typeof runs = [];
    for (const run of runs) {
      run.from = Math.max(0, run.from - (extra[run.beforeKey] ?? 0));
      run.to = Math.min(ops.length, run.to + (extra[run.afterKey] ?? 0));
      const last = visible.at(-1);
      if (last && last.to >= run.from) {
        last.to = Math.max(last.to, run.to);
        last.afterKey = run.afterKey;
      } else visible.push(run);
    }
    let old = region.oldStart, next = region.newStart, i = 0;
    for (const { from, to, beforeKey, afterKey } of visible) {
      old += from - i;
      next += from - i;
      const native = gitHunks?.find((h) => h.new_start >= next && h.new_start <= next + to - from);
      const h: ChangeHunk = {
        old_start: old, new_start: next, header: native?.header || region.heading, positioned: region.positioned,
        moreBefore: i < from, moreAfter: to < ops.length, beforeKey, afterKey, lines: [],
      };
      for (i = from; i < to; i++) {
        const op = ops[i];
        const mark = op.kind === "same" ? " " : op.kind === "add" ? "+" : "-";
        if (op.text.endsWith("\n")) h.lines.push(mark + op.text.slice(0, -1), "\\ No newline at end of file");
        else h.lines.push(mark + op.text);
        if (op.kind !== "add") old++;
        if (op.kind !== "del") next++;
      }
      hunks.push(h);
    }
  });
  // Git may choose another alignment for repeated lines. Its complete patch was checked against the final bytes.
  if (context === 3 && !Object.keys(extra).length && gitHunks?.length === hunks.length) {
    return hunks.map((h, i) => ({ ...h, ...gitHunks[i] }));
  }
  return hunks;
}
