// @vitest-environment jsdom
// A computer's "Cloud server" section (docs/design/machine-cloud-ops.md "The web"): nothing without a provider or a
// binding; unbound, a line and a picker of the unbound instances; bound, the instance's state, address and size and its
// own actions, each passing its gate as interface (read at once, confirm in a dialog naming the computer, destructive
// in the danger tone sending the instance's id back), the outcome in the row, a re-read after a write and while the
// instance is on its way; a provider that is off leaves only Unlink.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { resetCloudStore } from "../../lib/cloud-hosts";
import type { CloudActionKind, CloudBinding, CloudHost, CloudHostAction, CloudInstanceState, CloudMachine, CloudSafety } from "../../lib/extension-wire";
import { cloudAccountID, cloudCustomActionID, cloudInstanceID, cloudMachineID, cloudProviderID } from "../../lib/extension-wire";

vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
  HTMLElement.prototype.scrollIntoView ??= () => {};
});

const { CloudSection, CLOUD_POLL_MS } = await import("./CloudSection");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

type Call = { method: string; path: string; search: string; body?: unknown };
let calls: Call[] = [];
let routes: Record<string, (body: unknown, search: string) => Response> = {};
const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { "Content-Type": "application/json" } });

function fetcher(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const url = new URL(String(input), "http://x");
  const method = init?.method ?? "GET";
  const body = init?.body ? JSON.parse(String(init.body)) : undefined;
  calls.push({ method, path: url.pathname, search: url.search, body });
  const route = routes[`${method} ${url.pathname}`];
  return Promise.resolve(route ? route(body, url.search) : json({ error: "not found", code: "not_found" }, 404));
}

const PROVIDER = { extension: "example-cloud", id: "main", name: "Example Cloud", vendor: "examplecloud" };
const a = (action: CloudActionKind, safety: CloudSafety = "confirm"): CloudHostAction => ({ action, safety });
const MAIN = cloudProviderID("main");
const BUILD = cloudMachineID("build");
const hostOf = (over: Partial<CloudHost["instance"]> = {}, actions: CloudHostAction[] = [a("stop"), a("reboot"), a("force_stop", "destructive"), a("sync_ip", "read"), a("console", "read")]): CloudHost => ({
  extension: "example-cloud", provider: MAIN, provider_name: "Example Cloud", vendor: "examplecloud", machine_id: BUILD,
  instance: { account: cloudAccountID("team"), id: cloudInstanceID("i-0abc"), display_name: "build box", state: "running", region: "region-a", zone: "region-a-1", public_ips: ["203.0.113.7"], spec: { cpu: 4, memory_mb: 16384, disk_gb: 100 }, lifecycle: { spot: true }, ...over },
  actions,
});
const binding: CloudBinding = { machine_id: BUILD, extension: "example-cloud", provider: MAIN, account: cloudAccountID("team"), instance: cloudInstanceID("i-0abc"), bound_at: "2026-11-01T00:00:00Z", provider_name: "Example Cloud", vendor: "examplecloud", display_name: "build box", state: "running" };

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 10; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};
const $ = (sel: string) => document.body.querySelector<HTMLElement>(sel);
const click = async (el: Element | null | undefined) => {
  expect(el).toBeTruthy();
  await act(async () => { (el as HTMLElement).click(); });
  await flush();
};
const menuItem = (label: string) => [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].find((e) => e.textContent?.includes(label));
const menuLabels = () => [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].map((e) => e.textContent?.trim());
const posts = () => calls.filter((c) => c.method === "POST");

async function render() {
  act(() => root.render(<CloudSection mid="build" machineName="Build server" heading={(title, right) => <div data-testid="heading">{title}{right}</div>} />));
  await flush();
}

function bound(cm: Partial<CloudMachine> = {}) {
  routes["GET /api/v1/extensions/-/cloud"] = () => json({ providers: [PROVIDER] });
  routes["GET /api/v1/extensions/-/cloud/bindings"] = () => json({ bindings: [binding] });
  routes["GET /api/v1/machines/build/cloud"] = () => json({ bound: true, binding, host: hostOf(), ...cm });
}

beforeEach(() => {
  calls = [];
  routes = {};
  resetCloudStore();
  vi.stubGlobal("fetch", vi.fn(fetcher));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
  vi.useRealTimers();
  resetCloudStore();
});

describe("without cloud hosts", () => {
  it("draws nothing for an older hub, and asks no program", async () => {
    await render();
    expect(host.innerHTML).toBe("");
    expect(calls.map((c) => c.path).sort()).toEqual(["/api/v1/extensions/-/cloud", "/api/v1/extensions/-/cloud/bindings"]);
  });

  it("draws nothing when no provider is enabled and the computer has no binding", async () => {
    routes["GET /api/v1/extensions/-/cloud"] = () => json({ providers: [] });
    routes["GET /api/v1/extensions/-/cloud/bindings"] = () => json({ bindings: [] });
    await render();
    expect(host.innerHTML).toBe("");
  });
});

