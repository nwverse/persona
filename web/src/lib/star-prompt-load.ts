// The GitHub star prompt's rules load on demand (#5761, lib/star-prompt.ts): first-screen code that feeds them (a
// readiness answer, an Agent this browser started) shares this one import, so the first screen carries it once. A chunk
// that does not load (offline, the hub redeployed under the page) only skips the prompt.
export function withStarPrompt(run: (m: typeof import("./star-prompt")) => void): void {
  import("./star-prompt").then(run).catch(() => {});
}
