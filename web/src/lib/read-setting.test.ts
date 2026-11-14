// Manual mark as read's switch (#7356): off unless the hub says on, kept for the next reload, an old hub (404) is off
// and cannot be turned on, a refused write puts back what was shown, and an older answer does not undo a newer flip.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { manualRead, setManualReadLocal } from "./manual-read";
import { getReadSetting, refreshReadSetting, resetReadSettingForTest, setManualRead } from "./read-setting";

interface Held {
  method: string;
  body: unknown;
  answer: (body: unknown, status?: number) => void;
}

/** Every /read-settings request waits until the test answers it. */
function heldHub(): Held[] {
  const held: Held[] = [];
  vi.stubGlobal("fetch", vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (!url.includes("/read-settings")) return Promise.resolve(new Response("{}", { status: 200 }));
    return new Promise<Response>((r) => held.push({
      method: init?.method ?? "GET",
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
      answer: (body, status = 200) => r(new Response(JSON.stringify(body), { status })),
    }));
  }));
  return held;
}

const settle = () => new Promise((r) => setTimeout(r, 0));
let stored: Record<string, string> = {};

beforeEach(() => {
  stored = {};
  vi.stubGlobal("localStorage", {
    getItem: (k: string) => (k in stored ? stored[k] : k.includes("token") ? "mock-token" : null),
    setItem: (k: string, v: string) => { stored[k] = v; },
    removeItem: (k: string) => { delete stored[k]; },
  });
  resetReadSettingForTest();
});

afterEach(() => {
  resetReadSettingForTest();
  vi.unstubAllGlobals();
});

describe("manual mark as read switch", () => {
  it("is off until the hub answers, and off on a hub that never stored it", async () => {
    const held = heldHub();
    expect(getReadSetting()).toMatchObject({ manual: false, known: false });
    const read = refreshReadSetting();
    await settle();
    held[0].answer({ manual: false, can_write: true });
    await read;
    expect(getReadSetting()).toEqual({ manual: false, known: true, canWrite: true, supported: true });
    expect(manualRead()).toBe(false);
  });

  it("takes the hub's on, for the store and the next reload", async () => {
    const held = heldHub();
    const read = refreshReadSetting();
    await settle();
    held[0].answer({ manual: true, can_write: true });
    await read;
    expect(getReadSetting().manual).toBe(true);
    expect(manualRead()).toBe(true);
    expect(stored["persona.manualRead"]).toBe("1");
  });

  it("turns it on with a PUT, and puts it back when the hub refuses", async () => {
    const held = heldHub();
    const on = setManualRead(true);
    await settle();
    expect(held[0]).toMatchObject({ method: "PUT", body: { manual: true } });
    expect(manualRead()).toBe(true); // shown at once
    held[0].answer({ manual: true, can_write: true });
    await on;
    expect(getReadSetting()).toMatchObject({ manual: true, known: true });

    const off = setManualRead(false).catch(() => "refused");
    await settle();
    held[1].answer({ error: "read-only" }, 403);
    expect(await off).toBe("refused");
    expect(manualRead()).toBe(true);
  });

  it("is off and unsupported on a hub from before the switch", async () => {
    setManualReadLocal(true); // the hub before had it on (a reload starts from that)
    expect(stored["persona.manualRead"]).toBe("1");
    const held = heldHub();
    const read = refreshReadSetting();
    await settle();
    held[0].answer({ error: "not found" }, 404);
    await read;
    expect(getReadSetting()).toEqual({ manual: false, known: true, canWrite: false, supported: false });
    expect(manualRead()).toBe(false);
    expect(stored["persona.manualRead"]).toBeUndefined();
  });

  it("does not let a read on its way undo a flip", async () => {
    const held = heldHub();
    const read = refreshReadSetting();
    await settle();
    const write = setManualRead(true);
    await settle();
    held[1].answer({ manual: true, can_write: true });
    await write;
    held[0].answer({ manual: false, can_write: true });
    await read;
    expect(getReadSetting().manual).toBe(true);
  });
});
