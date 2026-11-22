// "What this computer can do" (docs/server/machines.md "Doctor: what this computer can do", Moshi's Doctor tab): what `personad node doctor` finds on a computer,
// read as features. The node reports individual checks (server/internal/node/doctor.go, DoctorReport as it is); a person
// wants to know whether phone approvals work, so each feature below is a set of those checks and takes the worst
// state among them. The raw list stays one click away. Also what the machine card says about a connection that failed.
import { t } from "../i18n";
import { machineDeployment } from "./machine-deploy";
import { currentEngine } from "./engines";
import type { Machine } from "./types";

export type CheckStatus = "ok" | "warn" | "fail";

/** One line of the node's report; `state` is current / stale / missing for a generated file, "" for the others. */
export interface DoctorCheck {
  name: string;
  status: CheckStatus;
  state?: string;
  detail: string;
  hint?: string;
  items?: string[];
}

export interface DoctorReport {
  ok: boolean;
  dir?: string;
  checks: DoctorCheck[];
  /** The generated files `fix` wrote again. */
  fixed?: string[];
}

export const CHECK_STATUS_LABEL: Record<CheckStatus, string> = { ok: t("node-health.check_ok"), warn: t("node-health.check_warn"), fail: t("node-health.check_fail") };

export type FeatureState = "ready" | "partial" | "broken" | "off";

export const FEATURE_STATE_LABEL: Record<FeatureState, string> = {
  ready: t("node-health.state_ready"),
  partial: t("node-health.state_partial"),
  broken: t("node-health.state_broken"),
  off: t("node-health.state_off"),
};

interface FeatureSpec {
  id: string;
  name: string;
  /** What it lets the user do, one line. */
  detail: string;
  /** The checks it stands on (DoctorCheck.name). */
  checks: string[];
  /** Something a computer may well not use (an engine other than herdr, no runtimes): a warning alone means "Off". */
  optional?: boolean;
  /** For an optional one: whether this computer needs it, which turns the warning into "Problem". */
  needed?: (ctx: HealthContext) => boolean;
}

/** What the hub knows about the computer that the node's report does not say. */
export interface HealthContext {
  /** The machine's engine (engines.ts `currentEngine`). */
  engine?: string;
}

/** The checks the node reports and no feature stands on: the user's own settings, shown as information. */
export const INFO_CHECKS = ["global-permission-hooks"];

export const FEATURES: FeatureSpec[] = [
  {
    id: "approvals",
    name: t("node-health.feature_approvals_name"),
    detail: t("node-health.feature_approvals_detail"),
    checks: ["node.sock", "claude-settings.json", "claude-hooks", "assistant-settings.json", "assistant-hooks", "command-paths"],
  },
  { id: "status", name: t("node-health.feature_status_name"), detail: t("node-health.feature_status_detail"), checks: ["node.sock", "command-paths"] },
  {
    id: "terminal",
    name: t("node-health.feature_terminal_name"),
    detail: t("node-health.feature_terminal_detail"),
    checks: ["herdr"],
    optional: true,
    needed: (c) => ["herdr", "herdr-both"].includes(c.engine ?? "herdr"),
  },
  { id: "mcp", name: t("node-health.feature_mcp_name"), detail: t("node-health.feature_mcp_detail"), checks: ["node.sock", "mcp.json", "command-paths"] },
  { id: "skills", name: t("node-health.feature_skills_name"), detail: t("node-health.feature_skills_detail"), checks: ["agent-plugin", "claude-version"] },
  { id: "runtimes", name: t("node-health.feature_runtimes_name"), detail: t("node-health.feature_runtimes_detail"), checks: ["runtime.sock"], optional: true },
];

export interface FeatureView {
  id: string;
  name: string;
  detail: string;
  state: FeatureState;
  /** The checks it stands on that the report has. */
  checks: DoctorCheck[];
  /** Why it is not ready, from its worst check: what to do, else what was found. "" when ready. */
  note: string;
}

const RANK: Record<CheckStatus, number> = { ok: 0, warn: 1, fail: 2 };

function worst(checks: DoctorCheck[]): DoctorCheck | undefined {
  let w: DoctorCheck | undefined;
  for (const c of checks) if (c.status !== "ok" && (!w || RANK[c.status] > RANK[w.status])) w = c;
  return w;
}

/** What to tell about a check that is not fine. A stale or missing generated file has a fix here ("Fix"), so the node's
 * hint, which names the command line, is not repeated. */
function noteOf(c: DoctorCheck | undefined): string {
  if (!c) return "";
  if (c.state === "stale") return t("node-health.note_stale");
  if (c.state === "missing") return t("node-health.note_missing");
  return c.hint || c.detail;
}

/** The report as features, in a fixed order. A feature none of whose checks the report has (an older node reports
 * fewer) is left out; a check no feature covers and that is not fine makes "Other checks" so nothing hides. */
