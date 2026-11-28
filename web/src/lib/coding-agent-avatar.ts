// Contact-style identity and hosting-engine badge resolution for coding Agents (Web).
// Pure data helpers: reusable from Agents list rows, session headers, and future mixed contact feeds.
import { t } from "../i18n";
import { agentId, agentLetter, agentName, isMarked, markId, sessionAgent, type MarkedAgent } from "./agent-kind";
import { paneHost } from "./agent-groups";
import { ENGINE_NAMES, engineName } from "./engine-names";
import type { Machine, Session } from "./types";

export type CodingAgentAvatarSession = Pick<
  Session,
  "agent" | "agent_label" | "launcher" | "model" | "source_kind" | "source_instance_id" | "host" | "space_kind" | "pane_id"
>;

export const KNOWN_HOSTING_ENGINES = [
  "orca", "herdr", "herdr-core", "paseo", "t3code", "cindy", "tern", "rex", "botmux",
] as const;

export type KnownHostingEngine = (typeof KNOWN_HOSTING_ENGINES)[number];
const KNOWN_SET: ReadonlySet<string> = new Set(KNOWN_HOSTING_ENGINES);

export interface ModelVendorMark {
  id: MarkedAgent;
  label: string;
}

export interface CodingAgentAvatarIdentity {
  mark: MarkedAgent | null;
  letter: string;
  label: string;
  model?: string;
}

export interface HostingEngineBadgeInfo {
  id: string;
  kind: KnownHostingEngine | "custom";
  name: string;
  letter: string;
}

const MODEL_VENDORS: readonly [RegExp, MarkedAgent, string][] = [
  [/(?:^|\/)(?:anthropic\/|claude[^/]*$)/, "claude", "Claude"],
  [/(?:^|\/)(?:openai\/|(?:gpt|chatgpt|codex|o[134](?:$|-|\.))[^/]*$)/, "codex", "OpenAI"],
  [/(?:^|\/)gemini/, "gemini", "Gemini"],
  [/(?:^|\/)deepseek/, "dsh", "DeepSeek"],
  [/(?:^|\/)(?:moonshot\/|kimi)/, "kimi", "Kimi"],
  [/(?:^|\/)(?:xai\/|grok)/, "grok", "Grok"],
];

export function resolveModelVendorMark(model: string | undefined | null): ModelVendorMark | undefined {
  const raw = (model ?? "").trim().toLowerCase().replace(/\[[^\]]*\]$/, "").split(":", 1)[0].trim();
  if (!raw) return undefined;
  const hit = MODEL_VENDORS.find(([re]) => re.test(raw));
  return hit ? { id: hit[1], label: hit[2] } : undefined;
}

export function resolveCodingAgentAvatar(s: CodingAgentAvatarSession): CodingAgentAvatarIdentity {
  const { agent, label } = sessionAgent(s);
  const rawAgent = (s.agent ?? "").trim().toLowerCase();
  const id = markId(agent, label);
  const marked = isMarked(id);
  const vendor = resolveModelVendorMark(s.model);
  const explicit = agentName(agent, label?.trim() || (marked ? t(`agent-mark.${id}`) : undefined));
  const model = s.model?.trim() || undefined;

  if (marked && id !== "claude" && id !== "codex") {
    return { mark: id, letter: agentLetter(explicit), label: explicit, model };
  }
  if (vendor) {
    const out = marked && id === vendor.id ? explicit : vendor.label;
    return { mark: vendor.id, letter: agentLetter(out), label: out, model };
  }
  if (marked && (rawAgent !== "paseo" || isMarked(markId(s.launcher, label)))) {
    return { mark: id, letter: agentLetter(explicit), label: explicit, model };
  }
  const src = label?.trim() || (rawAgent && rawAgent !== "paseo" ? agentId(rawAgent) : "") || s.launcher?.trim() || model || explicit;
  return { mark: null, letter: agentLetter(src), label: label?.trim() || explicit, model };
}

export function resolveHostingEngine(
  s: CodingAgentAvatarSession,
  machine?: Pick<Machine, "engines">,
): HostingEngineBadgeInfo | null {
  const host = s.host?.trim().toLowerCase();
  const space = s.space_kind?.trim().toLowerCase();
  const pane = paneHost(s.pane_id).toLowerCase();
  const rt = s.source_kind === "runtime" ? s.source_instance_id?.trim().toLowerCase() : "";
  const id = host
    || (space && KNOWN_SET.has(space) ? space : "")
    || pane
    || (rt && (KNOWN_SET.has(rt) || ENGINE_NAMES[rt]) ? rt : "");
  if (!id) return null;
  const kind: KnownHostingEngine | "custom" = KNOWN_SET.has(id) ? (id as KnownHostingEngine) : "custom";
  const name = engineName(id, machine);
  return { id, kind, name, letter: agentLetter(name) };
}

export function codingAgentAvatarLabel(
  avatar: Pick<CodingAgentAvatarIdentity, "label">,
  engine: Pick<HostingEngineBadgeInfo, "name"> | null,
): string {
  return engine ? `${avatar.label} · ${engine.name}` : avatar.label;
}
