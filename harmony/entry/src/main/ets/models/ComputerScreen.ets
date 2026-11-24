import { util } from '@kit.ArkTS';
import { ScreenVideo } from '../platform/ScreenVideo';
import { screenConnectionInfo } from './ScreenConnectionInfo';
import { ClientModel } from './ClientModel';
import type { ClientViewLease } from './ClientModel';
import { ResourceRow, Wire } from './Wire';
import { HubRepository, segment } from './HubRepository';
import { HubTransport, HttpFailure } from '../platform/HubTransport';
import { RFBClient, RFBScreenInfo, RFBCursor } from '../core/RFBClient';
import type { RFBTransport } from '../core/RFBClient';
import { RFBFramebuffer, RFBRegion } from '../core/RFBFramebuffer';
import { ScreenDisplay, ScreenDisplays } from './ScreenDisplays';
import { decodeScreenPatch } from '../platform/ScreenDecode';
import { RFBCredentials } from '../core/RFBAuth';
import { RFBOwnerLease } from '../core/RFBOwnerLease';
import { ScreenCredentialStore } from '../platform/ScreenCredentialStore';

const KEY_BATCH_SIZE: number = 256;
// macOS Screen Sharing accepts pointer events before it reliably accepts keys after sign-in.
const KEYBOARD_SETTLE_MS: number = 3500;
const MAX_PRELUDE_BYTES: number = 2 * 1024 * 1024;
const SCREEN_RESPONSE_TIMEOUT_MS: number = 20000;
const FRAME_INTERVAL_MS: number = 16;
const VIDEO_HEAD_START_MS: number = 3000;

class ComputerScreenRelay implements RFBTransport {
  private readonly stream: HubTransport;
  constructor(stream: HubTransport) { this.stream = stream; }
  sendBinary(bytes: ArrayBuffer, replaceQueuedMotion: boolean = false): boolean { return this.stream.sendBinary(bytes, replaceQueuedMotion); }
  close(): void { this.stream.close(); }
}

/** One visible computer screen, bound to its selected machine owner and connection generation. */
@ObservedV2
export class ComputerScreen {
  @Trace phase: string = 'idle';
  @Trace messageKey: string = 'computer_screen_waiting';
  @Trace credentialsRequested: boolean = false;
  @Trace loginRemembered: boolean = false;
  @Trace loginNotice: string = '';
  @Trace frameRevision: number = 0;
  @Trace remoteWidth: number = 0;
  @Trace remoteHeight: number = 0;
  @Trace displays: ScreenDisplay[] = [];
  @Trace selectedDisplay: string = '';
  @Trace videoAvailable: boolean = false;
  @Trace videoPreferred: boolean = false;
  @Trace videoRequested: boolean = false;
  @Trace videoCodec: string = '';
  @Trace videoFailed: boolean = false;
  private picturePreference?: ClientViewLease;
  private static preferenceWrite: Promise<void> = Promise.resolve();
  private videoLeadTimer: number = -1;
  private videoPair: string = '';
  private videoSurface: string = '';
  private video?: ScreenVideo;
  private viewingGesture: boolean = false;
  @Trace hasFrame: boolean = false;
  @Trace failureDetail: string = '';
  @Trace cursor: RFBCursor | undefined = undefined;
  @Trace clipboard: string = '';
  @Trace controlling: boolean = false;
  @Trace keyboardInputFailed: boolean = false;

  private parent: ClientModel | undefined;
  private owner: ClientModel | undefined;
  private row: ResourceRow | undefined;
  private rowKey: string = '';
  private repository: HubRepository | undefined;
  private stream: HubTransport | undefined;
  private client: RFBClient | undefined;
  private lease: RFBOwnerLease | undefined;
  private parentGeneration: number = 0;
  private parentScope: string = '';
  private ownerGeneration: number = 0;
  private ownerScope: string = '';
  private attempt: number = 0;
  private controlRevision: number = 0;
  private keysReadyAt: number = 0;
  private inputTail: Promise<void> = Promise.resolve();
  private frameTimer: number = -1;
  private lastFrameAt: number = 0;
  private responseTimer: number = -1;
  private visible: boolean = false;
  private socketReady: boolean = false;
  private credentialAnswer: ((value: RFBCredentials | undefined) => void) | undefined;
  private loginStore: ScreenCredentialStore | undefined;
  private savedAttempt: RFBCredentials | undefined;
  private rememberOnSuccess: RFBCredentials | undefined;
  private manualLogin: boolean = false;
  private machineUID: string = '';
  private checkedMachineRaw: string | undefined;
  private machineManager: string = '';
  private skipSaved: boolean = false;

