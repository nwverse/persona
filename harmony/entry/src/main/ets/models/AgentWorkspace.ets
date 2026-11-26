import type { ClientModel } from './ClientModel';
import { SessionRelations, sessionRelations, unavailableSessionSources } from './SessionRelations';
import { ResourceRow, Wire } from './Wire';
import type { AgentListAdapter } from '../core/AgentListAdapter';
import type { AgentListInput, AgentListRow, AgentListPreferences, CoreAgentListGroup } from '../core/AgentListProjection';
import { CoreAgentListAdapter } from '../core/AgentListAdapter';

interface AgentRepository {
  call(path: string, method?: string, body?: Record<string, Object>, operationId?: string, admit?: () => boolean): Promise<Record<string, Object>>;
}

interface AgentOwnerSnapshot {
  root: ClientModel;
  rootScope: string;
  rootGeneration: number;
  revision: number;
  client: ClientModel;
  repo: AgentRepository;
  generation: number;
  scope: string;
  viewScope: string;
  manager: string;
  rowKey: string;
  rowTarget: string;
  actionEpoch: number;
}

@ObservedV2
export class AgentWorkspaceGroup {
  id: string = '';
  machineId: string = '';
  machineName: string = '';
  repositoryPath: string = '';
  repositoryName: string = '';
  worktreePath: string = '';
  worktreeName: string = '';
  branch: string = '';
  showMachine: boolean = false;
  showRepository: boolean = false;
  showWorktree: boolean = false;
  folded: boolean = false;
  parent: string = '';
  stateCounts: Record<string, Object> = {};
  spoken: string = '';
  totalRowCount: number = 0;
  memberIds: string[] = [];
  rows: ResourceRow[] = [];

  constructor(id: string, machineId: string, machineName: string, repositoryPath: string, repositoryName: string,
    worktreePath: string, worktreeName: string, branch: string, rows: ResourceRow[] = []) {
    this.id = id; this.machineId = machineId; this.machineName = machineName; this.repositoryPath = repositoryPath;
    this.repositoryName = repositoryName; this.worktreePath = worktreePath; this.worktreeName = worktreeName;
    this.branch = branch; this.rows = rows; this.totalRowCount = rows.length;
  }
}

/** The Needs you section above every grouping but By Status, which has its own (DESIGN_SYSTEM 3.4). */
export const NEEDS_GROUP: string = 'needs';
const STATE_COUNT_KEYS: string[] = ['needs_you', 'working', 'unread', 'idle'];

function segment(value: string): string { return encodeURIComponent(value); }
function rowSession(row: ResourceRow): string { return row.session || Wire.text(row.data(), 'session_id') || row.id; }
function rowMachine(row: ResourceRow): string { return row.machine || Wire.text(row.data(), 'machine_id'); }
function rowKey(row: ResourceRow): string { return JSON.stringify(row.group ? [row.group, row.kind, row.manager, rowMachine(row), rowSession(row)] : [row.kind, row.manager, rowMachine(row), rowSession(row)]); }
function rowSpaceKey(row: ResourceRow): string {
  const name = Wire.text(row.data(), 'space_name');
  return name ? rowMachine(row) + '/' + name : '';
}
function coreRowId(row: ResourceRow): string {
  return (row.group ? row.group + '|' : '') + (row.manager ? row.manager + '|' : '') + rowMachine(row) + '|' + rowSession(row);
}
function stringsFrom(value: Record<string, Object>, key: string, optional: boolean = false): string[] {
  if (!Wire.has(value, key) && optional) return [];
  const item = value[key];
  if (!Array.isArray(item)) throw new Error('invalid_preferences');
  const values = item as Object[];
  if (values.some((part: Object) => typeof part !== 'string')) throw new Error('invalid_preferences');
  return values.map((part: Object) => part as string);
}
function removeKey(values: string[], key: string): string[] { return values.filter((value: string) => value !== key); }
function toggleKey(values: string[], key: string): string[] {
  return values.includes(key) ? removeKey(values, key) : values.concat([key]);
}

class AgentPreferences {
  pinsSpaces: string[] = []; pinsSessions: string[] = [];
  mutesSpaces: string[] = []; mutesSessions: string[] = []; mutesAllowed: string[] = [];
}

/** Native Agents list state and the hub's pins, mutes, and read receipts. */
@ObservedV2
export class AgentWorkspace {
  @Trace groups: AgentWorkspaceGroup[] = [];
  /** The quick filters' counts, before the filter narrows the list; Needs you counts the section's rows. */
  @Trace counts: Record<string, number> = {};
  @Trace quickFilter: string = 'all';
  @Trace groupingMode: string = 'state';
  @Trace machineFilter: string = '';
  @Trace search: string = '';
  @Trace history: boolean = false;
  @Trace selectedKey: string = '';
  @Trace loading: boolean = false;
  @Trace busy: boolean = false;
  @Trace error: string = '';
  @Trace pinsSpaces: string[] = [];
  @Trace pinsSessions: string[] = [];
  @Trace mutesSpaces: string[] = [];
  @Trace mutesSessions: string[] = [];
  @Trace mutesAllowed: string[] = [];

