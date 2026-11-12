// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {} })) as typeof window.matchMedia;
  Element.prototype.scrollIntoView ??= function () {};
});
vi.mock("../../ui/atoms", async (original) => ({ ...(await original<typeof import("../../ui/atoms")>()), toast: vi.fn() }));
import { resetExtensionRoot, setCsrf, setTransport } from "../api";
import { Extensions } from "./Extensions";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement, root: Root;
let puts: { path: string; body: unknown }[];
let sandbox: Record<string, unknown>;
const flush = async () => { for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); }); };
const ext = () => ({ id: "rsshub", name: "RSSHub", source: "installed", status: "running", enabled: true, permissions: [], sandbox });
const dialog = () => document.querySelector<HTMLElement>('[role="dialog"]');
const sandboxSwitch = () => host.querySelector<HTMLButtonElement>('[role="switch"][aria-label="RSSHub 在沙箱里运行"]')!;
const render = async (search = "?extension=rsshub") => {
  history.replaceState(null, "", "/admin/extensions" + search);
  await act(async () => root.render(<Extensions />));
  await flush();
};

beforeEach(() => {
  resetExtensionRoot();
  setCsrf("tok");
  puts = [];
  sandbox = { enabled: true, default: true, chosen: false, active: true, egress: { allowed: 12, denied: 3, last_denied: "rss.example.org:443" } };
  setTransport(async (path, init) => {
    const reply = (body: unknown) => new Response(JSON.stringify(body), { status: 200 });
    if (init?.method === "PUT") {
      const body = JSON.parse(String(init.body));
      puts.push({ path, body });
      sandbox = { ...sandbox, enabled: body.enabled ?? sandbox.default, chosen: body.enabled !== null };
      return reply(ext());
    }
    if (path === "/api/v1/admin/extensions") return reply({ can_install: false, extensions: [ext()] });
    if (path.includes("metrics")) return new Response(JSON.stringify({ error: "not found" }), { status: 404 });
    return reply({ extensions: [], items: [] });
  });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  document.body.innerHTML = "";
  history.replaceState(null, "", "/");
  setTransport((path, init) => fetch(path, init));
});

describe("网络与隔离 (#5696)", () => {
  it("the settings page's link opens the row at its network block, with the counts", async () => {
    await render();
    const block = host.querySelector('[data-extension-network="rsshub"]');
    expect(block).toBeTruthy();
    expect(block!.textContent).toContain("网络与隔离");
    expect(block!.textContent).toContain("默认开：不是从官方市场装的");
    expect(block!.textContent).toContain("放行 12");
    expect(block!.textContent).toContain("拦下 3");
  });

  it("asks before turning the sandbox off, and turning it on again goes back to the default", async () => {
    await render();
    await act(async () => sandboxSwitch().click());
    await flush();
    expect(dialog()?.textContent).toContain("关掉 RSSHub 的沙箱？");
    expect(puts).toHaveLength(0);
    const off = [...dialog()!.querySelectorAll("button")].find((b) => b.textContent?.trim() === "关掉沙箱")!;
    await act(async () => off.click());
    await flush();
    expect(puts).toEqual([{ path: "/api/v1/admin/extensions/rsshub/sandbox", body: { enabled: false } }]);
    expect(host.querySelector('[data-extension-network="rsshub"]')!.textContent).toContain("沙箱已关：RSSHub 能连任何地址");
    expect(host.querySelector('[data-extension-network="rsshub"]')!.textContent).toContain("你关掉了 · 默认开");

    await act(async () => sandboxSwitch().click());
    await flush();
    expect(dialog()).toBeNull();
    expect(puts[1]).toEqual({ path: "/api/v1/admin/extensions/rsshub/sandbox", body: { enabled: null } });
  });

  it("is closed without the link, and absent for an extension the hub does not run from a package", async () => {
    await render("");
    expect(host.querySelector('[data-extension-network="rsshub"]')).toBeNull();
  });
});
