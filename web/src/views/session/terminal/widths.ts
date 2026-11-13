import type { Terminal as XTerm } from "@xterm/xterm";
import { UnicodeGraphemesAddon } from "@xterm/addon-unicode-graphemes";

/**
 * Gives `term` the character widths herdr draws with (docs/integrations/terminal.md "Character width and CJK alignment"). xterm's default table is
 * Unicode 6: emoji such as 😀 ✅ 🚀 are one cell there and two in herdr, so the cell herdr leaves to the second half of an
 * emoji kept whatever was drawn there before (a stray block of colour), and a table row with an emoji came out a cell short.
 * Unicode 15 with grapheme clusters (ZWJ families, flags, variation selectors) agrees with herdr on every sequence tried
 * and on every code point of the ranges swept except about 85 added after Unicode 15. Needs `allowProposedApi`.
 */
export function matchHerdrWidths(term: XTerm) {
  term.loadAddon(new UnicodeGraphemesAddon()); // makes "15-graphemes" the active table itself
}
