import type { ToolTurnProjection } from './Domain';

export const MAX_MESSAGE_IMAGE_GALLERY_COUNT: number = 16;
export const MAX_MESSAGE_IMAGE_GALLERY_BYTES: number = 64 * 1024 * 1024;

/** An authenticated image source in the current conversation transcript. */
export interface MessageImageGalleryEntry {
  key: string;
  itemId: string;
  remotePath: string;
  target: string;
}

/** A loaded image keeps its exact transcript source and authenticated chat target. */
export interface LoadedMessageImage {
  source: MessageImageGalleryEntry;
  target: string;
  scope: string;
  generation: number;
  hub: string;
  data: ArrayBuffer;
  mime: string;
}

export interface MessageImageGalleryWindow {
  entries: MessageImageGalleryEntry[];
  selectedIndex: number;
  signature: string;
}

export interface ConversationPhotoRequest {
  gallery: MessageImageGalleryWindow;
  scope: string;
  hub: string;
  thumbnail?: ArrayBuffer;
  original?: LoadedMessageImage;
}

/** Keep the selected source in a bounded native gallery, centered where possible. */
export function messageImageGalleryWindow(entries: MessageImageGalleryEntry[], selectedIndex: number): MessageImageGalleryWindow | undefined {
  if (!Number.isSafeInteger(selectedIndex) || selectedIndex < 0 || selectedIndex >= entries.length) return undefined;
  const count = Math.min(entries.length, MAX_MESSAGE_IMAGE_GALLERY_COUNT);
  const start = Math.max(0, Math.min(selectedIndex - Math.floor(count / 2), entries.length - count));
  const selected: MessageImageGalleryEntry[] = [];
  const keys = new Set<string>();
  for (let index = start; index < start + count; index++) {
    const entry = entries[index];
    if (!entry || !entry.key || (!entry.itemId && !entry.remotePath) || !entry.target || keys.has(entry.key)) return undefined;
    keys.add(entry.key);
    selected.push({ key: entry.key, itemId: entry.itemId, remotePath: entry.remotePath, target: entry.target });
  }
  const localIndex = selectedIndex - start;
  const signature = JSON.stringify([selected.map((entry: MessageImageGalleryEntry): Object =>
    [entry.key, entry.itemId, entry.remotePath, entry.target]), localIndex]);
  return { entries: selected, selectedIndex: localIndex, signature: signature };
}

export function sameMessageImageSource(left: MessageImageGalleryEntry, right: MessageImageGalleryEntry): boolean {
  return left.key === right.key && left.itemId === right.itemId && left.remotePath === right.remotePath && left.target === right.target;
}

interface UserImagePart {
  role: string;
  ownerTarget: string;
  itemId: string;
  imageId: string;
  imageIds: string[];
}

/** The provider writes a single prompt as entry#0, entry#1, etc. Keep its pictures above its first text block. */
export function groupUserImages<T extends UserImagePart>(messages: T[]): T[] {
  const result: T[] = [];
  for (let i = 0; i < messages.length;) {
    const first = messages[i];
    const part = first.role === 'user' ? /^(.*)#\d+$/.exec(first.itemId) : null;
    let end = i + 1;
    if (part) {
      const prefix = part[1] + '#';
      while (end < messages.length && messages[end].role === 'user' && messages[end].ownerTarget === first.ownerTarget &&
        messages[end].itemId.startsWith(prefix) && /^\d+$/.test(messages[end].itemId.slice(prefix.length))) end++;
    }
    if (end === i + 1) { result.push(first); i = end; continue; }
    const siblings = messages.slice(i, end);
    const pictures = siblings.filter(message => !!message.imageId);
    if (pictures.length) {
      const anchor = siblings.find(message => !message.imageId) || first;
      anchor.imageIds = pictures.map(message => message.imageId);
      for (const message of siblings) if (message === anchor || !message.imageId) result.push(message);
    } else result.push(...siblings);
    i = end;
  }
  return result;
}

export interface ConversationImageMessage {
  work?: ToolTurnProjection;
  id: string;
  ownerTarget: string;
  imageId: string;
  imageIds?: readonly string[];
  imagePaths: string[];
}
export function conversationImageKey(target: string, messageId: string, kind: string, source: string, index: number = 0): string {
  return JSON.stringify([target, messageId, kind, source, index]);
}
/** Paths are local to the session machine. Inline images use their original transcript item IDs. */
export function messageAttachmentImages(message: ConversationImageMessage, target: string): MessageImageGalleryEntry[] {
  const entries: MessageImageGalleryEntry[] = [];
  if (!target || message.ownerTarget !== target) return entries;
  for (let index = 0; index < message.imagePaths.length; index++) {
    const path = message.imagePaths[index];
    if (path) entries.push({key: conversationImageKey(target, message.id, 'path', path, index), itemId:'', remotePath:path, target});
  }
  for (const itemId of message.imageIds?.length ? message.imageIds : message.imageId ? [message.imageId] : []) {
    entries.push({key: conversationImageKey(target, message.id, 'item', itemId), itemId, remotePath:'', target});
  }
  return entries;
}

/** The original fits inside its compact slot, including portrait screenshots and unusually wide panoramas. */
export function fittedThumbnail(ratio: number, maxWidth: number, maxHeight: number): number[] {
  const aspect = Number.isFinite(ratio) && ratio > 0 ? ratio : 1;
  const width = Math.min(Math.max(1, maxWidth), Math.max(1, maxHeight) * aspect);
  return [width, width / aspect];
}

export function attachmentFileName(path: string): string {
  const clean = path.replace(/[\\/]+$/, '');
  return clean.split(/[\\/]/).pop() || clean;
}

/** Preserve transcript order while excluding messages belonging to a retired chat owner. */
export function conversationImageEntries(messages: ConversationImageMessage[], target: string): MessageImageGalleryEntry[] {
  const entries: MessageImageGalleryEntry[] = [];
  for (const message of messages) {
    if (!target || message.ownerTarget !== target) continue;
    const attachments = messageAttachmentImages(message, target);
    if (!message.work) { entries.push(...attachments); continue; }
    entries.push(...attachments.filter(entry => !!entry.remotePath));
    for (const step of message.work.inspectionSteps) {
      if (step.kind !== 'tool') continue;
      for (const item of step.images) entries.push({ key: conversationImageKey(target, message.id, 'tool', item.id), itemId: item.id, remotePath: '', target });
    }
    entries.push(...attachments.filter(entry => !!entry.itemId));
  }
  return entries;
}
