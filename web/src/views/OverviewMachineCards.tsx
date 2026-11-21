import { Component, memo, Suspense, useMemo, useState, type ErrorInfo, type ReactNode } from "react";
import { t } from "../i18n";
import { isPending } from "../lib/add-computer";
import { reportBoundary } from "../lib/error-reports";
import { storedValue, storeValue } from "../lib/compatibility";
import { lazyPage } from "../lib/lazy-page";
import { useMachineMetrics } from "../lib/machine-metrics";
import { hubLabel } from "../lib/mesh";
import { href, onLinkClick } from "../lib/router";
import type { Machine, MachineMetrics } from "../lib/types";
import { cx } from "../ui/atoms";
import { Icon } from "../ui/icons";
import { Segmented } from "./assistant/parts";
import { isChunkError } from "./PageBoundary";

const MachineMetricsSummary = lazyPage(() => import("./machines/MachineMetricsSummary").then((m) => m.MachineMetricsSummary));

/** Only a failed summary remounts: each successful metrics read offers the chunk another try, even identical data. */
class OverviewSummaryBoundary extends Component<{ generation: number; children: ReactNode }, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    reportBoundary(error, "overlay_boundary", isChunkError(error), info.componentStack);
  }

  componentDidUpdate(previous: { generation: number }) {
    if (this.state.failed && previous.generation !== this.props.generation) this.setState({ failed: false });
  }

  render() {
    return this.state.failed ? null : this.props.children;
  }
}

export type OverviewMachineView = "cards" | "rows";
export const OVERVIEW_MACHINE_VIEW_KEY = "persona.overview.machines.view";

/** The browser's Computers display choice, like the Machines and Files view preferences. Cards are the default. */
export function useOverviewMachineView(): [OverviewMachineView, (view: OverviewMachineView) => void] {
  const [view, setView] = useState<OverviewMachineView>(() => {
    try {
      return storedValue(OVERVIEW_MACHINE_VIEW_KEY) === "rows" ? "rows" : "cards";
    } catch {
      return "cards";
    }
  });
  const choose = (next: OverviewMachineView) => {
    try {
      storeValue(OVERVIEW_MACHINE_VIEW_KEY, next);
    } catch {
      // A page-only choice still works when browser storage is disabled or full.
    }
    setView(next);
  };
  return [view, choose];
}

export function OverviewMachineViewSwitch({ value, onChange }: { value: OverviewMachineView; onChange: (view: OverviewMachineView) => void }) {
  return (
    <Segmented
      compact
      value={value}
      onChange={onChange}
      label={t("machines.view_label")}
      options={[{ v: "cards", label: t("machines.view_cards") }, { v: "rows", label: t("home.view_rows") }]}
    />
  );
}

/** The Overview's original computer rows, unchanged. This mode mounts no metrics reader. */
export function OverviewMachineRows({ machines, agentCounts, offlineText }: {
  machines: Machine[];
  agentCounts?: ReadonlyMap<string, number>;
  offlineText: (machine: Machine) => string;
}) {
  return (
    <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 @min-[60rem]:grid-cols-3 @min-[84rem]:grid-cols-4 @min-[110rem]:grid-cols-5">
      {machines.map((m) => {
        const count = agentCounts && (agentCounts.get(m.id) ?? 0);
        const line = m.online ? [count !== undefined && t("home.machine_agents", { count }), m.system?.name].filter(Boolean).join(" · ") || t("home.online") : offlineText(m);
        const hub = hubLabel(m, machines);
        const detail = { name: "machine" as const, mid: m.id };
        return (
          <a key={m.id} href={href(detail)} onClick={(e) => onLinkClick(e, detail)} className="flex items-center gap-3 rounded-card bg-surface px-3 py-2.5 shadow-card transition-colors hover:bg-hover">
            <span className={cx("flex size-8 shrink-0 items-center justify-center rounded-[8px]", m.online ? "bg-field text-ink-2" : "bg-inset text-ink-3")}>
              <Icon name="machine" size={15} />
            </span>
            <span className="min-w-0 flex-1">
              <span className="flex min-w-0 items-center gap-1.5">
                <span className="truncate text-[13px] font-medium text-ink">{m.name}</span>
                {hub && <span className="shrink-0 truncate text-[11.5px] text-ink-3">{hub}</span>}
              </span>
              <span className="block truncate text-[11.5px] text-ink-3" title={!m.online && m.error && offlineText(m) !== m.error ? m.error : undefined} data-machine-line>{line}</span>
            </span>
            <span className={cx("size-2 shrink-0 rounded-full", m.online ? "bg-green" : "bg-line-strong")} title={m.online ? t("home.online") : t("home.offline")} />
          </a>
        );
      })}
    </div>
  );
}

