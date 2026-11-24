import type { JsonObject, JsonValue, TranscriptItem } from './Domain';

export type EventWords = (key: string) => string;
export type EventTone = 'normal' | 'attention' | 'danger';
export interface AgentEventDisplay { label: string; title: string; body: string; meta: string; tone: EventTone; compaction?: boolean; detailOnly?: boolean; }

function obj(value: JsonValue | undefined): JsonObject | undefined {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as JsonObject : undefined;
}
function str(value: JsonValue | undefined): string { return typeof value === 'string' ? value : ''; }

export function recordedDuration(value: JsonValue | undefined): string {
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) return '';
  const s = Math.floor(value / 1000), m = Math.floor(s / 60);
  if (m >= 60) return Math.floor(m / 60) + 'h ' + (m % 60) + 'm';
  if (m) return m + 'm ' + (s % 60) + 's';
  return value > 0 && value < 1000 ? Math.round(value) + 'ms' : s + 's';
}
export function eventClock(value: JsonValue | undefined): string {
  if (typeof value !== 'string') return '';
  const date = new Date(value);
  return Number.isFinite(date.getTime()) ? String(date.getHours()).padStart(2, '0') + ':' + String(date.getMinutes()).padStart(2, '0') : '';
}

const contextBlock = /^\s*<(persona-context|jarvis-context)\b([^>]*)>([\s\S]*?)<\/\1>\s*/;
function inputSource(item: TranscriptItem): JsonObject | undefined {
  const explicit = obj(item.source), injected = obj(item.injected);
  if (str(explicit?.kind)) return explicit?.kind === 'user' ? undefined : explicit;
  if (str(item.from)) return { kind: 'conversation', title: item.from };
  if (item.client_msg_id) return undefined;
  let text = item.text || '';
  for (let count = 0; count < 32; count++) {
    const block = contextBlock.exec(text);
    if (!block) break;
    if (/\bkind="message-source"/.test(block[2])) {
      try {
        const source = obj(JSON.parse(block[3]) as JsonValue);
        if (source?.author === 'user' || !str(source?.kind).trim()) return undefined;
        return source;
      } catch (_) { return undefined; }
    }
    text = text.slice(block[0].length);
  }
  return str(injected?.kind) ? injected : undefined;
}
export function isAgentInput(item: TranscriptItem): boolean {
  return item.kind === 'text' && item.role === 'user' && inputSource(item) !== undefined;
}

export function taskStateKey(status: string): string {
  const keys: Record<string, string> = {
    running: 'working', completed: 'done', failed: 'work_tool_failed', killed: 'ev_stopped',
    stopped: 'ev_stopped', waiting: 'ev_awaiting', event: 'ev_update', ended: 'ev_ended',
    started: 'ev_started', interacted: 'ev_contacted', interrupted: 'work_interrupted'
  };
  return Object.prototype.hasOwnProperty.call(keys, status) ? keys[status] : '';
}
const sourceLabels: Record<string, string> = {
  conversation: 'ev_peer', patrol: 'ev_patrol', followup: 'ev_followup', supervision: 'ev_supervision',
  scheduled_task: 'ev_scheduled', meeting: 'ev_meeting', task_notice: 'ev_task', system: 'ev_system'
};
const taskLabels: Record<string, string> = {
  agent: 'ev_agent', shell: 'ev_background_command', monitor: 'ev_monitor', workflow: 'ev_workflow'
};
function keyFor(keys: Record<string, string>, name: string, fallback: string): string {
  return Object.prototype.hasOwnProperty.call(keys, name) ? keys[name] : fallback;
}

