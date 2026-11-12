// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { setToken } from "../lib/api";
import { acquireExtensionLogo, extensionLogoSource } from "../lib/extension-logo";
import { ExtensionLogo } from "./ExtensionLogo";
import { pinManager } from "../lib/manager-choice";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const path = "/api/v1/extensions/notes/-/icon?v=1.0.0";
const apiPath = path.slice("/api/v1".length);
const catalog = `/api/v1/extensions/-/catalog/official/notes/icon?sha256=${"a".repeat(64)}`;
let host: HTMLDivElement, root: Root;
let created: ReturnType<typeof vi.fn>, revoked: ReturnType<typeof vi.fn>;
const flush = async () => { for (let i = 0; i < 4; i++) await act(async () => { await new Promise(r => setTimeout(r, 0)); }); };
beforeEach(() => {
  setToken(`synthetic-${Math.random()}`);
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  created = vi.fn(() => "blob:synthetic-icon"); revoked = vi.fn();
  vi.stubGlobal("fetch", vi.fn(async () => new Response("icon-bytes", { headers: { "Content-Type": "image/png" } })));
  Object.defineProperty(URL, "createObjectURL", { configurable: true, value: created });
  Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: revoked });
});
afterEach(() => { act(() => root.unmount()); host.remove(); vi.unstubAllGlobals(); });

it("authenticates only exact raw hub icon routes, including HTTP and proxy prefixes", () => {
  for (const value of [path, catalog]) expect(extensionLogoSource(value)).toEqual({ path: value.slice(7) });
  expect(extensionLogoSource(`http://hub.example/prefix${path}`, "http://hub.example", "/prefix")).toEqual({ path: apiPath });
  expect(extensionLogoSource(`https://hub.example${path}`, "https://hub.example")).toEqual({ path: apiPath });
  expect(extensionLogoSource(`https://other.example${path}`, "https://hub.example")).toHaveProperty("publicURL");
  expect(extensionLogoSource("https://images.example/logo.png")).toEqual({ publicURL: "https://images.example/logo.png" });
  for (const value of [undefined, "", "//other.example" + path, "/logo.png", path + "&token=anything", path + "#fragment",
    path.replace("notes", ".."), path.replace("notes", "%2e%2e"), path.replace("notes", "notes/../notes"),
    path.replace("notes", "notes%2fextra"), path.replace("/-/", "/"), path.replace("/notes/", "/notes\\/"),
    "data:image/png;base64,AAA", "http://other.example/logo.png", "https://user:pass@hub.example" + path]) {
    expect(extensionLogoSource(value, "https://hub.example")).toBeNull();
  }
  expect(extensionLogoSource(`https://hub.example/x/..${path}`, "https://hub.example")?.path).toBeUndefined();
});

