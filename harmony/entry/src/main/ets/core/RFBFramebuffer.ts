export class RFBRegion {
  x: number;
  y: number;
  width: number;
  height: number;
  constructor(x: number, y: number, width: number, height: number) {
    this.x = x; this.y = y; this.width = width; this.height = height;
  }
}

/** Bounded damage in backing-pixel coordinates. Keep separated old and new window positions separated. */
export class RFBDamage {
  private regions: RFBRegion[] = [];

  add(rect: RFBRegion): void {
    if (!rect.width || !rect.height) return;
    let merged = rect;
    for (let i = this.regions.length - 1; i >= 0; i--) {
      const old = this.regions[i];
      const x = Math.min(old.x, merged.x), y = Math.min(old.y, merged.y);
      const width = Math.max(old.x + old.width, merged.x + merged.width) - x;
      const height = Math.max(old.y + old.height, merged.y + merged.height) - y;
      if (this.regions.length < 16 && width * height > old.width * old.height + merged.width * merged.height + 256) continue;
      this.regions.splice(i, 1);
      merged = new RFBRegion(x, y, width, height);
    }
    this.regions.push(merged);
  }

  take(): RFBRegion[] { const result = this.regions; this.regions = []; return result; }
}

/** Coordinates on the wire stay in Mac pixels. Only the selected display is backed by a phone bitmap. */
export class RFBFramebuffer {
  static readonly maxDimension: number = 65535;
  static readonly maxStoredPixels: number = 8 * 1024 * 1024;
  width: number = 0;
  height: number = 0;
  view: RFBRegion = new RFBRegion(0, 0, 0, 0);
  step: number = 1;
  storedWidth: number = 0;
  storedHeight: number = 0;
  pixels: Uint8Array = new Uint8Array(0);
  private words: Uint32Array = new Uint32Array(0);
  revision: number = 0;
  damage: RFBDamage = new RFBDamage();

  constructor(width: number, height: number, region?: RFBRegion) { this.resize(width, height, region); }

  static validSize(width: number, height: number): boolean {
    return Number.isInteger(width) && Number.isInteger(height) && width > 0 && height > 0 &&
      width <= RFBFramebuffer.maxDimension && height <= RFBFramebuffer.maxDimension;
  }

  resize(width: number, height: number, region?: RFBRegion): boolean {
    if (!RFBFramebuffer.validSize(width, height)) throw new Error('rfb_size_limit');
    const view = region || new RFBRegion(0, 0, width, height);
    if (!RFBFramebuffer.validSize(view.width, view.height) || !Number.isInteger(view.x) || !Number.isInteger(view.y) ||
      view.x < 0 || view.y < 0 || view.x + view.width > width || view.y + view.height > height) throw new Error('rfb_bad_rect');
    if (width === this.width && height === this.height && this.view.x === view.x && this.view.y === view.y &&
      this.view.width === view.width && this.view.height === view.height) return false;
    this.width = width; this.height = height; this.view = view;
    this.step = Math.max(1, Math.ceil(Math.sqrt(view.width * view.height / RFBFramebuffer.maxStoredPixels)));
    while (Math.ceil(view.width / this.step) * Math.ceil(view.height / this.step) > RFBFramebuffer.maxStoredPixels) this.step++;
    this.storedWidth = Math.ceil(view.width / this.step);
    this.storedHeight = Math.ceil(view.height / this.step);
    this.words = new Uint32Array(this.storedWidth * this.storedHeight);
    this.words.fill(0xff000000);
    this.pixels = new Uint8Array(this.words.buffer);
    this.revision++;
    this.damage = new RFBDamage();
    this.damage.add(new RFBRegion(0, 0, this.storedWidth, this.storedHeight));
    return true;
  }

  canCopy(): boolean { return this.step === 1 && this.view.width === this.width && this.view.height === this.height; }

  inside(x: number, y: number, width: number, height: number): boolean {
    return Number.isInteger(x) && Number.isInteger(y) && Number.isInteger(width) && Number.isInteger(height) &&
      x >= 0 && y >= 0 && width > 0 && height > 0 && x + width <= this.width && y + height <= this.height;
  }

  intersects(x: number, y: number, width: number, height: number): boolean {
    return x < this.view.x + this.view.width && y < this.view.y + this.view.height &&
      x + width > this.view.x && y + height > this.view.y;
  }

  requireRect(x: number, y: number, width: number, height: number): void {
    if (!this.inside(x, y, width, height)) throw new Error('rfb_bad_rect');
  }

  private firstColumn(x: number): number { return Math.max(0, Math.min(this.storedWidth, Math.ceil((x - this.view.x) / this.step))); }
  private firstRow(y: number): number { return Math.max(0, Math.min(this.storedHeight, Math.ceil((y - this.view.y) / this.step))); }

