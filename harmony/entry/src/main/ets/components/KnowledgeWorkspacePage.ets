import { ClientModel } from '../models/ClientModel';
import { KnowledgeDirectory, KnowledgeSearchHit, KnowledgeWorkspace } from '../models/KnowledgeWorkspace';
import { ResourceRow, Wire } from '../models/Wire';
import { MarkdownText } from './MarkdownText';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import { common } from '@kit.AbilityKit';
import { KnowledgeRecording } from '../models/KnowledgeRecording';
import { NativeRecording } from '../platform/NativeRecording';
import { KnowledgeRecordingView } from './KnowledgeRecordingView';

const KB_KINDS: string[] = ['projects', 'chats', 'people', 'docs', 'meetings', 'events', 'recordings'];

@ComponentV2
export struct KnowledgeWorkspacePage {
  @Require @Param model: ClientModel;
  @Param row: ResourceRow | undefined = undefined;
  @Param list: ResourceRow[] = [];
  @Param rail: boolean = false;
  @Event onOpenResource: (row: ResourceRow) => void = (_row: ResourceRow) => {};
  @Event onChat: (row: ResourceRow) => void = (_row: ResourceRow) => {};
  @Event onLink: (url: string) => void = (_url: string) => {};
  @Local workspace: KnowledgeWorkspace = new KnowledgeWorkspace();
  @Local momentText: string = '';
  @Local selectedActions: string[] = [];
  @Local recordingOpen: boolean = false;
  @Local recording: KnowledgeRecording = new KnowledgeRecording(new NativeRecording());

  aboutToAppear(): void { this.workspace.open(this.model, this.row, this.list).catch(() => undefined); }
  aboutToDisappear(): void { this.workspace.hide(); this.recording.dispose().catch(() => undefined); }

  @Monitor('row') rowChanged(): void { this.workspace.open(this.model, this.row, this.list).catch(() => undefined); }
  @Monitor('model.profile') profileChanged(): void { this.recordingOpen = false; this.recording.stopForBackground().catch(() => undefined); this.workspace.open(this.model, this.row, this.list).catch(() => undefined); }
  @Monitor('model.connected') connectionChanged(): void { if (this.model.connected) { this.workspace.refresh().catch(() => undefined); if (this.recordingOpen) this.openRecording(); } }

  private async openRecording(): Promise<void> {
    const context = this.getUIContext().getHostContext();
    if (!context || !this.model.connected || this.model.demo) return;
    this.recordingOpen = true;
    await this.recording.open(this.model, this.row?.manager || this.model.profile.manager, context as common.UIAbilityContext);
  }

  private deleteRecording(): void {
    const id = this.recording.entry?.id, scope = this.model.scope(), pairing = this.model.pairingRevision();
    this.getUIContext().showAlertDialog({ title: tr('knowledge_recording_delete_local'), message: this.recording.entry?.startedAt || tr('confirm'),
      primaryButton: { value: tr('cancel'), action: (): void => {} },
      secondaryButton: { value: tr('delete'), action: (): void => {
        if (this.recordingOpen && this.model.scope() === scope && this.model.pairingRevision() === pairing && this.recording.entry?.id === id) this.recording.deleteLocal();
      } } });
  }

  @Builder recordingSheet() {
    Scroll() { KnowledgeRecordingView({ model: this.recording, onDeleteLocal: (): void => this.deleteRecording() }) }
      .width('100%').height('100%')
  }

  private kindLabel(kind: string): string { return tr('knowledge_kind_' + kind); }
  private rowSubtitle(row: ResourceRow): string {
    const meta = Wire.object(row.data(), 'meta'), tags = Wire.strings(row.data(), 'tags');
    const value = row.detail || Wire.text(row.data(), 'one_line') || '';
    const extra = row.path === 'projects' ? [Wire.text(meta, 'kind'), Wire.text(meta, 'status')].filter((part: string) => !!part).join(' · ') : '';
    return [extra, value, ...tags.map((tag: string) => '#' + tag)].filter((part: string) => !!part).join(' · ');
  }

