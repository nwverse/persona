import { nativeEnvironment } from '../platform/NativeEnvironment';
import { i18n } from '@kit.LocalizationKit';
import { IconActions } from '../design/IconActions';
import { Symbols } from '../design/Symbols';
import { Colors } from '../design/Colors';
import { ButtonStyles, SelectStyles } from '../design/Controls';
import { ComposeListItemV2, ContentItemV2, LengthMetrics } from '@kit.ArkUI';
import { ClientModel } from '../models/ClientModel';
import { TodoWorkspace, TodoWorkspaceAgent, TodoWorkspaceGroup, TodoWorkspaceItem, TodoWorkspaceSection,
  TodoWorkspaceSpace, TodoWorkspaceSpaceChoice, TODO_DUE_FILTERS, TODO_LIST_ORDERS } from '../models/TodoWorkspace';
import { BoundRow, reconcileBoundRows } from '../models/RowBinding';
import { todoHubLabel } from '../models/TodoHubView';
import { ResourceRow, Wire } from '../models/Wire';
import { Tokens } from '../design/Tokens';
import { tr, trError } from '../design/Strings';
import { MarkdownText } from './MarkdownText';

@ObservedV2
class BoundTodoSection {
  readonly identity: string;
  readonly key: string;
  @Trace value: TodoWorkspaceSection;
  @Trace rows: BoundRow<TodoWorkspaceGroup>[] = [];

  constructor(identity: string, key: string, value: TodoWorkspaceSection) {
    this.identity = identity;
    this.key = key;
    this.value = value;
  }
}

@ComponentV2
export struct TodoWorkspacePage {
  @Require @Param model: ClientModel;
  @Param row: ResourceRow | undefined = undefined;
  @Param list: boolean = true;
  @Param visible: boolean = true;
  @Event onOpen: (row: ResourceRow) => void = (_row: ResourceRow) => {};
  @Event onOpenResource: (row: ResourceRow) => void = (_row: ResourceRow) => {};
  @Event onLink: (address: string) => void = (_address: string) => {};
  @Event onCreateAgent: (todo: ResourceRow) => void = (_todo: ResourceRow) => {};
  @Event onTitle: (title: string) => void = (_title: string) => {};
  @Local workspace: TodoWorkspace = new TodoWorkspace();
  @Local sectionBindings: BoundTodoSection[] = [];
  @Local expandedGroups:string[] = [];
  @Local showDone:boolean = false;
  @Local clock:number = Date.now();
  private clockTimer:number = -1;
  private weekStart:number = 1;
  @Local lineageRowBindings: BoundRow<ResourceRow>[] = [];
  @Local relatedTodoBindings: BoundRow<TodoWorkspaceItem>[] = [];
  @Local spaceBindings: BoundRow<TodoWorkspaceSpace>[] = [];
  @Local activeAgentBindings: BoundRow<TodoWorkspaceAgent>[] = [];
  @Local previousAgentBindings: BoundRow<TodoWorkspaceAgent>[] = [];
  @Local previousAgentsOpen: boolean = false;
  private rowBindingOwner: Object | undefined;
  private rowBindingScope: string = '';
  private sectionBindingSequence: number = 0;
  private pageAvailable: boolean = false;
  private lastVisible: boolean = true;
  private workspaceOpening: boolean = false;
  private workspaceInitialized: boolean = false;
  private queuedRefresh: boolean = false;
  private openSerial: number = 0;

  aboutToAppear(): void {
    this.pageAvailable = true;
    try { this.weekStart = i18n.System.getFirstDayOfWeek() % 7; } catch (_) {}
    this.clock=Date.now(); this.clockTimer=setInterval(() => { if (this.visible) this.clock=Date.now(); },60000);
    this.lastVisible = this.visible;
    this.previousAgentsOpen = false;
    this.syncRowBindings();
    this.openWorkspace();
  }
  aboutToDisappear(): void {
    this.pageAvailable = false;
    clearInterval(this.clockTimer); this.clockTimer=-1;
    this.openSerial++;
    this.workspaceOpening = false;
    this.workspaceInitialized = false;
    this.queuedRefresh = false;
    this.workspace.close();
  }

  @Monitor('row', 'list') routeChanged(): void { this.syncRowBindings(); if (this.pageAvailable) this.openWorkspace(); }

  @Monitor('model.profile') profileChanged(): void {
    this.expandedGroups=[]; this.showDone=false;
    this.previousAgentsOpen = false;
    this.syncRowBindings();
    if (this.pageAvailable) this.openWorkspace();
  }
  @Monitor('model.connected') connectionChanged(): void {
    if (!this.pageAvailable) return;
    if (this.model.connected) this.refreshWhenReady(true);
    else this.workspace.error = 'offline';
  }
  @Monitor('visible') visibilityChanged(): void {
    const becameVisible = !this.lastVisible && this.visible;
    this.lastVisible = this.visible;
    if (becameVisible && this.pageAvailable) {
      this.publishTitle();
      this.refreshWhenReady();
    }
  }
  @Monitor('model.sessions') sessionsChanged(): void { this.workspace.update(this.model); }
  @Monitor('model.machines') machinesChanged(): void { this.workspace.update(this.model); }
  @Monitor('model.knowledge') projectsChanged(): void { this.workspace.update(this.model); }
  @Monitor('workspace.selected') selectedChanged(): void { this.syncRowBindings(); this.publishTitle(); }
  @Monitor('workspace.items', 'workspace.search', 'workspace.statusFilter', 'workspace.groupBy','workspace.dueFilter','workspace.order','clock',
    'workspace.hubCounts', 'workspace.hubChoice',
    'workspace.projects', 'workspace.sessions', 'workspace.machines') rowsChanged(): void { this.syncRowBindings(); }

