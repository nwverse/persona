// A signature-blocked macOS scenario is visible evidence, never a skipped or full-acceptance pass.
export function firstRunVerdict({ platform, signature, failure, blocked, passed }) {
  const base = { platform, signature: signature ?? "not-applicable", passed, blocked, functionalFirstRunAccepted: false };
  if (failure || passed === 0) return { ...base, ok: false, status: "failed", label: `FAIL (${failure ?? "no checks ran"})` };
  if (blocked.length === 0) return { ...base, ok: true, status: "passed", functionalFirstRunAccepted: true, label: "PASS" };
  const item = blocked[0];
  const unsignedMacHubOnly = platform === "darwin" && signature === "ad-hoc" && blocked.length === 1
    && item.scenario === "hub setup, web pairing and chat"
    && item.uncovered.length === 3 && item.reason.includes("EnableCookieEncryption")
    && item.requiredEnvironment.includes("Developer ID signed and notarized");
  if (unsignedMacHubOnly) {
    return { ...base, ok: true, status: "setup-passed-hub-blocked", label: "SETUP PASS; FULL FIRST-RUN BLOCKED (unsigned macOS: cookie encryption requires a valid consistent signature)" };
  }
  return { ...base, ok: false, status: "failed", label: "FAIL (a required or unexpected scenario was blocked)" };
}
