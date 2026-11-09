#!/usr/bin/env python3
"""Fail when a database migration already on the base branch is edited, deleted or renamed.

Migrations that have been applied are recorded by file name and are not run again, so a later
edit does not repair existing databases. Comment-only and whitespace-only edits pass (whitespace
outside quoted strings is ignored; text inside quotes is not). A new .sql file may be added.

The frozen files are the .sql files directly under server/internal/db/migrations/hub and
server/internal/db/migrations/manager at the merge base of HEAD and the base ref. The base ref
is --base, else CI_BASE_SHA, else the clone's default branch: origin/HEAD if it points at a
commit, else origin/main, else origin/master. Those files are compared with the index and the
working tree.

Duplicate numbers across the two sets are not checked here. TestMigrationSets already rejects
them, with the frozen bothSets and sharedNumbers exceptions kept in that test.
"""
import argparse
import os
from pathlib import Path
import re
import subprocess
import sys

MIGRATION_DIRS = ("server/internal/db/migrations/hub", "server/internal/db/migrations/manager")
MIGRATION_PATH = re.compile(r"^server/internal/db/migrations/(hub|manager)/[^/]+\.sql$")


class FreezeError(Exception):
    """The check could not be run (no base commit, or git failed)."""


def normalize_sql(text):
    """SQL with comments removed and whitespace outside quotes removed.

    Quoted strings (and quoted identifiers) keep their exact text, so a space inside a literal
    is still a change. A comment or a blank line is not.
    """
    text = text.replace("\r\n", "\n").replace("\r", "\n")
    out = []
    i = 0
    n = len(text)
    while i < n:
        ch = text[i]
        if ch == "-" and i + 1 < n and text[i + 1] == "-":
            i += 2
            while i < n and text[i] != "\n":
                i += 1
            continue
        if ch == "/" and i + 1 < n and text[i + 1] == "*":
            i += 2
            while i + 1 < n and not (text[i] == "*" and text[i + 1] == "/"):
                i += 1
            i = i + 2 if i + 1 < n else n
            continue
        if ch in ("'", '"'):
            j = i + 1
            while j < n:
                if text[j] == ch:
                    if j + 1 < n and text[j + 1] == ch:
                        j += 2
                        continue
                    j += 1
                    break
                j += 1
            out.append(text[i:j])
            i = j
            continue
        if ch.isspace():
            i += 1
            continue
        out.append(ch)
        i += 1
    return "".join(out)


def frozen_changes(base, current):
    """Paths in base that current deleted, renamed or edited. Extra files in current are new."""
    found = []
    for path in sorted(base):
        if path not in current:
            found.append((path, "deleted"))
        elif normalize_sql(base[path]) != normalize_sql(current[path]):
            found.append((path, "edited"))
    return found


def changes_against(base, views):
    """Report each frozen path once. A missing copy wins over an edited one."""
    kinds = {}
    for view in views:
        for path, kind in frozen_changes(base, view):
            if path not in kinds or kind == "deleted":
                kinds[path] = kind
    return [(path, kinds[path]) for path in sorted(kinds)]


def _git(root, args, check=True):
    result = subprocess.run(["git", "-C", str(root), *args], capture_output=True, timeout=60)
    if check and result.returncode != 0:
        detail = (result.stderr or result.stdout).decode("utf-8", "replace").strip()
        raise FreezeError("migration freeze: git {0} failed{1}".format(
            " ".join(args[:6]), ": " + detail if detail else ""))
    return result


def _default_ref(root):
    """The clone's default branch, local refs only: origin/HEAD, else origin/main, else origin/master."""
    symbolic = subprocess.run(
        ["git", "-C", str(root), "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"],
        capture_output=True, text=True, timeout=30)
    if symbolic.returncode == 0 and symbolic.stdout.strip():
        candidates = [symbolic.stdout.strip()]
    else:
        candidates = ["refs/remotes/origin/HEAD"]
    candidates.extend(["refs/remotes/origin/main", "refs/remotes/origin/master"])
    for ref in candidates:
        verify = subprocess.run(
            ["git", "-C", str(root), "rev-parse", "--verify", "--quiet", "--end-of-options", ref + "^{commit}"],
            capture_output=True, timeout=30)
        if verify.returncode == 0:
            return ref
    return None


