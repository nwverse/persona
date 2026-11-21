import { useId, useMemo, useState, type ReactNode } from "react";
import { formatNumber, t } from "../../i18n";
import { stripAnsi } from "../../lib/ansi";
import { ago } from "../../lib/format";
import { blockReason } from "../../lib/launcher-health";
import { runtimeLabel } from "../../lib/model-config";
import {
  groupOverviewSessions, type OverviewAgentFit, type OverviewChat, type OverviewFit, type OverviewMachine, type OverviewProvider, type OverviewRunningGroup,
} from "../../lib/model-overview";
import { navigate } from "../../lib/router";
import { Button, Collapse, Dialog, cx } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { Segmented } from "../assistant/parts";
import { Group, Pill, Row, SettingsHint } from "./SettingsParts";

type ComputerActions = {
  onPurpose: (id: string) => void;
  onProvider: (name?: string) => void;
  onMachine: (id: string) => void;
  onRefreshMachine: (id: string) => void;
};

type Props = ComputerActions & {
  machines: OverviewMachine[];
  running: ReadonlyMap<string, OverviewRunningGroup[]>;
  refreshingMachine?: string | null;
  query: string;
};

type FitTarget = { machine: string; purpose: string };
type TranslatorVars = Parameters<typeof t>[1];

const words = (key: string, vars?: TranslatorVars) => t(`model-overview-computers.${key}`, vars);
const columnKeys = ["computer", "new_agent", "chats", "running", "providers"] as const;
const columns = "gap-x-4 gap-y-3 px-4 sm:grid-cols-2 lg:grid-cols-5";
const tapTarget = "pointer-coarse:min-h-11 pointer-coarse:min-w-11";
const noGroups: OverviewRunningGroup[] = [];

function matchesComputer(machine: OverviewMachine, running: OverviewRunningGroup[], q: string) {
  if (!q) return true;
  const contains = (value?: string | null) => value?.toLocaleLowerCase().includes(q);
  return [machine.name, machine.new_agent.launcher, machine.new_agent.model, machine.new_agent.instead_of].some(contains)
    || machine.chats.some((chat) => [chat.name, chat.launcher, chat.model, chat.instead_of].some(contains))
    || machine.providers.some((provider) => [provider.name, provider.display_name, provider.model].some(contains))
    || running.some((group) => [group.launcher, group.model, group.agent].some(contains))
    || machine.tiers.some((tier) => contains(tier.name));
}

export function ModelOverviewComputers({ machines, running, refreshingMachine, query, ...actions }: Props) {
  const [why, setWhy] = useState<FitTarget | null>(null);
  const q = query.trim().toLocaleLowerCase();
  const visible = useMemo(() => machines.filter((machine) => matchesComputer(machine, running.get(machine.id) ?? noGroups, q)),
    [machines, running, q]);
  const whyMachine = why && machines.find((machine) => machine.id === why.machine);
  const whyChat = why && whyMachine && why.purpose !== "agent.new" ? whyMachine.chats.find((chat) => chat.purpose === why.purpose) : undefined;
  const whyFit = why && whyMachine && (why.purpose === "agent.new" ? whyMachine.new_agent : whyChat);

  return (
    <Group title={words("computers")}>
      {visible.length > 0 ? (
        <>
          <div aria-hidden className={cx(columns, "hidden bg-inset py-2 text-label font-medium text-ink-3 lg:grid")}>
            {columnKeys.map((key) => <span key={key}>{words(key)}</span>)}
          </div>
          {visible.map((machine) => (
            <ComputerRow key={machine.id} machine={machine} running={running.get(machine.id) ?? noGroups}
              query={q} actions={actions} refreshing={refreshingMachine === machine.id} onWhy={setWhy} />
          ))}
        </>
      ) : <SettingsHint>{words(q ? "no_match" : "no_machines")}</SettingsHint>}
      <Dialog open={Boolean(whyFit)} onClose={() => setWhy(null)}
        title={whyMachine ? words("why_title", { purpose: whyChat?.name ?? words("agents_purpose"), machine: whyMachine.name }) : undefined}
        footer={why && whyMachine && whyFit ? (
          <div className="flex flex-wrap justify-end gap-2">
            {whyFit.fit === "unchecked" && whyMachine.list !== "fresh" ? (
              <Button className={tapTarget} tone="quiet" disabled={!whyMachine.online} busy={refreshingMachine === whyMachine.id}
                onClick={() => actions.onRefreshMachine(whyMachine.id)}>{words("refresh_list")}</Button>
            ) : whyFit.fit !== "unchecked" && whyFit.launcher && (
              <Button className={tapTarget} tone="quiet" onClick={() => {
                setWhy(null);
                actions.onProvider(whyFit.fit === "fallback" ? whyFit.instead_of || whyFit.launcher : whyFit.launcher);
              }}>{words("provider")}</Button>
            )}
            <Button className={tapTarget} onClick={() => {
              setWhy(null);
              actions.onPurpose(why.purpose);
            }}>{words("change")}</Button>
          </div>
        ) : undefined}>
        {whyMachine && whyFit && <FitExplanation machine={whyMachine} fit={whyFit} />}
      </Dialog>
    </Group>
  );
}