  constructor() {}

  async open(parent: ClientModel, row: ResourceRow, rejectedSaved: boolean = false): Promise<void> {
    this.close();
    this.skipSaved = rejectedSaved;
    if (rejectedSaved) this.loginNotice = 'computer_screen_saved_rejected';
    this.parent = parent;
    this.row = row;
    this.rowKey = row.key();
    this.parentGeneration = parent.connectionGeneration();
    this.parentScope = parent.scope();
    this.visible = true;
    this.phase = 'checking';
    this.messageKey = 'computer_screen_waiting';
    this.credentialsRequested = false;
    this.attempt++;
    const attempt: number = this.attempt;

    if (!row.id || row.kind !== 'machine' || parent.demo || !parent.connected || !parent.owns(this.parentGeneration, this.parentScope)) {
      this.fail(attempt, 'computer_screen_screen_unavailable');
      return;
    }

    try {
      const owner: ClientModel = await parent.viewFor(row);
      if (!this.parentCurrent(attempt) || !owner.connected || owner.demo ||
        (row.manager && owner.profile.manager !== row.manager)) {
        this.fail(attempt, 'computer_screen_screen_unavailable');
        return;
      }
      const machine: ResourceRow | undefined = owner.machines.find((candidate: ResourceRow) => candidate.id === row.id);
      if (!machine || row.manager && machine.manager !== row.manager || !this.isMac(machine) || !this.machineCanConnect(machine)) {
        this.fail(attempt, this.isMac(machine || row) ? 'computer_screen_screen_unavailable' : 'computer_screen_not_mac');
        return;
      }

      this.owner = owner;
      this.ownerGeneration = owner.connectionGeneration();
      this.ownerScope = owner.scope();
      const repository: HubRepository = owner.repo();
      this.repository = repository;
      this.machineUID = Wire.text(machine.data(), 'machine_uid');
      this.machineManager = machine.manager || owner.profile.manager;
      if (owner.profile.id && owner.profile.hubId) {
        this.loginStore = new ScreenCredentialStore(owner.profile.id, owner.profile.hubId,
          owner.profile.pin || owner.profile.baseUrl, this.machineManager, machine.id, this.machineUID);
      }
      const lease: RFBOwnerLease = new RFBOwnerLease((): boolean => this.isCurrent(attempt));
      this.lease = lease;

      const preference = owner.captureView('screen-picture:' + JSON.stringify([machine.id, this.machineUID]));
      this.picturePreference = preference;
      const preferred = ComputerScreen.preferenceWrite.then(() => preference.read()).catch(() => '');
      const response: Record<string, Object> = await screenConnectionInfo(owner, machine);
      const useVideo = await preferred;
      if (!this.isCurrent(attempt)) return;
      if (!Wire.bool(response, 'screen_sharing')) {
        this.fail(attempt, 'computer_screen_sharing_off');
        return;
      }

      this.videoPreferred = useVideo === 'video';
      const videoInfo = Wire.object(response, 'screen_video');
      this.videoAvailable = Wire.number(videoInfo, 'version') === 1 && Wire.text(videoInfo, 'permission') === 'granted';
      this.videoPair = this.videoAvailable ? util.generateRandomUUID().replace(/-/g, '') : '';
      const streamPath: string = '/api/v1/machines/' + segment(row.id) + '/ports/5900/connect' +
        (this.videoPair ? '?pair=' + this.videoPair : '');
      const stream: HubTransport = repository.transport.stream(streamPath);
      this.stream = stream;
      const pendingRFB: ArrayBuffer[] = [];
      let pendingBytes: number = 0;
      let clientStarted: boolean = false;
      const client: RFBClient = new RFBClient(
        new ComputerScreenRelay(stream),
        (): Promise<RFBCredentials | undefined> => this.askForCredentials(attempt),
        (): boolean => this.lease?.allowsInput() === true && this.isCurrent(attempt)
      );
      this.client = client;
      client.decodePatch = decodeScreenPatch;
      const machineDisplays = ScreenDisplays.machine(response);
      client.chooseView = (width: number, height: number, screens: RFBScreenInfo[]): RFBRegion | undefined => {
        if (!this.isCurrent(attempt)) return undefined;
        const available = ScreenDisplays.layout(width, height, screens, machineDisplays);
        const selected = available.find((d: ScreenDisplay) => d.key === this.selectedDisplay) || available[0];
        this.displays = available;
        this.selectedDisplay = selected?.key || '';
        this.videoRequested = this.videoPreferred && !this.videoFailed && this.canUseVideo();
        return selected;
      };
      client.onState = (phase: string, code: string, detail: string): void => this.protocolState(attempt, phase, code, detail);
      client.onFrame = (_framebuffer: RFBFramebuffer): void => this.frameArrived(attempt);
      client.onResize = (width: number, height: number): void => {
        if (!this.isCurrent(attempt)) return;
        this.stopVideo(false);
        this.remoteWidth = width;
        this.remoteHeight = height;
        this.beginPicture(attempt);
      };
      client.onCursor = (next: RFBCursor): void => {
        if (this.isCurrent(attempt)) this.cursor = next;
      };
      client.onClipboard = (text: string): void => {
        if (this.isCurrent(attempt)) this.clipboard = text;
      };

      await stream.connect((_message: string): void => {
        if (this.isCurrent(attempt)) this.fail(attempt, 'computer_screen_unsupported');
      }, (state: string): void => {
        if ((state === 'offline' || state === 'closed') && this.socketReady && this.visible &&
          !['failed', 'auth_failed', 'sharing_off', 'closed'].includes(this.phase)) {
          if (this.isCurrent(attempt)) this.fail(attempt, 'computer_screen_connection_lost');
          else if (attempt === this.attempt) this.close();
        }
      }, (bytes: ArrayBuffer): void => {
        if (!this.isCurrent(attempt)) { if (attempt === this.attempt) this.close(); return; }
        if (clientStarted) { client.receive(bytes); return; }
        if (pendingBytes + bytes.byteLength > MAX_PRELUDE_BYTES) { this.fail(attempt, 'computer_screen_unsupported'); return; }
        const copy: ArrayBuffer = new ArrayBuffer(bytes.byteLength);
        new Uint8Array(copy).set(new Uint8Array(bytes));
        pendingRFB.push(copy);
        pendingBytes += copy.byteLength;
      });
      if (!this.isCurrent(attempt)) { if (attempt === this.attempt) this.close(); return; }
      this.socketReady = true;
      const work: Promise<void> = client.start();
      clientStarted = true;
      pendingRFB.forEach((bytes: ArrayBuffer) => client.receive(bytes));
      pendingRFB.length = 0;
      pendingBytes = 0;
      await work;
    } catch (error) {
      if (!this.parentCurrent(attempt)) { if (attempt === this.attempt) this.close(); return; }
      const failure: Error = error as Error;
      if (failure instanceof HttpFailure && failure.status === 403) this.fail(attempt, 'computer_screen_read_only_device');
      else if (failure instanceof HttpFailure && failure.status === 404) this.fail(attempt, 'computer_screen_screen_unavailable');
      else if (failure.message === 'connection_closed' || failure.message === 'client_closed') this.fail(attempt, 'computer_screen_connection_lost');
      else this.fail(attempt, 'computer_screen_connection_failed');
    }
  }

