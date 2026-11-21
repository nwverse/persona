// The optional sidebar terminal tree joins live Agent rows to the host pane inventory.
import { isStoppedAgent, stateOf } from "./format";
import type { TermSpace, TermTab } from "./terminals";
import type { Machine, Session } from "./types";

export type ShellSpace = Omit<TermSpace, "tabs"> & { tabs: (TermTab & { split: boolean })[] };

const machinePane = (mid: string, pane: string) => JSON.stringify([mid, pane]);

/** Herdr pane IDs are local to the machine. A paged session summary names its host through space_kind. */
export function liveHerdrPanes(rows: readonly Session[]) {
  const panes = new Map<string, Session[]>();
  for (const s of rows) {
    if (!s.pane_id || (s.host || s.space_kind) !== "herdr" || s.status === "offline" || stateOf(s) === "offline" || isStoppedAgent(s)) continue;
    const key = machinePane(s.machine_id, s.pane_id);
    const siblings = panes.get(key);
    if (siblings) siblings.push(s);
    else panes.set(key, [s]);
  }
  return panes;
}

/** A live Agent owns its exact pane even if the inventory still shows the shell of an earlier session.
 * Keep split membership from the full layout so each remaining shell still has its own pane actions. */
export function shellsOnly(spaces: readonly TermSpace[], agents?: { machine: Machine; panes: ReturnType<typeof liveHerdrPanes> }): ShellSpace[] {
  const out: ShellSpace[] = [];
  for (const sp of spaces) {
    const tabs = sp.tabs.map((tab) => {
      const panes = tab.panes.filter((p) => {
        if (p.state !== "shell" || p.sessionId) return false;
        const current = sp.host === "herdr" && agents ? agents.panes.get(machinePane(agents.machine.id, p.pane)) : undefined;
        return !current?.some(s => s.session_id !== p.agentSession && !(s.manager && agents?.machine.manager && s.manager !== agents.machine.manager) &&
          !(s.workspace_id && s.workspace_id !== sp.id) && !(s.space_id && s.space_id !== sp.id) &&
          !(s.tab_id && s.tab_id !== tab.id) && !(s.slot_id && s.slot_id !== tab.id));
      });
      return { ...tab, split: tab.panes.length > 1, panes };
    }).filter((tab) => tab.panes.length > 0);
    if (tabs.length) out.push({ ...sp, tabs });
  }
  return out;
}
