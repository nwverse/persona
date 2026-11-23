// The Workbench sidebar (#13439, docs/web/web.md "Workbench (Labs)"): Persona's main conversation as one row, then the
// Agents' threads by project across computers. A thread that needs you shows in its project without opening anything; the
// counts are the sidebar's own numbers (the same rows, the same folded state). Rows are links, so open in a new tab and
// middle click work, and the next / previous row shortcuts (⌘⌥↑ / ⌘⌥↓) walk them like the usual sidebar's.
import { useMemo } from "react";
import { t } from "../../i18n";
import { ago, backgroundWaiting, sessionTitle } from "../../lib/format";
import { useFullAgentSessions } from "../../lib/sessions";
import { openNewAgent } from "../../lib/new-agent";
import { href, type Route } from "../../lib/router";
import { useStore } from "../../lib/store";
import { STATE_LABEL, stateCount, stateName } from "../../lib/state-labels";
import { useBindings, formatChord } from "../../lib/shortcuts";
import { readWorkbenchLayout, useWorkbenchLayout, writeWorkbenchLayout } from "../../lib/labs-workbench";
import { useGo } from "../SidebarParts";
import { CollapseButton } from "../SidebarRail";
import { cx, IconButton } from "../../ui/atoms";
import { CountBadge } from "../../ui/CountBadge";
import { Icon } from "../../ui/icons";
import { foldedState } from "../../lib/child-sessions";
import { StatusMark } from "../../ui/StatusMark";
import { groupByProject, type Project } from "./project-groups";
import type { Session } from "../../lib/types";

const CURRENT = "bg-hover-2";

function threadRoute(s: Session): Route {
  return { name: "session", mid: s.machine_id, sid: s.session_id };
}

/** One thread: its title and the plain word for its state; the state dot stands where a state needs saying. */
export function ThreadRow({ s, active, go }: { s: Session; active: boolean; go: ReturnType<typeof useGo> }) {
  const st = foldedState(s);
  const bg = backgroundWaiting(s);
  const word = bg ? stateName("bg_waiting") : STATE_LABEL[st];
  const to = threadRoute(s);
  return (
    <a
      href={href(to)}
      onClick={go(to)}
      aria-current={active ? "page" : undefined}
      data-workbench-thread
      className={cx("flex w-full items-center gap-2 rounded-[7px] px-2 sidebar-row transition-colors duration-150", active ? CURRENT : "hover:bg-hover focus-visible:bg-hover")}
    >
      <span className="flex w-3 shrink-0 justify-center"><StatusMark state={st} size="sm" label={false} waiting={bg} /></span>
      <span className="min-w-0 flex-1 truncate text-meta text-ink">{sessionTitle(s)}</span>
      <span className={cx("shrink-0 text-submeta", st === "needs_you" ? "text-attention" : "text-ink-3")}>{st === "idle" || st === "unknown" ? ago(s.activity_at || s.updated_at) : word}</span>
    </a>
  );
}

function ProjectBlock({ p, folded, activeSid, go }: { p: Project; folded: boolean; activeSid: string; go: ReturnType<typeof useGo> }) {
  // a project with the open thread, or one that needs you, is never drawn folded: nothing waiting is hidden
  const open = !folded || p.needs > 0 || p.threads.some((s) => s.session_id === activeSid);
  const toggle = () => {
    const keys = new Set(readWorkbenchLayout().folded);
    if (keys.has(p.key)) keys.delete(p.key);
    else keys.add(p.key);
    writeWorkbenchLayout({ folded: [...keys] });
  };
  return (
    <section aria-label={p.label} data-workbench-project className="mb-1">
      <div className="group/proj flex items-center gap-1 pr-1">
        <button
          type="button"
          onClick={toggle}
          aria-expanded={open}
          aria-label={t(open ? "workbench.collapse_project" : "workbench.expand_project", { name: p.label })}
          className="flex min-h-7 min-w-0 flex-1 items-center gap-1.5 rounded-[7px] px-2 text-left hover:bg-hover"
        >
          <Icon name="chevronRight" size={11} stroke={2} className={cx("shrink-0 text-ink-3 transition-transform duration-150", open && "rotate-90")} />
          <span className="min-w-0 flex-1 truncate text-label font-semibold text-ink-2">{p.label}</span>
          <CountBadge count={p.needs} dense label={stateCount("needs_you", p.needs)} />
        </button>
        {p.cwd && (
          <IconButton
            icon="compose"
            size={24}
            iconSize={13}
            label={t("workbench.new_thread_in", { name: p.label })}
            onClick={() => openNewAgent({ machineId: p.machineId, cwd: p.cwd })}
            className="opacity-0 focus-visible:opacity-100 group-hover/proj:opacity-100 [@media(hover:none)]:opacity-100"
          />
        )}
      </div>
      {open && (
        <div className="flex flex-col gap-px">
          {p.threads.map((s) => <ThreadRow key={`${s.machine_id}/${s.session_id}`} s={s} active={s.session_id === activeSid} go={go} />)}
        </div>
      )}
    </section>
  );
}