  private tagOptions(): SelectOption[] {
    const options: SelectOption[] = [{ value: tr('knowledge_all_tags') }];
    this.workspace.tags.forEach((tag: string) => options.push({ value: '#' + tag }));
    return options;
  }

  private kindOptions(): SelectOption[] { return KB_KINDS.map((kind: string): SelectOption => ({ value: this.kindLabel(kind) })); }

  private selectKind(index: number): void { if (index >= 0 && index < KB_KINDS.length) this.workspace.setKind(KB_KINDS[index]); }
  private selectTag(index: number): void { this.workspace.setTag(index === 0 ? '' : this.workspace.tags[index - 1] || ''); }

  @Builder listContent() {
    Column({ space: Tokens.gap }) {
      Search({ value: this.workspace.query, placeholder: tr('knowledge_search') })
        .onChange((value: string) => this.workspace.setQuery(value)).id('knowledge.search')
      Row({ space: Tokens.gap }) {
        Select(this.kindOptions()).value(this.kindLabel(this.workspace.kind)).layoutWeight(1)
          .onSelect((index: number) => this.selectKind(index)).id('knowledge.kind')
        if (!this.workspace.query.trim()) {
          Select(this.tagOptions()).value(this.workspace.tag ? '#' + this.workspace.tag : tr('knowledge_all_tags')).layoutWeight(1)
            .onSelect((index: number) => this.selectTag(index)).id('knowledge.tag')
        }
        Button(tr('refresh')).height(Tokens.touch).onClick(() => this.workspace.refresh()).id('knowledge.refresh')
        if (this.workspace.kind === 'recordings') Button(tr('knowledge_recording_open')).height(Tokens.touch)
          .enabled(this.model.connected && !this.model.demo).onClick(() => this.openRecording()).id('knowledge.recording.open')
      }.width('100%')
      if (this.workspace.error) Text(tr(this.workspace.error)).fontSize(Tokens.meta).fontColor($r('app.color.persona_danger')).width('100%')
      if (this.workspace.loading && !this.workspace.entries.length && !this.workspace.results.length) LoadingProgress().width(Tokens.touch).height(Tokens.touch)
      if (this.workspace.query.trim()) {
        if (!this.workspace.results.length && !this.workspace.loading && !this.workspace.error) Text(tr('knowledge_no_results')).fontSize(Tokens.body).width('100%')
        List() {
          ForEach(this.workspace.results, (hit: KnowledgeSearchHit) => {
            ListItem() {
              Column({ space: Tokens.fine }) {
                Text(hit.entry.title || hit.entry.id).fontSize(Tokens.body).fontWeight(FontWeight.Medium).maxLines(2).width('100%')
                Text(this.kindLabel(hit.entry.path) + ' · ' + String(hit.count)).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).width('100%')
                ForEach(hit.hits.slice(0, 3), (line: string, index: number) => Text(line).fontSize(Tokens.meta).maxLines(3).width('100%'),
                  (line: string, index: number) => String(index) + line.slice(0, 40))
              }.alignItems(HorizontalAlign.Start).padding(Tokens.inset).width('100%')
            }.onClick(() => this.workspace.openEntry(hit.entry).then((entry: ResourceRow) => this.onOpenResource(entry)).catch(() => undefined))
              .id('knowledge.hit.' + hit.entry.path + '.' + hit.entry.id)
          }, (hit: KnowledgeSearchHit) => hit.entry.key())
        }.width('100%').layoutWeight(1)
      } else {
        if (!this.workspace.entries.length && !this.workspace.loading && !this.workspace.error) Text(tr('knowledge_empty')).fontSize(Tokens.body).width('100%')
        List() {
          ForEach(this.workspace.entries, (entry: ResourceRow) => {
            ListItem() {
              Column({ space: Tokens.fine }) {
                Text(entry.title || entry.id).fontSize(Tokens.body).fontWeight(FontWeight.Medium).maxLines(2).width('100%')
                if (this.rowSubtitle(entry)) Text(this.rowSubtitle(entry)).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).maxLines(3).width('100%')
              }.alignItems(HorizontalAlign.Start).padding(Tokens.inset).width('100%')
            }.onClick(() => this.workspace.openEntry(entry).then((next: ResourceRow) => this.onOpenResource(next)).catch(() => undefined))
              .id('knowledge.entry.' + entry.path + '.' + entry.id)
          }, (entry: ResourceRow) => entry.key())
        }.width('100%').layoutWeight(1)
      }
    }.padding(Tokens.inset).width('100%').height('100%').alignItems(HorizontalAlign.Start)
      .bindSheet($$this.recordingOpen, this.recordingSheet(), { title: { title: tr('knowledge_recording_open') },
        height: SheetSize.LARGE, showClose: true, onDisappear: (): void => { this.recording.stopForBackground().catch(() => undefined); } })
  }

  private openSafeLink(url: string): void { if (/^https?:\/\//i.test(url.trim())) this.onLink(url.trim()); }

  private askPersona(): void {
    this.workspace.askPersona().then((row: ResourceRow | undefined) => { if (row) this.onChat(row); }).catch(() => undefined);
  }

  private confirmDeleteRecording(): void {
    if (!this.workspace.canDeleteRecording()) return;
    const generation = this.model.connectionGeneration(), scope = this.model.scope(), target = this.workspace.selected.key();
    this.getUIContext().showAlertDialog({ title: tr('knowledge_forget_recording'), message: tr('knowledge_forget_recording_note'),
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('delete'), action: () => {
        if (!this.model.owns(generation, scope) || this.workspace.selected.key() !== target) return;
        this.workspace.deleteRecording().catch(() => undefined);
      } } });
  }

  private confirmDispatch(): void {
    const machine = this.workspace.machineForDirectory(this.workspace.targetMachines);
    if (!machine || !this.workspace.agentDraft.trim() || !this.workspace.agentLauncher.trim()) return;
    const generation = this.model.connectionGeneration(), scope = this.model.scope(), target = this.workspace.selected.key();
    this.getUIContext().showAlertDialog({ title: tr('knowledge_start_agent'), message: machine.title + ' · ' + this.workspace.directoryTargets[this.workspace.selectedDirectory]?.path,
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('knowledge_start_agent'), action: () => {
        if (!this.model.owns(generation, scope) || this.workspace.selected.key() !== target) return;
        this.workspace.dispatchAgent(machine, this.workspace.agentDraft, this.workspace.agentLauncher)
          .then((row: ResourceRow | undefined) => { if (row) this.onChat(row); }).catch(() => undefined);
      } } });
  }

  private toggleAction(id: string): void {
    this.selectedActions = this.selectedActions.includes(id) ? this.selectedActions.filter((value: string) => value !== id) : this.selectedActions.concat([id]);
  }

  private confirmAddTodos(): void {
    if (!this.selectedActions.length) return;
    const generation = this.model.connectionGeneration(), scope = this.model.scope(), target = this.workspace.selected.key();
    this.getUIContext().showAlertDialog({ title: tr('knowledge_minutes_add_todos'), message: String(this.selectedActions.length),
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('knowledge_minutes_add_todos'), action: () => {
        if (!this.model.owns(generation, scope) || this.workspace.selected.key() !== target) return;
        this.workspace.addMinutesTodos(this.selectedActions.slice()).then((saved: boolean) => { if (saved) this.selectedActions = []; }).catch(() => undefined);
      } } });
  }

  @Builder editForm() {
    Column({ space: Tokens.gap }) {
      Text(tr('knowledge_edit_metadata')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
      Select(['P0', 'P1', 'P2', 'P3'].map((value: string): SelectOption => ({ value: value }))).value(this.workspace.editImportance)
        .onSelect((index: number) => this.workspace.setEditImportance(['P0', 'P1', 'P2', 'P3'][index] || 'P2')).id('knowledge.edit.importance')
      TextInput({ text: this.workspace.editRelevance, placeholder: tr('knowledge_relevance') }).type(InputType.Number)
        .onChange((value: string) => this.workspace.setEditRelevance(value)).id('knowledge.edit.relevance')
      TextInput({ text: this.workspace.editTags, placeholder: tr('knowledge_tags') }).onChange((value: string) => this.workspace.setEditTags(value)).id('knowledge.edit.tags')
      if (this.workspace.selected.path === 'projects') {
        Select([{ value: tr('knowledge_project_short') }, { value: tr('knowledge_project_long') }])
          .value(this.workspace.editProjectKind === 'long' ? tr('knowledge_project_long') : tr('knowledge_project_short') )
          .onSelect((index: number) => this.workspace.setEditProjectKind(index === 1 ? 'long' : 'short')).id('knowledge.edit.project_kind')
        Select([{ value: tr('knowledge_status_active') }, { value: tr('knowledge_status_paused') },
          { value: tr('knowledge_status_done') }, { value: tr('knowledge_status_ignored') }])
          .value(tr('knowledge_status_' + this.workspace.editStatus))
          .onSelect((index: number) => this.workspace.setEditStatus(['active', 'paused', 'done', 'ignored'][index] || 'active')).id('knowledge.edit.status')
      }
      Row({ space: Tokens.gap }) {
        Button(tr('cancel')).layoutWeight(1).height(Tokens.touch).onClick(() => this.workspace.editOpen = false)
        Button(tr(this.workspace.writeState === 'pending' || this.workspace.writeState === 'unknown' ? this.workspace.writeState : 'save'))
          .layoutWeight(1).height(Tokens.touch).enabled(this.workspace.canSaveEdit())
          .onClick(() => this.workspace.saveEdit()).id('knowledge.edit.save')
      }.width('100%')
    }.width('100%').alignItems(HorizontalAlign.Start).padding(Tokens.inset)
  }

  @Builder detailContent() {
    Scroll() {
      Column({ space: Tokens.section }) {
        Column({ space: Tokens.gap }) {
          if (Wire.text(Wire.object(this.workspace.detail, 'meta'), 'importance')) Text(Wire.text(Wire.object(this.workspace.detail, 'meta'), 'importance') + ' · ' + tr('knowledge_relevance') + ' ' + String(Wire.number(Wire.object(this.workspace.detail, 'meta'), 'relevance')))
            .fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).width('100%')
          if (this.workspace.selected.detail) Text(this.workspace.selected.detail).fontSize(Tokens.body).width('100%')
          Row({ space: Tokens.gap }) {
            Button(tr('knowledge_ask_persona')).layoutWeight(1).height(Tokens.touch).onClick(() => this.askPersona()).id('knowledge.ask')
            Button(tr('knowledge_edit_metadata')).layoutWeight(1).height(Tokens.touch)
              .onClick(() => this.workspace.beginEdit()).id('knowledge.edit.open')
          }.width('100%')
          if (this.workspace.selected.path === 'projects') {
            TextArea({ text: this.workspace.agentDraft, placeholder: tr('knowledge_agent_prompt') }).height(Tokens.inputExpanded)
              .onChange((value: string) => this.workspace.setAgentDraft(value)).id('knowledge.agent.prompt')
            if (this.workspace.directoryTargets.length) {
              Select(this.directoryOptions()).value(this.workspace.directoryTargets[this.workspace.selectedDirectory]?.label || '').onSelect((index: number) => this.workspace.selectedDirectory = index)
                .id('knowledge.agent.directory')
              TextInput({ text: this.workspace.agentLauncher, placeholder: tr('launcher') }).onChange((value: string) => this.workspace.setAgentLauncher(value))
                .id('knowledge.agent.launcher')
              Button(tr('knowledge_start_agent')).enabled(!!this.workspace.agentDraft.trim() && !!this.workspace.agentLauncher.trim() && !this.workspace.busy)
                .height(Tokens.touch).onClick(() => this.confirmDispatch()).id('knowledge.agent.start')
            } else Text(tr('knowledge_agent_unavailable')).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary'))
          }
          if (this.workspace.error) Text(tr(this.workspace.error)).fontSize(Tokens.meta).fontColor($r('app.color.persona_danger')).width('100%')
        }.alignItems(HorizontalAlign.Start).width('100%')
        if (this.workspace.editOpen) this.editForm()
        if (this.workspace.agents.length) {
          Column({ space: Tokens.gap }) {
            Text(tr('knowledge_project_agents')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
            ForEach(this.workspace.agents, (agent: ResourceRow) => Button(agent.title || agent.id).width('100%').height(Tokens.touch)
              .onClick(() => this.onChat(agent)).id('knowledge.agent.' + agent.id), (agent: ResourceRow) => agent.key())
          }.width('100%').alignItems(HorizontalAlign.Start)
        }
        if (this.workspace.matches.length) {
          Column({ space: Tokens.gap }) {
            Text(tr('knowledge_search_matches')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
            ForEach(this.workspace.matches, (match: Record<string, Object>, index: number) => Text(Wire.text(match, 'line'))
              .fontSize(Tokens.meta).width('100%').id('knowledge.match.' + String(index)), (match: Record<string, Object>, index: number) => String(index))
          }.width('100%').alignItems(HorizontalAlign.Start)
        }
        if (Wire.text(this.workspace.detail, 'summary')) {
          Column({ space: Tokens.gap }) { Text(tr(this.workspace.selected.path === 'recordings' ? 'knowledge_recording_summary' : 'knowledge_profile')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
            MarkdownText({ text: Wire.text(this.workspace.detail, 'summary'), onLink: (url: string) => this.openSafeLink(url) })
          }.width('100%').alignItems(HorizontalAlign.Start)
        }
        if (Wire.strings(this.workspace.detail, 'log').length) {
          Column({ space: Tokens.gap }) { Text(tr('knowledge_timeline')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
            ForEach(Wire.strings(this.workspace.detail, 'log').slice().reverse(), (line: string, index: number) => Text(line).fontSize(Tokens.meta).width('100%')
              .id('knowledge.timeline.' + String(index)), (line: string, index: number) => String(index) + line.slice(0, 24))
          }.width('100%').alignItems(HorizontalAlign.Start)
        }
        if (this.workspace.selected.path === 'recordings') this.recordingSection(this.workspace.detail)
        if (this.workspace.selected.path !== 'recordings' && Wire.text(this.workspace.detail, 'recent')) {
          Column({ space: Tokens.gap }) { Text(tr('knowledge_recent')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
            Text(Wire.text(this.workspace.detail, 'recent')).fontSize(Tokens.meta).width('100%')
          }.width('100%').alignItems(HorizontalAlign.Start)
        }
        if (this.workspace.selected.path === 'projects' && Wire.rows(this.workspace.detail, 'moments').length) {
          Column({ space: Tokens.gap }) {
            Text(tr('knowledge_related_moments')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
            ForEach(Wire.rows(this.workspace.detail, 'moments'), (moment: Record<string, Object>) => Button(Wire.text(moment, 'author') + ' · ' + Wire.text(moment, 'text'))
              .width('100%').height(Tokens.touch).onClick(() => this.loadMoment(Wire.text(moment, 'id'))).id('knowledge.moment.' + Wire.text(moment, 'id')),
              (moment: Record<string, Object>) => Wire.text(moment, 'id'))
            if (this.momentText) MarkdownText({ text: this.momentText, onLink: (url: string) => this.openSafeLink(url) })
          }.width('100%').alignItems(HorizontalAlign.Start)
        }
        this.relatedEntries(this.workspace.detail)
        if (Wire.text(Wire.object(this.workspace.detail, 'meta'), 'url')) Button(tr('knowledge_open_link')).height(Tokens.touch).onClick(() => this.openSafeLink(Wire.text(Wire.object(this.workspace.detail, 'meta'), 'url')))
        if (Wire.text(this.workspace.detail, 'path')) Text(Wire.text(this.workspace.detail, 'path')).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).width('100%')
        if (this.workspace.selected.path === 'recordings') Button(tr('knowledge_forget_recording')).enabled(this.workspace.canDeleteRecording())
          .height(Tokens.touch).onClick(() => this.confirmDeleteRecording()).id('knowledge.recording.delete')
      }.padding(Tokens.page).width('100%').alignItems(HorizontalAlign.Start)
    }.scrollable(ScrollDirection.Vertical).scrollBar(BarState.Auto).width('100%').height('100%')
  }

  @Builder relatedEntries(detail: Record<string, Object>) {
    if (Wire.rows(detail, 'links').length) {
      Column({ space: Tokens.gap }) {
        Text(tr('knowledge_related')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
        ForEach(Wire.rows(detail, 'links'), (item: Record<string, Object>) => {
          Button(Wire.text(item, 'title', Wire.text(item, 'id'))).width('100%').height(Tokens.touch).onClick(() => this.openRelated(item)).id('knowledge.related.' + Wire.text(item, 'kind') + '.' + Wire.text(item, 'id'))
        }, (item: Record<string, Object>) => Wire.text(item, 'kind') + '/' + Wire.text(item, 'id'))
      }.width('100%').alignItems(HorizontalAlign.Start)
    }
  }

  @Builder recordingSection(detail: Record<string, Object>) {
    Column({ space: Tokens.gap }) {
      if (Object.keys(Wire.object(detail, 'minutes')).length) {
        Text(tr('knowledge_minutes')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
        if (Wire.text(Wire.object(detail, 'minutes'), 'markdown')) MarkdownText({ text: Wire.text(Wire.object(detail, 'minutes'), 'markdown'), onLink: (url: string) => this.openSafeLink(url) })
        ForEach(Wire.rows(Wire.object(detail, 'minutes'), 'actions'), (action: Record<string, Object>) => {
          Row({ space: Tokens.gap }) { Toggle({ type: ToggleType.Checkbox, isOn: this.selectedActions.includes(Wire.text(action, 'id')) }).onChange(() => this.toggleAction(Wire.text(action, 'id')))
            Text(Wire.text(action, 'what')).fontSize(Tokens.meta).layoutWeight(1)
          }.width('100%').id('knowledge.minutes.action.' + Wire.text(action, 'id'))
        }, (action: Record<string, Object>) => Wire.text(action, 'id'))
        if (this.selectedActions.length) Button(tr('knowledge_minutes_add_todos')).height(Tokens.touch).onClick(() => this.confirmAddTodos())
      }
      Button(tr('knowledge_minutes_make')).enabled(!this.workspace.busy && this.workspace.writeState !== 'pending' && this.workspace.writeState !== 'unknown')
        .height(Tokens.touch).onClick(() => this.workspace.makeMinutes()).id('knowledge.minutes.make')
      if (Wire.rows(detail, 'segments').length) {
        Text(tr('knowledge_transcript')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
        ForEach(Wire.rows(detail, 'segments'), (line: Record<string, Object>, index: number) => Text(Wire.text(line, 'text'))
          .fontSize(Tokens.meta).width('100%').id('knowledge.transcript.' + String(index)),
          (line: Record<string, Object>, index: number) => String(index))
      } else if (Wire.text(detail, 'recent')) Text(Wire.text(detail, 'recent')).fontSize(Tokens.meta).width('100%')
    }.width('100%').alignItems(HorizontalAlign.Start)
  }

  private directoryOptions(): SelectOption[] { return this.workspace.directoryTargets.map((target: KnowledgeDirectory): SelectOption => ({ value: target.label })); }
  private openRelated(item: Record<string, Object>): void { const target = ResourceRow.from(item, 'knowledge'); target.path = Wire.text(item, 'kind'); target.manager = this.workspace.selected.manager; this.onOpenResource(target); }
  private async loadMoment(id: string): Promise<void> { this.momentText = await this.workspace.openMoment(id).catch(() => ''); }

  build() {
    if (this.row && this.workspace.selected.id) this.detailContent();
    else this.listContent();
  }
}
