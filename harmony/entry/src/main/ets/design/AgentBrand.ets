import { Colors } from './Colors';

export function agentBrand(agent: string): Resource | undefined {
  switch (agent) {
    case 'claude': return $r('app.media.agent_brand_claude');
    case 'codex': return $r('app.media.agent_brand_codex');
    case 'gemini': return $r('app.media.agent_brand_gemini');
    case 'opencode': return $r('app.media.agent_brand_opencode');
    default: return undefined;
  }
}

export function agentBrandColor(agent: string): ResourceColor {
  if (agent === 'claude') return $r('app.color.persona_brand_claude');
  if (agent === 'codex') return $r('app.color.persona_brand');
  return agent === 'opencode' ? Colors.ai : Colors.teal;
}