type Disclosure = { plain: boolean; search?: { query: string; open: boolean } };

function ComputerRow({ machine, running, query, actions, refreshing, onWhy }: {
  machine: OverviewMachine;
  running: OverviewRunningGroup[];
  query: string;
  actions: ComputerActions;
  refreshing: boolean;
  onWhy: (target: FitTarget) => void;
}) {
  const [disclosure, setDisclosure] = useState<Disclosure>({ plain: false });
  const detailsId = useId();
  const expanded = query ? (disclosure.search?.query === query ? disclosure.search.open : true) : disclosure.plain;
  const visibleCount = machine.online ? running.reduce((count, group) => count + group.sessions.length, 0) : 0;
  const toggle = () => setDisclosure((value) => query
    ? { ...value, search: { query, open: !expanded } }
    : { ...value, plain: !expanded });

  return (
    <article data-model-overview-computer={machine.id} aria-label={machine.name}>
      <div className={cx(columns, "grid py-3")}>
        <ComputerCell label="computer">
          <Button tone="quiet" className={cx(tapTarget, "-mx-3 max-w-full justify-start")} onClick={() => actions.onMachine(machine.id)}>
            <span className="truncate">{machine.name}</span>
          </Button>
          <div className="flex flex-wrap gap-1">
            <Pill tone={machine.online ? "green" : undefined}>{t(machine.online ? "machines.online" : "machines.offline")}</Pill>
            {machine.hub && <Pill>{words("hub")}</Pill>}
          </div>
          <p className="mt-1 break-words text-label text-ink-3">{freshness(machine)}</p>
          {!machine.online && machine.last_seen && <p className="break-words text-label text-ink-3">{words("last_seen", { time: ago(machine.last_seen) })}</p>}
        </ComputerCell>
        <ComputerCell label="new_agent">
          <FitSummary machine={machine} fit={machine.new_agent} purpose="agent.new" label={words("agents_purpose")}
            onPurpose={actions.onPurpose} onWhy={onWhy} />
        </ComputerCell>
        <ComputerCell label="chats">
          {machine.chats.length > 0 ? (
            <ul className="space-y-1">
              {machine.chats.map((chat) => <li key={chat.purpose}>
                <Button className={cx(tapTarget, "-mx-3 max-w-full")} tone="quiet" onClick={() => actions.onPurpose(chat.purpose)}>
                  <span className="truncate">{chat.name}</span>
                </Button>
              </li>)}
            </ul>
          ) : <p className="text-label text-ink-3">{words("no_chats")}</p>}
        </ComputerCell>
        <ComputerCell label="running">
          {machine.online ? (
            <>
              <p className="text-meta text-ink">{visibleCount > 0 ? t("machines.agents", { count: visibleCount }) : t("machines.no_agents")}</p>
              {running.length > 0 && <ul className="mt-1 space-y-0.5 text-label text-ink-3">
                {running.slice(0, 2).map((group) => (
                  <li key={groupKey(group)} className="truncate">{words("group_summary", {
                    name: runningLabel(machine, group), count: formatNumber(group.sessions.length),
                  })}</li>
                ))}
              </ul>}
            </>
          ) : <p className="text-label text-ink-3">{t("machines.offline")}</p>}
        </ComputerCell>
        <ComputerCell label="providers">
          <p className="text-meta text-ink">{words("provider_count", { count: machine.providers.length })}</p>
          <Button tone="quiet" className={cx(tapTarget, "-mx-3 mt-1")} expanded={expanded} aria-controls={detailsId}
            aria-label={words(expanded ? "hide_computer_details" : "show_computer_details", { machine: machine.name })}
            onClick={toggle}>
            {words(expanded ? "hide_details" : "details")}
            <Icon name="chevron" size={13} className={expanded ? "rotate-180" : undefined} />
          </Button>
        </ComputerCell>
      </div>
      <div id={detailsId}>
        <Collapse open={expanded} lazy>
          <ComputerDetails machine={machine} running={running} actions={actions} refreshing={refreshing} onWhy={onWhy} />
        </Collapse>
      </div>
    </article>
  );
}