  retry(): void {
    const parent: ClientModel | undefined = this.parent;
    const row: ResourceRow | undefined = this.row;
    if (!parent || !row || !this.visible) return;
    this.open(parent, row).catch((): void => this.fail(this.attempt, 'computer_screen_connection_failed'));
  }

  close(): void {
    this.visible = false;
    this.attempt++;
    this.clearVideo();
    this.keysReadyAt = 0;
    this.keyboardInputFailed = false;
    this.loginStore = undefined;
    this.savedAttempt = undefined;
    this.rememberOnSuccess = undefined;
    this.manualLogin = false;
    this.machineUID = '';
    this.checkedMachineRaw = undefined;
    this.machineManager = '';
    this.loginRemembered = false;
    this.loginNotice = '';
    this.clearResponseTimer();
    this.socketReady = false;
    this.lastFrameAt = 0;
    if (this.frameTimer !== -1) { clearTimeout(this.frameTimer); this.frameTimer = -1; }
    const answer: ((value: RFBCredentials | undefined) => void) | undefined = this.credentialAnswer;
    this.credentialAnswer = undefined;
    this.credentialsRequested = false;
    answer?.(undefined);
    this.lease?.revoke();
    this.lease = undefined;
    this.client?.close();
    this.client = undefined;
    this.stream?.close();
    this.stream = undefined;
    this.repository = undefined;
    this.owner = undefined;
    this.parent = undefined;
    this.row = undefined;
    this.rowKey = '';
    this.remoteWidth = 0;
    this.remoteHeight = 0;
    this.displays = [];
    this.selectedDisplay = '';
    this.hasFrame = false;
    this.failureDetail = '';
    this.cursor = undefined;
    this.clipboard = '';
    this.phase = 'closed';
    this.frameRevision++;
    this.controlling = false;
  }

