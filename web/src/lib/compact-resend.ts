// Safe "Compact and send again" recovery for image-limit turn failures (#13045):
// - Persona assistant conversations trigger `assistant_compact` (POST .../compact), wait for session.compaction to
//   settle, and resend the failed message only if compaction succeeded.
// - Terminal Claude Code sessions send `/compact`, wait for a new compaction boundary and idle state, and resend only
//   if compaction succeeded.
// - Sessions that do not support compaction (non-Claude terminals, runtimes, botmux, offline, read-only, or hubs
//   without `assistant_compact`) are not offered a broken action.
import { IMAGE_LIMIT_CODE, isRetryText, parseAPIError } from "./api-errors";
import { COMPACTED_TEXT, isCompaction } from "./compaction";
import { sessionCan, stateOf } from "./format";
import { getSession, subscribe } from "./store";
import type { ChatLine } from "./turns";
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
  composerDisabled?: boolean;
  routed?: boolean;
  working?: boolean;
  outgoing?: boolean;
  stopped?: boolean;
}

/** Whether a turn's error line (or session turn_error_code) is an image-limit failure (#13045). */
export function isImageLimitError(lineCode: string | undefined, sessionCode: string | undefined): boolean {
  return lineCode === IMAGE_LIMIT_CODE || sessionCode === IMAGE_LIMIT_CODE;
}

/**
 * Determines whether "Compact and send again" can be safely offered for `session`.
 * - Blocked (setup gate), routed to a task, currently working/sending, stopped, or unable to send: never offered.
 * - Persona assistant conversations: requires hub `assistant_compact`.
 * - Agent chats: live terminal Claude Code sessions only (`!source_kind`, Claude agent, not botmux, not offline/needs_you).
 */
export function compactResendSupport(session: Session | undefined, opts: CompactResendOptions): CompactResendSupport {
  if (!session || opts.blocked || opts.routed || opts.working || opts.outgoing || opts.stopped) {
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
 * Finds the user message text to resend after compacting for `tailError`.
 * Walks back from the error line, skipping folded retry-only messages, and returns the immediately preceding user
 * prompt's text when available; otherwise returns `fallbackText` (e.g. "Continue").
 */
export function failedMessageToResend(lines: readonly ChatLine[], tailErrorId: string, fallbackText: string): string {
  const idx = lines.findIndex((l) => l.id === tailErrorId);
  const start = idx >= 0 ? idx - 1 : lines.length - 1;
  for (let i = start; i >= 0; i--) {
    const line = lines[i];
    if (line.kind === "apiError") continue;
    if (line.kind !== "segment") break;
    const user = line.seg.user;
    if (!user) break;
    const text = user.text.trim();
    if (!user.injected && text && isRetryText(text) && !line.seg.steps.length && !line.seg.answer) continue;
    if (!user.injected && text && !text.startsWith("/compact")) return text;
    break;
  }
  return fallbackText;
}

/** Counts compaction boundary items present in `items`. */
export function countCompactions(items: readonly Item[]): number {
  let n = 0;
  for (const it of items) {
    if (isCompaction(it) || (it.kind === "notice" && !it.task && !it.wake && (it.text ?? "").trim() === COMPACTED_TEXT)) n++;
  }
  return n;
}

/** Reports whether an API error item appeared strictly after `afterId` (or anywhere if `afterId` is undefined). */
export function hasAPIErrorAfter(items: readonly Item[], afterId: string | undefined): boolean {
  let seen = afterId === undefined;
  for (const it of items) {
    if (!seen) {
      if (it.id === afterId) seen = true;
      continue;
    }
    if ((it.role === "assistant" && it.kind === "text") || it.kind === "notice") {
      if (parseAPIError(it.text)) return true;
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

export interface TerminalCompactionSnapshot {
  compactions: number;
  lastItemId?: string;
}

/**
 * Waits for a terminal Claude Code `/compact` to record a new compaction boundary and return to idle.
 * Resolves to `"done"`, `"failed"`, or `"cancelled"`.
 */
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
  if (signal.aborted) return Promise.resolve("cancelled");

  return new Promise((resolve) => {
    let settled = false;
    let sawWorking = false;
    let sawBoundary = false;
    let timer = 0;
    let settleTimer = 0;
    let unsubStore: (() => void) | undefined;
    let unsubItems: (() => void) | undefined;

    const finish = (outcome: "done" | "failed" | "cancelled") => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      clearTimeout(settleTimer);
      signal.removeEventListener("abort", onAbort);
      unsubStore?.();
      unsubItems?.();
      resolve(outcome);
    };
    const onAbort = () => finish("cancelled");

    const check = () => {
      const items = getItems();
      if (countCompactions(items) > snapshot.compactions) sawBoundary = true;
      if (!sawBoundary && hasAPIErrorAfter(items, snapshot.lastItemId)) {
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
      if (sawBoundary) {
        finish("done");
        return;
      }
      if (sawWorking && !settleTimer) {
        settleTimer = window.setTimeout(() => {
          if (countCompactions(getItems()) > snapshot.compactions) finish("done");
          else finish("failed");
        }, settleMs);
      }
    };

    signal.addEventListener("abort", onAbort, { once: true });
    unsubStore = subscribe(check);
    unsubItems = subscribeItems(check);
    timer = window.setTimeout(() => finish("cancelled"), timeoutMs);
    check();
  });
}
