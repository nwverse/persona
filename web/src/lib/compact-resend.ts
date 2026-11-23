// Safe "Compact and send again" recovery for image-limit turn failures (#13045):
// - Persona assistant conversations trigger `assistant_compact` (POST .../compact), wait for session.compaction to
//   settle, and resend the failed message only if compaction succeeded.
// - Terminal Claude Code sessions send `/compact`, wait for a post-command compaction boundary after the session
//   transitions through working back to idle (with no post-command error), and resend only if compaction succeeded.
// - Original user payload (raw text including contexts/files/attachments + full text + image bytes) is captured before
//   compaction; if original content/image bytes cannot be recovered, automatic resend is withheld rather than silently
//   dropping attachments or degrading to a generic prompt.
// - Sessions that do not support compaction (non-Claude terminals, runtimes, botmux, offline, read-only, or hubs
//   without `assistant_compact`) are not offered a broken action.
import { fullItem, imageURL } from "./api";
import { IMAGE_LIMIT_CODE, isRetryText, parseAPIError } from "./api-errors";
import { imagePayload, MAX_IMAGE } from "./attachments";
import { COMPACTED_TEXT, isCompaction } from "./compaction";
import { CONTEXT_RE } from "./compatibility";
import { sessionCan, stateOf } from "./format";
import { getSession, subscribe } from "./store";
import { isInterjectContext, tagAttrs, withoutViewErrors, type ChatLine, type UserMessage } from "./turns";
import type { Compaction, Item, Session } from "./types";

export type CompactResendMode = "assistant" | "claude-terminal";

export interface CompactResendSupport {
  supported: boolean;
  mode: CompactResendMode | null;
}

export interface CompactResendOptions {
  assistant?: boolean;
  hubSupportsAssistantCompact?: boolean;
  blocked?: boolean;
  offline?: boolean;
  composerDisabled?: boolean;
  routed?: boolean;
  working?: boolean;
  outgoing?: boolean;
  stopped?: boolean;
}

export interface OutgoingPayload {
  text: string;
  images: { mime: string; data: string }[];
}

export interface FailedTurnCandidate {
  user: UserMessage;
  captured?: OutgoingPayload;
}

export interface FailedTurnOptions {
  /** `client_msg_id` values of internal `/compact` commands sent by this recovery flow. */
  internalCompactIds?: ReadonlySet<string>;
  /** Recently delivered outgoing payloads keyed by `client_msg_id`, preserving exact text + image bytes. */
  captured?: ReadonlyMap<string, OutgoingPayload>;
}

export type ResolvedPayload =
  | { ok: true; payload: OutgoingPayload }
  | { ok: false; reason: "empty" | "images_unavailable" | "text_unavailable" };

export interface ResolvePayloadDeps {
  fetchFullItem?: (mid: string, sid: string, itemId: string) => Promise<Item>;
  fetchImage?: (mid: string, sid: string, item: Item) => Promise<{ mime: string; data: string } | null>;
}

/** Whether a turn's error line (or session turn_error_code) is an image-limit failure (#13045). */
export function isImageLimitError(lineCode: string | undefined, sessionCode: string | undefined): boolean {
  return lineCode === IMAGE_LIMIT_CODE || sessionCode === IMAGE_LIMIT_CODE;
}

/**
 * Determines whether "Compact and send again" can be safely offered for `session`.
 * - Blocked (setup gate), offline/manager-offline, routed to a task, currently working/sending, stopped, or unable to send: never offered.
 * - Persona assistant conversations: requires hub `assistant_compact`.
 * - Agent chats: live terminal Claude Code sessions only (`!source_kind`, Claude agent, not botmux, not offline/needs_you).
 */
