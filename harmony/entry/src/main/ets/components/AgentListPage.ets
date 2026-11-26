import { agentBrand, agentBrandColor } from '../design/AgentBrand';
import { IconActions } from '../design/IconActions';
import { ColorMetrics, LengthMetrics } from '@kit.ArkUI';
import { util } from '@kit.ArkTS';
import { CapsuleSegmentButtonV2, SegmentButtonV2ItemOptions, SegmentButtonV2Items } from '@kit.ArkUI';
import { ClientModel } from '../models/ClientModel';
import { AgentWorkspace, AgentWorkspaceGroup, NEEDS_GROUP } from '../models/AgentWorkspace';
import { agentRowPreview, agentRowTone, runningSubagents } from '../core/AgentRowPreview';
import { ResourceRow, Wire } from '../models/Wire';
import { unavailableSessionSources } from '../models/SessionRelations';
import { BoundRow, reconcileBoundRows } from '../models/RowBinding';
import { Tokens } from '../design/Tokens';
import { Colors } from '../design/Colors';
import { ButtonStyles, SelectStyles } from '../design/Controls';
import { Symbols } from '../design/Symbols';
import { ToolGlyph } from '../design/ToolGlyph';
import { tr } from '../design/Strings';
import { nativeEnvironment, NativeEnvironment } from '../platform/NativeEnvironment';
import { nativeSheetInset } from '../platform/NativeInsets';
import { DeviceLook, deviceLook } from '../models/DeviceLook';

const QUICK_FILTERS: string[] = ['all', 'needsYou', 'working', 'unread', 'backgroundWaiting'];
const QUICK_LABELS: Record<string, string> = {'all': 'all', 'needsYou': 'needs_you', 'working': 'working', 'unread': 'unread',
  'backgroundWaiting': 'agent_group_background'};
const STATE_TOTAL_GROUPS: string[] = ['pinned', NEEDS_GROUP];
const ROW_TIME_REFRESH_MS: number = 30000;
const GROUPINGS: string[] = ['state', 'machine', 'space'];
const GROUP_LABELS: Record<string, string> = {'state': 'agent_group_state', 'machine': 'agent_filter_machine', 'space': 'agent_group_space'};
const STATE_LABELS: Record<string, string> = {'pinned': 'agent_group_pinned', 'needs': 'needs_you', 'state/needs': 'needs_you',
  'state/unread': 'agent_group_unread', 'state/awaiting': 'agent_group_awaiting', 'state/working': 'working',
  'state/background': 'agent_group_background', 'state/just_finished': 'agent_group_just_finished',
  'state/waiting': 'agent_group_waiting', 'state/muted': 'agent_group_muted',
  'state/idle': 'agent_group_idle', 'state/offline': 'offline'};

@ObservedV2
class AgentRowPresentation {
  @Trace raw: string = '';
  @Trace lastText: string = '';
  @Trace brand: string = '';
  @Trace rootTitle: string = '';
  @Trace relativeKind: string = '';
  @Trace familySummary: string = '';
  @Trace familyHasTasks: boolean = false;
  @Trace familyExpanded: boolean = false;
  @Trace indented: boolean = false;
  @Trace rawStatus: string = '';
  @Trace updatedAt: number = Number.NaN;
  @Trace worktree: string = '';
  @Trace repositoryRoot: string = '';
  @Trace cwd: string = '';
  @Trace machineId: string = '';
  @Trace sessionId: string = '';
  @Trace spaceName: string = '';
  @Trace recentContent: string = '';
  @Trace status: string = '';
  /** needs_you, working, background or unread: the mark on the avatar corner; empty for none. */
  @Trace tone: string = '';
  @Trace muted: boolean = false;
  @Trace subagents: number = 0;
  @Trace place: string = '';
  data: Record<string, Object> = {};
  @Trace metadata: string[] = [];
  @Trace selectionKey: string = '';
}

@ObservedV2
class BoundAgentRow {
  readonly identity: string;
  readonly key: string;
  @Trace bound: BoundRow<ResourceRow>;
  @Trace presentation: AgentRowPresentation;

  constructor(bound: BoundRow<ResourceRow>) {
    this.identity = bound.identity; this.key = bound.key; this.bound = bound; this.presentation = new AgentRowPresentation();
  }
}

@ObservedV2
class BoundAgentGroup {
  readonly identity: string;
  readonly key: string;
  @Trace value: AgentWorkspaceGroup;
  @Trace rows: BoundAgentRow[] = [];

  constructor(identity: string, key: string, value: AgentWorkspaceGroup) { this.identity = identity; this.key = key; this.value = value; }
}

/** The state mark on an Agent's avatar: Needs you breathes unless Reduce Motion is on. */
@ComponentV2
struct AgentStateMark {
  @Param tone: string = '';
  @Param breathing: boolean = false;
  @Local faded: boolean = false;

  @Monitor('breathing') breathingChanged(): void { this.syncBreath(); }
  private syncBreath(): void {
    if (this.breathing && !this.faded) {
      this.getUIContext().animateTo({ duration: Tokens.breath, iterations: -1, playMode: PlayMode.Alternate, curve: Curve.EaseInOut },
        () => { this.faded = true; });
    } else if (!this.breathing && this.faded) this.getUIContext().animateTo({ duration: 0 }, () => { this.faded = false; });
  }
  private fill(): ResourceColor {
    if (this.tone === 'needs_you') return Colors.needs;
    if (this.tone === 'unread') return Colors.unread;
    return Colors.working;
  }

  build() {
    if (this.tone === 'background') {
      SymbolGlyph(Symbols.waiting).fontSize(Tokens.stateMark).fontColor([Colors.working])
        .width(Tokens.stateMark).height(Tokens.stateMark).backgroundColor(Colors.canvas).borderRadius(Tokens.stateMark / 2)
    } else {
      Circle().width(Tokens.stateMark).height(Tokens.stateMark).fill(this.fill())
        .stroke(Colors.canvas).strokeWidth(Tokens.stateMarkRing)
        .opacity(this.faded ? Tokens.breathFloor : 1).onAppear(() => this.syncBreath())
    }
  }
}

