import type { NextStepProposal, ToolTurnProjection, TranscriptItem } from '../core/Domain';

// Decode untrusted JSON at the transport boundary. Unknown keys never become executable actions.
export class Wire {
  static readonly legacyStartedBy: string = 'started_by_jarvis';
  static payload(keys: string[], values: Object[]): Record<string, Object> {
    const result: Record<string, Object> = {}; keys.forEach((key: string, index: number) => result[key] = values[index]); return result;
  }
  static readonly maxBytes: number = 4 * 1024 * 1024;
  static parse(raw: string): Record<string, Object> {
    if (raw.length > Wire.maxBytes) throw new Error('response_too_large');
    if (!raw.trim()) return {};
    const value: Object = JSON.parse(raw) as Object;
    return Wire.record(value);
  }
  static record(value: Object | undefined | null): Record<string, Object> {
    if (value === undefined || value === null || typeof value !== 'object' || Array.isArray(value)) return {};
    return value as Record<string, Object>;
  }
  static object(value: Record<string, Object>, key: string): Record<string, Object> { return Wire.record(value[key]); }
  static has(value: Record<string, Object>, key: string): boolean { return Object.keys(value).includes(key); }
  static text(value: Record<string, Object>, key: string, fallback: string = ''): string {
    const result = value[key];
    return typeof result === 'string' ? result as string : fallback;
  }
  static number(value: Record<string, Object>, key: string, fallback: number = 0): number {
    const result = value[key];
    return typeof result === 'number' && Number.isFinite(result as number) ? result as number : fallback;
  }
  static identifier(value: Record<string, Object>, key: string): string {
    const result = value[key];
    if (typeof result === 'string') return result as string;
    return typeof result === 'number' && Number.isSafeInteger(result as number) && (result as number) > 0 ? String(result) : '';
  }
  static bool(value: Record<string, Object>, key: string, fallback: boolean = false): boolean {
    const result = value[key]; return typeof result === 'boolean' ? result as boolean : fallback;
  }
  static rows(value: Record<string, Object>, key: string): Record<string, Object>[] {
    const result = value[key];
    if (!Array.isArray(result)) return [];
    return (result as Object[]).map((item: Object) => Wire.record(item));
  }
  static strings(value: Record<string, Object>, key: string): string[] {
    const result = value[key];
    if (!Array.isArray(result)) return [];
    return (result as Object[]).filter((item: Object) => typeof item === 'string').map((item: Object) => item as string);
  }
}

/** Keep the small directory fields needed by the home badge out of serialized navigation rows. */
export class SessionBadgeFields {
  readonly snapshot: string;
  readonly hidden: boolean;
  readonly quietTodo: string;
  readonly childrenWaiting: boolean;
  readonly pending: boolean;
  readonly parent: string;
  readonly space: string;

  constructor(snapshot: string, data: Record<string, Object>) {
    this.snapshot = snapshot;
    this.hidden = Wire.bool(data, 'assistant') || Wire.bool(data, 'hidden_by_directory') ||
      Wire.bool(data, 'muted') || Wire.bool(data, 'quiet');
    this.quietTodo = Wire.text(Wire.object(data, 'todo'), 'id');
    this.childrenWaiting = Wire.number(Wire.object(data, 'children'), 'needs_you') > 0;
    this.pending = Wire.number(data, 'pending_interactions') > 0;
    this.parent = Wire.text(data, 'folded_under');
    this.space = Wire.text(data, 'space_name');
  }
}

const sessionBadgeFields: WeakMap<ResourceRow, SessionBadgeFields> = new WeakMap();

