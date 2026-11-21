import { LengthMetrics } from '@kit.ArkUI';
import { pasteboard } from '@kit.BasicServicesKit';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { Tokens } from '../design/Tokens';
import { PhoneIconButton, PhoneSectionTitle, PhoneStyle } from '../design/PhoneStyle';
import { tr } from '../design/Strings';
import { ClientModel } from '../models/ClientModel';
import { ProjectCatalog, PersonaProject, ProjectAgent, ProjectWorktree } from '../models/ProjectCatalog';
import { ResourceRow } from '../models/Wire';
import { ProjectLine, ProjectHost, projectOutline, projectHosts, worktreeSessionLines } from '../models/ProjectOutline';
import { WorktreeActions, WorktreeChange } from '../models/WorktreeActions';
import { WorktreeSheet } from './WorktreeSheet';
import { nativeEnvironment } from '../platform/NativeEnvironment';

@ComponentV2
export struct ProjectsPage {
  @Require @Param model: ClientModel;
  @Require @Param catalog: ProjectCatalog;
  @Param page: string = 'projects';
  @Param target: string = '';
  private scroller: Scroller = new Scroller();
  private restoredRoute: string = '';
  @Local refreshing: boolean = false;
  @Local editor: WorktreeActions = new WorktreeActions();
  @Local editorShown: boolean = false;
  private editedTree: ProjectWorktree | undefined;
  @Monitor('catalog.loading') finishedRefresh(): void { if (!this.catalog.loading) this.refreshing = false; }
  @Monitor('model.profile') ownerChanged(): void { this.editor.close(); this.editorShown = false; this.restoredRoute = ''; }
  aboutToDisappear(): void { this.editor.close(); }
  @Event onProject: (key: string) => void = () => {};
  @Event onTree: (key: string) => void = () => {};
  @Event onSession: (row: ResourceRow) => void = () => {};
  @Event onStart: (tree: ProjectWorktree) => void = () => {};
  @Event onChanged: (tree: ProjectWorktree, change: WorktreeChange) => void = () => {};
  @Event onRefresh: () => void = () => {};

