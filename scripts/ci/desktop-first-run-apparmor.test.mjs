// Synthetic validation of the production state machine. No parser load, sudo or real kernel profile is used.
// An isolated copy is made because the production entry point accepts no parser, profile-list or sudo overrides.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { chmod, copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import test from "node:test";

const sourceDirectory = fileURLToPath(new URL(".", import.meta.url));
const source = join(sourceDirectory, "desktop-first-run-apparmor.sh");
const receiptSource = join(sourceDirectory, "desktop_first_run_apparmor_receipt.py");
const doubles = {
  normal: "desktop-first-run-parser-double.sh",
  finalQuery: "desktop-first-run-final-query-error-double.sh",
  race: "desktop-first-run-race-double.sh",
};
const fixtureRoot = await mkdtemp(join(tmpdir(), "persona-apparmor-fixture-"));
const script = join(fixtureRoot, "persona-7702-apparmor-proposal.sh");
const helper = join(fixtureRoot, "persona-7702-apparmor-receipt-helper.py");
let production = await readFile(source, "utf8");
production = production.replace('[[ "${GITHUB_ACTIONS:-}" == true && "${CI:-}" == true ]] || { printf \'approved hosted CI environment required\\n\' >&2; exit 2; }', '[[ "${CI:-}" != true ]] || { printf \'isolated synthetic fixture requires CI to be unset\\n\' >&2; exit 2; }');
production = production.replace('profiles_file=/sys/kernel/security/apparmor/profiles', 'profiles_file="${PERSONA_APPARMOR_PROFILES_FILE:?synthetic profile list required}"');
production = production.replace('receipt_helper="$GITHUB_WORKSPACE/scripts/ci/desktop_first_run_apparmor_receipt.py"', `receipt_helper=${JSON.stringify(helper)}`);
production = production.replaceAll('"${sudo_cmd[@]}"', '"$PERSONA_APPARMOR_TEST_DOUBLE"');
const queryLine = production.split("\n").filter(line => line.includes("sudo grep")).at(-1);
if (!queryLine.startsWith('  sudo grep -E -q -e "^${name} ') || !queryLine.endsWith('" -- "$profiles_file" 2>/dev/null && { printf \'present\\n\'; return 0; }')) throw new Error("The read-only sudo profile query changed");
production = production.replace("profile_state() {", `sudo() {
  if [[ "$1" != grep || "$2" != -E || "$3" != -q || "$4" != -e || "$6" != -- || "$7" != "$profiles_file" || "$#" -ne 7 ]]; then return 97; fi
  shift
  command grep "$@"
}
profile_state() {`);
if (!production.includes('sudo apparmor_parser --abort-on-error -K -T -B -a "$compiled"')) throw new Error("The approved load command changed");
production = production.replace('if ! sudo apparmor_parser --abort-on-error -K -T -B -a "$compiled"; then', 'if ! "$PERSONA_APPARMOR_TEST_DOUBLE" apparmor_parser --abort-on-error -K -T -B -a "$compiled"; then');
if (!production.includes('sudo apparmor_parser --abort-on-error -R "$profile"')) throw new Error("The approved removal command changed");
production = production.replace('if ! sudo apparmor_parser --abort-on-error -R "$profile"; then', 'if ! "$PERSONA_APPARMOR_TEST_DOUBLE" apparmor_parser --abort-on-error -R "$profile"; then');
production = production.replace("  apparmor_parser -Q -K -T -o", '  "$PERSONA_APPARMOR_TEST_DOUBLE" -Q -K -T -o');
assert.equal(production.includes("apparmor_compile"), false);
await writeFile(script, production, { mode: 0o700 });
const syntax = spawnSync("bash", ["-n", script], { encoding: "utf8" });
if (syntax.status !== 0) throw new Error(`Isolated shell syntax: ${syntax.stderr}`);
await copyFile(receiptSource, helper);
for (const name of Object.values(doubles)) {
  const target = join(fixtureRoot, name.replace("desktop-first-run", "persona-7702"));
  await copyFile(join(sourceDirectory, "test-fixtures", name), target);
  await chmod(target, 0o755);
}
const double = name => join(fixtureRoot, name.replace("desktop-first-run", "persona-7702"));
const run = (args, options = {}) => {
  const result = spawnSync("bash", [script, ...args], { encoding: "utf8", env: options.env });
  return { code: result.status, output: `${result.stdout}${result.stderr}` };
};
const cleanupAfterPassed = env => {
  const result = run(["first-run-ok"], { env });
  if (result.code !== 0) return result;
  return run(["cleanup"], { env });
};
const name = "persona-first-run-12345-1";
const receiptPath = directory => join(directory, `${name}.cleanup.json`);
const readReceipt = directory => readFile(join(directory, `${name}.run.json`), "utf8").then(JSON.parse);
const readCleanupReceipt = directory => readFile(receiptPath(directory), "utf8").then(JSON.parse);
const fixture = async directory => {
  const temporaryRoot = await mkdtemp(join(directory, "persona-7702-"));
  const workspace = join(temporaryRoot, "safe-runner", "desktop", "release", "linux-unpacked");
  await mkdir(workspace, { recursive: true });
  const exe = join(workspace, "persona-desktop");
  await writeFile(exe, "#!/bin/sh\nexit 0\n");
  await chmod(exe, 0o755);
  return join(temporaryRoot, "safe-runner");
};
const environment = async (directory, extra = {}) => {
  const env = { ...process.env };
  for (const key of Object.keys(env)) if (key.startsWith("PERSONA_APPARMOR_")) delete env[key];
  return {
    ...env,
    CI: "",
    GITHUB_WORKSPACE: await fixture(directory),
    GITHUB_RUN_ID: "12345",
    GITHUB_RUN_ATTEMPT: "1",
    RUNNER_TEMP: directory,
    PERSONA_APPARMOR_TEST_DOUBLE: double(doubles.normal),
    ...extra,
  };
};
const temporary = async (t, prefix) => {
  const directory = await mkdtemp(join(tmpdir(), `persona-7702-${prefix}-`));
  t.after(() => rm(directory, { recursive: true, force: true }));
  return directory;
};

