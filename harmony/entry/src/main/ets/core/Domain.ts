/** Pure client behavior shared by the Harmony models. This module has no platform calls. */

export interface ScopeKey {
  readonly groupId: string;
  /** The default manager is the empty string. Keep it distinct from every named manager. */
  readonly managerId: string;
}

export function sameScope(left: ScopeKey, right: ScopeKey): boolean {
  return left.groupId === right.groupId && left.managerId === right.managerId;
}

/** A collision-safe storage key for a hub group and its manager owner. */
export function scopeKey(scope: ScopeKey): string {
  return JSON.stringify([scope.groupId, scope.managerId]);
}

/** Use an item's returned owner when a merged response supplies one. */
export function ownedScopeForItem(
  groupId: string,
  selectedManagerId: string,
  returnedOwnerId?: string | null,
): ScopeKey {
  return {
    groupId,
    managerId: returnedOwnerId === undefined || returnedOwnerId === null ? selectedManagerId : returnedOwnerId,
  };
}

export interface ScopeRequestToken {
  readonly scope: ScopeKey;
  readonly generation: number;
}

/**
 * Fences late reads when the visible group/manager changes. The owner still cancels its
 * transport task; `isCurrent` prevents a transport that ignores cancellation from publishing.
 */
export class ScopeGeneration {
  private activeScope: ScopeKey | null = null;
  private generation = 0;

  select(scope: ScopeKey): ScopeRequestToken {
    if (this.activeScope === null || !sameScope(this.activeScope, scope)) {
      this.activeScope = { groupId: scope.groupId, managerId: scope.managerId };
      this.generation += 1;
    }
    return this.tokenForActiveScope();
  }

  /** Invalidate outstanding work after a pairing change, sign-out, or explicit refresh reset. */
  invalidate(): ScopeRequestToken | null {
    if (this.activeScope === null) return null;
    this.generation += 1;
    return this.tokenForActiveScope();
  }

  currentToken(): ScopeRequestToken | null {
    if (this.activeScope === null) return null;
    return this.tokenForActiveScope();
  }

  isCurrent(token: ScopeRequestToken): boolean {
    return this.activeScope !== null && this.generation === token.generation && sameScope(this.activeScope, token.scope);
  }

  private tokenForActiveScope(): ScopeRequestToken {
    const scope = this.activeScope;
    if (scope === null) throw new Error("No active scope");
    return { scope: { groupId: scope.groupId, managerId: scope.managerId }, generation: this.generation };
  }
}

export interface SessionCapabilityFlags {
  readonly read?: boolean;
  readonly send?: boolean;
  readonly interrupt?: boolean;
  readonly approve?: boolean;
  readonly terminal?: boolean;
  readonly attach?: boolean;
}

export interface SessionCapabilityInput {
  readonly sourceKind?: string | null;
  readonly capabilities?: SessionCapabilityFlags | null;
  readonly readOnly?: boolean;
  readonly readOnlyReason?: string | null;
  readonly attached?: boolean | null;
}

export interface EffectiveSessionCapabilities {
  readonly hasSource: boolean;
  readonly readOnly: boolean;
  readonly canRead: boolean;
  readonly canSend: boolean;
  readonly canInterrupt: boolean;
  readonly canApprove: boolean;
  readonly canTerminal: boolean;
  readonly canAttach: boolean;
}

/**
 * Old sessions without `source_kind` keep legacy access. A sourced session opts in one
 * capability at a time; missing fields refuse that operation. Explicit read-only and identity
 * conflict metadata narrow writes; `read_only_reason` explains the source state but is not a gate.
 * Attachment is available only until the hub reports it attached.
 */
export function sessionCapabilities(input: SessionCapabilityInput): EffectiveSessionCapabilities {
  const sourceKind = input.sourceKind ?? "";
  const hasSource = sourceKind.length > 0;
  const identityConflict = sourceKind === "identity_conflict";
  const readOnly = input.readOnly === true || identityConflict;
  const raw = input.capabilities;
  const allows = (capability: keyof SessionCapabilityFlags): boolean => !hasSource || raw?.[capability] === true;

  return {
    hasSource,
    readOnly,
    canRead: allows("read"),
    canSend: !readOnly && allows("send"),
    canInterrupt: !readOnly && allows("interrupt"),
    canApprove: !readOnly && allows("approve"),
    canTerminal: !readOnly && allows("terminal"),
    canAttach: hasSource && !readOnly && input.attached !== true && raw?.attach === true,
  };
}

export type InteractionAnswerState =
  | "pending"
  | "answering"
  | "answered"
  | "expired"
  | "resolvedElsewhere"
  | "handedToTerminal"
  | "unknown";

/** The unified answer flow only begins a new answer for a known pending card. */
export function canSubmitInteractionAnswer(state: InteractionAnswerState): boolean {
  return state === "pending";
}

export function canAnswerInteraction(
  state: InteractionAnswerState,
  session: SessionCapabilityInput | null,
  sessionless: boolean,
): boolean {
  if (!canSubmitInteractionAnswer(state)) return false;
  if (sessionless) return true;
  if (session === null) return false;
  return sessionCapabilities(session).canApprove;
}

export type JsonValue = string | number | boolean | null | JsonObject | readonly JsonValue[];

export interface JsonObject {
  readonly [key: string]: JsonValue | undefined;
}

export interface TranscriptFold extends JsonObject {
  readonly through_seq: number;
  readonly through_id: string;
  readonly items?: number;
  readonly tools?: number;
  readonly thoughts?: number;
  readonly errors?: number;
  readonly tool_names?: readonly string[];
  readonly supervision_user?: boolean;
}

export interface TranscriptItem extends JsonObject {
  readonly id: string;
  readonly seq: number;
  readonly kind: string;
  readonly role?: string;
  readonly text?: string;
  readonly tool_use_id?: string;
  readonly toolUseId?: string;
  readonly supervision_user?: boolean;
  readonly fold?: TranscriptFold;
  readonly wake?: boolean;
}

export type TranscriptGap = "none" | "fill" | "reload";

export interface TranscriptInitialState {
  readonly items: readonly TranscriptItem[];
  readonly covered: number;
  readonly history: string | null;
  readonly hasMore: boolean | null;
  readonly loadedOnce: boolean;
}

export interface TranscriptBatch {
  readonly items: readonly TranscriptItem[];
  readonly history?: string | null;
  readonly fromSeq?: number | null;
  readonly toSeq?: number | null;
  readonly backlog?: boolean;
  readonly hasMore?: boolean | null;
  readonly moreAfter?: boolean;
}

export type TranscriptAction =
  | { readonly kind: "fetch_after"; readonly afterSeq: number }
  | { readonly kind: "fetch_latest"; readonly foldWork: true }
  | { readonly kind: "subscribe"; readonly sinceSeq?: number; readonly foldWork: true }
  | { readonly kind: "fetch_older"; readonly beforeSeq: number; readonly foldWork: true }
  | { readonly kind: "fetch_range"; readonly fromSeq: number; readonly throughSeq: number };

export interface TranscriptSnapshot {
  readonly items: readonly TranscriptItem[];
  readonly covered: number;
  readonly history: string | null;
  readonly hasMore: boolean | null;
  readonly loadedOnce: boolean;
}

function cloneJson(value: JsonValue): JsonValue {
  if (Array.isArray(value)) {
    const copied: JsonValue[] = [];
    for (const entry of value) copied.push(cloneJson(entry));
    return copied;
  }
  if (typeof value === "object" && value !== null) {
    const copied: { [key: string]: JsonValue | undefined } = {};
    for (const key of Object.keys(value)) {
      const child = value[key];
      if (child !== undefined) copied[key] = cloneJson(child);
    }
    return copied;
  }
  return value;
}

function cloneItem(item: TranscriptItem): TranscriptItem {
  return cloneJson(item) as TranscriptItem;
}

function itemPart(id: string): number {
  const suffix = id.slice(id.lastIndexOf("#") + 1);
  if (!/^[+-]?\d+$/.test(suffix)) return 0;
  const part = Number(suffix);
  return Number.isInteger(part) ? part : 0;
}

function compareItems(left: TranscriptItem, right: TranscriptItem): number {
  if (left.seq !== right.seq) return left.seq - right.seq;
  return itemPart(left.id) - itemPart(right.id);
}