describe("unbound", () => {
  beforeEach(() => {
    routes["GET /api/v1/extensions/-/cloud"] = () => json({ providers: [PROVIDER] });
    routes["GET /api/v1/extensions/-/cloud/bindings"] = () => json({ bindings: [] });
  });

  it("offers to link one of the unbound instances, and links it", async () => {
    await render();
    expect($("[data-testid=cloud-section]")?.dataset.bound).toBe("no");
    expect(host.textContent).toContain(t("machines-cloud.unbound_line", { machine: "Build server" }));
    const free = hostOf({ id: cloudInstanceID("vm-free"), display_name: "spare" }, [a("start")]);
    routes["GET /api/v1/extensions/-/cloud/instances"] = () => json({
      hosts: [hostOf(), { ...free, machine_id: undefined }],
      problems: [{ extension: "other", provider: "x", provider_name: "Other Cloud", code: "not_signed_in", error: "Sign in again." }],
    });
    await click($("[data-testid=cloud-link-open]"));
    const rows = [...document.querySelectorAll<HTMLElement>("[data-cloud-host]")];
    expect(rows.map((r) => r.dataset.cloudHost)).toEqual(["example-cloud/main/team/vm-free"]);
    expect($("[data-testid=cloud-problems]")?.textContent).toContain("Other Cloud");
    const linked: CloudBinding = { ...binding, instance: cloudInstanceID("vm-free"), display_name: "spare", state: "stopped" };
    routes["PUT /api/v1/machines/build/cloud"] = () => json({ bound: true, binding: linked });
    routes["GET /api/v1/extensions/-/cloud/bindings"] = () => json({ bindings: [linked] });
    routes["GET /api/v1/machines/build/cloud"] = () => json({ bound: true, binding: linked, host: { ...free, machine_id: "build", instance: { ...free.instance, state: "stopped" } } });
    await click($("[data-testid=cloud-link]"));
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ extension: "example-cloud", provider: "main", account: "team", instance: "vm-free" });
    expect($("[data-testid=cloud-section]")?.dataset.bound).toBe("yes");
    expect($("[data-testid=cloud-name]")?.textContent).toBe("spare");
    expect($("[data-testid=cloud-action-start]")).not.toBeNull();
  });

  it("says why a link was refused", async () => {
    await render();
    routes["GET /api/v1/extensions/-/cloud/instances"] = () => json({ hosts: [{ ...hostOf(), machine_id: undefined }], problems: [] });
    routes["PUT /api/v1/machines/build/cloud"] = () => json({ error: "Another computer holds this server.", code: "already_bound", machine_id: "nas" }, 409);
    await click($("[data-testid=cloud-link-open]"));
    await click($("[data-testid=cloud-link]"));
    expect($("[data-cloud-error=already_bound]")?.textContent).toContain("Another computer holds this server.");
  });
});