  hide(): void { this.close(); }

  isLive(): boolean { return this.phase === 'live' && this.isCurrent(this.attempt); }

  validateOwner(): boolean {
    if (!this.visible) return false;
    if (!this.owner) {
      if (this.parentCurrent(this.attempt)) return true;
      this.close();
      return false;
    }
    if (this.isCurrent(this.attempt)) return true;
    this.close();
    return false;
  }

  controlAllowed(): boolean { return this.controlling && this.isCurrent(this.attempt) && this.lease?.allowsInput() === true; }

  connectionRevision(): number { return this.attempt; }

  grantControl(): boolean {
    if (!this.isLive() || !this.lease?.grant()) return false;
    if (!this.controlling) this.controlRevision++;
    this.keyboardInputFailed = false;
    this.controlling = true;
    return true;
  }

  releaseControl(): void {
    if (this.controlAllowed()) this.client?.releasePointer();
    this.controlRevision++;
    this.keyboardInputFailed = false;
    this.lease?.disable();
    this.controlling = false;
  }

  refresh(): void {
    if (this.isLive()) this.client?.refresh();
    else this.retry();
  }

  setViewingGesture(active: boolean): void {
    this.viewingGesture = active;
    this.updateRfbFlow();
  }

  private updateRfbFlow(): void {
    this.client?.holdUpdates(this.viewingGesture || this.videoLeadTimer !== -1 || !!this.videoCodec);
  }

  canUseVideo(): boolean {
    return this.videoAvailable && /^[0-9]+$/.test(this.selectedDisplay);
  }

  private beginPicture(attempt: number): void {
    this.viewingGesture = false;
    this.waitForFrame(attempt);
    this.clearVideoLead();
    if (this.videoRequested && this.canUseVideo()) {
      // The live callback runs before RFB sends its first request. Give the selected video surface
      // the link first, but let RFB fetch a picture if the surface or the helper is slow to start.
      this.videoLeadTimer = setTimeout((): void => {
        this.videoLeadTimer = -1;
        if (this.isCurrent(attempt)) this.updateRfbFlow();
      }, VIDEO_HEAD_START_MS);
    }
    this.updateRfbFlow();
    this.startVideo();
  }

  private clearVideoLead(): void {
    if (this.videoLeadTimer !== -1) clearTimeout(this.videoLeadTimer);
    this.videoLeadTimer = -1;
  }

  setVideoSurface(id: string): void {
    if (this.videoSurface === id) return;
    if (this.videoSurface) this.stopVideo(true);
    this.videoSurface = id;
    this.startVideo();
  }

  toggleVideo(): Promise<boolean> {
    return this.setVideoPreferred(!this.videoPreferred);
  }

  async setVideoPreferred(enabled: boolean): Promise<boolean> {
    const preference = this.picturePreference;
    if (!this.isLive() || !preference || !preference.valid() || (enabled && !this.canUseVideo())) return false;
    this.videoPreferred = enabled;
    this.videoRequested = enabled;
    this.videoFailed = false;
    if (enabled) this.startVideo();
    else this.stopVideo(true);
    const write = preference.write(enabled ? 'video' : 'rfb');
    ComputerScreen.preferenceWrite = write.catch((): void => {});
    try { await write; return true; } catch (_) { return false; }
  }

