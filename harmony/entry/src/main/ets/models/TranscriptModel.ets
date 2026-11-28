import { decisionSourceItem } from './InteractionHistory';
import type { LiveToolCall } from '../core/Domain';
import type { TranscriptItem, TranscriptBatch, TranscriptAction, TranscriptSnapshot } from '../core/Domain';
import { TranscriptReducer, TranscriptSync, transcriptExchanges } from '../core/Domain';
import { ConversationPlanReplay, samePlanSummary } from '../core/ConversationPlan';
import type { ConversationPlan } from '../core/ConversationPlan';
import { HubRepository, segment } from './HubRepository';
import { ResourceRow, Wire } from './Wire';
import { LocalStore } from '../platform/LocalStore';
import { boundedTranscriptCache } from '../core/TranscriptCache';

// One full provider snapshot. The Hub transport's own connection state is tracked separately.
class ToolActivityFrame {
  calls: LiveToolCall[] = [];
  summary: string = '';
  thinking: boolean = false;
  active: boolean = false;
  retryCount: string = '';
  compacting: boolean = false;

  static read(data: Record<string, Object>): ToolActivityFrame {
    const frame = new ToolActivityFrame(), seen = new Set<string>();
    for (const record of Wire.rows(data, 'tools')) {
      const id = Wire.text(record, 'id'), name = Wire.text(record, 'name');
      if (id && name && !seen.has(id)) {
        seen.add(id); frame.calls.push({id, name});
      }
    }
    frame.summary = typeof data['summary'] === 'string' ? Wire.text(data, 'summary').trim() : '';
    frame.thinking = data['thinking'] === true; frame.active = data['active'] === true; frame.compacting = data['compacting'] === true;
    const attempt = data['retry_attempt'], max = data['retry_max'];
    if (typeof attempt === 'number' && typeof max === 'number' && Number.isSafeInteger(attempt) &&
        Number.isSafeInteger(max) && attempt > 0 && max >= attempt) frame.retryCount = String(attempt) + '/' + String(max);
    return frame;
  }
  hasActivity(): boolean { return !!(this.calls.length || this.summary || this.thinking || this.active || this.retryCount || this.compacting); }
  same(other: ToolActivityFrame): boolean {
    return this.summary === other.summary && this.thinking === other.thinking && this.active === other.active &&
      this.retryCount === other.retryCount && this.compacting === other.compacting && this.calls.length === other.calls.length &&
      this.calls.every((tool, i) => tool.id === other.calls[i].id && tool.name === other.calls[i].name);
  }
}

interface OlderPage {
  owner: TranscriptSync;
  before: number;
  request: Promise<Record<string, Object>>;
}

// A visible conversation owns its subscription, paging and continuity state.
export class TranscriptModel {
  decisionSource(toolUseId: string): string { return decisionSourceItem(this.reducer.items, toolUseId); }
  private reducer: TranscriptReducer = new TranscriptReducer();
  private sync: TranscriptSync = new TranscriptSync(this.reducer);
  private active: boolean = true;
  private historyReady: boolean = false;
  private fillRunning: boolean = false;
  private bufferedOlder?: OlderPage;
  private warmedBefore: number = -1;
  private mergingOlder?: Promise<void>;
  private mergingOwner?: TranscriptSync;
  private refreshing: boolean = false;
  private refreshAgain: boolean = false;
  private fillRequested: boolean = false;
  private pendingFetches: Promise<void> = Promise.resolve();
  private readonly liveSubscription: boolean;
  private historyReaders: number = 0;
  private pendingItems: boolean = false;
  private pendingTools: boolean = false;
  private pendingPreview: boolean = false;
  private publishedVersion: number = -1;
  private cachedStamp: string = '';
  get readingHistory(): boolean { return this.historyReaders > 0; }
  private publishedItems: readonly TranscriptItem[] = [];
  private liveText: string = '';
  private toolActivity: ToolActivityFrame = new ToolActivityFrame();
  private paneThinking: boolean = false;
  private paneCompacting: boolean = false;
  get historyKey(): string { return this.reducer.history || ''; }
  get liveTools(): readonly LiveToolCall[] { return this.toolActivity.calls; }
  get liveSummary(): string { return this.toolActivity.summary; }
  get liveThinking(): boolean { return this.toolActivity.thinking || this.paneThinking; }
  get liveCompacting(): boolean { return this.paneCompacting || this.toolActivity.compacting; }
  get liveActive(): boolean { return this.toolActivity.active; }
  get liveRetryCount(): string { return this.toolActivity.retryCount; }
  private readonly toolsChanged: () => void;
  private liveThrough: number = 0;
  private liveEndTimer: number = -1;
  private readonly row: ResourceRow;
  private readonly repository: HubRepository;
  private readonly local: LocalStore;
  private readonly key: string;
  private readonly publish: (items: readonly TranscriptItem[], hasMore: boolean, historyReady: boolean) => void;
  private readonly failure: (error: Error) => void;
  private readonly preview: (text: string) => void;

