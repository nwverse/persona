import { LengthMetrics, Theme } from '@kit.ArkUI';
import { pasteboard } from '@kit.BasicServicesKit';
import { tr } from '../design/Strings';
import { IconActions } from '../design/IconActions';
import { ClientModel } from '../models/ClientModel';
import { TranscriptImage } from './TranscriptImage';
import { conversationImageKey, messageImageGalleryWindow } from '../core/MessageImageGallery';
import type { ConversationPhotoRequest, LoadedMessageImage, MessageImageGalleryEntry } from '../core/MessageImageGallery';
import { MarkdownBlock, MarkdownInline, MarkdownProseRun, MarkdownTableRow, agentReplyBodySize, hasHanProse, markdownProseRuns, parseMarkdown, recapGlyphSize, markdownProseText, markdownCodeSelection, ConversationMarkdownCache, chatImagePath, markdownLinkProvider, markdownMediaRuns, MarkdownMediaRun, markdownImagePaths, chatFilePath, markdownInlineText, markdownVisibleText, markdownTableWidths } from '../core/Markdown';
import { Tokens } from '../design/Tokens';
import { ConversationCode } from './ConversationCode';
import { Colors } from '../design/Colors';
import { BoundRow, reconcileBoundRows } from '../models/RowBinding';
import { nativeEnvironment, NativeEnvironment } from '../platform/NativeEnvironment';

function referenceMark(url: string): Resource | undefined {
  switch (markdownLinkProvider(url)) {
    case 'github': return $r('app.media.reference_github');
    case 'figma': return $r('app.media.reference_figma');
    case 'notion': return $r('app.media.reference_notion');
    case 'linear': return $r('app.media.reference_linear');
    case 'googledrive': return $r('app.media.reference_googledrive');
    default: return undefined;
  }
}

@ComponentV2
struct MarkdownTable {
  @Require @Param table: MarkdownBlock;
  @BuilderParam cellText: (nodes: MarkdownInline[], header: boolean, alignment: TextAlign) => void;
  @Param environment: NativeEnvironment = nativeEnvironment;
  @Local viewport: number = 0;

  @Computed get measured(): number[] {
    const scale = this.environment.fontScale;
    const measured = this.table.alignments.map(() => 0);
    const ui = this.getUIContext();
    for (const row of this.table.rows.slice(0, 48)) {
      row.cells.forEach((cell: MarkdownInline[], column: number) => {
        for (const line of markdownInlineText(cell).slice(0, 64).split('\n')) {
          const px = ui.getMeasureUtils().measureText({ textContent: line, fontSize: '15fp',
            fontWeight: row.header ? FontWeight.Medium : FontWeight.Normal });
          measured[column] = Math.max(measured[column], ui.px2vp(px));
        }
      });
    }
    return measured.map((n: number) => Math.min(n, 320 * scale));
  }
  @Computed get widths(): number[] {
    return markdownTableWidths(this.measured, Math.max(0, this.viewport - 2 * Tokens.hairline), this.environment.fontScale);
  }
  @Computed get total(): number { return this.widths.reduce((a: number, b: number) => a + b, 0); }
  private alignment(index: number): TextAlign {
    const a = this.table.alignments[index];
    return a === 'center' ? TextAlign.Center : a === 'end' ? TextAlign.End : TextAlign.Start;
  }
  build() {
    Scroll() {
      Column() {
        ForEach(this.table.rows, (row: MarkdownTableRow, rowIndex: number) => {
          if (rowIndex > 0) Divider().strokeWidth(Tokens.hairline).color(Colors.divider)
          Flex({ direction: FlexDirection.Row, alignItems: ItemAlign.Stretch }) {
            ForEach(row.cells, (cell: MarkdownInline[], col: number) => {
              Column() { this.cellText(cell, row.header, this.alignment(col)) }
                .width(this.widths[col]).flexShrink(0).alignItems(HorizontalAlign.Start)
                .padding({ left: col === 0 ? 0 : 9, right: col === this.widths.length - 1 ? 0 : 9,
                  top: Tokens.gap, bottom: Tokens.gap })
                .id('markdown.table.cell.' + rowIndex + '.' + col)
            }, (_cell: MarkdownInline[], col: number) => String(col))
          }.width(this.total)
        }, (row: MarkdownTableRow, index: number) => String(index) + ':' + JSON.stringify(row.cells))
      }.width(this.total).alignItems(HorizontalAlign.Start)
    }.width('100%').align(Alignment.TopStart).scrollable(ScrollDirection.Horizontal)
      .scrollBar(this.total > this.viewport + 1 ? BarState.Auto : BarState.Off).clip(true)
      .onAreaChange((_old: Area, area: Area) => {
        const width = Number(area.width);
        if (width > 0 && Math.abs(width - this.viewport) > 0.5) this.viewport = width;
      }).id('markdown.table')
  }
}

@ComponentV2
struct UserPassage {
  @Require @Param preview: string;
  @Require @Param limit: number;
  @Require @Param overflow: boolean;
  @Require @Param ink: ResourceColor;
  @Require @Param actionInk: ResourceColor;
  @Require @Param proseSize: number;
  @Require @Param identity: string;
  @BuilderParam full: () => void;
  @Event hold: (change: () => void) => void = change => change();
  @Event onReveal: () => void = () => {};
  @Event onBodyTap?: () => void;
  @Local open: boolean = false;
  @Monitor('identity') reset(): void { this.open = false; }

