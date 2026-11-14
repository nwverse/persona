import assert from "node:assert/strict";
import test from "node:test";
import { firstRunVerdict } from "../scripts/first-run-verdict.mjs";

const macBlock = {
  scenario: "hub setup, web pairing and chat",
  reason: "EnableCookieEncryption requires a valid consistent macOS code signature for Keychain access; an ad-hoc development signature is not signed-release acceptance",
  uncovered: ["submit the hub address", "web sign-in and pairing", "enter the existing chat page"],
  requiredEnvironment: "the administrator-approved Developer ID signed and notarized release workflow",
};

test("only the declared unsigned macOS hub scenario may report a structural pass", () => {
  const verdict = firstRunVerdict({ platform: "darwin", signature: "ad-hoc", blocked: [macBlock], passed: 18 });
  assert.equal(verdict.ok, true);
  assert.equal(verdict.status, "setup-passed-hub-blocked");
  assert.equal(verdict.fullFirstRunAccepted, false);
  assert.match(verdict.label, /BLOCKED/);
  assert.doesNotMatch(verdict.label, /^PASS$/);
});

test("signed macOS, Windows and Linux must run the full path", () => {
  for (const [platform, signature] of [["darwin", "developer-id"], ["win32", "not-applicable"], ["linux", "not-applicable"]]) {
    const verdict = firstRunVerdict({ platform, signature, blocked: [macBlock], passed: 18 });
    assert.equal(verdict.ok, false);
    assert.equal(verdict.fullFirstRunAccepted, false);
  }
});

test("no hidden or additional block can become a green full-acceptance result", () => {
  for (const input of [
    { platform: "darwin", signature: "ad-hoc", blocked: [], passed: 0 },
    { platform: "darwin", signature: "ad-hoc", blocked: [], passed: 20, failure: "setup failed" },
    { platform: "darwin", signature: "ad-hoc", blocked: [macBlock, macBlock], passed: 20 },
    { platform: "darwin", signature: "ad-hoc", blocked: [{ ...macBlock, scenario: "another scenario" }], passed: 20 },
    { platform: "darwin", signature: "ad-hoc", blocked: [{ ...macBlock, uncovered: [] }], passed: 20 },
  ]) {
    const verdict = firstRunVerdict(input);
    assert.equal(verdict.ok, false);
    assert.equal(verdict.fullFirstRunAccepted, false);
  }
});

test("a completed Windows or signed macOS path is the only full acceptance", () => {
  for (const [platform, signature] of [["win32", "not-applicable"], ["darwin", "developer-id"]]) {
    const verdict = firstRunVerdict({ platform, signature, blocked: [], passed: 21 });
    assert.equal(verdict.ok, true);
    assert.equal(verdict.fullFirstRunAccepted, true);
  }
});
