// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { t } from "../i18n";
const fake = vi.hoisted(() => ({ paired: vi.fn() }));
vi.mock("../lib/api", () => ({ errorText: (e: Error) => e.message, setPairedToken: fake.paired }));
vi.mock("./settings/LoginSteps", () => ({ QRCode: () => <svg aria-label="sign-in QR" /> }));
import { BrowserLoginQR } from "./BrowserLoginQR";
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root, host: HTMLDivElement;
let status = "pending";
let pollCount = 0;
let starts = 0;
let onDone = vi.fn<() => void>();
const reply = (data: unknown) => Promise.resolve({ ok: true, json: () => Promise.resolve(data) });
beforeEach(() => {
 vi.useFakeTimers(); fake.paired.mockReset(); status = "pending"; pollCount = 0; starts = 0; onDone = vi.fn<() => void>();
 Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
 vi.stubGlobal("fetch", vi.fn((url: string) => {
  if (url.endsWith("/start")) { starts++; return reply({ id: `request-${starts}`, secret: "browser-only", expires_at: new Date(Date.now()+300000).toISOString(), qr: { rows: ["1"] } }); }
  pollCount++;
  return reply(status === "approved" ? { state: status, token: "device-token", id: "device-id" } : { state: status });
 }));
 host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); vi.useRealTimers(); vi.unstubAllGlobals(); });
async function mount() { await act(async () => { root.render(<BrowserLoginQR onDone={onDone} />); }); }
async function tick(ms = 1500) { await act(async () => { await vi.advanceTimersByTimeAsync(ms); }); }
it("waits for phone approval before storing the browser credential", async () => {
 await mount(); expect(fake.paired).not.toHaveBeenCalled();
 status = "scanned"; await tick(); expect(host.textContent).toContain(t("login.qr_scanned"));
 expect(fake.paired).not.toHaveBeenCalled(); status = "approved"; await tick();
 expect(fake.paired).toHaveBeenCalledExactlyOnceWith("device-token", "device-id"); expect(onDone).toHaveBeenCalledOnce();
 await tick(6000); expect(fake.paired).toHaveBeenCalledOnce();
});
it("stops after denial and refreshes into a new request", async () => {
 await mount(); status = "denied"; await tick(); expect(host.textContent).toContain(t("login.qr_denied"));
 const n = pollCount; await tick(6000); expect(pollCount).toBe(n);
 status = "pending"; await act(async () => host.querySelector<HTMLButtonElement>("button")!.click());
 expect(starts).toBe(2); expect(fake.paired).not.toHaveBeenCalled();
});
it("pauses network polling while hidden and refuses an expired code", async () => {
 await mount(); const n = pollCount;
 Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
 await tick(6000); expect(pollCount).toBe(n);
 await tick(300000); expect(host.textContent).toContain(t("login.qr_expired")); expect(fake.paired).not.toHaveBeenCalled();
});
