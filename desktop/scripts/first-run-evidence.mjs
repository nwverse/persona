import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { join } from "node:path";

export async function sha256File(file) {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(file)) hash.update(chunk);
  return hash.digest("hex");
}

/** Record the source identity used for this build without treating unrelated untracked files as source changes. */
export function sourceIdentity(root, runGit = args => execFileSync("git", args, { cwd: root, encoding: "utf8" })) {
  const run = args => {
    try { return runGit(args); }
    catch (error) {
      // `git diff --quiet` uses 1 only for an actual tracked difference, not for a Git execution failure.
      if (args[0] === "diff" && args[1] === "--quiet" && error?.status === 1) return 1;
      throw error;
    }
  };
  const head = run(["rev-parse", "HEAD"]).trim();
  const sourceTree = run(["rev-parse", "HEAD^{tree}"]).trim();
  const trackedDiff = run(["diff", "--quiet", "HEAD", "--"]);
  const untracked = run(["ls-files", "--others", "--exclude-standard"]);
  return { sourceCommit: head, sourceTree, sourceMatchesHead: !trackedDiff && untracked.length === 0, untrackedFilesPresent: untracked.length > 0 };
}

export function appAsarPath(platform, file) {
  if (platform === "darwin") return join(file, "..", "..", "Resources", "app.asar");
  if (platform === "win32" || platform === "linux") return join(file, "..", "resources", "app.asar");
  throw new Error(`Unsupported packaged first-run platform: ${platform}`);
}