@ComponentV2
export struct AgentListPage {
  @Require @Param model: ClientModel;
  @Param rail: boolean = false;
  @Param readingDetail: boolean = false;
  @Param active: boolean = true;
  @Param retainWorkspace: boolean = false;
  private editingAdmission: (() => boolean) | undefined;
  @Param selectedRow: ResourceRow = new ResourceRow();
  @Param selectionVisible: boolean = true;
  @Local refreshing: boolean = false;
  @Local environment: NativeEnvironment = nativeEnvironment;
  @Event onOpen: (row: ResourceRow) => void = () => {};
  @Event onVisible: (row: ResourceRow, ratio: number) => void = () => {};
  @Param workspace: AgentWorkspace = new AgentWorkspace();
  @Local look: DeviceLook = deviceLook;
  @Local groupBindings: BoundAgentGroup[] = [];
  @Local mixedMachines: boolean = false;
  @Local mixedHubs: boolean = false;
  /** Row times read this, so they move on every 30 s while the list is on screen. */
  @Local clock: number = Date.now();
  private clockTimer: number = -1;
  private rowVisibility: Map<string, number> = new Map();
  @Local filterWidth: number = 0;
  private rowBindingOwner: Object | undefined;
  private rowBindingScope: string = '';
  private groupBindingSequence: number = 0;
  @Local editorOpen: boolean = false;
  @Local editingRow: ResourceRow | undefined;
  @Local renameTitle: string = '';
  @Local renameTab: string = '';
  @Local filterSheetOpen: boolean = false;
  @Local filterInset: number = 0;
  private filterReady: boolean = false;
  private filterNodeId: string = 'agents.filters.' + util.generateRandomUUID();

  @Monitor('environment.safeBottomPx') filterBoundaryChanged(): void { this.measureFilters(); }
  private measureFilters(): void {
    if (!this.filterReady) return;
    const value = nativeSheetInset(this.getUIContext(), this.filterNodeId, this.environment.safeBottomPx);
    if (Math.abs(value - this.filterInset) > 0.1) this.filterInset = value;
  }

  aboutToAppear(): void { this.syncRowBindings(); if (this.active) { this.activateWorkspace(); this.startClock(); } }
  aboutToDisappear(): void { this.stopClock(); this.editorOpen = false; this.filterSheetOpen = false; this.editingAdmission = undefined; this.workspace.revokeRowActions(); if (!this.retainWorkspace) this.workspace.hide(); }

  private startClock(): void {
    this.clock = Date.now();
    if (this.clockTimer === -1) this.clockTimer = setInterval(() => { this.clock = Date.now(); }, ROW_TIME_REFRESH_MS);
  }
  private stopClock(): void { if (this.clockTimer !== -1) { clearInterval(this.clockTimer); this.clockTimer = -1; } }

  @Monitor('active') visibleChanged(): void { if (this.active) this.startClock(); else this.stopClock(); if (!this.active) { this.editorOpen = false; this.filterSheetOpen = false; this.editingRow = undefined; this.editingAdmission = undefined; this.workspace.revokeRowActions(); if (!this.retainWorkspace) this.workspace.hide(); } else this.activateWorkspace(); }
  @Monitor('model.forgottenEpoch') forgottenChanged(): void { this.workspace.forgetHub(this.model.forgottenProfile); if (this.editingRow?.group === this.model.forgottenProfile) { this.editorOpen = false; this.editingRow = undefined; this.editingAdmission = undefined; } }
  @Monitor('model.profile') profileChanged(): void { this.editorOpen = false; this.editingAdmission = undefined; this.syncRowBindings(); if (!this.active) return; this.activateWorkspace(); }
  @Monitor('selectedRow') externalSelectionChanged(): void { this.workspace.selectExternal(this.selectedRow); }
  @Computed get machineRosterKey(): string {
    // Ignore metrics and last_seen updates. A reachability change also invalidates cached child activity.
    const machines = this.model.directoryReady ? this.model.directoryMachines : this.model.machines;
    return JSON.stringify([machines.map((row: ResourceRow) => [row.group, row.manager, row.id, row.title, row.state]),
      unavailableSessionSources(this.model.directoryOwners())]);
  }
  @Monitor('model.directorySessions', 'model.directoryReady', 'model.sessionsReady', 'model.sessions', 'machineRosterKey')
  catalogChanged(): void {
    if (!this.active) return;
    this.workspace.update(this.model, this.readingDetail);
    this.workspace.selectExternal(this.selectedRow);
  }
  @Monitor('model.selected') selectionChanged(): void {
    if (this.active && this.workspace.isBoundTo(this.model)) this.workspace.syncSelected(this.model);
  }
  @Monitor('model.features') featuresChanged(): void {
    if (!this.active) return; this.workspace.update(this.model, this.readingDetail);
    this.workspace.refresh(this.model).catch(() => undefined);
  }
  @Monitor('model.connected') connectionChanged(): void {
    if (!this.active) return; this.workspace.update(this.model, this.readingDetail);
    if (this.model.connected) this.workspace.refresh(this.model).catch(() => undefined);
  }
  @Monitor('workspace.groups', 'workspace.groupingMode') rowsChanged(): void { this.syncRowBindings(); }
  @Monitor('readingDetail') detailChanged(): void { this.workspace.setReadingDetail(this.readingDetail); }

  private activateWorkspace(): void {
    if (this.workspace.isBoundTo(this.model)) {
      this.workspace.update(this.model, this.readingDetail);
      this.workspace.selectExternal(this.selectedRow);
      this.syncRowBindings();
      return;
    }
    this.workspace.open(this.model, this.readingDetail).then(() => {
      if (!this.active || !this.workspace.isBoundTo(this.model)) return;
      this.workspace.selectExternal(this.selectedRow); this.syncRowBindings();
    }).catch(() => undefined);
  }

  private syncRowBindings(): void {
    this.mixedMachines = new Set(this.workspace.visibleRows().map((row: ResourceRow) => JSON.stringify([row.group, row.manager, row.machine]))).size > 1;
    this.mixedHubs = new Set(this.workspace.visibleRows().map((row: ResourceRow) => row.group)).size > 1;
    const scope = JSON.stringify([this.model.scope(), this.model.profile.id, this.model.profile.manager]);
    const sameOwnerScope = this.rowBindingOwner === this.model && this.rowBindingScope === scope;
    const previous = sameOwnerScope ? this.groupBindings : [];
    const available = new Map<string, BoundAgentGroup[]>();
    previous.forEach((binding: BoundAgentGroup) => {
      const bindings = available.get(binding.identity) || [];
      bindings.push(binding);
      available.set(binding.identity, bindings);
    });
    const occurrences = new Map<string, number>(), next: BoundAgentGroup[] = [];
    this.workspace.groups.forEach((group: AgentWorkspaceGroup) => {
      const displayed = this.workspace.displayRows(group);
      const occurrence = occurrences.get(group.id) || 0;
      occurrences.set(group.id, occurrence + 1);
      const identity = JSON.stringify([group.id, occurrence]);
      const matches = available.get(identity);
      const existing = matches && matches.length ? matches.shift() : undefined;
      let binding: BoundAgentGroup;
      if (existing) {
        binding = existing; binding.value = group;
        const groupRows = reconcileBoundRows(binding.rows.map((rowBinding: BoundAgentRow) => rowBinding.bound), displayed,
          (row: ResourceRow) => row.key(), this.model, this.model, scope, scope);
        binding.rows = this.bindAgentRows(binding.rows, groupRows, true);
      } else {
        this.groupBindingSequence++;
        binding = new BoundAgentGroup(identity, 'agent-group-binding.' + String(this.groupBindingSequence), group);
        const groupRows = reconcileBoundRows([], displayed, (row: ResourceRow) => row.key(), undefined, this.model, '', scope);
        binding.rows = this.bindAgentRows([], groupRows, true);
      }
      next.push(binding);
    });
    this.groupBindings = next;
    this.rowBindingOwner = this.model; this.rowBindingScope = scope;
  }

