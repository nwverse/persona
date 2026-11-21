import { util } from '@kit.ArkTS';
import type { ClientModel, ClientViewLease } from './ClientModel';
import type { MutationRecord } from './MutationStore';
import { ProjectWorktree } from './ProjectCatalog';
import { ResourceRow, Wire } from './Wire';
import { HttpFailure } from '../platform/HubTransport';

export class WorktreeChange {
  action: string = '';
  id: string = '';
  path: string = '';
  branch: string = '';
  parent: string = '';
  branchRetained: boolean = false;
}

export function checkoutName(branch: string): string {
  return branch.trim().toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
}
export function checkoutPath(base: string, branch: string): string {
  const leaf = checkoutName(branch);
  if (!leaf || !base) return '';
  return base.replace(/[\\/]+$/, '') + (base.includes('\\') && !base.includes('/') ? '\\' : '/') + leaf;
}
export function validWorktreeBranch(input: string): boolean {
  const name = input.trim();
  return !!name && name.length <= 255 && name !== '@' && !name.startsWith('-') && !name.endsWith('.') &&
    !/[\s~^:?*\[\\\x00-\x1f\x7f]/.test(name) && !name.includes('..') && !name.includes('@{') &&
    name.split('/').every((part: string) => !!part && !part.startsWith('.') && !part.endsWith('.lock'));
}

const OP_UUID = /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i;
const NODE_CREATE_TIMEOUT = 150000;
const NODE_RENAME_TIMEOUT = 30000;
const RECEIPT_RETENTION = 30 * 24 * 60 * 60 * 1000;
const submittingRoutes = new Set<string>();
function checkoutComparable(path: string): string {
  const value = path.trim().replace(/\\/g, '/').replace(/\/+$/, '');
  return /^[a-z]:\//i.test(value) ? value.toLowerCase() : value;
}
function releaseSupportsCreateReceipts(version: string): boolean {
  // v0.1.0 is the first official release containing 88631b0c57 (worktree_create). Unversioned and prerelease builds need a real receipt.
  const match = /^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:\+[a-zA-Z0-9.-]+)?$/.exec(version);
  return !!match && (Number(match[1]) > 0 || Number(match[2]) >= 1);
}
function operationAcknowledged(action: string, sourceId: string, request: Record<string, Object>, value: Record<string, Object>): boolean {
  if (['pending','unknown','failed'].includes(Wire.text(value,'state')) || Wire.text(value,'delivered') === 'unknown') return false;
  if (action === 'rename') return Wire.bool(value,'ok');
  if (action === 'clean') return Wire.text(value,'workspace_id') === sourceId && Wire.bool(value,'removed') && Wire.text(value,'state') === 'removed';
  const id = Wire.text(value,'workspace_id'), path = Wire.text(value,'path'), branch = Wire.text(value,'branch');
  return !!id && !!path && !!branch && (Wire.bool(request,'auto_name') ||
    (checkoutComparable(path) === checkoutComparable(Wire.text(request,'path')) && branch === Wire.text(request,'branch')));
}

@ObservedV2
export class WorktreeActions {
  @Trace action: string = '';
  @Trace loading: boolean = false;
  @Trace busy: boolean = false;
  @Trace ready: boolean = false;
  @Trace unknown: boolean = false;
  @Trace error: string = '';
  @Trace title: string = '';
  @Trace machine: string = '';
  @Trace branch: string = '';
  @Trace name: string = '';
  @Trace path: string = '';
  @Trace baseBranch: string = '';
  @Trace dirty: number = 0;
  @Trace folder: boolean = false;
  @Trace deleteBranch: boolean = false;
  @Trace force: boolean = false;
  @Trace automatic: boolean = false;
  @Trace supportsAutomatic: boolean = false;
  @Trace retrySupported: boolean = false;
  @Trace legacy: boolean = false;
  @Trace releaseReady: boolean = false;
  @Trace renameCurrent: string = '';
  @Trace renameOriginal: string = '';
  private renameSnapshot: string = '';
  private renameReviewedRecord: MutationRecord | undefined;
  private reviewedRenameBase: string = '';
  private base: string = '';
  private pathEdited: boolean = false;
  private token: string = '';
  private requestId: string = '';
  private requestBody: string = '';
  private requestPath: string = '';
  private requestTime: number = 0;
  private originalReceiptRoute: boolean = false;
  private legacyId: string = '';
  private releaseTarget: string = '';
  private view: ClientViewLease | undefined;
  private target: string = '';
  private revision: number = 0;
  private owner: ClientModel | undefined;
  private root: ClientModel | undefined;
  private tree: ProjectWorktree | undefined;
  private ownerEpoch: number = 0;
  private rootEpoch: number = 0;
  private rootScope: string = '';
  private ownerScope: string = '';

