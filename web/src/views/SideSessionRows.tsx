// Side sessions under their parent, with the fold that puts them away (#5935, #7755): a Claude desktop app's side chat,
// /btw question or spawned session (`parent_agent_id`) is drawn a level in under the session it was started from, after
// a "Side sessions (N)" row that folds them. Both Agents lists draw a group with SideSessionGroup: the paged list in its
// rows and Browse by sections, the complete list in every grouping's sections through SideSessionRows (Pinned keeps the
// order the user chose). Folding hides rows, never sessions: every count stays the same, and a side session that needs
// the user stays a row of its own (docs/design/DESIGN_SYSTEM.md 3.4).
import { createContext, useContext, useMemo, type ReactElement, type ReactNode } from "react";
import { t } from "../i18n";
import { runtimeIdentity, runtimeNeedsYou, runtimeParentIdentity } from "../lib/runtime-list";
import { agentKey } from "../lib/pins";
import { nestUnderParents, shownSideSessions, sideFoldKey, sideGroups, type SideGroup } from "../lib/side-sessions";
import { useFolded } from "../lib/use-folded";
import type { Session } from "../lib/types";
import { Collapse } from "../ui/atoms";
import { SESSION_LIST_STYLE } from "../ui/list-metrics";
import { ShowMoreRow } from "./SidebarSection";
import { WindowedRows } from "./WindowedRows";

/** A search or a filter narrows the list: every "Side sessions (N)" fold opens, as the list's other folds open for a
 *  query, so a side session it finds is in view (useFolded's `holds`: not kept, and it can still be folded by hand). */
export const SideSessionsOpen = createContext(false);

export interface SideSessionGroupProps<T> {
  top: T;
  nested: readonly T[];
  /** A row's identity among the list's rows (runtimeIdentity): the top's keeps its fold. */
  identity: (row: T) => string;
  /** A side session that needs the user stays a row while its fold is folded. */
  needsYou: (row: T) => boolean;
  /** The open conversation is one of `nested`: the fold opens for it, as a group holding the open row does. */
  holds: boolean;
  /** The fold-all the fold follows (lib/sidebar-api.ts AGENTS_FOLD); the FoldScope around it without one. */
  scope?: string;
  /** A row as the list draws it; `nested`: one of the side sessions, drawn in the block a level in. */
  render: (row: T, nested: boolean) => ReactNode;
}

/** A row, its "Side sessions (N)" fold in the list's quiet "Show N more" line, then its side sessions a level in. Open by
 *  default; the choice is kept per parent (useFolded, the sidebar's fold store), so the Agents list's Collapse all /
 *  Expand all reach it, and a search, a filter or the open conversation among them opens it. */
export function SideSessionGroup<T>({ top, nested, identity, needsYou, holds, scope, render }: SideSessionGroupProps<T>) {
  const searching = useContext(SideSessionsOpen);
  const [folded, toggle] = useFolded(sideFoldKey(identity(top)), false, { holds: holds || searching, scope });
  const shown = shownSideSessions(nested, folded, needsYou);
  // the fold and the side sessions are one block a level in, so the line down its side is one line
  return <>
    {render(top, false)}
    <div data-side-sessions className={SESSION_LIST_STYLE.nestedRow}>
      <div data-side-fold><ShowMoreRow label={t("sidebar.side_sessions", { count: nested.length })} folded={folded} onClick={toggle} /></div>
      {nested.map((row) => {
        const open = shown.includes(row);
        return (
          <Collapse key={identity(row)} open={open} sidebarFold>
            <div inert={!open || undefined}>{render(row, true)}</div>
          </Collapse>
        );
      })}
    </div>
  </>;
}

const ownSession = (s: Session) => s;
/** A session's row key in the sidebar (SidebarParts' rowKey, the open row's `activeKey`). */
const rowKey = (s: Session) => `s:${agentKey(s)}`;

/** One section's rows of the complete Agents list with each side session under its parent (#7755), by the paged list's
 *  rules (lib/side-sessions.ts): the section's rows, a part at a time (WindowedRows), a group as one of them. Nothing
 *  nests across sections, and nothing is added or left out. `session`: the session an item is (a stable function);
 *  `holds`: the open row is this item (its session's row by default). */
export function SideSessionRows<T = Session>({ items, render, activeKey, session = ownSession as unknown as (item: T) => Session, holds }: {
  items: readonly T[];
  render: (item: T) => ReactElement;
  activeKey: string;
  session?: (item: T) => Session;
  holds?: (item: T) => boolean;
}) {
  const groups = useMemo((): SideGroup<T>[] | null => {
    const nesting = nestUnderParents(items, (x) => runtimeIdentity(session(x)), (x) => runtimeParentIdentity(session(x)));
    return nesting.nested.size ? sideGroups(nesting) : null;
  }, [items, session]);
  const open = holds ?? ((x: T) => rowKey(session(x)) === activeKey);
  if (!groups) return <WindowedRows items={items} holds={open} render={render} />;
  return (
    <WindowedRows items={groups} holds={(g) => open(g.top) || g.nested.some(open)} render={(g) => !g.nested.length ? render(g.top) : (
      <SideSessionGroup key={`side:${rowKey(session(g.top))}`} top={g.top} nested={g.nested} identity={(x) => runtimeIdentity(session(x))}
        needsYou={(x) => runtimeNeedsYou(session(x))} holds={g.nested.some(open)}
        render={(x, nested) => nested ? <div data-nested-session>{render(x)}</div> : render(x)} />
    )} />
  );
}