  private bindAgentRows(current: BoundAgentRow[], rows: BoundRow<ResourceRow>[], project: boolean): BoundAgentRow[] {
    const available = new Map<string, BoundAgentRow>();
    current.forEach((binding: BoundAgentRow) => available.set(binding.identity, binding));
    const result: BoundAgentRow[] = [];
    rows.forEach((bound: BoundRow<ResourceRow>) => {
      let binding = available.get(bound.identity);
      if (binding) { available.delete(bound.identity); binding.bound = bound; }
      else binding = new BoundAgentRow(bound);
      if (project) this.updateAgentRowPresentation(binding.presentation, bound.value);
      result.push(binding);
    });
    return result;
  }

  private updateAgentRowPresentation(presentation: AgentRowPresentation, row: ResourceRow): void {
    if (presentation.raw !== row.raw) {
      const data = Wire.parse(row.raw);
      presentation.raw = row.raw; presentation.data = data;
      presentation.lastText = Wire.text(data, 'last_text');
      presentation.rawStatus = Wire.text(data, 'status');
      presentation.brand = Wire.text(data, 'agent') || Wire.text(data, 'agent_type') || (Wire.text(data, 'source_kind').startsWith('codex') ? 'codex' : 'claude');
      presentation.updatedAt = Date.parse(Wire.text(data, 'updated_at'));
      presentation.worktree = Wire.text(data, 'worktree');
      presentation.repositoryRoot = Wire.text(data, 'repo_root');
      presentation.cwd = Wire.text(data, 'cwd');
      presentation.machineId = Wire.text(data, 'machine_id');
      presentation.sessionId = Wire.text(data, 'session_id');
      presentation.spaceName = Wire.text(data, 'space_name');
    }
    const root = this.workspace.relations.root(row), kids = this.workspace.foldedChildren(row);
    presentation.rootTitle = root?.title || root?.id || '';
    presentation.relativeKind = root ? this.workspace.relations.kind(row) : '';
    presentation.indented = !!root && this.workspace.foldFamilies() && !this.workspace.isPinned(row);
    presentation.familyExpanded = this.workspace.familyExpanded(row);
    presentation.familySummary = '';
    presentation.familyHasTasks = false;
    if (kids.length && this.workspace.foldFamilies()) {
      const sides = kids.filter(child => this.workspace.relations.kind(child) === 'side').length;
      const needs = kids.filter(child => child.state === 'needs_you').length;
      const parts: string[] = [];
      if (sides) parts.push(tr('relation_side_count').replace('{n}',String(sides)));
      if (kids.length > sides) {
        parts.push(tr('relation_task_count').replace('{n}', String(kids.length - sides)));
        presentation.familyHasTasks = true;
      }
      if (needs) parts.push(tr('relation_needs_count').replace('{n}',String(needs)));
      // The family line already counts its subtasks, so it says how many run instead of a second subagent line.
      const running = kids.filter(child => child.state === 'working').length;
      if (running) parts.push(tr('relation_running_count').replace('{n}', String(running)));
      presentation.familySummary = parts.join(' · ');
    }
    // A folded family also shows the state of its children, as the core sorts it and the Needs you section lists it.
    const state = this.workspace.familyState(row) || presentation.rawStatus;
    const muted = this.workspace.isMuted(row);
    const content = agentRowPreview(presentation.data, state, muted, Date.now(), tr) || row.detail;
    const machineId = row.machine || presentation.machineId;
    const sessionId = row.session || presentation.sessionId || row.id;
    const path = row.path || presentation.worktree || presentation.repositoryRoot || presentation.cwd;
    const owner = this.model.directoryOwnerFor(row);
    const machine = machineId ? owner?.machineName(machineId) || machineId : '';
    // The Hub name, never its manager ID: primary screens show names (DESIGN_SYSTEM 5.6).
    const hub = this.model.groupName(row);
    const shortPath = path ? this.model.shortPath(path) : '';
    presentation.status = state;
    presentation.muted = muted;
    presentation.tone = agentRowTone(presentation.data, state, muted);
    presentation.subagents = runningSubagents(presentation.data);
    presentation.recentContent = content;
    presentation.place = [this.mixedHubs ? hub : '', machine].filter((value: string) => !!value).join(' · ');
    presentation.metadata = [hub, machine, shortPath, state, content];
    presentation.selectionKey = JSON.stringify(row.group ? [row.group, row.kind, row.manager, machineId, sessionId] : [row.kind, row.manager, machineId, sessionId]);
  }

  /** All, the states that have agents now, and the chosen one while it empties so it can be left (as iOS). */
  private quickFilters(): string[] {
    return QUICK_FILTERS.filter((key: string) => key === 'all' || key === this.workspace.quickFilter || (this.workspace.counts[key] || 0) > 0);
  }
  private quickLabel(key: string): string {
    const count = key === 'all' ? 0 : this.workspace.counts[key] || 0;
    return tr(QUICK_LABELS[key]) + (count ? ' ' + String(count) : '');
  }
  private quickIndex(): number {
    const index = this.quickFilters().indexOf(this.workspace.quickFilter);
    return index < 0 ? 0 : index;
  }
  private chooseQuick(index: number): void {
    const key = this.quickFilters()[index];
    if (key && key !== this.workspace.quickFilter) this.workspace.setQuickFilter(key);
  }

  private machineOptions(): ResourceRow[] {
    return this.model.directoryReady ? this.model.directoryMachines : this.model.machines;
  }

