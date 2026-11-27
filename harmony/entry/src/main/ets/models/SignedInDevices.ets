import type { ClientModel } from './ClientModel';
import { BoundRow, reconcileBoundRows } from './RowBinding';
import type { BrowserIdentity } from './DeviceIdentity';
import { browserIdentity } from './DeviceIdentity';
import { Wire } from './Wire';
import type { HttpFailure } from '../platform/HubTransport';

export class SignedInDevice {
  readonly id: string;
  readonly name: string;
  readonly kind: string;
  readonly machineId: string;
  readonly browser?: BrowserIdentity;
  readonly created: string;
  readonly lastSeen: string;
  readonly revoked: string;
  readonly activityTime: number;

  constructor(row: Record<string, Object>) {
    this.id = Wire.text(row, 'id');
    this.name = Wire.text(row, 'name');
    this.kind = Wire.text(row, 'kind');
    this.machineId = Wire.text(row, 'machine_id');
    if (this.kind === 'web') this.browser = browserIdentity(this.name);
    this.created = Wire.text(row, 'created_at');
    this.lastSeen = Wire.text(row, 'last_seen');
    this.revoked = Wire.text(row, 'revoked_at');
    this.activityTime = Date.parse(this.revoked || this.lastSeen || this.created) || 0;
  }
}

const devicesPath = '/api/v1/auth/tokens';

@ObservedV2
export class SignedInDevices {
  @Trace rows: BoundRow<SignedInDevice>[] = [];
  @Trace currentId: string = '';
  @Trace loading: boolean = false;
  @Trace revoking: string = '';
  @Trace error: string = '';
  private owner?: ClientModel;
  private scope: string = '';
  private generation: number = -1;
  private epoch: number = 0;
  private readSequence: number = 0;

  @Computed get signedInRows(): BoundRow<SignedInDevice>[] {
    const current = this.currentId;
    return this.rows.filter((row: BoundRow<SignedInDevice>) => !row.value.revoked)
      .sort((a: BoundRow<SignedInDevice>, b: BoundRow<SignedInDevice>): number => {
        if (a.value.id === current) return b.value.id === current ? 0 : -1;
        if (b.value.id === current) return 1;
        return b.value.activityTime - a.value.activityTime;
      });
  }

  @Computed get revokedRows(): BoundRow<SignedInDevice>[] {
    return this.rows.filter((row: BoundRow<SignedInDevice>) => !!row.value.revoked)
      .sort((a: BoundRow<SignedInDevice>, b: BoundRow<SignedInDevice>) => b.value.activityTime - a.value.activityTime);
  }

  matching(revoked: boolean, query: string, describe: (row: SignedInDevice) => string): BoundRow<SignedInDevice>[] {
    const group = revoked ? this.revokedRows : this.signedInRows;
    const needle = query.trim().toLocaleLowerCase();
    if (!needle) return group;
    return group.filter((row: BoundRow<SignedInDevice>) =>
      row.value.name.toLocaleLowerCase().includes(needle) || describe(row.value).toLocaleLowerCase().includes(needle));
  }

  bind(owner?: ClientModel): void {
    this.epoch++;
    this.owner = owner;
    this.scope = owner?.scope() || '';
    this.generation = owner?.connectionGeneration() ?? -1;
    this.rows = []; this.currentId = ''; this.revoking = ''; this.error = ''; this.loading = false;
    if (owner?.demo) this.error = 'devices_demo';
    else if (owner && !owner.connected) this.error = 'offline';
    else if (owner) this.refresh();
  }

  private current(epoch: number): boolean {
    return epoch === this.epoch && !!this.owner && !this.owner.demo && this.owner.connected &&
      this.owner.owns(this.generation, this.scope);
  }

  async refresh(): Promise<void> {
    const owner = this.owner, epoch = this.epoch;
    if (!owner || !this.current(epoch) || this.revoking) return;
    const read = ++this.readSequence;
    this.loading = true; this.error = '';
    try {
      const data = await owner.repo().call(devicesPath);
      if (!this.current(epoch) || this.readSequence !== read) return;
      if (!Array.isArray(data['tokens'])) throw new Error('response_invalid');
      const devices = Wire.rows(data, 'tokens').map((row: Record<string, Object>) => new SignedInDevice(row));
      const ids = new Set<string>();
      for (const row of devices) {
        if (!row.id || ids.has(row.id) || row.id === '.' || row.id === '..') throw new Error('response_invalid');
        ids.add(row.id);
      }
      this.currentId = Wire.text(data, 'current');
      this.rows = reconcileBoundRows(this.rows, devices, (row: SignedInDevice) => row.id, owner, owner, this.scope, this.scope);
    } catch (error) {
      if (!this.current(epoch) || this.readSequence !== read) return;
      this.rows = []; this.currentId = '';
      const status = (error as HttpFailure).status;
      this.error = status === 403 ? 'permission_denied' : status === 404 ? 'unsupported' : 'devices_load_failed';
    } finally {
      if (this.epoch === epoch && this.readSequence === read) this.loading = false;
    }
  }

  // Capture the credential and pairing at the time the confirmation is presented. A name is only a label.
  prepareRevoke(device: SignedInDevice): (() => Promise<void>) | undefined {
    const owner = this.owner, epoch = this.epoch, currentId = this.currentId;
    if (!owner) return undefined;
    const profile = owner.profile.id, pairing = owner.pairingRevision();
    const valid = (): boolean => this.current(epoch) && owner.pairingRevision() === pairing && this.currentId === currentId &&
      !this.loading && !this.error && this.rows.some((row: BoundRow<SignedInDevice>) =>
        row.value.id === device.id && row.value.name === device.name && !row.value.revoked);
    if (this.revoking || !valid()) return undefined;
    return async (): Promise<void> => {
      if (this.revoking || !valid()) return;
      this.revoking = device.id;
      const path = devicesPath + '/' + encodeURIComponent(device.id);
      let failed = false;
      try {
        await owner.repo().call(path, 'DELETE', {}, '', valid, false, (reply: Record<string, Object>) => Wire.bool(reply, 'ok'));
        if (device.id === currentId && owner.pairingRevision(profile) === pairing) await owner.forgetProfile(profile);
      } catch (_) {
        // A lost reply or a concurrent revocation is resolved by reading the list, never by replaying the DELETE.
        failed = true;
      } finally {
        if (this.epoch === epoch) this.revoking = '';
      }
      if (!this.current(epoch)) return;
      await this.refresh();
      if (failed && this.current(epoch) && !this.error && this.rows.some((row: BoundRow<SignedInDevice>) =>
        row.value.id === device.id && !row.value.revoked)) this.error = 'devices_revoke_failed';
    };
  }
}
