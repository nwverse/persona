import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import { createCacheIdentity, createRunPaths, prepareRunPaths, removeOwnedRunPaths, updaterCacheName } from "./update-test-cache.mjs";

const marker = async path => {
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, `sentinel: ${path}\n`, { mode: 0o600 });
  return readFile(path, "utf8");
};

for (const platform of ["linux", "darwin", "win32"]) {
  test(`${platform}: cleanup owns one run's caches and cannot delete foreign HOME content`, async t => {
    const work = await mkdtemp(join(tmpdir(), "persona-update-test-cache-"));
    t.after(async () => { const { rm } = await import("node:fs/promises"); await rm(work, { recursive: true, force: true }); });
    const identity = createCacheIdentity();
    assert.notEqual(identity, createCacheIdentity());
    assert.match(updaterCacheName(identity), /^persona-desktop-update-test-[a-z0-9-]+-updater$/);
    const paths = createRunPaths(work, identity, platform);
    await prepareRunPaths(paths);
    const home = paths.home;
    const own = [paths.cache, paths.shipIt, paths.defaultProfile];
    const protectedMarkers = [
      // The unsigned development identity is never a valid cleanup target.
      join(home, platform === "win32" ? "AppData/Local" : platform === "darwin" ? "Library/Caches" : ".cache", "com.example.persona.desktop.ShipIt", "sentinel.txt"),
      // A concurrent run has a different random cache identity under the same cache root.
      join(paths.cache, "..", `${updaterCacheName(createCacheIdentity())}`, "sentinel.txt"),
      join(home, platform === "darwin" ? "Documents" : "documents", "sentinel.txt"),
    ];
    if (platform === "linux") protectedMarkers.push(join(home, ".config", "unrelated-application", "sentinel.txt"));
    const ownMarkers = [];
    for (const directory of own) ownMarkers.push(await marker(join(directory, "sentinel.txt")));
    const expected = await Promise.all(protectedMarkers.map(marker));

    assert.deepEqual(await removeOwnedRunPaths(paths), []);
    for (const directory of own) await assert.rejects(readFile(join(directory, "sentinel.txt"), "utf8"), { code: "ENOENT" });
    assert.deepEqual(await Promise.all(protectedMarkers.map(path => readFile(path, "utf8"))), expected);
    assert.ok(ownMarkers.every(Boolean));
  });
}

test("cleanup refuses a foreign path and reports a failed removal separately", async t => {
  const work = await mkdtemp(join(tmpdir(), "persona-update-test-cache-"));
  t.after(async () => { const { rm } = await import("node:fs/promises"); await rm(work, { recursive: true, force: true }); });
  const paths = createRunPaths(work, createCacheIdentity(), "linux");
  await prepareRunPaths(paths);
  const foreign = join(work, "another-run", "sentinel.txt");
  await marker(foreign);
  paths.cache = join(work, "another-run");
  paths.shipIt = paths.home;
  assert.match((await removeOwnedRunPaths(paths)).join("\n"), /updater cache is outside the owned HOME[\s\S]*Squirrel cache is outside the owned HOME/);
  assert.equal(await readFile(foreign, "utf8"), `sentinel: ${foreign}\n`);

  const failing = createRunPaths(work, createCacheIdentity(), "linux");
  await mkdir(failing.home, { recursive: true });
  failing.cache = join(failing.home, ".cache", updaterCacheName(createCacheIdentity()), "sentinel", "descendant");
  await mkdir(dirname(dirname(failing.cache)), { recursive: true });
  await writeFile(dirname(failing.cache), "not a directory");
  const failures = await removeOwnedRunPaths(failing);
  assert.ok(failures.some(failure => failure.startsWith("updater cache:")), failures.join("\n"));
  assert.deepEqual(failures.filter(failure => failure.startsWith("Squirrel cache:")), []);
});

test("the harness uses the test-only cache override, not a fixed HOME cache deletion", async () => {
  const script = await readFile(new URL("./update-test.mjs", import.meta.url), "utf8");
  assert.match(script, /extraMetadata\.name=\$\{cacheIdentity\}/);
  assert.doesNotMatch(script, /com\.example\.persona\.desktop\.ShipIt/);
  assert.doesNotMatch(script, /const CACHE =/);
});
