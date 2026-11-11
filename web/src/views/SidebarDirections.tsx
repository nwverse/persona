// A 常驻 row's disclosure in the sidebar (#4352): each leader row under Persona chats (#4610: its conversation's row with
// "Doing N · Needs you N") gets a chevron that folds open to the tasks the hub's direction tree lists under it (the one
// tree, lib/direction-tree.ts, the counts come from too), each task in its state word and opening its to-do, each task
// folding open to its agents, each agent to its own. Folded (the default, kept per leader on this device) the row is
// #4610's own. A leader the tree does not list (an older hub, a leader made a moment ago) keeps the plain row.
import { memo, useMemo } from "react";
import { t } from "../i18n";
import { agentRef, taskAgents, useDirectionTree, type Direction } from "../lib/direction-tree";
import { directionTaskNode } from "../lib/direction-agent-tree";
import { useAgentSessions } from "../lib/sessions";
import { useStore } from "../lib/store";
import { useFolded } from "../lib/use-folded";
import { Collapse, sidebarFoldMotion } from "../ui/atoms";
import { Icon } from "../ui/icons";
import type { GoFn, RegFn } from "./SidebarParts";
import { TreeTodoRows, useMachineLabel, within } from "./SidebarTree";

type Props = {
  /** The leader conversation's id (its direction's id in the tree). */
  id: string;
  /** Its name as the row shows it, for the disclosure's label. */
  name: string;
  /** The row #4610 draws for it. */
  row: React.ReactNode;
  reg: RegFn;
  hover: (k: string | null) => void;
  activeKey: string;
  go: GoFn;
  onNavigate?: () => void;
};

/** A 常驻 row and, with the tree, its disclosure and its tasks. */
export function SidebarDirectionRow({ id, row, ...p }: Props) {
  const tree = useDirectionTree();
  const node = useMemo(() => tree?.directions.find((d) => d.id === id && !d.secretary), [tree, id]);
  if (!node) return <>{row}</>;
  return <DirectionTasks key={id} id={id} row={row} node={node} {...p} />;
}

const DirectionTasks = memo(function DirectionTasks({ id, name, row, node, reg, hover, activeKey, go, onNavigate }: Props & { node: Direction }) {
  const all = useAgentSessions();
  const machines = useStore((s) => s.machines);
  const machineLabel = useMachineLabel(machines);
  // what is open inside it unfolds it: one of its tasks' agents
  const refs = useMemo(() => (node.tasks ?? []).flatMap((td) => taskAgents(td).map((a) => `s:${agentRef(a)}`)), [node]);
  const [folded, toggle] = useFolded(`direction:${id}`, true, { holds: refs.includes(activeKey) });
  const todos = useMemo(() => {
    const sessions = new Map(all.map((s) => [`${s.machine_id}/${s.session_id}`, s]));
    const placed = new Set<string>();
    return (node.tasks ?? []).map((td) => directionTaskNode(td, sessions, placed, { keyPrefix: "direction:task:", folded: true, standIns: true }));
  }, [all, node]);
  const tasks = todos.length;
  return (
    <div data-direction={id}>
      <div className="flex items-center">
        <div className="min-w-0 flex-1">{row}</div>
        <button
          type="button"
          aria-expanded={!folded}
          aria-label={t("sidebar.direction_tasks", { name })}
          title={t("sidebar.direction_tasks", { name })}
          disabled={tasks === 0}
          onClick={toggle}
          className="relative z-10 flex size-5 shrink-0 items-center justify-center rounded-chip text-ink-3 hover:bg-hover hover:text-ink-2 disabled:opacity-40 disabled:hover:bg-transparent [@media(pointer:coarse)]:size-11"
        >
          <Icon name="chevron" size={10} stroke={2.4} className="transition-transform" style={{ ...sidebarFoldMotion(!folded), transform: folded ? "rotate(-90deg)" : "none" }} />
        </button>
      </div>
      {tasks > 0 && (
        <Collapse open={!folded} sidebarFold>
          <div className="flex flex-col gap-px pl-3" inert={folded || undefined}>
            {todos.map((td) => (
              <TreeTodoRows key={td.key} td={td} machineLabel={machineLabel} noContext reg={reg} hover={hover} activeKey={within(td.sessions, activeKey)} go={go} onNavigate={onNavigate} />
            ))}
          </div>
        </Collapse>
      )}
    </div>
  );
});
