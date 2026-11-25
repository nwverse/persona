// The Agents list By project (knowledge base, lib/agent-projects.ts): "Needs you" across every project at the top, then
// a section per project the Overview board shows (its sub-projects rolled up into it, named on the row), its Agents by
// state, and Unassigned last. Each row is the short title and one line of what it is doing (SessionRow's own line).
import { memo } from "react";
import { t } from "../i18n";
import type { ProjectAgentGroup, ProjectAgentList, ProjectSection } from "../lib/agent-projects";
import { stateName } from "../lib/state-labels";
import type { Machine, Session } from "../lib/types";
import { useFolded } from "../lib/use-folded";
import { Icon } from "../ui/icons";
import { type GoFn, GroupCount, holdsRow, type RegFn, type RowMenuFn, Section } from "./SidebarParts";
import { NeedsCount } from "./SidebarSection";
import { SessionRow } from "./SidebarRows";

interface RowProps { list: ProjectAgentList; machines: Machine[]; reg: RegFn; hover: (k: string | null) => void; activeKey: string; go: GoFn; onRowMenu: RowMenuFn }

function ProjectRow({ s, list, reg, hover, activeKey, go, onRowMenu, strip = false }: RowProps & { s: Session; strip?: boolean }) {
  const k = `s:${s.machine_id}/${s.session_id}`;
  const row = list.rows.get(`${s.machine_id}/${s.session_id}`);
  // the row's end: under a heading its sub-project; in the Needs you strip its project (› sub-project)
  const place = strip ? [row?.head, row?.sub].filter(Boolean).join(" › ") : row?.sub ? `› ${row.sub}` : "";
  // no computer: the row is its short title, its one line and where it belongs
  const where = place;
  return <SessionRow s={s} k={k} reg={reg} hover={hover} active={activeKey === k} to={{ name: "session", mid: s.machine_id, sid: s.session_id }} go={go} label={row?.title || undefined} hint={where || undefined} showTodoTag={false} onMenu={onRowMenu} />;
}

/** A state's rows inside a project, under the state's word; Seen folds by default. */
function StateRows({ group, section, ...p }: RowProps & { group: ProjectAgentGroup; section: ProjectSection }) {
  const foldable = section.key === "idle";
  const [folded, toggle] = useFolded(`kbproject:${group.key || "-"}:${section.key}`, foldable, { follow: true, holds: holdsRow(section.sessions, p.activeKey) });
  const shut = foldable && folded;
  const word = stateName(section.key);
  return (
    <div data-project-section={section.key}>
      {foldable ? (
        <button type="button" aria-expanded={!shut} onClick={toggle} className="touch-hit relative z-10 flex w-full items-center gap-1 px-2 pt-1.5 pb-0.5 text-left text-[10.5px] text-ink-3 hover:text-ink-2">
          <span className="truncate">{word}</span>
          <span className="tabular-nums">{section.sessions.length}</span>
          <Icon name="chevron" size={9} stroke={2.4} className="shrink-0 opacity-60" style={{ transform: shut ? "rotate(-90deg)" : "none" }} />
        </button>
      ) : (
        <div className="flex items-center gap-1 px-2 pt-1.5 pb-0.5 text-[10.5px] text-ink-3"><span className="truncate">{word}</span></div>
      )}
      {!shut && section.sessions.map((s) => <ProjectRow key={`${s.machine_id}/${s.session_id}`} s={s} {...p} />)}
    </div>
  );
}

const ProjectGroup = memo(function ProjectGroup({ group, ...p }: RowProps & { group: ProjectAgentGroup }) {
  const sessions = group.sections.flatMap((x) => x.sessions);
  // a quiet project folds at first; one whose Agent needs you never does (they are in the strip, its number here)
  const [folded, toggle] = useFolded(`kbproject:${group.key || "-"}`, group.quiet, { follow: true, holds: holdsRow(sessions, p.activeKey) });
  const title = (
    <span className="flex min-w-0 items-center gap-1.5">
      <Icon name={group.rest ? "todo" : "folder"} size={12} stroke={2} className="shrink-0 text-ink-3" />
      <span className="truncate normal-case tracking-normal text-[11.5px] text-ink-2">{group.name}</span>
    </span>
  );
  const right = (
    <span className="flex items-center gap-1">
      <NeedsCount n={group.needs} />
      {folded && group.quiet && group.needs === 0
        ? <span className="text-[10.5px] whitespace-nowrap text-ink-3">{t("sidebar.kbproject_all_seen", { count: group.count })}</span>
        : <GroupCount n={group.count} label={t("sidebar.agent_count", { count: group.count })} />}
    </span>
  );
  return (
    <div data-kbproject={group.rest ? "unassigned" : group.key}>
      <Section title={title} folded={folded} onToggle={toggle} right={right}>
        {group.sections.length === 0
          ? <p className="px-2 py-1 text-[11.5px] text-ink-3">{t("sidebar.kbproject_needs_only")}</p>
          : group.sections.map((section) => <StateRows key={section.key} group={group} section={section} {...p} />)}
      </Section>
    </div>
  );
});

export function ProjectAgentsView({ list, ...p }: RowProps & { list: ProjectAgentList }) {
  // folds like any group, open at first; its number stays on the heading either way
  const [needsFolded, toggleNeeds] = useFolded("kbproject:needs", false, { holds: holdsRow(list.needsYou, p.activeKey) });
  return (
    <>
      {list.needsYou.length > 0 && (
        <div data-kbproject-needs="">
          <Section title={<span className="flex items-center gap-1 text-attention">{stateName("needs_you")}</span>} folded={needsFolded} onToggle={toggleNeeds} right={<NeedsCount n={list.needsYou.length} />}>
            {list.needsYou.map((s) => <ProjectRow key={`${s.machine_id}/${s.session_id}`} s={s} list={list} {...p} strip />)}
          </Section>
        </div>
      )}
      {list.groups.map((g) => <ProjectGroup key={g.key || "-"} group={g} list={list} {...p} />)}
    </>
  );
}
