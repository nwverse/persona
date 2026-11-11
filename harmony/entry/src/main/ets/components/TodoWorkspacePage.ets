import { ComposeListItemV2, ContentItemV2 } from '@kit.ArkUI';
import { ClientModel } from '../models/ClientModel';
import { TodoWorkspace, TodoWorkspaceAgent, TodoWorkspaceGroup, TodoWorkspaceItem, TodoWorkspaceSection,
  TodoWorkspaceSpace, TodoWorkspaceSpaceChoice } from '../models/TodoWorkspace';
import { ResourceRow, Wire } from '../models/Wire';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import { MarkdownText } from './MarkdownText';

@ComponentV2
export struct TodoWorkspacePage {
  @Require @Param model: ClientModel;
  @Param row: ResourceRow | undefined = undefined;
  @Param list: boolean = true;
  @Event onOpen: (row: ResourceRow) => void = (_row: ResourceRow) => {};
  @Event onOpenResource: (row: ResourceRow) => void = (_row: ResourceRow) => {};
  @Event onLink: (address: string) => void = (_address: string) => {};
  @Event onCreateAgent: (todo: ResourceRow) => void = (_todo: ResourceRow) => {};
  @Local workspace: TodoWorkspace = new TodoWorkspace();
  @Local previousAgentsOpen: boolean = false;

  aboutToAppear(): void { this.previousAgentsOpen = false; this.workspace.open(this.model, this.row, this.list).catch(() => undefined); }
  aboutToDisappear(): void { this.workspace.close(); }

  @Monitor('model.profile') profileChanged(): void {
    this.previousAgentsOpen = false;
    this.workspace.open(this.model, this.row, this.list).catch(() => undefined);
  }
  @Monitor('model.connected') connectionChanged(): void {
    if (this.model.connected) this.workspace.refresh().catch(() => undefined);
    else this.workspace.error = 'offline';
  }
  @Monitor('model.sessions') sessionsChanged(): void { this.workspace.update(this.model); }
  @Monitor('model.machines') machinesChanged(): void { this.workspace.update(this.model); }
  @Monitor('model.knowledge') projectsChanged(): void { this.workspace.update(this.model); }