  changed(x: number, y: number, width: number, height: number): void {
    const left = this.firstColumn(x), top = this.firstRow(y);
    this.damage.add(new RFBRegion(left, top, this.firstColumn(x + width) - left, this.firstRow(y + height) - top));
  }

  applyDecoded(rect: RFBRegion, bytes: Uint8Array): void {
    const left = rect.x - this.view.x, top = rect.y - this.view.y;
    if (this.step !== 1 || left < 0 || top < 0 || left + rect.width > this.storedWidth || top + rect.height > this.storedHeight ||
      bytes.length !== rect.width * rect.height * 4) throw new Error('rfb_bad_rect');
    if (rect.width === this.storedWidth) { this.pixels.set(bytes, top * this.storedWidth * 4); return; }
    const stride = rect.width * 4;
    for (let y = 0; y < rect.height; y++)
      this.pixels.set(bytes.subarray(y * stride, (y + 1) * stride), ((top + y) * this.storedWidth + left) * 4);
  }

  capture(region: RFBRegion, step: number = 1): ArrayBuffer {
    if (step > 1) {
      const output = new Uint32Array(region.width * region.height);
      for (let y = 0; y < region.height; y++) {
        let src = ((region.y + y) * this.storedWidth + region.x) * step;
        let dst = y * region.width;
        for (let x = 0; x < region.width; x++) { output[dst++] = this.words[src]; src += step; }
      }
      return output.buffer as ArrayBuffer;
    }
    const stride = region.width * 4;
    if (region.width === this.storedWidth)
      return this.pixels.buffer.slice(region.y * stride, (region.y + region.height) * stride) as ArrayBuffer;
    const out = new Uint8Array(stride * region.height);
    for (let y = 0; y < region.height; y++) {
      const at = ((region.y + y) * this.storedWidth + region.x) * 4;
      out.set(this.pixels.subarray(at, at + stride), y * stride);
    }
    return out.buffer as ArrayBuffer;
  }

  raw(x: number, y: number, width: number, height: number, data: Uint8Array): void {
    this.requireRect(x, y, width, height);
    if (data.length !== width * height * 4) throw new Error('rfb_bad_rect');
    this.writePixels(x, y, width, height, data, 0, 4);
  }

  private writePixels(x: number, y: number, width: number, height: number, data: Uint8Array, offset: number, stride: number): void {
    const left = this.firstColumn(x), right = this.firstColumn(x + width);
    const top = this.firstRow(y), bottom = this.firstRow(y + height);
    for (let row: number = top; row < bottom; row++) {
      let source: number = offset + ((this.view.y + row * this.step - y) * width + this.view.x + left * this.step - x) * stride;
      let destination: number = row * this.storedWidth + left;
      for (let column: number = left; column < right; column++) {
        this.words[destination++] = (0xff000000 | (data[source + 2] << 16) | (data[source + 1] << 8) | data[source]) >>> 0;
        source += stride * this.step;
      }
    }
  }

  copy(x: number, y: number, width: number, height: number, sourceX: number, sourceY: number): void {
    this.requireRect(x, y, width, height);
    this.requireRect(sourceX, sourceY, width, height);
    if (!this.canCopy()) throw new Error('rfb_bad_rect');
    const backwards: boolean = sourceY < y;
    for (let n: number = 0; n < height; n++) {
      const row: number = backwards ? height - 1 - n : n;
      const source: number = (sourceY + row) * this.width + sourceX;
      this.words.copyWithin((y + row) * this.width + x, source, source + width);
    }
  }

  fill(x: number, y: number, width: number, height: number, blue: number, green: number, red: number): void {
    this.requireRect(x, y, width, height);
    this.fillWords(x, y, width, height, (0xff000000 | (red << 16) | (green << 8) | blue) >>> 0);
  }

  private fillWords(x: number, y: number, width: number, height: number, color: number): void {
    const left = this.firstColumn(x), right = this.firstColumn(x + width);
    const bottom = this.firstRow(y + height);
    for (let row: number = this.firstRow(y); row < bottom; row++)
      this.words.fill(color, row * this.storedWidth + left, row * this.storedWidth + right);
  }

  private paintRun(x: number, y: number, width: number, offset: number, length: number, value: number): void {
    let column = offset % width, row = Math.floor(offset / width);
    if (length === 1) { this.putWord(x + column, y + row, value); return; }
    while (length > 0) {
      const count = Math.min(length, width - column);
      this.fillWords(x + column, y + row, count, 1, value);
      length -= count; row++; column = 0;
    }
  }

  set(x: number, y: number, blue: number, green: number, red: number): void {
    if (x < 0 || y < 0 || x >= this.width || y >= this.height) throw new Error('rfb_bad_rect');
    this.putWord(x, y, (0xff000000 | (red << 16) | (green << 8) | blue) >>> 0);
  }