  private readonly activityKinds: string[] = ['needs_you', 'working', 'agent_group_unread', 'agent_group_waiting', 'idle', 'offline'];
  private count(n: number, word: string): string { return String(n) + ' ' + tr(word); }
  private project(): PersonaProject | undefined { return this.catalog.projects.find((row: PersonaProject) => row.key === this.target); }
  @Computed get hosts(): ProjectHost[] { return projectHosts(this.catalog.projects); }
  @Computed get hostActivity(): Map<string, Map<string, number>> {
    const byHost = new Map<string, Map<string, number>>();
    for (const host of this.hosts) {
      const counts = new Map<string, number>();
      for (const project of host.projects) {
        for (const tree of project.trees) {
          for (const agent of tree.agents) {
            let kind = agent.state;
            if (agent.rank === 6) kind = 'agent_group_waiting';
            else if (kind === 'unread') kind = 'agent_group_unread';
            else if (!this.activityKinds.includes(kind)) kind = 'idle';
            counts.set(kind, (counts.get(kind) || 0) + 1);
          }
        }
      }
      byHost.set(host.key, counts);
    }
    return byHost;
  }
  private hostKinds(key: string): string[] {
    return this.activityKinds.filter((kind: string) => (this.hostActivity.get(key)?.get(kind) || 0) > 0);
  }
  private hostSummary(key: string): string {
    return this.hostKinds(key).map((kind: string) => this.count(this.hostActivity.get(key)!.get(kind)!, kind)).join(' · ');
  }
  private activityTint(kind: string): ResourceColor {
    if (kind === 'needs_you') return Colors.needs;
    if (kind === 'working') return Colors.working;
    if (kind === 'agent_group_unread') return Colors.unread;
    return kind === 'agent_group_waiting' ? Colors.ai : Colors.secondary;
  }
  private filteringText(): boolean { return this.page === 'projects' && !!this.catalog.browser.query.trim(); }
  private treeInset(level: number): number {
    // Reserve the same mark column for plain and primary worktrees. Limit deep indentation on a phone.
    return PhoneStyle.inset + (nativeEnvironment.fontScale >= 1.5 ? 7 : 22) * Math.min(level, 3);
  }
  @Computed get detailTree(): ProjectWorktree | undefined {
    return this.page === 'project_tree' ? this.catalog.tree(this.target) : undefined;
  }
  @Computed get detailSessions(): ProjectLine[] {
    const tree = this.detailTree;
    if (!tree) return [];
    const project = this.catalog.projects.find((p: PersonaProject) => p.trees.includes(tree));
    return project ? worktreeSessionLines(project, tree, this.catalog.browser) : [];
  }
  @Computed get lines(): ProjectLine[] {
    return projectOutline(this.catalog.projects,this.catalog.browser,this.page === 'project' ? this.target : '');
  }
  @Computed get tabRows(): Map<string, ProjectLine> {
    const result = new Map<string, ProjectLine>();
    const rows = this.page === 'project_tree' ? this.detailSessions : this.lines;
    for (const row of rows) if (row.kind === 'tab') result.set(row.key, row);
    return result;
  }
  private route(): string { return this.page + '/' + this.target; }
  private restorePosition(): void {
    if (!this.lines.length || this.restoredRoute === this.route()) return;
    const route = this.route(), key = this.catalog.browser.anchor(route);
    this.restoredRoute = route;
    if (!key) return;
    const index = this.lines.findIndex((l:ProjectLine) => l.key === key);
    if (index > 0) this.scroller.scrollToIndex(index,false,ScrollAlign.START);
  }
  private search(text:string):void {
    this.catalog.browser.query = text; this.catalog.browser.remember(this.route(),'');
    if (this.lines.length) this.scroller.scrollToIndex(0,false,ScrollAlign.START);
  }
  private selectMachine(key:string):void {
    this.catalog.browser.machine = key; this.catalog.browser.remember(this.route(),'');
    if (this.lines.length) this.scroller.scrollToIndex(0,false,ScrollAlign.START);
  }
  private toggle(line:ProjectLine):void {
    this.catalog.toggleBrowserFold(this.model,line.key,line.open);
  }
  private tabOpen(key: string): boolean {
    return this.filteringText() || this.catalog.browser.open(key, true);
  }
  private toggleTab(key: string): void {
    if (this.filteringText() || !this.tabRows.has(key)) return;
    this.catalog.toggleBrowserFold(this.model, key, this.catalog.browser.open(key, true));
  }
  private tabDescription(key: string): string {
    const row = this.tabRows.get(key);
    if (!row) return '';
    return [row.tabTitle, row.tabLead?.row.title || '', tr('project_agent_count').replace('{count}',String(row.count)),
      this.filteringText() ? '' : tr(this.tabOpen(key) ? 'agent_collapse' : 'agent_expand')].filter((v:string) => !!v).join(' · ');
  }
  private tabTint(key: string): ResourceColor {
    const lead = this.tabRows.get(key)?.tabLead;
    return lead ? this.tint(lead) : Colors.secondary;
  }
  private openHeader(line: ProjectLine): void {
    if (!this.filteringText()) this.toggle(line);
    else if (line.kind === 'host') this.selectMachine(line.project.hostKey);
    else this.onProject(line.project.key);
  }
  private openTree(tree: ProjectWorktree): void {
    const entry = this.catalog.tree(tree.key);
    if (!entry) return;
    if (entry.agents.length) this.chooseAgent(entry.agents[0]); else this.chooseTree(entry.key);
  }
  private chooseAgent(agent:ProjectAgent):void {
    this.catalog.browser.selectedTree = ''; this.catalog.browser.selectedSession = agent.row.key();
    this.onSession(agent.row);
  }
  private chooseTree(key:string):void {
    if (!this.catalog.tree(key)) return;
    this.catalog.browser.selectedSession = ''; this.catalog.browser.selectedTree = key;
    this.onTree(key);
  }
  private age(agent:ProjectAgent):string {
    if (!agent.updated) return '';
    const minutes = Math.max(0,Math.floor((Date.now() - agent.updated)/60000));
    if (!minutes) return tr('agent_time_just_now');
    if (minutes < 60) return String(minutes) + tr('agent_time_minute_suffix');
    if (minutes < 1440) return String(Math.floor(minutes/60)) + tr('agent_time_hour_suffix');
    return String(Math.floor(minutes/1440)) + tr('agent_time_day_suffix');
  }
  private tint(agent:ProjectAgent):ResourceColor {
    if (agent.state === 'needs_you' || agent.rank === 2) return Colors.needs;
    if (agent.state === 'unread' || agent.rank === 5) return Colors.unread;
    return agent.state === 'working' ? Colors.working : Colors.secondary;
  }
  private treeDetail(tree:ProjectWorktree):string {
    const parent = tree.parentKey ? this.catalog.tree(tree.parentKey) : undefined;
    if (parent && tree.projectTitle && parent.projectTitle && tree.projectTitle !== parent.projectTitle) return tree.projectTitle;
    if (tree.branch) return tree.branch === tree.title ? '' : tree.branch;
    if (tree.workspaceKind === 'git') return tr('project_detached') + (tree.head ? ' @ ' + tree.head.slice(0,7) : '');
    return '';
  }
  private async copyWorktree(tree:ProjectWorktree, path:boolean):Promise<void> {
    const current = this.menuTarget(tree.key), text = path ? tree.cwd : tree.title;
    if (!current || !text || (path ? current.cwd : current.title) !== text) return;
    const epoch = this.model.connectionGeneration(), scope = this.model.scope();
    try { await pasteboard.getSystemPasteboard().setData(pasteboard.createData(pasteboard.MIMETYPE_TEXT_PLAIN,text)); }
    catch (_) { if (this.model.owns(epoch,scope)) this.catalog.notice = tr('failed'); }
  }
  private menuTarget(key: string): ProjectWorktree | undefined {
    if (this.page === 'project_tree' && this.target !== key) return undefined;
    return this.catalog.tree(key);
  }
  private menuEntries(key: string): ProjectWorktree[] {
    const current = this.menuTarget(key);
    return current ? [current] : [];
  }
  private projectMenuRoots(key: string): ProjectWorktree[] {
    const project = this.catalog.projects.find((item: PersonaProject) => item.key === key);
    return project && project.roots.length === 1 ? project.roots : [];
  }
  private headerIdentity(tree: ProjectWorktree): string {
    return JSON.stringify([tree.key, tree.cwd, tree.title, tree.branch, tree.machineTitle, tree.parentKey, tree.breadcrumb,
      tree.online, tree.canStart, tree.permits('create'), tree.permits('rename'), tree.permits('clean')]);
  }
  private startFromTree(key: string): void {
    const current = this.menuTarget(key);
    if (current?.canStart) this.onStart(current);
  }
  private chooseParent(key: string): void {
    const current = this.menuTarget(key);
    if (current?.parentKey) this.chooseTree(current.parentKey);
  }
  private openEditor(tree: ProjectWorktree, action: string): void {
    if (this.editor.busy) return;
    const current = this.menuTarget(tree.key);
    if (!current || current.cwd !== tree.cwd || !current.permits(action)) return;
    this.editedTree = current; this.editorShown = true; this.editor.open(this.model, current, action);
  }
  private async complete(check: boolean = false, replay: boolean = false): Promise<void> {
    const tree = this.editedTree;
    if (!tree) return;
    const change = check ? await this.editor.reconcile(replay) : await this.editor.submit();
    if (!change || this.editedTree !== tree || !this.editorShown) return;
    this.editorShown = false; this.onChanged(tree, change);
  }
  @Builder editorSheet() {
    WorktreeSheet({ form: this.editor, onCancel: () => { if (!this.editor.busy) this.editorShown = false; },
      onSubmit: () => this.complete(), onReconcile: (replay: boolean) => this.complete(true,replay),
      onRetry: () => { if (this.editedTree) this.editor.open(this.model, this.editedTree, this.editor.action); } })
  }
  @Builder actionMenu(key: string) {
    Menu() {
      ForEach(this.menuEntries(key), (tree: ProjectWorktree) => {
        MenuItemGroup() {
          if (this.page !== 'project_tree') MenuItem({ content: tr('project_view_worktree') })
            .onClick(() => this.chooseTree(tree.key)).id('tree.action.open')
          if (tree.canStart) MenuItem({content:tr('project_start_here')}).onClick(() => this.startFromTree(tree.key)).id('tree.action.start')
          if (tree.cwd) MenuItem({content:tr('project_copy_path')}).onClick(() => this.copyWorktree(tree,true)).id('tree.action.copy')
          MenuItem({content:tr('project_copy_name')}).onClick(() => this.copyWorktree(tree,false)).id('tree.action.copy_name')
          if (tree.parentKey) MenuItem({content:tr('project_open_parent')}).onClick(() => this.chooseParent(tree.key)).id('tree.action.parent')
          if (tree.permits('create')) MenuItem({ content: tr('tree_create') }).onClick(() => this.openEditor(tree, 'create')).id('tree.action.create')
          if (tree.permits('rename')) MenuItem({ content: tr('tree_rename') }).onClick(() => this.openEditor(tree, 'rename')).id('tree.action.rename')
          if (tree.permits('clean')) MenuItem({ content: tr('tree_cleanup') }).contentFontColor(Colors.danger).onClick(() => this.openEditor(tree, 'clean')).id('tree.action.clean')
        }
      }, (tree: ProjectWorktree) => this.headerIdentity(tree))
    }
  }
  @Builder more(tree: ProjectWorktree) {
    if (tree.cwd || tree.canStart || tree.permits('create') || tree.permits('rename') || tree.permits('clean')) {
      Button({ type: ButtonType.Circle }) {
        Image(Symbols.more).width(20).height(20).fillColor(Colors.ink)
      }.width(38).height(38).padding(0).backgroundColor(Colors.control).accessibilityText(tr('tree_actions'))
        .responseRegion({ x: -3, y: -3, width: 44, height: 44 })
        .bindMenu(this.actionMenu(tree.key)).id('tree.actions')
    }
  }
  @Builder icon(symbol: Resource, tint: ResourceColor) {
    Stack() { SymbolGlyph(symbol).fontSize('18vp').fontColor([tint]) }
      .width(32).height(32).borderRadius(8).backgroundColor(Colors.control)
  }
  @Builder detailRow(title: string, detail: string, symbol: Resource, color: ResourceColor, identity: string, action: () => void) {
    Row({ space: 11 }) {
      this.icon(symbol, color)
      Column({ space: 2 }) {
        Text(title).fontSize(17).fontWeight(FontWeight.Medium).fontColor(Colors.ink).maxLines(2).textOverflow({ overflow: TextOverflow.Ellipsis })
        if (detail) Text(detail).fontSize(13).fontColor(Colors.secondary).maxLines(2).textOverflow({ overflow: TextOverflow.Ellipsis })
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
      SymbolGlyph(Symbols.expand).fontSize('13vp').fontColor([Colors.secondary])
    }.width('100%').padding({ left: 14, right: 14, top: 10, bottom: 10 }).constraintSize({ minHeight: 52 })
      .accessibilityGroup(true).accessibilityText(title + (detail ? ' · ' + detail : '')).onClick(action).id(identity)
  }
  @Builder message(value: string) {
    Text(value).fontSize(15).fontColor(Colors.secondary).padding(16).width('100%').textAlign(TextAlign.Start)
  }
  @Builder machineMenu() {
    Menu() {
      MenuItem({content:tr('project_all_machines')}).selected(!this.catalog.browser.machine).selectIcon(true).onClick(() => this.selectMachine(''))
      ForEach(this.hosts,(host:ProjectHost) => {
        MenuItem({content:host.name}).selected(this.catalog.browser.machine === host.key).selectIcon(true).onClick(() => this.selectMachine(host.key))
      },(host:ProjectHost) => host.key)
    }
  }
  @Builder searchAndFilter() {
    Row({space:8}) {
      Search({value:this.catalog.browser.query,placeholder:tr('project_search')}).searchIcon({size:'16vp'}).height(Math.max(38, 21 * nativeEnvironment.fontScale + 12)).layoutWeight(1)
        .searchButton('').borderRadius(11).fontColor(Colors.ink).backgroundColor(Colors.control)
        .onChange((value:string) => this.search(value)).id('projects.search')
      if (this.hosts.length > 1 || this.catalog.browser.machine) Button({type:ButtonType.Circle}) {
        SymbolGlyph(Symbols.filter).fontSize('17vp').fontColor([this.catalog.browser.machine ? Colors.onAction : Colors.ink])
      }.width(36).height(36).padding(0).backgroundColor(this.catalog.browser.machine ? Colors.action : Colors.surface)
        .responseRegion({ x: -4, y: -4, width: 44, height: 44 })
        .accessibilityText(tr('agent_filter_machine')).accessibilityDescription(this.hosts.find((host: ProjectHost) =>
          host.key === this.catalog.browser.machine)?.name || tr('project_all_machines')).bindMenu(this.machineMenu).id('projects.machine')
      PhoneIconButton({symbol:Symbols.refresh,label:tr('refresh'),identity:'projects.refresh',active:!this.catalog.loading,action:() => this.onRefresh()})
    }.width('100%').padding({bottom:8})
  }
  @Builder projectMenu(key: string) {
    Menu() {
      MenuItem({content:tr('project_open')}).onClick(() => this.onProject(key)).id('projects.open_project')
      ForEach(this.projectMenuRoots(key), (tree: ProjectWorktree) => {
        MenuItemGroup() {
          MenuItem({content:tr('project_view_worktree')}).onClick(() => this.chooseTree(tree.key)).id('tree.action.open')
          if (tree.canStart) MenuItem({content:tr('project_start_here')}).onClick(() => this.startFromTree(tree.key)).id('tree.action.start')
          if (tree.permits('create')) MenuItem({content:tr('tree_create')}).onClick(() => this.openEditor(tree,'create')).id('tree.action.create')
        }
      }, (tree: ProjectWorktree) => this.headerIdentity(tree))
    }
  }
  @Builder agentMark(agent:ProjectAgent) {
    Stack({ alignContent: Alignment.BottomEnd }) {
      Stack() {
        if (agent.runtime.toLowerCase().includes('claude')) Image($r('app.media.project_agent_claude')).width(17).height(17).objectFit(ImageFit.Contain)
        else if (agent.runtime.toLowerCase().includes('codex') || agent.runtime.toLowerCase().includes('openai'))
          Image($r('app.media.project_agent_openai')).width(17).height(17).fillColor(Colors.ink).objectFit(ImageFit.Contain)
        else if (agent.runtime) Text(agent.runtime.slice(0,1).toUpperCase()).fontSize('16vp').fontWeight(FontWeight.Medium).fontColor(Colors.ink)
        else SymbolGlyph(Symbols.agent).fontSize('17vp').fontColor([Colors.secondary])
      }.width(36).height(36).backgroundColor(Colors.subtle).borderRadius(18)
      if (agent.state && agent.state !== 'unknown') Circle().width(12).height(12).fill(this.tint(agent))
        .stroke(Colors.surface).strokeWidth(2)
    }.width(36).height(36)
  }
  private agentContext(agent: ProjectAgent, paneName: string = '', starter: boolean = false): string {
    const special = [2, 4, 5, 6].includes(agent.rank) || agent.state === 'offline';
    return [special ? tr(agent.stateLabel) : '', paneName || agent.tab, starter ? tr('session_space_starter') : '', agent.model || agent.runtime]
      .filter((value: string) => !!value).join(' · ');
  }
  @Builder needsReply() {
    Text(tr('needs_you')).fontSize(12).fontWeight(FontWeight.Medium).fontColor(Colors.needs)
      .padding({left:6,right:6,top:1,bottom:1}).backgroundColor(Colors.selection).borderRadius(6)
      .constraintSize({maxWidth:nativeEnvironment.fontScale >= 1.5 ? '100%' : '41%'}).maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis})
  }
  @Builder agentCell(agent:ProjectAgent, treeKey:string, identity:string, paneName:string = '', starter:boolean = false) {
    Row({space:9}) {
      this.agentMark(agent)
      Column({space:3}) {
        Row({space:6}) {
          Text(agent.row.title || tr('agent_untitled')).fontSize(16).fontWeight(FontWeight.Bold).fontColor(Colors.ink)
            .layoutWeight(1).maxLines(nativeEnvironment.fontScale >= 1.5 ? 4 : 2).textOverflow({overflow:TextOverflow.Ellipsis})
          if (nativeEnvironment.fontScale < 1.5 && this.age(agent)) Text(this.age(agent)).fontSize(11).fontColor(Colors.secondary).maxLines(1)
        }.width('100%')
        if (agent.preview) Row({space:6}) {
          Text(agent.preview).maxLines(nativeEnvironment.fontScale >= 1.5 ? 4 : 2).fontSize(14).fontColor(Colors.secondary)
            .layoutWeight(1).textOverflow({overflow:TextOverflow.Ellipsis})
          if (agent.state === 'needs_you' && nativeEnvironment.fontScale < 1.5) this.needsReply()
        }.width('100%').alignItems(VerticalAlign.Top)
        if (agent.state === 'needs_you' && (!agent.preview || nativeEnvironment.fontScale >= 1.5)) this.needsReply()
        if (this.agentContext(agent,paneName,starter)) Text(this.agentContext(agent,paneName,starter)).width('100%').textAlign(TextAlign.Start)
          .fontSize(12).fontColor(Colors.secondary).maxLines(nativeEnvironment.fontScale >= 1.5 ? 3 : 1).textOverflow({overflow:TextOverflow.Ellipsis})
        if (nativeEnvironment.fontScale >= 1.5 && this.age(agent)) Text(this.age(agent)).fontSize(11).fontColor(Colors.secondary)
      }.alignItems(HorizontalAlign.Start).layoutWeight(1)
    }.width('100%').alignItems(VerticalAlign.Top).constraintSize({minHeight:58}).padding({top:9,bottom:9,right:PhoneStyle.inset})
      .accessibilityGroup(true).accessibilityText([agent.row.title,agent.preview,tr(agent.stateLabel),this.agentContext(agent,paneName,starter),this.age(agent)].filter((v:string) => !!v).join(' · '))
      .accessibilitySelected(this.catalog.browser.selectedSession === agent.row.key())
      .onClick(() => this.chooseAgent(agent)).bindContextMenu(this.actionMenu(treeKey),ResponseType.LongPress).id(identity)
  }
  @Builder foldChevron(open:boolean) {
    SymbolGlyph(open ? Symbols.down : Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
  }
  // A virtual row can outlive the projection object that created it. Keep only its scoped key in this builder.
  @Builder tabLine(key: string) {
    Row({ space: 6 }) {
      Stack() { Circle().width(7).height(7).fill(this.tabTint(key)) }.width(18)
      if (nativeEnvironment.fontScale >= 1.5) {
        Column({space:2}) {
          Text(this.tabRows.get(key)?.tabTitle || '').fontSize(15).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
          if (this.tabRows.get(key)?.tabLead?.row.title) Text(this.tabRows.get(key)?.tabLead?.row.title || '').fontSize(13).fontColor(Colors.secondary).maxLines(3)
          Text(tr('project_agent_count').replace('{count}', String(this.tabRows.get(key)?.count || 0))).fontSize(13).fontColor(Colors.secondary)
        }.layoutWeight(1).alignItems(HorizontalAlign.Start)
      } else {
        Text(this.tabRows.get(key)?.tabTitle || '').fontSize(15).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
          .constraintSize({ maxWidth: '40%' }).maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
        Text(this.tabRows.get(key)?.tabLead?.row.title || '').fontSize(13).fontColor(Colors.secondary).layoutWeight(1)
          .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
        Text(tr('project_agent_count').replace('{count}', String(this.tabRows.get(key)?.count || 0)))
          .fontSize(13).fontColor(Colors.secondary).maxLines(1)
      }
      SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary]).rotate({angle:this.tabOpen(key) ? 90 : 0})
        .visibility(this.filteringText() ? Visibility.None : Visibility.Visible)
    }.width('100%').constraintSize({minHeight:44}).padding({left:this.treeInset(this.tabRows.get(key)?.level || 0),right:PhoneStyle.inset})
      .accessibilityGroup(true).accessibilityText(this.tabDescription(key)).onClick(() => this.toggleTab(key)).id('projects.tab')
  }
  @Builder countItems(key: string) {
    ForEach(this.hostKinds(key), (kind: string) => {
      Row({ space: 4 }) {
        Circle().width(6).height(6).fill(this.activityTint(kind))
        Text(String(this.hostActivity.get(key)?.get(kind) || 0)).fontSize(13).fontWeight(FontWeight.Medium).fontColor(Colors.secondary)
      }
    }, (kind: string) => kind)
  }
  @Builder counts(key: string) {
    if (nativeEnvironment.fontScale >= 1.5) {
      Flex({wrap:FlexWrap.Wrap, space:{main:LengthMetrics.vp(8),cross:LengthMetrics.vp(4)}}) { this.countItems(key) }.width('100%')
    } else {
      Row({space:8}) { this.countItems(key) }
    }
  }
  @Builder headerLine(line:ProjectLine) {
    Row({space:8}) {
      if (line.kind === 'host') {
        Stack() { SymbolGlyph(Symbols.device).fontSize('17vp').fontColor([Colors.ink]) }.width(29).height(29).backgroundColor(Colors.surface).borderRadius(8)
        Column({ space: 3 }) {
          Text(line.project.machine).fontSize(20).fontWeight(FontWeight.Bold).fontColor(Colors.ink)
            .maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis})
          if (!this.filteringText() && (nativeEnvironment.fontScale >= 1.5 || this.hostKinds(line.project.hostKey).length > 3)) this.counts(line.project.hostKey)
        }.layoutWeight(1).alignItems(HorizontalAlign.Start)
        if (!this.filteringText() && nativeEnvironment.fontScale < 1.5 && this.hostKinds(line.project.hostKey).length <= 3) this.counts(line.project.hostKey)
      } else {
        SymbolGlyph(Symbols.folder).fontSize('17vp').fontColor([Colors.secondary]).width(18)
        Text(line.project.title).fontSize(17).fontWeight(FontWeight.Bold)
          .fontColor(Colors.ink).layoutWeight(1).maxLines(nativeEnvironment.fontScale >= 1.5 ? 3 : 1).textOverflow({overflow:TextOverflow.Ellipsis})
      }
      if (this.filteringText() && line.kind === 'host') SymbolGlyph(Symbols.filter).fontSize('13vp').fontColor([Colors.secondary])
      else this.foldChevron(!this.filteringText() && line.open)
    }.width('100%').constraintSize({minHeight:line.kind === 'host' ? 52 : 44})
      .padding({left:line.kind === 'host' ? 2 : PhoneStyle.inset,right:PhoneStyle.inset,top:4,bottom:4})
      .accessibilityGroup(true).accessibilityText([line.kind === 'host' ? line.project.machine : line.project.title,
        line.kind === 'host' && !this.filteringText() ? this.hostSummary(line.project.hostKey) : this.count(line.count,'project_sessions'),
        tr(this.filteringText() ? (line.kind === 'host' ? 'agent_filter_machine' : 'project_open') : line.open ? 'agent_collapse' : 'agent_expand')]
        .filter((part: string) => !!part).join(' · '))
      .onClick(() => this.openHeader(line)).id(line.kind === 'host' ? 'projects.host' : 'projects.project')
  }
  private treeSummary(line:ProjectLine):string {
    return [line.tree!.main ? tr('project_primary') : '', this.treeDetail(line.tree!),
      line.count ? tr('project_agent_count').replace('{count}',String(line.count)) : ''].filter((part:string) => !!part).join(' · ');
  }
  @Builder treeLine(line:ProjectLine) {
    Row({space:0}) {
      Row({space:6}) {
        Stack() {
          if (line.tree!.main) SymbolGlyph($r('sys.symbol.house')).fontSize('14vp').fontColor([Colors.secondary])
        }.width(18)
        if (nativeEnvironment.fontScale >= 1.5) {
          Column({space:3}) {
            Row({space:5}) {
              Text(line.tree!.title).fontSize(15).fontWeight(FontWeight.Medium).fontColor(line.tree!.online ? Colors.ink : Colors.secondary).layoutWeight(1)
              if (line.tree!.pinnedAt > 0) SymbolGlyph($r('sys.symbol.pin')).fontSize('12vp').fontColor([Colors.link])
            }.width('100%')
            if (this.treeSummary(line)) Text(this.treeSummary(line)).fontSize(12).fontColor(Colors.secondary).maxLines(4)
          }.alignItems(HorizontalAlign.Start).layoutWeight(1)
        } else {
          Text(line.tree!.title).fontSize(15).fontWeight(FontWeight.Medium).fontColor(line.tree!.online ? Colors.ink : Colors.secondary)
            .constraintSize({maxWidth:this.treeDetail(line.tree!) ? '59%' : '76%'}).maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis})
          if (line.tree!.pinnedAt > 0) SymbolGlyph($r('sys.symbol.pin')).fontSize('12vp').fontColor([Colors.link])
          if (line.tree!.main) Text(tr('project_primary')).fontSize(11).fontColor(Colors.secondary)
            .backgroundColor(Colors.control).borderRadius(5).padding({left:4,right:4,top:2,bottom:2})
          if (this.treeDetail(line.tree!)) Text(this.treeDetail(line.tree!)).fontSize(12)
            .fontColor(line.tree!.workspaceKind === 'git' && !line.tree!.branch ? Colors.working : Colors.secondary)
            .layoutWeight(1).maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis})
          else Blank().layoutWeight(1)
          if (line.count) Text(line.count === 1 ? this.count(1,'agent') : tr('project_agent_count').replace('{count}',String(line.count)))
            .fontSize(13).fontColor(Colors.secondary).maxLines(1)
        }
      }.layoutWeight(1).constraintSize({minHeight:44}).accessibilityGroup(true)
        .accessibilityText([line.tree!.title,this.treeDetail(line.tree!),line.tree!.cwd,this.count(line.count,'project_sessions')].filter((v:string) => !!v).join(' · '))
        .onClick(() => { if (this.filteringText() || line.tree!.source || !line.count) this.openTree(line.tree!); else this.toggle(line); })
        .bindContextMenu(this.actionMenu(line.tree!.key),ResponseType.LongPress).id('projects.worktree')
      if (!this.filteringText() && (line.count || line.tree!.children.length)) Button({type:ButtonType.Normal}) { this.foldChevron(line.open) }
        .width(44).height(44).padding(0).backgroundColor(Color.Transparent).accessibilityText(tr(line.open ? 'agent_collapse' : 'agent_expand'))
        .onClick(() => this.toggle(line)).id('projects.fold')
    }.width('100%').padding({left:this.treeInset(line.level),right:this.filteringText() ? PhoneStyle.inset : 3})
      .id('projects.tree_owner.' + line.tree!.machine + '.' + line.tree!.source + '.' + line.tree!.sourceId)
  }
  @Builder subsidiaryLine(line:ProjectLine) {
    Row({space:8}) {
      SymbolGlyph(line.kind === 'children' ? Symbols.folder : line.kind === 'more' ? Symbols.agents : Symbols.check).fontSize('14vp').fontColor([Colors.secondary]).width(18)
      Text(line.kind === 'more' ? tr('project_show_all') + ' ' + this.count(line.count,'project_sessions') :
        line.kind === 'children' ? this.count(line.count,'project_children') : tr('agent_group_idle') + ' · ' + String(line.count))
        .fontSize(14).fontColor(line.kind === 'more' ? Colors.link : Colors.secondary).layoutWeight(1)
      if (!this.filteringText() || line.kind === 'more') this.foldChevron(line.kind !== 'more' && line.open)
    }.width('100%').constraintSize({minHeight:44}).padding({left:this.treeInset(line.level),right:PhoneStyle.inset})
      .accessibilityGroup(true).onClick(() => {
        if (line.kind === 'more') this.catalog.browser.showAll(line.tree!.key);
        else if (!this.filteringText()) this.toggle(line);
      })
      .id(line.kind === 'children' ? 'projects.children' : line.kind === 'more' ? 'projects.show_all' : 'projects.idle_agents')
  }
  @Builder lineContent(line:ProjectLine) {
    if (line.kind === 'host') this.headerLine(line)
    else if (line.kind === 'idle-projects') Text(tr('project_idle_repositories')).fontSize(14).fontColor(Colors.secondary)
      .padding({left:6,right:8,top:10,bottom:5})
    else if (line.kind === 'project') Row() { this.headerLine(line) }.width('100%').bindContextMenu(this.projectMenu(line.project.key),ResponseType.LongPress)
    else if (line.kind === 'tree') this.treeLine(line)
    else if (line.kind === 'tab') this.tabLine(line.key)
    else if (line.kind === 'session') Row() { this.agentCell(line.agent!,line.tree!.key,'projects.inline_session',line.paneName,line.starter) }
      .width('100%').padding({left:this.treeInset(line.level)})
    else this.subsidiaryLine(line)
  }
  @Builder repositoryList() {
    Column() {
      if (this.page === 'projects') this.searchAndFilter()
      else if (this.project()) PhoneSectionTitle({text:this.project()!.machine,trailing:this.count(this.project()!.trees.length,'project_worktrees')})
      List({scroller:this.scroller}) {
        Repeat(this.lines).each((entry:RepeatItem<ProjectLine>) => {
          ListItem() {
            Column() {
              this.lineContent(entry.item)
            }.width('100%').alignItems(HorizontalAlign.Start)
              .backgroundColor(entry.item.kind === 'host' || entry.item.kind === 'idle-projects' ? Color.Transparent :
                entry.item.agent && entry.item.agent!.row.key() === this.catalog.browser.selectedSession ||
                entry.item.kind === 'tree' && entry.item.tree!.key === this.catalog.browser.selectedTree ? Colors.selection : Colors.surface)
              .borderRadius({topLeft:entry.item.first ? PhoneStyle.cardRadius : 0,topRight:entry.item.first ? PhoneStyle.cardRadius : 0,
                bottomLeft:entry.item.last ? PhoneStyle.cardRadius : 0,bottomRight:entry.item.last ? PhoneStyle.cardRadius : 0})
              .margin({bottom:entry.item.last ? 10 : 0})
          }
        }).key((line:ProjectLine) => line.key).virtualScroll({totalCount:this.lines.length,reusable:false})
        if (!this.lines.length && !this.catalog.loading) ListItem() {
          this.message(tr(this.catalog.browser.query || this.catalog.browser.machine ? 'no_results' :
            this.catalog.partial ? 'project_catalog_unavailable' : 'project_empty'))
        }
      }.layoutWeight(1).width('100%').scrollBar(BarState.Auto).maintainVisibleContentPosition(true)
        .onAreaChange(() => this.restorePosition())
        .onScrollIndex((start:number) => {
          if (this.restoredRoute === this.route() && start >= 0 && start < this.lines.length)
            this.catalog.browser.remember(this.route(),this.lines[start].key);
        }).id(this.page === 'projects' ? 'projects.list' : 'projects.worktrees')
    }.width('100%').height('100%').padding({left:PhoneStyle.page,right:PhoneStyle.page})
  }
  @Builder detailHeader(tree: ProjectWorktree) {
    ListItem() {
        Column({ space: 10 }) {
          if (nativeEnvironment.fontScale >= 1.5) {
            Flex({wrap:FlexWrap.Wrap,justifyContent:FlexAlign.SpaceBetween,alignItems:ItemAlign.Center,
              space:{main:LengthMetrics.vp(10),cross:LengthMetrics.vp(4)}}) {
              Text(tree.machineTitle).fontColor(Colors.ink).fontSize(17).fontWeight(FontWeight.Medium).constraintSize({maxWidth:'100%'})
              this.more(tree)
            }.width('100%')
            if (tree.branch) Text(tree.branch).fontFamily('monospace').fontColor(Colors.secondary).fontSize(13).wordBreak(WordBreak.BREAK_ALL)
          } else {
            Row({ space: 10 }) {
              this.icon(Symbols.device, Colors.working)
              Column({ space: 2 }) {
                Text(tree.machineTitle).fontColor(Colors.ink).fontSize(17).fontWeight(FontWeight.Medium)
                  .maxLines(2).textOverflow({ overflow: TextOverflow.Ellipsis })
                if (tree.branch) Text(tree.branch).fontFamily('monospace').fontColor(Colors.secondary).fontSize(13)
                  .wordBreak(WordBreak.BREAK_ALL)
              }.layoutWeight(1).alignItems(HorizontalAlign.Start)
              this.more(tree)
            }.width('100%')
          }
          if (tree.parentKey) Button({type:ButtonType.Normal}) {
            Row({space:5}) {
              SymbolGlyph(Symbols.collapse).fontSize('12vp').fontColor([Colors.link])
              Text(tree.breadcrumb).fontColor(Colors.link).fontSize(13).maxLines(2)
            }
          }.constraintSize({minHeight:44}).padding(0).backgroundColor(Color.Transparent).accessibilityText(tr('project_open_parent') + ' · ' + tree.breadcrumb)
            .onClick(() => this.chooseParent(tree.key)).id('projects.parent')
          if (tree.cwd) Text(tree.cwd).fontFamily('monospace').fontColor(Colors.secondary).fontSize(13)
            .wordBreak(WordBreak.BREAK_ALL).copyOption(CopyOptions.LocalDevice)
            .id('projects.detail_owner.' + tree.machine + '.' + tree.source + '.' + tree.sourceId)
          if (!tree.online) Text(tr('offline')).fontSize(13).fontColor(Colors.secondary)
        }.alignItems(HorizontalAlign.Start).width('100%').padding(15).backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius)
    }
  }
  private openSpaceCard(treeKey:string,sessionKey:string):void {
    if (this.page !== 'project_tree' || this.target !== treeKey) return;
    const live = this.catalog.tree(treeKey)?.agents.find((a:ProjectAgent) => a.row.key() === sessionKey);
    if (live) this.chooseAgent(live);
  }
  @Builder spaceCard(agent:ProjectAgent,paneName:string,starter:boolean,treeKey:string) {
    Column({space:7}) {
      Row({space:6}) {
        Text([agent.tabTitle || agent.tab || agent.runtime,paneName].filter((s:string) => !!s).join(' · ') || '—')
          .fontSize(12).fontWeight(FontWeight.Medium).fontColor(agent.active ? this.tint(agent) : Colors.secondary)
          .padding({left:7,right:7,top:3,bottom:3}).backgroundColor(Colors.subtle).borderRadius(12)
          .maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis}).flexShrink(1)
        Blank().layoutWeight(1)
        Circle().width(8).height(8).fill(this.tint(agent))
      }.width('100%')
      Text(agent.row.title || tr('agent_untitled')).fontSize(16).fontWeight(FontWeight.Medium)
        .fontColor(Colors.ink).maxLines(nativeEnvironment.fontScale >= 1.5 ? 4 : 2).textOverflow({overflow:TextOverflow.Ellipsis})
      Text(tr(agent.stateLabel)+(starter ? ' · '+tr('session_space_starter') : ''))
        .fontSize(13).fontWeight(FontWeight.Medium).fontColor(agent.active ? this.tint(agent) : Colors.secondary)
      if (agent.preview) Text(agent.preview).fontSize(13).fontColor(Colors.secondary).maxLines(nativeEnvironment.fontScale >= 1.5 ? 5 : 3).textOverflow({overflow:TextOverflow.Ellipsis})
      if (agent.model) Text(agent.model).fontSize(12).fontColor(Colors.secondary).maxLines(nativeEnvironment.fontScale >= 1.5 ? 3 : 1).textOverflow({overflow:TextOverflow.Ellipsis})
    }.width('100%').constraintSize({minHeight:146}).padding(12).alignItems(HorizontalAlign.Start)
      .backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius)
      .border({width:agent.state === 'needs_you' ? 1 : 0,color:Colors.needs,radius:PhoneStyle.cardRadius})
      .accessibilityGroup(true).onClick(() => this.openSpaceCard(treeKey,agent.row.key())).id('projects.session')
  }
  @Builder sessions() {
    // Only the header and its menu are replaced when cached permissions become live.
    // The enclosing List and its session identities survive every catalog refresh.
    List() {
      ForEach(this.detailTree ? [this.detailTree!] : [], (tree: ProjectWorktree) => {
        this.detailHeader(tree)
      }, (tree: ProjectWorktree) => this.headerIdentity(tree))
      if (this.detailTree && this.detailTree!.children.length) {
        ListItem() { PhoneSectionTitle({ text: tr('project_children') }) }
        ListItemGroup() {
          ForEach(this.detailTree!.children, (child: ProjectWorktree) => {
            ListItem() { this.detailRow(child.title, child.branch || child.cwd, Symbols.folder, Colors.link, 'projects.worktree', () => this.chooseTree(child.key)) }
          }, (child: ProjectWorktree) => JSON.stringify([child.key,child.title,child.branch,child.cwd]))
        }.divider({strokeWidth:0.5,color:Colors.divider,startMargin:56})
          .backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius).clip(true)
      }
      ListItem() { PhoneSectionTitle({ text: tr('agents'), trailing: this.count(this.detailTree?.sessions.length || 0, 'project_sessions') }) }
      if (this.tabRows.size) ListItem() {
        Scroll() {
          Row({space:6}) {
            ForEach(Array.from(this.tabRows.keys()),(key:string) => {
              Button({type:ButtonType.Capsule}) {
                Row({space:5}) {
                  Circle().width(6).height(6).fill(this.tabTint(key))
                  Text(this.tabRows.get(key)?.tabTitle || '').fontSize(13).maxFontScale(Tokens.navigationTextMaxScale).fontColor(Colors.ink)
                  Text(String(this.tabRows.get(key)?.count || 0)).fontSize(12).maxFontScale(Tokens.navigationTextMaxScale).fontColor(Colors.secondary)
                  SymbolGlyph(this.tabOpen(key) ? Symbols.down : Symbols.expand).fontSize('11vp').fontColor([Colors.secondary])
                }
              }.padding({left:10,right:10}).constraintSize({minHeight:44}).backgroundColor(Colors.surface)
                .accessibilityText(this.tabDescription(key)).onClick(() => this.toggleTab(key)).id('projects.tab')
            },(key:string) => key)
          }.padding({top:4,bottom:6})
        }.width('100%').scrollable(ScrollDirection.Horizontal).scrollBar(BarState.Off)
      }
      ListItem() {
        GridRow({columns:nativeEnvironment.fontScale >= 1.5 ? 1 : 2,gutter:{x:10,y:10}}) {
          ForEach(this.detailSessions.filter((line:ProjectLine) => line.kind === 'session'),(line:ProjectLine) => {
            GridCol({span:1}) { this.spaceCard(line.agent!,line.paneName,line.starter,this.target) }
          },(line:ProjectLine) => JSON.stringify([line.key,line.open,line.agent?.row.raw,line.agent?.state,line.paneName,line.starter]))
          if (this.detailTree?.canStart) GridCol({span:1}) {
            Column({space:8}) {
              SymbolGlyph(Symbols.plus).fontSize('22vp').fontColor([Colors.link])
              Text(tr('new_agent')).fontSize(15).fontWeight(FontWeight.Medium).fontColor(Colors.link)
            }.width('100%').constraintSize({minHeight:146}).justifyContent(FlexAlign.Center)
              .backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius)
              .accessibilityGroup(true).onClick(() => this.startFromTree(this.target)).id('projects.start')
          }
        }.width('100%').id('projects.session_grid')
      }
      if (!this.detailTree?.sessions.length) ListItem() { this.message(tr('project_no_sessions')) }
    }.width('100%').layoutWeight(1).maintainVisibleContentPosition(true)
      .padding({ left: PhoneStyle.page, right: PhoneStyle.page, top: 5 }).id('projects.sessions')
  }
  build() {
    Column() {
      if (this.catalog.loading && !this.catalog.projects.length) LoadingProgress().width(29).height(29).margin(14)
      if (this.catalog.startError) this.message(tr('project_start_unavailable'))
      if (this.catalog.notice) Text(this.catalog.notice).fontSize(14).fontColor(Colors.link).width('100%').padding({ left: 18, right: 18, top: 4, bottom: 7 }).id('tree.result')
      if (this.catalog.partial) {
        Row({ space: 6 }) {
          Text(tr('project_catalog_partial')).fontSize(14).fontColor(Colors.secondary).layoutWeight(1)
          Button(tr('refresh'), { buttonStyle: ButtonStyleMode.TEXTUAL }).enabled(!this.catalog.loading).onClick(() => this.onRefresh()).id('projects.retry')
        }.padding({ left: 16, right: 16 }).width('100%')
      }
      Refresh({ refreshing: $$this.refreshing }) {
        Column() {
          if (this.page === 'projects' || this.page === 'project' && this.project()) this.repositoryList()
          else if (this.detailTree) {
            this.sessions()
          } else if (!this.catalog.loading) this.message(tr('project_gone'))
        }.width('100%').height('100%')
      }.width('100%').layoutWeight(1).onRefreshing(() => this.onRefresh())
    }.width('100%').height('100%').alignItems(HorizontalAlign.Start).backgroundColor(Colors.grouped)
      .bindSheet($$this.editorShown, this.editorSheet(), { height: SheetSize.LARGE, showClose: false, dragBar: false,
        onWillDismiss: (dismiss: DismissSheetAction) => { if (!this.editor.busy) dismiss.dismiss(); },
        onDisappear: () => { this.editor.close(); this.editedTree = undefined; } })
  }
}