  private statusOptions(): SelectOption[] {
    const options: SelectOption[] = [{ value: tr('all') }];
    ['needs_you', 'checking', 'working', 'followup', 'not_started', 'done'].forEach((state: string) => options.push({ value: tr('todo_state_' + state) }));
    return options;
  }
  private statusIndex(): number {
    const states: string[] = ['all', 'needs_you', 'checking', 'working', 'followup', 'not_started', 'done'];
    const index = states.indexOf(this.workspace.statusFilter);
    return index < 0 ? 0 : index;
  }
  private groupOptions(): SelectOption[] {
    return [{ value: tr('todo_group_status') }, { value: tr('todo_group_project') }, { value: tr('todo_group_thread') }];
  }
  private groupIndex(): number { return ['status', 'project', 'thread'].indexOf(this.workspace.groupBy); }
  private sessionKey(row: ResourceRow): string { return JSON.stringify([row.manager, row.machine, row.session || row.id]); }
  private choiceKey(choice: TodoWorkspaceSpaceChoice): string { return JSON.stringify([choice.machineId, choice.space]); }
  private selectedSession(): ResourceRow | undefined {
    return this.workspace.candidateSessions().find((row: ResourceRow) => this.sessionKey(row) === this.workspace.selectedAgentKey);
  }
  private selectedSpace(): TodoWorkspaceSpaceChoice | undefined {
    return this.workspace.spaceChoices().find((choice: TodoWorkspaceSpaceChoice) => this.choiceKey(choice) === this.workspace.selectedSpaceKey);
  }
  private selectSession(index: number): void {
    const row = this.workspace.candidateSessions()[index];
    if (row) this.workspace.selectedAgentKey = this.sessionKey(row);
  }
  private selectSpace(index: number): void {
    const choice = this.workspace.spaceChoices()[index];
    if (choice) this.workspace.selectedSpaceKey = this.choiceKey(choice);
  }
  private sessionOptions(): SelectOption[] {
    return this.workspace.candidateSessions().map((row: ResourceRow): SelectOption => ({ value: (row.title || row.id) + ' · ' + this.workspace.machineName(row.machine) }));
  }
  private spaceOptions(): SelectOption[] {
    return this.workspace.spaceChoices().map((choice: TodoWorkspaceSpaceChoice): SelectOption => ({ value: choice.label }));
  }
  private targetCurrent(todo: TodoWorkspaceItem, generation: number, scope: string): boolean {
    return this.model.owns(generation, scope) && this.workspace.isSelected(todo);
  }
  private todoSubtitle(group: TodoWorkspaceGroup): string {
    const relatedCount = group.related().length;
    const state = tr('todo_state_' + group.state);
    return relatedCount > 0 ? state + ' · ' + String(relatedCount + 1) + ' · ' + tr('todo_related') : state;
  }
  private sectionTitle(section: TodoWorkspaceSection): string {
    if (this.workspace.groupBy === 'status' || section.title === 'todo_unsorted' || section.title === 'todo_no_thread')
      return tr(section.title);
    return section.title;
  }
  private completionLabel(): string {
    return tr(this.workspace.selected.isDone() && !this.workspace.selected.startError ? 'reopen' : 'todo_complete');
  }
  private detailEmptyKey(): string {
    return this.workspace.loading ? 'loading' : this.workspace.error || 'todo_gone';
  }
  private changeCompletion(): void {
    const todo = this.workspace.selected;
    if (!this.workspace.isSelected(todo)) return;
    if (todo.isDone() && !todo.startError) this.workspace.setDone(todo, false).catch(() => undefined);
    else this.confirmDone(todo);
  }
  private openProject(): void {
    const resource = this.workspace.projectResource(this.workspace.selected);
    if (resource) this.onOpenResource(resource);
  }
  private openThread(): void {
    const resource = this.workspace.threadResource(this.workspace.selected);
    if (resource) this.onOpenResource(resource);
  }
  private openSource(): void {
    const resource = this.workspace.sourceResource(this.workspace.selected);
    if (resource) this.onOpenResource(resource);
  }

