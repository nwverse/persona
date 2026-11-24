const MAX_MESSAGE = 4 * 1024 * 1024;

// A message can span several WebSocket payloads. Allocate once when its length is known.
export class ScreenVideoReader {
  private header = new Uint8Array(4);
  private prefix: number = 0;
  private body?: Uint8Array;
  private used: number = 0;

  push(bytes: Uint8Array, emit: (type: number, body: Uint8Array) => void): void {
    let pos = 0;
    while (pos < bytes.length) {
      if (!this.body) {
        while (this.prefix < 4 && pos < bytes.length) this.header[this.prefix++] = bytes[pos++];
        if (this.prefix < 4) return;
        const size = new DataView(this.header.buffer).getUint32(0);
        if (size < 1 || size > MAX_MESSAGE) throw new Error('screen_video_size');
        this.body = new Uint8Array(size);
        this.used = 0;
        this.prefix = 0;
      }
      const n = Math.min(this.body.length - this.used, bytes.length - pos);
      this.body.set(bytes.subarray(pos, pos + n), this.used);
      this.used += n;
      pos += n;
      if (this.used === this.body.length) {
        const complete = this.body;
        this.body = undefined;
        emit(complete[0], complete.subarray(1));
      }
    }
  }
}

export function videoPacket(type: number, payload: Uint8Array): ArrayBuffer {
  const bytes = new Uint8Array(payload.length + 5);
  new DataView(bytes.buffer).setUint32(0, payload.length + 1);
  bytes[4] = type;
  bytes.set(payload, 5);
  return bytes.buffer;
}

// The helper uses AVCC lengths. The native video decoder accepts Annex B access units.
export function videoAccessUnit(bytes: Uint8Array, format: boolean): ArrayBuffer {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const prefix = format ? 2 : 4;
  let offset = format ? 1 : 0;
  const count = format ? bytes[0] : 0;
  if (format && (!count || count > 16)) throw new Error('screen_video_format');
  const out = new Uint8Array(format ? bytes.length - 1 + count * 2 : bytes.length);
  let written = 0, units = 0;
  while (offset < bytes.length) {
    if (offset + prefix > bytes.length) throw new Error('screen_video_unit');
    const size = format ? view.getUint16(offset) : view.getUint32(offset);
    offset += prefix;
    if (size === 0 || size > bytes.length - offset || (format && units >= count)) throw new Error('screen_video_unit');
    out[written + 3] = 1;
    out.set(bytes.subarray(offset, offset + size), written + 4);
    offset += size;
    written += size + 4;
    units++;
  }
  if (!units || (format && count !== units)) throw new Error('screen_video_unit');
  return out.buffer;
}
