import { IconActions } from '../design/IconActions';
import { Symbols } from '../design/Symbols';
import { PhoneSectionTitle } from '../design/PhoneStyle';
import type { KnowledgeRecordingEntry } from '../models/KnowledgeRecording';
import { Colors } from '../design/Colors';
import { ButtonStyles, SelectStyles } from '../design/Controls';
import { ClientModel } from '../models/ClientModel';
import { KnowledgeDirectory, KnowledgeSearchHit, KnowledgeWorkspace, RecordingFactsDraft } from '../models/KnowledgeWorkspace';
import { ResourceRow, Wire } from '../models/Wire';
import { BoundRow, reconcileBoundRows } from '../models/RowBinding';
import { MarkdownText } from './MarkdownText';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import { common } from '@kit.AbilityKit';
import { KnowledgeRecording } from '../models/KnowledgeRecording';
import { NativeRecording } from '../platform/NativeRecording';
import { KnowledgeRecordingView } from './KnowledgeRecordingView';
import { LengthMetrics, UIUtils } from '@kit.ArkUI';
import { nativeEnvironment } from '../platform/NativeEnvironment';
import type { Binding } from '@kit.ArkUI';

const KB_KINDS: string[] = ['projects', 'topics', 'chats', 'people', 'docs', 'meetings', 'events'];

@ComponentV2
export struct KnowledgeWorkspacePage {
  @Local dispatchExpanded: boolean = false;
  @Local facts: RecordingFactsDraft = new RecordingFactsDraft();
  @Local recordingTools: boolean = false;
  private kindScroller: Scroller = new Scroller();
  private kindListMounted: boolean = false;
  @Require @Param model: ClientModel;
  @Param row: ResourceRow | undefined = undefined;
  @Param list: ResourceRow[] = [];
  @Param visible: boolean = true;
  @Param rail: boolean = false;
  @Param initialKind: string = '';
  @Local localMeetings: boolean = false;
  @Event onAllMeetings: () => void = () => { this.localMeetings = true; this.workspace.setKind('meetings'); };
  private localOwner: string = '';
  private localOpening: Promise<void> | undefined;
  @Event onOpenResource: (row: ResourceRow) => void = (_row: ResourceRow) => {};
  @Event onChat: (row: ResourceRow) => void = (_row: ResourceRow) => {};
  @Event onLink: (url: string) => void = (_url: string) => {};
  @Local workspace: KnowledgeWorkspace = new KnowledgeWorkspace();
  @Local entryRowBindings: BoundRow<ResourceRow>[] = [];
  @Local resultRowBindings: BoundRow<KnowledgeSearchHit>[] = [];
  @Local agentRowBindings: BoundRow<ResourceRow>[] = [];
  private rowBindingOwner: Object | undefined;
  private rowBindingScope: string = '';
  @Local momentText: string = '';
  @Local selectedActions: string[] = [];
  @Local recordingOpen: boolean = false;
  @Local recording: KnowledgeRecording = new KnowledgeRecording(new NativeRecording());
  private lastVisible: boolean = true;
  private workspaceOpening: boolean = false;
  private queuedRefresh: boolean = false;
  private openSerial: number = 0;
  private pageAvailable: boolean = false;

  aboutToAppear(): void {
    this.pageAvailable = true; this.lastVisible = this.visible; this.syncRowBindings(); this.openWorkspace();
    if (this.initialKind === 'recordings' && !this.row) this.loadRecordings().catch(() => undefined);
  }
  aboutToDisappear(): void {
    this.pageAvailable = false; this.localOwner = ''; this.localOpening = undefined; this.openSerial++; this.workspaceOpening = false; this.queuedRefresh = false;
    this.workspace.hide(); this.recording.dispose().catch(() => undefined);
  }

  @Monitor('row') rowChanged(): void { this.facts.close(); this.recordingTools=false; this.syncRowBindings(); this.openWorkspace(); }
  @Monitor('workspace.kind') centerKind(): void {
    const index=KB_KINDS.indexOf(this.workspace.kind);
    if (index>=0 && this.kindListMounted) this.kindScroller.scrollToIndex(index,false,ScrollAlign.CENTER);
  }
  @Monitor('model.profile') profileChanged(): void {
    this.facts.close(); this.recordingTools=false; this.recordingOpen = false; this.localMeetings = false; this.localOwner = ''; this.localOpening = undefined;
    this.recording.stopForBackground().catch(() => undefined); this.syncRowBindings(); this.openWorkspace();
    if (this.initialKind === 'recordings' && !this.row) this.loadRecordings().catch(() => undefined);
  }
  @Monitor('model.connected') connectionChanged(): void { if (this.model.connected) { this.refreshWhenReady(); if (this.recordingOpen || this.initialKind === 'recordings') this.loadRecordings().catch(() => undefined); } }
  @Monitor('visible') visibilityChanged(): void {
    const becameVisible = !this.lastVisible && this.visible;
    this.lastVisible = this.visible;
    if (becameVisible && this.pageAvailable && !this.row) this.refreshWhenReady();
  }
  @Monitor('workspace.entries', 'workspace.results', 'workspace.agents') rowSourcesChanged(): void { this.syncRowBindings(); }

  private openWorkspace(): void {
    const serial = ++this.openSerial;
    this.workspaceOpening = true;
    this.workspace.open(this.model, this.row, this.list, this.initialKind).catch(() => undefined).finally(() => {
      if (serial !== this.openSerial) return;
      this.workspaceOpening = false;
      const shouldRefresh = this.queuedRefresh; this.queuedRefresh = false;
      if (shouldRefresh && this.pageAvailable) this.workspace.refresh().catch(() => undefined);
    });
  }

  private refreshWhenReady(): void {
    if (!this.pageAvailable) return;
    if (this.workspaceOpening) { this.queuedRefresh = true; return; }
    this.workspace.refresh().catch(() => undefined);
  }

  private syncRowBindings(): void {
    const scope = JSON.stringify([this.model.scope(), this.model.profile.id, this.model.profile.manager, this.row?.key() || 'list']);
    this.entryRowBindings = reconcileBoundRows(this.entryRowBindings, this.workspace.entries, (row: ResourceRow) => row.key(),
      this.rowBindingOwner, this.model, this.rowBindingScope, scope);
    this.resultRowBindings = reconcileBoundRows(this.resultRowBindings, this.workspace.results, (hit: KnowledgeSearchHit) => hit.entry.key(),
      this.rowBindingOwner, this.model, this.rowBindingScope, scope);
    this.agentRowBindings = reconcileBoundRows(this.agentRowBindings, this.workspace.agents, (row: ResourceRow) => row.key(),
      this.rowBindingOwner, this.model, this.rowBindingScope, scope);
    this.rowBindingOwner = this.model; this.rowBindingScope = scope;
  }