  private confirmDone(todo: TodoWorkspaceItem): void {
    const agents = todo.activeAgents().map((agent: TodoWorkspaceAgent) => (agent.title || tr('agent')) + ' · ' + this.workspace.machineName(agent.machineId));
    const message = todo.title + (agents.length ? '\n' + agents.join('\n') : '');
    const generation = this.model.connectionGeneration(), scope = this.model.scope(), target = todo.id;
    this.getUIContext().showAlertDialog({ title: tr('todo_complete'), message: message,
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('todo_complete'), action: () => {
        if (!this.model.owns(generation, scope) || !this.workspace.isSelected(todo) || this.workspace.selected.id !== target) return;
        this.workspace.setDone(todo, true).catch(() => undefined);
      } } });
  }

  private confirmDelete(todo: TodoWorkspaceItem): void {
    const target = todo.id, generation = this.model.connectionGeneration(), scope = this.model.scope();
    const agents = todo.activeAgents().map((agent: TodoWorkspaceAgent) => (agent.title || tr('agent')) + ' · ' + this.workspace.machineName(agent.machineId));
    const message = todo.title + (agents.length ? '\n' + agents.join('\n') : '');
    this.getUIContext().showAlertDialog({ title: tr('delete'), message: message,
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('delete'), action: () => {
        if (!this.model.owns(generation, scope) || !this.workspace.isSelected(todo) || this.workspace.selected.id !== target) return;
        this.workspace.remove(todo).then((removed: boolean) => { if (removed) this.workspace.selected = new TodoWorkspaceItem(); }).catch(() => undefined);
      } } });
  }

  private confirmLinkAgent(todo: TodoWorkspaceItem): void {
    const session = this.selectedSession();
    if (!session) return;
    const machine = this.workspace.machineName(session.machine);
    const current = Wire.object(session.data(), 'todo');
    const previous = Wire.text(current, 'title');
    const message = todo.title + '\n' + (session.title || tr('agent')) + ' · ' + machine +
      (previous ? '\n' + tr('todo_move_from') + ': ' + previous : '');
    const generation = this.model.connectionGeneration(), scope = this.model.scope(), key = this.sessionKey(session);
    this.getUIContext().showAlertDialog({ title: tr('todo_link_agent'), message: message,
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('todo_link_agent'), action: () => {
        if (!this.targetCurrent(todo, generation, scope) || this.sessionKey(session) !== key ||
          !this.workspace.candidateSessions(todo).some((row: ResourceRow) => this.sessionKey(row) === key)) return;
        this.workspace.linkAgent(todo, session).catch(() => undefined);
      } } });
  }

  private confirmUnlinkAgent(todo: TodoWorkspaceItem, agent: TodoWorkspaceAgent): void {
    const target = todo.id, machine = this.workspace.machineName(agent.machineId);
    const generation = this.model.connectionGeneration(), scope = this.model.scope();
    this.getUIContext().showAlertDialog({ title: tr('todo_unlink_agent'),
      message: todo.title + '\n' + (agent.title || tr('agent')) + ' · ' + machine,
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('todo_unlink_agent'), action: () => {
        if (this.targetCurrent(todo, generation, scope) && this.workspace.selected.id === target && agent.isActive())
          this.workspace.unlinkAgent(todo, agent).catch(() => undefined);
      } } });
  }

  private confirmLinkSpace(todo: TodoWorkspaceItem): void {
    const choice = this.selectedSpace();
    if (!choice) return;
    const target = todo.id, key = this.choiceKey(choice), generation = this.model.connectionGeneration(), scope = this.model.scope();
    this.getUIContext().showAlertDialog({ title: tr('todo_link_project'), message: todo.title + '\n' + choice.label,
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('todo_link_project'), action: () => {
        if (!this.targetCurrent(todo, generation, scope) || this.workspace.selected.id !== target || this.choiceKey(choice) !== key ||
          !this.workspace.spaceChoices(todo).some((item: TodoWorkspaceSpaceChoice) => this.choiceKey(item) === key)) return;
        this.workspace.linkSpace(todo, choice).catch(() => undefined);
      } } });
  }

  private confirmUnlinkSpace(todo: TodoWorkspaceItem, space: TodoWorkspaceSpace): void {
    const target = todo.id, generation = this.model.connectionGeneration(), scope = this.model.scope();
    this.getUIContext().showAlertDialog({ title: tr('todo_remove_project'), message: todo.title + '\n' +
      space.space + ' · ' + this.workspace.machineName(space.machineId),
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('todo_remove_project'), action: () => {
        if (this.targetCurrent(todo, generation, scope) && this.workspace.selected.id === target)
          this.workspace.unlinkSpace(todo, space).catch(() => undefined);
      } } });
  }

  @Builder todoMenu(todo: TodoWorkspaceItem) {
    Menu() {
      MenuItem({ content: tr('delete') }).onClick(() => this.confirmDelete(todo))
    }
  }

  @Builder todoRow(group: TodoWorkspaceGroup) {
    ListItem() {
      ComposeListItemV2({ contentItemV2: new ContentItemV2({ primaryText: group.lead.title, secondaryText: this.todoSubtitle(group) }) })
    }.onClick(() => this.onOpenResource(this.workspace.todoResource(group.lead)))
      .id('todo.' + group.lead.manager + '.' + group.lead.id)
  }

  @Builder sectionHeader(section: TodoWorkspaceSection) {
    Text(this.sectionTitle(section))
      .fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).width('100%')
      .padding({ left: Tokens.page, right: Tokens.page, top: Tokens.inset, bottom: Tokens.fine })
  }

  @Builder listContent() {
    Column() {
      Row({ space: Tokens.gap }) {
        Search({ value: this.workspace.search, placeholder: tr('todo_search') })
          .onChange((value: string) => this.workspace.setSearch(value)).layoutWeight(1).id('todos.search')
        Button(tr('add')).onClick(() => this.workspace.startCreate()).height(Tokens.touch).id('todos.add')
      }.padding({ left: Tokens.inset, right: Tokens.inset, top: Tokens.inset, bottom: Tokens.gap })
      Row({ space: Tokens.gap }) {
        Select(this.statusOptions()).selected(this.statusIndex()).value(this.statusOptions()[this.statusIndex()].value)
          .onSelect((index: number) => this.workspace.setStatusFilter(['all', 'needs_you', 'checking', 'working', 'followup', 'not_started', 'done'][index]))
          .layoutWeight(1).accessibilityText(tr('todo_filter_status')).id('todos.filter')
        Select(this.groupOptions()).selected(Math.max(0, this.groupIndex())).value(this.groupOptions()[Math.max(0, this.groupIndex())].value)
          .onSelect((index: number) => this.workspace.setGroupBy(['status', 'project', 'thread'][index]))
          .layoutWeight(1).accessibilityText(tr('todo_group_by')).id('todos.group')
      }.padding({ left: Tokens.inset, right: Tokens.inset, bottom: Tokens.inset })
      if (this.workspace.error) Text(tr(this.workspace.error)).fontSize(Tokens.meta)
        .fontColor($r('app.color.persona_danger')).padding(Tokens.inset).width('100%')
      List() {
        if (this.workspace.loading && !this.workspace.items.length) ListItem() { LoadingProgress().height(Tokens.touch).width(Tokens.touch).padding(Tokens.page) }
        ForEach(this.workspace.sections(), (section: TodoWorkspaceSection) => {
          ListItemGroup({ header: () => this.sectionHeader(section) }) {
            ForEach(section.rows, (group: TodoWorkspaceGroup) => this.todoRow(group), (group: TodoWorkspaceGroup) => group.key)
          }.id('todo.section.' + section.key)
        }, (section: TodoWorkspaceSection) => section.key)
        if (!this.workspace.sections().length && !this.workspace.loading) ListItem() {
          Text(tr(this.workspace.search ? 'no_results' : 'empty')).fontSize(Tokens.meta).padding(Tokens.page).width('100%')
        }
      }.width('100%').layoutWeight(1).padding({ bottom: Tokens.listTail })
    }.width('100%').height('100%')
  }

  @Builder agentRow(agent: TodoWorkspaceAgent) {
    if (agent.isActive()) {
      ListItem() { this.agentContent(agent) }.onClick(() => this.onOpen(this.workspace.agentSession(agent)))
        .bindContextMenu((): void => this.agentMenu(this.workspace.selected, agent), ResponseType.LongPress)
        .id('todo.agent.' + agent.machineId + '.' + agent.sessionId)
    } else {
      ListItem() { this.agentContent(agent) }.onClick(() => this.onOpen(this.workspace.agentSession(agent)))
        .id('todo.agent.' + agent.machineId + '.' + agent.sessionId)
    }
  }

  @Builder agentContent(agent: TodoWorkspaceAgent) {
    ComposeListItemV2({ contentItemV2: new ContentItemV2({ primaryText: agent.title || tr('agent'),
      secondaryText: this.workspace.machineName(agent.machineId) + ' · ' + tr('todo_state_' + this.workspace.agentState(agent)) }) })
  }

  @Builder agentMenu(todo: TodoWorkspaceItem, agent: TodoWorkspaceAgent) {
    Menu() { MenuItem({ content: tr('todo_unlink_agent') }).onClick(() => this.confirmUnlinkAgent(todo, agent)) }
  }

  @Builder spaceRow(space: TodoWorkspaceSpace) {
    ListItem() {
      ComposeListItemV2({ contentItemV2: new ContentItemV2({ primaryText: space.space,
        secondaryText: this.workspace.machineName(space.machineId) }) })
    }.bindContextMenu((): void => this.spaceMenu(this.workspace.selected, space), ResponseType.LongPress)
      .id('todo.space.' + space.machineId + '.' + space.space)
  }

  @Builder spaceMenu(todo: TodoWorkspaceItem, space: TodoWorkspaceSpace) {
    Menu() { MenuItem({ content: tr('todo_remove_project') }).onClick(() => this.confirmUnlinkSpace(todo, space)) }
  }

  @Builder detailContent() {
    Column() {
      Scroll() {
        if (this.workspace.selected.id) Column({ space: Tokens.page }) {
          Text(tr(this.workspace.stateLabelKey(this.workspace.selected))).fontSize(Tokens.meta).width('100%')
            .bindContextMenu((): void => this.todoMenu(this.workspace.selected), ResponseType.LongPress)
          if (this.workspace.selected.startError) Text(this.workspace.selected.startError).fontColor($r('app.color.persona_danger')).width('100%')
          if (this.workspace.selected.doneReason) Text(this.workspace.selected.doneReason).fontSize(Tokens.meta).width('100%')
          if (this.workspace.selected.notes) MarkdownText({ text: this.workspace.selected.notes, onLink: (address: string) => this.onLink(address) })

          if (this.workspace.selected.project) {
            Row() { ComposeListItemV2({ contentItemV2: new ContentItemV2({ primaryText: this.workspace.projectTitle(this.workspace.selected) }) }) }
              .onClick(() => this.openProject())
          }
          if (this.workspace.threadTitle(this.workspace.selected)) {
            Row() { ComposeListItemV2({ contentItemV2: new ContentItemV2({ primaryText: this.workspace.threadTitle(this.workspace.selected) }) }) }
              .onClick(() => this.openThread())
          }
          if (this.workspace.sourceResource(this.workspace.selected)) {
            Row() { ComposeListItemV2({ contentItemV2: new ContentItemV2({ primaryText: tr('todo_source_conversation') }) }) }
              .onClick(() => this.openSource())
          }
          ForEach(this.workspace.lineageResources(this.workspace.selected), (resource: ResourceRow) => {
            Row() { ComposeListItemV2({ contentItemV2: new ContentItemV2({ primaryText: resource.title || tr('todo_related') }) }) }
              .onClick(() => this.onOpenResource(resource))
          }, (resource: ResourceRow) => resource.id)
          ForEach(this.workspace.relatedTodos(this.workspace.selected), (todo: TodoWorkspaceItem) => {
            Row() { ComposeListItemV2({ contentItemV2: new ContentItemV2({ primaryText: todo.title }) }) }
              .onClick(() => this.onOpenResource(this.workspace.todoResource(todo)))
          }, (todo: TodoWorkspaceItem) => todo.id)

          if (this.workspace.selected.spaces.length) {
            Text(tr('todo_projects')).fontSize(Tokens.meta).width('100%')
            List() { ForEach(this.workspace.selected.spaces, (space: TodoWorkspaceSpace) => this.spaceRow(space),
              (space: TodoWorkspaceSpace) => space.machineId + '/' + space.space) }.height(Math.min(300, Tokens.touch * this.workspace.selected.spaces.length))
          }

          Text(tr('todo_agents')).fontSize(Tokens.meta).width('100%')
          ForEach(this.workspace.selected.activeAgents(), (agent: TodoWorkspaceAgent) => this.agentRow(agent),
            (agent: TodoWorkspaceAgent) => 'active:' + agent.machineId + '/' + agent.sessionId)
          if (this.workspace.selected.previousAgents().length) {
            Row() { ComposeListItemV2({ contentItemV2: new ContentItemV2({ primaryText: tr('todo_previously_involved') }) }) }
              .onClick(() => { this.previousAgentsOpen = !this.previousAgentsOpen; })
            if (this.previousAgentsOpen) {
              ForEach(this.workspace.selected.previousAgents(), (agent: TodoWorkspaceAgent) => this.agentRow(agent),
                (agent: TodoWorkspaceAgent) => 'past:' + agent.machineId + '/' + agent.sessionId)
            }
          }

          if (!this.workspace.selected.isDone()) {
            if (this.workspace.candidateSessions().length) {
              Select(this.sessionOptions()).selected(Math.max(0, this.workspace.candidateSessions().findIndex((row: ResourceRow) => this.sessionKey(row) === this.workspace.selectedAgentKey)))
                .value(this.selectedSession()?.title || tr('todo_choose_agent'))
                .onSelect((index: number) => this.selectSession(index))
                .accessibilityText(tr('todo_choose_agent')).width('100%')
              Button(tr('todo_link_agent')).enabled(!!this.selectedSession() && !this.workspace.saving)
                .onClick(() => this.confirmLinkAgent(this.workspace.selected)).height(Tokens.touch).width('100%')
            }
            Button(tr('new_agent')).enabled(!this.workspace.saving).onClick(() => this.onCreateAgent(this.workspace.todoResource(this.workspace.selected)))
              .height(Tokens.touch).width('100%')
            if (this.workspace.spaceChoices().length) {
              Select(this.spaceOptions()).selected(Math.max(0, this.workspace.spaceChoices().findIndex((item: TodoWorkspaceSpaceChoice) => this.choiceKey(item) === this.workspace.selectedSpaceKey)))
                .value(this.selectedSpace()?.label || tr('todo_choose_project'))
                .onSelect((index: number) => this.selectSpace(index))
                .accessibilityText(tr('todo_choose_project')).width('100%')
              Button(tr('todo_link_project')).enabled(!!this.selectedSpace() && !this.workspace.saving)
                .onClick(() => this.confirmLinkSpace(this.workspace.selected)).height(Tokens.touch).width('100%')
            }
          }
          if (this.workspace.error) Text(tr(this.workspace.error)).fontSize(Tokens.meta)
            .fontColor($r('app.color.persona_danger')).width('100%')
        }.alignItems(HorizontalAlign.Start).padding(Tokens.page).width('100%')
        else Text(tr(this.detailEmptyKey())).fontSize(Tokens.meta).padding(Tokens.page).width('100%')
      }.layoutWeight(1).width('100%')
      if (this.workspace.selected.id) Row({ space: Tokens.gap }) {
        Button(this.completionLabel()).enabled(!this.workspace.saving)
          .layoutWeight(1).height(Tokens.touch).onClick(() => this.changeCompletion())
        Button(tr('edit')).enabled(!this.workspace.saving).layoutWeight(1).height(Tokens.touch)
          .onClick(() => this.workspace.beginEdit(this.workspace.selected))
      }.padding(Tokens.inset).width('100%')
    }.width('100%').height('100%')
  }

  @Builder editor() {
    Column({ space: Tokens.page }) {
      TextInput({ text: this.workspace.titleDraft, placeholder: tr('title') }).onChange((value: string) => this.workspace.setTitleDraft(value))
        .id('todos.title')
      TextArea({ text: this.workspace.notesDraft, placeholder: tr('todo_notes') }).onChange((value: string) => this.workspace.setNotesDraft(value))
        .height(Tokens.inputExpanded).id('todos.notes')
      if (this.workspace.error) Text(tr(this.workspace.error)).fontColor($r('app.color.persona_danger')).width('100%')
      Row({ space: Tokens.gap }) {
        Button(tr('cancel')).layoutWeight(1).height(Tokens.touch).onClick(() => this.workspace.closeEditor())
        Button(tr('save')).layoutWeight(1).height(Tokens.touch).enabled(!this.workspace.saving && !!this.workspace.titleDraft.trim())
          .onClick(() => this.workspace.save().catch(() => undefined)).id('todos.save')
      }.width('100%')
    }.padding(Tokens.page).width('100%')
  }

  build() {
    Column() {
      if (this.list) this.listContent()
      else this.detailContent()
    }.width('100%').height('100%')
      .bindSheet($$this.workspace.editorOpen, this.editor(), { title: { title: tr(this.workspace.selected.id ? 'edit' : 'create') }, height: SheetSize.LARGE, showClose: true })
  }
}
