import type { JsonObject, JsonValue, ToolInspectionStep, ToolTurnProjection, WorkInspectionStep, TranscriptItem } from './Domain';

function record(value: JsonValue | undefined): JsonObject | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as JsonObject : undefined;
}

function firstText(source: JsonObject | undefined, keys: readonly string[]): string {
  return firstVerbatim(source, keys).trim();
}
function firstVerbatim(source: JsonObject | undefined, keys: readonly string[]): string {
  for (const key of keys) {
    const value = source?.[key];
    if (typeof value === 'string' && value.trim()) return value;
  }
  return '';
}

export function workToolName(use: TranscriptItem): string {
  return firstText(use, ['tool_name', 'tool', 'name']);
}

export function workToolSubject(use: TranscriptItem): string {
  const input = use.tool_input ?? use.input;
  const subject = typeof input === 'string' ? input : firstText(record(input),
    ['command', 'cmd', 'file_path', 'pattern', 'query', 'path', 'url', 'description']);
  return subject.split('\n', 1)[0].trim();
}

// Format the full arguments only when the reader opens this call.
export function workToolArguments(use: TranscriptItem): string {
  const input = use.tool_input ?? use.input;
  if (input === undefined || input === null) return use.text || '';
  return typeof input === 'string' ? input : JSON.stringify(input, null, 2);
}

export function workToolStatus(step: ToolInspectionStep, live: boolean): string {
  if (!step.result) return live || step.use.live_tool === true ? 'working' : 'work_no_result';
  switch (firstText(record(step.result.outcome), ['status'])) {
    case 'ok': return 'done';
    case 'failed': return 'failed';
    case 'timed_out': return 'work_timed_out';
    case 'denied': return 'work_denied';
    case 'blocked': return 'work_blocked';
    case 'interrupted': return 'work_interrupted';
    case 'no_match': return 'work_no_match';
    case 'background': return 'work_background';
    default: return step.failed ? 'failed' : 'done';
  }
}

export function workStatusFailed(status: string): boolean {
  return status === 'failed' || status === 'work_timed_out';
}

export type WorkKind = 'shell' | 'read' | 'edit' | 'search' | 'fetch' | 'agent' | 'wait' | 'plan' | 'question' | 'mcp' | 'skill' | 'monitor' | 'task' | 'tool';
export type WorkWords = (key: string) => string;
type DetailStyle = 'text' | 'code' | 'diff';
export interface WorkDetail { key: string; style: DetailStyle; text: string; language?: string; }
export interface WorkRun { first: string; count: number; kind: WorkKind; }

// Keep short diagnostics on the originating tool row. Full receipts remain in its detail sheet.
export function workRowExcerpt(step: ToolInspectionStep, status: string): string {
  const failed = workStatusFailed(status);
  if (!failed && status !== 'working' && !['work_denied', 'work_blocked', 'work_interrupted'].includes(status)) return '';
  const text = firstVerbatim(outcome(step), ['tail']) || step.result?.text || '';
  const exitCode = outcome(step)?.exit_code;
  const lines = text.slice(-2400).trimEnd().split(/\r?\n/).map(line => line.trim()).filter(line => {
    if (!line) return false;
    const exit = /^\(?exit code (-?\d+)\)?$/i.exec(line);
    return !exit || Number(exit[1]) !== exitCode;
  });
  const diagnostic = failed ? lines.slice().reverse().find(line => /(?:\b\w*(?:Error|Exception):|^(?:fatal|error|FAIL)\b)/i.test(line)) : undefined;
  const last = diagnostic || lines[lines.length - 1] || '';
  return last.length > 120 ? last.slice(0, 119) + '…' : last;
}

const toolKinds: Record<string, WorkKind> = {
  bash: 'shell', shell: 'shell', exec_command: 'shell', execute_command: 'shell', command_execution: 'shell',
  read: 'read', read_file: 'read', readfile: 'read', view_image: 'read', imageview: 'read',
  write: 'edit', edit: 'edit', multiedit: 'edit', apply_patch: 'edit', write_file: 'edit', filechange: 'edit',
  glob: 'search', grep: 'search', ls: 'search', websearch: 'search', web_search: 'search', search_files: 'search',
  webfetch: 'fetch', web_fetch: 'fetch', fetch: 'fetch',
  subagent: 'agent', task: 'agent', agent: 'agent', spawn_agent: 'agent', send_input: 'agent', send_message: 'agent',
  wait_agent: 'wait', wait: 'wait', sleep: 'wait', 'clock.sleep': 'wait',
  todowrite: 'plan', update_plan: 'plan', askuserquestion: 'question', ask_user: 'question', request_user_input: 'question',
  skill: 'skill', monitor: 'monitor', taskstop: 'task', taskoutput: 'task', taskget: 'task', taskupdate: 'task', taskcreate: 'task'
};
function mcpOperation(use: TranscriptItem): string {
  const name = workToolName(use).replace(/^(functions|multi_agent)\./, '');
  const parts = /^mcp__(.+?)__(.+)$/.exec(name) || /^mcp[./]([^./]+)[./](.+)$/.exec(name);
  return parts ? parts[1] + ' · ' + parts[2] : '';
}
export function workKind(use: TranscriptItem): WorkKind {
  const name = workToolName(use).toLowerCase().replace(/^(functions|multi_agent)\./, '');
  if (name === 'mcp__ccd_turn__update_plan') return 'plan';
  if (mcpOperation(use)) return 'mcp';
  return Object.prototype.hasOwnProperty.call(toolKinds, name) ? toolKinds[name] : 'tool';
}
export function workAction(use: TranscriptItem, t: WorkWords): string {
  const kind = workKind(use);
  if (kind === 'mcp') return /(?:^|\.)mcp__codex_app__update_running_summary$/i.test(workToolName(use))
    ? t('work_update_summary') : mcpOperation(use);
  if (kind === 'task') return t('ev_action_task') + ' · ' + workToolName(use);
  if (kind === 'wait') return t(workToolName(use).toLowerCase().endsWith('sleep') ? 'ev_sleep' : 'ev_wait_agent');
  if (kind === 'agent') {
    const phase = firstText(toolInput(use), ['event']);
    const verbs: Record<string, string> = { started: 'ev_child_started', interacted: 'ev_child_contacted', completed: 'ev_child_ended',
      failed: 'ev_child_failed', stopped: 'ev_child_stopped', waiting: 'ev_child_waiting' };
    if (Object.prototype.hasOwnProperty.call(verbs, phase)) return t(verbs[phase]);
  }
  return kind === 'tool' ? workToolName(use) || t('tool') : t('ev_action_' + kind);
}
export type ActivityKind = WorkKind | 'image';
export type ToolMotion = 'command' | 'search' | 'read' | 'web' | 'scan' | 'computer' | 'knowledge' | 'memory' | 'generate';
export interface ActivityHeading {
  title: string; kind: ActivityKind; motion?: ToolMotion; status: string; failures: number; returned: number; total: number; active: number; startedAt: number;
}
export function activityKind(step: ToolInspectionStep): ActivityKind {
  const kind = workKind(step.use);
  return kind === 'read' && (step.images.length > 0 || /(?:^|\.)(view_image|imageview)$/i.test(workToolName(step.use))) ? 'image' : kind;
}

