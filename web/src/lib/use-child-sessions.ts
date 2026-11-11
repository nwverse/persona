// The child sessions folded under one chat's session (#5120, docs/protocol/protocol.md "Session hierarchy"): what its
// cards are drawn from. A hub that pushes every row has them in the store already; a paging hub (`sessions_list`) is asked
// for exactly those rows (`parent_session_id`, with `sessions_hierarchy`) while the chat is open and the session's row says
// it has some (its `children` rollup), and they go into the store, where each card and the children's request cards find
// them. An older paging hub folds nothing: no cards.
import { useEffect, useMemo, useRef, useSyncExternalStore } from "react";
import { foldedChildren, SESSIONS_HIERARCHY_FEATURE } from "./child-sessions";
import { runtimeIdentity, runtimeOrder, runtimeRemoval, runtimeSessions } from "./runtime-list";
import { SESSIONS_LIST_FEATURE, SessionList, type ListSnapshot } from "./session-list";
import { applyCompleteSessions, onHubEvent, useStore } from "./store";
import type { Session } from "./types";

const NONE: Session[] = [];
const EMPTY: ListSnapshot<Session> = { rows: [], loaded: false, loading: false, error: null, total: 0, counts: null, hasMore: false, supported: null };
const noSubscribe = () => () => {};
const noSnapshot = () => EMPTY;

export interface ChildSessions {
  /** The children, oldest first (by when each started); the same array while none of them changed. */
  rows: Session[];
  /** All of them are known: a chat waiting for one (a link to it) can tell it is not there. */
  loaded: boolean;
}

/** `prev` while it holds the same rows as `next`, so what is built from it holds still between pushes. */
export function sameRows(prev: Session[] | undefined, next: Session[]): Session[] {
  return prev && prev.length === next.length && prev.every((s, i) => s === next[i]) ? prev : next;
}

/** The sessions folded under mid/sid; none while `enabled` is off. `hasChildren`: the session's row counts some (a paging
 *  hub is not asked otherwise). */
export function useChildSessions(mid: string, sid: string, hasChildren: boolean, enabled = true): ChildSessions {
  const paged = useStore((s) => s.features.includes(SESSIONS_LIST_FEATURE));
  const hierarchy = useStore((s) => s.features.includes(SESSIONS_HIERARCHY_FEATURE));
  const storeLoaded = useStore((s) => s.loaded);
  // a hub that pushes every row: the children among the machine's rows, read there so that a push for another of its
  // sessions leaves the chat alone (useStore keeps the array while it holds the same rows)
  const pushed = useStore((s) => (enabled && !paged ? foldedChildren(s.sessions[mid] ?? NONE, mid, sid) : NONE));
  // a paging hub: one bounded list of this session's children, complete rows (their model's effort is not in a summary)
  const list = useMemo(
    () => (enabled && paged && hierarchy && hasChildren ? new SessionList<Session>("/sessions", runtimeIdentity, runtimeOrder, undefined, runtimeRemoval, { summary: false, pageCounts: () => runtimeSessions.usesPageCounts() }) : null),
    [enabled, paged, hierarchy, hasChildren],
  );
  const page = useSyncExternalStore(list?.subscribe ?? noSubscribe, list?.getSnapshot ?? noSnapshot, list?.getSnapshot ?? noSnapshot);
  useEffect(() => {
    if (!list) return;
    void list.setFilters({ machine_id: mid, parent_session_id: sid }).catch(() => {});
    // the hub's hint for any change of the list: a read of what changed since (`since`), only for these rows
    const off = onHubEvent("sessions_list", () => list.invalidate());
    return () => {
      off();
      list.reset();
    };
  }, [list, mid, sid]);
  // a session with more children than a page: the rest too (there are seldom more than a few)
  useEffect(() => {
    if (list && page.loaded && page.hasMore && !page.loading && !page.error) void list.more().catch(() => {});
  }, [list, page]);
  // into the store: the cards read their row there, and a child's request card checks there that it may be answered
  useEffect(() => {
    if (page.rows.length) applyCompleteSessions(page.rows);
  }, [page.rows]);
  const last = useRef<Session[] | undefined>(undefined);
  const listed = list ? page.rows : undefined;
  const rows = useMemo(() => (last.current = sameRows(last.current, listed ? foldedChildren(listed, mid, sid) : pushed)), [listed, pushed, mid, sid]);
  const loaded = !enabled || (list ? (page.loaded && !page.hasMore) || page.supported === false : paged || storeLoaded);
  return useMemo(() => ({ rows, loaded }), [rows, loaded]);
}
