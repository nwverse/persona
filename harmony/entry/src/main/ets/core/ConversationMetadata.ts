import type { JsonObject, ToolTurnProjection } from './Domain';

export interface SessionContextUsage {
  tokens: number;
  capacity: number;
  percent: number;
}

export function waitsOnClaudeDesktop(session: JsonObject | undefined): boolean {
  if (session?.source_kind !== 'claude_desktop' || session.deleted === true || session.state === 'offline') return false;
  const caps = session.capabilities as JsonObject | undefined;
  if (caps && typeof caps === 'object' && !Array.isArray(caps) && (caps.approve === true || caps.terminal === true)) return false;
  return session.status === 'blocked' || (!session.status && session.state === 'needs_you');
}

export interface DesktopSessionProposal { title: string; branch: string }

export function pendingDesktopProposal(work: ToolTurnProjection | undefined): DesktopSessionProposal | undefined {
  if (!work?.latestExchange) return undefined;
  for (let i = work.inspectionSteps.length - 1; i >= 0; i--) {
    const step = work.inspectionSteps[i];
    if (step.kind !== 'tool') continue;
    if (step.use.tool_name !== 'mcp__ccd_session__start_session' || step.result) return undefined;
    const input = step.use.tool_input as JsonObject | undefined;
    if (!input || typeof input !== 'object' || Array.isArray(input)) return undefined;
    const branch = input.use_worktree === true && typeof input.branch_name === 'string' ? input.branch_name : '';
    const source = typeof input.source_branch === 'string' ? input.source_branch : '';
    return { title: typeof input.title === 'string' ? input.title : '',
      branch: source && branch ? source + ' → ' + branch : branch };
  }
  return undefined;
}

/** One session snapshot reports the last model input and its effective window together. */
export function sessionContextUsage(session: JsonObject | undefined): SessionContextUsage | undefined {
  const tokens = session?.context_tokens, capacity = session?.context_window;
  if (session?.usage_unknown === true || typeof tokens !== 'number' || typeof capacity !== 'number' ||
    !Number.isSafeInteger(tokens) || !Number.isSafeInteger(capacity) || tokens <= 0 || capacity <= 0) return undefined;
  return { tokens, capacity, percent: Math.min(100, Math.round(tokens / capacity * 100)) };
}

export function messageSentTime(at: number, language?: string): string {
  if (!Number.isFinite(at) || at <= 0) return '';
  const date = new Date(at);
  if (!Number.isFinite(date.getTime())) return '';
  return date.toLocaleString(language, {
    year:'numeric', month:'short', day:'numeric', hour:'2-digit', minute:'2-digit'
  });
}