/** Resolve the active artwork from the operation. The UI uses a progress indicator for calls without dedicated artwork. */
export function toolMotion(step: ToolInspectionStep): ToolMotion | undefined {
  const kind = activityKind(step);
  const name = workToolName(step.use).toLowerCase().split(/__|\./).pop() || '';
  if (kind === 'shell' || /^(functions\.)?exec$/i.test(workToolName(step.use))) return 'command';
  if (kind === 'image') return 'scan';
  if (kind === 'read' || name === 'read_skill') return 'read';
  if (name === 'web_search' || name === 'websearch' || name === 'search_web') return 'web';
  if (kind === 'search') return 'search';
  switch (name) {
    case 'browser_use': case 'computer_use': return 'computer';
    case 'read_internal_knowledge': return 'knowledge';
    case 'read_memory': return 'memory';
    case 'imagegen': case 'image_generation': return 'generate';
    default: return undefined;
  }
}
export function recordedAction(step: ToolInspectionStep, t: WorkWords): string {
  if (step.use.live_tool_preview === true) return workToolName(step.use);
  if (workToolStatus(step, false) !== 'done') return workAction(step.use, t);
  const kind = activityKind(step);
  if (kind === 'image' || kind === 'read') return t('activity_detail_' + kind);
  if (kind === 'search') {
    const input = toolInput(step.use), path = firstText(input, ['path', 'cwd', 'directory']);
    if (path && firstText(input, ['pattern', 'query'])) return t('activity_search_in').replace('{path}', path);
  }
  return workAction(step.use, t);
}
function activityCount(kind: ActivityKind, count: number, t: WorkWords): string {
  const group = ['shell', 'read', 'edit', 'search', 'image', 'fetch'].includes(kind) ? kind : 'other';
  return t('activity_' + group + (count === 1 ? '_one' : '_many')).replace('{n}', String(count));
}

function isExecutorTitle(name: string): boolean {
  return /^(?:functions\.)?exec(?:_command)?$/i.test(name.trim());
}

function runningToolTitle(step: ToolInspectionStep, t: WorkWords, agent?: string): string {
  const description = firstText(toolInput(step.use), ['description', 'title']).split(/\r?\n/, 1)[0].slice(0, 160);
  const name = workToolName(step.use).replace(/^(functions|multi_agent)\./i, '');
  const subject = description || workTarget(step.use);
  if (isExecutorTitle(name)) {
    if (subject && !isExecutorTitle(subject)) return subject;
    return t(name === 'exec_command' ? 'activity_running_shell' : 'activity_running_script');
  }
  if (agent === 'claude' && description) return description;
  if (workKind(step.use) === 'shell' && !subject) return t('activity_running_shell');
  const label = workKind(step.use) === 'mcp' ? workAction(step.use, t) : name || workAction(step.use, t);
  return subject && label !== subject ? label + ' · ' + subject : label;
}

/** Describe recorded actions between two public paragraphs; successful verbs require a matching receipt. */
export function activityHeading(work: ToolTurnProjection, t: WorkWords): ActivityHeading {
  const result: ActivityHeading = { title: '', kind: 'tool', status: '', failures: 0, returned: 0, total: 0, active: 0, startedAt: 0 };
  const counts = new Map<ActivityKind, number>();
  let latest: ToolInspectionStep | undefined, only: ToolInspectionStep | undefined;
  let notice = '', folded = 0, foldedFailures = 0, recovered = 0, exception = '';
  let priority = -1;
  for (const step of work.inspectionSteps) {
    if (step.kind === 'fold') {
      folded += step.toolCount;
      // A mixed historical count cannot establish which failures survived a retry.
      if (step.toolCount > 0 && step.item.fold?.errors === step.toolCount) foldedFailures += step.toolCount;
      continue;
    }
    if (step.kind === 'narration') {
      if (!notice && step.role !== 'thinking') notice = step.item.text || t('work_notice');
      continue;
    }
    const kind = activityKind(step), status = workToolStatus(step, work.running);
    if (kind === 'plan' && status === 'done') continue;
    if (!only) result.kind = kind;
    only = step; result.total++;
    if (step.result) result.returned++;
    if (status === 'working') {
      result.active++;
      // Prefer a confirmed in-flight operation over an unpaired history item or its outer executor.
      const rank = (step.use.live_tool === true ? 2 : 0) + (/^(?:functions\.)?exec$/i.test(workToolName(step.use)) ? 0 : 1);
      if (rank >= priority) { latest = step; priority = rank; }
      const start = typeof step.use.ts === 'string' ? Date.parse(step.use.ts) : 0;
      if (start > 0 && (!result.startedAt || start < result.startedAt)) result.startedAt = start;
    } else if (step.recovered) {
      recovered++;
    } else if (workStatusFailed(status)) {
      result.failures++;
    } else if (status !== 'done') exception = status;
    const category = ['shell','read','edit','search','image','fetch'].includes(kind) ? kind : 'tool';
    counts.set(category, (counts.get(category) ?? 0) + (kind === 'image' ? Math.max(1, step.images.length) : 1));
  }
  if (!result.total && foldedFailures === folded) result.failures = foldedFailures;
  if (latest) {
    result.kind = activityKind(latest); result.motion = toolMotion(latest); result.status = 'working';
    result.title = runningToolTitle(latest, t, work.agent);
  } else if (result.total || folded) {
    result.status = result.failures ? 'failed' : exception || (result.total > recovered && !folded ? 'done' : '');
    const first = work.inspectionSteps[0];
    const names = first?.kind === 'fold' ? first.item.fold?.tool_names : undefined;
    if (work.agent === 'claude' && work.inspectionSteps.length === 1 && Array.isArray(names) &&
      names.length === 1 && names[0] === 'Bash' && !result.failures) {
      result.kind = 'shell'; result.title = activityCount('shell', folded, t);
    } else if (folded || recovered || result.failures || exception || counts.size > 2 || (counts.size > 1 && result.total > 8)) {
      result.title = t('activity_total_calls').replace('{n}', String(result.total + folded));
    } else if (result.total === 1 && only) {
      const description = firstText(toolInput(only.use), ['description']);
      if (description && (result.kind === 'shell' || result.kind === 'agent')) result.title = description.split('\n', 1)[0];
      else if (result.kind === 'shell' && workTarget(only.use)) result.title = t('activity_ran_command').replace('{command}', () => workTarget(only!.use));
      else result.title = ['shell','read','edit','search','image','fetch'].includes(result.kind) ?
        activityCount(result.kind, counts.get(result.kind) || 1, t) : workAction(only.use, t);
    } else {
      result.title = Array.from(counts, ([kind, count]) => activityCount(kind, count, t)).join(t('activity_separator'));
    }
  } else {
    result.title = notice || t('work_summary');
    if (work.running) result.status = 'working';
  }
  if (!latest && work.sectionTitle && !isExecutorTitle(work.sectionTitle)) result.title = work.sectionTitle;
  if (isExecutorTitle(result.title)) result.title = t('activity_total_calls').replace('{n}', String(result.total));

  return result;
}

