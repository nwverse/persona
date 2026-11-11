import { CapsuleSegmentButtonV2, ComposeListItemV2, ContentItemV2, SegmentButtonV2Items } from '@kit.ArkUI';
import { ClientModel } from '../models/ClientModel';
import { AgentWorkspace, AgentWorkspaceGroup } from '../models/AgentWorkspace';
import { ResourceRow, Wire } from '../models/Wire';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';

const QUICK_FILTERS: string[] = ['all', 'needs_you', 'working', 'unread'];

@ComponentV2
export struct AgentListPage {
  @Require @Param model: ClientModel;
  @Param rail: boolean = false;
  @Param readingDetail: boolean = false;
  @Param active: boolean = true;
  private editingAdmission: (() => boolean) | undefined;
  @Param selectedRow: ResourceRow = new ResourceRow();
  @Local refreshing: boolean = false;
  @Event onOpen: (row: ResourceRow) => void = () => {};
  @Event onVisible: (row: ResourceRow, ratio: number) => void = () => {};
  @Local workspace: AgentWorkspace = new AgentWorkspace();
  @Local editorOpen: boolean = false;
  @Local editingRow: ResourceRow | undefined;
  @Local renameTitle: string = '';
  @Local renameTab: string = '';

  aboutToAppear(): void { if (!this.active) return; this.workspace.open(this.model, this.readingDetail).then(() => this.workspace.selectExternal(this.selectedRow)).catch(() => undefined); }
  aboutToDisappear(): void { this.editorOpen = false; this.editingAdmission = undefined; this.workspace.hide(); }

  @Monitor('active') visibleChanged(): void { if (!this.active) { this.editorOpen = false; this.editingRow = undefined; this.editingAdmission = undefined; this.workspace.hide(); } else this.workspace.open(this.model, this.readingDetail).then(() => this.workspace.selectExternal(this.selectedRow)).catch(() => undefined); }
  @Monitor('model.forgottenEpoch') forgottenChanged(): void { this.workspace.forgetHub(this.model.forgottenProfile); if (this.editingRow?.group === this.model.forgottenProfile) { this.editorOpen = false; this.editingRow = undefined; this.editingAdmission = undefined; } }
  @Monitor('model.profile') profileChanged(): void { this.editorOpen = false; this.editingAdmission = undefined; if (!this.active) return; this.workspace.open(this.model, this.readingDetail).then(() => this.workspace.selectExternal(this.selectedRow)).catch(() => undefined); }
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
  @Monitor('readingDetail') detailChanged(): void { this.workspace.setReadingDetail(this.readingDetail); }

  private quickIndex(): number {
    const index = QUICK_FILTERS.indexOf(this.workspace.quickFilter);
    return index < 0 ? 0 : index;
  }

  private machineOptions(): ResourceRow[] {
    return this.model.directoryReady ? this.model.directoryMachines : this.model.machines;
  }