  private client: ClientModel | undefined;
  private preferences: Map<string, AgentPreferences> = new Map();
  private owner: string = '';
  private scope: string = '';
  private viewScope: string = '';
  private revision: number = 0;
  private actionEpoch: number = 0;
  revokeRowActions(): void { this.actionEpoch++; this.busy = false; }
  relations: SessionRelations = new SessionRelations([]);
  private expandedFamilies = new Set<string>();
  foldFamilies(): boolean { return this.quickFilter === 'all' && !this.search.trim(); }
  foldedChildren(row: ResourceRow, graph: SessionRelations = this.relations): ResourceRow[] {
    return graph.children(row).filter(child => !this.isPinned(child));
  }
  familyExpanded(row: ResourceRow): boolean { return this.expandedFamilies.has(row.key()); }
  toggleFamily(row: ResourceRow): void {
    if (!this.foldFamilies() || !this.foldedChildren(row).length) return;
    if (!this.expandedFamilies.delete(row.key())) this.expandedFamilies.add(row.key());
    this.groups = this.groups.slice();
  }
  /** A group's rows with each opened family under its parent; Needs you lists parents only, so no child shows twice. */
  displayRows(group: AgentWorkspaceGroup): ResourceRow[] {
    if (group.id === NEEDS_GROUP || !this.foldFamilies() || !this.expandedFamilies.size) return group.rows;
    const rows: ResourceRow[] = [];
    for (const row of group.rows) {
      rows.push(row);
      if (this.familyExpanded(row)) rows.push(...this.foldedChildren(row));
    }
    return rows;
  }
  private sourceRows: ResourceRow[] = [];
  private latestSourceRows: ResourceRow[] = [];
  private externalSelection: ResourceRow | undefined;
  private historyRows: ResourceRow[] = [];
  private historyLoaded: boolean = false;
  private frozenGroups: AgentWorkspaceGroup[] | undefined;
  private readingDetail: boolean = false;
  private openingView: boolean = false;
  private freezeSuppressed: boolean = false;
  private historyRequest: number = 0;
  private persistTimer: number = -1;
  private adapter: AgentListAdapter;
  constructor(adapter?: AgentListAdapter) { this.adapter = adapter || new CoreAgentListAdapter(); }
  private folded: string[] = [];
  private expandedIdle: string[] = [];
  private expandedQuiet: string[] = [];
  private heldSession: string = '';
  historySupported(): boolean { return !!this.client?.directoryOwners().some((owner: ClientModel) => owner.features.includes('agent_session_archives')); }
  rowOnline(row: ResourceRow): boolean { return this.ownerFor(row)?.connected === true; }
  hasRows(): boolean { return this.groups.some((group: AgentWorkspaceGroup) => group.totalRowCount > 0); }
  /** No list has arrived yet, from the Hub or a launch snapshot: no rows means "not known yet", not "none". */
  awaitingSource(): boolean {
    return !this.history && !!this.client && !this.client.sessionsReady && !this.client.directoryReady;
  }
  /** Why an unknown list stays unknown: the owner's connection error, '' while it is still connecting. */
  sourceProblem(): string {
    if (!this.awaitingSource() || !this.client || this.client.connected || !this.client.error) return '';
    return this.client.error === 'offline' ? 'hub_unreachable' : this.client.error;
  }
  /** Each row on screen once, though Needs you repeats a row from its group. */
  visibleRows(): ResourceRow[] {
    const rows: ResourceRow[] = [], seen = new Set<string>();
    const add = (row: ResourceRow): void => { const key = rowKey(row); if (!seen.has(key)) { seen.add(key); rows.push(row); } };
    this.groups.forEach((group: AgentWorkspaceGroup) => { if (group.id !== NEEDS_GROUP) this.displayRows(group).forEach(add); });
    this.groups.forEach((group: AgentWorkspaceGroup) => { if (group.id === NEEDS_GROUP) group.rows.forEach(add); });
    return rows;
  }
  isSelected(row: ResourceRow): boolean { return this.selectedKey === rowKey(row); }
  rowMetadata(row: ResourceRow): string[] {
    const data = row.data(), machineId = rowMachine(row);
    const path = row.path || Wire.text(data, 'worktree') || Wire.text(data, 'repo_root') || Wire.text(data, 'cwd');
    const owner = this.ownerFor(row);
    const machine = machineId ? owner?.machineName(machineId) || machineId : '';
    const identity = this.client ? [this.client.groupName(row), row.manager].filter((value: string) => !!value).join(' · ') : '';
    const shortPath = path && this.client ? this.client.shortPath(path) : path;
    const state = row.state || Wire.text(data, 'status');
    const content = Wire.text(data, 'last_text') || row.detail;
    return [identity, machine, shortPath, state, content];
  }

  listMetadata(row: ResourceRow): string[] {
    const metadata = this.rowMetadata(row);
    const machineId = rowMachine(row);
    return [this.client?.groupName(row) || '', metadata[1] === machineId ? '' : metadata[1], metadata[3], metadata[4]];
  }

  isBoundTo(client: ClientModel): boolean {
    return this.client === client && this.scope === client.scope() && this.owner === client.profile.manager;
  }

  async open(client: ClientModel, readingDetail: boolean = false): Promise<void> {
    const revision = ++this.revision;
    this.client = client; this.scope = client.scope(); this.owner = client.profile.manager;
    this.viewScope = JSON.stringify([this.scope, this.owner]);
    this.openingView = true; this.loading = true; this.busy = false; this.error = '';
    this.externalSelection = undefined; this.groups = []; this.expandedFamilies.clear(); this.relations = new SessionRelations([]); this.sourceRows = []; this.latestSourceRows = []; this.historyRows = []; this.historyLoaded = false; this.frozenGroups = undefined;
    this.quickFilter = 'all'; this.groupingMode = 'state'; this.machineFilter = ''; this.search = ''; this.history = false; this.selectedKey = '';
    this.readingDetail = false; this.freezeSuppressed = false;
    this.preferences.clear(); this.pinsSpaces = []; this.pinsSessions = []; this.mutesSpaces = []; this.mutesSessions = []; this.mutesAllowed = [];
    try {
      const saved = Wire.parse(await client.loadView('agent-workspace'));
      if (!this.loadCurrent(client, revision)) return;
      this.quickFilter = ['all', 'needsYou', 'unread', 'working', 'backgroundWaiting'].includes(Wire.text(saved, 'quick')) ? Wire.text(saved, 'quick') :
        (Wire.text(saved, 'quick') === 'needs_you' ? 'needsYou' : 'all');
      this.groupingMode = ['state', 'machine', 'space'].includes(Wire.text(saved, 'grouping')) ? Wire.text(saved, 'grouping') : 'state';
      this.machineFilter = Wire.text(saved, 'machine'); this.search = Wire.text(saved, 'search');
      this.history = Wire.bool(saved, 'history');
      if (this.history && client.connected && !this.historySupported()) this.history = false;
      this.selectedKey = Wire.text(saved, 'selected');
      this.folded = Wire.strings(saved, 'folded'); this.expandedIdle = Wire.strings(saved, 'expanded_idle');
      this.expandedQuiet = Wire.strings(saved, 'expanded_quiet');
      this.latestSourceRows = this.history ? this.historyRows.slice() : this.rowsForClient(client);
      this.syncSelected(client);
      this.rebuild();
      this.setReadingDetail(readingDetail);
      if (client.connected) {
        await client.refreshDirectory();
        if (!this.loadCurrent(client, revision)) return;
        this.latestSourceRows = this.history ? this.historyRows.slice() : this.rowsForClient(client); this.rebuild();
        await this.refreshDirectoryPreferences(client, revision);
        if (!this.loadCurrent(client, revision)) return;
        if (this.history) await this.loadHistory(client, revision);
      }
    } catch (failure) {
      if (this.loadCurrent(client, revision)) this.fail(failure as Error);
    } finally {
      if (this.loadCurrent(client, revision)) { this.openingView = false; this.loading = false; this.rebuild(); }
    }
  }

