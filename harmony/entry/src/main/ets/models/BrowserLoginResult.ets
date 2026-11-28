import type { ClientModel } from './ClientModel';
import type { HttpFailure } from '../platform/HubTransport';
import { Wire } from './Wire';

const approvalPath = '/api/v1/auth/browser-login/approve';

interface BrowserLoginCode {
  hub: string;
  request: string;
}

function readCode(raw: string): BrowserLoginCode | undefined {
  const match = /^persona:\/\/browser-login\?([^#\s]+)$/.exec(raw.trim());
  const fields = match ? match[1].split('&') : [];
  if (fields.length !== 2) return undefined;
  const code: BrowserLoginCode = { hub: '', request: '' };
  try {
    for (const field of fields) {
      const at = field.indexOf('=');
      if (at < 1) return undefined;
      const key = decodeURIComponent(field.slice(0, at));
      const value = decodeURIComponent(field.slice(at + 1));
      if (key === 'hub') code.hub = value;
      if (key === 'request') code.request = value;
    }
  } catch (_) { return undefined; }
  return code.hub && code.hub.length <= 80 && /^[A-Za-z0-9_-]{43}$/.test(code.request) ? code : undefined;
}

@ObservedV2
export class BrowserLoginResult {
  @Trace phase: string = 'checking';
  @Trace name: string = '';
  @Trace network: string = 'unknown';
  @Trace requestedAt: number = 0;
  @Trace number: string = '';
  @Trace choice: boolean | undefined = undefined;
  private readonly owner: ClientModel;
  private readonly scope: string;
  private readonly generation: number;
  private readonly pairing: number;
  private readonly onSignedIn: () => void;
  private code?: BrowserLoginCode;
  private revision: number = 0;
  private timer: number = -1;
  private expiresAt: number = 0;
  private dismissed: boolean = false;

  constructor(owner: ClientModel, onSignedIn: () => void) {
    this.owner = owner;
    this.scope = owner.scope();
    this.generation = owner.connectionGeneration();
    this.pairing = owner.pairingRevision();
    this.onSignedIn = onSignedIn;
  }

  samePairing(owner: ClientModel): boolean {
    return this.owner === owner && owner.scope() === this.scope && owner.pairingRevision() === this.pairing;
  }

  connected(): boolean {
    return this.samePairing(this.owner) && this.owner.connected && this.owner.owns(this.generation, this.scope);
  }

  private pending(): boolean {
    return this.phase === 'checking' || this.phase === 'ask' || this.phase === 'number';
  }

  private finish(phase: string): void {
    clearTimeout(this.timer);
    this.revision++;
    this.choice = undefined;
    this.number = '';
    this.phase = phase;
  }

  unavailable(): void {
    if (this.pending()) this.finish('failed');
  }

  async load(raw: string): Promise<void> {
    const code = readCode(raw);
    if (!code) { this.finish('invalid'); return; }
    if (code.hub !== this.owner.profile.hubId) { this.finish('other_hub'); return; }
    if (!this.connected()) { this.unavailable(); return; }
    this.code = code;
    await this.request();
  }

  async decide(allow: boolean): Promise<void> {
    if (this.dismissed || this.choice !== undefined || (this.phase !== 'ask' && !(this.phase === 'number' && !allow))) return;
    await this.request(allow);
  }

  dismiss(): void {
    if (this.dismissed) return;
    this.dismissed = true;
    // Closing the sheet also closes a still-pending request on its original Hub.
    if (this.code && this.pending() && this.choice !== false && this.connected()) {
      this.send(this.code, false).catch(() => {});
    }
    clearTimeout(this.timer);
    this.revision++;
  }

  private send(code: BrowserLoginCode, allow?: boolean): Promise<string> {
    const body = Wire.payload(['id', 'hub'], [code.request, code.hub]);
    if (allow !== undefined) body['allow'] = allow;
    if (allow === true) body['show_number'] = true;
    return this.owner.repo().transport.request(approvalPath, 'POST', JSON.stringify(body));
  }

  private watch(): void {
    const remaining = this.expiresAt - Date.now();
    if (remaining <= 0) { this.finish('expired'); return; }
    this.timer = setTimeout(() => {
      if (Date.now() >= this.expiresAt) this.finish('expired');
      else this.request();
    }, Math.min(1500, remaining));
  }

  private async request(allow?: boolean): Promise<void> {
    if (!this.code || !this.connected()) { this.unavailable(); return; }
    clearTimeout(this.timer);
    const revision = ++this.revision;
    this.choice = allow;
    try {
      const response = Wire.parse(await this.send(this.code, allow));
      if (revision !== this.revision) return;
      if (!this.connected()) { this.unavailable(); return; }
      const state = Wire.text(response, 'state');
      this.name = Wire.text(response, 'name', this.name);
      const network = Wire.text(response, 'network');
      this.network = network === 'lan' || network === 'internet' ? network : 'unknown';
      this.choice = undefined;
      if ((state === 'pending' || state === 'scanned') && allow === undefined) {
        const age = Wire.number(response, 'age_seconds', -1);
        this.requestedAt = age >= 0 ? Date.now() - age * 1000 : 0;
        this.phase = 'ask';
      } else if (state === 'confirm') {
        const number = Wire.text(response, 'number');
        if (!/^\d{2}$/.test(number)) { this.finish('ended'); return; }
        if (!this.expiresAt) this.expiresAt = Date.parse(Wire.text(response, 'expires_at')) || Date.now() + 300000;
        this.number = number;
        this.phase = 'number';
        this.watch();
      } else if (state === 'approved') {
        this.finish('success');
        this.onSignedIn();
      } else {
        this.finish(state === 'denied' || state === 'voided' ? state : 'failed');
      }
    } catch (error) {
      if (revision !== this.revision) return;
      const status = (error as HttpFailure).status;
      if (this.phase === 'number' && allow === undefined && (!status || status >= 500) && this.connected()) {
        this.watch();
      } else {
        this.finish(status === 410 ? 'expired' : status === 409 ? 'ended' : 'failed');
      }
    }
  }
}