  constructor(row: ResourceRow, repository: HubRepository, local: LocalStore, key: string,
    publish: (items: readonly TranscriptItem[], hasMore: boolean, historyReady: boolean) => void, failure: (error: Error) => void,
    preview: (text: string) => void = (_text: string): void => {}, toolsChanged: () => void = (): void => {}, liveSubscription: boolean = true) {
    this.row = row; this.repository = repository; this.local = local; this.key = key; this.publish = publish; this.failure = failure; this.preview = preview; this.toolsChanged = toolsChanged;
    this.liveSubscription = liveSubscription;
  }
  // A historical viewport can finish a gesture without rebuilding its live tail.
  // The reducer continues accepting every event. Explicit history reads still publish immediately.
  holdHistoryRendering(): () => void {
    this.historyReaders++;
    let released = false;
    return (): void => {
      if (released) return;
      released = true; this.historyReaders--;
      if (!this.active || this.historyReaders > 0) return;
      const items = this.pendingItems, tools = this.pendingTools, preview = this.pendingPreview;
      this.pendingItems = false; this.pendingTools = false; this.pendingPreview = false;
      if (preview) this.preview(this.liveText);
      if (items) this.emit();
      else if (tools) this.toolsChanged();
    };
  }
  private notifyPreview(): void {
    if (this.historyReaders > 0) this.pendingPreview = true;
    else this.preview(this.liveText);
  }
  private replaceLiveTools(frame: ToolActivityFrame, paneThinking: boolean, paneCompacting: boolean = false): void {
    if (paneThinking === this.paneThinking && paneCompacting === this.paneCompacting && frame.same(this.toolActivity)) return;
    this.toolActivity = frame; this.paneThinking = paneThinking; this.paneCompacting = paneCompacting;
    if (this.historyReaders > 0) this.pendingTools = true;
    else this.toolsChanged();
  }
  clearLive(): void {
    this.clearPreview();
    this.replaceLiveTools(new ToolActivityFrame(), false);
  }
  clearPreview(): void {
    if (this.liveEndTimer !== -1) clearTimeout(this.liveEndTimer);
    this.liveEndTimer = -1;
    if (this.liveText) { this.liveText = ''; this.notifyPreview(); }
  }
  private receivePreview(text: string): void {
    if (!text) {
      // The empty frame can precede the saved message. Keep its words on screen through that handoff.
      if (this.liveText && this.liveEndTimer === -1) this.liveEndTimer = setTimeout(() => this.clearPreview(), 2500);
      return;
    }
    if (this.liveEndTimer !== -1) clearTimeout(this.liveEndTimer);
    this.liveEndTimer = -1;
    if (text === this.liveText) return;
    this.liveThrough = this.reducer.covered; this.liveText = text;
    if (this.previewLanded()) this.clearPreview();
    else this.notifyPreview();
  }
  private previewLanded(): boolean {
    const items = this.publishedItems;
    const text = this.liveText.trim();
    let passedEnd = false;
    for (let i = items.length - 1; i >= 0; i--) {
      const item = items[i];
      if (item.kind === 'turn_duration') {
        if (item.seq > this.liveThrough) return true;
        if (passedEnd) return false;
        passedEnd = true; continue;
      }
      if (item.wake || (item.role === 'user' && item.kind === 'text' && !item.queued)) {
        return item.seq > this.liveThrough;
      }
      if (item.role !== 'assistant' || item.kind !== 'text') continue;
      const saved = (item.text || '').trim();
      if (!saved) continue;
      if (saved.startsWith(text)) return true;
      const cut = saved.lastIndexOf('\n…');
      if (item.full_size && cut > 0 && text.startsWith(saved.slice(0, cut))) return true;
      return this.row.kind === 'conversation' && item.seq > this.liveThrough;
    }
    return false;
  }
  private path(query: string): string {
    return '/api/v1/sessions/' + segment(this.row.machine) + '/' + segment(this.row.session || this.row.id) + '/items?' + query;
  }
  private observeHistory(history?: string | null): boolean {
    if (!history || history === this.reducer.history) return false;
    if (this.reducer.history) this.clearLive();
    this.publishedVersion = -1; this.cachedStamp = ''; this.publishedItems = []; this.bufferedOlder = undefined; this.warmedBefore = -1;
    // Cursors, in-flight fills and open folds belong to one version of the file.
    this.sync = new TranscriptSync(this.reducer);
    this.fillRunning = false;
    this.fillRequested = false;
    this.historyReady = false;
    return true;
  }
  private currentRead(owner: TranscriptSync): boolean { return this.active && owner === this.sync; }
  private cursorWasReplaced(batch: TranscriptBatch): boolean {
    if (!this.observeHistory(batch.history)) return false;
    // The cursor named a position in the old file. Clear its cache before attempting the replacement read.
    this.reducer.restore({ items: [], history: batch.history ?? null, covered: 0, hasMore: null, loadedOnce: false });
    this.emit();
    return true;
  }
  private batch(data: Record<string, Object>): TranscriptBatch {
    // Presence matters: an older server omitted sequencing and has_more rather than returning zero/false.
    const canonical: Record<string, Object> = {};
    canonical['items'] = Wire.rows(data, 'items');
    if (Wire.has(data, 'history')) canonical['history'] = Wire.text(data, 'history');
    if (Wire.has(data, 'from_seq')) canonical['fromSeq'] = Wire.number(data, 'from_seq');
    if (Wire.has(data, 'to_seq')) canonical['toSeq'] = Wire.number(data, 'to_seq');
    if (Wire.has(data, 'backlog')) canonical['backlog'] = Wire.bool(data, 'backlog');
    if (Wire.has(data, 'has_more')) canonical['hasMore'] = Wire.bool(data, 'has_more');
    if (Wire.has(data, 'more_after')) canonical['moreAfter'] = Wire.bool(data, 'more_after');
    return JSON.parse(JSON.stringify(canonical)) as TranscriptBatch;
  }
  async open(): Promise<void> {
    const owner = this.sync;
    const cached = await this.local.get('transcript:' + this.key);
    if (!this.active) return;
    if (cached && this.currentRead(owner)) {
      try {
        const snapshot = JSON.parse(cached) as TranscriptSnapshot;
        this.observeHistory(snapshot.history);
        this.reducer.restore(snapshot);
        this.emit();
      } catch (_) {}
    }
    if (this.liveSubscription) await this.drive(this.sync.reconnect());
    await this.latest();
  }
  async latest(): Promise<void> {
    if (!this.active) return;
    let release: () => void = () => {};
    this.pendingFetches = new Promise<void>(resolve => { release = resolve; });
    if (this.refreshing) { this.refreshAgain = true; release(); return; }
    this.refreshing = true;
    let owner = this.sync;
    try {
      // Keep commentary in chronological order. A legacy work fold can contain several public paragraphs.
      const data = await this.repository.call(this.path('limit=80&lazy_images=1'));
      if (!this.active) return;
      // An unscoped latest page can still be current if a push established the same history in the meantime.
      if (owner !== this.sync && Wire.text(data, 'history') !== this.reducer.history) { this.refreshAgain = true; return; }
      const batch = this.batch(data);
      this.observeHistory(batch.history);
      owner = this.sync;
      await this.drive(owner.settle(this.reducer.latest(batch)));
      if (this.currentRead(owner)) { this.historyReady = Wire.has(data, 'has_more'); this.emit(); }
    } catch (error) {
      if (this.currentRead(owner)) this.failure(error as Error);
      else this.refreshAgain = this.active;
    } finally {
      this.refreshing = false;
      // A push may overtake this latest page. Resume its fill through the normal paged reader.
      if (this.currentRead(owner) && !this.fillRunning) {
        owner.finishFill();
        if (owner.behind || this.fillRequested || this.reducer.items.some((item) => item.seq > this.reducer.covered)) {
          this.fillRequested = false;
          this.drive(owner.continueFill()).catch((error: Error) => { if (this.currentRead(owner)) this.failure(error); });
        }
      }
      if (this.refreshAgain && this.active) { this.refreshAgain = false; this.latest(); }
      release();
    }
  }
  receive(data: Record<string, Object>): void {
    if (!this.active || Wire.text(data, 'machine_id') !== this.row.machine || Wire.text(data, 'session_id') !== (this.row.session || this.row.id)) return;
    if (Wire.text(data, 'type') === 'live_tools') {
      if (!Array.isArray(data['tools'])) return;
      const frame = ToolActivityFrame.read(data);
      this.replaceLiveTools(frame, this.paneThinking && frame.hasActivity(), this.paneCompacting && frame.hasActivity());
      return;
    }
    if (Wire.text(data, 'type') === 'live') {
      const pane = Wire.object(data, 'live');
      this.replaceLiveTools(this.toolActivity, pane['thinking'] === true, /^compact/i.test(Wire.text(pane, 'verb')));
      return;
    }
    if (Wire.text(data, 'type') === 'delta') {
      if (typeof data['text'] === 'string') this.receivePreview(Wire.text(data, 'text'));
      return;
    }
    const batch = this.batch(data), previouslyCovered = this.reducer.covered;
    const rewritten = this.observeHistory(batch.history);
    if (rewritten) this.historyReady = batch.fromSeq === 0 || (batch.backlog === true && batch.hasMore !== undefined);
    if (Wire.text(data, 'type') === 'items_replace') {
      this.bufferedOlder = undefined; this.warmedBefore = -1;
      this.sync.reload(batch.items, batch.history || null, batch.toSeq || null, batch.hasMore === true);
    }
    else {
      const gap = this.reducer.apply(batch);
      if (gap === 'fill') this.fillRequested = true;
      this.drive(this.sync.settle(gap)).catch((error: Error) => this.failure(error));
    }
    if ((this.toolActivity.hasActivity() || this.paneThinking || this.paneCompacting) && !batch.backlog &&
        batch.items.some(item => item.kind === 'turn_duration' && item.seq > previouslyCovered)) {
      const turns = transcriptExchanges(this.reducer.items);
      const tail = turns[turns.length - 1];
      if (tail?.some(item => item.kind === 'turn_duration' && item.seq > previouslyCovered)) {
        this.replaceLiveTools(new ToolActivityFrame(), false);
      }
    }
    if (this.historyReaders > 0 && !rewritten && Wire.text(data, 'type') !== 'items_replace') this.pendingItems = true;
    else this.emit();
  }
  /** Recover a summary-only plan without unfolding work or moving the chat's history cursor. */
  async readPlanDetails(wanted: ConversationPlan, stillOpen: () => boolean): Promise<ConversationPlan | undefined> {
    const owner = this.sync, replay = new ConversationPlanReplay();
    const current = (): boolean => this.currentRead(owner) && stillOpen();
    let history = this.reducer.history, cursor = 0;
    while (current()) {
      const data = await this.repository.call(this.path('after_seq=' + cursor + '&lazy_images=1'));
      if (!current()) return undefined;
      const batch = this.batch(data);
      if (history && batch.history && history !== batch.history) return undefined;
      if (batch.history) history = batch.history;
      if (typeof batch.fromSeq === 'number' && batch.fromSeq !== cursor) return undefined;
      replay.accept(batch.items.filter((item: TranscriptItem) => item.seq > cursor));
      if (!batch.moreAfter) {
        if (batch.hasMore) return undefined;
        const plan = replay.view();
        if (!plan?.complete) return undefined;
        // An aggregate can reach the phone before its final items. Never label a stale list as complete.
        if (!wanted.sourceId && !samePlanSummary(wanted.done, wanted.total, wanted.current, plan.steps)) return undefined;
        return plan;
      }
      const last = batch.items[batch.items.length - 1];
      const next = batch.toSeq ?? last?.seq;
      if (typeof next !== 'number' || next <= cursor) return undefined;
      cursor = next;
    }
    return undefined;
  }
  private olderPage(before: number): OlderPage {
    if (this.bufferedOlder?.owner === this.sync && this.bufferedOlder.before === before) return this.bufferedOlder;
    const page: OlderPage = { owner:this.sync, before,
      request:this.repository.call(this.path('before_seq=' + before + '&turns=3&limit=160&fold_work=1&lazy_images=1')) };
    this.bufferedOlder = page;
    page.request.catch(() => { if (this.bufferedOlder === page) this.bufferedOlder = undefined; });
    return page;
  }
  prefetchOlder(before: number): void {
    if (!this.active || before <= 0 || this.warmedBefore === before || this.reducer.hasMore !== true) return;
    this.warmedBefore = before; this.olderPage(before);
  }
  async older(): Promise<void> {
    const owner = this.sync;
    if (this.mergingOlder && this.mergingOwner === owner) return this.mergingOlder;
    const operation = this.drive(owner.loadOlder());
    this.mergingOlder = operation; this.mergingOwner = owner;
    try { await operation; }
    finally { if (this.mergingOlder === operation) { this.mergingOlder = undefined; this.mergingOwner = undefined; } }
  }
  async unfold(id: string): Promise<void> { await this.drive(this.sync.openFold(id)); }
  private async drive(actions: readonly TranscriptAction[]): Promise<void> {
    const owner = this.sync;
    for (const action of actions) {
      if (!this.currentRead(owner)) return;
      if (action.kind === 'fetch_after') {
        if (this.fillRunning) continue;
        this.fillRunning = true;
        try {
          let next: number | undefined = action.afterSeq;
          while (next !== undefined && this.currentRead(owner)) {
            const data = await this.repository.call(this.path('after_seq=' + next + '&lazy_images=1'));
            if (!this.currentRead(owner)) return;
            const batch = this.batch(data);
            if (this.cursorWasReplaced(batch)) { await this.latest(); return; }
            const result = owner.after(batch);
            this.emit();
            await this.drive(result.actions.filter((candidate) => candidate.kind !== 'fetch_after'));
            const following = result.actions.find((candidate) => candidate.kind === 'fetch_after');
            next = following?.kind === 'fetch_after' ? following.afterSeq : undefined;
          }
        } catch (error) {
          if (!this.currentRead(owner)) return;
          owner.finishFill();
          if (error instanceof Error) throw error;
          throw new Error('failed');
        } finally { if (this.currentRead(owner)) this.fillRunning = false; }
      } else if (action.kind === 'subscribe') {
        if (!this.liveSubscription) continue;
        const payload: Record<string, Object> = Wire.payload(['type', 'machine_id', 'session_id'], ['subscribe', this.row.machine, this.row.session || this.row.id]);
        if (action.sinceSeq !== undefined) payload['since_seq'] = action.sinceSeq;
        this.repository.transport.send(JSON.stringify(payload));
      } else if (action.kind === 'fetch_latest') {
        await this.pendingFetches;
        if (this.currentRead(owner)) await this.latest();
      } else if (action.kind === 'fetch_older') {
        try {
          const page = this.olderPage(action.beforeSeq);
          const data = await page.request;
          if (!this.currentRead(owner)) return;
          if (this.bufferedOlder !== page || this.reducer.firstSeq !== action.beforeSeq) { owner.finishOlder(); return; }
          this.bufferedOlder = undefined;
          const batch = this.batch(data);
          if (this.cursorWasReplaced(batch)) { await this.latest(); return; }
          owner.older(batch.items, batch.hasMore === true); this.historyReady = Wire.has(data, 'has_more'); this.emit();
        } catch (error) {
          if (!this.currentRead(owner)) return;
          owner.finishOlder();
          throw error instanceof Error ? error : new Error('failed');
        }
      } else if (action.kind === 'fetch_range') {
        try {
          const data = await this.repository.call(this.path('from_seq=' + action.fromSeq + '&through_seq=' + action.throughSeq + '&lazy_images=1'));
          if (!this.currentRead(owner)) return;
          const batch = this.batch(data); await this.drive(owner.range(batch.items, Wire.bool(data, 'more'))); this.emit();
        } catch (error) {
          if (!this.currentRead(owner)) return;
          owner.cancelFold();
          throw error instanceof Error ? error : new Error('failed');
        }
      }
    }
  }
  private emit(): void {
    if (!this.active) return;
    this.pendingItems = false;
    if (this.publishedVersion !== this.reducer.contentVersion) {
      this.publishedItems = this.reducer.items;
      this.publishedVersion = this.reducer.contentVersion;
    }
    const snapshot: TranscriptSnapshot = {items:this.publishedItems, history:this.reducer.history, covered:this.reducer.covered,
      hasMore:this.reducer.hasMore, loadedOnce:this.reducer.loadedOnce};
    const stamp = JSON.stringify([this.publishedVersion,snapshot.history,snapshot.covered,snapshot.hasMore,snapshot.loadedOnce]);
    if (this.liveSubscription && stamp !== this.cachedStamp) {
      this.cachedStamp = stamp;
      this.local.put('transcript:' + this.key, boundedTranscriptCache(snapshot)).catch((error: Error) => {
        if (this.cachedStamp === stamp) this.cachedStamp = '';
        if (this.active) this.failure(error);
      });
    }
    if (this.liveText && this.previewLanded()) this.clearPreview();
    this.publish(snapshot.items, snapshot.hasMore === true, this.historyReady);
  }
  close(): void {
    if (!this.active) return;
    if (this.liveSubscription && this.pendingItems) this.local.put('transcript:' + this.key, boundedTranscriptCache(this.reducer.snapshot())).catch(() => {});
    this.active = false; this.clearLive(); this.publishedItems = []; this.bufferedOlder = undefined;
    if (!this.liveSubscription) return;
    try { this.repository.transport.send(JSON.stringify(Wire.payload(['type', 'machine_id', 'session_id'], ['unsubscribe', this.row.machine, this.row.session || this.row.id]))); } catch (_) {}
  }
}