export function WorkbenchSidebar({ route, onSearch, onNewAgent }: { route: Route; onSearch: () => void; onNewAgent: () => void }) {
  const { sessions } = useFullAgentSessions();
  const machines = useStore((s) => s.machines);
  const assistant = useStore((s) => s.assistant);
  const bindings = useBindings();
  const go = useGo();
  const layout = useWorkbenchLayout();
  const projects = useMemo(() => groupByProject(sessions, machines, t("workbench.other_project")), [sessions, machines]);
  const main = assistant?.main ? assistant.conversations.find((c) => c.session_id === assistant.main) : undefined;
  const pending = main?.pending_interactions ?? 0;
  const activeSid = route.name === "session" ? route.sid : route.name === "tab" ? (route.sid ?? "") : "";
  const folded = new Set(layout.folded);
  const personaTo: Route = { name: "persona" };
  const settingsTo: Route = { name: "settings" };
  const searchHint = bindings.jump ? formatChord(bindings.jump) : "";
  return (
    <aside aria-label={t("workbench.sidebar_label")} data-workbench-sidebar className="flex h-full w-full flex-col bg-canvas">
      <div className="px-2.5 pt-2.5">
        <div className="mb-2 flex items-center gap-1">
          <button type="button" onClick={onSearch} className="touch-hit flex h-8 min-w-0 flex-1 items-center gap-2 rounded-control bg-inset px-2.5 text-left shadow-hairline transition-colors hover:bg-hover">
            <Icon name="search" size={12} stroke={2} className="text-ink-3" />
            <span className="min-w-0 flex-1 truncate text-label text-ink-3">{t("sidebar.search_sessions")}</span>
            {searchHint && <span className="hidden sm:inline-flex [@media(hover:none)]:hidden text-submeta text-ink-3">{searchHint}</span>}
          </button>
          <IconButton icon="compose" label={t("workbench.new_thread")} onClick={onNewAgent} />
          <CollapseButton collapsed={false} chord={bindings["left-sidebar"]} />
        </div>
      </div>
      <nav data-sidebar-rows data-row-scroller aria-label={t("workbench.projects")} className="relative isolate min-h-0 flex-1 overflow-y-auto overscroll-contain px-2.5 pb-3">
        <a
          href={href(personaTo)}
          onClick={go(personaTo)}
          aria-current={route.name === "persona" ? "page" : undefined}
          data-workbench-persona
          className={cx("mb-1 flex w-full items-center gap-2 rounded-[7px] px-2 sidebar-row transition-colors duration-150", route.name === "persona" ? CURRENT : "hover:bg-hover focus-visible:bg-hover")}
        >
          <Icon name="home" size={14} stroke={1.8} className="shrink-0 text-ink" />
          <span className="min-w-0 flex-1 truncate text-meta font-semibold text-ink">{t("sidebar.main_chat")}</span>
          <CountBadge count={pending} label={stateCount("needs_you", pending)} />
        </a>
        {projects.map((p) => <ProjectBlock key={p.key} p={p} folded={folded.has(p.key)} activeSid={activeSid} go={go} />)}
      </nav>
      <div className="flex items-center gap-1 border-t border-line px-2.5 py-2">
        <a
          href={href(settingsTo)}
          onClick={go(settingsTo)}
          aria-current={route.name === "settings" ? "page" : undefined}
          className={cx("flex h-8 min-w-0 flex-1 items-center gap-2 rounded-[7px] px-2 text-meta text-ink-2", route.name === "settings" ? CURRENT : "hover:bg-hover")}
        >
          <Icon name="gear" size={14} stroke={1.8} />
          <span className="truncate">{t("app.title_settings")}</span>
        </a>
      </div>
    </aside>
  );
}