function isWorkItem(item: TranscriptItem): boolean {
  if (item.kind === "thinking" || item.kind === "tool_use" || item.kind === "tool_result" || item.kind === "fold") return true;
  // A fold range distinguishes assistant narration from its final answer.
  if (item.kind === "text") return item.role === "assistant";
  if (item.kind !== "image") return false;
  const toolUseId = item.tool_use_id ?? item.toolUseId;
  return typeof toolUseId === "string" && toolUseId.length > 0;
}

function foldContains(fold: TranscriptItem, other: TranscriptItem): boolean {
  const range = fold.fold;
  if (range === undefined) return false;
  const atOrAfterStart = other.seq > fold.seq ||
    (other.seq === fold.seq && itemPart(other.id) >= itemPart(fold.id));
  const atOrBeforeEnd = other.seq < range.through_seq ||
    (other.seq === range.through_seq && itemPart(other.id) <= itemPart(range.through_id));
  return atOrAfterStart && atOrBeforeEnd;
}

function hasSupervisionEvidence(item: TranscriptItem): boolean {
  return item.supervision_user === true || item.fold?.supervision_user === true;
}

function markSupervisionVisible(item: TranscriptItem): TranscriptItem {
  if (item.supervision_user === true && (!item.fold || item.fold.supervision_user === true)) return item;
  if (item.fold === undefined) return { ...item, supervision_user: true };
  return { ...item, supervision_user: true, fold: { ...item.fold, supervision_user: true } };
}

function retainSupervisionEvidence(
  item: TranscriptItem,
  promotedIds: ReadonlySet<string>,
  promotedFolds: readonly TranscriptItem[],
  promotedWork: readonly TranscriptItem[],
): TranscriptItem {
  const shouldKeep = promotedIds.has(item.id) ||
    (isWorkItem(item) && promotedFolds.some((fold) => foldContains(fold, item))) ||
    (item.kind === "fold" && promotedWork.some((work) => foldContains(item, work)));
  return shouldKeep ? markSupervisionVisible(item) : item;
}

/** Whether this fold's represented work fits inside a range already opened in this history. */
function foldWasOpened(fold: TranscriptItem, opened: TranscriptItem): boolean {
  const currentRange = fold.fold;
  const openedRange = opened.fold;
  if (currentRange === undefined || openedRange === undefined) return false;
  return currentRange.through_seq < openedRange.through_seq ||
    (currentRange.through_seq === openedRange.through_seq &&
      itemPart(currentRange.through_id) <= itemPart(openedRange.through_id));
}

function sameFold(left: TranscriptFold | undefined, right: TranscriptFold | undefined): boolean {
  if (left === undefined || right === undefined) return left === right;
  return jsonEqual(left, right);
}

function jsonEqual(left: JsonValue | undefined, right: JsonValue | undefined): boolean {
  if (left === right) return true;
  if (left === undefined || right === undefined || left === null || right === null) return false;
  if (typeof left !== typeof right) return false;
  if (Array.isArray(left)) {
    if (!Array.isArray(right) || left.length !== right.length) return false;
    for (let index = 0; index < left.length; index += 1) {
      if (!jsonEqual(left[index], right[index])) return false;
    }
    return true;
  }
  if (typeof left === "object") {
    if (Array.isArray(right) || typeof right !== "object") return false;
    const leftKeys = Object.keys(left).sort();
    const rightKeys = Object.keys(right).sort();
    if (leftKeys.length !== rightKeys.length) return false;
    for (let index = 0; index < leftKeys.length; index += 1) {
      const key = leftKeys[index];
      if (key !== rightKeys[index] || !jsonEqual(left[key], right[key])) return false;
    }
    return true;
  }
  return false;
}

/** Reduces history pages and pushed transcript batches without transport or UI state. */
export class TranscriptReducer {
  private readonly byId = new Map<string, TranscriptItem>();
  private cachedPromotionIds = new Set<string>();
  private readonly openedFoldRanges = new Map<string, TranscriptItem>();
  private _history: string | null = null;
  private _covered = 0;
  private _hasMore: boolean | null = null;
  private _loadedOnce = false;
  private _contentVersion = 0;
  get contentVersion(): number { return this._contentVersion; }

  get history(): string | null { return this._history; }
  get covered(): number { return this._covered; }
  get hasMore(): boolean | null { return this._hasMore; }
  get loadedOnce(): boolean { return this._loadedOnce; }

  get firstSeq(): number | null {
    let first: number | null = null;
    for (const item of this.byId.values()) first = first === null ? item.seq : Math.min(first, item.seq);
    return first;
  }

  get items(): readonly TranscriptItem[] {
    return [...this.byId.values()].sort(compareItems).map(cloneItem);
  }

  snapshot(): TranscriptSnapshot {
    return {
      items: this.items,
      covered: this._covered,
      history: this._history,
      hasMore: this._hasMore,
      loadedOnce: this._loadedOnce,
    };
  }

  restore(state: TranscriptInitialState): void {
    this.byId.clear(); this._contentVersion++;
    this.openedFoldRanges.clear();
    this.cachedPromotionIds.clear();
    for (const item of state.items) {
      if (item.supervision_user === true) this.cachedPromotionIds.add(item.id);
      this.byId.set(item.id, cloneItem(item));
    }
    this._history = state.history;
    this._covered = Math.max(0, state.covered);
    this._hasMore = state.hasMore;
    this._loadedOnce = state.loadedOnce;
  }

  latest(batch: TranscriptBatch): TranscriptGap {
    return this.apply({ ...batch, backlog: true });
  }

  apply(batch: TranscriptBatch): TranscriptGap {
    const history = batch.history ?? null;
    const fromSeq = batch.fromSeq ?? null;
    const toSeq = batch.toSeq ?? null;
    const backlog = batch.backlog === true;

    if (history !== null && this._history !== null && history !== this._history) {
      this.restore({ items: [], history, covered: 0, hasMore: null, loadedOnce: false });
      if (fromSeq !== null && fromSeq > 0) return "reload";
    }
    if (history !== null) this._history = history;

    if (toSeq === null) {
      this.merge(batch.items, backlog, batch.hasMore ?? null);
      let maxItemSeq = 0;
      for (const item of this.byId.values()) maxItemSeq = Math.max(maxItemSeq, item.seq);
      this._covered = Math.max(this._covered, maxItemSeq);
      return batch.moreAfter === true ? "reload" : "none";
    }

    if (fromSeq !== null) {
      this.merge(batch.items, backlog, batch.hasMore ?? null);
      if (this._covered === 0 && fromSeq > 0) return this._loadedOnce ? "reload" : "none";
      if (fromSeq > this._covered) return "fill";
      this._covered = Math.max(this._covered, toSeq);
      return batch.moreAfter === true ? "fill" : "none";
    }

    const first = batch.items[0];
    if (this._covered > 0 && first !== undefined && first.seq > this._covered + 1) {
      this.replace(batch.items, history, toSeq, batch.hasMore ?? true);
      if (backlog) this._loadedOnce = true;
      return "none";
    }
    this.merge(batch.items, backlog, batch.hasMore ?? null);
    this._covered = Math.max(this._covered, toSeq);
    return "none";
  }

  /** Reload the newest page while retaining newer pushes from the same transcript history. */
  replace(items: readonly TranscriptItem[], history: string | null, toSeq: number | null, hasMore: boolean): void {
    const keep: TranscriptItem[] = [];
    const sameHistory = history === null || history === this._history;
    if (sameHistory && toSeq !== null) {
      for (const item of this.byId.values()) if (item.seq > toSeq) keep.push(item);
    }
    this.openedFoldRanges.clear();
    this.byId.clear(); this._contentVersion++;
    this._hasMore = hasMore;
    if (history !== null) this._history = history;
    this.merge(items);
    this.merge(keep);
    let maxItemSeq = 0;
    for (const item of this.byId.values()) maxItemSeq = Math.max(maxItemSeq, item.seq);
    this._covered = toSeq ?? maxItemSeq;
  }

  /** Older pages add rows and change only an explicitly supplied pagination flag. */
  older(items: readonly TranscriptItem[], hasMore: boolean | null = null): void {
    if (hasMore !== null) this._hasMore = hasMore;
    this.merge(items);
  }

