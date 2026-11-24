import { util } from '@kit.ArkTS';
import { hilog } from '@kit.PerformanceAnalysisKit';
import { systemDateTime } from '@kit.BasicServicesKit';
import nativeVideo from 'libpersona_screen.so';
import type { ScreenDecoder } from 'libpersona_screen.so';
import type { HubTransport } from './HubTransport';
import { ScreenVideoReader, videoAccessUnit, videoPacket } from '../core/ScreenVideoWire';
import { Wire } from '../models/Wire';

const MAX_PENDING_FRAMES = 8;
const MAX_PENDING_BYTES = 8 * 1024 * 1024;
const KEYFRAME_TIMEOUT_MS = 2500;
const MAX_RECOVERY_ATTEMPTS = 3;
const STABLE_VIDEO_MS = 30000;
const FRAME_RATES: number[] = [60, 30, 20];
const BIT_RATES: number[] = [8000, 4000, 2500];

class Sample {
  readonly bytes: ArrayBuffer;
  readonly pts: number;
  readonly flags: number;
  constructor(bytes: ArrayBuffer, pts: number, flags: number) { this.bytes = bytes; this.pts = pts; this.flags = flags; }
}

// One selected physical display, one authorized RFB connection, and one native texture.
export class ScreenVideo {
  private active: boolean = true;
  private reader = new ScreenVideoReader();
  private decoder?: ScreenDecoder;
  private pending: Sample[] = [];
  private queuedBytes: number = 0;
  private timer: number = -1;
  private keyNeeded: boolean = true;
  private format?: Sample;
  private width: number = 0;
  private height: number = 0;
  private tier: number = 0;
  private steadySince: number = 0;
  private recoveryAttempts: number = 0;
  private recoveryDeadline: number = 0;
  private outputsToKey: number = 0;
  private nextWake: number = 0;
  private shown: boolean = false;
  private presented: number = 0;
  private resyncs: number = 0;
  private received: number = 0;
  private lastMessage: number = Date.now();
  private started: number = Date.now();
  private outstanding: number = 0;
  private lastPaint: number = 0;
  private lastProgress: number = 0;
  private codecs: string[] = [];

  private socket: HubTransport;
  private surface: string;
  private display: number;
  private aspect: number;
  private current: () => boolean;
  private onPicture: (codec: string) => void;
  private onEnd: () => void;

  constructor(socket: HubTransport, surface: string, display: number, aspect: number, current: () => boolean,
    onPicture: (codec: string) => void, onEnd: () => void) {
    this.socket = socket;
    this.surface = surface;
    this.display = display;
    this.aspect = aspect;
    this.current = current;
    this.onPicture = onPicture;
    this.onEnd = onEnd;
  }
  private codec: string = '';

  async start(): Promise<void> {
    try {
      this.codecs = ['hevc', 'h264'].filter((codec: string) => nativeVideo.supports(codec));
      if (!this.codecs.length) throw new Error('screen_video_decoder');
      await this.socket.connect((): void => this.end(), (state: string): void => {
        if (state === 'closed' || state === 'error' || state === 'offline') this.end();
      }, (bytes: ArrayBuffer): void => {
        if (!this.active || !this.current()) { this.end(); return; }
        try {
          this.reader.push(new Uint8Array(bytes), (type: number, body: Uint8Array): void => {
            if (this.active) this.message(type, body);
          });
        } catch (_) { this.end(); }
      });
      if (!this.active || !this.current()) { this.end(); return; }
      const start = JSON.stringify({ display: this.display, max_size: 1920, fps: FRAME_RATES[0], max_kbps: BIT_RATES[0], codecs: this.codecs });
      if (!this.socket.sendBinary(videoPacket(16, util.TextEncoder.create('utf-8').encodeInto(start)))) throw new Error('screen_video_start');
      this.schedule();
    } catch (_) { this.end(); }
  }