test.after(() => rm(fixtureRoot, { recursive: true, force: true }));

test("a confirmed add with a later verification failure permits only owned cleanup", async t => {
  for (const failure of ["query-after-load-fails", "query-empty"]) {
    const directory = await temporary(t, `confirmed-${failure}`);
    const profiles = join(directory, "profiles");
    const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles, PERSONA_APPARMOR_TEST_FAILURE: failure });
    await writeFile(profiles, "unrelated-profile (enforce)\n");
    assert.notEqual(run(["load"], { env }).code, 0);
    assert.equal(await readFile(join(directory, `${name}.ownership`), "utf8"), "load-confirmed\n");
    const loaded = await readReceipt(directory);
    const expectedFailure = failure === "query-empty" ? "load-verification-failed" : "profile-query-failed-after-load";
    assert.equal(loaded.ok, false);
    assert.equal(loaded.failure, expectedFailure);
    const visibleProfiles = failure === "query-empty" ? profiles : `${profiles}.contents`;
    const expectedEntries = failure === "query-empty" ? "unrelated-profile (enforce)\n" : `unrelated-profile (enforce)\n${name} (unconfined)\n`;
    assert.equal(await readFile(visibleProfiles, "utf8"), expectedEntries);
    const cleaned = run(["cleanup-owned"], { env: { ...env, PERSONA_APPARMOR_PROFILES_FILE: visibleProfiles, PERSONA_APPARMOR_TEST_FAILURE: "" } });
    assert.equal(cleaned.code, 0, cleaned.output);
    assert.equal(await readFile(visibleProfiles, "utf8"), "unrelated-profile (enforce)\n");
    const cleanup = await readCleanupReceipt(directory);
    assert.equal(cleanup.ok, true);
    assert.equal(cleanup.verifiedAbsent, true);
    assert.equal(cleanup.removal, failure === "query-empty" ? "already-absent" : "removed");
    assert.equal((await readReceipt(directory)).failure, expectedFailure);
  }
});

