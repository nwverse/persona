// What the sidebar's files share: the row and menu types, a row's key, the section heading and count (SidebarSection,
// passed on from here), the badge and dot colors, the pages hook and navigation. It imports none of them (Sidebar,
// SidebarRows, SidebarTree, SidebarRail).
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { t } from "../i18n";
import { resolveTab } from "../lib/herdr-tree";
import { useInboxBadge } from "../lib/inbox";
import { useLarkHome } from "../lib/lark-chats";
import { agentKey } from "../lib/pins";
import { NAV_PAGES, extensionNavKey, sidebarActiveKey } from "../lib/persona-nav";
import { arrangePages, useSidebarPrefs } from "../lib/sidebar-pages";
import { useExtensionSlot } from "../lib/extension-slots";
import { toneOf } from "../lib/extension-ui";
import { BADGE_TONE, navIcon, pageRoute } from "./extensions/ExtensionSlots";
import { href, inWorkView, navigate, type Route } from "../lib/router";
import { useAgentSessions } from "../lib/sessions";
import { scoped, useScope, useScopedCounts, useScopedTodos } from "../lib/current-assistant";
import { triageTodos } from "../lib/todo-triage";
import { makeSessionLookup } from "../lib/todos";
import { useRestoredTodos } from "../lib/todo-restore";
import { useLarkOn, useStore } from "../lib/store";
import { connPhase } from "../lib/connection";
import { useWorkspaceBoardSetting } from "../lib/workspace-board-setting";
import type { Session } from "../lib/types";
import type { MenuItem } from "../ui/atoms";
import { AGENTS_FOLD, onFoldAll, setAgentsFolded } from "../lib/sidebar-api";
import { SESSION_LIST_STYLE } from "../ui/list-metrics";
import { Icon, type IconName } from "../ui/icons";
import type { MenuNode } from "../lib/node-actions";
import type { MenuAt } from "../ui/menu-model";

/** The open row's own fill (#2283): it stays while the pointer is elsewhere in the list, a step darker than hover. */
export const CURRENT_ROW = "bg-hover-2";
/** The least an agent row's title gets behind a prefix (styles.css `--row-title-min`, about 4.5 characters). */
export const ROW_TITLE_MIN = "min-w-(--row-title-min)";

/** The row a route lights up in the sidebar: a tab's page is the pane it shows (its row in the tree is the tab's). */
export function useActiveKey(route: Route): string {
  const all = useAgentSessions();
  const assistant = useStore((s) => s.assistant);
  const tabPath = route.name === "tab" ? href(route) : "";
  // eslint-disable-next-line react-hooks/exhaustive-deps -- `route` is parsed anew each render; its path says it all
  const tabPane = useMemo(() => (route.name === "tab" ? resolveTab(all, route) : undefined), [all, tabPath]);
  return inWorkView(route) ? "terminal" : route.name === "tab" ? (tabPane ? rowKey(tabPane) : "") : sidebarActiveKey(route, assistant?.main);
}

// One function for the component's life, so the rows given it skip a re-render (memo); a click still calls the
// latest onNavigate (the phone's drawer closing).
export function useGo(onNavigate?: () => void): GoFn {
  const latest = useRef(onNavigate);
  useLayoutEffect(() => {
    latest.current = onNavigate;
  });
  return useCallback(
    (to: Route) => (e: React.MouseEvent) => {
      if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
      e.preventDefault();
      navigate(to);
      latest.current?.();
    },
    [],
  );
}

/** One entry of the pages under Persona ("Overview", "Inbox" … "Settings", and the extensions' pages). */
export type NavPage = { k: string; to: Route; icon: IconName; label: string; short?: string; badge?: number | string; badgeTone?: keyof typeof BADGE_CLASS; hint?: string; title?: string };

