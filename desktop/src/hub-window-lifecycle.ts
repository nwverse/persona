import { inHub } from "./urls.js";
import type { HubAddress } from "./urls.js";

/** A window belongs to the hub captured at creation. A hub transition creates a new generation, even if the old
 * window is still loading or being destroyed; its asynchronous callbacks must never own the next hub. */
export class HubWindowLifecycle<T> {
  #pending: Promise<T> | undefined;
  #generation = 0;

  get generation(): number { return this.#generation; }
  get pending(): boolean { return this.#pending !== undefined; }

  show(create: () => Promise<T>): Promise<T> {
    if (!this.#pending) {
      this.#pending = (async () => create())();
      const pending = this.#pending;
      const clear = () => { if (this.#pending === pending) this.#pending = undefined; };
      pending.then(clear, clear);
    }
    return this.#pending;
  }

  replace(): number {
    this.#generation++;
    this.#pending = undefined;
    return this.#generation;
  }
}

export function sameHubWindow(hub: HubAddress | undefined, currentURL: string): boolean {
  if (!hub) return false;
  try {
    return inHub(hub, currentURL);
  } catch { return false; }
}
