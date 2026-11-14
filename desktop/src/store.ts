import { dirname, join } from "node:path";
import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import type { WindowState } from "./window-state.js";

/** The state file contains geometry only, never a hub credential or web storage. */
export class JSONStore {
  private pending: Promise<unknown> = Promise.resolve();

  constructor(private readonly file: string) {}
  static inUserData(userData: string): JSONStore { return new JSONStore(join(userData, "desktop-state.json")); }

  async read<T>(): Promise<T | undefined> {
    try {
      const value: unknown = JSON.parse(await readFile(this.file, "utf8"));
      return value && typeof value === "object" && !Array.isArray(value) ? value as T : undefined;
    } catch { return undefined; }
  }

  async update(patch: Record<string, unknown>): Promise<void> {
    // Window events and the setup-to-hub transition can save in the same turn. Serialize each read-modify-rename so
    // neither one overwrites the other's hub/geometry, and a queued failure cannot reject a later operation.
    const next = this.pending.then(() => this.writeUpdate(patch), () => this.writeUpdate(patch));
    this.pending = next;
    return next;
  }

  private async writeUpdate(patch: Record<string, unknown>): Promise<void> {
    const previous = await this.read<Record<string, unknown>>() ?? {};
    const next = { ...previous, ...patch };
    const temporary = `${this.file}.${process.pid}.${Math.random().toString(36).slice(2)}.tmp`;
    await mkdir(dirname(this.file), { recursive: true });
    await writeFile(temporary, `${JSON.stringify(next, null, 2)}\n`, { mode: 0o600 });
    await rename(temporary, this.file);
  }

  async saveWindow(state: WindowState): Promise<void> { await this.update({ window: state }); }
}