  private startVideo(): void {
    if (this.video || !this.videoRequested || !this.videoSurface || !this.repository ||
      !this.row || !this.videoPair || !this.isLive()) return;
    const display = this.displays.find((d: ScreenDisplay) => d.key === this.selectedDisplay);
    if (!display || !this.canUseVideo()) return;
    const attempt = this.attempt;
    const path = '/api/v1/machines/' + segment(this.row.id) + '/screen/video/connect?pair=' + this.videoPair;
    try {
      const session: ScreenVideo = new ScreenVideo(this.repository.transport.stream(path), this.videoSurface,
        Number(display.key), display.width / display.height,
        (): boolean => this.video === session && this.isCurrent(attempt) && this.selectedDisplay === display.key,
        (codec: string): void => {
          if (this.video !== session || !this.isCurrent(attempt)) return;
          this.videoCodec = codec;
          this.clearResponseTimer();
          this.clearVideoLead();
          this.updateRfbFlow();
        }, (): void => {
          if (this.video === session && this.isCurrent(attempt)) this.fallbackToRfb();
        });
      this.video = session;
      session.start();
    } catch (_) { this.fallbackToRfb(); }
  }

  private fallbackToRfb(): void {
    this.videoRequested = false;
    this.videoFailed = true;
    this.stopVideo(true);
  }

  private stopVideo(refresh: boolean): void {
    const session = this.video;
    this.video = undefined;
    session?.close();
    this.clearVideoLead();
    const wasShowing = !!this.videoCodec;
    this.videoCodec = '';
    if (refresh && this.isLive()) {
      if (wasShowing) {
        this.viewingGesture = false;
        this.waitForFrame(this.attempt);
        this.client?.refresh();
      }
      this.updateRfbFlow();
    }
  }

  private clearVideo(): void {
    this.stopVideo(false);
    this.picturePreference = undefined;
    this.videoPreferred = false;
    this.videoRequested = false;
    this.videoAvailable = false;
    this.videoFailed = false;
    this.videoPair = '';
    this.videoSurface = '';
    this.viewingGesture = false;
  }

  displayAspect(): number {
    const view = this.client?.framebuffer.view;
    return view ? view.width / view.height : 0;
  }

  selectDisplay(index: number): void {
    const next: ScreenDisplay | undefined = this.displays[index];
    if (!next || next.key === this.selectedDisplay || !this.isLive()) return;
    this.client?.holdUpdates(true);
    this.stopVideo(false);
    this.releasePointer();
    if (!this.client?.selectView(next)) { this.fail(this.attempt, 'computer_screen_connection_lost'); return; }
    this.selectedDisplay = next.key;
    this.videoFailed = false;
    this.videoRequested = this.videoPreferred && this.canUseVideo();
    this.beginPicture(this.attempt);
  }

  pointerFromView(x: number, y: number, viewWidth: number, viewHeight: number, buttons: number): boolean {
    const view = this.client?.framebuffer.view;
    if ((!this.hasFrame && !this.videoCodec) || !this.controlAllowed() || !view || viewWidth <= 0 || viewHeight <= 0) return false;
    const scale: number = Math.min(viewWidth / view.width, viewHeight / view.height);
    if (!Number.isFinite(scale) || scale <= 0) return false;
    const contentWidth: number = view.width * scale;
    const contentHeight: number = view.height * scale;
    const offsetX: number = (viewWidth - contentWidth) / 2;
    const offsetY: number = (viewHeight - contentHeight) / 2;
    const localX: number = x - offsetX;
    const localY: number = y - offsetY;
    if (localX < 0 || localY < 0 || localX >= contentWidth || localY >= contentHeight) return false;
    return this.pointerAt(localX / contentWidth, localY / contentHeight, buttons);
  }

  pointerAt(x: number, y: number, buttons: number): boolean {
    const view = this.client?.framebuffer.view;
    if (!view || (!this.hasFrame && !this.videoCodec) || !this.controlAllowed() || !(x >= 0 && x < 1 && y >= 0 && y < 1)) return false;
    return this.client?.pointer(view.x + Math.floor(x * view.width), view.y + Math.floor(y * view.height), buttons) || false;
  }

