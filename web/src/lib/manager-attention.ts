// What waits for the user at each assistant (docs/assistant/managers.md "Clients"): the switcher's count beside the other
// assistants and the dot on the sidebar's avatar. A count is the agents waiting (a pending card to approve or answer,
// or a session in Needs you; one agent with both counts once) plus the assistant's inbox items that need the user
// (`inbox_needs_you` of the roster; a hub without it: the agents only). The agents are counted here from what the hub
// already pushes: sessions and cards carry their `manager`.
import { useMemo } from "react";
import { stateOf } from "./format";
import { useShownManager } from "./manager-choice";
import { installedManagers, multiManager, ownerIn, useRoster, type ManagerList } from "./managers";
import { useFullAgentSessions } from "./sessions";
import { useStore } from "./store";
import type { Interaction, Session } from "./types";

/** The agents waiting for the user, per manager id: sessions in Needs you (quiet tasks' agents left out, as in the
 *  sidebar's count: the main conversation tells about those) and pending cards, each agent once. A card without a
 *  session (a script's request) counts by itself. */
export function waitingAgents(list: ManagerList, sessions: readonly Session[], interactions: readonly Interaction[]): Record<string, number> {
  const waiting = new Map<string, Set<string>>();
  const add = (manager: string | undefined, key: string) => {
    const id = ownerIn(list, manager);
    if (!waiting.has(id)) waiting.set(id, new Set());
    waiting.get(id)!.add(key);
  };
  for (const s of sessions) if (!s.assistant && !s.quiet && stateOf(s) === "needs_you") add(s.manager, `${s.machine_id}/${s.session_id}`);
  for (const ix of interactions) add(ix.manager, ix.session_id ? `${ix.machine_id}/${ix.session_id}` : `card:${ix.id}`);
  return Object.fromEntries([...waiting].map(([id, keys]) => [id, keys.size]));
}

/** Each installed assistant's count: its agents waiting plus its inbox items that need the user. */
export function managerBadges(list: ManagerList, sessions: readonly Session[], interactions: readonly Interaction[]): Record<string, number> {
  const agents = waitingAgents(list, sessions, interactions);
  return Object.fromEntries(installedManagers(list).map((m) => [m.id, (agents[m.id] ?? 0) + Math.max(0, m.inbox_needs_you ?? 0)]));
}

const NONE: Record<string, number> = {};

/** managerBadges for the roster as it is now; empty with one assistant (nothing shows a count then, and the complete
 *  list of agents is not asked for). */
export function useManagerBadges(): Record<string, number> {
  const roster = useRoster();
  const several = multiManager(roster);
  const { sessions } = useFullAgentSessions(several);
  const interactions = useStore((s) => s.interactions);
  return useMemo(() => (several ? managerBadges(roster, sessions, interactions) : NONE), [several, roster, sessions, interactions]);
}

/** Whether an assistant other than the current one has something waiting: the dot on the sidebar's avatar. */
export function useOthersWaiting(): boolean {
  const badges = useManagerBadges();
  const current = useShownManager();
  return Object.entries(badges).some(([id, n]) => id !== current && n > 0);
}
