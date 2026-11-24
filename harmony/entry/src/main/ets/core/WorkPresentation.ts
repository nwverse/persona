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
