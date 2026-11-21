import { IconActions } from '../design/IconActions';
import { util } from '@kit.ArkTS';
import { ClientModel } from '../models/ClientModel';
import { AgentWorkspace, AgentWorkspaceGroup } from '../models/AgentWorkspace';
import { ResourceRow, Wire } from '../models/Wire';
import { BoundRow, reconcileBoundRows } from '../models/RowBinding';
import { Tokens } from '../design/Tokens';
import { Colors } from '../design/Colors';
import { ButtonStyles, SelectStyles } from '../design/Controls';
import { Symbols } from '../design/Symbols';
import { tr } from '../design/Strings';
import { nativeEnvironment, NativeEnvironment } from '../platform/NativeEnvironment';
import { nativeSheetInset } from '../platform/NativeInsets';
import { DeviceLook, deviceLook } from '../models/DeviceLook';

const QUICK_FILTERS: string[] = ['all', 'needsYou', 'unread', 'working', 'backgroundWaiting'];
const QUICK_LABELS: string[] = ['all', 'needs_you', 'agent_group_unread', 'working', 'agent_group_background'];
const GROUPINGS: string[] = ['state', 'machine', 'space'];
const GROUP_LABELS: Record<string, string> = {'state': 'agent_group_state', 'machine': 'agent_filter_machine', 'space': 'agent_group_space'};
const STATE_LABELS: Record<string, string> = {'pinned': 'agent_group_pinned', 'state/needs': 'needs_you',
  'state/unread': 'agent_group_unread', 'state/awaiting': 'agent_group_awaiting', 'state/working': 'working',
  'state/background': 'agent_group_background', 'state/just_finished': 'agent_group_just_finished',
  'state/waiting': 'agent_group_waiting', 'state/muted': 'agent_group_muted',
  'state/idle': 'agent_group_idle'};

@ObservedV2
class AgentRowPresentation {
  @Trace raw: string = '';
  @Trace lastText: string = '';
  @Trace rawStatus: string = '';
  @Trace updatedAt: number = Number.NaN;
  @Trace worktree: string = '';
  @Trace repositoryRoot: string = '';
  @Trace cwd: string = '';
  @Trace machineId: string = '';
  @Trace sessionId: string = '';
  @Trace spaceName: string = '';
  @Trace recentContent: string = '';
  @Trace provider: string = '';
  @Trace brand: string = '';
  @Trace status: string = '';
  @Trace metadata: string[] = [];
  @Trace selectionKey: string = '';
}

@ObservedV2
class BoundAgentRow {
  readonly identity: string;
  readonly key: string;
  @Trace bound: BoundRow<ResourceRow>;
  @Trace first: boolean = false;
  @Trace last: boolean = false;
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

@ComponentV2
export struct AgentListPage {
  @Require @Param model: ClientModel;
  @Param rail: boolean = false;
  @Param compactRoot: boolean = false;
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
  @Local searchOpen: boolean = false;
  private agentScroller: Scroller = new Scroller();
  private pullY: number = -1;
  private pullX: number = 0;
  private revealedDuringPull: boolean = false;
  private focusSearch: boolean = false;
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

  aboutToAppear(): void { this.syncRowBindings(); if (this.active) this.activateWorkspace(); }
  aboutToDisappear(): void { this.resetSearchReveal(); this.editorOpen = false; this.filterSheetOpen = false; this.editingAdmission = undefined; this.workspace.revokeRowActions(); if (!this.retainWorkspace) this.workspace.hide(); }

