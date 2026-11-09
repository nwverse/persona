// Language catalogs are resources, not hand-maintained TypeScript entry points. Vite bundles one client catalog
// and one admin catalog per language from the namespace JSON beside _meta.json; adding a directory needs no code.
//
// The client catalog comes in two parts: client-startup holds the namespaces the first screen names
// (src/i18n/startup-namespaces.json, which the build checks: vite.config.ts), client-rest the others, loaded after the
// first screen. `client` is the whole of it, for tests.
//
// The page loads only its own language's catalog (src/i18n/index.ts initI18n). t() still falls back to en, then
// zh-Hans, for a key the language lacks (docs/protocol/i18n.md), so each catalog module also exports `fallbacks`: the entries
// of those two languages that t() could still reach from this one, computed here at build time. With complete
// catalogs (src/i18n/catalog.test.ts) that is almost nothing, instead of two more full catalogs on the first load.
import { readdirSync, readFileSync } from "node:fs";
import { basename, dirname, join } from "node:path";
import type { Plugin } from "vite";
import { flatten } from "./src/i18n/locales/flatten";

type Kind = "client" | "client-startup" | "client-rest" | "admin";
const KINDS: readonly string[] = ["client", "client-startup", "client-rest", "admin"] satisfies Kind[];
type Catalog = Record<string, string>;

/** The languages t() falls back to, in order: FALLBACK_LANG and SOURCE_LANG of src/i18n/index.ts (a test checks). */
export const FALLBACK_LANGS = ["en", "zh-Hans"] as const;

const PLURAL = /_(zero|one|two|few|many|other)$/;

/** src/i18n/startup-namespaces.json, from a language's _meta.json: the client namespaces in client-startup. */
export function startupNamespacesFile(metaPath: string): string {
  return join(dirname(metaPath), "..", "..", "startup-namespaces.json");
}

/** The namespace JSON files of one catalog, sorted, as absolute paths. */
export function catalogFiles(metaPath: string, kind: Kind): string[] {
  const dir = dirname(metaPath);
  const startup = kind === "client-startup" || kind === "client-rest"
    ? new Set(JSON.parse(readFileSync(startupNamespacesFile(metaPath), "utf8")) as string[])
    : undefined;
  return readdirSync(dir).filter((name) =>
    name.endsWith(".json") && !name.startsWith("_") && (name.startsWith("admin-") === (kind === "admin")) &&
    (!startup || startup.has(name.slice(0, -".json".length)) === (kind === "client-startup")),
  ).sort().map((name) => join(dir, name));
}

function readCatalog(dir: string, kind: Kind): Catalog {
  return flatten(Object.fromEntries(catalogFiles(join(dir, "_meta.json"), kind).map((file) =>
    [`./${basename(file)}`, JSON.parse(readFileSync(file, "utf8")) as Catalog],
  )));
}

/**
 * The entries of `fallback` that t() can still read when the page's catalog is `own`: everything but the keys `own`
 * answers itself. A key `own` has answers every lookup of it; a plural form (`b_one`) is never read when `own` has the
 * base `b` or `b_other`, because a counted lookup of `b` then stops in `own`.
 */
export function fallbackEntries(own: Catalog, fallback: Catalog): Catalog {
  const out: Catalog = {};
  for (const [key, text] of Object.entries(fallback)) {
    if (Object.hasOwn(own, key)) continue;
    const base = key.replace(PLURAL, "");
    if (base !== key && (Object.hasOwn(own, base) || Object.hasOwn(own, `${base}_other`))) continue;
    out[key] = text;
  }
  return out;
}

/** The other languages whose files a catalog's `fallbacks` reads: FALLBACK_LANGS without the catalog's own. */
function fallbackLangs(dir: string): string[] {
  return FALLBACK_LANGS.filter((l) => l !== basename(dir));
}

/** The fallback languages' entries t() may need for the language in `dir`, keyed by language. A key and its plural
 *  forms share a namespace, so each part of the client catalog gets just its share of the whole's. */
export function catalogFallbacks(dir: string, kind: Kind): Record<string, Catalog> {
  const own = readCatalog(dir, kind);
  return Object.fromEntries(fallbackLangs(dir).map((l) => [l, fallbackEntries(own, readCatalog(join(dirname(dir), l), kind))]));
}

// The import form imports every namespace JSON, so the dev server treats each as its own module and an edit to one
// reloads only it. The inline form (the build and tests) reads and flattens them here into one module holding one
// JSON.parse of a string: in the bundle that is about 26 KB smaller gzipped per language than ~300 object literals
// (each key's namespace prefix repeated, and per-module wrapping), and engines parse JSON faster than the same object
// as code; under Vitest every test file loads the catalogs, and ~300 JSON modules per language through the module
// runner were most of the suite's setup time.
export function catalogModule(metaPath: string, kind: Kind, inline = false): string {
  const dir = dirname(metaPath);
  const fallbacks = `export const fallbacks = ${JSON.stringify(catalogFallbacks(dir, kind))};`;
  if (inline) {
    return [`export default JSON.parse(${JSON.stringify(JSON.stringify(readCatalog(dir, kind)))});`, fallbacks].join("\n");
  }
  const namespaces = catalogFiles(metaPath, kind).map((file) => basename(file));
  const imports = namespaces.map((name, i) => `import ns${i} from ${JSON.stringify(join(dir, name).replace(/\\/g, "/"))};`);
  const entries = namespaces.map((name, i) => `${JSON.stringify(`./${name}`)}: ns${i}`);
  return [
    ...imports,
    `import { flatten } from ${JSON.stringify(join(dir, "../flatten.ts").replace(/\\/g, "/"))};`,
    `export default flatten({${entries.join(",")}});`,
    fallbacks,
  ].join("\n");
}

/** Whether Vite is building (not serving): set by configResolved. */
let building = false;

export const i18nCatalogs: Plugin = {
  name: "i18n-resource-catalogs",
  enforce: "pre",
  configResolved(config) {
    building = config.command === "build";
  },
  resolveId(source, importer) {
    const [path, query] = source.split("?");
    if (!path.endsWith("/_meta.json")) return;
    const kind = new URLSearchParams(query).get("catalog");
    const absolute = path.startsWith(".") && importer ? join(dirname(importer.split("?")[0]), path) : path;
    if (kind && KINDS.includes(kind)) return `\0i18n-catalog:${kind}:${absolute}.js`;
  },
  load(id) {
    const match = /^\0i18n-catalog:([a-z-]+):(.+)\.js$/.exec(id);
    if (!match) return;
    const [, kind, path] = match;
    this.addWatchFile(path);
    if (kind.startsWith("client-")) this.addWatchFile(startupNamespacesFile(path));
    const inline = building || Boolean(process.env.VITEST);
    // Inlined JSON is no longer an import, so watch each file for Vitest's (and vite build --watch's) watch mode to
    // rerun on an edit.
    if (inline) for (const file of catalogFiles(path, kind as Kind)) this.addWatchFile(file);
    // `fallbacks` reads the fallback languages' files: editing one rebuilds this module too. (Vite treats a watched
    // path as an import of the module, so only files: a directory would fail import analysis.)
    const dir = dirname(path);
    for (const l of fallbackLangs(dir)) for (const file of catalogFiles(join(dirname(dir), l, "_meta.json"), kind as Kind)) this.addWatchFile(file);
    return catalogModule(path, kind as Kind, inline);
  },
};
