import type { ClientModel } from './ClientModel';
import { segment } from './HubRepository';

export class MessageImageData {
  data: ArrayBuffer = new ArrayBuffer(0);
  mime: string = '';
  original: boolean = true;
}
/** Both lazy transcript images and mobile photo-note paths use the captured authenticated session owner. */
export async function loadMessageImage(model: ClientModel, itemId: string, path: string, thumbnail: boolean = false, edge: number = 384): Promise<MessageImageData> {
  const repository = model.repo(), generation = model.connectionGeneration(), scope = model.scope(), target = model.target(), row = model.selected;
  const current = (): boolean => model.connected && model.owns(generation, scope) && model.target() === target && model.repo() === repository;
  if (thumbnail && path) {
    try {
      const thumb = await repository.transport.requestBytes('/api/v1/machines/' + segment(row.machine) +
        '/file/thumb?path=' + segment(path) + '&cwd=' + segment(row.path || '~') + '&size=' + (edge === 1024 ? '1024' : '384'));
      if (!current()) throw new Error('request_updated');
      const data = imageResponse(thumb.data, thumb.contentType);
      data.original = false;
      return data;
    } catch (_) {
      if (!current()) throw new Error('request_updated');
      // Older nodes and formats without a thumbnail can still serve the original.
    }
  }
  const route = path ? '/api/v1/machines/' + segment(row.machine) + '/file/download?path=' + segment(path) + '&cwd=' + segment(row.path || '~') :
    '/api/v1/sessions/' + segment(row.machine) + '/' + segment(row.session || row.id) + '/items/' + segment(itemId) + '/image';
  const response = await repository.transport.requestBytes(route);
  if (!current()) throw new Error('request_updated');
  return imageResponse(response.data, response.contentType);
}
function imageResponse(bytes: ArrayBuffer, contentType: string): MessageImageData {
  let mime = contentType.split(';')[0].trim().toLowerCase();
  if (!mime || mime === 'application/octet-stream') {
    const b = new Uint8Array(bytes, 0, Math.min(bytes.byteLength, 12));
    if (b[0] === 0x89 && b[1] === 0x50 && b[2] === 0x4e && b[3] === 0x47 && b[4] === 13 && b[5] === 10 && b[6] === 26 && b[7] === 10) mime = 'image/png';
    else if (b[0] === 0x47 && b[1] === 0x49 && b[2] === 0x46 && b[3] === 0x38 && (b[4] === 0x37 || b[4] === 0x39) && b[5] === 0x61) mime = 'image/gif';
    else if (b[0] === 0xff && b[1] === 0xd8 && b[2] === 0xff) mime = 'image/jpeg';
  }
  if (!['image/jpeg', 'image/png', 'image/webp', 'image/gif', 'image/heif', 'image/heic'].includes(mime) || !bytes.byteLength || bytes.byteLength > 32 * 1024 * 1024) throw new Error('preview_unavailable');
  const result = new MessageImageData(); result.data = bytes; result.mime = mime; return result;
}
