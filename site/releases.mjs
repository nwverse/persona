// /releases/data.json: the small metadata behind the Releases page. Packages are never proxied.
//
// Only published v* version tags in nwverse/persona are product releases; media releases such as promo-v1 are not.
// The latest stable release is the highest semantic version, not whichever tag was created most recently. A cached
// copy is fresh for ten minutes and survives a GitHub outage for up to 30 days, marked stale by the page.
// Every download URL is checked against the repository's own GitHub releases/download/ prefix. There is deliberately
// no download route here: the browser follows the GitHub URL, and GitHub serves the bytes, not our entry or Worker.
export const RELEASE_REPO = "nwverse/persona";
export const RELEASES_URL = `https://api.github.com/repos/${RELEASE_REPO}/releases?per_page=100`;
export const RELEASES_PAGE = `https://github.com/${RELEASE_REPO}/releases`;
const DOWNLOAD_ROOT = `${RELEASES_PAGE}/download/`;
const CACHE_KEY = "https://persona-releases.invalid/v1";
const FRESH_MS = 10 * 60 * 1000;
const KEEP_SECONDS = 30 * 24 * 3600;
const FETCH_TIMEOUT_MS = 8000;
const TAG = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/;
const ASSET_NAME = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;

const string = (value, max = 200) => typeof value === "string" ? value.trim().slice(0, max) : "";