test("missing first-run results can clean an owned load without hiding the failure", async t => {
  const directory = await temporary(t, "cancelled-first-run");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles });
  await writeFile(profiles, "");
  assert.equal(run(["load"], { env }).code, 0);
  assert.notEqual(run(["cleanup"], { env }).code, 0);
  assert.equal(await readFile(profiles, "utf8"), `${name} (unconfined)\n`);
  const cleaned = run(["cleanup-owned"], { env });
  assert.equal(cleaned.code, 0, cleaned.output);
  assert.equal(await readFile(profiles, "utf8"), "");
  assert.equal((await readCleanupReceipt(directory)).verifiedAbsent, true);
  assert.equal((await readReceipt(directory)).firstRun, undefined);
  assert.equal((await readReceipt(directory)).ok, true);
});

test("cleanup-owned cannot erase a completed first-run outcome", async t => {
  const directory = await temporary(t, "cleanup-owned-result");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles });
  await writeFile(profiles, "");
  assert.equal(run(["load"], { env }).code, 0);
  assert.equal(run(["first-run-failed"], { env }).code, 0);
  assert.notEqual(run(["cleanup-owned"], { env }).code, 0);
  assert.equal(await readFile(profiles, "utf8"), `${name} (unconfined)\n`);
  assert.equal((await readReceipt(directory)).firstRun, "failed");
  assert.notEqual(run(["cleanup"], { env }).code, 0);
  assert.equal(await readFile(profiles, "utf8"), "");
  const cleanup = await readCleanupReceipt(directory);
  assert.equal(cleanup.failure, "first-run-failed");
  assert.equal(cleanup.verifiedAbsent, true);
});

test("cleanup-owned refuses failed parser loads and stale ownership or profile identity", async t => {
  const directory = await temporary(t, "cleanup-owned-refusals");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles, PERSONA_APPARMOR_TEST_FAILURE: "load-fails" });
  await writeFile(profiles, "");
  assert.notEqual(run(["load"], { env }).code, 0);
  await writeFile(profiles, `${name} (unconfined)\n`);
  assert.notEqual(run(["cleanup-owned"], { env }).code, 0);
  assert.equal(await readFile(profiles, "utf8"), `${name} (unconfined)\n`);
  assert.equal((await readReceipt(directory)).failure, "load-failed-owner-review-required");
  for (const [file, value] of [
    [`${name}.ownership`, "load-pending\n"],
    [`${name}.apparmor`, "profile another-name /tmp/another-executable flags=(unconfined) {\n  userns,\n}\n"],
  ]) {
    const caseDirectory = await temporary(t, "cleanup-owned-identity");
    const caseProfiles = join(caseDirectory, "profiles");
    const caseEnv = await environment(caseDirectory, { PERSONA_APPARMOR_PROFILES_FILE: caseProfiles });
    await writeFile(caseProfiles, `${name} (unconfined)\n`);
    await writeFile(join(caseDirectory, `${name}.run.json`), JSON.stringify({ profile: name, ok: true, failure: "" }));
    await writeFile(join(caseDirectory, `${name}.ownership`), "load-confirmed\n");
    await writeFile(join(caseDirectory, file), value);
    assert.notEqual(run(["cleanup-owned"], { env: caseEnv }).code, 0);
    assert.equal(await readFile(caseProfiles, "utf8"), `${name} (unconfined)\n`);
  }
});

test("an unreadable profile file fails closed before owned removal", async t => {
  const directory = await temporary(t, "unreadable-profile");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles });
  await writeFile(profiles, "");
  assert.equal(run(["load"], { env }).code, 0);
  const policy = join(directory, `${name}.apparmor`);
  await chmod(policy, 0);
  t.after(() => chmod(policy, 0o600).catch(() => {}));
  assert.notEqual(cleanupAfterPassed(env).code, 0);
  assert.equal((await readCleanupReceipt(directory)).failure, "profile-file-not-readable");
  assert.equal((await readCleanupReceipt(directory)).verifiedAbsent, false);
  assert.equal(await readFile(profiles, "utf8"), `${name} (unconfined)\n`);
});

