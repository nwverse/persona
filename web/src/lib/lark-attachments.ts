// Pictures and files sent into a Feishu chat (docs/extensions/lark-attachments.md, iOS LarkAttachment.swift): picked, pasted or
// dropped into the chat's composer, checked and previewed there, then sent one by one through the hub's own routes
// (POST /lark/conversations/{feed}/images as JSON with the bytes in base64, …/files as the raw bytes). A quote or a
// thread goes with them only when the hub's Feishu extension can carry it (`lark_attachment_reply`, #1035): an older
// one would drop it and send the attachment on its own.
import { api, enc } from "./api";
import { uuid } from "./format";
import { larkPath } from "./lark-accounts";
import { t } from "../i18n";

/** The hub's Feishu extension sends pictures and files. */
export const LARK_ATTACHMENTS_FEATURE = "lark_attachments";
/** …and they can quote a message or go into its thread. */
export const LARK_ATTACHMENT_REPLY_FEATURE = "lark_attachment_reply";

/** At most this many in one go (iOS LarkAttachment.selectionLimit). */
export const MAX_PICK = 4;
export const FILE_LIMIT = 100 * 1024 * 1024;
/** A picture's JPEG, after it is scaled down. */
export const IMAGE_LIMIT = 15 << 19;
/** The long side a picture is scaled down to. */
export const IMAGE_SIDE = 2560;

export interface LarkAttachment {
  id: string;
  kind: "image" | "file";
  name: string;
  mime: string;
  data: Blob;
  size: number;
  /** A preview of a picture (an object URL): revoke it with releaseLarkAttachments. */
  url?: string;
}

/** Where it goes besides the chat: quoting `replyTo`, or with `thread` into its thread (`threadRoot` its first message). */
export interface LarkAttachmentRoute {
  replyTo?: string;
  thread?: boolean;
  threadRoot?: string;
}

/** Still pictures go as pictures; moving ones (GIF) and drawings (SVG) keep what they are as files, like iOS's "Original". */
export function sendsAsImage(mime: string): boolean {
  return /^image\//i.test(mime) && !/^image\/(gif|svg\+xml|x-icon|vnd\.)/i.test(mime);
}

/** Scales a picture down to IMAGE_SIDE on its long side and writes it as a JPEG; the browser drops its metadata
 * (location) on the way. Null when the browser cannot decode it. */
export type JPEGEncoder = (file: Blob) => Promise<Blob | null>;

export const canvasJPEG: JPEGEncoder = async (file) => {
  if (typeof createImageBitmap !== "function" || typeof document === "undefined") return null;
  let bitmap: ImageBitmap;
  try {
    bitmap = await createImageBitmap(file, { imageOrientation: "from-image" });
  } catch {
    return null;
  }
  try {
    const scale = Math.min(1, IMAGE_SIDE / Math.max(bitmap.width, bitmap.height, 1));
    const canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.round(bitmap.width * scale));
    canvas.height = Math.max(1, Math.round(bitmap.height * scale));
    const ctx = canvas.getContext("2d");
    if (!ctx) return null;
    // a transparent PNG would turn black as a JPEG
    ctx.fillStyle = "#fff";
    ctx.fillRect(0, 0, canvas.width, canvas.height);
    ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    return await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/jpeg", 0.88));
  } finally {
    bitmap.close();
  }
};

/** A picked file made ready to send: checked, pictures turned into JPEGs. Throws with the reason to show. */
export async function stageLarkAttachment(file: File, toJPEG: JPEGEncoder = canvasJPEG): Promise<LarkAttachment> {
  const name = file.name || t("lib-lark-attachments.unnamed");
  if (file.size <= 0) throw new Error(t("lib-lark-attachments.empty", { name }));
  if (file.size > FILE_LIMIT) throw new Error(t("lib-lark-attachments.too_big", { name }));
  const mime = file.type || "application/octet-stream";
  if (sendsAsImage(mime)) {
    const jpeg = await toJPEG(file);
    if (jpeg) {
      if (jpeg.size > IMAGE_LIMIT) throw new Error(t("lib-lark-attachments.image_too_big", { name }));
      const base = name.replace(/\.[^./]+$/, "") || "image";
      return { id: uuid(), kind: "image", name: `${base}.jpg`, mime: "image/jpeg", data: jpeg, size: jpeg.size, url: URL.createObjectURL(jpeg) };
    }
    // one the browser cannot draw (HEIC in most of them) still goes, as a file
  }
  return { id: uuid(), kind: "file", name, mime, data: file, size: file.size };
}

