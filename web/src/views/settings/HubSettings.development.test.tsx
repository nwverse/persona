// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import type { HubUpdateStatus } from "../../lib/hub-update";

let status: HubUpdateStatus;
const saved: unknown[] = [];
vi.mock("../../lib/hub-update", async (original) => ({
  ...await original<typeof import("../../lib/hub-update")>(),
  hubUpdate: async () => status,
  hubUpdateHistory: async () => [],
  setHubUpdateSettings: async (input: { source?: "release" | "web-dev" }) => {
    saved.push(input);
    status = { ...status, ...input, auto: false };
    return status;
  },
}));
vi.mock("../../lib/store", async (original) => ({
  ...await original<typeof import("../../lib/store")>(),
  onHubEvent: () => () => { },
  useStore: () => [],
}));
vi.mock("./ExtensionDownloadMirrors", () => ({ ExtensionDownloadMirrors: () => null }));
const { HubSettings } = await import("./HubSettings");
let root: ReturnType<typeof createRoot> | undefined;
let host: HTMLDivElement;
afterEach(() => { act(() => root?.unmount()); host?.remove(); saved.length = 0; });

async function mount(state = "available") {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  await initI18n("en");
  status = {
    version: "web-dev-old", build: 202611130000, channel: "release", source: "web-dev", can_update: true,
    state, busy: [], auto: false, auto_notice: false, confirmed: true, auto_update_nodes: false,
    development: { configured: true, branch: "master", resolved_branch: "main", mapped_master: true, current_commit: "a".repeat(40) },
    latest: { version: "web-dev-new", build: 202611130001, newer: true, bad: false, commit: "b".repeat(40), branch: "main" },
    backup: { id: "snapshot-fixture", retained: 3, pruned: ["snapshot-old"] }
  };
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  await act(async () => root!.render(<HubSettings />));
}

it("shows actual source/commits and verified retention, and switches only after the save", async () => {
  await mount();
  expect(host.textContent).toContain("verified default branch");
  expect(host.textContent).toContain("a".repeat(12));
  expect(host.textContent).toContain("b".repeat(12));
  expect(host.textContent).toContain("3 / 3");
  const automatic = host.querySelector('[role="switch"]') as HTMLButtonElement;
  expect(automatic.getAttribute("aria-checked")).toBe("false");
  const source = host.querySelector('select[aria-label="Update source"]') as HTMLSelectElement;
  await act(async () => { source.value = "release"; source.dispatchEvent(new Event("change", { bubbles: true })); });
  expect(saved).toEqual([{ source: "release" }]);
  expect(host.textContent).not.toContain("snapshot-fixture");
});

it.each(["building", "backing_up"])("keeps channel and duplicate actions disabled while %s", async state => {
  await mount(state);
  expect((host.querySelector("select") as HTMLSelectElement).disabled).toBe(true);
  expect([...host.querySelectorAll("button")].some(button => button.textContent === "Update hub")).toBe(false);
  expect(host.textContent).toContain(state === "building" ? "Building" : "Backing up");
});
