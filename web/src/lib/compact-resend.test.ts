// @vitest-environment jsdom
// Unit tests for #13045: image-limit error recognition and safe "Compact and send again" recovery.
import { afterEach, describe, expect, it, vi } from "vitest";
import { IMAGE_LIMIT_CODE, imageLimitError, parseAPIError, retryable } from "./api-errors";
import {
  compactResendSupport,
  countCompactions,
  failedMessageToResend,
  hasAPIErrorAfter,
  waitForAssistantCompaction,
  waitForTerminalCompaction,
} from "./compact-resend";
import { dropSession, upsertSession } from "./store";
import { chatLines } from "./turns";
import type { Item, Session } from "./types";

const baseSession = (extra: Partial<Session> = {}): Session => ({
  machine_id: "box",
  session_id: "s1",
  title: "Test",
  cwd: "/code",
  status: "idle",
  ...extra,
});

afterEach(() => {
  dropSession("box", "s1");
  vi.useRealTimers();
});

describe("image-limit turn failure parsing (#13045)", () => {
  const samples = [
    "Request too large (max 32MB). Accumulated images and attachments in the conversation pushed the request over the limit.",
    "API Error: 413 request_too_large: request exceeds maximum size",
    "API Error: an image in the conversation could not be processed and was removed. Please try again.",
    "API Error: 400 At least one of the image dimensions exceed max allowed size for many-image requests: 2000 pixels",
  ];

  it("classifies all three image-limit failure patterns as images_too_large and non-retryable", () => {
    for (const raw of samples) {
      expect(imageLimitError(raw)).toBe(true);
      const info = parseAPIError(raw);
      expect(info, raw).not.toBeNull();
      expect(info!.code).toBe(IMAGE_LIMIT_CODE);
      expect(info!.transient).toBe(false);
      expect(retryable(info!)).toBe(false);
      expect(info!.raw).toBe(raw);
    }
  });

  it("does not misclassify ordinary prose mentioning request too large", () => {
    expect(parseAPIError("I fixed the Request too large error by resizing images.")).toBeNull();
    expect(parseAPIError("Done: all checks passed.")).toBeNull();
  });
});

describe("compactResendSupport", () => {
  it("supports Persona assistant conversations only when the hub announces assistant_compact", () => {
    const s = baseSession({ assistant: true });
    expect(compactResendSupport(s, { assistant: true, hubSupportsAssistantCompact: true })).toEqual({ supported: true, mode: "assistant" });
    expect(compactResendSupport(s, { assistant: true, hubSupportsAssistantCompact: false })).toEqual({ supported: false, mode: null });
    expect(compactResendSupport(s, { assistant: true, hubSupportsAssistantCompact: true, blocked: true })).toEqual({ supported: false, mode: null });
    expect(compactResendSupport(s, { assistant: true, hubSupportsAssistantCompact: true, routed: true })).toEqual({ supported: false, mode: null });
    expect(compactResendSupport(baseSession({ assistant: true, capabilities: { send: false } }), { assistant: true, hubSupportsAssistantCompact: true })).toEqual({ supported: false, mode: null });
  });

  it("supports live terminal Claude Code sessions and withholds unsupported agent sessions", () => {
    expect(compactResendSupport(baseSession({ status: "idle" }), {})).toEqual({ supported: true, mode: "claude-terminal" });
    expect(compactResendSupport(baseSession({ status: "done", agent: "claude" }), {})).toEqual({ supported: true, mode: "claude-terminal" });
    // Unsupported runtimes, agents, hosts, and states must not offer a broken action.
    expect(compactResendSupport(baseSession({ agent: "codex" }), {})).toEqual({ supported: false, mode: null });
    expect(compactResendSupport(baseSession({ agent: "agy" }), {})).toEqual({ supported: false, mode: null });
    expect(compactResendSupport(baseSession({ source_kind: "runtime" }), {})).toEqual({ supported: false, mode: null });
    expect(compactResendSupport(baseSession({ host: "botmux" }), {})).toEqual({ supported: false, mode: null });
    expect(compactResendSupport(baseSession({ status: "offline" }), {})).toEqual({ supported: false, mode: null });
    expect(compactResendSupport(baseSession({ status: "blocked", state: "needs_you" }), {})).toEqual({ supported: false, mode: null });
    expect(compactResendSupport(baseSession({ status: "idle" }), { composerDisabled: true })).toEqual({ supported: false, mode: null });
    expect(compactResendSupport(baseSession({ status: "idle" }), { working: true })).toEqual({ supported: false, mode: null });
    expect(compactResendSupport(baseSession({ status: "idle" }), { stopped: true })).toEqual({ supported: false, mode: null });
  });
});