  releasePointer(): void { if (this.controlAllowed()) this.client?.releasePointer(); }

  private enqueueInput(write: (current: () => boolean) => Promise<boolean>): Promise<boolean> {
    if (!this.controlAllowed()) return Promise.resolve(false);
    const grant: number = this.controlRevision;
    const attempt: number = this.attempt;
    const current = (): boolean => grant === this.controlRevision && attempt === this.attempt && this.controlAllowed();
    const result: Promise<boolean> = this.inputTail.then(async (): Promise<boolean> => {
      if (!current()) return false;
      const wait: number = this.keysReadyAt - Date.now();
      if (wait > 0) await new Promise<void>((resolve): void => { setTimeout(resolve, wait); });
      if (!current()) return false;
      const sent: boolean = await write(current).catch((): boolean => false);
      if (!sent && current()) this.keyboardInputFailed = true;
      return sent;
    });
    this.inputTail = result.then((): void => {});
    return result;
  }

  pressKey(keysym: number, count: number = 1): Promise<boolean> {
    return this.enqueueInput(async (): Promise<boolean> => {
      if (!this.client || !Number.isSafeInteger(count) || count < 1 || count > KEY_BATCH_SIZE) return false;
      for (let i = 0; i < count; i++) if (!this.client.pressKey(keysym)) return false;
      return true;
    });
  }

  sendText(text: string): Promise<boolean> {
    return this.enqueueInput(async (current: () => boolean): Promise<boolean> => {
      if (!text || !this.client || !this.repository || !this.row) return false;
      if (!/[^\x00-\xff]/.test(text)) {
        for (let at: number = 0; at < text.length; at += KEY_BATCH_SIZE) {
          if (!this.client.typeText(text.slice(at, at + KEY_BATCH_SIZE))) return false;
        }
        return true;
      }
      await this.repository.transport.request('/api/v1/machines/' + segment(this.row.id) + '/computer/clipboard',
        'POST', JSON.stringify(Wire.payload(['text'], [text])));
      return current() && this.client.pressWithModifiers(0x76, [0xffeb]);
    });
  }

  presentationSource(): RFBFramebuffer | undefined {
    return this.hasFrame && this.isLive() ? this.client?.framebuffer : undefined;
  }

  presentationFailed(attempt: number): void {
    if (this.isCurrent(attempt) && this.isLive()) this.fail(attempt, 'computer_screen_frame_failed');
  }

  private async askForCredentials(attempt: number): Promise<RFBCredentials | undefined> {
    if (!this.isCurrent(attempt) || this.credentialAnswer) return undefined;
    if (this.loginStore && !this.skipSaved) {
      let saved: RFBCredentials | undefined;
      try { saved = await this.loginStore.load(); }
      catch (_) { if (this.isCurrent(attempt)) this.loginNotice = 'computer_screen_store_unavailable'; }
      if (!this.isCurrent(attempt)) return undefined;
      if (saved) {
        // The RFB client wipes the object it receives as soon as authentication bytes are sent.
        this.savedAttempt = new RFBCredentials(saved.account, saved.password);
        this.phase = 'authenticating';
        this.messageKey = 'computer_screen_authenticating';
        this.startResponseTimer(attempt, 'computer_screen_auth_timeout');
        return saved;
      }
    }
    this.phase = 'credentials';
    this.messageKey = 'computer_screen_enter_credentials';
    this.credentialsRequested = true;
    return new Promise<RFBCredentials | undefined>((resolve) => { this.credentialAnswer = resolve; });
  }

  submitCredentials(account: string, password: string, remember: boolean = false): boolean {
    if (!this.credentialsRequested) return false;
    const answer: ((value: RFBCredentials | undefined) => void) | undefined = this.credentialAnswer;
    if (!this.isCurrent(this.attempt) || !answer) {
      this.fail(this.attempt, 'computer_screen_connection_lost');
      return false;
    }
    const cleanAccount: string = account.trim();
    if (!cleanAccount || cleanAccount.length > 256 || password.length === 0 || password.length > 256) return false;
    this.credentialAnswer = undefined;
    this.credentialsRequested = false;
    this.phase = 'authenticating';
    this.messageKey = 'computer_screen_authenticating';
    this.startResponseTimer(this.attempt, 'computer_screen_auth_timeout');
    const credentials = new RFBCredentials(cleanAccount, password);
    this.manualLogin = true;
    if (remember) this.rememberOnSuccess = new RFBCredentials(cleanAccount, password);
    answer(credentials);
    return true;
  }