  /** Replace one fold with its fetched work, ignoring fold placeholders in range responses. */
  unfold(fold: TranscriptItem, work: readonly TranscriptItem[]): void {
    const retained = this.byId.get(fold.id);
    if (retained === undefined) return;
    this.byId.delete(fold.id); this._contentVersion++;
    this.openedFoldRanges.set(fold.id, cloneItem(retained));
    this.merge(work.filter((item) => item.kind !== "fold"));
  }

  private merge(items: readonly TranscriptItem[], backlog = false, hasMore: boolean | null = null): void {
    const evidence: TranscriptItem[] = [];
    for (const item of this.byId.values()) if (hasSupervisionEvidence(item)) evidence.push(item);
    for (const item of this.openedFoldRanges.values()) if (hasSupervisionEvidence(item)) evidence.push(item);
    for (const item of items) if (hasSupervisionEvidence(item)) evidence.push(item);
    const promotedIds = new Set([...evidence.filter((item) => item.supervision_user === true).map((item) => item.id), ...this.cachedPromotionIds]);
    this.cachedPromotionIds.clear();
    const promotedFolds = evidence.filter((item) => item.fold !== undefined);
    const promotedWork = evidence.filter(isWorkItem);
    const promote = promotedIds.size > 0 || promotedFolds.length > 0 || promotedWork.length > 0;
    if (promote) {
      for (const [id, existing] of this.byId) {
        const promoted = retainSupervisionEvidence(existing, promotedIds, promotedFolds, promotedWork);
        if (promoted !== existing) { this.byId.set(id, promoted); this._contentVersion++; }
      }
    }
    const promotedItems = promote ? items.map(item => retainSupervisionEvidence(item, promotedIds, promotedFolds, promotedWork)) : items;
    const batchWork = promotedItems.filter((item) => item.kind !== "fold" && isWorkItem(item));
    const freshItems = promotedItems.filter((item) => {
      if (item.kind !== "fold") return true;
      const opened = this.openedFoldRanges.get(item.id);
      if (opened !== undefined && foldWasOpened(item, opened)) return false;
      return !batchWork.some((work) => foldContains(item, work));
    });
    const newFolds: TranscriptItem[] = [];
    for (const item of freshItems) {
      if (item.kind !== "fold") continue;
      const old = this.byId.get(item.id);
      if (old === undefined || !sameFold(old.fold, item.fold)) newFolds.push(item);
    }

    if (newFolds.length > 0) {
      for (const [id, existing] of this.byId) {
        if (isWorkItem(existing) && newFolds.some((fold) => foldContains(fold, existing))) { this.byId.delete(id); this._contentVersion++; }
      }
    }

    if (batchWork.length > 0) {
      for (const [id, existing] of this.byId) {
        if (existing.kind === "fold" && batchWork.some((work) => foldContains(existing, work))) { this.byId.delete(id); this._contentVersion++; }
      }
    }

    const existingFirst = this.firstSeq;
    for (const item of freshItems) {
      const old = this.byId.get(item.id);
      if (!old || !jsonEqual(old, item)) {
        this.byId.set(item.id, cloneItem(item)); this._contentVersion++;
      }
    }

    let reachesStart = false;
    if (items.length > 0) {
      let minSeq = items[0].seq;
      for (const item of items) minSeq = Math.min(minSeq, item.seq);
      reachesStart = minSeq <= (existingFirst ?? items[0].seq);
    }
    if (hasMore !== null && backlog && (!this._loadedOnce || this.byId.size <= items.length) && (hasMore || reachesStart)) {
      this._hasMore = hasMore;
    }
    if (backlog) this._loadedOnce = true;
  }
}

export interface TranscriptSyncResult {
  readonly gap: TranscriptGap;
  readonly actions: readonly TranscriptAction[];
}

/** Pure fetch decisions. Callers own timers, task cancellation, and HTTP/WebSocket execution. */
export class TranscriptSync {
  private filling = false;
  private fillPages = 0;
  private heard = 0;
  private asked = 0;
  private loadingOlder = false;
  private openingFold: TranscriptItem | null = null;
  private rangePages = 0;
  private readonly rangeWork: TranscriptItem[] = [];

  readonly reducer: TranscriptReducer;

  constructor(reducer: TranscriptReducer) {
    this.reducer = reducer;
  }

  settle(gap: TranscriptGap): readonly TranscriptAction[] {
    if (gap === "fill") { this.heard++; return this.continueFill(); }
    if (gap === "reload") return [{ kind: "fetch_latest", foldWork: true }];
    return [];
  }

  /** iOS starts immediately; an in-flight fill collapses duplicates and reads again after a move heard in flight. */
  continueFill(): readonly TranscriptAction[] {
    if (this.filling) return [];
    this.filling = true;
    this.fillPages = 0;
    this.asked = this.heard;
    return [{ kind: "fetch_after", afterSeq: this.reducer.covered }];
  }

  after(batch: TranscriptBatch): TranscriptSyncResult {
    const before = this.reducer.covered;
    const toSeq = batch.toSeq;
    const gap = this.reducer.apply(batch);
    this.fillPages += 1;
    const progressed = this.reducer.covered > before || toSeq !== undefined && toSeq !== null && toSeq > before;
    const again = gap === "none" && this.heard > this.asked;
    const actions: readonly TranscriptAction[] = gap === "reload" || ((gap === "fill" || again) && this.fillPages >= 8)
      ? [{ kind: "fetch_latest", foldWork: true }]
      : ((gap === "fill" && progressed) || again)
        ? [{ kind: "fetch_after", afterSeq: this.reducer.covered }]
        : [];
    if (actions.length > 0) this.asked = this.heard;
    if (actions.length === 0 || actions[0].kind === "fetch_latest") this.finishFill();
    return { gap, actions };
  }

  get behind(): boolean { return this.heard > this.asked; }

  finishFill(): void {
    this.filling = false;
    this.fillPages = 0;
  }

  reload(items: readonly TranscriptItem[], history: string | null, toSeq: number | null, hasMore: boolean): void {
    this.reducer.replace(items, history, toSeq, hasMore);
    this.finishFill();
  }

  reconnect(): readonly TranscriptAction[] {
    const covered = this.reducer.covered;
    return covered > 0
      ? [{ kind: "subscribe", sinceSeq: covered, foldWork: true }]
      : [{ kind: "subscribe", foldWork: true }];
  }

  loadOlder(): readonly TranscriptAction[] {
    if (this.loadingOlder || this.reducer.hasMore !== true) return [];
    const first = this.reducer.firstSeq;
    if (first === null) return [];
    this.loadingOlder = true;
    return [{ kind: "fetch_older", beforeSeq: first, foldWork: true }];
  }

  older(items: readonly TranscriptItem[], hasMore: boolean): void {
    this.reducer.older(items, hasMore);
    this.finishOlder();
  }

  finishOlder(): void { this.loadingOlder = false; }

  openFold(id: string): readonly TranscriptAction[] {
    if (this.openingFold !== null) return [];
    const fold = this.reducer.items.find((item) => item.id === id && item.kind === "fold");
    if (fold === undefined || fold.fold === undefined) return [];
    this.openingFold = fold;
    this.rangePages = 0;
    this.rangeWork.length = 0;
    return [{ kind: "fetch_range", fromSeq: fold.seq, throughSeq: fold.fold.through_seq }];
  }

  range(items: readonly TranscriptItem[], more: boolean): readonly TranscriptAction[] {
    const fold = this.openingFold;
    if (fold === null || fold.fold === undefined) throw new Error("Range response without an open fold");
    for (const item of items) this.rangeWork.push(cloneItem(item));
    this.rangePages += 1;
    const last = items[items.length - 1];
    if (more && last !== undefined && this.rangePages < 100) {
      return [{ kind: "fetch_range", fromSeq: last.seq + 1, throughSeq: fold.fold.through_seq }];
    }
    this.reducer.unfold(fold, this.rangeWork);
    this.cancelFold();
    return [];
  }

  cancelFold(): void {
    this.openingFold = null;
    this.rangePages = 0;
    this.rangeWork.length = 0;
  }
}

export interface NarrationInspectionStep {
  readonly kind: "narration";
  readonly id: string;
  readonly role: "text" | "thinking" | "notice";
  readonly item: TranscriptItem;
}

export interface ToolInspectionStep {
  readonly kind: "tool";
  readonly id: string;
  readonly use: TranscriptItem;
  readonly results: readonly TranscriptItem[];
  readonly result: TranscriptItem | null;
  readonly images: readonly TranscriptItem[];
  readonly reports: readonly TranscriptItem[];
  readonly running: boolean;
  readonly failed: boolean;
  readonly recovered?: boolean;
}

