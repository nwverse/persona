import { pasteboard } from '@kit.BasicServicesKit';
import { CodeInk, CodeRun, chatCodeLanguageLabel, chatCodeRuns } from '../core/ChatCode';
import { workExcerpt } from '../core/WorkPresentation';
import { Colors } from '../design/Colors';
import { IconActions } from '../design/IconActions';
import { tr } from '../design/Strings';
import { Tokens } from '../design/Tokens';

@ComponentV2
export struct ConversationCodeText {
  @Require @Param code: string;
  @Event onBodyTap?: () => void;
  @Param language: string = '';
  @Param wrap: boolean = true;
  @Param compact: boolean = false;
  @Computed get runs(): CodeRun[] { return chatCodeRuns(this.code, this.language); }
  private ink(kind: CodeInk): ResourceColor {
    if (kind === 'keyword') return Colors.codeKeyword;
    if (kind === 'string') return Colors.codeString;
    if (kind === 'literal') return Colors.codeLiteral;
    if (kind === 'comment') return Colors.secondary;
    if (kind === 'added') return Colors.positive;
    if (kind === 'removed') return Colors.danger;
    return Colors.ink;
  }
  build() {
    Text() {
      if (this.runs.length === 1) {
        Span(this.runs[0].text).fontColor(this.ink(this.runs[0].ink))
      } else {
        ForEach(this.runs, (run: CodeRun) => { Span(run.text).fontColor(this.ink(run.ink)) },
          (run: CodeRun) => String(run.offset) + ':' + run.ink + ':' + run.text)
      }
    }.fontFamily('monospace').fontSize(this.compact ? Tokens.toolLabel : Tokens.meta).lineHeight(Tokens.codeLeading).fontColor(Colors.ink)
      .wordBreak(this.wrap && !this.compact ? WordBreak.BREAK_ALL : WordBreak.BREAK_WORD).copyOption(CopyOptions.LocalDevice)
      .width(this.wrap ? '100%' : undefined).onClick(this.onBodyTap)
  }
}

@ComponentV2
export struct ConversationCode {
  @Require @Param code: string;
  @Event onBodyTap?: () => void;
  @Param language: string = '';
  @Param caption: string = '';
  @Param preview: boolean = false;
  @Param embedded: boolean = false;
  @Event hold: (change: () => void) => void = (change: () => void): void => change();
  @Local wrap: boolean = false;
  @Local full: boolean = false;
  @Computed get prefix(): string { return this.preview ? workExcerpt(this.code, false) : this.code; }
  private async copyCode(): Promise<void> {
    try {
      await pasteboard.getSystemPasteboard().setData(pasteboard.createData(pasteboard.MIMETYPE_TEXT_PLAIN, this.code));
      this.getUIContext().getPromptAction().showToast({ message: tr('ev_code_copied') });
    } catch (_) { this.getUIContext().getPromptAction().showToast({ message: tr('ev_code_copy_failed') }); }
  }
  @Builder body() {
    ConversationCodeText({ code: this.full ? this.code : this.prefix, language: this.language, wrap: this.wrap, onBodyTap:this.onBodyTap })
      .padding({ left: Tokens.inset, right: Tokens.inset, bottom: Tokens.inset })
  }
  build() {
    Column() {
      Row({ space: Tokens.fine }) {
        Text(this.caption || chatCodeLanguageLabel(this.language)).fontFamily(this.caption ? 'sans-serif' : 'monospace').fontSize(Tokens.caption).fontWeight(FontWeight.Medium).fontColor(Colors.secondary)
          .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis }).layoutWeight(1)
        Button({ type: ButtonType.Normal, buttonStyle: ButtonStyleMode.TEXTUAL }) {
          SymbolGlyph($r('sys.symbol.return_down_left')).fontSize(16).fontColor([this.wrap ? Colors.link : Colors.secondary])
        }.width(44).height(44).padding(0).backgroundColor(Color.Transparent).accessibilityText(tr(this.wrap ? 'ev_no_wrap' : 'ev_wrap'))
          .bindTips(tr(this.wrap ? 'ev_no_wrap' : 'ev_wrap')).onClick(() => this.hold(() => { this.wrap = !this.wrap; }))
        Button({ type: ButtonType.Normal, buttonStyle: ButtonStyleMode.TEXTUAL }) {
          SymbolGlyph(IconActions.symbol('copy')).fontSize(16).fontColor([Colors.secondary])
        }.width(44).height(44).padding(0).backgroundColor(Color.Transparent)
          .accessibilityText(tr('ev_copy_code')).bindTips(tr('ev_copy_code')).onClick(() => this.copyCode())
      }.width('100%').padding({ left: Tokens.inset, right: Tokens.fine })
      if (this.wrap) { this.body() }
      else {
        Scroll() { this.body() }.scrollable(ScrollDirection.Horizontal).scrollBar(BarState.Auto).align(Alignment.TopStart).width('100%')
      }
      if (this.prefix !== this.code) Button(tr(this.full ? 'ev_less' : 'ev_more'), { buttonStyle: ButtonStyleMode.TEXTUAL })
        .fontSize(Tokens.meta).constraintSize({ minHeight: Tokens.touch }).onClick(() => this.hold(() => { this.full = !this.full; }))
    }.width('100%').alignItems(HorizontalAlign.Start).backgroundColor(Colors.grouped)
      .border({ width: this.embedded ? 0 : Tokens.hairline, color: Colors.divider, radius: Tokens.gap }).borderRadius(this.embedded ? 0 : Tokens.gap).clip(true)
  }
}