export function compactResendSupport(session: Session | undefined, opts: CompactResendOptions): CompactResendSupport {
  if (!session || opts.blocked || opts.offline || opts.routed || opts.working || opts.outgoing || opts.stopped) {
    return { supported: false, mode: null };
  }
  if (!sessionCan(session, "send")) return { supported: false, mode: null };
  const isAssistant = Boolean(opts.assistant || session.assistant);
  if (isAssistant) {
    if (!opts.hubSupportsAssistantCompact) return { supported: false, mode: null };
    return { supported: true, mode: "assistant" };
  }
  if (opts.composerDisabled) return { supported: false, mode: null };
  if (session.source_kind) return { supported: false, mode: null };
  if (session.host === "botmux") return { supported: false, mode: null };
  if (session.agent && session.agent !== "claude") return { supported: false, mode: null };
  const st = stateOf(session);
  if (st === "offline" || st === "needs_you" || st === "working") return { supported: false, mode: null };
  return { supported: true, mode: "claude-terminal" };
}

/**
 * Strips hub-only wrappers (`<persona-view-errors>`, realtime snapshot `<persona-context>`, `skill-instructions`,
 * `message-source`, interject note) while keeping all user-supplied rich content (`<persona-context>` quotes/edits/
 * forwards, `<persona-file>`, `<pasted_content>`, `<attached_images>`, slash commands, and plain text) verbatim.
 */