  close(): void {
    this.revision++; this.owner = undefined; this.root = undefined; this.tree = undefined;
    this.token = ''; this.ready = false; this.busy = false; this.loading = false; this.unknown = false;
    this.view = undefined; this.requestId = ''; this.requestPath = ''; this.requestBody = ''; this.requestTime = 0;
    this.retrySupported = false; this.originalReceiptRoute = false; this.legacy = false; this.legacyId = ''; this.releaseReady = false;
    this.releaseTarget = ''; this.target = '';
    this.renameCurrent = ''; this.renameOriginal = ''; this.renameSnapshot = ''; this.renameReviewedRecord = undefined; this.reviewedRenameBase = '';
  }
  private current(revision: number): boolean {
    return revision === this.revision && !!this.root && !!this.owner && this.root.owns(this.rootEpoch, this.rootScope) &&
      this.owner.connected && this.owner.owns(this.ownerEpoch, this.ownerScope) && (!this.view || this.view.valid()) &&
      (!this.target || this.target === this.formTarget());
  }
  private staleError(online: boolean = this.root?.connected === true && this.owner?.connected === true): string {
    if (!online) return 'offline';
    return this.action === 'clean' ? 'tree_preview_changed' : 'request_updated';
  }
  private requireCurrent(revision: number): void {
    if (!this.current(revision)) throw new Error(this.staleError());
  }
  private formTarget(): string {
    const tree = this.tree!;
    return JSON.stringify([this.ownerScope, tree.group, tree.manager, tree.machine, tree.source, tree.sourceId, tree.cwd, this.action]);
  }
  private endpoint(action: string): string {
    return '/api/v1/machines/' + encodeURIComponent(this.tree!.machine) + '/worktree/' + action;
  }
  private body(): Record<string, Object> {
    return Wire.payload(['workspace_id', 'kind'], [this.tree!.sourceId, this.tree!.source]);
  }
  private exactRecord(id: string): MutationRecord | undefined {
    const path = this.endpoint(this.action === 'clean' ? 'delete' : this.action);
    return this.owner?.repo().mutations?.blocked().find((record: MutationRecord) => record.id === id && record.path === path && record.method === 'POST');
  }
  private async verifyCreateVersion(): Promise<boolean> {
    const rev = this.revision, owner = this.owner!;
    if (!owner.features.includes('client_ops')) return false;
    const version = owner.verifiedServerVersion('client_ops');
    if (version) return this.current(rev) && releaseSupportsCreateReceipts(version);
    try {
      const health = await owner.repo().call('/api/v1/health');
      return this.current(rev) && Wire.bool(health, 'ok') && !!owner.profile.hubId &&
        Wire.text(health, 'hub_id') === owner.profile.hubId && Wire.strings(health, 'features').includes('client_ops') &&
        releaseSupportsCreateReceipts(Wire.text(health, 'version'));
    } catch (_) { return false; }
  }
  private snapshot(): string {
    const saved = Wire.payload(['target','action','path','id','payload','at','receipts'],
      [this.target, this.action, this.requestPath, this.requestId, this.requestBody, this.requestTime, this.originalReceiptRoute]);
    if (this.action === 'rename') saved['originalName'] = this.title;
    return JSON.stringify(saved);
  }
  private restoreRequest(saved: Record<string, Object>, path: string): boolean {
    const id = Wire.text(saved, 'id'), payload = Wire.text(saved, 'payload'), body = Wire.parse(payload);
    if (!OP_UUID.test(id) || Wire.text(saved, 'target') !== this.target || Wire.text(saved, 'action') !== this.action ||
      Wire.text(saved, 'path') !== path || Wire.text(body, 'workspace_id') !== this.tree!.sourceId || Wire.text(body, 'kind') !== this.tree!.source) return false;
    if (this.action === 'create' && !Wire.bool(body,'auto_name') && (!Wire.text(body,'branch') || !Wire.text(body,'path'))) return false;
    if (this.action === 'rename' && !Wire.text(body,'name') || this.action === 'clean' && !Wire.text(body,'preview_token')) return false;
    this.requestId = id; this.requestPath = path; this.requestBody = payload; this.requestTime = Wire.number(saved,'at');
    this.originalReceiptRoute = Wire.bool(saved,'receipts');
    if (this.action === 'create') {
      this.automatic = Wire.bool(body,'auto_name'); this.supportsAutomatic = this.automatic || this.supportsAutomatic;
      this.branch = Wire.text(body,'branch'); this.path = Wire.text(body,'path'); this.pathEdited = true;
    } else if (this.action === 'rename') this.name = Wire.text(body,'name');
    else { this.token = Wire.text(body,'preview_token'); this.force = Wire.bool(body,'force'); this.deleteBranch = Wire.bool(body,'delete_branch'); }
    this.unknown = true; this.error = 'result_unknown'; return true;
  }
  private async query(action: string, body: Record<string, Object>): Promise<Record<string, Object>> {
    // prepare, preview and reconcile use POST on the wire but never dispatch a change.
    const rev = this.revision, route = this.endpoint(action), owner = this.owner!, payload = JSON.stringify(body);
    return this.read(rev, async (): Promise<Record<string,Object>> => Wire.parse(await owner.repo().transport.request(route, 'POST', payload)));
  }
  private async read(rev: number, fetch: () => Promise<Record<string,Object>>): Promise<Record<string,Object>> {
    this.requireCurrent(rev);
    try { return await fetch(); }
    catch (error) {
      if (!(error instanceof HttpFailure)) throw error as Error;
      if (error.status !== 0 || !error.code.startsWith('transport_failed_')) throw error;
      this.requireCurrent(rev);
      await new Promise<void>((resolve) => setTimeout(resolve, 350));
      this.requireCurrent(rev);
      return await fetch();
    }
  }
  private failure(err: Error): string {
    const code = err instanceof HttpFailure ? err.code : err.message;
    if (err instanceof HttpFailure && err.status === 403) return 'permission_denied';
    if (['permission_denied','request_updated','offline','result_unknown','pending_action','node_outdated'].includes(code)) return code;
    if (code.startsWith('transport_failed_')) return 'connection_failed';
    if (code.includes('dirty')) return 'tree_changed_files';
    if (code.includes('main_checkout') || code.includes('main_workspace')) return 'tree_main_protected';
    if (code.includes('stale') || code.includes('preview_required')) return 'tree_preview_changed';
    return code && code.length <= 180 ? code : 'failed';
  }
  async open(model: ClientModel, tree: ProjectWorktree, action: string): Promise<void> {
    this.close();
    const revision = this.revision, scope = model.scope(), epoch = model.connectionGeneration();
    this.action = action; this.title = tree.title; this.machine = tree.machineTitle;
    this.branch = ''; this.baseBranch = ''; this.name = tree.title; this.path = tree.cwd;
    this.error = ''; this.base = ''; this.token = ''; this.requestId = ''; this.dirty = 0; this.folder = false;
    this.force = false; this.deleteBranch = false; this.supportsAutomatic = false; this.automatic = false; this.pathEdited = false;
    this.loading = true;
    try {
      if (!model.connected || !tree.online) throw new Error('offline');
      if (!tree.permits(action)) throw new Error('permission_denied');
      const target = new ResourceRow(); target.group = tree.group; target.manager = tree.manager;
      const owner = await model.viewFor(target);
      if (this.revision !== revision) return;
      if (!model.owns(epoch, scope)) throw new Error(this.staleError(model.connected));
      this.root = model; this.rootScope = scope; this.rootEpoch = epoch;
      this.owner = owner; this.ownerScope = owner.scope(); this.ownerEpoch = owner.connectionGeneration(); this.tree = tree;
      this.requireCurrent(revision);
      this.target = this.formTarget();
      this.view = owner.captureView('worktree-request:' + this.target);
      const route = this.endpoint(action === 'clean' ? 'delete' : action);
      const snapshot = await this.view.read(), saved = Wire.parse(snapshot);
      this.requireCurrent(revision);
      if (this.restoreRequest(saved, route)) {
        if (action === 'rename') { this.renameSnapshot = snapshot; this.renameOriginal = Wire.text(saved,'originalName'); }
        const verified = action === 'create' && this.originalReceiptRoute && await this.verifyCreateVersion();
        if (this.current(revision)) { this.retrySupported = verified; this.ready = true; }
        return;
      }
      const unresolved = owner.repo().mutations?.blocked().find((item: MutationRecord) => item.path === route && item.method === 'POST');
      if (unresolved) {
        this.unknown = true; this.error = 'pending_action';
        if (action !== 'create' || !OP_UUID.test(unresolved.id)) return;
        this.legacy = true; this.legacyId = unresolved.id; this.error = 'tree_verify_original_checkout';
      }
      if (action === 'create') {
        const loadingVersion = this.verifyCreateVersion();
        const prepared = await this.query('prepare', this.body());
        const supported = await loadingVersion;
        this.requireCurrent(revision);
        this.originalReceiptRoute = supported;
        this.base = Wire.text(prepared, 'path'); this.baseBranch = Wire.text(prepared, 'branch');
        if (!this.base || !Wire.text(prepared, 'root')) throw new Error('tree_repo_unavailable');
        this.path = ''; this.supportsAutomatic = Wire.bool(prepared, 'automatic_name');
      } else if (action === 'clean') {
        const body = this.body(); body['preview'] = true;
        const preview = await this.query('delete', body);
        this.requireCurrent(revision);
        const path = Wire.text(preview, 'path'), dirty = Wire.number(preview, 'dirty', -1);
        if (Wire.text(preview, 'workspace_id') !== tree.sourceId || Wire.text(preview, 'kind') !== tree.source ||
          !path || dirty < 0 || !Wire.text(preview, 'preview_token') || Wire.bool(preview, 'close_only')) throw new Error('tree_preview_changed');
        this.token = Wire.text(preview, 'preview_token'); this.path = path; this.branch = Wire.text(preview, 'branch');
        this.dirty = dirty; this.folder = Wire.bool(preview, 'folder');
      }
      this.ready = true;
    } catch (error) {
      if (this.revision === revision) this.error = this.failure(error as Error);
    } finally { if (this.revision === revision) this.loading = false; }
  }
  setBranch(value: string): void { if (this.busy || this.unknown && !this.legacy) return; this.branch = value; this.releaseReady = false; if (!this.pathEdited) this.path = checkoutPath(this.base, value); }
  setPath(value: string): void { if (this.busy || this.unknown && !this.legacy) return; const suggested = checkoutPath(this.base, this.branch); this.releaseReady = false; this.pathEdited = !!value && value !== suggested; this.path = value || suggested; }
  get canSubmit(): boolean {
    if (!this.ready || this.busy || this.loading || this.unknown) return false;
    if (this.action === 'rename') return !!this.name.trim() && this.name.trim().length <= 120 && this.name.trim() !== this.reviewedRenameBase.trim();
    if (this.action === 'create') return this.supportsAutomatic && this.automatic ||
      validWorktreeBranch(this.branch) && !!this.path.trim();
    return this.action === 'clean' && !!this.token && (!this.dirty || this.force);
  }
  private removed(value: Record<string, Object>): boolean {
    return Wire.text(value, 'workspace_id') === this.tree?.sourceId &&
      Wire.bool(value, 'removed') && Wire.text(value, 'state') === 'removed';
  }
  private acknowledged(value: Record<string, Object>): boolean {
    return operationAcknowledged(this.action,this.tree!.sourceId,Wire.parse(this.requestBody),value);
  }
  private change(value: Record<string, Object>): WorktreeChange {
    const change = new WorktreeChange();
    change.action = this.action; change.id = Wire.text(value, 'workspace_id', this.tree!.sourceId);
    change.path = Wire.text(value, 'path', this.path); change.branch = Wire.text(value, 'branch', this.branch);
    change.parent = Wire.text(value, 'parent_workspace_id') || this.tree!.sourceId;
    change.branchRetained = this.action === 'clean' && this.deleteBranch && !Wire.bool(value, 'branch_deleted');
    return change;
  }
  async submit(): Promise<WorktreeChange | undefined> {
    if (!this.canSubmit) return undefined;
    const rev = this.revision;
    if (!this.current(rev)) { this.error = this.staleError(); this.ready = false; return undefined; }
    const owner = this.owner!, action = this.action, sourceId = this.tree!.sourceId, body = this.body(), view = this.view!;
    const path = this.endpoint(action === 'clean' ? 'delete' : action);
    const lane = JSON.stringify([this.ownerScope,path]);
    if (submittingRoutes.has(lane)) return undefined;
    const other = owner.repo().mutations?.blocked().some((item: MutationRecord) => item.path === path && item.method === 'POST');
    if (other) { this.ready = false; this.error = 'pending_action'; return undefined; }
    submittingRoutes.add(lane);
    this.busy = true; this.error = '';
    let snapshot = '', rejectedBeforeDispatch = false;
    const admit = (): boolean => {
      rejectedBeforeDispatch = !this.current(rev);
      return !rejectedBeforeDispatch;
    };
    try {
      if (action === 'create') {
        if (this.automatic && this.supportsAutomatic) body['auto_name'] = true;
        else { body['branch'] = this.branch.trim(); body['path'] = this.path.trim(); }
      } else if (action === 'rename') {
        if (this.reviewedRenameBase) {
          const live = await this.liveRenameName(rev);
          this.requireCurrent(rev);
          if (live !== this.reviewedRenameBase) {
            this.title = live; this.name = live; this.reviewedRenameBase = live;
            this.error = 'tree_rename_check_again'; return undefined;
          }
        }
        body['name'] = this.name.trim();
      }
      else { body['preview_token'] = this.token; body['force'] = this.force; body['delete_branch'] = !this.folder && this.deleteBranch; }
      this.requestId = util.generateRandomUUID();
      this.requestPath = path; this.requestBody = JSON.stringify(body); this.requestTime = Date.now();
      snapshot = this.snapshot();
      if (action === 'rename') { this.renameSnapshot = snapshot; this.renameOriginal = this.title; }
      await view.write(snapshot);
      if (!admit()) throw new Error('request_updated');
      const result = await owner.repo().call(path, 'POST', body, this.requestId, admit, false,
        (value: Record<string,Object>): boolean => operationAcknowledged(action,sourceId,body,value));
      if (!this.current(rev)) return undefined;
      const change = this.change(result); this.ready = false;
      if (action === 'rename') await view.clearIfUnchanged(snapshot).catch(() => undefined);
      else await view.write('').catch(() => undefined);
      return this.current(rev) ? change : undefined;
    } catch (error) {
      if (rejectedBeforeDispatch && !(error instanceof HttpFailure && error.resultUnknown)) {
        if (this.revision === rev) { this.error = this.staleError(); this.ready = false; }
        // The local admission check declined this request before transport. A replacement
        // sheet or pairing can own the same storage key by the time the write completes.
        await view.clearIfUnchanged(snapshot).catch(() => undefined);
        return undefined;
      }
      if (!this.current(rev)) return undefined;
      this.error = this.failure(error as Error);
      this.unknown = error instanceof HttpFailure && error.resultUnknown;
      if (this.unknown) {
        this.retrySupported = action === 'create' && this.originalReceiptRoute && !!this.exactRecord(this.requestId);
        this.error = error instanceof HttpFailure && error.resultPending ? 'pending' : 'result_unknown';
      } else if (action === 'rename') {
        if (snapshot) await view.clearIfUnchanged(snapshot).catch(() => undefined);
      } else if (view.valid()) await view.write('').catch(() => undefined);
      if (action === 'clean' && !this.unknown) this.ready = false;
      return undefined;
    } finally { submittingRoutes.delete(lane); if (this.revision === rev) this.busy = false; }
  }
  get requestCheckAvailable(): boolean {
    if (!this.unknown || !this.current(this.revision)) return false;
    if (this.action === 'create') return !!(this.requestId || this.legacyId) && this.owner!.features.includes('client_ops');
    return !!this.requestBody && (this.action === 'rename' || this.action === 'clean' && !!this.token);
  }
  get canCheck(): boolean { return !this.busy && this.requestCheckAvailable; }
  get canRetrySame(): boolean {
    return this.canCheck && this.action === 'create' && this.canReplayDuringCheck(this.requestId);
  }
  private fresh(start: number): boolean { const age = Date.now() - start; return start > 0 && age >= 0 && age < RECEIPT_RETENTION; }
  private async finishRecord(id: string, state: string): Promise<void> {
    const owner = this.owner!, journal = owner.repo().mutations, record = this.exactRecord(id);
    if (record && journal && record.state !== state) await journal.finish(record, state);
    if (journal && this.owner === owner && this.current(this.revision)) owner.unresolvedWrites = journal.blocked();
  }
  private async completed(rev: number, result: Record<string,Object>): Promise<WorktreeChange | undefined> {
    if (!this.current(rev)) return undefined;
    const change = this.legacy ? undefined : this.change(result), view = this.view!;
    await this.finishRecord(this.legacy ? this.legacyId : this.requestId, 'done');
    if (!this.current(rev)) return undefined;
    if (this.action === 'rename') await view.clearIfUnchanged(this.renameSnapshot).catch(() => undefined);
    else if (!this.legacy) await view.write('').catch(() => undefined);
    if (!this.current(rev)) return undefined;
    this.unknown = false; this.ready = false; this.retrySupported = false; this.releaseReady = false; this.error = change ? '' : 'done';
    return change;
  }
  private async refused(rev: number, code: string): Promise<void> {
    await this.finishRecord(this.legacy ? this.legacyId : this.requestId, 'failed');
    if (!this.current(rev)) return;
    if (!this.legacy) await this.view!.write('').catch(() => undefined);
    if (!this.current(rev)) return;
    this.unknown = false; this.ready = true; this.retrySupported = false; this.legacy = false; this.releaseReady = false;
    this.error = code || 'failed';
  }
  private async status(id: string): Promise<Record<string,Object>> {
    const rev = this.revision, owner = this.owner!;
    try {
      const op = await this.read(rev, () => owner.repo().call('/api/v1/ops/' + encodeURIComponent(id)));
      if (!this.current(rev)) throw new Error('request_updated');
      const path = Wire.text(Wire.object(op, 'target'), 'path');
      if (Wire.text(op,'id') !== id || Wire.text(op,'kind') !== 'worktree_create' ||
        (path !== this.endpoint('create') && path !== '/api/v1/machines/' + this.tree!.machine + '/worktree/create')) throw new Error('request_updated');
      return op;
    } catch (error) {
      if (error instanceof HttpFailure && error.status === 404 && ['no such operation','no_such_operation'].includes(error.code))
        return Wire.payload(['state'],['missing']);
      throw error as Error;
    }
  }
  private receiptBody(result: Record<string,Object>): Record<string,Object> {
    const encoded = Wire.text(result,'body');
    if (!encoded) return Wire.object(result,'body');
    if (encoded.length > 256 * 1024) return {};
    try {
      const bytes = new util.Base64Helper().decodeSync(encoded);
      return Wire.parse(util.TextDecoder.create('utf-8', { fatal: true }).decodeToString(bytes));
    } catch (_) { return {}; }
  }
  private sameSource(row: Record<string,Object>): boolean {
    const tree = this.tree!;
    return Wire.text(row,'machine_id') === tree.machine && Wire.text(row,'source') === tree.source &&
      Wire.text(row,'source_id') === tree.sourceId && checkoutComparable(Wire.text(row,'cwd')) === checkoutComparable(tree.cwd) &&
      Wire.bool(row,'online') && (!Wire.has(row,'available') || Wire.bool(row,'available'));
  }
  private async workspaces(rev: number, requireComplete: boolean = false): Promise<Record<string,Object>[]> {
    const owner = this.owner!;
    const result = await this.read(rev, () => owner.repo().call('/api/v1/workspaces'));
    if (!this.current(rev)) throw new Error('request_updated');
    if (requireComplete && (Wire.bool(result,'has_more',Wire.has(result,'has_more')) || Wire.bool(result,'partial',Wire.has(result,'partial')) ||
      !!Wire.text(result,'next_cursor') || Wire.has(result,'errors') && !Array.isArray(result['errors']))) throw new Error('result_unknown');
    if (!Array.isArray(result['workspaces']) || Wire.rows(result,'errors').some((err: Record<string,Object>) =>
      !Wire.text(err,'machine_id') || Wire.text(err,'machine_id') === this.tree!.machine)) throw new Error('result_unknown');
    return Wire.rows(result,'workspaces');
  }
  private async liveRenameName(rev: number): Promise<string> {
    const tree = this.tree!, rows = await this.workspaces(rev,true);
    this.requireCurrent(rev);
    if (!tree.group || !tree.machine || !tree.source || !tree.sourceId || !tree.cwd.trim()) throw new Error('result_unknown');
    const matches = rows.filter((row: Record<string,Object>) => Wire.text(row,'machine_id') === tree.machine &&
      Wire.text(row,'source') === tree.source && Wire.text(row,'source_id') === tree.sourceId);
    if (matches.length !== 1 || !this.sameSource(matches[0]) ||
      Wire.has(matches[0],'manager_id') && Wire.text(matches[0],'manager_id') !== tree.manager) throw new Error('result_unknown');
    const name = Wire.text(matches[0],'title');
    if (!name.trim()) throw new Error('result_unknown');
    return name;
  }
  private canKeepRename(record: MutationRecord | undefined): boolean {
    if (!record || record.id !== this.requestId || record.method !== 'POST' || record.path !== this.endpoint('rename') ||
      record.confirmedPending !== false || record.requiresReceipt !== false || !['unknown','checked'].includes(record.state)) return false;
    const created = Date.parse(record.createdAt);
    return Number.isFinite(created) && created > 0 && this.requestTime > 0 &&
      Date.now() - Math.max(created,this.requestTime) >= NODE_RENAME_TIMEOUT;
  }
  private async recoverRename(rev: number, confirm: boolean): Promise<WorktreeChange | undefined> {
    const previous = this.renameCurrent, reviewed = this.renameReviewedRecord;
    const snapshot = this.renameSnapshot, view = this.view!, id = this.requestId;
    this.renameCurrent = ''; this.renameReviewedRecord = undefined;
    this.error = 'result_unknown';
    const current = await this.liveRenameName(rev);
    this.requireCurrent(rev);
    const owner = this.owner!, journal = owner.repo().mutations, record = journal?.record(id);
    const saved = await view.read();
    this.requireCurrent(rev);
    // A successful journal check survives an interrupted UI update or a failed snapshot removal.
    const alreadyCleared = saved === '' && record?.state === 'checked' && this.canKeepRename(record);
    if (!snapshot || saved !== snapshot && !alreadyCleared) return undefined;
    if (confirm && (previous !== current || reviewed !== record)) { this.error = 'tree_rename_check_again'; return undefined; }
    const wanted = Wire.text(Wire.parse(this.requestBody),'name');
    if (current === wanted) return this.completed(rev,Wire.payload(['ok'],[true]));
    if (!journal || !this.canKeepRename(record) ||
      journal.blocked().some((item: MutationRecord) => item !== record && item.method === 'POST' && item.path === this.requestPath)) return undefined;
    if (!confirm) { this.renameCurrent = current; this.renameReviewedRecord = record; this.error = 'tree_rename_keep_explanation'; return undefined; }
    // A negative readback cannot prove non-delivery. The explicit choice only marks this
    // local record checked; a late reply or another person's title must never cause a POST.
    await journal.acknowledge(id, () => this.current(rev) && journal.record(id) === record && this.canKeepRename(record));
    if (!this.current(rev) || journal.record(id) !== record || record?.state !== 'checked') return undefined;
    owner.unresolvedWrites = journal.blocked();
    await view.clearIfUnchanged(snapshot);
    const remaining = await view.read();
    if (!this.current(rev)) return undefined;
    if (remaining) { this.error = remaining === snapshot ? 'storage_unavailable' : 'request_updated'; return undefined; }
    if (journal.blocked().some((item: MutationRecord) => item.method === 'POST' && item.path === this.requestPath)) {
      this.error = 'pending_action'; return undefined;
    }
    this.title = current; this.name = current; this.reviewedRenameBase = current;
    this.renameOriginal = ''; this.renameSnapshot = ''; this.requestBody = ''; this.requestId = '';
    this.unknown = false; this.ready = true; this.error = '';
    return undefined;
  }
  private async checkMissingLegacy(rev: number, confirm: boolean): Promise<void> {
    const record = this.exactRecord(this.legacyId), at = record ? Date.parse(record.createdAt) : 0;
    const target = JSON.stringify([this.branch.trim(),this.path.trim()]);
    const admitted = this.releaseReady && this.releaseTarget === target;
    this.releaseReady = false;
    if (!record || !this.fresh(at) || Date.now() - at <= NODE_CREATE_TIMEOUT || !validWorktreeBranch(this.branch) ||
      !this.path.trim() || this.automatic || !await this.verifyCreateVersion() || !this.current(rev)) {
      if (this.current(rev)) this.error = 'tree_verify_original_checkout'; return;
    }
    const rows = await this.workspaces(rev);
    if (!this.current(rev)) return;
    const sourceOnline = rows.some((row: Record<string,Object>) => this.sameSource(row));
    const found = rows.some((row: Record<string,Object>) => Wire.text(row,'machine_id') === this.tree!.machine &&
      (checkoutComparable(Wire.text(row,'cwd')) === checkoutComparable(this.path) || Wire.text(row,'branch') === this.branch.trim()));
    if (!sourceOnline || found || this.exactRecord(this.legacyId) !== record) { this.error = 'result_unknown'; return; }
    if (!confirm || !admitted) { this.releaseReady = true; this.releaseTarget = target; this.error = 'tree_verify_original_checkout'; return; }
    await this.refused(rev,'');
    if (this.current(rev)) { this.error = ''; this.legacyId = ''; this.requestId = ''; this.requestBody = ''; }
  }
  private async recoverCreate(rev: number, replay: boolean): Promise<WorktreeChange | undefined> {
    const id = this.legacy ? this.legacyId : this.requestId, canReplay = this.canReplayDuringCheck(id);
    const op = await this.status(id);
    if (!this.current(rev)) return undefined;
    const state = Wire.text(op,'state'), result = Wire.object(op,'result'), answer = this.receiptBody(result);
    if (state !== 'missing') this.releaseReady = false;
    if (state === 'done' && (this.legacy || this.acknowledged(answer))) return this.completed(rev, answer);
    if (state === 'failed') { await this.refused(rev, Wire.text(answer,'code') || Wire.text(answer,'error') || 'failed'); return undefined; }
    if (state === 'missing' && this.legacy) { await this.checkMissingLegacy(rev,replay); return undefined; }
    if (!['pending','unknown','missing','done'].includes(state)) throw new Error('result_unknown');
    const supported = state !== 'missing' || this.originalReceiptRoute && await this.verifyCreateVersion();
    if (!this.current(rev)) return undefined;
    this.retrySupported = !this.legacy && supported;
    if (state === 'pending' || state === 'unknown') await this.finishRecord(id,state);
    if (!this.current(rev)) return undefined;
    if (this.legacy || !replay || !canReplay || !this.retrySupported || !this.exactRecord(id)) {
      this.error = state === 'pending' ? 'pending' : 'result_unknown'; return undefined;
    }
    const owner = this.owner!, path = this.requestPath, bytes = this.requestBody;
    try {
      const value = Wire.parse(await owner.repo().transport.request(path,'POST',bytes,id));
      if (!this.current(rev)) return undefined;
      if (this.acknowledged(value)) return this.completed(rev, value);
      const pending = Wire.text(value,'state') === 'pending'; await this.finishRecord(id, pending ? 'pending' : 'unknown');
      if (this.current(rev)) this.error = pending ? 'pending' : 'result_unknown';
    } catch (error) {
      if (!this.current(rev)) return undefined;
      if (error instanceof HttpFailure && error.code === 'op_id_reused') { this.retrySupported = false; this.error = 'request_updated'; return undefined; }
      if (error instanceof HttpFailure && !error.resultUnknown) {
        const latest = await this.status(id);
        if (!this.current(rev)) return undefined;
        const lastState = Wire.text(latest,'state'), cached = this.receiptBody(Wire.object(latest,'result'));
        if (lastState === 'failed' || lastState === 'unknown' && Wire.text(cached,'delivered') === 'none')
          await this.refused(rev, Wire.text(cached,'code') || this.failure(error as Error));
        else if (lastState === 'done' && this.acknowledged(cached)) return await this.completed(rev,cached);
        else this.error = lastState === 'pending' ? 'pending' : 'result_unknown';
        return undefined;
      }
      await this.finishRecord(id, error instanceof HttpFailure && error.resultPending ? 'pending' : 'unknown');
      if (this.current(rev)) this.error = error instanceof HttpFailure && error.resultPending ? 'pending' : 'result_unknown';
    }
    return undefined;
  }
  private canReplayDuringCheck(id: string): boolean {
    if (this.legacy || !this.retrySupported || !this.requestBody || !this.fresh(this.requestTime) || !this.exactRecord(id)) return false;
    const body = Wire.parse(this.requestBody);
    return this.automatic === Wire.bool(body,'auto_name') && (this.automatic ||
      this.branch.trim() === Wire.text(body,'branch') && this.path.trim() === Wire.text(body,'path'));
  }
  async reconcile(replay: boolean = false): Promise<WorktreeChange | undefined> {
    if (!this.canCheck) return undefined;
    const rev = this.revision, lane = JSON.stringify([this.ownerScope,this.endpoint(this.action === 'clean' ? 'delete' : this.action)]);
    if (submittingRoutes.has(lane)) return undefined;
    submittingRoutes.add(lane); this.busy = true;
    try {
      if (this.action === 'create') return await this.recoverCreate(rev,replay);
      if (this.action === 'rename') return await this.recoverRename(rev,replay);
      const body = this.body(); body['preview_token'] = this.token; body['reconcile'] = true;
      const result = await this.query('delete', body);
      if (this.current(rev) && this.removed(result)) return await this.completed(rev, result);
      if (this.current(rev)) this.error = 'result_unknown';
      return undefined;
    } catch (error) {
      if (this.current(rev)) this.error = this.failure(error as Error);
      return undefined;
    } finally { submittingRoutes.delete(lane); if (this.revision === rev) this.busy = false; }
  }
}
