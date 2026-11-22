// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import enCatalog from "../../i18n/locales/en/machines-storage.json";
import zhCatalog from "../../i18n/locales/zh-Hans/machines-storage.json";
import jaCatalog from "../../i18n/locales/ja/machines-storage.json";
import type { Machine } from "../../lib/types";
import { StoragePanel } from "./StoragePanel";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const GB = 1024 ** 3;
const MB = 1024 ** 2;

const json = (v: unknown, status = 200) =>
  new Response(JSON.stringify(v), { status, headers: { "Content-Type": "application/json" } });

let host: HTMLDivElement;
let root: Root;

const flush = async () => {
  for (let i = 0; i < 6; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
};

const click = (el: Element | null) => {
  if (!el) throw new Error("expected clickable element");
  act(() => {
    el.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
  });
};

const machines: Machine[] = [
  { id: "devbox", name: "DevBox", online: true, os: "linux" },
  { id: "offbox", name: "OfflineBox", online: false, os: "linux" },
];

beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

describe("StoragePanel", () => {
  it("loads per-mount capacity and inode warnings without automatically scanning hotspots", async () => {
    const calls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        calls.push(url);
        if (url === "/api/v1/machines/devbox/storage") {
          return Promise.resolve(
            json({
              machine_id: "devbox",
              at: 1000,
              os: "linux",
              supported: true,
              collected_ms: 1,
              mounts: [
                {
                  mount: "/",
                  device: "/dev/vda2",
                  fs_type: "ext4",
                  used_bytes: 40 * GB,
                  avail_bytes: 60 * GB,
                  total_bytes: 100 * GB,
                  used_percent: 40,
                  inodes_total: 1000000,
                  inodes_used: 920000,
                  inodes_free: 80000,
                  inodes_percent: 92,
                },
                {
                  mount: "/data00",
                  device: "/dev/vdb",
                  fs_type: "ext4",
                  used_bytes: 450 * GB,
                  avail_bytes: 50 * GB,
                  total_bytes: 500 * GB,
                  used_percent: 90,
                  inodes_total: 2000000,
                  inodes_used: 200000,
                  inodes_free: 1800000,
                  inodes_percent: 10,
                },
              ],
            }),
          );
        }
        return Promise.resolve(json({ error: "unexpected" }, 500));
      }),
    );

    act(() => root.render(<StoragePanel machines={machines} />));
    await flush();

    expect(calls).toEqual(["/api/v1/machines/devbox/storage"]);
    expect(host.querySelector('[data-mount="/"]')).not.toBeNull();
    expect(host.querySelector('[data-mount="/data00"]')).not.toBeNull();
    expect(host.querySelectorAll('[data-testid="inode-warning"]')).toHaveLength(1);
    expect(host.querySelector('[data-testid="storage-hotspots-idle"]')).not.toBeNull();
  });

  it("runs on-demand hotspot scan and enforces preview before confirmed selective cleanup", async () => {
    const cleanupCalls: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url === "/api/v1/machines/devbox/storage") {
          return Promise.resolve(
            json({
              machine_id: "devbox",
              at: 1000,
              os: "linux",
              supported: true,
              collected_ms: 1,
              mounts: [
                {
                  mount: "/",
                  device: "/dev/vda2",
                  fs_type: "ext4",
                  used_bytes: 50 * GB,
                  avail_bytes: 50 * GB,
                  total_bytes: 100 * GB,
                  used_percent: 50,
                  inodes_total: 1000000,
                  inodes_used: 100000,
                  inodes_free: 900000,
                  inodes_percent: 10,
                },
              ],
            }),
          );
        }
        if (url === "/api/v1/machines/devbox/storage/hotspots") {
          return Promise.resolve(
            json({
              machine_id: "devbox",
              at: 1001,
              os: "linux",
              partial: false,
              duration_ms: 42,
              categories: [
                {
                  id: "pip-cache",
                  path: "~/.cache/pip",
                  bytes: 500 * MB,
                  reclaimable_bytes: 500 * MB,
                  status: "ok",
                  cleanable: true,
                  cleanup_target_id: "pip-cache",
                },
                {
                  id: "persona-logs",
                  path: "~/.persona/logs",
                  bytes: 120 * MB,
                  reclaimable_bytes: 120 * MB,
                  status: "ok",
                  cleanable: true,
                  cleanup_target_id: "persona-logs",
                },
                {
                  id: "user-cache",
                  path: "~/.cache",
                  bytes: 2 * GB,
                  status: "ok",
                  cleanable: false,
                },
              ],
            }),
          );
        }
        if (url === "/api/v1/machines/devbox/storage/cleanup" && init?.method === "POST") {
          const body = JSON.parse(String(init.body)) as { targets: string[]; dry_run?: boolean; confirm?: boolean };
          cleanupCalls.push(body);
          if (body.dry_run) {
            return Promise.resolve(
              json({
                machine_id: "devbox",
                dry_run: true,
                estimated_bytes: 620 * MB,
                freed_bytes: 0,
                items: [
                  { target_id: "pip-cache", path: "~/.cache/pip", bytes: 500 * MB, status: "preview" },
                  { target_id: "persona-logs", path: "~/.persona/logs", bytes: 120 * MB, status: "preview" },
                ],
              }),
            );
          }
          return Promise.resolve(
            json({
              machine_id: "devbox",
              dry_run: false,
              estimated_bytes: 620 * MB,
              freed_bytes: 620 * MB,
              items: [
                { target_id: "pip-cache", path: "~/.cache/pip", bytes: 500 * MB, status: "cleaned" },
                { target_id: "persona-logs", path: "~/.persona/logs", bytes: 120 * MB, status: "cleaned" },
              ],
            }),
          );
        }
        return Promise.resolve(json({ error: "not found" }, 404));
      }),
    );

    act(() => root.render(<StoragePanel machines={machines} />));
    await flush();

    // Explicit hotspot scan trigger
    click(host.querySelector('[data-testid="storage-scan-hotspots"]'));
    await flush();

    expect(host.querySelector('[data-testid="storage-hotspots-list"]')).not.toBeNull();
    expect(host.querySelector('[data-target-checkbox="pip-cache"]')).not.toBeNull();
    expect(host.querySelector('[data-target-checkbox="persona-logs"]')).not.toBeNull();

    // Preview cleanup first (dry_run: true)
    click(host.querySelector('[data-testid="storage-preview-cleanup"]'));
    await flush();

    expect(cleanupCalls).toEqual([{ targets: ["pip-cache", "persona-logs"], dry_run: true }]);
    expect(document.body.querySelector('[data-testid="storage-preview-list"]')).not.toBeNull();

    // Confirm cleanup inside dialog (dry_run: false, confirm: true)
    click(document.body.querySelector('[data-testid="storage-confirm-cleanup"]'));
    await flush();

    expect(cleanupCalls).toEqual([
      { targets: ["pip-cache", "persona-logs"], dry_run: true },
      { targets: ["pip-cache", "persona-logs"], dry_run: false, confirm: true },
    ]);
    expect(host.querySelector('[data-testid="storage-cleanup-result"]')).not.toBeNull();
  });

  it("shows node_outdated notice when node does not implement machine.storage", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.resolve(json({ error: "old node", code: "node_outdated" }, 409))),
    );

    act(() => root.render(<StoragePanel machines={machines} />));
    await flush();

    expect(host.querySelector('[data-testid="storage-mounts-error"]')).not.toBeNull();
  });

  it("keeps en, zh-Hans, and ja machines-storage catalogs in sync", () => {
    const enKeys = Object.keys(enCatalog).sort();
    expect(Object.keys(zhCatalog).sort()).toEqual(enKeys);
    expect(Object.keys(jaCatalog).sort()).toEqual(enKeys);
  });
});