  hide(): void {
    this.revision++; this.openingView = false; this.client = undefined; this.groups = []; this.expandedFamilies.clear(); this.relations = new SessionRelations([]); this.sourceRows = []; this.latestSourceRows = []; this.historyRows = []; this.historyLoaded = false;
    this.frozenGroups = undefined; this.readingDetail = false;
    if (this.persistTimer !== -1) clearTimeout(this.persistTimer);
    this.persistTimer = -1;
  }

  update(client: ClientModel, readingDetail: boolean = this.readingDetail): void {
    const nextScope = JSON.stringify([client.scope(), client.profile.manager]);
    if (this.client !== client || this.viewScope !== nextScope) {
      this.open(client, readingDetail).catch(() => undefined);
      return;
    }
    if (this.history && client.connected && !this.historySupported()) {
      this.history = false; this.latestSourceRows = this.rowsForClient(client); this.saveViewLater();
    }
    this.syncSelected(client);
    this.setReadingDetail(readingDetail);
    this.latestSourceRows = this.history ? this.historyRows.slice() : this.rowsForClient(client);
    this.rebuild();
  }

  async refresh(client: ClientModel): Promise<void> {
    const nextScope = JSON.stringify([client.scope(), client.profile.manager]);
    if (this.client !== client || this.viewScope !== nextScope) { await this.open(client, this.readingDetail); return; }
    this.update(client, this.readingDetail);
    const snapshot = this.capture(client);
    if (!snapshot) return;
    try {
      await client.refreshDirectory();
      if (!this.ownerActive(snapshot)) return;
      this.latestSourceRows = this.history ? this.historyRows.slice() : this.rowsForClient(client); this.rebuild();
      await this.refreshDirectoryPreferences(client, this.revision);
      if (this.history) await this.loadHistory(client, this.revision);
    } catch (failure) { if (this.ownerActive(snapshot)) this.fail(failure as Error); }
  }

  setReadingDetail(value: boolean): void {
    if (value === this.readingDetail) return;
    if (value) {
      if (this.readingDetail) return;
      this.heldSession = ''; this.rebuild();
      this.readingDetail = true;
      this.freezeSuppressed = false;
      if (this.sourceReady() || this.groups.length > 0) {
        this.frozenGroups = this.copyGroups(this.groups);
      }
    } else {
      this.readingDetail = false;
      this.heldSession = '';
      this.freezeSuppressed = false;
      this.frozenGroups = undefined;
      this.rebuild();
    }
  }

  setSearch(value: string): void {
    this.releaseFreeze(); this.search = value; this.rebuild(); this.refreezeAfterFilter(); this.saveViewLater();
  }

  setQuickFilter(value: string): void {
    if (!['all', 'needsYou', 'working', 'unread', 'backgroundWaiting'].includes(value)) return;
    this.releaseFreeze(); this.quickFilter = value; this.rebuild(); this.refreezeAfterFilter(); this.saveViewLater();
  }

  setGroupingMode(value: string): void {
    if (!['state', 'machine', 'space'].includes(value) || value === this.groupingMode) return;
    this.releaseFreeze(); this.groupingMode = value; this.rebuild(); this.refreezeAfterFilter(); this.saveViewLater();
  }

  toggleFold(id: string): void {
    if (this.groups.some((group) => group.parent === 'quiet' && group.id === id)) {
      this.expandedQuiet = this.expandedQuiet.includes(id) ? removeKey(this.expandedQuiet, id) : this.expandedQuiet.concat([id]);
    } else if (id === 'state/idle') {
      this.folded = this.folded.includes('!state/idle') ? removeKey(this.folded, '!state/idle') : this.folded.concat(['!state/idle']);

    } else {
      this.folded = this.folded.includes(id) ? removeKey(this.folded, id) : this.folded.concat([id]);
    }
    this.releaseFreeze();
    this.rebuild(); this.refreezeAfterFilter(); this.saveViewLater();
  }

  idleRowsExpanded(id: string): boolean {
    return this.expandedIdle.includes(this.idleRowsKey(id));
  }

  private idleRowsKey(id: string): string {
    return id.startsWith('machine/') ? 'machine-' + id.slice(8) : id;
  }

  toggleIdleRows(id: string): void {
    id = this.idleRowsKey(id);
    this.releaseFreeze();
    this.expandedIdle = this.expandedIdle.includes(id) ? removeKey(this.expandedIdle, id) : this.expandedIdle.concat([id]);
    this.rebuild(); this.refreezeAfterFilter(); this.saveViewLater();
  }

  async setMachineFilter(value: string): Promise<void> {
    this.releaseFreeze(); this.machineFilter = value; this.rebuild(); this.saveViewLater();
    if (this.history) {
      const client = this.client;
      if (client) await this.loadHistory(client, this.revision, true);
      else this.refreezeAfterFilter();
    } else {
      this.refreezeAfterFilter();
    }
  }

  async setHistory(value: boolean): Promise<void> {
    if (value && !this.historySupported()) return;
    this.releaseFreeze();
    this.historyRequest++; this.loading = false;
    this.history = value;
    this.error = '';
    this.latestSourceRows = value ? [] : this.rowsForClient(this.client);
    this.rebuild(); this.saveViewLater();
    if (value && this.client) await this.loadHistory(this.client, this.revision, true);
    else this.refreezeAfterFilter();
  }

  actionTicket(row: ResourceRow): () => boolean { const snapshot = this.captureRow(row, this.sessionPath(row)); return (): boolean => !!snapshot && this.active(snapshot); }
  forgetHub(group: string): void {
    this.historyRequest++; this.loading = false;
    const keep = (row: ResourceRow): boolean => row.group !== group;
    this.latestSourceRows = this.latestSourceRows.filter(keep); this.historyRows = this.historyRows.filter(keep);
    if (this.externalSelection?.group === group) { this.externalSelection = undefined; this.selectedKey = ''; }
    if (this.frozenGroups) this.frozenGroups = this.frozenGroups.map((source: AgentWorkspaceGroup) => { const copy = this.copyGroup(source); copy.rows = copy.rows.filter(keep); return copy; }).filter((source: AgentWorkspaceGroup) => source.rows.length > 0);
    this.preferences.clear(); this.rebuild();
  }
  selectExternal(row: ResourceRow): void { this.externalSelection = row; if (this.client) this.syncSelected(this.client); }

