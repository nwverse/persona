// Loaded only when an assistant's mention panel opens, not by the panel's idle preload.
import { api } from "./api";
import { MANAGER_HEADER } from "./manager-choice";
import type { SkillCatalog } from "./skill-mentions";

export async function fetchSkillCatalog(machine: string, source: { manager: string; session?: string; launcher?: string }, signal: AbortSignal): Promise<SkillCatalog> {
  const query = new URLSearchParams({ machine_id: machine });
  if (source.session) query.set("session_id", source.session);
  if (source.launcher) query.set("launcher", source.launcher);
  const catalog = await api<SkillCatalog>(`/assistant/skills?${query}`, { signal, headers: { [MANAGER_HEADER]: source.manager } });
  if (catalog.catalog_version !== 1) throw new Error("Unsupported skill catalog");
  return catalog;
}