def resolve_base_commit(root, base=None, env=None):
    """The commit whose migrations are already on the base branch (merge-base of HEAD and the ref)."""
    env = os.environ if env is None else env
    ref = base or env.get("CI_BASE_SHA") or _default_ref(root)
    if not ref:
        raise FreezeError(
            "migration freeze: no base commit (set CI_BASE_SHA, pass --base, or fetch origin: "
            "origin/HEAD, else origin/main)")
    sha = _git(root, ["rev-parse", "--verify", "--end-of-options", ref + "^{commit}"]).stdout.decode().strip()
    return _git(root, ["merge-base", "HEAD", sha]).stdout.decode().strip()


def _blobs(root, specs):
    """Object text keyed by the spec that asked for it. Missing objects are absent."""
    if not specs:
        return {}
    result = subprocess.run(
        ["git", "-C", str(root), "cat-file", "--batch"],
        input="".join(spec + "\n" for spec in specs).encode(),
        capture_output=True, timeout=60)
    if result.returncode != 0 and not result.stdout:
        detail = result.stderr.decode("utf-8", "replace").strip()
        raise FreezeError("migration freeze: git cat-file --batch failed{0}".format(": " + detail if detail else ""))
    found = {}
    data = result.stdout
    pos = 0
    for spec in specs:
        newline = data.find(b"\n", pos)
        if newline < 0:
            raise FreezeError("migration freeze: git cat-file --batch ended early")
        header = data[pos:newline].decode("utf-8", "replace")
        pos = newline + 1
        if header.endswith(" missing"):
            continue
        try:
            size = int(header.rsplit(" ", 1)[1])
        except (IndexError, ValueError):
            raise FreezeError("migration freeze: unexpected git cat-file header: " + header)
        blob = data[pos:pos + size]
        pos += size + 1
        found[spec] = blob.decode("utf-8", "backslashreplace")
    return found


def _matching(names):
    return [name for name in names if MIGRATION_PATH.match(name)]


def _named_blobs(root, names, spec_for):
    """Blob text for every name. A name git listed but cannot show is an error, not a pass."""
    specs = [spec_for(name) for name in names]
    blobs = _blobs(root, specs)
    found = {}
    for name, spec in zip(names, specs):
        if spec not in blobs:
            raise FreezeError("migration freeze: git has no blob for {0}".format(spec))
        found[name] = blobs[spec]
    return found


def tree_migrations(root, commit):
    raw = _git(root, ["ls-tree", "-r", "-z", "--name-only", commit, "--", *MIGRATION_DIRS]).stdout
    names = _matching(raw.decode("utf-8", "surrogateescape").split("\0"))
    return _named_blobs(root, names, lambda name: "{0}:{1}".format(commit, name))


def index_migrations(root):
    raw = _git(root, ["ls-files", "-z", "--", *MIGRATION_DIRS]).stdout
    names = _matching(raw.decode("utf-8", "surrogateescape").split("\0"))
    return _named_blobs(root, names, lambda name: ":" + name)


def worktree_migrations(root):
    found = {}
    for directory in MIGRATION_DIRS:
        folder = Path(root) / directory
        if not folder.is_dir():
            continue
        for path in folder.iterdir():
            rel = directory + "/" + path.name
            if path.is_file() and MIGRATION_PATH.match(rel):
                found[rel] = path.read_text(encoding="utf-8", errors="backslashreplace")
    return found


def describe(path, kind):
    if kind == "deleted":
        action = "was deleted or renamed after it reached the base branch"
    else:
        action = "was edited after it reached the base branch"
    return "migration freeze: {0} {1}; add a new migration instead".format(path, action)


def check(root, base=None, env=None):
    """Return (commit, frozen count, violation lines). Raises FreezeError when the base cannot be resolved."""
    root = Path(root)
    commit = resolve_base_commit(root, base, env)
    base_files = tree_migrations(root, commit)
    violations = changes_against(base_files, [index_migrations(root), worktree_migrations(root)])
    return commit, len(base_files), [describe(path, kind) for path, kind in violations]


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--base", help="base ref (default: CI_BASE_SHA, else origin/HEAD, else origin/main)")
    parser.add_argument("--root", default=".", help="repository root (default: the working directory)")
    args = parser.parse_args(argv)
    try:
        commit, count, violations = check(args.root, args.base)
    except FreezeError as error:
        print(error, file=sys.stderr)
        return 2
    if violations:
        print("\n".join(violations), file=sys.stderr)
        return 1
    print("migration freeze: {0} migration(s) on {1} are unchanged".format(count, commit[:12]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
