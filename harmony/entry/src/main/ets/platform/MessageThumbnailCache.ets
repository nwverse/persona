import { common } from '@kit.AbilityKit';
import { util } from '@kit.ArkTS';
import { cryptoFramework } from '@kit.CryptoArchitectureKit';
import { fileIo } from '@kit.CoreFileKit';
import { isMessageThumbnailName } from '../core/AttachmentTypes';

export const thumbnailByteLimit: number = 192 * 1024;
const thumbnailCountLimit: number = 48;

/** The app-private preview directory is also purged when this Hub is forgotten. */
export function thumbnailCachePath(context: common.UIAbilityContext, hub: string, identity: string): string {
  if (!hub || hub === '.' || hub === '..' || hub.length > 256 || !identity) return '';
  const hash = cryptoFramework.createMd('SHA256');
  hash.updateSync({data:new util.TextEncoder().encodeInto(identity)});
  const name = Array.from(hash.digestSync().data).map((byte: number) => byte.toString(16).padStart(2, '0')).join('');
  return context.cacheDir + '/message-images/' + encodeURIComponent(hub) + '/thumb-' + name + '.img';
}

export function readThumbnail(path: string): ArrayBuffer | undefined {
  if (!path) return undefined;
  let file: fileIo.File | undefined;
  try {
    file = fileIo.openSync(path, fileIo.OpenMode.READ_ONLY);
    const size = fileIo.statSync(file.fd).size;
    if (size <= 0 || size > thumbnailByteLimit) return undefined;
    const bytes = new ArrayBuffer(size);
    return fileIo.readSync(file.fd, bytes) === size ? bytes : undefined;
  } catch (_) { return undefined; } finally { if (file) fileIo.closeSync(file.fd); }
}

// Scrolling uses asynchronous I/O. The small synchronous reader is for an explicit open in the viewer.
export async function readDisplayThumbnail(path: string): Promise<ArrayBuffer | undefined> {
  let file: fileIo.File | undefined;
  try {
    file = await fileIo.open(path, fileIo.OpenMode.READ_ONLY);
    const size = (await fileIo.stat(file.fd)).size;
    if (size <= 0 || size > thumbnailByteLimit) return undefined;
    const bytes = new ArrayBuffer(size);
    return await fileIo.read(file.fd, bytes) === size ? bytes : undefined;
  } catch (_) { return undefined; }
  finally { if (file) { try { await fileIo.close(file.fd); } catch (_) {} } }
}

const pendingWrites: Set<string> = new Set();
interface ThumbnailAge { name: string; mtime: number; }
export async function writeThumbnail(path: string, bytes: ArrayBuffer, valid: () => boolean): Promise<void> {
  if (!path || !bytes.byteLength || bytes.byteLength > thumbnailByteLimit || !valid() || pendingWrites.has(path)) return;
  pendingWrites.add(path);
  const directory = path.slice(0, path.lastIndexOf('/'));
  const temporary = path + '.part';
  let renamed = false;
  try {
    try { await fileIo.stat(directory); } catch (_) { if (!valid()) return; await fileIo.mkdir(directory, true); }
    if (!valid()) return;
    const file = await fileIo.open(temporary, fileIo.OpenMode.CREATE | fileIo.OpenMode.WRITE_ONLY | fileIo.OpenMode.TRUNC);
    try { if (await fileIo.write(file.fd, bytes) !== bytes.byteLength) return; } finally { await fileIo.close(file.fd); }
    if (!valid()) return;
    await fileIo.rename(temporary, path);
    renamed = true;
    const names = (await fileIo.listFile(directory)).filter(name => isMessageThumbnailName(name) && directory + '/' + name !== path);
    if (names.length < thumbnailCountLimit) return;
    const ages = await Promise.all(names.map(async (name: string): Promise<ThumbnailAge> => {
      try { return { name, mtime:(await fileIo.stat(directory + '/' + name)).mtime }; }
      catch (_) { return { name, mtime:0 }; }
    }));
    ages.sort((a,b) => a.mtime - b.mtime);
    for (const old of ages.slice(0, ages.length - thumbnailCountLimit + 1)) {
      if (!valid()) return;
      try { await fileIo.unlink(directory + '/' + old.name); } catch (_) {}
    }
  } catch (_) { /* A cache miss or an eviction race must not interrupt the displayed image. */ }
  finally {
    if (!renamed) { try { await fileIo.unlink(temporary); } catch (_) {} }
    pendingWrites.delete(path);
  }
}