  private putWord(x: number, y: number, value: number): void {
    const column: number = x - this.view.x, row: number = y - this.view.y;
    if (column < 0 || row < 0 || column >= this.view.width || row >= this.view.height || column % this.step || row % this.step) return;
    this.words[(row / this.step) * this.storedWidth + column / this.step] = value;
  }

  pixel(x: number, y: number): number[] {
    if (!this.intersects(x, y, 1, 1)) return [];
    const at: number = (Math.floor((y - this.view.y) / this.step) * this.storedWidth + Math.floor((x - this.view.x) / this.step)) * 4;
    return [this.pixels[at], this.pixels[at + 1], this.pixels[at + 2], this.pixels[at + 3]];
  }

  /** Decodes the tile subencodings used by RFB ZRLE into the same persistent framebuffer. */
  drawZRLE(data: Uint8Array, x: number, y: number, width: number, height: number): void {
    this.requireRect(x, y, width, height);
    let at: number = 0;
    const byte = (): number => {
      if (at >= data.length) throw new Error('rfb_zrle_tile');
      const value: number = data[at];
      at++;
      return value;
    };
    const color = (): number => {
      if (at + 3 > data.length) throw new Error('rfb_zrle_tile');
      const value: number = (0xff000000 | (data[at + 2] << 16) | (data[at + 1] << 8) | data[at]) >>> 0;
      at += 3;
      return value;
    };
    const runLength = (): number => {
      let length: number = 1;
      let next: number = 0;
      do {
        next = byte();
        length += next;
        if (length > 64 * 64) throw new Error('rfb_zrle_tile');
      } while (next === 255);
      return length;
    };
    let tileY: number = y;
    while (tileY < y + height) {
      const tileHeight: number = Math.min(64, y + height - tileY);
      let tileX: number = x;
      while (tileX < x + width) {
        const tileWidth: number = Math.min(64, x + width - tileX);
        const visible = this.intersects(tileX, tileY, tileWidth, tileHeight);
        const subencoding: number = byte();
        if (subencoding === 0) {
          if (at + tileWidth * tileHeight * 3 > data.length) throw new Error('rfb_zrle_tile');
          this.writePixels(tileX, tileY, tileWidth, tileHeight, data, at, 3);
          at += tileWidth * tileHeight * 3;
        } else if (subencoding === 1) {
          const value = color();
          if (visible) this.fillWords(tileX, tileY, tileWidth, tileHeight, value);
        } else if (subencoding >= 2 && subencoding <= 16) {
          const palette = new Uint32Array(subencoding);
          for (let item: number = 0; item < subencoding; item++) palette[item] = color();
          const bits: number = subencoding === 2 ? 1 : subencoding <= 4 ? 2 : 4;
          const mask: number = (1 << bits) - 1;
          const direct = visible && this.step === 1 && tileX >= this.view.x && tileY >= this.view.y &&
            tileX + tileWidth <= this.view.x + this.view.width && tileY + tileHeight <= this.view.y + this.view.height;
          for (let row: number = 0; row < tileHeight; row++) {
            const destination = (tileY + row - this.view.y) * this.storedWidth + tileX - this.view.x;
            let packed: number = 0;
            let left: number = 0;
            for (let column: number = 0; column < tileWidth; column++) {
              if (left === 0) { packed = byte(); left = 8; }
              left -= bits;
              const index: number = (packed >>> left) & mask;
              if (index >= palette.length) throw new Error('rfb_zrle_tile');
              const value = palette[index];
              if (direct) this.words[destination + column] = value;
              else if (visible) this.putWord(tileX + column, tileY + row, value);
            }
          }
        } else if (subencoding === 128) {
          let pixel: number = 0;
          const count: number = tileWidth * tileHeight;
          while (pixel < count) {
            const value = color();
            const length: number = runLength();
            if (length > count - pixel) throw new Error('rfb_zrle_tile');
            if (visible) this.paintRun(tileX, tileY, tileWidth, pixel, length, value);
            pixel += length;
          }
        } else if (subencoding >= 130) {
          const paletteSize: number = subencoding - 128;
          const palette = new Uint32Array(paletteSize);
          for (let item: number = 0; item < paletteSize; item++) palette[item] = color();
          let pixel: number = 0;
          const count: number = tileWidth * tileHeight;
          while (pixel < count) {
            const packed: number = byte();
            const index: number = packed & 127;
            if (index >= palette.length) throw new Error('rfb_zrle_tile');
            const length: number = (packed & 128) !== 0 ? runLength() : 1;
            if (length > count - pixel) throw new Error('rfb_zrle_tile');
            if (visible) this.paintRun(tileX, tileY, tileWidth, pixel, length, palette[index]);
            pixel += length;
          }
        } else throw new Error('rfb_zrle_tile');
        tileX += 64;
      }
      tileY += 64;
    }
  }
}