export interface FoldInspectionStep {
  readonly kind: "fold";
  readonly id: string;
  readonly item: TranscriptItem;
  readonly toolCount: number;
  readonly thoughtCount: number;
}

export type WorkInspectionStep = NarrationInspectionStep | ToolInspectionStep | FoldInspectionStep;

export interface ToolTurnProjection {
  readonly turnId: string;
  readonly workKey: string;
  readonly running: boolean;
  readonly latestExchange?: boolean;
  readonly agent?: string;
  readonly phaseTitle?: string;
  readonly sectionTitle?: string;
  readonly summaryVisible: boolean;
  readonly collapsedByDefault: true;
  readonly expanded: boolean;
  readonly userMessages: readonly TranscriptItem[];
  readonly userImages: readonly TranscriptItem[];
  readonly events: readonly TranscriptItem[];
  readonly visibleAssistantText: TranscriptItem | null;
  readonly visibleTextKind: "interim" | "final" | null;
  /** Ordered steps with their original call/result ownership. */
  readonly inspectionSteps: readonly WorkInspectionStep[];
  readonly toolCount: number;
  readonly thoughtCount: number;
  readonly unresolvedToolCount: number;
}

interface MutableToolInspection {
  readonly item: TranscriptItem;
  readonly results: TranscriptItem[];
  readonly images: TranscriptItem[];
  readonly reports: TranscriptItem[];
}

export function transcriptRecord(value: JsonValue | undefined): JsonObject | undefined {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as JsonObject : undefined;
}

// Match against full recorded arguments, never the shortened title shown on a phone.
function retryTarget(use: TranscriptItem): string {
  const input = use.tool_input ?? use.input;
  const args = transcriptRecord(input);
  const command = args?.command ?? args?.cmd;
  if (typeof command === 'string') return JSON.stringify(['command', command]);
  const name = String(use.tool_name ?? use.tool ?? use.name ?? '').toLowerCase().replace(/^functions\./, '');
  if (['read', 'read_file', 'readfile', 'view_image', 'imageview'].includes(name)) {
    const file = args?.file_path ?? args?.path;
    if (typeof file === 'string') return JSON.stringify(['file', file]);
  }
  return JSON.stringify(input ?? null);
}

function receiptStatus(step: ToolInspectionStep): string {
  if (!step.result) return '';
  const status = transcriptRecord(step.result.outcome)?.status;
  return typeof status === 'string' && status ? status : step.result.is_error ? 'failed' : 'ok';
}

function retryResource(args: JsonObject | undefined): JsonValue | undefined {
  return args?.file_path ?? args?.path ?? args?.url ?? args?.target ?? args?.id ?? args?.task_id ?? args?.session_id;
}

function commandFamily(args: JsonObject | undefined): string {
  const words = String(args?.command ?? args?.cmd ?? '').trim().split(/\s+/, 2);
  // Treat distinct subcommands of common command-line tools as distinct operations.
  return ['git','npm','cargo','go','kubectl'].includes(words[0]) ? words.join(' ') : words[0];
}

function rejectedArguments(step: ToolInspectionStep): boolean {
  const text = (step.result?.text || '').slice(0, 1600);
  return /(?:InputValidationError|(?:invalid|bad)[_ ](?:params|parameters|arguments|tool input)|(?:unknown|unrecognized|invalid|missing|unexpected|required) (?:option|flag|argument|parameter)|failed to parse (?:function |tool )?arguments|missing required (?:field|property))/i.test(text);
}

/** Reconcile retries within one exchange before narration splits it into visible groups. */
function markRecoveredAttempts(steps: WorkInspectionStep[]): void {
  const firstFailure = steps.findIndex(step => step.kind === 'tool' && ['failed','timed_out','blocked'].includes(receiptStatus(step)));
  if (firstFailure < 0) return;
  const latestSuccess = new Map<string, TranscriptItem>();
  const nextAttempt = new Map<string, ToolInspectionStep>();
  for (let i = steps.length - 1; i >= firstFailure; i--) {
    const step = steps[i];
    if (step.kind !== 'tool') continue;
    const use = step.use, receipt = step.result;
    const tool = String(use.tool_name ?? use.tool ?? use.name ?? '').replace(/^functions\./, '');
    if (!tool) continue;
    const args = transcriptRecord(use.tool_input ?? use.input);
    const scope = JSON.stringify([tool, args?.workdir ?? args?.cwd ?? '']);
    const key = JSON.stringify([scope, retryTarget(use)]);
    const status = receiptStatus(step);
    if (receipt && status === 'ok') {
      if (!latestSuccess.has(key)) latestSuccess.set(key, use);
    } else if (receipt && (status === 'failed' || status === 'timed_out' || status === 'blocked')) {
      let success = latestSuccess.get(key);
      const next = nextAttempt.get(scope);
      if ((!success || compareItems(receipt, success) >= 0) && next?.result && (next.recovered || receiptStatus(next) === 'ok')) {
        const nextArgs = transcriptRecord(next.use.tool_input ?? next.use.input);
        const resource = retryResource(args), command = commandFamily(args);
        const sameTask = !!command && typeof args?.description === 'string' && !!args.description.trim() &&
          args.description === nextArgs?.description;
        const sameBlockedResource = status === 'blocked' && !!resource && resource === retryResource(nextArgs);
        // Claude can reject a shell invocation before it starts. The corrected script for the
        // same described task may use a different launcher (for example, cd instead of VAR=…).
        const refusedShellRetry = sameTask && tool.toLowerCase() === 'bash' &&
          /^Claude Code did not run this command:/im.test((receipt.text || '').slice(0, 1600));
        if ((sameTask || sameBlockedResource || rejectedArguments(step)) && (!resource || resource === retryResource(nextArgs)) &&
          (!command || command === commandFamily(nextArgs) || refusedShellRetry)) {
          success = next.use;
        }
      }
      if (success && compareItems(receipt, success) < 0) {
        const recovered: ToolInspectionStep = { ...step, recovered:true };
        steps[i] = recovered;
        nextAttempt.set(scope, recovered);
        continue;
      }
    }
    nextAttempt.set(scope, step);
  }
}

function inputBlock(item: TranscriptItem): boolean {
  return item.role === 'user' && (item.kind === 'text' || (item.kind === 'image' && !itemToolUseId(item)));
}

function entryId(id: string): string { return id.split('#', 1)[0]; }

function codexOpening(item: TranscriptItem | undefined): string {
  const receipt = transcriptRecord(item?.turn_duration);
  return receipt?.source === 'codex_task_complete' && typeof receipt.user_item_id === 'string' ? entryId(receipt.user_item_id) : '';
}

/** Codex steers can be ordinary user items. Only join them when its end receipt names the opening prompt. */
function completedCodexSteers(items: readonly TranscriptItem[]): Set<string> {
  const steers = new Set<string>(), prompts = new Map<string, number>();
  let boundary = 0;
  items.forEach((item, index) => {
    const id = entryId(item.id);
    if (inputBlock(item) && !prompts.has(id)) prompts.set(id, index);
    if (item.kind !== 'turn_duration') return;
    const opening = codexOpening(item);
    const start = prompts.get(opening);
    if (opening && (start !== undefined || boundary === 0)) {
      let answered = false;
      for (let i = start ?? 0; i < index; i++) {
        const part = items[i];
        if (part.wake || part.kind === 'recap' || part.supervision_user ||
          (part.kind === 'notice' && ['Interrupted','已中断','中断されました'].includes(part.text || ''))) break;
        if (part.kind === 'turn_duration' && (prompts.get(codexOpening(part)) ?? i) >= (start ?? 0)) break;
        if (part.role === 'assistant' && part.kind === 'text' && !part.narration && !isEmptyText(part)) answered = true;
        if (inputBlock(part)) {
          if (answered) break; // A late duration cannot swallow the next independent question.
          if (entryId(part.id) !== opening) steers.add(entryId(part.id));
        }
      }
    }
    boundary = index + 1;
  });
  return steers;
}