  private message(type: number, body: Uint8Array): void {
    this.lastMessage = Date.now();
    this.received += 5 + body.byteLength;
    if (type === 5) { this.end(); return; } // Lock, permission loss, or display removal.
    if (type === 1) {
      const hello = Wire.parse(util.TextDecoder.create('utf-8').decodeToString(body));
      const codec = Wire.text(hello, 'codec'), w = Wire.number(hello, 'width'), h = Wire.number(hello, 'height');
      if (Wire.number(hello, 'version') !== 1 || Wire.number(hello, 'display', -1) !== this.display ||
        !this.codecs.includes(codec) || !(w > 0 && h > 0) || Math.abs(w / h / this.aspect - 1) > 0.04) {
        throw new Error('screen_video_hello');
      }
      this.codec = codec;
      this.width = w;
      this.height = h;
      this.format = undefined;
      this.replaceDecoder();
    } else if (type === 2) {
      if (!this.decoder) throw new Error('screen_video_format');
      // A new format is followed by its own keyframe. Never feed old queued pictures after it.
      this.format = new Sample(videoAccessUnit(body, true), 0, 8);
      this.waitForKey();
      this.pump();
      this.schedule();
    } else if (type === 3) {
      if (!this.format || body.length <= 13) throw new Error('screen_video_sample');
      const key = (body[12] & 1) !== 0;
      this.report(body);
      if (this.keyNeeded && !key) return;
      if (this.pending.length >= MAX_PENDING_FRAMES || this.queuedBytes + body.length - 13 > MAX_PENDING_BYTES) {
        this.recover(false);
        if (!key) return;
      }
      this.keyNeeded = false;
      const header = new DataView(body.buffer, body.byteOffset, body.byteLength);
      const pts = header.getUint32(4) * 4294967296 + header.getUint32(8);
      const sample = new Sample(videoAccessUnit(body.subarray(13), false), pts, key ? 2 : 0);
      if (!this.pending.length && !this.outstanding) this.lastProgress = Date.now();
      this.pending.push(sample);
      this.queuedBytes += sample.bytes.byteLength;
      this.pump();
      this.schedule();
    }
  }

  private report(body: Uint8Array): void {
    const report = new Uint8Array(25);
    report.set(body.subarray(0, 12));
    const view = new DataView(report.buffer);
    const arrival = systemDateTime.getUptime(systemDateTime.TimeType.STARTUP) * 1000;
    view.setUint32(12, Math.floor(arrival / 4294967296)); view.setUint32(16, arrival >>> 0);
    view.setUint32(20, this.received);
    this.received = 0;
    if (!this.socket.sendBinary(videoPacket(17, report))) throw new Error('screen_video_report');
  }

  private setTier(next: number): void {
    if (next === this.tier) return;
    const update = JSON.stringify({ fps: FRAME_RATES[next], max_kbps: BIT_RATES[next], target_kbps: BIT_RATES[next] });
    if (!this.socket.sendBinary(videoPacket(19, util.TextEncoder.create('utf-8').encodeInto(update)))) {
      throw new Error('screen_video_update');
    }
    this.tier = next;
    hilog.info(0x0000, 'PersonaScreen', 'video target %{public}d fps', FRAME_RATES[next]);
  }

  private waitForKey(): void {
    this.pending = this.format ? [this.format] : [];
    this.queuedBytes = this.format?.bytes.byteLength || 0;
    this.keyNeeded = true;
    this.outputsToKey = 0;
    this.lastProgress = Date.now();
  }

  private replaceDecoder(): void {
    if (this.decoder) nativeVideo.close(this.decoder);
    this.decoder = undefined;
    this.outstanding = 0;
    this.waitForKey();
    this.decoder = nativeVideo.create(this.codec, this.surface, this.width, this.height);
    if (!this.decoder) throw new Error('screen_video_decoder');
  }

  private recover(restart: boolean): void {
    if (!this.format || this.recoveryAttempts >= MAX_RECOVERY_ATTEMPTS) throw new Error('screen_video_recovery');
    this.recoveryAttempts++;
    this.resyncs++;
    this.steadySince = 0;
    this.setTier(Math.min(this.tier + 1, FRAME_RATES.length - 1));
    // Keep displaying the last valid picture. The first queue overflow only discards unsent
    // dependencies. Rebuild a stuck decoder if a fresh keyframe did not restore output.
    if (restart || this.recoveryAttempts > 1) this.replaceDecoder();
    else this.waitForKey();
    if (!this.socket.sendBinary(videoPacket(18, new Uint8Array(0)))) throw new Error('screen_video_keyframe');
    this.recoveryDeadline = Date.now() + KEYFRAME_TIMEOUT_MS;
    hilog.info(0x0000, 'PersonaScreen', 'video resync attempt %{public}d', this.recoveryAttempts);
    this.schedule();
  }