  async forgetLogin(): Promise<void> {
    const store = this.loginStore, attempt = this.attempt;
    if (!store || !this.loginRemembered) return;
    try {
      await store.forget();
      if (this.isCurrent(attempt)) { this.loginRemembered = false; this.loginNotice = ''; }
    } catch (_) { if (this.isCurrent(attempt)) this.loginNotice = 'computer_screen_forget_failed'; }
  }

  private acceptedLogin(attempt: number): void {
    const newlyEntered = this.rememberOnSuccess;
    const manual = this.manualLogin;
    this.manualLogin = false;
    if (this.savedAttempt) this.loginRemembered = true;
    this.savedAttempt = undefined;
    this.rememberOnSuccess = undefined;
    this.loginNotice = '';
    if (!newlyEntered) {
      if (manual) this.loginStore?.forget().catch((): void => {
        if (this.isCurrent(attempt)) this.loginNotice = 'computer_screen_forget_failed';
      });
      return;
    }
    if (!this.loginStore) { this.loginNotice = 'computer_screen_save_failed'; return; }
    this.loginStore.save(newlyEntered).then((): void => {
      if (this.isCurrent(attempt)) this.loginRemembered = true;
    }).catch((): void => { if (this.isCurrent(attempt)) this.loginNotice = 'computer_screen_save_failed'; });
  }

  private protocolState(attempt: number, phase: string, code: string, detail: string): void {
    if (!this.isCurrent(attempt)) { if (attempt === this.attempt) this.close(); return; }
    if (this.phase === 'failed' || this.phase === 'auth_failed' || this.phase === 'sharing_off' || this.phase === 'closed') return;
    if (phase === 'live') {
      if (this.phase !== 'live') {
        this.keysReadyAt = Date.now() + KEYBOARD_SETTLE_MS;
        this.acceptedLogin(attempt);
      }
      this.phase = 'live';
      this.remoteWidth = this.client?.framebuffer.width || 0;
      this.remoteHeight = this.client?.framebuffer.height || 0;
      this.beginPicture(attempt);
      return;
    }
    if (phase === 'credentials') {
      // The credential callback publishes the form only after looking for this Mac's saved login.
      return;
    }
    if (phase === 'connecting') {
      this.phase = 'connecting';
      this.messageKey = 'computer_screen_waiting';
      return;
    }
    if (phase === 'closed' || phase === 'failed') {
      if (code === 'auth_failed' && this.savedAttempt && this.loginStore && this.parent && this.row) {
        this.loginStore.forget(this.savedAttempt).catch((): void => {});
        this.open(this.parent, this.row, true).catch((): void => {});
        return;
      }
      if (code === 'auth_failed') this.fail(attempt, 'computer_screen_auth_failed');
      else if (code === 'unsupported') {
        if (['rfb_size_limit','rfb_rect_limit','rfb_input_limit','rfb_zrle_limit','rfb_zrle_inflate','rfb_zrle_tile',
          'rfb_bad_rect','rfb_encoding','rfb_unknown_message','rfb_name_limit','rfb_clipboard_limit'].includes(detail)) this.failureDetail = detail;
        this.fail(attempt, 'computer_screen_unsupported');
      }
      else if (code === 'connection_lost') this.fail(attempt, 'computer_screen_connection_lost');
      else this.fail(attempt, 'computer_screen_connection_failed');
    }
  }

  private frameArrived(attempt: number): void {
    if (!this.isCurrent(attempt)) { if (attempt === this.attempt) this.close(); return; }
    this.clearResponseTimer();
    this.hasFrame = true;
    if (this.videoCodec) return;
    if (this.frameTimer !== -1) return;
    const wait = FRAME_INTERVAL_MS - (Date.now() - this.lastFrameAt);
    if (wait <= 0) { this.lastFrameAt = Date.now(); this.frameRevision++; return; }
    this.frameTimer = setTimeout((): void => {
      this.frameTimer = -1;
      if (this.isCurrent(attempt)) { this.lastFrameAt = Date.now(); this.frameRevision++; }
    }, wait);
  }