// A short readable excerpt, not another copy of GitHub's complete generated release notes. Pick a few feature / fix
// titles, omit contributor handles and generated links, and render only as text. Full notes stay one click away.
export function releaseExcerpt(body) {
  if (typeof body !== "string") return "";
  const features = [], fixes = [];
  for (const line of body.replace(/\r/g, "").split("\n")) {
    const match = /^[-*+]\s+(feat|fix|perf|refactor)(?:\([^)]+\))?!?:\s+(.+)$/i.exec(line.trim());
    if (!match) continue;
    const title = match[2].replace(/\s+by\s+@\S+.*$/i, "").replace(/https?:\/\/\S+/g, "").replace(/\s*\(#\d+\)/g, "").replace(/\s+#\d+\b/g, "").trim();
    if (!title) continue;
    const bucket = /^feat$/i.test(match[1]) ? features : fixes;
    if (bucket.length < (bucket === features ? 3 : 2)) bucket.push(title);
  }
  const lines = [...features.map((title) => `新功能 · ${title}`), ...fixes.map((title) => `修复 · ${title}`)];
  const text = lines.join("\n");
  return text.length > 480 ? text.slice(0, 477).trimEnd() + "…" : text;
}

function parseTag(tag) {
  const match = TAG.exec(tag);
  return match ? { core: match.slice(1, 4).map(Number), pre: match[4]?.split(".") || [] } : null;
}

// Semver 2.0 precedence: numeric identifiers compare numerically and precede alphanumeric prerelease identifiers.
export function compareVersions(a, b) {
  const left = parseTag(a), right = parseTag(b);
  if (!left || !right) return 0;
  for (let i = 0; i < 3; i++) if (left.core[i] !== right.core[i]) return left.core[i] - right.core[i];
  if (!left.pre.length || !right.pre.length) return right.pre.length - left.pre.length;
  for (let i = 0; i < Math.max(left.pre.length, right.pre.length); i++) {
    const x = left.pre[i], y = right.pre[i];
    if (x === undefined || y === undefined) return left.pre.length - right.pre.length;
    if (x === y) continue;
    const nx = /^\d+$/.test(x), ny = /^\d+$/.test(y);
    if (nx && ny) return Number(x) - Number(y);
    if (nx !== ny) return nx ? -1 : 1;
    return x < y ? -1 : 1;
  }
  return 0;
}

function asset(raw, tag) {
  const name = string(raw?.name, 160);
  const url = string(raw?.browser_download_url, 1000);
  const expected = `${DOWNLOAD_ROOT}${encodeURIComponent(tag)}/`;
  if (!name || !ASSET_NAME.test(name) || !url.startsWith(expected) || url.includes("\n")) return null;
  try {
    // Never pass a GitHub API-supplied URL on without checking its canonical host, path and credentials.
    const parsed = new URL(url);
    if (parsed.origin !== "https://github.com" || parsed.username || parsed.password || parsed.search || parsed.hash) return null;
    if (parsed.pathname !== `/nwverse/persona/releases/download/${tag}/${name}`) return null;
  } catch {
    return null;
  }
  return {
    name,
    url,
    size: Number.isSafeInteger(raw.size) && raw.size >= 0 ? raw.size : null,
    kind: name === "SHA256SUMS" ? "checksums" :
      name.endsWith(".apk") ? "android" :
      /^Persona-.*-(?:darwin|linux|windows)-(?:amd64|arm64)\.(?:tar\.gz|zip)$/.test(name) ? "desktop" : "other",
  };
}

export function slimReleases(raw) {
  if (!Array.isArray(raw)) throw new Error("release list is not an array");
  const releases = [];
  const tags = new Set();
  for (const item of raw) {
    if (!item || item.draft !== false) continue;
    const tag = string(item.tag_name, 100);
    if (!parseTag(tag) || tags.has(tag) || item.prerelease !== parseTag(tag).pre.length > 0) continue;
    const published = string(item.published_at, 40);
    if (!published || !Number.isFinite(Date.parse(published))) continue;
    const assets = (Array.isArray(item.assets) ? item.assets : []).map((a) => asset(a, tag)).filter(Boolean);
    // A release without packages is still useful as a version-history entry and has its GitHub notes link.
    releases.push({
      tag,
      name: string(item.name, 120) || tag,
      url: `${RELEASES_PAGE}/tag/${encodeURIComponent(tag)}`,
      published_at: published,
      prerelease: item.prerelease,
      summary: releaseExcerpt(item.body),
      assets,
    });
    tags.add(tag);
  }
  releases.sort((a, b) => Date.parse(b.published_at) - Date.parse(a.published_at) || compareVersions(b.tag, a.tag));
  const stable = releases.filter((r) => !r.prerelease).sort((a, b) => compareVersions(b.tag, a.tag));
  return { latest: stable[0]?.tag || null, releases };
}

function edgeCache() {
  return typeof caches !== "undefined" && caches.default ? caches.default : null;
}

async function cached(cache) {
  if (!cache) return null;
  try {
    const response = await cache.match(CACHE_KEY);
    return response ? await response.json() : null;
  } catch {
    return null;
  }
}

async function fetchFresh(env) {
  const response = await fetch(RELEASES_URL, {
    headers: { accept: "application/vnd.github+json", "user-agent": "persona-site", "x-github-api-version": "2022-11-28" },
    signal: AbortSignal.timeout(FETCH_TIMEOUT_MS),
  });
  if (!response.ok) throw new Error(`releases: HTTP ${response.status}`);
  // A malformed payload is not cached as though it were an empty release history.
  const data = { ...slimReleases(await response.json()), fetched_at: new Date().toISOString() };
  if (!data.latest) throw new Error("no published stable releases");
  return data;
}

export async function releasesData(env, ctx) {
  const cache = edgeCache();
  const old = await cached(cache);
  if (old && Date.now() - Date.parse(old.fetched_at) < FRESH_MS) return old;
  try {
    const data = await fetchFresh(env);
    if (cache) {
      const put = cache.put(CACHE_KEY, new Response(JSON.stringify(data), {
        headers: { "content-type": "application/json", "cache-control": `public, max-age=${KEEP_SECONDS}` },
      }));
      ctx?.waitUntil ? ctx.waitUntil(put) : await put;
    }
    return data;
  } catch (error) {
    if (old) return { ...old, stale: true };
    console.error("release list:", error?.message || error);
    return null;
  }
}

export async function serveReleases(request, env, ctx) {
  if (request.method !== "GET" && request.method !== "HEAD") {
    return new Response("method not allowed", { status: 405, headers: { allow: "GET, HEAD" } });
  }
  const data = await releasesData(env, ctx);
  if (!data) return Response.json({ error: "release list unavailable", releases_url: RELEASES_PAGE }, { status: 503, headers: { "cache-control": "no-store" } });
  const response = Response.json(data, { headers: { "cache-control": data.stale ? "public, max-age=60" : "public, max-age=300" } });
  return request.method === "HEAD" ? new Response(null, { headers: response.headers }) : response;
}
