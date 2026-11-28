// @vitest-environment jsdom
// The computers' list with cloud hosts (docs/design/machine-cloud-ops.md "The web"): a bound computer's card shows a
// small vendor badge with the last state, read from the bindings (no program asked); with a provider enabled, a quiet
// line under the list reads the instances only when opened and shows the unbound ones, each with "Link…" choosing the
// computer. Loading the list never asks for the instances.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../i18n";
import { resetCloudStore } from "../lib/cloud-hosts";
import type { CloudBinding, CloudHost } from "../lib/extension-wire";
import { cloudAccountID, cloudInstanceID, cloudMachineID, cloudProviderID } from "../lib/extension-wire";
import type { Machine } from "../lib/types";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  HTMLElement.prototype.scrollTo ??= () => {};
  HTMLElement.prototype.scrollIntoView ??= () => {};
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  const kept = new Map<string, string>();
  g.localStorage = { getItem: (k: string) => kept.get(k) ?? null, setItem: (k: string, v: string) => void kept.set(k, v), removeItem: (k: string) => void kept.delete(k) };
});

const fake = vi.hoisted(() => ({ machines: [] as Machine[] }));
vi.mock("../lib/store", async (importOriginal) => {
  const real = await importOriginal<typeof import("../lib/store")>();
  return { ...real, useStore: <T,>(select: (s: ReturnType<typeof real.getState>) => T) => select({ ...real.getState(), loaded: true, machines: fake.machines, features: [] }) };
});

const { Machines } = await import("./Machines");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

type Call = { method: string; path: string; body?: unknown };
let calls: Call[] = [];
let routes: Record<string, (body: unknown) => Response> = {};
const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { "Content-Type": "application/json" } });
function hub(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const url = new URL(String(input), "http://x");
  const method = init?.method ?? "GET";
  const body = init?.body ? JSON.parse(String(init.body)) : undefined;
  calls.push({ method, path: url.pathname, body });
  const route = routes[`${method} ${url.pathname}`];
  if (route) return Promise.resolve(route(body));
  if (url.pathname === "/api/v1/metrics") return Promise.resolve(json({ machines: [] }));
  return Promise.resolve(json({ error: "not found" }, 404));
}

const PROVIDER = { extension: "example-cloud", id: "main", name: "Example Cloud", vendor: "examplecloud" };
const binding: CloudBinding = { machine_id: cloudMachineID("build"), extension: "example-cloud", provider: cloudProviderID("main"), account: cloudAccountID("team"), instance: cloudInstanceID("i-0abc"), bound_at: "2026-11-01T00:00:00Z", provider_name: "Example Cloud", vendor: "examplecloud", state: "preempted" };
const free = (id: string): CloudHost => ({
  extension: "example-cloud", provider: cloudProviderID("main"), provider_name: "Example Cloud", vendor: "examplecloud",
  instance: { account: cloudAccountID("team"), id: cloudInstanceID(id), display_name: `box ${id}`, state: "stopped" }, actions: [{ action: "start", safety: "confirm" }],
});

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 10; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};
// the line's chunk loads on its own: wait for it (bounded) rather than a fixed number of ticks
const until = async (ok: () => boolean) => {
  for (let i = 0; i < 200 && !ok(); i++) await act(async () => { await new Promise((r) => setTimeout(r, 5)); });
  expect(ok()).toBe(true);
};
const $ = (sel: string) => document.body.querySelector<HTMLElement>(sel);
const click = async (el: Element | null | undefined) => {
  expect(el).toBeTruthy();
  await act(async () => { (el as HTMLElement).click(); });
  await flush();
};