/** The sidebar's pages ("Overview" … "Settings", the extensions' pages) and the connection, shared by the sidebar and its rail. */
export function useNavPages() {
  const phase = connPhase(useStore((s) => s.conn), useStore((s) => s.link));
  // the counts on the pages are the current assistant's (docs/assistant/managers.md "Clients"); the other assistants' are in
  // the switcher at the sidebar's head. Numbers, not the computers: a push of a computer's load or memory leaves them
  // equal and doesn't draw the sidebar again.
  const scope = useScope();
  const online = useStore((s) => scoped(s.machines, scope).filter((m) => m.online).length);
  const total = useStore((s) => scoped(s.machines, scope).length);
  const assistant = useStore((s) => s.assistant);
  const todos = useScopedTodos();
  const todosSupported = useStore((s) => s.todosSupported);
  // The To-dos page's open count (#3156): not shelved, not finished; quiet tasks (#1731) are told about in the main
  // conversation, not counted here.
  const agentSessions = useAgentSessions();
  const [restored] = useRestoredTodos();
  const uncompletedTodosCount = useMemo(() => triageTodos(todos, makeSessionLookup(agentSessions), Date.now(), restored, { quiet: false }).open, [todos, agentSessions, restored]);
  const counts = useScopedCounts();
  const inbox = useInboxBadge();
  const larkSwitch = useLarkHome();
  const lark = useLarkOn();
  // "Messages" needs the hub to read Feishu (feature `lark`) as well as this browser's switch
  const larkHome = larkSwitch && lark;
  const extensionPages = useExtensionSlot("nav_page");
  // off unless turned on in Settings › General › Agent (#2475)
  const boardOn = useWorkspaceBoardSetting().enabled === true;

  const main = assistant?.main ? assistant.conversations.find((c) => c.session_id === assistant.main) : undefined;
  const page: Record<(typeof NAV_PAGES)[number], NavPage | false> = {
    home: { k: "home", to: { name: "home" }, icon: "home", label: t("sidebar.nav_home"), short: t("sidebar.nav_home_short"), badge: counts.badge || undefined, badgeTone: "attention" },
    inbox: { k: "inbox", to: { name: "inbox" }, icon: "inbox", label: t("sidebar.nav_inbox"), short: t("sidebar.nav_inbox_short"), badge: inbox || undefined, badgeTone: "attention", title: inbox ? t("sidebar.inbox_badge_title", { count: inbox }) : undefined },
    health: { k: "health", to: { name: "health" }, icon: "heart", label: t("sidebar.nav_health"), short: t("sidebar.nav_health_short") },
    messages: larkHome && { k: "messages", to: { name: "messages" }, icon: "chat", label: t("sidebar.nav_messages") },
    "workspace-board": boardOn && { k: "workspace-board", to: { name: "workspace-board" }, icon: "layers", label: t("sidebar.nav_board"), short: t("sidebar.nav_board_short") },
    todos: todosSupported !== false && { k: "todos", to: { name: "todos" }, icon: "todo", label: t("sidebar.nav_todos"), badge: uncompletedTodosCount || undefined },
    routines: { k: "routines", to: { name: "routines" }, icon: "clock", label: t("sidebar.nav_routines"), short: t("sidebar.nav_routines_short") },
    kb: { k: "kb", to: { name: "kb" }, icon: "book", label: t("sidebar.nav_kb"), short: t("sidebar.nav_kb_short") },
    machines: { k: "machines", to: { name: "machines" }, icon: "machine", label: t("sidebar.nav_machines"), short: t("sidebar.nav_machines_short"), hint: total ? `${online}/${total}` : undefined },
    terminal: { k: "terminal", to: { name: "terminal" }, icon: "terminal", label: t("work-entry.title"), short: t("work-entry.short") },
    settings: { k: "settings", to: { name: "settings" }, icon: "gear", label: t("sidebar.nav_settings") },
  };
  const all = NAV_PAGES.flatMap((k): NavPage[] => [
    // the extensions' pages (docs/extensions/extensions.md "UI slots" nav_page): one entry each, right before "Settings"
    ...(k === "settings"
      ? extensionPages.map((u) => {
          const key = extensionNavKey(u.extension, u.id);
          return { k: key, to: pageRoute(u.extension, u.id), icon: navIcon(u.icon), label: u.title, badge: u.badge?.text || undefined, badgeTone: BADGE_TONE[toneOf(u.badge?.tone)], title: u.extension_name ? `${u.title} · ${u.extension_name}` : u.title };
        })
      : []),
    ...(page[k] ? [page[k]] : []),
  ]);

  // as arranged in Settings › General › Sidebar pages: the list, and the pages that wait under More (docs/web/web.md "The sidebar's pages")
  const { shown: pages, more } = arrangePages(all, useSidebarPrefs());

  return { phase, online, total, main, pages, more, all };
}