  private machineLabel(): string {
    if (!this.workspace.machineFilter) return tr('agent_filter_all_machines');
    const row = this.machineOptions().find((item: ResourceRow) => this.workspace.machineIdentity(item) === this.workspace.machineFilter || item.id === this.workspace.machineFilter);
    return row?.title || this.workspace.machineFilter;
  }

  private machineIndex(): number {
    if (!this.workspace.machineFilter) return 0;
    const index = this.machineOptions().findIndex((item: ResourceRow) =>
      this.workspace.machineIdentity(item) === this.workspace.machineFilter || item.id === this.workspace.machineFilter);
    return index < 0 ? 0 : index + 1;
  }

  private machineSelectOptions(): SelectOption[] {
    const options: SelectOption[] = [{ value: tr('agent_filter_all_machines') }];
    const hubs = new Set(this.machineOptions().map((row: ResourceRow) => row.group)).size > 1;
    this.machineOptions().forEach((row: ResourceRow) => options.push({ value: [hubs ? this.model.groupName(row) : '', row.title || row.id].filter((value: string) => !!value).join(' · ') }));
    return options;
  }

  private selectMachine(index: number): void {
    const machine = index > 0 ? this.machineOptions()[index - 1] : undefined;
    this.workspace.setMachineFilter(machine ? this.workspace.machineIdentity(machine) : '').catch(() => undefined);
  }

  private groupingIndex(): number {
    const index = GROUPINGS.indexOf(this.workspace.groupingMode);
    return index < 0 ? 0 : index;
  }

  private selectGrouping(index: number): void {
    this.workspace.setGroupingMode(GROUPINGS[index] || 'state');
  }

  private openRow(row: ResourceRow): void {
    this.workspace.selectRow(row); this.onOpen(row);
  }

  private openRename(row: ResourceRow): void {
    if (!this.workspace.canRename(row) || this.workspace.busy) return;
    this.editingAdmission = this.workspace.actionTicket(row); this.editingRow = row; this.renameTitle = row.title || Wire.text(row.data(), 'title');
    this.renameTab = Wire.text(row.data(), 'slot_name'); this.editorOpen = true;
  }

  private async saveRename(): Promise<void> {
    const row = this.editingRow;
    if (!row || this.workspace.busy) return;
    if (!this.active || !this.editingAdmission || !this.editingAdmission()) { this.editorOpen = false; this.editingRow = undefined; return; }
    const saved = await this.workspace.rename(row, this.renameTitle, this.renameTab);
    if (saved) { this.editorOpen = false; this.editingRow = undefined; }
  }