  private recordingScope(): string {
    return JSON.stringify([this.model.scope(),this.model.pairingRevision(),this.row?.manager || this.model.profile.manager]);
  }
  private async loadRecordings(): Promise<boolean> {
    const context = this.getUIContext().getHostContext(), key = this.recordingScope();
    if (!this.pageAvailable || !context || this.model.demo || !this.model.profile.id) return false;
    if (key !== this.localOwner) {
      this.localOwner = key;
      this.localOpening = this.recording.open(this.model,this.row?.manager || this.model.profile.manager,context as common.UIAbilityContext);
    }
    const pending = this.localOpening;
    if (pending) {
      try { await pending; } finally { if (this.localOpening === pending) this.localOpening = undefined; }
    }
    return this.pageAvailable && this.visible && key === this.localOwner && this.recordingScope() === key;
  }
  private async openRecording(id: string = '', start: boolean = false): Promise<void> {
    if (!await this.loadRecordings()) return;
    if (id) this.recording.selectEntry(id);
    this.recordingOpen = true;
    if (start && this.recording.canStart()) await this.recording.start();
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
      .width('100%').height('100%').align(Alignment.TopStart).backgroundColor(Colors.grouped)
  }

  private kindLabel(kind: string): string { return tr('knowledge_kind_' + kind); }
  private kindIcon(kind: string): Resource {
    if (kind === 'projects') return Symbols.folder;
    if (kind === 'chats') return Symbols.persona;
    if (kind === 'people') return Symbols.agents;
    if (kind === 'meetings' || kind === 'events') return Symbols.calendar;
    if (kind === 'recordings') return $r('sys.symbol.mic');
    return kind === 'topics' ? Symbols.apps : Symbols.document;
  }
  private clock(seconds: number): string {
    const whole = Number.isFinite(seconds) ? Math.max(0,Math.floor(seconds)) : 0;
    return String(Math.floor(whole / 60)) + ':' + String(whole % 60).padStart(2,'0');
  }
  private localDate(raw:string): string {
    const date = new Date(raw);
    return Number.isFinite(date.getTime()) ? date.toLocaleDateString() + ' ' + String(date.getHours()).padStart(2,'0') + ':' + String(date.getMinutes()).padStart(2,'0') : '';
  }
  private rowRank(row:ResourceRow):string {
    if (row.path!=='meetings' && row.path!=='recordings') return '';
    const data=row.data(), meta=Wire.object(data,'meta');
    const priority=Wire.text(data,'importance') || Wire.text(meta,'importance');
    const relevance=Wire.number(data,'relevance',Wire.number(meta,'relevance'));
    return [priority,relevance>0 ? tr('knowledge_relevance')+' '+String(relevance) : ''].filter((value:string) => !!value).join(' · ');
  }
  private rowSubtitle(row: ResourceRow): string {
    const data = row.data(), meta = Wire.object(data,'meta');
    const value = row.detail || Wire.text(data,'one_line') || '';
    if (row.path === 'recordings' || row.path === 'meetings') {
      const duration = Wire.number(data,'duration',Wire.number(meta,'duration'));
      return [row.path === 'recordings' ? tr('knowledge_kind_recordings') : Wire.text(data,'source'),
        duration > 0 ? this.clock(duration) : '', this.localDate(Wire.text(data,'updated') || Wire.text(data,'started_at')),value].filter((v:string) => !!v).join(' · ');
    }
    const extra = row.path === 'projects' ? [Wire.text(meta,'kind'), Wire.text(meta,'status')].filter((part:string) => !!part).join(' · ') : '';
    return [extra,value,...Wire.strings(data,'tags').map((tag:string) => '#' + tag)].filter((part:string) => !!part).join(' · ');
  }
  private visibleLocal(): KnowledgeRecordingEntry[] {
    const q = this.workspace.query.trim().toLowerCase();
    return this.recording.entries.filter((v:KnowledgeRecordingEntry) =>
      v.uploadState !== 'stored' && (!q || [v.title,v.startedAt,v.transcript].join(' ').toLowerCase().includes(q)));
  }

  private tagOptions(): SelectOption[] {
    const options: SelectOption[] = [{ value: tr('knowledge_all_tags') }];
    this.workspace.tags.forEach((tag: string) => options.push({ value: '#' + tag }));
    return options;
  }


  private selectTag(index: number): void { this.workspace.setTag(index === 0 ? '' : this.workspace.tags[index - 1] || ''); }

  @Builder knowledgeFilters() {
    Menu() {
      if (!this.workspace.query.trim()) MenuItemGroup({ header: tr('knowledge_filter_tag') }) {
        ForEach(this.tagOptions(), (option: SelectOption, index: number) => {
          MenuItem({ content: option.value }).selected((this.workspace.tag ? '#' + this.workspace.tag : tr('knowledge_all_tags')) === option.value).selectIcon(true)
            .onClick(() => this.selectTag(index))
        }, (option: SelectOption) => JSON.stringify(option.value))
      }
    }.width(Tokens.popupWidth)
  }