/** The pages under "More" as menu rows (the phone's grid, the rail): a choice opens the page, then `onNavigate`
 *  (the phone's drawer closes, as it does for a tap on any other page). */
export function morePagesMenu(more: NavPage[], activeKey: string, onNavigate?: () => void): MenuItem[] {
  const open = (to: Route) => () => {
    navigate(to);
    onNavigate?.();
  };
  return [
    ...more.map((p): MenuItem => ({ key: p.k, label: p.label, icon: p.icon, hint: p.badge !== undefined ? String(p.badge) : p.hint, checked: p.k === activeKey || undefined, onSelect: open(p.to) })),
    { key: "-arrange", label: t("sidebar.arrange_pages"), icon: "gear", divider: true, onSelect: open({ name: "settings" }) },
  ];
}

export { GroupCount, Section, SidebarGroup } from "./SidebarSection";

/** One action reflects the visible groups, including manual folds and keyboard/menu actions. */
export function GroupFoldButton({ groups, onAction }: { groups: React.RefObject<HTMLElement | null>; onAction: () => void }) {
  const [collapse, setCollapse] = useState(true);
  useEffect(() => {
    const root = groups.current;
    if (!root) return;
    const read = () => setCollapse([...root.querySelectorAll<HTMLButtonElement>("[data-sidebar-group-heading] button[aria-expanded]")]
      .some((button) => !button.closest("[data-sidebar-agents-controls], [inert]") && button.getAttribute("aria-expanded") === "true"));
    read();
    const observer = new MutationObserver(read);
    observer.observe(root, { attributes: true, attributeFilter: ["aria-expanded", "inert"], childList: true, subtree: true });
    const off = onFoldAll((folded) => setCollapse(!folded), AGENTS_FOLD);
    return () => { observer.disconnect(); off(); };
  }, [groups]);
  const label = t(collapse ? "sidebar.fold_all" : "sidebar.open_all");
  return <button type="button" title={label} aria-label={label} onClick={() => { setAgentsFolded(collapse); onAction(); }} className={SESSION_LIST_STYLE.foldButton}>
    <Icon name="foldToggle" size={12} stroke={2}  />
  </button>;
}


export type RegFn = (k: string) => (el: HTMLElement | null) => void;
/** An agent row's key in the sidebar (highlight, active): sidebarActiveKey's for a session. */
export const rowKey = (s: Pick<Session, "machine_id" | "session_id">) => `s:${agentKey(s)}`;
/** Whether the open row (`activeKey`) is one of these agents': its group opens for it (useFolded). */
export const holdsRow = (sessions: readonly Session[], activeKey: string) => sessions.some((s) => rowKey(s) === activeKey);
export type GoFn = (to: Route) => (e: React.MouseEvent) => void;
/** `at`: where a right click or the menu key opened it; without it (the ⋯) the row's menu toggles beside its button.
 *  A row's ⋯ lies beside its link, not in it, and gives as `anchor` its parent, the box holding both: the menu
 *  closes on a pointerdown outside its anchor, and a press on the ⋯ has to stay inside it for the click that
 *  follows to toggle. */
export type RowMenuFn = (s: Session, anchor: HTMLElement, at?: MenuAt) => void;
/** A machine's, a workspace's or a tab's menu (src/lib/node-actions.ts); `toggle`: from its ⋯, which closes it again
 *  (a row's ⋯ anchors it on the box holding the row and the ⋯, as in RowMenuFn). */
export type NodeMenuFn = (node: MenuNode, at: MenuAt, header?: string, toggle?: boolean) => void;

export const BADGE_CLASS = { attention: "bg-field text-attention", red: "bg-red-tint text-red", orange: "bg-orange-tint text-orange", green: "bg-green-tint text-green", accent: "bg-accent-tint text-accent-ink" } as const;

/** The badge of a folded page, as a dot on its icon. */
export const DOT_CLASS = { attention: "bg-attention", red: "bg-red", orange: "bg-orange", green: "bg-green", accent: "bg-accent" } as const;
