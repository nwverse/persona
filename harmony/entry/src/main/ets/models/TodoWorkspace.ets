import { ClientModel } from './ClientModel';
import { HubRepository, segment } from './HubRepository';
import { HttpFailure } from '../platform/HubTransport';
import { ResourceRow, Wire } from './Wire';
import { TODO_HUB_CHOICES, TODO_HUB_SECTIONS, TodoHubCounts, TodoHubRow, todoHubChoiceIncludes, todoHubLabel } from './TodoHubView';

const TODO_STATES: string[] = ['needs_you', 'checking', 'working', 'followup', 'not_started', 'done'];
const TODO_STATUS_ORDER: string[] = ['needs_you', 'checking', 'working', 'followup', 'not_started', 'done'];

interface TodoOwnerSnapshot {
  root: ClientModel;
  client: ClientModel;
  repo: HubRepository;
  rootGeneration: number;
  rootScope: string;
  generation: number;
  scope: string;
  revision: number;
  groupId: string;
  managerId: string;
  todoId: string;
  todoManagerId: string;
}

export class TodoWorkspaceAgent {
  machineId: string = '';
  sessionId: string = '';
  title: string = '';
  cwd: string = '';
  agent: string = '';
  startError: string = '';
  linkedAt: string = '';
  releasedAt: string = '';
  dismissedAt: string = '';
  source: string = '';
  via: string = '';
  raw: string = '{}';

  constructor(data: Record<string, Object> = {}) {
    this.machineId = Wire.text(data, 'machine_id');
    this.sessionId = Wire.text(data, 'session_id');
    this.title = Wire.text(data, 'title');
    this.cwd = Wire.text(data, 'cwd');
    this.agent = Wire.text(data, 'agent');
    this.startError = Wire.text(data, 'start_error');
    this.linkedAt = Wire.text(data, 'linked_at');
    this.releasedAt = Wire.text(data, 'released_at');
    this.dismissedAt = Wire.text(data, 'dismissed_at');
    this.source = Wire.text(data, 'source');
    this.via = Wire.text(data, 'via');
    this.raw = JSON.stringify(data);
  }

  isActive(): boolean { return !this.releasedAt && !this.dismissedAt; }
}

export class TodoWorkspaceSpace {
  machineId: string = '';
  space: string = '';
  linkedAt: string = '';

  constructor(data: Record<string, Object> = {}) {
    this.machineId = Wire.text(data, 'machine_id');
    this.space = Wire.text(data, 'space');
    this.linkedAt = Wire.text(data, 'linked_at');
  }
}

export class TodoWorkspaceItem {
  id: string = '';
  title: string = '';
  notes: string = '';
  manager: string = '';
  origin: string = '';
  planId: string = '';
  doneAt: string = '';
  dueAt: string = '';
  dueDate: string = '';
  due: number = Number.NaN;
  dueBy: number = Number.NaN;
  createdAt: string = '';
  updatedAt: string = '';
  reopenedAt: string = '';
  doneBy: string = '';
  doneReason: string = '';
  startError: string = '';
  project: string = '';
  priority: string = '';
  partOf: string = '';
  hubView: TodoHubRow | undefined;
  stage: string = '';
  prUrl: string = '';
  kind: string = '';
  agents: TodoWorkspaceAgent[] = [];
  spaces: TodoWorkspaceSpace[] = [];
  ask: Record<string, Object> = {};
  supervision: Record<string, Object> = {};
  delivery: Record<string, Object> = {};
  check: Record<string, Object> = {};
  thread: Record<string, Object> = {};
  source: Record<string, Object> = {};
  brief: Record<string, Object> = {};
  lineage: Record<string, Object>[] = [];
  raw: string = '{}';

  constructor(data: Record<string, Object> = {}, manager: string = '') {
    this.id = Wire.text(data, 'id');
    this.title = Wire.text(data, 'title');
    this.notes = Wire.text(data, 'notes');
    this.manager = Wire.text(data, 'manager', manager);
    this.origin = Wire.text(data, 'origin');
    this.planId = Wire.text(data, 'plan_id');
    this.doneAt = Wire.text(data, 'done_at');
    this.dueAt=Wire.text(data,'due_at'); this.dueDate=Wire.text(data,'due_date');
    this.due=Date.parse(this.dueAt); this.dueBy=this.due;
    if (!Number.isFinite(this.due) && /^\d{4}-\d{2}-\d{2}$/.test(this.dueDate)) {
      const parts=this.dueDate.split('-').map((n:string) => Number(n));
      const day=new Date(0); day.setFullYear(parts[0],parts[1]-1,parts[2]); day.setHours(0,0,0,0);
      if (day.getFullYear() === parts[0] && day.getMonth() === parts[1]-1 && day.getDate() === parts[2]) {
        this.due=day.getTime(); day.setDate(day.getDate()+1); this.dueBy=day.getTime()-1000;
      }
    }
    this.createdAt = Wire.text(data, 'created_at');
    this.updatedAt = Wire.text(data, 'updated_at');
    this.reopenedAt = Wire.text(data, 'reopened_at');
    this.doneBy = Wire.text(data, 'done_by');
    this.doneReason = Wire.text(data, 'done_reason');
    this.startError = Wire.text(data, 'start_error');
    this.project = Wire.text(data, 'project');
    this.priority = Wire.text(data, 'priority');
    this.partOf = Wire.text(data, 'part_of');
    this.hubView = TodoHubRow.read(data);
    this.stage = Wire.text(data, 'stage');
    this.prUrl = Wire.text(data, 'pr_url');
    this.kind = Wire.text(data, 'kind');
    this.agents = Wire.rows(data, 'agents').map((item: Record<string, Object>) => new TodoWorkspaceAgent(item));
    this.spaces = Wire.rows(data, 'spaces').map((item: Record<string, Object>) => new TodoWorkspaceSpace(item));
    this.ask = Wire.object(data, 'ask');
    this.supervision = Wire.object(data, 'supervision');
    this.delivery = Wire.object(data, 'delivery');
    this.check = Wire.object(data, 'check');
    this.thread = Wire.object(data, 'thread');
    this.source = Wire.object(data, 'source');
    this.brief = Wire.object(data, 'brief');
    this.lineage = Wire.rows(data, 'lineage');
    this.raw = JSON.stringify(data);
  }

  isDone(): boolean { return !!this.doneAt; }
  activeAgents(): TodoWorkspaceAgent[] { return this.agents.filter((agent: TodoWorkspaceAgent) => agent.isActive()); }
  previousAgents(): TodoWorkspaceAgent[] { return this.agents.filter((agent: TodoWorkspaceAgent) => !agent.isActive()); }
  data(): Record<string, Object> { return Wire.parse(this.raw); }
}

export const TODO_DUE_FILTERS:string[] = ['all','no_due','today','week','month'];
export const TODO_LIST_ORDERS:string[] = ['recent','due','priority'];

export function todoMatchesDue(todo:TodoWorkspaceItem,filter:string,now:number,firstWeekday:number):boolean {
  if (filter === 'all') return true;
  if (filter === 'no_due') return !Number.isFinite(todo.due);
  if (!Number.isFinite(todo.due)) return false;
  if (!todo.isDone() && todo.dueBy < now) return true;
  const start=new Date(now); start.setHours(0,0,0,0);
  if (filter === 'month') start.setDate(1);
  if (filter === 'week') start.setDate(start.getDate() - (start.getDay() - firstWeekday + 7) % 7);
  const end=new Date(start.getTime());
  if (filter === 'month') end.setMonth(end.getMonth()+1);
  else end.setDate(end.getDate()+(filter === 'week' ? 7 : 1));
  return todo.due >= start.getTime() && todo.due < end.getTime();
}

export class TodoWorkspaceGroup {
  key: string = '';
  title: string = '';
  state: string = '';
  members: TodoWorkspaceItem[] = [];

  constructor(key: string, title: string, state: string, members: TodoWorkspaceItem[]) {
    this.key = key;
    this.title = title;
    this.state = state;
    this.members = members;
  }

