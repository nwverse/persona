import { image } from '@kit.ImageKit';
import { RFBDamage, RFBFramebuffer, RFBRegion } from '../core/RFBFramebuffer';

class ScreenBuffer {
  map: image.PixelMap | undefined = undefined;
  pending: RFBDamage = new RFBDamage();
  busy: boolean = false;
  retired: boolean = false;

  release(): void {
    if (this.busy) return;
    this.map?.release();
    this.map = undefined;
  }
}

class ScreenPatch {
  region: RFBRegion;
  pixels: ArrayBuffer;
  constructor(source: RFBFramebuffer, region: RFBRegion, step: number) {
    this.region = region;
    this.pixels = source.capture(region, step);
  }
}

class ScreenResolution {
  readonly step: number;
  readonly buffers: ScreenBuffer[] = [new ScreenBuffer(), new ScreenBuffer()];
  front: number = -1;
  constructor(step: number) { this.step = step; }

  changed(rect: RFBRegion): void {
    const x = Math.ceil(rect.x / this.step), y = Math.ceil(rect.y / this.step);
    const patch = new RFBRegion(x, y, Math.ceil((rect.x + rect.width) / this.step) - x, Math.ceil((rect.y + rect.height) / this.step) - y);
    for (const slot of this.buffers) slot.pending.add(patch);
  }
}

/** Update the back buffer completely before displaying it. Each buffer keeps its own missed rectangles. */
export class ScreenPresenter {
  private resolutions: ScreenResolution[] = [];
  private shown: ScreenResolution | undefined = undefined;
  private source: RFBFramebuffer | undefined = undefined;
  private revision: number = -1;

  clear(): void {
    for (const res of this.resolutions) for (const slot of res.buffers) { slot.retired = true; slot.release(); }
    this.resolutions = [];
    this.shown = undefined;
    this.source = undefined;
  }

  async update(source: RFBFramebuffer, sampleStep: number = 1): Promise<image.PixelMap | undefined> {
    if (source !== this.source || this.revision !== source.revision) {
      this.clear();
      this.source = source;
      this.revision = source.revision;
    }
    const step = sampleStep === 2 ? 2 : 1;
    let res = this.resolutions.find((r: ScreenResolution) => r.step === step);
    if (!res) { res = new ScreenResolution(step); this.resolutions.push(res); }
    const index = res.front === 0 ? 1 : 0;
    const slot = res.buffers[index];
    if (slot.busy) return undefined;
    const changed = source.damage.take();
    if (!changed.length && res === this.shown) return undefined;
    for (const rect of changed) for (const cached of this.resolutions) cached.changed(rect);
    const regions = slot.pending.take();
    slot.busy = true;
    try {
      if (!slot.map) {
        const width = Math.ceil(source.storedWidth / step), height = Math.ceil(source.storedHeight / step);
        slot.map = await image.createPixelMapFromPixels(source.capture(new RFBRegion(0, 0, width, height), step), {
          size: { width: width, height: height }, editable: true,
          srcPixelFormat: image.PixelMapFormat.BGRA_8888, pixelFormat: image.PixelMapFormat.BGRA_8888
        });
      } else {
        // Freeze only the affected pixels before yielding to the next RFB update.
        const patches = regions.map((r: RFBRegion): ScreenPatch => new ScreenPatch(source, r, step));
        for (const patch of patches) {
          if (slot.retired) return undefined;
          const r = patch.region;
          await slot.map.writePixels({ pixels: patch.pixels, offset: 0, stride: r.width * 4,
            region: { x: r.x, y: r.y, size: { width: r.width, height: r.height } } });
        }
      }
      if (slot.retired) return undefined;
      res.front = index;
      this.shown = res;
      return slot.map;
    } catch (error) {
      slot.map?.release();
      slot.map = undefined;
      if (!slot.retired) source.damage.add(new RFBRegion(0, 0, source.storedWidth, source.storedHeight));
      throw error as Error;
    } finally {
      slot.busy = false;
      if (slot.retired) slot.release();
    }
  }
}