function ComputerCell({ label, children }: { label: (typeof columnKeys)[number]; children: ReactNode }) {
  return <div className="min-w-0">
    <p className="mb-0.5 text-label font-medium text-ink-3 lg:sr-only">{words(label)}</p>
    {children}
  </div>;
}

function freshness(machine: OverviewMachine): string {
  const syncedAt = machine.sync?.synced_at;
  if (machine.list === "none") return words("list_unknown");
  if (syncedAt) return words(machine.list === "stale" ? "list_from" : "list_synced", { time: ago(syncedAt) });
  return words(machine.list === "stale" ? "cached_list" : "current_list");
}

function launcherName(machine: OverviewMachine, launcher?: string | null): string {
  const provider = machine.providers.find((candidate) => candidate.name === launcher);
  return provider?.display_name || launcher || words("not_identified");
}

function modelLabel(machine: OverviewMachine, launcher: string, model: string, effort: string): string {
  return [launcherName(machine, launcher), model || (launcher && words("model_default")), effort].filter(Boolean).join(" · ");
}

function runningLabel(machine: OverviewMachine, group: OverviewRunningGroup): string {
  return [launcherName(machine, group.launcher), group.model].filter(Boolean).join(" · ");
}

function fitTone(fit: OverviewFit): "orange" | "red" | undefined {
  if (fit === "fallback") return "orange";
  if (fit === "blocked" || fit === "refused" || fit === "failing") return "red";
}

function FitSummary({ machine, fit, purpose, label, onPurpose, onWhy }: {
  machine: OverviewMachine;
  fit: OverviewAgentFit;
  purpose: string;
  label: string;
  onPurpose: ComputerActions["onPurpose"];
  onWhy: (target: FitTarget) => void;
}) {
  const name = modelLabel(machine, fit.launcher, fit.model, fit.effort);
  const status = fit.fit !== "as_set" ? words(`fit_${fit.fit}`) : "";
  return (
    <div className="flex min-w-0 flex-col items-start">
      <Button tone="quiet" className={cx(tapTarget, "-mx-3 max-w-full")} onClick={() => onPurpose(purpose)}
        aria-label={words("change_purpose", { purpose: label, choice: name })} title={name}>
        <span className="truncate">{name}</span>
      </Button>
      {status && <Button tone="quiet" size="sm" className={cx(tapTarget, "-mx-3 max-w-full")}
        aria-label={words("why_status", { status, purpose: label, machine: machine.name })}
        onClick={() => onWhy({ machine: machine.id, purpose })}>
        <Pill wrap tone={fitTone(fit.fit)}>{status}</Pill>
      </Button>}
    </div>
  );
}

function ComputerDetails({ machine, running, actions, refreshing, onWhy }: {
  machine: OverviewMachine;
  running: OverviewRunningGroup[];
  actions: ComputerActions;
  refreshing: boolean;
  onWhy: (target: FitTarget) => void;
}) {
  return (
    <div className="space-y-4 border-t border-line bg-inset px-4 py-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex min-w-0 flex-wrap gap-x-3 gap-y-1 text-label text-ink-3">
          {machine.claude_version && <span>{words("claude_version", { version: machine.claude_version })}</span>}
          {machine.node?.version && <span>{words("node_version", { version: machine.node.version })}</span>}
          {machine.sync?.rc_file && <span className="break-all">{machine.sync.rc_file}</span>}
        </div>
        <Button className={tapTarget} icon="retry" busy={refreshing} disabled={!machine.online} onClick={() => actions.onRefreshMachine(machine.id)}>
          {words("refresh_list")}
        </Button>
      </div>
      {machine.sync?.error && <p className="break-words text-label text-orange">{stripAnsi(machine.sync.error)}</p>}
      <div className="grid gap-4 lg:grid-cols-2">
        <section className="min-w-0">
          <h3 className="mb-1 text-label font-semibold text-ink-2">{words("chats")}</h3>
          {machine.chats.length > 0 ? <ul className="divide-y divide-line">
            {machine.chats.map((chat) => <ChatDetails key={chat.purpose} chat={chat} machine={machine}
              onPurpose={actions.onPurpose} onWhy={onWhy} />)}
          </ul> : <p className="text-label text-ink-3">{words("no_chats")}</p>}
        </section>
        <RunningDetails machine={machine} running={running} />
      </div>
      <section>
        <h3 className="mb-1 text-label font-semibold text-ink-2">{words("providers_here")}</h3>
        {machine.providers.length > 0 ? <ul className="divide-y divide-line overflow-hidden rounded-card bg-surface shadow-card">
          {machine.providers.map((provider) => <ProviderRow key={provider.name} machine={machine} provider={provider} onProvider={actions.onProvider} />)}
        </ul> : <p className="text-label text-ink-3">{words("no_providers")}</p>}
      </section>
      {machine.tiers.length > 0 && (
        <section>
          <h3 className="mb-1 text-label font-semibold text-ink-2">{words("tiers")}</h3>
          <ul className="divide-y divide-line overflow-hidden rounded-card bg-surface shadow-card">
            {machine.tiers.map((tier) => <li key={tier.name}><Row label={tier.name} stackOnNarrow>
              <Pill tone={tier.available ? "green" : undefined}>{words(tier.available ? "tier_available" : "tier_unavailable")}</Pill>
            </Row></li>)}
          </ul>
        </section>
      )}
    </div>
  );
}

