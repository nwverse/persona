import { api, sessionPath } from "./api";
import { normalizeTagFilter } from "./session-list";
import type { Session } from "./types";

export const AGENT_PRESENTATION_FEATURE = "agent_presentation.v1";
export const SESSIONS_TAGS_FEATURE = "sessions_tags";

export interface AgentPresentationInput {
  display_name: string;
  tags: string[];
}

export function matchesAgentTags(session: Session, selected: readonly string[] | undefined): boolean {
  const wanted = normalizeTagFilter(selected);
  if (!wanted.length) return true;
  const tags = new Set(normalizeTagFilter(session.tags));
  return wanted.some((tag) => tags.has(tag));
}

/** Save only Persona's presentation metadata; unlike /rename this never reaches the runtime or its terminal. */
export async function saveAgentPresentation(session: Session, input: AgentPresentationInput): Promise<Session> {
  const out = await api<{ session: Session }>(`${sessionPath(session.machine_id, session.session_id)}/presentation`, { method: "PUT", json: input });
  return out.session;
}