  private confirmClose(row: ResourceRow): void {
    if (!this.workspace.canClose(row) || this.workspace.busy) return;
    const scope = this.model.scope(), generation = this.model.connectionGeneration(), key = row.key(), admitted = this.workspace.actionTicket(row);
    this.getUIContext().showAlertDialog({ title: tr('close'), message: row.title || row.id,
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('close'), action: () => {
        if (!this.active || !admitted() || !this.model.owns(generation, scope) || row.key() !== key) return;
        this.workspace.close(row).catch(() => undefined);
      } } });
  }

  private confirmDelete(row: ResourceRow): void {
    if (!this.workspace.canDelete(row) || this.workspace.busy) return;
    const scope = this.model.scope(), generation = this.model.connectionGeneration(), key = row.key(), admitted = this.workspace.actionTicket(row);
    this.getUIContext().showAlertDialog({ title: tr('delete'), message: row.title || row.id,
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('delete'), action: () => {
        if (!this.active || !admitted() || !this.model.owns(generation, scope) || row.key() !== key) return;
        this.workspace.delete(row).catch(() => undefined);
      } } });
  }

  @Builder rowMenu(binding: BoundAgentRow) {
    Menu() {
      MenuItem({ content: tr(this.workspace.isPinned(binding.bound.value) ? 'agent_unpin' : 'agent_pin') })
        .enabled(this.workspace.rowOnline(binding.bound.value) && !this.workspace.busy)
        .onClick(() => this.workspace.togglePin(binding.bound.value).catch(() => undefined))
      MenuItem({ content: tr(this.workspace.isMuted(binding.bound.value) ? 'unmute' : 'mute') })
        .enabled(this.workspace.rowOnline(binding.bound.value) && !this.workspace.busy)
        .onClick(() => this.workspace.toggleMute(binding.bound.value).catch(() => undefined))
      if (this.workspace.isSpacePinned(binding.bound.value) || binding.presentation.spaceName) {
        MenuItem({ content: tr(this.workspace.isSpacePinned(binding.bound.value) ? 'agent_unpin_space' : 'agent_pin_space') })
          .enabled(this.workspace.rowOnline(binding.bound.value) && !this.workspace.busy)
          .onClick(() => this.workspace.togglePin(binding.bound.value, true).catch(() => undefined))
        MenuItem({ content: tr(this.workspace.isSpaceMuted(binding.bound.value) ? 'agent_unmute_space' : 'agent_mute_space') })
          .enabled(this.workspace.rowOnline(binding.bound.value) && !this.workspace.busy)
          .onClick(() => this.workspace.toggleMute(binding.bound.value, true).catch(() => undefined))
      }
      if (this.workspace.canMarkSeen(binding.bound.value)) MenuItem({ content: tr('agent_mark_seen') })
        .enabled(!this.workspace.busy).onClick(() => this.workspace.markSeen(binding.bound.value).catch(() => undefined))
      if (this.workspace.canRename(binding.bound.value)) MenuItem({ content: tr('rename') }).enabled(!this.workspace.busy)
        .onClick(() => this.openRename(binding.bound.value))
      if (this.workspace.canClose(binding.bound.value)) MenuItem({ content: tr('close') }).enabled(!this.workspace.busy)
        .onClick(() => this.confirmClose(binding.bound.value))
      if (this.workspace.canDelete(binding.bound.value)) MenuItem({ content: tr('delete') }).enabled(!this.workspace.busy)
        .onClick(() => this.confirmDelete(binding.bound.value))
    }
  }

  @Builder groupHeader(binding: BoundAgentGroup) {
    Column({ space: Tokens.fine }) {
      if (binding.value.parent === 'quiet') {
        Text(tr('agent_group_quiet_spaces')).fontSize(Tokens.body).fontWeight(FontWeight.Medium).fontColor(Colors.ink).width('100%')
      }
      Row({ space: Tokens.gap }) {
        Column({ space: Tokens.fine }) {
          Row({ space: Tokens.gap }) {
            Text(this.groupTitle(binding.value)).fontSize(Tokens.meta).fontWeight(FontWeight.Medium)
              .fontColor(binding.value.id === NEEDS_GROUP ? Colors.needs : Colors.secondary).maxLines(2).flexShrink(1)
            if (this.groupTotal(binding.value)) Text(this.groupTotal(binding.value)).fontSize(Tokens.caption).fontWeight(FontWeight.Medium)
              .fontColor(binding.value.id === NEEDS_GROUP ? Colors.needs : Colors.secondary)
          }.alignItems(VerticalAlign.Center)
          if (this.groupNeeds(binding.value) || this.groupCounts(binding.value)) Text() {
            if (this.groupNeeds(binding.value)) Span(this.groupNeeds(binding.value)).fontColor(Colors.needs).fontWeight(FontWeight.Medium)
            if (this.groupNeeds(binding.value) && this.groupCounts(binding.value)) Span(' · ').fontColor(Colors.secondary)
            if (this.groupCounts(binding.value)) Span(this.groupCounts(binding.value)).fontColor(Colors.secondary)
          }.fontSize(Tokens.caption)
        }.alignItems(HorizontalAlign.Start).layoutWeight(1).constraintSize({minHeight:Tokens.touch}).justifyContent(FlexAlign.Center)
          .onClick(() => { if (!this.workspace.search && binding.value.id !== NEEDS_GROUP) this.workspace.toggleFold(binding.value.id); })
          .accessibilityGroup(true).accessibilityText([this.groupTitle(binding.value), this.groupTotal(binding.value), this.groupNeeds(binding.value),
            this.groupCounts(binding.value)].filter((value: string) => !!value).join(' · '))
        if (binding.value.id !== NEEDS_GROUP) Button({ type: ButtonType.Circle }) {
          SymbolGlyph(binding.value.folded ? Symbols.down : Symbols.up).fontSize(Tokens.symbolFont).fontColor([Colors.link])
        }.attributeModifier(ButtonStyles.icon).enabled(!this.workspace.search)
          .accessibilityText(this.groupTitle(binding.value) + ' ' + tr(binding.value.folded ? 'agent_expand' : 'agent_collapse'))
          .onClick(() => this.workspace.toggleFold(binding.value.id)).id('agents.group.toggle.' + binding.value.id)
      }.width('100%').alignItems(VerticalAlign.Center)
    }.alignItems(HorizontalAlign.Start)
      .padding({ left: Tokens.page, right: Tokens.page, top: Tokens.inset, bottom: Tokens.fine }).width('100%')
  }

  private groupTitle(group: AgentWorkspaceGroup): string { return tr(STATE_LABELS[group.id] || '') || group.machineName; }

  /** By Status sections, Pinned and Needs you say how many rows they hold, as iOS's section headers. */
  private groupTotal(group: AgentWorkspaceGroup): string {
    return (STATE_TOTAL_GROUPS.includes(group.id) || group.id.startsWith('state/')) && group.totalRowCount ? String(group.totalRowCount) : '';
  }

  /** What waits for the user in a computer or space, on its header even while it is folded (DESIGN_SYSTEM 3.4). */
  private groupNeeds(group: AgentWorkspaceGroup): string {
    const count = Wire.number(group.stateCounts, 'needs_you');
    return count ? String(count) + ' ' + tr('needs_you') : '';
  }

  private groupCounts(group: AgentWorkspaceGroup): string {
    const values: string[] = [];
    const labels: Record<string, string> = {'working': 'working', 'unread': 'agent_group_unread',
      'idle': 'agent_group_idle', 'unknown': 'unknown'};
    ['working', 'unread', 'idle', 'unknown'].forEach((key) => {
      const count = Wire.number(group.stateCounts, key);
      if (count) values.push(String(count) + ' ' + tr(labels[key]));
    });
    if (!group.folded && group.totalRowCount > group.rows.length) values.push(String(group.totalRowCount - group.rows.length) + ' ' + tr('agent_group_more_idle'));
    return values.join(' · ');
  }

  private rowStatus(presentation: AgentRowPresentation): string {
    if (presentation.tone === 'background') return tr('agent_group_background');
    return presentation.status ? tr(presentation.status) : '';
  }

  private rowStatusColor(presentation: AgentRowPresentation): ResourceColor {
    const state = presentation.status;
    if (presentation.tone === 'background') return Colors.working;
    if (state === 'needs_you') return Colors.needs;
    if (state === 'working') return Colors.working;
    if (state === 'unread') return Colors.unread;
    return Colors.secondary;
  }

  private rowTime(presentation: AgentRowPresentation): string {
    if (!Number.isFinite(presentation.updatedAt)) return '';
    const elapsed = Math.floor(((this.clock || Date.now()) - presentation.updatedAt) / 1000);
    // A row newer than the last tick reads as just now; only a clock far behind the hub hides the time.
    if (elapsed < -60) return '';
    if (elapsed < 60) return tr('agent_time_just_now');
    if (elapsed < 3600) return String(Math.floor(elapsed / 60)) + tr('agent_time_minute_suffix');
    if (elapsed < 86400) return String(Math.floor(elapsed / 3600)) + tr('agent_time_hour_suffix');
    return String(Math.floor(elapsed / 86400)) + tr('agent_time_day_suffix');
  }

  @Builder filterSheet() {
    Column() {
      Row({ space: Tokens.page }) {
        Text(tr('agent_filter_machine')).fontSize(Tokens.body).fontColor(Colors.ink).layoutWeight(1)
        Select(this.machineSelectOptions()).selected(this.machineIndex()).value(this.machineLabel())
          .attributeModifier(SelectStyles.inline).constraintSize({ minHeight: Tokens.touch, maxWidth: '68%' })
          .onSelect((index: number) => this.selectMachine(index)).id('agents.machine_filter')
      }.width('100%').constraintSize({ minHeight: Tokens.header }).alignItems(VerticalAlign.Center)
      Divider().strokeWidth(Tokens.hairline).color(Colors.divider)
      if (this.workspace.historySupported()) {
        Row({ space: Tokens.page }) {
          Text(tr('agent_history')).fontSize(Tokens.body).fontColor(Colors.ink).layoutWeight(1)
          Toggle({ type: ToggleType.Switch, isOn: this.workspace.history })
            .onChange((value: boolean) => this.workspace.setHistory(value))
            .accessibilityText(tr('agent_history')).id('agents.history')
        }.width('100%').alignItems(VerticalAlign.Center).constraintSize({ minHeight: Tokens.header })
        Divider().strokeWidth(Tokens.hairline).color(Colors.divider)
      }
      Row({ space: Tokens.page }) {
        Text(tr('grouping')).fontSize(Tokens.body).fontColor(Colors.ink).layoutWeight(1)
        Select(GROUPINGS.map((key) => ({ value: tr(GROUP_LABELS[key]) } as SelectOption)))
          .selected(this.groupingIndex()).value(tr(GROUP_LABELS[GROUPINGS[this.groupingIndex()]]))
          .attributeModifier(SelectStyles.inline).constraintSize({ minHeight: Tokens.touch, maxWidth: '68%' })
          .onSelect((index: number) => this.selectGrouping(index)).id('agents.grouping')
      }.width('100%').constraintSize({ minHeight: Tokens.header }).alignItems(VerticalAlign.Center)
    }.alignItems(HorizontalAlign.Start).padding({ left: Tokens.page, right: Tokens.page, top: Tokens.gap, bottom: Tokens.page + this.filterInset }).width('100%')
      .id(this.filterNodeId).onAreaChange(() => this.measureFilters())
  }

  private isRowSelected(binding: BoundAgentRow): boolean {
    return this.selectionVisible && this.workspace.selectedKey === binding.presentation.selectionKey;
  }
  /** A row can show twice (its group and Needs you); the list reports the larger of the two. */
  private reportVisible(row: ResourceRow, copy: string, ratio: number): void {
    const key = row.key();
    this.rowVisibility.set(copy + '\n' + key, ratio);
    this.onVisible(row, Math.max(this.rowVisibility.get('list\n' + key) || 0, this.rowVisibility.get(NEEDS_GROUP + '\n' + key) || 0));
  }

  private rowSpoken(binding: BoundAgentRow): string {
    const presentation = binding.presentation;
    const relation = presentation.rootTitle ? tr(presentation.relativeKind === 'side' ? 'relation_side' : 'relation_task') + ' · ' +
      tr('relation_root') + ' · ' + presentation.rootTitle : '';
    return [binding.bound.value.title || tr('agent_untitled'), relation, presentation.metadata.slice(0, 2).filter((value: string) => !!value).join(' · '),
      this.rowStatus(presentation), presentation.muted ? tr('agent_group_muted') : '', presentation.familySummary || this.subagentLine(presentation)]
      .filter((value: string) => !!value).join(' · ');
  }

  private subagentLine(presentation: AgentRowPresentation): string {
    return presentation.subagents > 0 && !presentation.familySummary ? tr('agent_subagents_running').replace('{count}', String(presentation.subagents)) : '';
  }

  @Builder agentRow(binding: BoundAgentRow, groupId: string) {
    ListItem() {
      Column() {
      Row({ space: Tokens.gap }) {
        Stack({ alignContent: Alignment.BottomEnd }) {
          Column() {
            if (agentBrand(binding.presentation.brand)) Image(agentBrand(binding.presentation.brand)).width(Tokens.section).height(Tokens.section)
              .objectFit(ImageFit.Contain).fillColor(agentBrandColor(binding.presentation.brand)).accessibilityText(binding.presentation.brand)
            else SymbolGlyph(Symbols.agent).fontSize(Tokens.symbolFont).fontColor([Colors.teal]).accessibilityText(binding.presentation.brand)
          }.width(Tokens.avatar).height(Tokens.avatar).alignItems(HorizontalAlign.Center).justifyContent(FlexAlign.Center)
            .backgroundColor(Colors.control).borderRadius(Tokens.avatar / 2).opacity(binding.presentation.muted ? Tokens.mutedOpacity : 1)
          if (binding.presentation.tone) AgentStateMark({ tone: binding.presentation.tone,
            breathing: binding.presentation.tone === 'needs_you' && this.active && !this.environment.reduceMotion })
        }.width(Tokens.avatar).height(Tokens.avatar)
        if (!this.rail) Column({ space: Tokens.fine }) {
          Row({ space: Tokens.gap }) {
            Text(binding.bound.value.title || tr('agent_untitled')).fontSize(Tokens.body)
              .fontWeight(binding.presentation.status === 'unread' && !binding.presentation.muted ? FontWeight.Bold : FontWeight.Medium)
              .fontColor(Colors.ink).layoutWeight(1).maxLines(this.environment.fontScale >= 1.5 ? 2 : 1).textOverflow({ overflow: TextOverflow.Ellipsis })
            if (binding.presentation.muted) SymbolGlyph(Symbols.muted).fontSize(Tokens.caption).fontColor([Colors.secondary])
            if (this.environment.fontScale < 1.5 && this.rowTime(binding.presentation)) Text(this.rowTime(binding.presentation)).fontSize(Tokens.caption).fontColor(Colors.secondary)
          }.width('100%').alignItems(VerticalAlign.Center)
          if (this.environment.fontScale >= 1.5 && this.rowTime(binding.presentation)) Text(this.rowTime(binding.presentation)).fontSize(Tokens.caption).fontColor(Colors.secondary)
          if (binding.presentation.rootTitle) {
            Row({space:Tokens.fine}) {
              if (binding.presentation.relativeKind === 'subtask') ToolGlyph({name:'agent', glyphSize:Tokens.meta, ink:Colors.teal})
              Text(tr(binding.presentation.relativeKind === 'side' ? 'relation_side' : 'relation_task') +
                (binding.presentation.indented ? '' : ' · ' + tr('relation_root') + ' · ' + binding.presentation.rootTitle))
                .fontSize(Tokens.caption).fontColor(Colors.secondary).layoutWeight(1).maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis})
            }.width('100%')
          }
          if (this.mixedMachines && this.workspace.groupingMode !== 'machine' && binding.presentation.place) Text(binding.presentation.place).fontSize(Tokens.caption).fontColor(Colors.secondary).width('100%').maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
          if (binding.presentation.recentContent) Text(binding.presentation.recentContent).fontSize(Tokens.meta)
            .fontColor(Colors.secondary).width('100%').maxLines(this.look.density === 'compact' || this.rail ? 1 : 2).textOverflow({ overflow: TextOverflow.Ellipsis })
          if (this.subagentLine(binding.presentation)) Text(this.subagentLine(binding.presentation)).fontSize(Tokens.caption)
            .fontColor(Colors.secondary).width('100%').maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
          if (binding.presentation.status === 'working' && binding.presentation.tone !== 'background') {
            Row({ space: Tokens.fine }) {
              if (this.active && !this.environment.reduceMotion) LoadingProgress().width(Tokens.caption).height(Tokens.caption).color(Colors.working)
              else Circle().width(Tokens.stateMark).height(Tokens.stateMark).fill(Colors.working)
              Text(tr('working')).fontSize(Tokens.caption).fontWeight(FontWeight.Medium).fontColor(Colors.working)
            }.padding({left:Tokens.gap, right:Tokens.gap, top:2, bottom:2}).backgroundColor(Colors.control).borderRadius(Tokens.capsule)
          } else if (this.rowStatus(binding.presentation)) Text(this.rowStatus(binding.presentation)).fontSize(Tokens.meta).fontWeight(FontWeight.Medium)
            .fontColor(this.rowStatusColor(binding.presentation)).width('100%')
        }.alignItems(HorizontalAlign.Start).layoutWeight(1).opacity(binding.presentation.muted ? Tokens.mutedOpacity : 1)
      }.width('100%').alignItems(VerticalAlign.Center).justifyContent(this.rail ? FlexAlign.Center : FlexAlign.Start)
        .padding({ left: this.rail ? Tokens.inset : binding.presentation.indented ? Tokens.page + Tokens.section : Tokens.page, right: this.rail ? Tokens.inset : Tokens.page, top: this.look.density === 'compact' ? Tokens.gap : Tokens.inset,
          bottom: this.look.density === 'compact' ? Tokens.gap : Tokens.inset })
        .onClick(() => this.openRow(binding.bound.value))
        .accessibilityGroup(true).accessibilityText(this.rowSpoken(binding))
      if (!this.rail && binding.presentation.familySummary) {
        Button({type:ButtonType.Normal,buttonStyle:ButtonStyleMode.TEXTUAL}) {
          Row({space:Tokens.fine}) {
            if (binding.presentation.familyHasTasks) ToolGlyph({name:'agent', glyphSize:Tokens.meta, ink:Colors.teal})
            Text(binding.presentation.familySummary).fontSize(Tokens.caption).fontColor(Colors.secondary)
            SymbolGlyph(binding.presentation.familyExpanded ? Symbols.up : Symbols.down).fontSize(Tokens.caption).fontColor([Colors.secondary])
          }
        }.alignSelf(ItemAlign.Start).margin({left:Tokens.page + Tokens.avatar}).padding({left:Tokens.gap,right:Tokens.gap})
          .constraintSize({minHeight:Tokens.touch}).backgroundColor(Color.Transparent)
          .accessibilityText(binding.presentation.familySummary + ' ' + tr(binding.presentation.familyExpanded ? 'agent_collapse' : 'agent_expand'))
          .onClick(() => this.workspace.toggleFamily(binding.bound.value)).id('agents.family.' + binding.bound.value.key())
      }
      }.width('100%')
    }
      .selectable(true).selected(this.isRowSelected(binding))
      .backgroundColor(this.isRowSelected(binding) ? Colors.selection : Colors.canvas)
      .onVisibleAreaChange([0, 1], (visible: boolean, ratio: number) => this.reportVisible(binding.bound.value, groupId === NEEDS_GROUP ? NEEDS_GROUP : 'list', visible ? ratio : 0))
      .onDisAppear(() => this.reportVisible(binding.bound.value, groupId === NEEDS_GROUP ? NEEDS_GROUP : 'list', 0))
      .bindContextMenu(this.rowMenu(binding), ResponseType.LongPress)
      .id('agent.' + (groupId === NEEDS_GROUP ? 'needs.' : '') + binding.bound.value.group + '.' + binding.bound.value.manager + '.' + binding.bound.value.machine + '.' + binding.bound.value.id)
  }

  @Builder searchControl() {
    Search({ value: this.workspace.search, placeholder: tr('search_sessions') })
      .height(Math.max(Tokens.touch, Tokens.lineHeight * this.environment.fontScale + Tokens.page))
      .textFont({ size: Tokens.body }).placeholderFont({ size: Tokens.body }).caretStyle({ color: Colors.action })
      .onChange((value: string) => this.workspace.setSearch(value)).id('agents.search')
  }
  @Builder filterControl() {
    Button({ type: ButtonType.Circle }) { SymbolGlyph(Symbols.filter).fontSize(Tokens.symbolFont).fontColor([Colors.link]) }.attributeModifier(ButtonStyles.chrome).accessibilityText(tr('agent_filter_button'))
      .onClick(() => this.filterSheetOpen = true).id('agents.filter')
  }
  private quickFilterUsesSelect(): boolean {
    if (this.environment.fontScale >= 1.5 || this.filterWidth <= 0) return true;
    const context = this.getUIContext();
    let labelWidth = 0;
    const filters = this.quickFilters();
    filters.forEach((key: string) => {
      labelWidth = Math.max(labelWidth, context.px2vp(context.getMeasureUtils().measureText({
        textContent: this.quickLabel(key), fontSize: String(Tokens.meta) + 'fp', fontWeight: FontWeight.Medium
      })));
    });
    const slotWidth = Math.max(Tokens.touch, labelWidth + Tokens.inset * 2);
    return this.filterWidth - Tokens.page * 2 - Tokens.fine * 2 < slotWidth * filters.length;
  }

  @Builder filters() {
    if (!this.rail) Column({ space: Tokens.gap }) {
      if (this.environment.fontScale >= 1.5) {
        this.searchControl()
        this.filterControl()
      } else {
        Row({ space: Tokens.gap }) {
          Row() { this.searchControl() }.layoutWeight(1)
          this.filterControl()
        }.width('100%')
      }
      if (this.quickFilterUsesSelect()) {
        Select(this.quickFilters().map((key: string) => ({ value: this.quickLabel(key) } as SelectOption)))
          .selected(this.quickIndex()).value(this.quickLabel(this.quickFilters()[this.quickIndex()])).attributeModifier(SelectStyles.inline).width('100%').constraintSize({ minHeight: Tokens.touch })
          .onSelect((index: number) => this.chooseQuick(index)).id('agents.quick')
      } else {
        // The filters list only states that have sessions, so the set can change while the chosen filter keeps its key
        // at a new position. CapsuleSegmentButtonV2 then reads a backplate position it has not laid out yet and throws
        // (getBackplatePosition): build a new one whenever the set of segments changes rather than updating it in place.
        ForEach([this.quickFilters().join('|')], (_set: string) => {
        CapsuleSegmentButtonV2({ items: new SegmentButtonV2Items(this.quickFilters().map((key: string) => ({ text: this.quickLabel(key) } as SegmentButtonV2ItemOptions))),
          selectedIndex: this.quickIndex(), $selectedIndex: (index: number) => this.chooseQuick(index),
          itemSelectedBackgroundColor: ColorMetrics.resourceColor(Colors.action), itemSelectedFontColor: ColorMetrics.resourceColor(Colors.onAction),
          itemFontColor: ColorMetrics.resourceColor(Colors.secondary), buttonBackgroundColor: ColorMetrics.resourceColor(Colors.control),
          itemFontSize: LengthMetrics.fp(Tokens.meta), itemSelectedFontSize: LengthMetrics.fp(Tokens.meta),
          itemMinHeight: LengthMetrics.vp(Tokens.touch), buttonMinHeight: LengthMetrics.vp(Tokens.touch), itemPadding: { start: LengthMetrics.vp(Tokens.inset), end: LengthMetrics.vp(Tokens.inset) },
          buttonPadding: LengthMetrics.vp(Tokens.fine) }).width('100%')
        }, (set: string) => set)
      }
    }.padding({ left: Tokens.page, right: Tokens.page, top: Tokens.inset, bottom: Tokens.gap }).width('100%')
      .onAreaChange((_old: Area, area: Area) => { const width = Number(area.width); if (Number.isFinite(width) && width > 0) this.filterWidth = width; })
  }

  @Builder renameForm() {
    Column({ space: Tokens.gap }) {
      TextInput({ text: this.renameTitle, placeholder: tr('agent_title') }).onChange((value: string) => this.renameTitle = value)
        .id('agents.rename_title')
      TextInput({ text: this.renameTab, placeholder: tr('agent_tab_name') }).onChange((value: string) => this.renameTab = value)
        .id('agents.rename_tab')
      Row({ space: Tokens.gap }) {
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('save')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('save', true)]) }.attributeModifier(ButtonStyles.iconPrimary).accessibilityText(tr('save')).bindTips(tr('save')).enabled(!this.workspace.busy)
          .onClick(() => this.saveRename()).id('agents.rename_save')
      }.width('100%').justifyContent(FlexAlign.End)
      if (this.workspace.error) Text(tr(this.workspace.error)).fontSize(Tokens.meta)
        .fontColor($r('app.color.persona_danger')).width('100%')
    }.padding(Tokens.page).width('100%')
  }

  build() {
    Column() {
      this.filters()
      if (this.model.directoryUnavailable > 0) Text(tr('directory_partly_unavailable')).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).padding(Tokens.inset).width('100%')
      if (this.workspace.error && !this.editorOpen) Text(tr(this.workspace.error)).fontSize(Tokens.meta)
        .fontColor($r('app.color.persona_danger')).padding(Tokens.inset).width('100%')
      Refresh({ refreshing: $$this.refreshing }) {
      List() {
        if (!this.workspace.hasRows() && (this.workspace.loading || this.workspace.awaitingSource() && !this.workspace.sourceProblem())) ListItem() {
          LoadingProgress().height(Tokens.touch).width(Tokens.touch).padding(Tokens.page)
        }.id('agents.loading')
        ForEach(this.groupBindings, (binding: BoundAgentGroup) => {
            ListItemGroup({ header: () => this.groupHeader(binding) }) {
              Repeat<BoundAgentRow>(binding.rows).each((entry: RepeatItem<BoundAgentRow>) => this.agentRow(entry.item, binding.value.id))
                .key((rowBinding: BoundAgentRow) => rowBinding.key)
                .virtualScroll({ totalCount: binding.rows.length, reusable: false })
            }.id('agents.group.' + binding.value.id)
            if (!binding.value.folded && !this.workspace.search && this.workspace.quickFilter === 'all' && (binding.value.id === 'state/idle' || binding.value.totalRowCount > binding.value.rows.length || this.workspace.idleRowsExpanded(binding.value.id))) ListItem() {
              Button() {
                Row({ space: Tokens.gap }) {
                  Text(binding.value.id === 'state/idle' || this.workspace.idleRowsExpanded(binding.value.id) ? tr('agent_collapse') : String(binding.value.totalRowCount - binding.value.rows.length) + ' ' + tr('agent_group_more_idle')).fontSize(Tokens.meta).fontColor(Colors.link).layoutWeight(1)
                  SymbolGlyph(binding.value.id === 'state/idle' || this.workspace.idleRowsExpanded(binding.value.id) ? Symbols.up : Symbols.down).fontSize(Tokens.symbolFont).fontColor([Colors.link])
                }.width('100%').padding(Tokens.page).constraintSize({ minHeight: Tokens.touch })
              }.type(ButtonType.Normal).buttonStyle(ButtonStyleMode.TEXTUAL).backgroundColor(Color.Transparent).width('100%')
                .onClick(() => {
                  if (binding.value.id === 'state/idle') this.workspace.toggleFold(binding.value.id);
                  else this.workspace.toggleIdleRows(binding.value.id);
                }).id('agents.idle.expand.' + binding.value.id)
            }
          }, (binding: BoundAgentGroup) => binding.key)
        if (!this.workspace.hasRows() && !this.workspace.loading && !this.workspace.awaitingSource()) ListItem() {
          Text(tr(this.workspace.search ? 'no_results' : 'empty')).fontSize(Tokens.meta).padding(Tokens.page).width('100%')
        }.id('agents.empty')
        if (!this.workspace.hasRows() && !this.workspace.loading && this.workspace.sourceProblem()) ListItem() {
          Text(tr(this.workspace.sourceProblem())).fontSize(Tokens.meta).fontColor(Colors.secondary).padding(Tokens.page).width('100%')
        }.id('agents.unavailable')
      }.width('100%').height('100%').padding({ bottom: Tokens.listTail })
        .divider({ strokeWidth: Tokens.hairline, startMargin: Tokens.page + Tokens.avatar + Tokens.gap,
          endMargin: Tokens.page, color: Colors.divider })
      }.width('100%').layoutWeight(1).onRefreshing(() => { this.workspace.setReadingDetail(false); this.workspace.refresh(this.model).finally(() => { this.workspace.setReadingDetail(this.readingDetail); this.refreshing = false; }); })
      .bindSheet($$this.filterSheetOpen, this.filterSheet(), {
        title: { title: tr('agent_filters_title') }, height: SheetSize.FIT_CONTENT, showClose: true,
        onAppear: () => { this.filterReady = true; this.measureFilters(); }, onDisappear: () => this.filterReady = false
      })
    }.width('100%').height('100%').backgroundColor(Colors.canvas)
      .bindSheet($$this.editorOpen, this.renameForm(), { title: { title: tr('rename') }, height: SheetSize.MEDIUM, showClose: true })
  }
}