test("rejects path, run identity and CI injection before emitting a rule", async t => {
  const directory = await temporary(t, "injection");
  const base = await environment(directory);
  for (const [key, values] of Object.entries({
    GITHUB_WORKSPACE: ["/tmp/space runner", "/tmp/star*runner", "/tmp/quote'runner", "/tmp/brace{runner", "/tmp/run\nner"],
    GITHUB_RUN_ID: ["", "12x", "../other", "123 45", "123\n45"],
    GITHUB_RUN_ATTEMPT: ["", "1x", "../other", "1 2"],
  })) {
    for (const value of values) {
      const result = run(["load"], { env: { ...base, [key]: value } });
      assert.notEqual(result.code, 0, `${key}=${value}`);
      assert.equal(result.output.includes("profile persona-first-run"), false);
    }
  }
  const ci = run(["load"], { env: { ...base, CI: "true" } });
  assert.notEqual(ci.code, 0);
  assert.match(await readFile(source, "utf8"), /profiles_file=\/sys\/kernel\/security\/apparmor\/profiles/);
  assert.doesNotMatch(await readFile(source, "utf8"), /PERSONA_APPARMOR|apparmor_compile|PERSONA_APPARMOR_SUDO/);
  const registration = await readFile(join(sourceDirectory, "desktop.sh"), "utf8");
  assert.ok(registration.includes("node --test scripts/ci/desktop-first-run-apparmor.test.mjs scripts/ci/desktop-first-run-apparmor-diagnostics.test.mjs"));
});

test("a foreign profile is preserved through both failed load and always-cleanup", async t => {
  const directory = await temporary(t, "foreign");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles });
  await writeFile(profiles, `${name} (unconfined)\n`);
  const loaded = run(["load"], { env });
  assert.notEqual(loaded.code, 0);
  assert.equal((await readReceipt(directory)).failure, "profile-already-exists");
  const cleaned = run(["cleanup"], { env });
  assert.notEqual(cleaned.code, 0);
  assert.equal((await readReceipt(directory)).failure, "profile-already-exists");
  assert.equal((await readReceipt(directory)).ok, false);
  assert.equal(await readFile(profiles, "utf8"), `${name} (unconfined)\n`);
});

test("confirmed owned load and cleanup remove only that entry and preserve failure-free evidence", async t => {
  const directory = await temporary(t, "success");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles });
  await writeFile(profiles, "unrelated-profile (enforce)\n");
  const loaded = run(["load"], { env });
  assert.equal(loaded.code, 0, loaded.output);
  const cleaned = cleanupAfterPassed(env);
  assert.equal(cleaned.code, 0, cleaned.output);
  assert.equal(await readFile(profiles, "utf8"), "unrelated-profile (enforce)\n");
  const receipt = await readCleanupReceipt(directory);
  assert.equal(receipt.ok, true);
  assert.equal(receipt.removal, "removed");
  assert.equal(receipt.verifiedAbsent, true);
  assert.equal((await readReceipt(directory)).ok, true);
});

test("initial and final query errors never certify absence", async t => {
  const directory = await temporary(t, "query");
  const directoryProfile = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: directory });
  await writeFile(join(directory, `${name}.run.json`), JSON.stringify({ profile: name, ok: true, failure: "", firstRun: "passed" }));
  await writeFile(join(directory, `${name}.ownership`), "load-pending\n");
  const initial = run(["cleanup"], { env: directoryProfile });
  assert.notEqual(initial.code, 0);
  assert.equal((await readCleanupReceipt(directory)).failure, "profile-query-failed");

  const finalDirectory = await temporary(t, "final-query");
  const profiles = join(finalDirectory, "profiles");
  const env = await environment(finalDirectory, {
    PERSONA_APPARMOR_PROFILES_FILE: profiles,
    PERSONA_APPARMOR_TEST_DOUBLE: double(doubles.finalQuery),
  });
  await writeFile(profiles, "");
  assert.equal(run(["load"], { env }).code, 0);
  const final = cleanupAfterPassed(env);
  assert.notEqual(final.code, 0);
  const receipt = await readCleanupReceipt(finalDirectory);
  assert.equal(receipt.ok, false);
  assert.equal(receipt.verifiedAbsent, false);
  assert.equal(receipt.failure, "profile-query-failed");
});

test("load verification failure remains a failure after a later absent cleanup", async t => {
  const directory = await temporary(t, "verify-fail");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles, PERSONA_APPARMOR_TEST_FAILURE: "query-empty" });
  await writeFile(profiles, "");
  assert.notEqual(run(["load"], { env }).code, 0);
  const cleaned = run(["cleanup"], { env });
  assert.notEqual(cleaned.code, 0);
  const receipt = await readReceipt(directory);
  assert.equal(receipt.ok, false);
  assert.equal(receipt.failure, "load-verification-failed");
  assert.equal(await readFile(receiptPath(directory), "utf8").catch(() => ""), "");
});

