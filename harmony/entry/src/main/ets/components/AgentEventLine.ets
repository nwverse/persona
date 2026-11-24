import { AgentEventDisplay } from '../core/AgentEvents';
import { conversationDisclosure } from '../design/ConversationMotion';
import { MarkdownText } from './MarkdownText';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';

@ComponentV2
export struct AgentEventLine {
  @Require @Param event: AgentEventDisplay;
  @Require @Param identity: string;
  @Param clock: string = '';
  @Param sourceMessage: boolean = false;
  @Event onLink: (address: string) => void = (_address: string): void => {};
  @Event hold: (change: () => void) => void = (change: () => void): void => change();
  @Local open: boolean = false;
  @Monitor('identity') identityChanged(): void { this.open = false; }
  @Computed get longBody(): boolean { return !!this.event.detailOnly || this.event.body.length > 140 || this.event.body.includes('\n'); }
  @Computed get ink(): ResourceColor { return this.event.tone === 'danger' ? Colors.danger : this.event.tone === 'attention' ? Colors.needs : Colors.secondary; }
  @Builder source() {
    Button({ type: ButtonType.Normal, buttonStyle: ButtonStyleMode.TEXTUAL }) {
      Row({ space: 6 }) {
        Text() { Span(this.event.label).fontColor(Colors.activity); Span('  ' + this.event.title).fontColor(Colors.ink) }
          .fontSize(15).lineHeight('22fp').fontWeight(FontWeight.Normal).maxLines(2)
          .textOverflow({ overflow: TextOverflow.Ellipsis }).flexShrink(1)
        if (this.event.body) SymbolGlyph(this.open ? Symbols.down : Symbols.expand).fontSize(10).fontColor([Colors.activity])
      }.width('100%').justifyContent(FlexAlign.Start)
    }.width('100%').constraintSize({ minHeight: Tokens.touch }).padding(0).backgroundColor(Color.Transparent)
      .enabled(!!this.event.body).accessibilityText([this.event.label, this.event.title].filter(Boolean).join(' · '))
      .accessibilityStateDescription(tr(this.open ? 'work_expanded' : 'work_collapsed'))
      .onClick(() => this.hold(() => { this.open = !this.open; }))
      .id('chat.source.' + this.identity)
    if (this.open && this.event.body) {
      Column() { MarkdownText({ transcript: true, text: this.event.body, onLink: this.onLink }) }
        .width('100%').alignItems(HorizontalAlign.Start).padding({ left: 12, bottom: 5 })
        .border({ width: { left: 1 }, color: Colors.divider }).transition(conversationDisclosure())
    }
  }
  @Builder heading() {
    Row({ space: Tokens.gap }) {
      Text() {
        Span(this.event.label).fontColor(this.ink)
        if (this.event.title) Span('  ' + this.event.title).fontColor(Colors.ink)
      }.fontSize(Tokens.meta).fontWeight(FontWeight.Medium).layoutWeight(1)
      if (this.open && this.clock) Text(this.clock).fontSize(Tokens.caption).fontFamily('monospace').fontColor(Colors.secondary)
      if (this.longBody) SymbolGlyph(this.open ? Symbols.down : Symbols.expand).fontSize(Tokens.caption).fontColor([Colors.secondary])
    }.width('100%')
  }
  build() {
    if (this.event.compaction) {
      Row({ space: Tokens.gap }) {
        Divider().width(14).strokeWidth(Tokens.hairline).color(Colors.divider)
        Text() {
          Span(this.event.label)
          if (this.event.meta) Span(' · ' + this.event.meta)
        }.fontSize(Tokens.toolLabel).lineHeight(Tokens.toolLeading).fontColor(Colors.activity).flexShrink(1)
        Divider().layoutWeight(1).strokeWidth(Tokens.hairline).color(Colors.divider)
      }.width('100%').padding({ top: Tokens.fine, bottom: Tokens.fine }).accessibilityGroup(true)
        .accessibilityText([this.event.label, this.event.meta].filter(Boolean).join(' · ')).id('chat.compaction.' + this.identity)
    } else if (this.sourceMessage) {
      Column() { this.source() }.width('100%').alignItems(HorizontalAlign.Start)
    } else {
      this.receipt()
    }
  }
  @Builder receipt() {
    Column({ space: Tokens.fine }) {
      if (this.longBody) {
        Button({ type: ButtonType.Normal, buttonStyle: ButtonStyleMode.TEXTUAL }) { this.heading() }
          .constraintSize({ minHeight: Tokens.touch }).width('100%').padding(0).backgroundColor(Color.Transparent)
          .accessibilityText(this.event.label + ' ' + this.event.title)
          .accessibilityStateDescription(tr(this.open ? 'work_expanded' : 'work_collapsed')).onClick(() => { this.open = !this.open; })
          .id('chat.event.detail.' + this.identity)
      } else {
        this.heading()
      }
      if (this.event.body && (!this.event.detailOnly || this.open)) Text(this.longBody && !this.open ? this.event.body.split('\n', 1)[0] : this.event.body)
        .fontSize(Tokens.meta).fontColor(Colors.ink)
        .maxLines(this.longBody && !this.open ? 2 : undefined).textOverflow({ overflow: TextOverflow.Ellipsis }).copyOption(CopyOptions.LocalDevice)
      if (this.event.meta) Text(this.event.meta).fontSize(Tokens.caption).fontColor(Colors.secondary)
    }.width('100%').alignItems(HorizontalAlign.Start).padding({ left: this.event.tone === 'normal' ? 0 : Tokens.inset, top: Tokens.fine, bottom: Tokens.fine })
      .border({ width: { left: this.event.tone === 'normal' ? 0 : Tokens.hairline }, color: this.ink })
  }
}