  private machineLabel(): string {
    if (!this.workspace.machineFilter) return tr('agent_filter_all_machines');
    const row = this.machineOptions().find((item: ResourceRow) => this.workspace.machineIdentity(item) === this.workspace.machineFilter || item.id === this.workspace.machineFilter);
    return row?.title || this.workspace.machineFilter;
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

  @Builder rowMenu(row: ResourceRow) {
    Menu() {
      MenuItem({ content: tr(this.workspace.isPinned(row) ? 'agent_unpin' : 'agent_pin') })
        .enabled(this.workspace.rowOnline(row) && !this.workspace.busy)
        .onClick(() => this.workspace.togglePin(row).catch(() => undefined))
      MenuItem({ content: tr(this.workspace.isMuted(row) ? 'unmute' : 'mute') })
        .enabled(this.workspace.rowOnline(row) && !this.workspace.busy)
        .onClick(() => this.workspace.toggleMute(row).catch(() => undefined))
      if (this.workspace.isSpacePinned(row) || Wire.text(row.data(), 'space_name')) {
        MenuItem({ content: tr(this.workspace.isSpacePinned(row) ? 'agent_unpin_space' : 'agent_pin_space') })
          .enabled(this.workspace.rowOnline(row) && !this.workspace.busy)
          .onClick(() => this.workspace.togglePin(row, true).catch(() => undefined))
        MenuItem({ content: tr(this.workspace.isSpaceMuted(row) ? 'agent_unmute_space' : 'agent_mute_space') })
          .enabled(this.workspace.rowOnline(row) && !this.workspace.busy)
          .onClick(() => this.workspace.toggleMute(row, true).catch(() => undefined))
      }
      if (this.workspace.canMarkSeen(row)) MenuItem({ content: tr('agent_mark_seen') })
        .enabled(!this.workspace.busy).onClick(() => this.workspace.markSeen(row).catch(() => undefined))
      if (this.workspace.canRename(row)) MenuItem({ content: tr('rename') }).enabled(!this.workspace.busy)
        .onClick(() => this.openRename(row))
      if (this.workspace.canClose(row)) MenuItem({ content: tr('close') }).enabled(!this.workspace.busy)
        .onClick(() => this.confirmClose(row))
      if (this.workspace.canDelete(row)) MenuItem({ content: tr('delete') }).enabled(!this.workspace.busy)
        .onClick(() => this.confirmDelete(row))
    }
  }

  @Builder groupHeader(group: AgentWorkspaceGroup) {
    Column({ space: Tokens.fine }) {
      if (group.showMachine) Text(group.machineName || group.machineId).fontSize(Tokens.meta).fontWeight(FontWeight.Medium)
        .fontColor($r('sys.color.font_secondary')).width('100%')
      if (group.showRepository) Text(group.repositoryName || tr('agent_filter_repository')).fontSize(Tokens.meta)
        .fontColor($r('sys.color.font_secondary')).width('100%')
      if (group.showWorktree) Text(group.branch || group.worktreeName || tr('agent_filter_worktree')).fontSize(Tokens.meta)
        .fontWeight(FontWeight.Medium).width('100%')
    }.alignItems(HorizontalAlign.Start)
      .padding({ left: Tokens.page, right: Tokens.page, top: Tokens.inset, bottom: Tokens.fine }).width('100%')
  }

  private rowSubtitle(row: ResourceRow): string {
    const metadata = this.workspace.rowMetadata(row);
    if (metadata[3]) metadata[3] = tr(metadata[3]);
    return metadata.filter((value: string) => !!value).join(' · ');
  }

  @Builder groupingMenu() {
    Menu() {
      MenuItem({ content: tr('agent_group_flat') }).selected(this.workspace.groupingMode === 'flat').selectIcon(true).onClick(() => this.workspace.setGroupingMode('flat'))
      MenuItem({ content: tr('agent_filter_machine') }).selected(this.workspace.groupingMode === 'machine').selectIcon(true).onClick(() => this.workspace.setGroupingMode('machine'))
      MenuItem({ content: tr('agent_filter_repository') }).selected(this.workspace.groupingMode === 'repository').selectIcon(true).onClick(() => this.workspace.setGroupingMode('repository'))
      MenuItem({ content: tr('agent_filter_worktree') }).selected(this.workspace.groupingMode === 'worktree').selectIcon(true).onClick(() => this.workspace.setGroupingMode('worktree'))
    }
  }

  @Builder agentRow(row: ResourceRow) {
    ListItem() {
      if (this.rail) Text((row.title || row.id).slice(0, 1)).fontSize(Tokens.body).height(Tokens.touch).width('100%')
        .textAlign(TextAlign.Center)
      else ComposeListItemV2({ contentItemV2: new ContentItemV2({ primaryText: row.title || row.id,
        secondaryText: this.rowSubtitle(row) || undefined }) })
    }.selectable(true).selected(this.workspace.isSelected(row))
      .backgroundColor(this.workspace.isSelected(row)
        ? $r('sys.color.comp_background_tertiary') : Color.Transparent)
      .onClick(() => this.openRow(row))
      .onVisibleAreaChange([0, 1], (visible: boolean, ratio: number) => this.onVisible(row, visible ? ratio : 0))
      .onDisAppear(() => this.onVisible(row, 0))
      .bindContextMenu((): void => this.rowMenu(row), ResponseType.LongPress)
      .id('agent.' + row.group + '.' + row.manager + '.' + row.machine + '.' + row.id)
  }

  @Builder filters() {
    if (!this.rail) {
      Column({ space: Tokens.gap }) {
        Search({ value: this.workspace.search, placeholder: tr('search_sessions') })
          .onChange((value: string) => this.workspace.setSearch(value)).id('agents.search')
        CapsuleSegmentButtonV2({ items: new SegmentButtonV2Items([
          { text: tr('all') }, { text: tr('needs_you') }, { text: tr('working') }, { text: tr('unread') }
        ]), selectedIndex: this.quickIndex(), $selectedIndex: (index: number) => this.workspace.setQuickFilter(QUICK_FILTERS[index]) })
        Row({ space: Tokens.gap }) {
          Select(this.machineSelectOptions())
            .value(this.machineLabel()).layoutWeight(1)
            .onSelect((index: number) => this.selectMachine(index)).id('agents.machine_filter')
          if (this.workspace.historySupported()) {
            Row({ space: Tokens.fine }) {
              Text(tr('agent_history')).fontSize(Tokens.meta)
              Toggle({ type: ToggleType.Switch, isOn: this.workspace.history }).onChange((value: boolean) => this.workspace.setHistory(value))
                .accessibilityText(tr('agent_history')).id('agents.history')
            }.alignItems(VerticalAlign.Center)
          }
        }.width('100%')
        Button(tr('grouping')).bindMenu(this.groupingMenu).height(Tokens.touch)
          .id('agents.grouping')
      }.padding({ left: Tokens.inset, right: Tokens.inset, top: Tokens.inset, bottom: Tokens.gap }).width('100%')
    }
  }

  @Builder renameForm() {
    Column({ space: Tokens.gap }) {
      TextInput({ text: this.renameTitle, placeholder: tr('agent_title') }).onChange((value: string) => this.renameTitle = value)
        .id('agents.rename_title')
      TextInput({ text: this.renameTab, placeholder: tr('agent_tab_name') }).onChange((value: string) => this.renameTab = value)
        .id('agents.rename_tab')
      Row({ space: Tokens.gap }) {
        Button(tr('cancel')).layoutWeight(1).height(Tokens.touch).onClick(() => this.editorOpen = false)
        Button(tr('save')).layoutWeight(1).height(Tokens.touch).enabled(!this.workspace.busy)
          .onClick(() => this.saveRename()).id('agents.rename_save')
      }.width('100%')
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
        if (this.workspace.loading && !this.workspace.hasRows()) ListItem() { LoadingProgress().height(Tokens.touch).width(Tokens.touch).padding(Tokens.page) }
        if (this.workspace.groupingMode === 'flat') {
          ForEach(this.workspace.visibleRows(), (row: ResourceRow) => this.agentRow(row), (row: ResourceRow) =>
            row.key())
        } else {
          ForEach(this.workspace.groups, (group: AgentWorkspaceGroup) => {
            ListItemGroup({ header: () => this.groupHeader(group) }) {
              ForEach(group.rows, (row: ResourceRow) => this.agentRow(row), (row: ResourceRow) =>
                row.key())
            }.id('agents.group.' + group.id)
          }, (group: AgentWorkspaceGroup) => group.id)
        }
        if (!this.workspace.hasRows() && !this.workspace.loading) ListItem() {
          Text(tr(this.workspace.search ? 'no_results' : 'empty')).fontSize(Tokens.meta).padding(Tokens.page).width('100%')
        }.id('agents.empty')
      }.width('100%').height('100%').padding({ bottom: Tokens.listTail })
      }.width('100%').layoutWeight(1).onRefreshing(() => { this.workspace.setReadingDetail(false); this.workspace.refresh(this.model).finally(() => { this.workspace.setReadingDetail(this.readingDetail); this.refreshing = false; }); })
    }.width('100%').height('100%')
      .bindSheet($$this.editorOpen, this.renameForm(), { title: { title: tr('rename') }, height: SheetSize.MEDIUM, showClose: true })
  }
}
