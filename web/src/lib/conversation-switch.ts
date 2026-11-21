// Explicit logical-conversation switching. Preparing never sends a model input;
// a retry keeps the exact operation/epoch after an uncertain response.
import { t } from "../i18n";
import { agentId } from "./agent-kind";
import { managedProvider } from "./compatibility";
import { api } from "./api";
import type { Launcher, ModelPick, Session } from "./types";

export const ASSISTANT_SEGMENTS_FEATURE = "assistant_segments";
export interface ConversationSwitchRequest {
  operation_id: string;
  expected_epoch: number;
  source_machine_id: string;
  machine_id: string;
  launcher: string;
  model?: string;
  effort?: string;
  permission_mode?: string;
}
const path = (session: Session) => `/assistant/conversations/${encodeURIComponent(session.session_id)}`;
export async function prepareConversationSwitch(session: Session, pick: ModelPick): Promise<ConversationSwitchRequest> {
  const state = await api<{ enabled: boolean; epoch: number }>(`${path(session)}/segments?machine_id=${encodeURIComponent(session.machine_id)}`);
  if (!state.enabled || !Number.isSafeInteger(state.epoch) || state.epoch < 1) throw new Error(t("chat-modelpanel.switch_unavailable"));
  return { ...pick, source_machine_id: session.machine_id, machine_id: session.machine_id,
    operation_id: crypto.randomUUID(), expected_epoch: state.epoch, permission_mode: session.permission_mode };
}
export async function submitConversationSwitch(session: Session, request: ConversationSwitchRequest): Promise<void> {
  await api(`${path(session)}/switch`, { method: "POST", json: request });
}

/** A runtime connection cannot resume a native transcript of the same engine,
 * nor another runtime's session. Different connection identity needs staging. */
export function assistantSwitchNeeded(session: Session, target: Launcher, engine: string): boolean {
  if (session.launcher === target.name) return false;
  if (agentId(session.agent || "claude") !== agentId(engine)) return true;
  if (session.launcher?.startsWith("persona_ext_") || target.name.startsWith("persona_ext_")) return true;
  const runtimeTarget = !managedProvider(target.source) && !["", "zshrc", "path", "builtin"].includes(target.source ?? "");
  if (session.source_kind === "runtime") return !runtimeTarget || session.source_instance_id !== target.source;
  return runtimeTarget;
}
