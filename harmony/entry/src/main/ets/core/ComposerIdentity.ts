import type { JsonObject } from './Domain';

export class ComposerIdentity {
  agent: string = '';
  model: string = '';
  brand: string = '';
  effort: string = '';
  speed: string = '';
}

function word(value: unknown): string {
  return typeof value === 'string' ? value.trim() : '';
}

export function sessionComposerIdentity(session: JsonObject | undefined): ComposerIdentity {
  const result = new ComposerIdentity();
  if (!session) return result;
  const agent = word(session.agent) || 'claude';
  const names: Record<string, string> = {codex:'Codex', claude:'Claude Code', gemini:'Gemini', opencode:'OpenCode', traex:'TRAE'};
  result.agent = word(session.agent_label) || names[agent] || agent;
  result.model = word(session.model) || word(session.requested_model);
  result.brand = agent;
  result.effort = word(session.effort);
  const tier = word(session.service_tier).toLowerCase();
  if (!tier || tier === 'default' || tier === 'standard') return result;
  if (['priority', 'fast', 'ultrafast'].includes(tier)) result.speed = 'fast';
  else {
    const advertised = Array.isArray(session.service_tiers) ? session.service_tiers.find(row =>
      !!row && typeof row === 'object' && !Array.isArray(row) && (row as JsonObject).id === tier) : undefined;
    const label = word((advertised as JsonObject | undefined)?.name);
    result.speed = /^[\x20-\x7E]+$/.test(label) ? label : tier;
  }
  return result;
}
