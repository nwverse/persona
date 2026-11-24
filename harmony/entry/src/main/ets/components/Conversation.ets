import { agentBrand, agentBrandColor } from '../design/AgentBrand';
import { ComposerIdentity, sessionComposerIdentity } from '../core/ComposerIdentity';
import { messageSentTime, sessionContextUsage, pendingDesktopProposal, waitsOnClaudeDesktop } from '../core/ConversationMetadata';
import type { DesktopSessionProposal, SessionContextUsage } from '../core/ConversationMetadata';
import { deviceLookQuoteInk, DeviceLookColorSet } from '../core/DeviceLook';
import { ConversationMarkdownCache, parseMarkdown, markdownInlineText, structuredBubble } from '../core/Markdown';
import { ToolGlyph } from '../design/ToolGlyph';
import { IconActions } from '../design/IconActions';
import { Theme, SymbolGlyphModifier, promptAction } from '@kit.ArkUI';
import { ComposerOverlayWorkspace, ComposerOverlaySession } from '../models/ComposerOverlayWorkspace';
import { composerPopupLayout, ComposerPopupLayout } from '../core/ComposerPopupLayout';
import { util } from '@kit.ArkTS';
import { HttpFailure } from '../platform/HubTransport';
import { i18n } from '@kit.LocalizationKit';
import { FoldSplitContainer, HoverModeStatus, FrameCallback, LengthMetrics } from '@kit.ArkUI';
import { hilog } from '@kit.PerformanceAnalysisKit';
import { NativeEnvironment, nativeEnvironment } from '../platform/NativeEnvironment';
import { DeviceLook, deviceLook } from '../models/DeviceLook';
import { nativeMessageTheme } from '../design/LookTheme';
import { AssistantMessage } from './AssistantMessage';
import { AgentEventLine } from './AgentEventLine';
import { agentEventDisplay, AgentEventDisplay, eventClock, recordedDuration, sessionIssueDisplay } from '../core/AgentEvents';
import { transcriptRecord } from '../core/Domain';
import type { JsonObject, ToolInspectionStep, TranscriptItem } from '../core/Domain';
import { NextStepCard } from './NextStepCard';
import { ConversationPlanCard } from './ConversationPlanCard';
import { onlyPlanUpdates, planPinned, planPosition } from '../core/ConversationPlan';
import type { ConversationPlan } from '../core/ConversationPlan';
import type { ToolTurnProjection, WorkInspectionStep } from '../core/Domain';
import { ConversationWork } from './ConversationWork';
import { pasteboard } from '@kit.BasicServicesKit';
import { SessionRevision, SessionRevisionOutcome } from '../models/SessionRevision';
import type { SessionRevisionRestore } from '../core/SessionRevision';
import { ConversationRevisionLease, restoreAcknowledgedSessionDraft } from '../core/ConversationRevisionLease';
import { TranscriptImage } from './TranscriptImage';
import { ConversationPhotoViewer } from './ConversationPhotoViewer';
import { attachmentFileName, conversationImageEntries, conversationImageKey, messageAttachmentImages } from '../core/MessageImageGallery';
import type { ConversationPhotoRequest, MessageImageGalleryEntry, LoadedMessageImage } from '../core/MessageImageGallery';
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
  @Param structured: boolean = false;
  @Param text: string = '';
  @Param maximum: number = Tokens.readingMax;
  @Param timeHint: string = '';
  @Event onTime: () => void = () => {};
  @BuilderParam content: () => void;
  @Local fill: ResourceColor = Color.Transparent;
  onWillApplyTheme(theme: Theme): void { this.fill = theme.colors.compBackgroundPrimary; }
  @Computed get structuredWidth(): number {
    if (!this.text.startsWith('>') || this.text.length > 256 || nativeEnvironment.fontScale <= 0) return this.maximum;
    const blocks = parseMarkdown(this.text);
    const ui = this.getUIContext();
    let width = 0;
    for (const block of blocks) {
      const quoted = block.kind === 'quote';
      const paragraph = quoted && block.children.length === 1 ? block.children[0] : block;
      if (paragraph.kind !== 'paragraph' || paragraph.inlines.some(node => node.kind !== 'text')) return this.maximum;
      const size = Tokens.userBody + 'fp';
      for (const line of markdownInlineText(paragraph.inlines).split('\n')) {
        width = Math.max(width, ui.px2vp(ui.getMeasureUtils().measureText({textContent:line, fontSize:size})));
      }
    }
    return Math.min(this.maximum, Math.ceil(width) + 2 * Tokens.inset + 2);
  }
  @Builder surface() {
    Column() { this.content() }.width(this.own ? (this.structured ? this.structuredWidth : undefined) : '100%').constraintSize({ maxWidth: this.maximum })
      .padding(this.own ? { left:Tokens.inset, right:Tokens.inset, top:10, bottom:10 } : 0).backgroundColor(this.own ? this.fill : Color.Transparent)
      .borderRadius(this.own ? Tokens.bubble : 0).clip(this.own)
  }
  build() {
    if (this.timeHint) {
      Column() { this.surface() }.onClick(() => this.onTime()).accessibilityDescription(this.timeHint)
    } else { this.surface() }
  }
}

// Long disclosures participate in the transcript's own virtual list. Only nearby batches acquire ArkUI nodes.
const workBatchSize: number = 6;
const inlineWorkLimit: number = 18;
class ConversationLine {
  workBatch?: WorkInspectionStep[];
  continuation: boolean = false;
  joinsNext: boolean = false;
  id: string;
  readingId: string;
  message: ChatMessage | undefined;
  card: Record<string, Object> | undefined;
  decision: Decision | undefined;
  plan?: ConversationPlan;
  /** The plan stays under the newest content because the owner wrote after it (planPinned). */
  planPinned: boolean = false;
  processReply?: ChatMessage;
  static same(a: ConversationLine, b: ConversationLine): boolean {
    return a.workBatch === b.workBatch && a.joinsNext === b.joinsNext && a.message === b.message && a.card === b.card && a.decision === b.decision && a.plan === b.plan && a.planPinned === b.planPinned &&
      a.processReply === b.processReply && a.hasProcess === b.hasProcess && a.soleProcessGroup === b.soleProcessGroup;
  }
  hasProcess: boolean = false;
  soleProcessGroup: boolean = false;
  constructor(id: string, message?: ChatMessage, card?: Record<string, Object>, decision?: Decision) {
    this.id = id; this.readingId = message ? 'message:' + (message.itemId || message.id) : id; this.message = message; this.card = card; this.decision = decision;
  }
}

