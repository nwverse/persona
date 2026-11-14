import assert from "node:assert/strict";
import { appendFile, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import { isNsisInstallAt, packagedUpdateConfigPath, readPackagedUpdateConfig, watchForNsisInstall, watchUpdateTestEvents } from "./update-test-receipts.mjs";

async function fixture(t) {
  const directory = await mkdtemp(join(tmpdir(), "persona-update-test-receipts-"));
  t.after(async () => { await rm(directory, { recursive: true, force: true }); });
  return directory;
}

async function configFixture(t, platform, architecture = "x64") {
  const directory = await fixture(t);
  const path = packagedUpdateConfigPath(directory, platform, architecture);
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, "provider: generic\nurl: http://127.0.0.1:12345/\nupdaterCacheDirName: run-updater\n");
  return { directory, path };
}

const layouts = [
  ["win32", "x64", "win-unpacked/resources/app-update.yml"],
  ["darwin", "x64", "mac-x64/Persona Update Test.app/Contents/Resources/app-update.yml"],
  ["darwin", "arm64", "mac-arm64/Persona Update Test.app/Contents/Resources/app-update.yml"],
  ["linux", "x64", "linux-unpacked/resources/app-update.yml"],
];

for (const [platform, architecture, relative] of layouts) {
  test(`${platform} ${architecture}: reads app-update.yml from the exact packaged resources path`, async t => {
    const { directory, path } = await configFixture(t, platform, architecture);
    assert.equal(path, join(directory, relative));
    assert.match(await readPackagedUpdateConfig(directory, platform, architecture), /^updaterCacheDirName: run-updater$/m);
  });
}

test("a decoy at the legacy macOS Resources/resources path does not satisfy the real path", async t => {
  const directory = await fixture(t);
  const decoy = join(directory, "mac-arm64/Persona Update Test.app/Contents/Resources/resources/app-update.yml");
  await mkdir(dirname(decoy), { recursive: true });
  await writeFile(decoy, "updaterCacheDirName: wrong-decoy\n");
  assert.equal(packagedUpdateConfigPath(directory, "darwin", "arm64"), join(directory, "mac-arm64/Persona Update Test.app/Contents/Resources/app-update.yml"));
  await assert.rejects(readPackagedUpdateConfig(directory, "darwin", "arm64"), { code: "ENOENT" });
  assert.equal(await readFile(decoy, "utf8"), "updaterCacheDirName: wrong-decoy\n");
});

for (const [platform, architecture, relative] of [
  ["win32", "x64", "win-unpacked/app-update.yml"],
  ["linux", "x64", "linux-unpacked/app-update.yml"],
  ["darwin", "x64", "mac-x64/Persona Update Test.app/Contents/Resources/resources/app-update.yml"],
]) {
  test(`${platform}: a config only above or below its resources directory is rejected`, async t => {
    const directory = await fixture(t);
    const decoy = join(directory, relative);
    await mkdir(dirname(decoy), { recursive: true });
    await writeFile(decoy, "updaterCacheDirName: wrong-decoy\n");
    await assert.rejects(readPackagedUpdateConfig(directory, platform, architecture), { code: "ENOENT" });
  });
}

const record = entry => `${JSON.stringify({ at: Date.now(), ...entry })}\n`;

test("watch awaits a genuinely delayed NSIS event and validates its real executable directory", async t => {
  const profile = await fixture(t);
  const installed = join(profile, "owned-app");
  const receipt = watchForNsisInstall(profile, 2, installed, { pollMilliseconds: 10 });
  // Leave the file absent for several polls, then create it asynchronously. No expected directory is assigned to the
  // updater field: its real locked implementation leaves that optional property null.
  const write = new Promise(resolve => setTimeout(resolve, 75)).then(() => writeFile(
    join(profile, "update-test.jsonl"),
    record({ event: "nsis-install", executableDirectory: installed, executableInRunDirectory: true, updaterInstallDirectory: null }),
  ));
  const result = await receipt;
  await write;
  assert.equal(result.matchesExpectedDirectory, true);
  assert.equal(result.event.executableDirectory, installed);
  assert.equal(result.event.updaterInstallDirectory, null);
  assert.equal(result.event.executableInRunDirectory, true);
});

test("watch returns undefined after a missing-event deadline, not a Promise treated as an event", async t => {
  const profile = await fixture(t);
  const started = Date.now();
  const result = await watchForNsisInstall(profile, 0.06, join(profile, "owned-app"), { pollMilliseconds: 10 });
  assert.equal(result.event, undefined);
  assert.equal(result.matchesExpectedDirectory, false);
  assert.ok(Date.now() - started >= 50, "the deadline must be a real asynchronous wait");
});

test("a valid NSIS event with the wrong executable directory cannot pass", async t => {
  const profile = await fixture(t);
  await writeFile(join(profile, "update-test.jsonl"), record({ event: "nsis-install", executableDirectory: join(profile, "other-app"), executableInRunDirectory: false, updaterInstallDirectory: null }));
  const result = await watchForNsisInstall(profile, 0, join(profile, "owned-app"), { pollMilliseconds: 1 });
  assert.equal(result.matchesExpectedDirectory, false);
  assert.equal(result.event.executableDirectory, join(profile, "other-app"));
});

test("watch ignores unrelated early events and accepts a later appended matching event", async t => {
  const profile = await fixture(t);
  const installed = join(profile, "owned-app");
  const file = join(profile, "update-test.jsonl");
  await writeFile(file, record({ event: "start", version: "0.9.0" }) + record({ event: "nsis-install", executableDirectory: join(profile, "wrong-app") }));
  const result = await watchForNsisInstall(profile, 0.04, installed, { pollMilliseconds: 5 });
  assert.equal(result.matchesExpectedDirectory, false);
  assert.equal(result.event.executableDirectory, join(profile, "wrong-app"));
  const appended = watchUpdateTestEvents(profile, 2, events => events.find(entry => isNsisInstallAt(entry, installed)), { pollMilliseconds: 10 });
  await new Promise(resolve => setTimeout(resolve, 70));
  await appendFile(file, record({ event: "nsis-install", executableDirectory: installed }));
  assert.equal((await appended)?.executableDirectory, installed);
});

test("a null optional updater field never substitutes for the executable receipt", () => {
  assert.equal(isNsisInstallAt({ event: "nsis-install", executableDirectory: "/owned/app", updaterInstallDirectory: null }, "/owned/app"), true);
  assert.equal(isNsisInstallAt({ event: "nsis-install", updaterInstallDirectory: "/owned/app" }, "/owned/app"), false);
  assert.equal(isNsisInstallAt(undefined, "/owned/app"), false);
});

test("the harness awaits watch before reading the NSIS event (legacy expression negative control)", async () => {
  const expected = "/new-owned-fixture/app";
  const watch = async () => ({ event: "nsis-install", executableDirectory: expected, updaterInstallDirectory: null });
  // This is the former expression, retained as a falsifiable control: property access on a pending Promise yields
  // undefined. The production helper must not use it. See watchForNsisInstall for the actual awaited read.
  const legacy = await watch()?.installDirectory === expected;
  const awaited = (await watch())?.installDirectory === expected;
  assert.equal(legacy, false);
  assert.equal(awaited, false);
  assert.equal(isNsisInstallAt(await watch(), expected), true);
});

test("unsupported platforms cannot silently fall back to the Linux layout", () => {
  assert.throws(() => packagedUpdateConfigPath("/new-fixture", "freebsd"), /unsupported update-test platform/);
});
