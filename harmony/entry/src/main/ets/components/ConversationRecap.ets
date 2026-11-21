import { ClientModel, ClientViewLease } from '../models/ClientModel';
import { ChatMessage, ConversationRecapItem } from '../models/Wire';
import { Colors } from '../design/Colors';
import { IconActions } from '../design/IconActions';
import { Symbols } from '../design/Symbols';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import { markdownVisibleText, parseMarkdown } from '../core/Markdown';
import { MarkdownText } from './MarkdownText';

@ComponentV2
export struct ConversationRecap {
  @Require @Param model: ClientModel;
  @Require @Param message: ChatMessage;
  @Param active: boolean = true;
  @Event onLink: (address: string) => void = () => {};
  @Local closed: string = '';
  @Local ready: boolean = false;
  @Local shown: boolean = false;
  @Local sheetItems: ConversationRecapItem[] = [];
  private ownerKey: string = '';
  private lease: ClientViewLease | undefined = undefined;

  aboutToAppear(): void { this.syncOwner(); }
  aboutToDisappear(): void { this.lease = undefined; this.ownerKey = ''; this.shown = false; }
  @Monitor('active', 'message', 'model.selected', 'model.profile', 'model.connected') ownerChanged(): void { this.syncOwner(); }
  @Monitor('model.draft') draftChanged(): void {
    if (this.model.draft.length && this.open()) this.dismiss();
  }
  private current(): boolean { return this.active && this.message.ownerTarget === this.model.target(); }
  private newest(): ConversationRecapItem { return this.message.recaps[this.message.recaps.length - 1]; }
  private syncOwner(): void {
    const current = this.current();
    const key = current ? JSON.stringify([this.model.scope(), this.model.connectionGeneration(), this.message.ownerTarget,
      this.message.terminalRecap, this.message.id]) : '';
    if (this.ownerKey === key) return;
    this.ownerKey = key; this.lease = undefined; this.closed = ''; this.ready = false; this.shown = false; this.sheetItems = [];
    if (!current || !this.message.terminalRecap || this.model.selected.kind === 'conversation') return;
    const lease = this.model.captureView('closed-recap:' + this.message.ownerTarget);
    this.lease = lease;
    lease.read().then((id: string) => {
      if (this.lease === lease && this.current() && lease.valid()) { this.closed = id; this.ready = true; }
    }).catch(() => { if (this.lease === lease && this.current() && lease.valid()) this.ready = true; });
  }
  private open(): boolean {
    return this.current() && this.ready && this.message.terminalRecap && this.message.recaps.length > 0 &&
      this.model.selected.kind !== 'conversation' && this.closed !== this.newest().id;
  }
  private dismiss(): void {
    const lease = this.lease;
    if (!this.open() || !lease?.valid()) return;
    const id = this.newest().id;
    this.closed = id;
    lease.write(id).catch(() => {
      if (this.lease === lease && this.current()) this.getUIContext().getPromptAction().showToast({ message: tr('failed') });
    });
  }
  private showItems(items: ConversationRecapItem[]): void {
    if (!this.current()) return;
    this.sheetItems = items.slice().reverse(); this.shown = true;
  }
  private relative(at: number): string {
    if (!at || !Number.isFinite(at)) return '';
    const minutes = Math.floor((Date.now() - at) / 60000);
    if (minutes < 0) return '';
    if (!minutes) return tr('agent_time_just_now');
    if (minutes < 60) return String(minutes) + tr('agent_time_minute_suffix');
    if (minutes < 1440) return String(Math.floor(minutes / 60)) + tr('agent_time_hour_suffix');
    return String(Math.floor(minutes / 1440)) + tr('agent_time_day_suffix');
  }
  @Builder marker(items: ConversationRecapItem[]) {
    Button({ type: ButtonType.Normal }) {
      Row({ space: 6 }) {
        SymbolGlyph(IconActions.symbol('restore_draft')).fontSize(15).fontColor([Colors.link])
        Text(tr('chat_away_recap')).fontSize(14).fontColor(Colors.ink).maxLines(1)
        if (items.length > 1) Text('· ' + String(items.length) + '×').fontSize(13).fontColor(Colors.secondary)
        if (this.relative(items[items.length - 1].at)) Text('· ' + this.relative(items[items.length - 1].at))
          .fontSize(13).fontColor(Colors.secondary).maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
        SymbolGlyph(Symbols.expand).fontSize(12).fontColor([Colors.secondary])
      }.constraintSize({ minHeight: 36 })
    }.backgroundColor(Colors.control).borderRadius(12).padding({left: 10, right: 10})
      .accessibilityText(tr('chat_away_recap') + ' · ' + tr('expand'))
      .id('chat.recap.history').onClick(() => this.showItems(items))
  }
  @Builder strip(item: ConversationRecapItem) {
    Button({ type: ButtonType.Normal }) {
      Column({ space: 4 }) {
        Row({ space: 6 }) {
          SymbolGlyph(IconActions.symbol('restore_draft')).fontSize(15).fontColor([Colors.link])
          Text(tr('chat_away_recap')).fontSize(14).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
          if (this.relative(item.at)) Text('· ' + this.relative(item.at)).fontSize(13).fontColor(Colors.secondary)
            .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis }).layoutWeight(1)
          else Blank().layoutWeight(1)
          SymbolGlyph($r('sys.symbol.xmark')).fontSize(13).fontColor([Colors.secondary])
        }.width('100%')
        Text(markdownVisibleText(parseMarkdown(item.text))).fontSize(16).fontColor(Colors.ink).lineHeight('22fp')
          .maxLines(8).textOverflow({ overflow: TextOverflow.Ellipsis }).width('100%').textAlign(TextAlign.Start)
      }.width('100%').alignItems(HorizontalAlign.Start).padding({left: 10})
        .border({ width: {left: 2}, color: Colors.link })
    }.width('100%').padding(12).backgroundColor(Colors.selection).borderRadius(12)
      .accessibilityText(tr('chat_away_recap') + ' · ' + item.text + ' · ' + tr('close'))
      .id('chat.recap.open').onClick(() => this.dismiss())
  }
  @Builder details() {
    Scroll() {
      Column({ space: 16 }) {
        ForEach(this.sheetItems, (item: ConversationRecapItem, index: number) => {
          if (index > 0) Divider().strokeWidth(Tokens.divider).color(Colors.divider)
          if (this.relative(item.at)) Text(this.relative(item.at)).fontSize(Tokens.caption).fontColor(Colors.secondary)
          MarkdownText({ text: item.text, chatReply: true, onLink: (url: string) => { if (this.current()) this.onLink(url); } })
        }, (item: ConversationRecapItem) => item.id)
      }.width('100%').alignItems(HorizontalAlign.Start).padding(Tokens.page)
    }.width('100%').height('100%').align(Alignment.TopStart).id('chat.recap.detail')
  }
  build() {
    Column({ space: Tokens.gap }) {
      if (this.message.recaps.length) {
        if (this.open()) {
          if (this.message.recaps.length > 1) this.marker(this.message.recaps.slice(0, -1))
          this.strip(this.newest())
        } else {
          this.marker(this.message.recaps)
        }
      }
    }.width('100%').alignItems(HorizontalAlign.Start).padding({left: Tokens.page, right: Tokens.page})
      .bindSheet($$this.shown, this.details(), { title: { title: tr('chat_away_recap') },
        height: SheetSize.MEDIUM, detents: [SheetSize.MEDIUM, SheetSize.LARGE], showClose: true })
  }
}