/** Keep the provider's turn boundaries and route late results to the call that produced them. */
export function transcriptExchanges(items: readonly TranscriptItem[]): TranscriptItem[][] {
  const groups: TranscriptItem[][] = [];
  const steers = completedCodexSteers(items);
  const calls = new Map<string, TranscriptItem[]>();
  const entries = new Map<string, { tail: TranscriptItem[] }>();
  let group: TranscriptItem[] = [];
  let exchange = { tail: group };
  let ended = false;
  let previousInput = '';
  for (const item of items) {
    const call = itemToolUseId(item);
    if (call && (item.kind === 'tool_result' || item.kind === 'image')) {
      const owner = calls.get(call);
      if (owner) { owner.push(item); continue; }
    }
    if (item.kind === 'turn_duration') {
      const duration = transcriptRecord(item.turn_duration);
      const reference = duration?.user_item_id || duration?.parent_uuid;
      const owner = typeof reference === 'string' ? entries.get(entryId(reference))?.tail : undefined;
      if (owner && owner !== group) { owner.push(item); continue; }
    }
    const input = inputBlock(item), entry = entryId(item.id);
    const newInput = input && entry !== previousInput && !steers.has(entry);
    if (group.length && (ended || newInput || item.wake === true || item.kind === 'recap')) {
      group = [];
      // A queued addition starts a display segment. Its duration can still name the original prompt.
      if (newInput && item.queued === true && !ended && item.supervision_user !== true) exchange.tail = group;
      else exchange = { tail: group };
    }
    if (!group.length) groups.push(group);
    group.push(item);
    entries.set(entry, exchange);
    if (call && item.kind === 'tool_use') calls.set(call, group);
    previousInput = input ? entry : '';
    ended = item.kind === 'recap' || item.kind === 'turn_duration';
  }
  return groups;
}

/** Only recorded questions become quotes. Old nodes may have retained just the user's answer. */
export function displayQuestionReply(item: TranscriptItem): TranscriptItem {
  const value = item.question_replies;
  if (!item.text || !Array.isArray(value) || !value.length) return item;
  const answers: string[] = [], formatted: string[] = [];
  for (const pair of value) {
    const record = transcriptRecord(pair);
    if (!record || typeof record.question !== 'string' || !record.question.trim()) return item;
    const question = record.question.trim();
    const quote = question.split(/\r?\n/).map(line => '> ' + line).join('\n');
    if (value.length === 1) return { ...item, text:quote + '\n\n' + item.text };
    if (record.answer !== undefined && typeof record.answer !== 'string') return item;
    const answer = typeof record.answer === 'string' ? record.answer : '';
    answers.push(question + '：' + answer);
    formatted.push(quote + '\n\n' + answer);
  }
  const plain = answers.join('\n').trim();
  if (!item.text.startsWith(plain)) return item;
  return { ...item, text:formatted.join('\n\n') + item.text.slice(plain.length) };
}

function displayTranscriptItem(item: TranscriptItem): TranscriptItem {
  if (item.role === 'user' && item.kind === 'text') return displayQuestionReply(item);
  if (item.role !== 'assistant' || item.kind !== 'text' || !item.text) return item;
  const text = item.text.trim();
  const completed = /^::browser-complete\{title=("(?:[^"\\]|\\.)*")\}$/.exec(text);
  if (completed) {
    try {
      const title = JSON.parse(completed[1]);
      if (typeof title === 'string' && title.trim()) return { ...item, kind:'browser_complete', text:title };
    } catch (_) { /* A malformed or quoted example stays literal. */ }
  }
  if (/^API Error:\s*(?:Request rejected\s*\(\d{3}\)|[45]\d\d\b)/i.test(text)) return { ...item, kind:'provider_error' };
  return item;
}

/** Older nodes identify a compaction by its canonical system notice. Claude can then emit
 * a continuation notice for the same boundary, including across two pages of history. */
function displayTranscriptItems(items: readonly TranscriptItem[]): TranscriptItem[] {
  const display: TranscriptItem[] = [];
  for (const item of items) {
    if (item.kind === 'notice' && item.role === 'system' && !item.task && !item.wake && !item.compact &&
      (item.text === '上下文已压缩' || item.text === '已从压缩的上下文继续')) {
      if (item.text === '已从压缩的上下文继续' && display[display.length - 1]?.compact) continue;
      display.push({ ...item, compact: {} });
    } else {
      display.push(displayTranscriptItem(item));
    }
  }
  return display;
}

function isSeparateEvent(item: TranscriptItem): boolean {
  if (item.kind === 'model_change' || item.kind === 'recap' || item.kind === 'visualization' || item.kind === 'browser_complete' || item.kind === 'provider_error') return true;
  if (item.kind === 'turn_duration') {
    const ms = transcriptRecord(item.turn_duration)?.duration_ms;
    return typeof ms === 'number' && Number.isFinite(ms) && ms >= 0;
  }
  if (item.kind === 'image') return item.role !== 'user' && !itemToolUseId(item);
  return item.kind === 'notice' && (item.wake === true || !!item.model_refusal || !!item.task ||
    ['已中断', 'Interrupted', '中断されました'].includes(item.text || ''));
}

function itemToolUseId(item: TranscriptItem): string | null {
  const raw = item.tool_use_id ?? item.toolUseId;
  return typeof raw === "string" && raw.length > 0 ? raw : null;
}

function isEmptyText(item: TranscriptItem): boolean {
  return typeof item.text !== "string" || item.text.trim().length === 0;
}

/**
 * Projects one already-scoped turn for the chat list. The last assistant text replaces earlier
 * live narration in place; on completion it is the final answer. Everything else stays in one
 * manually expandable work summary, collapsed unless the caller restored an explicit user choice.
 */
export function projectToolTurn(
  turnId: string,
  inputItems: readonly TranscriptItem[],
  running: boolean,
  expanded = false,
): ToolTurnProjection {
  const items = [...inputItems].sort(compareItems);
  let visibleAssistantText: TranscriptItem | null = null;
  let latestNarration: TranscriptItem | null = null;
  const userMessages: TranscriptItem[] = [];
  const userImages: TranscriptItem[] = [];
  const events: TranscriptItem[] = [];
  for (const item of items) {
    if (item.kind === 'text' && item.role === 'assistant' && !isEmptyText(item)) {
      if (item.narration === true) latestNarration = item;
      else visibleAssistantText = item;
    }
    if (!inputBlock(item)) continue;
    if (item.kind === "image") {
      if (itemToolUseId(item) === null) userImages.push(item);
      continue;
    }
    userMessages.push(item);
  }
  if (running && !visibleAssistantText) visibleAssistantText = latestNarration;

  const byToolUseId = new Map<string, MutableToolInspection>();
  const byItemId = new Map<string, MutableToolInspection>();
  let firstCall = -1, lastCall = -1;
  for (let index = 0; index < items.length; index++) {
    const item = items[index];
    if (item.kind !== "tool_use") continue;
    if (firstCall < 0) firstCall = index;
    lastCall = index;
    const record: MutableToolInspection = { item, results: [], images: [], reports: [] };
    byItemId.set(item.id, record);
    const useId = itemToolUseId(item);
    if (useId !== null) byToolUseId.set(useId, record);
  }
  for (const item of items) {
    const useId = itemToolUseId(item);
    if (useId === null) continue;
    const record = byToolUseId.get(useId);
    if (record === undefined) continue;
    if (item.kind === "tool_result") record.results.push(item);
    if (item.kind === "image") record.images.push(item);
  }

  const inspectionSteps: WorkInspectionStep[] = [];
  let toolCount = 0;
  let thoughtCount = 0;
  let unresolvedToolCount = 0;
  for (let index = 0; index < items.length; index += 1) {
    const item = items[index];
    if (visibleAssistantText !== null && item.id === visibleAssistantText.id) continue;
    if (isSeparateEvent(item)) {
      const task = transcriptRecord(item.task);
      const origin = typeof task?.tool_use_id === 'string' ? byToolUseId.get(task.tool_use_id) : undefined;
      if (item.kind === 'notice' && origin && item.wake !== true &&
        ['running', 'completed', 'event', 'ended'].includes(String(task?.status))) origin.reports.push(item);
      else events.push(item);
      continue;
    }
    if (item.kind === "tool_use") {
      const record = byItemId.get(item.id);
      if (record === undefined) continue;
      const results = [...record.results].sort(compareItems);
      const images = [...record.images].sort(compareItems);
      const result = results.length === 0 ? null : results[results.length - 1];
      inspectionSteps.push({
        kind: "tool",
        id: item.id,
        use: item,
        results,
        result,
        images,
        reports: record.reports,
        running: result === null,
        failed: result?.is_error === true,
      });
      toolCount += 1;
      if (result === null) unresolvedToolCount += 1;
      continue;
    }
    if (item.kind === "tool_result") continue;
    if (item.kind === "image" && itemToolUseId(item) !== null) continue;
    if (item.kind === "thinking") {
      if (isEmptyText(item) && !(firstCall >= 0 && firstCall < index && index < lastCall)) continue;
      inspectionSteps.push({ kind: "narration", id: item.id, role: "thinking", item });
      thoughtCount += 1;
      continue;
    }
    if (item.kind === "text" && item.role === "assistant") {
      inspectionSteps.push({ kind: "narration", id: item.id, role: "text", item });
      continue;
    }
    if (item.kind === "notice") {
      inspectionSteps.push({ kind: "narration", id: item.id, role: "notice", item });
      continue;
    }
    if (item.kind === "fold") {
      const fold = item.fold;
      const foldedTools = fold?.tools ?? 0;
      const foldedThoughts = fold?.thoughts ?? 0;
      inspectionSteps.push({ kind: "fold", id: item.id, item, toolCount: foldedTools, thoughtCount: foldedThoughts });
      toolCount += foldedTools;
      thoughtCount += foldedThoughts;
    }
  }

  markRecoveredAttempts(inspectionSteps);
  return {
    turnId,
    workKey: `w-${turnId}`,
    running,
    summaryVisible: running || inspectionSteps.length > 0,
    collapsedByDefault: true,
    expanded,
    userMessages,
    userImages,
    events,
    visibleAssistantText,
    visibleTextKind: visibleAssistantText === null ? null : running ? "interim" : "final",
    inspectionSteps,
    toolCount,
    thoughtCount,
    unresolvedToolCount,
  };
}