  build() {
    Column({ space: 3 }) {
      if (this.overflow && !this.open) {
        Text(this.preview).width('100%').fontSize(this.proseSize).fontColor(this.ink)
          .lineHeight(String(Math.round(this.proseSize * Tokens.replyBodyLeading)) + 'fp').wordBreak(WordBreak.BREAK_WORD)
          .maxLines(this.limit).textOverflow({overflow:TextOverflow.Ellipsis}).copyOption(CopyOptions.LocalDevice).onClick(this.onBodyTap)
      } else { this.full() }
      if (this.overflow) {
        Button({type:ButtonType.Normal,buttonStyle:ButtonStyleMode.TEXTUAL}) {
          Row({space:4}) {
            Text(tr(this.open ? 'user_passage_less' : 'user_passage_more')).fontSize(14).fontWeight(FontWeight.Medium).fontColor(this.actionInk)
            SymbolGlyph($r('sys.symbol.chevron_down')).fontSize(12).fontColor([this.actionInk]).rotate({angle:this.open ? 180 : 0})
          }
        }.height(34).padding(0).backgroundColor(Color.Transparent)
          .accessibilityText(tr(this.open ? 'user_passage_less' : 'user_passage_more'))
          .accessibilityStateDescription(tr(this.open ? 'work_expanded' : 'work_collapsed'))
          .onClick(() => this.hold(() => { this.open = !this.open; if (this.open) this.onReveal(); })).id('chat.passage.toggle.' + this.identity)
      }
    }.width('100%').alignItems(HorizontalAlign.Start)
  }
}

@ComponentV2
export struct MarkdownText {
  @Require @Param text: string;
  @Param fitContent: boolean = false;
  @Param nativeTextSelection: boolean = true;
  @Param fixedType: boolean = false;
  @Param agentPrompt: boolean = false;
  @Param chatReply: boolean = false;
  @Param agentReply: boolean = false;
  @Param agentFontScale: number = 1;
  @Param recapReading: boolean = false;
  @Param profileReading: boolean = false;
  @Param dockPlan: boolean = false;
  @Param paragraphLineSpacingEm: number = 0.26;
  @Param compact: boolean = false;
  @Param ownMessage: boolean = false;
  @Param narration: boolean = false;
  @Event onBodyTap?: () => void;
  @Param messageInk: ResourceColor | undefined = undefined;
  @Param messageQuoteInk: ResourceColor | undefined = undefined;
  @Param ownWidth: number = 0;
  @Param quoteInk: boolean = false;
  @Param messageId: string = '';
  @Param clipped: boolean = false;
  @Event readOriginal: () => Promise<string> = async (): Promise<string> => '';
  @Local original: string = '';
  @Local pending: boolean = false;
  @Local unavailable: boolean = false;
  private readSequence: number = 0;
  @Event hold: (change: () => void) => void = change => change();
  @Param transcript: boolean = false;
  @Param documents: ConversationMarkdownCache | undefined = undefined;
  @Param workspaceRoot: string = '';
  @Param mediaModel: ClientModel | undefined = undefined;
  @Param mediaTarget: string = '';
  @Param mediaWidth: number = 0;
  @Event onEditImage: (image: LoadedMessageImage) => void = () => {};
  @Param mediaActive: boolean = false;
  @Event onPreview: (request: ConversationPhotoRequest) => void = () => {};
  @Event onOpenFile: (path: string) => void = (_path: string) => {};
  @Event onLink: (url: string) => void = (_url: string) => {};
  @Event onSelectionChange: (selected: boolean) => void = (_selected: boolean) => {};
  @Local blocks: BoundRow<MarkdownBlock>[] = [];
  @Local foreground: ResourceColor = $r('sys.color.font_primary');
  @Local secondary: ResourceColor = $r('sys.color.font_secondary');
  @Local emphasize: ResourceColor = $r('sys.color.font_emphasize');
  @Local codeFill: ResourceColor = $r('sys.color.comp_background_tertiary');
  onWillApplyTheme(theme: Theme): void {
    this.foreground = theme.colors.fontPrimary; this.secondary = theme.colors.fontSecondary;
    this.emphasize = theme.colors.fontEmphasize; this.codeFill = theme.colors.compBackgroundTertiary;
  }

  aboutToAppear(): void { this.parseBlocks(); }
  aboutToDisappear(): void { this.readSequence++; this.pending = false; }
  @Monitor('text', 'messageId') textChanged(): void {
    this.readSequence++; this.original = ''; this.pending = false; this.unavailable = false;
    this.parseBlocks();
  }
  @Computed get scaleAgentProse(): boolean { return Math.abs(this.agentFontScale - 1) > 0.001; }
  @Computed get recapProse(): boolean { return this.recapReading && this.chatReply && !this.dockPlan; }
  @Computed get agentBodyVp(): number { return agentReplyBodySize(this.agentFontScale); }
  @Computed get agentHanVp(): number { return agentReplyBodySize(this.agentFontScale, true); }
  // The recording profile uses MarkdownUI's display face at larger reading sizes. Its standard face already matches.
  @Computed get profileDisplayProse():boolean { return this.profileReading && this.agentBodyVp >= 20; }
  @Computed get promptBodyVp(): number { return Math.min(53, Math.max(14, Tokens.body * this.agentFontScale)); }

  private inlineSpacing(size: number, lineSpacingEm: number, readingBody:boolean = false): number {
    if (this.recapProse || readingBody && this.profileDisplayProse) return 0;
    if (this.recapReading) return this.readingPoints(size) * (this.dockPlan ? 0.37 : lineSpacingEm);
    return 0;
  }

