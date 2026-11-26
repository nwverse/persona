// @vitest-environment jsdom
// Unit tests for the shared channel detail controller: sequencing, exclusive writes, stale-write re-reads,
// channel-switch / ABA fencing, null clearing, adopt, and test feedback.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import type { ChannelDetail, ChannelTestResult } from "../../lib/channels";
import { useChannelController, type ChannelController } from "./useChannelController";

type Asked = {
  kind: "get" | "put" | "unbind" | "test";
  id: string;
  body?: unknown;
  answer: (d: ChannelDetail | ChannelTestResult) => void;
  reject: (err: unknown) => void;
};

const hub = vi.hoisted(() => ({
  asked: [] as Asked[],
  forgotten: 0,
  pins: [] as Array<{ prefix: string; manager?: string }>,
}));

vi.mock("../../lib/channels", async (original) => ({
  ...(await original<typeof import("../../lib/channels")>()),
  channelDetail: (id: string) => new Promise((resolve, reject) => hub.asked.push({ kind: "get", id, answer: resolve, reject })),
  updateChannel: (id: string, body: unknown) => new Promise((resolve, reject) => hub.asked.push({ kind: "put", id, body, answer: resolve, reject })),
  unbindChannel: (id: string) => new Promise((resolve, reject) => hub.asked.push({ kind: "unbind", id, answer: resolve, reject })),
  testChannel: (id: string) => new Promise((resolve, reject) => hub.asked.push({ kind: "test", id, answer: resolve, reject })),
  forgetChannels: () => {
    hub.forgotten++;
  },
}));
vi.mock("../../lib/manager-choice", async (original) => ({
  ...(await original<typeof import("../../lib/manager-choice")>()),
  pinManager: (prefix: string, manager?: string) => {
    hub.pins.push({ prefix, manager });
    return () => {};
  },
}));
vi.mock("../../ui/atoms", async (original) => ({
  ...(await original<typeof import("../../ui/atoms")>()),
  toast: vi.fn(),
}));

import { toast } from "../../ui/atoms";

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

const makeDetail = (id: string, patch: Partial<ChannelDetail> = {}): ChannelDetail => ({
  id,
  name: id.toUpperCase(),
  kind: "im",
  source: "builtin",
  installed: true,
  enabled: true,
  state: "connected",
  account: { name: `${id}@example.com` },
  capabilities: { push: true },
  bind: "none",
  target: { kind: "main" },
  waiting: { answers: 0, asks: 0 },
  ...patch,
});

let host: HTMLDivElement;
let root: Root;
let latest: ChannelController;

function Harness({ channelId, manager }: { channelId: string | null; manager?: string }) {
  latest = useChannelController(channelId, manager);
  return null;
}

const settle = async () => {
  for (let i = 0; i < 8; i++) {
    await act(async () => {
      await Promise.resolve();
    });
  }
};

