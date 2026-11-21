// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import type { Machine } from "../../lib/types";
import { ToolchainPanel, type MachineToolchainReport } from "./ToolchainPanel";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const json = (v: unknown, status = 200) =>
  new Response(JSON.stringify(v), { status, headers: { "Content-Type": "application/json" } });

const sampleReport: MachineToolchainReport = {
  collected_at: "2026-11-21T10:00:00Z",
  cached: true,
  cache_ttl_sec: 60,
  duration_ms: 42,
  os: "linux",
  arch: "amd64",
  tools: [
    { id: "git", category: "core", found: true, version: "2.47.1", path: "/usr/bin/git", resolved_path: "/usr/bin/git", status: "ready" },
    { id: "go", category: "core", found: true, version: "1.24.2", path: "/usr/local/go/bin/go", resolved_path: "/usr/local/go/bin/go", status: "ready" },
    { id: "node", category: "core", found: true, version: "22.14.0", path: "/home/dev/.nvm/versions/node/v22.14.0/bin/node", resolved_path: "/home/dev/.nvm/versions/node/v22.14.0/bin/node", status: "ready" },
    { id: "gh", category: "core", found: true, version: "2.67.0", path: "/usr/local/bin/gh", resolved_path: "/usr/local/bin/gh", status: "ready", capabilities: ["authenticated"] },
    { id: "docker", category: "core", found: true, version: "27.5.1", path: "/usr/bin/docker", resolved_path: "/usr/bin/docker", status: "ready", capabilities: ["daemon_accessible"] },
    { id: "pg_dump", category: "core", found: false, status: "missing" },
  ],
  agent_clis: [
    { id: "claude", category: "agent", found: true, version: "2.1.45", path: "/home/dev/.local/bin/claude", resolved_path: "/opt/claude/claude", status: "ready", capabilities: ["symlink"] },
    { id: "codex", category: "agent", found: true, version: "0.118.0", path: "/home/dev/.nvm/versions/node/v22.14.0/bin/codex", resolved_path: "/home/dev/.nvm/versions/node/v22.14.0/bin/codex", status: "ready" },
    { id: "agy", category: "agent", found: false, status: "missing" },
    { id: "omp", category: "agent", found: false, status: "missing" },
  ],
  docker: {
    installed: true,
    client_version: "27.5.1",
    server_version: "27.5.1",
    socket_path: "/var/run/docker.sock",
    socket_accessible: true,
    status: "ready",
  },
  github_cli: {
    installed: true,
    version: "2.67.0",
    authenticated: true,
    hosts: ["github.com"],
    status: "authenticated",
  },
  hardware: {
    gpus: [{ name: "NVIDIA GeForce RTX 4090", vendor: "nvidia", vram_mb: 24576, driver: "560.35.03" }],
    power: { has_battery: true, battery_percent: 82, charging: true, source: "ac", sleep_state: "deep" },
  },
  environment: {
    shell: "zsh",
    path_entries: ["/home/dev/.nvm/versions/node/v22.14.0/bin", "/usr/local/go/bin", "/usr/bin"],
    version_managers: ["nvm", "mise"],
    has_custom_go_path: true,
    proxy_configured: true,
  },
};

let host: HTMLDivElement;
let root: Root;

const flush = async () => {
  for (let i = 0; i < 6; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
};

beforeEach(async () => {
  await initI18n("en");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

describe("ToolchainPanel", () => {
  it("loads toolchain profile on demand and supports manual refresh", async () => {
    const requested: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        requested.push(url);
        const live = url.includes("refresh=1");
        return Promise.resolve(json({ ...sampleReport, cached: !live }));
      }),
    );

    const machines: Machine[] = [
      { id: "dev-box", name: "Dev Box", online: true },
      { id: "offline-box", name: "Offline Box", online: false },
    ];
    act(() => root.render(<ToolchainPanel machines={machines} />));
    await flush();

    expect(requested).toEqual(["/api/v1/machines/dev-box/toolchain"]);
    expect(host.textContent).toContain("Development toolchain & environment");
    expect(host.textContent).toContain("v2.47.1");
    expect(host.textContent).toContain("v22.14.0");
    expect(host.textContent).toContain("/var/run/docker.sock");
    expect(host.textContent).toContain("github.com");
    expect(host.textContent).toContain("NVIDIA GeForce RTX 4090");
    expect(host.textContent).toContain("24 GB VRAM");
    expect(host.textContent).toContain("82%");
    expect(host.textContent).toContain("Configured (redacted)");

    // Clicking Refresh sends ?refresh=1 without background polling.
    const refreshBtn = [...host.querySelectorAll("button")].find((b) => b.textContent?.includes("Refresh"));
    expect(refreshBtn).toBeTruthy();
    act(() => refreshBtn!.click());
    await flush();

    expect(requested).toEqual(["/api/v1/machines/dev-box/toolchain", "/api/v1/machines/dev-box/toolchain?refresh=1"]);
    expect(host.textContent).toContain("Fresh probe");

    // Switching to an offline machine shows the offline notice without firing a request.
    const offlineTab = [...host.querySelectorAll('[role="tab"]')].find((b) => b.textContent?.includes("Offline Box")) as HTMLButtonElement;
    expect(offlineTab).toBeTruthy();
    act(() => offlineTab.click());
    await flush();

    expect(requested).toHaveLength(2);
    expect(host.textContent).toContain("This computer is offline");
  });

  it("shows an update hint when an older node does not implement machine.toolchain", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.resolve(json({ error: "unknown method: machine.toolchain" }, 500))),
    );
    // Test 409 / unknown method classification via ApiError.
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.resolve(json({ error: "unknown method: machine.toolchain" }, 409))),
    );

    const machines: Machine[] = [{ id: "old-node", name: "Old Node", online: true }];
    act(() => root.render(<ToolchainPanel machines={machines} />));
    await flush();

    expect(host.textContent).toContain("does not support toolchain profiling yet");
  });
});