export interface NextStepChoice { label: string; action?: string; }
export interface NextStepProposal { recap: string; ask: string; choices: NextStepChoice[]; selected?: string; }

const desktopActions: Record<string,string> = {
  commit:'Commit changes', create_pr:'Create pull request', merge:'Merge changes', fix_ci:'Fix CI',
  address_comments:'Address comments', resolve_conflicts:'Resolve conflicts', verify:'Verify changes', archive:'Archive chat'
};

function nextStepProposal(step: ToolInspectionStep): NextStepProposal | undefined {
  if (step.result?.is_error || receiptStatus(step) !== 'ok' || !step.result?.text?.startsWith('Shown in the next-step box.')) return undefined;
  const input = transcriptRecord(step.use.tool_input);
  const recap = input?.recap ?? input?.outcome, ask = input?.ask ?? input?.next_ask ?? input?.next_brief;
  const actions = input?.actions ?? input?.next_actions;
  if (typeof recap !== 'string' || !recap.trim() || typeof ask !== 'string' || !ask.trim() || !Array.isArray(actions)) return undefined;
  const choices: NextStepChoice[] = [];
  for (const raw of actions.slice(0,3)) {
    const entry = transcriptRecord(raw);
    if (typeof entry?.action === 'string') {
      if (Object.prototype.hasOwnProperty.call(desktopActions,entry.action)) choices.push({action:entry.action,label:desktopActions[entry.action]});
    } else if (typeof entry?.label === 'string') {
      const label = entry.label.trim();
      if (label && label.length <= 100 && !/^[!/]|[\u0000-\u001f\u007f\u200b-\u200f\u202a-\u202e\u2060-\u206f\ufe00-\ufe0f\ufeff]/.test(label)) choices.push({label});
    }
  }
  return choices.length ? {recap:recap.trim(),ask:ask.trim(),choices} : undefined;
}

export type TranscriptDisplayRow =
  | { kind: 'work'; id: string; work: ToolTurnProjection }
  | { kind: 'input' | 'narration' | 'reply' | 'image' | 'event' | 'next_step'; id: string; item: TranscriptItem; proposal?: NextStepProposal; duration?: TranscriptItem; processIds?: string[]; leadIds?: string[]; partialProcess?: boolean };

/** A viewed image forms its own disclosure when it interrupts a run of file or command activity. */
export function isImageInspection(step: ToolInspectionStep): boolean {
  const name = String(step.use.tool_name ?? step.use.tool ?? step.use.name ?? '').toLowerCase().replace(/^functions\./, '');
  return name === 'view_image' || name === 'imageview' || ((name === 'read' || name === 'read_file') && step.images.length > 0);
}

export interface LiveToolCall { readonly id: string; readonly name: string }

/** Overlay the current call inventory for rendering. Cursors, caches and edit history use only durable items. */
function withLiveTools(input: readonly TranscriptItem[], liveTools: readonly LiveToolCall[]): readonly TranscriptItem[] {
  if (!liveTools.length) return input;
  const pending = new Map(liveTools.map(tool => [tool.id, tool] as const));
  const returned = new Set(input.filter(item => item.kind === 'tool_result').map(itemToolUseId));
  let seq = 0;
  const displayed = input.map(item => {
    seq = Math.max(seq, item.seq);
    const call = itemToolUseId(item);
    if (item.kind === 'tool_use' && call && pending.delete(call) && !returned.has(call)) {
      return { ...item, live_tool:true };
    }
    return item;
  });
  for (const [id, tool] of pending) {
    if (!returned.has(id)) displayed.push({ id:id + '#0', seq:++seq, kind:'tool_use', role:'assistant',
      tool_use_id:id, tool_name:tool.name, live_tool:true, live_tool_preview:true });
  }
  return displayed;
}