describe("failedMessageToResend", () => {
  const msg = (id: string, seq: number, role: "user" | "assistant", text: string): Item => ({
    id, seq, role, kind: "text", text, ts: "2026-11-22T12:00:00Z",
  });

  it("returns the failed turn's user message text, skipping folded retry-only messages", () => {
    const err = "Request too large (max 32MB). Accumulated images and attachments pushed the request over the limit.";
    const lines = chatLines([
      msg("u1", 1, "user", "Compare these two screenshots"),
      msg("e1", 2, "assistant", err),
      msg("u2", 3, "user", "Continue"),
      msg("e2", 4, "assistant", err),
    ]);
    const tail = lines[lines.length - 1];
    expect(tail.kind).toBe("apiError");
    expect(failedMessageToResend(lines, tail.id, "Continue")).toBe("Compare these two screenshots");
  });

  it("falls back when the failed turn has no plain user text", () => {
    const err = "API Error: an image in the conversation could not be processed and was removed.";
    const lines = chatLines([
      { id: "u1", seq: 1, role: "user", kind: "image", ts: "2026-11-22T12:00:00Z" } as Item,
      msg("e1", 2, "assistant", err),
    ]);
    const tail = lines[lines.length - 1];
    expect(failedMessageToResend(lines, tail.id, "Continue")).toBe("Continue");
  });
});

describe("waitForAssistantCompaction", () => {
  it("resolves done after compaction transitions running -> done", async () => {
    const prev = { state: "done" as const, started_at: "2026-11-22T11:00:00Z", finished_at: "2026-11-22T11:00:10Z" };
    upsertSession(baseSession({ assistant: true, compaction: prev }));
    const ctl = new AbortController();
    const pending = waitForAssistantCompaction("box", "s1", prev, ctl.signal, 5_000);
    upsertSession(baseSession({ assistant: true, compaction: { state: "running", started_at: "2026-11-22T12:00:00Z" } }));
    upsertSession(baseSession({ assistant: true, compaction: { state: "done", started_at: "2026-11-22T12:00:00Z", finished_at: "2026-11-22T12:00:08Z" } }));
    expect(await pending).toBe("done");
  });

  it("resolves failed when compaction fails", async () => {
    upsertSession(baseSession({ assistant: true }));
    const ctl = new AbortController();
    const pending = waitForAssistantCompaction("box", "s1", undefined, ctl.signal, 5_000);
    upsertSession(baseSession({ assistant: true, compaction: { state: "running", started_at: "2026-11-22T12:00:00Z" } }));
    upsertSession(baseSession({ assistant: true, compaction: { state: "failed", started_at: "2026-11-22T12:00:00Z", finished_at: "2026-11-22T12:00:04Z", error: "too short" } }));
    expect(await pending).toBe("failed");
  });

  it("resolves cancelled when aborted", async () => {
    upsertSession(baseSession({ assistant: true }));
    const ctl = new AbortController();
    const pending = waitForAssistantCompaction("box", "s1", undefined, ctl.signal, 5_000);
    ctl.abort();
    expect(await pending).toBe("cancelled");
  });
});

describe("waitForTerminalCompaction", () => {
  it("resolves done when a compaction boundary lands and the terminal returns to idle", async () => {
    let curItems: Item[] = [{ id: "e1", seq: 2, role: "assistant", kind: "text", text: "Request too large (max 32MB)." }];
    const listeners = new Set<() => void>();
    const notify = () => listeners.forEach((fn) => fn());
    upsertSession(baseSession({ status: "working", state: "working" }));

    const ctl = new AbortController();
    const pending = waitForTerminalCompaction(
      "box",
      "s1",
      { compactions: countCompactions(curItems), lastItemId: "e1" },
      () => curItems,
      (fn) => {
        listeners.add(fn);
        return () => listeners.delete(fn);
      },
      ctl.signal,
      5_000,
      50,
    );

    curItems = [...curItems, { id: "c1", seq: 3, role: "assistant", kind: "notice", compact: { trigger: "manual" }, text: "上下文已压缩" }];
    notify();
    upsertSession(baseSession({ status: "idle", state: "idle" }));
    expect(await pending).toBe("done");
  });

  it("resolves failed if an API error appears after /compact", async () => {
    let curItems: Item[] = [{ id: "e1", seq: 2, role: "assistant", kind: "text", text: "Request too large (max 32MB)." }];
    const listeners = new Set<() => void>();
    const notify = () => listeners.forEach((fn) => fn());
    upsertSession(baseSession({ status: "working", state: "working" }));

    const ctl = new AbortController();
    const pending = waitForTerminalCompaction(
      "box",
      "s1",
      { compactions: 0, lastItemId: "e1" },
      () => curItems,
      (fn) => {
        listeners.add(fn);
        return () => listeners.delete(fn);
      },
      ctl.signal,
      5_000,
      50,
    );

    curItems = [...curItems, { id: "e2", seq: 3, role: "assistant", kind: "text", text: "API Error: 500 internal error" }];
    expect(hasAPIErrorAfter(curItems, "e1")).toBe(true);
    notify();
    expect(await pending).toBe("failed");
  });

  it("resolves failed if the terminal returns to idle without compacting", async () => {
    const curItems: Item[] = [{ id: "e1", seq: 2, role: "assistant", kind: "text", text: "Request too large (max 32MB)." }];
    upsertSession(baseSession({ status: "working", state: "working" }));
    const ctl = new AbortController();
    const pending = waitForTerminalCompaction(
      "box",
      "s1",
      { compactions: 0, lastItemId: "e1" },
      () => curItems,
      () => () => {},
      ctl.signal,
      5_000,
      20,
    );
    upsertSession(baseSession({ status: "idle", state: "idle" }));
    expect(await pending).toBe("failed");
  });
});
