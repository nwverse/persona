import { api, enc } from "./api";
import type { Hunk } from "./code-view";
import { below, type FileContent } from "./files";
import { reviewURL, type GitReview, type ReviewTarget } from "./session-review";

export interface ScopeBase { text: string; hunks?: Hunk[] }
const commitID = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;
const maxBytes = 256 * 1024;

// A previously observed commit or today's HEAD is only a candidate. The first edit in the scope must
// identify its original bytes before that immutable blob can supply file positions or surrounding lines.
export async function scopeBase(mid: string, cwd: string, path: string, hash: string, commit: string | undefined, signal: AbortSignal): Promise<ScopeBase | undefined> {
  const relative = below(cwd, path)?.join("/");
  const object = hash.startsWith("git:") ? hash.slice(4) : undefined;
  if (!relative || (object ? !commitID.test(object) : !/^[0-9a-f]{64}$/.test(hash))) return;
  const targets: ReviewTarget[] = [];
  if (commit && commitID.test(commit)) targets.push({ scope: "commit", base: commit });
  targets.push({ scope: "uncommitted" });
  for (const target of targets) {
    if (signal.aborted) return;
    try {
      const git: GitReview = await api<GitReview>(reviewURL(mid, cwd, target, [relative]), { signal });
      const name = below(git.root, path)?.join("/");
      if (!name || !git.base.hash || !commitID.test(git.base.hash)) continue;
      const query = new URLSearchParams({ cwd, path: name, rev: git.base.hash });
      const blob = await api<{ path?: string; size?: number }>("/machines/" + enc(mid) + "/git/blob?" + query, { signal });
      // The node omits a zero byte size for an empty blob.
      if (!blob.path || (blob.size ?? 0) > maxBytes) continue;
      const file = await api<FileContent>("/machines/" + enc(mid) + "/file?path=" + enc(blob.path) + "&stream=1", { signal });
      if (file.truncated || file.next || typeof file.text !== "string") continue;
      const bytes = new TextEncoder().encode(file.text);
      if (bytes.byteLength > maxBytes) continue;
      // An exact Git index line identifies the object, whose hash includes Git's blob header.
      const input = object ? new TextEncoder().encode("blob " + bytes.byteLength + "\0" + file.text) : bytes;
      const digest = new Uint8Array(await crypto.subtle.digest(object?.length === 40 ? "SHA-1" : "SHA-256", input));
      const hex = Array.from(digest, (byte) => byte.toString(16).padStart(2, "0")).join("");
      if (hex !== (object ?? hash)) continue;
      let hunks = git.files.find((f) => f.path === name)?.hunks;
      if (name !== relative && !hunks && !signal.aborted) {
        // The node takes paths from the repository root. The first read discovers that root
        // for sessions opened in a subdirectory. Pin the follow-up to the same immutable base.
        const pinned = { scope: "commit" as const, base: git.base.hash };
        const full = await api<GitReview>(reviewURL(mid, cwd, pinned, [name]), { signal }).catch(() => undefined);
        if (full && full.root === git.root && full.base.hash === git.base.hash) hunks = full.files.find((f) => f.path === name)?.hunks;
      }
      return { text: file.text, hunks };
    } catch {
      // Older hubs, offline nodes and missing blobs can still show the recorded changes.
    }
  }
}
