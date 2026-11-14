import { dirname, join, resolve } from "node:path";
import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import type { WindowState } from "./window-state.js";

/** The state file contains geometry only, never a hub credential or web storage. */
const pendingWrites = new Map<string, Promise<unknown>>();

export class JSONStore {

  constructor(private readonly file: string) {}
  static inUserData(userData: string): JSONStore { return new JSONStore(join(userData, "desktop-state.json")); }

  async read<T>(): Promise<T | undefined> {
    try {
      const value: unknown = JSON.parse(await readFile(this.file, "utf8"));
      return value && typeof value === "object" && !Array.isArray(value) ? value as T : undefined;
    } catch { return undefined; }
  }

  async update(patch: Record<string, unknown>): Promise<void> {
    // Window events and the setup-to-hub transition can save in the same turn. Serialize each read-modify-rename by
    // canonical file: main's store() factory deliberately returns a new facade for each caller.
    const file = resolve(this.file);
    const previous = pendingWrites.get(file) ?? Promise.resolve();
    const next = previous.then(() => this.writeUpdate(patch), () => this.writeUpdate(patch));
    pendingWrites.set(file, next);
    try { await next; } finally { if (pendingWrites.get(file) === next) pendingWrites.delete(file); }
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