  private inlineLineHeight(size: number, readingBody: boolean, spacingEm: number, nodes: MarkdownInline[], promptBody: boolean = false): string | undefined {
    // Theme.Level.body uses a 1.45-em advance. On ArkUI a fixed advance keeps its first and last lines at their intrinsic height.
    if (promptBody) return String(this.promptBodyVp * 1.45) + 'vp';
    // The native recap advances 23.5 pt at standard type and 66.5 pt at the largest reading size.
    if (readingBody && this.recapProse) return String(this.readingPoints(size) * (1.125 + spacingEm)) + 'vp';
    if (readingBody && this.agentBodyVp >= 17 && this.agentBodyVp <= 20) {
      // The system text face precedes the CJK Display crossover. Its two- and four-line replies
      // measure 44.7/93.3pt at standard type and 50.7/106pt at the next native category.
      const points = this.agentBodyVp;
      const advance = points <= 19 ? 73/3 + (points-17) * 11/6 : 28 + points-19;
      return String(advance) + 'vp';
    }
    if (readingBody && this.scaleAgentProse) {
      // SwiftUI adds ReplyMetrics.lineSpacing to the system body line advance.
      // The display CJK face advances 69.3pt per line of 48pt mixed prose in the native chat.
      const points = this.agentBodyVp;
      if (points >= 20 && hasHanProse(nodes)) {
        // The measured iPhone display sizes advance by 29, 38 and 45pt at 20, 26 and 31pt.
        // its largest display face advances by 69⅓pt. Retain the requested leading between those native sizes.
        const advance = points <= 26 ? 29 + (points-20)*1.5 :
          points <= 31 ? 38 + (points-26)*1.4 : 45 + (points-31)*(73/3)/17;
        return String(advance + points*(spacingEm-0.26)) + 'vp';
      }
      return String(points * (1.19 + spacingEm)) + 'vp';
    }
    return this.chatReply && !this.recapReading ? String(Math.ceil(size * 1.45)) + 'fp' : undefined;
  }

  private readingPoints(size: number): number {
    const scale = nativeEnvironment.fontScale;
    // MarkdownUI uses a metric relative to body: 17 points at standard type and 48 at AX5.
    const readingScale = scale <= 1 ? scale : 1 + (scale - 1) * (48 / 17 - 1) / (3.2 - 1);
    return size * readingScale;
  }
  private readingFont(size: number): number | string {
    return this.recapReading ? String(this.readingPoints(size)) + 'vp' : size;
  }
  private proseFont(han: boolean): string | undefined {
    if (this.recapProse) return String(recapGlyphSize(this.readingPoints(Tokens.body), han)) + 'vp';
    if (this.profileDisplayProse && !han) return String(this.agentBodyVp) + 'vp';
    return han ? String(this.agentHanVp) + 'vp' : undefined;
  }
  private recapTopInset(): number {
    // The smaller display face loses 5 vp of intrinsic first-line ascent at the largest reading size.
    return Math.max(0, this.readingPoints(Tokens.body) - Tokens.body) * 5 / (48 - Tokens.body);
  }
  private parseBlocks(): void {
    const source = this.original || this.text;
    const parsed = this.documents ? this.documents.parse(source) : parseMarkdown(source);
    const keys = new Map<MarkdownBlock, string>();
    parsed.forEach((block: MarkdownBlock, i: number) => {
      const value = this.ownMessage || block.kind === 'table' ? block.kind : block.source;
      keys.set(block, String(i) + ':' + value);
    });
    this.blocks = reconcileBoundRows(this.blocks, parsed, (b: MarkdownBlock) => keys.get(b)!, this, this, 'text', 'text');
  }
  private async loadOriginal(): Promise<void> {
    if (!this.clipped || this.original || this.pending) return;
    const sequence = ++this.readSequence;
    this.pending = true; this.unavailable = false;
    try {
      const text = await this.readOriginal();
      if (sequence === this.readSequence && text) this.hold(() => { this.original = text; this.parseBlocks(); });
    } catch (_) {
      if (sequence === this.readSequence) this.unavailable = true;
    } finally {
      if (sequence === this.readSequence) this.pending = false;
    }
  }

  @Computed get replyProse(): boolean { return this.transcript && !this.compact && !this.ownMessage && !this.quoteInk && !this.narration; }
  @Computed get proseSize(): number {
    if (this.compact) return Tokens.toolLabel;
    if (this.ownMessage || this.quoteInk) return Tokens.userBody;
    return this.narration ? Tokens.progressBody : Tokens.body;
  }
  @Computed get primaryInk(): ResourceColor { return this.messageInk ?? (this.narration ? Colors.progress : this.foreground); }
  @Computed get quotedInk(): ResourceColor { return this.messageQuoteInk ?? this.secondary; }
  private measuredHeight(text: string, size: number, lines: number): number {
    return Number(this.getUIContext().getMeasureUtils().measureTextSize({ textContent:text, fontSize:size + 'fp',
      constraintWidth:this.ownWidth + 'vp', lineHeight:Math.round(size * Tokens.replyBodyLeading) + 'fp',
      wordBreak:WordBreak.BREAK_WORD, maxLines:lines }).height);
  }
  private overflows(text: string, size: number, limit: number): boolean {
    if (!text || this.ownWidth <= 0 || nativeEnvironment.fontScale <= 0) return false;
    return this.measuredHeight(text,size,limit + 1) > this.measuredHeight(text,size,limit) + 0.5;
  }
  @Computed get userPreview(): string {
    if (!this.ownMessage || this.blocks.some(row => row.value.kind === 'quote')) return '';
    return markdownVisibleText(this.blocks.map(row => row.value));
  }
  @Computed get longUserMessage(): boolean { return !!this.userPreview && (this.clipped || this.overflows(this.userPreview, Tokens.userBody, 6)); }