test("a same-name load race is not owned or deleted by subsequent cleanup", async t => {
  const directory = await temporary(t, "collision");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, {
    PERSONA_APPARMOR_PROFILES_FILE: profiles,
    PERSONA_APPARMOR_TEST_DOUBLE: double(doubles.race),
  });
  await writeFile(profiles, "");
  assert.notEqual(run(["load"], { env }).code, 0);
  assert.equal((await readReceipt(directory)).failure, "load-failed-owner-review-required");
  assert.notEqual(cleanupAfterPassed(env).code, 0);
  assert.equal((await readReceipt(directory)).failure, "load-failed-owner-review-required");
  assert.equal(await readFile(profiles, "utf8"), `${name} (unconfined)\n`);
});

test("local file cleanup failure invalidates stale success evidence", async t => {
  const directory = await temporary(t, "file-cleanup");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles });
  await writeFile(profiles, "");
  await writeFile(join(directory, `${name}.run.json`), JSON.stringify({ profile: name, ok: true, failure: "", firstRun: "passed" }));
  await writeFile(join(directory, `${name}.ownership`), "load-pending\n");
  await mkdir(join(directory, `${name}.apparmor`));
  const cleaned = run(["cleanup"], { env });
  assert.notEqual(cleaned.code, 0, cleaned.output);
  const receipt = await readCleanupReceipt(directory);
  assert.equal(receipt.ok, false);
  assert.equal(receipt.verifiedAbsent, true);
});

test("failed load and failed removal remain failures after cleanup", async t => {
  const directory = await temporary(t, "failures");
  for (const mode of ["load-fails", "remove-fails"]) {
    const caseDirectory = await temporary(t, mode);
    const profiles = join(caseDirectory, "profiles");
    const env = await environment(caseDirectory, { PERSONA_APPARMOR_PROFILES_FILE: profiles, PERSONA_APPARMOR_TEST_FAILURE: mode });
    await writeFile(profiles, "");
    const loaded = run(["load"], { env });
    if (mode === "remove-fails") assert.equal(loaded.code, 0, loaded.output);
    else assert.notEqual(loaded.code, 0);
    if (mode === "remove-fails") {
      await writeFile(join(caseDirectory, `${name}.run.json`), JSON.stringify({ profile: name, ok: true, failure: "", firstRun: "passed" }));
    }
    const cleaned = run(["cleanup"], { env });
    assert.notEqual(cleaned.code, 0, mode);
    const receipt = mode === "load-fails" ? await readReceipt(caseDirectory) : await readCleanupReceipt(caseDirectory);
    assert.equal(receipt.ok, false);
    if (mode === "remove-fails") assert.equal(receipt.verifiedAbsent, false);
    assert.equal(receipt.failure, mode === "load-fails" ? "load-failed-owner-review-required" : "removal-failed");
  }
});

test("a missing ownership record cannot remove a present profile even without a prior receipt", async t => {
  const directory = await temporary(t, "no-ownership");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles });
  await writeFile(profiles, `${name} (complain)\n`);
  await writeFile(join(directory, `${name}.run.json`), JSON.stringify({ profile: name, ok: true, failure: "", firstRun: "passed" }));
  const cleaned = run(["cleanup"], { env });
  assert.notEqual(cleaned.code, 0);
  assert.equal((await readReceipt(directory)).ok, true);
  assert.equal(await readFile(receiptPath(directory), "utf8").catch(() => ""), "");
  assert.equal(await readFile(profiles, "utf8"), `${name} (complain)\n`);
});

test("unreadable regular-file queries and receipt-write failures cannot leave success evidence", async t => {
  const directory = await temporary(t, "unreadable");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles });
  await writeFile(profiles, "");
  await chmod(profiles, 0o000);
  t.after(() => chmod(profiles, 0o600).catch(() => {}));
  const result = run(["load"], { env });
  assert.notEqual(result.code, 0);
  const receipt = await readReceipt(directory);
  assert.equal(receipt.ok, false);
  assert.equal(receipt.failure, "profile-query-failed");
});

