import { availableParallelism } from "node:os";

// Every isolated file loads the shared catalog and its own module graph. Spawning one
// process per host CPU makes small runners spend their CPU on initialization and
// scheduling instead of the suite (vitest run --maxWorkers=8 measured in #12458).
export function testWorkers(parallelism = availableParallelism()): number {
  return Math.min(8, Math.max(parallelism - 1, 1));
}