  selectRow(row: ResourceRow): void {
    if (row.kind !== 'session' || !this.groups.some((group: AgentWorkspaceGroup) =>
      this.displayRows(group).some((item: ResourceRow) => rowKey(item) === rowKey(row)))) return;
    this.externalSelection = row; this.selectedKey = rowKey(row);
    this.heldSession = row.state === 'unread' ? (row.group ? row.group + '|' : '') + (row.manager ? row.manager + '|' : '') +
      rowMachine(row) + '|' + rowSession(row) : '';
    this.saveViewLater();
  }

  private sessionPins(row: ResourceRow, pins: string[]): string[] {
    const key = this.sessionKey(row);
    return pins.filter(pin => pin === key || pin === (row.manager || row.group) + '|' + key);
  }
  isPinned(row: ResourceRow): boolean { return this.sessionPins(row, this.preferencesFor(row).pinsSessions).length > 0; }
  isSpacePinned(row: ResourceRow): boolean { const key = rowSpaceKey(row); return !!key && this.preferencesFor(row).pinsSpaces.includes(key); }
  isSpaceMuted(row: ResourceRow): boolean { const key = rowSpaceKey(row); return !!key && this.preferencesFor(row).mutesSpaces.includes(key); }
  isMuted(row: ResourceRow): boolean {
    const key = this.sessionKey(row), space = rowSpaceKey(row);
    const preferences = this.preferencesFor(row);
    return preferences.mutesSessions.includes(key) || (!preferences.mutesAllowed.includes(key) && !!space && preferences.mutesSpaces.includes(space));
  }

  canRename(row: ResourceRow): boolean {
    if (!this.canMutateSession(row)) return false;
    const data = row.data(), source = Wire.text(data, 'source_kind');
    if (!source) return true;
    return Wire.bool(Wire.object(data, 'capabilities'), 'terminal');
  }

  canClose(row: ResourceRow): boolean {
    if (!this.canMutateSession(row)) return false;
    const data = row.data(), source = Wire.text(data, 'source_kind'), capabilities = Wire.object(data, 'capabilities');
    if (Wire.has(capabilities, 'close')) return Wire.bool(capabilities, 'close');
    return !source || Wire.bool(capabilities, 'terminal');
  }

  canDelete(row: ResourceRow): boolean {
    if (!this.canMutateSession(row) || this.history) return false;
    const data = row.data();
    return !Wire.bool(data, 'assistant') && !Wire.bool(data, 'starting') &&
      ['offline', 'done', 'idle', 'unknown', 'interrupted'].includes(Wire.text(data, 'status'));
  }

  canMarkSeen(row: ResourceRow): boolean {
    return this.rowBelongs(row) && (row.state === 'unread' ||
      (row.state === 'idle' && Wire.bool(Wire.object(row.data(), 'work'), 'just_finished')));
  }
  readOnly(row: ResourceRow): boolean {
    const data = row.data();
    return !!Wire.text(data, 'read_only_reason') || Wire.bool(data, 'read_only') || Wire.text(data, 'source_kind') === 'codex_history';
  }

  async togglePin(row: ResourceRow, space: boolean = false): Promise<boolean> {
    const key = space ? rowSpaceKey(row) : this.sessionKey(row);
    return key ? this.changePreference('pins', key, space, row) : false;
  }

  async toggleMute(row: ResourceRow, space: boolean = false): Promise<boolean> {
    const key = space ? rowSpaceKey(row) : this.sessionKey(row);
    if (!key) return false;
    const snapshot = this.captureRow(row, '/api/v1/mutes');
    if (!snapshot || this.blocked(snapshot, '/api/v1/mutes')) return false;
    if (space) return this.changePreference('mutes', key, true, row);
    if (this.busy) return false;
    this.busy = true; this.error = '';
    try {
      const current = await snapshot.repo.call('/api/v1/mutes');
      if (!this.active(snapshot) || !this.rowBelongs(row)) return false;
      const spaces = stringsFrom(current, 'spaces');
      const sessions = stringsFrom(current, 'sessions');
      const allowed = stringsFrom(current, 'allowed', true);
      const spaceKey = rowSpaceKey(row), inherited = !!spaceKey && spaces.includes(spaceKey);
      const muted = sessions.includes(key) || (!allowed.includes(key) && inherited);
      const nextSessions = muted ? removeKey(sessions, key) : toggleKey(sessions, key);
      const nextAllowed = muted && inherited ? togglePresent(allowed, key) : removeKey(allowed, key);
      const body = Wire.payload(['spaces', 'sessions', 'allowed'], [spaces, nextSessions, nextAllowed]);
      if (!this.active(snapshot) || this.blocked(snapshot, '/api/v1/mutes')) return false;
      await snapshot.repo.call('/api/v1/mutes', 'PUT', body, '', (): boolean => this.active(snapshot));
      if (!this.ownerActive(snapshot)) return false;
      this.applyMutes(spaces, nextSessions, nextAllowed, snapshot.client); return true;
    } catch (failure) { if (this.active(snapshot)) this.fail(failure as Error); return false; }
    finally { if (this.ownerActive(snapshot)) this.busy = false; }
  }

  async rename(row: ResourceRow, title: string, tabName: string): Promise<boolean> {
    if (!this.canRename(row)) return false;
    const data = row.data(), priorTitle = Wire.text(data, 'title'), priorTab = Wire.text(data, 'slot_name');
    const nextTitle = title.trim(), nextTab = tabName.trim();
    const keys: string[] = [], values: Object[] = [];
    if (nextTitle !== priorTitle && nextTitle) { keys.push('title'); values.push(nextTitle); }
    if (Wire.text(data, 'pane_id') && nextTab !== priorTab && nextTab) { keys.push('tab_name'); values.push(nextTab); }
    if (!keys.length) return false;
    const path = this.sessionPath(row) + '/rename', snapshot = this.captureRow(row, path);
    if (!snapshot || this.blocked(snapshot, path) || this.busy) return false;
    this.busy = true; this.error = '';
    try {
      await snapshot.repo.call(path, 'POST', Wire.payload(keys, values), '', (): boolean => this.active(snapshot));
      if (!this.ownerActive(snapshot)) return false;
      await this.refreshAfterMutation(snapshot);
      return this.ownerActive(snapshot);
    } catch (failure) { if (this.active(snapshot)) this.fail(failure as Error); return false; }
    finally { if (this.ownerActive(snapshot)) this.busy = false; }
  }

