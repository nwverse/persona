// Searching the web terminal's text (docs/integrations/terminal.md "What search covers"). What can be searched depends on who holds the text:
// xterm holds only the screen (scrollback 0: herdr redraws the grid in place), and while watching, the page holds the
// pages of herdr's history it loaded above it — and the whole screen too once "Wrap long lines" draws it. So there are two lists of
// matches: "rows" (the lines the page holds, found here) and "screen" (xterm's grid, found by its search addon, which also
// selects the current one). This file is the part that needs neither: the line matcher, the order of the two lists, and
// the find bar's state.
import { t } from "../i18n";

/** More matches than this are not counted separately (the bar says "999+"). */
export const FIND_LIMIT = 999;

export interface RowMatch {
  row: number;
  start: number;
  end: number;
}

/** Every match of `query` in `lines` (a plain substring, not a pattern), top to bottom; at most FIND_LIMIT. */
export function findInLines(lines: string[], query: string, caseSensitive: boolean): RowMatch[] {
  if (!query) return [];
  const re = new RegExp(query.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), caseSensitive ? "g" : "gi");
  const out: RowMatch[] = [];
  for (let row = 0; row < lines.length && out.length < FIND_LIMIT; row++) {
    re.lastIndex = 0;
    let m: RegExpExecArray | null;
    while ((m = re.exec(lines[row])) && out.length < FIND_LIMIT) {
      if (!m[0]) {
        re.lastIndex++;
        continue;
      }
      out.push({ row, start: m.index, end: m.index + m[0].length });
    }
  }
  return out;
}

/** The lines that hold a match, and the one the current match is on: what the page draws marked. */
export interface FindMarks {
  rows: ReadonlySet<number>;
  current: number | null;
}

export function marksFor(matches: RowMatch[], current: number | null): FindMarks {
  return { rows: new Set(matches.map((m) => m.row)), current: current === null ? null : (matches[current]?.row ?? null) };
}

/** Where the current match is: among the page's rows, or on xterm's screen (after the rows). */
export interface FindPos {
  seg: "rows" | "screen";
  i: number;
}

const wrap = (n: number, total: number) => ((n % total) + total) % total;

/**
 * The match after (dir 1) or before (dir -1) `pos` over both lists, the rows first and the screen after them,
 * wrapping around the ends. Without a position, the first (dir 1) or the last (dir -1) match. null when there is none.
 */
export function stepFind(pos: FindPos | null, dir: 1 | -1, rows: number, screen: number): FindPos | null {
  const total = rows + screen;
  if (total <= 0) return null;
  const g = pos ? wrap((pos.seg === "rows" ? pos.i : rows + pos.i) + dir, total) : dir === 1 ? 0 : total - 1;
  return g < rows ? { seg: "rows", i: g } : { seg: "screen", i: g - rows };
}

/** The 0-based number of the current match in the whole count. */
export function findIndex(pos: FindPos, rows: number): number {
  return pos.seg === "rows" ? pos.i : rows + pos.i;
}

/** What the bar says next to the field: nothing before a query, "No results", or "3/12" (999+ past the limit). */
export function findLabel(query: string, found: { index: number; count: number } | null): string {
  if (!query) return "";
  if (!found || found.count <= 0) return t("lib-terminal-search.no_results");
  const count = found.count > FIND_LIMIT ? `${FIND_LIMIT}+` : String(found.count);
  return found.index < 0 ? count : `${found.index + 1}/${count}`;
}

// ── the bar's state ─────────────────────────────────────

export interface FindState {
  open: boolean;
  query: string;
  caseSensitive: boolean;
}

export type FindAction = { type: "open"; query?: string } | { type: "close" } | { type: "query"; query: string } | { type: "toggleCase" };

export const FIND_CLOSED: FindState = { open: false, query: "", caseSensitive: false };

/** Opening keeps the last query (as a browser's bar does) unless the selection brings a new one; closing forgets it, as the highlights go. */
export function findReducer(s: FindState, a: FindAction): FindState {
  switch (a.type) {
    case "open":
      return { ...s, open: true, query: a.query ?? s.query };
    case "close":
      return s.open || s.query ? { ...s, open: false, query: "" } : s;
    case "query":
      return a.query === s.query ? s : { ...s, query: a.query };
    case "toggleCase":
      return { ...s, caseSensitive: !s.caseSensitive };
  }
}

/** The text a selection brings to the bar: one line only, at most 200 characters; null when there is none worth searching. */
export function queryFromSelection(selection: string): string | null {
  const t = selection.trim();
  if (!t || /[\r\n]/.test(t)) return null;
  return t.slice(0, 200);
}

/**
 * Opens the bar: ⌘F on a Mac. Elsewhere Ctrl+Shift+F, as Linux and Windows terminals have it, and plain Ctrl+F only while
 * the terminal is not driven (driving, ^F is the program's: readline's forward-char, vim's page down).
 */
export function isFindShortcut(
  e: { type: string; key: string; ctrlKey: boolean; metaKey: boolean; altKey: boolean; shiftKey: boolean },
  where: { mac: boolean; driving: boolean },
): boolean {
  if (e.type !== "keydown" || e.altKey || e.key.toLowerCase() !== "f") return false;
  if (where.mac) return e.metaKey && !e.ctrlKey && !e.shiftKey;
  if (e.metaKey || !e.ctrlKey) return false;
  return e.shiftKey || !where.driving;
}
