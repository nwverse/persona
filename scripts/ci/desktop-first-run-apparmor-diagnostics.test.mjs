import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { chmod, mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import test from "node:test";

const script = fileURLToPath(new URL("desktop-first-run-apparmor-diagnostics.sh", import.meta.url));
const production = await readFile(script, "utf8");
for (const forbidden of ["apparmor_parser", "umount", "chmod", "chown", "tee", "systemctl", "sysctl", "mount ", "stat ", "command "]) assert.equal(production.includes(forbidden), false, forbidden);

async function fixture(t, mode = "available") {
  const directory = await mkdtemp(join(tmpdir(), "persona-apparmor-diagnostics-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const bin = join(directory, "bin");
  await mkdir(bin);
  const calls = join(directory, "calls");
  const profiles = join(directory, "profiles");
  await writeFile(profiles, "unrelated-profile (enforce)\n");
  await writeFile(join(bin, "sudo"), `#!/bin/bash
set -uo pipefail
printf '%s\\n' "$*" >> ${JSON.stringify(calls)}
case "$1" in
  grep)
    if [[ "$6" == /sys/kernel/security/apparmor/profiles ]]; then
      if [[ ${JSON.stringify(mode)} == missing ]]; then printf 'missing interface\\n' >&2; exit 2; fi
      if [[ ${JSON.stringify(mode)} == unavailable ]]; then printf 'unreadable interface\\n' >&2; exit 2; fi
      /usr/bin/grep -E -e "$4" -- ${JSON.stringify(profiles)}
      exit $?
    fi
    if [[ "$6" == /proc/mounts ]]; then /usr/bin/grep -E -e "$4" -- /proc/mounts; exit $?; fi
    /usr/bin/grep "$@"
    ;;
  ls) printf 'synthetic file metadata\\n' ;;
  cat)
    if [[ "$3" == /sys/kernel/security/apparmor/profiles && ${JSON.stringify(mode)} == available ]]; then
      /usr/bin/cat -- ${JSON.stringify(profiles)}
    else
      printf 'interface is unavailable\\n' >&2; exit 2
    fi
    ;;
  aa-status) printf 'synthetic AppArmor status\\n' ;;
  *) exit 97 ;;
esac
`);
  await chmod(join(bin, "sudo"), 0o755);
  return { directory, calls, profiles, env: {
    PATH: `${bin}:${process.env.PATH}`,
    GITHUB_ACTIONS: "true", CI: "true", GITHUB_RUN_ID: "12345", GITHUB_RUN_ATTEMPT: "1", RUNNER_TEMP: directory,
  } };
}

test("read-only diagnostics preserve the exact profile query and synthetic profile list", async t => {
  const { directory, calls, env } = await fixture(t);
  for (const phase of ["before-load", "after-load", "after-cleanup"]) {
    const result = spawnSync("bash", [script, phase], { env, encoding: "utf8" });
    assert.equal(result.status, 0, `${result.stdout}${result.stderr}`);
  }
  const log = await readFile(calls, "utf8");
  assert.ok(log.includes("/sys/kernel/security/apparmor/profiles"));
  assert.ok(log.includes("persona-first-run-12345-1"));
  assert.ok(log.includes("cat -- /sys/kernel/security/apparmor/profiles"));
  assert.ok(log.includes("aa-status --enabled"));
  assert.ok(log.includes("ls -l -- /sys/kernel/security/apparmor/profiles"));
  const evidence = await readFile(join(directory, "persona-first-run-12345-1.diagnostics", "profiles-cat.txt"), "utf8");
  assert.ok(evidence.includes("unrelated-profile (enforce)"));
  assert.ok(evidence.includes("exit=0"));
  const names = await readdir(join(directory, "persona-first-run-12345-1.diagnostics"));
  for (const required of ["profiles-grep.txt", "profiles-ls.txt", "profiles-cat.txt", "aa-status.txt", "mounts-grep.txt"]) assert.ok(names.includes(required));
});

test("query and diagnostic errors fail without writing policy", async t => {
  const { directory, calls, env } = await fixture(t, "unavailable");
  const result = spawnSync("bash", [script, "before-load"], { env, encoding: "utf8" });
  assert.equal(result.status, 1);
  assert.ok((await readFile(calls, "utf8")).includes("aa-status"));
  assert.ok((await readFile(join(directory, "persona-first-run-12345-1.diagnostics", "profiles-cat.txt"), "utf8")).includes("exit=2"));
  assert.doesNotMatch(await readFile(script, "utf8"), /apparmor_parser/);
});

test("the diagnostics command accepts only its three phases", async t => {
  const { env } = await fixture(t);
  for (const phase of ["", "load", "cleanup", "../../tmp"]) {
    const result = spawnSync("bash", [script, phase], { env, encoding: "utf8" });
    assert.notEqual(result.status, 0);
  }
});