  private pump(): void {
    if (!this.decoder) return;
    while (this.pending.length) {
      const sample = this.pending[0];
      const sent = nativeVideo.submit(this.decoder, sample.bytes, sample.pts, sample.flags);
      if (sent < 0) { this.recover(true); return; }
      if (!sent) break;
      if (sample.flags !== 8) {
        if (!this.outstanding) this.lastProgress = Date.now();
        this.outstanding++;
        // Both Mac encoders emit frames in presentation order. Count through the new key,
        // including any pictures the native renderer releases while catching up.
        if (this.recoveryDeadline && sample.flags === 2 && !this.outputsToKey) this.outputsToKey = this.outstanding;
      }
      this.queuedBytes -= sample.bytes.byteLength;
      this.pending.shift();
    }
    const rendered = nativeVideo.render(this.decoder);
    if (rendered < 0) { this.recover(true); return; }
    if (!rendered) return;
    this.presented++;
    const now = Date.now();
    this.outstanding = Math.max(0, this.outstanding - rendered);
    this.lastProgress = now;
    if (this.outputsToKey) {
      this.outputsToKey = Math.max(0, this.outputsToKey - rendered);
      if (!this.outputsToKey) {
        this.recoveryDeadline = 0;
        this.recoveryAttempts = 0;
        hilog.info(0x0000, 'PersonaScreen', 'video resync complete');
      }
    }
    if (!this.recoveryDeadline && !this.shown) {
      this.shown = true;
      hilog.info(0x0000, 'PersonaScreen', 'first video output after %{public}d ms', now - this.started);
      this.onPicture(this.codec);
    }
    if (this.recoveryDeadline || this.pending.length > 1 || this.outstanding > 3 || now - this.lastPaint > 1000) {
      this.steadySince = 0;
    } else if (!this.steadySince) {
      this.steadySince = now;
    } else if (this.tier && now - this.steadySince >= STABLE_VIDEO_MS) {
      this.setTier(this.tier - 1);
      this.steadySince = now;
    }
    this.lastPaint = now;
  }

  private schedule(): void {
    if (!this.active) return;
    const now = Date.now(), delay = this.pending.length || this.outstanding ? 8 : 250;
    if (this.timer !== -1 && this.nextWake <= now + delay) return;
    if (this.timer !== -1) clearTimeout(this.timer);
    this.nextWake = now + delay;
    this.timer = setTimeout((): void => {
      this.timer = -1;
      if (!this.current()) { this.end(); return; }
      try {
        this.pump();
        const at = Date.now();
        if (at - this.lastMessage > 6000 || (!this.shown && !this.recoveryDeadline && at - this.started > 6000)) {
          this.end(); return;
        }
        const blocked = (this.pending.length > 0 || this.outstanding > 0) && at - this.lastProgress >= KEYFRAME_TIMEOUT_MS;
        if (this.recoveryDeadline ? at >= this.recoveryDeadline : blocked) {
          this.recover(true);
        }
        this.schedule();
      } catch (_) { this.end(); }
    }, delay);
  }

  close(): void {
    if (!this.active) return;
    this.active = false;
    hilog.info(0x0000, 'PersonaScreen', 'video closed after %{public}d ms, %{public}d pictures, %{public}d resyncs',
      Date.now() - this.started, this.presented, this.resyncs);
    if (this.timer !== -1) clearTimeout(this.timer);
    this.timer = -1;
    this.socket.close();
    if (this.decoder) nativeVideo.close(this.decoder);
    this.decoder = undefined;
    this.pending = [];
    this.format = undefined;
  }

  private end(): void {
    if (!this.active) return;
    this.close();
    this.onEnd();
  }
}