function cleanRawUserText(raw: string, stripImagePlaceholders: boolean): string {
  let out = withoutViewErrors(raw).replace(CONTEXT_RE, (whole, attrs: string) => {
    if (!attrs.trim()) return ""; // hub's realtime snapshot, re-prepended on send
    const { kind, title } = tagAttrs(attrs);
    if (kind === "skill-instructions" || kind === "message-source" || isInterjectContext({ kind, title })) return "";
    return whole;
  });
  if (stripImagePlaceholders) out = out.replace(/\[Image #\d+\]\s?/g, "");
  return out.trim();
}

function userTextItems(user: UserMessage): Item[] {
  return (user.rawInput ?? []).filter((it) => it.kind === "text");
}

function userImageItems(user: UserMessage): Item[] {
  const rawImgs = (user.rawInput ?? []).filter((it) => it.kind === "image");
  return rawImgs.length ? rawImgs : user.images;
}

function isPlainRetryOnly(user: UserMessage, hasStepsOrAnswer: boolean): boolean {
  if (hasStepsOrAnswer || user.injected) return false;
  if (user.images.length || user.files?.length || user.photos?.length || user.contexts.length) return false;
  const text = user.text.trim();
  return Boolean(text && isRetryText(text));
}

/**
 * Finds the immediately preceding non-injected user message for `tailErrorId`.
 * - Preserves legitimate user slash commands (including `/clear`, `/review`, or a user-typed `/compact`).
 * - Skips only internal `/compact` commands tracked in `opts.internalCompactIds` and folded bare retry-only turns.
 * - Stops at injected messages or non-segment boundaries rather than walking into unrelated earlier history.
 */
export function failedTurnToResend(
  lines: readonly ChatLine[],
  tailErrorId: string,
  opts: FailedTurnOptions = {},
): FailedTurnCandidate | null {
  const idx = lines.findIndex((l) => l.id === tailErrorId);
  const start = idx >= 0 ? idx - 1 : lines.length - 1;
  for (let i = start; i >= 0; i--) {
    const line = lines[i];
    if (line.kind === "apiError") continue;
    if (line.kind !== "segment") return null;
    const user = line.seg.user;
    if (!user || user.injected) return null;
    if (user.client_msg_id && opts.internalCompactIds?.has(user.client_msg_id)) continue;
    if (isPlainRetryOnly(user, Boolean(line.seg.steps.length || line.seg.answer))) continue;
    const captured = user.client_msg_id ? opts.captured?.get(user.client_msg_id) : undefined;
    return { user, ...(captured ? { captured } : {}) };
  }
  return null;
}

/**
 * Static check before offering "Compact and send again": returns false when no failed user message exists or when any
 * attached image item has neither inline bytes nor a lazy image endpoint to recover it from.
 */
export function canResendFailedTurn(candidate: FailedTurnCandidate | null): boolean {
  if (!candidate) return false;
  if (candidate.captured) {
    return Boolean(candidate.captured.text.trim() || candidate.captured.images.length);
  }
  const { user } = candidate;
  const images = userImageItems(user);
  for (const img of images) {
    const hasBytes = Boolean(img.image?.data);
    const hasLazyFetch = Boolean(img.image?.lazy && img.id);
    if (!hasBytes && !hasLazyFetch) return false;
  }
  if (images.length > 0) return true;
  const texts = userTextItems(user);
  if (texts.some((it) => Boolean(it.full_size))) return true;
  if (texts.length > 0) return texts.some((it) => Boolean(cleanRawUserText(it.text ?? "", false)));
  return Boolean(user.text.trim());
}

async function defaultFetchImage(mid: string, sid: string, item: Item, signal?: AbortSignal): Promise<{ mime: string; data: string } | null> {
  try {
    const r = await fetch(imageURL(mid, sid, item.id), { signal });
    const blob = r.ok ? await r.blob() : null;
    if (!blob || !blob.type.startsWith("image/") || blob.size > MAX_IMAGE) return null;
    return await imagePayload(blob);
  } catch {
    return null;
  }
}

/**
 * Captures the complete original `OutgoingPayload` (raw text with contexts/files/attachments + full untruncated text +
 * all image bytes) BEFORE compaction starts. Fails cleanly if any image bytes or truncated text cannot be recovered.
 */
export async function resolveFailedTurnPayload(
  candidate: FailedTurnCandidate | null,
  mid: string,
  sid: string,
  deps: ResolvePayloadDeps = {},
  signal?: AbortSignal,
): Promise<ResolvedPayload> {
  if (!candidate) return { ok: false, reason: "empty" };
  if (candidate.captured) {
    const text = candidate.captured.text;
    const images = candidate.captured.images.map((img) => ({ mime: img.mime, data: img.data }));
    if (!text.trim() && !images.length) return { ok: false, reason: "empty" };
    return { ok: true, payload: { text, images } };
  }
  const fetchFull = deps.fetchFullItem ?? fullItem;
  const fetchImg = deps.fetchImage ?? ((m: string, s: string, item: Item) => defaultFetchImage(m, s, item, signal));
  const { user } = candidate;

  const imgItems = userImageItems(user);
  const images: { mime: string; data: string }[] = [];
  for (const img of imgItems) {
    if (img.image?.data) {
      images.push({ mime: img.image.mime || "image/png", data: img.image.data });
      continue;
    }
    if (!img.image?.lazy || !img.id) return { ok: false, reason: "images_unavailable" };
    const loaded = await fetchImg(mid, sid, img);
    if (!loaded?.data) return { ok: false, reason: "images_unavailable" };
    images.push(loaded);
  }

  const textItems = userTextItems(user);
  let text = "";
  if (textItems.length > 0) {
    const parts: string[] = [];
    for (const it of textItems) {
      let raw = it.text ?? "";
      if (it.full_size) {
        try {
          const whole = await fetchFull(mid, sid, it.id);
          if (typeof whole.text !== "string") return { ok: false, reason: "text_unavailable" };
          raw = whole.text;
        } catch {
          return { ok: false, reason: "text_unavailable" };
        }
      }
      const cleaned = cleanRawUserText(raw, images.length > 0);
      if (cleaned) parts.push(cleaned);
    }
    text = parts.join("\n");
  } else {
    text = user.text.trim();
  }

  if (!text.trim() && !images.length) return { ok: false, reason: "empty" };
  return { ok: true, payload: { text, images } };
}

function isBoundaryItem(it: Item): boolean {
  return isCompaction(it) || (it.kind === "notice" && !it.task && !it.wake && (it.text ?? "").trim() === COMPACTED_TEXT);
}

export interface TerminalCompactionSnapshot {
  /** Highest `seq` present before `/compact` was sent; older paged/replayed items have `seq <= afterSeq`. */
  afterSeq: number;
  /** Item IDs already present before `/compact` was sent. */
  knownIds: ReadonlySet<string>;
  /** `client_msg_id` of the `/compact` command sent by this recovery flow. */
  commandMsgId: string;
}

/** Captures the transcript watermark right before sending `/compact`. */
export function snapshotTerminalCompaction(items: readonly Item[], commandMsgId: string): TerminalCompactionSnapshot {
  let afterSeq = 0;
  const knownIds = new Set<string>();
  for (const it of items) {
    knownIds.add(it.id);
    if (it.seq > afterSeq) afterSeq = it.seq;
  }
  return { afterSeq, knownIds, commandMsgId };
}

function postCommandItems(items: readonly Item[], snapshot: TerminalCompactionSnapshot): Item[] {
  return items.filter((it) => !snapshot.knownIds.has(it.id) && it.seq > snapshot.afterSeq);
}

/** Whether a new compaction boundary was written strictly after the pre-command watermark (and command item, if present). */
export function hasPostCommandBoundary(items: readonly Item[], snapshot: TerminalCompactionSnapshot): boolean {
  const post = postCommandItems(items, snapshot);
  const cmdUser = post.find((it) => it.client_msg_id === snapshot.commandMsgId && it.role === "user");
  const minSeq = cmdUser ? cmdUser.seq : snapshot.afterSeq;
  return post.some((it) => isBoundaryItem(it) && (cmdUser ? it.seq >= minSeq : it.seq > snapshot.afterSeq));
}

/** Whether any API error or error notice appeared strictly after the pre-command watermark. */
export function hasPostCommandError(items: readonly Item[], snapshot: TerminalCompactionSnapshot): boolean {
  for (const it of postCommandItems(items, snapshot)) {
    if ((it.role === "assistant" && it.kind === "text") || it.kind === "notice") {
      if (it.is_error || parseAPIError(it.text)) return true;
    }
  }
  return false;
}

const DEFAULT_WAIT_MS = 5 * 60_000;
const TERMINAL_SETTLE_MS = 1_500;

function compactionSig(c: Compaction | undefined): string {
  return c ? `${c.started_at}|${c.state}|${c.finished_at ?? ""}` : "";
}

/**
 * Waits for an assistant conversation's `session.compaction` to settle to a new `"done"` or `"failed"` state.
 * Resolves to `"done"`, `"failed"`, or `"cancelled"` (aborted / timed out).
 */
export function waitForAssistantCompaction(
  mid: string,
  sid: string,
  before: Compaction | undefined,
  signal: AbortSignal,
  timeoutMs = DEFAULT_WAIT_MS,
): Promise<"done" | "failed" | "cancelled"> {
  if (signal.aborted) return Promise.resolve("cancelled");
  const prevSig = compactionSig(before);
  const prevStart = before?.started_at ?? "";
  const prevFinish = before?.finished_at ?? "";
  let sawRunning = false;

  const inspect = (): "done" | "failed" | null => {
    const c = getSession(mid, sid)?.compaction;
    if (!c) return null;
    if (c.state === "running") {
      sawRunning = true;
      return null;
    }
    const fresh = sawRunning || compactionSig(c) !== prevSig || c.started_at !== prevStart || (c.finished_at ?? "") !== prevFinish;
    if (!fresh) return null;
    if (c.state === "done") return "done";
    if (c.state === "failed") return "failed";
    return null;
  };

  const immediate = inspect();
  if (immediate) return Promise.resolve(immediate);

  return new Promise((resolve) => {
    let settled = false;
    let timer = 0;
    let unsub: (() => void) | undefined;

    const finish = (outcome: "done" | "failed" | "cancelled") => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      signal.removeEventListener("abort", onAbort);
      unsub?.();
      resolve(outcome);
    };
    const onAbort = () => finish("cancelled");
    const check = () => {
      const result = inspect();
      if (result) finish(result);
    };

    signal.addEventListener("abort", onAbort, { once: true });
    unsub = subscribe(check);
    timer = window.setTimeout(() => finish("cancelled"), timeoutMs);
    check();
  });
}

export interface TerminalCompactionWatcher {
  /** Call once `/compact` has been delivered so idle settlement may finish. */
  markDelivered: () => void;
  /** Cancel and clean up subscriptions immediately (e.g. if `/compact` failed to send). */
  cancel: () => void;
  /** Resolves to `"done"`, `"failed"`, or `"cancelled"`. */
  promise: Promise<"done" | "failed" | "cancelled">;
}

/**
 * Starts watching for a terminal Claude Code `/compact` BEFORE `/compact` is sent, avoiding a race if the terminal
 * transitions `idle -> working -> idle` while the send HTTP request is still in flight.
 *
 * Requires ALL of:
 * 1. The terminal transitioned through `"working"` after watching began (`sawWorking`).
 * 2. The terminal returned to idle (not offline / needs_you).
 * 3. A new compaction boundary appeared strictly after `snapshot.afterSeq` (and not in `snapshot.knownIds`).
 * 4. No post-command API error or error notice appeared (checked unconditionally, even if a boundary also appeared).
 */
export function watchTerminalCompaction(
  mid: string,
  sid: string,
  snapshot: TerminalCompactionSnapshot,
  getItems: () => readonly Item[],
  subscribeItems: (fn: () => void) => () => void,
  signal: AbortSignal,
  timeoutMs = DEFAULT_WAIT_MS,
  settleMs = TERMINAL_SETTLE_MS,
): TerminalCompactionWatcher {
  let settled = false;
  let delivered = false;
  let sawWorking = false;
  let timer = 0;
  let settleTimer = 0;
  let unsubStore: (() => void) | undefined;
  let unsubItems: (() => void) | undefined;
  let resolvePromise!: (outcome: "done" | "failed" | "cancelled") => void;

  const promise = new Promise<"done" | "failed" | "cancelled">((resolve) => {
    resolvePromise = resolve;
  });

  const finish = (outcome: "done" | "failed" | "cancelled") => {
    if (settled) return;
    settled = true;
    clearTimeout(timer);
    clearTimeout(settleTimer);
    signal.removeEventListener("abort", onAbort);
    unsubStore?.();
    unsubItems?.();
    resolvePromise(outcome);
  };
  const onAbort = () => finish("cancelled");

  const check = () => {
    if (settled) return;
    const items = getItems();
    // Unconditional failure check: an error after /compact always fails recovery, even if a boundary also appeared.
    if (hasPostCommandError(items, snapshot)) {
      finish("failed");
      return;
    }
    const sess = getSession(mid, sid);
    if (!sess) return;
    const st = stateOf(sess);
    if (st === "working") {
      sawWorking = true;
      clearTimeout(settleTimer);
      settleTimer = 0;
      return;
    }
    if (st === "offline" || st === "needs_you") {
      finish("failed");
      return;
    }
    // Never resolve done before /compact has been delivered and observed running (`sawWorking`).
    if (!delivered || !sawWorking) return;
    if (hasPostCommandBoundary(items, snapshot)) {
      finish("done");
      return;
    }
    if (!settleTimer) {
      settleTimer = window.setTimeout(() => {
        const latest = getItems();
        if (!hasPostCommandError(latest, snapshot) && hasPostCommandBoundary(latest, snapshot)) finish("done");
        else finish("failed");
      }, settleMs);
    }
  };

  if (signal.aborted) {
    finish("cancelled");
  } else {
    signal.addEventListener("abort", onAbort, { once: true });
    unsubStore = subscribe(check);
    unsubItems = subscribeItems(check);
    timer = window.setTimeout(() => finish("cancelled"), timeoutMs);
    check();
  }

  return {
    markDelivered: () => {
      delivered = true;
      check();
    },
    cancel: () => finish("cancelled"),
    promise,
  };
}

/** Convenience wrapper when `/compact` has already been delivered. */
export function waitForTerminalCompaction(
  mid: string,
  sid: string,
  snapshot: TerminalCompactionSnapshot,
  getItems: () => readonly Item[],
  subscribeItems: (fn: () => void) => () => void,
  signal: AbortSignal,
  timeoutMs = DEFAULT_WAIT_MS,
  settleMs = TERMINAL_SETTLE_MS,
): Promise<"done" | "failed" | "cancelled"> {
  const watcher = watchTerminalCompaction(mid, sid, snapshot, getItems, subscribeItems, signal, timeoutMs, settleMs);
  watcher.markDelivered();
  return watcher.promise;
}
