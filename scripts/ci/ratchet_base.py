"""The merge base a count ratchet holds a change to, and the files as they are there (#12785).

A count ratchet fails only when a count rises (docs/operations/testing.md, "Count ratchets"). A count below its baseline
passes, so `main` can sit under a baseline until someone runs scripts/ci/lower_baselines.py, and the baseline alone
would then let a change add back up to it. Where a base is known, a ratchet holds each file the change touches to the
lower of its baseline and its count at the merge base; a file the change does not touch has the merge base's count
already. With no base (a tree outside Git, or a clone without the default branch) only the baseline applies.

The base is the one the ratchets already use: --base, else CI_BASE_SHA, else the clone's default branch from local refs
(origin/HEAD, origin/main, origin/master). An explicit base that does not resolve is an error, never "no base": that
would quietly drop the limit. Whether a file is at the merge base comes from the tree (git ls-tree), and a listed file
that cannot be read is an error too: CI's guards checkout is blobless (#12753), and a failed fetch is not absence.

The sleep, web font and prompt-language ratchets import it. scripts/ci/changed_ratchets.py (run by
scripts/hooks/merge-pr.sh) and the PR watcher's guards load it through them, but use only the ratchets' counting and
messages, never a base of their own.
"""
import os
import subprocess

GIT_TIMEOUT = 60  # seconds for one git command; each reads a few trees or a batch of small files
LS_TREE_CHUNK = 500  # paths per git ls-tree call, well under any command-line limit
NOTICE_LINES = 10  # entries a passing run names under their baseline; the rest are a count


class BaseError(Exception):
    """The base could not be read: it does not resolve, or git failed."""


def _git(root, args, data=None):
    try:
        return subprocess.run(["git", "-C", str(root), *args], input=data, capture_output=True, timeout=GIT_TIMEOUT)
    except subprocess.TimeoutExpired:
        raise BaseError("git {0} timed out".format(" ".join(args[:3])))


def _detail(result):
    detail = (result.stderr or result.stdout).decode("utf-8", "replace").strip()
    return ": " + detail if detail else ""


def _commit(root, ref):
    result = _git(root, ["rev-parse", "--verify", "--quiet", "--end-of-options", ref + "^{commit}"])
    return result.stdout.decode().strip() if result.returncode == 0 else None


def _default_ref(root):
    """The clone's default branch, local refs only: origin/HEAD, else origin/main, else origin/master."""
    symbolic = _git(root, ["symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"])
    candidates = [symbolic.stdout.decode().strip()] if symbolic.returncode == 0 and symbolic.stdout.strip() else []
    candidates += ["refs/remotes/origin/HEAD", "refs/remotes/origin/main", "refs/remotes/origin/master"]
    for ref in candidates:
        if _commit(root, ref):
            return ref
    return None


def resolve(root, base=None, env=None):
    """The MergeBase of HEAD and the base, or None when there is no base to hold a change to."""
    env = os.environ if env is None else env
    inside = _git(root, ["rev-parse", "--is-inside-work-tree"])
    if inside.returncode != 0 or inside.stdout.strip() != b"true" or not _commit(root, "HEAD"):
        return None
    explicit = base or env.get("CI_BASE_SHA")
    ref = explicit or _default_ref(root)
    if not ref:
        return None
    target = _commit(root, ref)
    merged = _git(root, ["merge-base", "HEAD", target]) if target else None
    if merged is None or merged.returncode != 0:
        if explicit:
            raise BaseError("base {0} does not resolve to a commit with a merge base here; refusing to skip the "
                            "merge-base limit".format(explicit))
        return None
    return MergeBase(root, merged.stdout.decode().strip())


class MergeBase:
    """One merge base: which paths the change touches since it, and their text there."""

    def __init__(self, root, commit):
        self.root = root
        self.commit = commit
        self.label = commit[:12]
        self._changed = None

    def changed(self):
        """Paths whose working-tree file differs from the merge base: committed, staged, unstaged and untracked."""
        if self._changed is None:
            paths = set()
            for args in (["diff", "--name-only", "--no-renames", "-z", self.commit, "--"],
                         ["ls-files", "--others", "--exclude-standard", "-z"]):
                result = _git(self.root, args)
                if result.returncode != 0:
                    raise BaseError("git {0} failed{1}".format(" ".join(args[:2]), _detail(result)))
                paths.update(name for name in result.stdout.decode("utf-8", "surrogateescape").split("\0") if name)
            self._changed = paths
        return self._changed

    def files(self, directory):
        """The files under directory at the merge base, as repository paths."""
        result = _git(self.root, ["ls-tree", "-r", "-z", "--name-only", "--full-tree", self.commit, "--", directory])
        if result.returncode != 0:
            raise BaseError("git ls-tree {0} failed{1}".format(self.label, _detail(result)))
        return [name for name in result.stdout.decode("utf-8", "surrogateescape").split("\0") if name]

    def texts(self, paths, errors="replace"):
        """{path: text} for the paths that are files at the merge base; a path absent there is left out."""
        oids = {}
        paths = list(paths)
        for start in range(0, len(paths), LS_TREE_CHUNK):
            result = _git(self.root, ["ls-tree", "-z", "--full-tree", self.commit, "--"] + paths[start:start + LS_TREE_CHUNK])
            if result.returncode != 0:
                raise BaseError("git ls-tree {0} failed{1}".format(self.label, _detail(result)))
            for record in result.stdout.decode("utf-8", "surrogateescape").split("\0"):
                if not record:
                    continue
                meta, _, name = record.partition("\t")
                fields = meta.split()  # mode, type, object id
                if len(fields) == 3 and fields[1] == "blob":
                    oids[name] = fields[2]
        if not oids:
            return {}
        unique = list(dict.fromkeys(oids.values()))
        result = _git(self.root, ["cat-file", "--batch"], data="".join(oid + "\n" for oid in unique).encode())
        if result.returncode != 0:
            raise BaseError("cannot read {0} file(s) at {1}{2}".format(len(unique), self.label, _detail(result)))
        out, at, blobs = result.stdout, 0, {}
        for oid in unique:
            end = out.index(b"\n", at)
            header = out[at:end].split()
            if len(header) != 3 or header[1] != b"blob":
                raise BaseError("cannot read {0} at {1}".format(oid, self.label))
            size = int(header[2])
            blobs[oid] = out[end + 1:end + 1 + size].decode("utf-8", errors)
            at = end + 1 + size + 1  # the content, then the newline cat-file adds after it
        return {name: blobs[oid] for name, oid in oids.items()}


def held(path, allowed, base_entries, base_count):
    """The most a changed file may have: the lower of its baseline and its merge-base count.

    An entry the merge base's baseline does not have yet is the change's own (a moved file the web font ratchet's
    --update recorded, or a raise the sleep ratchet's never-raise check refuses), so the baseline alone holds it."""
    if path not in base_entries:
        return allowed
    return min(allowed, base_count)


def print_notice(name, lines, command):
    """A passing run's notice of the entries under their baseline and how to tidy them. Never a failure."""
    if not lines:
        return
    print("{0}: {1} file(s) under the baseline; lowering it is tidying anyone can do, not this change's job: "
          "python3 scripts/ci/lower_baselines.py, or {2}".format(name, len(lines), command))
    for line in lines[:NOTICE_LINES]:
        print("  " + line)
    if len(lines) > NOTICE_LINES:
        print("  ... and {0} more".format(len(lines) - NOTICE_LINES))