export function accessibleToolState(status: string, t: WorkWords): string {
  if (!status) return '';
  return t(status === 'done' ? 'tool_inspect_returned' : status === 'working' ? 'work_progress_running' :
    status === 'failed' ? 'work_tool_failed' : status);
}

function toolInput(use: TranscriptItem): JsonObject | undefined { return record(use.tool_input ?? use.input); }
function outcome(step: ToolInspectionStep): JsonObject | undefined { return record(step.result?.outcome); }
function objects(value: JsonValue | undefined): JsonObject[] {
  if (!Array.isArray(value)) return [];
  const rows: JsonObject[] = [];
  for (const item of value) { const row = record(item); if (row) rows.push(row); }
  return rows;
}
export function workTarget(use: TranscriptItem): string {
  const kind = workKind(use), input = toolInput(use);
  let subject = workToolSubject(use);
  if (kind === 'shell' || kind === 'monitor') {
    const command = firstVerbatim(input, ['command', 'cmd']) || subject;
    const description = firstText(input, ['description']);
    if (description) subject = description;
    else {
      for (let start = 0; start < command.length;) {
        const next = command.indexOf('\n', start);
        const line = command.slice(start, next < 0 ? command.length : next).trim();
        if (line && !/^#!|^set\s+(?:-[a-zA-Z]+(?:\s+pipefail)?|-o\s+pipefail)\s*;?$/.test(line)) { subject = line; break; }
        if (next < 0) break;
        start = next + 1;
      }
    }
  }
  if (kind === 'agent') subject = firstText(input, ['name', 'description', 'agent_path', 'subagent_type']) || subject;
  if (kind === 'wait') subject = firstText(input, ['duration', 'name']) || subject;
  if (kind === 'skill') subject = firstText(input, ['skill', 'name']) || subject;
  if (kind === 'task') subject = firstText(input, ['subject', 'description', 'task_id']) || subject;
  subject = (subject || firstText(input, ['title', 'description', 'subagent_type'])).split('\n', 1)[0];
  return subject.length > 160 ? subject.slice(0, 160) + '…' : subject;
}

export function hasWorkArguments(use: TranscriptItem): boolean {
  const value = use.tool_input ?? use.input;
  if (value == null) return !!use.text;
  if (typeof value === 'string') return !!value;
  return typeof value !== 'object' || Object.keys(value).length > 0;
}

export function hasWorkDetails(step: ToolInspectionStep): boolean {
  const out = outcome(step);
  return hasWorkArguments(step.use) || step.images.length > 0 || step.reports.length > 0 ||
    step.results.length > 0 || typeof out?.exit_code === 'number' || typeof out?.duration_ms === 'number' || !!(out?.patch || out?.tail || out?.persisted || out?.git || out?.answers || objects(out?.files).length);
}

export function workMetrics(step: ToolInspectionStep, t: WorkWords, duration: (ms: JsonValue | undefined) => string): string {
  const out = outcome(step), words: string[] = [];
  if (typeof out?.exit_code === 'number') words.push(t('ev_exit').replace('{n}', String(out.exit_code)));
  const elapsed = duration(out?.duration_ms);
  if (elapsed) words.push(elapsed);
  const files = objects(out?.files);
  if (files.length && files.some(f => typeof f.adds === 'number' || typeof f.dels === 'number')) {
    const added = files.reduce((n, f) => n + (typeof f.adds === 'number' ? f.adds : 0), 0);
    const removed = files.reduce((n, f) => n + (typeof f.dels === 'number' ? f.dels : 0), 0);
    words.push('+' + added + ' −' + removed);
  }
  const read = record(out?.read);
  if (read?.unchanged === true) words.push(t('ev_unchanged'));
  else if (typeof read?.start === 'number' && typeof read.lines === 'number' && read.lines > 0) {
    words.push(t('ev_line_range').replace('{first}', String(read.start)).replace('{last}', String(read.start + read.lines - 1)));
  }
  return words.join(' · ');
}

/** Multiple lifecycle receipts for one child are one agent, even across intervening waits. */
export function childActivitySummary(steps: readonly WorkInspectionStep[], t: WorkWords): string {
  const children = new Map<string, string>();
  for (const step of steps) {
    if (step.kind !== 'tool' || workKind(step.use) !== 'agent') continue;
    const input = toolInput(step.use), id = firstText(input, ['agent_thread_id', 'agent_path']), phase = firstText(input, ['event']);
    if (id && phase) children.set(id, phase);
  }
  if (!children.size) return '';
  const ended = [...children.values()].filter(phase => ['completed', 'failed', 'stopped'].includes(phase)).length;
  const count = t('ev_children').replace('{n}', String(children.size));
  return ended ? count + ' · ' + t('ev_children_ended').replace('{n}', String(ended)) : count;
}

export function workDetails(step: ToolInspectionStep, t: WorkWords, includeArguments: boolean = true): WorkDetail[] {
  const use = step.use, input = toolInput(use), out = outcome(step), kind = workKind(use);
  const fields: WorkDetail[] = [];
  const add = (key: string, style: DetailStyle, text: string, language?: string): void => { if (text) fields.push({ key, style, text, language }); };
  if (kind === 'shell' || kind === 'monitor') add('ev_command', 'code', firstVerbatim(input, ['command', 'cmd']));
  if (kind === 'mcp') add('ev_action_mcp', 'code', workToolName(use));
  if (kind === 'skill') add('ev_action_skill', 'text', [firstText(input, ['skill', 'name']), firstText(input, ['args'])].filter(Boolean).join('\n'));
  if (kind === 'edit') {
    add('ev_files', 'code', objects(out?.files).map(f => {
      const path = firstText(f, ['path']);
      return path && (typeof f.adds === 'number' || typeof f.dels === 'number' ? path + '   +' + (f.adds || 0) + ' −' + (f.dels || 0) : path);
    }).filter(Boolean).join('\n'));
    const patch = firstVerbatim(out, ['patch']);
    if (patch) add('ev_patch', 'diff', patch);
    else {
      const proposed = firstVerbatim(input, ['diff', 'patch', 'content']);
      const file = firstText(input, ['file_path', 'path']);
      const language = /\.([\w+]+)$/.exec(file)?.[1];
      if (/^(?:diff --git |@@ |--- [^\n]*\n\+\+\+ )/m.test(proposed)) add('ev_proposed_diff', 'diff', proposed);
      else add('ev_proposed_content', 'code', proposed, language);
      add('ev_replace_before', 'code', firstVerbatim(input, ['old_string']), language);
      add('ev_replace_after', 'code', firstVerbatim(input, ['new_string']), language);
    }
    if (out?.patch_cut === true) add('ev_patch_cut', 'text', t('ev_patch_excerpt'));
  }
  if (kind === 'search' || kind === 'fetch' || kind === 'read') {
    add(kind === 'read' ? 'ev_files' : kind === 'fetch' ? 'ev_address' : 'ev_query', 'code', workToolSubject(use));
  }
  if (kind === 'agent') add('ev_agent', 'text', [firstText(input, ['name', 'agent_path']), firstText(input, ['model']), firstText(input, ['message', 'prompt'])].filter(Boolean).join('\n'));
  if (kind === 'plan') {
    add('ev_action_plan', 'text', objects(input?.plan ?? input?.todos ?? input?.items).map(row => {
      const title = firstText(row, ['step', 'content', 'subject']);
      const state = firstText(row, ['status']);
      const keys: Record<string, string> = { pending: 'ev_pending', in_progress: 'working', completed: 'done' };
      return title && (Object.prototype.hasOwnProperty.call(keys, state) ? t(keys[state]) + ' · ' : '') + title;
    }).filter(Boolean).join('\n'));
  }
  const answers = record(out?.answers);
  if (kind === 'question') {
    const prompts = objects(input?.questions).map(row => firstText(row, ['question', 'prompt'])).filter(Boolean);
    if (answers) add('ev_answers', 'text', Object.keys(answers).map(q => [q, firstText(answers, [q])].filter(Boolean).join('\n')).join('\n\n'));
    else add('ev_action_question', 'text', prompts.join('\n'));
  }
  const git = record(out?.git);
  if (git) {
    const commit = record(git.commit), push = record(git.push), pr = record(git.pr), branch = record(git.branch);
    add('ev_git', 'code', [firstText(commit, ['sha']), firstText(commit, ['branch']), firstText(push, ['branch']),
      firstText(pr, ['url']), firstText(branch, ['ref'])].filter(Boolean).join(' · '));
  }
  const output = step.results.map((r, index) => {
    if (step.results.length === 1) return r.text || '';
    const status = workToolStatus({ ...step, result: r, failed: r.is_error === true }, false);
    const label = t('ev_result_n').replace('{n}', String(index + 1)) + ' · ' + t(status === 'failed' ? 'work_tool_failed' : status);
    return [label, r.text || ''].filter(Boolean).join('\n');
  }).filter(Boolean).join('\n');
  add('work_output', kind === 'agent' || kind === 'question' ? 'text' : 'code', output);
  const tail = firstText(out, ['tail']);
  if (tail && !output.endsWith(tail)) add('ev_output_tail', 'code', tail);
  if (step.results.some(r => typeof r.full_size === 'number' && r.full_size > 0)) add('ev_output_excerpt', 'text', t('ev_output_excerpt_note'));
  add('ev_output_file', 'code', firstText(record(out?.persisted), ['path']));
  if (includeArguments && (!fields.length || kind === 'tool') && hasWorkArguments(use)) {
    fields.unshift({ key: 'work_arguments', style: 'code', text: workToolArguments(use) });
  }
  return fields;
}

export interface InspectedTool { step: ToolInspectionStep; live: boolean; }

export interface ToolInspectorView {
  name: string;
  title: string;
  path: string;
  kind: WorkKind;
  status: string;
  tab: string;
  fields: WorkDetail[];
  firstLine?: number;
  fileResult: boolean;
  fileSearch: boolean;
  hasOutput: boolean;
}

/** Keep recorded output and proposed input distinguishable, including calls that returned without text. */
export function inspectTool(selection: InspectedTool, t: WorkWords): ToolInspectorView {
  const { step } = selection;
  const use = step.use, kind = workKind(use), input = toolInput(use), out = outcome(step);
  const status = workToolStatus(step, selection.live);
  const name = workToolName(use) || t('tool');
  const path = kind === 'read' || kind === 'edit' ? firstText(input, ['file_path', 'path']) : '';
  const title = path.split(/[/\\]/).pop() || firstText(input, ['description', 'title']) ||
    (kind === 'search' ? workToolSubject(use) : '') || workAction(use, t);
  const files = objects(out?.files);
  const pathInHeader = path && (kind === 'read' || (files.length === 1 && firstText(files[0], ['path']) === path));
  let fields = workDetails(step, t, false).filter(f =>
    !(pathInHeader && f.key === 'ev_files') && !(f.key === 'ev_query' && f.text === title));
  const before = fields.find(f => f.key === 'ev_replace_before'), after = fields.find(f => f.key === 'ev_replace_after');
  if (before && after) {
    const prefix = (text: string, sign: string): string => text.split('\n').map(line => sign + line).join('\n');
    fields = fields.filter(f => f !== before && f !== after);
    fields.push({ key: 'ev_proposed_diff', style: 'diff', text: prefix(before.text, '-') + '\n' + prefix(after.text, '+') });
  }
  const output = fields.find(f => f.key === 'work_output') || fields.find(f => f.key === 'ev_output_tail');
  const patch = fields.find(f => f.key === 'ev_patch');
  const primary = status === 'done' && patch ? patch : output || patch;
  if (primary) fields = [primary, ...fields.filter(f => f !== primary)];
  const read = record(out?.read);
  const start = read?.start;
  const singleResult = status === 'done' && step.results.length === 1;
  return { name, title, path, kind, status, fields, hasOutput: !!output,
    firstLine: typeof start === 'number' && Number.isInteger(start) && start > 0 && singleResult ? start : undefined,
    fileResult: singleResult && kind === 'read' && !step.images.length && read?.unchanged !== true,
    fileSearch: singleResult && /(?:^|\.)(?:grep|search_files)$/i.test(name),
    tab: patch && status === 'done' ? 'tool_inspect_changes' : kind === 'read' && status === 'done' ? 'content' :
      kind === 'shell' || kind === 'monitor' ? 'tool_inspect_output' : 'tool_inspect_result' };
}

/** Only adjacent successful operations of the same kind share a disclosure. */
export function workRuns(steps: readonly WorkInspectionStep[]): Map<string, WorkRun> {
  const runs = new Map<string, WorkRun>();
  let members: string[] = [], kind: WorkKind = 'tool';
  const flush = (): void => {
    if (members.length >= 3) {
      const run: WorkRun = { first: members[0], count: members.length, kind };
      for (const id of members) runs.set(id, run);
    }
    members = [];
  };
  for (const step of steps) {
    const next = step.kind === 'tool' ? workKind(step.use) : 'tool';
    if (step.kind !== 'tool' || !['read', 'edit', 'search'].includes(next) || workToolStatus(step, false) !== 'done' || step.reports.length || step.images.length) { flush(); continue; }
    if (next !== kind) flush();
    kind = next; members.push(step.id);
  }
  flush();
  return runs;
}

/** Limit both line count and a single minified line in an opened tool detail. */
export function workExcerpt(text: string, expanded: boolean): string {
  if (expanded) return text;
  const head = text.slice(0, 1200);
  const lines = head.split('\n', 9);
  const value = lines.length > 8 ? lines.slice(0, 8).join('\n') : head;
  return value.length < text.length ? value + '…' : text;
}

/**
 * Structured tool-turn presentation matching iOS `ToolViews.swift`, `WorkNow.swift`,
 * and `TurnChangesCard.swift`. Pure TypeScript — zero ArkUI dependencies.
 */

export type ToolIconRole =
  | 'terminal'
  | 'read'
  | 'edit'
  | 'write'
  | 'search'
  | 'find'
  | 'web'
  | 'subagent'
  | 'todo'
  | 'git'
  | 'code'
  | 'plan'
  | 'question'
  | 'skill'
  | 'thought'
  | 'fold'
  | 'tool';

export interface WorkStepView {
  readonly id: string;
  readonly kind: 'tool' | 'thought' | 'narration' | 'fold';
  readonly iconRole: ToolIconRole;
  readonly verb: string;
  readonly subject: string;
  readonly inputPreview: string;
  readonly outputPreview: string;
  readonly running: boolean;
  readonly failed: boolean;
  readonly filePath: string;
  readonly additions: number;
  readonly deletions: number;
}

export interface WorkNowLine {
  readonly id: string;
  readonly text: string;
  readonly more: number;
}

export interface TurnChangedFile {
  readonly path: string;
  readonly shown: string;
  readonly additions: number;
  readonly deletions: number;
  readonly exact: boolean;
  readonly created: boolean;
}

export interface TurnChangeSet {
  readonly files: readonly TurnChangedFile[];
  readonly totalAdditions: number;
  readonly totalDeletions: number;
  readonly exact: boolean;
}

const QUIET_WORK_NOW_TOOLS = new Set([
  'TodoWrite', 'TaskCreate', 'TaskUpdate', 'TaskList', 'TaskGet', 'ToolSearch'
]);

const KNOWN_VERBS: Record<string, string> = {
  Bash: 'Run',
  PowerShell: 'Run',
  Read: 'Read',
  NotebookRead: 'Read',
  Edit: 'Edit',
  MultiEdit: 'Edit',
  NotebookEdit: 'Edit',
  Write: 'Write',
  Grep: 'Search',
  Glob: 'Find',
  LS: 'Find',
  WebFetch: 'Open Web Page',
  WebSearch: 'Web Search',
  Task: 'Subtask',
  Agent: 'Subtask',
  TodoWrite: 'Update To-dos',
  TaskCreate: 'Add To-Do',
  TaskUpdate: 'Update To-Do',
  TaskList: 'List To-Dos',
  TaskGet: 'Read To-Do',
  TaskStop: 'Stop Task',
  TaskOutput: 'Read Output',
  BashOutput: 'Read Output',
  SendMessage: 'Message Agent',
  ListAgents: 'List Agents',
  LSP: 'Look Up Code',
  EnterWorktree: 'Enter Worktree',
  ExitWorktree: 'Leave Worktree',
  AskUserQuestion: 'Question',
  ask_user: 'Question',
  ExitPlanMode: 'Submit Plan',
  EnterPlanMode: 'Enter Plan Mode',
  Skill: 'Use Skill',
  Workflow: 'Run Workflow',
  computer_state: 'View Computer',
  computer_run: 'Control Computer',
  browser_run: 'Use Browser',
  browser_look: 'View Browser'
};

export function toolBaseName(raw: string): string {
  const trimmed = (raw || '').trim();
  if (!trimmed) return '';
  if (trimmed.startsWith('mcp__')) {
    const parts = trimmed.split('__').filter((p) => p.length > 0);
    return parts.length >= 3 ? parts[parts.length - 1] : trimmed;
  }
  return trimmed;
}

function humanizeToken(raw: string): string {
  const spaced = raw
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/[_-]+/g, ' ')
    .trim();
  if (!spaced) return '';
  return spaced
    .split(/\s+/)
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(' ');
}

