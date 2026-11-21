import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { formatDate, formatNumber, t } from "../../i18n";
import { api, ApiError, enc, errorText } from "../../lib/api";
import { MANAGER_HEADER, messageManager, sameManager } from "../../lib/manager-choice";
import { groupOverviewSessions, modelOverview, type ModelOverview, type OverviewProblem, type OverviewProblemFix, type OverviewRunningGroup, type OverviewSource } from "../../lib/model-overview";
import { useAgentSessions } from "../../lib/sessions";
import { onHubEvent } from "../../lib/store";
import { Button, Loading, toast } from "../../ui/atoms";
import { SearchField } from "../../ui/SearchField";
import { Group, Pill, Row, SettingsHint } from "./SettingsParts";
import { ModelOverviewComputers } from "./ModelOverviewComputers";

interface OverviewRead {
  manager: string;
  data: ModelOverview | null;
  loading: boolean;
  unsupported: boolean;
  error: string;
}

const NO_OVERVIEW: OverviewRead = { manager: "", data: null, loading: true, unsupported: false, error: "" };
const SYNC_RETRIES = 5;
const SYNC_INTERVAL = 2500;

/** The page is keyed by manager and credential. Keep good data during refresh and retire in-flight reads on reload. */
export function useModelOverview(manager: string) {
  const [view, setView] = useState<OverviewRead>(NO_OVERVIEW);
  const [request, setRequest] = useState(0);
  const reading = useRef<AbortController | null>(null);
  const reload = useCallback(() => {
    reading.current?.abort();
    setRequest((n) => n + 1);
  }, []);

  useEffect(() => {
    if (!manager) return;
    const controller = new AbortController();
    reading.current = controller;
    let poll: ReturnType<typeof setTimeout> | undefined;
    let attempts = 0;
    async function read() {
      try {
        const next = await modelOverview(manager, controller.signal);
        if (controller.signal.aborted) return;
        if (!next?.snapshot || !Array.isArray(next.machines) || !Array.isArray(next.problems) || !next.agents) {
          throw new Error(t("settings-modeloverview.invalid_response"));
        }
        setView((before) => ({
          manager,
          data: before.manager === manager && before.data?.revision === next.revision ? before.data : next,
          loading: false, unsupported: false, error: "",
        }));
        if (next.snapshot.refreshing && attempts++ < SYNC_RETRIES) {
          poll = setTimeout(() => void read(), SYNC_INTERVAL);
        }
      } catch (e) {
        if (controller.signal.aborted) return;
        if (e instanceof ApiError && e.status === 404) {
          setView({ manager, data: null, loading: false, unsupported: true, error: "" });
        } else {
          setView((before) => ({ ...before, loading: false, unsupported: false, error: errorText(e) }));
        }
      }
    }
    setView((before) => ({ ...(before.manager === manager ? before : NO_OVERVIEW), manager, loading: true, error: "" }));
    void read();
    return () => {
      controller.abort();
      clearTimeout(poll);
      if (reading.current === controller) reading.current = null;
    };
  }, [manager, request]);

  const scopedView = view.manager === manager ? view : NO_OVERVIEW;
  useEffect(() => {
    if (!manager || scopedView.unsupported) return;
    let queued: ReturnType<typeof setTimeout> | undefined;
    const onEvent = (event: Record<string, unknown>) => {
      if (messageManager(event) !== manager || queued !== undefined || document.visibilityState === "hidden") return;
      queued = setTimeout(() => { queued = undefined; reload(); }, 150);
    };
    const onVisible = () => { if (document.visibilityState === "visible") reload(); };
    const stopHealth = onHubEvent("launcher_health", onEvent);
    const stopProfile = onHubEvent("model_profile", onEvent);
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      stopHealth();
      stopProfile();
      clearTimeout(queued);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [manager, reload, scopedView.unsupported]);
  return { ...scopedView, reload };
}

export function modelOverviewSourceLabel(source: OverviewSource, purposeName?: (id: string) => string | undefined): string {
  const parent = source.from && purposeName?.(source.from);
  switch (source.code) {
    case "set": return t("settings-modeloverview.source_set");
    case "inherited": return parent
      ? t("settings-modeloverview.source_inherited", { name: parent })
      : t("settings-modeloverview.source_inherited_unknown");
    case "profile": return source.profile
      ? t("settings-modeloverview.source_profile", { name: source.profile })
      : t("settings-modeloverview.source_profile_unknown");
    case "auto": return t("settings-modeloverview.source_auto");
    case "default": return t("settings-modeloverview.source_default");
    default: return "";
  }
}

const FIX_LABEL = {
  change_purpose: "settings-modeloverview.fix_change",
  edit_provider: "settings-modeloverview.fix_provider",
  open_machine: "settings-modeloverview.fix_machine",
  open_profile: "settings-modeloverview.fix_profile",
  refresh_list: "settings-modeloverview.fix_refresh",
  test: "settings-modeloverview.fix_test",
  unhide: "settings-modeloverview.fix_unhide",
} satisfies Record<OverviewProblemFix["action"], string>;

function bindingLine(launcher?: string, model?: string, effort?: string, machine?: string): string {
  return [
    launcher || t("settings-modelconfiguration.not_configured"),
    model,
    effort && t("lib-model-config.effort", { effort }),
    machine,
  ].filter(Boolean).join(" · ");
}

interface OverviewActions {
  onPurpose: (id: string) => void;
  onProvider: (name?: string) => void;
  onMachine: (id: string) => void;
  onProfile: (id: string) => void;
  onInventoryChanged: () => void;
}

export function ModelOverviewSection({ manager, read, onPurpose, onProvider, onMachine, onProfile, onInventoryChanged }: OverviewActions & {
  manager: string;
  read: ReturnType<typeof useModelOverview>;
}) {
  if (read.unsupported || !manager) return null;
  return (
    <section id="settings-model-overview" className="scroll-mt-4" aria-label={t("settings-modeloverview.title")}>
      <h2 className="mb-3 px-1 text-label font-semibold text-ink">{t("settings-modeloverview.title")}</h2>
      {read.error && <Group>
        <Row label={read.error} wrapValue>
          <Button onClick={read.reload} busy={read.loading}>{t("settings.retry")}</Button>
        </Row>
      </Group>}
      {read.data
        ? <OverviewContent key={manager} manager={manager} data={read.data} reload={read.reload} loading={read.loading}
            onPurpose={onPurpose} onProvider={onProvider} onMachine={onMachine} onProfile={onProfile} onInventoryChanged={onInventoryChanged} />
        : read.loading && <Loading />}
    </section>
  );
}

function OverviewContent({ manager, data, reload, loading, onPurpose, onProvider, onMachine, onProfile, onInventoryChanged }: OverviewActions & {
  manager: string;
  data: ModelOverview;
  reload: () => void;
  loading: boolean;
}) {
  const [query, setQuery] = useState("");
  const [allProblems, setAllProblems] = useState(false);
  const [busyAction, setBusyAction] = useState<string | null>(null);
  const action = useRef<AbortController | null>(null);
  useEffect(() => () => action.current?.abort(), []);
  const sessions = useAgentSessions();
  const running = useMemo(() => {
    const owners = new Set(data.machines.map((m) => m.id));
    const byMachine = new Map<string, OverviewRunningGroup[]>();
    for (const group of groupOverviewSessions(sessions.filter((s) => owners.has(s.machine_id) && sameManager(s.manager, manager)))) {
      const current = byMachine.get(group.machine_id);
      if (current) current.push(group);
      else byMachine.set(group.machine_id, [group]);
    }
    return byMachine;
  }, [data.machines, sessions, manager]);
  const mainComputer = data.machines.find((m) => m.chats.some((chat) => chat.purpose === "assistant"));
  const main = mainComputer?.chats.find((chat) => chat.purpose === "assistant");
  const purposeName = (id: string) => id === "agent.new" ? t("settings-modeloverview.agents") : data.machines.flatMap((m) => m.chats).find((c) => c.purpose === id)?.name;
  const problems = allProblems ? data.problems : data.problems.slice(0, 3);

  async function runAction(key: string, run: (signal: AbortSignal) => Promise<unknown>, inventory = false) {
    if (action.current) return;
    const controller = new AbortController();
    action.current = controller;
    setBusyAction(key);
    try {
      await run(controller.signal);
      if (controller.signal.aborted) return;
      if (inventory) onInventoryChanged();
      reload();
    } catch (e) {
      if (!controller.signal.aborted) toast(errorText(e), "error");
    } finally {
      if (action.current === controller) {
        action.current = null;
        if (!controller.signal.aborted) setBusyAction(null);
      }
    }
  }

  function refreshMachine(machine: string) {
    if (!data.machines.some((m) => m.id === machine && m.online)) return;
    return runAction(`refresh:${machine}`,
      (signal) => api(`/launchers?refresh=1&machine=${enc(machine)}`, { signal, headers: { [MANAGER_HEADER]: manager } }), true);
  }

  function fixAllowed(problem: OverviewProblem, fix: OverviewProblemFix) {
    return Object.hasOwn(FIX_LABEL, fix.action) && Boolean(fix.target)
      && (fix.action !== "test" || data.machines.some((m) => m.id === problem.machine && m.online))
      && (fix.action !== "refresh_list" || data.machines.some((m) => m.id === fix.target && m.online));
  }

  function runFix(problem: OverviewProblem, fix: OverviewProblemFix) {
    if (!fixAllowed(problem, fix)) return;
    switch (fix.action) {
      case "change_purpose": return onPurpose(fix.target);
      case "edit_provider": return onProvider(fix.target);
      case "open_machine": return onMachine(fix.target);
      case "open_profile": return onProfile(fix.target);
      case "refresh_list": return refreshMachine(fix.target);
      case "unhide":
        return runAction(fixKey(problem, fix), async (signal) => {
          await api(`/launchers/${enc(fix.target)}/restore`, { method: "POST", signal, headers: { [MANAGER_HEADER]: manager } });
        }, true);
      case "test":
        return runAction(fixKey(problem, fix), async (signal) => {
          const result = await api<{ check: { ok: boolean; error?: string } }>(`/launchers/${enc(fix.target)}/check?machine=${enc(problem.machine!)}`,
            { method: "POST", signal, headers: { [MANAGER_HEADER]: manager } });
          if (!result.check?.ok) throw new Error(result.check?.error || t("settings-modeloverview.test_failed"));
          if (!signal.aborted) toast(t("settings-modeloverview.test_passed"), "ok");
        });
    }
  }

  return (
    <>
      <Group right={<Button icon="retry" tone="quiet" busy={loading} onClick={reload}>{t("settings-modeloverview.refresh")}</Button>}
        footer={data.snapshot.oldest_synced_at
          ? t("settings-modeloverview.inventory_time", { time: formatDate(data.snapshot.oldest_synced_at, { dateStyle: "medium", timeStyle: "short" }) })
          : undefined}>
        <Row label={t("settings-modeloverview.agents")} hint={bindingLine(data.agents.launcher, data.agents.model, data.agents.effort)} wrapHint
          wrapValue onClick={() => onPurpose("agent.new")}>
          <Pill wrap>{modelOverviewSourceLabel(data.agents.source, purposeName)}</Pill>
        </Row>
        {main && mainComputer && <Row label={main.name} hint={bindingLine(main.launcher, main.model, main.effort, mainComputer.name)} wrapHint
          wrapValue onClick={() => onPurpose(main.purpose)}>
          <Pill wrap>{modelOverviewSourceLabel(main.source, purposeName)}</Pill>
        </Row>}
        {data.profile && <Row label={data.profile.name} hint={t("settings-modeloverview.profile")} onClick={() => onProfile(data.profile!.id)}>
          {data.profile.drift > 0 && <Pill tone="orange" wrap>{t("settings-modeloverview.profile_drift", { count: data.profile.drift, number: formatNumber(data.profile.drift) })}</Pill>}
        </Row>}
      </Group>
      {problems.length > 0 && <Group title={t("settings-modeloverview.attention")}>
        {problems.map((problem) => (
          <div key={problemKey(problem)} className="flex flex-wrap items-center gap-2 px-4 py-3">
            <Pill tone={problem.severity === "error" ? "red" : problem.severity === "warning" ? "orange" : undefined}>
              {t(`settings-modeloverview.severity_${problem.severity}`)}
            </Pill>
            <p className="min-w-0 flex-1 break-words text-label text-ink">{problem.text}</p>
            <div className="flex flex-wrap items-center gap-1">
              {problem.fixes.filter((fix) => fixAllowed(problem, fix)).map((fix) => (
                <Button key={fixKey(problem, fix)} tone="quiet" className="min-h-11" disabled={!!busyAction} busy={busyAction === fixKey(problem, fix)}
                  onClick={() => void runFix(problem, fix)}>{t(FIX_LABEL[fix.action])}</Button>
              ))}
            </div>
          </div>
        ))}
        {data.problems.length > 3 && <Row label={allProblems
          ? t("settings-modeloverview.show_less")
          : t("settings-modeloverview.show_all_problems", { number: formatNumber(data.problems.length) })}
          onClick={() => setAllProblems((all) => !all)} />}
      </Group>}
      {data.snapshot.refreshing && <SettingsHint>{t("settings-modeloverview.refreshing")}</SettingsHint>}
      {data.machines.length > 0 && <SearchField value={query} onChange={setQuery} size="md" className="mb-3"
        placeholder={t("settings-modeloverview.search")} />}
      <ModelOverviewComputers machines={data.machines} running={running} query={query} onPurpose={onPurpose} onProvider={onProvider}
        onMachine={onMachine} onRefreshMachine={(id) => void refreshMachine(id)}
        refreshingMachine={busyAction?.startsWith("refresh:") ? busyAction.slice("refresh:".length) : null} />
    </>
  );
}

function problemKey(problem: OverviewProblem): string {
  return JSON.stringify([problem.code, problem.machine, problem.purpose, problem.launcher, problem.tier]);
}

function fixKey(problem: OverviewProblem, fix: OverviewProblemFix): string {
  return fix.action === "refresh_list" ? `refresh:${fix.target}` : JSON.stringify([problemKey(problem), fix.action, fix.target]);
}
