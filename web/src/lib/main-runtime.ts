import { t } from "../i18n";

export interface MainExecution {
  session_id?: string;
  /** Routing node, never the execution location. */
  machine_id?: string;
  proxy?: string;
  state: "unknown" | "starting" | "running" | "stopped" | "offline";
  source?: "native" | "runtime";
  harness?: string;
  harness_label?: string;
  location?: { host?: string; container?: string };
}

function harnessOf(report: MainExecution): string | undefined {
  switch (report.harness) {
    case "claude": return "Claude Code";
    case "agentsdk": return "Claude Agent SDK";
    case "codex": return "Codex";
    default: return report.harness_label || report.harness;
  }
}

/** The main chat's line in its header, or "" when nothing about where it runs is known (an older hub, a failed read, a
 *  report for another conversation): the line is left out then, rather than telling an everyday user what was not
 *  reported. A missing location is left out the same way; a proxy is named as the way in, never as the location. */
export function mainRuntimeSummary(main: string | undefined, report?: MainExecution, starting = false): string {
  const label = t("main-runtime.main");
  if (!main) return t(starting ? "main-runtime.starting" : "main-runtime.not_started");
  if (report?.session_id !== main) return "";
  const harness = harnessOf(report);
  const location = report.source && (report.location?.container
    ? t("main-runtime.container", { name: report.location.container })
    : report.location?.host);
  const offline = report.state === "offline";
  const stopped = report.state === "stopped";
  const parts = (...values: (string | undefined)[]) => values.filter(Boolean).join(" · ");
  if (report.state === "starting") return t("main-runtime.starting");
  if (offline || stopped) {
    return parts(t(offline ? "main-runtime.offline" : "main-runtime.stopped"),
      location ? t("main-runtime.last_location", { location }) : undefined, harness,
      !location && report.source === "runtime" && report.proxy ? t("main-runtime.proxy", { name: report.proxy }) : undefined);
  }
  if (report.state === "running" || report.source || harness) {
    return parts(label, report.state === "running" ? t("main-runtime.running") : undefined,
      location || (report.source === "runtime" && report.proxy ? t("main-runtime.proxy", { name: report.proxy }) : undefined), harness);
  }
  return "";
}