  get lead(): TodoWorkspaceItem { return this.members[0] || new TodoWorkspaceItem(); }
  related(): TodoWorkspaceItem[] { return this.members.slice(1); }
}

export class TodoWorkspaceSection {
  key: string = '';
  title: string = '';
  rows: TodoWorkspaceGroup[] = [];

  constructor(key: string, title: string, rows: TodoWorkspaceGroup[]) {
    this.key = key;
    this.title = title;
    this.rows = rows;
  }
}

export class TodoWorkspaceSpaceChoice {
  machineId: string = '';
  space: string = '';
  label: string = '';
  constructor(machineId: string, space: string, label: string) {
    this.machineId = machineId;
    this.space = space;
    this.label = label;
  }
}

function todoAgentSession(agents: TodoWorkspaceAgent[], sessions: ResourceRow[], agent: TodoWorkspaceAgent): ResourceRow | undefined {
  return sessions.find((session: ResourceRow) => session.kind === 'session' &&
    session.machine === agent.machineId && (session.session || session.id) === agent.sessionId);
}

function askIsOpen(todo: TodoWorkspaceItem): boolean {
  return !todo.isDone() && !!Wire.text(todo.ask, 'verdict') && !!Wire.text(todo.ask, 'asked_at');
}

function acceptanceStands(todo: TodoWorkspaceItem): boolean {
  if (Wire.text(todo.supervision, 'state') !== 'accepted') return false;
  const reviews = Wire.rows(todo.supervision, 'reviews');
  return !!reviews.length && Wire.text(reviews[reviews.length - 1], 'verdict') === 'accepted';
}

/** The native six-state view, projected from the server's todo and live session fields. */
export function todoWorkspaceStatus(todo: TodoWorkspaceItem, sessions: ResourceRow[]): string {
  // A failed task start is stored as done_at + start_error. It still needs the owner's
  // attention until they retry or close it explicitly, so inspect this before done_at.
  if (todo.startError) return 'needs_you';
  if (todo.isDone()) return 'done';

  const askOutcome = Wire.text(todo.ask, 'outcome');
  const active = todo.activeAgents();
  const supervisionState = Wire.text(todo.supervision, 'state');
  const ownerDecision = supervisionState === 'user_check' || supervisionState === 'needs_user' || askIsOpen(todo) && (askOutcome === 'decide' || askOutcome === 'cannot');
  let checking: boolean = false;
  let working: boolean = false;
  let needsYou: boolean = false;
  for (const agent of active) {
    if (agent.startError) continue;
    const session = todoAgentSession(active, sessions, agent);
    if (!session) continue;
    const data = session.data();
    const state = session.state || Wire.text(data, 'state', Wire.text(data, 'status'));
    const turnError = Wire.text(data, 'turn_error');
    const stopped = state === 'unread' || state === 'idle';
    const interrupted = !!turnError && stopped && Wire.text(data, 'turn_error_kind') !== 'usage_limit';
    if (state === 'needs_you' || interrupted) needsYou = true;
    else if (!!turnError && stopped && Wire.text(data, 'turn_error_kind') === 'usage_limit') checking = true;
    else if (state === 'unread') checking = true;
    else {
      const work = Wire.object(data, 'work');
      const waitingOnWork = Wire.text(work, 'phase') === 'blocked' &&
        ['agent', 'external', 'transient'].includes(Wire.text(work, 'on'));
      if (state === 'working' || state === 'waiting' || Wire.bool(data, 'starting') || Wire.bool(data, 'background_working') || waitingOnWork) working = true;
    }
  }
  if (needsYou) return 'needs_you';
  if (ownerDecision) return 'needs_you';
  if (working && !checking) return 'working';
  if (acceptanceStands(todo) || Wire.text(todo.delivery, 'state') === 'live') return 'done';
  if (checking || supervisionState === 'pending_review' || askIsOpen(todo)) return 'checking';
  const deliveryState = Wire.text(todo.delivery, 'state');
  const hasOpenPr = deliveryState === 'pr' || !!todo.prUrl && ['none', ''].includes(deliveryState) &&
    !['merge', 'deploy', 'verify'].includes(todo.stage);
  if (hasOpenPr || ['merged', 'off'].includes(deliveryState) || todo.prUrl && ['merge', 'deploy', 'verify'].includes(todo.stage)) return 'followup';
  if (working) return 'working';
  return active.length ? 'followup' : 'not_started';
}

function normalizedTitle(value: string): string {
  return value.toLowerCase().replace(/[\s\p{P}\p{S}]+/gu, '');
}

