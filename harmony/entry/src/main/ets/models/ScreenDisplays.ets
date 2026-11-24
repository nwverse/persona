import { RFBRegion } from '../core/RFBFramebuffer';
import type { RFBScreenInfo } from '../core/RFBClient';
import { Wire } from './Wire';

export class ScreenDisplay extends RFBRegion {
  key: string;
  main: boolean;
  scale: number;
  constructor(key: string, x: number, y: number, width: number, height: number, main: boolean = false, scale: number = 0) {
    super(x, y, width, height);
    this.key = key; this.main = main; this.scale = scale;
  }
}

function normalize(displays: ScreenDisplay[], width: number, height: number, nativePixels: boolean): ScreenDisplay[] {
  const left = Math.min(...displays.map((d: ScreenDisplay) => d.x));
  const top = Math.min(...displays.map((d: ScreenDisplay) => d.y));
  const boxW = Math.max(...displays.map((d: ScreenDisplay) => d.x + d.width)) - left;
  const boxH = Math.max(...displays.map((d: ScreenDisplay) => d.y + d.height)) - top;
  const sx = nativePixels ? 1 : width / boxW, sy = nativePixels ? 1 : height / boxH;
  if (nativePixels ? Math.abs(boxW - width) > 2 || Math.abs(boxH - height) > 2 : Math.abs(sx / sy - 1) > 0.02) return [];
  return displays.map((d: ScreenDisplay) => {
    const x = Math.round((d.x - left) * sx), y = Math.round((d.y - top) * sy);
    const right = Math.min(width, Math.round((d.x + d.width - left) * sx));
    const bottom = Math.min(height, Math.round((d.y + d.height - top) * sy));
    return new ScreenDisplay(d.key, x, y, right - x, bottom - y, d.main);
  }).filter((d: ScreenDisplay) => d.width > 0 && d.height > 0);
}

// macOS joins the touching edges in native pixels but can keep their sideways offset in desktop points.
// Accept that geometry only when it accounts for every screen and exactly fits the RFB desktop.
function touchingLayout(machine: ScreenDisplay[], width: number, height: number, commonScale: number): ScreenDisplay[] {
  const main = machine.find((d: ScreenDisplay) => d.main);
  if (!main || machine.length < 2 || machine.some((d: ScreenDisplay) => !(d.scale || commonScale))) return [];
  const scale = main.scale || commonScale;
  const placed: ScreenDisplay[] = [new ScreenDisplay(main.key, 0, 0, main.width * scale, main.height * scale, true)];
  const remaining = machine.filter((d: ScreenDisplay) => d !== main);
  const bases: ScreenDisplay[] = [main];
  while (remaining.length) {
    let next: ScreenDisplay | undefined;
    let nextIndex = -1, bestOffset = Infinity;
    // Prefer aligned neighbours. Two screens in the same row can both touch the main
    // screen; placing both relative to it would overlap their backing-pixel rectangles.
    for (let at = 0; at < placed.length; at++) {
      const frame = placed[at], base = bases[at];
      for (let i = 0; i < remaining.length; i++) {
        const d = remaining[i];
        const w = d.width * (d.scale || commonScale), h = d.height * (d.scale || commonScale);
        const dx = d.x - base.x, dy = d.y - base.y;
        let x = frame.x + dx, y = frame.y + dy, offset: number;
        if (dx < base.width && dx > -d.width && (dy === base.height || dy === -d.height)) {
          y = dy > 0 ? frame.y + frame.height : frame.y - h;
          offset = Math.abs(dx);
        } else if (dy < base.height && dy > -d.height && (dx === base.width || dx === -d.width)) {
          x = dx > 0 ? frame.x + frame.width : frame.x - w;
          offset = Math.abs(dy);
        } else continue;
        if (offset >= bestOffset || placed.some((p: ScreenDisplay) =>
          x < p.x + p.width && x + w > p.x && y < p.y + p.height && y + h > p.y)) continue;
        next = new ScreenDisplay(d.key, x, y, w, h, d.main);
        nextIndex = i;
        bestOffset = offset;
      }
    }
    if (!next) return [];
    placed.push(next);
    bases.push(remaining.splice(nextIndex, 1)[0]);
  }
  return normalize(placed, width, height, true);
}

export class ScreenDisplays {
  static machine(response: Record<string, Object>): ScreenDisplay[] {
    const rows = Wire.rows(response, 'displays');
    if (rows.length > 64) return [];
    return rows.map((r: Record<string, Object>) => new ScreenDisplay(Wire.identifier(r, 'id'),
      Wire.number(r, 'x'), Wire.number(r, 'y'), Wire.number(r, 'width'), Wire.number(r, 'height'),
      Wire.bool(r, 'main'), Wire.number(r, 'scale'))).filter((d: ScreenDisplay) => !!d.key && d.width > 0 && d.height > 0);
  }

  static layout(width: number, height: number, server: RFBScreenInfo[], machine: ScreenDisplay[]): ScreenDisplay[] {
    let mapped: ScreenDisplay[] = [];
    if (machine.length > 0) {
      mapped = normalize(machine, width, height, false);
      const commonScale = mapped.length ? mapped[0].width / machine[0].width : 0;
      const joined = touchingLayout(machine, width, height, commonScale);
      if (joined.length) mapped = joined;
      if (!mapped.length && machine.every((d: ScreenDisplay) => d.scale > 0)) {
        // Screen Sharing anchors adjoining edges at the main display's scale on mixed-DPI Macs.
        const main = machine.find((d: ScreenDisplay) => d.main) || machine[0];
        const anchored = machine.map((d: ScreenDisplay) => {
          const w = d.width * d.scale, h = d.height * d.scale;
          const x = d.x + d.width <= main.x ? (d.x + d.width) * main.scale - w : d.x * main.scale;
          const y = d.y + d.height <= main.y ? (d.y + d.height) * main.scale - h : d.y * main.scale;
          return new ScreenDisplay(d.key, x, y, w, h, d.main);
        });
        mapped = normalize(anchored, width, height, true);
      }
    }
    const reported = server.filter((s: RFBScreenInfo) => s.width > 0 && s.height > 0 && s.x >= 0 && s.y >= 0 &&
      s.x + s.width <= width && s.y + s.height <= height);
    if (reported.length > 1) mapped = reported.map((s: RFBScreenInfo) => {
      const sameSize = mapped.filter((d: ScreenDisplay) => Math.abs(d.width - s.width) <= 2 && Math.abs(d.height - s.height) <= 2);
      const named = sameSize.find((d: ScreenDisplay) => Math.abs(d.x - s.x) <= 2 && Math.abs(d.y - s.y) <= 2) ||
        (sameSize.length === 1 ? sameSize[0] : undefined);
      return new ScreenDisplay(named?.key || 'rfb-' + s.id, s.x, s.y, s.width, s.height, named?.main || false);
    });
    return mapped.sort((a: ScreenDisplay, b: ScreenDisplay) => Number(b.main) - Number(a.main) || a.x - b.x || a.y - b.y)
      .filter((d: ScreenDisplay, index: number, all: ScreenDisplay[]) =>
        all.findIndex((s: ScreenDisplay) => s.x === d.x && s.y === d.y && s.width === d.width && s.height === d.height) === index);
  }
}
