#!/usr/bin/env python3
"""Regular-file-only packaged-first-run and AppArmor cleanup receipt transitions."""
import json
import os
from pathlib import Path
import sys


def regular(path: Path) -> bool:
    return path.is_file() and not path.is_symlink()


def read(path: Path) -> dict:
    if not regular(path):
        raise ValueError("receipt must be an existing non-symlink regular file")
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError("receipt must be a JSON object")
    return value


def exclusive_json(path: Path, value: dict) -> None:
    if path.is_symlink() or path.exists():
        raise FileExistsError("receipt path already exists or is not a regular file")
    data = (json.dumps(value, separators=(",", ":")) + "\n").encode()
    # O_EXCL refuses existing directories, files and symlinks. No rename into a directory.
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    try:
        with os.fdopen(fd, "wb") as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
    except BaseException:
        try:
            path.unlink()
        except OSError:
            pass
        raise


def replace_regular_json(path: Path, value: dict) -> None:
    if path.is_symlink() or (path.exists() and not path.is_file()):
        raise ValueError("receipt destination must be a non-symlink regular file")
    temporary = path.with_name(path.name + ".next")
    exclusive_json(temporary, value)
    try:
        os.rename(temporary, path)
    except BaseException:
        try:
            temporary.unlink()
        except OSError:
            pass
        raise


def require_identity(value: dict, name: str) -> None:
    if value.get("profile") != name or not isinstance(value.get("ok"), bool):
        raise ValueError("receipt identity or top-level ok field is invalid")


def main() -> None:
    action, name, raw_directory = sys.argv[1:4]
    directory = Path(raw_directory)
    run = directory / f"{name}.run.json"
    cleanup = directory / f"{name}.cleanup.json"
    if action == "begin-load":
        exclusive_json(run, {"profile": name, "ok": False, "failure": "load-in-progress"})
    elif action == "load-success":
        current = read(run)
        require_identity(current, name)
        if current["ok"] is not False or current.get("failure") != "load-in-progress":
            raise ValueError("load success requires its own pending result")
        replace_regular_json(run, {"profile": name, "ok": True, "failure": ""})
    elif action == "load-failure":
        current = read(run)
        require_identity(current, name)
        if current["ok"] is not False or current.get("failure") != "load-in-progress":
            raise ValueError("load failure must replace only its own pending result")
        replace_regular_json(run, {"profile": name, "ok": False, "failure": sys.argv[4]})
    elif action == "first-run-result":
        outcome = sys.argv[4]
        if outcome not in ("passed", "failed"):
            raise ValueError("first-run outcome must be passed or failed")
        current = read(run)
        require_identity(current, name)
        if current["ok"] is not True or current.get("failure") != "":
            raise ValueError("first-run outcome requires a successful load")
        if current.get("firstRun") is not None:
            raise ValueError("first-run outcome is immutable")
        current["firstRun"] = outcome
        replace_regular_json(run, current)
    elif action == "validate-first-run-result":
        current = read(run)
        require_identity(current, name)
        if current["ok"] is not True or current.get("failure") != "" or current.get("firstRun") not in ("passed", "failed"):
            raise ValueError("cleanup requires successful load and a completed first-run result")
    elif action == "validate-owned-cleanup":
        current = read(run)
        require_identity(current, name)
        if current.get("firstRun") is not None:
            raise ValueError("owned cleanup cannot replace a completed first-run result")
        pending = current["ok"] is False and current.get("failure") in (
            "load-in-progress", "profile-query-failed-after-load", "load-verification-failed")
        loaded = current["ok"] is True and current.get("failure") == ""
        if not pending and not loaded:
            raise ValueError("owned cleanup requires a confirmed add, not an ambiguous parser failure")
        marker = directory / f"{name}.ownership"
        if not regular(marker) or marker.read_text(encoding="utf-8") != "load-confirmed\n":
            raise ValueError("owned cleanup ownership marker does not match the receipt")
        profile = directory / f"{name}.apparmor"
        if not regular(profile):
            raise ValueError("owned cleanup profile must be a regular file")
        expected = f"profile {name} {sys.argv[4]} flags=(unconfined) {{"
        if expected not in profile.read_text(encoding="utf-8").splitlines():
            raise ValueError("owned cleanup profile identity does not match the run")
    elif action == "cleanup-attempt":
        # Never overwrite a prior success OR a prior specific failure.
        exclusive_json(cleanup, {"profile": name, "ok": False, "failure": "cleanup-in-progress"})
    elif action in ("cleanup-success", "cleanup-failure"):
        current = read(cleanup)
        require_identity(current, name)
        if current["ok"] is not False or current.get("failure") != "cleanup-in-progress":
            raise ValueError("cleanup result must replace only its own pending attempt")
        result = {"profile": name, "ok": action == "cleanup-success", "failure": ""}
        current_run = read(run)
        test_failed = current_run.get("firstRun") == "failed"
        if test_failed:
            result["ok"] = False
            result["failure"] = "first-run-failed"
        if action == "cleanup-success":
            result.update(removal=sys.argv[4], verifiedAbsent=True)
        else:
            verified = sys.argv[6] == "true" if len(sys.argv) > 6 else False
            result.update(removal=sys.argv[4], verifiedAbsent=verified)
            if test_failed:
                result["cleanupFailure"] = sys.argv[5]
            else:
                result["failure"] = sys.argv[5]
        replace_regular_json(cleanup, result)
    else:
        raise ValueError("unknown receipt action")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, json.JSONDecodeError) as error:
        print(f"receipt state rejected: {error}", file=sys.stderr)
        sys.exit(1)
