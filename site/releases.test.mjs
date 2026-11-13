// node --test site/worker.test.mjs site/releases.test.mjs
import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import worker from "./worker.mjs";
import { RELEASES_URL, slimReleases, compareVersions, releaseExcerpt } from "./releases.mjs";
import { directDownload, fileSize, latestRelease, partitionAssets } from "./public/releases/releases.js";

const assets = { fetch: async (request) => new Response("asset " + new URL(request.url).pathname) };
const page = (path = "/releases/data.json", method = "GET") => worker.fetch(new Request("https://site.example" + path, { method }), { ASSETS: assets }, { waitUntil() {} });
const url = (tag, name) => `https://github.com/nwverse/persona/releases/download/${tag}/${name}`;
function release(tag, extra = {}) {
  return {
    tag_name: tag, name: tag, draft: false, prerelease: tag.includes("-"), published_at: "2026-11-12T05:06:33Z",
    html_url: `https://github.com/nwverse/persona/releases/tag/${tag}`,
    assets: [
      { name: `Persona-${tag.slice(1)}-darwin-arm64.tar.gz`, size: 117300513, browser_download_url: url(tag, `Persona-${tag.slice(1)}-darwin-arm64.tar.gz`) },
      { name: `Persona-${tag.slice(1)}-android.apk`, size: 21111552, browser_download_url: url(tag, `Persona-${tag.slice(1)}-android.apk`) },
      { name: "SHA256SUMS", size: 1882, browser_download_url: url(tag, "SHA256SUMS") },
      { name: "persona-hub-linux-amd64.gz", size: 28361964, browser_download_url: url(tag, "persona-hub-linux-amd64.gz") },
    ],
    body: "A huge release body is deliberately not copied to the website.", ...extra,
  };
}
function edge() {
  const store = new Map();
  globalThis.caches = { default: {
    async match(key) { const r = store.get(String(key)); return r ? r.clone() : undefined; },
    async put(key, r) { store.set(String(key), r.clone()); },
  } };
  return store;
}
function upstream(handler) {
  const calls = [];
  globalThis.fetch = async (input, init) => { calls.push({ url: String(input), init }); return handler(String(input), init); };
  return calls;
}

test("a short release summary is inert text without contributors or generated links", () => {
  const summary = releaseExcerpt([
    "## What's Changed",
    "* feat(tasks): reuse a workspace by @someone in https://github.com/nwverse/persona/pull/1",
    "* fix(ui): keep downloads readable (#123)",
    "* feat(api): an addition (#99)",
    "* feat(web): another addition",
    "* feat(ios): not part of the short excerpt",
    "**Full Changelog**: https://github.com/nwverse/persona/compare/v1...v2",
  ].join("\n"));
  assert.equal(summary, "新功能 · reuse a workspace\n新功能 · an addition\n新功能 · another addition\n修复 · keep downloads readable");
  assert.ok(!summary.includes("@") && !summary.includes("http") && !summary.includes("Full Changelog"));
  assert.equal(releaseExcerpt("<script>alert(1)</script>"), "");
  assert.equal(releaseExcerpt("## What's Changed\n**Full Changelog**: https://example.com"), "");
});

test("only published semver releases appear; the newest stable semver is latest", () => {
  const data = slimReleases([
    release("v0.3.0"),
    release("v0.10.0", { published_at: "2026-11-01T00:00:00Z" }),
    release("v0.4.0-rc.1", { published_at: "2026-11-13T00:00:00Z" }),
    release("promo-v1", { prerelease: false, published_at: "2026-11-14T00:00:00Z" }),
    release("v9.0.0", { draft: true }),
    release("v0.2.0", { prerelease: false, published_at: null }),
    release("v0.1.0", { prerelease: true }),
    release("v0.2.1", { published_at: "2026-11-11T00:00:00Z" }),
  ]);
  assert.equal(data.latest, "v0.10.0", "0.10 is newer than 0.3, even when its release was created earlier");
  assert.deepEqual(data.releases.map((r) => r.tag), ["v0.4.0-rc.1", "v0.3.0", "v0.2.1", "v0.10.0"]);
  assert.equal(data.releases[0].prerelease, true);
  assert.equal(data.releases[1].body, undefined, "the huge generated GitHub release body is not sent to browsers");
  assert.equal(data.releases[1].assets[0].url, url("v0.3.0", "Persona-0.3.0-darwin-arm64.tar.gz"));
  assert.ok(compareVersions("v1.0.0", "v1.0.0-rc.1") > 0);
  assert.ok(compareVersions("v1.0.0-rc.10", "v1.0.0-rc.2") > 0);
});

test("a download is never a proxy, external host, malformed path or arbitrary scheme", () => {
  const good = release("v0.3.0");
  const bad = (name, browser_download_url) => release("v0.3.0", { assets: [{ name, size: 1, browser_download_url }] });
  for (const item of [
    bad("Persona-0.3.0-linux-amd64.tar.gz", "https://site.example/dl/personad.gz"),
    bad("evil.gz", "https://example.com/nwverse/persona/releases/download/v0.3.0/evil.gz"),
    bad("evil.gz", "https://github.com.evil.example/nwverse/persona/releases/download/v0.3.0/evil.gz"),
    bad("evil.gz", "https://github.com/nwverse/persona/releases/download/v0.3.0/evil.gz?host=proxy"),
    bad("evil.gz", "https://github.com/nwverse/persona/releases/download/v0.3.0/other.gz"),
    bad("../evil.gz", url("v0.3.0", "../evil.gz")),
    bad("evil.gz", "javascript:alert(1)"),
  ]) {
    const data = slimReleases([item]);
    assert.deepEqual(data.releases[0].assets, [], JSON.stringify(item.assets));
  }
  assert.equal(slimReleases([good]).releases[0].assets.length, 4);
  for (const a of slimReleases([good]).releases[0].assets) {
    assert.ok(a.url.startsWith("https://github.com/nwverse/persona/releases/download/"));
    assert.equal(a.url.startsWith("https://site.example"), false);
  }
});

