import { join } from "node:path";
import { pathToFileURL } from "node:url";

export const SETUP_SCHEME = "persona-setup";
export const SETUP_URL = `${SETUP_SCHEME}://app/setup.html`;

const resources = new Map([
  ["/setup.html", "setup.html"],
  ["/setup.css", "setup.css"],
  ["/setup.cjs", "setup.cjs"],
]);

/** Only these public resources are served. No renderer-provided path reaches the filesystem or any other asar file. */
export function setupResourceURL(raw: string, method = "GET"): string | undefined {
  if (method !== "GET" && method !== "HEAD") return undefined;
  let url: URL;
  try { url = new URL(raw); } catch { return undefined; }
  const name = resources.get(url.pathname);
  // WHATWG serializes a custom scheme's origin as "null"; Chromium still uses the registered scheme and host as its
  // site. Require both explicitly, rather than accepting a prefix or a serialized opaque origin.
  if (url.protocol !== `${SETUP_SCHEME}:` || url.hostname !== "app" || !name
    || url.username || url.password || url.port || url.search || url.hash
    || /[\\%]/.test(raw)) return undefined;
  return pathToFileURL(join(import.meta.dirname, "setup", name)).href;
}
