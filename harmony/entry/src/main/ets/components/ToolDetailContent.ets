import type { WorkDetail } from '../core/WorkPresentation';
import { diffDetailLines, searchDetailLines, sourceDetailLines, toolDetailPage, ToolDetailLine } from '../core/ToolDetailLines';
import { ConversationCodeText } from './ConversationCode';
import { Colors } from '../design/Colors';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';

@ComponentV2
export struct ToolDetailContent {
  @Require @Param field: WorkDetail;
  @Param fileResult: boolean = false;
  @Param fileSearch: boolean = false;
  @Param failure: boolean = false;
  @Param firstLine: number | undefined = undefined;
  @Param file: string = '';
  @Param caption: string = '';
  @Param wrap: boolean = true;
  @Local pages: number = 1;
  @Computed get value(): string { return toolDetailPage(this.field.text, this.pages); }
  @Computed get command(): boolean { return this.field.key === 'ev_command'; }
  @Computed get json(): boolean { return this.field.key === 'work_arguments' && /^[\[{]/.test(this.value.trimStart()); }
  @Computed get source(): boolean { return this.fileResult && this.field.key === 'work_output'; }
  @Computed get language(): string {
    if (this.command) return 'sh';
    if (this.json) return 'json';
    if (this.source) return /\.([\w+]+)$/.exec(this.file)?.[1] || '';
    return this.field.language || '';
  }
  @Computed get rows(): ToolDetailLine[] {
    if (this.field.style === 'diff') return diffDetailLines(this.value);
    if (this.source) return sourceDetailLines(this.value, this.firstLine);
    if (this.fileSearch && this.field.key === 'work_output') return searchDetailLines(this.value);
    return [];
  }
  @Computed get numbers(): boolean { return this.rows.some((line: ToolDetailLine) => !!line.number); }

  @Builder line(line: ToolDetailLine) {
    if (line.kind === 'file' || line.kind === 'hunk') {
      Text(line.text).fontSize(Tokens.toolMeta).fontFamily('monospace').fontColor(line.kind === 'hunk' ? Colors.codeKeyword : Colors.ink)
        .fontWeight(line.kind === 'file' ? FontWeight.Medium : FontWeight.Normal).lineHeight('19fp')
        .wordBreak(WordBreak.BREAK_ALL).copyOption(CopyOptions.LocalDevice)
        .width(this.wrap ? '100%' : undefined).padding({ left: 10, right: 10, top: 5, bottom: 5 })
        .backgroundColor(Colors.grouped)
    } else {
      Row({ space: 6 }) {
        if (this.numbers) Text(line.number).fontFamily('monospace').fontSize(Tokens.toolMeta).fontColor(Colors.activity)
          .lineHeight('20fp').constraintSize({ minWidth: 26 }).textAlign(TextAlign.End)
        if (this.field.style === 'diff') Text(line.kind === 'add' ? '+' : line.kind === 'remove' ? '−' : '')
          .width(10).fontFamily('monospace').fontSize(Tokens.toolLabel).lineHeight('20fp')
          .fontColor(line.kind === 'add' ? Colors.positive : Colors.danger)
        if (this.wrap) {
          ConversationCodeText({ code: line.text || ' ', language: this.language, compact: true }).layoutWeight(1)
        } else {
          ConversationCodeText({ code: line.text || ' ', language: this.language, compact: true, wrap: false })
        }
      }.alignItems(VerticalAlign.Top).width(this.wrap ? '100%' : undefined).padding({ left: 7, right: 8, top: 1, bottom: 1 })
        .backgroundColor(line.kind === 'add' ? Colors.diffAddedBackground : line.kind === 'remove' ? Colors.diffRemovedBackground : Color.Transparent)
    }
  }
  @Builder contents() {
    if (this.rows.length) {
      Column() {
        ForEach(this.rows, (line: ToolDetailLine) => { this.line(line) },
          (line: ToolDetailLine, i: number) => JSON.stringify([i, line.kind, line.number, line.text]))
      }.width(this.wrap ? '100%' : undefined).alignItems(HorizontalAlign.Start)
    } else if (this.command) {
      Row({ space: 7 }) {
        Text('$').fontSize(Tokens.toolLabel).fontFamily('monospace').lineHeight('20fp').fontColor(Colors.activity)
        if (this.wrap) ConversationCodeText({ code: this.value, language: 'sh', compact: true }).layoutWeight(1)
        else ConversationCodeText({ code: this.value, language: 'sh', compact: true, wrap: false })
      }.width(this.wrap ? '100%' : undefined).alignItems(VerticalAlign.Top).padding(10)
    } else if (this.field.style === 'text') {
      Text(this.value).fontSize(Tokens.meta).lineHeight('21fp').fontColor(Colors.ink).wordBreak(WordBreak.BREAK_WORD)
        .width('100%').copyOption(CopyOptions.LocalDevice)
    } else {
      ConversationCodeText({ code: this.value, language: this.language, compact: true, wrap: this.wrap })
        .padding(this.field.key === 'work_arguments' ? 10 : 0)
    }
  }
  build() {
    Column({ space: 7 }) {
      if (this.caption) Text(this.caption).fontSize(Tokens.toolMeta).fontWeight(FontWeight.Medium)
        .fontColor(this.failure && this.field.key === 'work_output' ? Colors.danger : Colors.secondary)
      Column() {
        if (this.wrap || this.field.style === 'text') { this.contents() }
        else { Scroll() { this.contents() }.width('100%').scrollable(ScrollDirection.Horizontal).scrollBar(BarState.Auto).align(Alignment.TopStart) }
      }.width('100%').alignItems(HorizontalAlign.Start)
        .backgroundColor(this.command || this.field.key === 'work_arguments' ? Colors.grouped : Colors.canvas)
        .border({ width: this.rows.length ? 0.5 : 0, color: Colors.divider, radius: 7 }).borderRadius(7).clip(true)
      if (this.value.length < this.field.text.length) Button(tr('tool_inspect_more'), { buttonStyle: ButtonStyleMode.TEXTUAL })
        .fontSize(Tokens.toolLabel).fontColor(Colors.link).constraintSize({ minHeight: 36 }).onClick(() => { this.pages++; })
    }.width('100%').alignItems(HorizontalAlign.Start)
  }
}
