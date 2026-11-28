import { inputMethod } from '@kit.IMEKit';
import { KeyCode } from '@kit.InputKit';
import { COMPOSER_GLOW_OUTSET, PhoneComposerGlow } from './PhoneComposerGlow';
import { PhoneChatForward } from '../models/PhoneChatForward';
import { PhoneChatPickBar, PhoneChatForwardTray, PhoneForwardSentCard } from './PhoneChatForward';
import { SessionComposerModeChip } from './SessionComposerModeChip';
import { ComposerSize, restingComposerSize, ComposerReadingSize, composerReadingSize, composerTypeSample, CompactDraftRoom, compactDraftRoom } from '../core/ComposerSize';
import { PhoneMessageLift, PhoneMessageLiftHost, PhoneLiftPresentation } from './PhoneMessageLift';
import { MessageLiftFrame } from '../core/MessageLift';
import { PhoneDictationControl } from './PhoneDictationControl';
import { ConversationRecap } from './ConversationRecap';
import { AssistantWelcome } from './AssistantWelcome';
import { agentBrand, agentBrandColor } from '../design/AgentBrand';
import { ComposerIdentity, sessionComposerIdentity } from '../core/ComposerIdentity';
import { messageSentTime, sessionContextUsage, pendingDesktopProposal, waitsOnClaudeDesktop } from '../core/ConversationMetadata';
import type { DesktopSessionProposal, SessionContextUsage } from '../core/ConversationMetadata';
import { deviceLookQuoteInk, DeviceLookColorSet } from '../core/DeviceLook';
import { ConversationMarkdownCache, parseMarkdown, markdownInlineText, structuredBubble } from '../core/Markdown';
import { ToolGlyph } from '../design/ToolGlyph';
import { IconActions } from '../design/IconActions';
import { Theme, SymbolGlyphModifier, FoldSplitContainer, HoverModeStatus, FrameCallback, LengthMetrics, window, promptAction } from '@kit.ArkUI';
import { ComposerOverlayWorkspace, ComposerOverlaySession } from '../models/ComposerOverlayWorkspace';
import { composerPopupLayout, ComposerPopupLayout } from '../core/ComposerPopupLayout';
import { util } from '@kit.ArkTS';
import type { AgentReadingHeader } from '../models/AgentReadingHeader';
import { HttpFailure } from '../platform/HubTransport';
import { i18n } from '@kit.LocalizationKit';
import { hilog } from '@kit.PerformanceAnalysisKit';
import { NativeEnvironment, nativeEnvironment } from '../platform/NativeEnvironment';
import { DeviceLook, deviceLook } from '../models/DeviceLook';
import { nativeMessageTheme } from '../design/LookTheme';
import { AssistantMessage } from './AssistantMessage';
import { AgentEventLine } from './AgentEventLine';
import { agentEventDisplay, AgentEventDisplay, eventClock, recordedDuration, sessionIssueDisplay } from '../core/AgentEvents';
import { transcriptRecord } from '../core/Domain';
import type { JsonObject, ToolInspectionStep, TranscriptItem, ToolTurnProjection, WorkInspectionStep } from '../core/Domain';
import { NextStepCard } from './NextStepCard';
import { ConversationPlanCard } from './ConversationPlanCard';
import { onlyPlanUpdates, planPinned, planPosition } from '../core/ConversationPlan';
import type { ConversationPlan } from '../core/ConversationPlan';
import { ConversationWork } from './ConversationWork';
import { pasteboard } from '@kit.BasicServicesKit';
import { SessionRevision, SessionRevisionOutcome } from '../models/SessionRevision';
import type { SessionRevisionRestore } from '../core/SessionRevision';
import { ConversationRevisionLease, restoreAcknowledgedSessionDraft } from '../core/ConversationRevisionLease';
import { TranscriptImage } from './TranscriptImage';
import { BeautifulSendStatus } from './BeautifulSendStatus';
import { ConversationPhotoViewer } from './ConversationPhotoViewer';
import { conversationImageEntries, conversationImageKey, attachmentFileName, messageAttachmentImages } from '../core/MessageImageGallery';
import type { MessageImageGalleryEntry, LoadedMessageImage, ConversationPhotoRequest } from '../core/MessageImageGallery';
import { MarkdownText } from './MarkdownText';
import { AssistantCards } from './AssistantCards';
import { DecisionPager } from '../models/DecisionPager';
import { QuestionWorkspace, QuestionSheet, questionPrompt } from '../models/QuestionWorkspace';
import { InteractionDecisionForm } from './InteractionDecisionForm';
import { AgentPendingCards } from './AgentPendingCards';
import { AssistantFeedModel } from '../models/AssistantFeed';
import { ChatAttachments, ChatAttachment } from '../models/ChatAttachments';
import { AttachmentStrip } from './AttachmentStrip';
import { common } from '@kit.AbilityKit';
import { previewAttachment } from '../platform/NativePreview';
import { mergeConversationTimeline, reconcileTimelineRows, TimelineMessage, TimelineCard } from '../core/ConversationTimeline';
import { ClientModel } from '../models/ClientModel';
import { ChatMessage, Decision, ResourceRow, Wire } from '../models/Wire';
import { Tokens } from '../design/Tokens';
import { PhoneStyle } from '../design/PhoneStyle';
import { tr } from '../design/Strings';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { ButtonStyles } from '../design/Controls';
import { nativeSheetInset } from '../platform/NativeInsets';
import { ContextPicker } from './ContextPicker';
import type { ClientViewLease } from '../models/ClientModel';
import { measuredReadingBoundary, parseReadingAnchor, readingAnchorIndex, readingSizeClassChanges } from '../core/ConversationReading';
import type { ReadingAnchor, ReadingBoundary } from '../core/ConversationReading';
import { workingTurnLabel } from '../core/PhoneDate';
import { Motion } from '../design/Motion';
import { MotionTokens } from '../core/MotionTokens';
import { Haptics } from '../platform/Haptics';
import { WorkingArcSpinner } from './StatusMotionMarks';
import { TurnChangesCard } from './WorkStepsCard';
import { evaluateBubbleFold } from '../core/BubbleFold';
import type { BubbleFoldDecision } from '../core/BubbleFold';
import { buildEdgeScrubMarks } from '../core/EdgeScrub';
import type { EdgeScrubMark, EdgeScrubSourceLine } from '../core/EdgeScrub';
import { ChatEdgeScrubOverlay } from './ChatEdgeScrubOverlay';
import { canMutateQueueIndex, extractComposerQueue, mergeQueuedTakeBackDraft, queueKeepSwapped, queueKeepWithout } from '../core/ComposerQueue';
import type { ComposerQueueState } from '../core/ComposerQueue';
import { ComposerTopStrip } from './ComposerTopStrip';
import { isVerifiedStarSource } from '../core/StarredMessages';
import type { StarredSnapshot, StarredSource } from '../core/StarredMessages';
import { starredStore, StarredStore } from '../models/StarredStore';
import { toastCenter } from '../models/ToastCenter';

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
  @Param agentPrompt: boolean = false;
  @Param personaReply: boolean = false;
  @Param structured: boolean = false;
  @Param text: string = '';
  @Param maximum: number = Tokens.readingMax;
  @Param sourceId: string = '';
  @Param timeHint: string = '';
  @Event onTime: () => void = () => {};
  @BuilderParam content: () => void;
  @Local fill: ResourceColor = Color.Transparent;
  @Local private env: NativeEnvironment = nativeEnvironment;
  onWillApplyTheme(theme: Theme): void { this.fill = theme.colors.compBackgroundPrimary; }
  private surfaceFill(): ResourceColor {
    if (this.own) return this.fill;
    if (this.personaReply) {
      if (Colors.styleCard) return Colors.styleCard;
      return this.env.dark ? '#2A2A2E' : '#F0F0F3';
    }
    return Color.Transparent;
  }
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
      .padding(this.agentPrompt ? { left: Tokens.page, right: Tokens.page, top: Tokens.inset, bottom: Tokens.inset } :
        this.personaReply ? { left: 16, right: 16, top: 12, bottom: 12 } :
          this.own ? { left: Tokens.inset, right: Tokens.inset, top: 10, bottom: 10 } : 0)
      .backgroundColor(this.surfaceFill())
      .borderRadius(this.own || this.personaReply ? Tokens.bubble : 0)
      .border(this.own || this.personaReply ? { width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: Tokens.bubble } : { width: 0 })
      .shadow(this.personaReply ? PhoneStyle.cardElevation(this.env.dark) : { radius: 0, color: Color.Transparent })
      .clip(this.own).id(this.sourceId)
  }
  build() {
    if (this.timeHint) {
      Column() { this.surface() }.onClick(() => this.onTime()).accessibilityDescription(this.timeHint)
    } else { this.surface() }
  }
}

@ComponentV2
struct ChatSendStatus {
  @Require @Param phase: string;
  @Require @Param reduced: boolean;
  @Local lit: boolean = false;
  build() {
    Row({space:12}) {
      Row({space:4}) {
        ForEach([0,1,2], (index:number) => {
          Circle().width(7).height(7).fill(Colors.interfaceAccent)
            .opacity(this.reduced ? .35+.325*index : this.lit ? 1 : .35)
            .animation(this.reduced ? undefined : { duration:600, delay:index*110, curve:Curve.EaseInOut, iterations:-1, playMode:PlayMode.Alternate })
        }, (index:number) => String(index))
      }
      Text(tr('chat_feedback_'+this.phase)).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontColor(Colors.secondary)
    }.width('100%').padding({left:Tokens.page,right:Tokens.page,top:2,bottom:3})
      .accessibilityGroup(true).accessibilityText(tr('chat_feedback_'+this.phase)).id('chat.sendFeedback.'+this.phase)
      .onAppear(() => { this.lit = true; })
  }
}

@ComponentV2
struct RunningTurnLabel {
  @Require @Param start: number;
  @Require @Param active: boolean;
  @Local now: number = Date.now();
  private visible: boolean = false;
  private timer: number = -1;
  @Monitor('active', 'start') syncClock(): void {
    if (this.timer !== -1) { clearInterval(this.timer); this.timer = -1; }
    if (!this.visible || !this.active || this.start <= 0) return;
    this.now = Date.now();
    this.timer = setInterval(() => { this.now = Date.now(); }, 1000);
  }
  aboutToDisappear(): void { this.visible = false; this.syncClock(); }
  build() {
    Text(workingTurnLabel(this.start, this.now, tr)).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary)
      .onVisibleAreaChange([0,1], (shown: boolean, ratio: number) => {
        const visible = shown && ratio > 0;
        if (this.visible !== visible) { this.visible = visible; this.syncClock(); }
      })
  }
}

interface ReadingTailPosition {
  index: number;
  id: string;
  scale: number;
  fromViewportBottom: number;
  rowHeight: number;
  distance: number;
  count: number;
  lastId: string;
}

interface MeasuredReadingRow {
  line: ConversationLine;
  height: number;
  atNewest?: number;
  count?: number;
  tail?: ConversationLine;
}

interface ReadingFrameOrigin {
  scale: number;
  width: number;
  epoch: number;
  rows: Map<number, MeasuredReadingRow>;
}