  async close(row: ResourceRow): Promise<boolean> {
    if (!this.canClose(row)) return false;
    const path = this.sessionPath(row) + '/close', snapshot = this.captureRow(row, path);
    if (!snapshot || this.blocked(snapshot, path) || this.busy) return false;
    this.busy = true; this.error = '';
    try {
      await snapshot.repo.call(path, 'POST', Wire.payload([], []), '', (): boolean => this.active(snapshot));
      if (!this.ownerActive(snapshot)) return false;
      await this.refreshAfterMutation(snapshot);
      return this.ownerActive(snapshot);
    } catch (failure) { if (this.active(snapshot)) this.fail(failure as Error); return false; }
    finally { if (this.ownerActive(snapshot)) this.busy = false; }
  }

  async delete(row: ResourceRow): Promise<boolean> {
    if (!this.canDelete(row)) return false;
    const path = this.sessionPath(row), snapshot = this.captureRow(row, path);
    if (!snapshot || this.blocked(snapshot, path) || this.busy) return false;
    this.busy = true; this.error = '';
    try {
      await snapshot.repo.call(path, 'DELETE', Wire.payload([], []), '', (): boolean => this.active(snapshot));
      if (!this.ownerActive(snapshot)) return false;
      await this.refreshAfterMutation(snapshot);
      return this.ownerActive(snapshot);
    } catch (failure) { if (this.active(snapshot)) this.fail(failure as Error); return false; }
    finally { if (this.ownerActive(snapshot)) this.busy = false; }
  }

  async markSeen(row: ResourceRow): Promise<boolean> {
    if (!this.canMarkSeen(row) || this.busy) return false;
    const path = this.sessionPath(row) + '/seen', snapshot = this.captureRow(row, path);
    if (!snapshot || this.blocked(snapshot, path)) return false;
    this.busy = true; this.error = '';
    try {
      await snapshot.repo.call(path, 'POST', Wire.payload([], []), '', (): boolean => this.active(snapshot));
      if (!this.ownerActive(snapshot)) return false;
      this.patchState(row, 'idle');
      return true;
    } catch (failure) { if (this.active(snapshot)) this.fail(failure as Error); return false; }
    finally { if (this.ownerActive(snapshot)) this.busy = false; }
  }

  machineIdentity(row: ResourceRow): string { return JSON.stringify([row.group || this.client?.profile.id || '', row.manager, row.kind === 'machine' ? row.id : rowMachine(row)]); }

  private sessionKey(row: ResourceRow): string {
    const machine = rowMachine(row), session = rowSession(row);
    return machine && session ? machine + '/' + session : '';
  }

  private sessionPath(row: ResourceRow): string { return '/api/v1/sessions/' + segment(rowMachine(row)) + '/' + segment(rowSession(row)); }

  private rowBelongs(row: ResourceRow): boolean {
    if (row.kind !== 'session' || !rowMachine(row) || !rowSession(row)) return false;
    return this.sourceRows.some((item: ResourceRow) => rowKey(item) === rowKey(row));
  }

  private canMutateSession(row: ResourceRow): boolean {
    if (!this.client || this.history || !this.rowBelongs(row) || this.readOnly(row)) return false;
    const owner = this.ownerFor(row); if (!owner || !owner.connected) return false;
    const machine = owner.machines.find((item: ResourceRow) => item.id === rowMachine(row) && item.manager === row.manager);
    if (!machine) return false;
    const trust = Wire.text(machine.data(), 'trust');
    return ['', 'confirmed', 'personal', 'work'].includes(trust);
  }

  private ownerFor(row: ResourceRow): ClientModel | undefined { return this.client?.directoryOwnerFor(row); }
  private capture(client: ClientModel): AgentOwnerSnapshot | undefined {
    const root = this.client;
    if (!root || !client.connected || root.scope() !== this.scope) return undefined;
    try {
      const repo = client.repo() as AgentRepository;
      return { root: root, rootScope: root.scope(), rootGeneration: root.connectionGeneration(), revision: this.revision,
        client: client, repo: repo, generation: client.connectionGeneration(), scope: client.scope(),
        viewScope: this.viewScope, manager: client.profile.manager, rowKey: '', rowTarget: '', actionEpoch: this.actionEpoch };
    } catch (_) { return undefined; }
  }
  private captureRow(row: ResourceRow, path: string): AgentOwnerSnapshot | undefined {
    const client = this.ownerFor(row);
    if (!client || !this.rowBelongs(row)) return undefined;
    const snapshot = this.capture(client); if (!snapshot) return undefined;
    if (client.operationBlocked(path)) { this.error = 'pending_action'; return undefined; }
    snapshot.rowKey = rowKey(row); snapshot.rowTarget = client.target(row); return snapshot;
  }
  private active(snapshot: AgentOwnerSnapshot): boolean {
    if (!this.ownerActive(snapshot)) return false;
    if (snapshot.rowKey) {
      const row = this.findSourceRow(snapshot.rowKey);
      if (!row || snapshot.client.target(row) !== snapshot.rowTarget || this.ownerFor(row) !== snapshot.client) return false;
    }
    return true;
  }
  private ownerActive(snapshot: AgentOwnerSnapshot): boolean {
    if (snapshot.rowKey && snapshot.actionEpoch !== this.actionEpoch) return false;
    if (this.client !== snapshot.root || this.revision !== snapshot.revision || this.viewScope !== snapshot.viewScope ||
      !snapshot.root.owns(snapshot.rootGeneration, snapshot.rootScope) || !snapshot.client.owns(snapshot.generation, snapshot.scope) ||
      !snapshot.client.connected || snapshot.client.profile.manager !== snapshot.manager) return false;
    try { return snapshot.client.repo() === snapshot.repo; } catch (_) { return false; }
  }
  private preferencesFor(row: ResourceRow): AgentPreferences {
    const owner = this.ownerFor(row), saved = owner ? this.preferences.get(owner.scope()) : undefined;
    if (saved) return saved;
    const preferences = new AgentPreferences();
    if (owner === this.client) { preferences.pinsSpaces = this.pinsSpaces; preferences.pinsSessions = this.pinsSessions; preferences.mutesSpaces = this.mutesSpaces; preferences.mutesSessions = this.mutesSessions; preferences.mutesAllowed = this.mutesAllowed; }
    return preferences;
  }
  private async refreshDirectoryPreferences(client: ClientModel, revision: number): Promise<void> {
    const owners = new Set<ClientModel>(client.directoryOwners());
    for (const owner of owners) {
      if (!this.loadCurrent(client, revision)) return;
      const snapshot = this.capture(owner); if (snapshot) { try { await this.refreshPreferences(snapshot); } catch (failure) { if (this.ownerActive(snapshot)) this.fail(failure as Error); } }
    }
  }
  private findSourceRow(key: string): ResourceRow | undefined { return this.sourceRows.find((row: ResourceRow) => rowKey(row) === key); }

