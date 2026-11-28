import type { JsonObject, JsonValue, ToolTurnProjection, TranscriptItem } from './Domain';

export type PlanStatus = 'pending' | 'in_progress' | 'completed' | 'blocked' | 'failed' | 'cancelled' | 'skipped' | 'unknown';

export interface PlanStep {
  id: string;
  title: string;
  activeForm: string;
  status: PlanStatus;
}

export interface ConversationPlan {
  sourceId: string;
  at: number;
  done: number;
  total: number;
  current: string;
  steps: PlanStep[];
  complete: boolean;
}

interface PlanEdit { id: string; title?: string; activeForm?: string; status?: string; }
interface DesktopDelta { done: number[]; start?: number; advance: boolean; }
interface PlanChange { delta?: DesktopDelta; family: string; replace: boolean; edits: PlanEdit[]; fallback?: string; }

const planTools = new Set(['mcp__ccd_turn__update_plan', 'todowrite', 'update_plan', 'taskcreate', 'taskupdate', 'tasklist']);

function object(value: JsonValue | undefined): JsonObject | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as JsonObject : undefined;
}
function str(value: JsonValue | undefined): string | undefined { return typeof value === 'string' ? value.trim() : undefined; }
function identifier(value: JsonValue | undefined): string { return typeof value === 'number' ? String(value) : str(value) || ''; }
function status(raw?: string): PlanStatus {
  switch (raw) {
    case 'pending': case 'in_progress': case 'completed': case 'blocked': case 'failed': case 'skipped': case 'cancelled': return raw;
    case 'canceled': return 'cancelled';
    default: return 'unknown';
  }
}
function stamp(value: JsonValue | undefined): number { return typeof value === 'string' ? Date.parse(value) || 0 : 0; }

function replacements(raw: JsonValue | undefined, family: string): PlanChange | undefined {
  if (!Array.isArray(raw)) return undefined;
  const edits: PlanEdit[] = [];
  for (const value of raw) {
    const row = object(value);
    const title = str(row?.step) || str(row?.content) || str(row?.subject);
    if (!row || !title) return undefined; // A partial/malformed snapshot cannot erase the last accepted plan.
    const key = family === 'task' ? identifier(row.id ?? row.taskId) : String(edits.length);
    if (!key) return undefined;
    edits.push({ id: family + ':' + key, title, activeForm: str(row.activeForm), status: str(row.status) });
  }
  return { family, replace: true, edits };
}