function ChatDetails({ chat, machine, onPurpose, onWhy }: {
  chat: OverviewChat; machine: OverviewMachine; onPurpose: ComputerActions["onPurpose"]; onWhy: (target: FitTarget) => void;
}) {
  const source = chat.source;
  const sourceName = source.from === "agent.new" ? words("agents_purpose")
    : source.from === "assistant" ? words("main_chat") : machine.chats.find((entry) => entry.purpose === source.from)?.name;
  const provenance = source.code === "profile" ? (source.profile ? words("source_profile", { profile: source.profile }) : words("source_profile_generic"))
    : source.code === "inherited" ? (sourceName ? words("source_follows", { purpose: sourceName }) : words("source_inherited"))
    : words(`source_${source.code}`);
  return (
    <li className="py-2">
      <p className="break-words text-label text-ink-2">{chat.name}</p>
      <FitSummary machine={machine} fit={chat} purpose={chat.purpose} label={chat.name} onPurpose={onPurpose} onWhy={onWhy} />
      <div className="flex flex-wrap gap-1">
        <Pill wrap>{provenance}</Pill>
        {chat.runtime && <Pill wrap>{runtimeLabel(chat.runtime, chat.agent)}</Pill>}
      </div>
    </li>
  );
}

function groupKey(group: OverviewRunningGroup): string {
  return JSON.stringify([group.agent, group.launcher, group.model]);
}

const runningFilters = ["all", "persona", "terminal"] as const;
type RunningFilter = (typeof runningFilters)[number];

function RunningDetails({ machine, running }: { machine: OverviewMachine; running: OverviewRunningGroup[] }) {
  const [filter, setFilter] = useState<RunningFilter>("all");
  const filtered = useMemo(() => {
    if (filter === "all") return running;
    return groupOverviewSessions(running.flatMap((group) =>
      group.sessions.filter((session) => Boolean(session.started_by_persona) === (filter === "persona"))));
  }, [filter, running]);
  return (
    <section className="min-w-0">
      <h3 className="mb-1 text-label font-semibold text-ink-2">{words("running")}</h3>
      {!machine.online ? <p className="text-label text-ink-3">{t("machines.offline")}</p>
        : running.length === 0 ? <p className="text-label text-ink-3">{t("machines.no_agents")}</p>
        : <>
          <Segmented compact wrap label={words("running_filter")} value={filter} onChange={setFilter}
            options={runningFilters.map((v) => ({ v, label: words(`running_filter_${v}`) }))} />
          {filtered.length > 0 ? <ul className="divide-y divide-line">
            {filtered.map((group) => <RunningGroupRow key={groupKey(group)} group={group} machine={machine} />)}
          </ul> : <p className="mt-2 text-label text-ink-3">{words("no_running_match")}</p>}
        </>}
    </section>
  );
}

function RunningGroupRow({ group, machine }: { group: OverviewRunningGroup; machine: OverviewMachine }) {
  const [open, setOpen] = useState(false);
  const id = useId();
  const canOpen = group.sessions.some((session) => Boolean(session.session_id));
  const configured = machine.new_agent;
  const differs = Boolean(group.launcher && configured.launcher && (configured.fit === "as_set" || configured.fit === "fallback")
    && (group.launcher !== configured.launcher || configured.model && group.model && group.model !== configured.model));
  const label = runningLabel(machine, group);
  return (
    <li className="py-2">
      {canOpen ? <Button tone="quiet" className={cx(tapTarget, "-mx-3 max-w-full")} expanded={open} aria-controls={id}
        onClick={() => setOpen(!open)}>
        <span className="truncate">{label}</span><Icon name="chevron" size={12} className={open ? "rotate-180" : undefined} />
      </Button> : <p className="break-words text-meta text-ink">{label}</p>}
      <div className="flex flex-wrap gap-1">
        <Pill wrap>{group.working ? t("machines.agents_working", { count: group.sessions.length, working: formatNumber(group.working) })
          : t("machines.agents", { count: group.sessions.length })}</Pill>
        {differs && <Pill wrap>{words("different_default")}</Pill>}
      </div>
      <div id={id}>
        {open && <ul className="mt-2 divide-y divide-line overflow-hidden rounded-card bg-surface shadow-card">
          {group.sessions.map((session) => {
            const sid = session.session_id;
            if (!sid) return null;
            const openSession = () => navigate(session.assistant
              ? { name: "persona", mid: group.machine_id, sid, manager: session.manager }
              : { name: "session", mid: group.machine_id, sid });
            return <li key={sid}><Row label={session.title || words("untitled_session")}
              onClick={openSession} /></li>;
          })}
        </ul>}
      </div>
    </li>
  );
}