  @Builder searchField(meetings:boolean) {
    Search({value:this.workspace.query,placeholder:tr(meetings ? 'phone_record_search' : 'knowledge_search')})
      .height(Math.max(38,Math.ceil(24*nativeEnvironment.fontScale)+8)).textFont({size:16}).backgroundColor(Colors.control)
      .onChange((value:string) => this.workspace.setQuery(value)).id('knowledge.search')
  }
  @Builder libraryRow(row: ResourceRow) {
    Row({space:10}) {
      Stack() { SymbolGlyph(this.kindIcon(row.path)).fontSize('18vp').fontColor([row.path === 'recordings' ? Colors.danger : Colors.link]) }
        .width(32).height(32).borderRadius(8).backgroundColor(Colors.control)
      Column({space:3}) {
        Text(row.title || row.id).fontSize(17).fontWeight(FontWeight.Medium).fontColor(Colors.ink).maxLines(2)
        if (this.rowRank(row)) Text(this.rowRank(row)).fontSize(12).fontColor(Colors.link)
        if (this.rowSubtitle(row)) Text(this.rowSubtitle(row)).fontSize(13).fontColor(Colors.secondary).maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis})
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
      SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
    }.width('100%').padding({left:13,right:12,top:9,bottom:9}).constraintSize({minHeight:49})
      .backgroundColor(Colors.surface).accessibilityGroup(true)
      .onClick(() => this.workspace.openEntry(row).then((next:ResourceRow) => this.onOpenResource(next)).catch(() => undefined))
      .id('knowledge.entry.' + row.path + '.' + row.id)
  }
  @Builder searchHit(hit:KnowledgeSearchHit) {
    Column({space:4}) {
      Text(hit.entry.title || hit.entry.id).fontSize(17).fontWeight(FontWeight.Medium).fontColor(Colors.ink).maxLines(2)
      Text(this.kindLabel(hit.entry.path) + ' · ' + String(hit.count)).fontSize(12).fontColor(Colors.secondary)
      ForEach(hit.hits.slice(0,2), (line:string,index:number) => Text(line).fontSize(14).fontColor(Colors.secondary)
        .maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis}), (line:string,index:number) => JSON.stringify([index, line]))
    }.width('100%').padding(13).alignItems(HorizontalAlign.Start).backgroundColor(Colors.surface).borderRadius(12)
      .onClick(() => this.workspace.openEntry(hit.entry).then((next:ResourceRow) => this.onOpenResource(next)).catch(() => undefined))
      .id('knowledge.hit.' + hit.entry.path + '.' + hit.entry.id)
  }
  @Builder sectionEntries() {
    if (this.workspace.query.trim()) {
      ForEach(this.resultRowBindings, (binding:BoundRow<KnowledgeSearchHit>) => {
        ListItem() { this.searchHit(binding.value) }
      }, (binding:BoundRow<KnowledgeSearchHit>) => binding.key)
    } else {
      ForEach(this.entryRowBindings, (binding:BoundRow<ResourceRow>) => {
        ListItem() { this.libraryRow(binding.value) }
      }, (binding:BoundRow<ResourceRow>) => binding.key)
    }
  }
  @Builder listStatus() {
    if (this.workspace.error) ListItem() {
      Row({space:8}) {
        Text(tr(this.workspace.error)).fontSize(14).fontColor(Colors.secondary).layoutWeight(1)
        Button(tr('retry'),{buttonStyle:ButtonStyleMode.TEXTUAL}).fontColor(Colors.link).onClick(() => this.workspace.refresh()).id('knowledge.retry')
      }.width('100%').padding(10)
    }
    if (this.workspace.loading && !this.workspace.entries.length && !this.workspace.results.length)
      ListItem() { LoadingProgress().width(26).height(26).margin(10) }
    if (!this.workspace.loading && !this.workspace.error && (this.initialKind !== 'recordings' || !this.visibleLocal().length) && (this.workspace.query.trim() ? !this.workspace.results.length : !this.workspace.entries.length))
      ListItem() { Text(tr(this.workspace.query.trim() ? 'knowledge_no_results' : this.workspace.kind === 'recordings' ? 'phone_read_only_recording' : 'knowledge_empty')).fontSize(15).fontColor(Colors.secondary).padding(10) }
  }
  @Builder kindChip(kind:string) {
    Button({type:ButtonType.Capsule}) {
      Row({space:4}) {
        if (nativeEnvironment.fontScale>1.3) SymbolGlyph(this.kindIcon(kind)).fontSize('14vp')
          .fontColor([kind===this.workspace.kind ? Colors.onAction : Colors.ink])
        Text(this.kindLabel(kind)).fontSize(15).fontWeight(FontWeight.Medium)
          .fontColor(kind===this.workspace.kind ? Colors.onAction : Colors.ink)
      }
    }.constraintSize({minHeight:34}).padding({left:10,right:10,top:4,bottom:4})
      .backgroundColor(kind===this.workspace.kind ? Colors.action : Colors.control)
      .accessibilitySelected(kind===this.workspace.kind).onClick(() => this.workspace.setKind(kind)).id('knowledge.kind.'+kind)
  }
  @Builder kindBar() {
    if (nativeEnvironment.fontScale>1.3) {
      Flex({wrap:FlexWrap.Wrap,space:{main:LengthMetrics.vp(6),cross:LengthMetrics.vp(5)}}) {
        ForEach(KB_KINDS,(kind:string) => { this.kindChip(kind) },(kind:string) => kind)
      }.width('100%').padding({top:6,bottom:6}).id('knowledge.kinds')
    } else {
      List({space:6,scroller:this.kindScroller}) {
        ForEach(KB_KINDS,(kind:string) => { ListItem() { this.kindChip(kind) } },(kind:string) => kind)
      }.width('100%').height(48).listDirection(Axis.Horizontal).scrollBar(BarState.Off).alignListItem(ListItemAlign.Center)
        .onAppear(() => { this.kindListMounted=true; })
        .onAreaChange(() => this.centerKind()).onDisAppear(() => { this.kindListMounted=false; }).id('knowledge.kinds')
    }
  }
  @Builder rootList() {
    Column({space:6}) {
      this.searchField(false)
      if (!this.workspace.query.trim()) {
        this.kindBar()
        Row() {
          Text(tr('all')).fontSize(15).fontWeight(FontWeight.Medium).fontColor(Colors.secondary)
          Text(String(this.workspace.entries.length)).fontSize(13).fontColor(Colors.secondary).margin({left:5}).layoutWeight(1)
          if (this.workspace.tags.length || this.workspace.tag) Button((this.workspace.tag ? '#' + this.workspace.tag : tr('knowledge_all_tags')) + ' ˅',
            {buttonStyle:ButtonStyleMode.TEXTUAL}).fontSize(14).fontColor(Colors.link).height(34)
            .accessibilityText(tr('knowledge_filter_tag')).bindMenu(this.knowledgeFilters).id('knowledge.filters')
          Button({type:ButtonType.Circle}) { SymbolGlyph(Symbols.refresh).fontSize('15vp').fontColor([Colors.secondary]) }
            .width(32).height(32).padding(0).backgroundColor(Color.Transparent).accessibilityText(tr('refresh'))
            .onClick(() => this.workspace.refresh()).id('knowledge.refresh')
        }.width('100%').padding({left:4,right:2})
      }
      List() {
        this.listStatus()
        if (this.entryRowBindings.length || this.resultRowBindings.length) ListItemGroup() {
          this.sectionEntries()
        }.divider({strokeWidth:0.5,color:Colors.divider,startMargin:56}).backgroundColor(Colors.surface).borderRadius(14).clip(true)
          .id('knowledge.entries')
      }.layoutWeight(1).width('100%')
    }.width('100%').height('100%').padding({left:16,right:16,top:6,bottom:8}).backgroundColor(Colors.grouped)
  }
  @Builder recordingHome() {
    Column({space:6}) {
      this.searchField(true)
      List({space:5}) {
        if (!this.workspace.query.trim()) ListItem() {
          Column({space:6}) {
            Row({space:9}) {
              SymbolGlyph($r('sys.symbol.mic')).fontSize('19vp').fontColor([Colors.danger])
              Text(tr('knowledge_recording_open')).fontSize(16).fontWeight(FontWeight.Medium).fontColor(Colors.ink).layoutWeight(1)
              SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
            }.width('100%').constraintSize({minHeight:42}).onClick(() => this.openRecording()).id('recordings.options')
            Text(tr('phone_record_audio_local')).fontSize(13).fontColor(Colors.secondary).width('100%')
          }.width('100%').alignItems(HorizontalAlign.Start).padding(14).borderRadius(14).backgroundColor(Colors.surface)
        }
        if (this.visibleLocal().length) ListItem() { PhoneSectionTitle({text:tr('phone_record_local'),trailing:String(this.visibleLocal().length)}) }
        ForEach(this.visibleLocal(), (entry:KnowledgeRecordingEntry) => {
          ListItem() {
            Row({space:10}) {
              SymbolGlyph($r('sys.symbol.mic')).fontSize('19vp').fontColor([Colors.danger])
              Column({space:3}) {
                Text(entry.title || this.localDate(entry.startedAt)).fontSize(16).fontWeight(FontWeight.Medium).fontColor(Colors.ink).maxLines(2)
                Text(this.clock(entry.duration) + ' · ' + this.localDate(entry.startedAt)).fontSize(13).fontColor(Colors.secondary)
              }.layoutWeight(1).alignItems(HorizontalAlign.Start)
              SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
            }.width('100%').padding(13).borderRadius(12).backgroundColor(Colors.surface)
              .onClick(() => this.openRecording(entry.id)).id('recordings.local.' + entry.id)
          }
        }, (entry:KnowledgeRecordingEntry) => JSON.stringify([entry.id,entry.title,entry.duration,entry.uploadState,entry.transcriptionState]))
        if (!this.workspace.query.trim()) ListItem() {
          Row({space:10}) {
            SymbolGlyph(Symbols.calendar).fontSize('19vp').fontColor([Colors.link])
            Text(tr('phone_all_meetings')).fontSize(16).fontWeight(FontWeight.Medium).fontColor(Colors.ink).layoutWeight(1)
            SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
          }.width('100%').padding({left:14,right:13,top:6,bottom:6}).constraintSize({minHeight:48}).backgroundColor(Colors.surface).borderRadius(13)
            .onClick(() => this.onAllMeetings()).id('recordings.all')
        }
        this.listStatus()
        this.sectionEntries()
      }.width('100%').layoutWeight(1).id('recordings.list')
      Row() {
        Button({type:ButtonType.Capsule}) {
          Row({space:8}) {
            SymbolGlyph($r('sys.symbol.mic_fill')).fontSize('18vp').fontColor([Color.White])
            Text(tr(this.recording.captureState === 'recording' ? 'knowledge_recording_active' : 'phone_record_start')).fontSize(16).fontWeight(FontWeight.Medium).fontColor(Color.White)
          }
        }.width('100%').height(45).backgroundColor(Colors.danger).enabled(!this.model.demo && (this.recording.canStart() || this.recording.canStop()))
          .onClick(() => this.openRecording('', !this.recording.canStop())).id('recordings.capture')
      }.width('100%').padding({top:4,bottom:4}).id('knowledge.recording.open')
    }.width('100%').height('100%').padding({left:16,right:16,top:6,bottom:8}).backgroundColor(Colors.grouped).id('recordings.home')
      .bindSheet($$this.recordingOpen,this.recordingSheet(),{title:{title:tr('knowledge_recording_open')},height:SheetSize.LARGE,showClose:true,
        onDisappear:() => { this.recording.stopForBackground().catch(() => undefined); }})
  }
  @Builder listContent() {
    if (this.initialKind === 'recordings' && !this.localMeetings) this.recordingHome()
    else this.rootList()
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

  private editSaveKey(): string {
    if (this.workspace.writeState === 'unknown') return 'result_unknown';
    return ['sending', 'pending'].includes(this.workspace.writeState) ? this.workspace.writeState : 'save';
  }

  @Builder editForm() {
    Column({ space: Tokens.gap }) {
      Text(tr('knowledge_edit_metadata')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
      Select(['P0', 'P1', 'P2', 'P3'].map((value: string): SelectOption => ({ value: value }))).attributeModifier(SelectStyles.inline).value(this.workspace.editImportance)
        .onSelect((index: number) => this.workspace.setEditImportance(['P0', 'P1', 'P2', 'P3'][index] || 'P2')).id('knowledge.edit.importance')
      TextInput({ text: this.workspace.editRelevance, placeholder: tr('knowledge_relevance') }).type(InputType.Number)
        .onChange((value: string) => this.workspace.setEditRelevance(value)).id('knowledge.edit.relevance')
      TextInput({ text: this.workspace.editTags, placeholder: tr('knowledge_tags') }).onChange((value: string) => this.workspace.setEditTags(value)).id('knowledge.edit.tags')
      if (this.workspace.selected.path === 'projects') {
        Select([{ value: tr('knowledge_project_short') }, { value: tr('knowledge_project_long') }]).attributeModifier(SelectStyles.inline)
          .value(this.workspace.editProjectKind === 'long' ? tr('knowledge_project_long') : tr('knowledge_project_short') )
          .onSelect((index: number) => this.workspace.setEditProjectKind(index === 1 ? 'long' : 'short')).id('knowledge.edit.project_kind')
        Select([{ value: tr('knowledge_status_active') }, { value: tr('knowledge_status_paused') },
          { value: tr('knowledge_status_done') }, { value: tr('knowledge_status_ignored') }]).attributeModifier(SelectStyles.inline)
          .value(tr('knowledge_status_' + this.workspace.editStatus))
          .onSelect((index: number) => this.workspace.setEditStatus(['active', 'paused', 'done', 'ignored'][index] || 'active')).id('knowledge.edit.status')
      }
      Row({ space: Tokens.gap }) {
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('cancel')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('cancel', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('cancel')).bindTips(tr('cancel')).onClick(() => this.workspace.editOpen = false)
        Button({ type: ButtonType.Circle }) { if (IconActions.busy(this.editSaveKey())) LoadingProgress().width(Tokens.symbol).height(Tokens.symbol).color(Colors.onAction); else SymbolGlyph(IconActions.symbol(this.editSaveKey())).fontSize(Tokens.symbolFont).fontColor([IconActions.color(this.editSaveKey(), true)]) }.attributeModifier(ButtonStyles.iconPrimary).accessibilityText(tr(this.editSaveKey())).bindTips(tr(this.editSaveKey())).enabled(this.workspace.canSaveEdit())
          .onClick(() => this.workspace.saveEdit()).id('knowledge.edit.save')
      }.width('100%').justifyContent(FlexAlign.End)
    }.width('100%').alignItems(HorizontalAlign.Start).padding(Tokens.inset)
  }

  private openFacts():void { this.facts.begin(Wire.text(Wire.object(this.workspace.detail,'meta'),'pinned')); }

  @Builder entryEvidence() {
    if (this.workspace.agents.length) {
      Column({ space: Tokens.gap }) {
        Text(tr('knowledge_project_agents')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
        ForEach(this.agentRowBindings, (binding: BoundRow<ResourceRow>) => Button({ type: ButtonType.Normal }) { this.navigationLabel(binding.value.title || binding.value.id) }.buttonStyle(ButtonStyleMode.TEXTUAL).backgroundColor(Color.Transparent).padding(0).width('100%')
          .onClick(() => this.onChat(binding.value)).id('knowledge.agent.' + binding.value.id), (binding: BoundRow<ResourceRow>) => binding.key)
      }.width('100%').alignItems(HorizontalAlign.Start)
    }
    if (this.workspace.matches.length) {
      Column({ space: Tokens.gap }) {
        Text(tr('knowledge_search_matches')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
        ForEach(this.workspace.matches, (match: Record<string, Object>, index: number) => Text(Wire.text(match, 'line'))
          .fontSize(Tokens.meta).width('100%').id('knowledge.match.' + String(index)),
          (match: Record<string, Object>, index: number) => JSON.stringify([index, Wire.text(match, 'id'), JSON.stringify(match)]))
      }.width('100%').alignItems(HorizontalAlign.Start)
    }
    if (Wire.strings(this.workspace.detail, 'log').length) {
      Column({ space: Tokens.gap }) { Text(tr('knowledge_timeline')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
        ForEach(Wire.strings(this.workspace.detail, 'log').slice().reverse(), (line: string, index: number) => Text(line).fontSize(Tokens.meta).width('100%')
          .id('knowledge.timeline.' + String(index)), (line: string, index: number) => JSON.stringify([index, line]))
      }.width('100%').alignItems(HorizontalAlign.Start)
    }
  }

  @Builder recordingDetail() {
    Scroll() {
      Column({space:8}) {
        Row({space:10}) {
          Stack() { SymbolGlyph(this.kindIcon('recordings')).fontSize('20vp').fontColor([Colors.secondary]) }
            .width(36).height(36).backgroundColor(Colors.control).borderRadius(10)
          Text(this.workspace.selected.title || this.workspace.selected.id).fontSize(22).fontWeight(FontWeight.Bold).fontColor(Colors.ink).layoutWeight(1)
        }.width('100%')
        if (this.workspace.selected.detail) Text(this.workspace.selected.detail).fontSize(16).fontColor(Colors.ink).width('100%')
        Button({type:ButtonType.Capsule}) {
          Row({space:7}) {
            SymbolGlyph(IconActions.symbol('knowledge_ask_persona')).fontSize('18vp').fontColor([Colors.onAction])
            Text(tr('knowledge_ask_persona')).fontSize(17).fontColor(Colors.onAction)
          }
        }.width('100%').constraintSize({minHeight:46}).backgroundColor(Colors.action).onClick(() => this.askPersona()).id('knowledge.ask')
        this.entryEvidence()
        PhoneSectionTitle({text:tr('phone_kb_confirmed')})
        Column({space:6}) {
          if (this.facts.open) {
            if (this.facts.conflict) {
              Text(tr('phone_facts_changed')).fontSize(14).fontColor(Colors.needs).width('100%')
              Text(this.facts.baseline || tr('empty')).fontSize(15).fontColor(Colors.ink).width('100%').copyOption(CopyOptions.LocalDevice).id('knowledge.facts.latest')
            }
            TextArea({text:this.facts.text,placeholder:tr('phone_kb_confirmed')}).width('100%').height(100)
              .onChange((v:string) => this.facts.edit(v)).id('knowledge.facts.text')
            Row() {
              Button(tr('cancel'),{buttonStyle:ButtonStyleMode.TEXTUAL}).onClick(() => this.facts.close())
              Button(tr('save'),{buttonStyle:ButtonStyleMode.TEXTUAL})
                .enabled(this.workspace.supportsExpectedPinned && !this.workspace.busy && !['pending','unknown'].includes(this.workspace.writeState) &&
                  this.facts.text.trim()!==Wire.text(Wire.object(this.workspace.detail,'meta'),'pinned').trim())
                .onClick(() => this.facts.save(this.workspace)).id('knowledge.facts.save')
            }
          } else {
            if (Wire.text(Wire.object(this.workspace.detail,'meta'),'pinned')) Text(Wire.text(Wire.object(this.workspace.detail,'meta'),'pinned'))
              .fontSize(16).fontColor(Colors.ink).width('100%')
            if (!this.workspace.loading && !this.workspace.error && Object.keys(this.workspace.detail).length>0 && !this.workspace.supportsExpectedPinned) Text(tr('phone_conditional_read_only'))
              .fontSize(14).fontColor(Colors.secondary).width('100%').padding({top:8,bottom:8}).id('knowledge.facts.read_only')
            if (this.workspace.supportsExpectedPinned) Button({type:ButtonType.Normal}) {
              Row({space:8}) {
                SymbolGlyph(Wire.text(Wire.object(this.workspace.detail,'meta'),'pinned') ? Symbols.edit : Symbols.plus).fontSize('17vp').fontColor([Colors.link])
                Text(tr(Wire.text(Wire.object(this.workspace.detail,'meta'),'pinned') ? 'edit' : 'phone_kb_add_fact')).fontSize(16).fontColor(Colors.link)
              }.width('100%')
            }.width('100%').constraintSize({minHeight:44}).padding(0).backgroundColor(Color.Transparent).onClick(() => this.openFacts()).id('knowledge.facts.open')
          }
        }.width('100%').alignItems(HorizontalAlign.Start).padding({left:14,right:14,top:4,bottom:4}).backgroundColor(Colors.surface).borderRadius(14)
        Text(tr('phone_kb_fact_note')).fontSize(13).fontColor(Colors.secondary).width('100%').padding({left:6,right:6})
        if (Wire.text(this.workspace.detail,'summary')) {
          PhoneSectionTitle({text:tr('phone_kb_profile')})
          Column() { MarkdownText({text:Wire.text(this.workspace.detail,'summary'),onLink:(url:string) => this.openSafeLink(url)}) }
            .width('100%').alignItems(HorizontalAlign.Start).padding(14).backgroundColor(Colors.surface).borderRadius(14).id('knowledge.summary')
        }
        if (Wire.text(this.workspace.detail,'path')) {
          PhoneSectionTitle({text:tr('directory')})
          Text(Wire.text(this.workspace.detail,'path')).fontFamily('monospace').fontSize(13).fontColor(Colors.secondary).width('100%')
            .padding(14).backgroundColor(Colors.surface).borderRadius(14).copyOption(CopyOptions.LocalDevice).id('knowledge.recording.directory')
        }
        if (this.workspace.error && !(this.facts.open && this.facts.conflict && this.workspace.error==='phone_facts_changed')) Text(tr(this.workspace.error)).fontSize(14).fontColor(Colors.danger).width('100%')
        Button({type:ButtonType.Normal}) {
          Row() {
            Text(tr('knowledge_minutes')+' · '+tr('knowledge_transcript')).fontSize(16).fontColor(Colors.ink).layoutWeight(1)
            SymbolGlyph(this.recordingTools ? Symbols.down : Symbols.expand).fontSize('14vp').fontColor([Colors.secondary])
          }.width('100%')
        }.width('100%').constraintSize({minHeight:44}).padding(12).backgroundColor(Colors.surface).borderRadius(12)
          .accessibilityDescription(tr(this.recordingTools ? 'control_expanded' : 'control_collapsed')).onClick(() => this.recordingTools=!this.recordingTools).id('knowledge.recording.tools')
        if (this.recordingTools) {
          this.recordingSection(UIUtils.makeBinding(():Record<string,Object> => this.workspace.detail))
          this.relatedEntries(UIUtils.makeBinding(():Record<string,Object> => this.workspace.detail))
          if (this.importedMeeting()) Button(this.importedMeeting()!.title,{buttonStyle:ButtonStyleMode.TEXTUAL}).onClick(() => {
            const item=this.importedMeeting(); if (item) this.onOpenResource(item);
          }).id('knowledge.recording.imported')
          Button(tr('knowledge_edit_metadata'),{buttonStyle:ButtonStyleMode.TEXTUAL}).onClick(() => this.workspace.beginEdit()).id('knowledge.edit.open')
          if (this.workspace.editOpen) this.editForm()
          Button(tr('knowledge_forget_recording'),{buttonStyle:ButtonStyleMode.TEXTUAL}).fontColor(Colors.danger)
            .enabled(this.workspace.canDeleteRecording()).onClick(() => this.confirmDeleteRecording()).id('knowledge.recording.delete')
        }
      }.width('100%').alignItems(HorizontalAlign.Start).padding({left:16,right:16,top:20,bottom:24})
    }.width('100%').height('100%').align(Alignment.TopStart).backgroundColor(Colors.grouped).id('knowledge.recording.detail')
  }

  @Builder detailContent() {
    Scroll() {
      Column({ space: Tokens.section }) {
        Column({ space: Tokens.gap }) {
          if (Wire.text(Wire.object(this.workspace.detail, 'meta'), 'importance')) Text(Wire.text(Wire.object(this.workspace.detail, 'meta'), 'importance') + ' · ' + tr('knowledge_relevance') + ' ' + String(Wire.number(Wire.object(this.workspace.detail, 'meta'), 'relevance')))
            .fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).width('100%')
          if (this.workspace.selected.detail) Text(this.workspace.selected.detail).fontSize(Tokens.body).width('100%')
        if (Wire.text(this.workspace.detail, 'summary')) {
          Column({ space: Tokens.gap }) { Text(tr(this.workspace.selected.path === 'recordings' ? 'knowledge_recording_summary' : 'knowledge_profile')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
            MarkdownText({ text: Wire.text(this.workspace.detail, 'summary'), onLink: (url: string) => this.openSafeLink(url) })
          }.width('100%').alignItems(HorizontalAlign.Start).padding(14).backgroundColor(Colors.surface).borderRadius(14).id('knowledge.summary')
        }

          if (this.workspace.selected.path === 'topics' && Wire.rows(this.workspace.detail,'points').length) {
            ForEach(Wire.rows(this.workspace.detail,'points'), (point:Record<string,Object>) => {
              Column({space:5}) {
                if (Wire.strings(point,'path').length) Text(Wire.strings(point,'path').join(' › ')).fontSize(13).fontColor(Colors.link)
                Text(Wire.text(point,'text')).fontSize(17).fontColor(Colors.ink).copyOption(CopyOptions.LocalDevice)
                if (Wire.text(point,'why')) Text(Wire.text(point,'why')).fontSize(14).fontColor(Colors.secondary)
                if (Wire.text(point,'when')) Text(Wire.text(point,'when')).fontSize(12).fontColor(Colors.secondary)
                ForEach(Wire.rows(point,'sources'), (source:Record<string,Object>) => {
                  if (Wire.text(source,'id') && KB_KINDS.includes(Wire.text(source,'kind'))) {
                    Button(Wire.text(source,'name') || Wire.text(source,'id'),{buttonStyle:ButtonStyleMode.TEXTUAL})
                      .fontSize(14).fontColor(Colors.link).onClick(() => this.openRelated(source))
                  }
                }, (source:Record<string,Object>) => JSON.stringify([Wire.text(source,'kind'),Wire.text(source,'id')]))
              }.width('100%').alignItems(HorizontalAlign.Start).padding(13).backgroundColor(Colors.surface).borderRadius(12)
                .id('knowledge.point.' + Wire.text(point,'id'))
            }, (point:Record<string,Object>) => JSON.stringify(point))
          }
          if (this.workspace.selected.path === 'recordings' && this.importedMeeting()) {
            Button({type:ButtonType.Normal}) {
              Row({space:8}) {
                SymbolGlyph(Symbols.calendar).fontSize('17vp').fontColor([Colors.link])
                Text(this.importedMeeting()!.title).fontSize(16).fontColor(Colors.ink).layoutWeight(1)
                SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
              }.width('100%').padding(12)
            }.padding(0).width('100%').backgroundColor(Colors.surface).onClick(() => {
              const row = this.importedMeeting(); if (row) this.onOpenResource(row);
            }).id('knowledge.recording.imported')
          }
          Row({ space: Tokens.gap }) {
            Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('knowledge_ask_persona')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('knowledge_ask_persona', true)]) }.attributeModifier(ButtonStyles.iconPrimary).accessibilityText(tr('knowledge_ask_persona')).bindTips(tr('knowledge_ask_persona')).onClick(() => this.askPersona()).id('knowledge.ask')
            Button({ type: ButtonType.Circle }) { SymbolGlyph(Symbols.edit).fontSize(Tokens.symbolFont).fontColor([Colors.link]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('knowledge_edit_metadata'))
              .onClick(() => this.workspace.beginEdit()).id('knowledge.edit.open')
          }.width('100%')
          if (this.workspace.selected.path === 'projects') Button({ type: ButtonType.Normal }) {
            Row({ space: Tokens.gap }) {
            SymbolGlyph(Symbols.agent).fontSize(Tokens.symbolFont).fontColor([Colors.ai])
            Text(tr('knowledge_agent_configuration')).fontSize(Tokens.body).fontColor(Colors.link).layoutWeight(1)
            SymbolGlyph(this.dispatchExpanded ? Symbols.up : Symbols.down).fontSize(Tokens.meta).fontColor([Colors.secondary])
          }.width('100%')
          }.attributeModifier(ButtonStyles.quiet).width('100%').onClick(() => this.dispatchExpanded = !this.dispatchExpanded).accessibilityText(tr('knowledge_agent_configuration')).accessibilityDescription(tr(this.dispatchExpanded ? 'control_expanded' : 'control_collapsed')).id('knowledge.dispatch.toggle')
          if (this.workspace.selected.path === 'projects' && this.dispatchExpanded) {
            TextArea({ text: this.workspace.agentDraft, placeholder: tr('knowledge_agent_prompt') }).height(Tokens.inputExpanded)
              .onChange((value: string) => this.workspace.setAgentDraft(value)).id('knowledge.agent.prompt')
            if (this.workspace.directoryTargets.length) {
              Select(this.directoryOptions()).attributeModifier(SelectStyles.inline).value(this.workspace.directoryTargets[this.workspace.selectedDirectory]?.label || '').onSelect((index: number) => this.workspace.selectedDirectory = index)
                .id('knowledge.agent.directory')
              TextInput({ text: this.workspace.agentLauncher, placeholder: tr('launcher') }).onChange((value: string) => this.workspace.setAgentLauncher(value))
                .id('knowledge.agent.launcher')
              Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('knowledge_start_agent')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('knowledge_start_agent', true)]) }.attributeModifier(ButtonStyles.iconPrimary).accessibilityText(tr('knowledge_start_agent')).bindTips(tr('knowledge_start_agent')).enabled(!!this.workspace.agentDraft.trim() && !!this.workspace.agentLauncher.trim() && !this.workspace.busy).onClick(() => this.confirmDispatch()).id('knowledge.agent.start')
            } else Text(tr('knowledge_agent_unavailable')).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary'))
          }
          if (this.workspace.error) Text(tr(this.workspace.error)).fontSize(Tokens.meta).fontColor($r('app.color.persona_danger')).width('100%')
        }.alignItems(HorizontalAlign.Start).width('100%')
        if (this.workspace.editOpen) this.editForm()
        this.entryEvidence()
        if (this.workspace.selected.path === 'recordings') this.recordingSection(UIUtils.makeBinding((): Record<string, Object> => this.workspace.detail))
        if (this.workspace.selected.path !== 'recordings' && Wire.text(this.workspace.detail, 'recent')) {
          Column({ space: Tokens.gap }) { Text(tr('knowledge_recent')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
            Text(Wire.text(this.workspace.detail, 'recent')).fontSize(Tokens.meta).width('100%')
          }.width('100%').alignItems(HorizontalAlign.Start)
        }
        if (this.workspace.selected.path === 'projects' && Wire.rows(this.workspace.detail, 'moments').length) {
          Column({ space: Tokens.gap }) {
            Text(tr('knowledge_related_moments')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
            ForEach(Wire.rows(this.workspace.detail, 'moments'), (moment: Record<string, Object>) => Button({ type: ButtonType.Normal }) { this.navigationLabel(Wire.text(moment, 'author') + ' · ' + Wire.text(moment, 'text')) }.buttonStyle(ButtonStyleMode.TEXTUAL).backgroundColor(Color.Transparent).padding(0).width('100%').onClick(() => this.loadMoment(Wire.text(moment, 'id'))).id('knowledge.moment.' + Wire.text(moment, 'id')),
              (moment: Record<string, Object>, index: number) => JSON.stringify([index, Wire.text(moment, 'id'), JSON.stringify(moment)]))
            if (this.momentText) MarkdownText({ text: this.momentText, onLink: (url: string) => this.openSafeLink(url) })
          }.width('100%').alignItems(HorizontalAlign.Start)
        }
        this.relatedEntries(UIUtils.makeBinding((): Record<string, Object> => this.workspace.detail))
        if (Wire.text(Wire.object(this.workspace.detail, 'meta'), 'url')) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('knowledge_open_link')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('knowledge_open_link', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('knowledge_open_link')).bindTips(tr('knowledge_open_link')).onClick(() => this.openSafeLink(Wire.text(Wire.object(this.workspace.detail, 'meta'), 'url')))
        if (Wire.text(this.workspace.detail, 'path')) Text(Wire.text(this.workspace.detail, 'path')).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).width('100%')
        if (this.workspace.selected.path === 'recordings') Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('knowledge_forget_recording')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('knowledge_forget_recording', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('knowledge_forget_recording')).bindTips(tr('knowledge_forget_recording')).enabled(this.workspace.canDeleteRecording()).onClick(() => this.confirmDeleteRecording()).id('knowledge.recording.delete')
      }.padding(Tokens.page).width('100%').alignItems(HorizontalAlign.Start)
    }.align(Alignment.TopStart).scrollable(ScrollDirection.Vertical).scrollBar(BarState.Auto).width('100%').height('100%')
  }

  @Builder navigationLabel(label: string) {
    Row({ space: Tokens.gap }) {
      Text(label).fontSize(Tokens.body).fontColor(Colors.ink).layoutWeight(1)
      SymbolGlyph(Symbols.expand).fontSize(Tokens.meta).fontColor([Colors.secondary])
    }.width('100%').constraintSize({ minHeight: Tokens.touch }).padding({ top: Tokens.inset, bottom: Tokens.inset })
  }

  @Builder relatedEntries(detail: Binding<Record<string, Object>>) {
    if (Wire.rows(detail.value, 'links').length) {
      Column({ space: Tokens.gap }) {
        Text(tr('knowledge_related')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
        ForEach(Wire.rows(detail.value, 'links'), (item: Record<string, Object>) => {
          Button({ type: ButtonType.Normal }) { this.navigationLabel(Wire.text(item, 'title', Wire.text(item, 'id'))) }.buttonStyle(ButtonStyleMode.TEXTUAL).backgroundColor(Color.Transparent).padding(0).width('100%').onClick(() => this.openRelated(item)).id('knowledge.related.' + Wire.text(item, 'kind') + '.' + Wire.text(item, 'id'))
        }, (item: Record<string, Object>, index: number) => JSON.stringify([index, Wire.text(item, 'kind') + '/' + Wire.text(item, 'id'), JSON.stringify(item)]))
      }.width('100%').alignItems(HorizontalAlign.Start)
    }
  }

  @Builder recordingSection(detail: Binding<Record<string, Object>>) {
    Column({ space: Tokens.gap }) {
      if (Object.keys(Wire.object(detail.value, 'minutes')).length) {
        Text(tr('knowledge_minutes')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
        if (Wire.text(Wire.object(detail.value, 'minutes'), 'markdown')) MarkdownText({ text: Wire.text(Wire.object(detail.value, 'minutes'), 'markdown'), onLink: (url: string) => this.openSafeLink(url) })
        ForEach(Wire.rows(Wire.object(detail.value, 'minutes'), 'actions'), (action: Record<string, Object>) => {
          Row({ space: Tokens.gap }) { Toggle({ type: ToggleType.Checkbox, isOn: this.selectedActions.includes(Wire.text(action, 'id')) }).onChange(() => this.toggleAction(Wire.text(action, 'id')))
            Text(Wire.text(action, 'what')).fontSize(Tokens.meta).layoutWeight(1)
          }.width('100%').id('knowledge.minutes.action.' + Wire.text(action, 'id'))
        }, (action: Record<string, Object>, index: number) => JSON.stringify([index, Wire.text(action, 'id'), JSON.stringify(action)]))
        if (this.selectedActions.length) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('knowledge_minutes_add_todos')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('knowledge_minutes_add_todos', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('knowledge_minutes_add_todos')).bindTips(tr('knowledge_minutes_add_todos')).onClick(() => this.confirmAddTodos())
      }
      Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('knowledge_minutes_make')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('knowledge_minutes_make', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('knowledge_minutes_make')).bindTips(tr('knowledge_minutes_make')).enabled(!this.workspace.busy && this.workspace.writeState !== 'pending' && this.workspace.writeState !== 'unknown').onClick(() => this.workspace.makeMinutes()).id('knowledge.minutes.make')
      if (Wire.rows(detail.value, 'segments').length) {
        Text(tr('knowledge_transcript')).fontSize(Tokens.body).fontWeight(FontWeight.Medium)
        ForEach(Wire.rows(detail.value, 'segments'), (line: Record<string, Object>, index: number) => Text(Wire.text(line, 'text'))
          .fontSize(Tokens.meta).width('100%').id('knowledge.transcript.' + String(index)),
          (line: Record<string, Object>, index: number) => JSON.stringify([index, JSON.stringify(line)]))
      } else if (Wire.text(detail.value, 'recent')) Text(Wire.text(detail.value, 'recent')).fontSize(Tokens.meta).width('100%')
    }.width('100%').alignItems(HorizontalAlign.Start).id('knowledge.recording.transcript')
  }

  private importedMeeting(): ResourceRow | undefined {
    const selected = this.workspace.selected;
    const merged = Wire.object(selected.data(),'imported_meeting');
    const data = Wire.text(merged,'id') ? merged : Wire.object(Wire.object(this.workspace.detail,'meta'),'imported_meeting');
    if (!Wire.text(data,'id')) return undefined;
    const row = ResourceRow.from(data,'knowledge');
    row.path = 'meetings'; row.group = selected.group; row.manager = selected.manager;
    if (!row.title || row.title === row.id) row.title = tr('knowledge_kind_meetings');
    return row;
  }
  private directoryOptions(): SelectOption[] { return this.workspace.directoryTargets.map((target: KnowledgeDirectory): SelectOption => ({ value: target.label })); }
  private openRelated(item: Record<string, Object>): void { const target = ResourceRow.from(item, 'knowledge'); target.path = Wire.text(item, 'kind'); target.manager = this.workspace.selected.manager; this.onOpenResource(target); }
  private async loadMoment(id: string): Promise<void> { this.momentText = await this.workspace.openMoment(id).catch(() => ''); }

  build() {
    if (this.row && this.workspace.selected.id && this.workspace.selected.path==='recordings') this.recordingDetail();
    else if (this.row && this.workspace.selected.id) this.detailContent();
    else this.listContent();
  }
}