@ComponentV2
export struct Conversation {
  @Require @Param model: ClientModel;
  @Param active: boolean = true;
  @Param showContextMenu: boolean = false;
  @Param actionItems: SheetInfo[] = [];
  @Param actionsVisible: boolean = false;
  @Param actionsRequest: number = 0;
  @Event onOpenComputer: (row: ResourceRow) => void = () => {};
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
  @Local takingOver: boolean = false;
  @Local revisions: SessionRevision = new SessionRevision();
  @Local revisionResult: string = '';
  @Local revisionCacheError: string = '';
  private revisionKey: string = '';
  private revisionVersion: number = -1;
  private revisionContextKey: string = '';
  private revisionVisibilityRevision: number = 0;
  private revisionMenuLease: ConversationRevisionLease | undefined = undefined;
  private attachmentKey: string = '';
  private mounted: boolean = false;
  @Local lines: ConversationLine[] = [];
  private photoDialog: promptAction.DialogController | undefined;
  @Local photoOpen: boolean = false;
  @Local imageGallery: MessageImageGalleryEntry[] = [];
  private imageIndices: Map<string, number> = new Map();
  private feedKey: string = '';
  private feedTimer: number = -1;
  @Local expanded: string[] = [];
  @Local timeMessageId: string = '';
  @Local contextDetailOpen: boolean = false;
  @Local workReady: boolean = false;
  private workTarget: string = '';
  private shownPlanReport: string = '';
  private readingPlan: string = '';
  private beforePlan: string = '';
  private workView: ClientViewLease | undefined;
  private scroller: Scroller = new Scroller();
  private readingKey: string = '';
  private readingEpoch: number = 0;
  @Local readingLoading: boolean = false;
  private readingMotion: number = 0;
  private inspectingWork: boolean = false;
  private inspectionRevision: number = 0;
  @Local openProcesses: string[] = [];
  @Local closedSoloGroups: string[] = [];
  private processRevision: number = 0;
  private documents: ConversationMarkdownCache = new ConversationMarkdownCache();
  private workBatches: WeakMap<ToolTurnProjection, WorkInspectionStep[][]> = new WeakMap();
  private hiddenProcessAnchors: Map<string, string> = new Map();
  private firstVisibleIndex: number = 0;
  private lastVisibleIndex: number = 0;
  private resumeHistory?: () => void;
  private timelinePending: boolean = false;
  private autoPageCursor: number = -1;
  private revealOlder: boolean = false;
  @Local earlierFailed: boolean = false;
  private userScrolling: boolean = false;
  private followEpoch: number = -1;
  private tailMarker: string = '';
  @Local unseenLatest: boolean = false;
  private readingPrefix(): number {
    return (this.feed.error && this.feed.error !== this.model.error ? 1 : 0) + (this.model.selected.kind === 'conversation' ? 1 : 0) + (this.model.hasMoreMessages || this.feed.nextCursor ? 1 : 0);
  }
  @Local dismissedNextStep: string = '';
  @Local answeredNextStepId: string = '';
  @Local nextStepAnswer: string = '';
  @Local nextStepStateReady: boolean = false;
  private selectedNextStep(message: ChatMessage): string {
    return message.nextStep?.selected || (message.id === this.answeredNextStepId ? this.nextStepAnswer : '');
  }
  private nextStepCurrent(message: ChatMessage): boolean {
    return this.sendCurrent() && !this.selectedNextStep(message) && this.dismissedNextStep !== message.id && message.ownerTarget === this.model.target() &&
      this.model.messages.some((row: ChatMessage) => row.id === message.id && !!row.nextStep &&
        JSON.stringify(row.nextStep) === JSON.stringify(message.nextStep));
  }
  private dismissNextStep(message: ChatMessage): void {
    if (!this.nextStepCurrent(message)) return;
    this.dismissedNextStep = message.id;
    this.syncTimeline();
    void this.model.captureView('next-step:' + this.model.target()).write(message.id).catch(() => {});
  }
  private canSendNextStep(): boolean {
    return this.nextStepStateReady && !this.sending && !this.model.draftLoading && !this.model.decision && this.model.canSend() &&
      !['pending', 'unknown'].includes(this.model.operationState);
  }
  private async useNextStep(message: ChatMessage, label: string): Promise<void> {
    if (!this.canSendNextStep() || !this.nextStepCurrent(message)) return;
    const model = this.model, owner = this.composerOwnerKey(), epoch = this.readingEpoch;
    const current = (): boolean => this.model === model && this.composerOwnerKey() === owner &&
      this.readingEpoch === epoch && this.nextStepCurrent(message);
    this.sending = true;
    try {
      if (await model.send(current, label) && current()) {
        this.answeredNextStepId = message.id; this.nextStepAnswer = label;
        void model.captureView('next-step:' + model.target()).write(JSON.stringify({id:message.id,answer:label})).catch(() => {});
      }
    } catch (error) { if (current()) model.reportError(error as Error); }
    finally { this.sending = false; }
  }
  private syncReading(): void {
    const key = JSON.stringify([this.model.scope(), this.model.target()]);
    if (!this.active || this.readingKey === key) return;
    this.documents = new ConversationMarkdownCache();
    if (this.resumeHistory) this.releaseHistoryRendering();
    const epoch = ++this.readingEpoch;
    this.readingKey = key;
    this.inspectionRevision++; this.inspectingWork = false; this.readingPlan = ''; this.beforePlan = '';
    this.followEpoch = -1; this.userScrolling = false; this.unseenLatest = false; this.tailMarker = '';
    this.openProcesses = []; this.closedSoloGroups = []; this.hiddenProcessAnchors.clear();
    this.autoPageCursor = -1; this.earlierFailed = false; this.readingLoading = false; this.revealOlder = false;
    this.retirePhoto();
    this.timeMessageId = '';
    this.contextDetailOpen = false;
    this.atEnd = true; this.firstVisibleIndex = 0; this.lastVisibleIndex = 0;
    this.dismissedNextStep = ''; this.answeredNextStepId = ''; this.nextStepAnswer = ''; this.nextStepStateReady = false;
    const saved = this.model.captureView('next-step:' + this.model.target());
    const current = (): boolean => saved.valid() && this.readingEpoch === epoch && this.readingKey === key;
    void saved.read().then((value: string) => {
      if (!value || !current() || this.dismissedNextStep) return;
      if (value.startsWith('{')) {
        const record = Wire.parse(value);
        this.answeredNextStepId = Wire.text(record,'id'); this.nextStepAnswer = Wire.text(record,'answer');
      } else this.dismissedNextStep = value;
      this.syncTimeline();
    }).catch(() => {}).finally(() => { if (current()) this.nextStepStateReady = true; });
    this.followReading();
  }
  private visibleReadingAnchor(): ReadingAnchor | undefined {
    const prefix = this.readingPrefix(), index = Math.max(prefix, this.firstVisibleIndex), line = this.lines[index - prefix];
    if (!line) return undefined;
    try {
      const rect = this.scroller.getItemRect(index);
      if (Number.isFinite(rect.y) && Number.isFinite(rect.height) && rect.height > 0) return { id: line.readingId, offset: rect.y };
    } catch (_) {}
    return undefined;
  }
  private async loadEarlier(manual: boolean = true): Promise<void> {
    if (!this.active || !this.model.connected) return;
    if (this.readingLoading) { if (manual) this.revealOlder = true; return; }
    const anchor = this.visibleReadingAnchor(), known = new Set(this.lines.map(line => line.readingId));
    const owner = this.model, generation = owner.connectionGeneration(), scope = owner.scope(), target = owner.target();
    const epoch = this.readingEpoch, key = this.readingKey, motion = this.readingMotion;
    const valid = (): boolean => this.active && this.model === owner && owner.owns(generation, scope) && owner.target() === target &&
      this.readingEpoch === epoch && this.readingKey === key && this.readingMotion === motion;
    this.atEnd = false; this.readingLoading = true; this.earlierFailed = false; this.revealOlder = manual;
    try {
      let boundary = 0;
      do {
        const before = owner.oldestTranscriptSeq, cursor = this.feed.nextCursor;
        const results = await Promise.all([owner.hasMoreMessages ? owner.loadOlder() : Promise.resolve(true),
          cursor ? this.feed.loadMore() : Promise.resolve(true)]);
        if (!valid()) return;
        this.syncTimeline();
        boundary = this.lines.findIndex(line => known.has(line.readingId));
        const stalled = owner.oldestTranscriptSeq === before && this.feed.nextCursor === cursor;
        this.earlierFailed = results.some(ok => !ok) || (stalled && boundary === 0 && (owner.hasMoreMessages || !!this.feed.nextCursor));
        if (boundary !== 0 || !this.revealOlder || this.earlierFailed) break;
      } while (owner.hasMoreMessages || this.feed.nextCursor);
      if (!valid()) return;
      const reveal = this.revealOlder && boundary > 0;
      let destination: ReadingAnchor | undefined = anchor;
      if (reveal) {
        let index = boundary - 1;
        for (let i = index; i >= 0; i--) {
          if (this.lines[i].message?.role === 'user') { index = i; break; }
        }
        destination = { id:this.lines[index].readingId, offset:0 };
      }
      if (destination && (reveal || !this.userScrolling)) {
        const focus = destination;
        this.getUIContext().postFrameCallback(new ConversationFrame(() => {
          if (!valid()) return;
          const index = this.lines.findIndex(line => line.readingId === focus.id);
          if (index < 0) return;
          this.scroller.scrollToIndex(index + this.readingPrefix(), false, ScrollAlign.START, { extraOffset:LengthMetrics.vp(-focus.offset) });
        }));
      }
    } finally { if (epoch === this.readingEpoch) { this.readingLoading = false; this.revealOlder = false; } }
  }
  private approachHistory(start: number): void {
    const cursor = this.model.oldestTranscriptSeq;
    if (this.model.selected.kind !== 'session' || !this.userScrolling || this.atEnd ||
      this.readingLoading || !this.model.hasMoreMessages || cursor <= 0 || this.autoPageCursor === cursor ||
      start > this.readingPrefix() + 5 || start >= this.firstVisibleIndex) return;
    this.autoPageCursor = cursor;
    void this.loadEarlier(false);
  }
  @Local atEnd: boolean = true;
  private userScroll(offset: number, source: ScrollSource): void {
    if (!offset || ![ScrollSource.DRAG, ScrollSource.FLING, ScrollSource.OTHER_USER_INPUT,
      ScrollSource.SCROLL_BAR, ScrollSource.SCROLL_BAR_FLING].includes(source)) return;
    if (!this.userScrolling) { this.readingMotion++; this.userScrolling = true; }
    if (offset < 0) this.atEnd = false;
  }
  private reachedLatest(): void {
    if (!this.active || this.photoOpen || !this.scroller.isAtEnd()) return;
    this.atEnd = true; this.unseenLatest = false;
  }
  private jumpToLatest(): void {
    this.readingMotion++; this.inspectionRevision++; this.inspectingWork = false; this.userScrolling = false;
    this.followEpoch = -1;
    this.atEnd = true; this.unseenLatest = false;
    if (this.resumeHistory) this.releaseHistoryRendering();
    if (this.timelinePending) this.syncTimeline();
    if (this.readingPlan) this.setPlanReading(this.readingPlan, false);
    this.scroller.scrollEdge(Edge.Bottom);
    this.followReading();
  }
  private releaseHistoryRendering(): void {
    const resume = this.resumeHistory;
    this.resumeHistory = undefined;
    resume?.();
  }
  private trackHistoryRendering(last: number): void {
    const hold = this.active && this.userScrolling && !this.atEnd &&
      this.model.selected.kind === 'session' && last < this.readingPrefix() + this.lines.length - 1;
    if (hold && !this.resumeHistory) this.resumeHistory = this.model.holdHistoryRendering();
    else if (!hold && this.resumeHistory) this.releaseHistoryRendering();
  }
  private finishScroll(): void {
    if (this.userScrolling) this.reachedLatest();
    this.userScrolling = false;
    if (this.resumeHistory) this.releaseHistoryRendering();
    if (this.timelinePending) this.syncTimeline();
    this.tailLayoutChanged();
  }
  private tailLayoutChanged(): void {
    if (this.active && this.atEnd && !this.userScrolling && !this.scroller.isAtEnd()) this.followReading();
  }
  @Local availableHeight: number = 800;
  @Local availableWidth: number = 360;
  @Local environment: NativeEnvironment = nativeEnvironment;
  // Keep native input, IME composition and transcript nodes stable for this view.
  // Re-entering the conversation may sample a newly available native capability.
  private useNativeHover: boolean = nativeEnvironment.nativeHoverAvailable;
  @Local controlsHeight: number = 80;
  @Local fieldFocused: boolean = false;
  @Local editorDismissed: boolean = false;
  private editorController: TextAreaController = new TextAreaController();
  private dismissEditor(): void {
    if (!this.fieldFocused) return;
    this.editorDismissed = true;
    this.editorController.stopEditing();
    this.getUIContext().getFocusController().clearFocus();
    this.fieldFocused = false;
  }
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
  private expandedComposer(): boolean { return this.fieldFocused || (!!this.model.draft && !this.editorDismissed) || this.attachments.attachments.length > 0 || this.environment.fontScale >= 1.5 || this.composerWidth < Tokens.composerNarrowActions || this.composerWidth - this.leftActionWidth() - Tokens.touch - (this.leftActionWidth() ? 2 : 1) * Tokens.composerGutter < Tokens.minimumInput; }
  private verticalActions(): boolean { return this.environment.fontScale >= 1.5 || this.composerWidth < Tokens.composerNarrowActions; }
  // Native ordering keeps the same editor mounted. Compact slots use pixel precision, not twelfths of the row.
  private composerColumns(): number { return this.expandedComposer() ? 12 : Math.max(1, Math.round(this.getUIContext().vp2px(this.composerWidth))); }
  private sendColumns(): number { const width = Tokens.touch + (this.expandedComposer() ? 0 : Tokens.composerGutter); return Math.ceil(width * this.composerColumns() / this.composerWidth); }
  private leftColumns(): number { const width = this.leftActionWidth(); return Math.ceil((width + (width && !this.expandedComposer() ? Tokens.composerGutter : 0)) * this.composerColumns() / this.composerWidth); }
  private primaryStop(): boolean { return !this.model.decision && this.model.canCompose() && this.model.canStop() && !this.model.draft.trim() && !this.attachments.attachments.length; }
  @Monitor('active', 'model.selected', 'model.sessions', 'model.connected', 'model.draft', 'model.decision', 'attachments.attachments', 'model.interruptPendingKey')
  primaryStopChanged(): void { this.onPrimaryStop(this.active && this.primaryStop()); }
  private maxControlsHeight(): number { return this.model.decision ? this.availableHeight * (this.environment.keyboardHeightPx > 0 ? 1 : Tokens.decisionFraction) : this.availableHeight * 0.8; }
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
    const secondary = this.model.decision ? this.maxControlsHeight() : Math.max(1, Math.min(this.controlsHeight, this.maxControlsHeight()));
    return Math.max(1, (this.foldContainerHeight || this.availableHeight) - secondary) / secondary;
  }
  @Monitor('model.messages', 'model.transcriptHistoryReady') messagesChanged(): void {
    this.syncRevision(); this.refreshFeed();
    if (this.userScrolling && !this.atEnd && this.active && this.model.selected.kind === 'session' &&
      this.lastVisibleIndex < this.readingPrefix() + this.lines.length - 1) {
      this.timelinePending = true;
      return;
    }
    this.syncTimeline();
  }
  @Monitor('feed.items', 'feed.hasLoaded') feedChanged(): void { this.syncTimeline(); }
  @Monitor('active', 'model.selected', 'model.connected') feedContextChanged(): void {
    if (this.active) this.syncReading();
    else {
      this.readingMotion++; this.readingEpoch++; this.readingKey = '';
      if (this.resumeHistory) this.releaseHistoryRendering();
    }
    this.syncFeed(); this.syncAttachments(); this.syncRevision();
  }
  @Monitor('model.decision') decisionChanged(): void { this.refreshFeed(); }
  @Monitor('active', 'model.selected', 'model.profile', 'model.connected', 'model.personaQuestions')
  questionsContextChanged(): void { this.questions.sync(this.model, this.active); }
  @Monitor('model.personaQuestions') personaQuestionsChanged(): void {
    this.syncTimeline(); this.refreshFeed();
  }
  private holdWorkReading(change: () => void): void {
    const anchor = this.active ? this.visibleReadingAnchor() : undefined;
    if (!anchor) { change(); return; }
    const epoch = this.readingEpoch, motion = this.readingMotion, ticket = ++this.inspectionRevision;
    this.inspectingWork = true;
    change();
    this.getUIContext().postFrameCallback(new ConversationFrame(() => {
      if (ticket !== this.inspectionRevision) return;
      this.inspectingWork = false;
      if (!this.active || epoch !== this.readingEpoch || motion !== this.readingMotion) return;
      const visibleId = this.hiddenProcessAnchors.get(anchor.id) || anchor.id;
      const index = this.lines.findIndex(line => line.readingId === visibleId);
      if (index < 0) return;
      const nativeIndex = this.readingPrefix() + index;
      try {
        const rect = this.scroller.getItemRect(nativeIndex);
        if (!rect.height || Math.abs(rect.y - anchor.offset) > Tokens.hairline) {
          this.scroller.scrollToIndex(nativeIndex, false, ScrollAlign.START, { extraOffset: LengthMetrics.vp(-anchor.offset) });
        }
      } catch (_) { /* The destination can disappear between measuring the row and the layout callback. */ }
    }));
  }
  private followReading(): void {
    if (!this.active || !this.atEnd || this.userScrolling || this.inspectingWork || this.photoOpen || this.followEpoch === this.readingEpoch) return;
    const epoch = this.readingEpoch, inspection = this.inspectionRevision;
    this.followEpoch = epoch;
    this.getUIContext().postFrameCallback(new ConversationFrame(() => {
      if (this.followEpoch === epoch) this.followEpoch = -1;
      if (epoch === this.readingEpoch && inspection === this.inspectionRevision && !this.inspectingWork && !this.userScrolling && !this.photoOpen && this.active && this.atEnd) this.scroller.scrollEdge(Edge.Bottom);
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
      this.revisionVersion = this.model.transcriptVersion;
    } else if (this.revisionVersion !== this.model.transcriptVersion) {
      this.revisions.updateItems(this.model.transcriptItems());
      this.revisionVersion = this.model.transcriptVersion;
    }
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
  @Monitor('model.sessions') sessionReportChanged(): void {
    if (!this.contextUsage) this.contextDetailOpen = false;
    if (JSON.stringify(this.currentSessionData?.plan ?? null) !== this.shownPlanReport) this.syncTimeline();
  }
  private syncTimeline(): void {
    this.timelinePending = false;
    if (this.active && this.model.transcriptHistoryReady && this.model.hasMoreMessages) this.model.prefetchOlder();
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
    const report = transcriptRecord(this.currentSessionData?.plan);
    this.shownPlanReport = JSON.stringify(report ?? null);
    const plan = this.model.executionPlan(report);
    if (plan) {
      const card = new ConversationLine('plan:' + this.model.target());
      card.plan = plan; card.planPinned = planPinned(lines, plan, this.model.turnWorking());
      const anchor = this.readingPlan === card.id ? lines.findIndex((line: ConversationLine) => line.id === this.beforePlan) : -1;
      lines.splice(anchor >= 0 ? anchor + 1 : planPosition(lines, plan.sourceId, card.planPinned), 0, card);
    }
    this.model.personaQuestions.forEach((request: Decision) => lines.push(new ConversationLine('persona-question:' + request.id, undefined, undefined, request)));
    if ((!plan || this.readingPlan !== 'plan:' + this.model.target()) && this.readingPlan) this.setPlanReading(this.readingPlan, false);
    const visible = lines.filter((line: ConversationLine) => (line.message?.id !== this.dismissedNextStep || !!line.message?.nextStep?.selected) && !onlyPlanUpdates(line.message?.work));
    this.markLatest(visible);
    const display = this.virtualWorkLines(this.processLines(visible, plan));
    reconcileTimelineRows(this.lines, display, ConversationLine.same);
    this.followReading();
  }
  @Monitor('expanded', 'closedSoloGroups') expandedWorkChanged(): void {
    if (this.mounted) this.syncTimeline();
  }
  private virtualWorkLines(lines: ConversationLine[]): ConversationLine[] {
    const result: ConversationLine[] = [];
    for (const header of lines) {
      result.push(header);
      const message = header.message, work = message?.work;
      if (!message || !work || work.inspectionSteps.length <= inlineWorkLimit) continue;
      const open = header.soleProcessGroup ? !this.closedSoloGroups.includes(message.id) : this.expanded.includes(message.id);
      if (!open) continue;
      let batches = this.workBatches.get(work);
      if (!batches) {
        const rows = work.inspectionSteps;
        batches = [];
        for (let i = 0; i < rows.length; i += workBatchSize) batches.push(rows.slice(i, i + workBatchSize));
        this.workBatches.set(work, batches);
      }
      header.workBatch = batches[0]; header.joinsNext = batches.length > 1;
      for (let i = 1; i < batches.length; i++) {
        const batch = batches[i];
        const id = 'work-batch:' + JSON.stringify([header.readingId, batch[0].kind, batch[0].id]);
        const line = new ConversationLine(id, message);
        line.readingId = id; line.workBatch = batch; line.continuation = true; line.joinsNext = i + 1 < batches.length;
        result.push(line);
      }
    }
    return result;
  }
  private processLines(lines: ConversationLine[], plan?: ConversationPlan): ConversationLine[] {
    const owners = new Map<string, ChatMessage>(), leads = new Map<string, ChatMessage>();
    for (const line of lines) {
      const reply = line.message;
      if (reply?.processIds) {
        for (const id of reply.processIds) owners.set(id, reply);
        for (const id of reply.leadIds ?? []) leads.set(id, reply);
      }
    }
    this.hiddenProcessAnchors.clear();
    const counts = new Map<string, number>(), visibleProcess = new Set<string>();
    for (const line of lines) {
      const owner = line.message && owners.get(line.message.id);
      if (!owner) continue;
      visibleProcess.add(owner.itemId);
      if (line.message?.work) counts.set(owner.itemId, (counts.get(owner.itemId) ?? 0) + 1);
    }
    const planOwner = plan?.sourceId ? this.model.messages.find((m: ChatMessage) =>
      m.work?.inspectionSteps.some(step => step.id === plan.sourceId)) : undefined;
    const shown = new Set<string>(), hidden = new Set<string>(), result: ConversationLine[] = [];
    const addHeader = (reply: ChatMessage): void => {
      if (shown.has(reply.itemId)) return;
      const header = new ConversationLine('process:' + reply.itemId);
      header.processReply = reply; result.push(header); shown.add(reply.itemId);
    };
    for (const line of lines) {
      const lead = line.message && leads.get(line.message.id);
      if (lead && visibleProcess.has(lead.itemId)) addHeader(lead);
      // A pinned plan has left its update, so it neither folds nor hides with the process of that turn.
      const owner = owners.get(line.message?.id || (line.plan && !line.planPinned ? planOwner?.id || '' : ''));
      if (owner) {
        const key = owner.itemId;
        if (line.plan && !visibleProcess.has(key) && !(owner.partialProcess && this.model.hasMoreMessages)) continue;
        if (owner.leadIds?.length) addHeader(owner);
        if (!this.openProcesses.includes(key)) {
          hidden.add(key); this.hiddenProcessAnchors.set(line.readingId, 'process:' + key);
          if (line.plan && this.readingPlan === line.id) this.setPlanReading(line.id, false);
          continue;
        }
        addHeader(owner);
        line.soleProcessGroup = !!line.message?.work && counts.get(key) === 1;
      }
      const reply = line.message;
      if (reply && (hidden.has(reply.itemId) || (reply.partialProcess && this.model.hasMoreMessages))) addHeader(reply);
      line.hasProcess = !!reply && shown.has(reply.itemId);
      result.push(line);
    }
    return result;
  }
  private toggleProcess(reply: ChatMessage): void {
    if (!this.active || reply.ownerTarget !== this.model.target()) return;
    const opening = !this.openProcesses.includes(reply.itemId);
    const revision = ++this.processRevision, epoch = this.readingEpoch, motion = this.readingMotion;
    const headerId = 'process:' + reply.itemId;
    const nativeIndex = this.lines.findIndex(line => line.id === headerId) + this.readingPrefix();
    const offset = this.scroller.getItemRect(nativeIndex).y;
    this.holdWorkReading(() => {
      if (opening) {
        this.atEnd = false;
        this.openProcesses = this.openProcesses.concat([reply.itemId]);
      } else this.openProcesses = this.openProcesses.filter(id => id !== reply.itemId);
      this.syncTimeline();
    });
    if (!opening) return;
    const isOpen = (): boolean => this.mounted && this.active && epoch === this.readingEpoch && this.openProcesses.includes(reply.itemId);
    void this.model.expandTurnProcess(reply, isOpen).then(() => {
      if (!isOpen()) return;
      this.getUIContext().postFrameCallback(new ConversationFrame(() => {
        if (!isOpen() || this.processRevision !== revision || this.readingMotion !== motion || !Number.isFinite(offset)) return;
        const index = this.lines.findIndex(line => line.id === headerId);
        if (index < 0) return;
        const nativeIndex = this.readingPrefix() + index, rect = this.scroller.getItemRect(nativeIndex);
        if (!rect.height || Math.abs(rect.y - offset) > Tokens.hairline) {
          this.scroller.scrollToIndex(nativeIndex, false, ScrollAlign.START, {extraOffset:LengthMetrics.vp(-offset)});
        }
      }));
    });
  }
  private planRunning(plan: ConversationPlan, pinned: boolean = false): boolean {
    if (!this.model.turnWorking()) return false;
    if (!plan.sourceId || pinned) return true;
    const origin = this.model.messages.find(message => message.work?.inspectionSteps.some(step => step.id === plan.sourceId));
    return origin?.work?.latestExchange === true && origin.work.running;
  }
  private processLabel(reply: ChatMessage): string {
    const ms = reply.turnMilliseconds;
    if (ms === undefined || !Number.isFinite(ms) || ms < 0) return tr('turn_process');
    const sec = Math.floor(ms / 1000), min = Math.floor(sec / 60);
    const key = min >= 60 ? 'turn_elapsed_hours' : min ? 'turn_elapsed_minutes' : ms > 0 && ms < 1000 ? 'turn_elapsed_short' : 'turn_elapsed_seconds';
    return tr(key).replace('{h}', String(Math.floor(min / 60))).replace('{m}', String(min % 60)).replace('{s}', String(sec % 60));
  }
  @Builder processHeader(reply: ChatMessage) {
    Button({type:ButtonType.Normal, buttonStyle:ButtonStyleMode.TEXTUAL}) {
      Row({space:5}) {
        Text(this.processLabel(reply)).fontSize(Tokens.progressBody).fontColor(Colors.activity).fontWeight(FontWeight.Normal)
        SymbolGlyph(Symbols.expand).fontSize(12).fontColor([Colors.activity])
          .rotate({angle:this.openProcesses.includes(reply.itemId) ? 90 : 0}).animation({duration:this.environment.reduceMotion ? 0 : 160,curve:Curve.EaseOut})
      }.constraintSize({minHeight:32}).width('100%').justifyContent(FlexAlign.Start)
    }.width('100%').height('auto').padding(0).backgroundColor(Color.Transparent)
      .responseRegion({ x:0, y:-12, width:'100%', height:'140%' })
      .accessibilityText(this.processLabel(reply)).accessibilityStateDescription(tr(this.openProcesses.includes(reply.itemId) ? 'work_expanded' : 'work_collapsed'))
      .onClick(() => this.toggleProcess(reply)).id('chat.turn.process.' + reply.itemId)
  }
  private markLatest(lines: ConversationLine[]): void {
    const m = this.model.messages[this.model.messages.length - 1], last = lines[lines.length - 1];
    const work = m?.work, steps = work?.inspectionSteps;
    const step = steps?.length ? steps[steps.length - 1] : undefined;
    const result = step?.kind === 'tool' ? step.result : undefined;
    const text = m?.text || '';
    const marker = last ? JSON.stringify([last.id, last.plan?.current, last.plan?.done, last.decision?.status, m?.id,
      text.length, text.slice(-128), m?.liveSummary, m?.deliveryState, m?.failed, m?.turnDuration,
      work?.toolCount, work?.unresolvedToolCount, work?.thoughtCount, step?.id, result?.id, result?.text?.length]) : '';
    if (this.tailMarker && marker && marker !== this.tailMarker && (!this.atEnd || this.photoOpen)) this.unseenLatest = true;
    this.tailMarker = marker;
  }
  private setPlanReading(id: string, open: boolean): void {
    if (!open && id !== this.readingPlan) return;
    this.readingPlan = open ? id : '';
    const index = open ? this.lines.findIndex((line: ConversationLine) => line.id === id) : -1;
    this.beforePlan = index > 0 ? this.lines[index - 1].id : '';
  }
  aboutToAppear(): void {
    this.mounted = true;
    this.questions.sync(this.model, this.active); this.chooser.sync(this.model, this.active);
    this.syncReading(); this.syncFeed(); this.syncAttachments(); this.syncTimeline(); this.syncRevision(); this.primaryStopChanged();
    this.syncWork();
  }
  aboutToDisappear(): void {
    this.mounted = false; if (this.resumeHistory) this.releaseHistoryRendering();
    this.inspectionRevision++; this.inspectingWork = false;
    this.workView = undefined; this.workTarget = '';
    this.questions.retire(); this.actionIntent = undefined; this.chooser.retire();
    this.retirePhoto();
    this.onPrimaryStop(false); this.readingKey = ''; this.readingEpoch++;
    if (this.feedTimer !== -1) clearTimeout(this.feedTimer); this.feedTimer = -1; this.feed.close(); this.feedKey = '';
    this.attachmentKey = ''; this.attachments.close(); this.invalidateRevisionMenuContext(); this.revisionContextKey = '';
    this.revisions.hide(); this.revisionKey = '';
  }
  @Monitor('model', 'model.selected', 'model.profile') workContextChanged(): void { if (this.mounted) this.syncWork(); }
  private async syncWork(): Promise<void> {
    const target = this.model.target();
    if (this.workTarget === target && this.workView?.valid()) return;
    const lease = this.model.captureView('work:' + target);
    this.workTarget = target; this.workView = lease; this.workReady = false; this.expanded = [];
    try {
      const value = await lease.read();
      if (value && this.workView === lease && lease.valid()) this.expanded = Wire.strings(Wire.parse(value), 'expanded');
    } catch (_) { /* Unreadable preferences start folded. */ }
    if (this.workView === lease && lease.valid()) this.workReady = true;
  }
  private toggleWork(message: ChatMessage): void {
    const lease = this.workView, id = message.id;
    if (!this.active || !this.workReady || !lease?.valid() || message.ownerTarget !== this.model.target() || this.workTarget !== message.ownerTarget) return;
    this.expanded = this.expanded.includes(id) ? this.expanded.filter((value: string) => value !== id) : this.expanded.concat([id]);
    lease.write(JSON.stringify(Wire.payload(['expanded'], [this.expanded]))).catch(() => {
      if (this.workView === lease && lease.valid()) this.model.error = 'cache_unavailable';
    });
    if (this.expanded.includes(id)) this.model.expandWork(message);
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
  @Computed get bubbleColors(): DeviceLookColorSet {
    const palette = this.look.palette(this.model.style);
    return this.environment.dark ? palette.dark : palette.light;
  }
  @Computed get bubbleMaximum(): number {
    return Math.max(Tokens.touch, Math.min(this.availableWidth, Tokens.readingMax) - Tokens.page * 2 - Tokens.touch);
  }
  private messageMaximum(message: ChatMessage): number {
    const hasAttachments = message.imagePaths.length > 0 || message.imageIds.length > 0 || !!message.imageId || message.filePaths.length > 0;
    return this.bubbleMaximum + (hasAttachments ? Tokens.touch - Tokens.section : 0);
  }
  @Computed get markdownWorkspace(): string {
    const row = this.model.selected;
    return row.machine ? row.path || Wire.text(row.data(), 'cwd') : '';
  }

  @Computed get currentSessionData(): JsonObject | undefined {
    if (this.model.selected.kind !== 'session') return undefined;
    const row = this.model.sessions.find((item: ResourceRow) => item.machine === this.model.selected.machine &&
      (item.session || item.id) === (this.model.selected.session || this.model.selected.id)) || this.model.selected;
    return row.data() as Object as JsonObject;
  }
  @Computed get desktopWait(): boolean {
    if (!this.active || !this.model.connected || this.model.demo || this.model.decision || this.model.personaQuestions.length) return false;
    const row = this.model.selected;
    const live = this.model.sessions.find(item => item.machine === row.machine && (item.session || item.id) === (row.session || row.id));
    return row.kind === 'session' && !!live && waitsOnClaudeDesktop(live.data() as Object as JsonObject);
  }
  @Computed get desktopProposal(): DesktopSessionProposal | undefined {
    if (!this.desktopWait) return undefined;
    const rows = this.model.messages;
    for (let i = rows.length - 1; i >= 0; i--) {
      const row = rows[i];
      if (row.ownerTarget !== this.model.target() || row.held || row.localOperation) continue;
      if (row.work) return pendingDesktopProposal(row.work);
      if (row.role === 'user' || row.role === 'assistant') break;
    }
    return undefined;
  }
  @Computed get handoffComputer(): ResourceRow | undefined {
    return this.model.machines.find(row => row.id === this.model.selected.machine && row.supportsScreen() &&
      (!row.group || row.group === this.model.profile.id) && (!row.manager || row.manager === this.model.profile.manager));
  }
  @Builder desktopWaitContent() {
    Column({space:3}) {
      Row({space:8}) {
        Text(tr(this.desktopProposal ? 'claude_start_needs_input' : 'claude_desktop_needs_input'))
          .fontSize(Tokens.meta).fontWeight(FontWeight.Medium).fontColor(Colors.ink).layoutWeight(1)
        if (this.handoffComputer) {
          Button(tr('claude_desktop_view_request'), { buttonStyle: ButtonStyleMode.TEXTUAL })
            .fontSize(Tokens.meta).fontColor(Colors.link).height('auto').constraintSize({minHeight:36})
            .onClick(() => {
              const computer = this.handoffComputer;
              if (this.admission() && this.desktopWait && computer) this.onOpenComputer(computer);
            }).id('chat.desktop.wait.open')
        }
      }.width('100%')
      if (this.desktopProposal?.title) Text(this.desktopProposal!.title).fontSize(Tokens.meta).fontColor(Colors.ink).maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis})
      if (this.desktopProposal?.branch) Text(tr('claude_desktop_worktree_branch').replace('{branch}', () => this.desktopProposal!.branch))
        .fontSize(Tokens.caption).fontColor(Colors.secondary).maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis})
      if (!this.handoffComputer) Text(tr('claude_desktop_handle_on_computer')).fontSize(Tokens.caption).fontColor(Colors.secondary)
    }.width('100%').alignItems(HorizontalAlign.Start).padding({left:12,right:8,top:4,bottom:8})
      .backgroundColor(Colors.grouped).borderRadius(Tokens.inset).id('chat.desktop.wait')
  }
  @Computed get sessionIssue(): AgentEventDisplay | undefined {
    if (!this.currentSessionData) return undefined;
    return sessionIssueDisplay(this.currentSessionData, tr);
  }
  @Computed get issueEchoId(): string {
    const issue = this.sessionIssue;
    if (!issue) return '';
    const last = this.model.messages.filter(m => m.role === 'assistant' || m.role === 'user' || m.event?.kind === 'provider_error').pop();
    if (!last || last.role === 'user') return '';
    const text = last.event?.kind === 'provider_error' ? last.event.text : last.text;
    return text && text.trim() === issue.body.trim() ? last.id : '';
  }
  private showPhoto(request: ConversationPhotoRequest): void {
    const item = request.gallery.entries[request.gallery.selectedIndex];
    if (this.photoDialog || !this.active || !item || item.target !== this.model.target() || request.scope !== this.model.scope() ||
      request.hub !== (this.model.profile.hubId || this.model.profile.id)) return;
    // A page cover hides the NavDestination and pauses its live session. A dialog keeps the chat visible.
    const dialog = new promptAction.DialogController();
    this.photoDialog = dialog;
    this.photoOpen = true;
    this.getUIContext().getPromptAction().presentCustomDialog(() => this.photoContent(request, dialog), dialog, {
      width:'100%', height:'100%', alignment:DialogAlignment.Center, cornerRadius:0,
      maskColor:Color.Transparent, backgroundColor:Color.Transparent, backgroundBlurStyle:BlurStyle.NONE,
      transition:this.environment.reduceMotion ? TransitionEffect.IDENTITY : TransitionEffect.OPACITY.animation({duration:160}),
      onDidDisappear:() => this.finishPhoto(dialog)
    }).then(() => {
      if (this.photoDialog !== dialog) dialog.close();
    }).catch(() => {
      if (this.photoDialog !== dialog) return;
      this.finishPhoto(dialog);
      if (this.mounted && this.active) this.getUIContext().getPromptAction().showToast({ message:tr('preview_unavailable') });
    });
  }
  private finishPhoto(dialog: promptAction.DialogController): void {
    if (this.photoDialog !== dialog) return;
    this.photoDialog = undefined;
    if (this.unseenLatest) { this.atEnd = false; }
    this.photoOpen = false;
  }
  private retirePhoto(): void {
    const dialog = this.photoDialog;
    if (!dialog) return;
    this.finishPhoto(dialog);
    dialog.close();
  }
  @Builder photoContent(request: ConversationPhotoRequest, dialog: promptAction.DialogController) {
    ConversationPhotoViewer({ request, model:this.model, active:this.active, onClose:() => dialog.close() })
  }
  @Builder workImages(message: ChatMessage, step: ToolInspectionStep) {
    ForEach(step.images.slice(), (item: TranscriptItem) => {
      TranscriptImage({ model: this.model, itemId: item.id, active: this.active && message.ownerTarget === this.model.target(),
        sourceKey: this.imageKey(message, 'tool', item.id), galleryEntries: this.imageGallery,
        galleryIndex: this.imageIndex(message, 'tool', item.id), onPreview:(request: ConversationPhotoRequest) => this.showPhoto(request), onEdit: (image: LoadedMessageImage) => this.editReceivedImage(image) })
    }, (item: TranscriptItem) => this.imageKey(message, 'tool', item.id))
  }
  @Builder userThumbnail(message: ChatMessage, source: MessageImageGalleryEntry, width: number, height: number) {
    TranscriptImage({ model:this.model, remotePath:source.remotePath, itemId:source.itemId, sourceKey:source.key,
      active:this.active && message.ownerTarget === this.model.target(), galleryEntries:this.imageGallery,
      galleryIndex:this.imageIndices.get(source.key) ?? -1, maximumThumbnailWidth:width, maximumThumbnailHeight:height,
      onPreview:(request: ConversationPhotoRequest) => this.showPhoto(request), onEdit:(image: LoadedMessageImage) => this.editReceivedImage(image) })
  }
  @Builder userImages(message: ChatMessage, sources: MessageImageGalleryEntry[]) {
    if (sources.length === 1) {
      this.userThumbnail(message, sources[0], Math.min(this.bubbleMaximum, Tokens.attachmentSingleWidth), Tokens.attachmentSingleHeight)
    } else if (sources.length > 1) {
      Scroll() {
        Row({space:Tokens.gap}) {
          ForEach(sources, (source: MessageImageGalleryEntry) => {
            this.userThumbnail(message, source, Math.min(this.bubbleMaximum, Tokens.attachmentItem), Tokens.attachmentPreview)
          }, (source: MessageImageGalleryEntry) => source.key)
        }.alignItems(VerticalAlign.Center)
      }.scrollable(ScrollDirection.Horizontal).scrollBar(BarState.Auto).align(Alignment.End)
        .width(Math.min(this.bubbleMaximum, sources.length * Tokens.attachmentItem + (sources.length - 1) * Tokens.gap))
        .height(Tokens.attachmentPreview).clip(true).id('chat.attachments.images.' + message.id)
    } else if (message.localImageCount > 0) {
      Text(tr('photos') + ' · ' + message.localImageCount).fontSize(Tokens.meta).fontColor(Colors.secondary)
    }
  }
  @Builder attachedFile(path: string) {
    Button({type:ButtonType.Normal, buttonStyle:ButtonStyleMode.TEXTUAL}) {
      Row({space:Tokens.gap}) {
        SymbolGlyph(Symbols.document).fontSize(Tokens.meta).fontColor([Colors.activity])
        Text(attachmentFileName(path)).fontSize(Tokens.meta).fontColor(Colors.link)
          .maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis}).ellipsisMode(EllipsisMode.CENTER).flexShrink(1)
      }
    }.constraintSize({maxWidth:this.bubbleMaximum,minHeight:Tokens.touch}).padding({left:Tokens.gap,right:Tokens.gap})
      .backgroundColor(Color.Transparent).accessibilityText(attachmentFileName(path)).onClick(() => this.onOpenFile(path))
  }
  private timeHint(message: ChatMessage): string {
    if (message.role !== 'user' || !(message.at > 0)) return '';
    return tr(this.timeMessageId === message.id ? 'chat_hide_sent_time' : 'chat_show_sent_time');
  }
  private displayLanguage(): string | undefined {
    if (this.model.language !== 'default') return this.model.language;
    try { return i18n.getSystemLanguage() || undefined; } catch (_) { return undefined; }
  }
  private toggleMessageTime(message: ChatMessage): void {
    if (!this.active || !this.timeHint(message) || (message.ownerTarget && message.ownerTarget !== this.model.target())) return;
    this.holdWorkReading(() => { this.timeMessageId = this.timeMessageId === message.id ? '' : message.id; });
  }
  @Builder messageTime(message: ChatMessage) {
    if (this.timeMessageId === message.id) {
      Text(messageSentTime(message.at, this.displayLanguage())).fontSize(Tokens.toolMeta).fontColor(Colors.secondary)
        .textAlign(TextAlign.End).margin({ right:Tokens.fine }).onClick(() => this.toggleMessageTime(message))
        .accessibilityDescription(this.timeHint(message)).id('chat.sentTime.' + message.id)
    } else if (!message.text) {
      Button({ type:ButtonType.Normal, buttonStyle:ButtonStyleMode.TEXTUAL }) {
        SymbolGlyph($r('sys.symbol.clock')).fontSize(Tokens.meta).fontColor([Colors.secondary])
      }.width(Tokens.touch).height(28).padding(0).backgroundColor(Color.Transparent)
        .accessibilityText(this.timeHint(message)).onClick(() => this.toggleMessageTime(message)).id('chat.attachmentTime.' + message.id)
    }
  }
  @Builder workBody(entry: RepeatItem<ConversationLine>) {
    ConversationWork({ work: entry.item.message!.work!, ownerTarget: entry.item.message!.ownerTarget,
          batch:entry.item.workBatch, batched:entry.item.message!.work!.inspectionSteps.length > inlineWorkLimit, continuation:entry.item.continuation, joinsNext:entry.item.joinsNext, liveSummary:entry.item.message!.liveSummary, providerRetry:entry.item.message!.providerRetry, expanded: entry.item.continuation || (entry.item.soleProcessGroup ? !this.closedSoloGroups.includes(entry.item.message!.id) : this.expanded.includes(entry.item.message!.id)),
          hold: (change: () => void): void => this.holdWorkReading(change),
          images: (step: ToolInspectionStep): void => this.workImages(entry.item.message!, step),
          ready: this.workReady, active: this.active, onToggle: () => {
            const message = entry.item.message!;
            if (!entry.item.soleProcessGroup) { this.toggleWork(message); return; }
            if (this.closedSoloGroups.includes(message.id)) {
              this.closedSoloGroups = this.closedSoloGroups.filter(id => id !== message.id);
              void this.model.expandWork(message);
            } else this.closedSoloGroups = this.closedSoloGroups.concat([message.id]);
          },
          onLoad: () => { if (this.active) this.model.expandWork(entry.item.message!); } })
  }
  @Builder message(entry: RepeatItem<ConversationLine>) {
    Column({ space: Tokens.gap }) {
      if (entry.item.message!.work) {
        this.workBody(entry)
      }
      if (entry.item.message!.id === this.issueEchoId && this.sessionIssue) {
        AgentEventLine({ event:this.sessionIssue!, identity:this.model.target() + ':' + entry.item.message!.id })
      } else if (entry.item.message!.event) {
        if (entry.item.message!.event!.kind === 'model_change') {
          Row({space:8}) {
            Divider().layoutWeight(1).color(Colors.divider)
            Text(tr('chat_model_change').replace('{from}', entry.item.message!.event!.text || '').replace('{to}', String(entry.item.message!.event!.model || '')))
              .fontSize(Tokens.caption).fontColor(Colors.secondary).constraintSize({maxWidth:'86%'}).textAlign(TextAlign.Center)
            Divider().layoutWeight(1).color(Colors.divider)
          }.width('100%').padding({top:8,bottom:8})
        } else if (entry.item.message!.event!.kind === 'turn_duration') {
          Text(tr('ev_elapsed') + ' · ' + recordedDuration(transcriptRecord(entry.item.message!.event!.turn_duration)?.duration_ms))
            .fontSize(Tokens.caption).fontColor(Colors.secondary)
        } else {
          AgentEventLine({ event: agentEventDisplay(entry.item.message!.event!, tr), sourceMessage: entry.item.message!.event!.kind === 'text',
            onLink: (address: string): void => this.onLink(address), hold: (change: () => void): void => this.holdWorkReading(change),
            identity: this.model.target() + entry.item.message!.id,
            clock: eventClock(entry.item.message!.event!.ts) })
        }
      }
      if (entry.item.message!.queued) Text(tr('ev_queued')).fontSize(Tokens.caption).fontColor(Colors.secondary)
      if (entry.item.message!.source) Text(entry.item.message!.source).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).width('100%')
      if (entry.item.message!.role === 'user') {
        if (entry.item.message!.ownerTarget === this.model.target()) this.userImages(entry.item.message!, messageAttachmentImages(entry.item.message!, this.model.target()))
      } else if (entry.item.message!.ownerTarget === this.model.target()) {
        ForEach(messageAttachmentImages(entry.item.message!, this.model.target()), (source: MessageImageGalleryEntry) => {
          TranscriptImage({ model:this.model, remotePath:source.remotePath, itemId:source.itemId, active:this.active,
            sourceKey:source.key, galleryEntries:this.imageGallery, galleryIndex:this.imageIndices.get(source.key) ?? -1,
            onPreview:(request: ConversationPhotoRequest) => this.showPhoto(request), onEdit:(image: LoadedMessageImage) => this.editReceivedImage(image) })
        }, (source: MessageImageGalleryEntry) => source.key)
      }
      ForEach(entry.item.message!.filePaths, (path: string) => this.attachedFile(path), (path: string, index: number) => JSON.stringify([path,index]))
      if (entry.item.message!.role === 'live_activity') {
        Row({ space:6 }) {
          if (entry.item.message!.providerRetry) ToolGlyph({ name:'wifi', ink:Colors.activity }).id('chat.provider.retry')
          Text(entry.item.message!.text).fontSize(Tokens.toolLabel).fontColor(Colors.activity)
            .lineHeight(Tokens.toolLeading).wordBreak(WordBreak.BREAK_WORD).layoutWeight(1)
        }.width('100%').alignItems(VerticalAlign.Center).padding({top:4,bottom:4}).id('chat.live.activity')
      } else if (entry.item.message!.text && entry.item.message!.id !== this.issueEchoId) {
        WithTheme({ colorMode: this.environment.dark ? ThemeColorMode.DARK : ThemeColorMode.LIGHT, theme: nativeMessageTheme(this.look, this.model.style, entry.item.message!.role === 'user', this.environment.dark) }) {
        MessageSurface({ own: entry.item.message!.role === 'user', structured: entry.item.message!.role === 'user' && structuredBubble(entry.item.message!.text), text:entry.item.message!.text, maximum: entry.item.message!.role === 'user' ? this.messageMaximum(entry.item.message!) : Tokens.readingMax,
          timeHint:this.timeHint(entry.item.message!), onTime:() => this.toggleMessageTime(entry.item.message!) }) {
          if (entry.item.message!.role === 'assistant' && this.model.selected.kind === 'conversation') AssistantMessage({ model: this.model, documents:this.documents, feed: this.feed, message: entry.item.message!, active: this.active, workspaceRoot:this.markdownWorkspace, mediaWidth:Math.min(Tokens.readingMax,this.availableWidth - 2 * Tokens.page), onPreview:(request: ConversationPhotoRequest) => this.showPhoto(request), onEditImage:(image: LoadedMessageImage) => this.editReceivedImage(image), onOpenFile:(path: string) => this.onOpenFile(path), onLink: (address: string) => this.onLink(address), onOpenResource: (row: ResourceRow) => this.onOpenResource(row) })
          else MarkdownText({ transcript: true, documents:this.documents, text: entry.item.message!.text, narration:entry.item.message!.narration, ownMessage:entry.item.message!.role === 'user',
            onBodyTap:entry.item.message!.role === 'user' && entry.item.message!.at > 0 ? () => this.toggleMessageTime(entry.item.message!) : undefined,
            messageId:entry.item.message!.id, clipped:entry.item.message!.userTextClipped,
            readOriginal:() => this.model.readFullUserText(entry.item.message!),
            messageInk:entry.item.message!.role === 'user' ? this.bubbleColors.userBubbleText : undefined,
            messageQuoteInk:entry.item.message!.role === 'user' ? deviceLookQuoteInk(this.bubbleColors) : undefined, ownWidth:Math.max(1,this.messageMaximum(entry.item.message!) - 2 * Tokens.inset),
            hold:(change: () => void) => this.holdWorkReading(change), fitContent: entry.item.message!.role === 'user' && !structuredBubble(entry.item.message!.text),
            workspaceRoot:this.markdownWorkspace, mediaModel:entry.item.message!.role === 'assistant' ? this.model : undefined,
            mediaActive:this.active, mediaTarget:entry.item.message!.ownerTarget, mediaWidth:Math.min(Tokens.readingMax,this.availableWidth - 2 * Tokens.page),
            onPreview:(request: ConversationPhotoRequest) => this.showPhoto(request), onEditImage:(image: LoadedMessageImage) => this.editReceivedImage(image),
            onOpenFile: (path: string) => this.onOpenFile(path), onLink: (address: string) => this.onLink(address) })
        }
        }
      }
      if (entry.item.message!.role === 'user' && entry.item.message!.at > 0) this.messageTime(entry.item.message!)
      if (entry.item.message!.held) this.heldStatus(entry.item.message!)
      if (entry.item.message!.turnDuration && !entry.item.hasProcess) Text(tr('ev_elapsed') + ' ' + entry.item.message!.turnDuration)
        .fontSize(Tokens.caption).fontColor(Colors.secondary)
      if (entry.item.message!.deliveryState && entry.item.message!.deliveryState !== 'done' && !entry.item.message!.failed) Text(tr(entry.item.message!.deliveryState === 'unknown' ? 'result_unknown' : entry.item.message!.deliveryState)).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary'))
      if (entry.item.message!.failed) Text(tr('failed')).fontColor($r('app.color.persona_danger')).fontSize(Tokens.meta)
      if (entry.item.message!.localOperation && this.model.canCheckLocalMessage(entry.item.message!.localOperation)) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('checked')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('checked', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('checked')).bindTips(tr('checked')).onClick(() => this.confirmChecked(entry.item.message!.localOperation))
      if (entry.item.message!.localOperation && this.model.canRestoreMessage(entry.item.message!.localOperation)) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('restore_draft')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('restore_draft', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('restore_draft')).bindTips(tr('restore_draft')).onClick(() => this.restoreDraft(entry.item.message!))
    }.width('100%').alignItems(entry.item.message!.role === 'user' ? HorizontalAlign.End : HorizontalAlign.Start)
      .padding({ left: Tokens.page, right: Tokens.page, top: entry.item.message!.work ? 0 : Tokens.fine, bottom: entry.item.message!.work ? 0 : Tokens.fine })
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
  private async takeOver(continuation: boolean): Promise<void> {
    if (!this.sendCurrent() || this.takingOver) return;
    const model = this.model, key = this.composerOwnerKey(), visibility = this.revisionVisibilityRevision;
    const current = (): boolean => this.sendCurrent() && this.model === model && this.composerOwnerKey() === key && this.revisionVisibilityRevision === visibility;
    this.takingOver = true;
    try {
      const next = await model.takeOverSession(continuation, (fresh: boolean): Promise<boolean> => new Promise<boolean>((resolve) => {
        if (!current()) { resolve(false); return; }
        const label = tr(fresh ? 'agent_continue_session' : 'agent_resume_session');
        this.getUIContext().showAlertDialog({ title:label, message:tr(fresh ? 'agent_confirm_continue' : 'agent_confirm_resume'),
          primaryButton:{value:tr('cancel'),action:() => resolve(false)}, secondaryButton:{value:label,action:() => resolve(current())},
          cancel:() => resolve(false) });
      }), current);
      if (next && current()) this.onOpenResource(next);
    } catch (error) { if (current()) model.reportError(error as Error); }
    finally { this.takingOver = false; }
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
      Stack({ alignContent: Alignment.Bottom }) {
        List({ scroller: this.scroller }) {
          if (this.model.selected.kind === 'session' && this.model.chatHistoryLoading && !this.model.transcriptHistoryReady &&
            !this.lines.length && !this.model.error) ListItem() {
            Text(tr('loading')).fontSize(Tokens.meta).fontColor(Colors.secondary).padding(Tokens.page).id('chat.history.loading')
          }
          if (this.feed.error && this.feed.error !== this.model.error) ListItem() {
            Row({ space: Tokens.gap }) { Text(tr(this.feed.error)).fontSize(Tokens.meta).layoutWeight(1); Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('retry')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('retry', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('retry')).bindTips(tr('retry')).onClick(() => this.feed.refresh()) }.padding(Tokens.inset)
          }
          if (this.model.selected.kind === 'conversation') ListItem() {
            AssistantCards({ model: this.model, feed: this.feed, active: this.active && this.model.connected, onOpenResource: (row: ResourceRow) => this.onOpenResource(row), dockOnly: true, onOpenCard: (itemId: string, kind: string) => this.onOpenCard(itemId, kind), onOpenSession: (row: ResourceRow) => this.onOpenResource(row),
              onOpenTodo: (row: ResourceRow) => this.onOpenResource(row), onOpenInbox: (row: ResourceRow) => this.onOpenResource(row), onLink: (address: string) => this.onLink(address) })
          }
          if (this.model.hasMoreMessages || this.feed.nextCursor) ListItem() {
            Row() {
              Button({ type:ButtonType.Normal, buttonStyle:ButtonStyleMode.TEXTUAL }) {
                Row({space:4}) {
                  SymbolGlyph(Symbols.up).fontSize(12).fontColor([Colors.activity])
                  Text(tr(!this.model.connected ? 'offline' : this.readingLoading ? 'loading' : this.earlierFailed ? 'retry' : 'load_more')).fontSize(Tokens.toolLabel).fontColor(Colors.activity)
                }
              }.width('100%').height(44).padding({left:8,right:8}).backgroundColor(Color.Transparent).enabled(this.model.connected)
                .accessibilityText(tr(!this.model.connected ? 'offline' : this.readingLoading ? 'loading' : this.earlierFailed ? 'retry' : 'load_more')).onClick(() => this.loadEarlier()).id('chat.history.more')
            }.width('100%').justifyContent(FlexAlign.Center)
          }
          Repeat(this.lines).each((entry: RepeatItem<ConversationLine>) => {
            ListItem() {
              if (entry.item.processReply) this.processHeader(entry.item.processReply!)
              else if (entry.item.message?.nextStep) {
                NextStepCard({ proposal: entry.item.message!.nextStep!, selected: this.selectedNextStep(entry.item.message!), choicesEnabled: this.canSendNextStep(),
                  choose: (label: string): void => { void this.useNextStep(entry.item.message!, label); },
                  dismiss: (): void => this.dismissNextStep(entry.item.message!) })
              } else if (entry.item.plan) {
                ConversationPlanCard({ plan: entry.item.plan!, scope: entry.item.id, active: this.active, running: this.planRunning(entry.item.plan!, entry.item.planPinned),
                  note: entry.item.planPinned ? tr('plan_before_last_message') : '',
                  onExpansion: (open: boolean): void => this.setPlanReading(entry.item.id, open),
                  hold: (change: () => void): void => this.holdWorkReading(change),
                  read: (plan: ConversationPlan, valid: () => boolean): Promise<ConversationPlan | undefined> => this.model.readPlanDetails(plan, valid) })
              } else if (entry.item.continuation) {
                Column() { this.workBody(entry) }.width('100%').padding({left:Tokens.page,right:Tokens.page})
              } else if (entry.item.message) this.message(entry)
              else if (entry.item.card) AssistantCards({ model: this.model, feed: this.feed, active: this.active && this.model.connected, onOpenResource: (row: ResourceRow) => this.onOpenResource(row), item: entry.item.card, onOpenCard: (itemId: string, kind: string) => this.onOpenCard(itemId, kind), onOpenSession: (row: ResourceRow) => this.onOpenResource(row),
                onOpenTodo: (row: ResourceRow) => this.onOpenResource(row), onOpenInbox: (row: ResourceRow) => this.onOpenResource(row), onLink: (address: string) => this.onLink(address) })
              else if (entry.item.decision) this.questionCard(entry)
            }.padding({ left: entry.item.message && !entry.item.message.nextStep ? 0 : Tokens.page, right: entry.item.message && !entry.item.message.nextStep ? 0 : Tokens.page }).margin({bottom:entry.item.processReply || entry.item.joinsNext ? 0 : Tokens.fine}).id('chat.line.' + entry.item.id)
              .onSizeChange(() => this.followReading())
          }).key((line: ConversationLine) => line.id).virtualScroll({ totalCount: this.lines.length, reusable: false })
          if (this.sessionIssue && !this.issueEchoId) ListItem() {
            AgentEventLine({ event: this.sessionIssue!, identity: this.model.target() + this.sessionIssue!.label })
          }.padding({ left: Tokens.page, right: Tokens.page }).id('chat.turn.issue')
        }.cachedCount(2).width('100%').height('100%').id('chat.transcript')
          .bindSheet(this.questions.shown, this.questionContent(), { title: { title: tr('interaction_question_reopen') }, height: SheetSize.LARGE, keyboardAvoidMode: SheetKeyboardAvoidMode.RESIZE_ONLY, showClose: true, onWillAppear: () => this.presentedQuestion = this.questions.beginPresentation(), onAppear: () => { this.questionLayoutReady = true; this.measureQuestion(); }, onWillDismiss: (action: DismissSheetAction) => this.questions.dismiss(this.presentedQuestion, () => action.dismiss()), onDisappear: () => { this.questionLayoutReady = false; this.questions.endPresentation(this.presentedQuestion); this.presentedQuestion = undefined; } })
          .maintainVisibleContentPosition(!this.atEnd || this.photoOpen)
          .onWillScroll((offset: number, _state: ScrollState, source: ScrollSource) => this.userScroll(offset, source))
          .onScrollIndex((start: number, end: number) => {
            this.trackHistoryRendering(end);
            this.approachHistory(start);
            this.lastVisibleIndex = end;
            this.firstVisibleIndex = start;
          })
          .onReachEnd(() => this.reachedLatest())
          .onScrollStop(() => this.finishScroll())
          .onSizeChange(() => this.followReading())
        if (!this.atEnd && this.lines.length > 0) {
          Button({ type: ButtonType.Circle }) {
            SymbolGlyph(IconActions.symbol('latest')).fontSize(19)
              .fontColor([this.unseenLatest ? Colors.link : Colors.ink])
          }.width(36).height(36).padding(0).margin({ bottom: 8 })
            .backgroundColor(Colors.planSurface).border({ width: Tokens.hairline, color: Colors.planBorder })
            .responseRegion({ x: -6, y: -6, width: Tokens.touch, height: Tokens.touch })
            .accessibilityText(tr('chat_jump_to_latest'))
            .accessibilityDescription(this.unseenLatest ? tr('chat_new_messages') : '')
            .onClick(() => this.jumpToLatest()).id('chat.latest')
        }
      }.layoutWeight(1).width('100%')
    }.width('100%').constraintSize({ maxWidth: Tokens.readingMax }).height('100%')
      .parallelGesture(TapGesture().onAction(() => this.dismissEditor()))
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

  @Computed get sessionIdentity(): ComposerIdentity { return sessionComposerIdentity(this.currentSessionData); }
  @Computed get contextUsage(): SessionContextUsage | undefined {
    const row = this.model.selected;
    if (row.kind === 'session') return sessionContextUsage(this.currentSessionData);
    if (row.kind !== 'conversation') return undefined;
    const usage = sessionContextUsage(row.data() as Object as JsonObject);
    if (usage || !row.session || !row.machine) return usage;
    const session = this.model.sessions.find((item: ResourceRow) => item.machine === row.machine && (item.session || item.id) === row.session);
    return session ? sessionContextUsage(session.data() as Object as JsonObject) : undefined;
  }
  @Computed get contextPercent(): number { return this.contextUsage?.percent ?? 0; }
  @Computed get contextDetails(): string {
    const usage = this.contextUsage;
    if (!usage) return '';
    const language = this.displayLanguage();
    return tr('chat_context_tokens').replace('{used}', usage.tokens.toLocaleString(language))
      .replace('{window}', usage.capacity.toLocaleString(language)).replace('{left}', String(100 - usage.percent));
  }
  @Builder contextMeter() {
    Button({ type:ButtonType.Normal, buttonStyle:ButtonStyleMode.TEXTUAL }) {
      Row({ space:Tokens.fine }) {
        Progress({ value:this.contextPercent, total:100, type:ProgressType.Ring })
          .width(13).height(13).style({ strokeWidth:2, enableSmoothEffect:!this.environment.reduceMotion })
          .color(this.contextPercent >= 90 ? Colors.danger : this.contextPercent >= 70 ? Colors.working :
            this.contextPercent >= 45 ? Colors.action : Colors.teal)
          .backgroundColor(Colors.control).accessibilityLevel('no')
        Text(this.contextPercent + '%').fontSize(Tokens.caption)
          .fontColor(Colors.secondary).maxLines(1)
      }
    }.padding({ left:Tokens.fine, right:Tokens.fine }).constraintSize({ minHeight:28 })
      .backgroundColor(Color.Transparent).accessibilityText(tr('chat_context_used').replace('{percent}', String(this.contextPercent)))
      .accessibilityDescription(this.contextDetails).onClick(() => this.contextDetailOpen = !this.contextDetailOpen)
      .bindPopup(this.contextDetailOpen, { message:this.contextDetails, placement:Placement.TopRight, autoCancel:true,
        onStateChange:(event: PopupStateChangeParam) => { if (!event.isVisible) this.contextDetailOpen = false; } })
      .id('chat.context.usage')
  }
  @Builder identityOptions() {
    Row({space:Tokens.fine}) {
      if (this.sessionIdentity.effort) Text(this.sessionIdentity.effort).fontSize(Tokens.caption).fontColor(Colors.secondary)
        .maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis}).flexShrink(1)
      if (this.sessionIdentity.speed && this.sessionIdentity.effort) Text('·').fontSize(Tokens.caption).fontColor(Colors.secondary)
      if (this.sessionIdentity.speed) Text(this.sessionIdentity.speed).fontSize(Tokens.caption).fontColor(Colors.secondary)
        .maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis}).flexShrink(1)
    }.constraintSize({ maxWidth:'48%' })
  }
  @Builder composerIdentity() {
    Row({space:Tokens.gap}) {
      if (this.sessionIdentity.agent) {
        if (agentBrand(this.sessionIdentity.brand)) {
          Image(agentBrand(this.sessionIdentity.brand)).width(Tokens.page).height(Tokens.page).objectFit(ImageFit.Contain)
            .fillColor(agentBrandColor(this.sessionIdentity.brand))
            .accessibilityText(this.sessionIdentity.agent)
        } else {
          SymbolGlyph(Symbols.agents).fontSize(Tokens.page).fontColor([Colors.teal]).accessibilityText(this.sessionIdentity.agent)
        }
      }
      if (this.sessionIdentity.model) {
        Text(this.sessionIdentity.model).fontSize(Tokens.caption).fontColor(Colors.secondary)
          .maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis}).layoutWeight(1)
      } else { Blank().layoutWeight(1) }
      if (this.sessionIdentity.effort || this.sessionIdentity.speed) this.identityOptions()
      if (this.contextUsage) this.contextMeter()
    }.width('100%').alignItems(VerticalAlign.Center).padding({left:Tokens.gap,right:Tokens.gap})
      .accessibilityGroup(!this.contextUsage).accessibilityText([this.sessionIdentity.agent, this.sessionIdentity.model,
        this.sessionIdentity.effort ? tr('effort') + ' ' + this.sessionIdentity.effort : '',
        this.sessionIdentity.speed ? tr('composer_speed') + ' ' + this.sessionIdentity.speed : ''].filter(s => !!s).join(', '))
      .id('chat.composer.identity')
  }
  @Builder composer() {
    Column({ space: Tokens.gap }) {
      AttachmentStrip({ attachments: this.attachments.attachments, errorKey: this.attachments.errorKey, actionsEnabled: !this.sending && !this.attachments.busy,
          onRemove: (id: string) => this.attachments.remove(id), onEdit: (id: string) => this.attachments.editPhoto(id), onPreview: (item: ChatAttachment) => this.preview(item) })
      if (this.model.sourceContext) Row({ space: Tokens.gap }) {
        Text(this.model.contextTitle()).fontSize(Tokens.meta).fontColor(Colors.ink).maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis }).layoutWeight(1)
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('remove')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('remove', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('remove')).bindTips(tr('remove')).enabled(!this.sending).onClick(() => this.model.removeContext())
      }.width('100%')
      GridRow({ columns: this.composerColumns(), gutter: { x: 0, y: Tokens.gap } }) {
        GridCol({ span: this.expandedComposer() ? this.composerColumns() : this.composerColumns() - this.leftColumns() - this.sendColumns(), order: this.expandedComposer() ? 0 : 1 }) {
          TextArea({ controller:this.editorController, text: this.model.draft, placeholder: this.model.agentInputNotice() === 'agent_computer_holds' ? this.agentNotice() : $r('app.string.message') }).maxLength(Tokens.maxMessage).width('100%')
            .fontSize(Tokens.body).placeholderFont({ size: Tokens.body }).fontColor(Colors.ink).placeholderColor(Colors.secondary)
            .lineHeight(String(Tokens.lineHeight) + 'fp').caretColor(Colors.action).backgroundColor(Color.Transparent)
            .padding({ left: this.expandedComposer() ? Tokens.gap : Tokens.composerGutter, right: Tokens.fine, top: Tokens.inset, bottom: Tokens.inset })
            .constraintSize({ minHeight: Tokens.touch, maxHeight: Math.max(Tokens.inputLines, Tokens.lineHeight * this.environment.fontScale + Tokens.section) })
            .height(this.expandedComposer() && !this.editorDismissed ? undefined : Math.max(Tokens.touch, Tokens.lineHeight * this.environment.fontScale))
            .enabled(!this.sending && !this.model.draftLoading).onFocus(() => { this.editorDismissed = false; this.fieldFocused = true; }).onBlur(() => this.fieldFocused = false)
            .onChange((value: string) => this.model.updateDraft(value)).id('chat.input')
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
          }.width('100%').constraintSize({ minHeight: Tokens.touch }).alignItems(VerticalAlign.Center)
        }
        if (this.expandedComposer() && !this.verticalActions()) GridCol({ span: this.composerColumns() - this.leftColumns() - this.sendColumns(), order: 3 }) {
          Row() {
            Blank().layoutWeight(1)

          }.width('100%').constraintSize({ minHeight: Tokens.touch })
        }
        GridCol({ span: this.verticalActions() ? this.composerColumns() : this.sendColumns(), order: this.expandedComposer() ? this.verticalActions() ? 1 : 4 : 2 }) {
          Row() {
          Button({ type: ButtonType.Circle }) {
            Stack() {
              if (IconActions.busy(this.primaryActionKey())) LoadingProgress().width(18).height(18).color(Colors.onAction)
              else SymbolGlyph(this.primaryActionKey() === 'stop' ? Symbols.stop : IconActions.symbol(this.primaryActionKey()))
                .fontSize(this.primaryActionKey() === 'stop' ? 16 : 18).fontColor([Colors.onAction])
            }.width(Tokens.composerActionDisc).height(Tokens.composerActionDisc).borderRadius(Tokens.composerActionDisc / 2).backgroundColor(Colors.action)
          }.accessibilityText(tr(this.primaryActionKey())).attributeModifier(ButtonStyles.quiet).padding(0).width(Tokens.touch).height(Tokens.touch)
            .enabled(this.sendCurrent() && (this.primaryStop() || this.model.canSend() && !this.model.stopPending() && !this.sending && !this.attachments.busy && !this.model.draftLoading &&
              (this.model.draft.trim().length > 0 || this.attachments.attachments.length > 0) && this.model.operationState !== 'pending' && this.model.operationState !== 'unknown'))
            .onClick(() => { if (!this.sendCurrent()) return; if (this.primaryStop()) this.model.stop(() => this.sendCurrent()); else this.sendDraft(); }).id('chat.send')
          }.width('100%').justifyContent(FlexAlign.End)
        }
      }.width('100%').onAreaChange((_old: Area, area: Area) => { const width = Number(area.width); if (Number.isFinite(width) && width > 0) this.composerWidth = width; })
    }.padding({left:this.expandedComposer() ? Tokens.gap : Tokens.fine,right:Tokens.fine,top:Tokens.fine,bottom:Tokens.fine}).backgroundColor(Colors.subtle).borderRadius(Tokens.capsule).width('100%').id('chat.composer')
  }

  @Builder blockingContent() {
    Column({ space: Tokens.gap }) {
      Column() {
        ForEach(this.model.decision ? [this.model.decision] : [], (request: Decision) => {
          InteractionDecisionForm({ model: this.model, request: request, fillAvailable: true, onLink: (address: string) => this.onLink(address) })
        }, (request: Decision) => request.id)
      }.width('100%').layoutWeight(1)
      if (this.showContextMenu) Button({ type: ButtonType.Circle }) { Image(Symbols.more).width(Tokens.symbol).height(Tokens.symbol).fillColor(Colors.ink) }
        .attributeModifier(ButtonStyles.quiet).width(Tokens.touch).height(Tokens.touch).padding(0).accessibilityText(tr('more')).onClick(() => this.openFallbackActions())
        .bindMenu(this.actionsVisible, this.actionsMenu, this.actionMenuOptions(this.actionsRequest)).id('chat.more.fallback')
    }.width('100%').constraintSize({ maxWidth: Tokens.readingMax }).height('100%')
      .padding({ left: Tokens.page, right: Tokens.page, bottom: Tokens.footer, top: Tokens.page })
  }
  @Builder interactionContent() {
    if (this.model.decision) this.blockingContent()
    else Scroll() {
      Column({ space: Tokens.gap }) {
      if (this.showContextMenu && (this.model.decision || !this.model.canCompose())) Button({ type: ButtonType.Circle }) { Image(Symbols.more).width(Tokens.symbol).height(Tokens.symbol).fillColor(Colors.ink) }
        .attributeModifier(ButtonStyles.quiet).width(Tokens.touch).height(Tokens.touch).padding(0).accessibilityText(tr('more')).onClick(() => this.openFallbackActions())
        .bindMenu(this.actionsVisible, this.actionsMenu, this.actionMenuOptions(this.actionsRequest)).id('chat.more.fallback')
      if (this.desktopWait) this.desktopWaitContent()
      if (this.contextUsage || (this.model.canCompose() && this.sessionIdentity.agent)) this.composerIdentity()
      if (this.model.canCompose() && !this.model.decision) {
        if (this.attachments.busy && !this.sending && (this.attachments.errorKey === 'chat_attachment_storage_unavailable' || this.model.error === 'cache_unavailable')) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('retry')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('retry', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('retry')).bindTips(tr('retry')).onClick(() => { this.attachmentKey = ''; this.syncAttachments(); })
        this.composer()
      } else if (!this.model.canSend() && !this.model.decision) {
        if (this.model.takeoverOffered()) Row({space:Tokens.gap}) {
          if (this.model.takeoverCanResume()) Button(tr('agent_resume_session')).attributeModifier(ButtonStyles.secondary).enabled(!this.takingOver)
            .onClick(() => { void this.takeOver(false); }).id('chat.takeover')
          Button(tr('agent_continue_session')).attributeModifier(ButtonStyles.quiet).enabled(!this.takingOver)
            .onClick(() => { void this.takeOver(true); }).id('chat.continue')
        }.width('100%').justifyContent(FlexAlign.Center)
        if (this.model.canAttach()) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('attach_live')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('attach_live', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('attach_live')).bindTips(tr('attach_live')).onClick(() => this.model.attach(() => this.sendCurrent()))
        Text(this.model.agentInputNotice() ? this.agentNotice() : tr('read_only')).fontSize(Tokens.meta).padding(Tokens.page)
      }
      }.width('100%').constraintSize({ maxWidth: Tokens.readingMax }).padding({ left: Tokens.page, right: Tokens.page, bottom: Tokens.footer, top: Tokens.page }).onAreaChange((_before: Area, area: Area): void => {
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
        Column() { this.interactionContent() }.width('100%')
          .height(this.model.decision ? this.maxControlsHeight() : Math.max(1, Math.min(this.controlsHeight, this.maxControlsHeight())))
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