export function toolVerb(raw: string): string {
  const trimmed = (raw || '').trim();
  if (!trimmed) return 'Work';
  if (trimmed.startsWith('mcp__')) {
    const parts = trimmed.split('__').filter((p) => p.length > 0);
    if (parts.length >= 3) {
      const server = humanizeToken(parts[1]);
      const action = humanizeToken(parts.slice(2).join(' '));
      return server && action ? `${server} · ${action}` : action || server;
    }
  }
  const base = toolBaseName(trimmed);
  if (KNOWN_VERBS[base]) return KNOWN_VERBS[base];
  return humanizeToken(base) || 'Work';
}

export function toolIconRole(raw: string): ToolIconRole {
  const trimmed = (raw || '').trim();
  if (trimmed.startsWith('mcp__')) return 'skill';
  const base = toolBaseName(trimmed);
  if (base === 'Bash' || base === 'PowerShell' || base === 'BashOutput') return 'terminal';
  if (base === 'Read' || base === 'NotebookRead') return 'read';
  if (base === 'Edit' || base === 'MultiEdit' || base === 'NotebookEdit') return 'edit';
  if (base === 'Write') return 'write';
  if (base === 'Grep') return 'search';
  if (base === 'Glob' || base === 'LS') return 'find';
  if (base === 'WebFetch' || base === 'WebSearch' || base === 'browser_run' || base === 'browser_look') return 'web';
  if (base === 'Task' || base === 'Agent' || base === 'ListAgents' || base === 'SendMessage') return 'subagent';
  if (base === 'TodoWrite' || base.startsWith('Task')) return 'todo';
  if (base === 'EnterWorktree' || base === 'ExitWorktree') return 'git';
  if (base === 'LSP') return 'code';
  if (base === 'ExitPlanMode' || base === 'EnterPlanMode') return 'plan';
  if (base === 'AskUserQuestion' || base === 'ask_user') return 'question';
  if (base === 'Skill' || base === 'Workflow') return 'skill';
  return 'tool';
}