  private waitForFrame(attempt: number): void {
    this.hasFrame = false;
    this.messageKey = 'computer_screen_waiting_frame';
    this.startResponseTimer(attempt, 'computer_screen_frame_timeout');
  }

  private startResponseTimer(attempt: number, messageKey: string): void {
    this.clearResponseTimer();
    this.responseTimer = setTimeout((): void => this.fail(attempt, messageKey), SCREEN_RESPONSE_TIMEOUT_MS);
  }

  private clearResponseTimer(): void {
    if (this.responseTimer !== -1) { clearTimeout(this.responseTimer); this.responseTimer = -1; }
  }

  private fail(attempt: number, messageKey: string): void {
    if (attempt !== this.attempt) return;
    this.attempt++;
    this.clearVideo();
    this.rememberOnSuccess = undefined;
    this.savedAttempt = undefined;
    this.clearResponseTimer();
    this.phase = messageKey === 'computer_screen_sharing_off' ? 'sharing_off' : messageKey === 'computer_screen_auth_failed' ? 'auth_failed' : 'failed';
    this.messageKey = messageKey;
    this.credentialsRequested = false;
    this.socketReady = false;
    this.lease?.revoke();
    if (this.frameTimer !== -1) { clearTimeout(this.frameTimer); this.frameTimer = -1; }
    this.remoteWidth = 0;
    this.remoteHeight = 0;
    this.displays = [];
    this.selectedDisplay = '';
    this.hasFrame = false;
    this.cursor = undefined;
    this.clipboard = '';
    this.frameRevision++;
    const answer: ((value: RFBCredentials | undefined) => void) | undefined = this.credentialAnswer;
    this.credentialAnswer = undefined;
    answer?.(undefined);
    const client: RFBClient | undefined = this.client;
    this.client = undefined;
    client?.close();
    const stream: HubTransport | undefined = this.stream;
    this.stream = undefined;
    stream?.close();
    this.controlling = false;
  }

  private parentCurrent(attempt: number): boolean {
    const parent: ClientModel | undefined = this.parent;
    const row: ResourceRow | undefined = this.row;
    return attempt === this.attempt && this.visible && !!parent && !!row && row.key() === this.rowKey &&
      parent.connected && !parent.demo && parent.owns(this.parentGeneration, this.parentScope);
  }

  private isCurrent(attempt: number): boolean {
    if (!this.parentCurrent(attempt)) return false;
    const owner: ClientModel | undefined = this.owner;
    const row: ResourceRow | undefined = this.row;
    const repository: HubRepository | undefined = this.repository;
    if (!owner || !row || !repository || !owner.connected || owner.demo || !owner.owns(this.ownerGeneration, this.ownerScope)) return false;
    try {
      if (owner.repo() !== repository) return false;
    } catch (_) { return false; }
    const machine: ResourceRow | undefined = owner.machines.find((candidate: ResourceRow) => candidate.id === row.id);
    if (!machine || (row.manager && machine.manager !== row.manager) ||
      (machine.manager || owner.profile.manager) !== this.machineManager) return false;
    // The directory payload is immutable until replaced. Avoid parsing it for each socket chunk and touch.
    if (machine.raw === this.checkedMachineRaw) return true;
    if (Wire.text(machine.data(), 'machine_uid') !== this.machineUID || !this.machineCanConnect(machine)) return false;
    this.checkedMachineRaw = machine.raw;
    return true;
  }

  private machineCanConnect(machine: ResourceRow): boolean {
    const data: Record<string, Object> = machine.data();
    const trust: string = Wire.text(data, 'trust');
    return Wire.bool(data, 'online') && !Wire.bool(data, 'pending') && !Wire.bool(data, 'pending_confirm') &&
      ['', 'confirmed', 'personal', 'work'].includes(trust);
  }

  private isMac(machine: ResourceRow): boolean {
    const data: Record<string, Object> = machine.data();
    const system: Record<string, Object> = Wire.object(data, 'system');
    const os: string = Wire.text(data, 'os', Wire.text(system, 'os'));
    return ['', 'mac', 'macos', 'darwin'].includes(os.toLowerCase());
  }
}
