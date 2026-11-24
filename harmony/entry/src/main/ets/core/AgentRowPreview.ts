// The Agents list row's second line, state mark and subagent count, in the iOS order
// (apple/Shared/Models.swift `listPreview`, `workLine`, `closeoutPreview`; apple/Shared/ChildSessions.swift `shownRunning`).

type Payload = Record<string, Object>;
export type PreviewWords = (key: string) => string;

function object(source: Payload | undefined, key: string): Payload | undefined {
  const value = source?.[key];
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Payload : undefined;
}
function text(source: Payload | undefined, key: string): string {
  const value = source?.[key];
  return typeof value === 'string' ? value.trim() : '';
}
function flag(source: Payload | undefined, key: string): boolean { return source?.[key] === true; }
function count(source: Payload | undefined, key: string): number {
  const value = source?.[key];
  return typeof value === 'number' && Number.isFinite(value) ? Math.max(0, Math.floor(value)) : 0;
}

/** One line of plain words from Markdown: no emphasis marks, link targets, heading or list markers. */
export function plainPreview(value: string): string {
  return value.split('\n').map((line: string) => line.trim().replace(/^(?:#{1,6}\s+|>\s?|[-*+]\s+(?:\[[ xX]\]\s+)?)/, ''))
    .filter((line: string) => !!line && !/^(```|~~~)/.test(line)).join(' ')
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1').replace(/(\*\*|__|`)/g, '').replace(/\s+/g, ' ').trim();
}

/** A failure notice without the warning sign some hosts start it with; the row has its own marks. */
export function failurePreview(reason: string): string {
  return reason.trim().replace(/^[⚠️\s]+/, '').trim();
}

function waitMinutes(seconds: number, words: PreviewWords): string {
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return words('agent_preview_minutes').replace('{minutes}', String(minutes));
  const hours = Math.floor(minutes / 60), rest = minutes % 60;
  return rest === 0 ? words('agent_preview_hours').replace('{hours}', String(hours)) :
    words('agent_preview_hours_minutes').replace('{hours}', String(hours)).replace('{minutes}', String(rest));
}

function wakeTime(work: Payload): number {
  const value = work['wake_at'];
  if (typeof value === 'number' && Number.isFinite(value)) return value > 1e12 ? value : value * 1000;
  return typeof value === 'string' ? Date.parse(value) : Number.NaN;
}

/** A stopped turn the hub triaged: "✓ conclusion" when done, else what it waits for and when the hub acts next. */
export function workPreview(data: Payload, state: string, now: number, words: PreviewWords): string {
  const work = object(data, 'work');
  const line = text(work, 'line');
  if (!work || !line || text(work, 'phase') === 'working' || state === 'working') return '';
  if (text(work, 'phase') === 'done') return '✓ ' + line;
  const wake = wakeTime(work);
  if (!Number.isFinite(wake)) return line;
  const retry = text(work, 'on') === 'transient', left = (wake - now) / 1000;
  const when = left < 60 ? words(retry ? 'agent_preview_retrying_now' : 'agent_preview_waking_now') :
    words(retry ? 'agent_preview_retrying_in' : 'agent_preview_waking_in').replace('{time}', waitMinutes(left, words));
  return words('agent_preview_line_when').replace('{line}', () => line).replace('{when}', () => when);
}

const CLOSEOUT_STATUSES: string[] = ['done', 'not_done', 'blocked'];

function yesNo(value: string): boolean | undefined { return value === 'yes' ? true : value === 'no' ? false : undefined; }

function claimWords(closeout: Payload, subject: string, words: PreviewWords): string {
  const said = yesNo(text(closeout, subject)), found = yesNo(text(object(closeout, 'checked'), subject));
  if (said === undefined && found === undefined) return '';
  const merged = subject === 'merged';
  if (said === true && found === false) return words(merged ? 'agent_preview_claim_merged_false' : 'agent_preview_claim_deployed_false');
  if (found === true) return words(merged ? 'agent_preview_merged_checked' : 'agent_preview_deployed_checked');
  if (found === undefined && said === true) return words(merged ? 'agent_preview_merged' : 'agent_preview_deployed');
  return words(merged ? 'agent_preview_not_merged' : 'agent_preview_not_deployed');
}

function closeoutLine(closeout: Payload, words: PreviewWords): string {
  switch (text(closeout, 'status')) {
    case 'blocked': {
      const waiting = text(closeout, 'waiting');
      return waiting ? words('agent_preview_blocked_on').replace('{waiting}', () => waiting) : words('agent_preview_blocked');
    }
    case 'not_done': {
      const missing = text(closeout, 'missing');
      return missing ? words('agent_preview_not_done_missing').replace('{missing}', () => missing) : words('agent_preview_not_done');
    }
    case 'done': return words('agent_preview_reported_done');
    default: return '';
  }
}

/** A stopped turn the agent closed out: the hub's line (else the closeout's own), then where merged and deployed stand. */
export function closeoutPreview(data: Payload, state: string, now: number, words: PreviewWords): string {
  const work = object(data, 'work'), closeout = object(work, 'closeout');
  if (!work || !closeout || !CLOSEOUT_STATUSES.includes(text(closeout, 'status')) || state === 'working' ||
    text(work, 'phase') === 'working') return '';
  const line = workPreview(data, state, now, words) || closeoutLine(closeout, words);
  const suffix = [claimWords(closeout, 'merged', words), claimWords(closeout, 'deployed', words)].filter((part: string) => !!part).join(' · ');
  return [line, suffix].filter((part: string) => !!part).join(' · ');
}

/** Why the row matters now, first match wins; the last words when nothing more specific is known. */
export function agentRowPreview(data: Payload, state: string, muted: boolean, now: number, words: PreviewWords): string {
  if (flag(data, 'awaiting_first_message') && state !== 'needs_you') return words('agent_preview_first_message');
  const attention = text(data, 'attention_reason');
  if (state === 'needs_you' && attention) return attention;
  const failure = state !== 'working' ? failurePreview(text(data, 'failure_reason')) : '';
  if (failure) return failure;
  if (state === 'idle' && flag(data, 'awaits_reply') && !muted) {
    const work = object(data, 'work');
    const line = text(work, 'line');
    if (text(work, 'phase') === 'blocked' && text(work, 'on') === 'user' && line) return line;
    const asks = plainPreview(text(data, 'asks'));
    if (asks) return asks;
  }
  return closeoutPreview(data, state, now, words) || workPreview(data, state, now, words) || plainPreview(text(data, 'last_text'));
}

/** Waiting in background: a working turn that only waits for its background tasks, with no child working or asking. */
export function waitsInBackground(data: Payload, state: string): boolean {
  const children = object(data, 'children');
  return state === 'working' && flag(data, 'background_waiting') && count(children, 'working') === 0 && count(children, 'needs_you') === 0;
}

/** The mark on the avatar's corner: Needs you even when muted, otherwise nothing for a muted row. */
export function agentRowTone(data: Payload, state: string, muted: boolean): string {
  if (state === 'needs_you') return 'needs_you';
  if (muted) return '';
  if (waitsInBackground(data, state)) return 'background';
  return state === 'working' || state === 'unread' ? state : '';
}

/** Subagents running under a parent row, never counting one the hub reports twice. */
export function runningSubagents(data: Payload): number {
  const children = object(data, 'children');
  const hubWorking = Math.max(0, count(children, 'working') - count(children, 'sides_working'));
  const summary = data['waiting'] !== undefined || data['background_tasks_count'] !== undefined;
  const embedded = data['subagents'];
  if (!summary && Array.isArray(embedded)) {
    const session = text(data, 'session_id'), running = new Set<string>();
    (embedded as Object[]).forEach((entry: Object) => {
      const agent = entry !== null && typeof entry === 'object' && !Array.isArray(entry) ? entry as Payload : undefined;
      if (!agent || agent['running'] !== true) return;
      running.add(text(agent, 'session_id') || session + '@' + text(agent, 'id'));
    });
    return Math.max(running.size, hubWorking);
  }
  return Math.max(count(data, 'running_subagents'), hubWorking);
}