/** What fits in the composer: the first ones up to MAX_PICK in all; `dropped` how many were left out. */
export function fitPick<T>(existing: number, incoming: T[]): { take: T[]; dropped: number } {
  const room = Math.max(0, MAX_PICK - existing);
  return { take: incoming.slice(0, room), dropped: Math.max(0, incoming.length - room) };
}

export function releaseLarkAttachments(list: { url?: string }[]) {
  for (const a of list) if (a.url) URL.revokeObjectURL(a.url);
}

/** Whether the attachments may be sent the way they are routed: a quote or a thread needs the hub to carry it. */
export function routeAllowed(route: LarkAttachmentRoute, features: string[]): boolean {
  return !route.replyTo || features.includes(LARK_ATTACHMENT_REPLY_FEATURE);
}

async function base64(data: Blob): Promise<string> {
  const bytes = new Uint8Array(await data.arrayBuffer());
  let bin = "";
  for (let i = 0; i < bytes.length; i += 0x8000) bin += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return btoa(bin);
}

/** The request for one attachment (exported for the tests). */
export async function attachmentRequest(feed: string, a: LarkAttachment, clientID: string, route: LarkAttachmentRoute, ol?: number, manager?: string):
  Promise<{ path: string; init: RequestInit & { json?: unknown } }> {
  const root = `/lark/conversations/${enc(feed)}`;
  const thread = Boolean(route.replyTo && route.thread);
  if (a.kind === "image") {
    return {
      path: larkPath(`${root}/images`, ol, manager),
      init: {
        method: "POST",
        json: {
          client_id: clientID, name: a.name, mime: a.mime, data: await base64(a.data),
          ...(route.replyTo ? { reply_to: route.replyTo } : {}), ...(thread ? { thread: true } : {}),
        },
      },
    };
  }
  const q = new URLSearchParams({ client_id: clientID, name: a.name, mime: a.mime });
  if (route.replyTo) q.set("reply_to", route.replyTo);
  if (thread) q.set("thread_root", route.threadRoot ?? route.replyTo!);
  return {
    path: larkPath(`${root}/files?${q.toString()}`, ol, manager),
    init: { method: "POST", body: a.data, headers: { "Content-Type": "application/octet-stream" } },
  };
}

/** Thrown when a picture went without a receipt the page can match: what happened is not known. */
export class LarkReceiptMissing extends Error {
  constructor() {
    super(t("lib-lark-attachments.no_receipt"));
  }
}

/**
 * Sends one attachment. A picture needs its message id back; a file may come back without one (it was taken, so it
 * must not be sent again: docs/extensions/lark-attachments.md "Limits and regression coverage").
 */
export async function sendLarkAttachment(feed: string, a: LarkAttachment, clientID: string, route: LarkAttachmentRoute = {}, ol?: number,
  manager?: string): Promise<{ message_id?: string }> {
  const { path, init } = await attachmentRequest(feed, a, clientID, route, ol, manager);
  const r = await api<{ message_id?: string } | null>(path, init);
  const id = r && typeof r === "object" && typeof r.message_id === "string" ? r.message_id.trim() : "";
  if (a.kind === "file") {
    if (r === null || typeof r !== "object" || Array.isArray(r)) throw new LarkReceiptMissing();
    return { message_id: id && !id.startsWith("local:") ? id : undefined };
  }
  if (!id) throw new LarkReceiptMissing();
  return { message_id: id };
}

/** A file whose upload broke off halfway (timeout, dropped connection, the hub's 5xx, an unreadable receipt) may
 * already be in the chat: it is not sent again on its own (docs/extensions/lark-attachments.md). */
export function fileOutcomeUnknown(e: unknown): boolean {
  if (e instanceof LarkReceiptMissing) return true;
  const status = (e as { status?: unknown })?.status;
  if (typeof status === "number") return status >= 500 || status === 408;
  return true;
}
