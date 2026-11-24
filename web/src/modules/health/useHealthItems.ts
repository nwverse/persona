// The Health home's list of what it tracks (items.ts, #14716): read while the hub's hello has `health_items` (and
// intake, the only items the web draws), again on the hub's `health_items` message and on every hello (a hub back from
// away, or one with another feature set), and saved as a whole at the revision it was read at.
//
// A read never takes the list back past one this page already has: answers can cross (a read sent before a save that
// answers after it), and the revision says which is newer.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { errorText } from "../../lib/api";
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
  /** Saves `shown` as the web's items, keeping the rest of the list where it was. Throws StaleItemsError (the page now
   *  shows the hub's list) or what the hub said. */
  save: (shown: readonly IntakeKind[]) => Promise<void>;
}

export function useHealthItems(): HealthItems {
  const enabled = useStore((s) => hubHas(s.features, ITEMS_FEATURE) && hubHas(s.features, INTAKE_FEATURE));
  const [state, setState] = useState<HealthItemsState | null>(null);
  const [error, setError] = useState("");
  const current = useRef<HealthItemsState | null>(null);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => { alive.current = false; };
  }, []);

  /** Takes a list the hub sent unless the page already has a newer one. */
  const take = useCallback((next: HealthItemsState) => {
    if (!alive.current) return;
    const had = current.current;
    if (had && had.revision > next.revision) return;
    current.current = next;
    setState(next);
    setError("");
  }, []);

  const load = useCallback(async () => {
    try {
      take(await readItems());
    } catch (e) {
      if (alive.current) setError(errorText(e));
    }
  }, [take]);

  useEffect(() => {
    if (!enabled) return;
    void load();
    const stops = [onHubEvent(ITEMS_EVENT, () => void load()), onHubEvent("hello", () => void load())];
    return () => stops.forEach((stop) => stop());
  }, [enabled, load]);

  const save = useCallback(async (shown: readonly IntakeKind[]) => {
    const base = current.current;
    if (!base) throw new Error("not loaded");
    try {
      take(await writeItems(mergeItems(base.items, shown), base.revision));
    } catch (e) {
      if (e instanceof StaleItemsError) take(e.current);
      throw e;
    }
  }, [take]);

  const shown = useMemo(() => (state ? shownItems(state) : null), [state]);
  const offered = useMemo(() => (state ? offeredItems(state) : []), [state]);
  const ask = Boolean(state && !state.saved && shown?.length === 0 && offered.length > 0);
  return { enabled, state, shown, offered, ask, error: state ? "" : error, reload: () => void load(), save };
}
