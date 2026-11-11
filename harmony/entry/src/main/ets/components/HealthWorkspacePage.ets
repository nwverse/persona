import { common } from '@kit.AbilityKit';
import { dataSharePredicates } from '@kit.ArkData';
import { fileIo, picker } from '@kit.CoreFileKit';
import { photoAccessHelper } from '@kit.MediaLibraryKit';
import { ClientModel } from '../models/ClientModel';
import { HealthMemoryFile, HealthReport, HealthWorkspace } from '../models/HealthWorkspace';
import { ResourceRow, Wire } from '../models/Wire';
import { MarkdownText } from './MarkdownText';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';

const HEALTH_SUGGESTIONS: string[] = ['health_question_sleep', 'health_question_steps', 'health_log_weight', 'health_log_water'];
const HEALTH_REPORT_LIMIT: number = 32 * 1024 * 1024;
const HEALTH_REPORT_SELECTION_LIMIT: number = 8;

class HealthPickedReport {
  name: string = '';
  bytes: ArrayBuffer = new ArrayBuffer(0);
  constructor(name: string, bytes: ArrayBuffer) { this.name = name; this.bytes = bytes; }
}

@ComponentV2
export struct HealthWorkspacePage {
  @Require @Param model: ClientModel;
  @Param row: ResourceRow | undefined = undefined;
  @Param list: boolean = true;
  @Event onOpenResource: (row: ResourceRow) => void = (_row: ResourceRow) => {};
  @Event onChat: (row: ResourceRow) => void = (_row: ResourceRow) => {};
  @Event onLink: (url: string) => void = (_url: string) => {};
  @Local workspace: HealthWorkspace = new HealthWorkspace();
  @Local editingMemory: boolean = false;
  @Local pickerBusy: boolean = false;
  @Local pickerError: string = '';
  private active: boolean = false;
  private routedKey: string = '';
  private pickerRevision: number = 0;

  aboutToAppear(): void {
    this.active = true; this.routedKey = ''; this.pickerRevision++;
    this.workspace.open(this.model).then(() => this.routeRow()).catch(() => undefined);
  }
  aboutToDisappear(): void {
    this.active = false; this.pickerRevision++; this.pickerBusy = false; this.pickerError = ''; this.workspace.hide();
  }

  @Monitor('row') rowChanged(): void { if (this.active) this.workspace.open(this.model).then(() => this.routeRow()).catch(() => undefined); }
  @Monitor('list') listChanged(): void { if (this.active) this.workspace.open(this.model).then(() => this.routeRow()).catch(() => undefined); }
  @Monitor('model.profile') profileChanged(): void {
    this.pickerRevision++; this.pickerBusy = false; this.pickerError = '';
    if (this.active) this.workspace.open(this.model).then(() => this.routeRow()).catch(() => undefined);
  }
  @Monitor('model.connected') connectionChanged(): void {
    if (this.active && this.model.connected) this.workspace.refresh().catch(() => undefined);
  }

  private send(text: string): void {
    if (!text.trim() || !this.active || this.workspace.busy || this.pickerBusy) return;
    this.workspace.startConversation(text).then((row: ResourceRow | undefined) => {
      if (row && this.active && this.workspace.isHealthConversation(row)) this.onChat(row);
    }).catch(() => undefined);
  }

  private routeRow(): void {
    if (!this.active || this.list || !this.row) return;
    const target = this.row.key();
    if (target === this.routedKey) return;
    this.routedKey = target;
    if (this.row.kind === 'file') this.onOpenResource(this.row);
    else if (this.workspace.isHealthConversation(this.row)) this.onChat(this.row);
  }

