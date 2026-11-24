// The Health home's list of what it tracks (items.ts, #14716): read while the hub's hello has `health_items` (and
// intake, the only items the web draws), again on the hub's `health_items` message and on every hello (a hub back from
// away, or one with another feature set), and saved as a whole at the revision it was read at.
//
// Answers can cross (a read sent before a save that answers after it): only the answer to the request sent last is
// taken, by the order the requests went out, not by revision. A revision is only comparable within one manager's list,
// and the default manager can move (lib/manager-choice.ts onDefaultManager): then the list is dropped and read again.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { errorText } from "../../lib/api";
import { onDefaultManager } from "../../lib/manager-choice";
import { hubHas, onHubEvent, useStore } from "../../lib/store";
import { INTAKE_FEATURE, type IntakeKind } from "./intake";
import { ITEMS_EVENT, ITEMS_FEATURE, mergeItems, offeredItems, readItems, shownItems, StaleItemsError, writeItems, type HealthItemsState } from "./items";

export interface HealthItems {
  /** Whether the hub keeps the list: without it the home is drawn as before (both rings while intake is on). */
  enabled: boolean;
  /** Null until the first read has come back. */
  state: HealthItemsState | null;
  /** The web items added, in order; null until the first read. */
  shown: IntakeKind[] | null;
  /** The web items the editor offers. */
  offered: IntakeKind[];
  /** Whether to ask what to track: nobody chose yet, and neither water nor protein is on. */
  ask: boolean;
  /** The first read's failure, while there is nothing to show. */
  error: string;
  reload: () => void;
  /** Saves `shown` as the web's items, keeping the rest of the list where it was, at the revision of `base` (the list
   *  an edit started from; the page's current one without it). Throws StaleItemsError (the page now shows the hub's
   *  list) or what the hub said. */
  save: (shown: readonly IntakeKind[], base?: ItemsBase) => Promise<void>;
}

/** The list an edit started from: what its save merges into, and the revision it saves at. */
export type ItemsBase = Pick<HealthItemsState, "items" | "revision">;

export function useHealthItems(): HealthItems {
  const enabled = useStore((s) => hubHas(s.features, ITEMS_FEATURE) && hubHas(s.features, INTAKE_FEATURE));
  const [state, setState] = useState<HealthItemsState | null>(null);
  const [error, setError] = useState("");
  const current = useRef<HealthItemsState | null>(null);
  // the number of the request sent last (a read or a save): an answer to an earlier one is not taken
  const sent = useRef(0);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => { alive.current = false; };
  }, []);

  /** Takes a list the hub sent in answer to request `n`, if no request went out after it. */
  const take = useCallback((n: number, next: HealthItemsState) => {
    if (!alive.current || n !== sent.current) return;
    current.current = next;
    setState(next);
    setError("");
  }, []);

  const load = useCallback(async () => {
    const n = ++sent.current;
    try {
      take(n, await readItems());
    } catch (e) {
      if (alive.current && n === sent.current) setError(errorText(e));
    }
  }, [take]);

  useEffect(() => {
    if (!enabled) return;
    void load();
    const stops = [
      onHubEvent(ITEMS_EVENT, () => void load()),
      onHubEvent("hello", () => void load()),
      // the default manager moved (not merely became known): its list is another one, with revisions of its own
      onDefaultManager((was) => {
        if (!was.settled) return;
        current.current = null;
        setState(null);
        void load();
      }),
    ];
    return () => stops.forEach((stop) => stop());
  }, [enabled, load]);

  const save = useCallback(async (shown: readonly IntakeKind[], from?: ItemsBase) => {
    const base = from ?? current.current;
    if (!base) throw new Error("not loaded");
    const n = ++sent.current;
    try {
      take(n, await writeItems(mergeItems(base.items, shown), base.revision));
    } catch (e) {
      if (e instanceof StaleItemsError) take(n, e.current);
      throw e;
    }
  }, [take]);

  const shown = useMemo(() => (state ? shownItems(state) : null), [state]);
  const offered = useMemo(() => (state ? offeredItems(state) : []), [state]);
  const ask = Boolean(state && !state.saved && shown?.length === 0 && offered.length > 0);
  return { enabled, state, shown, offered, ask, error: state ? "" : error, reload: () => void load(), save };
}
