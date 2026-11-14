// Filesystem and event-receipt helpers for the test-only desktop update harness.
import { readFile } from "node:fs/promises";
import { join } from "node:path";

const PRODUCT = "Persona Update Test";
const sleep = milliseconds => new Promise(done => setTimeout(done, milliseconds));

/** electron-builder writes app-update.yml directly to the platform's resources directory. */
export function packagedUpdateConfigPath(buildDirectory, platform, architecture = process.arch) {
  let parts;
  if (platform === "win32") parts = ["win-unpacked", "resources", "app-update.yml"];
  else if (platform === "darwin") parts = [`mac-${architecture}`, `${PRODUCT}.app`, "Contents", "Resources", "app-update.yml"];
  else if (platform === "linux") parts = ["linux-unpacked", "resources", "app-update.yml"];
  else throw new Error(`unsupported update-test platform: ${platform}`);
  return join(buildDirectory, ...parts);
}

export function readPackagedUpdateConfig(buildDirectory, platform, architecture) {
  return readFile(packagedUpdateConfigPath(buildDirectory, platform, architecture), "utf8");
}

export function readUpdateTestEvents(profile) {
  return readFile(join(profile, "update-test.jsonl"), "utf8")
    .then(text => text.split("\n").filter(Boolean).map(line => JSON.parse(line)), () => []);
}

/** Poll the app's real event file until the predicate returns a value or the deadline passes. */
export async function watchUpdateTestEvents(profile, seconds, predicate, { pollMilliseconds = 500 } = {}) {
  const deadline = Date.now() + seconds * 1000;
  for (;;) {
    const found = predicate(await readUpdateTestEvents(profile));
    if (found) return found;
    if (Date.now() > deadline) return undefined;
    await sleep(pollMilliseconds);
  }
}

export function isNsisInstallAt(event, expectedExecutableDirectory) {
  return event?.event === "nsis-install" && event.executableDirectory === expectedExecutableDirectory;
}

/**
 * Await the event before reading it. Only the running executable proves its location; the optional updater field
 * remains diagnostic and is deliberately not part of this verdict.
 */
export async function watchForNsisInstall(profile, seconds, expectedExecutableDirectory, options) {
  const event = await watchUpdateTestEvents(profile, seconds, events => events.find(entry => entry.event === "nsis-install"), options);
  return { event, matchesExpectedDirectory: isNsisInstallAt(event, expectedExecutableDirectory) };
}
