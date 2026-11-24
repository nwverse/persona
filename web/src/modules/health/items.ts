// What Health tracks (docs/assistant/health.md "Health items", docs/protocol/protocol.md "Health items", #14716): the
// hub's ordered list of health items, the feature that says it keeps one, and how the web saves it without losing the
// items only phones show.
//
// Requests name no manager, as intake's do (intake.ts): the list is the default manager's whichever assistant the
// Persona page shows. `space=health` keeps lib/manager-choice.ts from adding the shown manager's header.
import { api, ApiError } from "../../lib/api";
import { INTAKE_KINDS, type IntakeKind } from "./intake";

/** The hello feature that says the hub keeps the list: without it the Health home is what it was before. */
export const ITEMS_FEATURE = "health_items";
/** The WebSocket message that says the list changed (`{revision}`): read it again. */
export const ITEMS_EVENT = "health_items";

/** The hub's list: its catalog (ids and where their data comes from), the added ids in order, the revision a save must
 *  name, and whether the owner has ever saved one (`saved: false`: the hub's defaults, nobody chose them yet). */
export interface HealthItemsState {
  catalog: { id: string; source: string }[];
  items: string[];
  revision: number;
  saved: boolean;
}

/** The items the web draws, in the catalog's order: water and protein. Activity rings and trends are the phone's. */
export const WEB_ITEMS: readonly IntakeKind[] = INTAKE_KINDS;
const isWebItem = (id: string): id is IntakeKind => (WEB_ITEMS as readonly string[]).includes(id);

const PATH = "/assistant/health/items?space=health";

function normalize(r: Partial<HealthItemsState>): HealthItemsState {
  return {
    catalog: Array.isArray(r.catalog) ? r.catalog : [],
    items: Array.isArray(r.items) ? r.items.filter((x): x is string => typeof x === "string") : [],
    revision: typeof r.revision === "number" ? r.revision : 0,
    saved: r.saved === true,
  };
}

export function readItems(): Promise<HealthItemsState> {
  return api<Partial<HealthItemsState>>(PATH).then(normalize);
}

/** The save lost to one made elsewhere: the hub's list as it is now (its 409 answer). */
export class StaleItemsError extends Error {
  constructor(readonly current: HealthItemsState) {
    super("health items changed elsewhere");
  }
}

/** Saves the whole list at the revision it was read at. A stale revision throws StaleItemsError with the hub's list. */
export async function writeItems(items: string[], revision: number): Promise<HealthItemsState> {
  try {
    return normalize(await api<Partial<HealthItemsState>>(PATH, { method: "PUT", json: { items, revision } }));
  } catch (e) {
    if (e instanceof ApiError && e.status === 409 && Array.isArray(e.body.items)) throw new StaleItemsError(normalize(e.body as Partial<HealthItemsState>));
    throw e;
  }
}

/** The web items the owner added, in their order (only ones the hub's catalog has). */
export function shownItems(state: HealthItemsState): IntakeKind[] {
  const known = new Set(state.catalog.map((c) => c.id));
  return state.items.filter((id): id is IntakeKind => isWebItem(id) && known.has(id));
}

/** The web items the editor offers: the catalog's, in the catalog's order. */
export function offeredItems(state: HealthItemsState): IntakeKind[] {
  return state.catalog.map((c) => c.id).filter(isWebItem);
}

/** The whole list to save when the web's items become `shown`, in that order. Everything else the list holds (Activity
 *  rings, trends, ids this page does not know) stays where it was: the slots the web's items took are filled with
 *  `shown` in order, a slot left over is dropped, and an item added beyond them goes right after the last of them (first
 *  of all when there were none, as the hub's own defaults put water and protein first). */
export function mergeItems(stored: string[], shown: readonly string[]): string[] {
  const next = [...shown];
  const out: string[] = [];
  let last = -1;
  for (const id of stored) {
    if (!isWebItem(id)) {
      out.push(id);
      continue;
    }
    const fill = next.shift();
    if (fill !== undefined) {
      out.push(fill);
      last = out.length - 1;
    }
  }
  out.splice(last + 1, 0, ...next);
  return out;
}