  private openWorkspace(): void {
    const serial = ++this.openSerial;
    this.workspaceOpening = true;
    this.workspaceInitialized = false;
    this.queuedRefresh = false;
    this.workspace.open(this.model, this.row, this.list).catch(() => undefined).finally(() => {
      if (serial !== this.openSerial) return;
      this.workspaceOpening = false;
      this.workspaceInitialized = true;
      if (this.queuedRefresh && this.pageAvailable && this.visible) {
        this.queuedRefresh = false;
        this.workspace.refresh().catch(() => undefined);
      }
    });
  }

  private refreshWhenReady(retryOpening: boolean = false): void {
    if (!this.pageAvailable || !this.visible) return;
    if (this.workspaceOpening) {
      // The initial open loads the workspace and detail target. A connection retry
      // still needs a fresh read once that open has rebound its owner.
      if (this.workspaceInitialized || retryOpening) this.queuedRefresh = true;
      return;
    }
    if (this.workspaceInitialized) this.workspace.refresh().catch(() => undefined);
  }

  private publishTitle(): void {
    const row = this.row, selected = this.workspace.selected;
    if (this.list || !row || !selected.id || this.workspace.targetGone || row.id !== selected.id) return;
    const manager = row.manager || this.model.profile.manager;
    if (selected.manager && selected.manager !== manager) return;
    this.onTitle(selected.title);
  }

