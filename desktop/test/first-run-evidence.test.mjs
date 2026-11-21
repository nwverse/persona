import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { appAsarPath, sha256File, sourceIdentity } from "../scripts/first-run-evidence.mjs";

test("first-run evidence uses the source tree and distinguishes tracked edits from untracked probes", () => {
  const commands = [];
  const runGit = args => {
    commands.push(args);
    if (args[0] === "diff" && args[1] === "--quiet") return 1;
    if (args[0] === "rev-parse" && args[1] === "HEAD") return "267b61a01dcf07a659a8ab3b14b9e8d8bd3fc513\n";
    if (args[0] === "rev-parse") return "561e17b34257ae795394be2324800aae398009db\n";
    return "desktop/scripts/probe-first-run.mjs\n";
  };
  const identity = sourceIdentity("/synthetic/repository", runGit);
  assert.equal(identity.sourceCommit, "267b61a01dcf07a659a8ab3b14b9e8d8bd3fc513");
  assert.equal(identity.sourceTree, "561e17b34257ae795394be2324800aae398009db");
  assert.equal(identity.sourceMatchesHead, false);
  assert.equal(identity.untrackedFilesPresent, true);
  assert.deepEqual(commands[2], ["diff", "--quiet", "HEAD", "--"]);
  assert.equal(appAsarPath("linux", "/tmp/supplied/persona-desktop"), join("/tmp/supplied", "resources", "app.asar"));
  assert.notEqual(appAsarPath("linux", "/tmp/supplied/persona-desktop"), join("/synthetic/repository/desktop", "release", "linux-unpacked", "resources", "app.asar"));
});

test("first-run evidence rejects untracked code even when tracked files match", () => {
  const identity = sourceIdentity("/synthetic/repository", args => {
    if (args[0] === "diff" && args[1] === "--quiet") return 0;
    if (args[0] === "rev-parse" && args[1] === "HEAD") return "267b61a01dcf07a659a8ab3b14b9e8d8bd3fc513\n";
    if (args[0] === "rev-parse") return "561e17b34257ae795394be2324800aae398009db\n";
    return "desktop/src/untracked-dependency.ts\n";
  });
  assert.equal(identity.sourceMatchesHead, false);
  assert.equal(identity.untrackedFilesPresent, true);
});

test("first-run provenance checks status without buffering a large patch or accepting staged edits", async t => {
  const directory = await mkdtemp(join(tmpdir(), "persona-first-run-git-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const git = args => execFileSync("git", args, { cwd: directory, encoding: "utf8" });
  const file = join(directory, "source.txt");
  git(["init", "--quiet"]);
  git(["config", "user.name", "Test Fixture"]);
  git(["config", "user.email", "test@example.invalid"]);
  await writeFile(file, "initial\n");
  git(["add", "source.txt"]);
  git(["commit", "--quiet", "-m", "fixture"]);
  assert.equal(sourceIdentity(directory).sourceMatchesHead, true);
  await writeFile(file, "x".repeat(2 * 1024 * 1024));
  git(["add", "source.txt"]);
  assert.equal(sourceIdentity(directory).sourceMatchesHead, false, "staged-only edits must not certify the HEAD tree");
  git(["restore", "--staged", "source.txt"]);
  git(["restore", "source.txt"]);
  await writeFile(join(directory, "probe.mjs"), "// retained untracked probe\n");
  const untracked = sourceIdentity(directory);
  assert.equal(untracked.sourceMatchesHead, false);
  assert.equal(untracked.untrackedFilesPresent, true);
});

test("first-run evidence hashes the real artifact contents", async t => {
  const directory = await mkdtemp(join(tmpdir(), "persona-first-run-evidence-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const file = join(directory, "app.asar");
  await writeFile(file, "synthetic archive contents");
  assert.equal(await sha256File(file), "b44bc93b0b103e120910b2b583845bd5d31242ce99fa8a4140b90e52148880ed");
});
