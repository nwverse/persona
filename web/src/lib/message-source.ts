import { t } from "../i18n";
import { CONTEXT_RE } from "./compatibility";
import type { Injected, Item, MessageSource } from "./types";

/** The server protects an app user's quoted machine template with an internal author sentinel. */
export function userSourceSentinel(text: string): boolean {
  for (const match of text.matchAll(new RegExp(CONTEXT_RE))) {
    if (!/\bkind="message-source"/.test(match[1])) continue;
    try { return JSON.parse(match[2]).author === "user"; } catch { return false; }
  }
  return false;
}

const RELAY_TITLE = /^来自对话「(.+)」$/u; // i18n-ignore: canonical relay template
const RELAY_MARKER = /这条消息不是用户本人发的，是 Persona 的另一段对话（([^）]+)）用 conversation_send 发来的，第 \d+ 跳（最多 \d+ 跳）。/u; // i18n-ignore: canonical relay template

/** Read legacy provenance only from complete machine-generated relay contexts, not a title a user quoted. */
export function legacyConversationSource(contexts: readonly { title?: string; text: string }[]): MessageSource | undefined {
  for (const context of contexts) {
    const title = RELAY_TITLE.exec(context.title ?? "");
    const marker = RELAY_MARKER.exec(context.text);
    if (title && marker) return { kind: "conversation", conversation_id: marker[1], title: title[1] };
  }
}

/** Explicit provenance wins. The injected mark supplies compatibility for pre-source hubs. */
export function sourceOf(item: Item, injected: Injected | null, contexts: readonly { title?: string; text: string }[] = []): MessageSource | undefined {
  if (item.source && typeof item.source.kind === "string" && item.source.kind.trim()) return item.source;
  if (item.client_msg_id || item.from || userSourceSentinel(item.text ?? "")) return undefined;
  if (injected?.state === "supervise") return { kind: "supervision", title: injected.title };
  if (injected?.kind === "followup") return {
    kind: injected.state === "daily" || injected.state === "interval" ? "patrol" : "followup",
    title: injected.title,
  };
  return legacyConversationSource(contexts);
}

export function sourceLabel(source: MessageSource): string {
  return source.title?.trim() || t(`chat-messages.source_${["conversation", "patrol", "followup", "supervision"].includes(source.kind) ? source.kind : "other"}`);
}

/** Identity includes the originating conversation; equal titles alone must not merge distinct speakers. */
export function sourceKey(source: MessageSource): string {
  return JSON.stringify([source.kind, source.conversation_id ?? "", source.title ?? ""]);
}