export function agentEventDisplay(item: TranscriptItem, t: EventWords): AgentEventDisplay {
  const view: AgentEventDisplay = { label: t('work_notice'), title: '', body: item.text || '', meta: '', tone: 'normal' };
  const task = obj(item.task), refusal = obj(item.model_refusal), compact = obj(item.compact);
  if (item.kind === 'browser_complete') return { ...view, label:t('ev_browser'), title:item.text || '', body:'' };
  if (item.kind === 'provider_error') return sessionIssueDisplay({ turn_error:item.text || '' }, t) || view;
  if (item.kind === 'text') {
    const source = inputSource(item), injected = obj(item.injected);
    view.label = t(keyFor(sourceLabels, str(source?.kind), 'ev_external'));
    view.title = str(source?.title) || str(injected?.title);
    if (item.from === '另一个会话' && view.title === item.from) view.title = ''; // Canonical untitled peer from the Paseo bridge.
    if (injected?.line) view.body = [str(injected.line), str(injected.detail)].filter(Boolean).join('\n');
    else for (let count = 0; count < 32; count++) {
      const block = contextBlock.exec(view.body);
      if (!block) break;
      view.body = view.body.slice(block[0].length);
    }
    if (item.queued === true) view.label += ' · ' + t('ev_queued');
  } else if (item.kind === 'recap') {
    view.label = t('ev_recap');
  } else if (item.kind === 'turn_duration') {
    view.label = t('ev_elapsed');
    view.meta = recordedDuration(obj(item.turn_duration)?.duration_ms);
  } else if (item.kind === 'visualization') {
    const asset = obj(item.visualization);
    view.label = t('ev_visualization');
    view.title = str(asset?.title) || str(asset?.path).split('/').pop() || '';
    view.body = str(asset?.path);
  } else if (refusal) {
    view.label = t(refusal.fallback_model ? 'ev_model_fallback' : 'ev_model_refusal'); view.tone = 'attention';
    view.title = [str(refusal.original_model), str(refusal.fallback_model)].filter(Boolean).join(' → ');
    view.body = [view.body || str(refusal.text), str(refusal.explanation)].filter((s, i, all) => !!s && all.indexOf(s) === i).join('\n');
  } else if (compact) {
    const keys: Record<string, string> = { auto: 'ev_compaction_auto', manual: 'ev_compaction_manual', idle: 'ev_compaction_idle' };
    const count = compact.pre_tokens;
    view.compaction = true;
    view.label = t(keyFor(keys, str(compact.trigger), 'ev_compaction'));
    view.body = '';
    if (typeof count === 'number' && Number.isSafeInteger(count) && count > 0) {
      view.meta = t('ev_tokens_before').replace('{n}', count.toLocaleString());
    }
  } else if (task) {
    const status = str(task.status), key = taskStateKey(status);
    view.label = t(keyFor(taskLabels, str(task.kind), 'ev_task')) + (key ? ' · ' + t(key) : '');
    view.title = str(task.title);
    view.body = str(task.detail) || (view.title ? '' : view.body);
    view.meta = typeof task.exit_code === 'number' ? t('ev_exit').replace('{n}', String(task.exit_code)) : '';
    view.tone = status === 'failed' ? 'danger' : status === 'waiting' ? 'attention' : 'normal';
  } else if (item.wake === true) view.label = t('ev_wake');
  return view;
}

export function sessionIssueDisplay(data: JsonObject, t: EventWords): AgentEventDisplay | undefined {
  const body = str(data.turn_error);
  if (!body) return undefined;
  const keys: Record<string, string> = { transient: 'ev_service_error', usage_limit: 'ev_limit', user: 'ev_action_required',
    unrecoverable: 'ev_session_closed', no_output: 'ev_no_output', stopped: 'ev_stopped' };
  const recordedKind = str(data.turn_error_kind);
  const quota = /^API Error:\s*(?:Request rejected\s*\(429\)|429\b)/i.test(body) && /(?:usage limit|\d+[- ]hour limit|quota)/i.test(body);
  const kind = quota && (!recordedKind || recordedKind === 'transient' || recordedKind === 'unknown') ? 'usage_limit' : recordedKind;
  const label = t(keyFor(keys, kind, 'ev_turn_error'));
  const reset = str(data.turn_error_resets_at);
  const date = reset ? new Date(reset) : undefined;
  const resetText = quota ? /\bresets?\s+(?:at\s+)?([^·\r\n]+)$/i.exec(body)?.[1].trim() || '' : '';
  const when = date && Number.isFinite(date.getTime()) ? date.toLocaleString() : resetText;
  const meta = when ? t('ev_reset').replace('{time}', () => when) : '';
  const tone: EventTone = kind === 'stopped' ? 'normal' : ['user', 'usage_limit', 'no_output'].includes(kind) ? 'attention' : 'danger';
  return { label, title:data.turn_error_resumes === true ? t('ev_will_resume') : '', body, meta, tone,
    detailOnly: /^API Error:/i.test(body) };

}
