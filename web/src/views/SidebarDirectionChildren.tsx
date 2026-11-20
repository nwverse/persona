import { useMemo } from "react";
import { directionTaskNode } from "../lib/direction-agent-tree";
import type { DirectionTask } from "../lib/direction-tree";
import { useAgentSessions } from "../lib/sessions";
import { useStore } from "../lib/store";
import type { GoFn, RegFn } from "./SidebarParts";
import { TreeTodoRows, useMachineLabel } from "./SidebarTree";

export type DirectionChildrenProps = {
  id: string;
  tasks: DirectionTask[];
  reg: RegFn;
  hover: (k: string | null) => void;
  go: GoFn;
  onNavigate?: () => void;
};

/** The pinned row can show its counts before the owner opens this session tree. */
export default function SidebarDirectionChildren({ id, tasks, reg, hover, go, onNavigate }: DirectionChildrenProps) {
  const all = useAgentSessions();
  const machines = useStore((s) => s.machines);
  const machineLabel = useMachineLabel(machines);
  const todos = useMemo(() => {
    const sessions = new Map(all.map((s) => [`${s.machine_id}/${s.session_id}`, s]));
    const placed = new Set<string>();
    return tasks.map((td) => directionTaskNode(td, sessions, placed, { keyPrefix: "direction:task:", folded: true, standIns: true }));
  }, [all, tasks]);

  return todos.map((td) => (
    <TreeTodoRows key={td.key} td={td} machineLabel={machineLabel} noContext keyPrefix={`d:${id}:`} reg={reg} hover={hover} activeKey="" go={go} onNavigate={onNavigate} />
  ));
}