test("an unwritable cleanup receipt directory cannot retain a previous success", async t => {
  const directory = await temporary(t, "receipt-write");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles });
  await writeFile(profiles, "");
  assert.equal(run(["load"], { env }).code, 0);
  assert.equal(run(["first-run-ok"], { env }).code, 0);
  await mkdir(receiptPath(directory));
  const result = run(["cleanup"], { env });
  assert.notEqual(result.code, 0);
  assert.equal((await readFile(receiptPath(directory), "utf8").catch(() => "")), "");
});

test("ownership persistence and failed run-receipt writes remain fail closed", async t => {
  const directory = await temporary(t, "ownership-persistence");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles });
  await writeFile(profiles, "");
  await mkdir(join(directory, `${name}.ownership`));
  const loaded = run(["load"], { env });
  assert.notEqual(loaded.code, 0);
  const receipt = await readReceipt(directory);
  assert.equal(receipt.ok, false);
  assert.equal(receipt.failure, "ownership-record-already-exists");
  assert.equal(await readFile(profiles, "utf8"), "");
});

test("a run-receipt failure retains a pending failure and blocks cleanup success", async t => {
  const directory = await temporary(t, "run-receipt-failure");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles });
  await writeFile(profiles, `${name} (unconfined)\n`);
  await writeFile(join(directory, `${name}.run.json.next`), "not-json\n");
  const loaded = run(["load"], { env });
  assert.notEqual(loaded.code, 0);
  const receipt = await readReceipt(directory);
  assert.equal(receipt.ok, false);
  assert.equal(receipt.failure, "load-in-progress");
  await writeFile(profiles, "");
  const cleaned = run(["cleanup"], { env });
  assert.notEqual(cleaned.code, 0);
  assert.equal((await readReceipt(directory)).ok, false);
});

test("profile matching uses the complete NAME (mode) entry, not a substring", async t => {
  for (const foreign of [`${name}-suffix (enforce)\n`, `other-profile /tmp/${name} (enforce)\n`, "persona-first-run-12345 (enforce)\n"]) {
    const directory = await temporary(t, "exact-name");
    const profiles = join(directory, "profiles");
    const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles });
    await writeFile(profiles, foreign);
    assert.equal(run(["load"], { env }).code, 0, foreign);
    assert.equal(cleanupAfterPassed(env).code, 0, foreign);
    assert.equal(await readFile(profiles, "utf8"), foreign);
    assert.equal((await readCleanupReceipt(directory)).verifiedAbsent, true);
  }
});

test("a stale cleanup receipt makes a retry fail closed rather than refresh success", async t => {
  const directory = await temporary(t, "stale-cleanup");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles });
  await writeFile(profiles, "");
  assert.equal(run(["load"], { env }).code, 0);
  assert.equal(cleanupAfterPassed(env).code, 0);
  const result = run(["cleanup"], { env });
  assert.notEqual(result.code, 0);
  const receipt = await readReceipt(directory);
  assert.equal(receipt.ok, true);
  assert.equal(receipt.failure, "");
  const cleanup = await readCleanupReceipt(directory);
  assert.equal(cleanup.ok, true);
  assert.equal(cleanup.verifiedAbsent, true);
});

test("malformed and cross-profile run receipts cannot authorize removal", async t => {
  for (const bad of [
    { profile: "another-profile", ok: true, failure: "" },
    { ok: true },
    { profile: name, ok: false, nested: { ok: true } },
  ]) {
    const directory = await temporary(t, "receipt-identity");
    const profiles = join(directory, "profiles");
    const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles });
    await writeFile(profiles, `${name} (unconfined)\n`);
    await writeFile(join(directory, `${name}.ownership`), "load-pending\n");
    await writeFile(join(directory, `${name}.run.json`), JSON.stringify(bad));
    assert.notEqual(cleanupAfterPassed(env).code, 0);
    assert.equal(await readFile(profiles, "utf8"), `${name} (unconfined)\n`);
  }
});

test("directory-valued receipt destinations cannot become apparent success", async t => {
  const directory = await temporary(t, "receipt-directory");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles });
  await writeFile(profiles, "");
  await mkdir(join(directory, `${name}.run.json`));
  assert.notEqual(run(["load"], { env }).code, 0);
  assert.equal(await readFile(profiles, "utf8"), "");
});