test("the page labels common packages and folds server / CLI artifacts", () => {
  const data = slimReleases([release("v0.3.0")]);
  const groups = partitionAssets(latestRelease(data));
  assert.deepEqual(groups.android.map((a) => a.name), ["Persona-0.3.0-android.apk"]);
  assert.deepEqual(groups.desktop.map((a) => a.name), ["Persona-0.3.0-darwin-arm64.tar.gz"]);
  assert.deepEqual(groups.other.map((a) => a.name), ["persona-hub-linux-amd64.gz"]);
  assert.equal(groups.checksums[0].name, "SHA256SUMS");
  assert.equal(fileSize(117300513), "112 MB");
  assert.equal(directDownload({ url: "/dl/a.gz" }), "");
  assert.equal(directDownload({ url: "https://example.com/a.gz" }), "");
});

test("release metadata is fetched once while fresh, and a stale copy outlives GitHub", async () => {
  const store = edge();
  let fail = false;
  const calls = upstream(() => fail ? new Response("down", { status: 502 }) : Response.json([release("v0.3.0")]));
  let response = await page();
  assert.equal(response.status, 200);
  const first = await response.json();
  assert.equal(first.latest, "v0.3.0");
  assert.equal(first.stale, undefined);
  assert.equal(calls[0].url, RELEASES_URL);
  assert.equal(calls[0].init.headers.accept, "application/vnd.github+json");
  assert.equal(calls[0].init.headers.authorization, undefined, "public metadata needs no token");
  await page();
  assert.equal(calls.length, 1);
  const [key, cached] = [...store.entries()][0];
  const old = await cached.json();
  store.set(key, Response.json({ ...old, fetched_at: new Date(Date.now() - 3600_000).toISOString() }));
  fail = true;
  response = await page();
  const stale = await response.json();
  assert.equal(stale.stale, true);
  assert.equal(stale.latest, "v0.3.0");
  assert.equal(calls.length, 2);
});

test("a newly published release replaces the cached metadata after expiry", async () => {
  const store = edge();
  let current = [release("v0.3.0")];
  upstream(() => Response.json(current));
  assert.equal((await (await page()).json()).latest, "v0.3.0");
  current = [release("v0.3.1", { published_at: "2026-11-13T05:06:33Z" }), ...current];
  assert.equal((await (await page()).json()).latest, "v0.3.0", "fresh cache does not fetch again");
  const [key, cached] = [...store.entries()][0];
  const old = await cached.json();
  store.set(key, Response.json({ ...old, fetched_at: new Date(Date.now() - 3600_000).toISOString() }));
  const updated = await (await page()).json();
  assert.equal(updated.latest, "v0.3.1");
  assert.equal(updated.releases[1].tag, "v0.3.0");
});

test("without a cached list GitHub errors are recoverable and writes are refused", async () => {
  edge();
  upstream(() => { throw new Error("unreachable"); });
  const response = await page();
  assert.equal(response.status, 503);
  assert.equal(response.headers.get("cache-control"), "no-store");
  assert.equal((await response.json()).releases_url, "https://github.com/nwverse/persona/releases");
  upstream(() => Response.json([release("promo-v1", { prerelease: false })]));
  assert.equal((await page()).status, 503, "a media-only list is not a product release history");
  assert.equal((await page("/releases/data.json", "POST")).status, 405);
  const head = await page("/releases/data.json", "HEAD");
  assert.equal(head.status, 503);
});

test("the page itself is static and no release package route exists", async () => {
  for (const path of ["/releases/", "/releases", "/releases/download/v0.3.0/personad.gz", "/releases/data.json.bak"]) {
    const response = await worker.fetch(new Request("https://site.example" + path), { ASSETS: assets }, {});
    assert.equal(await response.text(), "asset " + path, path);
  }
  const source = await readFile(new URL("./public/releases/index.html", import.meta.url), "utf8");
  assert.ok(source.includes('fetch("/releases/data.json"'));
  assert.ok(source.includes("下载，由 GitHub 直接提供"));
  assert.ok(!source.includes('innerHTML = asset'), "upstream package metadata is not injected as HTML");
  assert.ok(!source.includes('innerHTML = release'), "upstream release metadata is not injected as HTML");
  for (const file of ["index.html", "extensions/index.html"]) {
    const html = await readFile(new URL("./public/" + file, import.meta.url), "utf8");
    assert.ok(html.includes('href="/releases/"'), file + " links to the release page");
  }
  const wrangler = await readFile(new URL("./wrangler.jsonc", import.meta.url), "utf8");
  assert.ok(wrangler.includes('"/releases/data.json"'));
  assert.ok(!wrangler.includes('"/releases/download'), "only metadata runs through the Worker");
});
