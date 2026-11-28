// @vitest-environment jsdom
// The offline sheet's "Start and reconnect" (docs/design/machine-cloud-ops.md "Start and reconnect", "The web"): only
// for a computer bound to an instance that offers start, first in the "do" part before Reconnect; one press sends the
// start with its confirmation and reconnect; the recovery's phase follows until it ends; a failure says why and
// leaves Reconnect usable. Without a binding the part is as it was, with no cloud read of the computer.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { resetCloudStore } from "../../lib/cloud-hosts";
import type { CloudActionKind, CloudBinding, CloudHost, CloudInstanceState, CloudRecovery } from "../../lib/extension-wire";
import { cloudAccountID, cloudInstanceID, cloudMachineID, cloudProviderID } from "../../lib/extension-wire";
import type { Machine } from "../../lib/types";

vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});

const { OfflineSection } = await import("./AlertOffline");
const { RECOVERY_POLL_MS } = await import("./CloudOffline");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

type Call = { method: string; path: string; body?: unknown };
let calls: Call[] = [];
let routes: Record<string, (body: unknown) => Response> = {};
const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { "Content-Type": "application/json" } });
function fetcher(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const url = new URL(String(input), "http://x");
  const method = init?.method ?? "GET";
  const body = init?.body ? JSON.parse(String(init.body)) : undefined;
  calls.push({ method, path: url.pathname, body });
  const route = routes[`${method} ${url.pathname}`];
  return Promise.resolve(route ? route(body) : json({ error: "not found", code: "not_found" }, 404));
}

const spot: Machine = { id: "spot", name: "Spot runner", online: false, kind: "ssh", last_seen: new Date(Date.now() - 3_600_000).toISOString(), error: "dial tcp: i/o timeout" };
const binding: CloudBinding = { machine_id: cloudMachineID("spot"), extension: "example-cloud", provider: cloudProviderID("main"), account: cloudAccountID("team"), instance: cloudInstanceID("vm-1"), bound_at: "2026-11-01T00:00:00Z", vendor: "examplecloud", state: "preempted" };
const host = (actions: CloudActionKind[], state: CloudInstanceState = "preempted"): CloudHost => ({
  extension: "example-cloud", provider: cloudProviderID("main"), provider_name: "Example Cloud", vendor: "examplecloud", machine_id: cloudMachineID("spot"),
  instance: { account: cloudAccountID("team"), id: cloudInstanceID("vm-1"), state, lifecycle: { spot: true } }, actions: actions.map((action) => ({ action, safety: "confirm" })),
});

let el: HTMLDivElement;
let root: Root;
const tick = async (ms = 0) => {
  for (let i = 0; i < 10; i++) await act(async () => { await vi.advanceTimersByTimeAsync(ms / 10); });
};
// the cloud part's chunk loads on its own: wait for it (bounded) rather than a fixed number of ticks
const until = async (ok: () => boolean) => {
  for (let i = 0; i < 200 && !ok(); i++) await tick(10);
  expect(ok()).toBe(true);
};
const $ = (sel: string) => document.body.querySelector<HTMLElement>(sel);
const doButtons = () => [...document.querySelectorAll<HTMLButtonElement>('[data-part="do"] button')].map((b) => b.dataset.testid);

function render(onReconnect = vi.fn()) {
  act(() => root.render(<OfflineSection machine={spot} reason="timeout" onReconnect={onReconnect} onRedeploy={() => {}} />));
  return onReconnect;
}

beforeEach(() => {
  calls = [];
  routes = {};
  resetCloudStore();
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  vi.stubGlobal("fetch", vi.fn(fetcher));
  el = document.createElement("div");
  document.body.append(el);
  root = createRoot(el);
});

afterEach(() => {
  act(() => root.unmount());
  el.remove();
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
  vi.useRealTimers();
  resetCloudStore();
});