  private blocked(snapshot: AgentOwnerSnapshot, path: string): boolean {
    if (!this.active(snapshot)) return true;
    if (snapshot.client.operationBlocked(path)) { this.error = 'pending_action'; return true; }
    return false;
  }

  private loadCurrent(client: ClientModel, revision: number): boolean {
    return this.client === client && this.revision === revision && this.scope === client.scope() && this.owner === client.profile.manager;
  }

  private async refreshPreferences(snapshot: AgentOwnerSnapshot): Promise<void> {
    const pins = await snapshot.repo.call('/api/v1/pins');
    if (!this.ownerActive(snapshot)) return;
    const mutes = await snapshot.repo.call('/api/v1/mutes');
    if (!this.ownerActive(snapshot)) return;
    this.applyPins(stringsFrom(pins, 'spaces'), stringsFrom(pins, 'sessions'), snapshot.client);
    this.applyMutes(stringsFrom(mutes, 'spaces'), stringsFrom(mutes, 'sessions'), stringsFrom(mutes, 'allowed', true), snapshot.client);
    this.rebuild();
  }

  private async changePreference(kind: string, key: string, space: boolean, row: ResourceRow): Promise<boolean> {
    if (!this.client || this.busy) return false;
    const path = '/api/v1/' + kind, snapshot = this.captureRow(row, path);
    if (!snapshot || this.blocked(snapshot, path)) return false;
    this.busy = true; this.error = '';
    try {
      const current = await snapshot.repo.call(path);
      if (!this.active(snapshot)) return false;
      const spaces = stringsFrom(current, 'spaces'), sessions = stringsFrom(current, 'sessions');
      if (kind === 'pins') {
        const nextSpaces = space ? toggleKey(spaces, key) : spaces;
        const existing = this.sessionPins(row, sessions);
        const nextSessions = space ? sessions : existing.length ? sessions.filter(pin => !existing.includes(pin)) : sessions.concat([key]);
        const body = Wire.payload(['spaces', 'sessions'], [nextSpaces, nextSessions]);
        if (!this.active(snapshot) || this.blocked(snapshot, path)) return false;
        await snapshot.repo.call(path, 'PUT', body, '', (): boolean => this.active(snapshot));
        if (!this.ownerActive(snapshot)) return false;
        this.releaseFreeze();
        this.applyPins(nextSpaces, nextSessions, snapshot.client);
        this.refreezeAfterFilter();
        return true;
      }
      const allowed = stringsFrom(current, 'allowed', true);
      const nextSpaces = space ? toggleKey(spaces, key) : spaces;
      const nextSessions = space ? sessions : toggleKey(sessions, key);
      const nextAllowed = space ? allowed : removeKey(allowed, key);
      const body = Wire.payload(['spaces', 'sessions', 'allowed'], [nextSpaces, nextSessions, nextAllowed]);
      if (!this.active(snapshot) || this.blocked(snapshot, path)) return false;
      await snapshot.repo.call(path, 'PUT', body, '', (): boolean => this.active(snapshot));
      if (!this.ownerActive(snapshot)) return false;
      this.applyMutes(nextSpaces, nextSessions, nextAllowed, snapshot.client); return true;
    } catch (failure) { if (this.active(snapshot)) this.fail(failure as Error); return false; }
    finally { if (this.ownerActive(snapshot)) this.busy = false; }
  }

  private applyPins(spaces: string[], sessions: string[], owner: ClientModel): void {
    const preferences = this.preferences.get(owner.scope()) || new AgentPreferences(); preferences.pinsSpaces = spaces.slice(); preferences.pinsSessions = sessions.slice(); this.preferences.set(owner.scope(), preferences);
    if (owner === this.client) { this.pinsSpaces = spaces.slice(); this.pinsSessions = sessions.slice(); } this.rebuild();
  }
  private applyMutes(spaces: string[], sessions: string[], allowed: string[], owner: ClientModel): void {
    const preferences = this.preferences.get(owner.scope()) || new AgentPreferences(); preferences.mutesSpaces = spaces.slice(); preferences.mutesSessions = sessions.slice(); preferences.mutesAllowed = allowed.slice(); this.preferences.set(owner.scope(), preferences);
    if (owner === this.client) { this.mutesSpaces = spaces.slice(); this.mutesSessions = sessions.slice(); this.mutesAllowed = allowed.slice(); } this.rebuild();
  }

  private async refreshAfterMutation(snapshot: AgentOwnerSnapshot): Promise<void> {
    try { await snapshot.client.refresh(); } catch (_) { /* the acknowledged mutation stays authoritative; a later list event refreshes rows */ }
  }

  private patchState(row: ResourceRow, state: string): void {
    const key = rowKey(row);
    const patch = (items: ResourceRow[]): ResourceRow[] => items.map((item: ResourceRow) => {
      if (rowKey(item) !== key) return item;
      const next = item.copy(), data = next.data();
      if (state === 'idle') {
        const work = Wire.object(data, 'work');
        if (Wire.bool(work, 'just_finished')) { work['just_finished'] = false; data['work'] = work; }
      }
      data['state'] = state; next.raw = JSON.stringify(data); next.state = state; return next;
    });
    this.latestSourceRows = patch(this.latestSourceRows); this.historyRows = patch(this.historyRows); this.rebuild();
  }

  private async loadHistory(client: ClientModel, revision: number, refreezeAfterLoad: boolean = false): Promise<void> {
    if (!this.history || !this.historySupported() || client.demo) { if (refreezeAfterLoad) this.refreezeAfterFilter(); return; }
    const request = ++this.historyRequest, machine = this.machineFilter;
    const owners = new Set<ClientModel>(client.directoryOwners());
    this.loading = true; this.error = ''; const rows: ResourceRow[] = [];
    try {
      for (const owner of owners) {
        if (!this.loadCurrent(client, revision) || request !== this.historyRequest || machine !== this.machineFilter) return;
        if (!owner.features.includes('agent_session_archives')) continue;
        const snapshot = this.capture(owner); if (!snapshot) continue;
        let machineId = machine;
        if (machine.startsWith('[')) {
          const match = owner.machines.find((row: ResourceRow) => this.machineIdentity(row) === machine);
          if (!match) continue;
          machineId = match.id;
        }
        const current = (): boolean => this.loadCurrent(client, revision) && this.ownerActive(snapshot) &&
          request === this.historyRequest && machine === this.machineFilter;
        try {
          const archived = await owner.archives.read(path => snapshot.repo.call(path), owner.profile.id || '', owner.profile.manager, machineId, current);
          if (!current()) return;
          rows.push(...archived);
        } catch (failure) { if (this.ownerActive(snapshot)) this.fail(failure as Error); }
      }
      if (!this.loadCurrent(client, revision) || request !== this.historyRequest || machine !== this.machineFilter) return;
      if ((refreezeAfterLoad || !this.historyLoaded) && this.readingDetail) { this.freezeSuppressed = false; this.frozenGroups = undefined; }
      this.historyRows = rows; this.historyLoaded = true; this.latestSourceRows = rows.slice(); this.rebuild();
    } finally { if (this.loadCurrent(client, revision) && request === this.historyRequest) { this.loading = false; if (refreezeAfterLoad && this.freezeSuppressed) this.refreezeAfterFilter(); } }
  }