  @Monitor('active') visibleChanged(): void { if (!this.active) { this.resetSearchReveal(); this.editorOpen = false; this.filterSheetOpen = false; this.editingRow = undefined; this.editingAdmission = undefined; this.workspace.revokeRowActions(); if (!this.retainWorkspace) this.workspace.hide(); } else this.activateWorkspace(); }
  @Monitor('model.forgottenEpoch') forgottenChanged(): void { this.workspace.forgetHub(this.model.forgottenProfile); if (this.editingRow?.group === this.model.forgottenProfile) { this.editorOpen = false; this.editingRow = undefined; this.editingAdmission = undefined; } }
  @Monitor('model.profile') profileChanged(): void { this.resetSearchReveal(); this.editorOpen = false; this.editingAdmission = undefined; this.syncRowBindings(); if (!this.active) return; this.activateWorkspace(); }
  @Monitor('selectedRow') externalSelectionChanged(): void { this.workspace.selectExternal(this.selectedRow); }
  @Monitor('model.directorySessions', 'model.directoryReady') directoryChanged(): void { if (!this.active) return; this.workspace.update(this.model, this.readingDetail); this.workspace.selectExternal(this.selectedRow); }
  @Monitor('model.sessionsReady') catalogChanged(): void { if (!this.active) return; this.workspace.update(this.model, this.readingDetail); }
  @Monitor('model.sessions') sessionsChanged(): void { if (!this.active) return; this.workspace.update(this.model, this.readingDetail); }
  @Monitor('model.selected') selectionChanged(): void { if (!this.active) return; this.workspace.update(this.model, this.readingDetail); }
  @Monitor('model.machines') machinesChanged(): void { if (!this.active) return; this.workspace.update(this.model, this.readingDetail); }
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
      const occurrence = occurrences.get(group.id) || 0;
      occurrences.set(group.id, occurrence + 1);
      const identity = JSON.stringify([group.id, occurrence]);
      const matches = available.get(identity);
      const existing = matches && matches.length ? matches.shift() : undefined;
      let binding: BoundAgentGroup;
      if (existing) {
        binding = existing; binding.value = group;
        const groupRows = reconcileBoundRows(binding.rows.map((rowBinding: BoundAgentRow) => rowBinding.bound), group.rows,
          (row: ResourceRow) => row.key(), this.model, this.model, scope, scope);
        binding.rows = this.bindAgentRows(binding.rows, groupRows, true);
      } else {
        this.groupBindingSequence++;
        binding = new BoundAgentGroup(identity, 'agent-group-binding.' + String(this.groupBindingSequence), group);
        const groupRows = reconcileBoundRows([], group.rows, (row: ResourceRow) => row.key(), undefined, this.model, '', scope);
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
    rows.forEach((bound: BoundRow<ResourceRow>, index: number) => {
      let binding = available.get(bound.identity);
      if (binding) { available.delete(bound.identity); binding.bound = bound; }
      else binding = new BoundAgentRow(bound);
      binding.first = index === 0; binding.last = index === rows.length - 1;
      if (project) this.updateAgentRowPresentation(binding.presentation, bound.value);
      result.push(binding);
    });
    return result;
  }

  private updateAgentRowPresentation(presentation: AgentRowPresentation, row: ResourceRow): void {
    if (presentation.raw !== row.raw) {
      const data = Wire.parse(row.raw);
      presentation.raw = row.raw;
      presentation.lastText = Wire.text(data, 'last_text');
      const agent = (Wire.text(data, 'agent') || Wire.text(data, 'agent_type')).toLowerCase();
      const label = Wire.text(data, 'agent_label') || Wire.text(data, 'launcher');
      const known = agent || label.toLowerCase();
      presentation.brand = known === 'codex' || known === 'openai' ? 'codex' :
        (known === 'claude' || known === 'claude-code' || known === 'claude code' ? 'claude' : '');
      presentation.provider = label || (presentation.brand === 'codex' ? 'Codex' : presentation.brand === 'claude' ? 'Claude' : agent);
      presentation.rawStatus = Wire.text(data, 'status');
      presentation.updatedAt = Date.parse(Wire.text(data, 'updated_at'));
      presentation.worktree = Wire.text(data, 'worktree');
      presentation.repositoryRoot = Wire.text(data, 'repo_root');
      presentation.cwd = Wire.text(data, 'cwd');
      presentation.machineId = Wire.text(data, 'machine_id');
      presentation.sessionId = Wire.text(data, 'session_id');
      presentation.spaceName = Wire.text(data, 'space_name');
    }
    const state = row.state || presentation.rawStatus;
    const content = presentation.lastText || row.detail;
    const machineId = row.machine || presentation.machineId;
    const sessionId = row.session || presentation.sessionId || row.id;
    const path = row.path || presentation.worktree || presentation.repositoryRoot || presentation.cwd;
    const owner = this.model.directoryOwnerFor(row);
    const machine = machineId ? owner?.machineName(machineId) || machineId : '';
    const identity = [this.model.groupName(row), row.manager].filter((value: string) => !!value).join(' · ');
    const shortPath = path ? this.model.shortPath(path) : '';
    presentation.status = state;
    presentation.recentContent = content;
    presentation.metadata = [identity, machine, shortPath, state, content];
    presentation.selectionKey = JSON.stringify(row.group ? [row.group, row.kind, row.manager, machineId, sessionId] : [row.kind, row.manager, machineId, sessionId]);
  }