  private headingSize(level: number): number {
    if (this.transcript) {
      if (level <= 1) return Tokens.replyHeading1;
      if (level === 2) return Tokens.replyHeading2;
      if (level === 3) return Tokens.title;
      return level === 4 ? Tokens.replyHeading4 : Tokens.body;
    }
    if (level <= 1) return Tokens.markdownHeading1;
    if (level === 2) return Tokens.markdownHeading2;
    if (level === 3) return Tokens.markdownHeading3;
    if (level === 4) return Tokens.markdownHeading4;
    if (level === 5) return Tokens.markdownHeading5;
    return Tokens.body;
  }

  /** Inline code keeps the ink of words drawn in an ink of their own (a quote, quiet agent words, a bubble), else a step quieter than the body. */
  private codeInk(bold: boolean): ResourceColor {
    if (this.quoteInk) return this.quotedInk;
    if (this.messageInk !== undefined) return this.messageInk;
    return this.narration && !bold ? Colors.progress : Colors.inlineCode;
  }
  private copyCode(value: string): void {
    const prompt = this.getUIContext().getPromptAction();
    pasteboard.getSystemPasteboard().setData(pasteboard.createData(pasteboard.MIMETYPE_TEXT_PLAIN, value))
      .then(() => prompt.showToast({ message: tr('ev_code_copied') }))
      .catch(() => prompt.showToast({ message: tr('ev_code_copy_failed') }));
  }
  @Computed get hasMediaOwner(): boolean {
    return !!this.mediaModel && !!this.mediaTarget && this.mediaModel.target() === this.mediaTarget;
  }
  @Computed get replyImages(): MessageImageGalleryEntry[] {
    if (!this.hasMediaOwner) return [];
    return markdownImagePaths(this.blocks.map(row => row.value), this.workspaceRoot).map((path: string): MessageImageGalleryEntry =>
      ({key:conversationImageKey(this.mediaTarget,this.messageId,'markdown',path),itemId:'',remotePath:path,target:this.mediaTarget}));
  }
  private openImage(path: string): void {
    const model = this.mediaModel;
    if (!model || !this.mediaActive || !this.hasMediaOwner) return;
    const gallery = messageImageGalleryWindow(this.replyImages, this.replyImages.findIndex(entry => entry.remotePath === path));
    if (gallery) this.onPreview({gallery, scope:model.scope(), hub:model.profile.hubId || model.profile.id});
  }
  @Computed get linkInk(): ResourceColor {
    return this.transcript ? Colors.reference : this.emphasize;
  }
  @Builder webLink(node: MarkdownInline, mark: Resource | undefined, size: number, bold: boolean, italic: boolean, code: boolean) {
    if (mark) {
      ImageSpan(mark).width((size - 2) + 'fp').height((size - 2) + 'fp').verticalAlign(ImageSpanAlignment.CENTER)
        .margin({right:3}).onClick(() => this.onLink(node.url))
    }
    ForEach(node.children, (child: MarkdownInline) => this.inlineNode(child, bold, italic, code, node.url, size),
      (child: MarkdownInline, index: number) => String(index))
  }
  @Builder inlineText(nodes: MarkdownInline[], size: number = Tokens.body, bold: boolean = false, alignment: TextAlign = TextAlign.Start, fillCell: boolean = false) {
    if (this.hasMediaOwner && this.replyImages.length) {
      ForEach(markdownMediaRuns(nodes, this.workspaceRoot), (run: MarkdownMediaRun) => {
        if (run.path) {
          TranscriptImage({ model:this.mediaModel!, active:this.mediaActive, remotePath:run.path, label:run.label,
            sourceKey:conversationImageKey(this.mediaTarget,this.messageId,'markdown',run.path), galleryEntries:this.replyImages,
            galleryIndex:this.replyImages.findIndex(entry => entry.remotePath === run.path),
            maximumThumbnailWidth:Math.max(Tokens.touch,this.mediaWidth),maximumThumbnailHeight:260,thumbnailEdge:1024,
            onPreview:this.onPreview, onEdit:this.onEditImage }).margin({top:4,bottom:4}).id('chat.markdown.image')
        } else { this.prose(run.nodes,size,bold,alignment,fillCell) }
      }, (run: MarkdownMediaRun, i: number) => JSON.stringify([i, run.path, run.nodes]))
    } else { this.prose(nodes,size,bold,alignment,fillCell) }
  }

  private copyMenu(nodes: MarkdownInline[]): EditMenuOptions {
    return {
      onCreateMenu: (items: TextMenuItem[]): TextMenuItem[] => items,
      onMenuItemClick: (item: TextMenuItem, range: TextRange): boolean => {
        if (!this.transcript || !item.id.equals(TextMenuItemId.COPY) || range.start === undefined || range.end === undefined) return false;
        const text = markdownCodeSelection(nodes,range.start,range.end,this.transcript,this.workspaceRoot,this.hasMediaOwner);
        if (text === undefined) return false;
        pasteboard.getSystemPasteboard().setData(pasteboard.createData(pasteboard.MIMETYPE_TEXT_PLAIN,text)).catch(() => {});
        return true;
      }
    };
  }
  @Builder prose(nodes: MarkdownInline[], size: number = Tokens.body, bold: boolean = false, alignment: TextAlign = TextAlign.Start, fillCell: boolean = false) {
    Text() {
      ForEach(nodes, (node: MarkdownInline) => this.inlineNode(node, bold, false, false, '', size), (node: MarkdownInline, index: number) => String(index))
    }
      .fontSize(size).fontColor(this.foreground).fontWeight(bold ? FontWeight.Bold : FontWeight.Normal)
      .lineHeight(this.transcript ? String(Math.round(size * (size > Tokens.body ? Tokens.replyHeadingLeading : this.replyProse && size === Tokens.body ? 1.52 : Tokens.replyBodyLeading))) + 'fp' : undefined)
      .textAlign(alignment).wordBreak(WordBreak.BREAK_WORD)
      .lineBreakStrategy(this.ownMessage && this.fitContent && this.text.length < 200 && !this.longUserMessage ? LineBreakStrategy.BALANCED :
        this.transcript && !this.ownMessage ? LineBreakStrategy.HIGH_QUALITY : LineBreakStrategy.GREEDY)
      .copyOption(CopyOptions.LocalDevice).editMenuOptions(this.copyMenu(nodes)).onClick(this.onBodyTap).width(this.fitContent && !fillCell ? undefined : '100%')
  }