export function deriveFeatures(report: DoctorReport, ctx: HealthContext = {}): FeatureView[] {
  const byName = new Map(report.checks.map((c) => [c.name, c]));
  const out: FeatureView[] = [];
  for (const spec of FEATURES) {
    const checks = spec.checks.flatMap((n) => byName.get(n) ?? []);
    if (checks.length === 0) continue;
    const w = worst(checks);
    let state: FeatureState = "ready";
    if (w?.status === "fail") state = "broken";
    else if (w?.status === "warn") state = spec.optional ? (spec.needed?.(ctx) ? "broken" : "off") : "partial";
    out.push({ id: spec.id, name: spec.name, detail: spec.detail, state, checks, note: noteOf(w) });
  }
  const covered = new Set([...FEATURES.flatMap((f) => f.checks), ...INFO_CHECKS]);
  const rest = report.checks.filter((c) => !covered.has(c.name));
  const w = worst(rest);
  if (w) out.push({ id: "other", name: t("node-health.feature_other_name"), detail: t("node-health.feature_other_detail"), state: w.status === "fail" ? "broken" : "partial", checks: rest, note: noteOf(w) });
  return out;
}

export interface HealthSummary {
  /** Features that are broken or partial. */
  attention: number;
  title: string;
  /** "Not set up: …" ("" when every feature is in use). */
  off: string;
}

/** Moshi's "Everything works" / "N feature(s) need attention" / "Not set up for:". */
export function summarize(features: FeatureView[]): HealthSummary {
  const attention = features.filter((f) => f.state === "broken" || f.state === "partial").length;
  const off = features.filter((f) => f.state === "off").map((f) => f.name);
  return {
    attention,
    title: attention === 0 ? t("node-health.summary_ok") : t("node-health.summary_attention", { count: attention }),
    off: off.length ? t("node-health.summary_off", { features: off.join(t("node-health.list_separator")) }) : "",
  };
}

/** The checks "Fix" would put right: Persona's own generated files that are stale or missing. */
export function fixableChecks(report: DoctorReport): DoctorCheck[] {
  return report.checks.filter((c) => c.state === "stale" || c.state === "missing");
}

/** The user's own settings the node only looks at (global-permission-hooks): a row of information, not a feature. */
export function infoChecks(report: DoctorReport): DoctorCheck[] {
  return report.checks.filter((c) => INFO_CHECKS.includes(c.name) && c.status !== "ok");
}

/** What the toast says after "Fix": the node re-checks after writing, so the report is already the new one. */
export function fixResult(report: DoctorReport): { text: string; tone: "ok" | "error" } {
  const failed = report.checks.find((c) => c.name === "fix" && c.status === "fail");
  if (failed) return { text: failed.detail, tone: "error" };
  const n = report.fixed?.length ?? 0;
  return n > 0 ? { text: t("node-health.fixed", { count: n }), tone: "ok" } : { text: t("node-health.fixed_none"), tone: "ok" };
}

/** Why a self-check could not be had, from the hub's answer. An old node answers "unknown method"; an old hub has no
 * such route (a plain 404, not the JSON of "unknown machine"). */
export type DoctorUnavailable = "old-node" | "old-hub" | "offline" | "forbidden" | null;

export function doctorUnavailable(e: { status?: number; message?: string; isJson?: boolean }): DoctorUnavailable {
  if (/unknown method/i.test(e.message ?? "")) return "old-node";
  if (e.status === 404 && e.isJson === false) return "old-hub";
  if (e.status === 503) return "offline";
  if (e.status === 403) return "forbidden";
  return null;
}

export type ConnectionFailureKind = "auth" | "unreachable" | "timeout" | "version";

export interface ConnectionFailure {
  kind: ConnectionFailureKind;
  title: string;
  /** One line: what to do next. */
  next: string;
}

const AUTH = /permission denied|publickey|authentication|unauthori[sz]ed|invalid[ _]token|forbidden|host key verification|access denied|\b40[13]\b|配对|认证/i;
const VERSION = /version mismatch|protocol version|unknown method|incompatible|不是同一版|版本/i;
const TIMEOUT = /timed out|timeout|deadline exceeded|no pong/i;
const UNREACHABLE = /refused|no route|unreachable|could not resolve|name or service|no such host|network is|connection (reset|closed|lost)|broken pipe|\beof\b|node exited|waiting for|lookup |dial /i;

/** What a machine that is not online says about why, read from the error text the hub keeps, with a next step; null
 * when the text does not say (nothing is guessed). A planned restart (a deployment) is not a failure: the caller
 * leaves those out. Matching order matters: "connection timed out" is a timeout, not "connection …". */
export function classifyConnection(m: Machine): ConnectionFailure | null {
  if (m.online) return null;
  const text = m.error ?? "";
  if (!text) return null;
  const link = m.kind === "link" || m.kind === "service";
  if (AUTH.test(text)) {
    return {
      kind: "auth",
      title: t("node-health.auth_title"),
      next: link ? t("node-health.auth_next_link") : t("node-health.auth_next_ssh"),
    };
  }
  if (VERSION.test(text) || m.node_mismatch) {
    const d = machineDeployment(m);
    return {
      kind: "version",
      title: t("node-health.version_title"),
      next: d.supported ? t("node-health.version_next_deploy") : t("node-health.version_next_local"),
    };
  }
  if (TIMEOUT.test(text)) {
    return { kind: "timeout", title: t("node-health.timeout_title"), next: t("node-health.timeout_next") };
  }
  if (UNREACHABLE.test(text)) {
    return {
      kind: "unreachable",
      title: t("node-health.unreachable_title"),
      next: link ? t("node-health.unreachable_next_link") : t("node-health.unreachable_next_ssh"),
    };
  }
  return null;
}

/** `currentEngine` for the derivation, so the card needs one import. */
export function healthContext(m: Pick<Machine, "engine">): HealthContext {
  return { engine: currentEngine(m) };
}
