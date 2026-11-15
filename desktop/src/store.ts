import { join } from "node:path";
import { readFile, rename, unlink, writeFile } from "node:fs/promises";
import type { WindowState } from "./window-state.js";

/** The state file contains geometry only, never a hub credential or web storage. */
export class JSONStore {
  private pending: Promise<void> = Promise.resolve();
  private writeSequence = 0;
  constructor(private readonly file: string) {}
  static inUserData(userData: string): JSONStore { return new JSONStore(join(userData, "desktop-state.json")); }

  async read<T>(): Promise<T | undefined> {
    try {
      const value: unknown = JSON.parse(await readFile(this.file, "utf8"));
      return value && typeof value === "object" && !Array.isArray(value) ? value as T : undefined;
    } catch { return undefined; }
  }

  async update(patch: Record<string, unknown>): Promise<void> {
    // Updates read the state written by the preceding operation, not a stale concurrent snapshot.
    const operation = this.pending.then(async () => {
      const previous = await this.read<Record<string, unknown>>() ?? {};
      const next = { ...previous, ...patch };
      const temporary = `${this.file}.${process.pid}.${++this.writeSequence}.tmp`;
      try {
        await writeFile(temporary, `${JSON.stringify(next, null, 2)}\n`, { mode: 0o600 });
        await rename(temporary, this.file);
      } finally {
        // A failed rename must not leave a temporary file that can be mistaken for state.
        await unlink(temporary).catch(() => {});
      }
    });
    this.pending = operation.catch(() => {});
    return operation;
  }

  async saveWindow(state: WindowState): Promise<void> { await this.update({ window: state }); }
}