  private machineOptions(): ResourceRow[] {
    return (this.model.directoryReady ? this.model.directoryMachines : this.model.machines)
      .filter((row: ResourceRow) => this.workspace.includesHub(row.group));
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
    this.machineOptions().forEach((row: ResourceRow) => options.push({ value: [this.model.groupName(row), row.manager, row.title || row.id].filter((value: string) => !!value).join(' · ') }));
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
    Column({ space: 4 }) {
      if (binding.value.parent === 'quiet') {
        Text(tr('agent_group_quiet_spaces')).fontSize(15).fontWeight(FontWeight.Medium).fontColor(Colors.ink).width('100%')
      }
      Button({ type: ButtonType.Normal }) {
        Row({ space: 6 }) {
          Text(tr(STATE_LABELS[binding.value.id] || '') || binding.value.machineName)
            .fontSize(15).fontWeight(FontWeight.Medium).fontColor(Colors.ink).flexShrink(1).maxLines(this.environment.fontScale >= 1.5 ? 4 : 2)
          Text(String(binding.value.totalRowCount)).fontSize(13).fontColor(Colors.secondary)
          Blank().layoutWeight(1)
          SymbolGlyph(binding.value.folded ? Symbols.expand : Symbols.down).fontSize('12vp').fontColor([Colors.secondary])
        }.width('100%').constraintSize({ minHeight: 44 })
      }.width('100%').padding({ left: 4, right: 5 }).backgroundColor(Color.Transparent)
        .accessibilityText((tr(STATE_LABELS[binding.value.id] || '') || binding.value.machineName) + ' · ' +
          String(binding.value.totalRowCount) + ' · ' + tr(binding.value.folded ? 'agent_expand' : 'agent_collapse'))
        .onClick(() => this.workspace.toggleFold(binding.value.id)).id('agents.group.toggle.' + binding.value.id)
    }.width('100%').padding({ top: 6 }).alignItems(HorizontalAlign.Start)
  }

  private rowStatus(presentation: AgentRowPresentation): string {
    return presentation.status ? tr(presentation.status) : '';
  }
  private needsStatusLabel(groupId: string): boolean {
    return groupId !== 'pinned' && (!groupId.startsWith('state/') || groupId === 'state/muted');
  }

  private rowStatusColor(presentation: AgentRowPresentation): ResourceColor {
    const state = presentation.status;
    if (state === 'needs_you') return Colors.needs;
    if (state === 'working') return Colors.working;
    if (state === 'unread') return Colors.unread;
    return Colors.secondary;
  }

  private rowTime(presentation: AgentRowPresentation): string {
    if (!Number.isFinite(presentation.updatedAt)) return '';
    const elapsed = Math.floor((Date.now() - presentation.updatedAt) / 1000);
    if (elapsed < 0) return '';
    if (elapsed < 60) return tr('agent_time_just_now');
    if (elapsed < 3600) return String(Math.floor(elapsed / 60)) + tr('agent_time_minute_suffix');
    if (elapsed < 86400) return String(Math.floor(elapsed / 3600)) + tr('agent_time_hour_suffix');
    return String(Math.floor(elapsed / 86400)) + tr('agent_time_day_suffix');
  }

