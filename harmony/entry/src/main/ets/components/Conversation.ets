import { PhoneDictationControl } from './PhoneDictationControl';
import { ConversationRecap } from './ConversationRecap';
import { AssistantWelcome } from './AssistantWelcome';
import { IconActions } from '../design/IconActions';
import { Theme, SymbolGlyphModifier } from '@kit.ArkUI';
import { ComposerOverlayWorkspace, ComposerOverlaySession } from '../models/ComposerOverlayWorkspace';
import { composerPopupLayout, ComposerPopupLayout } from '../core/ComposerPopupLayout';
import { util } from '@kit.ArkTS';
import { FoldSplitContainer, HoverModeStatus, FrameCallback, LengthMetrics } from '@kit.ArkUI';
import { hilog } from '@kit.PerformanceAnalysisKit';
import { NativeEnvironment, nativeEnvironment } from '../platform/NativeEnvironment';
import { DeviceLook, deviceLook } from '../models/DeviceLook';
import { nativeMessageTheme } from '../design/LookTheme';
import { AssistantMessage } from './AssistantMessage';
import { pasteboard } from '@kit.BasicServicesKit';
import { SessionRevision, SessionRevisionOutcome } from '../models/SessionRevision';
import type { SessionRevisionRestore } from '../core/SessionRevision';
import { ConversationRevisionLease, restoreAcknowledgedSessionDraft } from '../core/ConversationRevisionLease';
import { TranscriptImage } from './TranscriptImage';
import { conversationImageEntries, conversationImageKey } from '../core/MessageImageGallery';
import type { MessageImageGalleryEntry, LoadedMessageImage } from '../core/MessageImageGallery';
import { MarkdownText } from './MarkdownText';
import { AssistantCards } from './AssistantCards';
import { QuestionWorkspace, QuestionSheet, questionPrompt } from '../models/QuestionWorkspace';
import { InteractionDecisionForm } from './InteractionDecisionForm';
import { AssistantFeedModel } from '../models/AssistantFeed';
import { ChatAttachments, ChatAttachment } from '../models/ChatAttachments';
import { AttachmentStrip } from './AttachmentStrip';
import { common } from '@kit.AbilityKit';
import { previewAttachment } from '../platform/NativePreview';
import { mergeConversationTimeline, reconcileTimelineRows, TimelineMessage, TimelineCard } from '../core/ConversationTimeline';
import { ClientModel } from '../models/ClientModel';
import { ChatMessage, Decision, ResourceRow, Wire } from '../models/Wire';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { ButtonStyles } from '../design/Controls';
import { nativeSheetInset } from '../platform/NativeInsets';
import { ContextPicker } from './ContextPicker';
import type { ClientViewLease } from '../models/ClientModel';
import { parseReadingAnchor, readingAnchorIndex } from '../core/ConversationReading';
import type { ReadingAnchor } from '../core/ConversationReading';

class ConversationFrame extends FrameCallback {
  private action: () => void;
  constructor(action: () => void) { super(); this.action = action; }
  onFrame(_time: number): void { this.action(); }
}

class ComposerChoice implements AttributeModifier<ListItemAttribute> {
  readonly session: ComposerOverlaySession; readonly key: string; readonly title: ResourceStr;
  readonly symbol: Resource | undefined; readonly action: (() => void) | undefined;
  constructor(session: ComposerOverlaySession, key: string, title: ResourceStr, symbol?: Resource, action?: () => void) {
    this.session = session; this.key = key; this.title = title; this.symbol = symbol; this.action = action;
  }
  applyNormalAttribute(node: ListItemAttribute): void {
    node.accessibilityGroup(true);
    if (typeof this.title === 'string') node.accessibilityText(this.title);
    else node.accessibilityText(this.title);
  }
}
class ComposerActionIntent {
  readonly owner: string; readonly preserveInput: boolean; readonly previous: number;
  request: number = -1;
  constructor(owner: string, preserveInput: boolean, previous: number) { this.owner = owner; this.preserveInput = preserveInput; this.previous = previous; }
}

@ComponentV2
struct MessageSurface {
  @Param own: boolean = false;
  @Param personaReply: boolean = false;
  @Param maximum: number = Tokens.readingMax;
  @BuilderParam content: () => void;
  @Local fill: ResourceColor = Color.Transparent;
  onWillApplyTheme(theme: Theme): void { this.fill = theme.colors.compBackgroundPrimary; }
  build() {
    Column() { this.content() }.width(this.own ? undefined : '100%').constraintSize({ maxWidth: this.maximum })
      .padding(this.own || this.personaReply ? {left:14,right:14,top:9,bottom:9} : 0)
      .backgroundColor(this.own ? this.fill : this.personaReply ? Colors.control : Color.Transparent)
      .borderRadius(this.own || this.personaReply ? Tokens.bubble : 0)
  }
}

class ConversationLine {
  id: string;
  readingId: string;
  message: ChatMessage | undefined;
  card: Record<string, Object> | undefined;
  decision: Decision | undefined;
  constructor(id: string, message?: ChatMessage, card?: Record<string, Object>, decision?: Decision) {
    this.id = id; this.readingId = message ? 'message:' + (message.itemId || message.id) : id; this.message = message; this.card = card; this.decision = decision;
  }
}