function asRecord(value: Object | undefined): Record<string, Object> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {};
  return value as Record<string, Object>;
}

function strField(rec: Record<string, Object>, key: string): string {
  const val = rec[key];
  return typeof val === 'string' ? val.trim() : '';
}

function clip(value: string, max: number = 140): string {
  const single = value.replace(/\s+/g, ' ').trim();
  if (single.length <= max) return single;
  return single.slice(0, max - 1) + '…';
}

export function compactDisplayPath(rawPath: string): string {
  const cleaned = (rawPath || '').trim();
  if (!cleaned) return '';
  const parts = cleaned.split(/[\\/]/).filter((p) => p.length > 0);
  if (parts.length <= 2) return cleaned;
  return parts.slice(parts.length - 2).join('/');
}

function countLines(text: string): number {
  if (!text) return 0;
  let count = 1;
  for (let i = 0; i < text.length; i++) {
    if (text.charCodeAt(i) === 10) count++;
  }
  return count;
}

function estimateEditCounts(base: string, input: Record<string, Object>): { add: number; del: number; exact: boolean } {
  if (base === 'Edit') {
    const oldStr = typeof input['old_string'] === 'string' ? input['old_string'] : '';
    const newStr = typeof input['new_string'] === 'string' ? input['new_string'] : '';
    if (oldStr || newStr) {
      const oldLines = oldStr ? oldStr.split('\n') : [];
      const newLines = newStr ? newStr.split('\n') : [];
      let prefix = 0;
      while (prefix < oldLines.length && prefix < newLines.length && oldLines[prefix] === newLines[prefix]) prefix++;
      let suffix = 0;
      while (
        suffix < oldLines.length - prefix &&
        suffix < newLines.length - prefix &&
        oldLines[oldLines.length - 1 - suffix] === newLines[newLines.length - 1 - suffix]
      ) {
        suffix++;
      }
      return {
        add: Math.max(0, newLines.length - prefix - suffix),
        del: Math.max(0, oldLines.length - prefix - suffix),
        exact: true
      };
    }
  } else if (base === 'MultiEdit' && Array.isArray(input['edits'])) {
    let add = 0;
    let del = 0;
    let exact = false;
    for (const editObj of input['edits'] as Object[]) {
      const sub = estimateEditCounts('Edit', asRecord(editObj));
      if (sub.exact) {
        add += sub.add;
        del += sub.del;
        exact = true;
      }
    }
    return { add: add, del: del, exact: exact };
  } else if (base === 'Write') {
    const content = typeof input['content'] === 'string' ? input['content'] : '';
    if (content) return { add: countLines(content), del: 0, exact: true };
  }
  return { add: 0, del: 0, exact: false };
}