describe("the offline sheet with a cloud host", () => {
  it("stays as it was without a binding, and never reads the computer's cloud host", async () => {
    routes["GET /api/v1/extensions/-/cloud"] = () => json({ providers: [] });
    routes["GET /api/v1/extensions/-/cloud/bindings"] = () => json({ bindings: [] });
    render();
    await tick(50);
    expect(doButtons()).toEqual(["reconnect"]);
    expect($("[data-testid=reconnect]")?.className).toContain("bg-ink");
    expect(calls.some((c) => c.path === "/api/v1/machines/spot/cloud")).toBe(false);
  });

  it("stays as it was on an older hub", async () => {
    render();
    await tick(50);
    expect(doButtons()).toEqual(["reconnect"]);
    expect($("[data-testid=cloud-offline]")).toBeNull();
  });

  it("puts Start and reconnect first, sends it in one press, and follows the recovery to done", async () => {
    let recovery: CloudRecovery | undefined;
    routes["GET /api/v1/extensions/-/cloud"] = () => json({ providers: [{ extension: "example-cloud", id: "main", name: "Example Cloud", vendor: "examplecloud" }] });
    routes["GET /api/v1/extensions/-/cloud/bindings"] = () => json({ bindings: [binding] });
    routes["GET /api/v1/machines/spot/cloud"] = () => json({ bound: true, binding, host: host(recovery ? [] : ["start"], recovery ? "starting" : "preempted"), recovery });
    routes["POST /api/v1/machines/spot/cloud/actions"] = () => {
      recovery = { phase: "starting", started_at: new Date().toISOString() };
      return json({ status: "accepted", recovery }, 202);
    };
    render();
    await until(() => !!$("[data-testid=cloud-start-reconnect]"));
    expect(doButtons()).toEqual(["cloud-start-reconnect", "reconnect"]);
    expect($("[data-testid=cloud-start-reconnect]")?.textContent).toBe(t("machines-cloud.start_reconnect"));
    await act(async () => { $("[data-testid=cloud-start-reconnect]")!.click(); });
    await tick(50);
    expect(calls.filter((c) => c.method === "POST").map((c) => c.body)).toEqual([{ action: "start", confirm: true, reconnect: true }]);
    expect($("[data-testid=cloud-recovery]")?.dataset.phase).toBe("starting");
    expect($("[data-testid=cloud-recovery]")?.textContent).toBe(t("machines-cloud.recovery_starting"));
    recovery = { ...recovery!, phase: "reconnecting" };
    await tick(RECOVERY_POLL_MS + 50);
    expect($("[data-testid=cloud-recovery]")?.dataset.phase).toBe("reconnecting");
    recovery = { ...recovery, phase: "done", ip_changed: true };
    await tick(RECOVERY_POLL_MS + 50);
    expect($("[data-testid=cloud-recovery]")?.dataset.phase).toBe("done");
    expect($("[data-testid=cloud-recovery]")?.textContent).toContain(t("machines-cloud.ip_changed"));
    const reads = calls.filter((c) => c.path === "/api/v1/machines/spot/cloud").length;
    await tick(RECOVERY_POLL_MS * 3);
    expect(calls.filter((c) => c.path === "/api/v1/machines/spot/cloud").length, "done: no more reads").toBe(reads);
  });

  it("keeps following the recovery after a poll fails", async () => {
    let recovery: CloudRecovery | undefined;
    let fail = false;
    routes["GET /api/v1/extensions/-/cloud"] = () => json({ providers: [] });
    routes["GET /api/v1/extensions/-/cloud/bindings"] = () => json({ bindings: [binding] });
    routes["GET /api/v1/machines/spot/cloud"] = () => fail
      ? json({ error: "The provider didn't answer in time.", code: "deadline" }, 504)
      : json({ bound: true, binding, host: host(recovery ? [] : ["start"], recovery ? "starting" : "preempted"), recovery });
    routes["POST /api/v1/machines/spot/cloud/actions"] = () => {
      recovery = { phase: "starting", started_at: new Date().toISOString() };
      return json({ status: "accepted", recovery }, 202);
    };
    render();
    await until(() => !!$("[data-testid=cloud-start-reconnect]"));
    await act(async () => { $("[data-testid=cloud-start-reconnect]")!.click(); });
    await tick(50);
    const reads = () => calls.filter((c) => c.path === "/api/v1/machines/spot/cloud").length;
    const before = reads();
    fail = true;
    await tick(RECOVERY_POLL_MS + 50);
    expect(reads()).toBe(before + 1);
    expect($("[data-testid=cloud-recovery]")?.dataset.phase, "a failed poll keeps the last phase").toBe("starting");
    fail = false;
    recovery = { ...recovery!, phase: "done" };
    await tick(RECOVERY_POLL_MS + 50);
    expect(reads(), "a failed poll schedules the next one").toBe(before + 2);
    expect($("[data-testid=cloud-recovery]")?.dataset.phase).toBe("done");
    expect(($("[data-testid=cloud-start-reconnect]") as HTMLButtonElement | null)?.disabled ?? false).toBe(false);
  });

  it("says why a recovery failed and keeps Reconnect usable", async () => {
    routes["GET /api/v1/extensions/-/cloud"] = () => json({ providers: [] });
    routes["GET /api/v1/extensions/-/cloud/bindings"] = () => json({ bindings: [binding] });
    routes["GET /api/v1/machines/spot/cloud"] = () => json({ bound: true, binding, host: host(["start"]) });
    routes["POST /api/v1/machines/spot/cloud/actions"] = () => json({ error: "No capacity for this spot server now.", code: "capacity" }, 503);
    const reconnect = render();
    await until(() => !!$("[data-testid=cloud-start-reconnect]"));
    await act(async () => { $("[data-testid=cloud-start-reconnect]")!.click(); });
    await tick(50);
    expect($("[data-cloud-error=capacity]")?.textContent).toContain("No capacity for this spot server now.");
    const again = $("[data-testid=reconnect]") as HTMLButtonElement;
    expect(again.disabled).toBe(false);
    await act(async () => { again.click(); });
    expect(reconnect).toHaveBeenCalledWith("spot");
  });

  it("shows the recovery's failure from the hub", async () => {
    routes["GET /api/v1/extensions/-/cloud"] = () => json({ providers: [] });
    routes["GET /api/v1/extensions/-/cloud/bindings"] = () => json({ bindings: [binding] });
    routes["GET /api/v1/machines/spot/cloud"] = () => json({ bound: true, binding, host: host(["start"]), recovery: { phase: "failed", started_at: "2026-11-27T00:00:00Z", error: "The server didn't start in time." } });
    render();
    await until(() => !!$("[data-testid=cloud-recovery]"));
    expect($("[data-testid=cloud-recovery]")?.textContent).toBe(t("machines-cloud.recovery_failed_reason", { error: "The server didn't start in time." }));
    expect(doButtons()).toEqual(["cloud-start-reconnect", "reconnect"]);
  });

  it("offers nothing more when the bound host does not offer start", async () => {
    routes["GET /api/v1/extensions/-/cloud"] = () => json({ providers: [] });
    routes["GET /api/v1/extensions/-/cloud/bindings"] = () => json({ bindings: [binding] });
    routes["GET /api/v1/machines/spot/cloud"] = () => json({ bound: true, binding, host: host(["stop"], "running") });
    render();
    await until(() => calls.some((c) => c.path === "/api/v1/machines/spot/cloud"));
    await tick(50);
    expect(doButtons()).toEqual(["reconnect"]);
    expect($("[data-testid=reconnect]")?.className).toContain("bg-ink");
  });
});
