// @vitest-environment jsdom
// Overview Computers switches between the exact Machines summary and the original rows, with one browser preference.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resetManagerChoice } from "../lib/manager-choice";
import { storedValue } from "../lib/compatibility";
import { loadPages } from "../lib/lazy-page";
import { resetRoster } from "../lib/managers";
import type { Machine, MachineMetrics, MetricPoint } from "../lib/types";
import {
  OVERVIEW_MACHINE_VIEW_KEY,
  OverviewMachineCards,
  OverviewMachineRows,
  OverviewMachineViewSwitch,
  useOverviewMachineView,
} from "./OverviewMachineCards";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  const kept = new Map<string, string>();
  g.localStorage = { getItem: (k: string) => kept.get(k) ?? null, setItem: (k: string, v: string) => void kept.set(k, v), removeItem: (k: string) => void kept.delete(k) };
});

const GB = 1024 ** 3;
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const now = Math.floor(Date.now() / 1000);
const sample: MachineMetrics["latest"] = {
  t: now,
  cpu: 42.2,
  load: 2.5,
  mem_used: 32 * GB,
  mem_total: 64 * GB,
  disk_used: 750 * GB,
  disk_total: 1000 * GB,
  net_rx: 1024 * 1024,
  net_tx: 512 * 1024,
};
const points: MetricPoint[] = [
  { t: now - 120, cpu: 20, mem: 16 * GB, rx: 256 * 1024, tx: 128 * 1024 },
  { t: now - 60, cpu: 30, mem: 24 * GB, rx: 512 * 1024, tx: 256 * 1024 },
  { t: now, cpu: 42.2, mem: 32 * GB, rx: 1024 * 1024, tx: 512 * 1024 },
];
const machines: Machine[] = [
  { id: "mac", name: "Mac", online: true, system: { name: "macOS" } } as Machine,
  { id: "box", name: "Box", online: false, error: "Offline" } as Machine,
  { id: "new", name: "New", online: true } as Machine,
  { id: "pending", name: "Pending", online: true, trust: "pending" } as Machine,
];
const metrics: MachineMetrics[] = [
  { machine_id: "mac", online: true, latest: sample, points },
  { machine_id: "box", online: false, latest: sample, points },
  { machine_id: "new", online: true, points: [] },
  { machine_id: "pending", online: true, latest: sample, points },
];
const json = (value: unknown) => new Response(JSON.stringify(value), { status: 200, headers: { "Content-Type": "application/json" } });
const offlineText = (machine: Machine) => machine.error || "Offline";

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 20; i++) await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
};
const metricCalls = () => vi.mocked(fetch).mock.calls.map(([input]) => String(input)).filter((path) => path === "/api/v1/metrics");
const option = (label: string) => [...host.querySelectorAll<HTMLButtonElement>("button[role=radio]")].find((button) => button.textContent === label)!;
const click = (element: Element) => act(() => { element.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true, button: 0 })); });

function ViewHarness() {
  const [view, choose] = useOverviewMachineView();
  return (
    <>
      <OverviewMachineViewSwitch value={view} onChange={choose} />
      {view === "cards"
        ? <OverviewMachineCards machines={machines} agentCounts={new Map([["mac", 2]])} offlineText={offlineText} />
        : <OverviewMachineRows machines={machines} agentCounts={new Map([["mac", 2]])} offlineText={offlineText} />}
    </>
  );
}