  private safeLink(url: string): void { if (/^https?:\/\//i.test(url.trim())) this.onLink(url.trim()); }

  @Builder reportUploadMenu() {
    Menu() {
      MenuItem({ content: tr('health_upload_pdf') }).enabled(!this.pickerBusy && this.workspace.canUploadReports())
        .onClick(() => this.pickReportDocuments())
      MenuItem({ content: tr('health_upload_photos') }).enabled(!this.pickerBusy && this.workspace.canUploadReports())
        .onClick(() => this.pickReportPhotos())
    }
  }

  private pickerOwner(scope: string, manager: string): boolean {
    return this.active && this.model.scope() === scope && this.model.profile.manager === manager;
  }

  private pickerCurrent(scope: string, manager: string, revision: number): boolean {
    return revision === this.pickerRevision && this.pickerOwner(scope, manager);
  }

  private reportName(uri: string): string {
    const leaf = uri.substring(uri.lastIndexOf('/') + 1);
    try { return decodeURIComponent(leaf); } catch (_) { return leaf; }
  }

  private reportPickerError(failure: Error): string {
    return failure.message === 'health_report_file_too_large' ? 'health_report_file_too_large' : 'health_report_file_read_failed';
  }

  private readReport(uri: string, name: string = ''): HealthPickedReport {
    const info = fileIo.statSync(uri);
    if (!Number.isSafeInteger(info.size) || info.size <= 0) throw new Error('health_report_file_read_failed');
    if (info.size > HEALTH_REPORT_LIMIT) throw new Error('health_report_file_too_large');
    const file = fileIo.openSync(uri, fileIo.OpenMode.READ_ONLY);
    try {
      const bytes = new ArrayBuffer(info.size);
      let offset = 0;
      while (offset < info.size) {
        const chunk = new ArrayBuffer(Math.min(256 * 1024, info.size - offset));
        const read = fileIo.readSync(file.fd, chunk, { length: chunk.byteLength });
        if (read <= 0 || read > chunk.byteLength) throw new Error('health_report_file_read_failed');
        new Uint8Array(bytes, offset, read).set(new Uint8Array(chunk, 0, read));
        offset += read;
      }
      if (offset !== info.size) throw new Error('health_report_file_read_failed');
      return new HealthPickedReport(name || this.reportName(uri), bytes);
    } finally { fileIo.closeSync(file.fd); }
  }

  private appendReportPath(path: string): void {
    if (!this.workspace.appendReportPath(path, tr('health_report_prompt')))
      this.pickerError = 'health_report_file_read_failed';
  }

  private async pickReportDocuments(): Promise<void> {
    const scope = this.model.scope(), manager = this.model.profile.manager;
    const revision = ++this.pickerRevision;
    this.pickerBusy = true; this.pickerError = '';
    try {
      const options = new picker.DocumentSelectOptions();
      options.maxSelectNumber = HEALTH_REPORT_SELECTION_LIMIT;
      options.fileSuffixFilters = ['PDF (.pdf)|.pdf'];
      const uris = await new picker.DocumentViewPicker(this.getUIContext().getHostContext() as common.UIAbilityContext).select(options);
      if (!this.pickerCurrent(scope, manager, revision)) return;
      for (const uri of uris) {
        if (!this.pickerCurrent(scope, manager, revision)) return;
        const report = this.readReport(uri);
        const path = await this.workspace.uploadReport(report.name, report.bytes);
        if (!this.pickerCurrent(scope, manager, revision)) return;
        if (!path) return;
        this.appendReportPath(path);
      }
    } catch (failure) {
      if (this.pickerCurrent(scope, manager, revision)) this.pickerError = this.reportPickerError(failure as Error);
    } finally { if (this.pickerCurrent(scope, manager, revision)) this.pickerBusy = false; }
  }

  private async pickReportPhotos(): Promise<void> {
    const scope = this.model.scope(), manager = this.model.profile.manager;
    const revision = ++this.pickerRevision;
    const host = this.getUIContext().getHostContext();
    if (!host) { this.pickerError = 'health_report_file_read_failed'; return; }
    this.pickerBusy = true; this.pickerError = '';
    let helper: photoAccessHelper.PhotoAccessHelper | undefined;
    try {
      const options = new photoAccessHelper.PhotoSelectOptions();
      options.MIMEType = photoAccessHelper.PhotoViewMIMETypes.IMAGE_TYPE;
      options.maxSelectNumber = HEALTH_REPORT_SELECTION_LIMIT;
      const selected = await new photoAccessHelper.PhotoViewPicker().select(options);
      if (!this.pickerCurrent(scope, manager, revision)) return;
      helper = photoAccessHelper.getPhotoAccessHelper(host as common.UIAbilityContext);
      for (const uri of selected.photoUris) {
        if (!this.pickerCurrent(scope, manager, revision)) return;
        const predicates = new dataSharePredicates.DataSharePredicates().equalTo('uri', uri);
        const result = await helper.getAssets({ fetchColumns: ['uri', 'display_name', 'media_type'], predicates: predicates });
        let asset: photoAccessHelper.PhotoAsset | undefined;
        try {
          if (!this.pickerCurrent(scope, manager, revision)) return;
          if (result.getCount() !== 1) throw new Error('health_report_file_read_failed');
          asset = await result.getFirstObject();
        } finally { result.close(); }
        if (!this.pickerCurrent(scope, manager, revision)) return;
        if (!asset || asset.photoType !== photoAccessHelper.PhotoType.IMAGE || asset.uri !== uri) throw new Error('health_report_file_read_failed');
        const report = this.readReport(asset.uri, asset.displayName || this.reportName(asset.uri));
        const path = await this.workspace.uploadReport(report.name, report.bytes);
        if (!this.pickerCurrent(scope, manager, revision)) return;
        if (!path) return;
        this.appendReportPath(path);
      }
    } catch (failure) {
      if (this.pickerCurrent(scope, manager, revision)) this.pickerError = this.reportPickerError(failure as Error);
    } finally {
      try { await helper?.release(); } catch (_) {}
      if (this.pickerCurrent(scope, manager, revision)) this.pickerBusy = false;
    }
  }

  private confirmArchive(row: ResourceRow): void {
    if (!this.workspace.canArchiveConversation(row)) return;
    const generation = this.model.connectionGeneration(), scope = this.model.scope(), target = row.key();
    this.getUIContext().showAlertDialog({ title: tr('health_archive_title'), message: row.title || row.id,
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('health_archive'), action: () => {
        if (!this.active || !this.model.owns(generation, scope) || target !== row.key()) return;
        this.workspace.archive(row).catch(() => false);
      } } });
  }

  private confirmDeleteMemory(file: HealthMemoryFile): void {
    if (!this.workspace.canEditMemory() || this.workspace.selectedMemory?.name !== file.name) return;
    const generation = this.model.connectionGeneration(), scope = this.model.scope(), target = file.name;
    this.getUIContext().showAlertDialog({ title: tr('health_memory_delete_title'), message: file.name,
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('delete'), action: () => {
        if (!this.active || !this.model.owns(generation, scope) || this.workspace.selectedMemory?.name !== target) return;
        this.workspace.deleteMemory().catch(() => false);
      } } });
  }

