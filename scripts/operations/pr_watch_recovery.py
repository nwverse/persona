"""Bounded recovery of watcher-owned worktrees and Git fetch debris."""
import json
import os
import re
import shutil
import time
from pathlib import Path

from pr_watch_common import WatchError, bounded, checked, run

STORAGE_ERROR = re.compile(r"(?:no space left|out of diskspace|\bENOSPC\b)", re.I)
OBJECT_ERROR = re.compile(r"(?:object file|corrupt|missing necessary objects|invalid index-pack|unable to read [0-9a-f]{40}|bad object)", re.I)


def owned_root(state_file):
    return Path(state_file).resolve().parent / "worktrees"


def owned_path(state_file, path):
    """Only a real direct child of the watcher's private directory is removable."""
    root = owned_root(state_file)
    candidate = Path(path)
    resolved = candidate.resolve(strict=False)
    if candidate.is_symlink() or candidate.parent != root or resolved.parent != root or resolved == root:
        raise WatchError("refusing to clean a path outside watcher-owned worktrees")
    return resolved


def atomic_leases(state_file, data):
    """Store private lease state with the same atomic replacement as PR evidence."""
    path = Path(state_file)
    temporary = path.with_name(path.name + ".tmp")
    temporary.write_text(json.dumps(data, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    os.chmod(temporary, 0o600)
    temporary.replace(path)


def reclaim_worktrees(checkout, state_file, protected=(), older_than=86400):
    """Reclaim only registered expired leases, never unregistered directories."""
    state = json.loads(Path(state_file).read_text(encoding="utf-8"))
    leases = state.get("worktree_leases", {})
    keep = {Path(path).resolve(strict=False) for path in protected}
    candidates = []
    for name, lease in leases.items():
        path = owned_path(state_file, name)
        if (path in keep or lease.get("active") or not path.exists()
                or time.time() - float(lease.get("heartbeat", 0)) < older_than):
            continue
        candidates.append(path)
    if not candidates:
        return 0
    result = run(["git", "worktree", "remove", "--force", *map(str, candidates)],
                 cwd=checkout, timeout=180)
    if result.returncode:
        # A missing/corrupt administrative entry should not pin an otherwise
        # private temporary directory. Prune only Git's stale metadata next.
        run(["git", "worktree", "prune", "--expire", "now"], cwd=checkout, timeout=90)
    removed = 0
    run(["git", "worktree", "prune", "--expire", "now"], cwd=checkout, timeout=90)
    latest = json.loads(Path(state_file).read_text(encoding="utf-8")).get("worktree_leases", {})
    for path in candidates:
        lease = latest.get(str(path), {})
        if (path in keep or lease.get("active")
                or time.time() - float(lease.get("heartbeat", 0)) < older_than):
            continue
        if not path.exists():
            removed += 1
            continue
        path = owned_path(state_file, path)
        if path.is_dir() and not path.is_symlink():
            shutil.rmtree(str(path))
            removed += 1
    run(["git", "worktree", "prune", "--expire", "now"], cwd=checkout, timeout=90)
    return removed


def remove_worktree(checkout, state_file, path):
    """Remove one known temporary checkout and its possibly stale registration."""
    if not path:
        return
    candidate = owned_path(state_file, path)
    result = run(["git", "worktree", "remove", "--force", str(candidate)], cwd=checkout, timeout=90)
    if result.returncode:
        run(["git", "worktree", "prune", "--expire", "now"], cwd=checkout, timeout=90)
        candidate = owned_path(state_file, candidate)
        if candidate.exists():
            shutil.rmtree(str(candidate))
    run(["git", "worktree", "prune", "--expire", "now"], cwd=checkout, timeout=90)


def recover_object_debris(checkout):
    """Discard only incomplete local objects; refetching restores their contents.

    Never prune reachable objects, expire reflogs, reset a checkout, or remove
    the shared object database. A corrupt nonempty object remains a fail-closed
    error for the caller rather than being mistaken for repaired source code.
    """
    checkout = Path(checkout).resolve()
    common_text = checked(["git", "rev-parse", "--git-common-dir"], cwd=checkout).strip()
    common = (checkout / common_text).resolve() if not Path(common_text).is_absolute() else Path(common_text).resolve()
    if common.parent != checkout or common.name != ".git":
        raise WatchError("refusing to repair objects outside the trusted watcher checkout")
    objects = common / "objects"
    removed = 0
    for directory in objects.iterdir() if objects.is_dir() else ():
        if not re.fullmatch(r"[0-9a-f]{2}", directory.name) or not directory.is_dir() or directory.is_symlink():
            continue
        for item in directory.iterdir():
            if (re.fullmatch(r"[0-9a-f]{38}", item.name) and item.is_file()
                    and not item.is_symlink() and item.stat().st_size == 0):
                item.unlink()
                removed += 1
    pack = objects / "pack"
    if pack.is_dir():
        for item in pack.iterdir():
            if (item.name.startswith("tmp_") and item.is_file() and not item.is_symlink()
                    and time.time() - item.stat().st_mtime > 3600):
                item.unlink()
                removed += 1
    return removed


def recover_worktree_failure(checkout, state_file, error, protected=()):
    """Repair a known infrastructure condition and report whether to retry once."""
    if STORAGE_ERROR.search(str(error)):
        removed = reclaim_worktrees(checkout, state_file, protected)
        if not removed:
            return False
        return True
    if OBJECT_ERROR.search(str(error)):
        recover_object_debris(checkout)
        return True
    return False