  private rowsForClient(client: ClientModel | undefined): ResourceRow[] {
    if (!client) return [];
    return (client.directoryReady ? client.directorySessions : client.sessions).filter((row: ResourceRow) => {
      if (row.kind !== 'session') return false;
      const raw = row.data();
      return !Wire.bool(raw, 'archived') && !Wire.bool(raw, 'assistant');
    });
  }

  syncSelected(client: ClientModel): void {
    const selected = this.externalSelection || client.selected;
    if (!selected.kind) { if (this.externalSelection) this.selectedKey = ''; return; }
    const key = selected.kind === 'session' ? rowKey(selected) : '';
    const next = key && this.sourceRows.some((row: ResourceRow) => rowKey(row) === key) ? key : '';
    if (next === this.selectedKey) return;
    this.selectedKey = next; this.saveViewLater();
  }

  private releaseFreeze(): void {
    if (!this.readingDetail) return;
    this.freezeSuppressed = true; this.frozenGroups = undefined;
  }

  private sourceReady(): boolean { return !this.openingView && (this.history ? this.historyLoaded : !!this.client && (this.client.directoryReady || this.client.sessionsReady) && !this.client.directoryLoading); }

  private refreezeAfterFilter(): void {
    if (!this.readingDetail) return;
    this.freezeSuppressed = false;
    if (this.sourceReady() || this.groups.length > 0) this.frozenGroups = this.copyGroups(this.groups);
  }

  private rebuild(): void {
    let groups: CoreAgentListGroup[];
    let counts: Record<string, Object>;
    const byId = new Map<string, ResourceRow>();
    try {
      const relations = sessionRelations(this.latestSourceRows, unavailableSessionSources(this.client?.directoryOwners() || []));
      relations.rows.forEach(row => byId.set(coreRowId(row), row));
      const projection = this.adapter.project(this.makeListInput(relations));
      groups = projection.groups; counts = projection.counts || {};
      // The core matches the query (including machine and quick filters) before it hides
      // offline rows from its ordinary groups. Offer those readable matches once.
      if (this.search.trim() && projection.filtered) {
        const grouped = new Set(groups.flatMap(group => group.rows));
        const offline = projection.filtered.filter(id => {
          const row = byId.get(id);
          if (!row || row.state !== 'offline' || grouped.has(id) || !relations.find(row)) return false;
          return Wire.bool(Wire.object(row.data(), 'capabilities'), 'read', true);
        });
        if (offline.length) groups = groups.concat([{id:'state/offline', title:'', rows:offline, visible_rows:offline, folded:false}]);
      }
      this.relations = relations;
    } catch (_) {
      // Preserve both rows and action authority from the same successful snapshot.
      this.error = 'failed';
      return;
    }
    this.sourceRows = this.relations.rows;
    if (this.client) this.syncSelected(this.client);
    const needs = this.needsGroup(groups, byId);
    const nextCounts: Record<string, number> = {};
    ['needsYou', 'unread', 'working', 'backgroundWaiting'].forEach((key: string) => nextCounts[key] = Wire.number(counts, key));
    if (needs) nextCounts['needsYou'] = needs.rows.length;
    if (JSON.stringify(nextCounts) !== JSON.stringify(this.counts)) this.counts = nextCounts;
    if (this.readingDetail && !this.freezeSuppressed && this.frozenGroups) {
      const frozenIds = new Set<string>(this.frozenGroups.flatMap((group) => group.memberIds));
      const rowsById = new Map<string, ResourceRow>();
      groups.forEach((group) => group.visible_rows.forEach((id) => {
        const row = byId.get(id);
        if (frozenIds.has(id) && row) rowsById.set(id, row);
      }));
      this.groups = this.frozenGroups.map((group) => {
        const copy = this.copyGroup(group);
        const projected = groups.find((entry) => entry.id === group.id);
        if (projected) copy.folded = projected.folded;
        copy.rows = [];
        group.memberIds.forEach((id: string) => {
          const row = group.id === NEEDS_GROUP ? byId.get(id) : rowsById.get(id);
          if (row) copy.rows.push(row);
        });
        return copy;
      }).filter((group) => group.rows.length > 0 || group.totalRowCount > 0);
      return;
    }
    this.groups = groups.map((group) => {
      const native = new AgentWorkspaceGroup(group.id, '', group.title, '', '', '', '', '');
      native.folded = group.folded; native.parent = group.parent || '';
      native.stateCounts = group.state_counts || (group.id.startsWith('machine/') ? this.stateCounts(group.rows, byId) : {});
      native.spoken = group.spoken || ''; native.totalRowCount = group.rows.length;
      native.memberIds = group.rows.slice();
      native.showMachine = group.id.startsWith('machine/');
      native.rows = [];
      group.visible_rows.forEach((id: string) => { const row = byId.get(id); if (row) native.rows.push(row); });
      return native;
    });
    if (needs) this.groups.unshift(needs);
    if (this.readingDetail && this.sourceReady() && !this.freezeSuppressed && !this.frozenGroups) this.frozenGroups = this.copyGroups(this.groups);
  }

  /** The state a row shows in the list: a folded family shows its children's too (protocol "Folded state"). */
  familyState(row: ResourceRow, relations: SessionRelations = this.relations): string {
    if (!this.foldFamilies()) return row.state;
    const children = this.foldedChildren(row, relations);
    if (!children.length) return row.state;
    if (children.some(child => child.state === 'needs_you')) return 'needs_you';
    const quiet = ['idle', 'offline', 'unknown'].includes(row.state);
    if (quiet && children.some(child => child.state === 'working')) return 'working';
    if (quiet && children.some(child => child.state === 'unread')) return 'unread';
    if (row.state === 'offline' && children.some(child => child.state === 'idle')) return 'idle';
    return row.state;
  }