function toolSubject(rawName: string, input: Record<string, Object>): { subject: string; filePath: string } {
  const base = toolBaseName(rawName);
  const filePath = strField(input, 'file_path') || strField(input, 'path') || strField(input, 'notebook_path');
  if (base === 'Bash' || base === 'PowerShell') {
    const desc = strField(input, 'description');
    const cmd = strField(input, 'command');
    return { subject: clip(desc || cmd, 120), filePath: '' };
  }
  if (base === 'Read' || base === 'Edit' || base === 'MultiEdit' || base === 'Write' || base === 'NotebookEdit' || base === 'NotebookRead') {
    return { subject: compactDisplayPath(filePath), filePath: filePath };
  }
  if (base === 'Grep') {
    const pat = strField(input, 'pattern');
    return { subject: clip(pat, 80), filePath: filePath };
  }
  if (base === 'Glob' || base === 'LS') {
    return { subject: clip(strField(input, 'pattern') || compactDisplayPath(filePath), 80), filePath: filePath };
  }
  if (base === 'WebFetch') return { subject: clip(strField(input, 'url'), 90), filePath: '' };
  if (base === 'WebSearch') return { subject: clip(strField(input, 'query'), 90), filePath: '' };
  if (base === 'Task' || base === 'Agent') return { subject: clip(strField(input, 'description') || strField(input, 'prompt'), 90), filePath: '' };
  const fallback = strField(input, 'description') || strField(input, 'title') || strField(input, 'query') || compactDisplayPath(filePath);
  return { subject: clip(fallback, 90), filePath: filePath };
}