beforeEach(() => {
  hub.asked = [];
  hub.forgotten = 0;
  hub.pins = [];
  vi.mocked(toast).mockReset();
  host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("useChannelController", () => {
  it("clears state when channelId becomes null and ignores stale reads from the previous channel", async () => {
    await act(async () => root.render(<Harness channelId="alpha" />));
    const req = hub.asked.shift()!;
    expect(req.kind).toBe("get");
    expect(req.id).toBe("alpha");
    expect(latest.loading).toBe(true);

    await act(async () => root.render(<Harness channelId={null} />));
    expect(latest.detail).toBeNull();
    expect(latest.loading).toBe(false);

    await act(async () => req.answer(makeDetail("alpha")));
    await settle();
    expect(latest.detail).toBeNull();
    expect(latest.reloadKey).toBe(0);
  });

  it("fences in-flight writes and tests across channel switches and ABA returns", async () => {
    await act(async () => root.render(<Harness channelId="alpha" />));
    await act(async () => hub.asked.shift()!.answer(makeDetail("alpha")));
    await settle();
    expect(latest.detail?.id).toBe("alpha");

    // Start a write on alpha, then switch to beta before it finishes.
    let alphaWritePromise!: Promise<void>;
    await act(async () => {
      alphaWritePromise = latest.change("enabled", { enabled: false });
    });
    const alphaPut = hub.asked.shift()!;
    expect(alphaPut.kind).toBe("put");
    expect(alphaPut.id).toBe("alpha");
    expect(latest.busy).toBe("enabled");

    await act(async () => root.render(<Harness channelId="beta" />));
    const betaGet = hub.asked.shift()!;
    expect(betaGet.kind).toBe("get");
    expect(betaGet.id).toBe("beta");
    expect(latest.busy).toBe("");

    await act(async () => betaGet.answer(makeDetail("beta")));
    await settle();
    expect(latest.detail?.id).toBe("beta");

    // Beta is immediately writable even while alpha's old write is still pending.
    let betaWritePromise!: Promise<void>;
    await act(async () => {
      betaWritePromise = latest.change("enabled", { enabled: false });
    });
    const betaPut = hub.asked.shift()!;
    expect(betaPut.kind).toBe("put");
    expect(betaPut.id).toBe("beta");
    await act(async () => {
      betaPut.answer(makeDetail("beta", { enabled: false }));
      await betaWritePromise;
    });
    await settle();
    expect(latest.detail?.enabled).toBe(false);

    // Switch back to alpha (ABA), then resolve the stale write from the first alpha mount.
    await act(async () => root.render(<Harness channelId="alpha" />));
    const alphaGet2 = hub.asked.shift()!;
    expect(alphaGet2.kind).toBe("get");
    await act(async () => alphaGet2.answer(makeDetail("alpha", { enabled: true })));
    await settle();
    expect(latest.detail?.enabled).toBe(true);

    const reqCountBefore = hub.asked.length;
    await act(async () => {
      alphaPut.answer(makeDetail("alpha", { enabled: false }));
      await alphaWritePromise;
    });
    await settle();

    // Stale write must neither overwrite the new alpha detail nor trigger a spurious reload.
    expect(latest.detail?.enabled).toBe(true);
    expect(latest.busy).toBe("");
    expect(hub.asked.length).toBe(reqCountBefore);
  });

  it("ignores stale test errors and stale unbind results after switching channels", async () => {
    await act(async () => root.render(<Harness channelId="alpha" />));
    await act(async () => hub.asked.shift()!.answer(makeDetail("alpha")));
    await settle();

    let unbindResult: boolean | undefined;
    let unbindPromise!: Promise<void>;
    await act(async () => {
      unbindPromise = latest.unbind().then((ok) => {
        unbindResult = ok;
      });
    });
    const alphaUnbind = hub.asked.shift()!;
    expect(alphaUnbind.kind).toBe("unbind");

    await act(async () => root.render(<Harness channelId="beta" />));
    await act(async () => hub.asked.shift()!.answer(makeDetail("beta")));
    await settle();

    await act(async () => {
      alphaUnbind.answer(makeDetail("alpha", { state: "not_connected", account: undefined }));
      await unbindPromise;
    });
    await settle();

    expect(unbindResult).toBe(false);
    expect(latest.detail?.id).toBe("beta");
    expect(toast).not.toHaveBeenCalled();

    // Now start a test on beta and switch away before it rejects.
    let testPromise!: Promise<void>;
    await act(async () => {
      testPromise = latest.runTest();
    });
    const betaTest = hub.asked.shift()!;
    expect(betaTest.kind).toBe("test");

    await act(async () => root.render(<Harness channelId="alpha" />));
    await act(async () => hub.asked.shift()!.answer(makeDetail("alpha")));
    await settle();

    await act(async () => {
      betaTest.reject(new Error("boom"));
      await testPromise;
    });
    await settle();

    expect(latest.testFeedback).toBeNull();
    expect(toast).not.toHaveBeenCalled();
  });

  it("adopts a newer snapshot, invalidates older reads, and runs reply-only tests cleanly", async () => {
    await act(async () => root.render(<Harness channelId="alpha" manager="m-1" />));
    expect(hub.pins[0]).toEqual({ prefix: "/channels/alpha", manager: "m-1" });
    const slowGet = hub.asked.shift()!;

    act(() => latest.adopt(makeDetail("alpha", { capabilities: { reply_only: true }, name: "Alpha" })));
    expect(hub.forgotten).toBe(1);
    expect(latest.detail?.capabilities.reply_only).toBe(true);

    // Late initial read must not overwrite the adopted detail, and must still clear loading when it settles.
    await act(async () => slowGet.answer(makeDetail("alpha", { capabilities: { push: true }, name: "Stale" })));
    await settle();
    expect(latest.detail?.name).toBe("Alpha");
    expect(latest.loading).toBe(false);

    let run!: Promise<void>;
    await act(async () => {
      run = latest.runTest();
    });
    const testReq = hub.asked.shift()!;
    await act(async () => {
      testReq.answer({ ok: true, reply_only: true });
      await run;
    });
    await settle();

    expect(latest.testFeedback).toEqual({
      tone: "ok",
      text: t("settings-channels.connection_ok", { name: "Alpha" }),
    });
    expect(toast).toHaveBeenCalledWith(t("settings-channels.connection_ok", { name: "Alpha" }), "ok");
  });

  it("re-fences, clears state, and reloads when manager changes for the same channelId", async () => {
    await act(async () => root.render(<Harness channelId="wechat" manager="m-1" />));
    expect(hub.pins.at(-1)).toEqual({ prefix: "/channels/wechat", manager: "m-1" });
    const m1Get = hub.asked.shift()!;
    await act(async () => m1Get.answer(makeDetail("wechat", { name: "M1 WeChat", enabled: true })));
    await settle();
    expect(latest.detail?.name).toBe("M1 WeChat");

    // Start an in-flight write on m-1, then switch manager to m-2 with the same channelId.
    let m1PutPromise!: Promise<void>;
    await act(async () => {
      m1PutPromise = latest.change("enabled", { enabled: false });
    });
    const m1Put = hub.asked.shift()!;
    expect(m1Put.kind).toBe("put");

    await act(async () => root.render(<Harness channelId="wechat" manager="m-2" />));
    expect(hub.pins.at(-1)).toEqual({ prefix: "/channels/wechat", manager: "m-2" });
    expect(latest.detail).toBeNull();
    expect(latest.busy).toBe("");

    const m2Get = hub.asked.shift()!;
    expect(m2Get.kind).toBe("get");
    expect(m2Get.id).toBe("wechat");
    await act(async () => m2Get.answer(makeDetail("wechat", { name: "M2 WeChat", enabled: false })));
    await settle();
    expect(latest.detail?.name).toBe("M2 WeChat");

    // Resolving the stale m-1 write must neither overwrite m-2 nor trigger a reload.
    const countBefore = hub.asked.length;
    await act(async () => {
      m1Put.answer(makeDetail("wechat", { name: "M1 Stale", enabled: true }));
      await m1PutPromise;
    });
    await settle();
    expect(latest.detail?.name).toBe("M2 WeChat");
    expect(hub.asked.length).toBe(countBefore);
  });

  it("clears loading when an in-flight load finishes after an interleaved write", async () => {
    await act(async () => root.render(<Harness channelId="alpha" />));
    await act(async () => hub.asked.shift()!.answer(makeDetail("alpha", { enabled: true })));
    await settle();
    expect(latest.loading).toBe(false);

    // Start a manual refresh, then interleave a write before the refresh returns.
    let refreshPromise!: Promise<void>;
    await act(async () => {
      refreshPromise = latest.load();
    });
    const slowRefresh = hub.asked.shift()!;
    expect(slowRefresh.kind).toBe("get");
    expect(latest.loading).toBe(true);

    let writePromise!: Promise<void>;
    await act(async () => {
      writePromise = latest.change("enabled", { enabled: false });
    });
    const put = hub.asked.shift()!;
    expect(put.kind).toBe("put");
    await act(async () => {
      put.answer(makeDetail("alpha", { enabled: false }));
      await writePromise;
    });
    await settle();
    expect(latest.detail?.enabled).toBe(false);

    // When the older GET settles, it must not overwrite the write's result, yet must clear loading.
    await act(async () => {
      slowRefresh.answer(makeDetail("alpha", { enabled: true }));
      await refreshPromise;
    });
    await settle();
    expect(latest.detail?.enabled).toBe(false);
    expect(latest.loading).toBe(false);
  });

  it("fences adopt against channel switches, manager switches, null clearing, mismatched ids, and unmount", async () => {
    await act(async () => root.render(<Harness channelId="alpha" manager="m-1" />));
    await act(async () => hub.asked.shift()!.answer(makeDetail("alpha", { name: "Alpha M1" })));
    await settle();
    const staleAlphaAdopt = latest.adopt;

    // Wrong id on the active scope is ignored.
    act(() => staleAlphaAdopt(makeDetail("beta", { name: "Wrong" })));
    expect(latest.detail?.id).toBe("alpha");

    // Switching to beta with an in-flight GET: stale alpha adopt must neither drop beta's GET nor overwrite detail.
    await act(async () => root.render(<Harness channelId="beta" manager="m-1" />));
    const betaGet = hub.asked.shift()!;
    act(() => staleAlphaAdopt(makeDetail("alpha", { name: "Stale Alpha" })));
    expect(latest.detail).toBeNull();
    await act(async () => betaGet.answer(makeDetail("beta", { name: "Beta M1" })));
    await settle();
    expect(latest.detail?.name).toBe("Beta M1");

    // Switching back to alpha (ABA) or changing manager still fences the first scope's adopt.
    await act(async () => root.render(<Harness channelId="alpha" manager="m-1" />));
    const alphaGet2 = hub.asked.shift()!;
    act(() => staleAlphaAdopt(makeDetail("alpha", { name: "ABA Stale" })));
    await act(async () => alphaGet2.answer(makeDetail("alpha", { name: "Alpha Fresh" })));
    await settle();
    expect(latest.detail?.name).toBe("Alpha Fresh");

    // Switching to null fences adopt.
    const currentAdopt = latest.adopt;
    await act(async () => root.render(<Harness channelId={null} />));
    act(() => currentAdopt(makeDetail("alpha", { name: "After Null" })));
    expect(latest.detail).toBeNull();

    // Unmount fences adopt without throwing or updating state.
    await act(async () => root.render(<Harness channelId="alpha" />));
    await act(async () => hub.asked.shift()!.answer(makeDetail("alpha", { name: "Before Unmount" })));
    await settle();
    const beforeUnmountAdopt = latest.adopt;
    act(() => root.unmount());
    expect(() => beforeUnmountAdopt(makeDetail("alpha", { name: "After Unmount" }))).not.toThrow();
    // Recreate root for afterEach cleanup.
    root = createRoot(host);
  });
});