@ComponentV2
export struct Conversation {
  @Require @Param model: ClientModel;
  @Param active: boolean = true;
  @Param searchHitId: string = '';
  @Param showContextMenu: boolean = false;
  @Param actionItems: SheetInfo[] = [];
  @Param actionsVisible: boolean = false;
  @Param actionsRequest: number = 0;
  @Event onPlace: (name: string) => void = () => {};
  @Event onShowActions: () => void = () => {};
  @Event onHideActions: (request: number) => void = () => {};
  @Event admission: () => boolean = (): boolean => true;
  @Event onOpenCard: (itemId: string, kind: string) => void = () => {};
  @Event onPrimaryStop: (shown: boolean) => void = () => {};
  @Event onOpenFile: (path: string) => void = () => {};
  @Event onLink: (address: string) => void = () => {};
  @Event onOpenResource: (row: ResourceRow) => void = () => {};
  @Local feed: AssistantFeedModel = new AssistantFeedModel();
  @Local look: DeviceLook = deviceLook;
  @Local attachments: ChatAttachments = new ChatAttachments();
  @Local sending: boolean = false;
  @Local revisions: SessionRevision = new SessionRevision();
  @Local revisionResult: string = '';
  @Local revisionCacheError: string = '';
  private revisionKey: string = '';
  private revisionContextKey: string = '';
  private revisionVisibilityRevision: number = 0;
  private revisionMenuLease: ConversationRevisionLease | undefined = undefined;
  private attachmentKey: string = '';
  private mounted: boolean = false;
  @Local lines: ConversationLine[] = [];
  @Local imageGallery: MessageImageGalleryEntry[] = [];
  private imageIndices: Map<string, number> = new Map();
  private feedKey: string = '';
  private feedTimer: number = -1;
  @Local expanded: string[] = [];
  private scroller: Scroller = new Scroller();
  private readingLease: ClientViewLease | undefined;
  private readingKey: string = '';
  private readingEpoch: number = 0;
  private readingRestoring: boolean = true;
  private readingAnchorReady: boolean = false;
  private readingFetching: boolean = false;
  @Local readingLoading: boolean = false;
  private readingMotion: number = 0;
  private readingCursors: Set<string> = new Set();
  @Local readingFailed: boolean = false;
  private firstVisibleIndex: number = 0;
  private lastVisibleIndex: number = -1;
  private searchedHit: string = '';
  private seekingHit: string = '';
  private searchHighlightTimer: number = -1;
  @Local highlightedLine: string = '';
  private hitScope(): string {
    return JSON.stringify([this.model.scope(),this.model.connectionGeneration(),this.model.target(),this.searchHitId]);
  }
  private findSearchLine(hit:string): number {
    const direct = this.lines.findIndex((line:ConversationLine) => !!line.message &&
      (line.message.id === hit || line.message.itemId === hit || (line.message.role === 'recap' && line.message.recaps.some((recap) => recap.id === hit))));
    if (direct >= 0) return direct;
    return this.lines.findIndex((line:ConversationLine) => {
      if (!line.message?.work || !line.message.raw.startsWith('[')) return false;
      try {
        const items = JSON.parse(line.message.raw) as Object[];
        return Array.isArray(items) && items.some((item:Object) => Wire.text(Wire.record(item),'id') === hit);
      } catch (_) { return false; }
    });
  }
  private clearSearchHighlight(): void {
    if (this.searchHighlightTimer !== -1) clearTimeout(this.searchHighlightTimer);
    this.searchHighlightTimer = -1; this.highlightedLine = '';
  }
  @Monitor('searchHitId') requestedHitChanged(): void {
    this.searchedHit = ''; this.seekingHit = ''; this.clearSearchHighlight(); this.scheduleReading();
  }
  private async seekSearchHit(key:string): Promise<void> {
    if (this.seekingHit === key) return;
    const owner = this.model, hit = this.searchHitId, epoch = this.readingEpoch, motion = this.readingMotion;
    const current = (): boolean => this.active && this.model === owner && this.seekingHit === key &&
      epoch === this.readingEpoch && this.hitScope() === key && this.readingMotion === motion;
    this.seekingHit = key;
    try {
      for (let page = 0; page <= 6 && current(); page++) {
        const index = this.findSearchLine(hit);
        if (index >= 0) {
          const line = this.lines[index];
          this.readingRestoring = false;
          this.getUIContext().postFrameCallback(new ConversationFrame(() => {
            if (!this.active || this.model !== owner || epoch !== this.readingEpoch || this.hitScope() !== key || this.readingMotion !== motion) return;
            const visible = this.findSearchLine(hit);
            if (visible < 0) return;
            this.clearSearchHighlight(); this.readingRestoring = false; this.atEnd = false;
            this.scroller.scrollToIndex(visible + this.readingPrefix(), false, ScrollAlign.CENTER);
            this.highlightedLine = line.id;
            this.searchHighlightTimer = setTimeout(() => { this.highlightedLine = ''; this.searchHighlightTimer = -1; }, 2600);
          }));
          return;
        }
        if (page === 6 || !owner.hasMoreMessages || !owner.connected || !await owner.loadOlder()) break;
        if (current()) this.syncTimeline();
      }
    } catch (_) { /* Leave this conversation readable when more history is unavailable. */ } finally {
      if (this.seekingHit === key) {
        this.seekingHit = ''; this.searchedHit = key;
        if (this.readingRestoring && this.findSearchLine(hit) < 0) this.scheduleReading();
      }
    }
  }
  private anchor: ReadingAnchor = { id: '', offset: 0, atEnd: true };
  private showWelcome(): boolean {
    return this.model.selected.kind === 'conversation' && this.active && this.model.connected && this.model.transcriptHistoryReady &&
      !this.model.hasMoreMessages && !this.lines.length && !this.model.error && !this.feed.error;
  }
  private welcomeCanAsk(): boolean {
    return this.composerAvailable() && !this.model.draftLoading && !this.model.draft && !this.model.sourceContext &&
      !this.attachments.busy && !this.attachments.attachments.length && !this.sending;
  }
  private readingPrefix(): number {
    return (this.readingFailed ? 1 : 0) + (this.feed.error && this.feed.error !== this.model.error ? 1 : 0) + (this.showWelcome() ? 1 : 0) + (this.model.hasMoreMessages || this.feed.nextCursor ? 1 : 0);
  }
  private saveReading(): void {
    if (!this.readingRestoring && this.readingLease?.valid()) this.readingLease.write(JSON.stringify(this.anchor)).catch(() => {});
  }
  private syncReading(): void {
    const key = JSON.stringify([this.model.scope(), this.model.target()]);
    if (this.readingKey === key) return;
    this.saveReading(); this.jumpAvailable = false; this.lastVisibleIndex = -1; this.readingKey = key; this.searchedHit = ''; this.seekingHit = ''; if (this.highlightedLine) this.clearSearchHighlight();
    const epoch = ++this.readingEpoch, lease = this.model.captureView('reading:' + this.model.target());
    this.readingLease = lease; this.readingCursors.clear(); this.readingFetching = false; this.readingRestoring = true; this.readingAnchorReady = false; this.readingFailed = false;
    lease.read().then((value: string) => {
      if (epoch !== this.readingEpoch || key !== this.readingKey || !lease.valid()) return;
      this.anchor = parseReadingAnchor(value); this.atEnd = this.anchor.atEnd; this.readingAnchorReady = true;
      this.scheduleReading();
    }).catch(() => {
      if (epoch !== this.readingEpoch || key !== this.readingKey || !lease.valid()) return;
      this.readingFailed = true;
      if (this.searchHitId) this.scheduleReading();
    });
  }
  private scheduleReading(): void {
    const epoch = this.readingEpoch;
    this.getUIContext().postFrameCallback(new ConversationFrame(() => { if (epoch === this.readingEpoch && this.active) this.restoreReading(); }));
  }
  private async restoreReading(): Promise<void> {
    if (this.active && this.searchHitId && this.searchedHit !== this.hitScope()) {
      if ((this.readingAnchorReady || this.readingFailed) && this.model.transcriptHistoryReady) await this.seekSearchHit(this.hitScope());
      return;
    }
    if (!this.active || !this.readingRestoring || !this.readingAnchorReady || this.readingFetching || this.readingFailed) return;
    if (this.model.selected.kind === 'conversation' && this.feedKey && !this.feed.hasLoaded && !this.feed.feedUnavailable) return;
    if (this.showWelcome()) { this.scroller.scrollEdge(Edge.Top); this.readingRestoring = false; return; }
    if (this.anchor.atEnd || !this.anchor.id) { this.scroller.scrollEdge(Edge.Bottom); this.readingRestoring = false; return; }
    const index = readingAnchorIndex(this.lines.map((line: ConversationLine) => line.readingId), this.anchor);
    if (index >= 0) {
      this.scroller.scrollToIndex(index + this.readingPrefix(), false, ScrollAlign.START, { extraOffset: LengthMetrics.vp(-this.anchor.offset) });
      this.atEnd = false; this.readingRestoring = false; return;
    }
    if (!this.model.hasMoreMessages && !this.feed.nextCursor) {
      if (!this.model.transcriptHistoryReady || !this.model.connected || this.feed.error) return;
      if (this.lines.length > 0) { this.scroller.scrollToIndex(this.readingPrefix(), false, ScrollAlign.START); this.readingRestoring = false; this.atEnd = false; }
      return;
    }
    if (this.feed.nextCursor) {
      if (this.readingCursors.has(this.feed.nextCursor)) { this.readingFailed = true; return; }
      this.readingCursors.add(this.feed.nextCursor);
    }
    const epoch = this.readingEpoch, before = this.readingProgress();
    this.readingFetching = true;
    try {
      const results = await Promise.all([this.model.hasMoreMessages ? this.model.loadOlder() : Promise.resolve(true), this.feed.nextCursor ? this.feed.loadMore() : Promise.resolve(true)]);
      if (epoch !== this.readingEpoch || !this.active) return;
      const after = this.readingProgress();
      if (before === after || results.some((ok: boolean) => ok === false)) this.readingFailed = true;
    } finally { if (epoch === this.readingEpoch) { this.readingFetching = false; if (!this.readingFailed) this.scheduleReading(); } }
  }
  private readingProgress(): string { return JSON.stringify([this.model.transcriptItems().map((item) => item.id), this.model.hasMoreMessages, this.feed.nextCursor]); }
  private visibleReadingAnchor(): ReadingAnchor | undefined {
    const prefix = this.readingPrefix(), index = Math.max(prefix, this.firstVisibleIndex), line = this.lines[index - prefix];
    if (!line) return undefined;
    try {
      const rect = this.scroller.getItemRect(index);
      if (Number.isFinite(rect.y) && Number.isFinite(rect.height) && rect.height > 0) return { id: line.readingId, offset: rect.y, atEnd: false };
    } catch (_) {}
    return undefined;
  }
  private async loadEarlier(): Promise<void> {
    if (!this.active || this.readingLoading || this.readingRestoring) return;
    const anchor = this.visibleReadingAnchor(), owner = this.model, generation = owner.connectionGeneration(), scope = owner.scope(), target = owner.target();
    const epoch = this.readingEpoch, key = this.readingKey, motion = this.readingMotion;
    const valid = (): boolean => this.active && this.model === owner && owner.owns(generation, scope) && owner.target() === target && this.readingEpoch === epoch && this.readingKey === key && this.readingMotion === motion;
    this.atEnd = false; this.readingLoading = true;
    try {
      await Promise.all([owner.hasMoreMessages ? owner.loadOlder() : Promise.resolve(true), this.feed.nextCursor ? this.feed.loadMore() : Promise.resolve(true)]);
      if (anchor && valid()) this.getUIContext().postFrameCallback(new ConversationFrame(() => {
        if (!valid()) return;
        const index = readingAnchorIndex(this.lines.map((line: ConversationLine) => line.readingId), anchor);
        if (index < 0) return;
        this.scroller.scrollToIndex(index + this.readingPrefix(), false, ScrollAlign.START, { extraOffset: LengthMetrics.vp(-anchor.offset) });
        this.anchor = anchor; this.atEnd = false;
      }));
    } finally { this.readingLoading = false; }
  }
  private retryReading(): void {
    if (!this.readingAnchorReady) { this.readingKey = ''; this.syncReading(); }
    else { this.readingCursors.clear(); this.readingFailed = false; this.scheduleReading(); }
  }
  private recordReading(start: number): void {
    if (!this.active || this.readingRestoring) return;
    this.atEnd = this.scroller.isAtEnd();
    this.updateJumpAvailability();
    const prefix = this.readingPrefix(), index = Math.max(prefix, start), line = this.lines[index - prefix];
    if (!line) return;
    try { const rectangle = this.scroller.getItemRect(index); this.anchor = { id: line.readingId, offset: rectangle.y, atEnd: this.atEnd }; } catch (_) {}
  }
  private trackScrollMotion(offset: number, source: ScrollSource): void {
    if (!offset || source === ScrollSource.SCROLLER || source === ScrollSource.SCROLLER_ANIMATION || source === ScrollSource.EDGE_EFFECT) return;
    this.readingMotion++;
    if (this.searchHitId && this.seekingHit) this.readingRestoring = false;
  }
  @Local atEnd: boolean = true;
  @Local jumpAvailable: boolean = false;
  private transcriptHeight: number = 0;
  private transcriptViewportChanged(height:number):void {
    if (!Number.isFinite(height) || height <= 0 || Math.abs(height-this.transcriptHeight) < 0.1) return;
    this.transcriptHeight = height;
    if (this.active && !this.readingRestoring) this.timelinePositionChanged();
  }
  private updateJumpAvailability(): void {
    if (this.atEnd || !this.lines.length || this.transcriptHeight <= 0) {
      this.jumpAvailable = false;
      return;
    }
    const index = this.readingPrefix() + this.lines.length - 1;
    if (this.lastVisibleIndex < index) {
      this.jumpAvailable = this.lastVisibleIndex >= 0;
      return;
    }
    // A visible last row can be taller than the viewport. iPhone allows a 100pt
    // glance above the latest reply before the arrow appears.
    try {
      const last = this.scroller.getItemRect(index);
      this.jumpAvailable = last.height > 0 && last.y + last.height - this.transcriptHeight > 100;
    } catch (_) { /* The row can leave the viewport between the index and geometry callbacks. */ }
  }
  private jumpToLatest(): void {
    this.readingMotion++;
    this.atEnd = true;
    this.anchor.atEnd = true;
    this.jumpAvailable = false;
    this.scroller.scrollEdge(Edge.Bottom);
  }
  @Local availableHeight: number = 800;
  @Local availableWidth: number = 360;
  @Local environment: NativeEnvironment = nativeEnvironment;
  // Keep native input, IME composition and transcript nodes stable for this view.
  // Re-entering the conversation may sample a newly available native capability.
  private useNativeHover: boolean = nativeEnvironment.nativeHoverAvailable;
  @Local controlsHeight: number = 80;
  @Local measuredDecision: string = '';
  @Local fieldFocused: boolean = false;
  @Local draftEditorOpen: boolean = false;
  @Local draftEditorFocused: boolean = false;
  private inlineField: TextAreaController = new TextAreaController();
  private pageField: TextAreaController = new TextAreaController();
  private draftEditorOwner: string = '';
  private selectionStart: number = 0;
  private selectionEnd: number = 0;
  private parkedStart: number = 0;
  private parkedEnd: number = 0;
  private refocusInline: boolean = false;
  @Local composerWidth: number = 360;
  @Local questions: QuestionWorkspace = new QuestionWorkspace();
  @Local questionInset: number = 0;
  private questionNodeId: string = 'chat.question.' + util.generateRandomUUID();
  private questionLayoutReady: boolean = false;
  private presentedQuestion: QuestionSheet | undefined;
  @Local chooser: ComposerOverlayWorkspace = new ComposerOverlayWorkspace();
  @Local chooserItems: ComposerChoice[] = [];
  @Local chooserHeights: number[] = [];
  private actionIntent: ComposerActionIntent | undefined;
  @Local editorLeft: number = 0;
  @Local editorTop: number = 0;
  @Local editorWidth: number = 0;
  @Local editorHeight: number = 0;
  @Local contentTop: number = 0;
  private editorAnchorId: string = 'chat.editor.' + util.generateRandomUUID();
  @Local attachmentSheet: boolean = false;
  @Local contextKind: string = '';
  @Local attachmentInset: number = 0;
  private attachmentNodeId: string = 'chat.attachment.' + util.generateRandomUUID();
  private attachmentLayoutReady: boolean = false;
  private leftActionWidth(): number { const count = (this.model.demo ? 0 : 1) + (this.showContextMenu ? 1 : 0); return count * Tokens.touch + Math.max(0, count - 1) * Tokens.gap; }
  private hasComposerAction(): boolean {
    return this.primaryStop() || this.sending || !!this.model.draft.trim() || this.attachments.attachments.length > 0 || !!this.model.sourceContext;
  }
  private trailingActionWidth(): number { return Tokens.touch * (this.hasComposerAction() ? 2 : 1); }
  private expandedComposer(): boolean {
    const leading = this.leftActionWidth();
    const available = this.composerWidth - leading - this.trailingActionWidth() - (leading ? 2 : 1) * Tokens.gap;
    return this.fieldFocused || this.model.draft.length > 0 || this.attachments.attachments.length > 0 ||
      !!this.model.sourceContext || this.environment.fontScale >= 1.5 || available < Tokens.minimumInput;
  }
  private verticalActions(): boolean { return this.environment.fontScale >= 1.5 || this.composerWidth < Tokens.composerNarrowActions; }
  // Native ordering keeps the same editor mounted. Compact slots use pixel precision, not twelfths of the row.
  private composerColumns(): number { return this.expandedComposer() ? 12 : Math.max(1, Math.round(this.getUIContext().vp2px(this.composerWidth))); }
  private sendColumns(): number { const width = this.trailingActionWidth() + (this.expandedComposer() ? 0 : Tokens.gap); return Math.ceil(width * this.composerColumns() / this.composerWidth); }
  private leftColumns(): number { const width = this.leftActionWidth(); return Math.ceil((width + (width && !this.expandedComposer() ? Tokens.gap : 0)) * this.composerColumns() / this.composerWidth); }
  private primaryStop(): boolean { return !this.model.decision && this.model.canCompose() && this.model.canStop() && !this.model.draft.trim() && !this.attachments.attachments.length; }
  @Monitor('active', 'model.selected', 'model.sessions', 'model.connected', 'model.draft', 'model.decision', 'attachments.attachments', 'model.interruptPendingKey')
  primaryStopChanged(): void { this.onPrimaryStop(this.active && this.primaryStop()); }
  private maxControlsHeight(): number {
    if (!this.model.decision) return this.availableHeight * 0.8;
    if (this.environment.keyboardHeightPx > 0) return this.availableHeight;
    const half = this.availableHeight * Tokens.decisionFraction;
    return this.model.decision.kind === 'question' ? Math.max(half, this.availableHeight - 72) : half;
  }
  private decisionToolsHeight(): number {
    return this.showContextMenu ? Tokens.touch + Tokens.gap : 0;
  }
  private measureAttachment(): void {
    if (!this.attachmentLayoutReady) return;
    const inset = nativeSheetInset(this.getUIContext(), this.attachmentNodeId, this.environment.safeBottomPx);
    if (Math.abs(inset - this.attachmentInset) > 0.1) this.attachmentInset = inset;
  }
  @Monitor('environment.safeBottomPx') safeBottomChanged(): void { this.measureAttachment(); this.measureQuestion(); }
  private async selectContext(row: ResourceRow): Promise<void> {
    const generation = this.model.connectionGeneration(), scope = this.model.scope(), target = this.model.target();
    if (this.sending || !this.attachmentSheet || !this.active) return;
    this.sending = true;
    try {
      if (await this.model.addDraftContext(row, target, generation, scope) && this.active && this.model.owns(generation, scope) && this.model.target() === target) this.attachmentSheet = false;
    } finally { this.sending = false; }
  }
  private chooserLayout(): ComposerPopupLayout {
    const heights = this.chooserHeights.length ? this.chooserHeights : [Tokens.input], context = this.getUIContext();
    const naturalHeight = context.px2vp(heights.reduce((sum: number, value: number) => sum + Math.round(context.vp2px(value)), 0) + Math.max(0, heights.length - 1) * Math.round(context.vp2px(Tokens.divider)));
    return composerPopupLayout({ left: this.editorLeft, top: this.editorTop, width: this.editorWidth, height: this.editorHeight },
      { width: this.getUIContext().px2vp(this.environment.windowWidthPx) || this.availableWidth,
        top: this.getUIContext().px2vp(this.environment.safeTopPx), bottom: this.contentTop + this.availableHeight },
      Math.max(...heights), naturalHeight, this.environment.fontScale,
      { ordinaryWidth: Tokens.popupWidth, maximumWidth: Tokens.readingMax, margin: Tokens.page });
  }
  private preserveInput(event: ClickEvent): boolean { return this.fieldFocused && event.source === SourceType.TouchScreen; }
  private assignChooser(session: ComposerOverlaySession): void {
    this.chooserItems = session.kind === 'attachments' ? [
      new ComposerChoice(session, 'photos', tr('photos'), Symbols.apps),
      new ComposerChoice(session, 'files', tr('files'), Symbols.document),
      new ComposerChoice(session, 'knowledge', tr('knowledge'), Symbols.document),
      new ComposerChoice(session, 'todo', tr('todos'), Symbols.calendar),
      new ComposerChoice(session, 'inbox', tr('inbox'), Symbols.inbox)
    ] : this.actionItems.map((item: SheetInfo, index: number) => new ComposerChoice(session, String(index), item.title, undefined, item.action));
    this.chooserHeights = this.chooserItems.map(() => Tokens.input * Math.max(1, this.environment.fontScale));
  }
  private openAttachmentChooser(event: ClickEvent): void {
    if (!this.composerAvailable() || this.sending || this.attachments.busy || this.model.draftLoading) return;
    this.actionIntent = undefined;
    this.contextKind = ''; this.attachmentSheet = false;
    const session = this.chooser.open(this.model, this.active, 'attachments', 0, this.preserveInput(event));
    if (session) this.assignChooser(session);
  }
  private composerOwnerKey(): string { return JSON.stringify([this.model.scope(), this.model.connectionGeneration(), this.model.target()]); }
  private composerAvailable(): boolean { return this.active && !this.model.decision && this.model.canCompose(); }
  private openComposerActions(event: ClickEvent): void {
    this.requestComposerActions(this.preserveInput(event));
  }
  private requestComposerActions(preserveInput: boolean): void {
    if (!this.composerAvailable() || !this.showContextMenu || this.nativeChooser('actions')) return;
    this.actionIntent = new ComposerActionIntent(this.composerOwnerKey(), preserveInput, this.actionsRequest);
    this.onShowActions();
  }
  private openFallbackActions(): void { if (!this.active || !this.showContextMenu || this.composerAvailable()) return; this.actionIntent = undefined; this.chooser.retire(); this.onShowActions(); }
  @Monitor('active', 'model.profile', 'model.selected', 'model.selected.raw', 'model.sessions', 'model.connected', 'model.decision', 'actionsVisible', 'actionsRequest', 'showContextMenu')
  chooserContextChanged(): void { this.syncChooser(); }
  private syncChooser(): void {
    if (!this.composerAvailable()) {
      const previous = this.chooser.session, intent = this.actionIntent; this.actionIntent = undefined; this.chooser.retire();
      this.attachmentSheet = false; this.contextKind = '';
      if (previous?.kind === 'actions') this.onHideActions(previous.request);
      if (intent && intent.owner === this.composerOwnerKey() && this.actionsVisible) this.onHideActions(this.actionsRequest);
      return;
    }
    if (!this.showContextMenu) {
      const previous = this.chooser.session, intent = this.actionIntent; this.actionIntent = undefined;
      if (previous?.kind === 'actions') { this.chooser.retire(); this.onHideActions(previous.request); }
      if (intent && intent.owner === this.composerOwnerKey() && this.actionsVisible) this.onHideActions(this.actionsRequest);
    }
    this.chooser.sync(this.model, true);
    const current = this.chooser.session;
    if (current?.kind === 'actions' && (!this.actionsVisible || current.request !== this.actionsRequest)) this.closeChooser(current);
    const intent = this.actionIntent;
    if (!intent) return;
    if (!this.actionsVisible || intent.owner !== this.composerOwnerKey()) { this.actionIntent = undefined; return; }
    if (intent.request < 0) { if (intent.previous === this.actionsRequest) return; intent.request = this.actionsRequest; }
    if (intent.request !== this.actionsRequest) { this.actionIntent = undefined; return; }
    if (this.chooser.shown && this.chooser.session) { this.closeChooser(this.chooser.session); }
    if (this.chooser.presenting) return;
    const session = this.chooser.open(this.model, true, 'actions', intent.request, intent.preserveInput);
    if (session) { this.assignChooser(session); this.actionIntent = undefined; }
  }
  private closeChooser(session: ComposerOverlaySession): void {
    if (this.chooser.close(session) && session.kind === 'actions') this.onHideActions(session.request);
  }
  private finishChooser(session: ComposerOverlaySession): void {
    if (this.chooser.finish(session) && session.kind === 'actions') this.onHideActions(session.request);
    this.syncChooser();
  }
  private chooseItem(choice: ComposerChoice): void {
    const session = choice.session;
    if (!this.composerAvailable()) { this.chooser.retire(); return; }
    this.chooser.sync(this.model, true);
    if (session.owner !== this.model || !this.chooser.current(session) || session.kind === 'actions' && (!this.actionsVisible || session.request !== this.actionsRequest)) return;
    if (session.kind === 'actions') { this.closeChooser(session); choice.action?.(); return; }
    const local = choice.key === 'photos' || choice.key === 'files';
    if (!local && !this.chooser.current(session, true)) return;
    this.closeChooser(session);
    if (choice.key === 'photos') this.attachments.choosePhotos();
    else if (choice.key === 'files') this.attachments.chooseFiles();
    else { this.contextKind = choice.key; this.attachmentSheet = true; }
  }
  private itemEnabled(choice: ComposerChoice): boolean {
    return this.composerAvailable() && choice.session.owner === this.model && this.chooser.current(choice.session, choice.session.kind === 'attachments' && !['photos', 'files'].includes(choice.key));
  }
  private nativeChooser(kind: string): boolean { const session = this.chooser.session; return this.chooser.shown && session?.kind === kind && !session.preserveInput; }
  private recordChooserHeight(choice: ComposerChoice, index: number, height: number): void {
    if (!this.chooser.current(choice.session) || this.chooserItems[index] !== choice || !Number.isFinite(height) || height <= 0 || Math.abs(height - this.chooserHeights[index]) <= 0.5) return;
    const next = this.chooserHeights.slice(); next[index] = height; this.chooserHeights = next;
  }
  private attachmentMenuOptions(): MenuOptions {
    const session = this.chooser.session;
    return { onWillAppear: () => { if (session) this.chooser.begin(session); }, onDidDisappear: () => { if (session) this.finishChooser(session); } };
  }
  private actionContextMenuOptions(): ContextMenuOptions {
    const session = this.chooser.session;
    return { preview: MenuPreviewMode.IMAGE,
      onWillAppear: () => { if (session) this.chooser.begin(session); },
      onDidDisappear: () => { if (session) this.finishChooser(session); } };
  }
  private chooserPopupOptions(): CustomPopupOptions {
    const session = this.chooser.session, layout = this.chooserLayout();
    return { builder: (): void => this.chooserContent(), focusable: false, mask: true, autoCancel: true,
      placement: layout.side === 'left' ? Placement.LeftTop : layout.side === 'right' ? Placement.RightTop : layout.side === 'below' ? Placement.BottomLeft : Placement.TopLeft,
      keyboardAvoidMode: KeyboardAvoidMode.DEFAULT, avoidTarget: AvoidanceMode.AVOID_AROUND_TARGET,
      onStateChange: (event: PopupStateChangeParam): void => { if (!session) return; if (event.isVisible) this.chooser.begin(session); else this.finishChooser(session); },
      onWillDismiss: (action: DismissPopupAction): void => { if (session) this.closeChooser(session); action.dismiss(); } };
  }
  @Builder chooserContent() {
    List() {
      ForEach(this.chooserItems, (choice: ComposerChoice, index: number) => {
        ListItem() {
          Row({ space: Tokens.inset }) {
            if (choice.symbol) SymbolGlyph(choice.symbol).fontSize(Tokens.symbolFont).fontColor([Colors.link]).width(Tokens.symbol)
            Text(choice.title).fontSize(Tokens.body).fontColor(Colors.ink).layoutWeight(1)
          }.constraintSize({ minHeight: Tokens.input }).padding({ left: Tokens.page, right: Tokens.page, top: Tokens.gap, bottom: Tokens.gap }).width('100%')
            .onAreaChange((_old: Area, area: Area) => this.recordChooserHeight(choice, index, Number(area.height)))
        }.enabled(this.itemEnabled(choice)).onClick(() => this.chooseItem(choice)).attributeModifier(choice)
          .id('chat.' + (choice.session.kind === 'attachments' ? 'attachment.choice.' : 'action.choice.') + choice.key)
      }, (choice: ComposerChoice) => choice.session.id + ':' + choice.key)
    }.width(this.chooserLayout().width).height(this.chooserLayout().maximumHeight).scrollBar(BarState.Auto)
      .divider({ strokeWidth: Tokens.divider, startMargin: Tokens.page, endMargin: Tokens.page, color: Colors.divider })
  }
  @Builder attachmentNativeMenu() {
    Menu() {
      ForEach(this.chooserItems, (choice: ComposerChoice) => {
        MenuItem({ content: choice.title, symbolStartIcon: choice.symbol ? new SymbolGlyphModifier(choice.symbol) : undefined })
          .constraintSize({ minHeight: Tokens.touch }).enabled(this.itemEnabled(choice)).onClick(() => this.chooseItem(choice))
          .id('chat.' + (choice.session.kind === 'attachments' ? 'attachment.choice.' : 'action.choice.') + choice.key)
      }, (choice: ComposerChoice) => choice.session.id + ':' + choice.key)
    }
  }
  @Builder attachmentContent() {
    Column() {
      if (this.contextKind) ContextPicker({ model: this.model, kind: this.contextKind, active: this.active && this.attachmentSheet,
        onSelect: (row: ResourceRow) => this.selectContext(row), onRetired: () => this.attachmentSheet = false })
    }.width('100%').height('100%').padding({ bottom: this.attachmentInset + Tokens.page }).id(this.attachmentNodeId)
      .onAreaChange(() => this.measureAttachment())
  }