// Long disclosures participate in the transcript's own virtual list. Only nearby batches acquire ArkUI nodes.
const workBatchSize: number = 6;
// A failed history page is read again on the next pull at the top, but not more often than this.
const historyRetryMs: number = 3000;
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
  @Param liftHost: PhoneMessageLiftHost = new PhoneMessageLiftHost();
  @Param readingHeader:AgentReadingHeader | undefined=undefined;
  @Param headerOwner:string='';
  @Param forwardState: PhoneChatForward = new PhoneChatForward();
  @Event onForwardOrigin: (row:ResourceRow, item:string) => void = () => {};
  @Param active: boolean = true;
  @Param searchHitId: string = '';
  @Param searchPresented: boolean = false;
  @Param decisionTool: string | undefined = undefined;
  @Param showContextMenu: boolean = false;
  @Param actionItems: SheetInfo[] = [];
  @Param actionsVisible: boolean = false;
  @Param actionsRequest: number = 0;
  @Event onOpenTriage: () => void = () => {};
  @Event onPlace: (name: string) => void = () => {};
  @Event onOpenComputer: (row: ResourceRow) => void = () => {};
  @Event onShowActions: () => void = () => {};
  @Event onHideActions: (request: number) => void = () => {};
  @Event admission: () => boolean = (): boolean => true;
  @Event onOpenCard: (itemId: string, kind: string) => void = () => {};
  @Event onLongDraftHeader: (floating: boolean) => void = () => {};
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
  @Local private unfoldedBubbles: string[] = [];
  @Local timeMessageId: string = '';
  @Local contextDetailOpen: boolean = false;
  @Local workReady: boolean = false;
  private workTarget: string = '';
  private shownPlanReport: string = '';
  private readingPlan: string = '';
  private beforePlan: string = '';
  private workView: ClientViewLease | undefined;
  private scroller: Scroller = new Scroller();
  private readingLease: ClientViewLease | undefined;
  private readingKey: string = '';
  private readingEpoch: number = 0;
  private readingRestoring: boolean = true;
  private readingAnchorReady: boolean = false;
  private readingFetching: boolean = false;
  @Local readingLoading: boolean = false;
  private readingMotion: number = 0;
  private typeChange: number = 0;
  private tailPosition: ReadingTailPosition | undefined;
  private measuredRows: Map<number, MeasuredReadingRow> = new Map();
  private measuredScale: number = -1;
  private measuredWidth: number = -1;
  private precedingRows: Map<number, MeasuredReadingRow> = new Map();
  private precedingScale: number = -1;
  private lastReadingFrame: ReadingFrameOrigin | undefined;
  private priorReadingFrame: ReadingFrameOrigin | undefined;
  private scrollingByHand: boolean = false;
  private lastManualAnchor: ReadingAnchor | undefined = undefined;
  private manuallyReading: boolean = false;
  private selectedReadingLine: string = '';
  private readingMenuLine: string = '';
  private ownedLift: PhoneLiftPresentation | undefined;
  @Local liftOpen: boolean = false;
  @Local liftMessage: ChatMessage | undefined;
  @Local liftFrame: MessageLiftFrame | undefined;
  @Local liftSelectOnOpen: boolean = false;
  private liftGeneration: number = -1;
  private liftScope: string = '';
  private readingCursors: Set<string> = new Set();
  @Local readingFailed: boolean = false;
  private inspectingWork: boolean = false;
  private inspectionRevision: number = 0;
  @Local openProcesses: string[] = [];
  @Local closedSoloGroups: string[] = [];
  private processRevision: number = 0;
  private documents: ConversationMarkdownCache = new ConversationMarkdownCache();
  private workBatches: WeakMap<ToolTurnProjection, WorkInspectionStep[][]> = new WeakMap();
  private hiddenProcessAnchors: Map<string, string> = new Map();
  private firstVisibleIndex: number = 0;
  private lastVisibleIndex: number = -1;
  private searchedHit: string = '';
  private seekingHit: string = '';
  private searchHighlightTimer: number = -1;
  @Local highlightedLine: string = '';
  private hitScope(): string {
    return JSON.stringify([this.model.scope(),this.model.connectionGeneration(),this.model.target(),this.searchHitId,this.decisionTool]);
  }
  private findSearchLine(hit:string): number {
    const direct = this.lines.findIndex((line:ConversationLine) => !!line.message &&
      (line.message.id === hit || line.message.itemId === hit || (line.message.role === 'recap' && line.message.recaps.some((recap) => recap.id === hit))));
    if (direct >= 0) return direct;
    return this.lines.findIndex((line:ConversationLine) => {
      return line.message?.work?.events.some((item: TranscriptItem) => item.id === hit) === true;
    });
  }
  private clearSearchHighlight(): void {
    if (this.searchHighlightTimer !== -1) clearTimeout(this.searchHighlightTimer);
    this.searchHighlightTimer = -1; this.highlightedLine = '';
  }
  @Monitor('searchHitId','decisionTool') requestedHitChanged(): void {
    this.searchedHit = ''; this.seekingHit = ''; this.clearSearchHighlight(); this.scheduleReading();
  }
  private async seekSearchHit(key:string): Promise<void> {
    if (this.seekingHit === key) return;
    this.lastManualAnchor = undefined;
    const owner = this.model, source = this.decisionTool, epoch = this.readingEpoch, motion = this.readingMotion;
    let hit = this.searchHitId;
    const current = (): boolean => this.active && this.model === owner && this.seekingHit === key &&
      epoch === this.readingEpoch && this.hitScope() === key && this.readingMotion === motion;
    this.seekingHit = key;
    try {
      for (let page = 0; page <= 6 && current(); page++) {
        if (source !== undefined) hit = owner.decisionSourceMessage(source);
        const index = hit ? this.findSearchLine(hit) : -1;
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
        if (source === '' || page === 6 || !owner.hasMoreMessages || !owner.connected || !await owner.loadOlder()) break;
        if (current()) this.syncTimeline();
      }
    } catch (_) { /* Leave this conversation readable when more history is unavailable. */ } finally {
      if (current() && source !== undefined && (!hit || this.findSearchLine(hit) < 0)) {
        this.scroller.scrollEdge(Edge.Bottom); this.atEnd = true; this.readingRestoring = false;
      }
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
  private resumeHistory?: () => void;
  private timelinePending: boolean = false;
  private autoPageCursor: number = -1;
  private revealOlder: boolean = false;
  @Local earlierFailed: boolean = false;
  private earlierFailedAt: number = 0;
  private userScrolling: boolean = false;
  private followEpoch: number = -1;
  private tailMarker: string = '';
  @Local unseenLatest: boolean = false;
  // The feed notice and Load more rows are always in the list, empty when unused. A notice or the control coming and
  // going would otherwise shift every row index, and the list keeps indices, not content (docs/lessons/harmony.md).
  private readingPrefix(): number {
    return (this.model.selected.kind === 'conversation' ? 1 : 0) + 2;
  }
  private saveReading(): void {
    if (!this.readingRestoring && this.readingLease?.valid()) this.readingLease.write(JSON.stringify(this.lastManualAnchor || this.anchor)).catch(() => {});
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
    this.saveReading();
    this.manuallyReading = false; this.selectedReadingLine = ''; this.lastManualAnchor = undefined;
    this.documents = new ConversationMarkdownCache();
    if (this.resumeHistory) this.releaseHistoryRendering();
    const epoch = ++this.readingEpoch;
    this.readingKey = key;
    this.inspectionRevision++; this.inspectingWork = false; this.readingPlan = ''; this.beforePlan = '';
    this.followEpoch = -1; this.userScrolling = false; this.scrollingByHand = false; this.unseenLatest = false; this.tailMarker = '';
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
    this.readingLease = undefined; this.readingCursors.clear(); this.readingFetching = false;
    this.readingAnchorReady = true; this.readingFailed = false; this.readingRestoring = false;
    if (this.model.selected.kind === 'conversation') {
      const lease = this.model.captureView('reading:' + this.model.target());
      this.readingLease = lease; this.readingCursors.clear(); this.readingFetching = false; this.readingRestoring = true; this.readingAnchorReady = false; this.readingFailed = false;
      lease.read().then((value: string) => {
        if (epoch !== this.readingEpoch || key !== this.readingKey || !lease.valid()) return;
        this.anchor = parseReadingAnchor(value); this.atEnd = this.anchor.atEnd; this.readingAnchorReady = true;
        if (this.anchor.id && !this.atEnd) this.lastManualAnchor = { id: this.anchor.id, offset: this.anchor.offset, atEnd: false };
        this.scheduleReading();
      }).catch(() => {
        if (epoch !== this.readingEpoch || key !== this.readingKey || !lease.valid()) return;
        this.readingFailed = true;
        if (this.searchHitId || this.decisionTool !== undefined) this.scheduleReading();
      });
    } else if (this.searchHitId || this.decisionTool !== undefined) this.scheduleReading();
    else this.followReading();
  }
  private scheduleReading(): void {
    const epoch = this.readingEpoch;
    this.getUIContext().postFrameCallback(new ConversationFrame(() => { if (epoch === this.readingEpoch && this.active) this.restoreReading(); }));
  }
  private async restoreReading(): Promise<void> {
    if (this.active && (this.searchHitId || this.decisionTool !== undefined) && this.searchedHit !== this.hitScope()) {
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
        // A gesture keeps its momentum: the older rows go in when it settles, above the row the reader is on (finishScroll).
        if (this.userScrolling && !this.revealOlder) this.timelinePending = true;
        else { this.syncTimeline(); boundary = this.lines.findIndex(line => known.has(line.readingId)); }
        const stalled = owner.oldestTranscriptSeq === before && this.feed.nextCursor === cursor;
        this.earlierFailed = results.some(ok => !ok) || (stalled && boundary === 0 && (owner.hasMoreMessages || !!this.feed.nextCursor));
        if (this.earlierFailed) this.earlierFailedAt = Date.now();
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
        destination = { id:this.lines[index].readingId, offset:0, atEnd:false };
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
  private retryReading(): void {
    if (!this.readingAnchorReady) { this.readingKey = ''; this.syncReading(); }
    else { this.readingCursors.clear(); this.readingFailed = false; this.scheduleReading(); }
  }
  private recordReading(start: number): void {
    if (!this.active || this.readingRestoring) return;
    // A growing or newly laid out Agent transcript can be off the bottom before the pending scroll runs.
    // Only a user gesture or an explicit navigation target detaches it from the latest output.
    if (this.model.selected.kind !== 'session') this.atEnd = this.scroller.isAtEnd();
    this.updateJumpAvailability();
    const prefix = this.readingPrefix(), index = Math.max(prefix, start), line = this.lines[index - prefix];
    if (!line) return;
    try {
      const rectangle = this.scroller.getItemRect(index);
      this.anchor = { id: line.readingId, offset: rectangle.y, atEnd: this.atEnd };
      if (this.scrollingByHand) this.lastManualAnchor = this.atEnd ? undefined : { id: line.readingId, offset: rectangle.y, atEnd: false };
    } catch (_) {}
  }
  private trackScrollMotion(offset: number, source: ScrollSource): void {
    if (!offset || source === ScrollSource.SCROLLER || source === ScrollSource.SCROLLER_ANIMATION || source === ScrollSource.EDGE_EFFECT) return;
    if (this.readingHeader && this.headerOwner && this.active && !this.readingRestoring && this.admission()) {
      const drag=source===ScrollSource.DRAG || source===ScrollSource.SCROLL_BAR || source===ScrollSource.OTHER_USER_INPUT;
      if (drag || source===ScrollSource.FLING || source===ScrollSource.SCROLL_BAR_FLING)
        this.readingHeader.scroll(this.headerOwner,offset,!drag);
    }
    this.readingMotion++; this.scrollingByHand = true;
    if (this.searchHitId && this.seekingHit) this.readingRestoring = false;
  }
  private finishReadingMotion(): void {
    if (!this.scrollingByHand) return;
    this.scrollingByHand = false;
    if (!this.active || this.readingRestoring) return;
    // The disappearing composer can resize the list during navigation. Persist the last position the reader chose.
    this.lastManualAnchor = this.scroller.isAtEnd() ? undefined : this.visibleReadingAnchor();
    this.manuallyReading = this.lastManualAnchor !== undefined;
    if (this.readingHeader && this.headerOwner && this.admission()) {
      let near=this.scroller.isAtEnd();
      const last=this.readingPrefix()+this.lines.length-1;
      if (!near && last>=0 && this.lastVisibleIndex>=last) {
        const row=this.scroller.getItemRect(last);
        near=row.height>0 && row.y+row.height+this.transcriptEndInset()-this.transcriptHeight<40;
      }
      this.readingHeader.settle(this.headerOwner,near);
    }
  }
  private selectReadingText(line: string, selected: boolean): void {
    if (selected) this.selectedReadingLine = line;
    else if (this.selectedReadingLine === line) this.selectedReadingLine = '';
    if (this.readingHeader && this.headerOwner) this.readingOverlayChanged();
  }
  private keepSelectedReadingVisible(next: number, change: number): boolean {
    const selected = this.selectedReadingLine, owner = this.model;
    if (!selected || !this.active || !this.mounted || !this.admission() || owner.selected.kind !== 'session' ||
      !owner.isForegroundChat() || this.readingRestoring || this.fieldFocused || this.draftEditorOpen || this.searchPresented ||
      this.actionsVisible || this.environment.keyboardHeightPx || this.transcriptHeight <= 0) return false;
    const index = this.lines.findIndex((line: ConversationLine): boolean => line.id === selected);
    if (index < 0) return false;
    const listIndex = this.readingPrefix()+index, before = this.scroller.getItemRect(listIndex);
    const top = Math.max(0,before.y), bottom = Math.min(this.transcriptHeight,before.y+before.height);
    if (before.height <= 0 || bottom <= top) return false;
    const focus = (top+bottom)/2, fraction = (focus-before.y)/before.height;
    const generation = owner.connectionGeneration(), scope = owner.scope(), target = owner.target(), motion = this.readingMotion;
    const valid = (): boolean => this.active && this.mounted && this.admission() && this.typeChange === change && this.readingMotion === motion &&
      this.environment.fontScale === next && this.model === owner && owner.owns(generation,scope) && owner.target() === target &&
      this.lines[index]?.id === selected && (!this.selectedReadingLine || this.selectedReadingLine === selected) &&
      !this.fieldFocused && !this.draftEditorOpen && !this.searchPresented && !this.actionsVisible && !this.environment.keyboardHeightPx;
    const context = this.getUIContext(), observer = context.getUIObserver();
    const afterLayout = (): void => {
      observer.off('didLayout',afterLayout);
      if (!valid()) return;
      const realign = (): void => {
        if (!valid()) return;
        const row = this.scroller.getItemRect(this.readingPrefix()+index);
        if (row.height <= 0) return;
        const point = row.y+fraction*row.height;
        if (point < 0 || point > this.transcriptHeight) this.scroller.scrollBy(0,point-Math.min(focus,this.transcriptHeight));
      };
      const row = this.scroller.getItemRect(this.readingPrefix()+index);
      if (row.height <= 0) {
        this.scroller.scrollToIndex(this.readingPrefix()+index,false,ScrollAlign.START,{ extraOffset:LengthMetrics.vp(-top) });
      }
      context.postFrameCallback(new ConversationFrame(realign));
    };
    observer.on('didLayout',afterLayout);
    return true;
  }
  private canReflowManualHistory(): boolean {
    return this.mounted && this.active && this.admission() && this.model.selected.kind === 'session' && this.model.isForegroundChat() &&
      this.manuallyReading && !!this.lastManualAnchor && this.jumpAvailable && !this.atEnd && !this.scrollingByHand && !this.readingRestoring && !this.readingLoading &&
      !this.searchPresented && !this.searchHitId && !this.seekingHit && !this.highlightedLine && this.decisionTool === undefined &&
      !this.selectedReadingLine && !this.readingMenuLine && !this.fieldFocused && !this.draftEditorOpen &&
      this.selectionStart === this.selectionEnd && !this.environment.keyboardHeightPx && !this.model.draftLoading && !this.model.draft &&
      !this.model.sourceContext && !this.attachments.attachments.length && !this.attachmentSheet && !this.chooser.session && !this.actionsVisible;
  }
  @Monitor('environment.fontScale') readingTextSizeChanged(change: IMonitor): void {
    const size = change.value<number>('environment.fontScale');
    if (size) this.reflowReadingSize(size.before, size.now);
  }
  private transcriptEndInset(): number {
    if (!this.lines.length) return 0;
    if (this.model.selected.kind === 'session') return 44;
    // On iPhone the newest briefing card rests 40 pt above the pinned decision dock.
    // Keep that space in the scrolling list so older messages can still reach the dock.
    const phoneDock = this.assistantDockVisible && this.availableWidth > 0 && this.availableWidth < 600;
    return this.model.selected.kind === 'conversation' && phoneDock ? 32 + Tokens.gap : 0;
  }
  private sampleVisibleReadingRows(): void {
    if (!this.active || this.model.selected.kind !== 'session' || this.transcriptHeight <= 0 || !this.lines.length) return;
    const scale = this.environment.fontScale, width = this.availableWidth;
    if (width !== this.measuredWidth) {
      this.measuredRows.clear(); this.precedingRows.clear();
      this.measuredScale = scale; this.measuredWidth = width; this.precedingScale = -1;
    } else if (scale !== this.measuredScale) {
      const previous = this.measuredRows;
      this.measuredRows = scale === this.precedingScale ? this.precedingRows : new Map<number, MeasuredReadingRow>();
      this.precedingRows = previous; this.precedingScale = this.measuredScale; this.measuredScale = scale;
    }
    const prefix = this.readingPrefix();
    const first = Math.max(0,this.firstVisibleIndex-prefix), last = Math.min(this.lines.length-1,this.lastVisibleIndex-prefix);
    const keepAhead = Math.max(32,(last-first+1)*4);
    this.measuredRows.forEach((_v: MeasuredReadingRow, index: number): void => {
      if (index < first || index > last+keepAhead) this.measuredRows.delete(index);
    });
    try {
      const count = this.lines.length, tail = this.lines[count-1], end = this.scroller.getItemRect(prefix+count-1);
      const aligned = end.height > 0 && Math.abs(end.y+end.height+this.transcriptEndInset()-this.transcriptHeight) < 0.67;
      for (let index = first; index <= last; index++) {
        const rect = this.scroller.getItemRect(prefix+index);
        if (rect.height <= 0) continue;
        const line = this.lines[index], previous = this.measuredRows.get(index);
        const retained = previous?.line === line && previous.tail === tail && previous.count === count &&
          Math.abs(previous.height-rect.height) < 0.67 ? previous.atNewest : undefined;
        this.measuredRows.set(index,{ line: line, height: rect.height, count: count, tail: tail,
          atNewest: aligned ? rect.y-this.transcriptHeight : retained });
      }
    } catch (_) { /* A row may leave the measured native viewport before the callback. */ }
  }
  private captureReadingFrame(scale: number, distance: number): void {
    if (this.measuredScale !== scale || this.measuredWidth !== this.availableWidth) return;
    const count = this.lines.length, prefix = this.readingPrefix(), rows = new Map<number,MeasuredReadingRow>();
    const first = Math.max(0,this.firstVisibleIndex-prefix), last = Math.min(count-1,this.lastVisibleIndex-prefix);
    try {
      for (let index = first; index <= last; index++) {
        const rect = this.scroller.getItemRect(prefix+index);
        if (rect.height > 0) rows.set(index,{ line:this.lines[index], height:rect.height, count:count,
          tail:this.lines[count-1], atNewest:rect.y-this.transcriptHeight-distance });
      }
    } catch (_) { return; }
    if (!rows.size) return;
    if (this.lastReadingFrame?.scale !== scale) this.priorReadingFrame = this.lastReadingFrame;
    this.lastReadingFrame = { scale:scale,width:this.availableWidth,epoch:this.readingEpoch,rows:rows };
  }
  private readingFrameForSize(scale: number): ReadingFrameOrigin | undefined {
    if (this.lastReadingFrame?.scale === scale) return this.lastReadingFrame;
    return this.priorReadingFrame?.scale === scale ? this.priorReadingFrame : undefined;
  }
  private measuredDistanceFromNewest(scale: number, reference?: ReadingFrameOrigin): number | undefined {
    if (reference ? reference.scale !== scale || reference.width !== this.availableWidth || reference.epoch !== this.readingEpoch :
      this.measuredScale !== scale || this.measuredWidth !== this.availableWidth) return undefined;
    const count = this.lines.length, prefix = this.readingPrefix();
    const first = Math.max(0,this.firstVisibleIndex-prefix), last = Math.min(count-1,this.lastVisibleIndex-prefix);
    const rows = reference ? reference.rows : this.measuredRows;
    let distance: number | undefined;
    try {
      for (let index = first; index <= last; index++) {
        const saved = rows.get(index);
        if (saved?.atNewest === undefined || saved.line !== this.lines[index] || saved.count !== count || saved.tail !== this.lines[count-1]) continue;
        const rect = this.scroller.getItemRect(prefix+index);
        if (rect.height <= 0 || Math.abs(rect.height-saved.height) >= 0.67) continue;
        const measured = Math.max(0,rect.y-this.transcriptHeight-saved.atNewest);
        if (distance !== undefined && Math.abs(measured-distance) > 0.67) return undefined;
        distance = measured;
      }
    } catch (_) { return undefined; }
    return distance;
  }
  private readingBoundary(scale: number, requested: number = -1, reference?: ReadingFrameOrigin): ReadingBoundary | undefined {
    if (this.measuredScale !== scale || this.measuredWidth !== this.availableWidth) return undefined;
    const prefix = this.readingPrefix(), last = Math.min(this.lines.length-1,this.lastVisibleIndex-prefix);
    if (last < 0) return undefined;
    try {
      if (requested === this.lines.length) {
        const physical = (reference ? this.measuredDistanceFromNewest(scale,reference) : undefined) ?? this.measuredDistanceFromNewest(scale);
        if (physical !== undefined) return { nextIndex: requested, fromBottom: physical-this.transcriptEndInset() };
      }
      if (requested >= 0 && requested < this.lines.length) {
        const destination = this.scroller.getItemRect(prefix+requested);
        if (destination.height > 0) return { nextIndex: requested, fromBottom: destination.y-this.transcriptHeight };
        if (requested <= last) return undefined;
      }
      const rect = this.scroller.getItemRect(prefix+last);
      if (rect.height <= 0) return undefined;
      return measuredReadingBoundary(last,rect.y+rect.height,this.transcriptHeight,Tokens.gap,this.lines.length,
        (index: number): number => {
          const row = this.measuredRows.get(index);
          return row && row.line === this.lines[index] ? row.height : 0;
        },requested);
    } catch (_) { return undefined; }
  }
  private distanceFromTranscriptEnd(scale: number): number {
    if (this.transcriptHeight <= 0 || !this.lines.length) return -1;
    try {
      const end = this.scroller.getItemRect(this.readingPrefix()+this.lines.length-1);
      if (end.height > 0 && Number.isFinite(end.y)) {
        return Math.max(0,end.y+end.height+this.transcriptEndInset()-this.transcriptHeight);
      }
      const physical = this.measuredDistanceFromNewest(scale);
      if (physical !== undefined) return physical;
      const prior = this.tailPosition;
      if (prior && prior.scale === scale && prior.count === this.lines.length && this.lines[prior.count-1]?.readingId === prior.lastId &&
        this.lines[prior.index]?.readingId === prior.id) {
        const row = this.scroller.getItemRect(this.readingPrefix()+prior.index);
        if (row.height > 0 && Math.abs(row.height-prior.rowHeight) < 0.5) {
          return Math.max(0,prior.distance + row.y-this.transcriptHeight-prior.fromViewportBottom);
        }
      }
      // contentSize excludes the content end inset. The offset and the native viewport are both in vp.
      const distance = this.scroller.contentSize().height + this.transcriptEndInset() -
        this.scroller.currentOffset().yOffset - this.transcriptHeight;
      return Number.isFinite(distance) ? Math.max(0,distance) : -1;
    } catch (_) { return -1; }
  }
  private rememberTailPosition(known: number = -1): void {
    this.sampleVisibleReadingRows();
    if (!this.active || this.readingRestoring || this.model.selected.kind !== 'session' || !this.lines.length ||
      this.transcriptHeight <= 0 || known < 0 && !this.scrollingByHand && !this.atEnd) return;
    try {
      const scale = this.environment.fontScale;
      const distance = known >= 0 ? known : this.distanceFromTranscriptEnd(scale);
      if (distance < 0) return;
      const prefix = this.readingPrefix(), index = Math.min(this.lines.length-1,Math.max(0,Math.floor((this.firstVisibleIndex+this.lastVisibleIndex)/2)-prefix));
      const row = this.scroller.getItemRect(prefix+index);
      if (row.height > 0) this.tailPosition = { index:index,id:this.lines[index].readingId,scale:scale,
        fromViewportBottom:row.y-this.transcriptHeight,rowHeight:row.height,distance:distance,
        count:this.lines.length,lastId:this.lines[this.lines.length-1].readingId };
    } catch (_) { /* A row can leave the measured area before the scroll callback runs. */ }
  }
  private settleReadingBoundary(index: number, expected: number, scale: number, current: () => boolean, reference?: ReadingFrameOrigin): void {
    let previous = NaN, stable = 0, frames = 0, corrections = 0;
    const inspect = (): void => {
      if (!current()) return;
      this.sampleVisibleReadingRows();
      const measured = this.readingBoundary(scale,index,reference);
      const position = measured ? measured.fromBottom : NaN;
      stable = Math.abs(position-previous) < 0.35 ? stable+1 : 0;
      previous = position; frames++;
      if (frames < 10 && stable < 2) {
        this.getUIContext().postFrameCallback(new ConversationFrame(inspect)); return;
      }
      // An estimated native scroll offset can already equal the request while real rows still lie elsewhere.
      // A correction uses only the measured row geometry for this width, type size and transcript.
      if (measured && Math.abs(position-expected) > 0.67 && corrections < 2) {
        this.scroller.scrollBy(0,position-expected); corrections++; previous = NaN; stable = 0; frames = 0;
        this.getUIContext().postFrameCallback(new ConversationFrame(inspect)); return;
      }
      this.lastManualAnchor = this.visibleReadingAnchor() || this.lastManualAnchor;
      if (index === this.lines.length) this.rememberTailPosition(Math.max(0,(measured ? position : expected)+this.transcriptEndInset()));
    };
    this.getUIContext().postFrameCallback(new ConversationFrame(inspect));
  }
  private reflowReadingSize(before: number, next: number): void {
    const change = ++this.typeChange;
    if (before === next || this.keepSelectedReadingVisible(next,change) || !this.canReflowManualHistory()) return;
    const crossesClass = readingSizeClassChanges(before, next);
    const range = crossesClass ? undefined : this.readingBoundary(before);
    const boundary = range && range.nextIndex < this.lines.length ? range : undefined;
    const oldFrame = this.readingFrameForSize(before);
    const observed = !crossesClass && !boundary && oldFrame ? this.measuredDistanceFromNewest(before,oldFrame) : undefined;
    const distance = crossesClass || boundary ? 0 : observed ??
      (range ? Math.max(0,range.fromBottom+this.transcriptEndInset()) : this.distanceFromTranscriptEnd(before));
    if (distance < 0) return;
    const reference = this.readingFrameForSize(next);
    // Preserve the measured positions before the type change. After returning to a size, native estimates for
    // off-screen row heights may still be stale even while these same visible rows are correctly placed.
    if (observed !== undefined || range?.nextIndex === this.lines.length) this.captureReadingFrame(before,distance);
    const epoch = this.readingEpoch, motion = this.readingMotion, target = this.model.target();
    const generation = this.model.connectionGeneration(), scope = this.model.scope();
    const count = this.lines.length, last = this.lines[count-1];
    const current = (): boolean => change === this.typeChange && this.readingEpoch === epoch &&
      this.readingMotion === motion && this.environment.fontScale === next && this.model.owns(generation,scope) &&
      this.model.target() === target && this.lines.length === count && this.lines[count-1] === last &&
      this.canReflowManualHistory();
    const observer = this.getUIContext().getUIObserver();
    const afterLayout = (): void => {
      observer.off('didLayout', afterLayout);
      if (!current()) return;
      if (crossesClass) { this.jumpToLatest(); return; }
      let previousExtent = -1, previousViewport = -1, stableFrames = 0, remaining = 12;
      const afterResize = (): void => {
        if (!current()) return;
        const extent = this.scroller.contentSize().height, viewport = this.transcriptHeight;
        stableFrames = Math.abs(extent-previousExtent) < 0.1 && Math.abs(viewport-previousViewport) < 0.1 ? stableFrames+1 : 0;
        previousExtent = extent; previousViewport = viewport;
        if (stableFrames < 2 && --remaining > 0) {
          this.getUIContext().postFrameCallback(new ConversationFrame(afterResize));
          return;
        }
        // The inverted iPhone transcript keeps the distance from its newest row within a size class.
        // Native end alignment recalculates the off-screen tail without materializing an entire long history.
        if (boundary) {
          this.tailPosition = undefined;
          this.scroller.scrollToIndex(this.readingPrefix()+boundary.nextIndex,false,ScrollAlign.START,
            { extraOffset:LengthMetrics.vp(-(viewport+boundary.fromBottom)) });
        } else {
          this.scroller.scrollToIndex(this.readingPrefix()+count-1,false,ScrollAlign.END,{ extraOffset:LengthMetrics.vp(-distance) });
        }
        this.settleReadingBoundary(boundary ? boundary.nextIndex : count,
          boundary ? boundary.fromBottom : distance-this.transcriptEndInset(),next,current,reference);
      };
      this.getUIContext().postFrameCallback(new ConversationFrame(afterResize));
    };
    observer.on('didLayout', afterLayout);
  }
  private approachHistory(start: number): void {
    if (start < this.firstVisibleIndex) this.pageHistory(start);
  }
  /** At the top, a pull reads the next page even when the list did not move; a failed page is read again. */
  private pullHistory(offset: number): void {
    if (offset < 0 && this.firstVisibleIndex < this.readingPrefix()) this.pageHistory(this.firstVisibleIndex);
  }
  private reachedEarliest(): void {
    if (this.userScrolling) this.pageHistory(this.firstVisibleIndex);
  }
  /** Read the next older page as the reader nears the top. Each page is read once, unless its read failed. */
  private pageHistory(start: number): void {
    const cursor = this.model.oldestTranscriptSeq;
    const retry = this.earlierFailed && Date.now() - this.earlierFailedAt >= historyRetryMs;
    if (this.model.selected.kind !== 'session' || !this.userScrolling || this.atEnd ||
      this.readingLoading || !this.model.hasMoreMessages || cursor <= 0 || (this.autoPageCursor === cursor && !retry) ||
      start > this.readingPrefix() + 5) return;
    this.autoPageCursor = cursor;
    void this.loadEarlier(false);
  }
  @Local atEnd: boolean = true;
  @Local jumpAvailable: boolean = false;
  @Local private latestPressed: boolean = false;
  @Local private sendPressed: boolean = false;
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
  private userScroll(offset: number, source: ScrollSource): void {
    if (!offset || ![ScrollSource.DRAG, ScrollSource.FLING, ScrollSource.OTHER_USER_INPUT,
      ScrollSource.SCROLL_BAR, ScrollSource.SCROLL_BAR_FLING].includes(source)) return;
    this.trackScrollMotion(offset, source);
    this.userScrolling = true;
    if (offset < 0) this.atEnd = false;
  }
  private reachedLatest(): void {
    if (!this.active || this.photoOpen || !this.scroller.isAtEnd()) return;
    this.atEnd = true; this.unseenLatest = false;
  }
  private jumpToLatest(): void {
    if (this.readingHeader && this.headerOwner) this.readingHeader.overlay(this.headerOwner,'jump',false);
    this.readingMotion++; this.inspectionRevision++; this.inspectingWork = false; this.userScrolling = false;
    this.tailPosition = undefined; this.scrollingByHand = false; this.lastManualAnchor = undefined; this.jumpAvailable = false;
    if (this.anchor) this.anchor.atEnd = true;
    this.followEpoch = -1;
    this.atEnd = true; this.unseenLatest = false;
    if (this.resumeHistory) this.releaseHistoryRendering();
    if (this.timelinePending) this.syncTimeline();
    if (this.readingPlan) this.setPlanReading(this.readingPlan, false);
    this.scroller.scrollEdge(Edge.Bottom);
    this.followReading();
  }
  private smoothJumpToLatest(): void {
    Haptics.tap();
    if (!this.environment.reduceMotion && this.lines.length > 0) {
      if (this.readingHeader && this.headerOwner && this.admission()) this.readingHeader.jumped(this.headerOwner);
      this.manuallyReading = false;
      this.tailPosition = undefined;
      this.readingMotion++; this.scrollingByHand = false; this.lastManualAnchor = undefined;
      this.atEnd = true;
      this.anchor.atEnd = true;
      this.jumpAvailable = false;
      try {
        this.scroller.scrollToIndex(this.readingPrefix() + this.lines.length - 1, true, ScrollAlign.END);
        return;
      } catch (_) {}
    }
    this.jumpToLatest();
  }
  @Monitor('questions.shown','attachmentSheet','draftEditorOpen','modeMenuOpen','searchPresented','fieldFocused','headerOwner')
  readingOverlayChanged():void {
    this.readingHeader?.overlay(this.headerOwner,'chat-editor',this.questions.shown || this.attachmentSheet || this.draftEditorOpen ||
      this.modeMenuOpen || this.searchPresented || this.fieldFocused || !!this.selectedReadingLine);
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
    this.finishReadingMotion();
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
  private controlsScroller: Scroller = new Scroller();
  @Local measuredDecision: string = '';
  @Local fieldFocused: boolean = false;
  @Local draftEditorOpen: boolean = false;
  @Local modeMenuOpen:boolean=false;
  @Local draftEditorFocused: boolean = false;
  private inlineField: TextAreaController = new TextAreaController();
  private composingReturn: boolean = false;
  private allowLineBreak: boolean = false;
  private returnKeyMode: string = '';
  private modifierRevision: number = 0;
  private returnDispatching: boolean = false;
  private inputRevision: number = 0;
  private pageField: TextAreaController = new TextAreaController();
  private draftEditorOwner: string = '';
  private selectionStart: number = 0;
  private selectionEnd: number = 0;
  private parkedStart: number = 0;
  private parkedEnd: number = 0;
  private refocusInline: boolean = false;
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
  @Local composerFrameWidth: number = 0;
  @Local composerFrameHeight: number = 0;
  @Local private queueSendNowTarget: string = '';
  @Local private starred: StarredStore = starredStore;
  @Local decisionPager: DecisionPager = new DecisionPager();
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
  @Computed get restingSize(): ComposerSize { return restingComposerSize(this.environment.fontScale); }
  private composerActionSize(): number { return this.composerAvailable() ? this.restingSize.target : Tokens.touch; }
  private inlineKeyboardAction(): boolean { return this.fieldFocused || this.environment.keyboardHeightPx > 0; }
  private leftActionWidth(size: number): number { return !this.model.demo || this.showContextMenu ? size : 0; }
  private hasComposerAction(): boolean {
    return this.primaryStop() || this.sending || !!this.model.draft.trim() || this.attachments.attachments.length > 0 || !!this.model.sourceContext;
  }
  private trailingActionWidth(size: number): number { return size + (this.hasComposerAction() ? Tokens.touch : 0); }
  private expandedComposer(): boolean {
    const candidate = this.environment.keyboardHeightPx === 0 && this.composerAvailable() ? this.restingSize.target : Tokens.touch;
    const leading = this.leftActionWidth(candidate);
    const available = this.composerWidth - leading - this.trailingActionWidth(candidate) - (leading ? 2 : 1) * Tokens.gap;
    return this.fieldFocused || this.modeMenuOpen || (!this.editorDismissed && this.model.draft.length > 0) || this.attachments.attachments.length > 0 ||
      !!this.model.sourceContext || available < Tokens.minimumInput;
  }
  private restingComposer(): boolean {
    return this.environment.keyboardHeightPx === 0 && !this.expandedComposer() && this.composerAvailable();
  }
  private readonly composerStroke: number = 0.7;
  private restingStrokePx(): number {
    return Math.max(1, Math.round(this.getUIContext().vp2px(this.composerStroke)));
  }
  private composerPadding(): Padding {
    if (!this.restingComposer() && !this.expandedComposer()) return { left: 5, right: 5, top: 3, bottom: 3 };
    // ArkUI lays out the border and padding separately. Round their combined
    // 4vp / 2vp insets once so the stroke cannot cost either edge a device pixel.
    const ui = this.getUIContext(), stroke = this.restingStrokePx();
    const side = String(Math.max(0, Math.round(ui.vp2px(Tokens.fine)) - stroke)) + 'px';
    const end = String(Math.max(0, Math.round(ui.vp2px(this.expandedComposer() ? Tokens.gap : 2)) - stroke)) + 'px';
    return { left: side, right: side, top: this.expandedComposer() ? 0 : end, bottom: end };
  }
  @Computed get compactDraftSpace(): CompactDraftRoom | undefined {
    const env = this.environment;
    if (!env.isFullScreen || !env.windowWidthPx || !env.windowHeightPx || !env.safeBottomPx) return undefined;
    const ui = this.getUIContext(), height = ui.px2vp(env.windowHeightPx);
    return compactDraftRoom(ui.px2vp(env.windowWidthPx),height,this.availableWidth,ui.px2vp(env.safeTopPx),
      Math.max(0,height-ui.px2vp(env.safeBottomPx)),ui.px2vp(env.keyboardHeightPx),env.isFullScreen,env.nativeHoverAvailable);
  }
  private stackedAgentDraft(): boolean {
    return this.active && !!this.model.draft && !!this.model.decision && this.model.selected.kind === 'session' &&
      this.model.pendingDecisions.length > 1 && !this.attachments.attachments.length && !this.model.sourceContext;
  }
  private preferredCardSpace(room: CompactDraftRoom): number {
    return this.environment.keyboardHeightPx > 0 ? 44 : room.page / 2;
  }
  private sharedDraftLimit(room: CompactDraftRoom): number {
    // One unit of list remains. The count has an 8vp gap below it. From the
    // card viewport to the field are 12vp of group spacing and 16vp of text inset.
    const fixed = 1 + this.agentCountHeight + Tokens.gap + Tokens.inset + Tokens.page + this.composerActionSize() + Tokens.page;
    return Math.max(this.readingSize.height(1), room.visible - fixed - this.preferredCardSpace(room));
  }
  @Computed get floatsDraftHeader(): boolean {
    if (!this.stackedAgentDraft() || !this.compactDraftSpace) return false;
    return this.readingSize.height(Math.min(6,this.visibleDraftLines)) > this.sharedDraftLimit(this.compactDraftSpace);
  }
  private announcedFloatingHeader: boolean = false;
  @Monitor('floatsDraftHeader') publishDraftHeader(): void {
    const next = this.floatsDraftHeader;
    if (next !== this.announcedFloatingHeader) { this.announcedFloatingHeader = next; this.onLongDraftHeader(next); }
  }
  private controlsPadding(): Padding {
    const resting = this.restingComposer(), side = resting || this.expandedComposer() ? Tokens.inset : Tokens.page;
    const space = this.expandedComposer() || this.assistantDockVisible && resting ? this.compactDraftSpace : undefined;
    const stack = space && this.stackedAgentDraft();
    // The native Agent tray has two units of bottom padding when its keyboard is closed.
    const top = stack ? Math.max(0, Tokens.fine - (this.environment.keyboardHeightPx > 0 ? 0 : 2) -
      this.getUIContext().px2vp(this.restingStrokePx())) : Tokens.gap;
    return { left: side, right: side, top: top, bottom: (resting || this.expandedComposer() ? Tokens.gap : Tokens.footer) + (space?.bottomLift ?? 0) };
  }
  private verticalActions(): boolean {
    // The tools are icon buttons with vp touch targets. Only wrap when their actual slots cannot fit.
    if (this.composerWidth < Tokens.composerNarrowActions) return true;
    const middle = this.composerColumns() - this.leftColumns() - this.sendColumns();
    return this.expandedComposer() && middle * this.composerWidth / this.composerColumns() < Tokens.page;
  }
  // The native grid keeps one editor mounted in both layouts. Pixel columns place each
  // control at its measured target even when it is wider than one twelfth of a phone.
  private composerColumns(): number { return Math.max(1, Math.round(this.getUIContext().vp2px(this.composerWidth))); }
  private sendColumns(): number {
    const size = this.composerActionSize();
    const width = this.expandedComposer() ? Tokens.fine + size + (this.hasComposerAction() ? Tokens.fine + size : 0) :
      this.trailingActionWidth(size) + Tokens.gap;
    return Math.ceil(width * this.composerColumns() / this.composerWidth);
  }
  private leftColumns(): number {
    const size = this.composerActionSize(), leading = this.leftActionWidth(size);
    const width = this.expandedComposer() ? Tokens.fine + leading + (this.inlineKeyboardAction() ? size + (leading ? Tokens.fine : 0) : 0) :
      leading + (leading ? Tokens.gap : 0);
    return Math.ceil(width * this.composerColumns() / this.composerWidth);
  }
  private primaryStop(): boolean { return this.composerAvailable() && this.model.canStop() && !this.model.draft.trim() && !this.model.hasSendableContext() && !this.attachments.attachments.length; }
  @Monitor('active', 'model.selected', 'model.sessions', 'model.connected', 'model.draft', 'model.decision', 'attachments.attachments', 'model.interruptPendingKey')
  primaryStopChanged(): void { this.onPrimaryStop(this.active && this.primaryStop()); }
  private maxControlsHeight(): number {
    // The shell already resizes this view to the top of the IME.
    if (this.environment.keyboardHeightPx > 0) return this.availableHeight;
    if (this.assistantDockVisible && this.model.selected.kind==='conversation') return this.availableHeight;
    if (!this.model.decision) return this.availableHeight*0.8;
    if (this.model.selected.kind === 'session') return this.availableHeight;
    const half = this.availableHeight * Tokens.decisionFraction;
    return this.model.decision.kind === 'question' ? Math.max(half, this.availableHeight - 72) : half;
  }
  @Local agentComposerHeight: number = 0;
  @Local private assistantFooterHeight: number = 0;
  @Local private assistantDockVisible: boolean = false;
  private mainDockSpace():number {
    const row=this.model.selected, env=this.environment, ui=this.getUIContext();
    if (!this.model.mainId || row.kind!=='conversation' || row.id!==this.model.mainId ||
      this.model.mainMachineId && row.machine!==this.model.mainMachineId ||
      row.group && row.group!==this.model.profile.id || row.manager && row.manager!==this.model.profile.manager ||
      !env.isFullScreen || env.nativeHoverAvailable || env.keyboardHeightPx>0) return -1;
    const width=ui.px2vp(env.windowWidthPx), height=ui.px2vp(env.windowHeightPx);
    if (!(width>0 && width<600 && height>=600 && height>width && this.availableHeight>0 && this.contentTop>0)) return -1;
    const paneTop=(env.safeBottomPx>0 ? ui.px2vp(env.safeBottomPx) : height)-this.availableHeight;
    const safeHeaderTop=Math.max(Tokens.compactStatusInset,ui.px2vp(env.safeTopPx)+Tokens.gap);
    // AssistantTopBar reserves 70 points below the safe area. Ark navigation
    // already took most of this space before offering the conversation its pane.
    const header = Math.max(0,70-Math.max(0,paneTop-safeHeaderTop));
    if (this.model.decision) {
      const fallbackAction = this.showContextMenu && !this.composerAvailable() ? Tokens.touch + Tokens.gap : 0;
      return this.availableHeight - header - Tokens.footer - fallbackAction;
    }
    if (this.assistantFooterHeight <= 0) return -1;
    return this.availableHeight - header - Number(this.controlsPadding().bottom) - this.assistantFooterHeight - Tokens.gap;
  }
  @Local agentCountHeight: number = 0;
  private agentCardsRoom(): number {
    if (this.floatsDraftHeader && this.compactDraftSpace) return this.preferredCardSpace(this.compactDraftSpace);
    const ui=this.getUIContext(), keyboard=this.environment.keyboardHeightPx>0;
    let visibleBody=this.availableHeight, page=this.availableHeight;
    const windowName=ui.getWindowName();
    if (windowName) {
      try {
        const owner=window.findWindow(windowName), rect=owner.getWindowProperties().windowRect;
        const height=ui.px2vp(rect.height), width=ui.px2vp(rect.width), origin=ui.px2vp(rect.top);
        const topInset=Math.max(0,ui.px2vp(this.environment.safeTopPx));
        const bottomInset=this.environment.safeBottomPx>0 ? Math.max(0,ui.px2vp(rect.top+rect.height-this.environment.safeBottomPx)) : 0;
        const bottom=origin+height-Math.max(bottomInset,ui.px2vp(this.environment.keyboardHeightPx));
        // The Ark navigation header is outside Conversation. Its visible body already starts below that header.
        visibleBody=Math.max(0,Math.min(this.availableHeight,bottom-this.contentTop));
        const compact=owner.getWindowStatus()===window.WindowStatusType.FULL_SCREEN && !this.environment.nativeHoverAvailable &&
          width<600 && height>width && Math.abs(width-this.availableWidth)<2;
        if (compact) {
          // The compact phone chat's half-page budget includes its header and excludes its safe areas.
          // Larger native cutouts and navigation indicators still take precedence.
          page=height-Math.max(Tokens.compactStatusInset,topInset)-Math.max(34,bottomInset);
        } else page=visibleBody;
      } catch (_) { /* Before the window attaches, use the measured conversation pane. */ }
    }
    const composer=Math.max(this.agentComposerHeight,this.restingSize.target+Tokens.footer);
    const cards=this.model.pendingDecisions, loneQuestion=cards.length<=1 && this.model.decision?.kind==='question';
    const countRoom=cards.length>1 ? this.agentCountHeight+Tokens.gap : 0;
    const remaining=visibleBody-composer-Tokens.gap-countRoom;
    const floor=Tokens.gap+1, half=page/2;
    const cap=keyboard ? remaining-floor : loneQuestion ? Math.max(half,remaining-72-floor) : half;
    return Math.max(0,Math.min(remaining,Math.max(44,cap)));
  }
  private inlineEditorMaxHeight(): number {
    if (!this.expandedComposer()) return this.composerActionSize();
    if (this.floatsDraftHeader && this.compactDraftSpace) return this.sharedDraftLimit(this.compactDraftSpace);
    const line = this.readingSize.advance;
    const actions = this.composerActionSize() * (this.verticalActions() ? 2 : 1);
    // Include the outer spacing and both borders before the first native measurement.
    // After layout this also reserves the measured quote, attachment strip and Persona task dock.
    // Session requests have a separate scroll viewport that yields room to the composer.
    // Counting the cards in its own overhead makes an oversized editor shrink and grow on successive layouts.
    const height = this.model.decision && this.model.selected.kind === 'session' ? this.agentComposerHeight : this.controlsHeight;
    const chrome = Math.max(actions + Tokens.gap + Tokens.footer + 8, this.editorHeight > 0 ? height - this.editorHeight : 0);
    const room = Math.max(this.readingSize.height(1), this.maxControlsHeight() - chrome - Tokens.page);
    const lines = Math.max(1, Math.min(6, Math.floor(room / line)));
    return this.readingSize.height(lines);
  }
  private revealOverflowingActions(): void {
    // A long attachment error or a short split window can be taller than even a one-line editor.
    if (this.active && this.fieldFocused && !this.draftEditorOpen && this.environment.keyboardHeightPx > 0 &&
      this.controlsHeight > this.maxControlsHeight() + 1) this.controlsScroller.scrollEdge(Edge.Bottom);
  }
  private measureAttachment(): void {
    if (!this.attachmentLayoutReady) return;
    const inset = nativeSheetInset(this.getUIContext(), this.attachmentNodeId, this.environment.safeBottomPx);
    if (Math.abs(inset - this.attachmentInset) > 0.1) this.attachmentInset = inset;
  }
  @Monitor('environment.safeBottomPx') safeBottomChanged(): void { this.measureAttachment(); this.measureQuestion(); this.measurePageSheet(); }
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
    if (session.kind==='attachments' && this.showContextMenu) this.chooserItems=this.chooserItems.concat([new ComposerChoice(session,'actions',tr('more'),Symbols.apps)]);

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
  private composerAvailable(): boolean {
    return this.active && (!this.model.decision || this.model.selected.kind === 'session' && !this.model.decisionBlocksMessage()) && this.model.canCompose();
  }
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
    if (choice.key === 'actions') this.requestComposerActions(session.preserveInput);
    else if (choice.key === 'photos') this.attachments.choosePhotos();
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
            Text(choice.title).fontSize(Tokens.body).maxFontScale(Tokens.interfaceScaleFor(Tokens.body)).fontColor(Colors.ink).layoutWeight(1)
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
    this.syncRevision(); this.refreshFeed();
    if (this.userScrolling && !this.atEnd && this.active && this.model.selected.kind === 'session' &&
      this.lastVisibleIndex < this.readingPrefix() + this.lines.length - 1) {
      this.timelinePending = true;
      return;
    }
    this.syncTimeline(); this.timelinePositionChanged();
  }
  @Monitor('feed.items', 'feed.hasLoaded', 'feed.feedUnavailable') feedChanged(): void { this.syncTimeline(); this.timelinePositionChanged(); }
  @Monitor('active', 'model.selected', 'model.connected') feedContextChanged(): void {
    if (this.active) this.syncReading();
    else {
      this.readingMotion++; this.readingEpoch++; this.readingKey = '';
      if (this.resumeHistory) this.releaseHistoryRendering();
    }
    this.syncFeed(); this.syncAttachments(); this.syncRevision();
  }
  @Monitor('model.decision') decisionChanged(): void { this.measuredDecision = ''; this.refreshFeed(); this.scheduleReading(); }
  @Monitor('active', 'model.selected', 'model.profile', 'model.connected', 'model.personaQuestions')
  questionsContextChanged(): void { if (this.liftOpen && !this.currentLift()) this.closeOwnedLift(); this.questions.sync(this.model, this.active); }
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
    if (!this.active || !this.model.connected || this.model.selected.kind !== 'conversation') {
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
  @Computed get readingSize(): ComposerReadingSize { return composerReadingSize(this.environment.fontScale); }
  @Computed get draftTracking(): number {
    const ui = this.getUIContext(), size = this.readingSize;
    const nativeWidth = ui.px2vp(ui.getMeasureUtils().measureText({ textContent: composerTypeSample, fontSize: String(size.points) + 'vp' }));
    return Math.min(0, (size.sampleWidth - nativeWidth) / composerTypeSample.length);
  }
  private nativeDraftLines(width: number): number {
    const ui = this.getUIContext(), text = this.model.draft;
    if (!text) return 1;
    let offset = -1;
    for (let breaks = 0; breaks < 6; breaks++) {
      offset = text.indexOf('\n', offset + 1);
      if (offset < 0) break;
      if (breaks === 5) return 7;
    }
    const value = text.endsWith('\n') ? text + ' ' : text;
    const metrics = ui.getMeasureUtils().measureTextSize({ textContent: value, constraintWidth: String(Math.max(1, width)) + 'vp',
      fontSize: String(this.readingSize.points) + 'vp', lineHeight: String(this.readingSize.advance) + 'vp',
      letterSpacing: String(this.draftTracking) + 'vp', maxLines: 7, wordBreak: WordBreak.BREAK_WORD });
    return Math.max(1, Math.min(7, Math.round(ui.px2vp(Number(metrics.height)) / this.readingSize.advance)));
  }
  @Computed get unreservedDraftLines(): number { return this.nativeDraftLines(this.composerWidth - 2 * Tokens.inset); }
  private offersDraftEditor(): boolean {
    if (this.fieldFocused) return true;
    const text = this.model.draft;
    return !!text && (text.includes('\n') || this.unreservedDraftLines >= 6);
  }
  @Computed get visibleDraftLines(): number {
    if (!this.offersDraftEditor()) return this.unreservedDraftLines;
    return this.nativeDraftLines(this.composerWidth - 2 * Tokens.inset - this.restingSize.target);
  }
  private expandedFieldHeight(): number {
    return Math.min(this.inlineEditorMaxHeight(), this.readingSize.height(this.visibleDraftLines));
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
      !this.attachments.busy && !this.model.draftLoading && (this.model.draft.trim().length > 0 || this.attachments.attachments.length > 0 || this.model.hasSendableContext()) &&
      this.model.operationState !== 'pending' && this.model.operationState !== 'unknown');
  }
  private dictationOwner(): string { return JSON.stringify([this.model.connectionGeneration(), this.model.scope(), this.model.target()]); }
  private sendCurrent(): boolean { return this.mounted && this.active && this.admission(); }
  private async sendDraft(): Promise<void> {
    if (!this.sendCurrent() || this.sending || this.attachments.busy || !this.model.canSend()) return;
    const owner = this.model, generation = owner.connectionGeneration(), scope = owner.scope(), target = owner.target(), draft = owner.draft, context = owner.sourceContext;
    const current = (): boolean => this.sendCurrent() && this.model === owner && owner.owns(generation, scope) && owner.target() === target;
    if (!this.attachments.attachments.length) {
      const sent = await owner.send(current);
      if (sent && owner.demo && current() && typeof owner.refresh === 'function') void owner.refresh();
      return;
    }
    this.sending = true;
    try {
      const hasImage=this.attachments.attachments.some((item:ChatAttachment)=>item.kind==='image');
      const attachmentNote=hasImage ? 'chat_context_image_note' : 'chat_context_files_note';
      const prepared = await this.attachments.prepareSend(owner.prepareMessageText(draft,attachmentNote), current);
      const sent = await owner.sendPrepared(prepared, draft, context, current);
      if (sent && current() && prepared.overflowNoticeKey) {
        this.getUIContext().getPromptAction().showToast({ message: tr(prepared.overflowNoticeKey) });
      }
    } catch (error) { if (current()) this.attachments.errorKey = error instanceof Error && error.message.startsWith('chat_attachment_') ? error.message : 'chat_attachment_send_failed'; }
    finally { this.sending = false; }
  }
  private async preview(item: ChatAttachment): Promise<void> {
    const host = this.getUIContext().getHostContext(), key = this.attachmentKey; if (!host) return;
    try { await previewAttachment(host as common.UIAbilityContext, item.localPath, item.name, item.mime); }
    catch (_) { if (key === this.attachmentKey) this.attachments.errorKey = 'preview_unavailable'; }
  }
  private syncRevision(): void {
    this.starred.configure(this.model);
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
  private async copyMessage(message: ChatMessage, text: string = message.text): Promise<void> {
    if (message.ownerTarget !== this.model.target()) return;
    const generation = this.model.connectionGeneration(), scope = this.model.scope(), target = this.model.target();
    try { await pasteboard.getSystemPasteboard().setData(pasteboard.createData(pasteboard.MIMETYPE_TEXT_PLAIN, text)); }
    catch (_) { if (this.model.owns(generation, scope) && this.model.target() === target) this.model.error = 'failed'; }
  }
  private messageMenuBuilder(entry: RepeatItem<ConversationLine>): CustomBuilder {
    return (): void => { const message = entry.item.message; if (message) this.messageMenu(message); };
  }
  @Monitor('model', 'model.connected', 'model.features', 'model.starredRevision') syncStarredScope(): void {
    this.starred.configure(this.model);
  }
  private starSourceFor(message: ChatMessage): StarredSource | undefined {
    const row = this.model.selected;
    const machine = row.machine;
    const session = row.session || row.id;
    const itemId = message.itemId || message.id;
    if (!machine || !session || !itemId || message.ownerTarget !== this.model.target()) return undefined;
    if (message.role !== 'user' && message.role !== 'assistant') return undefined;
    if (message.held || message.failed || (message.localOperation && message.deliveryState !== 'done')) return undefined;
    const conversation = row.kind === 'conversation' ? 'assistant' : 'agent';
    const source: StarredSource = {
      machine_id: machine,
      session_id: session,
      item_id: itemId,
      conversation: conversation,
      source_fingerprint: message.sourceFingerprint || undefined
    };
    return isVerifiedStarSource(source) ? source : undefined;
  }
  private canStarMessage(message: ChatMessage): boolean {
    if (!this.active || !this.model.connected || !this.model.isChat()) return false;
    if (!this.starred.canWriteFor(this.model)) return false;
    if (!message.text.trim()) return false;
    return this.starSourceFor(message) !== undefined;
  }
  private isMessageStarred(message: ChatMessage): boolean {
    const _rev = this.starred.indexRevision;
    const source = this.starSourceFor(message);
    if (!source) return false;
    return this.starred.isStarred(this.model, source);
  }
  private isStarPending(message: ChatMessage): boolean {
    const _rev = this.starred.pendingRevision;
    const source = this.starSourceFor(message);
    if (!source) return false;
    return this.starred.isPending(this.model, source);
  }
  private async toggleMessageStar(message: ChatMessage): Promise<void> {
    if (!this.canStarMessage(message)) return;
    const source = this.starSourceFor(message);
    if (!source) return;
    if (this.starred.isStarred(this.model, source)) {
      await this.starred.remove(this.model, source);
      return;
    }
    const speaker = message.role === 'user'
      ? tr('chat_lift_me')
      : (this.composerAgentName() || tr(this.model.selected.kind === 'conversation' ? 'persona' : 'assistant'));
    const writtenAt = message.at > 0 ? new Date(message.at).toISOString() : undefined;
    const draft: StarredSnapshot = {
      id: '',
      machine_id: source.machine_id,
      session_id: source.session_id,
      item_id: source.item_id,
      conversation: source.conversation,
      source_fingerprint: source.source_fingerprint,
      role: message.role,
      title: this.model.selected.title || tr('conversation'),
      speaker: speaker,
      source_manager: this.model.profile.manager || undefined,
      written_at: writtenAt,
      text: message.text
    };
    await this.starred.add(this.model, draft);
  }
  @Builder messageMenu(message: ChatMessage) {
    Menu() {
      if (message.text) MenuItem({ content: tr('copy') }).onClick(() => this.copyMessage(message))
      if (message.text) MenuItem({ content: tr('use_as_context') }).onClick(() => this.model.useMessageContext(message))
      if (this.canStarMessage(message)) {
        MenuItem({
          content: tr(this.isMessageStarred(message) ? 'unstar_message' : 'star_message'),
          symbolStartIcon: new SymbolGlyphModifier(this.isMessageStarred(message) ? $r('sys.symbol.star_fill') : $r('sys.symbol.star'))
        })
          .enabled(!this.isStarPending(message))
          .onClick(() => { void this.toggleMessageStar(message); })
          .id('chat.message.star')
      }
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
    const activities: TimelineCard[] = this.feed.items.filter((item: Record<string, Object>): boolean => {
      const kind = Wire.text(item, 'kind');
      return !['task', 'today'].includes(kind) && !this.model.feedInBackground(kind, Date.parse(Wire.text(item, 'at')));
    })
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
    const place = this.readingPlace(), placed = place ? this.lines.findIndex(line => line.readingId === place.id) : -1;
    reconcileTimelineRows(this.lines, display, ConversationLine.same);
    this.keepReadingPlace(place, placed);
    this.pinLatest();
    this.followReading();
  }
  /** The row the reader is on, unless the view follows the latest. Work disclosure keeps its own anchor. */
  private readingPlace(): ReadingAnchor | undefined {
    return this.active && !this.atEnd && !this.inspectingWork ? this.visibleReadingAnchor() : undefined;
  }
  /** Rows added or removed above the reader leave that row where it was, in the same layout pass. */
  private keepReadingPlace(place: ReadingAnchor | undefined, before: number): void {
    if (!place) return;
    const index = this.lines.findIndex(line => line.readingId === place.id);
    if (index < 0 || index === before) return;
    try {
      this.scroller.scrollToIndex(this.readingPrefix() + index, false, ScrollAlign.START, { extraOffset: LengthMetrics.vp(-place.offset) });
    } catch (_) { /* A list that is not laid out yet has no place to keep. */ }
  }
  /** While the view follows the latest, the next layout starts at the end; the frame callback then settles measured rows. */
  private pinLatest(): void {
    if (!this.active || !this.atEnd || this.userScrolling || this.inspectingWork || this.photoOpen || !this.lines.length) return;
    try { this.scroller.scrollEdge(Edge.Bottom); } catch (_) { /* A list that is not laid out yet starts at its end. */ }
  }
  private historyLabel(): string {
    return !this.model.connected ? 'offline' : this.readingLoading ? 'loading' : this.earlierFailed ? 'chat_history_failed' : 'load_more';
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
    this.onLongDraftHeader(this.floatsDraftHeader);
    this.mounted = true;
    this.questions.sync(this.model, this.active); this.chooser.sync(this.model, this.active);
    this.syncReading(); this.syncFeed(); this.syncAttachments(); this.syncTimeline(); this.syncRevision(); this.primaryStopChanged();
    this.syncWork();
  }
  aboutToDisappear(): void {
    this.mounted = false; if (this.resumeHistory) this.releaseHistoryRendering();
    this.inspectionRevision++; this.inspectingWork = false;
    this.workView = undefined; this.workTarget = '';
    this.closeOwnedLift();
    this.seekingHit = ''; this.clearSearchHighlight();
    this.mounted = false;
    this.questions.retire(); this.actionIntent = undefined; this.chooser.retire();
    this.onLongDraftHeader(false);
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
  private messageBottomPad(entry: RepeatItem<ConversationLine>): number {
    const message = entry.item.message!;
    if (message.work) return 1;
    const next = this.lines[entry.index + 1]?.message;
    if (this.model.selected.kind === 'session' && message.role === 'assistant' && message.text &&
      next?.role === 'recap' && next.terminalRecap) return Tokens.gap;
    // The chat's native stack leaves 16pt between consecutive prompt and reply surfaces.
    // The List already contributes its own gap and the next row has a 3vp top inset.
    if (this.model.selected.kind === 'session' && next &&
      ((message.role === 'user' && next.role === 'assistant') || (message.role === 'assistant' && next.role === 'user'))) {
      return Tokens.page - Tokens.gap - 3;
    }
    return Tokens.fine;
  }
  private canLiftMessage(message: ChatMessage): boolean {
    return this.availableWidth>0 && this.availableWidth<600 && !this.useNativeHover && this.model.selected.kind==='session' &&
      (message.role==='user' || message.role==='assistant') && !!message.text.trim() && !message.held && !message.failed &&
      (!message.localOperation || message.deliveryState==='done');
  }
  private showLift(entry: RepeatItem<ConversationLine>, point?: number, select: boolean = false, touchArea?: Area): void {
    const message=entry.item.message, ui=this.getUIContext();
    if (this.liftOpen || !message || !this.active || !this.admission() || !this.canLiftMessage(message) ||
      message.ownerTarget!==this.model.target() || !this.model.isForegroundChat()) return;
    const surface=ui.getFrameNodeById('chat.lift.source.'+entry.item.id);
    const pos=surface?.getPositionToScreenWithTransform(), size=surface?.getMeasuredSize();
    const width=size ? ui.px2vp(size.width) : Number(touchArea?.width)-Tokens.page*2;
    const height=size ? ui.px2vp(size.height) : Number(touchArea?.height)-this.messageBottomPad(entry)-3;
    const x=pos ? pos.x : Number(touchArea?.globalPosition.x)+Tokens.page;
    const y=pos ? pos.y : Number(touchArea?.globalPosition.y)+3;
    if (!(width>0 && height>0 && Number.isFinite(x) && Number.isFinite(y))) return;
    const left=Math.max(4,x-8),top=y-12;
    this.liftFrame={left:left,top:top,width:Math.min(width+16,this.availableWidth-left-4),height:height+24,
      press:Math.max(0,(point ?? y+height/2)-top),fontScale:this.environment.fontScale};
    this.liftMessage=message; this.liftSelectOnOpen=select; this.liftGeneration=this.model.connectionGeneration();
    this.liftScope=this.model.scope();
    const presentation: PhoneLiftPresentation={content:():void => this.liftedContent(),didDismiss:():void => {
      this.liftOpen=false; this.liftMessage=undefined; this.liftFrame=undefined; this.ownedLift=undefined;
    }};
    this.liftHost.present(presentation); this.ownedLift=presentation; this.liftOpen=true;
    Haptics.tap();
  }
  private closeOwnedLift(): void {
    if (this.ownedLift && this.liftHost.current===this.ownedLift) this.liftHost.dismiss();
  }
  private currentLift(): ChatMessage | undefined {
    const message=this.liftMessage;
    return this.liftOpen && this.active && this.admission() && message && this.model.owns(this.liftGeneration,this.liftScope) &&
      this.model.target()===message.ownerTarget && this.model.messages.some((row:ChatMessage) => row.id===message.id && row.text===message.text) ?
      message : undefined;
  }
  private quoteLiftedText(text?: string): void {
    const message=this.currentLift(), model=this.model, generation=this.liftGeneration, scope=this.liftScope;
    if (!message) return;
    model.quoteMessageContext(message,message.role==='user' ? tr('chat_lift_me') : this.composerAgentName(),text).then((stored:boolean): void => {
      if (!stored || !this.active || this.model!==model || !model.owns(generation,scope) || model.target()!==message.ownerTarget || !model.sourceContext) return;
      this.getUIContext().postFrameCallback(new ConversationFrame((): void => {
        if (this.active && this.admission() && model.owns(generation,scope) && model.target()===message.ownerTarget)
          this.getUIContext().getFocusController().requestFocus('chat.input');
      }));
    });
  }
  private forwardLiftedMessage(multiple: boolean): void {
    const message=this.currentLift();
    if (!message || !this.forwardState.isEligible(this.model,message)) return;
    this.closeOwnedLift();
    if (multiple) this.forwardState.startPicking(this.model,message);
    else this.forwardState.startForward(this.model,message);
  }
  @Builder liftMore() {
    if (this.liftMessage) this.messageMenu(this.liftMessage!)
  }
  @Builder liftedContent() {
    if (this.liftMessage && this.liftFrame) {
      PhoneMessageLift({text:this.liftMessage!.text,own:this.liftMessage!.role==='user',frame:this.liftFrame!,
        canForward:this.model.connected && this.forwardState.isEligible(this.model,this.liftMessage!),
        onForward:():void => this.forwardLiftedMessage(false),onSelectMessages:():void => this.forwardLiftedMessage(true),
        selectOnOpen:this.liftSelectOnOpen,hasMore:this.canStarMessage(this.liftMessage!) || (!!this.liftMessage!.itemId &&
          (this.revisions.canFork(this.liftMessage!.itemId) || this.revisions.canRewind(this.liftMessage!.itemId))),
        moreMenu:():void => this.liftMore(),onClose:():void => { this.closeOwnedLift(); },
        onCopyText:(text:string):void => { const message=this.currentLift(); if (message) void this.copyMessage(message,text); },
        onQuoteText:(text?:string):void => this.quoteLiftedText(text)})
    }
  }
  @Builder pickMark(picked:boolean,brought:boolean) {
    Stack() {
      Circle().width(22).height(22).fill(picked || brought ? Colors.interfaceAccent : Color.Transparent)
        .stroke(picked || brought ? Colors.interfaceAccent : Colors.secondary).strokeWidth(1)
      if (picked || brought) SymbolGlyph($r('sys.symbol.checkmark')).fontSize(13).fontColor([Color.White])
    }.width(24).height(32).margin({left:Tokens.page,top:3}).opacity(brought ? .33 : 1)
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
    if (this.forwardState.isPicking(this.model) && this.forwardState.isEligible(this.model,entry.item.message!)) {
      Row({space:8}) {
        this.pickMark(this.forwardState.isPicked(this.model,entry.item.message!),this.forwardState.isBrought(this.model,entry.item.message!))
        Column() { this.messageBody(entry) }.layoutWeight(1).hitTestBehavior(HitTestMode.None).accessibilityLevel('no-hide-descendants')
      }.width('100%').alignItems(VerticalAlign.Top).accessibilityGroup(true).accessibilityRole(AccessibilityRoleType.BUTTON)
        .accessibilityText(entry.item.message!.text).accessibilitySelected(this.forwardState.isPicked(this.model,entry.item.message!))
        .id('chat.pick.'+(entry.item.message!.itemId || entry.item.message!.id))
        .onClick(() => this.forwardState.toggle(this.model,entry.item.message!))
    } else if (this.forwardState.isPicking(this.model)) {
      Column() { this.messageBody(entry) }.width('100%').hitTestBehavior(HitTestMode.None)
    } else if (this.canLiftMessage(entry.item.message!)) {
      Column() { this.messageBody(entry) }.width('100%')
        .priorityGesture(LongPressGesture({repeat:false}).onActionEnd((event:GestureEvent) => {
          this.showLift(entry,event.fingerList[0]?.globalY,false,event.target.area);
        }))
        .accessibilityCustomActions([{name:tr('chat_lift_select'),onAction:():void => this.showLift(entry,undefined,true)}])
    } else {
      Column() { this.messageBody(entry) }.width('100%')
        .bindContextMenu(this.messageMenuBuilder(entry), ResponseType.LongPress, {preview:MenuPreviewMode.IMAGE,
          onAppear:() => { this.readingMenuLine=entry.item.id; },
          onDisappear:() => { if (this.readingMenuLine===entry.item.id) this.readingMenuLine=''; }})
    }
  }
  private isStreamingMessage(message: ChatMessage): boolean {
    if (!this.active || message.role !== 'assistant' || !this.lines.length) return false;
    const working = this.model.assistantReplyWorking() || this.model.canStop();
    if (!working) return false;
    for (let i = this.lines.length - 1; i >= 0; i--) {
      const candidate = this.lines[i].message;
      if (candidate && candidate.role === 'assistant') return candidate.id === message.id;
    }
    return false;
  }




  private userBubbleFold(message: ChatMessage, maxBubbleWidth: number): BubbleFoldDecision {
    if (message.role !== 'user' || !message.text) {
      return { foldable: false, estimatedLines: 1, foldedText: message.text };
    }
    return evaluateBubbleFold(message.text, maxBubbleWidth, this.environment.fontScale);
  }

  private toggleBubbleFold(id: string): void {
    Haptics.tap();
    Motion.run(this.getUIContext(), Motion.layout(this.environment.reduceMotion), () => {
      this.unfoldedBubbles = this.unfoldedBubbles.includes(id)
        ? this.unfoldedBubbles.filter((entry: string) => entry !== id)
        : this.unfoldedBubbles.concat([id]);
    });
  }

  @Builder transcriptList() {
        List({ scroller: this.scroller }) {
          if (this.model.selected.kind === 'session' && this.model.chatHistoryLoading && !this.model.transcriptHistoryReady &&
            !this.lines.length && !this.model.error) ListItem() {
            Text(tr('loading')).fontSize(Tokens.meta).fontColor(Colors.secondary).padding(Tokens.page).id('chat.history.loading')
          }
          ListItem() {
            if (this.feed.error && this.feed.error !== this.model.error) Row({ space: Tokens.gap }) { Text(tr(this.feed.error)).fontSize(Tokens.meta).layoutWeight(1); Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('retry')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('retry', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('retry')).bindTips(tr('retry')).onClick(() => this.feed.refresh()) }.padding(Tokens.inset)
          }
          if (this.model.selected.kind === 'conversation') ListItem() {
            AssistantCards({ model: this.model, feed: this.feed, active: this.active && this.model.connected, onOpenResource: (row: ResourceRow) => this.onOpenResource(row), dockOnly: true, onOpenCard: (itemId: string, kind: string) => this.onOpenCard(itemId, kind), onOpenSession: (row: ResourceRow) => this.onOpenResource(row),
              onOpenTodo: (row: ResourceRow) => this.onOpenResource(row), onOpenInbox: (row: ResourceRow) => this.onOpenResource(row), onLink: (address: string) => this.onLink(address) })
          }
          ListItem() {
            if (this.model.hasMoreMessages || this.feed.nextCursor) Row() {
              Button({ type:ButtonType.Normal, buttonStyle:ButtonStyleMode.TEXTUAL }) {
                Row({space:4}) {
                  if (this.readingLoading && this.model.connected) LoadingProgress().width(Tokens.symbol).height(Tokens.symbol).color(Colors.activity)
                  else SymbolGlyph(this.earlierFailed ? IconActions.symbol('retry') : Symbols.up).fontSize(12).fontColor([Colors.activity])
                  Text(tr(this.historyLabel())).fontSize(Tokens.toolLabel).fontColor(Colors.activity)
                    .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
                }
              }.width('100%').height(44).padding({left:8,right:8}).backgroundColor(Color.Transparent).enabled(this.model.connected)
                .accessibilityText(tr(this.historyLabel())).onClick(() => this.loadEarlier()).id('chat.history.more')
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
              } else if (entry.item.message?.role === 'recap') {
                ConversationRecap({ model:this.model, message:entry.item.message!, active:this.active,
                  onSheetChange:(shown:boolean) => this.readingHeader?.overlay(this.headerOwner,'recap',shown), onLink:(address:string) => this.onLink(address) })
              } else if (entry.item.message) this.message(entry)
              else if (entry.item.card) AssistantCards({ model: this.model, feed: this.feed, active: this.active && this.model.connected, onOpenResource: (row: ResourceRow) => this.onOpenResource(row), item: entry.item.card, onOpenCard: (itemId: string, kind: string) => this.onOpenCard(itemId, kind), onOpenSession: (row: ResourceRow) => this.onOpenResource(row),
                onOpenTodo: (row: ResourceRow) => this.onOpenResource(row), onOpenInbox: (row: ResourceRow) => this.onOpenResource(row), onLink: (address: string) => this.onLink(address) })
              else if (entry.item.decision) this.questionCard(entry)
            }.padding({ left: entry.item.message && !entry.item.message.nextStep ? 0 : Tokens.page, right: entry.item.message && !entry.item.message.nextStep ? 0 : Tokens.page }).margin({bottom:entry.item.processReply || entry.item.joinsNext ? 0 : Tokens.fine}).id('chat.line.' + entry.item.id)
              .onSizeChange(() => this.followReading())
          }).key((line: ConversationLine) => line.id).virtualScroll({ totalCount: this.lines.length, reusable: false })
      if (this.active && this.model.sendFeedbackPhase()) ListItem() {
        if (this.model.style === 'beautiful') BeautifulSendStatus({phase:this.model.sendFeedbackPhase(),workHeader:this.model.sendFeedbackWorkHeader()})
        else ChatSendStatus({phase:this.model.sendFeedbackPhase(),reduced:this.environment.reduceMotion})
      }
          if (this.sessionIssue && !this.issueEchoId) ListItem() {
            AgentEventLine({ event: this.sessionIssue!, identity: this.model.target() + this.sessionIssue!.label })
          }.padding({ left: Tokens.page, right: Tokens.page }).id('chat.turn.issue')
        }.cachedCount(2).contentEndOffset(this.transcriptEndInset()).width('100%').height('100%').id('chat.transcript')
          .onAreaChange((_old: Area, area: Area) => this.transcriptViewportChanged(Number(area.height)))
          .bindSheet(this.questions.shown, this.questionContent(), { title: { title: tr('interaction_question_reopen') }, height: SheetSize.LARGE, keyboardAvoidMode: SheetKeyboardAvoidMode.RESIZE_ONLY, showClose: true, onWillAppear: () => this.presentedQuestion = this.questions.beginPresentation(), onAppear: () => { this.questionLayoutReady = true; this.measureQuestion(); }, onWillDismiss: (action: DismissSheetAction) => this.questions.dismiss(this.presentedQuestion, () => action.dismiss()), onDisappear: () => { this.questionLayoutReady = false; this.questions.endPresentation(this.presentedQuestion); this.presentedQuestion = undefined; } })
          .maintainVisibleContentPosition(!this.atEnd || this.photoOpen)
          .onWillScroll((offset: number, _state: ScrollState, source: ScrollSource) => { this.userScroll(offset, source); this.pullHistory(offset); })
          .onReachStart(() => this.reachedEarliest())
          .onScrollIndex((start: number, end: number) => {
            this.trackHistoryRendering(end);
            this.approachHistory(start);
            this.lastVisibleIndex = end;
            this.firstVisibleIndex = start; this.recordReading(start); this.rememberTailPosition();
          })
          .onDidScroll(() => { if (this.scrollingByHand) this.recordReading(this.firstVisibleIndex); })
          .onReachEnd(() => this.reachedLatest())
          .onScrollStop(() => this.finishScroll())
  }

  @Computed get edgeScrubMarks(): EdgeScrubMark[] {
    const sources: EdgeScrubSourceLine[] = [];
    for (let i = 0; i < this.lines.length; i++) {
      const msg = this.lines[i].message;
      if (!msg) continue;
      sources.push({
        id: this.lines[i].id,
        role: msg.role,
        text: msg.text,
        hasImages: msg.imagePaths.length > 0 || !!msg.imageId || msg.localImageCount > 0
      });
    }
    return buildEdgeScrubMarks(sources);
  }

  private jumpToEdgeMark(mark: EdgeScrubMark): void {
    Haptics.tap();
    try {
      this.scroller.scrollToIndex(this.readingPrefix() + mark.lineIndex, !this.environment.reduceMotion, ScrollAlign.START);
    } catch (_) {}
  }

  @Builder messageBody(entry: RepeatItem<ConversationLine>) {
    Column({ space: Tokens.gap }) {
      if (entry.item.message!.role === 'work' && entry.item.message!.running && !entry.item.message!.work) {
        Row({ space: 6 }) {
          WorkingArcSpinner({ diameter: 14, tint: Colors.working, surface: Color.Transparent, showCoreDot: false })
          RunningTurnLabel({ start: entry.item.message!.at, active: this.active })
        }.id('chat.work.' + entry.item.message!.id)
      } else if (entry.item.message!.work) {
        this.workBody(entry)
        if (entry.item.message!.turnChanges) {
          TurnChangesCard({ changes: entry.item.message!.turnChanges!, onOpenFile: (path: string) => this.onOpenFile(path) })
        }
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
      if (entry.item.message!.source) {
        if (this.model.sentForwards(entry.item.message!).length) PhoneForwardSentCard({model:this.model,message:entry.item.message!,
          onOpen:(row:ResourceRow,item:string):void => this.onForwardOrigin(row,item)})
        if (this.model.sentOtherSourceTitle(entry.item.message!))
          Text(this.model.sentOtherSourceTitle(entry.item.message!)).fontSize(Tokens.meta).fontColor($r('sys.color.font_secondary')).width('100%')
      }
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
        MessageSurface({ sourceId:'chat.lift.source.'+entry.item.id, personaReply:entry.item.message!.role === 'assistant' && this.model.selected.kind === 'conversation', own: entry.item.message!.role === 'user', structured: entry.item.message!.role === 'user' && structuredBubble(entry.item.message!.text), text:entry.item.message!.text, maximum: entry.item.message!.role === 'user' ? this.messageMaximum(entry.item.message!) : Tokens.readingMax,
          timeHint:this.timeHint(entry.item.message!), onTime:() => this.toggleMessageTime(entry.item.message!) }) {
          if (entry.item.message!.role === 'assistant' && this.model.selected.kind === 'conversation') AssistantMessage({ streaming:this.isStreamingMessage(entry.item.message!), model: this.model, documents:this.documents, feed: this.feed, message: entry.item.message!, active: this.active, workspaceRoot:this.markdownWorkspace, mediaWidth:Math.min(Tokens.readingMax,this.availableWidth - 2 * Tokens.page), onPreview:(request: ConversationPhotoRequest) => this.showPhoto(request), onEditImage:(image: LoadedMessageImage) => this.editReceivedImage(image), onOpenFile:(path: string) => this.onOpenFile(path), onLink: (address: string) => this.onLink(address), onOpenResource: (row: ResourceRow) => this.onOpenResource(row) })
          else MarkdownText({ streaming:this.isStreamingMessage(entry.item.message!), transcript: true, documents:this.documents, text: entry.item.message!.text, narration:entry.item.message!.narration, ownMessage:entry.item.message!.role === 'user',
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
      if (this.isMessageStarred(entry.item.message!)) {
        Row({ space: Tokens.spaceXs }) {
          SymbolGlyph($r('sys.symbol.star_fill'))
            .fontSize('11vp')
            .fontColor([Colors.working])
          Text(tr('starred_badge'))
            .fontSize(Tokens.typeLabel)
            .maxFontScale(Tokens.interfaceScaleFor(Tokens.typeLabel))
            .fontWeight(FontWeight.Medium)
            .fontColor(Colors.working)
            .maxLines(1)
        }
        .padding({ left: Tokens.spaceS, right: Tokens.spaceS, top: Tokens.spaceXxs, bottom: Tokens.spaceXxs })
        .backgroundColor(Colors.workingWash)
        .borderRadius(Tokens.radiusCapsule)
        .accessibilityText(tr('starred_badge'))
        .id('chat.starred.badge.' + (entry.item.message!.itemId || entry.item.message!.id))
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
      Stack({ alignContent: Alignment.BottomEnd }) {
        this.transcriptList()
        if (this.active && this.edgeScrubMarks.length >= 2) {
          ChatEdgeScrubOverlay({
            marks: this.edgeScrubMarks,
            viewportHeight: this.transcriptHeight,
            onJump: (mark: EdgeScrubMark) => this.jumpToEdgeMark(mark)
          })
        }
        if (!this.legacyInterface && !Colors.stylePage) {
          Row()
            .width('100%')
            .height(22)
            .linearGradient({
              direction: GradientDirection.Bottom,
              colors: [
                [this.environment.dark ? '#00000000' : '#00FFFFFF', 0],
                [this.environment.dark ? '#E6000000' : '#E6FFFFFF', 1]
              ]
            })
            .hitTestBehavior(HitTestMode.None)
            .accessibilityLevel('no')
            .id('chat.footer.fog')
        }
        if (this.jumpAvailable || this.unseenLatest || (!this.atEnd && this.lines.length > 0)) {
          Button({ type: ButtonType.Normal }) {
            Stack() {
              SymbolGlyph(IconActions.symbol('latest')).fontSize(String(this.latestArrowSize)+'vp')
                .fontWeight(FontWeight.Medium).fontColor([Colors.link])
                .width(this.latestArrowSize).height(this.latestArrowSize)
            }.width(this.latestDiameter).height(this.latestDiameter).borderRadius(this.latestDiameter/2).clip(true)
              .backgroundColor(Colors.styleID==='glass' ? (this.environment.dark ? '#CC1C1C1E' : '#E6FFFFFF') : Colors.surface)
              .backgroundBlurStyle(Colors.styleID==='glass' ? BlurStyle.COMPONENT_THICK : BlurStyle.NONE)
              .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.floatingBorder(this.environment.dark), radius: this.latestDiameter / 2 })
              .shadow(PhoneStyle.floatingElevation(this.environment.dark))
          }.width(this.latestDiameter+10).height(this.latestDiameter).padding(0).backgroundColor(Color.Transparent)
            .margin({ right: Tokens.page, bottom: 14 })
            .scale({ x: this.latestPressed ? MotionTokens.pressScale : 1, y: this.latestPressed ? MotionTokens.pressScale : 1 })
            .animation(Motion.release(this.environment.reduceMotion))
            .transition(Motion.fadeScale(this.environment.reduceMotion, 0.85))
            .onTouch((event: TouchEvent) => {
              if (event.type === TouchType.Down) this.latestPressed = true;
              else if (event.type === TouchType.Up || event.type === TouchType.Cancel) this.latestPressed = false;
            })
            .accessibilityText(tr('latest')).bindTips(tr('latest')).id('chat.latest')
            .onClick(() => this.smoothJumpToLatest())
        }
      }.layoutWeight(1).width('100%')
    }.width('100%').constraintSize({ maxWidth: Tokens.readingMax }).height('100%')
      .parallelGesture(TapGesture().onAction(() => this.dismissEditor()))
  }

  // The iPhone glass button has a circular face inside a slightly wider native hit area.
  @Computed get latestDiameter(): number { return PhoneStyle.formSize(54, 66.7, this.environment.fontScale); }
  @Computed get latestArrowSize(): number { return PhoneStyle.formSize(18, 24, this.environment.fontScale); }

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

  // Shell ComposerRoundButton uses its own ink/canvas pair in every style.
  @Computed get legacyInterface():boolean { return this.model.style==='brief' || this.model.style==='board'; }
  private roundInk():ResourceColor {
    if(this.model.style==='beautiful') return this.environment.dark ? '#F2F3F4' : '#1F2124';
    return this.legacyInterface ? Colors.ink : this.environment.dark ? '#F2F2F2' : '#0D0D0D';
  }
  private roundSymbol():ResourceColor {
    if(this.model.style==='beautiful') return this.environment.dark ? '#1C1D1F' : '#F1F2F3';
    return this.legacyInterface ? Colors.canvas : this.environment.dark ? '#0D0D0D' : '#FFFFFF';
  }
  private toolInk():ResourceColor {
    if(this.model.style==='beautiful') return this.environment.dark ? '#A5A8AD' : '#62656B';
    return this.legacyInterface ? Colors.secondary : this.environment.dark ? '#FFFFFF' : '#000000';
  }
  private composerSurface(): ResourceColor {
    if(this.model.style==='beautiful') return this.environment.dark ? '#232427' : '#FFFFFF';
    if(this.legacyInterface) return Colors.surface;
    if(Colors.styleID==='glass') return this.environment.dark ? '#CC1C1C1E' : '#E6FFFFFF';
    return Colors.surface;
  }
  private composerRim(): ResourceColor {
    if(this.model.style==='beautiful') return this.environment.dark ? '#21FFFFFF' : '#ECEDEF';
    return this.legacyInterface ? Colors.divider : PhoneStyle.floatingBorder(this.environment.dark);
  }
  @Builder moreDotsGlyph(tint: ResourceColor) {
    Row({ space: 3.5 }) {
      ForEach([0, 1, 2], (_dot: number) => {
        Circle().width(4).height(4).fill(tint)
      }, (dot: number) => String(dot))
    }.accessibilityLevel('no')
  }
  @Builder composerRound(size: number = 44, inline: boolean = false) {
    Stack() {
      Circle().width(inline ? size * 36 / 44 : 40).height(inline ? size * 36 / 44 : 40).fill(this.roundInk())
      if (IconActions.busy(this.primaryActionKey())) LoadingProgress().width(size * 20 / 44).height(size * 20 / 44).color(this.roundSymbol())
      else SymbolGlyph(this.primaryStop() ? Symbols.stop : Symbols.send).fontSize(String(inline ? this.readingSize.shellPoints : 22) + 'vp').fontWeight(FontWeight.Bold).fontColor([this.roundSymbol()])
    }.width(size).height(size)
  }
  private pageToolbarSize(): number {
    return this.compactDraftSpace ? PhoneStyle.formSize(44,57.7,this.environment.fontScale) : 44;
  }
  private pageSendSize(): number {
    if (!this.compactDraftSpace) return 44;
    // Round the shared row and its button together; independent rounding costs a pixel from the editor.
    const ui=this.getUIContext();
    return ui.px2vp(Math.round(ui.vp2px(this.restingSize.target)));
  }
  private pageSymbolSize(): number { return PhoneStyle.formSize(15,21,this.environment.fontScale); }
  private pageSheetHeight(): number | SheetSize {
    if (!this.compactDraftSpace) return SheetSize.LARGE;
    const ui=this.getUIContext(), env=this.environment;
    return ui.px2vp(env.windowHeightPx) - Math.max(Tokens.compactStatusInset,ui.px2vp(env.safeTopPx)+Tokens.gap);
  }
  @Local pageSheetInset: number = 0;
  private pageSheetNodeId: string = 'chat.draft-page.' + util.generateRandomUUID();
  private measurePageSheet(): void {
    if (!this.draftEditorOpen) return;
    const inset=nativeSheetInset(this.getUIContext(),this.pageSheetNodeId,this.environment.safeBottomPx);
    if (Math.abs(inset-this.pageSheetInset)>.1) this.pageSheetInset=inset;
  }
  @Builder hideKeyboard(page: boolean) {
    Button({type:ButtonType.Circle}) {
      Column({space:0}) {
        SymbolGlyph($r('sys.symbol.keyboard')).fontSize(String(page ? this.pageSymbolSize() : this.readingSize.shellPoints) + 'vp').fontColor([this.toolInk()])
        SymbolGlyph(Symbols.down).fontSize(String((page ? this.pageSymbolSize() : this.readingSize.shellPoints) * 7 / 19) + 'vp').fontWeight(FontWeight.Medium).fontColor([this.toolInk()])
      }
    }.width(page ? this.pageToolbarSize() : this.composerActionSize()).height(page ? this.pageToolbarSize() : this.composerActionSize()).padding(0)
      .responseRegion(ButtonStyles.touchRegion(page ? this.pageToolbarSize() : this.composerActionSize())).backgroundColor(Color.Transparent).accessibilityText(tr('phone_chat_hide_keyboard'))
      .bindTips(tr('phone_chat_hide_keyboard')).id(page ? 'chat.editor.keyboard' : 'chat.keyboard')
      .onClick(() => {
        if (page) { this.pageField.stopEditing(); this.draftEditorFocused=false; }
        else this.inlineField.stopEditing();
      })
  }
  private returnActive():boolean {
    return this.look.returnSends && this.fieldFocused && this.sendCurrent() && !this.draftEditorOpen &&
      !this.modeMenuOpen && !this.actionsVisible && !this.chooser.session && !this.attachmentSheet && this.composerAvailable();
  }
  private allowNextLineBreak():void {
    const revision=++this.inputRevision;
    this.allowLineBreak=true;
    setTimeout(() => { if (this.inputRevision===revision) this.allowLineBreak=false; },0);
  }
  private observeComposition(preview?: PreviewText): void {
    const composing = !!preview?.value;
    if (this.composingReturn && !composing) this.allowNextLineBreak();
    this.composingReturn = composing;
  }
  private inputChanged(value: string, preview?: PreviewText): void {
    this.observeComposition(preview);
    if (!this.draftEditorOpen) this.model.updateDraft(value);
  }
  private returnSend():void {
    if (!this.returnActive() || this.allowLineBreak || this.composingReturn || this.returnDispatching ||
      this.primaryStop() || !this.primaryEnabled()) return;
    this.returnDispatching=true;
    this.sendDraft().finally(() => { this.returnDispatching=false; });
  }
  private softwareImeMayOwnReturn(): boolean {
    if (this.environment.keyboardHeightPx <= 0) return false;
    // The built-in English direct-entry subtype commits each letter to the field. The Chinese subtype holds
    // Pinyin privately and still delivers a hardware Return as submit. Unknown subtypes keep the safer IME path.
    try {
      const subtype = inputMethod.getCurrentInputMethodSubtype();
      return !(subtype.id === 'InputEnLowerService' && subtype.language === 'english' && subtype.mode === 'lower');
    } catch (_) { return true; }
  }
  private observeReturnModifiers(event:KeyEvent):boolean {
    // Leave the actual key event to the editor and its IME.
    if (event.type===KeyType.Down) {
      this.modifierRevision++;
      this.returnKeyMode='';
    }
    if (event.keyCode!==KeyCode.KEYCODE_ENTER && event.keyCode!==KeyCode.KEYCODE_NUMPAD_ENTER) return false;
    if (event.type===KeyType.Down && this.returnActive()) {
      const modified=(key:string):boolean => event.getModifierKeyState?.([key]) ?? false;
      // A visible software keyboard can keep a private candidate strip without publishing marked text.
      // ArkUI still submits an external Return in that state. Keep Return with an IME that may own such a candidate strip.
      if (event.metaKey || modified('Ctrl')) this.returnKeyMode = 'shortcut';
      else if (modified('Shift') || modified('Alt')) {
        this.returnKeyMode = 'line';
        if (this.composingReturn || this.softwareImeMayOwnReturn()) return false;
        this.allowNextLineBreak();
        this.inlineField.addText('\n');
        return true;
      }
      else if (this.softwareImeMayOwnReturn()) this.returnKeyMode = 'ime';
    } else if (event.type===KeyType.Up && this.returnKeyMode) {
      const revision=this.modifierRevision;
      setTimeout(() => { if (revision===this.modifierRevision) this.returnKeyMode=''; },0);
    }
    return false;
  }
  private submittedInline(key:EnterKeyType,event?:SubmitEvent):void {
    if (key!==EnterKeyType.Send || !this.returnActive()) return;
    event?.keepEditableState();
    if (this.composingReturn || this.allowLineBreak || this.returnKeyMode) return;
    this.returnSend();
  }
  @Builder replyGlow() {
    if (this.active && this.look.composerGlow && this.model.assistantReplyWorking() && !this.draftEditorOpen &&
        this.composerFrameWidth > 0 && this.composerFrameHeight > 0) {
      PhoneComposerGlow({barWidth:this.composerFrameWidth,barHeight:this.composerFrameHeight})
    }
  }
  private composerPlaceholder():string {
    if (this.model.agentInputNotice()==='agent_computer_holds') return this.agentNotice();
    if (this.model.selected.kind==='conversation') return tr('chat_ask_persona');
    if (this.model.agentQueuesMessages()) return tr('chat_queued_followup');
    return tr('session_message_to_agent').replace('{agent}',this.composerAgentName());
  }
  private composerAgentName():string {
    const data=this.model.selected.data(), raw=Wire.text(data,'agent_label') || Wire.text(data,'launcher') || Wire.text(data,'agent') || Wire.text(data,'agent_type');
    const agent=raw.toLowerCase();
    const label=agent==='codex' ? 'Codex' : !agent || ['claude','claude-code','claude code'].includes(agent) ? 'Claude' : raw;
    return label;
  }

  @Builder draftPage() {
    Column() {
      Column({space:Tokens.gap}) {
        Row({space:8}) {
          Button({type:ButtonType.Circle}) {
            SymbolGlyph($r('sys.symbol.arrow_down_right_and_arrow_up_left')).fontSize(String(this.pageSymbolSize())+'vp').fontColor([Colors.ink])
          }.width(this.pageToolbarSize()).height(this.pageToolbarSize()).padding(0).backgroundColor(Color.Transparent)
            .responseRegion(ButtonStyles.touchRegion(this.pageToolbarSize())).accessibilityText(tr('phone_chat_close_editor'))
            .onClick(() => { this.finishPageInput(); this.draftEditorOpen=false; }).id('chat.editor.collapse')
          Blank().layoutWeight(1)
          if (this.draftEditorFocused || this.environment.keyboardHeightPx > 0) this.hideKeyboard(true)
        }.width('100%').height(this.pageToolbarSize()).padding({left:Tokens.page,right:Tokens.page})
        Column({space:8}) {
          if (this.model.sourceContext) Text(this.model.contextTitle()).fontSize(Tokens.meta).maxFontScale(Tokens.interfaceScaleFor(Tokens.meta)).fontColor(Colors.secondary)
            .maxLines(this.environment.keyboardHeightPx > 0 ? 1 : 2).textOverflow({overflow:TextOverflow.Ellipsis}).width('100%')
          TextArea({text:this.model.draft,placeholder:this.composerPlaceholder(),controller:this.pageField})
            .keyboardAppearance(this.environment.dark ? KeyboardAppearance.DARK_IMMERSIVE : KeyboardAppearance.LIGHT_IMMERSIVE)
            .maxLength(Tokens.maxMessage).layoutWeight(1).width('100%').fontSize(String(this.readingSize.points)+'vp')
            .placeholderFont({size:String(this.readingSize.shellPoints)+'vp'}).fontColor(Colors.ink)
            .lineHeight(String(this.readingSize.advance)+'vp').letterSpacing(String(this.draftTracking)+'vp').wordBreak(WordBreak.BREAK_WORD).backgroundColor(Color.Transparent).caretColor(Colors.link)
            .padding({left:6,right:6,top:Tokens.gap,bottom:Tokens.gap}).enabled(!this.sending && !this.model.draftLoading)
            .onFocus(() => this.draftEditorFocused=true).onBlur(() => this.draftEditorFocused=false)
            .onTextSelectionChange((start:number,end:number) => { if (this.draftEditorOpen && this.draftEditorCurrent()) this.rememberSelection(start,end); })
            .onChange((text:string) => { if (this.draftEditorOpen && this.draftEditorCurrent()) this.model.updateDraft(text); })
            .id('chat.editor.input')
        }.layoutWeight(1).width('100%').padding({left:Tokens.inset,right:Tokens.inset}).clip(true)
        Row() {
          Blank().layoutWeight(1)
          Button({type:ButtonType.Circle}) {
            this.composerRound(this.pageSendSize(),true)
          }.width(this.pageSendSize()).height(this.pageSendSize()).padding(0).backgroundColor(Color.Transparent)
            .responseRegion(ButtonStyles.touchRegion(this.pageSendSize())).accessibilityText(tr(this.primaryActionKey()))
            .enabled(this.draftEditorCurrent() && this.primaryEnabled()).id('chat.editor.send')
            .onClick(() => {
              if (!this.draftEditorCurrent() || !this.primaryEnabled()) return;
              if (this.primaryStop()) { this.model.stop(() => this.draftEditorCurrent()); return; }
              this.refocusInline=false; this.pageField.stopEditing(); this.draftEditorOpen=false; this.sendDraft();
            })
        }.width('100%').height(this.pageSendSize()+2*Tokens.gap)
          .padding({left:Tokens.page,right:Tokens.page,top:Tokens.gap,bottom:Tokens.gap})
      }.width('100%').height('100%').constraintSize({maxWidth:Tokens.readingMax})
        .padding({top:Tokens.gap,bottom:this.pageSheetInset+(this.compactDraftSpace?.bottomLift ?? 0)}).id('chat.editor')
    }.width('100%').height('100%').backgroundColor(Colors.surface).id(this.pageSheetNodeId)
      .onAreaChange(() => this.measurePageSheet())
  }
  @Computed get composerQueueState(): ComposerQueueState {
    const selected = this.model.selected;
    if (!selected || !selected.id) return extractComposerQueue({});
    const targetSession = selected.session || selected.id;
    const pool = selected.kind === 'conversation' ? this.model.conversations : this.model.sessions;
    const matched = pool.find((row: ResourceRow) =>
      (row.session || row.id) === targetSession &&
      (!selected.machine || row.machine === selected.machine) &&
      (!selected.manager || row.manager === selected.manager)
    );
    const source = matched || selected;
    return extractComposerQueue(source.data());
  }
  private queueSendNowKey(owner: ClientModel = this.model, row: ResourceRow = this.model.selected): string {
    if (!row || !row.id) return '';
    return JSON.stringify([owner.scope(), row.machine, row.session || row.id]);
  }
  private async sendQueuedNow(): Promise<void> {
    if (!this.sendCurrent()) return;
    const owner = this.model, row = owner.selected;
    if (!row || !row.machine || !(row.session || row.id)) return;
    const busyKey = this.queueSendNowKey(owner, row);
    if (!busyKey || this.queueSendNowTarget === busyKey) return;
    const generation = owner.connectionGeneration(), scope = owner.scope(), target = owner.target();
    const admit = (): boolean => this.active && owner === this.model && owner.owns(generation, scope) && owner.target() === target;
    this.queueSendNowTarget = busyKey;
    let sent = false;
    try {
      await owner.repo().call(
        '/api/v1/sessions/' + encodeURIComponent(row.machine) + '/' + encodeURIComponent(row.session || row.id) + '/queue/send-now',
        'POST',
        {},
        '',
        admit,
        false,
        undefined,
        true
      );
      sent = true;
    } catch (error) {
      if (this.model === owner && owner.owns(generation, scope) && owner.target() === target) {
        owner.reportError(error as Error);
        toastCenter.error(tr(owner.error || 'failed'));
      }
    } finally {
      if (this.queueSendNowTarget === busyKey) this.queueSendNowTarget = '';
    }
    if (sent && this.active && this.model === owner && owner.owns(generation, scope) && owner.target() === target) {
      try { await owner.refresh(); } catch (_) {}
    }
  }
  private async mutateQueued(index: number, action: 'edit' | 'delete' | 'move', delta: -1 | 1 = -1): Promise<void> {
    if (!this.sendCurrent()) return;
    const owner = this.model, row = owner.selected;
    if (!row || !row.machine || !(row.session || row.id)) return;
    const queueState = this.composerQueueState;
    if (!canMutateQueueIndex(queueState.ownCount, index)) return;
    if (action === 'move' && (!queueState.steersQueue || queueState.ownCount <= 1)) return;
    const keep = action === 'move'
      ? queueKeepSwapped(queueState.ownCount, index, delta)
      : queueKeepWithout(queueState.ownCount, index);
    if (action === 'move' && !keep.length) return;
    const path = '/api/v1/sessions/' + encodeURIComponent(row.machine) + '/' + encodeURIComponent(row.session || row.id) + '/queue';
    if (owner.operationBlocked(path)) {
      toastCenter.error(tr('pending_action'));
      return;
    }
    const busyKey = this.queueSendNowKey(owner, row);
    if (!busyKey || this.queueSendNowTarget === busyKey) return;
    const generation = owner.connectionGeneration(), scope = owner.scope(), target = owner.target();
    const admit = (): boolean => this.active && owner === this.model && owner.owns(generation, scope) && owner.target() === target;
    this.queueSendNowTarget = busyKey;
    let mutated = false;
    try {
      const response = await owner.repo().call(
        path,
        'POST',
        Wire.payload(['keep'], [keep]),
        '',
        admit,
        false,
        undefined,
        true
      );
      mutated = true;
      if (this.model === owner && owner.owns(generation, scope) && owner.target() === target) {
        if (action === 'edit') {
          const pulled = Wire.strings(response, 'queue');
          const taken = (index < pulled.length ? pulled[index] : '') || queueState.items[index]?.text || '';
          owner.updateDraft(mergeQueuedTakeBackDraft(owner.draft, taken));
          Haptics.success();
          toastCenter.success(tr('composer_queue_taken_back'));
        } else if (action === 'delete') {
          Haptics.success();
          toastCenter.success(tr('composer_queue_deleted'));
        } else {
          Haptics.tap();
        }
      }
    } catch (error) {
      if (this.model === owner && owner.owns(generation, scope) && owner.target() === target) {
        owner.reportError(error as Error);
        Haptics.error();
        toastCenter.error(tr(owner.error || 'failed'));
      }
    } finally {
      if (this.queueSendNowTarget === busyKey) this.queueSendNowTarget = '';
    }
    if (mutated && this.active && this.model === owner && owner.owns(generation, scope) && owner.target() === target) {
      try { await owner.refresh(); } catch (_) {}
    }
  }
  @Computed get quoteRemoveSize(): number { return Math.min(68,44*this.environment.fontScale); }
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
    // The shell measures the decorated editor. Its extra paint area never changes editing layout or hit targets.
    Stack() { this.composerContent() }.width('100%')
      .onAreaChange((_: Area, area: Area) => {
        this.composerFrameWidth = Number(area.width);
        this.composerFrameHeight = Number(area.height);
      })
      .overlay(this.replyGlow(), {align:Alignment.TopStart,offset:{x:-COMPOSER_GLOW_OUTSET,y:-COMPOSER_GLOW_OUTSET}})
  }
  @Builder composerContent() {
    Column({ space: Tokens.gap }) {
      if (this.composerQueueState.items.length > 0) {
        ComposerTopStrip({
          queue: this.composerQueueState,
          busy: this.queueSendNowTarget !== '' && this.queueSendNowTarget === this.queueSendNowKey(),
          onSendNow: () => { void this.sendQueuedNow(); },
          onEditQueued: (index: number) => { void this.mutateQueued(index, 'edit'); },
          onDeleteQueued: (index: number) => { void this.mutateQueued(index, 'delete'); },
          onMoveQueued: (index: number, delta: -1 | 1) => { void this.mutateQueued(index, 'move', delta); }
        })
      }
      if (this.model.hasForwardContext()) PhoneChatForwardTray({model:this.model})
      AttachmentStrip({ attachments: this.attachments.attachments, errorKey: this.attachments.errorKey, actionsEnabled: !this.sending && !this.attachments.busy,
          onRemove: (id: string) => this.attachments.remove(id), onEdit: (id: string) => this.attachments.editPhoto(id), onPreview: (item: ChatAttachment) => this.preview(item) })
      ForEach(this.model.draftMessageQuotes(),(ref:Record<string,Object>) => {
        Row({space:Tokens.gap}) {
          Rect().width(3).height(28).fill(Colors.ai).borderRadius(2)
          Column({space:2}) {
            Text(tr(Wire.text(ref,'part')==='selection' ? 'chat_lift_quote_part' : 'chat_lift_quote_whole').replace('{author}',Wire.text(ref,'title')))
              .fontSize(Tokens.caption).maxFontScale(Tokens.interfaceScaleFor(Tokens.caption)).fontColor(Colors.secondary).maxLines(1)
            Text(Wire.text(ref,'text')).fontSize(Tokens.meta).maxFontScale(Tokens.interfaceScaleFor(Tokens.meta)).fontColor(Colors.ink)
              .maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis}).id('chat.context.quote.text')
          }.layoutWeight(1).alignItems(HorizontalAlign.Start)
          Button({type:ButtonType.Normal}) {
            SymbolGlyph(IconActions.symbol('remove')).fontSize(Tokens.symbolFont).fontColor([Colors.secondary])
          }.width(this.quoteRemoveSize).height(this.quoteRemoveSize).padding(0)
            .buttonStyle(ButtonStyleMode.TEXTUAL).backgroundColor(Color.Transparent)
            .accessibilityText(tr('chat_lift_remove_quote')).enabled(!this.sending)
            .id('chat.context.quote.remove').onClick(() => this.model.removeMessageQuote(ref))
        }.width('100%').padding({left:Tokens.inset,right:Tokens.fine,top:3,bottom:3})
          .backgroundColor(this.model.style==='beautiful' ? this.environment.dark ? '#1F2022' : '#F7F8F9' : Colors.control)
          .borderRadius(Tokens.inset).id('chat.context.quote')
      },(ref:Record<string,Object>, index:number) => Wire.text(ref,'msg',Wire.text(ref,'item_id'))+':'+String(index))
      if (this.model.otherContextTitle()) Row({ space: Tokens.gap }) {
        Text(this.model.otherContextTitle()).fontSize(Tokens.meta).fontColor(Colors.ink).maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis }).layoutWeight(1)
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('remove')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('remove', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('remove')).bindTips(tr('remove')).enabled(!this.sending).onClick(() => this.model.removeOtherContexts())
      }.width('100%')
      GridRow({ columns: this.composerColumns(), gutter: { x: 0, y: 0 } }) {
        GridCol({ span: this.expandedComposer() ? this.composerColumns() : this.composerColumns() - this.leftColumns() - this.sendColumns(), order: this.expandedComposer() ? 0 : 1 }) {
          Stack({alignContent:Alignment.TopEnd}) {
            Stack({alignContent:Alignment.Start}) {
              TextArea({ text: this.model.draft, controller:this.inlineField, placeholder:this.composerPlaceholder() })
                .keyboardAppearance(this.environment.dark ? KeyboardAppearance.DARK_IMMERSIVE : KeyboardAppearance.LIGHT_IMMERSIVE)
                .maxLength(Tokens.maxMessage).width('100%')
                .fontSize(this.expandedComposer() ? String(this.readingSize.points) + 'vp' : Tokens.body)
                .placeholderFont({size:String(this.restingSize.promptPoints) + 'vp'})
                .fontColor(Colors.ink).placeholderColor(Color.Transparent).caretColor(Colors.action).backgroundColor(Color.Transparent)
                .lineHeight(this.expandedComposer() ? String(this.readingSize.advance) + 'vp' : String(Tokens.lineHeight) + 'fp')
                .letterSpacing(this.expandedComposer() ? String(this.draftTracking) + 'vp' : 0).wordBreak(WordBreak.BREAK_WORD)
                .padding(this.expandedComposer() ? 0 : { left: Tokens.gap, right: Tokens.gap, top: Tokens.inset, bottom: Tokens.inset })
                .height(this.expandedComposer() ? this.expandedFieldHeight() : this.composerActionSize())
                .enabled(!this.sending && !this.model.draftLoading).onFocus(() => this.fieldFocused = true)
                .onBlur(() => { this.fieldFocused=false;this.composingReturn=false;this.returnKeyMode='';this.modifierRevision++;this.allowLineBreak=false;this.inputRevision++; })
                .enterKeyType(this.look.returnSends ? EnterKeyType.Send : EnterKeyType.NEW_LINE).enablePreviewText(true)
                .onKeyPreIme((event:KeyEvent) => this.observeReturnModifiers(event))
                .onWillChange((change:EditableTextChangeValue) => { this.observeComposition(change.previewText); return true; })
                .onPaste(() => this.allowNextLineBreak())
                .onSubmit((key:EnterKeyType,event?:SubmitEvent) => this.submittedInline(key,event))
                .onTextSelectionChange((start:number,end:number) => { if (!this.draftEditorOpen) this.rememberSelection(start,end); })
                .onChange((value:string,preview?:PreviewText) => this.inputChanged(value,preview)).id('chat.input')
              // The native editor keeps its accessible hint and reading size. Its visible prompt uses the shell's single line.
              if (!this.model.draft) {
                Text(this.composerPlaceholder()).width('100%').maxLines(1)
                  .minFontSize(String(this.restingSize.promptPoints / 2) + 'vp').maxFontSize(String(this.restingSize.promptPoints) + 'vp')
                  .textOverflow({overflow:TextOverflow.Ellipsis}).fontColor(Colors.tertiary)
                  .hitTestBehavior(HitTestMode.None).accessibilityLevel('no')
              }
            }.width('100%').constraintSize({minHeight:this.expandedComposer() ? this.composerActionSize() * (this.offersDraftEditor() ? 48 : 44) / 44 : this.composerActionSize()})
              .padding(this.expandedComposer() ? {left:Tokens.inset, right:Tokens.inset + (this.offersDraftEditor() ? this.restingSize.target : 0), top:Tokens.page} : 0)
            if (this.offersDraftEditor()) {
              Button({type:ButtonType.Circle}) {
                SymbolGlyph($r('sys.symbol.arrow_up_left_and_arrow_down_right')).fontSize('17vp').fontColor([Colors.ink])
              }.width(this.restingSize.target).height(this.restingSize.target).padding(0).margin({right:Tokens.fine,top:Tokens.fine}).backgroundColor(Color.Transparent)
                .responseRegion(ButtonStyles.touchRegion(this.restingSize.target)).accessibilityText(tr('phone_chat_expand_editor')).bindTips(tr('phone_chat_expand_editor'))
                .onClick(() => this.expandDraftEditor()).id('chat.expand')
            }
          }.width('100%').constraintSize({minHeight:this.expandedComposer() ? this.restingSize.target : this.composerActionSize()}).bindSheet($$this.draftEditorOpen,this.draftPage(),
            {height:this.pageSheetHeight(),showClose:false,dragBar:false,backgroundColor:Colors.surface,keyboardAvoidMode:SheetKeyboardAvoidMode.RESIZE_ONLY,
              onAppear:() => { this.measurePageSheet(); this.focusDraftPage(); },onWillDismiss:(action:DismissSheetAction) => { this.finishPageInput(); action.dismiss(); },
              onDisappear:() => this.draftPageGone()})
        }.id(this.editorAnchorId)
          .onAreaChange((_old: Area, area: Area) => { this.editorLeft = Number(area.globalPosition.x); this.editorTop = Number(area.globalPosition.y); this.editorWidth = Number(area.width); this.editorHeight = Number(area.height); })
          .bindPopup(this.chooser.shown && this.chooser.session?.preserveInput === true, this.chooserPopupOptions())
        if (!this.model.demo || this.showContextMenu || this.expandedComposer() && this.inlineKeyboardAction()) GridCol({ span: this.verticalActions() ? this.composerColumns() : this.leftColumns(), order: this.expandedComposer() ? this.verticalActions() ? 2 : 1 : 0 }) {
          Row({ space: this.expandedComposer() ? Tokens.fine : Tokens.gap }) {
            if (!this.model.demo) Button({ type: ButtonType.Circle }) { SymbolGlyph(Symbols.plus).fontSize(String(this.restingSize.plus) + 'vp').fontWeight(FontWeight.Regular).fontColor([this.toolInk()]) }
              .attributeModifier(ButtonStyles.quiet).width(this.composerActionSize()).height(this.composerActionSize()).padding(0)
              .responseRegion(ButtonStyles.touchRegion(this.composerActionSize()))
              .accessibilityText(tr('attach'))
              .enabled(!this.sending && !this.attachments.busy && !this.model.draftLoading).onClick((event: ClickEvent) => this.openAttachmentChooser(event)).id('chat.attach')
              .bindMenu(this.nativeChooser('attachments'), this.attachmentNativeMenu, this.attachmentMenuOptions())
              .parallelGesture(LongPressGesture().onAction(() => { if (this.showContextMenu) this.requestComposerActions(false); }))
              .bindContextMenu(this.nativeChooser('actions'), this.attachmentNativeMenu, this.actionContextMenuOptions())
            if (this.showContextMenu && this.model.demo) Button({ type: ButtonType.Circle }) {
              SymbolGlyph(Symbols.plus).fontSize(String(this.restingSize.plus) + 'vp').fontWeight(FontWeight.Regular).fontColor([this.toolInk()])
            }
              .attributeModifier(ButtonStyles.quiet).width(this.composerActionSize()).height(this.composerActionSize()).padding(0).accessibilityText(tr('more')).onClick((event: ClickEvent) => { Haptics.tap(); this.openComposerActions(event); })
              .parallelGesture(LongPressGesture().onAction(() => this.requestComposerActions(false)))
              .bindContextMenu(this.nativeChooser('actions'), this.attachmentNativeMenu, this.actionContextMenuOptions()).id('chat.more')
            if (this.expandedComposer() && this.inlineKeyboardAction()) this.hideKeyboard(false)
          }.width('100%').padding({left:this.expandedComposer() ? Tokens.fine : 0}).constraintSize({ minHeight: this.composerActionSize() }).alignItems(VerticalAlign.Center)
        }
        if (this.expandedComposer() && !this.verticalActions()) GridCol({ span: this.composerColumns() - this.leftColumns() - this.sendColumns(), order: 3 }) {
          Row() {
            SessionComposerModeChip({model:this.model,active:this.active && !this.draftEditorOpen,onMenu:(open:boolean) => this.modeMenuOpen=open}).layoutWeight(1)
          }.width('100%').constraintSize({ minHeight: this.composerActionSize() })
        }
        GridCol({ span: this.verticalActions() ? this.composerColumns() : this.sendColumns(), order: this.expandedComposer() ? this.verticalActions() ? 1 : 4 : 2 }) {
          Row({space:this.expandedComposer() ? Tokens.fine : 0}) {
          if (this.verticalActions()) SessionComposerModeChip({model:this.model,active:this.active && !this.draftEditorOpen,onMenu:(open:boolean) => this.modeMenuOpen=open}).layoutWeight(1)
          PhoneDictationControl({owner: this.dictationOwner(),ink:this.toolInk(), target:this.composerActionSize(),
            resting:this.composerAvailable(), symbolSize:this.restingSize.microphone,
            active: this.active && !this.draftEditorOpen && !this.sending && !this.model.draftLoading, language: this.model.language,
            chooseLanguage: () => this.model.dictationLocaleForChat(), onListeningIn: (locale: string) => this.model.rememberDictationLocale(locale),
            allowed: (owner: string) => owner === this.dictationOwner() && this.sendCurrent() && this.composerAvailable(),
            keyboard: () => this.getUIContext().getFocusController().requestFocus('chat.input'),
            insert: (text: string, owner: string): boolean => {
              if (owner !== this.dictationOwner() || !this.sendCurrent() || !this.composerAvailable() || this.model.draftLoading || this.sending) return false;
              const draft = this.model.draft, separator = /[a-zA-Z0-9]$/.test(draft) ? ' ' : '';
              if (draft.length + separator.length + text.length > Tokens.maxMessage) return false;
              this.model.updateDraft(draft + separator + text);
              return true;
            }})
          if (this.hasComposerAction()) Button({ type: ButtonType.Circle }) {
            this.composerRound(this.expandedComposer() ? this.composerActionSize() : 44, this.expandedComposer())
          }.accessibilityText(tr(this.primaryActionKey())).buttonStyle(ButtonStyleMode.TEXTUAL).padding(0)
            .width(this.expandedComposer() ? this.composerActionSize() : 44).height(this.expandedComposer() ? this.composerActionSize() : 44)
            .responseRegion(ButtonStyles.touchRegion(this.expandedComposer() ? this.composerActionSize() : 44))
            .backgroundColor(Color.Transparent)
            .enabled(this.primaryEnabled())
            .scale({ x: this.sendPressed ? 0.90 : 1, y: this.sendPressed ? 0.90 : 1 })
            .animation(Motion.release(this.environment.reduceMotion))
            .transition(Motion.fadeScale(this.environment.reduceMotion, 0.8))
            .onTouch((event: TouchEvent) => {
              if (event.type === TouchType.Down) this.sendPressed = true;
              else if (event.type === TouchType.Up || event.type === TouchType.Cancel) this.sendPressed = false;
            })
            .onClick(() => {
              if (!this.sendCurrent()) return;
              if (this.primaryStop()) { Haptics.attention(); this.model.stop(() => this.sendCurrent()); }
              else { Haptics.tap(); this.sendDraft(); }
            }).id('chat.send')
          }.width('100%').height(this.composerActionSize()).alignItems(VerticalAlign.Center).justifyContent(FlexAlign.End)
            .padding({ right: this.expandedComposer() ? Tokens.fine : 0 })
        }
      }.width('100%').onAreaChange((_old: Area, area: Area) => { const width = Number(area.width); if (Number.isFinite(width) && width > 0) this.composerWidth = width; })
    }.padding(this.composerPadding()).backgroundColor(this.composerSurface())
      .backgroundBlurStyle(Colors.styleID === 'glass' && !this.legacyInterface && this.model.style !== 'beautiful' ? BlurStyle.COMPONENT_THICK : BlurStyle.NONE)
      .border({width:this.restingComposer() ? String(this.restingStrokePx()) + 'px' : this.composerStroke,
        color:this.composerRim(),radius:25}).borderRadius(25).width('100%').id('chat.composer')
      .animation(Motion.layout(this.environment.reduceMotion))
      .shadow(this.model.style==='beautiful'
        ? {radius:8,color:this.environment.dark ? '#40000000' : '#12000000',offsetY:5}
        : PhoneStyle.floatingElevation(this.environment.dark))
  }

  @Builder taskDock(horizontalInset: number = 0) {
    if (this.model.selected.kind === 'conversation' && this.model.connected) {
          AssistantCards({ model: this.model, feed: this.feed, active: this.active && this.model.connected, onOpenResource: (row: ResourceRow) => this.onOpenResource(row), dockOnly: true, decisionPager:this.decisionPager, dockRoom:this.availableHeight, composerHeight:this.assistantFooterHeight, mainDockSpace:this.mainDockSpace(), onDockVisibility:(shown:boolean):void => { this.assistantDockVisible=shown; }, onOpenCard: (itemId: string, kind: string) => this.onOpenCard(itemId, kind), onOpenSession: (row: ResourceRow) => this.onOpenResource(row),
            onOpenTodo: (row: ResourceRow) => this.onOpenResource(row), onOpenInbox: (row: ResourceRow) => this.onOpenResource(row), onLink: (address: string) => this.onLink(address) })
            .width('100%').padding({ left: horizontalInset, right: horizontalInset })
    }
  }
  @Builder blockingContent() {
    Column({ space: Tokens.gap }) {
      if (this.model.selected.kind === 'session') {
        AgentPendingCards({ model: this.model, room: this.agentCardsRoom(), keyboard: this.environment.keyboardHeightPx > 0,
          onCountHeight:(height:number) => { if (Math.abs(height-this.agentCountHeight)>.5) this.agentCountHeight=height; }, admission: () => this.sendCurrent(), onLink: (address: string) => this.onLink(address) })
          .padding({ left: 12, right: 12 })
        if (this.composerAvailable()) Column() { this.composer() }.width('100%').padding(this.controlsPadding())
          .onAreaChange((_old: Area, area: Area) => {
            const h = Number(area.height);
            if (Number.isFinite(h) && Math.abs(h - this.agentComposerHeight) > 0.5) this.agentComposerHeight = h;
          })
      } else {
        this.taskDock()
      }
      if (this.showContextMenu && !this.composerAvailable()) Button({ type: ButtonType.Circle }) { this.moreDotsGlyph(Colors.ink) }
        .attributeModifier(ButtonStyles.quiet).width(Tokens.touch).height(Tokens.touch).padding(0).accessibilityText(tr('more')).onClick(() => { Haptics.tap(); this.openFallbackActions(); })
        .bindMenu(this.actionsVisible, this.actionsMenu, this.actionMenuOptions(this.actionsRequest)).id('chat.more.fallback')
    }.width('100%').constraintSize({ maxWidth: Tokens.readingMax, maxHeight: this.maxControlsHeight() })
      .padding(this.model.selected.kind === 'session' ? 0 : { left: Tokens.inset, right: Tokens.inset, bottom: Tokens.footer, top: Tokens.gap })
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
    else Scroll(this.controlsScroller) {
      Column({ space: Tokens.gap }) {
      if (this.showContextMenu && (this.model.decision || !this.model.canCompose())) Button({ type: ButtonType.Circle }) { this.moreDotsGlyph(Colors.ink) }
        .attributeModifier(ButtonStyles.quiet).width(Tokens.touch).height(Tokens.touch).padding(0).accessibilityText(tr('more')).onClick(() => { Haptics.tap(); this.openFallbackActions(); })
        .bindMenu(this.actionsVisible, this.actionsMenu, this.actionMenuOptions(this.actionsRequest)).id('chat.more.fallback')
      this.taskDock()
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
        if (Number.isFinite(height) && height > 0 && Math.abs(height - this.controlsHeight) > 1) {
          this.controlsHeight = height;
          this.revealOverflowingActions();
        }
      })
    }.width('100%').height('100%').enableScrollInteraction(!this.assistantDockVisible || this.controlsHeight>this.maxControlsHeight()+1)
      .onAreaChange(() => this.revealOverflowingActions())
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
        if (this.forwardState.isPicking(this.model)) PhoneChatPickBar({source:this.model,state:this.forwardState})
        else if (this.model.decision) this.blockingContent()
        else Column() { this.interactionContent() }.width('100%')
          .height(Math.max(1, Math.min(this.controlsHeight, this.maxControlsHeight())))
      }
    }.width('100%').height('100%')
      .backgroundColor(this.legacyInterface || Colors.stylePage ? Colors.canvas : this.environment.dark ? Color.Black : Color.White)
      .expandSafeArea([SafeAreaType.SYSTEM], [SafeAreaEdge.BOTTOM])
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