function relationKeys(todo: TodoWorkspaceItem): string[] {
  const keys: string[] = [];
  if (todo.planId) keys.push('plan:' + todo.planId);
  const pr = todo.prUrl.trim().replace(/[#?].*$/, '').replace(/\/+$/, '').toLowerCase();
  if (pr) keys.push('pr:' + pr);
  const issue = todo.title.match(/#([0-9]{2,7})\b/);
  if (issue) keys.push('issue:' + issue[1]);
  const title = normalizedTitle(todo.title);
  if (title.length >= 6) keys.push('title:' + title);
  return keys;
}

function related(left: TodoWorkspaceItem, right: TodoWorkspaceItem): boolean {
  if (left.id === right.id || left.isDone() !== right.isDone()) return false;
  const rightKeys = relationKeys(right);
  return relationKeys(left).some((key: string) => rightKeys.includes(key));
}

export function relatedTodoMembers(items: TodoWorkspaceItem[], todo: TodoWorkspaceItem): TodoWorkspaceItem[] {
  const matching: TodoWorkspaceItem[] = [];
  const seen: Set<string> = new Set([todo.id]);
  const queue: TodoWorkspaceItem[] = [todo];
  while (queue.length) {
    const current = queue.shift();
    if (!current) continue;
    for (const candidate of items) {
      if (seen.has(candidate.id) || !related(current, candidate)) continue;
      seen.add(candidate.id);
      queue.push(candidate);
      matching.push(candidate);
    }
  }
  matching.sort((left: TodoWorkspaceItem, right: TodoWorkspaceItem) => compareTodoRecent(left, right));
  return [todo].concat(matching.filter((item: TodoWorkspaceItem) => item.id !== todo.id));
}

function compareTodoRecent(left: TodoWorkspaceItem, right: TodoWorkspaceItem): number {
  const a = left.updatedAt || left.createdAt;
  const b = right.updatedAt || right.createdAt;
  if (a === b) return left.id.localeCompare(right.id);
  return a < b ? 1 : -1;
}

function statusRank(value: string): number {
  const index = TODO_STATUS_ORDER.indexOf(value);
  return index < 0 ? TODO_STATUS_ORDER.length : index;
}

function groupLead(items: TodoWorkspaceItem[], sessions: ResourceRow[]): TodoWorkspaceItem[] {
  return items.slice().sort((left: TodoWorkspaceItem, right: TodoWorkspaceItem) => {
    const byState = statusRank(todoWorkspaceStatus(left, sessions)) - statusRank(todoWorkspaceStatus(right, sessions));
    return byState || compareTodoRecent(left, right);
  });
}

function managerQuery(managerId: string): string { return managerId ? '?manager=' + segment(managerId) : ''; }

function pathPart(value: string): string { return segment(value); }

function updateRecord(todo: TodoWorkspaceItem, data: Record<string, Object>, manager: string): TodoWorkspaceItem {
  const merged = Wire.payload([], []);
  Object.keys(todo.data()).forEach((key: string) => { merged[key] = todo.data()[key]; });
  Object.keys(data).forEach((key: string) => { merged[key] = data[key]; });
  if (!Wire.text(merged, 'id')) merged['id'] = todo.id;
  if (!Wire.text(merged, 'manager')) merged['manager'] = manager;
  return new TodoWorkspaceItem(merged, manager);
}

@ObservedV2
export class TodoWorkspace {
  @Trace items: TodoWorkspaceItem[] = [];
  @Trace sessions: ResourceRow[] = [];
  @Trace machines: ResourceRow[] = [];
  @Trace conversations: ResourceRow[] = [];
  @Trace search: string = '';
  @Trace statusFilter: string = 'all';
  @Trace groupBy: string = 'status';
  @Trace hubCounts: TodoHubCounts | undefined = undefined;
  @Trace hubChoice: string = 'all';
  @Trace hubUnfolded: string[] = [];
  @Trace dueFilter:string = 'all';
  @Trace order:string = 'recent';
  @Trace selected: TodoWorkspaceItem = new TodoWorkspaceItem();
  @Trace loading: boolean = false;
  @Trace saving: boolean = false;
  @Trace error: string = '';
  @Trace writeState: string = '';
  @Trace titleDraft: string = '';
  @Trace notesDraft: string = '';
  @Trace draftReady: boolean = false;
  @Trace targetGone: boolean = false;
  @Trace editorOpen: boolean = false;
  @Trace selectedAgentKey: string = '';
  @Trace selectedSpaceKey: string = '';
  @Trace projects: ResourceRow[] = [];

  private rootClient: ClientModel | undefined;
  private ownerClient: ClientModel | undefined;
  private boundRepo: HubRepository | undefined;
  private active: boolean = false;
  private revision: number = 0;
  private listRequestRevision: number = 0;
  private latestListReadRevision: number = 0;
  private latestListRead: Promise<void> | undefined;
  private rootGeneration: number = 0;
  private rootScope: string = '';
  private ownerGeneration: number = 0;
  private ownerScope: string = '';
  private groupId: string = '';
  private managerId: string = '';
  private draftWrites: Promise<void> = Promise.resolve();
  private initialRow: ResourceRow | undefined;
  private listMode: boolean = true;

  async open(client: ClientModel, row?: ResourceRow, list: boolean = true): Promise<void> {
    const revision = ++this.revision, openListRevision = ++this.listRequestRevision;
    this.active = true; this.initialRow = row; this.listMode = list;
    this.rootClient = client;
    this.ownerClient = undefined;
    this.boundRepo = undefined;
    this.rootGeneration = client.connectionGeneration();
    this.rootScope = client.scope();
    this.groupId = client.profile.id;
    this.loading = true;
    this.saving = false;
    this.error = '';
    this.writeState = '';
    this.items = [];
    this.hubCounts = undefined;
    this.hubChoice = 'all';
    this.hubUnfolded = [];
    this.groupBy = 'status';
    this.statusFilter = 'all';
    this.search = '';
    this.dueFilter = 'all';
    this.order = 'recent';
    this.sessions = [];
    this.machines = [];
    this.conversations = [];
    this.projects = [];
    this.selected = new TodoWorkspaceItem();
    this.titleDraft = '';
    this.notesDraft = '';
    this.draftReady = false;
    this.targetGone = false;
    try {
      let owner = client;
      if (row && row.manager && row.manager !== client.profile.manager) owner = await client.viewFor(row);
      if (!this.loadCurrent(client, revision)) return;
      this.ownerClient = owner;
      this.ownerGeneration = owner.connectionGeneration();
      this.ownerScope = owner.scope();
      this.managerId = owner.profile.manager;
      this.boundRepo = owner.repo();
      this.syncSessions(owner);
      await this.loadView(owner, revision);
      if (!this.loadCurrent(client, revision)) return;
      const refreshRevision = this.listRequestRevision + 1;
      await this.refresh();
      if (!this.loadCurrent(client, revision)) return;
      let awaitedRevision = refreshRevision;
      while (this.listRequestRevision > awaitedRevision) {
        const latestRead = this.latestListRead, latestRevision = this.latestListReadRevision;
        if (!latestRead || latestRevision !== this.listRequestRevision) break;
        awaitedRevision = latestRevision;
        await latestRead;
        if (!this.loadCurrent(client, revision)) return;
      }
      if (this.listRequestRevision === refreshRevision) this.loading = false;
    } catch (failure) {
      if (this.loadCurrent(client, revision) && this.listRequestRevision === openListRevision) {
        this.error = this.errorCode(failure as Error);
        this.loading = false;
      }
    }
  }

  close(): void {
    this.active = false;
    this.revision++;
    this.listRequestRevision++;
    this.latestListReadRevision = 0;
    this.latestListRead = undefined;
    this.rootClient = undefined;
    this.ownerClient = undefined;
    this.boundRepo = undefined;
    this.saving = false;
    this.items = [];
    this.hubCounts = undefined;
    this.sessions = [];
    this.machines = [];
    this.conversations = [];
    this.projects = [];
    this.selected = new TodoWorkspaceItem();
    this.draftReady = false;
    this.targetGone = false;
  }

  update(client: ClientModel): void {
    if (!this.active || client !== this.rootClient) return;
    const owner = this.ownerClient || client;
    this.syncSessions(owner);
  }

  private syncSessions(client: ClientModel): void {
    this.sessions = client.sessions.filter((row: ResourceRow) => row.kind === 'session' && row.manager === client.profile.manager);
    this.machines = client.machines.filter((row: ResourceRow) => row.kind === 'machine' && row.manager === client.profile.manager);
    this.conversations = client.conversations.filter((row: ResourceRow) => row.manager === client.profile.manager);
    this.projects = client.knowledge.filter((row: ResourceRow) => row.kind === 'knowledge' && row.path === 'projects' && row.manager === client.profile.manager);
  }

  private async loadView(client: ClientModel, revision: number): Promise<void> {
    try {
      const saved = Wire.parse(await client.loadView('todo-workspace'));
      if (this.ownerClient !== client || !this.loadCurrent(this.rootClient, revision)) return;
      this.search = Wire.text(saved, 'search');
      this.statusFilter = ['all'].concat(TODO_STATES).includes(Wire.text(saved, 'status')) ? Wire.text(saved, 'status', 'all') : 'all';
      this.groupBy = ['status', 'project', 'thread', 'hub'].includes(Wire.text(saved, 'group')) ? Wire.text(saved, 'group', 'status') : 'status';
      this.hubUnfolded = Wire.strings(saved, 'hubUnfolded').filter((key: string) => ['someday', 'can_wait', 'done'].includes(key));
      const due=Wire.text(saved,'due'),order=Wire.text(saved,'order');
      this.dueFilter=TODO_DUE_FILTERS.includes(due) ? due : 'all'; this.order=TODO_LIST_ORDERS.includes(order) ? order : 'recent';
    } catch (_) { if (this.ownerClient === client && this.loadCurrent(this.rootClient, revision)) this.error = 'draft_restore_failed'; }
  }

  private saveViewLater(): void {
    const client = this.ownerClient;
    if (!client || !this.active) return;
    const owner = this.capture(undefined);
    if (!owner) return;
    const value = JSON.stringify(Wire.payload(['search', 'status', 'group', 'due', 'order', 'hubUnfolded'],
      [this.search, this.statusFilter, this.groupBy, this.dueFilter, this.order, this.hubUnfolded]));
    this.draftWrites = this.draftWrites.catch(() => {}).then(async () => {
      if (!this.ownsScope(owner)) return;
      await owner.client.saveView('todo-workspace', value);
    }).catch(() => { if (this.ownsScope(owner)) this.error = 'draft_save_failed'; });
  }

  setSearch(value: string): void { this.search = value; this.saveViewLater(); }
  setStatusFilter(value: string): void {
    if (!['all'].concat(TODO_STATES).includes(value)) return;
    this.statusFilter = value; this.saveViewLater();
  }
  setDueFilter(value:string):void {
    if (TODO_DUE_FILTERS.includes(value)) { this.dueFilter=value; this.saveViewLater(); }
  }
  setOrder(value:string):void {
    if (TODO_LIST_ORDERS.includes(value)) { this.order=value; this.saveViewLater(); }
  }
  setGroupBy(value: string): void {
    if (!['status', 'project', 'thread'].includes(value) && !(value === 'hub' && this.hubCounts)) return;
    this.groupBy = value; this.saveViewLater();
  }

  effectiveGroup(): string { return this.groupBy === 'hub' && !this.hubCounts ? 'status' : this.groupBy; }

  setHubChoice(choice: string): void {
    if (this.effectiveGroup() === 'hub' && TODO_HUB_CHOICES.includes(choice)) this.hubChoice = choice;
  }

  toggleHubFold(key: string): void {
    if (!['someday', 'can_wait', 'done'].includes(key)) return;
    this.hubUnfolded = this.hubUnfolded.includes(key) ? this.hubUnfolded.filter((value: string) => value !== key) : this.hubUnfolded.concat([key]);
    this.saveViewLater();
  }

  private capture(todo: TodoWorkspaceItem | undefined): TodoOwnerSnapshot | undefined {
    const root = this.rootClient, client = this.ownerClient, repo = this.boundRepo;
    if (!this.active || !root || !client || !repo || !root.connected || !client.connected) return undefined;
    if (root.connectionGeneration() !== this.rootGeneration || root.scope() !== this.rootScope ||
      client.connectionGeneration() !== this.ownerGeneration || client.scope() !== this.ownerScope) return undefined;
    try {
      if (root.repo() === undefined || client.repo() !== repo) return undefined;
    } catch (_) { return undefined; }
    if (todo && todo.manager && todo.manager !== this.managerId) return undefined;
    return { root: root, client: client, repo: repo, rootGeneration: this.rootGeneration, rootScope: this.rootScope,
      generation: this.ownerGeneration, scope: this.ownerScope, revision: this.revision, groupId: this.groupId,
      managerId: this.managerId, todoId: todo?.id || '', todoManagerId: todo?.manager || this.managerId };
  }

  private ownsScope(owner: TodoOwnerSnapshot): boolean {
    if (!this.active || this.rootClient !== owner.root || this.ownerClient !== owner.client ||
      owner.root.connectionGeneration() !== owner.rootGeneration || owner.root.scope() !== owner.rootScope ||
      owner.client.connectionGeneration() !== owner.generation || owner.client.scope() !== owner.scope ||
      owner.root.profile.id !== owner.groupId || !owner.root.connected || !owner.client.connected) return false;
    try { return owner.client.repo() === owner.repo && this.boundRepo === owner.repo; } catch (_) { return false; }
  }

  private ownsListRequest(owner: TodoOwnerSnapshot, revision: number): boolean {
    return this.listRequestRevision === revision && this.revision === owner.revision && this.ownsScope(owner);
  }

  private invalidateListReads(): void {
    this.listRequestRevision++;
    this.loading = false;
  }

  private rejectGoneTarget(todo: TodoWorkspaceItem): boolean {
    if (!todo.id || !this.targetGone || this.selected.id !== todo.id ||
      (this.selected.manager || this.managerId) !== (todo.manager || this.managerId)) return false;
    this.error = 'todo_gone';
    return true;
  }

  private owns(owner: TodoOwnerSnapshot, requireTodo: boolean = true): boolean {
    if (!this.ownsScope(owner) || this.revision !== owner.revision) return false;
    return !requireTodo || !owner.todoId || (this.selected.id === owner.todoId &&
      (this.selected.manager || owner.managerId) === owner.todoManagerId);
  }

  isSelected(todo: TodoWorkspaceItem): boolean {
    if (this.rejectGoneTarget(todo)) return false;
    const owner = this.capture(todo);
    return !!owner && this.selected.id === todo.id && (this.selected.manager || owner.managerId) === owner.todoManagerId;
  }

  private loadCurrent(client: ClientModel | undefined, revision: number): boolean {
    if (!client || client !== this.rootClient || !this.active || this.revision !== revision) return false;
    return client.connectionGeneration() === this.rootGeneration && client.scope() === this.rootScope;
  }

  private errorCode(error: Error): string {
    if (error instanceof HttpFailure) {
      if (error.resultPending) return 'todo_write_pending';
      if (error.resultUnknown) return 'todo_write_unknown';
      if (error.status === 401 || error.status === 403) return 'permission_denied';
      if (error.status === 404) return 'todo_gone';
      if (error.status === 409 || error.status === 410) return 'request_updated';
    }
    return error.message === 'offline' ? 'offline' : 'failed';
  }

  refresh(): Promise<void> {
    const revision = ++this.listRequestRevision, root = this.rootClient;
    if (!this.active || !root || !root.connected || root.scope() !== this.rootScope) {
      if (revision === this.listRequestRevision) { this.error = 'offline'; this.loading = false; }
      return Promise.resolve();
    }
    if (!this.boundRepo) return this.open(root, this.initialRow, this.listMode);
    const rootScope = root.scope(), rootGeneration = root.connectionGeneration();
    this.loading = true;
    const request = this.readList(revision, root, rootScope, rootGeneration);
    this.latestListReadRevision = revision;
    this.latestListRead = request;
    return request;
  }

  private async readList(revision: number, root: ClientModel, rootScope: string, rootGeneration: number): Promise<void> {
    let ownerSnapshot: TodoOwnerSnapshot | undefined;
    try {
      const previousOwner = this.ownerClient;
      let repositoryCurrent = false;
      if (previousOwner && previousOwner.connected && this.boundRepo) {
        try { repositoryCurrent = previousOwner.repo() === this.boundRepo; } catch (_) { repositoryCurrent = false; }
      }
      if (!previousOwner || !repositoryCurrent || rootGeneration !== this.rootGeneration ||
        previousOwner.connectionGeneration() !== this.ownerGeneration || previousOwner.scope() !== this.ownerScope) {
        const ref = new ResourceRow('owner', '', 'manager');
        ref.manager = this.managerId || this.initialRow?.manager || root.profile.manager;
        const next = await root.viewFor(ref);
        if (revision !== this.listRequestRevision || !this.active || this.rootClient !== root ||
          rootScope !== this.rootScope || !root.owns(rootGeneration, rootScope)) return;
        this.hubCounts = undefined;
        this.ownerClient = next;
        this.boundRepo = next.repo();
        this.ownerGeneration = next.connectionGeneration();
        this.ownerScope = next.scope();
        this.rootGeneration = rootGeneration;
        this.managerId = next.profile.manager;
        this.syncSessions(next);
      }
      const owner = this.capture(undefined);
      if (!owner) {
        if (revision === this.listRequestRevision) this.error = 'offline';
        return;
      }
      ownerSnapshot = owner;
      const result = await owner.repo.call('/api/v1/todos' + managerQuery(owner.managerId));
      if (!this.ownsListRequest(owner, revision)) return;
      const items = Wire.rows(result, 'todos').map((item: Record<string, Object>) => new TodoWorkspaceItem(item, owner.managerId));
      this.hubCounts = owner.client.features.includes('todo_view') ? TodoHubCounts.read(result) : undefined;
      this.items = items;
      this.syncSessions(owner.client);
      if (this.selected.id) {
        const selectedId = this.selected.id, selectedManager = this.selected.manager || owner.managerId;
        const selected = items.find((item: TodoWorkspaceItem) => item.id === selectedId && (item.manager || owner.managerId) === selectedManager);
        if (selected) {
          this.selected = selected;
          this.targetGone = false;
        } else this.targetGone = true;
      }
      this.error = this.targetGone ? 'todo_gone' : '';
      await this.resumeInitialDetail(owner, revision, items);
    } catch (failure) {
      const currentOwner = ownerSnapshot ? this.ownsScope(ownerSnapshot) :
        this.active && this.rootClient === root && root.owns(rootGeneration, rootScope) && root.scope() === this.rootScope;
      if (revision === this.listRequestRevision && currentOwner)
        this.error = ownerSnapshot ? this.errorCode(failure as Error) : 'offline';
    } finally {
      if (revision === this.listRequestRevision && this.rootClient === root) this.loading = false;
    }
  }

  private async resumeInitialDetail(owner: TodoOwnerSnapshot, revision: number, items: TodoWorkspaceItem[]): Promise<void> {
    const initial = this.initialRow;
    if (this.listMode || !initial || this.editorOpen || !this.ownsListRequest(owner, revision)) return;
    const manager = initial.manager || owner.managerId;
    const target = items.find((item: TodoWorkspaceItem) => item.id === initial.id && (item.manager || owner.managerId) === manager);
    if (!target) {
      if (!this.selected.id || this.selected.id === initial.id) {
        this.targetGone = true;
        this.error = 'todo_gone';
      }
      return;
    }
    this.targetGone = false;
    if (this.selected.id) {
      const selectedManager = this.selected.manager || owner.managerId;
      if (this.selected.id !== target.id || selectedManager !== manager || this.draftReady) return;
    }
    await this.openRow(target);
  }

  async openRow(todo: TodoWorkspaceItem): Promise<void> {
    if (this.rejectGoneTarget(todo)) return;
    const owner = this.capture(undefined);
    if (!owner || !todo.id || todo.manager && todo.manager !== owner.managerId) { this.error = 'offline'; return; }
    this.revision++;
    this.selected = todo;
    this.targetGone = false;
    this.titleDraft = todo.title;
    this.notesDraft = todo.notes;
    this.draftReady = false;
    this.error = '';
    const snapshot = this.capture(todo);
    if (!snapshot) return;
    try {
      const saved = Wire.parse(await snapshot.client.loadView(this.draftStorageKey(snapshot, todo)));
      if (!this.owns(snapshot)) return;
      this.titleDraft = Wire.text(saved, 'title', todo.title);
      this.notesDraft = Wire.text(saved, 'notes', todo.notes);
      this.draftReady = true;
    } catch (_) { if (this.owns(snapshot)) { this.draftReady = true; this.error = 'draft_restore_failed'; } }
  }

  startCreate(): void {
    const current = this.capture(undefined);
    if (!current) { this.error = 'offline'; return; }
    this.revision++;
    const snapshot = this.capture(undefined);
    if (!snapshot) { this.error = 'offline'; return; }
    this.selected = new TodoWorkspaceItem(Wire.payload(['manager'], [snapshot.managerId]), snapshot.managerId);
    this.targetGone = false;
    this.titleDraft = '';
    this.notesDraft = '';
    this.draftReady = true;
    this.editorOpen = true;
    this.writeState = '';
    this.restoreDraft(snapshot, this.selected).catch(() => undefined);
  }

  beginEdit(todo: TodoWorkspaceItem): void {
    this.openRow(todo).then(() => { if (this.isSelected(todo)) this.editorOpen = true; });
  }

  closeEditor(): void { this.editorOpen = false; }

  setTitleDraft(value: string): void { this.titleDraft = value; this.persistSelectedDraft(); }
  setNotesDraft(value: string): void { this.notesDraft = value; this.persistSelectedDraft(); }

  private draftStorageKey(owner: TodoOwnerSnapshot, todo: TodoWorkspaceItem): string {
    return 'todo:draft:' + JSON.stringify([owner.groupId, owner.managerId, todo.id || 'new']);
  }

  private async restoreDraft(owner: TodoOwnerSnapshot, todo: TodoWorkspaceItem): Promise<void> {
    try {
      const saved = Wire.parse(await owner.client.loadView(this.draftStorageKey(owner, todo)));
      if (!this.owns(owner, false)) return;
      this.titleDraft = Wire.text(saved, 'title', this.titleDraft);
      this.notesDraft = Wire.text(saved, 'notes', this.notesDraft);
      this.draftReady = true;
    } catch (_) { if (this.owns(owner, false)) { this.draftReady = true; this.error = 'draft_restore_failed'; } }
  }

  private persistSelectedDraft(): void {
    const root = this.rootClient, client = this.ownerClient;
    if (!this.draftReady || !this.active || !root || !client || root.scope() !== this.rootScope || client.scope() !== this.ownerScope) return;
    const rootScope = this.rootScope, scope = this.ownerScope, group = this.groupId, key = 'todo:draft:' + JSON.stringify([this.groupId, this.managerId, this.selected.id || 'new']);
    const value = JSON.stringify(Wire.payload(['title', 'notes'], [this.titleDraft, this.notesDraft])), epoch = client.viewRevision(key);
    const current = (): boolean => root.scope() === rootScope && client.scope() === scope && root.profile.id === group;
    this.draftWrites = this.draftWrites.catch(() => {}).then(async () => { if (current()) await client.saveView(key, value, epoch); })
      .catch(() => { if (current()) this.error = 'draft_save_failed'; });
  }

  private async clearDraft(owner: TodoOwnerSnapshot, todo: TodoWorkspaceItem, title: string, notes: string): Promise<void> {
    if (!this.ownsScope(owner)) return;
    const key = this.draftStorageKey(owner, todo);
    const saved = Wire.parse(await owner.client.loadView(key));
    if (!this.ownsScope(owner)) return;
    if (Wire.text(saved, 'title') === title && Wire.text(saved, 'notes') === notes) await owner.client.saveView(key, '');
  }

  private async write(path: string, method: string, body: Record<string, Object>, todo: TodoWorkspaceItem | undefined, listRow:boolean=false): Promise<Record<string, Object> | undefined> {
    if (todo && this.rejectGoneTarget(todo)) return undefined;
    const owner = this.capture(todo);
    if (!owner) { this.error = 'offline'; return undefined; }
    const admit=():boolean => this.owns(owner,!!todo && !listRow) && (!listRow || !!todo && this.items.some((item:TodoWorkspaceItem) =>
      item.id === todo.id && (item.manager || owner.managerId) === owner.todoManagerId && item.isDone() === todo.isDone()));
    if (listRow && (this.saving || !todo || !this.items.includes(todo) || !admit())) return undefined;
    if (owner.client.operationBlocked(path)) { this.error = 'todo_write_pending'; return undefined; }
    this.saving = true;
    this.writeState = 'sending';
    this.error = '';
    try {
      const result = listRow ? await owner.repo.call(path,method,body,'',admit) : await owner.repo.call(path,method,body);
      if (!this.owns(owner, !!todo && !listRow)) return undefined;
      if (listRow && todo && !this.items.some((item:TodoWorkspaceItem) => item.id === todo.id && (item.manager || owner.managerId) === owner.todoManagerId)) {
        this.writeState=''; return undefined;
      }
      this.invalidateListReads();
      this.writeState = 'done';
      return result;
    } catch (failure) {
      if (!this.owns(owner, !!todo && !listRow)) return undefined;
      this.writeState = failure instanceof HttpFailure && failure.resultPending ? 'pending' :
        failure instanceof HttpFailure && failure.resultUnknown ? 'unknown' : 'failed';
      this.error = this.errorCode(failure as Error);
      return undefined;
    } finally { if (this.owns(owner, !!todo && !listRow)) this.saving = false; }
  }

  private upsert(data: Record<string, Object>, manager: string): TodoWorkspaceItem {
    const updated = new TodoWorkspaceItem(data, manager);
    const index = this.items.findIndex((item: TodoWorkspaceItem) => item.id === updated.id);
    const next = this.items.slice();
    if (index >= 0) next[index] = updated; else next.unshift(updated);
    this.items = next;
    if (this.selected.id === updated.id) this.selected = updated;
    this.refreshHubCountsAfterMutation();
    return updated;
  }

  private replace(todo: TodoWorkspaceItem): void {
    const next = this.items.slice();
    const index = next.findIndex((item: TodoWorkspaceItem) => item.id === todo.id);
    if (index >= 0) next[index] = todo; else next.unshift(todo);
    this.items = next;
    if (this.selected.id === todo.id) this.selected = todo;
    this.refreshHubCountsAfterMutation();
  }

  private refreshHubCountsAfterMutation(): void {
    // Mutation acknowledgements contain a row or an OK, never a count snapshot.
    if (!this.hubCounts) return;
    this.hubCounts = undefined;
    this.refresh().catch(() => undefined);
  }

  async save(): Promise<boolean> {
    if (this.selected.id && this.rejectGoneTarget(this.selected)) return false;
    const title = this.titleDraft.trim();
    if (!title) { this.error = 'todo_title_required'; return false; }
    const todo = this.selected, manager = this.managerId;
    const creating = !todo.id;
    const owner = this.capture(creating ? undefined : todo);
    if (!owner) { this.error = 'offline'; return false; }
    const path = '/api/v1/todos' + (creating ? '' : '/' + pathPart(todo.id)) + managerQuery(manager);
    const body = Wire.payload(['title', 'notes'], [title, this.notesDraft]);
    const result = await this.write(path, creating ? 'POST' : 'PATCH', body, creating ? undefined : todo);
    if (!result || !this.owns(owner, !creating)) return false;
    const data = Wire.object(result, 'todo');
    if (!Wire.text(data, 'id')) { this.error = 'failed'; return false; }
    const updated = this.upsert(data, manager);
    this.selected = updated;
    this.editorOpen = false;
    this.titleDraft = updated.title;
    this.notesDraft = updated.notes;
    this.draftReady = true;
    try { await this.clearDraft(owner, todo, title, this.notesDraft); } catch (_) { this.error = 'draft_save_failed'; }
    return true;
  }

  async setDone(todo: TodoWorkspaceItem, done: boolean, listRow:boolean=false): Promise<boolean> {
    if (!todo.id || this.rejectGoneTarget(todo)) return false;
    const owner = this.capture(todo);
    if (!owner) { this.error = 'offline'; return false; }
    const path = '/api/v1/todos/' + pathPart(todo.id) + managerQuery(todo.manager || this.managerId);
    const result = await this.write(path, 'PATCH', Wire.payload(['done'], [done]), todo,listRow);
    if (!result || !this.owns(owner,!listRow)) return false;
    const data = Wire.object(result, 'todo');
    if (Wire.text(data,'id') !== todo.id || Wire.text(data,'manager') && Wire.text(data,'manager') !== (todo.manager || owner.managerId)) { this.error = 'failed'; return false; }
    this.upsert(data, todo.manager || this.managerId);
    return true;
  }

  async remove(todo: TodoWorkspaceItem): Promise<boolean> {
    if (this.rejectGoneTarget(todo)) return false;
    const owner = this.capture(todo);
    if (!owner) { this.error = 'offline'; return false; }
    const path = '/api/v1/todos/' + pathPart(todo.id) + managerQuery(todo.manager || this.managerId);
    const result = await this.write(path, 'DELETE', Wire.payload([], []), todo);
    if (!result || !this.owns(owner)) return false;
    this.items = this.items.filter((item: TodoWorkspaceItem) => item.id !== todo.id);
    if (this.selected.id === todo.id) this.selected = new TodoWorkspaceItem();
    this.targetGone = false;
    if (this.initialRow?.id === todo.id && (!this.initialRow.manager || this.initialRow.manager === (todo.manager || this.managerId)))
      this.initialRow = undefined;
    this.refreshHubCountsAfterMutation();
    return true;
  }

  async linkAgent(todo: TodoWorkspaceItem, session: ResourceRow): Promise<boolean> {
    if (this.rejectGoneTarget(todo)) return false;
    const owner = this.capture(todo);
    if (!owner) { this.error = 'offline'; return false; }
    if (todo.isDone()) { this.error = 'request_updated'; return false; }
    const machine = session.machine || Wire.text(session.data(), 'machine_id');
    const sessionId = session.session || Wire.text(session.data(), 'session_id') || session.id;
    if (!this.eligibleMachine(machine) || !machine || !sessionId) { this.error = 'todo_agent_unavailable'; return false; }
    const path = '/api/v1/todos/' + pathPart(todo.id) + '/agents/' + pathPart(machine) + '/' + pathPart(sessionId) + managerQuery(todo.manager || this.managerId);
    const result = await this.write(path, 'PUT', Wire.payload([], []), todo);
    if (!result || !this.owns(owner)) return false;
    const data = session.data();
    const agent = new TodoWorkspaceAgent(Wire.payload(['machine_id', 'session_id', 'title', 'cwd', 'agent', 'source', 'linked_at'],
      [machine, sessionId, session.title, Wire.text(data, 'cwd', session.path), Wire.text(data, 'agent'), 'manual', new Date().toISOString()]));
    const next = updateRecord(todo, Wire.payload(['agents'], [todo.agents.filter((item: TodoWorkspaceAgent) =>
      !(item.machineId === machine && item.sessionId === sessionId)).concat([agent]).map((item: TodoWorkspaceAgent) => Wire.parse(item.raw))]), todo.manager || this.managerId);
    this.replace(next);
    return true;
  }

  async unlinkAgent(todo: TodoWorkspaceItem, agent: TodoWorkspaceAgent): Promise<boolean> {
    if (this.rejectGoneTarget(todo)) return false;
    const owner = this.capture(todo);
    if (!owner || !agent.isActive()) { this.error = 'request_updated'; return false; }
    const path = '/api/v1/todos/' + pathPart(todo.id) + '/agents/' + pathPart(agent.machineId) + '/' + pathPart(agent.sessionId) + managerQuery(todo.manager || this.managerId);
    const result = await this.write(path, 'DELETE', Wire.payload([], []), todo);
    if (!result || !this.owns(owner)) return false;
    const now = new Date().toISOString();
    const agents = todo.agents.map((item: TodoWorkspaceAgent) => {
      const data = Wire.parse(item.raw);
      if (item.machineId === agent.machineId && item.sessionId === agent.sessionId) {
        data['released_at'] = now; data['dismissed_at'] = now;
      }
      return data;
    });
    this.replace(updateRecord(todo, Wire.payload(['agents'], [agents]), todo.manager || this.managerId));
    return true;
  }

  async linkSpace(todo: TodoWorkspaceItem, choice: TodoWorkspaceSpaceChoice): Promise<boolean> {
    if (this.rejectGoneTarget(todo)) return false;
    const owner = this.capture(todo);
    if (!owner) { this.error = 'offline'; return false; }
    if (todo.isDone()) { this.error = 'request_updated'; return false; }
    if (!this.eligibleMachine(choice.machineId) || !choice.space) { this.error = 'todo_project_unavailable'; return false; }
    const path = '/api/v1/todos/' + pathPart(todo.id) + '/spaces/' + pathPart(choice.machineId) + '/' + pathPart(choice.space) + managerQuery(todo.manager || this.managerId);
    const result = await this.write(path, 'PUT', Wire.payload([], []), todo);
    if (!result || !this.owns(owner)) return false;
    const spaces = todo.spaces.filter((item: TodoWorkspaceSpace) => !(item.machineId === choice.machineId && item.space === choice.space))
      .map((item: TodoWorkspaceSpace): Record<string, Object> => Wire.payload(['machine_id', 'space', 'linked_at'], [item.machineId, item.space, item.linkedAt]));
    spaces.push(Wire.payload(['machine_id', 'space', 'linked_at'], [choice.machineId, choice.space, new Date().toISOString()]));
    this.replace(updateRecord(todo, Wire.payload(['spaces'], [spaces]), todo.manager || this.managerId));
    return true;
  }

  async unlinkSpace(todo: TodoWorkspaceItem, space: TodoWorkspaceSpace): Promise<boolean> {
    if (this.rejectGoneTarget(todo)) return false;
    const owner = this.capture(todo);
    if (!owner || !todo.spaces.some((item: TodoWorkspaceSpace) => item.machineId === space.machineId && item.space === space.space)) {
      this.error = 'request_updated'; return false;
    }
    const path = '/api/v1/todos/' + pathPart(todo.id) + '/spaces/' + pathPart(space.machineId) + '/' + pathPart(space.space) + managerQuery(todo.manager || this.managerId);
    const result = await this.write(path, 'DELETE', Wire.payload([], []), todo);
    if (!result || !this.owns(owner)) return false;
    const spaces = todo.spaces.filter((item: TodoWorkspaceSpace) => !(item.machineId === space.machineId && item.space === space.space))
      .map((item: TodoWorkspaceSpace): Record<string, Object> => Wire.payload(['machine_id', 'space', 'linked_at'], [item.machineId, item.space, item.linkedAt]));
    const agents = todo.agents.filter((item: TodoWorkspaceAgent) => !(item.source === 'space' && item.via === space.machineId + '/' + space.space))
      .map((item: TodoWorkspaceAgent): Record<string, Object> => Wire.parse(item.raw));
    this.replace(updateRecord(todo, Wire.payload(['spaces', 'agents'], [spaces, agents]), todo.manager || this.managerId));
    return true;
  }

  private eligibleMachine(machineId: string): boolean {
    const machine = this.machines.find((row: ResourceRow) => row.id === machineId && row.manager === this.managerId);
    if (!machine) return false;
    const data = machine.data();
    const trust = Wire.text(data, 'trust');
    return Wire.bool(data, 'online', false) && trust !== 'pending' && trust !== 'removed' && trust !== 'revoked' &&
      !Wire.bool(data, 'pending_confirm') && !Wire.bool(data, 'pending');
  }

  candidateSessions(todo: TodoWorkspaceItem = this.selected): ResourceRow[] {
    return this.sessions.filter((row: ResourceRow) => {
      const machine = row.machine || Wire.text(row.data(), 'machine_id');
      const session = row.session || Wire.text(row.data(), 'session_id') || row.id;
      return !Wire.bool(row.data(), 'assistant') && !!machine && !!session && this.eligibleMachine(machine) &&
        !todo.agents.some((agent: TodoWorkspaceAgent) => agent.machineId === machine && agent.sessionId === session && agent.isActive());
    });
  }

  spaceChoices(todo: TodoWorkspaceItem = this.selected): TodoWorkspaceSpaceChoice[] {
    const choices: TodoWorkspaceSpaceChoice[] = [];
    for (const session of this.sessions) {
      const machineId = session.machine || Wire.text(session.data(), 'machine_id');
      const machine = this.machines.find((row: ResourceRow) => row.id === machineId);
      const data = session.data();
      const space = Wire.text(data, 'space_name', Wire.text(data, 'space_id'));
      if (!machineId || !space || !this.eligibleMachine(machineId)) continue;
      if (todo.spaces.some((item: TodoWorkspaceSpace) => item.machineId === machineId && item.space === space)) continue;
      const label = (machine?.title || machineId) + ' · ' + space;
      if (!choices.some((item: TodoWorkspaceSpaceChoice) => item.machineId === machineId && item.space === space))
        choices.push(new TodoWorkspaceSpaceChoice(machineId, space, label));
    }
    return choices;
  }

  machineName(machineId: string): string {
    return this.machines.find((row: ResourceRow) => row.id === machineId)?.title || machineId;
  }

  agentSession(agent: TodoWorkspaceAgent): ResourceRow {
    const existing = todoAgentSession(this.selected.activeAgents(), this.sessions, agent);
    if (existing) return existing;
    const row = new ResourceRow(agent.sessionId, agent.title, 'session');
    row.manager = this.managerId; row.machine = agent.machineId; row.session = agent.sessionId; row.path = agent.cwd;
    row.raw = JSON.stringify(Wire.payload(['id', 'session_id', 'machine_id', 'manager', 'title', 'cwd', 'agent', 'state', 'status', 'start_error'],
      [agent.sessionId, agent.sessionId, agent.machineId, this.managerId, agent.title, agent.cwd, agent.agent, 'offline', 'offline', agent.startError]));
    return row;
  }

  agentState(agent: TodoWorkspaceAgent): string {
    const session = todoAgentSession(this.selected.activeAgents(), this.sessions, agent);
    if (!agent.isActive()) return 'followup';
    if (!session) return 'followup';
    return session.state || Wire.text(session.data(), 'state', Wire.text(session.data(), 'status')) || 'followup';
  }

  state(todo: TodoWorkspaceItem): string { return todoWorkspaceStatus(todo, this.sessions); }

  stateLabelKey(todo: TodoWorkspaceItem): string { return 'todo_state_' + this.state(todo); }

  projectTitle(todo: TodoWorkspaceItem): string {
    if (!todo.project) return '';
    const project = this.projects.find((row: ResourceRow) => row.id === todo.project);
    return project?.title || todo.project;
  }

  threadTitle(todo: TodoWorkspaceItem): string { return Wire.text(todo.thread, 'title') || Wire.text(todo.thread, 'thread_id'); }

  searchMatches(todo: TodoWorkspaceItem, query: string): boolean {
    const needle = query.trim().toLocaleLowerCase();
    if (!needle) return true;
    const values: string[] = [todo.title, todo.notes, todo.project, this.projectTitle(todo),
      Wire.text(todo.thread, 'title'), Wire.text(todo.source, 'channel')];
    todo.agents.forEach((agent: TodoWorkspaceAgent) => values.push(agent.title, agent.cwd));
    todo.spaces.forEach((space: TodoWorkspaceSpace) => values.push(space.space, this.machineName(space.machineId)));
    return values.some((value: string) => value.toLocaleLowerCase().includes(needle));
  }

  private groupsFor(visible: TodoWorkspaceItem[]): TodoWorkspaceGroup[] {
    const remaining: Set<string> = new Set(visible.map((item: TodoWorkspaceItem) => item.id));
    const groups: TodoWorkspaceGroup[] = [];
    for (const todo of visible) {
      if (!remaining.has(todo.id)) continue;
      const members = relatedTodoMembers(visible, todo).filter((item: TodoWorkspaceItem) => remaining.has(item.id));
      const sorted = groupLead(members, this.sessions);
      sorted.forEach((item: TodoWorkspaceItem) => remaining.delete(item.id));
      groups.push(new TodoWorkspaceGroup(sorted.map((item: TodoWorkspaceItem) => item.id).sort().join('|'), '',
        this.state(sorted[0] || todo), sorted));
    }
    return groups;
  }

  hubSection(item: TodoWorkspaceItem, now: number): string {
    if (item.hubView) return item.hubView.section;
    const local = this.state(item);
    if (local === 'needs_you' || local === 'working') return local;
    if (local === 'checking') return 'just_finished';
    if (local === 'done') {
      const closed = Date.parse(item.doneAt), seen = Date.parse(Wire.text(item.data(), 'seen_at'));
      return Number.isFinite(closed) && now - closed < 86400000 && !(seen >= closed) ? 'just_finished' : 'done';
    }
    return local === 'followup' && (item.prUrl || Wire.text(item.delivery, 'state') === 'pr') ? 'waiting' : 'todo';
  }

  hubCalm(): boolean {
    const counts = this.hubCounts;
    if (!counts || !counts.sections.get('todo')) return false;
    // A plain list of reminders needs neither the section header nor the bar.
    // Keep Done reachable on a device that also holds older, closed tasks.
    return TODO_HUB_SECTIONS.every((key: string) => key === 'todo' || counts.sections.get(key) === 0);
  }

  private hubSections(visible: TodoWorkspaceItem[], now: number): TodoWorkspaceSection[] {
    const grouped = new Map<string, TodoWorkspaceGroup[]>();
    const open = new Set<string>();
    this.items.forEach((item: TodoWorkspaceItem) => {
      if (item.hubView ? item.hubView.phase !== 'done' : !item.isDone() || !!item.startError) open.add(JSON.stringify([item.manager, item.id]));
    });
    const choice = this.search.trim() || this.hubCalm() ? 'all' : this.hubChoice;
    const fallbackRanks = new Map<string, number>();
    const nextRank = new Map<string, number>();
    for (const item of visible.slice().sort(compareTodoRecent)) {
      const identity = JSON.stringify([item.manager, item.id]);
      if (item.partOf && item.partOf !== item.id && open.has(JSON.stringify([item.manager, item.partOf]))) continue;
      const key = this.hubSection(item, now);
      if (!todoHubChoiceIncludes(choice, key)) continue;
      if (!item.hubView) {
        const group = JSON.stringify([item.manager, key]);
        const rank = nextRank.get(group) || 0;
        fallbackRanks.set(identity, rank);
        nextRank.set(group, rank + 1);
      }
      if (!grouped.has(key)) grouped.set(key, []);
      grouped.get(key)?.push(new TodoWorkspaceGroup(identity, '', key, [item]));
    }
    const rank = (group: TodoWorkspaceGroup): number => group.lead.hubView?.rank ?? fallbackRanks.get(group.key) ?? 0;
    const order = (item: TodoWorkspaceItem): number => {
      if (this.order === 'due') return Number.isFinite(item.dueBy) ? item.dueBy : Number.MAX_SAFE_INTEGER;
      if (this.order === 'priority') return item.priority === 'high' ? 0 : item.priority === 'low' ? 2 : 1;
      return 0;
    };
    const sections: TodoWorkspaceSection[] = [];
    TODO_HUB_SECTIONS.forEach((key: string) => {
      const groups = grouped.get(key);
      if (!groups) return;
      groups.sort((a: TodoWorkspaceGroup, b: TodoWorkspaceGroup) => order(a.lead) - order(b.lead) || rank(a) - rank(b) ||
        a.lead.manager.localeCompare(b.lead.manager) || a.lead.id.localeCompare(b.lead.id));
      sections.push(new TodoWorkspaceSection(key, todoHubLabel(key), groups));
    });
    return sections;
  }

  hiddenWaiting(now:number, firstWeekday:number): number {
    if (this.dueFilter === 'all') return 0;
    const hidden = new Set<string>();
    this.items.forEach((item: TodoWorkspaceItem) => {
      if (this.searchMatches(item,this.search) && !todoMatchesDue(item,this.dueFilter,now,firstWeekday) &&
        this.hubSection(item,now) === 'needs_you') hidden.add(JSON.stringify([item.manager,item.id]));
    });
    return hidden.size;
  }

  sections(now:number=Date.now(),firstWeekday:number=1): TodoWorkspaceSection[] {
    let visible = this.items.filter((item: TodoWorkspaceItem) => this.searchMatches(item,this.search) && todoMatchesDue(item,this.dueFilter,now,firstWeekday));
    if (this.effectiveGroup() === 'hub') return this.hubSections(visible, now);
    if (this.statusFilter !== 'all') visible = visible.filter((item: TodoWorkspaceItem) => this.state(item) === this.statusFilter);
    const groups = this.groupsFor(visible);
    const grouped: Map<string, TodoWorkspaceGroup[]> = new Map();
    const titles: Map<string, string> = new Map();
    for (const group of groups) {
      let key = this.state(group.lead), title = this.stateLabelKey(group.lead);
      if (this.groupBy === 'project') {
        key = group.lead.project || 'unsorted'; title = group.lead.project ? this.projectTitle(group.lead) : 'todo_unsorted';
      } else if (this.groupBy === 'thread') {
        const chatId = Wire.text(group.lead.thread, 'chat_id'), threadId = Wire.text(group.lead.thread, 'thread_id');
        key = chatId && threadId ? JSON.stringify([Wire.text(group.lead.thread, 'channel'), chatId, threadId]) : 'unthreaded';
        title = this.threadTitle(group.lead) || 'todo_no_thread';
      }
      if (!grouped.has(key)) grouped.set(key, []);
      grouped.get(key)?.push(group);
      titles.set(key, title);
    }
    const order: string[] = Array.from(grouped.keys());
    if (this.effectiveGroup() === 'status') order.sort((left: string, right: string) => statusRank(left) - statusRank(right));
    else order.sort((left: string, right: string) => (titles.get(left) || left).localeCompare(titles.get(right) || right));
    return order.map((key: string) => {
      const rows = grouped.get(key) || [];
      const priority=(g:TodoWorkspaceGroup):number => g.members.reduce((best:number,item:TodoWorkspaceItem) => Math.min(best,item.priority === 'high' ? 0 : item.priority === 'low' ? 2 : 1),2);
      const due=(g:TodoWorkspaceGroup):number => g.members.reduce((earliest:number,item:TodoWorkspaceItem) => Number.isFinite(item.dueBy) ? Math.min(earliest,item.dueBy) : earliest,Number.MAX_SAFE_INTEGER);
      rows.sort((left:TodoWorkspaceGroup,right:TodoWorkspaceGroup) => (this.order === 'priority' ? priority(left)-priority(right) : 0) ||
        (this.order !== 'recent' ? due(left)-due(right) : 0) || compareTodoRecent(left.lead,right.lead));
      return new TodoWorkspaceSection(key, titles.get(key) || key, rows);
    });
  }

  relatedTodos(todo: TodoWorkspaceItem): TodoWorkspaceItem[] { return relatedTodoMembers(this.items, todo).slice(1); }

  todoResource(todo: TodoWorkspaceItem): ResourceRow {
    const row = ResourceRow.from(todo.data(), 'todo');
    row.manager = todo.manager || this.managerId;
    return row;
  }

  sourceResource(todo: TodoWorkspaceItem): ResourceRow | undefined {
    if (todo.origin) {
      const existing = this.conversations.find((row: ResourceRow) => row.id === todo.origin);
      if (existing) return existing;
      const row = new ResourceRow(todo.origin, '', 'conversation');
      row.manager = todo.manager || this.managerId; row.session = todo.origin;
      row.raw = JSON.stringify(Wire.payload(['id', 'manager', 'source_kind'], [todo.origin, row.manager, 'assistant']));
      return row;
    }
    const source = todo.source;
    const chatId = Wire.text(source, 'chat_id'), messageId = Wire.text(source, 'message_id'), channel = Wire.text(source, 'channel');
    if (!chatId || !channel) return undefined;
    const row = new ResourceRow(chatId, Wire.text(source, 'title'), 'todo_source');
    row.manager = todo.manager || this.managerId; row.session = messageId; row.detail = channel;
    row.raw = JSON.stringify(Wire.payload(['id', 'manager', 'channel', 'chat_id', 'message_id', 'anchor', 'title'],
      [chatId, row.manager, channel, chatId, messageId, Wire.text(source, 'anchor'), Wire.text(source, 'title')]));
    return row;
  }

  threadResource(todo: TodoWorkspaceItem): ResourceRow | undefined {
    const threadId = Wire.text(todo.thread, 'thread_id'), chatId = Wire.text(todo.thread, 'chat_id');
    if (!threadId || !chatId) return undefined;
    const row = new ResourceRow(chatId, this.threadTitle(todo), 'todo_thread');
    row.manager = todo.manager || this.managerId; row.session = threadId; row.detail = Wire.text(todo.thread, 'channel');
    row.raw = JSON.stringify(JSON.parse(JSON.stringify(todo.thread)));
    return row;
  }

  projectResource(todo: TodoWorkspaceItem): ResourceRow | undefined {
    if (!todo.project) return undefined;
    const row = new ResourceRow(todo.project, this.projectTitle(todo), 'knowledge');
    row.path = 'projects'; row.manager = todo.manager || this.managerId;
    row.raw = JSON.stringify(Wire.payload(['id', 'title', 'manager'], [todo.project, row.title, row.manager]));
    return row;
  }

  lineageResources(todo: TodoWorkspaceItem): ResourceRow[] {
    const rows: ResourceRow[] = [];
    for (const step of todo.lineage) {
      const id = Wire.text(step, 'todo_id');
      if (!id || id === todo.id) continue;
      const row = new ResourceRow(id, Wire.text(step, 'title'), 'todo');
      row.manager = todo.manager || this.managerId; row.detail = Wire.text(step, 'why');
      row.raw = JSON.stringify(Wire.payload(['id', 'title', 'manager', 'todo_id', 'session_id', 'machine', 'why', 'continues', 'superseded_by'],
        [id, row.title, row.manager, id, Wire.text(step, 'session_id'), Wire.text(step, 'machine'), row.detail,
          Wire.text(step, 'continues'), Wire.text(step, 'superseded_by')]));
      rows.push(row);
    }
    return rows;
  }
}
