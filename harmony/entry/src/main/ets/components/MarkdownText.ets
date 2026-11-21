import { Theme } from '@kit.ArkUI';
import { pasteboard } from '@kit.BasicServicesKit';
import { tr } from '../design/Strings';
import { IconActions } from '../design/IconActions';
import { MarkdownBlock, MarkdownInline, MarkdownTableRow, parseMarkdown } from '../core/Markdown';
import { Tokens } from '../design/Tokens';

@ComponentV2
export struct MarkdownText {
  @Require @Param text: string;
  @Param fitContent: boolean = false;
  @Param chatReply: boolean = false;
  @Event onLink: (url: string) => void = (_url: string) => {};
  @Local blocks: MarkdownBlock[] = [];
  @Local foreground: ResourceColor = $r('sys.color.font_primary');
  @Local secondary: ResourceColor = $r('sys.color.font_secondary');
  @Local emphasize: ResourceColor = $r('sys.color.font_emphasize');
  @Local codeFill: ResourceColor = $r('sys.color.comp_background_tertiary');
  onWillApplyTheme(theme: Theme): void {
    this.foreground = theme.colors.fontPrimary; this.secondary = theme.colors.fontSecondary;
    this.emphasize = theme.colors.fontEmphasize; this.codeFill = theme.colors.compBackgroundTertiary;
  }

  aboutToAppear(): void { this.blocks = parseMarkdown(this.text); }
  @Monitor('text') textChanged(): void { this.blocks = parseMarkdown(this.text); }

  private headingSize(level: number): number {
    if (this.chatReply) {
      if (level === 1) return Math.round(Tokens.body * 1.3);
      return level <= 3 ? Tokens.body : 15;
    }
    if (level <= 1) return Tokens.markdownHeading1;
    if (level === 2) return Tokens.markdownHeading2;
    if (level === 3) return Tokens.markdownHeading3;
    if (level === 4) return Tokens.markdownHeading4;
    if (level === 5) return Tokens.markdownHeading5;
    return Tokens.body;
  }

  private async copyCode(text: string): Promise<void> {
    try {
      await pasteboard.getSystemPasteboard().setData(pasteboard.createData(pasteboard.MIMETYPE_TEXT_PLAIN, text));
      this.getUIContext().getPromptAction().showToast({ message: tr('done') });
    } catch (_) {
      this.getUIContext().getPromptAction().showToast({ message: tr('failed') });
    }
  }

  private tableTextAlign(table: MarkdownBlock, column: number): TextAlign {
    const alignment = table.alignments[column] || 'start';
    if (alignment === 'center') return TextAlign.Center;
    if (alignment === 'end') return TextAlign.End;
    return TextAlign.Start;
  }

  @Builder inlineText(nodes: MarkdownInline[], size: number = Tokens.body, weight: FontWeight = FontWeight.Normal,
    alignment: TextAlign = TextAlign.Start, breakAll: boolean = false, quiet: boolean = false) {
    Text() {
      ForEach(nodes, (node: MarkdownInline) => this.inlineNode(node, weight, false, '', quiet),
        (node: MarkdownInline, index: number) => String(index))
    }
      .fontSize(size).fontColor(quiet ? this.secondary : this.foreground).fontWeight(weight)
      .lineHeight(this.chatReply ? String(Math.ceil(size * 1.45)) + 'fp' : undefined)
      .textAlign(alignment).wordBreak(breakAll ? WordBreak.BREAK_ALL : WordBreak.BREAK_WORD)
      .copyOption(CopyOptions.LocalDevice).width(this.fitContent ? undefined : '100%')
  }

  @Builder inlineNode(node: MarkdownInline, weight: FontWeight, italic: boolean, url: string, quiet: boolean) {
    if (node.kind === 'bold' || node.kind === 'italic' || node.kind === 'link') {
      ForEach(node.children, (child: MarkdownInline) => this.inlineNode(child,
        node.kind === 'bold' ? (this.chatReply ? FontWeight.Medium : FontWeight.Bold) : weight,
        italic || node.kind === 'italic', node.kind === 'link' ? node.url : url, quiet),
        (child: MarkdownInline, index: number) => String(index))
    } else if (url) {
      Span(node.text).fontFamily(node.kind === 'code' ? 'monospace' : undefined)
        .fontSize(node.kind === 'code' ? (this.chatReply ? 16 : Tokens.meta) : undefined)
        .fontWeight(weight).fontStyle(italic ? FontStyle.Italic : FontStyle.Normal).fontColor(this.emphasize)
        .textBackgroundStyle({ color: node.kind === 'code' && !this.chatReply ? this.codeFill : Color.Transparent, radius: Tokens.fine })
        .decoration({ type: TextDecorationType.Underline, color: this.emphasize }).onClick(() => this.onLink(url))
    } else if (node.kind === 'code') {
      Span(node.text).fontFamily('monospace').fontSize(this.chatReply ? 16 : Tokens.meta)
        .fontWeight(weight).fontStyle(italic ? FontStyle.Italic : FontStyle.Normal)
        .fontColor(this.chatReply ? this.foreground : this.secondary)
        .textBackgroundStyle({ color: this.chatReply ? Color.Transparent : this.codeFill, radius: Tokens.fine })
    } else {
      Span(node.text).fontColor(quiet ? this.secondary : this.foreground).fontWeight(weight)
        .fontStyle(italic ? FontStyle.Italic : FontStyle.Normal)
    }
  }