  /** Every agent that waits for the user, once, above the computers and spaces whose folds could hide it.
   * A family whose folded side chat or subtask waits is listed by its parent row, as the core sorts it. */
  private needsGroup(groups: CoreAgentListGroup[], byId: Map<string, ResourceRow>): AgentWorkspaceGroup | undefined {
    if (this.groupingMode === 'state' || this.search.trim() || (this.quickFilter !== 'all' && this.quickFilter !== 'needsYou')) return undefined;
    const ids: string[] = [], rows: ResourceRow[] = [], seen = new Set<string>();
    groups.forEach((group: CoreAgentListGroup) => group.rows.forEach((id: string) => {
      const row = byId.get(id);
      if (!row || this.familyState(row) !== 'needs_you' || seen.has(id)) return;
      seen.add(id); ids.push(id); rows.push(row);
    }));
    if (!rows.length) return undefined;
    const group = new AgentWorkspaceGroup(NEEDS_GROUP, '', '', '', '', '', '', '', rows);
    group.memberIds = ids;
    return group;
  }

  /** A computer's header counts what its folded rows hide, as the core does for spaces. */
  private stateCounts(ids: string[], byId: Map<string, ResourceRow>): Record<string, Object> {
    const counts: Record<string, Object> = {};
    let unknown = 0;
    ids.forEach((id: string) => {
      const row = byId.get(id), state = row ? this.familyState(row) : '';
      if (STATE_COUNT_KEYS.includes(state)) counts[state] = Wire.number(counts, state) + 1;
      else unknown++;
    });
    if (unknown) counts['unknown'] = unknown;
    return counts;
  }

  private makeListInput(relations: SessionRelations = this.relations): AgentListInput {
    const fold = this.foldFamilies();
    const displayed = fold ? relations.rows.filter(row => !relations.root(row) || this.isPinned(row)) : relations.rows;
    const rows: AgentListRow[] = displayed.map((row: ResourceRow): AgentListRow => {
      const raw = row.data();
      raw['machine_id'] = rowMachine(row); raw['session_id'] = rowSession(row); raw['title'] = row.title;
      raw['cwd'] = row.path; raw['state'] = row.state; raw['manager'] = row.manager; raw['app_hub_group'] = row.group;
      if (fold) raw['state'] = this.familyState(row, relations);
      return {id: row.id, raw, source: row};
    });
    return {rows, history: this.history, grouping: this.groupingMode, quick: this.quickFilter,
      machineFilter: this.machineFilter, query: this.search, reading: this.readingDetail,
      kept: this.frozenGroups?.flatMap((group) => group.memberIds) || [],
      machines: this.client?.directoryOwners().flatMap((owner: ClientModel): Record<string, Object>[] => {
        const current: Record<string, Object>[] = owner.machines.map((row: ResourceRow): Record<string, Object> =>
          ({ 'id': row.id, 'name': row.title, 'group': owner.profile.id, 'manager': row.manager || owner.profile.manager }));
        this.historyRows.filter((session) => (session.group || owner.profile.id) === owner.profile.id && session.manager === owner.profile.manager)
          .forEach((session) => {
            if (!current.some((machine) => machine['id'] === rowMachine(session))) {
              current.push({ 'id': rowMachine(session), 'name': this.ownerFor(session)?.machineName(rowMachine(session)) || rowMachine(session),
                'group': owner.profile.id, 'manager': session.manager });
            }
          });
        return current;
      }) || [],
      coreSettings: { 'inventory': !!this.search.trim(), 'sort': 'state', 'folded': this.folded, 'expanded_idle': this.expandedIdle, 'expanded_quiet': this.expandedQuiet,
        'compact': false, 'history': this.history, 'now': new Date().toISOString(), 'held': this.heldSession },
      preferencesFor: (row: AgentListRow): AgentListPreferences => this.preferencesFor(row.source as ResourceRow),
      machineIdentity: (row: AgentListRow): string => this.machineIdentity(row.source as ResourceRow),
      machineName: (row: AgentListRow): string => {
        const source = row.source as ResourceRow;
        const machine = this.ownerFor(source)?.machines.find((item: ResourceRow) => item.id === rowMachine(source) && item.manager === source.manager);
        return machine?.title || rowMachine(source);
      }};
  }

  private copyGroup(group: AgentWorkspaceGroup): AgentWorkspaceGroup {
    const copy = new AgentWorkspaceGroup(group.id, group.machineId, group.machineName, group.repositoryPath, group.repositoryName,
      group.worktreePath, group.worktreeName, group.branch, group.rows.slice());
    copy.showMachine = group.showMachine; copy.showRepository = group.showRepository; copy.showWorktree = group.showWorktree;
    copy.folded = group.folded; copy.parent = group.parent; copy.stateCounts = group.stateCounts;
    copy.spoken = group.spoken; copy.totalRowCount = group.totalRowCount; copy.memberIds = group.memberIds.slice(); return copy;
  }

  private copyGroups(groups: AgentWorkspaceGroup[]): AgentWorkspaceGroup[] { return groups.map((group: AgentWorkspaceGroup) => this.copyGroup(group)); }

  private fail(failure: Error): void {
    const detail = failure as Error as Object as Record<string, Object>;
    this.error = Wire.bool(detail, 'resultPending') ? 'pending' : Wire.bool(detail, 'resultUnknown') ? 'result_unknown' :
      Wire.text(detail, 'message') === 'pending_action' ? 'pending_action' : Wire.text(detail, 'message') === 'invalid_preferences' ? 'failed' : 'failed';
  }

  private saveViewLater(): void {
    if (this.persistTimer !== -1) clearTimeout(this.persistTimer);
    const client = this.client, revision = this.revision, viewScope = this.viewScope;
    this.persistTimer = setTimeout(() => {
      this.persistTimer = -1;
      if (!client || !this.loadCurrent(client, revision) || this.viewScope !== viewScope) return;
      const values: Object[] = [this.quickFilter, this.groupingMode, this.machineFilter, this.search, this.history, this.selectedKey,
        this.folded, this.expandedIdle, this.expandedQuiet];
      client.saveView('agent-workspace', JSON.stringify(Wire.payload(['quick', 'grouping', 'machine', 'search', 'history', 'selected',
        'folded', 'expanded_idle', 'expanded_quiet'], values)))
        .catch(() => undefined);
    }, 180);
  }
}

function togglePresent(values: string[], key: string): string[] { return values.includes(key) ? values : values.concat([key]); }