  private syncRowBindings(): void {
    const scope = JSON.stringify([this.model.scope(), this.model.profile.id, this.model.profile.manager,
      this.row?.key() || 'list', this.list ? 'list' : 'detail']);
    const sameOwnerScope = this.rowBindingOwner === this.model && this.rowBindingScope === scope;
    const previous = sameOwnerScope ? this.sectionBindings : [];
    const available = new Map<string, BoundTodoSection[]>();
    previous.forEach((binding: BoundTodoSection) => {
      const bindings = available.get(binding.identity) || [];
      bindings.push(binding);
      available.set(binding.identity, bindings);
    });
    const occurrences = new Map<string, number>(), next: BoundTodoSection[] = [];
    this.workspace.sections(this.clock,this.weekStart).forEach((section: TodoWorkspaceSection) => {
      const occurrence = occurrences.get(section.key) || 0;
      occurrences.set(section.key, occurrence + 1);
      const identity = JSON.stringify([section.key, occurrence]);
      const matches = available.get(identity);
      const existing = matches && matches.length ? matches.shift() : undefined;
      let binding: BoundTodoSection;
      if (existing) {
        binding = existing;
        binding.value = section;
        binding.rows = reconcileBoundRows(binding.rows, section.rows, (group: TodoWorkspaceGroup) => group.key,
          this.model, this.model, scope, scope);
      } else {
        this.sectionBindingSequence++;
        binding = new BoundTodoSection(identity, 'todo-section-binding.' + String(this.sectionBindingSequence), section);
        binding.rows = reconcileBoundRows([], section.rows, (group: TodoWorkspaceGroup) => group.key,
          undefined, this.model, '', scope);
      }
      next.push(binding);
    });
    this.sectionBindings = next;
    const current = this.workspace.selected;
    this.lineageRowBindings = reconcileBoundRows(this.lineageRowBindings, current.id ? this.workspace.lineageResources(current) : [],
      (resource: ResourceRow) => resource.key(), this.rowBindingOwner, this.model, this.rowBindingScope, scope);
    this.relatedTodoBindings = reconcileBoundRows(this.relatedTodoBindings, current.id ? this.workspace.relatedTodos(current) : [],
      (todo: TodoWorkspaceItem) => JSON.stringify([todo.manager, todo.id]),
      this.rowBindingOwner, this.model, this.rowBindingScope, scope);
    this.spaceBindings = reconcileBoundRows(this.spaceBindings, current.id ? current.spaces : [],
      (space: TodoWorkspaceSpace) => JSON.stringify([space.machineId, space.space]),
      this.rowBindingOwner, this.model, this.rowBindingScope, scope);
    this.activeAgentBindings = reconcileBoundRows(this.activeAgentBindings, current.id ? current.activeAgents() : [],
      (agent: TodoWorkspaceAgent) => JSON.stringify([agent.machineId, agent.sessionId]),
      this.rowBindingOwner, this.model, this.rowBindingScope, scope);
    this.previousAgentBindings = reconcileBoundRows(this.previousAgentBindings, current.id ? current.previousAgents() : [],
      (agent: TodoWorkspaceAgent) => JSON.stringify([agent.machineId, agent.sessionId]),
      this.rowBindingOwner, this.model, this.rowBindingScope, scope);
    this.rowBindingOwner = this.model;
    this.rowBindingScope = scope;
  }

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
  private hubMode(): boolean { return this.workspace.effectiveGroup() === 'hub'; }
  private groupKeys(): string[] {
    return this.workspace.hubCounts ? ['hub', 'status', 'project', 'thread'] : ['status', 'project', 'thread'];
  }
  private groupOptions(): SelectOption[] {
    return this.groupKeys().map((key: string): SelectOption => ({ value: tr(key === 'hub' ? 'flow_todo_hub' : 'todo_group_' + key) }));
  }
  private groupIndex(): number { return this.groupKeys().indexOf(this.workspace.effectiveGroup()); }
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
  private todoSubtitle(group: TodoWorkspaceGroup, includeState: boolean = false): string {
    const relatedCount = group.related().length;
    const state = tr('todo_state_' + group.state);
    const visibleState = includeState || this.workspace.groupBy !== 'status' ? state : '';
    const related = relatedCount > 0 ? tr('todo_related') + ' ' + String(relatedCount) : '';
    return [visibleState, related].filter((value: string) => !!value).join(' · ');
  }
  private sectionTitle(section: TodoWorkspaceSection): string {
    if (this.workspace.effectiveGroup() === 'status' || this.hubMode() || section.title === 'todo_unsorted' || section.title === 'todo_no_thread')
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
    const target = todo.id, identity = JSON.stringify([agent.machineId, agent.sessionId]);
    const currentAgent = todo.agents.find((item: TodoWorkspaceAgent) => JSON.stringify([item.machineId, item.sessionId]) === identity);
    if (!currentAgent || !currentAgent.isActive()) return;
    const machine = this.workspace.machineName(currentAgent.machineId);
    const generation = this.model.connectionGeneration(), scope = this.model.scope();
    this.getUIContext().showAlertDialog({ title: tr('todo_unlink_agent'),
      message: todo.title + '\n' + (currentAgent.title || tr('agent')) + ' · ' + machine,
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('todo_unlink_agent'), action: () => {
        const selected = this.workspace.selected;
        const latest = selected.agents.find((item: TodoWorkspaceAgent) => JSON.stringify([item.machineId, item.sessionId]) === identity);
        if (this.targetCurrent(todo, generation, scope) && selected.id === target && latest?.isActive())
          this.workspace.unlinkAgent(selected, latest).catch(() => undefined);
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
    const identity = JSON.stringify([space.machineId, space.space]);
    const currentSpace = todo.spaces.find((item: TodoWorkspaceSpace) => JSON.stringify([item.machineId, item.space]) === identity);
    if (!currentSpace) return;
    const target = todo.id, generation = this.model.connectionGeneration(), scope = this.model.scope();
    this.getUIContext().showAlertDialog({ title: tr('todo_remove_project'), message: todo.title + '\n' +
      currentSpace.space + ' · ' + this.workspace.machineName(currentSpace.machineId),
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('todo_remove_project'), action: () => {
        const selected = this.workspace.selected;
        const latest = selected.spaces.find((item: TodoWorkspaceSpace) => JSON.stringify([item.machineId, item.space]) === identity);
        if (this.targetCurrent(todo, generation, scope) && selected.id === target && latest)
          this.workspace.unlinkSpace(selected, latest).catch(() => undefined);
      } } });
  }

  @Builder advancedMenu() {
    Menu() {
      MenuItem({ content: tr('new_agent') }).enabled(!this.workspace.saving && !this.workspace.targetGone)
        .onClick(() => this.onCreateAgent(this.workspace.todoResource(this.workspace.selected)))
    }
  }
  @Builder todoMenu(todo: TodoWorkspaceItem) {
    Menu() {
      MenuItem({ content: tr('delete') }).enabled(!this.workspace.targetGone).onClick(() => this.confirmDelete(todo))
    }
  }

  private dueTitle(todo:TodoWorkspaceItem):string {
    if (!Number.isFinite(todo.due)) return '';
    const date=new Date(todo.due);
    const locale=this.model.language && this.model.language !== 'default' ? this.model.language : i18n.System.getSystemLocaleInstance().toString();
    const day=todo.dueAt && Number.isFinite(Date.parse(todo.dueAt)) ? date.toLocaleDateString(locale) + ' ' +
      String(date.getHours()).padStart(2,'0') + ':' + String(date.getMinutes()).padStart(2,'0') : date.toLocaleDateString(locale);
    return (!todo.isDone() && todo.dueBy < this.clock ? tr('flow_todo_overdue') + ' · ' : '') + day;
  }
  private setRowDone(todo:TodoWorkspaceItem):void {
    this.workspace.setDone(todo,!todo.isDone() || !!todo.startError,true).catch(() => undefined);
  }
  private dueFilterLabel(key:string):string { return tr(key === 'all' ? 'all' : 'flow_todo_'+key); }
  private toggleGroup(key:string):void {
    this.expandedGroups=this.expandedGroups.includes(key) ? this.expandedGroups.filter((k:string) => k !== key) : this.expandedGroups.concat([key]);
  }
  private completionShown(todo:TodoWorkspaceItem):boolean { return todo.isDone() && !todo.startError; }
  private rowClosed(todo: TodoWorkspaceItem): boolean {
    return this.hubMode() && todo.hubView ? todo.hubView.phase === 'done' : this.completionShown(todo);
  }
  private rowLine(group: TodoWorkspaceGroup): string {
    if (!this.hubMode()) return group.lead.notes;
    const line = group.lead.hubView?.line || '';
    if (line) return line;
    return group.state === 'todo' || group.state === 'someday' ? group.lead.notes : tr(todoHubLabel(group.state));
  }
  @Builder rowMenu(todo:TodoWorkspaceItem) {
    Menu() {
      if (this.hubMode()) MenuItem({ content: tr('open') }).onClick(() => this.onOpenResource(this.workspace.todoResource(todo)))
      else {
        MenuItem({content:tr(this.completionShown(todo) ? 'reopen' : 'todo_complete')}).enabled(this.model.connected && !this.workspace.saving)
          .onClick(() => this.setRowDone(todo))
        MenuItem({content:tr('edit')}).onClick(() => this.workspace.beginEdit(todo))
      }
    }
  }
  @Builder metadata(todo:TodoWorkspaceItem) {
    if (Number.isFinite(todo.due) || !!todo.priority) Row({space:6}) {
      if (Number.isFinite(todo.due)) {
        SymbolGlyph(Symbols.calendar).fontSize('12vp').fontColor([!todo.isDone() && todo.dueBy < this.clock ? Colors.danger : Colors.secondary])
        Text(this.dueTitle(todo)).fontSize(12).fontColor(!todo.isDone() && todo.dueBy < this.clock ? Colors.danger : Colors.secondary)
      }
      if (todo.priority) Text(tr(['low','medium','high'].includes(todo.priority) ? todo.priority : 'flow_todo_priority') +
        (!['low','medium','high'].includes(todo.priority) ? ' · ' + todo.priority : '')).fontSize(12)
        .fontColor(todo.priority === 'high' ? Colors.needs : Colors.secondary).padding({left:5,right:5,top:1,bottom:1}).backgroundColor(Colors.control).borderRadius(5)
    }.width('100%')
  }
  @Builder todoRow(binding:BoundRow<TodoWorkspaceGroup>) {
    ListItem() {
      Column() {
        Row({space:7}) {
          if (this.hubMode()) {
            Column() {
              if (this.rowClosed(binding.value.lead)) SymbolGlyph(Symbols.check).fontSize('17vp').fontColor([Colors.positive])
              else Circle().width(9).height(9).fill(binding.value.state === 'needs_you' ? Colors.needs :
                binding.value.state === 'just_finished' ? Colors.positive : binding.value.state === 'working' ? Colors.link : Colors.secondary)
            }.width(38).height(44).justifyContent(FlexAlign.Center)
          } else Button({type:ButtonType.Normal}) {
            Stack() { if (this.completionShown(binding.value.lead)) SymbolGlyph(Symbols.check).fontSize('13vp').fontColor([Color.White]) }
              .width(21).height(21).borderRadius(11)
              .border({width:1.4,color:this.completionShown(binding.value.lead) ? Colors.positive : Colors.secondary,radius:11})
              .backgroundColor(this.completionShown(binding.value.lead) ? Colors.positive : Color.Transparent)
          }.width(44).height(44).padding(0).backgroundColor(Color.Transparent).enabled(this.model.connected && !this.workspace.saving)
            .accessibilityText(tr(this.completionShown(binding.value.lead) ? 'reopen' : 'todo_complete') + ' · ' + binding.value.lead.title)
            .onClick(() => this.setRowDone(binding.value.lead)).id('todos.toggle.'+binding.value.lead.id)
          Row({space:7}) {
            Column({space:4}) {
              Text(binding.value.lead.title).fontSize(17).fontWeight(FontWeight.Medium)
                .fontColor(this.rowClosed(binding.value.lead) ? Colors.secondary : Colors.ink)
                .decoration({type:this.rowClosed(binding.value.lead) ? TextDecorationType.LineThrough : TextDecorationType.None}).maxLines(2)
              if (this.rowLine(binding.value)) Text(this.rowLine(binding.value)).fontSize(14).fontColor(Colors.secondary).maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis})
              this.metadata(binding.value.lead)
              if (['project','thread'].includes(this.workspace.effectiveGroup())) Text(tr('todo_state_'+binding.value.state)).fontSize(12).fontColor(Colors.secondary)
            }.alignItems(HorizontalAlign.Start).layoutWeight(1)
            SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
          }.layoutWeight(1).constraintSize({minHeight:44}).accessibilityGroup(true)
            .onClick(() => this.onOpenResource(this.workspace.todoResource(binding.value.lead)))
            .bindContextMenu(this.rowMenu(binding.value.lead),ResponseType.LongPress).id('todos.open.'+binding.value.lead.id)
        }.width('100%').padding({left:6,right:13,top:8,bottom:8})
        if (binding.value.members.length > 1) {
          Divider().strokeWidth(0.5).color(Colors.divider).margin({left:54})
          Row({space:6}) {
            SymbolGlyph(this.expandedGroups.includes(binding.value.key) ? Symbols.down : Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
            Text(tr('todo_related') + ' · ' + String(binding.value.members.length-1)).fontSize(14).fontColor(Colors.link)
          }.height(42).width('100%').padding({left:57}).accessibilityGroup(true).onClick(() => this.toggleGroup(binding.value.key))
            .id('todos.related.'+binding.value.lead.id)
          if (this.expandedGroups.includes(binding.value.key)) ForEach(binding.value.related(),(todo:TodoWorkspaceItem) => {
            Row({space:8}) {
              Text(todo.title).fontSize(15).fontColor(Colors.ink).layoutWeight(1).maxLines(2)
              SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
            }.constraintSize({minHeight:44}).width('100%').padding({left:57,right:14})
              .accessibilityGroup(true).onClick(() => this.onOpenResource(this.workspace.todoResource(todo))).id('todos.member.'+todo.id)
          },(todo:TodoWorkspaceItem) => JSON.stringify([todo.id,todo.title]))
        }
      }.width('100%').backgroundColor(Colors.surface).borderRadius(12)
    }.id('todo.'+binding.value.lead.manager+'.'+binding.value.lead.id)
  }
  private sectionOpen(section:TodoWorkspaceSection):boolean {
    if (this.hubMode()) return !this.foldable(section) || !!this.workspace.search.trim() || this.workspace.hubUnfolded.includes(section.key);
    return this.workspace.effectiveGroup() !== 'status' || section.key !== 'done' || this.showDone || !!this.workspace.search.trim() || this.workspace.statusFilter === 'done';
  }
  private foldable(section: TodoWorkspaceSection): boolean {
    if (!this.hubMode()) return this.workspace.effectiveGroup() === 'status' && section.key === 'done';
    return section.key === 'someday' || section.key === 'can_wait' || (section.key === 'done' && this.workspace.hubChoice !== 'done');
  }
  private sectionCount(binding: BoundTodoSection): number {
    if (this.hubMode() && binding.value.key === 'needs_you') return this.workspace.hubCounts?.needsYou || 0;
    return binding.rows.reduce((n: number, r: BoundRow<TodoWorkspaceGroup>) => n + r.value.members.length, 0);
  }
  @Builder sectionHeader(binding:BoundTodoSection) {
    if (!this.hubMode() || !this.workspace.hubCalm()) Row({space:6}) {
      Text(this.sectionTitle(binding.value)).fontSize(15).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
      Text(String(this.sectionCount(binding))).fontSize(13)
        .fontColor(binding.value.key === 'needs_you' ? Colors.needs : Colors.secondary).layoutWeight(1)
      if (this.foldable(binding.value)) SymbolGlyph(this.sectionOpen(binding.value) ? Symbols.down : Symbols.expand)
        .fontSize(12).fontColor([Colors.secondary])
    }.width('100%').height(40).padding({left:5,right:8}).accessibilityGroup(true)
      .onClick(() => {
        if (!this.foldable(binding.value)) return;
        if (this.hubMode()) this.workspace.toggleHubFold(binding.value.key);
        else this.showDone = !this.showDone;
      })
      .id('todos.section.'+binding.value.key)
  }
  @Builder hubChoiceBar() {
    Scroll() {
      Row({ space: 5 }) {
        ForEach(this.workspace.hubCounts?.choices(this.workspace.hubChoice) || [], (key: string) => {
          Button({ type: ButtonType.Capsule }) {
            Row({ space: 5 }) {
              Text(tr(todoHubLabel(key))).fontSize(14).fontWeight(FontWeight.Medium)
                .fontColor(this.workspace.hubChoice === key ? Colors.onAction : Colors.ink)
              if (['needs_you','in_progress','todo'].includes(key) && this.workspace.hubCounts?.choiceCount(key))
                Text(String(this.workspace.hubCounts?.choiceCount(key) || 0)).fontSize(12).fontWeight(FontWeight.Bold)
                  .fontColor(this.workspace.hubChoice === key ? Colors.onAction : key === 'needs_you' ? Colors.needs : Colors.secondary)
            }
          }.height(34).padding({ left: 10, right: 10 }).backgroundColor(this.workspace.hubChoice === key ? Colors.action : Colors.surface)
            .accessibilitySelected(this.workspace.hubChoice === key).onClick(() => this.workspace.setHubChoice(key)).id('todos.hub.choice.' + key)
        }, (key: string) => key + '/' + String(this.workspace.hubCounts?.choiceCount(key) || 0))
      }.padding({ left: 16, right: 16 })
    }.scrollable(ScrollDirection.Horizontal).scrollBar(BarState.Off).align(Alignment.Start).width('100%').id('todos.hub.choices')
  }
  @Builder listFilters() {
    Menu() {
      MenuItemGroup({ header: tr('todo_group_by') }) {
        ForEach(this.groupKeys(), (key: string) => {
          MenuItem({ content: tr(key === 'hub' ? 'flow_todo_hub' : 'todo_group_' + key) }).selected(this.workspace.effectiveGroup() === key).selectIcon(true)
            .onClick(() => this.workspace.setGroupBy(key)).id('todos.group.' + key)
        }, (key: string) => key)
      }
      MenuItemGroup({header:tr('flow_todo_due')}) {
        ForEach(TODO_DUE_FILTERS,(key:string) => {
          MenuItem({content:this.dueFilterLabel(key)}).selected(this.workspace.dueFilter === key).selectIcon(true)
            .onClick(() => this.workspace.setDueFilter(key)).id('todos.due.'+key)
        },(key:string) => key)
      }
      MenuItemGroup({header:tr('flow_todo_order')}) {
        ForEach(TODO_LIST_ORDERS,(key:string) => {
          MenuItem({content:tr(this.hubMode() && key === 'recent' ? 'flow_todo_suggested' : 'flow_todo_'+key)}).selected(this.workspace.order === key).selectIcon(true)
            .onClick(() => this.workspace.setOrder(key)).id('todos.sort.'+key)
        },(key:string) => key)
      }
      if (!this.hubMode()) MenuItemGroup({ header: tr('todo_filter_status') }) {
        ForEach(this.statusOptions(), (option: SelectOption, index: number) => {
          MenuItem({ content: option.value }).selected(this.statusIndex() === index).selectIcon(true)
            .onClick(() => this.workspace.setStatusFilter(['all', 'needs_you', 'checking', 'working', 'followup', 'not_started', 'done'][index]))
        }, (option: SelectOption) => JSON.stringify(option.value))
      }
    }.width(Tokens.popupWidth)
  }

  @Computed get hiddenWaitingCount(): number { return this.workspace.hiddenWaiting(this.clock,this.weekStart); }

  @Builder listContent() {
    Column() {
      Row({ space: Tokens.gap }) {
        Flex({wrap:FlexWrap.Wrap,space:{main:LengthMetrics.vp(5),cross:LengthMetrics.vp(5)}}) {
          Button({type:ButtonType.Capsule}) {
            Row({space:5}) {
              Text(this.groupOptions()[Math.max(0,this.groupIndex())].value).fontSize(14).fontColor(Colors.ink)
              SymbolGlyph(Symbols.down).fontSize('10vp').fontColor([Colors.secondary])
            }
          }.padding({left:12,right:12}).height(Math.max(38,Math.ceil(21*nativeEnvironment.fontScale)+10)).backgroundColor(Colors.surface).bindMenu(this.listFilters).id('todos.group')
          if (this.workspace.dueFilter !== 'all') Button(this.dueFilterLabel(this.workspace.dueFilter) + ' ×', {type:ButtonType.Capsule})
            .fontColor(Colors.onAction).backgroundColor(Colors.action).fontSize(14).height(Math.max(38,Math.ceil(21*nativeEnvironment.fontScale)+10)).padding({left:12,right:12})
            .onClick(() => this.workspace.setDueFilter('all')).id('todos.due.clear')
        }.layoutWeight(1)
        Badge({ count: (!this.hubMode() && this.workspace.statusFilter !== 'all' ? 1 : 0) + (this.workspace.effectiveGroup() !== 'status' ? 1 : 0) + (this.workspace.dueFilter !== 'all' ? 1 : 0) + (this.workspace.order !== 'recent' ? 1 : 0),
          style: { color: Colors.onAction, badgeColor: Colors.action, borderWidth: 0, borderColor: Color.Transparent, badgeSize: 17, fontSize: '11vp' } }) {
        Button({ type: ButtonType.Circle }) { SymbolGlyph(Symbols.filter).fontSize(Tokens.symbolFont).fontColor([Colors.link]) }
          .attributeModifier(ButtonStyles.chrome).accessibilityText(tr('todo_filter_status') + ' · ' + this.statusOptions()[this.statusIndex()].value + ' · ' + this.groupOptions()[Math.max(0, this.groupIndex())].value)
          .bindMenu(this.listFilters).id('todos.filters')
        }
        Button({ type: ButtonType.Circle }) { SymbolGlyph(Symbols.plus).fontSize(Tokens.symbolFont).fontColor([Colors.link]) }.attributeModifier(ButtonStyles.chrome).accessibilityText(tr('add')).onClick(() => this.workspace.startCreate()).id('todos.add')
      }.padding({ left: Tokens.inset, right: Tokens.inset, top: Tokens.inset, bottom: Tokens.gap })
      Search({value:this.workspace.search,placeholder:tr('todo_search')}).width('100%').height(Math.max(40,Math.ceil(24*nativeEnvironment.fontScale)+8))
        .padding({left:Tokens.page,right:Tokens.page}).onChange((value:string) => this.workspace.setSearch(value)).id('todos.search')
      if (this.hiddenWaitingCount) Column({space:4}) {
        Row({space:8}) {
          SymbolGlyph(Symbols.inbox).fontSize('16vp').fontColor([Colors.needs])
          Text(tr('phone_todo_waiting_hidden').replace('{count}',String(this.hiddenWaitingCount))
            .replace('{filter}',this.dueFilterLabel(this.workspace.dueFilter))).fontSize(15).fontColor(Colors.ink).layoutWeight(1)
        }.width('100%')
        Button(tr('project_show_all'),{buttonStyle:ButtonStyleMode.TEXTUAL}).fontSize(15).fontColor(Colors.link)
          .alignSelf(ItemAlign.Start).constraintSize({minHeight:Math.max(36,Math.ceil(22*nativeEnvironment.fontScale)+8)}).padding({left:6,right:6}).onClick(() => this.workspace.setDueFilter('all')).id('todos.due.show_all')
      }.width('100%').padding({left:13,right:10,top:6,bottom:6}).constraintSize({minHeight:44})
        .backgroundColor(Colors.surface).borderRadius(12).margin({top:6}).id('todos.due.hidden')
      if (this.hubMode() && !this.workspace.hubCalm() && !this.workspace.search.trim()) this.hubChoiceBar()
      if (this.workspace.error) Text(tr(this.workspace.error)).fontSize(Tokens.meta)
        .fontColor($r('app.color.persona_danger')).padding(Tokens.inset).width('100%')
      List({space:3}) {
        if (this.workspace.loading && !this.workspace.items.length) ListItem() { LoadingProgress().height(Tokens.touch).width(Tokens.touch).padding(Tokens.page) }
        ForEach(this.sectionBindings, (section: BoundTodoSection) => {
          ListItemGroup({ header: () => this.sectionHeader(section) }) {
            if (this.sectionOpen(section.value)) ForEach(section.rows, (binding: BoundRow<TodoWorkspaceGroup>) => this.todoRow(binding),
              (binding: BoundRow<TodoWorkspaceGroup>) => binding.key)
          }.id(section.key)
        }, (section: BoundTodoSection) => section.key)
        if (!this.sectionBindings.length && !this.workspace.loading) ListItem() {
          Text(tr(this.workspace.search ? 'no_results' : 'empty')).fontSize(Tokens.meta).padding(Tokens.page).width('100%')
        }
      }.width('100%').layoutWeight(1).padding({ left: Tokens.page, right: Tokens.page, bottom: Tokens.inset })
    }.width('100%').height('100%').backgroundColor(Colors.grouped)
  }

  @Builder agentRow(binding: BoundRow<TodoWorkspaceAgent>) {
    if (binding.value.isActive()) {
      ListItem() { this.agentContent(binding) }.onClick(() => this.onOpen(this.workspace.agentSession(binding.value)))
        .bindContextMenu(this.agentMenu(binding), ResponseType.LongPress)
        .id('todo.agent.' + binding.value.machineId + '.' + binding.value.sessionId)
    } else {
      ListItem() { this.agentContent(binding) }.onClick(() => this.onOpen(this.workspace.agentSession(binding.value)))
        .id('todo.agent.' + binding.value.machineId + '.' + binding.value.sessionId)
    }
  }

  @Builder agentContent(binding: BoundRow<TodoWorkspaceAgent>) {
    ComposeListItemV2({ contentItemV2: new ContentItemV2({ primaryText: binding.value.title || tr('agent'),
      secondaryText: this.workspace.machineName(binding.value.machineId) + ' · ' +
        tr('todo_state_' + this.workspace.agentState(binding.value)) }) })
  }

  @Builder agentMenu(binding: BoundRow<TodoWorkspaceAgent>) {
    Menu() { MenuItem({ content: tr('todo_unlink_agent') }).enabled(!this.workspace.targetGone)
      .onClick(() => this.confirmUnlinkAgent(this.workspace.selected, binding.value)) }
  }

  @Builder spaceRow(binding: BoundRow<TodoWorkspaceSpace>) {
    ListItem() {
      ComposeListItemV2({ contentItemV2: new ContentItemV2({ primaryText: binding.value.space,
        secondaryText: this.workspace.machineName(binding.value.machineId) }) })
    }.bindContextMenu(this.spaceMenu(binding), ResponseType.LongPress)
      .id('todo.space.' + binding.value.machineId + '.' + binding.value.space)
  }

  @Builder spaceMenu(binding: BoundRow<TodoWorkspaceSpace>) {
    Menu() { MenuItem({ content: tr('todo_remove_project') }).enabled(!this.workspace.targetGone)
      .onClick(() => this.confirmUnlinkSpace(this.workspace.selected, binding.value)) }
  }

  @Builder detailContent() {
    Column() {
      Scroll() {
        if (this.workspace.selected.id) Column({ space: Tokens.page }) {
          Column({space:8}) {
            Text(tr(this.hubMode() ? todoHubLabel(this.workspace.hubSection(this.workspace.selected, this.clock)) :
              this.workspace.stateLabelKey(this.workspace.selected))).fontSize(13).fontWeight(FontWeight.Medium).fontColor(Colors.link)
              .padding({left:8,right:8,top:3,bottom:3}).backgroundColor(Colors.control).borderRadius(9)
              .bindContextMenu(this.todoMenu(this.workspace.selected), ResponseType.LongPress)
            Text(this.workspace.selected.title).fontSize(21).fontWeight(FontWeight.Bold).fontColor(Colors.ink).maxLines(4)
            this.metadata(this.workspace.selected)
            if (this.hubMode() && this.workspace.selected.hubView?.line && this.workspace.selected.hubView?.line !== this.workspace.selected.startError &&
              this.workspace.selected.hubView?.line !== this.workspace.selected.doneReason && this.workspace.selected.hubView?.line !== this.workspace.selected.notes)
              Text(this.workspace.selected.hubView?.line || '').fontSize(14).fontColor(Colors.secondary)
            if (this.workspace.selected.startError) Text(trError(this.workspace.selected.startError)).fontSize(14).fontColor(Colors.danger)
            if (this.workspace.selected.doneReason) Text(this.workspace.selected.doneReason).fontSize(14).fontColor(Colors.secondary)
            if (this.workspace.selected.notes) MarkdownText({text:this.workspace.selected.notes,onLink:(address:string) => this.onLink(address)})
          }.width('100%').alignItems(HorizontalAlign.Start).padding(15).backgroundColor(Colors.surface).borderRadius(14).id('todo.detail')
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
          ForEach(this.lineageRowBindings, (binding: BoundRow<ResourceRow>) => {
            Row() { ComposeListItemV2({ contentItemV2: new ContentItemV2({ primaryText: binding.value.title || tr('todo_related') }) }) }
              .onClick(() => this.onOpenResource(binding.value))
          }, (binding: BoundRow<ResourceRow>) => binding.key)
          ForEach(this.relatedTodoBindings, (binding: BoundRow<TodoWorkspaceItem>) => {
            Row() { ComposeListItemV2({ contentItemV2: new ContentItemV2({ primaryText: binding.value.title }) }) }
              .onClick(() => this.onOpenResource(this.workspace.todoResource(binding.value)))
          }, (binding: BoundRow<TodoWorkspaceItem>) => binding.key)

          if (this.spaceBindings.length) {
            Text(tr('todo_projects')).fontSize(Tokens.meta).width('100%')
            List() { ForEach(this.spaceBindings, (binding: BoundRow<TodoWorkspaceSpace>) => this.spaceRow(binding),
              (binding: BoundRow<TodoWorkspaceSpace>) => binding.key) }.height(Math.min(300, Tokens.touch * this.spaceBindings.length))
          }

          if (this.activeAgentBindings.length || this.previousAgentBindings.length || !this.workspace.selected.isDone() && this.workspace.candidateSessions().length) Text(tr('todo_agents')).fontSize(Tokens.meta).width('100%')
          ForEach(this.activeAgentBindings, (binding: BoundRow<TodoWorkspaceAgent>) => this.agentRow(binding),
            (binding: BoundRow<TodoWorkspaceAgent>) => binding.key)
          if (this.previousAgentBindings.length) {
            Row() { ComposeListItemV2({ contentItemV2: new ContentItemV2({ primaryText: tr('todo_previously_involved') }) }) }
              .onClick(() => { this.previousAgentsOpen = !this.previousAgentsOpen; })
            if (this.previousAgentsOpen) {
              ForEach(this.previousAgentBindings, (binding: BoundRow<TodoWorkspaceAgent>) => this.agentRow(binding),
                (binding: BoundRow<TodoWorkspaceAgent>) => binding.key)
            }
          }

          if (!this.workspace.selected.isDone()) {
            if (this.workspace.candidateSessions().length) {
              Select(this.sessionOptions()).attributeModifier(SelectStyles.inline).selected(Math.max(0, this.workspace.candidateSessions().findIndex((row: ResourceRow) => this.sessionKey(row) === this.workspace.selectedAgentKey)))
                .value(this.selectedSession()?.title || tr('todo_choose_agent'))
                .onSelect((index: number) => this.selectSession(index))
                .accessibilityText(tr('todo_choose_agent')).width('100%')
              Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('todo_link_agent')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('todo_link_agent', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('todo_link_agent')).bindTips(tr('todo_link_agent')).enabled(!!this.selectedSession() && !this.workspace.saving && !this.workspace.targetGone)
                .onClick(() => this.confirmLinkAgent(this.workspace.selected))
            }
            Button({ type: ButtonType.Circle }) { Image($r('sys.media.ohos_ic_public_more')).width(Tokens.symbol).height(Tokens.symbol).fillColor(Colors.link) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('more')).bindTips(tr('more')).bindMenu(this.advancedMenu)
              .enabled(!this.workspace.saving && !this.workspace.targetGone)
            if (this.workspace.spaceChoices().length) {
              Select(this.spaceOptions()).attributeModifier(SelectStyles.inline).selected(Math.max(0, this.workspace.spaceChoices().findIndex((item: TodoWorkspaceSpaceChoice) => this.choiceKey(item) === this.workspace.selectedSpaceKey)))
                .value(this.selectedSpace()?.label || tr('todo_choose_project'))
                .onSelect((index: number) => this.selectSpace(index))
                .accessibilityText(tr('todo_choose_project')).width('100%')
              Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('todo_link_project')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('todo_link_project', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('todo_link_project')).bindTips(tr('todo_link_project')).enabled(!!this.selectedSpace() && !this.workspace.saving && !this.workspace.targetGone)
                .onClick(() => this.confirmLinkSpace(this.workspace.selected))
            }
          }
          if (this.workspace.error) Text(tr(this.workspace.error)).fontSize(Tokens.meta)
            .fontColor($r('app.color.persona_danger')).width('100%')
        }.alignItems(HorizontalAlign.Start).padding(Tokens.page).width('100%')
        else Text(tr(this.detailEmptyKey())).fontSize(Tokens.meta).padding(Tokens.page).width('100%')
      }.align(Alignment.TopStart).layoutWeight(1).width('100%')
      if (this.workspace.selected.id) Row({ space: Tokens.gap }) {
        Button({ type: ButtonType.Circle }) { if (IconActions.busy(this.workspace.selected.isDone() && !this.workspace.selected.startError ? 'reopen' : 'todo_complete')) LoadingProgress().width(Tokens.symbol).height(Tokens.symbol).color(Colors.onAction); else SymbolGlyph(IconActions.symbol(this.workspace.selected.isDone() && !this.workspace.selected.startError ? 'reopen' : 'todo_complete')).fontSize(Tokens.symbolFont).fontColor([IconActions.color(this.workspace.selected.isDone() && !this.workspace.selected.startError ? 'reopen' : 'todo_complete', true)]) }.attributeModifier(ButtonStyles.iconPrimary).accessibilityText(this.completionLabel()).bindTips(this.completionLabel()).enabled(!this.workspace.saving && !this.workspace.targetGone).onClick(() => this.changeCompletion())
        Button({ type: ButtonType.Circle }) { SymbolGlyph(Symbols.edit).fontSize(Tokens.symbolFont).fontColor([Colors.link]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('edit')).enabled(!this.workspace.saving && !this.workspace.targetGone)
          .onClick(() => this.workspace.beginEdit(this.workspace.selected))
      }.justifyContent(FlexAlign.End).padding(Tokens.inset).width('100%')
    }.width('100%').height('100%').backgroundColor(Colors.grouped)
  }

  @Builder editor() {
    Column({ space: Tokens.page }) {
      TextInput({ text: this.workspace.titleDraft, placeholder: tr('title') }).onChange((value: string) => this.workspace.setTitleDraft(value))
        .id('todos.title')
      TextArea({ text: this.workspace.notesDraft, placeholder: tr('todo_notes') }).onChange((value: string) => this.workspace.setNotesDraft(value))
        .height(Tokens.inputExpanded).id('todos.notes')
      if (this.workspace.error) Text(tr(this.workspace.error)).fontColor($r('app.color.persona_danger')).width('100%')
      Row({ space: Tokens.gap }) {
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('save')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('save', true)]) }.attributeModifier(ButtonStyles.iconPrimary).accessibilityText(tr('save')).bindTips(tr('save'))
          .enabled(!this.workspace.saving && !this.workspace.targetGone && !!this.workspace.titleDraft.trim())
          .onClick(() => this.workspace.save().catch(() => undefined)).id('todos.save')
      }.width('100%').justifyContent(FlexAlign.End)
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