  @Local foldContainerHeight: number = 0;
  private ordinarySplitRatio(): number {
    const firstMeasure = this.model.decision && this.measuredDecision !== this.model.decision.id;
    const secondary = firstMeasure ? this.maxControlsHeight() : Math.max(1, Math.min(this.controlsHeight, this.maxControlsHeight()));
    return Math.max(1, (this.foldContainerHeight || this.availableHeight) - secondary) / secondary;
  }
  @Monitor('model.messages', 'model.transcriptHistoryReady') messagesChanged(): void {
    this.syncTimeline(); this.refreshFeed(); this.syncRevision();
    this.timelinePositionChanged();
  }
  @Monitor('feed.items', 'feed.hasLoaded', 'feed.feedUnavailable') feedChanged(): void { this.syncTimeline(); this.timelinePositionChanged(); }
  @Monitor('active', 'model.selected', 'model.connected') feedContextChanged(): void { if (!this.active) { this.readingMotion++; this.saveReading(); } else this.syncReading(); this.syncFeed(); this.syncAttachments(); this.syncRevision(); }
  @Monitor('model.decision') decisionChanged(): void { this.measuredDecision = ''; this.refreshFeed(); this.scheduleReading(); }
  @Monitor('active', 'model.selected', 'model.profile', 'model.connected', 'model.personaQuestions')
  questionsContextChanged(): void { this.questions.sync(this.model, this.active); }
  @Monitor('model.personaQuestions') personaQuestionsChanged(): void {
    this.syncTimeline(); this.refreshFeed();
    this.timelinePositionChanged();
  }
  private timelinePositionChanged(): void {
    if (this.readingRestoring || this.searchHitId && this.searchedHit !== this.hitScope()) { this.scheduleReading(); return; }
    if (!this.active || this.showWelcome()) return;
    const epoch = this.readingEpoch, motion = this.readingMotion, follow = this.atEnd;
    this.getUIContext().postFrameCallback(new ConversationFrame(() => {
      if (!this.active || this.readingEpoch !== epoch || this.readingRestoring || this.showWelcome()) return;
      if (follow && this.atEnd && this.readingMotion === motion) this.scroller.scrollEdge(Edge.Bottom);
      else this.updateJumpAvailability();
    }));
  }
  private syncFeed(): void {
    if (!this.active || !this.model.connected || this.model.demo || this.model.selected.kind !== 'conversation') {
      if (this.feedTimer !== -1) clearTimeout(this.feedTimer); this.feedTimer = -1;
      this.feed.close(); this.feedKey = ''; this.syncTimeline(); return;
    }
    const key = JSON.stringify([this.model.connectionGeneration(), this.model.target()]);
    if (key === this.feedKey) return;
    this.feedKey = key; this.feed.open(this.model, this.model.selected).catch(() => { if (this.feedKey === key) this.feed.error = 'failed'; });
  }
  private refreshFeed(): void {
    if (!this.active || !this.feedKey || this.feedTimer !== -1) return;
    this.feedTimer = setTimeout(() => { this.feedTimer = -1; if (this.active && this.feedKey) this.feed.refresh(); }, 200);
  }
  private syncAttachments(): void {
    if (!this.active) this.attachmentSheet = false;
    if (!this.active && this.attachments.picking && !this.model.demo && this.model.isChat() && this.attachmentKey === this.model.target()) return;
    if (!this.active || !this.model.isChat() || this.model.demo) { this.attachmentKey = ''; this.attachments.close(); return; }
    // System pickers may pause the ability and reconnect its transport; the local draft keeps its owner.
    const key = this.model.target(); if (key === this.attachmentKey) return;
    const host = this.getUIContext().getHostContext(); if (!host) return;
    this.attachmentKey = key;
    const row = this.model.selected; this.attachments.busy = true;
    this.model.restoreLocalDelivery().then(async (): Promise<void> => {
      if (!this.active || this.attachmentKey !== key || this.model.target() !== key) return;
      await this.attachments.open(this.model, host as common.UIAbilityContext, row);
    }).catch(() => { if (this.attachmentKey === key) { this.attachments.errorKey = 'chat_attachment_storage_unavailable'; this.attachments.busy = true; } });
  }
  private primaryActionKey(): string {
    if (this.primaryStop()) return 'stop';
    if (this.model.stopPending() || this.sending || this.model.operationState === 'pending') return 'pending';
    return this.model.operationState === 'unknown' ? 'result_unknown' : 'send';
  }
  private offersDraftEditor(): boolean {
    return this.fieldFocused || (this.model.draft.length > 0 && (this.model.draft.includes('\n') ||
      this.editorHeight >= 5.5 * Tokens.lineHeight * Math.max(1, this.environment.fontScale) + Tokens.section));
  }
  private draftEditorCurrent(): boolean {
    return !!this.draftEditorOwner && this.sendCurrent() && this.composerAvailable() &&
      this.draftEditorOwner === this.composerOwnerKey();
  }
  private rememberSelection(start: number, end: number): void {
    if (start >= 0 && end >= start) { this.selectionStart = start; this.selectionEnd = end; }
  }
  private parkSelection(): void { this.parkedStart = this.selectionStart; this.parkedEnd = this.selectionEnd; }
  private restoreSelection(field: TextAreaController): void {
    const length = this.model.draft.length;
    field.setTextSelection(Math.min(this.parkedStart, length), Math.min(this.parkedEnd, length));
  }
  private finishPageInput(): void { this.parkSelection(); this.pageField.stopEditing(); }
  private expandDraftEditor(): void {
    if (!this.sendCurrent() || !this.composerAvailable() || this.sending || this.model.draftLoading) return;
    this.draftEditorOwner = this.composerOwnerKey();
    this.refocusInline = this.fieldFocused;
    this.parkSelection();
    this.inlineField.stopEditing();
    this.draftEditorOpen = true;
  }
  private focusDraftPage(): void {
    const owner = this.draftEditorOwner;
    this.getUIContext().postFrameCallback(new ConversationFrame(() => {
      if (this.draftEditorOpen && this.draftEditorOwner === owner && this.draftEditorCurrent()) {
        this.getUIContext().getFocusController().requestFocus('chat.editor.input');
        this.restoreSelection(this.pageField);
      }
    }));
  }
  private draftPageGone(): void {
    const owner = this.draftEditorOwner;
    const focus = this.refocusInline && this.draftEditorCurrent();
    this.draftEditorFocused = false; this.refocusInline = false; this.draftEditorOwner = '';
    if (focus) this.getUIContext().postFrameCallback(new ConversationFrame(() => {
      if (!this.draftEditorOpen && this.sendCurrent() && this.composerAvailable() && owner === this.composerOwnerKey()) {
        this.getUIContext().getFocusController().requestFocus('chat.input');
        this.restoreSelection(this.inlineField);
      }
    }));
  }
  @Monitor('active', 'model.selected', 'model.profile', 'model.connected', 'model.decision')
  draftEditorContextChanged(): void {
    if (this.draftEditorOpen && !this.draftEditorCurrent()) { this.refocusInline = false; this.draftEditorOpen = false; }
  }
  private primaryEnabled(): boolean {
    return this.sendCurrent() && (this.primaryStop() || this.model.canSend() && !this.model.stopPending() && !this.sending &&
      !this.attachments.busy && !this.model.draftLoading && (this.model.draft.trim().length > 0 || this.attachments.attachments.length > 0) &&
      this.model.operationState !== 'pending' && this.model.operationState !== 'unknown');
  }
  private dictationOwner(): string { return JSON.stringify([this.model.connectionGeneration(), this.model.scope(), this.model.target()]); }
  private sendCurrent(): boolean { return this.mounted && this.active && this.admission(); }
  private async sendDraft(): Promise<void> {
    if (!this.sendCurrent() || this.sending || this.attachments.busy || !this.model.canSend()) return;
    const owner = this.model, generation = owner.connectionGeneration(), scope = owner.scope(), target = owner.target(), draft = owner.draft, context = owner.sourceContext;
    const current = (): boolean => this.sendCurrent() && this.model === owner && owner.owns(generation, scope) && owner.target() === target;
    if (!this.attachments.attachments.length) { await owner.send(current); return; }
    this.sending = true;
    try {
      const prepared = await this.attachments.prepareSend(owner.prepareMessageText(draft), current);
      await owner.sendPrepared(prepared, draft, context, current);
    } catch (error) { if (current()) this.attachments.errorKey = error instanceof Error && error.message.startsWith('chat_attachment_') ? error.message : 'chat_attachment_send_failed'; }
    finally { this.sending = false; }
  }
  private async preview(item: ChatAttachment): Promise<void> {
    const host = this.getUIContext().getHostContext(), key = this.attachmentKey; if (!host) return;
    try { await previewAttachment(host as common.UIAbilityContext, item.localPath, item.name, item.mime); }
    catch (_) { if (key === this.attachmentKey) this.attachments.errorKey = 'preview_unavailable'; }
  }
  private syncRevision(): void {
    if (!this.active || !this.model.connected || !this.model.isChat() || this.model.demo) {
      if (this.revisionContextKey) this.invalidateRevisionMenuContext();
      this.revisionContextKey = '';
      this.revisions.hide(); this.revisionKey = ''; return;
    }
    const key = JSON.stringify([this.model.connectionGeneration(), this.model.scope(), this.model.target()]);
    if (key !== this.revisionContextKey) {
      this.revisionContextKey = key;
      this.invalidateRevisionMenuContext();
    }
    if (key !== this.revisionKey) {
      this.revisionKey = key; this.revisionResult = ''; this.revisionCacheError = '';
      this.revisions.open(this.model, this.model.selected, this.model.transcriptItems());
    }
    else this.revisions.updateItems(this.model.transcriptItems());
  }
  private invalidateRevisionMenuContext(): void { this.revisionVisibilityRevision++; this.revisionMenuLease = undefined; }
  private captureRevisionMenuLease(message: ChatMessage): void {
    this.revisionMenuLease = undefined;
    const id = message.itemId, target = this.model.target(), generation = this.model.connectionGeneration(), scope = this.model.scope();
    if (!id || message.ownerTarget !== target || !this.active || !this.model.connected || this.model.demo || !this.model.isChat() ||
      !this.model.owns(generation, scope) || !this.revisions.isCurrent()) return;
    const fingerprint = this.revisions.fingerprint(id);
    if (!fingerprint || (!this.revisions.canFork(id) && !this.revisions.canRewind(id))) return;
    this.revisionMenuLease = new ConversationRevisionLease(this.model, generation, scope, target, this.revisionVisibilityRevision,
      id, fingerprint, message.ownerTarget);
  }
  private revisionOwnerCurrent(lease: ConversationRevisionLease, message: ChatMessage): boolean {
    const generation = this.model.connectionGeneration(), scope = this.model.scope(), target = this.model.target();
    const currentSession = this.revisions.isCurrent() && this.model.owns(lease.generation, lease.scope);
    return lease.owns(this.model, this.active, this.model.connected, this.model.demo, generation, scope, target,
      this.revisionVisibilityRevision, message.itemId, message.ownerTarget, currentSession);
  }
  private revisionLeaseCurrent(lease: ConversationRevisionLease, message: ChatMessage): boolean {
    const generation = this.model.connectionGeneration(), scope = this.model.scope(), target = this.model.target();
    const currentSession = this.revisions.isCurrent() && this.model.owns(lease.generation, lease.scope);
    return lease.matches(this.model, this.active, this.model.connected, this.model.demo, generation, scope, target,
      this.revisionVisibilityRevision, message.itemId, this.revisions.fingerprint(lease.itemId), message.ownerTarget, currentSession);
  }
  private takeRevisionMenuLease(message: ChatMessage): ConversationRevisionLease | undefined {
    const lease = this.revisionMenuLease;
    this.revisionMenuLease = undefined;
    return lease && this.revisionLeaseCurrent(lease, message) ? lease : undefined;
  }
  private openForkConfirmation(message: ChatMessage): void {
    const lease = this.takeRevisionMenuLease(message);
    if (lease) this.confirmFork(message, lease);
  }
  private openRewindConfirmation(message: ChatMessage, restore: SessionRevisionRestore): void {
    const lease = this.takeRevisionMenuLease(message);
    if (lease) this.confirmRewind(message, restore, lease);
  }
  private confirmFork(message: ChatMessage, lease: ConversationRevisionLease): void {
    const id = lease.itemId, fingerprint = lease.fingerprint;
    if (!this.revisionLeaseCurrent(lease, message) || !this.revisions.canFork(id)) return;
    this.getUIContext().showAlertDialog({ title: tr('branch_chat'), message: this.model.selected.title + '\n' + this.model.machineName(this.model.selected.machine),
      primaryButton: { value: tr('cancel'), action: () => {} }, secondaryButton: { value: tr('branch_chat'), action: () => {
        if (!this.revisionLeaseCurrent(lease, message) || !this.revisions.canFork(id)) return;
        this.revisions.fork(id, fingerprint).then((row: ResourceRow | undefined) => {
          if (row && this.revisionOwnerCurrent(lease, message)) this.onOpenResource(row);
        });
      } } });
  }
  private confirmRewind(message: ChatMessage, restore: SessionRevisionRestore, lease: ConversationRevisionLease): void {
    const id = lease.itemId, fingerprint = lease.fingerprint;
    if (!this.revisionLeaseCurrent(lease, message) || !this.revisions.canRewind(id)) return;
    this.getUIContext().showAlertDialog({ title: tr(restore === 'code_and_conversation' ? 'rewind_code_chat' : 'rewind_chat'),
      message: this.model.machineName(this.model.selected.machine) + '\n' + this.model.selected.path + '\n\n' + tr(restore === 'code_and_conversation' ? 'rewind_code_consequence' : 'rewind_chat_consequence') + '\n\n' + message.text,
      primaryButton: { value: tr('cancel'), action: () => {} }, secondaryButton: { value: tr('confirm'), action: () => {
        if (!this.revisionLeaseCurrent(lease, message) || !this.revisions.canRewind(id)) return;
        this.revisionResult = ''; this.revisionCacheError = '';
        this.revisions.rewind(id, restore, fingerprint).then((outcome: SessionRevisionOutcome) => this.rewindFinished(outcome, lease, message));
      } } });
  }
  private async rewindFinished(outcome: SessionRevisionOutcome, lease: ConversationRevisionLease, message: ChatMessage): Promise<void> {
    if (!this.revisionOwnerCurrent(lease, message) || outcome.state !== 'done') return;
    this.revisionResult = outcome.codeRestored ? 'rewind_code_done' : 'rewind_chat_done';
    if (!outcome.inputCleared) this.revisionResult = 'rewind_input_kept';
    else if (outcome.canFillDraft) {
      await this.restoreRevisionDraft(outcome, lease, message);
      if (this.revisionOwnerCurrent(lease, message)) this.model.loadChat(this.model.selected).catch(() => {});
      return;
    }
    else if (outcome.draftUnchanged && outcome.prompt) {
      this.getUIContext().showAlertDialog({ title: tr('restore_draft'), message: tr('replace_draft'), primaryButton: { value: tr('cancel'), action: () => {
        if (this.revisionOwnerCurrent(lease, message)) this.model.loadChat(this.model.selected).catch(() => {});
      } }, secondaryButton: { value: tr('confirm'), action: () => {
        void this.finishRewindDraftConfirmation(outcome, lease, message);
      } } });
      return;
    }
    if (this.revisionOwnerCurrent(lease, message)) this.model.loadChat(this.model.selected).catch(() => {});
  }
  private async restoreRevisionDraft(outcome: SessionRevisionOutcome, lease: ConversationRevisionLease, message: ChatMessage): Promise<void> {
    await restoreAcknowledgedSessionDraft(this.model, outcome, (): boolean => this.revisionOwnerCurrent(lease, message), (): void => {
      this.revisionCacheError = 'cache_unavailable';
    });
  }
  private async finishRewindDraftConfirmation(outcome: SessionRevisionOutcome, lease: ConversationRevisionLease, message: ChatMessage): Promise<void> {
    if (this.revisionOwnerCurrent(lease, message)) await this.restoreRevisionDraft(outcome, lease, message);
    if (this.revisionOwnerCurrent(lease, message)) this.model.loadChat(this.model.selected).catch(() => {});
  }
  private async copyMessage(message: ChatMessage): Promise<void> {
    if (message.ownerTarget !== this.model.target()) return;
    const generation = this.model.connectionGeneration(), scope = this.model.scope(), target = this.model.target();
    try { await pasteboard.getSystemPasteboard().setData(pasteboard.createData(pasteboard.MIMETYPE_TEXT_PLAIN, message.text)); }
    catch (_) { if (this.model.owns(generation, scope) && this.model.target() === target) this.model.error = 'failed'; }
  }
  private messageMenuBuilder(entry: RepeatItem<ConversationLine>): CustomBuilder {
    return (): void => { const message = entry.item.message; if (message) this.messageMenu(message); };
  }
  @Builder messageMenu(message: ChatMessage) {
    Menu() {
      if (message.text) MenuItem({ content: tr('copy') }).onClick(() => this.copyMessage(message))
      if (message.text) MenuItem({ content: tr('use_as_context') }).onClick(() => this.model.useMessageContext(message))
      if (message.itemId && message.ownerTarget === this.model.target() && this.revisions.canFork(message.itemId))
        MenuItem({ content: tr('branch_chat') }).onClick(() => this.openForkConfirmation(message))
      if (message.itemId && message.ownerTarget === this.model.target() && this.revisions.canRewind(message.itemId)) {
        MenuItem({ content: tr('rewind_chat') }).onClick(() => this.openRewindConfirmation(message, 'conversation'))
        MenuItem({ content: tr('rewind_code_chat') }).onClick(() => this.openRewindConfirmation(message, 'code_and_conversation'))
      }
    }.onAppear(() => this.captureRevisionMenuLease(message))
  }
  private syncTimeline(): void {
    this.imageGallery = conversationImageEntries(this.model.messages, this.model.target());
    this.imageIndices.clear();
    this.imageGallery.forEach((entry: MessageImageGalleryEntry, index: number): void => { this.imageIndices.set(entry.key, index); });
    const messages = new Map<string, ChatMessage>(), cards = new Map<string, Record<string, Object>>();
    const inputs: TimelineMessage[] = this.model.messages.map((message: ChatMessage): TimelineMessage => {
      messages.set(message.id, message); return { id: message.id, role: message.role, at: message.at };
    });
    const activities: TimelineCard[] = this.feed.items.filter((item: Record<string, Object>) => !['task', 'today', 'brief'].includes(Wire.text(item, 'kind')))
      .map((item: Record<string, Object>): TimelineCard => { const id = Wire.text(item, 'id'), stamp = Date.parse(Wire.text(item, 'at')); cards.set(id, item); return { id: id, at: Number.isFinite(stamp) ? stamp : 0 }; });
    const lines = mergeConversationTimeline(inputs, activities).map((entry) => new ConversationLine(entry.id, messages.get(entry.messageId), cards.get(entry.cardId)));
    this.model.personaQuestions.forEach((request: Decision) => lines.push(new ConversationLine('persona-question:' + request.id, undefined, undefined, request)));
    reconcileTimelineRows(this.lines, lines);
  }
  aboutToAppear(): void {
    this.mounted = true;
    this.questions.sync(this.model, this.active); this.chooser.sync(this.model, this.active);
    this.syncReading(); this.syncFeed(); this.syncAttachments(); this.syncTimeline(); this.syncRevision(); this.primaryStopChanged();
    const target = this.model.target(); this.model.loadView('work:' + target).then((value: string) => {
      if (target === this.model.target() && value) { try { this.expanded = Wire.strings(Wire.parse(value), 'expanded'); } catch (_) {} }
    });
  }
  aboutToDisappear(): void {
    this.seekingHit = ''; this.clearSearchHighlight();
    this.mounted = false;
    this.questions.retire(); this.actionIntent = undefined; this.chooser.retire();
    this.onPrimaryStop(false); this.saveReading(); this.readingEpoch++;
    if (this.feedTimer !== -1) clearTimeout(this.feedTimer); this.feedTimer = -1; this.feed.close(); this.feedKey = '';
    this.attachmentKey = ''; this.attachments.close(); this.invalidateRevisionMenuContext(); this.revisionContextKey = '';
    this.revisions.hide(); this.revisionKey = '';
  }
  private toggle(id: string): void {
    this.expanded = this.expanded.includes(id) ? this.expanded.filter((value: string) => value !== id) : this.expanded.concat([id]);
    this.model.saveView('work:' + this.model.target(), JSON.stringify(Wire.payload(['expanded'], [this.expanded]))).catch(() => this.model.error = 'failed');
  }
  private confirmChecked(id: string): void {
    this.getUIContext().showAlertDialog({ title: tr('confirm'), message: tr('checked_no_effect'), primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('confirm'), action: () => this.model.acknowledgeWrite(id) } });
  }
  private restoreDraft(message: ChatMessage): void {
    const target = this.model.target(), generation = this.model.connectionGeneration(), scope = this.model.scope();
    if (!this.model.draft && !this.model.sourceContext) { this.model.restoreMessageDraft(message.localOperation); return; }
    this.getUIContext().showAlertDialog({ title: tr('restore_draft'), message: tr('replace_draft'), primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr('confirm'), action: () => { if (this.model.owns(generation, scope) && this.model.target() === target) this.model.restoreMessageDraft(message.localOperation); } } });
  }
  private imageKey(message: ChatMessage, kind: string, source: string, index: number = 0): string {
    return conversationImageKey(message.ownerTarget, message.id, kind, source, index);
  }
  private imageIndex(message: ChatMessage, kind: string, source: string, index: number = 0): number {
    return this.imageIndices.get(this.imageKey(message, kind, source, index)) ?? -1;
  }
  private editReceivedImage(image: LoadedMessageImage): void {
    const attachments = this.attachments;
    if (!this.active || this.sending || this.model.demo || image.target !== this.model.target() || this.attachmentKey !== image.target) return;
    void attachments.editReceivedImage(image.data, image.mime, image.target, image.scope, image.generation, image.hub);
  }
  @Builder message(entry: RepeatItem<ConversationLine>) {
    Column({ space: Tokens.gap }) {
      if (entry.item.message!.work) {
        Button({ type: ButtonType.Normal }) {
          Row({ space: 7 }) {
            if (entry.item.message!.running) LoadingProgress().width(14).height(14).color(Colors.working)
            else SymbolGlyph($r('sys.symbol.list_bullet')).fontSize(15).fontColor([Colors.secondary])
            Text(tr(entry.item.message!.running ? 'working' : 'work_summary')).fontSize(14).fontWeight(FontWeight.Medium).fontColor(Colors.secondary)
            if (entry.item.message!.toolCount > 0) Text(tr('tool') + ' ' + String(entry.item.message!.toolCount)).fontSize(12).fontColor(Colors.secondary)
            if (entry.item.message!.thoughtCount > 0) Text(tr('phone_chat_thoughts') + ' ' + String(entry.item.message!.thoughtCount)).fontSize(12).fontColor(Colors.secondary)
            SymbolGlyph(this.expanded.includes(entry.item.message!.id) ? Symbols.down : Symbols.expand).fontSize(12).fontColor([Colors.secondary])
          }.constraintSize({ minHeight: 32 })
        }.padding({ left: 10, right: 10 }).borderRadius(12).backgroundColor(Colors.control)
          .accessibilityText(tr('work_summary') + ' · ' + tr(this.expanded.includes(entry.item.message!.id) ? 'agent_collapse' : 'agent_expand'))
          .onClick(() => { this.toggle(entry.item.message!.id); if (this.expanded.includes(entry.item.message!.id)) this.model.expandWork(entry.item.message!); })
          .id('chat.work.' + entry.item.message!.id)
        if (this.expanded.includes(entry.item.message!.id)) {
          Text(entry.item.message!.work).fontFamily('monospace').fontSize(14).fontColor(Colors.secondary).lineHeight(21)
            .copyOption(CopyOptions.LocalDevice).padding(12).width('100%').backgroundColor(Colors.control).borderRadius(12)
        }
      }
      if (entry.item.message!.localImageCount > 0) Text(tr('photos') + ' · ' + entry.item.message!.localImageCount).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary'))
      if (entry.item.message!.source) Text(entry.item.message!.source).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).width('100%')
      if (entry.item.message!.ownerTarget === this.model.target()) ForEach(entry.item.message!.imagePaths, (path: string, index: number) => {
        TranscriptImage({ model: this.model, remotePath: path, active: this.active,
          sourceKey: this.imageKey(entry.item.message!, 'path', path, index), galleryEntries: this.imageGallery,
          galleryIndex: this.imageIndex(entry.item.message!, 'path', path, index), onEdit: (image: LoadedMessageImage) => this.editReceivedImage(image) })
      }, (path: string, index: number) => this.imageKey(entry.item.message!, 'path', path, index))
      ForEach(entry.item.message!.filePaths, (path: string) => { Button(path.split('/').pop() || path, { buttonStyle: ButtonStyleMode.TEXTUAL }).height(Tokens.touch).onClick(() => this.onOpenFile(path)) }, (path: string) => path)
      if (entry.item.message!.imageId && entry.item.message!.ownerTarget === this.model.target()) TranscriptImage({ model: this.model, itemId: entry.item.message!.imageId, active: this.active,
        sourceKey: this.imageKey(entry.item.message!, 'item', entry.item.message!.imageId), galleryEntries: this.imageGallery,
        galleryIndex: this.imageIndex(entry.item.message!, 'item', entry.item.message!.imageId), onEdit: (image: LoadedMessageImage) => this.editReceivedImage(image) })
      if (entry.item.message!.text) {
        WithTheme({ colorMode: this.environment.dark ? ThemeColorMode.DARK : ThemeColorMode.LIGHT, theme: nativeMessageTheme(this.look, this.model.style, entry.item.message!.role === 'user', this.environment.dark) }) {
        MessageSurface({ own: entry.item.message!.role === 'user', personaReply: this.model.selected.kind === 'conversation' && entry.item.message!.role === 'assistant', maximum: entry.item.message!.role === 'user' ? Math.max(Tokens.touch, Math.min(this.availableWidth, Tokens.readingMax) - Tokens.page * 2 - Tokens.touch) : Tokens.readingMax }) {
          if (entry.item.message!.role === 'assistant' && this.model.selected.kind === 'conversation') AssistantMessage({ model: this.model, feed: this.feed, message: entry.item.message!, active: this.active, onLink: (address: string) => this.onLink(address), onOpenResource: (row: ResourceRow) => this.onOpenResource(row) })
          else MarkdownText({ text: entry.item.message!.text, chatReply: entry.item.message!.role === 'assistant', fitContent: entry.item.message!.role === 'user', onLink: (address: string) => this.onLink(address) })
        }
        }
      }
      if (entry.item.message!.held) this.heldStatus(entry.item.message!)
      if (entry.item.message!.deliveryState && entry.item.message!.deliveryState !== 'done' && !entry.item.message!.failed) Text(tr(entry.item.message!.deliveryState === 'unknown' ? 'result_unknown' : entry.item.message!.deliveryState)).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary'))
      if (entry.item.message!.failed) Text(tr('failed')).fontColor($r('app.color.persona_danger')).fontSize(Tokens.meta)
      if (entry.item.message!.localOperation && this.model.canCheckLocalMessage(entry.item.message!.localOperation)) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('checked')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('checked', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('checked')).bindTips(tr('checked')).onClick(() => this.confirmChecked(entry.item.message!.localOperation))
      if (entry.item.message!.localOperation && this.model.canRestoreMessage(entry.item.message!.localOperation)) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('restore_draft')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('restore_draft', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('restore_draft')).bindTips(tr('restore_draft')).onClick(() => this.restoreDraft(entry.item.message!))
    }.width('100%').alignItems(entry.item.message!.role === 'user' ? HorizontalAlign.End : HorizontalAlign.Start).padding({ left: Tokens.page, right: Tokens.page, top: 3, bottom: entry.item.message!.work ? 1 : 4 })
      .bindContextMenu(this.messageMenuBuilder(entry), ResponseType.LongPress, { preview: MenuPreviewMode.IMAGE })
  }
  @Builder actionsMenu() {
    Menu() {
      ForEach(this.actionItems, (item: SheetInfo) => {
        MenuItem({ content: item.title }).constraintSize({ minHeight: Tokens.touch }).onClick(() => item.action())
      }, (_item: SheetInfo, index: number) => String(this.actionsRequest) + ':' + String(index))
    }
  }
  private confirmHeldDelete(message: ChatMessage): void {
    const model = this.model, generation = model.connectionGeneration(), scope = model.scope(), visibility = this.revisionVisibilityRevision;
    const current = (): boolean => this.sendCurrent() && this.model === model && model.owns(generation, scope) && this.revisionVisibilityRevision === visibility;
    if (!current() || !model.canActOnHeld(message, false)) return;
    const photos = message.localImageCount > 0 ? tr('photos') + ' · ' + message.localImageCount : '';
    const body = [message.text, photos].filter((part: string) => !!part).join('\n');
    this.getUIContext().showAlertDialog({ title: tr('agent_delete_held_title'), message: body,
      primaryButton: { value: tr('cancel'), defaultFocus: true, action: () => {} },
      secondaryButton: { value: tr('delete'), action: () => model.actOnHeld(message, false, current) } });
  }
  @Builder heldStatus(message: ChatMessage) {
    Column({ space: Tokens.fine }) {
      Text(message.held!.line || tr(message.held!.state === 'failed' ? 'agent_delivery_failed' : 'agent_delivery_waiting').replace('{computer}', () => message.held!.computer))
        .fontSize(Tokens.meta).fontColor(message.held!.state === 'failed' ? Colors.danger : Colors.working)
        .constraintSize({ maxWidth: '100%' }).id('chat.held.status.' + message.held!.id)
      if (this.model.heldRetryPending(message)) Text(tr('agent_delivery_updating'))
        .fontSize(Tokens.meta).fontColor(Colors.secondary).constraintSize({ maxWidth: '100%' }).id('chat.held.update.' + message.held!.id)
      Flex({ wrap: FlexWrap.Wrap, justifyContent: FlexAlign.End }) {
        if (this.model.offersHeldAction(message, true)) Button(tr('retry')).buttonStyle(ButtonStyleMode.TEXTUAL)
          .constraintSize({ minHeight: Tokens.touch, minWidth: Tokens.touch }).enabled(this.model.canActOnHeld(message, true))
          .onClick(() => this.model.actOnHeld(message, true, () => this.sendCurrent())).id('chat.held.retry.' + message.held!.id)
        if (this.model.offersHeldAction(message, false)) Button(tr('cancel')).buttonStyle(ButtonStyleMode.TEXTUAL)
          .constraintSize({ minHeight: Tokens.touch, minWidth: Tokens.touch }).enabled(this.model.canActOnHeld(message, false))
          .onClick(() => this.confirmHeldDelete(message)).id('chat.held.cancel.' + message.held!.id)
      }.width('100%')
    }.alignItems(HorizontalAlign.End).width('100%')
  }
  private agentNotice(): string {
    return tr(this.model.agentInputNotice()).replace('{computer}', () => this.model.machineName(this.model.selected.machine));
  }
  private actionMenuOptions(request: number): MenuOptions {
    return { onDisappear: () => this.onHideActions(request) };
  }
  @Builder readingContent() {
    Column() {
      if (this.revisions.forkState === 'branching' || this.revisions.rewindState === 'rewinding') LoadingProgress().width(Tokens.touch).height(Tokens.touch)
      if (this.revisionResult || this.revisions.forkError || this.revisions.rewindError) Text(tr(this.revisions.forkError || this.revisions.rewindError || this.revisionResult)).fontSize(Tokens.meta).padding(Tokens.inset).width('100%')
      if (this.revisionCacheError) Text(tr(this.revisionCacheError)).fontSize(Tokens.meta).fontColor($r('app.color.persona_danger')).padding(Tokens.inset).width('100%')
      if (this.model.error) {
        Row() {
          Text(tr(this.model.error)).fontSize(Tokens.meta).layoutWeight(1)
          Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('retry')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('retry', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('retry')).bindTips(tr('retry')).onClick(() => this.model.refresh())
        }.padding(Tokens.inset)
      }
      if (this.model.unresolvedWrites.length) {
        Row({ space: Tokens.gap }) {
          Text(tr('result_unknown')).layoutWeight(1)
          Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('refresh')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('refresh', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('refresh')).bindTips(tr('refresh')).onClick(() => this.model.reconcileWrites())
          if (this.model.unresolvedWrites.some((record) => record.state === 'unknown' && !record.requiresReceipt))
            Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('checked')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('checked', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('checked')).bindTips(tr('checked')).onClick(() => { const record = this.model.unresolvedWrites.find((item) => item.state === 'unknown' && !item.requiresReceipt); if (record) this.confirmChecked(record.id); })
        }.padding(Tokens.inset)
      }
      Stack({ alignContent: Alignment.BottomEnd }) {
        this.transcriptList()
        if (this.jumpAvailable) {
          Button({ type: ButtonType.Circle }) {
            SymbolGlyph(IconActions.symbol('latest')).fontSize(18).fontColor([Colors.ink])
          }.width(40).height(40).padding(0).backgroundColor(Colors.surface)
            .border({ width: 0.7, color: Colors.divider, radius: 20 }).shadow(ShadowStyle.OUTER_DEFAULT_XS)
            .responseRegion({ x: -3, y: -3, width: 46, height: 46 }).margin({ right: Tokens.page, bottom: Tokens.gap })
            .accessibilityText(tr('latest')).bindTips(tr('latest')).id('chat.latest')
            .onClick(() => this.jumpToLatest())
        }
      }.width('100%').layoutWeight(1)
    }.width('100%').constraintSize({ maxWidth: Tokens.readingMax }).height('100%')
  }
  @Builder transcriptList() {
    List({ scroller: this.scroller, space: Tokens.gap }) {
      if (this.readingFailed) ListItem() { Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('retry')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('retry', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('retry')).bindTips(tr('retry')).onClick(() => this.retryReading()) }
      if (this.feed.error && this.feed.error !== this.model.error) ListItem() {
        Row({ space: Tokens.gap }) { Text(tr(this.feed.error)).fontSize(Tokens.meta).layoutWeight(1); Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('retry')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('retry', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('retry')).bindTips(tr('retry')).onClick(() => this.feed.refresh()) }.padding(Tokens.inset)
      }
      if (this.showWelcome()) ListItem() {
          AssistantWelcome({ model: this.model, canAsk: this.welcomeCanAsk(), onPlace: (name: string) => this.onPlace(name), onAsk: (prompt: string) => {
            if (this.sendCurrent() && this.welcomeCanAsk()) {
              this.model.updateDraft(prompt); this.sendDraft();
            }
          } })
      }
      if (this.model.hasMoreMessages || this.feed.nextCursor) ListItem() { Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('load_more')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('load_more', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('load_more')).bindTips(tr('load_more')).enabled(!this.readingLoading).onClick(() => this.loadEarlier()) }
      Repeat(this.lines).each((entry: RepeatItem<ConversationLine>) => {
        ListItem() {
          if (entry.item.message?.role === 'recap') {
            ConversationRecap({ model: this.model, message: entry.item.message!, active: this.active,
              onLink: (address: string) => this.onLink(address) })
          } else if (entry.item.message) this.message(entry)
          else if (entry.item.card) AssistantCards({ model: this.model, feed: this.feed, active: this.active && this.model.connected, onOpenResource: (row: ResourceRow) => this.onOpenResource(row), item: entry.item.card, onOpenCard: (itemId: string, kind: string) => this.onOpenCard(itemId, kind), onOpenSession: (row: ResourceRow) => this.onOpenResource(row),
            onOpenTodo: (row: ResourceRow) => this.onOpenResource(row), onOpenInbox: (row: ResourceRow) => this.onOpenResource(row), onLink: (address: string) => this.onLink(address) })
          else if (entry.item.decision) this.questionCard(entry)
        }.padding({ left: entry.item.message ? 0 : Tokens.page, right: entry.item.message ? 0 : Tokens.page })
          .backgroundColor(entry.item.id === this.highlightedLine ? Colors.selection : Color.Transparent).borderRadius(12)
          .id('chat.line.' + entry.item.id)
      }).key((line: ConversationLine) => line.id).virtualScroll({ totalCount: this.lines.length, reusable: false })
    }.stackFromEnd(!this.showWelcome()).height('100%').width('100%').id('chat.transcript')
      .onAreaChange((_old: Area, area: Area) => this.transcriptViewportChanged(Number(area.height)))
      .bindSheet(this.questions.shown, this.questionContent(), { title: { title: tr('interaction_question_reopen') }, height: SheetSize.LARGE, keyboardAvoidMode: SheetKeyboardAvoidMode.RESIZE_ONLY, showClose: true, onWillAppear: () => this.presentedQuestion = this.questions.beginPresentation(), onAppear: () => { this.questionLayoutReady = true; this.measureQuestion(); }, onWillDismiss: (action: DismissSheetAction) => this.questions.dismiss(this.presentedQuestion, () => action.dismiss()), onDisappear: () => { this.questionLayoutReady = false; this.questions.endPresentation(this.presentedQuestion); this.presentedQuestion = undefined; } })
      .maintainVisibleContentPosition(true)
      .onWillScroll((offset: number, _state: ScrollState, source: ScrollSource) => this.trackScrollMotion(offset, source))
      .onScrollIndex((start: number, end: number) => {
        this.firstVisibleIndex = start; this.lastVisibleIndex = end; this.recordReading(start);
      })
      .onDidScroll(() => this.recordReading(this.firstVisibleIndex))
      .onReachEnd(() => { if (this.active && !this.readingRestoring) { this.atEnd = true; this.anchor.atEnd = true; this.jumpAvailable = false; } })
      .onScrollFrameBegin((offset: number) => { if (offset < 0 && !this.readingRestoring) { this.atEnd = false; this.anchor.atEnd = false; } return { offsetRemain: offset }; })
  }

  private openQuestion(request: Decision): void {
    if (!this.active) return;
    if (this.chooser.session) this.closeChooser(this.chooser.session);
    this.attachmentSheet = false; this.contextKind = '';
    this.questions.open(this.model, this.active, request);
  }
  private measureQuestion(): void { if (this.questionLayoutReady) this.questionInset = nativeSheetInset(this.getUIContext(), this.questionNodeId, this.environment.safeBottomPx); }
  private questionLink(sheet: QuestionSheet): (address: string) => void {
    const valid = this.questions.ticket(sheet); return (address: string): void => { if (valid()) this.onLink(address); };
  }
  @Builder questionCard(entry: RepeatItem<ConversationLine>) {
    if (['open', 'pending', 'unknown'].includes(entry.item.decision!.status)) Row({ space: Tokens.inset }) {
      Text(questionPrompt(entry.item.decision!) || tr('question')).fontSize(Tokens.title).fontWeight(FontWeight.Medium).fontColor(Colors.ink).maxLines(3).textOverflow({ overflow: TextOverflow.Ellipsis }).layoutWeight(1)
      Button({ type: ButtonType.Circle }) { if (IconActions.busy(entry.item.decision!.status === 'open' ? 'interaction_question_reopen' : entry.item.decision!.status === 'unknown' ? 'result_unknown' : 'pending')) LoadingProgress().width(Tokens.symbol).height(Tokens.symbol).color(Colors.link); else SymbolGlyph(IconActions.symbol(entry.item.decision!.status === 'open' ? 'interaction_question_reopen' : entry.item.decision!.status === 'unknown' ? 'result_unknown' : 'pending')).fontSize(Tokens.symbolFont).fontColor([IconActions.color(entry.item.decision!.status === 'open' ? 'interaction_question_reopen' : entry.item.decision!.status === 'unknown' ? 'result_unknown' : 'pending', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr(entry.item.decision!.status === 'open' ? 'interaction_question_reopen' : entry.item.decision!.status === 'unknown' ? 'result_unknown' : 'pending')).bindTips(tr(entry.item.decision!.status === 'open' ? 'interaction_question_reopen' : entry.item.decision!.status === 'unknown' ? 'result_unknown' : 'pending'))
        .enabled(this.active && this.model.canApprove() && entry.item.decision!.status === 'open').onClick(() => this.openQuestion(entry.item.decision!)).id('interaction.open.' + entry.item.decision!.id)
    }.width('100%').padding(Tokens.page).alignItems(VerticalAlign.Center).backgroundColor(Colors.question).borderRadius(Tokens.card)
    else InteractionDecisionForm({ model: this.model, request: entry.item.decision!, readOnly: true, onLink: (address: string) => this.onLink(address) })
  }
  @Builder questionContent() {
    Column() {
      if (this.questions.sheet) ForEach([this.questions.sheet], (sheet: QuestionSheet) => {
        InteractionDecisionForm({ model: this.model, request: sheet.request, pinFooter: true, draft: sheet.draft, draftSession: sheet.id, admission: this.questions.ticket(sheet),
          onLater: () => this.questions.close(sheet), onResolved: () => this.questions.close(sheet), onLink: this.questionLink(sheet) })
      }, (sheet: QuestionSheet) => sheet.id)
    }.width('100%').height('100%').padding({ bottom: this.questionInset + Tokens.page }).id(this.questionNodeId)
      .onAreaChange(() => this.measureQuestion())
  }

  @Builder hideKeyboard(page: boolean) {
    Button({type:ButtonType.Circle}) {
      SymbolGlyph($r('sys.symbol.keyboard')).fontSize(19).fontColor([Colors.secondary])
    }.width(44).height(44).padding(0).backgroundColor(Color.Transparent).accessibilityText(tr('phone_chat_hide_keyboard'))
      .bindTips(tr('phone_chat_hide_keyboard')).id(page ? 'chat.editor.keyboard' : 'chat.keyboard')
      .onClick(() => { if (page) this.pageField.stopEditing(); else this.inlineField.stopEditing(); })
  }
  @Builder draftPage() {
    Column({space:8}) {
      Row({space:8}) {
        Button({type:ButtonType.Circle}) {
          SymbolGlyph($r('sys.symbol.arrow_down_right_and_arrow_up_left')).fontSize(19).fontColor([Colors.ink])
        }.width(44).height(44).padding(0).backgroundColor(Colors.control).accessibilityText(tr('phone_chat_close_editor'))
          .onClick(() => { this.finishPageInput(); this.draftEditorOpen=false; }).id('chat.editor.collapse')
        Blank().layoutWeight(1)
        if (this.draftEditorFocused || this.environment.keyboardHeightPx > 0) this.hideKeyboard(true)
      }.width('100%')
      if (this.model.sourceContext) Text(this.model.contextTitle()).fontSize(Tokens.meta).fontColor(Colors.secondary)
        .maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis}).width('100%')
      TextArea({text:this.model.draft,placeholder:tr('message'),controller:this.pageField})
        .maxLength(Tokens.maxMessage).layoutWeight(1).width('100%').fontSize(Tokens.body).fontColor(Colors.ink)
        .lineHeight(String(Tokens.lineHeight)+'fp').backgroundColor(Color.Transparent).caretColor(Colors.link)
        .padding({left:8,right:8,top:5,bottom:5}).enabled(!this.sending && !this.model.draftLoading)
        .onFocus(() => this.draftEditorFocused=true).onBlur(() => this.draftEditorFocused=false)
        .onTextSelectionChange((start:number,end:number) => { if (this.draftEditorOpen && this.draftEditorCurrent()) this.rememberSelection(start,end); })
        .onChange((text:string) => { if (this.draftEditorOpen && this.draftEditorCurrent()) this.model.updateDraft(text); })
        .id('chat.editor.input')
      Row() {
        Blank().layoutWeight(1)
        Button({type:ButtonType.Circle}) {
          SymbolGlyph(this.primaryStop() ? Symbols.stop : IconActions.symbol('send')).fontSize(Tokens.symbolFont).fontColor([Colors.onAction])
        }.width(44).height(44).backgroundColor(Colors.action).accessibilityText(tr(this.primaryActionKey()))
          .enabled(this.draftEditorCurrent() && this.primaryEnabled()).id('chat.editor.send')
          .onClick(() => {
            if (!this.draftEditorCurrent() || !this.primaryEnabled()) return;
            if (this.primaryStop()) { this.model.stop(() => this.draftEditorCurrent()); return; }
            this.refocusInline=false; this.pageField.stopEditing(); this.draftEditorOpen=false; this.sendDraft();
          })
      }.width('100%')
    }.width('100%').height('100%').padding({left:Tokens.page,right:Tokens.page,top:Tokens.inset,bottom:Tokens.page})
      .backgroundColor(Colors.surface).id('chat.editor')
  }
  @Builder composer() {
    Column({ space: Tokens.gap }) {
      AttachmentStrip({ attachments: this.attachments.attachments, errorKey: this.attachments.errorKey, actionsEnabled: !this.sending && !this.attachments.busy,
          onRemove: (id: string) => this.attachments.remove(id), onEdit: (id: string) => this.attachments.editPhoto(id), onPreview: (item: ChatAttachment) => this.preview(item) })
      if (this.model.sourceContext) Row({ space: Tokens.gap }) {
        Text(this.model.contextTitle()).fontSize(Tokens.meta).fontColor(Colors.ink).maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis }).layoutWeight(1)
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('remove')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('remove', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('remove')).bindTips(tr('remove')).enabled(!this.sending).onClick(() => this.model.removeContext())
      }.width('100%')
      GridRow({ columns: this.composerColumns(), gutter: { x: 0, y: 0 } }) {
        GridCol({ span: this.expandedComposer() ? this.composerColumns() : this.composerColumns() - this.leftColumns() - this.sendColumns(), order: this.expandedComposer() ? 0 : 1 }) {
          Stack({alignContent:Alignment.TopEnd}) {
          TextArea({ text: this.model.draft, controller:this.inlineField, placeholder: this.model.agentInputNotice() === 'agent_computer_holds' ? this.agentNotice() : tr('message') }).maxLength(Tokens.maxMessage).width('100%')
            .fontSize(Tokens.body).placeholderFont({ size: Tokens.body }).fontColor(Colors.ink).placeholderColor(Colors.secondary)
            .lineHeight(String(Tokens.lineHeight) + 'fp').caretColor(Colors.action).backgroundColor(Color.Transparent)
            .padding({ left: this.expandedComposer() ? Tokens.page : Tokens.gap, right: this.offersDraftEditor() ? Tokens.touch : this.expandedComposer() ? Tokens.page : Tokens.gap, top: Tokens.inset, bottom: Tokens.inset })
            .constraintSize({ minHeight: Tokens.touch, maxHeight: 6 * Tokens.lineHeight * Math.max(1, this.environment.fontScale) + Tokens.section })
            .height(this.expandedComposer() ? undefined : Tokens.touch)
            .enabled(!this.sending && !this.model.draftLoading).onFocus(() => this.fieldFocused = true).onBlur(() => this.fieldFocused = false)
            .onTextSelectionChange((start:number,end:number) => { if (!this.draftEditorOpen) this.rememberSelection(start,end); })
            .onChange((value: string) => { if (!this.draftEditorOpen) this.model.updateDraft(value); }).id('chat.input')
            if (this.offersDraftEditor()) {
              Button({type:ButtonType.Circle}) {
                SymbolGlyph($r('sys.symbol.arrow_up_left_and_arrow_down_right')).fontSize(17).fontColor([Colors.ink])
              }.width(40).height(40).padding(0).backgroundColor(Color.Transparent)
                .responseRegion({x:-2,y:-2,width:44,height:44}).accessibilityText(tr('phone_chat_expand_editor')).bindTips(tr('phone_chat_expand_editor'))
                .onClick(() => this.expandDraftEditor()).id('chat.expand')
            }
          }.width('100%').bindSheet($$this.draftEditorOpen,this.draftPage(),
            {height:SheetSize.LARGE,showClose:false,keyboardAvoidMode:SheetKeyboardAvoidMode.RESIZE_ONLY,
              onAppear:() => this.focusDraftPage(),onWillDismiss:(action:DismissSheetAction) => { this.finishPageInput(); action.dismiss(); },
              onDisappear:() => this.draftPageGone()})
        }.id(this.editorAnchorId)
          .onAreaChange((_old: Area, area: Area) => { this.editorLeft = Number(area.globalPosition.x); this.editorTop = Number(area.globalPosition.y); this.editorWidth = Number(area.width); this.editorHeight = Number(area.height); })
          .bindPopup(this.chooser.shown && this.chooser.session?.preserveInput === true, this.chooserPopupOptions())
        if (!this.model.demo || this.showContextMenu) GridCol({ span: this.verticalActions() ? this.composerColumns() : this.leftColumns(), order: this.expandedComposer() ? this.verticalActions() ? 2 : 1 : 0 }) {
          Row({ space: Tokens.gap }) {
            if (!this.model.demo) Button({ type: ButtonType.Circle }) { SymbolGlyph(Symbols.plus).fontSize(Tokens.symbolFont).fontColor([Colors.ink]) }
              .attributeModifier(ButtonStyles.quiet).width(Tokens.touch).height(Tokens.touch).padding(0).accessibilityText(tr('attach'))
              .enabled(!this.sending && !this.attachments.busy && !this.model.draftLoading).onClick((event: ClickEvent) => this.openAttachmentChooser(event)).id('chat.attach')
              .bindMenu(this.nativeChooser('attachments'), this.attachmentNativeMenu, this.attachmentMenuOptions())
            if (this.showContextMenu) Button({ type: ButtonType.Circle }) { Image(Symbols.more).width(Tokens.symbol).height(Tokens.symbol).fillColor(Colors.ink) }
              .attributeModifier(ButtonStyles.quiet).width(Tokens.touch).height(Tokens.touch).padding(0).accessibilityText(tr('more')).onClick((event: ClickEvent) => this.openComposerActions(event))
              .parallelGesture(LongPressGesture().onAction(() => this.requestComposerActions(false)))
              .bindContextMenu(this.nativeChooser('actions'), this.attachmentNativeMenu, this.actionContextMenuOptions()).id('chat.more')
            if (this.verticalActions() && (this.fieldFocused || this.environment.keyboardHeightPx > 0)) this.hideKeyboard(false)
          }.width('100%').constraintSize({ minHeight: Tokens.touch }).alignItems(VerticalAlign.Center)
        }
        if (this.expandedComposer() && !this.verticalActions()) GridCol({ span: this.composerColumns() - this.leftColumns() - this.sendColumns(), order: 3 }) {
          Row() {
            if (this.fieldFocused || this.environment.keyboardHeightPx > 0) this.hideKeyboard(false)
            Blank().layoutWeight(1)
          }.width('100%').constraintSize({ minHeight: Tokens.touch })
        }
        GridCol({ span: this.verticalActions() ? this.composerColumns() : this.sendColumns(), order: this.expandedComposer() ? this.verticalActions() ? 1 : 4 : 2 }) {
          Row() {
          PhoneDictationControl({owner: this.dictationOwner(),
            active: this.active && !this.draftEditorOpen && !this.sending && !this.model.draftLoading, language: this.model.language,
            allowed: (owner: string) => owner === this.dictationOwner() && this.sendCurrent() && this.model.canCompose() && !this.model.decision,
            keyboard: () => this.getUIContext().getFocusController().requestFocus('chat.input'),
            insert: (text: string, owner: string): boolean => {
              if (owner !== this.dictationOwner() || !this.sendCurrent() || !this.model.canCompose() || this.model.decision || this.model.draftLoading || this.sending) return false;
              const draft = this.model.draft, separator = /[a-zA-Z0-9]$/.test(draft) ? ' ' : '';
              if (draft.length + separator.length + text.length > Tokens.maxMessage) return false;
              this.model.updateDraft(draft + separator + text);
              return true;
            }})
          if (this.hasComposerAction()) Button({ type: ButtonType.Circle }) {
            if (IconActions.busy(this.primaryActionKey())) LoadingProgress().width(Tokens.symbol).height(Tokens.symbol).color(Colors.onAction)
            else SymbolGlyph(this.primaryActionKey() === 'stop' ? Symbols.stop : IconActions.symbol(this.primaryActionKey())).fontSize(Tokens.symbolFont).fontColor([Colors.onAction])
          }.accessibilityText(tr(this.primaryActionKey())).attributeModifier(ButtonStyles.primary).padding(0).width(this.environment.fontScale >= 1.5 ? 44 : 34).height(this.environment.fontScale >= 1.5 ? 44 : 34)
            .responseRegion({x:-5,y:-5,width:44,height:44})
            .enabled(this.primaryEnabled())
            .onClick(() => { if (!this.sendCurrent()) return; if (this.primaryStop()) this.model.stop(() => this.sendCurrent()); else this.sendDraft(); }).id('chat.send')
          }.width('100%').height(Tokens.touch).alignItems(VerticalAlign.Center).justifyContent(FlexAlign.End).padding({right:3})
        }
      }.width('100%').onAreaChange((_old: Area, area: Area) => { const width = Number(area.width); if (Number.isFinite(width) && width > 0) this.composerWidth = width; })
    }.padding({left:5,right:5,top:3,bottom:3}).backgroundColor(Colors.surface)
      .border({width:0.7,color:Colors.divider,radius:25}).borderRadius(25).width('100%').id('chat.composer')
  }

  @Builder taskDock() {
    if (this.model.selected.kind === 'conversation' && this.model.connected) {
          AssistantCards({ model: this.model, feed: this.feed, active: this.active && this.model.connected, onOpenResource: (row: ResourceRow) => this.onOpenResource(row), dockOnly: true, onOpenCard: (itemId: string, kind: string) => this.onOpenCard(itemId, kind), onOpenSession: (row: ResourceRow) => this.onOpenResource(row),
            onOpenTodo: (row: ResourceRow) => this.onOpenResource(row), onOpenInbox: (row: ResourceRow) => this.onOpenResource(row), onLink: (address: string) => this.onLink(address) })
    }
  }
  @Builder blockingContent() {
    Column({ space: Tokens.gap }) {
      this.taskDock()
      ForEach(this.model.decision ? [this.model.decision] : [], (request: Decision) => {
        InteractionDecisionForm({ model: this.model, request: request, fillAvailable: true,
          heightLimit: Math.max(Tokens.touch, this.maxControlsHeight() - this.decisionToolsHeight() - Tokens.gap - Tokens.footer),
          onLink: (address: string) => this.onLink(address) })
      }, (request: Decision) => request.id)
      if (this.showContextMenu) Button({ type: ButtonType.Circle }) { Image(Symbols.more).width(Tokens.symbol).height(Tokens.symbol).fillColor(Colors.ink) }
        .attributeModifier(ButtonStyles.quiet).width(Tokens.touch).height(Tokens.touch).padding(0).accessibilityText(tr('more')).onClick(() => this.openFallbackActions())
        .bindMenu(this.actionsVisible, this.actionsMenu, this.actionMenuOptions(this.actionsRequest)).id('chat.more.fallback')
    }.width('100%').constraintSize({ maxWidth: Tokens.readingMax, maxHeight: this.maxControlsHeight() })
      .padding({ left: Tokens.page, right: Tokens.page, bottom: Tokens.footer, top: Tokens.gap })
      .onAreaChange((_before: Area, area: Area) => {
        const height = Number(area.height);
        if (this.model.decision && Number.isFinite(height) && height > 0) {
          this.measuredDecision = this.model.decision.id;
          if (Math.abs(height - this.controlsHeight) > 1) this.controlsHeight = height;
        }
      })
  }
  @Builder interactionContent() {
    if (this.model.decision) this.blockingContent()
    else Scroll() {
      Column({ space: Tokens.gap }) {
      if (this.showContextMenu && (this.model.decision || !this.model.canCompose())) Button({ type: ButtonType.Circle }) { Image(Symbols.more).width(Tokens.symbol).height(Tokens.symbol).fillColor(Colors.ink) }
        .attributeModifier(ButtonStyles.quiet).width(Tokens.touch).height(Tokens.touch).padding(0).accessibilityText(tr('more')).onClick(() => this.openFallbackActions())
        .bindMenu(this.actionsVisible, this.actionsMenu, this.actionMenuOptions(this.actionsRequest)).id('chat.more.fallback')
      this.taskDock()
      if (this.model.canCompose() && !this.model.decision) {
        if (this.attachments.busy && !this.sending && (this.attachments.errorKey === 'chat_attachment_storage_unavailable' || this.model.error === 'cache_unavailable')) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('retry')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('retry', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('retry')).bindTips(tr('retry')).onClick(() => { this.attachmentKey = ''; this.syncAttachments(); })
        this.composer()
      } else if (!this.model.canSend() && !this.model.decision) {
        if (this.model.canAttach()) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('attach_live')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('attach_live', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('attach_live')).bindTips(tr('attach_live')).onClick(() => this.model.attach())
        Text(this.model.agentInputNotice() ? this.agentNotice() : tr('read_only')).fontSize(Tokens.meta).padding(Tokens.page)
      }
      }.width('100%').constraintSize({ maxWidth: Tokens.readingMax }).padding({ left: Tokens.page, right: Tokens.page, bottom: Tokens.footer, top: Tokens.gap }).onAreaChange((_before: Area, area: Area): void => {
        const height = Number(area.height);
        if (Number.isFinite(height) && height > 0 && Math.abs(height - this.controlsHeight) > 1) this.controlsHeight = height;
      })
    }.width('100%').height('100%')
  }

  build() {
    Column() {
      if (this.useNativeHover) {
      FoldSplitContainer({ primary: (): void => this.readingContent(), secondary: (): void => this.interactionContent(),
        expandedLayoutOptions: { verticalSplitRatio: this.ordinarySplitRatio() },
        foldedLayoutOptions: { verticalSplitRatio: this.ordinarySplitRatio() },
        hoverModeLayoutOptions: { showExtraRegion: false }, animationOptions: null,
        onHoverStatusChange: (status: HoverModeStatus): void => {
          hilog.info(0x0000, 'PersonaLayout', 'fold=%{public}d hover=%{public}s rotation=%{public}d window=%{public}d',
            status.foldStatus, String(status.isHoverMode), status.appRotation, status.windowStatusType);
        } }).onAreaChange((_old: Area, area: Area): void => { const height = Number(area.height); if (Number.isFinite(height) && height > 0 && Math.abs(height - this.foldContainerHeight) > 0.1) this.foldContainerHeight = height; })
      } else {
        Column() { this.readingContent() }.width('100%').layoutWeight(1).clip(true)
        if (this.model.decision) this.blockingContent()
        else Column() { this.interactionContent() }.width('100%')
          .height(Math.max(1, Math.min(this.controlsHeight, this.maxControlsHeight())))
      }
    }.width('100%').height('100%').backgroundColor(Colors.canvas).expandSafeArea([SafeAreaType.SYSTEM], [SafeAreaEdge.BOTTOM])
      .bindSheet($$this.attachmentSheet, this.attachmentContent(), { title: { title: tr(this.contextKind === 'knowledge' ? 'knowledge' : this.contextKind === 'todo' ? 'todos' : this.contextKind === 'inbox' ? 'inbox' : 'attach') }, height: SheetSize.LARGE, showClose: true, onAppear: () => { this.attachmentLayoutReady = true; this.measureAttachment(); }, onDisappear: () => this.attachmentLayoutReady = false })
      .onAreaChange((_before: Area, area: Area): void => {
      const height = Number(area.height);
      const width = Number(area.width);
      this.contentTop = Number(area.globalPosition.y);
      if (Number.isFinite(width) && width > 0 && Math.abs(width - this.availableWidth) > 1) this.availableWidth = width;
      if (Number.isFinite(height) && height > 0 && Math.abs(height - this.availableHeight) > 1) this.availableHeight = height;
    })
  }
}
