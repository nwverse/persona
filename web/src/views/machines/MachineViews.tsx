import { Suspense } from "react";
import { t } from "../../i18n";
import { lazyPage } from "../../lib/lazy-page";
import { href, onLinkClick } from "../../lib/router";
import type { MachineSubview } from "../../lib/machine-route";
import type { Machine } from "../../lib/types";
import { Loading } from "../../ui/atoms";

const PortsProcessesPanel = lazyPage(() => import("./PortsProcessesPanel").then((m) => m.PortsProcessesPanel));
const ToolchainPanel = lazyPage(() => import("./ToolchainPanel").then((m) => m.ToolchainPanel));
const ServicesPanel = lazyPage(() => import("./ServicesPanel").then((m) => m.ServicesPanel));
const FileBrowserPanel = lazyPage(() => import("./FileBrowserPanel").then((m) => m.FileBrowserPanel));
const NetworkPanel = lazyPage(() => import("./NetworkPanel").then((m) => m.NetworkPanel));
const StoragePanel = lazyPage(() => import("./StoragePanel").then((m) => m.StoragePanel));

const labels = {
  work: "machines.view_work", monitoring: "machines-machinedetail.monitoring", files: "machines.view_files",
  storage: "machines.view_storage", diagnostics: "machines.view_diagnostics", management: "machines.view_management",
  procs: "machines.view_procs", services: "machines.view_services", network: "machines-network.tab_label", toolchain: "machines.view_toolchain",
} as const;
const primary = ["work", "monitoring", "files", "storage", "diagnostics", "management"] as const;
const tools = { procs: PortsProcessesPanel, services: ServicesPanel, toolchain: ToolchainPanel, network: NetworkPanel };
type Diagnostic = keyof typeof tools;
function isDiagnostic(view: MachineSubview): view is Diagnostic {
  return Object.hasOwn(tools, view);
}

export function MachineViewLinks({ mid, view }: { mid: string; view: MachineSubview }) {
  const active = isDiagnostic(view) ? "diagnostics" : view === "config" ? "management" : view;
  function link(v: keyof typeof labels, current: string, className: string, label: string = labels[v]) {
    const to = { name: "machine" as const, mid, subview: v === "work" ? undefined : v };
    return <a key={v} href={href(to)} onClick={e => onLinkClick(e, to)} aria-current={current === v ? "page" : undefined}
      className={className}>{t(label)}</a>;
  }
  const tab = "flex min-h-11 items-center whitespace-nowrap border-b-2 px-3 text-label hover:text-ink";
  const pill = "flex min-h-8 items-center whitespace-nowrap rounded-full px-3 text-label hover:bg-hover [@media(pointer:coarse)]:min-h-11";
  return <div className="grid gap-2">
    <nav aria-label={t("machines.view_label")} className="flex flex-wrap border-b border-line">
      {primary.map(v => link(v, active, tab + (v === active ? " border-ink font-semibold text-ink" : " border-transparent text-ink-2")))}
    </nav>
    {active === "diagnostics" && <nav aria-label={t("machines.view_diagnostics")} className="flex flex-wrap gap-1">
      {(["diagnostics", ...Object.keys(tools) as Diagnostic[]] as const).map(v =>
        link(v, view, pill + (v === view ? " bg-field font-medium text-ink" : " text-ink-2"), v === "diagnostics" ? "node-health.title" : labels[v]))}
    </nav>}
  </div>;
}

export function MachineToolView({ machine, view, mode, path }: { machine: Machine; view: MachineSubview; path?: string; mode?: "reclaim" }) {
  const Panel = isDiagnostic(view) ? tools[view] : null;
  return <Suspense fallback={<Loading />}>
    {view === "files" && <FileBrowserPanel key={path} machine={machine} path={path} routed />}
    {Panel && <Panel machine={machine} />}
    {view === "storage" && <StoragePanel machine={machine} focus={mode && { mid: machine.id, mode, seq: 1 }} />}
  </Suspense>;
}