function runningPhrase(rawName: string, input: Record<string, Object>): string {
  const base = toolBaseName(rawName);
  const { subject } = toolSubject(rawName, input);
  if (!subject) return toolVerb(rawName);
  if (base === 'Bash' || base === 'PowerShell') {
    return strField(input, 'description') ? subject : `Running ${subject}`;
  }
  if (base === 'Read' || base === 'NotebookRead') return `Reading ${subject}`;
  if (base === 'Edit' || base === 'MultiEdit' || base === 'NotebookEdit') return `Editing ${subject}`;
  if (base === 'Write') return `Writing ${subject}`;
  if (base === 'Grep') return `Searching for ${subject}`;
  if (base === 'Glob' || base === 'LS') return `Finding ${subject}`;
  if (base === 'WebFetch') return `Opening ${subject}`;
  if (base === 'WebSearch') return `Searching web for ${subject}`;
  return `${toolVerb(rawName)} ${subject}`;
}

export function parseRawTranscriptItems(raw: string): Record<string, Object>[] {
  if (!raw || raw === '{}') return [];
  try {
    const parsed: Object = JSON.parse(raw) as Object;
    if (!Array.isArray(parsed)) return [];
    return (parsed as Object[]).map((entry) => asRecord(entry));
  } catch (_) {
    return [];
  }
}

/**
 * Extracts structured visual steps (`WorkStepView[]`) from a work message's raw `TranscriptItem[]`.
 */
export function extractWorkStepsFromRaw(raw: string, turnRunning: boolean = false): WorkStepView[] {
  const items = parseRawTranscriptItems(raw);
  if (!items.length) return [];

  const resultMap = new Map<string, Record<string, Object>[]>();
  for (const item of items) {
    const kind = strField(item, 'kind');
    if (kind === 'tool_result') {
      const refId = strField(item, 'tool_use_id') || strField(item, 'id');
      if (refId) {
        const list = resultMap.get(refId) || [];
        list.push(item);
        resultMap.set(refId, list);
      }
    }
  }

  const steps: WorkStepView[] = [];
  for (let i = 0; i < items.length; i++) {
    const item = items[i];
    const kind = strField(item, 'kind');
    const id = strField(item, 'id') || `step-${i}`;
    if (kind === 'tool_use') {
      const rawName = strField(item, 'tool_name') || strField(item, 'tool') || strField(item, 'name');
      const input = asRecord(item['tool_input'] || item['input']);
      const useId = strField(item, 'tool_use_id') || id;
      const results = resultMap.get(useId) || [];
      const failed = results.some((r) => r['is_error'] === true);
      const running = turnRunning && results.length === 0;
      const base = toolBaseName(rawName);
      const { subject, filePath } = toolSubject(rawName, input);
      const counts = estimateEditCounts(base, input);
      const inputKeys = Object.keys(input);
      const inputPreview = base === 'Bash' || base === 'PowerShell'
        ? strField(input, 'command')
        : inputKeys.length > 0
          ? JSON.stringify(input, null, 2).slice(0, 600)
          : strField(item, 'text');
      const outputPreview = results
        .map((r) => strField(r, 'text') || strField(r, 'content'))
        .filter((s) => s.length > 0)
        .join('\n')
        .slice(0, 600);
      steps.push({
        id: id,
        kind: 'tool',
        iconRole: toolIconRole(rawName),
        verb: toolVerb(rawName),
        subject: subject,
        inputPreview: inputPreview,
        outputPreview: outputPreview,
        running: running,
        failed: failed,
        filePath: filePath,
        additions: counts.add,
        deletions: counts.del
      });
    } else if (kind === 'thinking') {
      const text = strField(item, 'text');
      if (!text) continue;
      let firstLine = text.split('\n')[0].trim();
      if (firstLine.length > 4 && firstLine.startsWith('**') && firstLine.endsWith('**')) {
        firstLine = firstLine.slice(2, -2).trim();
      }
      steps.push({
        id: id,
        kind: 'thought',
        iconRole: 'thought',
        verb: 'Thinking',
        subject: clip(firstLine, 120),
        inputPreview: '',
        outputPreview: text.slice(0, 800),
        running: false,
        failed: false,
        filePath: '',
        additions: 0,
        deletions: 0
      });
    } else if (kind === 'fold') {
      const toolCount = typeof item['tool_count'] === 'number' ? item['tool_count'] as number : 0;
      const thoughtCount = typeof item['thought_count'] === 'number' ? item['thought_count'] as number : 0;
      steps.push({
        id: id,
        kind: 'fold',
        iconRole: 'fold',
        verb: 'Collapsed steps',
        subject: String(toolCount + thoughtCount),
        inputPreview: '',
        outputPreview: '',
        running: false,
        failed: false,
        filePath: '',
        additions: 0,
        deletions: 0
      });
    }
  }
  return steps;
}

/**
 * Mirrors iOS `WorkNow.of(_:)`: computes the live running summary line for a work pill.
 */
