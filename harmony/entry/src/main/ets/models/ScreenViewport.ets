export class ScreenPoint {
  x: number;
  y: number;
  constructor(x: number, y: number) { this.x = x; this.y = y; }
}

export class ScreenRect {
  x: number;
  y: number;
  width: number;
  height: number;
  constructor(x: number = 0, y: number = 0, width: number = 0, height: number = 0) {
    this.x = x; this.y = y; this.width = width; this.height = height;
  }
}

type SendPointer = (x: number, y: number, buttons: number) => void;

/** The image and remote input share one rectangle in the untransformed touch surface's coordinates. */
@ObservedV2
export class ScreenViewport {
  rect: ScreenRect = new ScreenRect();
  zoom: number = 1;
  @Trace displayed: ScreenRect = this.rect;
  @Trace displayedZoom: number = 1;
  @Trace zoomPercent: number = 100;
  @Trace fitWidth: number = 0;
  @Trace fitHeight: number = 0;
  private width: number = 0;
  private height: number = 0;
  private aspect: number = 1;
  private mode: string = 'idle';
  private origin: ScreenPoint = new ScreenPoint(0, 0);
  private anchor: ScreenPoint = new ScreenPoint(0.5, 0.5);
  private spread: number = 0;
  private pinching: boolean = false;
  private downAt: number = 0;
  private startZoom: number = 1;
  private startRect: ScreenRect = new ScreenRect();

  layout(width: number, height: number, aspect: number): void {
    if (width <= 0 || height <= 0 || aspect <= 0 || !Number.isFinite(aspect)) return;
    if (width === this.width && height === this.height && aspect === this.aspect) return;
    const middle = this.unit(this.width / 2, this.height / 2);
    this.width = width; this.height = height; this.aspect = aspect;
    this.fitWidth = Math.min(width, height * aspect);
    this.fitHeight = this.fitWidth / aspect;
    this.place(this.zoom, middle, width / 2, height / 2);
    this.publish();
  }

  reset(): void {
    this.mode = 'idle';
    this.place(1, new ScreenPoint(0.5, 0.5), this.width / 2, this.height / 2);
    this.publish();
  }

  cancel(): void { this.mode = 'idle'; }

  movingPicture(): boolean { return this.mode === 'two' || this.zoom > 1 && (this.mode === 'pan' || this.mode === 'pending'); }

  publish(): void {
    if (this.rect !== this.displayed) { this.displayed = this.rect; this.displayedZoom = this.zoom; }
    const percent = Math.round(this.zoom * 100);
    if (!this.movingPicture() || Math.abs(percent - this.zoomPercent) >= 10 || percent === 100) this.zoomPercent = percent;
  }

  private unit(x: number, y: number): ScreenPoint {
    return this.rect.width && this.rect.height ?
      new ScreenPoint((x - this.rect.x) / this.rect.width, (y - this.rect.y) / this.rect.height) : new ScreenPoint(0.5, 0.5);
  }

  private place(zoom: number, anchor: ScreenPoint, x: number, y: number): void {
    this.zoom = Math.min(5, Math.max(1, zoom));
    const w = this.fitWidth * this.zoom, h = this.fitHeight * this.zoom;
    const left = this.axis(x - anchor.x * w, this.width - w);
    const top = this.axis(y - anchor.y * h, this.height - h);
    if (this.rect.x !== left || this.rect.y !== top || this.rect.width !== w || this.rect.height !== h)
      this.rect = new ScreenRect(left, top, w, h);
  }

  private axis(offset: number, space: number): number {
    if (this.zoom === 1) return space / 2;
    // A wide desktop can still be shorter than a portrait phone. Allow panning within that space too.
    return Math.max(Math.min(0, space), Math.min(Math.max(0, space), offset));
  }

  private sendAt(point: ScreenPoint, buttons: number, send: SendPointer): boolean {
    const unit = this.unit(point.x, point.y);
    if (unit.x < 0 || unit.x >= 1 || unit.y < 0 || unit.y >= 1) return false;
    send(unit.x, unit.y, buttons);
    return true;
  }

  touch(kind: string, point: ScreenPoint, fingers: ScreenPoint[], control: boolean, send: SendPointer, release: () => void,
    now: number = Date.now()): void {
    if (kind === 'cancel') { release(); this.cancel(); return; }
    if (fingers.length >= 2) {
      const a = fingers[0], b = fingers[1];
      const center = new ScreenPoint((a.x + b.x) / 2, (a.y + b.y) / 2);
      const spread = Math.max(1, Math.hypot(b.x - a.x, b.y - a.y));
      if (this.mode !== 'two') {
        release();
        this.mode = 'two'; this.pinching = false; this.anchor = this.unit(center.x, center.y);
        this.startZoom = this.zoom; this.spread = spread;
      } else {
        if (Math.abs(spread - this.spread) >= 2) this.pinching = true;
        const factor = this.pinching ? spread / this.spread : 1;
        this.place(this.startZoom * factor, this.anchor, center.x, center.y);
      }
      return;
    }
    if (this.mode === 'two' || this.mode === 'lifting') {
      if (fingers.length === 1 && this.zoom > 1) {
        this.mode = 'pan'; this.origin = fingers[0]; this.startRect = this.rect;
        return;
      }
      this.mode = fingers.length ? 'lifting' : 'idle';
      return;
    }
    if (kind === 'down') {
      this.origin = point; this.startRect = this.rect; this.mode = 'pending'; this.downAt = now;
      return;
    }
    if (kind === 'move') {
      const dx = point.x - this.origin.x, dy = point.y - this.origin.y;
      if (this.mode === 'pending' && Math.hypot(dx, dy) >= 4) {
        if (control && (this.zoom === 1 || now - this.downAt >= 450) && this.sendAt(this.origin, 1, send)) this.mode = 'drag';
        else this.mode = 'pan';
      }
      if (this.mode === 'drag' && control) this.sendAt(point, 1, send);
      if (this.mode === 'pan' && this.zoom > 1)
        this.place(this.zoom, new ScreenPoint(0, 0), this.startRect.x + dx, this.startRect.y + dy);
      return;
    }
    if (kind === 'up') {
      if (control && this.mode === 'pending' && this.sendAt(point, 1, send)) this.sendAt(point, 0, send);
      else if (this.mode === 'drag' && (!control || !this.sendAt(point, 0, send))) release();
      this.mode = 'idle';
    }
  }
}
