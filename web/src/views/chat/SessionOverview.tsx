// The session overview's scrollable contents, loaded when the user turns it on.
import { useMemo, useState } from "react";
import { t } from "../../i18n";
import { formatTokens } from "../../lib/compaction";
import { contextHeaderLabel, contextShown, contextTier, contextUsage, useContextAsUsed } from "../../lib/context-usage";
import { sessionTitle, shortPath, stateOf } from "../../lib/format";
import { openPanel } from "../../lib/panel";
import { useStore } from "../../lib/store";
import { subagentTitle, subagentTree, type FinishedTask } from "../../lib/tasks";
import type { Session, Subagent } from "../../lib/types";
import { cx } from "../../ui/atoms";
import { Icon, type IconName } from "../../ui/icons";
import { StatusMark } from "../../ui/StatusMark";
import { SessionActivity } from "./Activity";
import { SessionInfoDialog } from "./SessionDialogs";

interface OverviewProps {
  session: Session;
  machineName: string;
  finished: readonly FinishedTask[];
  onStopLoop: () => void;
  onJumpToCall?: (call: string) => void;
}

export function SessionOverview({ session, machineName, finished, onStopLoop, onJumpToCall }: OverviewProps) {
  const [infoOpen, setInfoOpen] = useState(false);
  // Codex child threads are standalone sessions. Use the known rows from this machine and manager.
  const children = useStore((s) => (s.sessions[session.machine_id] ?? []).filter((row) =>
    row.session_id !== session.session_id && row.manager === session.manager && (
      row.parent_session_id === session.session_id || row.root_session_id === session.session_id ||
      (!row.parent_session_id && session.source_thread_id && row.parent_thread_id === session.source_thread_id)
    ),
  ));
  const agents = useMemo(() => {
    const embedded = session.subagents ?? [];
    const ids = new Set(embedded.map((a) => a.session_id));
    const threads = children.filter((child) => !ids.has(child.session_id)).map((child): Subagent => ({
      id: child.session_id,
      session_id: child.session_id,
      parent_id: child.parent_session_id,
      description: sessionTitle(child),
      running: stateOf(child) === "working" || stateOf(child) === "needs_you" || Boolean(child.background_working),
      started_at: child.updated_at,
    }));
    return [...embedded, ...threads];
  }, [children, session.subagents]);
  const running = agents.filter((a) => a.running).length;

  return (
    <>
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-3">
        <Environment session={session} machineName={machineName} onInfo={() => setInfoOpen(true)} />
        <OverviewContext session={session} />
        {agents.length > 0 && <Subagents agents={agents} running={running} open={(agent) => {
          openPanel({ kind: "agent", mid: session.machine_id, sid: agent.session_id });
        }} />}
        <SessionActivity session={session} finished={finished} onStopLoop={onStopLoop} onJumpToCall={onJumpToCall} />
      </div>
      <SessionInfoDialog session={session} machineName={machineName} open={infoOpen} onClose={() => setInfoOpen(false)} />
    </>
  );
}

function Environment({ session, machineName, onInfo }: Pick<OverviewProps, "session" | "machineName"> & { onInfo: () => void }) {
  const worktree = Boolean(session.worktree && session.repo_root && session.worktree !== session.repo_root);
  const worktreePath = worktree && (session.worktree !== session.cwd || !session.branch);
  const rows: { label: string; icon: IconName; value: string | undefined; badge?: string }[] = [
    { label: t("chat-sessiondialogs.machine"), icon: "monitor", value: machineName },
    { label: t("chat-sessiondialogs.folder"), icon: "folder", value: shortPath(session.cwd) },
    { label: t("chat-sessiondialogs.branch"), icon: "git", value: session.branch, badge: worktree && !worktreePath ? t("chat-sessionoverview.worktree") : undefined },
  ];
  if (worktreePath) {
    rows.push({ label: t("chat-sessionoverview.worktree"), icon: "folder", value: shortPath(session.worktree) });
  }
  return (
    <section className="py-3">
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-xs font-semibold text-ink-2">{t("chat-sessionoverview.environment")}</h3>
        <button type="button" onClick={onInfo} className="text-xs text-accent-ink hover:underline">{t("chat-sessiondialogs.info_title")}</button>
      </div>
      <dl className="mt-2 flex flex-col gap-1.5">
        {rows.filter((row) => row.value).map(({ label, icon, value, badge }) => (
          <div key={label} className="flex items-start gap-2 text-xs">
            <dt className="flex shrink-0 items-center gap-1.5 text-ink-3">
              <Icon name={icon} size={14} />
              {label}
            </dt>
            <dd className="min-w-0 flex-1 text-ink [overflow-wrap:anywhere]">{value}</dd>
            {badge && <dd className="shrink-0 text-ink-3">{badge}</dd>}
          </div>
        ))}
      </dl>
    </section>
  );
}