function changeFor(use: TranscriptItem, result: TranscriptItem): PlanChange | undefined {
  const outcome = str(object(result.outcome)?.status);
  if (result.is_error === true || (outcome && outcome !== 'ok')) return undefined;
  const name = toolName(use), input = object(use.tool_input ?? use.input);
  const text = (result.text || '').trim();
  let value: JsonValue | undefined;
  if (text.startsWith('{') || text.startsWith('[')) {
    try { value = JSON.parse(text) as JsonValue; } catch (_) {}
  }
  const output = object(value);
  if (output?.success === false || (output?.error !== undefined && output.error !== null)) return undefined;
  if (name === 'todowrite' || name === 'update_plan' || name === 'mcp__ccd_turn__update_plan') {
    const key = name === 'mcp__ccd_turn__update_plan' ? 'items' : name === 'todowrite' ? 'todos' : 'plan';
    if (name === 'mcp__ccd_turn__update_plan' && input && !(key in input)) {
      if (!text.startsWith('Plan updated')) return undefined;
      const advance = input.advance === true || input.advance === 'true';
      const done: number[] = [];
      if (input.done !== undefined) {
        if (!Array.isArray(input.done)) return undefined;
        for (const i of input.done) {
          if (typeof i !== 'number' || !Number.isSafeInteger(i) || i < 1) return undefined;
          done.push(i);
        }
      }
      const start = input.start;
      if (start !== undefined && (typeof start !== 'number' || !Number.isSafeInteger(start) || start < 1)) return undefined;
      if (!advance && !done.length && start === undefined) return undefined;
      return {family:'todo',replace:false,edits:[],delta:{advance,done,start:typeof start === 'number' ? start : undefined}};
    }
    return replacements(output && key in output ? output[key] : input?.[key], 'todo');
  }
  if (name === 'tasklist') {
    if (Array.isArray(value) || output?.tasks !== undefined) return replacements(output?.tasks ?? value, 'task');
    if (/^No tasks found\.?$/.test(text)) return replacements([], 'task');
    const edits: PlanEdit[] = [];
    for (const line of text.split('\n').filter(s => s.trim())) {
      const match = /^#([\w-]+) \[(pending|in_progress|completed)\] (.+)$/.exec(line.trim());
      if (!match) return undefined;
      edits.push({ id: 'task:' + match[1], title: match[3], status: match[2] });
    }
    return edits.length ? { family: 'task', replace: true, edits } : undefined;
  }
  if (name !== 'taskcreate' && name !== 'taskupdate') return undefined;
  if (/^Task #.+ not found\.?$/.test(text)) return undefined;
  const fields = object(output?.task) || output;
  const key = identifier(fields?.id ?? fields?.taskId) ||
    (name === 'taskupdate' ? identifier(input?.taskId) : /Task #([\w-]+) created successfully/.exec(text)?.[1]);
  if (!key) return undefined;
  return { family: 'task', replace: false, fallback: name === 'taskcreate' ? 'pending' : undefined,
    edits: [{ id: 'task:' + key, title: str(fields?.subject ?? input?.subject),
      activeForm: str(fields?.activeForm ?? input?.activeForm), status: str(fields?.status ?? input?.status) }] };
}

// A plan has its own card. Keep failed updates and folds with other work inspectable.
export function onlyPlanUpdates(work: ToolTurnProjection | undefined): boolean {
  if (!work?.inspectionSteps.length) return false;
  return work.inspectionSteps.every(step => {
    if (step.kind === 'tool') {
      if (!planTools.has(toolName(step.use))) return false;
      return step.result ? !!changeFor(step.use, step.result) : work.running;
    }
    if (step.kind !== 'fold') return false;
    const fold = step.item.fold, tools = step.toolCount;
    return tools > 0 && fold?.errors === 0 && fold.items === 2 * tools + step.thoughtCount &&
      !!fold.tool_names?.length && fold.tool_names.every(name => planTools.has(name.replace(/^functions\./i, '').toLowerCase())) &&
      Array.isArray(fold.tasks) && fold.tasks.length === tools;
  });
}

function toolName(item: TranscriptItem): string {
  return (str(item.tool_name) || str(item.tool) || str(item.name) || '').replace(/^functions\./i, '').toLowerCase();
}

function foldedChange(snapshot: JsonObject): PlanChange | undefined {
  if (!Array.isArray(snapshot.entries)) return undefined;
  const family = str(snapshot.replace) || 'task';
  if (family !== 'todo' && family !== 'task') return undefined;
  const edits: PlanEdit[] = [];
  for (const value of snapshot.entries) {
    const entry = object(value), id = str(entry?.id);
    if (!entry || !id || !/^(todo|task):/.test(id)) return undefined;
    edits.push({ id, title: str(entry.subject), status: str(entry.status),
      activeForm: id.startsWith('todo:') ? str(entry.description) : undefined });
  }
  return { family, replace: !!snapshot.replace, fallback: str(snapshot.fallback_status), edits };
}

function summaryLine(text: string): string {
  const first = text.split(/[\r\n]/, 1)[0].trim(), chars = Array.from(first);
  return chars.length > 120 ? chars.slice(0, 119).join('') + '…' : first;
}

export function samePlanSummary(done: number, total: number, current: string, steps: readonly PlanStep[]): boolean {
  if (steps.length !== total || steps.filter(step => step.status === 'completed').length !== done) return false;
  const active = steps.filter(step => step.status === 'in_progress');
  if (!current) return !active.length;
  return active.some(s => summaryLine(s.activeForm || s.title) === current || summaryLine(s.title) === current) ||
    active.map(s => s.activeForm || s.title).join(' · ') === current;
}

interface PlanLocation { message?: { role: string; work?: ToolTurnProjection }; }

function planOwner(lines: readonly PlanLocation[], sourceId: string): number {
  return sourceId ? lines.findIndex(line => line.message?.work?.inspectionSteps.some(step => step.id === sourceId)) : -1;
}

/**
 * The Agent still works through this plan, but the owner has written since it was last updated (a message sent mid-turn,
 * or a new request it has not planned yet). As in the Claude desktop app, the card then leaves its update and stays under
 * the newest content, saying it is the plan from before that message.
 */
export function planPinned(lines: readonly PlanLocation[], plan: { sourceId: string; done: number; total: number }, working: boolean): boolean {
  if (!working || plan.total <= 0 || plan.done >= plan.total) return false;
  const owner = planOwner(lines, plan.sourceId);
  return owner >= 0 && lines.slice(owner + 1).some(line => line.message?.role === 'user');
}

/**
 * Keep the card beside its recorded update. A session-wide summary can precede its history page. A pinned plan goes
 * below everything written so far and above the rows still running, as the working indicator stays last on desktop.
 */
export function planPosition(lines: readonly PlanLocation[], sourceId: string, pinned: boolean = false): number {
  if (pinned) {
    let pos = lines.length;
    while (pos > 0 && (lines[pos - 1].message?.role === 'live_activity' || lines[pos - 1].message?.work?.running === true)) pos--;
    return pos;
  }
  if (sourceId) {
    const owner = planOwner(lines, sourceId);
    if (owner >= 0) {
      const work = lines[owner].message?.work;
      if (work?.agent === 'claude' && work.running && work.latestExchange) {
        for (let i = lines.length - 1; i >= owner; i--) {
          const message = lines[i].message;
          if (message?.work?.turnId === work.turnId || message?.role === 'live_activity') return i;
          if (message?.role === 'assistant') return i + 1;
        }
      }
      // A new receipt within the same passage keeps the plan above its adjacent operation summaries.
      let start = owner;
      while (start > 0 && lines[start - 1].message?.work) start--;
      return start;
    }
  }
  let pos = lines.length;
  for (let i = lines.length - 1; i >= 0; i--) {
    const message = lines[i].message;
    if (message?.role === 'user') break;
    if (message?.work || message?.role === 'assistant') pos = i;
  }
  return pos;
}

/** Consume canonical items in receipt order. A full list replaces its family; task edits keep siblings and order. */
export class ConversationPlanReplay {
  private readonly calls = new Map<string, TranscriptItem>();
  private readonly steps = new Map<string, PlanStep>();
  private readonly replaced = new Set<string>();
  private readonly desktopDeltas = new Set<string>();
  private at = 0;
  private source = '';

  accept(items: readonly TranscriptItem[]): void {
    for (const item of items) {
      if (item.kind === 'fold') {
        const tasks = item.fold?.tasks;
        if (Array.isArray(tasks)) {
          for (const raw of tasks) {
            const snapshot = object(raw), change = snapshot && foldedChange(snapshot);
            if (change) this.apply(change, item.id, stamp(item.fold?.until) || stamp(item.ts));
          }
        }
        continue;
      }
      const id = item.tool_use_id || item.toolUseId;
      if (!id) continue;
      if (item.kind === 'tool_use' && planTools.has(toolName(item))) this.calls.set(id, item);
      if (item.kind !== 'tool_result') continue;
      const use = this.calls.get(id);
      if (!use) continue;
      this.calls.delete(id);
      const change = changeFor(use, item);
      if (change) {
        if (change.delta && this.desktopDeltas.has(id)) continue;
        this.apply(change, use.id, stamp(item.ts) || stamp(use.ts));
        if (change.delta && this.replaced.has('todo')) this.desktopDeltas.add(id);
      }
    }
  }

  private apply(change: PlanChange, source: string, at: number): void {
    if (change.delta) {
      if (!this.replaced.has('todo')) return;
      const rows = Array.from(this.steps.values()).filter(row => row.id.startsWith('todo:'));
      const delta = change.delta;
      if (!rows.length || delta.done.some(i => i > rows.length) || (delta.start !== undefined && delta.start > rows.length)) return;
      const states = rows.map(row => row.status);
      for (const n of delta.done) states[n-1] = 'completed';
      if (delta.start !== undefined) states[delta.start-1] = 'in_progress';
      if (delta.advance) {
        const active = states.indexOf('in_progress');
        if (active >= 0) states[active] = 'completed';
        let next = states.indexOf('pending',active+1);
        if (next < 0) next = states.indexOf('pending');
        if (next >= 0) states[next] = 'in_progress';
      }
      rows.forEach((row,i) => {
        if (states[i] !== row.status) this.steps.set(row.id,{...row,status:states[i]});
      });
    }
    if (change.replace) {
      if (change.family === 'todo') this.desktopDeltas.clear();
      this.replaced.add(change.family);
      const keep = new Set(change.edits.map(edit => edit.id));
      for (const id of this.steps.keys()) {
        if (id.startsWith(change.family + ':') && (change.family === 'todo' || !keep.has(id))) this.steps.delete(id);
      }
    }
    for (const edit of change.edits) {
      if (edit.status === 'deleted') { this.steps.delete(edit.id); continue; }
      const old = this.steps.get(edit.id);
      const title = change.family === 'task' && change.replace && old?.title ? old.title : edit.title ?? old?.title ?? '';
      this.steps.set(edit.id, { id: edit.id, title, activeForm: edit.activeForm ?? old?.activeForm ?? '',
        status: edit.status !== undefined ? status(edit.status) : old?.status ?? status(change.fallback) });
    }
    this.at = at; this.source = source;
  }

  view(report?: JsonObject, fromStart: boolean = true): ConversationPlan | undefined {
    const steps = Array.from(this.steps.values());
    const done = steps.filter(step => step.status === 'completed').length;
    const active = steps.filter(step => step.status === 'in_progress');
    const current = active.map(step => step.activeForm || step.title).filter(Boolean).join(' · ');
    const count = report?.total, reportedDone = report?.done, reportedCurrent = str(report?.current) || '', reportedAt = stamp(report?.at);
    const validReport = typeof count === 'number' && Number.isInteger(count) && typeof reportedDone === 'number' &&
      Number.isInteger(reportedDone) && reportedDone >= 0 && reportedDone <= count;
    const matches = validReport && samePlanSummary(reportedDone, count, reportedCurrent, steps);
    const newerReport = !this.source || (reportedAt > 0 && reportedAt > this.at);
    if (this.source && !steps.length && this.replaced.size && !newerReport) return undefined;
    if (validReport && !matches && (newerReport || (!fromStart && !(this.at && reportedAt && this.at > reportedAt)))) {
      if (!count) return undefined;
      return { done: reportedDone, total: count, current: reportedCurrent, steps: [], at: reportedAt, sourceId: '', complete: false };
    }
    if (!steps.length) return undefined;
    const complete = steps.every(s => !!s.title) && (fromStart || matches ||
      steps.every(s => this.replaced.has(s.id.split(':', 1)[0])));
    return { steps, done, total: steps.length, current, complete, sourceId: this.source, at: this.at };
  }
}
