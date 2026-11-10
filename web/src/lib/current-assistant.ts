// What the pages show once there are several assistants (docs/assistant/managers.md "Clients"): only the current one's
// computers, agents, cards, inbox items and to-dos. The hub's lists stay whole (every item carries its `manager`), so a
// link to another assistant's item still opens it; the pages filter what they list. With one assistant nothing is
// filtered: an item without `manager`, or of a manager the roster doesn't know, is the default's (ownerIn).
import { useMemo } from "react";
import { useShownManager } from "./manager-choice";
import { managerOf, multiManager, ownerIn, useRoster, type Manager, type ManagerList } from "./managers";
import { stateCounts, useAgentSessions, useCounts, useFullAgentSessions } from "./sessions";
import { useStore } from "./store";
import type { Session } from "./types";

export interface Scope {
  /** More than one assistant is installed: the pages filter. */
  several: boolean;
  /** The current assistant's id. */
  id: string;
  /** Its roster record; null with one assistant, or before the roster is read. */
  manager: Manager | null;
  /** Whether a thing with this `manager` is the current assistant's (always, with one assistant). */
  has: (manager: string | undefined) => boolean;
}

export function scopeOf(list: ManagerList | null, current: string): Scope {
  if (!multiManager(list)) return { several: false, id: current, manager: null, has: () => true };
  return { several: true, id: current, manager: managerOf(list, current), has: (m) => ownerIn(list, m) === current };
}

/** The current assistant's scope; the same object until the roster or the pick changes. */
export function useScope(): Scope {
  const roster = useRoster();
  const current = useShownManager();
  return useMemo(() => scopeOf(roster, current), [roster, current]);
}

/** `items` of the current assistant (by each one's `manager`); the same array with one assistant. */
export function scoped<T extends { manager?: string }>(items: T[], scope: Scope): T[] {
  return scope.several ? items.filter((x) => scope.has(x.manager)) : items;
}

/** A list read from the store, of the current assistant only. */
export function useScoped<T extends { manager?: string }>(items: T[]): T[] {
  const scope = useScope();
  return useMemo(() => scoped(items, scope), [items, scope]);
}

/** The current assistant's computers. */
export function useScopedMachines() {
  return useScoped(useStore((s) => s.machines));
}

/** The current assistant's to-dos. */
export function useScopedTodos() {
  return useScoped(useStore((s) => s.todos));
}

/** The current assistant's pending cards. */
export function useScopedInteractions() {
  return useScoped(useStore((s) => s.interactions));
}

/** The agents the pages list: the current assistant's with several (from the complete list: the hub's first page
 *  can't be asked for one assistant's), else the list as the page had it. */
export function useScopedAgentSessions(): Session[] {
  const scope = useScope();
  const page = useAgentSessions();
  const { sessions } = useFullAgentSessions(scope.several);
  return useMemo(() => (scope.several ? scoped(sessions, scope) : page), [scope, sessions, page]);
}

/** useCounts for the current assistant: its agents by state, and its pending cards. */
export function useScopedCounts(): ReturnType<typeof useCounts> {
  const scope = useScope();
  const whole = useCounts();
  const { sessions } = useFullAgentSessions(scope.several);
  const interactions = useScopedInteractions().length;
  return useMemo(
    () => (scope.several ? { ...stateCounts(scoped(sessions, scope)), interactions, quietCounted: false } : whole),
    [scope, sessions, interactions, whole],
  );
}
