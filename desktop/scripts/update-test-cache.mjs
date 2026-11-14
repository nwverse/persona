// Paths belonging to one update-test run. Every removable path is below the run's own root.
import { randomUUID } from "node:crypto";
import { mkdir, rm } from "node:fs/promises";
import { join, sep } from "node:path";

export const createCacheIdentity = () => `persona-desktop-update-test-${randomUUID()}`;

/** Must match electron-builder: sanitized extraMetadata.name.toLowerCase() + "-updater". */
export const updaterCacheName = identity => {
  if (!/^[a-z0-9-]+$/.test(identity)) throw new Error("update-test cache identity must contain only lowercase letters, digits and dashes");
  return `${identity}-updater`;
};

/**
 * The environment overrides are inherited by Squirrel and the updater-launched AppImage too.
 * The caller passes them explicitly to the processes it owns; no process environment is read here.
 */
export function createRunPaths(work, identity, platform = process.platform) {
  const root = join(work, "owned");
  const home = join(root, "home");
  const cacheBase = platform === "win32" ? join(home, "AppData", "Local")
    : platform === "darwin" ? join(home, "Library", "Caches")
      : join(home, ".cache");
  const configBase = platform === "win32" ? join(home, "AppData", "Roaming")
    : platform === "darwin" ? join(home, "Library", "Application Support")
      : join(home, ".config");
  return {
    root,
    home,
    identity,
    cache: join(cacheBase, updaterCacheName(identity)),
    // Squirrel.Mac uses the test package's bundle ID, under this run's temporary HOME.
    shipIt: join(cacheBase, "com.example.persona.update-test.ShipIt"),
    // The AppImage started by electron-updater has no --user-data-dir; it uses the default profile.
    defaultProfile: join(configBase, "persona-desktop-update-test"),
    environment: {
      HOME: home,
      ...(platform === "win32" ? { USERPROFILE: home, LOCALAPPDATA: join(home, "AppData", "Local"), APPDATA: join(home, "AppData", "Roaming") } : {}),
      ...(platform === "linux" ? { XDG_CACHE_HOME: join(home, ".cache"), XDG_CONFIG_HOME: join(home, ".config") } : {}),
    },
  };
}

export async function prepareRunPaths(paths) {
  await mkdir(paths.root, { recursive: false, mode: 0o700 });
}

/** Only an explicitly owned directory inside the run root can be removed; failure is returned, never hidden. */
export async function removeOwnedRunPaths(paths) {
  const failures = [];
  for (const [name, directory] of [["updater cache", paths.cache], ["Squirrel cache", paths.shipIt], ["default profile", paths.defaultProfile]]) {
    if (directory === join(paths.root, "home") || !directory.startsWith(`${join(paths.root, "home")}${sep}`)) {
      failures.push(`${name} is outside the owned HOME`);
      continue;
    }
    try { await rm(directory, { recursive: true, force: true }); }
    catch (error) { failures.push(`${name}: ${error instanceof Error ? error.message : String(error)}`); }
  }
  return failures;
}