  @Builder blockView(block: MarkdownBlock, first: boolean = false) {
    if (block.kind === 'paragraph') {
      this.inlineText(block.inlines)
    } else if (block.kind === 'heading') {
      Column() {
        this.inlineText(block.inlines, this.headingSize(block.level),
          this.chatReply && block.level > 1 ? FontWeight.Medium : FontWeight.Bold,
          TextAlign.Start, false, this.chatReply && block.level >= 4)
      }.alignItems(HorizontalAlign.Start).width(this.fitContent ? undefined : '100%')
        .padding({ top: this.chatReply && !first ? (block.level === 1 ? 8 : 4) : 0 })
    } else if (block.kind === 'code') {
      Column({ space: Tokens.gap }) {
        if (this.chatReply) {
          Row() {
            if (block.language) Text(block.language).fontFamily('monospace').fontSize(13).fontColor(this.secondary)
              .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
            Blank().layoutWeight(1)
            Button({ type: ButtonType.Circle }) {
              SymbolGlyph(IconActions.symbol('copy')).fontSize(16).fontColor([this.secondary])
            }.width(34).height(34).padding(0).backgroundColor(Color.Transparent)
              .responseRegion({ x: -5, y: -5, width: 44, height: 44 })
              .accessibilityText(tr('copy') + (block.language ? ' ' + block.language : ''))
              .bindTips(tr('copy')).onClick(() => this.copyCode(block.text))
          }.width('100%')
        } else if (block.language) Text(block.language).fontSize(Tokens.meta).fontColor(this.secondary)
        Scroll() {
          Text(block.text).fontFamily('monospace').fontSize(Tokens.meta).lineHeight(this.chatReply ? '21fp' : undefined).wordBreak(WordBreak.BREAK_ALL)
            .copyOption(CopyOptions.LocalDevice).fontColor(this.foreground)
        }.scrollable(ScrollDirection.Horizontal).align(Alignment.TopStart).scrollBar(BarState.Auto).width('100%')
      }
        .padding(Tokens.inset).width('100%').alignItems(HorizontalAlign.Start)
        .backgroundColor(this.codeFill)
        .border({ width: this.chatReply ? 0 : Tokens.hairline, color: this.secondary, radius: this.chatReply ? Tokens.inset : Tokens.fine })
        .borderRadius(this.chatReply ? Tokens.inset : Tokens.fine)
    } else if (block.kind === 'raw') {
      Text(block.text).fontSize(Tokens.body).fontColor(this.foreground).wordBreak(WordBreak.BREAK_WORD)
        .copyOption(CopyOptions.LocalDevice).width('100%')
    } else if (block.kind === 'rule') {
      Divider().strokeWidth(Tokens.hairline).color(this.secondary).width('100%')
        .margin({ top: Tokens.gap, bottom: Tokens.gap })
    } else if (block.kind === 'quote') {
      Column({ space: Tokens.gap }) {
        ForEach(block.children, (child: MarkdownBlock) => this.blockView(child),
          (child: MarkdownBlock, index: number) => 'quote.' + String(index))
      }
        .padding(Tokens.inset).width('100%').alignItems(HorizontalAlign.Start)
        .backgroundColor(this.codeFill)
        .border({ width: Tokens.hairline, color: this.emphasize, radius: Tokens.fine })
    } else if (block.kind === 'list') {
      Column({ space: Tokens.gap }) {
        ForEach(block.items, (item: MarkdownBlock) => this.listItem(item),
          (item: MarkdownBlock, index: number) => 'list.' + String(index))
      }.width('100%').alignItems(HorizontalAlign.Start)
    } else if (block.kind === 'table') {
      Scroll() {
        Column({ space: Tokens.fine }) {
          ForEach(block.rows, (row: MarkdownTableRow) => this.tableRow(row, block),
            (row: MarkdownTableRow, index: number) => 'row.' + String(index))
        }
      }
        .scrollable(ScrollDirection.Horizontal).align(Alignment.TopStart).scrollBar(BarState.Auto).width('100%')
    }
  }

  @Builder listItem(item: MarkdownBlock) {
    Row({ space: Tokens.inset }) {
      Text(item.marker).fontSize(Tokens.body).fontColor(this.secondary)
      Column({ space: Tokens.gap }) {
        ForEach(item.children, (child: MarkdownBlock) => this.blockView(child),
          (child: MarkdownBlock, index: number) => 'item.' + String(index))
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
    }.width('100%').alignItems(VerticalAlign.Top)
  }

  @Builder tableRow(row: MarkdownTableRow, table: MarkdownBlock) {
    Row() {
      ForEach(row.cells, (cell: MarkdownInline[], index: number) => this.tableCell(cell, table, index, row.header),
        (cell: MarkdownInline[], index: number) => 'cell.' + String(index))
    }.alignItems(VerticalAlign.Top)
  }

  @Builder tableCell(cell: MarkdownInline[], table: MarkdownBlock, index: number, header: boolean) {
    Column() {
      this.inlineText(cell, Tokens.body, header ? FontWeight.Bold : FontWeight.Normal, this.tableTextAlign(table, index), true)
    }
      .width(Tokens.markdownTableCell).padding(Tokens.inset).alignItems(HorizontalAlign.Start)
      .border({ width: Tokens.hairline, color: this.secondary, radius: Tokens.fine })
      .backgroundColor(header ? this.codeFill : Color.Transparent)
  }

  build() {
    Column({ space: Tokens.gap }) {
      ForEach(this.blocks, (block: MarkdownBlock, index: number) => this.blockView(block, index === 0),
        (block: MarkdownBlock, index: number) => 'block.' + String(index) + ':' + JSON.stringify(block))
    }.width(this.fitContent ? undefined : '100%').alignItems(HorizontalAlign.Start)
  }
}