export class SessionLauncher {
  readonly name: string;
  readonly agent: string;
  readonly label: string;
  readonly promptOptional: boolean;
  constructor(name: string, agent: string, label: string, promptOptional: boolean) { this.promptOptional = promptOptional; this.name = name; this.agent = agent; this.label = label; }
  static choices(records: Record<string, Object>[]): SessionLauncher[] {
    const found = new Set<string>(), choices: SessionLauncher[] = [];
    for (const record of records) {
      const name = Wire.text(record, 'name').trim();
      if (!name || found.has(name)) continue;
      found.add(name);
      const agent = Wire.text(record, 'agent_type', Wire.text(record, 'agent')) ||
        (name === 'codex' || name === 'codex-app' ? 'codex' : 'claude');
      const family = agent === 'claude' ? 'Claude Code' : agent === 'codex' ? 'Codex' : agent;
      const title = Wire.text(record, 'display_name') || name;
      const label = title.toLowerCase() === agent || title.toLowerCase() === family.toLowerCase() ? family : family + ' · ' + title;
      choices.push(new SessionLauncher(name, agent, label, agent !== 'codex' || Wire.bool(record, 'prompt_optional')));
    }
    return choices;
  }
}

export class SessionModelCatalog {
  readonly models: Record<string,Object>[];
  readonly locked: boolean;

  constructor(result: Record<string,Object>) {
    if (!Array.isArray(result['models'])) throw new Error('response_invalid');
    this.locked = !!Wire.text(result,'locked');
    const seen = new Set<string>();
    this.models = Wire.rows(result,'models').filter(model => {
      const id = Wire.text(model,'id');
      if (!id || seen.has(id)) return false;
      seen.add(id);
      return true;
    });
  }

  static validTypedId(id: string): boolean {
    return id.length <= 256 && /^~?[A-Za-z0-9][A-Za-z0-9._/:@\[\]-]*$/.test(id);
  }
}

export class ResourceRow {
  id: string = '';
  title: string = '';
  detail: string = '';
  kind: string = '';
  state: string = '';
  manager: string = '';
  group: string = '';
  machine: string = '';
  session: string = '';
  path: string = '';
  raw: string = '{}';
  constructor(id: string = '', title: string = '', kind: string = '') { this.id = id; this.title = title; this.kind = kind; }
  data(): Record<string, Object> { return Wire.parse(this.raw); }
  supportsScreen(): boolean {
    const data = this.data(), os = Wire.text(data, 'os', Wire.text(Wire.object(data, 'system'), 'os'));
    return this.kind === 'machine' && ['', 'mac', 'macos', 'darwin'].includes(os.toLowerCase()) && Wire.bool(data, 'online') &&
      !Wire.bool(data, 'pending') && !Wire.bool(data, 'pending_confirm') && ['', 'confirmed', 'personal', 'work'].includes(Wire.text(data, 'trust'));
  }
  badgeFields(): SessionBadgeFields {
    const known = sessionBadgeFields.get(this);
    if (known && known.snapshot === this.raw) return known;
    const fields = new SessionBadgeFields(this.raw, this.data());
    sessionBadgeFields.set(this, fields);
    return fields;
  }
  copy(group: string = this.group, state: string = this.state): ResourceRow {
    const row = new ResourceRow(this.id, this.title, this.kind);
    row.group = group; row.state = state; row.manager = this.manager; row.machine = this.machine;
    row.session = this.session; row.path = this.path; row.detail = this.detail; row.raw = this.raw;
    return row;
  }
  matches(other: ResourceRow, group: string = other.group, state: string = other.state): boolean {
    return this.group === group && this.state === state && this.id === other.id && this.kind === other.kind &&
      this.manager === other.manager && this.machine === other.machine && this.session === other.session &&
      this.title === other.title && this.detail === other.detail && this.path === other.path && this.raw === other.raw;
  }
  static reconcile(previous: ResourceRow[], next: ResourceRow[]): ResourceRow[] {
    if (previous === next) return previous;
    let unchanged = previous.length === next.length;
    const rows = next.map((row: ResourceRow, i: number): ResourceRow => {
      const old = previous[i];
      if (old && (old === row || old.matches(row))) return old;
      unchanged = false;
      return row;
    });
    return unchanged ? previous : rows;
  }