  @Builder inlineNode(node: MarkdownInline, bold: boolean = false, italic: boolean = false, code: boolean = false, url: string = '', size: number = Tokens.body) {
    if (node.kind === 'bold') {
      ForEach(node.children, (child: MarkdownInline) => this.inlineNode(child, true, italic, code, url, size),
        (child: MarkdownInline, index: number) => String(index))
    } else if (node.kind === 'italic') {
      ForEach(node.children, (child: MarkdownInline) => this.inlineNode(child, bold, true, code, url, size),
        (child: MarkdownInline, index: number) => String(index))
    } else if (url) {
      Span(node.text).fontFamily(node.kind === 'code' ? 'monospace' : undefined)
        .fontSize(node.kind === 'code' ? this.readingFont(this.chatReply ? 16 : Tokens.meta) : undefined)
        .fontWeight(bold ? FontWeight.Bold : FontWeight.Normal).fontStyle(italic ? FontStyle.Italic : FontStyle.Normal).fontColor(this.emphasize)
        .textBackgroundStyle({ color: node.kind === 'code' && !this.chatReply ? this.codeFill : Color.Transparent, radius: Tokens.fine })
        .decoration({ type: TextDecorationType.Underline, color: this.emphasize }).onClick(() => this.onLink(url))
    } else if (node.kind === 'code') {
      this.inlineLeaf(node.text, bold, italic, true, url, size, node.gapBefore, node.gapAfter)
    } else if (node.kind === 'image') {
      this.inlineLeaf(markdownInlineText(node.children) || tr('image'), bold, italic, false, '', size)
    } else if (node.kind === 'file' && this.hasMediaOwner && chatImagePath(node.url, this.workspaceRoot)) {
      SymbolSpan($r('sys.symbol.picture')).fontSize(size - 2).fontColor([this.linkInk])
        .onClick(() => this.openImage(chatImagePath(node.url, this.workspaceRoot)))
      Span(' ' + (markdownInlineText(node.children) || tr('image'))).fontSize(size).fontColor(this.linkInk).fontWeight(bold ? FontWeight.Bold : FontWeight.Medium)
        .onClick(() => this.openImage(chatImagePath(node.url, this.workspaceRoot)))
    } else if (node.kind === 'file') {
      if (chatFilePath(node.url, this.workspaceRoot)) {
        Span(markdownInlineText(node.children)).fontSize(size).fontColor(this.linkInk).fontWeight(bold ? FontWeight.Bold : FontWeight.Medium)
          .decoration({ type: this.transcript ? TextDecorationType.None : TextDecorationType.Underline, color: this.linkInk })
          .onClick(() => this.onOpenFile(chatFilePath(node.url, this.workspaceRoot)))
      } else {
        this.inlineLeaf(markdownInlineText(node.children), bold, italic, true, '', size, node.gapBefore, node.gapAfter)
      }
    } else if (node.kind === 'link') {
      this.webLink(node, this.transcript ? referenceMark(node.url) : undefined, size, bold, italic, code)
    } else {
      this.inlineLeaf(node.text, bold, italic, code, url, size)
    }
  }

  @Builder inlineLeaf(value: string, bold: boolean, italic: boolean, code: boolean, url: string, size: number,
    gapBefore: boolean = false, gapAfter: boolean = false) {
    if (code && this.transcript) {
      // No box or padding (DESIGN_SYSTEM 3.11): the monospaced face a little smaller than the body, in its own ink,
      // with a thin space outside the code where it meets a CJK letter. A tap on it in a reply copies it.
      if (gapBefore) Span('\u2009').fontSize(size)
      Span(value).fontFamily('monospace').fontSize(Math.round(size * Tokens.inlineCodeScale))
        .fontWeight(bold ? FontWeight.Bold : FontWeight.Normal).fontStyle(italic ? FontStyle.Italic : FontStyle.Normal)
        .fontColor(url ? this.linkInk : this.codeInk(bold))
        .onClick(url ? () => this.onLink(url) : this.ownMessage ? undefined : () => this.copyCode(value))
      if (gapAfter) Span('\u2009').fontSize(size)
    } else if (code) {
      Span(value).fontFamily('monospace').fontSize(Tokens.meta)
        .fontWeight(bold ? FontWeight.Bold : FontWeight.Normal).fontStyle(italic ? FontStyle.Italic : FontStyle.Normal)
        .fontColor(url ? this.linkInk : this.foreground)
        .decoration({type:url ? TextDecorationType.Underline : TextDecorationType.None,color:this.linkInk})
        .textBackgroundStyle({color:this.codeFill,radius:Tokens.fine})
        .onClick(url ? () => this.onLink(url) : undefined)
    } else if (url) {
      Span(this.transcript ? markdownProseText(value) : value).fontColor(this.linkInk)
        .fontWeight(bold ? FontWeight.Bold : this.transcript ? FontWeight.Medium : FontWeight.Normal).fontStyle(italic ? FontStyle.Italic : FontStyle.Normal)
        .decoration({ type: this.transcript ? TextDecorationType.None : TextDecorationType.Underline, color: this.linkInk })
        .onClick(() => this.onLink(url))
    } else {
      Span(this.transcript ? markdownProseText(value) : value).fontColor(this.quoteInk ? this.quotedInk : bold && this.narration ? this.foreground : this.primaryInk).fontWeight(bold ? FontWeight.Bold : FontWeight.Normal).fontStyle(italic ? FontStyle.Italic : FontStyle.Normal)
    }
  }