it("mounts 102 catalog tiles without fetching unseen packages and requests only intersecting logos", async () => {
  const observers: { callback: IntersectionObserverCallback; target?: Element; disconnected: boolean }[] = [];
  vi.stubGlobal("IntersectionObserver", class {
    private entry: typeof observers[number];
    constructor(callback: IntersectionObserverCallback, options?: IntersectionObserverInit) {
      expect(options?.rootMargin ?? "0px").toBe("0px");
      this.entry = { callback, disconnected: false }; observers.push(this.entry);
    }
    observe(target: Element) { this.entry.target = target; }
    disconnect() { this.entry.disconnected = true; }
  });
  const render = (version: string) => <>{Array.from({ length: 102 }, (_, i) =>
    <ExtensionLogo key={i} url={`/api/v1/extensions/notes_${i}/-/icon?v=${version}`} className="size-8" iconSize={16} />)}</>;
  act(() => root.render(render("1"))); await flush();
  expect(observers).toHaveLength(102); expect(fetch).not.toHaveBeenCalled();
  expect(host.querySelectorAll("[data-extension-logo] svg")).toHaveLength(102);
  for (const o of observers) o.callback([{ target: o.target, isIntersecting: false } as IntersectionObserverEntry], {} as IntersectionObserver);
  await flush(); expect(fetch).not.toHaveBeenCalled();
  for (const o of observers.slice(0, 2)) o.callback([{ target: o.target, isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver);
  await flush(); expect(fetch).toHaveBeenCalledTimes(2); expect(host.querySelectorAll("img")).toHaveLength(2);
  const retired = observers[50];
  act(() => root.render(render("2"))); await flush();
  expect(observers.slice(0, 102).every(o => o.disconnected)).toBe(true);
  expect(revoked).toHaveBeenCalledTimes(2); expect(fetch).toHaveBeenCalledTimes(2);
  retired.callback([{ target: retired.target, isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver);
  await flush(); expect(fetch).toHaveBeenCalledTimes(2);
  act(() => root.render(null));
  expect(observers.every(o => o.disconnected)).toBe(true);
});

it("downloads bytes through the existing auth API, never embeds the token, coalesces and revokes on removal", async () => {
  setToken("synthetic-private-token");
  act(() => root.render(<><ExtensionLogo url={path} className="size-8" iconSize={16} /><ExtensionLogo url={path} className="size-8" iconSize={16} /></>));
  await flush();
  expect(fetch).toHaveBeenCalledTimes(1);
  const [url, options] = vi.mocked(fetch).mock.calls[0];
  expect(url).toBe(path);
  expect(new Headers(options?.headers).get("Authorization")).toBe("Bearer synthetic-private-token");
  expect(new Headers(options?.headers).get("X-Persona-Client")).toBe("web/1");
  expect(options?.credentials).toBe("same-origin"); expect(options?.redirect).toBe("manual");
  expect(host.innerHTML).not.toContain("synthetic-private-token");
  expect(host.querySelectorAll('img[src="blob:synthetic-icon"]')).toHaveLength(2);
  act(() => root.render(null));
  expect(revoked).toHaveBeenCalledTimes(2);
  act(() => root.render(<ExtensionLogo url={path} className="size-8" iconSize={16} />)); await flush();
  expect(fetch).toHaveBeenCalledTimes(1);
});

it("keeps list and detail artwork scoped to their explicit manager, including the default beside another manager's detail", async () => {
  const unpin = pinManager("/extensions/notes", "detail-owner");
  try {
    act(() => root.render(<ExtensionLogo url={path} manager="list-owner" className="size-8" iconSize={16} />)); await flush();
    expect(new Headers(vi.mocked(fetch).mock.calls[0][1]?.headers).get("X-Persona-Manager")).toBe("list-owner");
    act(() => root.render(<ExtensionLogo url={path} manager="" className="size-8" iconSize={16} />)); await flush();
    expect(new Headers(vi.mocked(fetch).mock.calls[1][1]?.headers).get("X-Persona-Manager")).toBe("");
    expect(revoked).toHaveBeenCalledTimes(1);
    act(() => root.render(<ExtensionLogo url={path} manager="list-owner" className="size-8" iconSize={16} />)); await flush();
    expect(fetch).toHaveBeenCalledTimes(2);
  } finally { unpin(); }
});

it("leaves external HTTPS images anonymous without asking the hub", async () => {
  act(() => root.render(<ExtensionLogo url="https://images.example/logo.png" className="size-8" iconSize={16} />)); await flush();
  expect(fetch).not.toHaveBeenCalled();
  const image = host.querySelector("img")!;
  expect(image.crossOrigin).toBe("anonymous"); expect(image.getAttribute("referrerpolicy")).toBe("no-referrer");
});

it("retains fallback on HTTP/decode errors, retries URL changes and revokes the previous object URL", async () => {
  vi.mocked(fetch).mockResolvedValueOnce(new Response("missing", { status: 404, headers: { "Content-Type": "application/json" } }));
  act(() => root.render(<ExtensionLogo url={path} className="size-8" iconSize={16} />)); await flush();
  expect(host.querySelector("img")).toBeNull(); expect(host.querySelector("svg")).not.toBeNull();
  act(() => root.render(<ExtensionLogo url={path + "1"} className="size-8" iconSize={16} />)); await flush();
  act(() => host.querySelector("img")!.dispatchEvent(new Event("error")));
  expect(host.querySelector("img")).toBeNull();
  act(() => root.render(<ExtensionLogo url={path + "2"} className="size-8" iconSize={16} />)); await flush();
  expect(host.querySelector("img")).not.toBeNull(); expect(revoked).toHaveBeenCalledTimes(1);
  act(() => root.render(<ExtensionLogo className="size-8" iconSize={16} />));
  expect(revoked).toHaveBeenCalledTimes(2); expect(host.querySelector("svg")).not.toBeNull();
});

it("cancels only after the final consumer leaves and fences stale results on URL and credential replacement", async () => {
  let resolve!: (response: Response) => void;
  vi.mocked(fetch).mockImplementationOnce(() => new Promise(r => { resolve = r; }));
  const a = acquireExtensionLogo(apiPath), b = acquireExtensionLogo(apiPath);
  const signal = vi.mocked(fetch).mock.calls[0][1]!.signal!;
  a.release(); expect(signal.aborted).toBe(false);
  b.release(); expect(signal.aborted).toBe(true);
  resolve(new Response("old", { headers: { "Content-Type": "image/png" } }));
  await expect(a.promise).rejects.toBeDefined();
  await expect(b.promise).rejects.toBeDefined();
  act(() => root.render(<ExtensionLogo url={path} className="size-8" iconSize={16} />)); await flush();
  act(() => setToken("synthetic-replacement")); await flush();
  expect(revoked).toHaveBeenCalledTimes(1); expect(created).toHaveBeenCalledTimes(2);
  expect(fetch).toHaveBeenCalledTimes(3);
});

it("bounds cached bytes and never caches invalid media or failures", async () => {
  vi.mocked(fetch).mockResolvedValueOnce(new Response("not a bundled PNG", { headers: { "Content-Type": "image/jpeg" } }));
  const bad = acquireExtensionLogo(apiPath); await expect(bad.promise).rejects.toThrow("Invalid extension icon"); bad.release();
  vi.mocked(fetch).mockImplementation(async () => new Response(new Uint8Array(2 * 1024 * 1024), { headers: { "Content-Type": "image/png" } }));
  for (let i = 0; i < 5; i++) { const entry = acquireExtensionLogo(`${apiPath}${i}`); await entry.promise; entry.release(); }
  const first = acquireExtensionLogo(`${apiPath}0`); await first.promise; first.release();
  expect(fetch).toHaveBeenCalledTimes(7);
});
