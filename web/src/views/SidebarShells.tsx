import { useLayoutEffect, useMemo, useRef, type ReactNode, type RefObject } from "react";
import { stateOf } from "../lib/format";
import { usePanes } from "../lib/terminal-panes";
import { liveHerdrPanes, shellsOnly, type ShellSpace } from "../lib/sidebar-shells";
import { buildTerminalTree, type PanesResponse, type TermSpace } from "../lib/terminals";
import type { Machine, Session } from "../lib/types";
import { useSidebarNodeMenu } from "./NodeMenu";
import { GroupCount, type GoFn, type NodeMenuFn, type RegFn } from "./SidebarParts";
import { TreeBlock } from "./SidebarTree";
import { NavRow } from "./SidebarRows";
import { useRoute } from "../lib/router";
import { termPaneNode, termSpaceNode, termTabNode } from "../lib/node-actions";
import { t } from "../i18n";
import type { GroupTerminals } from "./agents/GroupedSessions";

type ShellRowsProps = { machine: Machine; reg: RegFn; go: GoFn; hover: (key: string | null) => void; activeKey: string };

const noSessions: readonly Session[] = [];

export function MachineShells({ machine, layout, rows = noSessions, ...navigation }: ShellRowsProps & {
  layout: PanesResponse; rows?: readonly Session[]; onNodeMenu: NodeMenuFn;
}) {
  const tree = useMemo(() => buildTerminalTree(layout, []), [layout]);
  const occupied = useMemo(() => liveHerdrPanes(rows), [rows]);
  const spaces = useMemo(() => shellsOnly(tree, { machine, panes: occupied }), [tree, machine, occupied]);
  return spaces.length > 0 && <ShellTree mid={machine.id} online={machine.online} spaces={spaces} {...navigation} />;
}

/** Each open machine follows its inventory. An uncounted, folded machine gets one read to discover whether it has
 * a Shell to show. Neither case asks for a complete Agent list. */
export function PagedMachineShells({ machine, folded, discover, section, ...navigation }: ShellRowsProps & {
  folded: boolean; discover: boolean; section: (terminals?: GroupTerminals<Session>) => ReactNode;
}) {
  const { listing, refresh: refreshPanes } = usePanes([machine], !folded || discover, !folded);
  const data = listing[machine.id]?.data;
  const tree = useMemo(() => buildTerminalTree(data, []), [data]);
  const reported = useMemo(() => {
    const ids = new Set<string>();
    for (const host of data?.hosts ?? []) {
      if (host.kind !== "herdr") continue;
      for (const pane of host.panes) {
        if (pane.session_id) ids.add(pane.session_id);
        if (pane.agent_session) ids.add(pane.agent_session);
      }
    }
    return ids;
  }, [data]);
  const remembered = useRef(new Set<string>());
  const { onNodeMenu, nodeMenu } = useSidebarNodeMenu(refreshPanes);
  const { reg, go, hover, activeKey } = navigation;
  const terminals: GroupTerminals<Session> = useMemo(() => ({
    hasShells: shellsOnly(tree).length > 0,
    shows: s => !(s.machine_id === machine.id && !(s.manager && machine.manager && s.manager !== machine.manager) &&
      stateOf(s) === "offline" && (reported.has(s.session_id) || remembered.current.has(s.session_id))),
    render: rows => <PagedShellRows machine={machine} tree={tree} rows={rows} reported={reported} remembered={remembered}
      reg={reg} go={go} hover={hover} activeKey={activeKey} onNodeMenu={onNodeMenu} />,
  }), [machine, tree, reported, reg, go, hover, activeKey, onNodeMenu]);
  return <>{section(terminals)}{nodeMenu}</>;
}

function PagedShellRows({ machine, tree, rows, reported, remembered, ...navigation }: ShellRowsProps & {
  tree: TermSpace[]; rows: readonly Session[]; reported: ReadonlySet<string>; remembered: RefObject<Set<string>>; onNodeMenu: NodeMenuFn;
}) {
  // An offline summary can lose its pane coordinates. Remember a positive identification only while the inventory
  // or this bounded page still holds the session; History keeps its independent session address.
  useLayoutEffect(() => {
    const keep = new Set(reported);
    for (const s of rows) if (s.machine_id === machine.id && remembered.current.has(s.session_id)) keep.add(s.session_id);
    remembered.current = keep;
  }, [machine.id, rows, reported, remembered]);
  const spaces = useMemo(() => shellsOnly(tree, { machine, panes: liveHerdrPanes(rows) }), [machine, tree, rows]);
  return spaces.length > 0 && <ShellTree mid={machine.id} online={machine.online} spaces={spaces} {...navigation} />;
}

// "Terminal" under a machine (Settings › Appearance › Show terminals in sidebar): its terminal host's workspaces and the plain shells in them, each
// opening the "Terminal" page on that pane. Folds are kept by machine/workspace name like the Agents' above.
function ShellTree({ mid, online, spaces, reg, hover, go, activeKey, onNodeMenu }: { mid: string; online: boolean; spaces: ShellSpace[] } & Omit<ShellRowsProps, "machine"> & { onNodeMenu: NodeMenuFn }) {
  const route = useRoute();
  const shown = route.name === "terminal" && route.mid === mid ? route.id : undefined;
  const count = spaces.reduce((n, sp) => n + sp.tabs.reduce((m, t) => m + t.panes.length, 0), 0);
  return (
    <TreeBlock foldKey={`term:${mid}`} sessions={[]} activeKey={activeKey} title={<span className="min-w-0 truncate font-medium">{t("sidebar.nav_terminal")}</span>} right={() => <GroupCount n={count} label={t("sidebar.pane_count", { count })} />}>
      {spaces.map((sp) => (
        <TreeBlock
          key={`${sp.host}/${sp.id}`}
          foldKey={`term:${mid}/${sp.host}:${sp.label}`}
          sessions={[]}
          activeKey={activeKey}
          title={<span className="min-w-0 truncate font-medium">{sp.label}</span>}
          right={() => null}
          onMenu={(at) => onNodeMenu(termSpaceNode(mid, sp, online), at, t("sidebar.space_named", { name: sp.label }))}
        >
          {sp.tabs.flatMap((tab) =>
            tab.panes.map((p) => {
              const k = `term:${mid}/${p.id}`;
              const node = tab.split ? termPaneNode(mid, sp, tab, p, online) : termTabNode(mid, sp, tab, online);
              return (
                <NavRow
                  key={k}
                  k={k}
                  reg={reg}
                  hover={hover}
                  active={shown === p.id}
                  to={{ name: "terminal", mid, id: p.id }}
                  go={go}
                  icon="terminal"
                  label={tab.split && tab.label ? `${tab.label} · ${p.title}` : sp.host === "herdr" ? tab.name || p.title : p.title}
                  title={p.detail ? `${p.title}\n${p.detail}` : p.title}
                  menuLabel={t("sidebar.terminal_actions")}
                  onMenu={(at, _el, button) => onNodeMenu(node, at, tab.label, button)}
                />
              );
            }),
          )}
        </TreeBlock>
      ))}
    </TreeBlock>
  );
}
