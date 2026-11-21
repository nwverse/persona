import { beforeEach, describe, expect, it, vi } from "vitest";
import { createHash } from "node:crypto";
import { api } from "./api";
import { scopeBase } from "./change-base";

vi.mock("./api", () => ({ api: vi.fn(), enc: encodeURIComponent }));
const read = vi.mocked(api);
const commit = "1".repeat(40);
const base = "before\n";
const hash = createHash("sha256").update(base).digest("hex");
const hunks = [{ old_start: 1, new_start: 1, lines: ["-before", "+after"] }];
const review = { root: "/repo", base: { hash: commit }, files: [{ path: "src/a.ts", hunks }] };
const lookup = (sha = hash, prior?: string, file = "/repo/src/a.ts") => scopeBase("m", "/repo/src", file, sha, prior, new AbortController().signal);
beforeEach(() => read.mockReset());

describe("the scope's Git preimage", () => {
  it("reads an immutable revision and checks the exact native bytes, even from a nested checkout folder", async () => {
    read.mockResolvedValueOnce({ ...review, files: [] }).mockResolvedValueOnce({ path: "/cache/a", size: 7 })
      .mockResolvedValueOnce({ text: base }).mockResolvedValueOnce(review);
    expect(await lookup(hash, commit)).toEqual({ text: base, hunks });
    const calls = read.mock.calls.map(([p]) => new URL(p as string, "http://fixture.invalid"));
    expect(calls[0].searchParams.get("scope")).toBe("commit");
    expect(calls[1].searchParams.get("rev")).toBe(commit);
    expect(calls[1].searchParams.get("path")).toBe("src/a.ts");
    expect(calls[2].searchParams.get("path")).toBe("/cache/a");
    expect(calls[3].searchParams.get("path")).toBe("src/a.ts");
    expect(calls[3].searchParams.get("base")).toBe(commit);
  });
  it("does not use a coincidentally similar branch, a missing file, or an out-of-folder path", async () => {
    read.mockResolvedValueOnce(review).mockResolvedValueOnce({ path: "/cache/a", size: 6 }).mockResolvedValueOnce({ text: "before" });
    expect(await lookup()).toBeUndefined();
    read.mockReset().mockResolvedValueOnce(review).mockResolvedValueOnce({ missing: true });
    expect(await lookup()).toBeUndefined();
    read.mockReset();
    expect(await lookup(hash, undefined, "/outside/a.ts")).toBeUndefined();
    expect(read).not.toHaveBeenCalled();
  });
  it("accepts a full Git blob object ID from a recorded patch, including its header", async () => {
    const hash = "git:" + createHash("sha1").update("blob 7\0" + base).digest("hex");
    read.mockResolvedValueOnce(review).mockResolvedValueOnce({ path: "/cache/a", size: 7 }).mockResolvedValueOnce({ text: base });
    expect(await lookup(hash)).toEqual({ text: base, hunks });
  });
  it("rejects paginated content and an unpinned revision before using any lines", async () => {
    read.mockResolvedValueOnce(review).mockResolvedValueOnce({ path: "/cache/a", size: 7 }).mockResolvedValueOnce({ text: base, next: 7 });
    expect(await lookup()).toBeUndefined();
    read.mockReset().mockResolvedValueOnce({ ...review, base: { hash: "HEAD" } });
    expect(await lookup()).toBeUndefined();
    expect(read).toHaveBeenCalledTimes(1);
  });
});