/** A change in a Codex summary can separate historical batches without displaying its text. */
function thoughtCaption(value: JsonValue | undefined): string {
  if (typeof value !== 'string' || !value.trim()) return '';
  const lines = value.trim().split(/\r?\n/);
  let title = '';
  for (const raw of lines) {
    const line = raw.trim();
    const heading = /^(?:#{1,6}\s+(.+)|\*\*(.+?)\*\*|__(.+?)__)$/.exec(line);
    if (heading) title = heading[1] || heading[2] || heading[3];
  }
  if (!title) title = lines.find(line => line.trim())?.trim() || '';
  return title.replace(/`([^`]+)`/g, '$1').slice(0, 160).trim();
}

/** Only the summary tool's successful public labels can name a phase between calls. */
function recordedPhase(step: ToolInspectionStep): string {
  if (step.use.tool_name !== 'mcp__codex_app__update_running_summary' || receiptStatus(step) !== 'ok') return '';
  const labels = transcriptRecord(step.use.tool_input)?.labels;
  if (!Array.isArray(labels) || typeof labels[0] !== 'string') return '';
  let title = labels[0].trim();
  if (labels.length === 3 && typeof labels[1] === 'string' && typeof labels[2] === 'string') {
    if (/^\S+ing$/i.test(title) && /^\S+ed$/i.test(labels[1])) title += ' ' + labels[2];
    else if (labels[1] === '已' + title.replace(/^正在/, '')) title += labels[2];
  }
  return title && title.length <= 160 && !/[\x00-\x1f\x7f]/.test(title) ? title : '';
}

function stepCaption(step: WorkInspectionStep, claude: boolean): string {
  if (step.kind === 'tool') return recordedPhase(step);
  if (claude) return ''; // Claude already sends public progress as separate narration.
  if (step.kind === 'fold') return thoughtCaption(transcriptRecord(step.item.fold?.latest_thinking)?.text);
  return step.role === 'thinking' ? thoughtCaption(step.item.text) : '';
}

/** Claude's public Bash description names the batch even between the receipt and the next assistant event. */
function commandCaption(value: JsonValue | undefined): string {
  if (typeof value !== 'string') return '';
  const line = value.trim().split(/[\r\n]/, 1)[0].trim();
  if (/^(?:bash|(?:functions\.)?exec(?:_command)?)$/i.test(line)) return '';
  const chars = Array.from(line);
  return chars.length > 160 ? chars.slice(0,159).join('') + '…' : line;
}

/** A new public caption starts a new batch with its next operation. Captions alone do not multiply groups. */
function completedWorkSections(steps: WorkInspectionStep[], claude: boolean): WorkInspectionStep[][] {
  const batches: WorkInspectionStep[][] = [];
  let start = 0, next = -1, caption = '', hasCall = false;
  steps.forEach((step, i) => {
    const title = stepCaption(step, claude);
    if (title) {
      if (hasCall && title !== caption && next < 0) next = i;
      caption = title;
    }
    if ((step.kind === 'tool' && !recordedPhase(step)) || (step.kind === 'fold' && step.toolCount > 0)) {
      if (next > start) { batches.push(steps.slice(start, next)); start = next; next = -1; }
      hasCall = true;
    }
  });
  batches.push(steps.slice(start));
  return batches;
}

/** Put each public update at its original position and keep receipts, images and reports attached to their call. */
export function projectTranscriptRows(input: readonly TranscriptItem[], live: boolean, liveTools: readonly LiveToolCall[] = [], agent: string = '', actionLabel?: (action: string) => string): TranscriptDisplayRow[] {
  const rows: TranscriptDisplayRow[] = [];
  const groups = transcriptExchanges(displayTranscriptItems(withLiveTools(input, liveTools)));
  groups.forEach((items, index) => {
    const start = rows.length;
    const first = items[0], latestExchange = index === groups.length - 1;
    const running = live && latestExchange && first.kind !== 'recap' && !items.some(item => item.kind === 'turn_duration');
    const work = projectToolTurn(first.id, items, running);
    const steps = new Map(work.inspectionSteps.map(step => [step.id, step]));
    const events = new Set(work.events.map(item => item.id));
    const claude = agent === 'claude' || (!agent && items.some(item => typeof item.model === 'string' && item.model.startsWith('claude-')));
    const final = (running && claude) || work.visibleAssistantText?.narration === true ? undefined : work.visibleAssistantText?.id;
    let reply: Extract<TranscriptDisplayRow, { item: TranscriptItem }> | undefined;
    let wrap: ToolInspectionStep | undefined;
    let proposal: NextStepProposal | undefined;
    if (!running) {
      for (const step of work.inspectionSteps) {
        if (step.kind === 'tool' && step.use.tool_name === 'mcp__ccd_turn__wrap_up') wrap = step;
      }
      if (wrap) {
        const closed = !!final && !!work.visibleAssistantText && compareItems(work.visibleAssistantText,wrap.use) > 0;
        if (closed || wrap.result?.text?.includes('box ends your turn')) proposal = nextStepProposal(wrap);
        if (proposal && !latestExchange) {
          const next = groups[index + 1][0];
          const response = next.kind === 'text' && next.role === 'user' && !next.wake && !next.supervision_user ? next.text?.trim() : '';
          if (response && proposal.choices.some(choice => choice.label === response || (choice.action && actionLabel?.(choice.action) === response))) {
            proposal.selected = response;
          } else proposal = undefined;
        }
      }
    }
    let section: WorkInspectionStep[] = [], sections = 0;
    let family = '', phaseTitle = '';
    const emit = (steps: WorkInspectionStep[]): void => {
      const batch = steps.filter(step => {
        if (step.kind === 'narration') return step.role !== 'thinking';
        if (step.kind !== 'fold') return true;
        // Old pages can also fold public prose. Suppress a fold only when every item is a thought.
        return !(step.toolCount === 0 && step.thoughtCount > 0 && step.item.fold?.items === step.thoughtCount);
      });
      if (!batch.length) return;
      const firstId = batch[0].id.replace(/^fold:/, '');
      const workKey = sections++ === 0 ? work.workKey : work.workKey + ':' + firstId;
      let toolCount = 0, thoughtCount = 0, unresolved = 0;
      let sectionTitle = '';
      let lastCall: ToolInspectionStep | FoldInspectionStep | undefined;
      let lastPending: ToolInspectionStep | undefined;
      for (const step of batch) {
        if (step.kind === 'tool') {
          sectionTitle = recordedPhase(step) || sectionTitle;
          lastCall = step;
          toolCount++;
          if (!step.result) { unresolved++; lastPending = step; }
        }
        else if (step.kind === 'fold') {
          if (step.toolCount) lastCall = step;
          toolCount += step.toolCount; thoughtCount += step.thoughtCount;
        }
      }
      phaseTitle = sectionTitle || phaseTitle;
      const currentCall = running && lastPending ? lastPending : lastCall;
      if (claude && !sectionTitle && currentCall) {
        if (currentCall.kind === 'fold') sectionTitle = commandCaption(currentCall.item.text);
        else if (currentCall.use.tool_name === 'Bash') {
          sectionTitle = commandCaption(transcriptRecord(currentCall.use.tool_input ?? currentCall.use.input)?.description);
        }
      }
      rows.push({ kind: 'work', id: workKey, work: { ...work, workKey, latestExchange, agent: claude ? 'claude' : agent, phaseTitle, sectionTitle,
        inspectionSteps: batch, toolCount, thoughtCount, unresolvedToolCount: unresolved } });
    };
    const flush = (): void => {
      if (!section.length) return;
      if (running) emit(section);
      else for (const batch of completedWorkSections(section, claude)) emit(batch);
      section = []; family = '';
    };
    for (const item of items) {
      const step = steps.get(item.id);
      if (item.kind === 'text' && item.role === 'assistant' && !isEmptyText(item)) {
        flush();
        phaseTitle = '';
        if (item.id === final) {
          reply = { kind:'reply', id:'visible-' + first.id, item }; rows.push(reply);
        } else rows.push({ kind:'narration', id:item.id, item });
      } else if (inputBlock(item)) {
        flush(); phaseTitle = ''; rows.push({ kind:item.kind === 'image' ? 'image' : 'input', id:item.id, item });
      } else if (events.has(item.id) || (step?.kind === 'narration' && !!item.compact)) {
        flush();
        if (item.kind === 'turn_duration' && reply && !reply.duration) reply.duration = item;
        else rows.push({ kind:item.kind === 'image' ? 'image' : 'event', id:item.id, item });
      } else if (step) {
        if (proposal && step === wrap) continue;
        if (step.kind === 'tool') {
          const name = String(step.use.tool_name ?? step.use.tool ?? step.use.name ?? '').toLowerCase().replace(/^functions\./, '');
          const next = ['todowrite', 'update_plan', 'mcp__ccd_turn__update_plan', 'taskcreate', 'taskupdate', 'tasklist'].includes(name) ? 'plan' :
            isImageInspection(step) ? 'image' : 'activity';
          if (family && family !== next) flush();
          family = next;
        }
        section.push(step);
      }
    }
    flush();
    if (reply && !running) {
      const replyAt = rows.lastIndexOf(reply);
      // Without a duration, older providers can leave interim prose followed by more work.
      const finished = !!reply.duration || (!work.unresolvedToolCount && (!latestExchange || !liveTools.length) &&
        !rows.slice(replyAt + 1).some(row => row.kind === 'work' || row.kind === 'event'));
      if (finished) {
        const process = rows.slice(start, replyAt).filter(row => row.kind === 'work' || row.kind === 'narration' ||
          (row.kind === 'event' && (row.item.kind === 'browser_complete' || !!row.item.compact ||
            (!!row.item.task && !row.item.wake && transcriptRecord(row.item.task)?.status === 'completed'))));
        const opening = codexOpening(reply.duration);
        const missingOpening = !!opening && !items.some(item => inputBlock(item) && entryId(item.id) === opening);
        if (process.length || (index === 0 && missingOpening)) {
          const leads = new Set<string>();
          if (opening) {
            let publicLead = '';
            for (const row of rows.slice(start, replyAt)) {
              if (row.kind === 'input') {
                if (publicLead) leads.add(publicLead);
                publicLead = '';
              } else if (row.kind === 'narration' && !publicLead) publicLead = row.id;
            }
          }
          if (leads.size) reply.leadIds = Array.from(leads);
          reply.processIds = process.filter(row => !leads.has(row.id)).map(row => row.id);
          reply.partialProcess = index === 0 && (!inputBlock(first) || missingOpening) && !first.wake && first.kind !== 'recap';
        }
      }
    }
    if (proposal && wrap) rows.push({kind:'next_step',id:'next-step:' + wrap.id,item:wrap.use,proposal});
    if (!sections && running && !items.some(item => item.kind === 'thinking' || item.kind === 'fold')) {
      const position = reply ? rows.indexOf(reply) : rows.length;
      rows.splice(position, 0, {kind:'work', id:work.workKey, work:{...work,latestExchange,inspectionSteps:[]}});
    }
  });
  return rows;
}

export type OperationReceiptState = "pending" | "done" | "failed" | "unknown";

export interface PendingOperationInput {
  readonly operationId: string;
  readonly credentialId: string;
  readonly scope: ScopeKey;
  readonly kind: string;
  readonly target: JsonObject;
  readonly payload: JsonObject;
  readonly createdAt: string;
}

export interface PendingOperation {
  readonly operationId: string;
  readonly credentialId: string;
  readonly scope: ScopeKey;
  readonly kind: string;
  readonly target: JsonObject;
  readonly payload: JsonObject;
  readonly createdAt: string;
}

function freezeJson(value: JsonValue): JsonValue {
  const copied = cloneJson(value);
  if (Array.isArray(copied)) {
    return Object.freeze(copied.map((entry) => freezeJson(entry)));
  }
  if (typeof copied === "object" && copied !== null) {
    const frozen: { [key: string]: JsonValue | undefined } = {};
    for (const key of Object.keys(copied)) {
      const child = copied[key];
      if (child !== undefined) frozen[key] = freezeJson(child);
    }
    return Object.freeze(frozen);
  }
  return copied;
}

function freezeJsonObject(value: JsonObject): JsonObject {
  return freezeJson(value) as JsonObject;
}

/** Copies and freezes the exact target and payload that were persisted before sending. */
export function createPendingOperation(input: PendingOperationInput): PendingOperation {
  if (input.operationId.trim().length === 0) throw new Error("Operation id is required");
  if (input.credentialId.trim().length === 0) throw new Error("Credential id is required");
  if (input.kind.trim().length === 0) throw new Error("Operation kind is required");
  const scope = Object.freeze({ groupId: input.scope.groupId, managerId: input.scope.managerId });
  return Object.freeze({
    operationId: input.operationId,
    credentialId: input.credentialId,
    scope,
    kind: input.kind,
    target: freezeJsonObject(input.target),
    payload: freezeJsonObject(input.payload),
    createdAt: input.createdAt,
  });
}

export type ReceiptGuidance = "wait" | "accept_result" | "show_failure" | "query_or_check_by_hand";

export function receiptGuidance(state: OperationReceiptState): ReceiptGuidance {
  switch (state) {
    case "pending": return "wait";
    case "done": return "accept_result";
    case "failed": return "show_failure";
    case "unknown": return "query_or_check_by_hand";
  }
}

/** A transport retry is never automatic for an operation receipt. */
export function mayAutomaticallyRetryOperation(_state: OperationReceiptState): boolean {
  return false;
}

/** Only an explicit user confirmation after a failed receipt or manual outcome check permits a new op. */
export function mayStartReplacementOperation(state: OperationReceiptState, userConfirmedNoEffect: boolean): boolean {
  return userConfirmedNoEffect && (state === "failed" || state === "unknown");
}

export type ResolveOutcome = "success" | "gone" | "prompt_changed" | "error";

export interface InteractionResolveReply {
  readonly outcome: ResolveOutcome;
  readonly already?: boolean;
  readonly resolution?: JsonObject | null;
}

export interface InteractionResolveInput {
  readonly apiAvailable: boolean;
  readonly networkUp: boolean;
  readonly requestedResolution: JsonObject;
  readonly reply: InteractionResolveReply;
}

export interface InteractionResolveDecision {
  readonly allowed: boolean;
  /** The exact body attempted, or null when no request was sent. */
  readonly requestResolution: JsonObject | null;
  /** For an `already` reply this is the hub's answer, never the local attempted answer. */
  readonly effectiveResolution: JsonObject | null;
  readonly alreadyResolved: boolean;
  readonly cardPresent: boolean;
  readonly refreshPendingCards: boolean;
}

/** Matches current optimistic card behavior while preserving an authoritative already-result. */
export function resolveInteractionResult(input: InteractionResolveInput): InteractionResolveDecision {
  if (!input.apiAvailable || !input.networkUp) {
    return {
      allowed: false,
      requestResolution: null,
      effectiveResolution: null,
      alreadyResolved: false,
      cardPresent: true,
      refreshPendingCards: false,
    };
  }

  const attempted = freezeJsonObject(input.requestedResolution);
  if (input.reply.outcome === "error") {
    return {
      allowed: true,
      requestResolution: attempted,
      effectiveResolution: null,
      alreadyResolved: false,
      cardPresent: true,
      refreshPendingCards: true,
    };
  }

  if (input.reply.outcome === "gone" || input.reply.outcome === "prompt_changed") {
    return {
      allowed: true,
      requestResolution: attempted,
      effectiveResolution: null,
      alreadyResolved: false,
      cardPresent: false,
      refreshPendingCards: true,
    };
  }

  const alreadyResolved = input.reply.already === true;
  return {
    allowed: true,
    requestResolution: attempted,
    effectiveResolution: alreadyResolved && input.reply.resolution != null
      ? freezeJsonObject(input.reply.resolution)
      : alreadyResolved ? null : attempted,
    alreadyResolved,
    cardPresent: false,
    refreshPendingCards: alreadyResolved && input.reply.resolution == null,
  };
}

export interface ComposerDraft {
  readonly scope: ScopeKey;
  readonly targetKind: string;
  readonly targetId: string;
  readonly text: string;
  readonly attachmentRefs: readonly string[];
  readonly caret: number;
  readonly updatedAt: string;
}

export interface ContextReference {
  readonly id: string;
  readonly title: string;
  readonly kind?: string;
  readonly url?: string;
  readonly body?: string;
}

export interface DraftContextState {
  readonly draft: ComposerDraft;
  readonly contexts: readonly ContextReference[];
}

/** Adds context references independently while preserving the existing draft byte-for-byte. */
export function mergeDraftContext(
  draft: ComposerDraft,
  existing: readonly ContextReference[],
  additions: readonly ContextReference[],
): DraftContextState {
  const contexts = new Map<string, ContextReference>();
  for (const ref of existing) if (!contexts.has(ref.id)) contexts.set(ref.id, ref);
  for (const ref of additions) if (!contexts.has(ref.id)) contexts.set(ref.id, ref);
  const copiedDraft: ComposerDraft = Object.freeze({
    scope: Object.freeze({ groupId: draft.scope.groupId, managerId: draft.scope.managerId }),
    targetKind: draft.targetKind,
    targetId: draft.targetId,
    text: draft.text,
    attachmentRefs: Object.freeze([...draft.attachmentRefs]),
    caret: draft.caret,
    updatedAt: draft.updatedAt,
  });
  const copiedContexts = [...contexts.values()].map((ref) => Object.freeze({ ...ref }));
  return { draft: copiedDraft, contexts: Object.freeze(copiedContexts) };
}

export interface RouteDescriptor {
  readonly groupId: string;
  readonly kind: string;
  readonly machineId?: string;
  readonly sessionId?: string;
  readonly conversationId?: string;
  readonly requestId?: string;
  readonly source?: string;
}

interface ScopedNavigationState {
  selectedTab: string | null;
  readonly stacks: Map<string, RouteDescriptor[]>;
}

/** Per-manager, per-tab route stacks with one selected tab stored for each scope. */
export class ScopedTabRoutes {
  private readonly scopes = new Map<string, ScopedNavigationState>();

  selectedTab(scope: ScopeKey, fallback: string): string {
    return this.scopes.get(scopeKey(scope))?.selectedTab ?? fallback;
  }

  selectTab(scope: ScopeKey, tab: string): void {
    this.state(scope).selectedTab = tab;
  }

  stack(scope: ScopeKey, tab: string): readonly RouteDescriptor[] {
    const routes = this.scopes.get(scopeKey(scope))?.stacks.get(tab) ?? [];
    return routes.map((route) => ({ ...route }));
  }

  setStack(scope: ScopeKey, tab: string, routes: readonly RouteDescriptor[]): void {
    this.state(scope).stacks.set(tab, routes.map((route) => Object.freeze({ ...route })));
  }

  clearScope(scope: ScopeKey): void {
    this.scopes.delete(scopeKey(scope));
  }

  private state(scope: ScopeKey): ScopedNavigationState {
    const key = scopeKey(scope);
    const existing = this.scopes.get(key);
    if (existing !== undefined) return existing;
    const created: ScopedNavigationState = { selectedTab: null, stacks: new Map<string, RouteDescriptor[]>() };
    this.scopes.set(key, created);
    return created;
  }
}