  static from(data: Record<string, Object>, kind: string): ResourceRow {
    const row = new ResourceRow(Wire.text(data, 'id', Wire.text(data, 'session_id')),
      Wire.text(data, 'title', Wire.text(data, 'name', Wire.text(data, 'subject', Wire.text(data, 'label')))), kind);
    row.detail = Wire.text(data, 'summary', Wire.text(data, 'notes', Wire.text(data, 'preview', Wire.text(data, 'description', Wire.text(data, 'one_line', Wire.text(data, 'content', Wire.text(data, 'text', Wire.text(data, 'body'))))))));
    row.state = Wire.text(data, 'state', Wire.text(data, 'status'));
    row.manager = Wire.text(data, 'manager', Wire.text(data, 'manager_id'));
    row.machine = Wire.text(data, 'machine_id', Wire.text(data, 'machine'));
    row.session = Wire.text(data, 'session_id', kind === 'session' ? row.id : '');
    row.path = Wire.text(data, 'cwd', Wire.text(data, 'path'));
    row.raw = JSON.stringify(data);
    if (kind === 'session') sessionBadgeFields.set(row, new SessionBadgeFields(row.raw, data));
    return row;
  }
  isInstallingManager(): boolean {
    const data = this.data();
    return !!Wire.text(data, 'installing') || Wire.bool(data, 'installing');
  }
  isDone(): boolean { const data = this.data(); return !!Wire.text(data, 'done_at') || Wire.bool(data, 'done'); }
  key(): string { return JSON.stringify(this.group ? [this.group, this.kind, this.manager, this.machine, this.id] : [this.kind, this.manager, this.machine, this.id]); }
}

export class HeldAgentMessage {
  id: string = '';
  text: string = '';
  computer: string = '';
  line: string = '';
  state: string = '';
  images: number = 0;
  at: string = '';
  deleteOnly: boolean = false;

  static from(data: Record<string, Object>): HeldAgentMessage | undefined {
    const id = Wire.text(data, 'id'), computer = Wire.text(data, 'computer'), state = Wire.text(data, 'state');
    if (!id || !computer || (state !== 'waiting' && state !== 'failed')) return undefined;
    const held = new HeldAgentMessage();
    held.id = id; held.computer = computer; held.state = state;
    held.text = Wire.text(data, 'text'); held.line = Wire.text(data, 'error'); held.images = Wire.number(data, 'images');
    held.at = Wire.text(data, 'at'); held.deleteOnly = Wire.bool(data, 'delete_only');
    return held;
  }
}

export interface ConversationRecapItem {
  id: string;
  text: string;
  at: number;
}

export class ChatMessage {
  id: string = '';
  role: string = 'assistant';
  recaps: ConversationRecapItem[] = [];
  terminalRecap: boolean = false;
  at: number = 0;
  text: string = '';
  liveSummary: string = '';
  providerRetry: boolean = false;
  narration: boolean = false;
  work?: ToolTurnProjection;
  toolCount: number = 0;
  thoughtCount: number = 0;
  nextStep?: NextStepProposal;
  event?: TranscriptItem;
  queued: boolean = false;
  turnDuration: string = '';
  turnMilliseconds?: number;
  processIds?: string[];
  leadIds?: string[];
  partialProcess: boolean = false;
  imageId: string = '';
  imageIds: string[] = [];
  itemId: string = '';
  userTextClipped: boolean = false;
  ownerTarget: string = '';
  imagePaths: string[] = [];
  filePaths: string[] = [];
  running: boolean = false;
  failed: boolean = false;
  deliveryState: string = '';
  held: HeldAgentMessage | undefined;
  localOperation: string = '';
  localImageCount: number = 0;
  source: string = '';
  constructor(id: string = '', role: string = 'assistant', text: string = '') { this.id = id; this.role = role; this.text = text; }
}

@ObservedV2
export class Decision {
  id: string = '';
  machine: string = '';
  session: string = '';
  kind: string = '';
  prompt: string = '';
  options: string[] = [];
  fields: DecisionField[] = [];
  @Trace status: string = 'open';
  revision: string = '';
  raw: string = '{}';
}
export class DecisionField {
  key: string = '';
  title: string = '';
  kind: string = 'text';
  required: boolean = false;
  choices: string[] = [];
}

export class HubProfile {
  id: string = '';
  name: string = '';
  baseUrl: string = '';
  addresses: string[] = [];
  hubId: string = '';
  pin: string = '';
  manager: string = '';
  constructor(id: string = '', name: string = '', baseUrl: string = '') { this.id = id; this.name = name; this.baseUrl = baseUrl; }
}
