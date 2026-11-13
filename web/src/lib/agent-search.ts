// ⌘K's search over Agent sessions (#6589): the Agents list's own field went, and Search chats finds what it found. Loaded
// with the first query typed (views/Palette.tsx), as lib/assistant-search.ts is: off the first screen and the idle tier
// (src/startup-preload.ts); the list itself only reads the query it is given.
import { baseName, sessionTitle } from "./format";
import { machineWithHub } from "./mesh";
import {
  buildEvidenceSnippet,
  compileSearchAsCode,
  DEFAULT_DIMENSION_FILTERS,
  parseSearchQuery,
  scopeHaystacks,
  sessionProjectName,
  type AgentFacetCounts,
  type EvalContext,
  type EvidenceSnippet,
} from "./agent-chatlist-filter";
import type { Session } from "./types";

/** Whether a query has an operator (`machine:`, `project:`, `is:pinned`, `since:`…): it then speaks of Agent sessions. */
export function hasSearchOperators(query: string): boolean {
  const parsed = parseSearchQuery(query);
  return parsed.terms.length < query.trim().split(/\s+/).filter(Boolean).length;
}

const NO_FACETS: AgentFacetCounts = { harnesses: [], projects: [], models: [], machines: [], pinned: 0 };

/** How ⌘K ranks an Agent session (#6589). */
export interface AgentSearchHit {
  /** 0: no match. 100 the name starts with what was typed, 80 contains it, 60 every word is in the name, the project or
   *  the computer (with the space, tab and folder), 40 every word is somewhere the list's Full text reads (the last line,
   *  what was asked, the branch, the model, an error), 30 operators alone, 1 nothing typed. */
  score: number;
  /** Found by what is inside it (40): the line that matched, the match apart. */
  snippet?: EvidenceSnippet;
}

/** ⌘K's match for an Agent session: the list's operators narrow (plain words never filter, #4397), then what was typed
 *  ranks the name first, then the project and computer, then the rest (`AgentSearchHit.score`). */
export function agentSearchHit(s: Session, query: string, ctx: EvalContext): AgentSearchHit {
  const parsed = parseSearchQuery(query);
  const tokens = query.trim().split(/\s+/).filter(Boolean);
  const operators = tokens.filter((w) => !parsed.terms.includes(w)).join(" ");
  if (operators) {
    const plan = compileSearchAsCode(operators, { ...DEFAULT_DIMENSION_FILTERS, query: operators }, NO_FACETS);
    if (!plan.predicates.every((p) => p(s, ctx))) return { score: 0 };
  }
  const terms = parsed.terms.map((w) => w.toLowerCase());
  if (terms.length === 0) return { score: operators ? 30 : 1 };
  const text = parsed.text.toLowerCase();
  const title = sessionTitle(s).toLowerCase();
  if (title.startsWith(text)) return { score: 100 };
  if (title.includes(text)) return { score: 80 };
  const place = [title, sessionProjectName(s), machineWithHub(s.machine_id, ctx.machines), s.machine_id, s.space_name, s.slot_name, baseName(s.cwd)]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
  if (terms.every((w) => place.includes(w))) return { score: 60 };
  const inside = scopeHaystacks(s, "all", ctx).map((h) => h.text).join("\n").toLowerCase();
  if (!terms.every((w) => place.includes(w) || inside.includes(w))) return { score: 0 };
  return { score: 40, snippet: buildEvidenceSnippet(s, parsed.text, "all", ctx) ?? undefined };
}