beforeEach(async () => {
  resetRoster();
  resetManagerChoice();
  localStorage.removeItem(OVERVIEW_MACHINE_VIEW_KEY);
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
    const path = String(input).split("?")[0];
    return Promise.resolve(path === "/api/v1/metrics" ? json({ machines: metrics }) : json({ error: "not found" }));
  }));
  history.replaceState(null, "", "/overview");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  await loadPages();
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("Overview computer monitoring cards", () => {
  it("uses one section read and renders the exact Machines summary with its charts", async () => {
    act(() => root.render(<OverviewMachineCards machines={machines} agentCounts={new Map([["mac", 2]])} offlineText={offlineText} />));
    await flush();

    expect(metricCalls()).toHaveLength(1);
    expect(vi.mocked(fetch).mock.calls.map(([input]) => String(input)).filter((path) => /\/machines\/[^/]+\/metrics/.test(path))).toHaveLength(0);
    expect(host.querySelectorAll("[data-machine-line]")).toHaveLength(4);

    const card = host.querySelector<HTMLAnchorElement>("a[href='/machines/mac']")!;
    const summary = card.querySelector("[data-testid='machine-metrics-summary']")!;
    expect(summary.getAttribute("data-compact")).toBeNull();
    expect(summary.querySelectorAll("svg")).toHaveLength(3);
    expect(summary.textContent).toContain("42%");
    expect(summary.textContent).toContain("50%");
    expect(summary.textContent).toContain("1.00 MB/s");
    expect(card.textContent).toContain("2 个 Agent");
    expect(card.classList.contains("h-full")).toBe(true);

    expect(host.querySelector("a[href='/machines/box'] [data-testid='machine-metrics-summary']")).toBeNull();
    expect(host.querySelector("a[href='/machines/new'] [data-testid='machine-metrics-summary']")).toBeNull();
    expect(host.querySelector("a[href='/machines/pending'] [data-testid='machine-metrics-summary']")).toBeNull();
    expect(host.querySelector("a[href='/machines/box'] [data-machine-line]")?.textContent).toBe("Offline");
    expect(host.querySelector("a[href='/machines/box']")?.classList.contains("self-start")).toBe(true);
  });

  it("opens the computer from its metrics as well as its header", async () => {
    act(() => root.render(<OverviewMachineCards machines={machines.slice(0, 1)} offlineText={offlineText} />));
    await flush();
    const value = host.querySelector("a[href='/machines/mac'] [data-testid='machine-metrics-summary'] > div > div")!;
    click(value);
    expect(location.pathname).toBe("/machines/mac");
  });

  it("switches immediately, persists Rows without metric requests, and restores Cards on demand", async () => {
    let generation = 0;
    act(() => root.render(<ViewHarness key={generation} />));
    await flush();
    expect(host.querySelector("[role=radiogroup]")?.getAttribute("aria-label")).toBe("显示方式");
    expect(option("卡片").getAttribute("aria-checked")).toBe("true");
    expect(option("行").getAttribute("aria-checked")).toBe("false");
    expect(metricCalls()).toHaveLength(1);

    click(option("行"));
    await flush();
    expect(storedValue(OVERVIEW_MACHINE_VIEW_KEY)).toBe("rows");
    expect(option("行").getAttribute("aria-checked")).toBe("true");
    expect(option("卡片").getAttribute("aria-checked")).toBe("false");
    expect(host.querySelector("[data-testid='machine-metrics-summary']")).toBeNull();
    expect(host.querySelector("a[href='/machines/mac']")?.className).toBe("flex items-center gap-3 rounded-card bg-surface px-3 py-2.5 shadow-card transition-colors hover:bg-hover");

    await act(async () => { vi.advanceTimersByTime(10_000); });
    await flush();
    expect(metricCalls()).toHaveLength(1);

    // Remounting is the component-level reload: the saved choice wins and Cards' reader stays unmounted.
    generation++;
    act(() => root.render(<ViewHarness key={generation} />));
    await flush();
    expect(option("行").getAttribute("aria-checked")).toBe("true");
    expect(metricCalls()).toHaveLength(1);
    expect(host.querySelector("[data-testid='machine-metrics-summary']")).toBeNull();

    click(option("卡片"));
    await flush();
    expect(storedValue(OVERVIEW_MACHINE_VIEW_KEY)).toBe("cards");
    expect(option("卡片").getAttribute("aria-checked")).toBe("true");
    expect(metricCalls()).toHaveLength(2);
    expect(host.querySelectorAll("[data-testid='machine-metrics-summary'] svg")).toHaveLength(3);
  });
});
