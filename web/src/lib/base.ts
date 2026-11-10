// Where the hub's page is under this origin: "" at the root, "/<prefix>" when a reverse proxy serves the hub under a
// sub-path. The hub writes it into index.html's <base> from the proxy's X-Forwarded-Prefix (docs/web/web.md "Subpaths"), so
// the bundle's relative assets load from there; everything the page builds from a root path (the API, the WebSocket,
// images and downloads, its own routes) puts BASE in front.
export function baseOf(href: string | null | undefined): string {
  if (!href) return "";
  let p: string;
  try {
    p = new URL(href, "http://x").pathname;
  } catch {
    return "";
  }
  return p.replace(/\/+$/, "");
}

export const BASE = typeof document === "undefined" ? "" : baseOf(document.querySelector("base")?.getAttribute("href"));

/** A path of the app ("/s/…") without the sub-path it is served under; a path outside it stays as it is. */
export function stripBase(pathname: string, base = BASE): string {
  if (base && (pathname === base || pathname.startsWith(base + "/"))) return pathname.slice(base.length) || "/";
  return pathname;
}