/** Overview's Computers in Cards mode: one shared metrics read and the exact Machines summary, charts included. */
export function OverviewMachineCards({ machines, agentCounts, offlineText }: {
  machines: Machine[];
  agentCounts?: ReadonlyMap<string, number>;
  offlineText: (machine: Machine) => string;
}) {
  // Section-level state keeps metric refreshes out of Home's Agent and task sections. No card starts work of its own.
  const { metrics, error, generation } = useMachineMetrics();
  const byMachine = useMemo(() => new Map((metrics ?? []).map((m) => [m.machine_id, m])), [metrics]);
  const hasMonitorable = machines.some((m) => hasCurrentSample(m, byMachine.get(m.id)));
  return (
    <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 @min-[60rem]:grid-cols-3 @min-[84rem]:grid-cols-4 @min-[110rem]:grid-cols-5">
      {error && (
        <p role="alert" className="col-span-full flex items-start gap-2 rounded-[8px] bg-red-tint px-3 py-2 text-[12.5px] text-red">
          <Icon name="warning" size={14} className="mt-px shrink-0" />
          <span className="min-w-0 break-words">{hasMonitorable ? t("machines.refresh_failed_stale", { error, seconds: 10 }) : t("machines.load_failed", { error })}</span>
        </p>
      )}
      {machines.map((m) => (
        <OverviewMachineCard
          key={m.id}
          m={m}
          count={agentCounts ? (agentCounts.get(m.id) ?? 0) : undefined}
          hub={hubLabel(m, machines)}
          metrics={byMachine.get(m.id)}
          generation={generation}
          offlineText={offlineText}
        />
      ))}
    </div>
  );
}

const OverviewMachineCard = memo(function OverviewMachineCard({ m, count, hub, metrics, generation, offlineText }: {
  m: Machine;
  count?: number;
  hub: string;
  metrics?: MachineMetrics;
  generation: number;
  offlineText: (machine: Machine) => string;
}) {
  const detail = { name: "machine" as const, mid: m.id };
  const line = m.online ? [count !== undefined && t("home.machine_agents", { count }), m.system?.name].filter(Boolean).join(" · ") || t("home.online") : offlineText(m);
  const monitored = hasCurrentSample(m, metrics);
  return (
    <a href={href(detail)} onClick={(e) => onLinkClick(e, detail)} className={cx("block rounded-card bg-surface shadow-card transition-colors hover:bg-hover", monitored ? "h-full" : "self-start")}>
      <span className={cx("flex items-center gap-3 px-3", monitored ? "pt-2.5" : "py-2.5")}>
        <span className={cx("flex size-8 shrink-0 items-center justify-center rounded-[8px]", m.online ? "bg-field text-ink-2" : "bg-inset text-ink-3")}>
          <Icon name="machine" size={15} />
        </span>
        <span className="min-w-0 flex-1">
          <span className="flex min-w-0 items-center gap-1.5">
            <span className="truncate text-[13px] font-medium text-ink">{m.name}</span>
            {hub && <span className="shrink-0 truncate text-[11.5px] text-ink-3">{hub}</span>}
          </span>
          <span className="block truncate text-[11.5px] text-ink-3" title={!m.online && m.error && line !== m.error ? m.error : undefined} data-machine-line>{line}</span>
        </span>
        <span className={cx("size-2 shrink-0 rounded-full", m.online ? "bg-green" : "bg-line-strong")} title={m.online ? t("home.online") : t("home.offline")} />
      </span>
      {monitored && (
        <OverviewSummaryBoundary generation={generation}>
          <Suspense fallback={null}><MachineMetricsSummary machine={m} metrics={metrics} /></Suspense>
        </OverviewSummaryBoundary>
      )}
    </a>
  );
});

function hasCurrentSample(machine: Machine, metrics?: MachineMetrics): boolean {
  if (!machine.online || isPending(machine) || metrics?.online === false || !metrics) return false;
  if (metrics.usage) return true;
  const latest = metrics.latest;
  if (!latest) return false;
  return [latest.cpu, latest.load, latest.mem_used, latest.mem_total, latest.net_rx, latest.net_tx, latest.battery_percent, latest.temp_c]
    .some((value) => typeof value === "number" && Number.isFinite(value));
}