  @Builder blockView(block: MarkdownBlock, path: string = '') {
    if (block.kind === 'paragraph') {
      this.inlineText(block.inlines, this.proseSize, false, TextAlign.Start, false)
    } else if (block.kind === 'heading') {
      this.inlineText(block.inlines, this.compact ? Tokens.meta : this.headingSize(block.level), true)
    } else if (block.kind === 'code' && this.transcript) {
      ConversationCode({ code: block.text, language: block.language, onBodyTap:this.onBodyTap })
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
            .copyOption(this.nativeTextSelection ? CopyOptions.LocalDevice : CopyOptions.None).onTextSelectionChange((start: number, end: number) => this.onSelectionChange(end > start)).fontColor(this.foreground)
        }.scrollable(ScrollDirection.Horizontal).align(Alignment.TopStart).scrollBar(BarState.Auto).width('100%')
      }
        .padding(Tokens.inset).width('100%').alignItems(HorizontalAlign.Start)
        .backgroundColor(this.codeFill)
        .border({ width: this.chatReply ? 0 : Tokens.hairline, color: this.secondary, radius: this.chatReply ? Tokens.inset : Tokens.fine })
        .borderRadius(this.chatReply ? Tokens.inset : Tokens.fine)
    } else if (block.kind === 'raw') {
      Text(block.text).fontSize(Tokens.body).fontColor(this.foreground).wordBreak(WordBreak.BREAK_WORD)
        .copyOption(this.nativeTextSelection ? CopyOptions.LocalDevice : CopyOptions.None).onTextSelectionChange((start: number, end: number) => this.onSelectionChange(end > start)).onClick(this.onBodyTap).width('100%')
    } else if (block.kind === 'rule') {
      Divider().strokeWidth(Tokens.hairline).color(this.transcript ? Colors.divider : this.secondary).width('100%')
        .margin({ top: Tokens.gap, bottom: Tokens.gap })
    } else if (block.kind === 'quote' && this.ownMessage) {
      UserPassage({ preview:markdownVisibleText(block.children), limit:2, overflow:this.clipped || this.overflows(markdownVisibleText(block.children),16,2),
        ink:this.quotedInk, actionInk:this.primaryInk, proseSize:16, identity:this.messageId + '.quote.' + path, hold:this.hold, onBodyTap:this.onBodyTap, onReveal:() => { void this.loadOriginal(); } }) {
        MarkdownText({ text:block.children.map(b => b.source).join('\n\n'), documents:this.documents, transcript:true, quoteInk:true, messageInk:this.primaryInk, messageQuoteInk:this.quotedInk,
          workspaceRoot:this.workspaceRoot, onOpenFile:this.onOpenFile, onLink:this.onLink, onBodyTap:this.onBodyTap })
      }
    } else if (block.kind === 'quote') {
      Column({ space: Tokens.gap }) {
        ForEach(block.children, (child: MarkdownBlock, i: number) => this.blockView(child, path + '.q' + i),
          (child: MarkdownBlock, index: number) => 'quote.' + String(index))
      }
        .padding(Tokens.inset).width('100%').alignItems(HorizontalAlign.Start)
        .backgroundColor(this.transcript ? Color.Transparent : this.codeFill)
        .border({ width: this.transcript ? { left: Tokens.fine } : Tokens.hairline, color: this.transcript ? Colors.divider : this.emphasize, radius: Tokens.fine })
    } else if (block.kind === 'list') {
      Column({ space: this.replyProse ? Tokens.fine : Tokens.gap }) {
        ForEach(block.items, (item: MarkdownBlock, i: number) => this.listItem(item, path + '.l' + i),
          (item: MarkdownBlock, index: number) => 'list.' + String(index))
      }.width('100%').alignItems(HorizontalAlign.Start)
        .padding({top:this.dockPlan && path !== '0' ? Math.max(0,this.readingPoints(Tokens.body)*0.2-6) : 0})
    } else if (block.kind === 'table') {
      MarkdownTable({ table: block, cellText: (nodes: MarkdownInline[], header: boolean, align: TextAlign): void =>
        this.inlineText(nodes, 15, header, align, true) })
    }
  }

  @Builder listItem(item: MarkdownBlock, path: string) {
    Row({ space: this.replyProse ? 6 : Tokens.inset }) {
      Text(item.marker).fontSize(this.proseSize).fontColor(this.secondary)
      Column({ space: Tokens.gap }) {
        ForEach(item.children, (child: MarkdownBlock, i: number) => this.blockView(child, path + '.i' + i),
          (child: MarkdownBlock, index: number) => 'item.' + String(index))
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
    }.width('100%').alignItems(VerticalAlign.Top).padding({left:this.dockPlan ? this.readingPoints(Tokens.body)*0.65 : 0})
  }


  @Builder contents() {
    Column({ space: this.compact ? Tokens.fine : this.replyProse ? Tokens.replyParagraphGap : Tokens.gap }) {
      ForEach(this.blocks, (row: BoundRow<MarkdownBlock>) => this.blockView(row.value, row.key), (row: BoundRow<MarkdownBlock>) => row.key)
    }.width(this.fitContent ? undefined : '100%').alignItems(HorizontalAlign.Start)
  }
  private systemTableTextAlign(table: MarkdownBlock, column: number): TextAlign {
    const alignment = table.alignments[column] || 'start';
    if (alignment === 'center') return TextAlign.Center;
    if (alignment === 'end') return TextAlign.End;
    return TextAlign.Start;
  }
  @Builder systemInlineText(nodes: MarkdownInline[], lineSpacingEm: number, size: number = Tokens.body, weight: FontWeight = FontWeight.Normal,
    alignment: TextAlign = TextAlign.Start, breakAll: boolean = false, quiet: boolean = false, readingBody: boolean = false,
    promptBody: boolean = false) {
    Text() {
      ForEach(nodes, (node: MarkdownInline) => this.systemInlineNode(node, weight, false, '', quiet, readingBody),
        (node: MarkdownInline, index: number) => String(index))
    }
      .fontSize(promptBody ? String(this.promptBodyVp) + 'vp' :
        readingBody && (this.scaleAgentProse || this.fixedType) ? String(this.agentBodyVp) + 'vp' : this.readingFont(size))
      .fontColor(quiet ? this.secondary : this.foreground).fontWeight(weight)
      .lineHeight(this.inlineLineHeight(size, readingBody, lineSpacingEm, nodes, promptBody))
      .lineSpacing(LengthMetrics.vp(this.inlineSpacing(size, lineSpacingEm, readingBody)), { onlyBetweenLines: true })
      .textAlign(alignment).wordBreak(breakAll ? WordBreak.BREAK_ALL : WordBreak.BREAK_WORD)
      .copyOption(this.nativeTextSelection ? CopyOptions.LocalDevice : CopyOptions.None).onTextSelectionChange((start: number, end: number) => this.onSelectionChange(end > start))
      .width(this.fitContent ? undefined : '100%')
  }
  @Builder systemInlineNode(node: MarkdownInline, weight: FontWeight, italic: boolean, url: string, quiet: boolean, readingBody: boolean) {
    if (node.kind === 'bold' || node.kind === 'italic' || node.kind === 'link') {
      ForEach(node.children, (child: MarkdownInline) => this.systemInlineNode(child,
        node.kind === 'bold' ? (this.chatReply ? FontWeight.Medium : FontWeight.Bold) : weight,
        italic || node.kind === 'italic', node.kind === 'link' ? node.url : url, quiet, readingBody),
        (child: MarkdownInline, index: number) => String(index))
    } else if (url) {
      Span(node.text).fontFamily(node.kind === 'code' ? 'monospace' : undefined)
        .fontSize(node.kind === 'code' ? this.readingFont(this.chatReply ? 16 : Tokens.meta) : undefined)
        .fontWeight(weight).fontStyle(italic ? FontStyle.Italic : FontStyle.Normal).fontColor(this.emphasize)
        .textBackgroundStyle({ color: node.kind === 'code' && !this.chatReply ? this.codeFill : Color.Transparent, radius: Tokens.fine })
        .decoration({ type: TextDecorationType.Underline, color: this.emphasize }).onClick(() => this.onLink(url))
    } else if (node.kind === 'code') {
      Span(node.text).fontFamily('monospace').fontSize(this.readingFont(this.chatReply ? 16 : Tokens.meta))
        .fontWeight(weight).fontStyle(italic ? FontStyle.Italic : FontStyle.Normal)
        .fontColor(this.recapProse ? Colors.inlineCodeInk : this.chatReply ? this.foreground : this.secondary)
        .textBackgroundStyle({ color: this.chatReply ? Color.Transparent : this.codeFill, radius: Tokens.fine })
    } else if (readingBody && (this.scaleAgentProse || this.recapProse)) {
      ForEach(markdownProseRuns(node.text), (run: MarkdownProseRun) => {
        Span(run.text).fontSize(this.proseFont(run.han))
          .fontColor(quiet ? this.secondary : this.foreground).fontWeight(weight).fontStyle(italic ? FontStyle.Italic : FontStyle.Normal)
      }, (run: MarkdownProseRun, index: number) => String(index))
    } else {
      Span(node.text).fontColor(quiet ? this.secondary : this.foreground).fontWeight(weight)
        .fontStyle(italic ? FontStyle.Italic : FontStyle.Normal)
    }
  }
  @Builder systemBlockView(block: MarkdownBlock, first: boolean = false) {
    if (block.kind === 'paragraph') {
      this.systemInlineText(block.inlines, this.profileDisplayProse ? 0.26 : this.paragraphLineSpacingEm, Tokens.body, FontWeight.Normal,
        TextAlign.Start, false, false, this.recapProse || this.profileDisplayProse)
    } else if (block.kind === 'heading') {
      Column() {
        this.systemInlineText(block.inlines, 0.26, this.headingSize(block.level),
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
            .copyOption(this.nativeTextSelection ? CopyOptions.LocalDevice : CopyOptions.None).onTextSelectionChange((start: number, end: number) => this.onSelectionChange(end > start)).fontColor(this.foreground)
        }.scrollable(ScrollDirection.Horizontal).align(Alignment.TopStart).scrollBar(BarState.Auto).width('100%')
      }
        .padding(Tokens.inset).width('100%').alignItems(HorizontalAlign.Start)
        .backgroundColor(this.codeFill)
        .border({ width: this.chatReply ? 0 : Tokens.hairline, color: this.secondary, radius: this.chatReply ? Tokens.inset : Tokens.fine })
        .borderRadius(this.chatReply ? Tokens.inset : Tokens.fine)
    } else if (block.kind === 'raw') {
      Text(block.text).fontSize(Tokens.body).fontColor(this.foreground).wordBreak(WordBreak.BREAK_WORD)
        .copyOption(this.nativeTextSelection ? CopyOptions.LocalDevice : CopyOptions.None).onTextSelectionChange((start: number, end: number) => this.onSelectionChange(end > start)).width('100%')
    } else if (block.kind === 'rule') {
      Divider().strokeWidth(Tokens.hairline).color(this.secondary).width('100%')
        .margin({ top: Tokens.gap, bottom: Tokens.gap })
    } else if (block.kind === 'quote') {
      Column({ space: Tokens.gap }) {
        ForEach(block.children, (child: MarkdownBlock) => this.systemBlockView(child),
          (child: MarkdownBlock, index: number) => 'quote.' + String(index))
      }
        .padding(Tokens.inset).width('100%').alignItems(HorizontalAlign.Start)
        .backgroundColor(this.codeFill)
        .border({ width: Tokens.hairline, color: this.emphasize, radius: Tokens.fine })
    } else if (block.kind === 'list') {
      Column({ space: this.dockPlan ? this.readingPoints(Tokens.body)*0.2 : Tokens.gap }) {
        ForEach(block.items, (item: MarkdownBlock) => this.systemListItem(item),
          (item: MarkdownBlock, index: number) => 'list.' + String(index))
      }.width('100%').alignItems(HorizontalAlign.Start)
        .padding({top:this.dockPlan && !first ? Math.max(0,this.readingPoints(Tokens.body)*0.2-6) : 0})
    } else if (block.kind === 'table') {
      Scroll() {
        Column({ space: Tokens.fine }) {
          ForEach(block.rows, (row: MarkdownTableRow) => this.systemTableRow(row, block),
            (row: MarkdownTableRow, index: number) => 'row.' + String(index))
        }
      }
        .scrollable(ScrollDirection.Horizontal).align(Alignment.TopStart).scrollBar(BarState.Auto).width('100%')
    }
  }
  @Builder systemListItem(item: MarkdownBlock) {
    Row({ space: this.dockPlan ? 10 : Tokens.inset }) {
      Text(item.marker).fontSize(this.dockPlan ? this.readingFont(Tokens.body) : Tokens.body).fontColor(this.secondary)
      Column({ space: Tokens.gap }) {
        ForEach(item.children, (child: MarkdownBlock) => this.systemBlockView(child),
          (child: MarkdownBlock, index: number) => 'item.' + String(index))
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
    }.width('100%').alignItems(VerticalAlign.Top).padding({left:this.dockPlan ? this.readingPoints(Tokens.body)*0.65 : 0})
  }
  @Builder systemTableRow(row: MarkdownTableRow, table: MarkdownBlock) {
    Row() {
      ForEach(row.cells, (cell: MarkdownInline[], index: number) => this.systemTableCell(cell, table, index, row.header),
        (cell: MarkdownInline[], index: number) => 'cell.' + String(index))
    }.alignItems(VerticalAlign.Top)
  }
  @Builder systemTableCell(cell: MarkdownInline[], table: MarkdownBlock, index: number, header: boolean) {
    Column() {
      this.systemInlineText(cell, 0.26, Tokens.body, header ? FontWeight.Bold : FontWeight.Normal, this.systemTableTextAlign(table, index), true)
    }
      .width(Tokens.markdownTableCell).padding(Tokens.inset).alignItems(HorizontalAlign.Start)
      .border({ width: Tokens.hairline, color: this.secondary, radius: Tokens.fine })
      .backgroundColor(header ? this.codeFill : Color.Transparent)
  }
  @Builder systemReading() {
    Column({ space: this.dockPlan ? 6 : Tokens.gap }) {
      ForEach(this.blocks, (entry: BoundRow<MarkdownBlock>, index: number) => {
        if (this.agentReply && entry.value.kind === 'paragraph') {
          Column() {
            this.systemInlineText(entry.value.inlines, this.paragraphLineSpacingEm, Tokens.body, FontWeight.Normal,
              TextAlign.Start, false, false, true)
          }.width('100%').alignItems(HorizontalAlign.Start)
            .padding({ top: index > 0 && this.blocks[index - 1]?.value.kind === 'paragraph' ? 10 - Tokens.gap : 0 })
        } else if (this.agentPrompt && entry.value.kind === 'paragraph') {
          this.systemInlineText(entry.value.inlines, this.paragraphLineSpacingEm, Tokens.body, FontWeight.Normal,
            TextAlign.Start, false, false, false, true)
        } else {
          this.systemBlockView(entry.value, index === 0)
        }
      }, (entry: BoundRow<MarkdownBlock>) => entry.key)
    }.width(this.fitContent ? undefined : '100%').alignItems(HorizontalAlign.Start)
      .padding({ top: this.recapProse ? this.recapTopInset() : 0 })
  }
  build() {
    if (this.recapProse || this.dockPlan || this.profileDisplayProse) { this.systemReading() } else {
    Column({ space:Tokens.fine }) {
      if (this.longUserMessage) {
        UserPassage({ preview:this.userPreview, limit:6, overflow:true, ink:this.primaryInk, actionInk:this.primaryInk,
          proseSize:Tokens.userBody, identity:this.messageId + '.body', hold:this.hold, onBodyTap:this.onBodyTap, onReveal:() => { void this.loadOriginal(); } }) { this.contents() }
      } else { this.contents() }
      if (this.pending) Text(tr('loading')).fontSize(Tokens.caption).fontColor(this.quotedInk)
      if (this.unavailable) {
        Button(tr('retry'), { type:ButtonType.Normal, buttonStyle:ButtonStyleMode.TEXTUAL })
          .fontSize(Tokens.meta).fontColor(this.primaryInk).backgroundColor(Color.Transparent)
          .onClick(() => { void this.loadOriginal(); })
      }
    }.width(this.longUserMessage ? this.ownWidth : this.fitContent ? undefined : '100%').alignItems(HorizontalAlign.Start)
    }
  }
}