export function extractWorkNowFromRaw(raw: string, turnRunning: boolean): WorkNowLine | undefined {
  if (!turnRunning) return undefined;
  const items = parseRawTranscriptItems(raw);
  if (!items.length) return undefined;

  const answered = new Set<string>();
  for (const item of items) {
    if (strField(item, 'kind') === 'tool_result') {
      const refId = strField(item, 'tool_use_id') || strField(item, 'id');
      if (refId) answered.add(refId);
    }
  }

  const runningCalls: Record<string, Object>[] = [];
  let latestThought: Record<string, Object> | undefined;
  let latestCall: Record<string, Object> | undefined;

  for (let i = items.length - 1; i >= 0; i--) {
    const item = items[i];
    const kind = strField(item, 'kind');
    if (kind === 'tool_use') {
      const rawName = strField(item, 'tool_name') || strField(item, 'tool') || strField(item, 'name');
      if (QUIET_WORK_NOW_TOOLS.has(toolBaseName(rawName))) continue;
      const useId = strField(item, 'tool_use_id') || strField(item, 'id');
      if (!answered.has(useId)) runningCalls.push(item);
      if (!latestCall) latestCall = item;
    } else if (kind === 'thinking' && !latestThought && !latestCall) {
      latestThought = item;
    }
  }

  if (runningCalls.length > 0) {
    const allReads = runningCalls.length > 1 && runningCalls.every((c) => {
      const name = strField(c, 'tool_name') || strField(c, 'tool') || strField(c, 'name');
      return toolBaseName(name) === 'Read';
    });
    const newest = runningCalls[0];
    const id = strField(newest, 'id') || 'work-now';
    if (allReads) {
      return { id: id, text: `Reading ${runningCalls.length} files`, more: 0 };
    }
    const rawName = strField(newest, 'tool_name') || strField(newest, 'tool') || strField(newest, 'name');
    const input = asRecord(newest['tool_input'] || newest['input']);
    return { id: id, text: runningPhrase(rawName, input), more: Math.max(0, runningCalls.length - 1) };
  }

  if (latestCall) {
    const rawName = strField(latestCall, 'tool_name') || strField(latestCall, 'tool') || strField(latestCall, 'name');
    const input = asRecord(latestCall['tool_input'] || latestCall['input']);
    return { id: strField(latestCall, 'id') || 'work-now', text: runningPhrase(rawName, input), more: 0 };
  }

  if (latestThought) {
    let line = strField(latestThought, 'text').split('\n')[0].trim();
    if (line.length > 4 && line.startsWith('**') && line.endsWith('**')) line = line.slice(2, -2).trim();
    return { id: strField(latestThought, 'id') || 'work-thought', text: clip(line || 'Thinking', 120), more: 0 };
  }

  return undefined;
}

/**
 * Mirrors iOS `TurnChangesCard`: aggregates files edited or written during a turn.
 */
export function extractTurnChangesFromRaw(raw: string): TurnChangeSet | undefined {
  return turnChangesFromItems(parseRawTranscriptItems(raw));
}

export function turnChangesFromWork(work: ToolTurnProjection): TurnChangeSet | undefined {
  const uses: Record<string, Object>[] = [];
  for (const step of work.inspectionSteps) {
    if (step.kind === 'tool') uses.push(step.use as unknown as Record<string, Object>);
  }
  return turnChangesFromItems(uses);
}

function turnChangesFromItems(items: Record<string, Object>[]): TurnChangeSet | undefined {
  if (!items.length) return undefined;

  const byPath = new Map<string, { add: number; del: number; exact: boolean; created: boolean }>();
  for (const item of items) {
    if (strField(item, 'kind') !== 'tool_use') continue;
    const rawName = strField(item, 'tool_name') || strField(item, 'tool') || strField(item, 'name');
    const base = toolBaseName(rawName);
    if (base !== 'Edit' && base !== 'MultiEdit' && base !== 'Write' && base !== 'NotebookEdit') continue;
    const input = asRecord(item['tool_input'] || item['input']);
    const path = strField(input, 'file_path') || strField(input, 'path') || strField(input, 'notebook_path');
    if (!path) continue;
    const counts = estimateEditCounts(base, input);
    const prev = byPath.get(path);
    if (prev) {
      prev.add += counts.add;
      prev.del += counts.del;
      prev.exact = prev.exact && counts.exact;
    } else {
      byPath.set(path, { add: counts.add, del: counts.del, exact: counts.exact, created: base === 'Write' });
    }
  }

  if (byPath.size === 0) return undefined;
  const files: TurnChangedFile[] = [];
  let totalAdd = 0;
  let totalDel = 0;
  let allExact = true;
  byPath.forEach((stats, path) => {
    totalAdd += stats.add;
    totalDel += stats.del;
    if (!stats.exact) allExact = false;
    files.push({
      path: path,
      shown: compactDisplayPath(path),
      additions: stats.add,
      deletions: stats.del,
      exact: stats.exact,
      created: stats.created
    });
  });

  return {
    files: files,
    totalAdditions: totalAdd,
    totalDeletions: totalDel,
    exact: allExact && (totalAdd > 0 || totalDel > 0)
  };
}

export const TURN_CHANGES_INITIAL_VISIBLE = 3;

export interface UnifiedDiffSummary {
  readonly hunks: number;
  readonly additions: number;
  readonly deletions: number;
}

export function summarizeUnifiedDiff(patch: string): UnifiedDiffSummary {
  if (!patch) return { hunks: 0, additions: 0, deletions: 0 };
  const lines = patch.replace(/\r\n?/g, '\n').split('\n');
  let hunks = 0;
  let additions = 0;
  let deletions = 0;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (line.startsWith('@@')) {
      hunks++;
    } else if (line.startsWith('+') && !line.startsWith('+++')) {
      additions++;
    } else if (line.startsWith('-') && !line.startsWith('---')) {
      deletions++;
    }
  }
  return { hunks: hunks, additions: additions, deletions: deletions };
}

export function gitMarkBadgeLabel(mark: string): string {
  const trimmed = (mark || '').trim();
  if (!trimmed) return 'M';
  if (trimmed === '??' || trimmed === '?' || trimmed === 'U') return '?';
  return trimmed.slice(0, 1).toUpperCase();
}