function ProviderRow({ machine, provider, onProvider }: { machine: OverviewMachine; provider: OverviewProvider; onProvider: ComputerActions["onProvider"] }) {
  const source = provider.source === "zshrc" ? "shell" : provider.source === "path" ? "path"
    : provider.source === "builtin" ? "builtin" : provider.source === "persona" ? "persona" : "runtime";
  const uses = provider.used_by.map((id) => {
    if (id === "agent.new") return words("agents_purpose");
    if (id === "assistant") return words("main_chat");
    if (id.startsWith("tier:")) return words("tier_use", { tier: id.slice(5) });
    return machine.chats.find((chat) => chat.purpose === id)?.name;
  }).filter(Boolean).join(" · ");
  return <li><Row label={provider.display_name || provider.name} wrapHint onClick={() => onProvider(provider.name)} hint={
    <div className="space-y-1">
      <p className="break-words">{[words(`provider_source_${source}`), provider.model, provider.rc_file].filter(Boolean).join(" · ")}</p>
      {uses && <p className="break-words">{words("provider_uses", { purposes: uses })}</p>}
      <div className="flex flex-wrap gap-1">
        <HealthPill health={provider.health} />
        {provider.health?.checked_at && <span className="text-label text-ink-3">{words("health_checked", { time: ago(provider.health.checked_at) })}</span>}
        {provider.hidden && <Pill>{words("provider_hidden")}</Pill>}
        {!provider.in_scope && <Pill tone="orange">{words("outside_scope")}</Pill>}
        {provider.has_key === false && provider.in_scope && <Pill tone="orange">{words("key_missing")}</Pill>}
      </div>
    </div>
  } /></li>;
}

type Health = OverviewProvider["health"] | OverviewAgentFit["health"];

function HealthPill({ health }: { health: Health }) {
  if (!health) return <Pill>{words("health_unchecked")}</Pill>;
  if (health.state === "ok") return <Pill tone="green">{words("health_ok")}</Pill>;
  if (health.state === "repairing") return <Pill tone="orange">{words("health_repairing")}</Pill>;
  if (health.state !== "blocked" && health.state !== "failing") return <Pill>{words("health_unchecked")}</Pill>;
  return <Pill wrap tone={health.state === "blocked" ? "red" : "orange"}>
    {health.kind === "usage_limit" ? words("health_usage_limit") : blockReason(health.kind ?? "")}
  </Pill>;
}

function FitExplanation({ machine, fit }: { machine: OverviewMachine; fit: OverviewAgentFit }) {
  const health = fit.health;
  const healthError = health && "error" in health && typeof health.error === "string" ? health.error : undefined;
  const note = fit.note || words(`fit_${fit.fit}_detail`);
  return (
    <div className="space-y-3 text-meta">
      <p className="break-words font-medium text-ink">{modelLabel(machine, fit.launcher, fit.model, fit.effort)}</p>
      {fit.instead_of && <p className="break-words text-ink-3">{words("configured_launcher", { launcher: fit.instead_of })}</p>}
      {note && <p className="break-words text-ink-2">{stripAnsi(note)}</p>}
      <p className="text-label text-ink-3">{freshness(machine)}</p>
      {health && <div className="space-y-1 rounded-control bg-inset p-3">
        <HealthPill health={health} />
        {health.checked_at && <p className="text-label text-ink-3">{words("health_checked", { time: ago(health.checked_at) })}</p>}
        {health.resets_at && <p className="text-label text-ink-3">{words("health_resets", { time: ago(health.resets_at) })}</p>}
        {healthError && <p className="whitespace-pre-wrap break-words text-label text-ink-3">{stripAnsi(healthError)}</p>}
      </div>}
    </div>
  );
}
