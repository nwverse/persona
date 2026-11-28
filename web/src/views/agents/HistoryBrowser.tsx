import { useCallback, useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { formatDate, t } from "../../i18n";
import { credentialOwner } from "../../lib/api";
import { withoutBotmux } from "../../lib/agent-filter";
import { useBotmuxHidden } from "../../lib/agents-display";
import { agentId, sessionAgent, sessionAgentName } from "../../lib/agent-kind";
import { resumeAgent, resumeState, terminalResume, useResume } from "../../lib/agent-resume";
import { useScope, useScoped, useScopedMachines } from "../../lib/current-assistant";
import { sessionTitle } from "../../lib/format";
import { machineWithHub } from "../../lib/mesh";
import { paneRoute } from "../../lib/herdr-tree";
import { LAUNCH_HANDOFF_MS } from "../../lib/launch-handoff";
import { useRuntimeSession } from "../../lib/runtime-session";
import { navigate, routeAt, routePath } from "../../lib/router";
import { runtimeIdentity, SESSIONS_LOOKUP_FEATURE } from "../../lib/runtime-list";
import { RUNTIME_SEARCH_FEATURE, RuntimeSearchModel } from "../../lib/runtime-search";
import { historyGroups, historySessions } from "../../lib/session-history";
import { retryFullAgentSessions, useFullAgentSessions } from "../../lib/sessions";
import { onHubEvent, rememberSessionRow, useStore } from "../../lib/store";
import type { Machine, Session } from "../../lib/types";
import { Button, useNow } from "../../ui/atoms";
import { SearchField } from "../../ui/SearchField";
import { WindowedRows } from "../WindowedRows";
import { WorkSessionRow } from "./WorkSessionBrowser";

const copy = (key: string) => t(`session-history.${key}`);
const selectClass = "min-h-8 min-w-0 flex-1 rounded-control bg-inset px-2 py-1 text-xs text-ink shadow-hairline focus-visible:outline-accent [@media(pointer:coarse)]:min-h-11";

interface ResumeTarget {
  session: Session;
  origin: string;
  accepted: boolean;
}

function stillAtResumeOrigin(origin: string) {
  if (location.href === origin) return true;
  const before = routeAt(origin), now = routeAt(location.href);
  // The shell replaces a session URL with its tab URL once that session's detail arrives.
  return before?.name === "session" && now?.name === "tab" && !!now.sid &&
    routePath(before) === routePath({ ...now, name: "session", sid: now.sid });
}

// The row leaves History as soon as it becomes live, which can happen before the POST returns.
function ResumeDestination({ target, listed, lookup, finish }: {
  target: ResumeTarget; listed?: Session; lookup: boolean; finish: (row?: Session) => void;
}) {
  const { machine_id, session_id } = target.session;
  const { detail } = useRuntimeSession(machine_id, session_id, lookup);
  const row = lookup && !detail.legacy ? detail.snapshot?.row : listed;
  useEffect(() => {
    if (!row || row.deleted || row.starting) return;
    const live = ["idle", "working", "blocked", "done"].includes(row.status) ||
      (row.status === "unknown" && Boolean(row.pane_id || (row.space_id && row.slot_id)));
    if (!live) return;
    // Let the shell finish replacing the previous session's URL in this render before opening the resumed Agent.
    let cancelled = false;
    queueMicrotask(() => {
      if (!cancelled) finish(stillAtResumeOrigin(target.origin) ? row : undefined);
    });
    return () => { cancelled = true; };
  }, [row, target.origin, finish]);
  useEffect(() => {
    const timer = setTimeout(finish, LAUNCH_HANDOFF_MS);
    return () => clearTimeout(timer);
  }, [finish]);
  return null;
}

function HistoryRow({ session, machines, active, now, pending, onResume, onSelect }: {
  session: Session; machines: Machine[]; active: boolean; now: number; pending: boolean;
  onResume: (s: Session) => void; onSelect: (s: Session) => void;
}) {
  const machine = machines.find(m => m.id === session.machine_id);
  const resume = useResume(session.machine_id, session.session_id);
  const available = terminalResume(session, machine) === "available" && machine?.online;
  return <div data-history-row={session.session_id}>
    <div className="flex min-w-0 items-center gap-1">
      <div className="min-w-0 flex-1"><WorkSessionRow session={{ ...session, harness_name: sessionAgentName(session) }} name={machineWithHub(session.machine_id, machines)} active={active} now={now} depth={0}
        onSelect={() => onSelect(session)} /></div>
      {available && <Button tone="quiet" busy={pending || resume.phase === "resuming"} aria-label={t("session-history.resume_named", { title: sessionTitle(session) })}
        onClick={() => onResume(session)}>
        {t("session-terminalpanel.resume")}
      </Button>}
    </div>
    {resume.phase === "failed" && <p role="alert" className="px-2 text-xs text-red">{resume.error}</p>}
  </div>;
}

/** The complete sidebar snapshot owns visibility/state; content search uses the existing frozen search window. */
export function HistoryBrowser({ activeSession }: { activeSession?: { machineId: string; sessionId: string } }) {
  const full = useFullAgentSessions();
  const scoped = useScoped(full.sessions);
  const hideBotmux = useBotmuxHidden();
  const machines = useScopedMachines();
  const scope = useScope();
  const features = useStore(s => s.features);
  const [pending, setPending] = useState<ResumeTarget>();
  function startResume(session: Session) {
    const { machine_id, session_id } = session;
    if (resumeState(machine_id, session_id).phase === "resuming") return;
    const target = { session, origin: location.href, accepted: false };
    setPending(target);
    void resumeAgent(machine_id, session_id).then(ok => {
      const result = ok ? { ...target, accepted: true } : undefined;
      setPending(latest => latest === target ? result : latest);
    });
  }
  const finish = useCallback((row?: Session) => {
    setPending(undefined);
    if (row) navigate(paneRoute(row));
  }, []);
  function selectSession(session: Session) {
    setPending(undefined);
    rememberSessionRow(session);
    navigate({ name: "session", mid: session.machine_id, sid: session.session_id, work: true });
  }
  const [query, setQuery] = useState("");
  const [machine, setMachine] = useState("");
  const [agent, setAgent] = useState("");
  const owner = credentialOwner();
  const model = useMemo(() => new RuntimeSearchModel(), [owner]);
  const search = useSyncExternalStore(model.subscribe, model.snapshot, model.snapshot);
  const capable = features.includes(RUNTIME_SEARCH_FEATURE);
  useEffect(() => { model.search(capable ? query : "", { manager: scope.several ? scope.id : undefined }); }, [model, query, capable, scope.several, scope.id]);
  useEffect(() => () => model.dispose(), [model]);
  useEffect(() => {
    if (!query.trim() || !capable) return;
    // Reconcile a resumed/removed row and fresh search evidence after the same list hint as the sidebar.
    const refresh = () => { void model.retry(); };
    const off = onHubEvent("sessions_list", refresh);
    const hello = onHubEvent("hello", refresh);
    return () => { off(); hello(); };
  }, [model, query, capable]);
  const now = useNow();
  const allHistory = useMemo(() => historySessions(withoutBotmux(scoped, hideBotmux)), [scoped, hideBotmux]);
  const current = search.query === query.trim();
  const rows = useMemo(() => {
    let source = scoped;
    if (query.trim()) {
      if (capable) {
        const live = new Map(scoped.map(s => [runtimeIdentity(s), s]));
        source = current ? search.hits.flatMap(hit => { const row = live.get(runtimeIdentity(hit.session)); return row ? [row] : full.loaded ? [] : [hit.session]; }).filter(s => scope.has(s.manager)) : [];
      } else {
        // Older hubs expose title/preview search only; say so rather than claiming a complete conversation search.
        const text = query.trim().toLocaleLowerCase();
        source = scoped.filter(s => [sessionTitle(s), s.last_text, s.activity].some(value => value?.toLocaleLowerCase().includes(text)));
      }
    }
    return historySessions(withoutBotmux(source, hideBotmux), { machine, agent });
  }, [scoped, query, capable, current, search.hits, scope, machine, agent, full.loaded, hideBotmux]);
  const groups = useMemo(() => historyGroups(rows), [rows]);
  const agents = new Map(allHistory.map(s => [agentId(sessionAgent(s).agent), sessionAgentName(s)]));
  const pendingOutsideList = pending && !rows.some(s => runtimeIdentity(s) === runtimeIdentity(pending.session));
  const loading = !full.loaded || Boolean(query.trim() && capable && (!current || search.loading));
  const error = full.error || (query.trim() && capable && current ? search.error || search.moreError : "");
  return <section aria-label={copy("title")} data-history-browser className="@container/work-sessions min-w-0">
    <div className="space-y-2 px-2 py-2">
      <SearchField value={query} onChange={setQuery} placeholder={copy("search")} />
      <div className="flex flex-wrap gap-2">
        <select aria-label={copy("computer")} className={selectClass} value={machine} onChange={e => setMachine(e.target.value)}>
          <option value="">{copy("all_computers")}</option>
          {machines.map(m => <option key={m.id} value={m.id}>{m.name}</option>)}
        </select>
        <select aria-label={copy("agent")} className={selectClass} value={agent} onChange={e => setAgent(e.target.value)}>
          <option value="">{copy("all_agents")}</option>
          {[...new Set([...agents.keys(), ...(agent ? [agent] : [])])].map(id => <option key={id} value={id}>{agents.get(id) || agent}</option>)}
        </select>
      </div>
      {!capable && query.trim() && <p className="text-xs text-ink-3">{copy("older_search")}</p>}
    </div>
    {pending?.accepted && <ResumeDestination key={runtimeIdentity(pending.session)} target={pending}
      listed={scoped.find(s => s.machine_id === pending.session.machine_id && s.session_id === pending.session.session_id)}
      lookup={features.includes(SESSIONS_LOOKUP_FEATURE)} finish={finish} />}
    {pendingOutsideList && <p role="status" aria-busy="true" className="px-2 py-2 text-xs text-ink-3">
      {t("session-history.resume_named", { title: sessionTitle(pending.session) })}…
    </p>}
    {loading && <p role="status" className="px-2 py-2 text-xs text-ink-3">{t("sidebar.loading")}</p>}
    {error && <div role="alert" className="px-2 py-2 text-xs text-red">{error} <Button tone="quiet" onClick={() => { retryFullAgentSessions(); void model.retry(); }}>{t("sidebar.retry")}</Button></div>}
    {groups.map(group => <section key={group.key} data-history-day={group.key}>
      <h2 className="flex items-baseline gap-2 bg-inset px-2 py-1.5 text-xs font-medium text-ink-2">
        <span className="min-w-0 flex-1">{group.date ? formatDate(group.date, { dateStyle: "full" }) : copy("earlier")}</span>
        <span data-history-count className="tabular-nums">{group.sessions.length}</span>
      </h2>
      <WindowedRows items={group.sessions} holds={s => activeSession?.machineId === s.machine_id && activeSession.sessionId === s.session_id}
        render={s => <HistoryRow key={runtimeIdentity(s)} session={s} machines={machines} now={now}
          pending={!!pending && runtimeIdentity(pending.session) === runtimeIdentity(s)} onResume={startResume} onSelect={selectSession}
          active={activeSession?.machineId === s.machine_id && activeSession.sessionId === s.session_id} />} />
    </section>)}
    {!pendingOutsideList && !loading && !error && !rows.length && <p className="px-2 py-2 text-xs text-ink-3">{query || machine || agent ? t("sidebar.no_match") : copy("empty")}</p>}
    {query.trim() && capable && current && search.hasMore && <div className="px-2 py-2"><Button busy={search.loadingMore} onClick={() => void model.more()}>{t("paged-agents.more")}</Button></div>}
  </section>;
}
