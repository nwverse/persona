import { agentId } from "./agent-kind";
import { api } from "./api";
import { MANAGER_HEADER } from "./manager-choice";
import type { LauncherSync } from "./launcher-list";
import type { ModelBinding } from "./model-config";
import type { LauncherHealth, Session } from "./types";

export type OverviewFit = "as_set" | "fallback" | "blocked" | "failing" | "unchecked" | "refused";

export interface OverviewSource {
  code: "set" | "inherited" | "profile" | "default" | "auto";
  from?: string;
  profile?: string;
}

export interface OverviewHealth extends Pick<LauncherHealth, "state" | "kind"> {
  checked_at: string;
  resets_at?: string;
}

export interface OverviewAgentFit {
  launcher: string;
  model: string;
  effort: string;
  permission_mode?: string;
  agent?: string;
  fit: OverviewFit;
  instead_of?: string;
  note?: string;
  health?: OverviewHealth | null;
}

export interface OverviewChat extends OverviewAgentFit {
  purpose: string;
  name: string;
  runtime?: string;
  source: OverviewSource;
}

export interface OverviewProvider {
  name: string;
  display_name: string;
  source: string;
  rc_file?: string;
  agent?: string;
  model?: string;
  hidden: boolean;
  in_scope: boolean;
  has_key?: boolean | null;
  health?: OverviewHealth | null;
  used_by: string[];
  in_picker: boolean;
}

export interface OverviewProviderIndex {
  used_by: string[];
  missing_on: string[] | null;
  failing_on: string[] | null;
  hidden: boolean;
}

export interface OverviewTier {
  name: string;
  launcher: string;
  model?: string;
  effort?: string;
  speed?: string;
  permission_mode?: string;
  description?: string;
  on: string[];
}

export interface OverviewTierFit {
  name: string;
  available: boolean;
}

export interface OverviewProfile {
  id: string;
  name: string;
  drift: number;
  changes: string[];
  tiers: OverviewTier[];
}

export interface OverviewMachine {
  id: string;
  name: string;
  online: boolean;
  last_seen?: string;
  hub: boolean;
  claude_version?: string;
  node: { version?: string; model_configuration: boolean };
  sync: LauncherSync;
  list: "fresh" | "stale" | "none";
  roles: string[];
  new_agent: OverviewAgentFit;
  chats: OverviewChat[];
  providers: OverviewProvider[];
  tiers: OverviewTierFit[];
}

export type OverviewPurposeStatusCode =
  | "ok" | "out_of_scope" | "provider_incompatible" | "no_api" | "no_model" | "no_credentials"
  | "paused" | "machine_offline" | "node_outdated" | "hidden_in_use" | "other";

export interface OverviewProblemFix {
  action: "change_purpose" | "test" | "unhide" | "edit_provider" | "open_profile" | "refresh_list" | "open_machine";
  target: string;
}

export interface OverviewProblem {
  code: string;
  severity: "error" | "warning" | "info";
  machine?: string;
  purpose?: string;
  launcher?: string;
  tier?: string;
  text: string;
  fixes: OverviewProblemFix[];
}

export interface ModelOverview {
  revision: string;
  generated_at: string;
  snapshot: { refreshing: boolean; oldest_synced_at: string | null };
  agents: ModelBinding & { launcher: string; source: OverviewSource };
  profile: OverviewProfile | null;
  machines: OverviewMachine[];
  purposes: { id: string; status_code: OverviewPurposeStatusCode; runs_on: string }[];
  providers: Record<string, OverviewProviderIndex>;
  problems: OverviewProblem[];
}

export function modelOverview(manager: string, signal?: AbortSignal): Promise<ModelOverview> {
  return api<ModelOverview>("/model-config/overview", {
    headers: manager ? { [MANAGER_HEADER]: manager } : undefined,
    signal,
  });
}

export type OverviewSession = Pick<Session, "machine_id"> & Partial<Pick<Session,
  "agent" | "launcher" | "model" | "started_by_persona" | "archived" | "is_subagent" |
  "status" | "state" | "starting" | "start_error" | "deleted" | "hidden_by_directory" |
  "session_id" | "title" | "manager"
>>;

export interface OverviewRunningGroup<T extends OverviewSession = Session> {
  machine_id: string;
  agent: string;
  launcher: string | null;
  model: string;
  sessions: T[];
  working: number;
}

/** Group the selected manager's already loaded live sessions by their reported model and launcher. */
export function groupOverviewSessions<T extends OverviewSession>(sessions: readonly T[]): OverviewRunningGroup<T>[] {
  const groups = new Map<string, OverviewRunningGroup<T>>();
  for (const session of sessions) {
    if (session.archived || session.is_subagent || session.deleted || session.starting || session.start_error ||
        session.hidden_by_directory ||
        session.status === "offline" || session.status === "interrupted" || session.state === "offline") continue;

    const { machine_id } = session;
    const agent = agentId(session.agent);
    const launcher = session.launcher || (session.started_by_persona && agent === "claude" ? "claude" : null);
    const model = session.model ?? "";
    const key = JSON.stringify([machine_id, agent, launcher, model]);
    const working = (session.state ?? session.status) === "working";
    const group = groups.get(key);
    if (group) {
      group.sessions.push(session);
      if (working) group.working++;
    } else {
      groups.set(key, { machine_id, agent, launcher, model, sessions: [session], working: Number(working) });
    }
  }
  return [...groups.values()];
}