function OverviewContext({ session }: { session: Session }) {
  const asUsed = useContextAsUsed();
  const usage = contextUsage(session);
  if (!usage) return null;
  const label = contextHeaderLabel(usage, asUsed);
  const percent = contextShown(usage, asUsed);
  const nearFull = contextTier(usage) !== "ok";
  return (
    <section className="flex flex-col gap-2 border-t border-line py-3">
      <h3 className="text-xs font-semibold text-ink-2">{t("chat-sessiondialogs.context")}</h3>
      <div className="flex flex-wrap justify-between gap-x-2 gap-y-1 text-xs tabular-nums">
        <span className={nearFull ? "text-orange" : "text-ink"}>{label}</span>
        <span className="text-ink-3">{t("context.tokens", { used: formatTokens(usage.tokens), window: formatTokens(usage.window) })}</span>
      </div>
      <div role="progressbar" aria-label={label} aria-valuenow={percent} aria-valuemin={0} aria-valuemax={100} className="h-1.5 overflow-hidden rounded-full bg-hover-2">
        <div className={cx("h-full rounded-full", nearFull ? "bg-orange" : "bg-accent")} style={{ width: `${percent}%` }} />
      </div>
    </section>
  );
}

function Subagents({ agents, running, open }: { agents: Subagent[]; running: number; open: (agent: Subagent) => void }) {
  const [expanded, setExpanded] = useState(false);
  const ended = agents.length - running;
  return (
    <section className="border-t border-line py-2">
      <h3>
        <button type="button" aria-expanded={expanded} onClick={() => setExpanded(!expanded)}
          className="flex min-h-9 w-full items-center gap-2 rounded-control px-1 text-left text-xs hover:bg-hover">
          <Icon name="agent" size={15} className="shrink-0 text-ink-3" />
          <span className="flex-1 font-semibold text-ink-2">{t("chat-sessionoverview.subagents")}</span>
          {running > 0 && <span className="whitespace-nowrap text-accent-ink tabular-nums">{t("chat-sessionoverview.running_count", { count: running })}</span>}
          {ended > 0 && <span className="whitespace-nowrap text-ink-3 tabular-nums">{t("chat-sessionoverview.ended_count", { count: ended })}</span>}
          <Icon name="chevron" size={13} className={cx("shrink-0 text-ink-3", expanded && "rotate-180")} />
        </button>
      </h3>
      {expanded && (
        <ul className="mt-1 flex flex-col gap-0.5">
          {subagentTree(agents).map(({ agent, depth }) => {
            const doing = agent.running ? agent.last_tool?.summary || agent.last_tool?.name : undefined;
            return (
              <li key={agent.id} style={{ paddingInlineStart: `calc(var(--spacing) * ${Math.min(depth, 4) * 3})` }}>
                <button type="button" onClick={() => open(agent)}
                  className="flex min-h-9 w-full items-center gap-2 rounded-control px-2 py-1.5 text-left text-xs hover:bg-hover">
                  {agent.running ? <StatusMark state="working" size="sm" /> : <Icon name="check" size={12} className="shrink-0 text-ink-3" />}
                  <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                    <span className="truncate text-ink" title={subagentTitle(agent)}>{subagentTitle(agent)}</span>
                    {doing && <span className="truncate text-ink-3">{doing}</span>}
                  </span>
                  <span className="shrink-0 whitespace-nowrap text-ink-3">{t(agent.running ? "chat-subagentcard.status_running" : "chat-subagentcard.ended")}</span>
                  <Icon name="chevronRight" size={12} className="shrink-0 text-ink-3" />
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