  @Builder filterSheet() {
    Column() {
      if (this.model.profiles.length > 1 || this.workspace.currentHubOnly) {
        Row({ space: Tokens.page }) {
          Text(tr('phone_agents_current_hub')).fontSize(Tokens.body).fontColor(Colors.ink).layoutWeight(1)
          Toggle({ type: ToggleType.Switch, isOn: this.workspace.currentHubOnly })
            .accessibilityText(tr('phone_agents_current_hub')).id('agents.current_hub')
            .onChange((value: boolean) => this.workspace.setCurrentHubOnly(value))
        }.width('100%').constraintSize({ minHeight: Tokens.header }).alignItems(VerticalAlign.Center)
        Divider().strokeWidth(Tokens.hairline).color(Colors.divider)
      }
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
  @Builder agentRow(binding: BoundAgentRow, groupId: string) {
    ListItem() {
      Column() {
        Row({ space: 10 }) {
          Stack({ alignContent: Alignment.BottomEnd }) {
            Stack() {
              if (this.rail) Text((binding.bound.value.title || tr('agent_untitled')).slice(0, 2)).fontSize('13vp').fontColor(Colors.ink).maxLines(1)
              else if (binding.presentation.brand === 'codex') {
                Image($r('app.media.project_agent_openai')).width(21).height(21).objectFit(ImageFit.Contain).fillColor(Colors.ink)
              } else if (binding.presentation.brand === 'claude') {
                Image($r('app.media.project_agent_claude')).width(22).height(22).objectFit(ImageFit.Contain)
              } else if (binding.presentation.provider) {
                Text(binding.presentation.provider.slice(0, 1).toUpperCase()).fontSize('16vp').fontWeight(FontWeight.Bold).fontColor(Colors.ink)
              } else SymbolGlyph(Symbols.agent).fontSize('18vp').fontColor([this.rowStatusColor(binding.presentation)])
            }.width(32).height(32).backgroundColor(Colors.control).borderRadius(9)
            if (binding.presentation.status === 'needs_you' || binding.presentation.status === 'unread') {
              Circle().width(8).height(8).fill(this.rowStatusColor(binding.presentation))
                .stroke(Colors.surface).strokeWidth(1.5)
            }
          }.width(34).height(34)
          if (!this.rail) Column({ space: 3 }) {
            Row({ space: 6 }) {
              Text(binding.bound.value.title || tr('agent_untitled')).fontSize(17).fontWeight(FontWeight.Bold)
                .fontColor(Colors.ink).layoutWeight(1).maxLines(this.environment.fontScale >= 1.5 ? 4 : 1)
                .textOverflow({ overflow: TextOverflow.Ellipsis })
              if (this.environment.fontScale < 1.5) Text(this.rowTime(binding.presentation)).fontSize(12).fontColor(Colors.secondary)
            }.width('100%').alignItems(VerticalAlign.Center)
            if (binding.presentation.recentContent) {
              Text(binding.presentation.recentContent).fontSize(15).fontColor(Colors.secondary).width('100%')
                .maxLines(this.environment.fontScale >= 1.5 ? 4 : this.look.density === 'compact' ? 1 : 2).textOverflow({ overflow: TextOverflow.Ellipsis })
            }
            this.rowContext(this.rowPlace(binding.presentation), this.needsStatusLabel(groupId) ? this.rowStatus(binding.presentation) : '',
              this.rowStatusColor(binding.presentation))
            if (this.environment.fontScale >= 1.5 && this.rowTime(binding.presentation)) {
              Text(this.rowTime(binding.presentation)).fontSize(13).fontColor(Colors.secondary)
            }
          }.alignItems(HorizontalAlign.Start).layoutWeight(1)
          if (!this.rail) SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
        }.width('100%').alignItems(VerticalAlign.Center).justifyContent(this.rail ? FlexAlign.Center : FlexAlign.Start)
          .padding({ left: this.rail ? 5 : 13, right: this.rail ? 5 : 11,
            top: this.look.density === 'compact' ? 7 : 10, bottom: this.look.density === 'compact' ? 7 : 10 })
        if (!binding.last && !this.rail) Divider().strokeWidth(0.5).color(Colors.divider).margin({ left: 57 })
      }.width('100%')
    }.accessibilityGroup(true).accessibilityText([binding.bound.value.title || tr('agent_untitled'),
      binding.presentation.recentContent, ...binding.presentation.metadata.slice(0, 2), this.rowStatus(binding.presentation)].filter((s: string) => !!s).join(' · '))
      .selectable(true).selected(this.isRowSelected(binding))
      .backgroundColor(this.isRowSelected(binding) ? Colors.selection : Colors.surface)
      .borderRadius({ topLeft: binding.first && !this.rail ? 13 : 0, topRight: binding.first && !this.rail ? 13 : 0,
        bottomLeft: binding.last && !this.rail ? 13 : 0, bottomRight: binding.last && !this.rail ? 13 : 0 })
      .onClick(() => this.openRow(binding.bound.value))
      .onVisibleAreaChange([0, 1], (visible: boolean, ratio: number) => this.onVisible(binding.bound.value, visible ? ratio : 0))
      .onDisAppear(() => this.onVisible(binding.bound.value, 0))
      .bindContextMenu(this.rowMenu(binding), ResponseType.LongPress)
      .id('agent.' + binding.bound.value.group + '.' + binding.bound.value.manager + '.' + binding.bound.value.machine + '.' + binding.bound.value.id)
  }

  @Builder rowContext(place: string, status: string, color: ResourceColor) {
    if ((place || status) && this.environment.fontScale >= 1.5) {
      Column({ space: 3 }) {
        if (status) Text(status).fontSize(12).fontWeight(FontWeight.Medium).fontColor(color)
        if (place) Text(place).fontSize(12).fontColor(Colors.secondary).maxLines(3).textOverflow({overflow:TextOverflow.Ellipsis})
      }.width('100%').alignItems(HorizontalAlign.Start)
    } else if (place || status) {
      Row({ space: 5 }) {
        if (status) Text(status).fontSize(12).fontWeight(FontWeight.Medium).fontColor(color).maxLines(1)
        if (place) Text(place).fontSize(12).fontColor(Colors.secondary).layoutWeight(1).maxLines(1)
          .textOverflow({ overflow: TextOverflow.Ellipsis })
      }.width('100%')
    }
  }

  private rowPlace(presentation: AgentRowPresentation): string {
    const parts: string[] = [];
    if (presentation.provider) parts.push(presentation.brand === 'claude' ? 'Claude' : presentation.brand === 'codex' ? 'Codex' : presentation.provider);
    if (this.mixedMachines && this.workspace.groupingMode !== 'machine' && presentation.metadata[1]) parts.push(presentation.metadata[1]);
    const folder = presentation.spaceName || (presentation.worktree || presentation.cwd || presentation.repositoryRoot).split(/[\\/]/).filter((part: string) => !!part).pop();
    if (folder && !parts.includes(folder)) parts.push(folder);
    return parts.join(' · ');
  }

  private searchVisible(): boolean { return !this.compactRoot || this.searchOpen || !!this.workspace.search; }
  private resetSearchReveal(): void {
    this.searchOpen = false; this.focusSearch = false; this.pullY = -1; this.revealedDuringPull = false;
  }
  private trackPull(event: TouchEvent): void {
    if (event.type === TouchType.Down) this.revealedDuringPull = false;
    if (!this.compactRoot || !this.active || this.searchVisible()) return;
    if (event.type === TouchType.Down) {
      let top = false;
      try { top = this.agentScroller.currentOffset().yOffset <= 1; } catch (_) { }
      if (top && event.touches.length === 1) {
        this.pullX = event.touches[0].windowX; this.pullY = event.touches[0].windowY;
      } else this.pullY = -1;
    } else if (event.type === TouchType.Move && event.touches.length === 1 && this.pullY >= 0) {
      const dy = event.touches[0].windowY - this.pullY, dx = Math.abs(event.touches[0].windowX - this.pullX);
      if (dy >= 48 && dy > 1.5 * dx) { this.revealedDuringPull = true; this.searchOpen = true; this.pullY = -1; }
    } else this.pullY = -1;
  }
  private refreshAgents(): void {
    if (this.revealedDuringPull) { this.revealedDuringPull = false; this.refreshing = false; return; }
    this.workspace.setReadingDetail(false);
    this.workspace.refresh(this.model).finally(() => {
      if (this.active && this.workspace.isBoundTo(this.model)) this.workspace.setReadingDetail(this.readingDetail);
      this.refreshing = false;
    });
  }
  @Builder searchControl() {
    Search({ value: this.workspace.search, placeholder: tr('search_sessions') })
      .height(Math.max(38, Tokens.lineHeight * this.environment.fontScale + 10))
      .textFont({ size: Tokens.body }).placeholderFont({ size: Tokens.body }).caretStyle({ color: Colors.action })
      .onChange((value: string) => { if (this.compactRoot) this.searchOpen = true; this.workspace.setSearch(value); })
      .onAppear(() => {
        if (!this.focusSearch) return;
        this.focusSearch = false;
        setTimeout(() => { if (this.active && this.searchOpen) this.getUIContext().getFocusController().requestFocus('agents.search'); }, 0);
      }).id('agents.search')
  }
  @Builder filterControl() {
    Button({ type: ButtonType.Circle }) { SymbolGlyph(Symbols.filter).fontSize(Tokens.symbolFont).fontColor([Colors.link]) }.attributeModifier(ButtonStyles.chrome).accessibilityText(tr('agent_filter_button'))
      .onClick(() => this.filterSheetOpen = true).id('agents.filter')
  }
  @Builder filters() {
    if (!this.rail) Column({ space: 6 }) {
      if (this.searchVisible()) {
        if (this.compactRoot) Row({space: 6}) {
          Column() { this.searchControl() }.layoutWeight(1)
          Button(tr('cancel'), {type:ButtonType.Normal}).fontSize(15).maxFontScale(Tokens.navigationTextMaxScale).fontColor(Colors.link).backgroundColor(Color.Transparent)
            .padding({left:4,right:4}).onClick(() => { this.workspace.setSearch(''); this.resetSearchReveal(); }).id('agents.search_cancel')
        }.width('100%')
        else this.searchControl()
      }
      Row({ space: 6 }) {
        Scroll() {
          Row({ space: 3 }) {
            ForEach(QUICK_FILTERS, (key: string, index: number) => {
              Button({ type: ButtonType.Capsule }) {
                Row({space:4}) {
                  if (key !== 'all') Circle().width(5).height(5)
                    .fill(key === 'needsYou' ? Colors.needs : key === 'unread' ? Colors.unread : key === 'working' ? Colors.working : Colors.secondary)
                  Text(tr(QUICK_LABELS[index])).fontSize(14).maxFontScale(Tokens.navigationTextMaxScale)
                    .fontWeight(this.workspace.quickFilter === key ? FontWeight.Bold : FontWeight.Normal)
                    .fontColor(this.workspace.quickFilter === key ? Colors.link : Colors.ink).maxLines(1)
                  if (Wire.number(this.workspace.filterCounts,key)>0) Text(String(Wire.number(this.workspace.filterCounts,key)))
                    .fontSize(13).maxFontScale(Tokens.navigationTextMaxScale).fontWeight(FontWeight.Medium).fontColor(Colors.secondary)
                }
              }
                .height(Math.max(32, 23 * Math.min(this.environment.fontScale, Tokens.navigationTextMaxScale))).padding({ left: 11, right: 11 })
                .backgroundColor(this.workspace.quickFilter === key ? Colors.selection : Colors.control)
                .accessibilityText(tr(QUICK_LABELS[index]))
                .accessibilityDescription(Wire.number(this.workspace.filterCounts,key)>0 ? String(Wire.number(this.workspace.filterCounts,key)) : '')
                .accessibilitySelected(this.workspace.quickFilter === key)
                .responseRegion({ x: 0, y: -6, width: '100%', height: Math.max(44, 23 * Math.min(this.environment.fontScale, Tokens.navigationTextMaxScale) + 12) })
                .onClick(() => { if (this.workspace.quickFilter !== key) this.workspace.setQuickFilter(key); }).id('agents.quick.' + key)
            }, (key: string) => key)
          }.padding({left:1,right:1})
        }.layoutWeight(1).constraintSize({ minHeight: 44 }).scrollable(ScrollDirection.Horizontal).scrollBar(BarState.Off)
          .id('agents.quick')
        if (this.compactRoot && !this.searchVisible()) {
          Button({type:ButtonType.Circle}) {
            SymbolGlyph($r('sys.symbol.magnifyingglass')).fontSize(Tokens.symbolFont).fontColor([Colors.link])
          }.attributeModifier(ButtonStyles.chrome).accessibilityText(tr('search_sessions'))
            .onClick(() => { this.focusSearch = true; this.searchOpen = true; }).id('agents.search_reveal')
        }
        this.filterControl()
      }.width('100%')
      if (this.workspace.machineFilter) {
        Button({ type: ButtonType.Capsule }) {
          Row({ space: 5 }) {
            SymbolGlyph($r('sys.symbol.xmark')).fontSize('11vp').fontColor([Colors.link])
            Text(this.machineLabel()).fontSize(13).maxFontScale(Tokens.navigationTextMaxScale).fontColor(Colors.link).flexShrink(1).maxLines(2)
          }
        }.constraintSize({minHeight:44,maxWidth:'100%'}).padding({ left: 10, right: 10, top:3, bottom:3 }).backgroundColor(Colors.surface)
          .accessibilityText(this.machineLabel() + ' · ' + tr('clear')).onClick(() => this.selectMachine(0)).id('agents.machine.clear')
      }
    }.alignItems(HorizontalAlign.Start).padding({ left: Tokens.page, right: Tokens.page, top: 6, bottom: 4 }).width('100%')
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
      List({scroller:this.agentScroller}) {
        if (this.workspace.loading && !this.workspace.hasRows()) ListItem() { LoadingProgress().height(Tokens.touch).width(Tokens.touch).padding(Tokens.page) }
        ForEach(this.groupBindings, (binding: BoundAgentGroup) => {
            ListItemGroup({ header: () => this.groupHeader(binding) }) {
              ForEach(binding.rows, (rowBinding: BoundAgentRow) => this.agentRow(rowBinding, binding.value.id), (rowBinding: BoundAgentRow) =>
                rowBinding.key)
            }.id('agents.group.' + binding.value.id)
            if (binding.value.totalRowCount > binding.value.rows.length && !binding.value.folded) ListItem() {
              Button() {
                Row({ space: Tokens.gap }) {
                  Text(String(binding.value.totalRowCount - binding.value.rows.length) + ' ' + tr('agent_group_more_idle')).fontSize(Tokens.meta).fontColor(Colors.link).layoutWeight(1)
                  SymbolGlyph(Symbols.down).fontSize(Tokens.symbolFont).fontColor([Colors.link])
                }.width('100%').padding(Tokens.page).constraintSize({ minHeight: Tokens.touch })
              }.type(ButtonType.Normal).buttonStyle(ButtonStyleMode.TEXTUAL).backgroundColor(Color.Transparent).width('100%')
                .onClick(() => this.workspace.toggleIdleRows(binding.value.id)).id('agents.idle.expand.' + binding.value.id)
            }
          }, (binding: BoundAgentGroup) => binding.key)
        if (!this.workspace.hasRows() && !this.workspace.loading) ListItem() {
          Text(tr(this.workspace.search ? 'no_results' : 'empty')).fontSize(Tokens.meta).padding(Tokens.page).width('100%')
        }.id('agents.empty')
      }.width('100%').height('100%').padding({ left: this.rail ? 0 : 12, right: this.rail ? 0 : 12, bottom: Tokens.listTail }).backgroundColor(Colors.grouped)
        .onTouch((event: TouchEvent) => this.trackPull(event))
      }.width('100%').layoutWeight(1).onRefreshing(() => this.refreshAgents())
      .bindSheet($$this.filterSheetOpen, this.filterSheet(), {
        title: { title: tr('agent_filters_title') }, height: SheetSize.FIT_CONTENT, showClose: true,
        onAppear: () => { this.filterReady = true; this.measureFilters(); }, onDisappear: () => this.filterReady = false
      })
    }.width('100%').height('100%').backgroundColor(Colors.grouped)
      .bindSheet($$this.editorOpen, this.renameForm(), { title: { title: tr('rename') }, height: SheetSize.MEDIUM, showClose: true })
  }
}