describe("bound", () => {
  it("shows the provider, state, address, place, size and spot note, with the power actions in the row", async () => {
    bound();
    await render();
    expect($("[data-testid=cloud-name]")?.textContent).toBe("build box");
    expect($("[data-cloud-state]")?.textContent).toBe(t("machines-cloud.state_running"));
    expect($("[data-cloud-vendor]")?.textContent).toBe("examplecloud");
    expect($("[data-testid=cloud-address]")?.textContent).toBe("203.0.113.7");
    expect($("[data-testid=cloud-facts]")?.textContent).toBe("region-a-1 · 4 vCPU · 16 GB · 100 GB");
    expect($("[data-testid=cloud-note]")?.textContent).toBe(t("machines-cloud.note_spot"));
    expect([...document.querySelectorAll("[data-testid=cloud-row-actions] button")].map((b) => b.textContent)).toEqual([t("machines-cloud.action_stop"), t("machines-cloud.action_reboot")]);
    await click(document.querySelector(`button[aria-label="${t("machines-cloud.more")}"]`));
    expect(menuLabels()).toEqual([t("machines-cloud.action_force_stop"), t("machines-cloud.action_sync_ip"), t("machines-cloud.action_console"), t("machines-cloud.unlink")]);
  });

  it("asks before a confirm action, naming the computer, sends confirm, says it is on its way and reads the host again", async () => {
    bound();
    await render();
    routes["POST /api/v1/machines/build/cloud/actions"] = () => json({ status: "accepted" }, 202);
    await click($("[data-testid=cloud-action-stop]"));
    expect(posts(), "the press only asks").toHaveLength(0);
    expect($("[data-testid=cloud-confirm-dialog]")?.dataset.gate).toBe("confirm");
    expect($("[data-testid=cloud-confirm-dialog]")?.textContent).toContain("Build server");
    const reads = calls.filter((c) => c.path === "/api/v1/machines/build/cloud").length;
    await click($("[data-testid=cloud-confirm]"));
    expect(posts()[0]?.body).toEqual({ action: "stop", confirm: true });
    expect($("[data-testid=cloud-confirm-dialog]")).toBeNull();
    expect($("[data-testid=cloud-outcome]")?.textContent).toBe(t("machines-cloud.outcome_accepted", { action: t("machines-cloud.action_stop") }));
    expect(calls.filter((c) => c.path === "/api/v1/machines/build/cloud").length).toBeGreaterThan(reads);
  });

  it("asks a destructive action in the danger tone and sends the instance's id back", async () => {
    bound();
    await render();
    routes["POST /api/v1/machines/build/cloud/actions"] = () => json({ status: "done" });
    await click(document.querySelector(`button[aria-label="${t("machines-cloud.more")}"]`));
    await click(menuItem(t("machines-cloud.action_force_stop")));
    const dialog = $("[data-testid=cloud-confirm-dialog]");
    expect(dialog?.dataset.gate).toBe("destructive");
    expect($("[data-testid=cloud-loss]")?.textContent).toBe(t("machines-cloud.confirm_loss"));
    expect($("[data-testid=cloud-ack]")?.textContent).toBe("i-0abc");
    expect($("[data-testid=cloud-confirm]")?.className).toContain("bg-danger-fill");
    await click($("[data-testid=cloud-confirm]"));
    expect(posts()[0]?.body).toEqual({ action: "force_stop", confirm: true, acknowledge: "i-0abc" });
  });

  it("runs a read action at once and keeps the server's refusal in the row", async () => {
    bound();
    await render();
    routes["POST /api/v1/machines/build/cloud/actions"] = () => json({ error: "The provider refused: try later.", code: "refused" }, 422);
    await click(document.querySelector(`button[aria-label="${t("machines-cloud.more")}"]`));
    await click(menuItem(t("machines-cloud.action_sync_ip")));
    expect($("[data-testid=cloud-confirm-dialog]")).toBeNull();
    expect(posts()[0]?.body).toEqual({ action: "sync_ip" });
    expect($("[data-testid=cloud-outcome] [data-cloud-error=refused]")?.textContent).toContain("The provider refused: try later.");
  });

  it("opens the console the action answers in a new tab without an opener", async () => {
    bound();
    await render();
    const open = vi.fn(() => null);
    vi.stubGlobal("open", open);
    routes["POST /api/v1/machines/build/cloud/actions"] = () => json({ status: "done", url: "https://console.example.com/signed" });
    await click(document.querySelector(`button[aria-label="${t("machines-cloud.more")}"]`));
    await click(menuItem(t("machines-cloud.action_console")));
    expect(open).toHaveBeenCalledWith("https://console.example.com/signed", "_blank", "noopener,noreferrer");
    expect($("[data-testid=cloud-console-link]")?.getAttribute("rel")).toBe("noopener noreferrer");
  });

  it("asks a custom action's own sentence", async () => {
    bound({ host: hostOf({}, [{ action: "custom", id: cloudCustomActionID("renew"), title: "Renew", safety: "confirm", confirm_text: "Renew this server for another term?" }]) });
    await render();
    routes["POST /api/v1/machines/build/cloud/actions"] = () => json({ status: "done", message: "Renewed until March." });
    await click(document.querySelector(`button[aria-label="${t("machines-cloud.more")}"]`));
    await click(menuItem("Renew"));
    expect($("[data-testid=cloud-confirm-dialog]")?.textContent).toContain("Renew this server for another term?");
    await click($("[data-testid=cloud-confirm]"));
    expect(posts()[0]?.body).toEqual({ action: "custom", id: "renew", confirm: true });
    expect($("[data-testid=cloud-outcome]")?.textContent).toBe("Renewed until March.");
  });

  it("lists snapshots and takes one with a name", async () => {
    bound({ host: hostOf({}, [a("snapshot_list", "read"), a("snapshot_create")]) });
    routes["GET /api/v1/machines/build/cloud/snapshots"] = () => json({ snapshots: [{ id: "snap-1", name: "before upgrade", state: "ready" }] });
    routes["POST /api/v1/machines/build/cloud/actions"] = () => json({ status: "accepted" }, 202);
    await render();
    await click(document.querySelector(`button[aria-label="${t("machines-cloud.more")}"]`));
    await click(menuItem(t("machines-cloud.action_snapshots")));
    expect($("[data-testid=cloud-snapshot-list]")?.textContent).toContain("before upgrade");
    const input = $("[data-testid=cloud-snapshot-name]") as HTMLInputElement;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "nightly");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await click($("[data-testid=cloud-snapshot-take]"));
    expect(posts()[0]?.body).toEqual({ action: "snapshot_create", confirm: true, name: "nightly" });
  });

  it("with the provider off shows its words and only Unlink, and unlinks", async () => {
    bound({ host: undefined, problem: { extension: "example-cloud", provider: MAIN, provider_name: "Example Cloud", code: "provider_off", error: "The extension is off." } });
    await render();
    expect($("[data-cloud-problem=provider_off]")?.textContent).toContain("The extension is off.");
    expect($("[data-testid=cloud-row-actions]")).toBeNull();
    await click(document.querySelector(`button[aria-label="${t("machines-cloud.more")}"]`));
    expect(menuLabels()).toEqual([t("machines-cloud.unlink")]);
    routes["DELETE /api/v1/machines/build/cloud"] = () => json({ bound: false });
    routes["GET /api/v1/extensions/-/cloud/bindings"] = () => json({ bindings: [] });
    await click(menuItem(t("machines-cloud.unlink")));
    expect(calls.some((c) => c.method === "DELETE"), "the first choice only asks").toBe(false);
    await click(menuItem(t("machines-cloud.unlink_confirm")));
    expect(calls.some((c) => c.method === "DELETE")).toBe(true);
    expect($("[data-testid=cloud-section]")?.dataset.bound).toBe("no");
  });

  it("refreshes from the vendor on Refresh", async () => {
    bound();
    await render();
    await click(document.querySelector(`button[aria-label="${t("machines-cloud.refresh")}"]`));
    expect(calls.some((c) => c.path === "/api/v1/machines/build/cloud" && c.search === "?refresh=1")).toBe(true);
  });

  it("reads the host again every few seconds while it starts, and stops once it runs or the section is gone", async () => {
    let state: CloudInstanceState = "starting";
    bound();
    routes["GET /api/v1/machines/build/cloud"] = () => json({ bound: true, binding, host: hostOf({ state }, [a("stop")]) });
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const tick = async (ms: number) => {
      for (let i = 0; i < 10; i++) await act(async () => { await vi.advanceTimersByTimeAsync(ms / 10); });
    };
    act(() => root.render(<CloudSection mid="build" machineName="Build server" heading={(title) => <div>{title}</div>} />));
    await tick(50);
    const reads = () => calls.filter((c) => c.path === "/api/v1/machines/build/cloud").length;
    expect(reads()).toBe(1);
    await tick(CLOUD_POLL_MS + 50);
    expect(reads()).toBe(2);
    expect(calls.filter((c) => c.path === "/api/v1/machines/build/cloud").at(-1)?.search).toBe("?refresh=1");
    state = "running";
    await tick(CLOUD_POLL_MS + 50);
    expect(reads()).toBe(3);
    expect($("[data-cloud-state]")?.textContent).toBe(t("machines-cloud.state_running"));
    await tick(CLOUD_POLL_MS * 3);
    expect(reads(), "running: no more reads").toBe(3);
  });

  it("keeps following the host after a poll fails, until it runs", async () => {
    let state: CloudInstanceState = "starting";
    let fail = false;
    bound();
    routes["GET /api/v1/machines/build/cloud"] = () => fail
      ? json({ error: "The provider didn't answer in time.", code: "deadline" }, 504)
      : json({ bound: true, binding, host: hostOf({ state }, [a("stop")]) });
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const tick = async (ms: number) => {
      for (let i = 0; i < 10; i++) await act(async () => { await vi.advanceTimersByTimeAsync(ms / 10); });
    };
    act(() => root.render(<CloudSection mid="build" machineName="Build server" heading={(title) => <div>{title}</div>} />));
    await tick(50);
    const reads = () => calls.filter((c) => c.path === "/api/v1/machines/build/cloud").length;
    expect(reads()).toBe(1);
    fail = true;
    await tick(CLOUD_POLL_MS + 50);
    expect(reads()).toBe(2);
    expect($("[data-cloud-error=deadline]")).toBeTruthy();
    fail = false;
    state = "running";
    await tick(CLOUD_POLL_MS + 50);
    expect(reads(), "a failed poll schedules the next one").toBe(3);
    expect($("[data-cloud-state]")?.textContent).toBe(t("machines-cloud.state_running"));
    expect($("[data-cloud-error]")).toBeNull();
    await tick(CLOUD_POLL_MS * 3);
    expect(reads(), "running: no more reads").toBe(3);
  });
});
