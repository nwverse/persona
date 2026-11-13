// The legacy-preference copy (public/compatibility-bootstrap.js) goes into both HTML entry points' <head> as an inline
// script when they are built, instead of the classic <script src> the source pages load it with. As its own file it
// was a parser-blocking request on every page load: the service worker passes it through (lib/sw-routes.ts) and the
// hub has root files checked again on every load (no-cache, a 304 at best). The page waited a round trip to the hub
// before its first paint, also when the worker had answered with the kept shell because the hub was slow or
// unreachable. The file stays the one source of the code (scripts/compatibility-identifiers.json lists it as the
// adapter) and is still copied for shells kept from before.
import { readFileSync } from "node:fs";
import type { Plugin } from "vite";

export const BOOTSTRAP_TAG = '<script src="compatibility-bootstrap.js"></script>';

/** html with the bootstrap tag replaced by the script itself; an error when the page has no such tag. */
export function inlineBootstrap(html: string, code: string, page = "page"): string {
  const at = html.indexOf(BOOTSTRAP_TAG);
  if (at < 0) throw new Error(`compat-bootstrap: ${page} has no ${BOOTSTRAP_TAG}`);
  if (code.includes("</script")) throw new Error("compat-bootstrap: the script must not contain </script");
  return html.slice(0, at) + `<script>${code.trim()}</script>` + html.slice(at + BOOTSTRAP_TAG.length);
}

export const compatBootstrap = (file: string): Plugin => ({
  name: "compat-bootstrap",
  apply: "build",
  transformIndexHtml: {
    order: "pre",
    handler(html, ctx) {
      return inlineBootstrap(html, readFileSync(file, "utf8"), ctx.filename);
    },
  },
});