beforeEach(() => {
  calls = [];
  routes = {};
  resetCloudStore();
  fake.machines = [{ id: "build", name: "Build server", online: false }, { id: "nas", name: "NAS", online: true }];
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
  vi.stubGlobal("fetch", vi.fn(hub));
  history.replaceState(null, "", "/machines");
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

describe("Machines with cloud hosts", () => {
  it("draws no badge and asks for no instances on a hub without cloud hosts", async () => {
    act(() => root.render(<Machines onToggleSidebar={() => {}} />));
    await flush();
    expect($("[data-testid=cloud-badge]")).toBeNull();
    expect($("[data-testid=cloud-unbound-line]")).toBeNull();
    expect(calls.some((c) => c.path.endsWith("/cloud/instances"))).toBe(false);
    expect(calls.some((c) => c.path.startsWith("/api/v1/machines/"))).toBe(false);
  });

  it("badges a bound computer with its vendor and last state from the bindings alone", async () => {
    routes["GET /api/v1/extensions/-/cloud"] = () => json({ providers: [] });
    routes["GET /api/v1/extensions/-/cloud/bindings"] = () => json({ bindings: [binding] });
    act(() => root.render(<Machines onToggleSidebar={() => {}} />));
    await flush();
    const badges = [...document.querySelectorAll<HTMLElement>("[data-testid=cloud-badge]")];
    expect(badges).toHaveLength(1);
    expect(badges[0]?.dataset.cloudState).toBe("preempted");
    expect(badges[0]?.textContent).toBe(`examplecloud${t("machines-cloud.state_preempted")}`);
    expect(calls.some((c) => c.path.endsWith("/cloud/instances")), "no provider: no instances").toBe(false);
  });

  it("asks for the instances only when the line is opened, and links one to the computer chosen", async () => {
    routes["GET /api/v1/extensions/-/cloud"] = () => json({ providers: [PROVIDER] });
    routes["GET /api/v1/extensions/-/cloud/bindings"] = () => json({ bindings: [binding] });
    routes["GET /api/v1/extensions/-/cloud/instances"] = () => json({ hosts: [{ ...free("i-0abc"), machine_id: "build" }, free("vm-2"), free("vm-3")], problems: [] });
    act(() => root.render(<Machines onToggleSidebar={() => {}} />));
    await until(() => !!$("[data-testid=cloud-unbound-line]"));
    await flush();
    const instanceReads = () => calls.filter((c) => c.path.endsWith("/cloud/instances")).length;
    expect(instanceReads(), "loading the list asks no program").toBe(0);
    expect($("[data-testid=cloud-unbound-line]")?.textContent).toBe(t("machines-cloud.unbound_open"));
    await click($("[data-testid=cloud-unbound-line]"));
    expect(instanceReads(), "opening the line reads the instances once").toBe(1);
    expect([...document.querySelectorAll<HTMLElement>("[data-cloud-host]")].map((r) => r.dataset.cloudHost)).toEqual(["example-cloud/main/team/vm-2", "example-cloud/main/team/vm-3"]);
    const link = document.querySelector<HTMLElement>("[data-cloud-host='example-cloud/main/team/vm-2'] [data-testid=cloud-unbound-link]");
    expect(link?.getAttribute("aria-label")).toBe(t("machines-cloud.link_to_label", { instance: "box vm-2" }));
    await click(link);
    const items = [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')];
    expect(items.map((i) => i.textContent?.trim()), "only computers not linked yet").toEqual(["NAS"]);
    routes["PUT /api/v1/machines/nas/cloud"] = () => json({ bound: true, binding: { ...binding, machine_id: "nas", instance: "vm-2" } });
    routes["GET /api/v1/extensions/-/cloud/instances"] = () => json({ hosts: [{ ...free("i-0abc"), machine_id: "build" }, { ...free("vm-2"), machine_id: "nas" }, free("vm-3")], problems: [] });
    await click(items[0]);
    expect(calls.find((c) => c.method === "PUT")).toEqual({ method: "PUT", path: "/api/v1/machines/nas/cloud", body: { extension: "example-cloud", provider: "main", account: "team", instance: "vm-2" } });
    expect([...document.querySelectorAll<HTMLElement>("[data-cloud-host]")].map((r) => r.dataset.cloudHost)).toEqual(["example-cloud/main/team/vm-3"]);
  });

  it("says nothing is waiting when every instance is linked", async () => {
    routes["GET /api/v1/extensions/-/cloud"] = () => json({ providers: [PROVIDER] });
    routes["GET /api/v1/extensions/-/cloud/bindings"] = () => json({ bindings: [binding] });
    routes["GET /api/v1/extensions/-/cloud/instances"] = () => json({ hosts: [{ ...free("i-0abc"), machine_id: "build" }], problems: [] });
    act(() => root.render(<Machines onToggleSidebar={() => {}} />));
    await until(() => !!$("[data-testid=cloud-unbound-line]"));
    expect(calls.some((c) => c.path.endsWith("/cloud/instances"))).toBe(false);
    await click($("[data-testid=cloud-unbound-line]"));
    expect($("[data-testid=cloud-host-list-empty]")?.textContent).toBe(t("machines-cloud.link_empty"));
    expect(document.querySelector("[data-cloud-host]")).toBeNull();
  });
});
