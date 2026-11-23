import { getSession, subscribe } from "./store";
import type { Compaction } from "./types";

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
  timeoutMs = 5 * 60_000,
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
