import { taskpool } from '@kit.ArkTS';
import { RFBFramebuffer, RFBRegion } from '../core/RFBFramebuffer';

@Concurrent
function decodeRegion(input: ArrayBuffer, width: number, height: number, x: number, y: number, w: number, h: number,
  left: number, top: number, cropWidth: number, cropHeight: number): ArrayBuffer {
  const fb = new RFBFramebuffer(width, height, new RFBRegion(left, top, cropWidth, cropHeight));
  fb.drawZRLE(new Uint8Array(input), x, y, w, h);
  return fb.pixels.buffer as ArrayBuffer;
}

export async function decodeScreenPatch(bytes: Uint8Array, rect: RFBRegion, crop: RFBRegion,
  width: number, height: number): Promise<ArrayBuffer> {
  // zlib owns its output stream. Transfer a compact copy of just this rectangle to the worker.
  const input = bytes.slice().buffer as ArrayBuffer;
  const task = new taskpool.Task(decodeRegion, input, width, height, rect.x, rect.y, rect.width, rect.height,
    crop.x, crop.y, crop.width, crop.height);
  task.setTransferList([input]);
  return await taskpool.execute(task) as ArrayBuffer;
}