  private confirmDeleteReport(report: HealthReport): void {
    const generation = this.model.connectionGeneration(), scope = this.model.scope(), target = report.row.key();
    this.getUIContext().showAlertDialog({ title: tr('health_report_delete_title'), message: tr('health_report_delete_message'),
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('delete'), action: () => {
        if (!this.active || !this.model.owns(generation, scope) || !this.workspace.reports.some((item: HealthReport) => item.row.key() === target)) return;
        this.workspace.deleteReport(report).catch(() => false);
      } } });
  }

  private openReport(report: HealthReport, record: boolean): void {
    const row = this.workspace.openReport(report, record);
    if (row) this.onOpenResource(row);
  }

  @Builder memoryDetail(file: HealthMemoryFile) {
    Column({ space: Tokens.gap }) {
      Row({ space: Tokens.gap }) {
        Text(file.name === 'MEMORY.md' ? tr('health_memory_index') : file.name).fontSize(Tokens.body).fontWeight(FontWeight.Medium).layoutWeight(1)
        if (file.name !== 'MEMORY.md' && !this.editingMemory) Button(tr('health_memory_edit')).height(Tokens.touch)
          .onClick(() => this.editingMemory = true).id('health.memory.edit')
      }.width('100%')
      if (this.editingMemory && file.name !== 'MEMORY.md') {
        TextArea({ text: this.workspace.memoryDraft, placeholder: tr('health_memory_content') }).height(Tokens.inputExpanded)
          .onChange((value: string) => this.workspace.setMemoryDraft(value)).id('health.memory.editor')
        if (this.workspace.memoryError) Text(tr(this.workspace.memoryError)).fontSize(Tokens.meta)
          .fontColor($r('app.color.persona_danger')).width('100%')
        Row({ space: Tokens.gap }) {
          Button(tr('cancel')).layoutWeight(1).height(Tokens.touch).onClick(() => {
            this.editingMemory = false; this.workspace.openMemory(file);
          })
          Button(tr('save')).layoutWeight(1).height(Tokens.touch).enabled(this.workspace.canSaveMemory())
            .onClick(() => this.workspace.saveMemory().then((saved: boolean) => { if (saved) this.editingMemory = false; }).catch(() => false))
            .id('health.memory.save')
          Button(tr('delete')).layoutWeight(1).height(Tokens.touch).enabled(this.workspace.canEditMemory() && !this.workspace.busy)
            .onClick(() => this.confirmDeleteMemory(file)).id('health.memory.delete')
        }.width('100%')
      } else {
        MarkdownText({ text: this.workspace.memoryDraft, onLink: (url: string) => this.safeLink(url) })
        if (this.workspace.memoryError) Text(tr(this.workspace.memoryError)).fontSize(Tokens.meta)
          .fontColor($r('app.color.persona_danger')).width('100%')
        if (file.name !== 'MEMORY.md') Button(tr('delete')).height(Tokens.touch).enabled(this.workspace.canEditMemory() && !this.workspace.busy)
          .onClick(() => this.confirmDeleteMemory(file)).id('health.memory.delete')
      }
    }.width('100%').alignItems(HorizontalAlign.Start).padding(Tokens.inset)
      .backgroundColor($r('sys.color.comp_background_tertiary')).borderRadius(Tokens.inset)
  }

  @Builder conversationSection() {
    Column({ space: Tokens.gap }) {
      Text(tr('health_chats')).fontSize(Tokens.body).fontWeight(FontWeight.Medium).width('100%')
      if (this.workspace.loading && !this.workspace.conversations.length) LoadingProgress().width(Tokens.touch).height(Tokens.touch)
      else if (!this.workspace.conversations.length) Text(tr(this.workspace.error ? 'health_chats_unavailable' : 'health_no_chats'))
        .fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).width('100%')
      ForEach(this.workspace.conversations, (row: ResourceRow) => {
        Row({ space: Tokens.gap }) {
          Button() {
            Column({ space: Tokens.fine }) {
              Text(row.title || tr('health_new_chat')).fontSize(Tokens.body).fontWeight(FontWeight.Medium).maxLines(1).width('100%')
              if (Wire.text(row.data(), 'last_text', row.detail)) Text(Wire.text(row.data(), 'last_text', row.detail)).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).maxLines(2).width('100%')
            }.alignItems(HorizontalAlign.Start).width('100%')
          }.layoutWeight(1).height(Tokens.touch).onClick(() => {
          if (this.workspace.isHealthConversation(row)) this.onChat(row);
          }).id('health.chat.' + row.id)
      Button(tr('health_archive')).height(Tokens.touch).enabled(!this.workspace.busy && this.workspace.canArchiveConversation(row))
            .onClick(() => this.confirmArchive(row)).id('health.chat.archive.' + row.id)
        }.width('100%').alignItems(VerticalAlign.Center)
      }, (row: ResourceRow) => row.key())
    }.width('100%').alignItems(HorizontalAlign.Start)
  }

  @Builder memorySection() {
    Column({ space: Tokens.gap }) {
      Text(tr('health_memory')).fontSize(Tokens.body).fontWeight(FontWeight.Medium).width('100%')
      if (this.workspace.loading && !this.workspace.memories.length) LoadingProgress().width(Tokens.touch).height(Tokens.touch)
      else if (!this.workspace.memoryEntries().length && !this.workspace.indexMemory()) Text(tr('health_no_memory'))
        .fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).width('100%')
      ForEach(this.workspace.memoryEntries(), (file: HealthMemoryFile) => Button(file.name).width('100%').height(Tokens.touch)
        .onClick(() => { this.editingMemory = false; this.workspace.openMemory(file); }).id('health.memory.' + file.name),
        (file: HealthMemoryFile) => file.name)
      if (this.workspace.indexMemory()) Button(tr('health_memory_index')).width('100%').height(Tokens.touch)
        .onClick(() => { this.editingMemory = false; const index = this.workspace.indexMemory(); if (index) this.workspace.openMemory(index); })
        .id('health.memory.index')
      if (this.workspace.selectedMemory) this.memoryDetail(this.workspace.selectedMemory)
    }.width('100%').alignItems(HorizontalAlign.Start)
  }

  @Builder reportsSection() {
    Column({ space: Tokens.gap }) {
      Text(tr('health_checkup_reports')).fontSize(Tokens.body).fontWeight(FontWeight.Medium).width('100%')
      if (this.workspace.loading && !this.workspace.reports.length) LoadingProgress().width(Tokens.touch).height(Tokens.touch)
      else if (!this.workspace.reports.length && !this.workspace.error) Text(tr('health_no_reports'))
        .fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).width('100%')
      ForEach(this.workspace.reports, (report: HealthReport) => {
        Column({ space: Tokens.fine }) {
          Text(report.title || report.name).fontSize(Tokens.body).fontWeight(FontWeight.Medium).maxLines(2).width('100%')
          Text(tr(report.recordPath ? 'health_report_summarized' : 'health_report_not_summarized')).fontSize(Tokens.meta)
            .fontColor($r('sys.color.font_secondary')).width('100%')
          Row({ space: Tokens.gap }) {
            if (report.recordPath) Button(tr('health_report_summary')).layoutWeight(1).height(Tokens.touch)
              .onClick(() => this.openReport(report, true)).id('health.report.summary.' + report.name)
            if (report.path) Button(tr('health_report_original')).layoutWeight(1).height(Tokens.touch)
              .onClick(() => this.openReport(report, false)).id('health.report.original.' + report.name)
            Button(tr('delete')).height(Tokens.touch).enabled(!this.workspace.busy)
              .onClick(() => this.confirmDeleteReport(report)).id('health.report.delete.' + report.name)
          }.width('100%')
        }.width('100%').alignItems(HorizontalAlign.Start).padding(Tokens.inset)
          .backgroundColor($r('sys.color.comp_background_tertiary')).borderRadius(Tokens.inset)
      }, (report: HealthReport) => report.row.key())
    }.width('100%').alignItems(HorizontalAlign.Start)
  }

  @Builder home() {
    Scroll() {
      Column({ space: Tokens.section }) {
        Column({ space: Tokens.gap }) {
          Text(tr('health_home_title')).fontSize(Tokens.title).fontWeight(FontWeight.Bold).width('100%')
          Text(tr('health_privacy_note')).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).width('100%')
          if (this.workspace.error) Text(tr(this.workspace.error)).fontSize(Tokens.meta).fontColor($r('app.color.persona_danger')).width('100%')
        }.alignItems(HorizontalAlign.Start).width('100%')
        Column({ space: Tokens.gap }) {
          Text(tr('health_try_asking')).fontSize(Tokens.body).fontWeight(FontWeight.Medium).width('100%')
          Button(tr('health_upload_report')).bindMenu(this.reportUploadMenu).width('100%').height(Tokens.touch)
            .enabled(!this.pickerBusy && this.workspace.canUploadReports()).id('health.report.upload')
          if (this.pickerBusy || this.workspace.reportUploadState === 'sending') Row({ space: Tokens.gap }) {
            LoadingProgress().width(Tokens.touch).height(Tokens.touch)
            Text(tr('health_report_uploading')).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary'))
          }.width('100%')
          if (this.pickerError) Text(tr(this.pickerError)).fontSize(Tokens.meta)
            .fontColor($r('app.color.persona_danger')).width('100%')
          ForEach(HEALTH_SUGGESTIONS, (key: string) => Button(tr(key)).width('100%').height(Tokens.touch)
            .enabled(this.workspace.canStartConversation() && !this.pickerBusy).onClick(() => this.send(tr(key))).id('health.suggestion.' + key), (key: string) => key)
        }.width('100%').alignItems(HorizontalAlign.Start)
        this.conversationSection()
        this.reportsSection()
        this.memorySection()
      }.padding(Tokens.page).width('100%').alignItems(HorizontalAlign.Start)
    }.scrollable(ScrollDirection.Vertical).scrollBar(BarState.Auto).layoutWeight(1).width('100%')
  }

  @Builder composer() {
    Row({ space: Tokens.gap }) {
      TextArea({ text: this.workspace.draft, placeholder: tr('health_ask_placeholder') }).height(Tokens.inputExpanded).layoutWeight(1)
        .onChange((value: string) => this.workspace.setDraft(value)).id('health.composer')
      Button(tr('send')).height(Tokens.touch).enabled(!!this.workspace.draft.trim() && this.workspace.canStartConversation() && !this.pickerBusy)
        .onClick(() => this.send(this.workspace.draft)).id('health.send')
    }.width('100%').padding({ left: Tokens.page, right: Tokens.page, top: Tokens.gap, bottom: Tokens.gap })
      .backgroundColor($r('app.color.persona_canvas'))
  }

  build() {
    Column() {
      this.home()
      this.composer()
    }.width('100%').height('100%')
  }
}