test("a failed cleanup result survives retries without overwriting its reason", async t => {
  const directory = await temporary(t, "stable-cleanup-failure");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles, PERSONA_APPARMOR_TEST_FAILURE: "remove-fails" });
  await writeFile(profiles, "");
  assert.equal(run(["load"], { env }).code, 0);
  assert.notEqual(cleanupAfterPassed(env).code, 0);
  const first = await readFile(receiptPath(directory), "utf8");
  assert.notEqual(run(["cleanup"], { env }).code, 0);
  assert.equal(await readFile(receiptPath(directory), "utf8"), first);
  assert.equal(JSON.parse(first).failure, "removal-failed");
});

test("cleanup requires a recorded first-run result and preserves test failure", async t => {
  const directory = await temporary(t, "first-run-result");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles });
  await writeFile(profiles, "");
  assert.equal(run(["load"], { env }).code, 0);
  assert.notEqual(run(["cleanup"], { env }).code, 0);
  assert.equal(await readFile(profiles, "utf8"), `${name} (unconfined)\n`);
  assert.equal(run(["first-run-failed"], { env }).code, 0);
  const cleaned = run(["cleanup"], { env });
  assert.notEqual(cleaned.code, 0);
  const cleanup = await readCleanupReceipt(directory);
  assert.equal(cleanup.ok, false);
  assert.equal(cleanup.failure, "first-run-failed");
  assert.equal(cleanup.verifiedAbsent, true);
  assert.equal(await readFile(profiles, "utf8"), "");
});

test("an executable directory is not the exact first-run executable", async t => {
  const directory = await temporary(t, "executable-directory");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles });
  await rm(join(env.GITHUB_WORKSPACE, "desktop", "release", "linux-unpacked", "persona-desktop"), { force: true });
  await mkdir(join(env.GITHUB_WORKSPACE, "desktop", "release", "linux-unpacked", "persona-desktop"));
  await writeFile(profiles, "");
  assert.notEqual(run(["load"], { env }).code, 0);
  assert.equal((await readReceipt(directory)).failure, "missing-exact-test-executable");
  assert.equal(await readFile(profiles, "utf8"), "");
});

test("a finalized load failure cannot be overwritten with another reason", async t => {
  const directory = await temporary(t, "immutable-load-failure");
  const helper = join(fixtureRoot, "persona-7702-apparmor-receipt-helper.py");
  for (const args of [
    ["begin-load", name, directory],
    ["load-failure", name, directory, "first-specific-failure"],
  ]) {
    assert.equal(spawnSync("python3", ["-I", helper, ...args]).status, 0);
  }
  assert.notEqual(spawnSync("python3", ["-I", helper, "load-failure", name, directory, "replacement-failure"]).status, 0);
  assert.equal((await readReceipt(directory)).failure, "first-specific-failure");
});

test("combined first-run and cleanup failures retain both outcomes", async t => {
  const directory = await temporary(t, "combined-failures");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles, PERSONA_APPARMOR_TEST_FAILURE: "remove-fails" });
  await writeFile(profiles, "");
  assert.equal(run(["load"], { env }).code, 0);
  assert.equal(run(["first-run-failed"], { env }).code, 0);
  assert.notEqual(run(["cleanup"], { env }).code, 0);
  const cleanup = await readCleanupReceipt(directory);
  assert.equal(cleanup.ok, false);
  assert.equal(cleanup.failure, "first-run-failed");
  assert.equal(cleanup.cleanupFailure, "removal-failed");
  assert.equal(cleanup.verifiedAbsent, false);
  assert.equal((await readReceipt(directory)).firstRun, "failed");
});

test("a pre-existing ownership record cannot be reused to authorize a fresh load", async t => {
  const directory = await temporary(t, "stale-ownership");
  const profiles = join(directory, "profiles");
  const env = await environment(directory, { PERSONA_APPARMOR_PROFILES_FILE: profiles });
  await writeFile(profiles, "");
  await writeFile(join(directory, `${name}.ownership`), "load-confirmed\n");
  const loaded = run(["load"], { env });
  assert.notEqual(loaded.code, 0);
  const receipt = await readReceipt(directory);
  assert.equal(receipt.ok, false);
  assert.equal(receipt.failure, "ownership-record-already-exists");
});
