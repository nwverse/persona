import { dirname, join, resolve } from "node:path";
import { mkdir, readFile, rename, unlink, writeFile } from "node:fs/promises";
import type { WindowState } from "./window-state.js";

/** The state file contains geometry only, never a hub credential or web storage. */
export class JSONStore {
  private static pendingWrites = new Map<string, Promise<void>>();
  private static writeSequence = 0;

  constructor(private readonly file: string) {}
  static inUserData(userData: string): JSONStore { return new JSONStore(join(userData, "desktop-state.json")); }

  async read<T>(): Promise<T | undefined> {
    try {
      const value: unknown = JSON.parse(await readFile(this.file, "utf8"));
      return value && typeof value === "object" && !Array.isArray(value) ? value as T : undefined;
    } catch { return undefined; }
  }

  async update(patch: Record<string, unknown>): Promise<void> {
    // Facades are lightweight, but their canonical state path shares one read-modify-rename queue.
    const file = resolve(this.file);
    const previousWrite = JSONStore.pendingWrites.get(file) ?? Promise.resolve();
    const operation = previousWrite.then(async () => {
      const previous = await this.read<Record<string, unknown>>() ?? {};
      const next = { ...previous, ...patch };
      const temporary = `${file}.${process.pid}.${++JSONStore.writeSequence}.tmp`;
      try {
        await mkdir(dirname(file), { recursive: true });
        await writeFile(temporary, `${JSON.stringify(next, null, 2)}\n`, { mode: 0o600 });
        await rename(temporary, file);
      } finally {
        // A failed write or rename must not leave a temporary file that can be mistaken for state.
        await unlink(temporary).catch(() => {});
      }
    });
    const queued = operation.catch(() => {});
    JSONStore.pendingWrites.set(file, queued);
    try { await operation; } finally { if (JSONStore.pendingWrites.get(file) === queued) JSONStore.pendingWrites.delete(file); }
  }

  async saveWindow(state: WindowState): Promise<void> { await this.update({ window: state }); }
}
