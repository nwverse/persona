"""Private helper for the existing Hub updater. No publication or service installation.

Requests arrive on stdin; credentials and command output never go into the receipt.
Run `python3 webdev.py verify BACKUP` to verify a retained recovery point offline.
"""
import datetime
import fcntl
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import shlex
import signal
import subprocess
import sys
import tarfile
import tempfile
from urllib.parse import urlsplit, urlunsplit

OWNER = "persona-web-development-backup-v1"
REPOSITORY = "https://github.com/nwverse/persona.git"
NAME = re.compile(r"snapshot-[0-9]{20}-[a-f0-9]{12}")
PLATFORMS = {"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64"}
# Automatic adoption must prove ancestry within this bounded history window.
# Older/divergent installations can still explicitly choose a manual update.
HISTORY_LIMIT = 1024


def run(argv, cwd=None, env=None, output=False, timeout=1800):
    # Do not echo argv: Git credentials, database URLs and hooks can be private.
    with subprocess.Popen(argv, cwd=cwd, env=env, start_new_session=True,
                          stdout=subprocess.PIPE if output else None, text=output) as process:
        try:
            stdout, _ = process.communicate(timeout=timeout)
        except BaseException:
            os.killpg(process.pid, signal.SIGTERM)
            try:
                process.communicate(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.communicate()
            raise
        if process.returncode:
            raise subprocess.CalledProcessError(process.returncode, argv)
        return stdout


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def write_json(path, value):
    with path.open("x", encoding="utf-8") as stream:
        json.dump(value, stream, indent=2)
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())


def sync_dir(path):
    fd = os.open(path, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def lock_backup(lock):
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError as exc:
        raise ValueError("another backup or prepare is in progress") from exc


def failure_detail(exc):
    # Do not stringify subprocess errors: their argv can contain credentials.
    if isinstance(exc, ValueError):
        return str(exc)
    if isinstance(exc, subprocess.CalledProcessError):
        return f"CalledProcessError (exit {exc.returncode})"
    return type(exc).__name__


def resolve(request):
    refs = run(["git", "ls-remote", REPOSITORY, "refs/heads/main"], output=True)
    match = re.fullmatch(r"([a-f0-9]{40})\trefs/heads/main\n?", refs)
    if not match:
        raise ValueError("project main branch did not return a full commit")
    source = Path(request["work"]) / "metadata"
    source.mkdir()
    run(["git", "init", "--quiet", str(source)])
    run(["git", "-C", str(source), "remote", "add", "origin", REPOSITORY])
    # Read the committer date of the captured object, not a later branch head or check time.
    run(["git", "-C", str(source), "fetch", "--depth=1", "--filter=blob:none", "--no-tags", "origin", match[1]])
    updated_at = run(["git", "-C", str(source), "show", "-s", "--format=%cI", match[1]], output=True).strip()
    return {"branch": "main", "commit": match[1], "updated_at": updated_at}


def build(request):
    root = Path(request["work"])
    source, artifacts = root / "source", root / "artifacts"
    source.mkdir()
    artifacts.mkdir()
    selected = request["candidate"]
    run(["git", "init", "--quiet", str(source)])
    run(["git", "-C", str(source), "remote", "add", "origin", REPOSITORY])
    # Fetch the captured object, never a moving branch head; detached checkout only.
    run(["git", "-C", str(source), "fetch", f"--depth={HISTORY_LIMIT}", "--filter=blob:none", "--no-tags", "origin", selected["commit"]])
    run(["git", "-C", str(source), "checkout", "--detach", selected["commit"]])
    env = dict(os.environ, GIT_TERMINAL_PROMPT="0", CGO_ENABLED="0")
    web, server = source / "web", source / "server"
    run(["npm", "ci", "--no-audit", "--no-fund"], web, env)
    run(["npm", "run", "build:development"], web, env)
    shutil.copytree(web / "dist", server / "internal/hub/webdist", dirs_exist_ok=True)
    run(["go", "run", "./internal/webasset/precompress", "internal/hub/webdist"], server, env)
    bridge = source / "integrations/agent-sdk"
    run(["npm", "ci", "--no-audit", "--no-fund"], bridge, env)
    run(["npm", "run", "build"], bridge, env)
    # Runtime assets are build inputs, not a test or release pipeline.
    run(["go", "generate", "./internal/runtimepkg"], server, env)
    flags = (f'-s -w -X main.version={request["version"]} -X main.channel=release '
             f'-X main.build={request["build"]} -X main.commit={selected["commit"]}')
    if request.get("release_url"):
        flags += " -X " + shlex.quote("main.updateURL=" + request["release_url"])
    tags = (server / "build-tags.txt").read_text().strip()
    platforms = request["platforms"]
    if not set(platforms) <= PLATFORMS:
        raise ValueError("unsupported node platform")
    for platform in platforms:
        goos, arch = platform.split("-")
        programs = ["personad"]
        if platform == request["platform"]:
            programs += ["persona-hub", "persona-manager"]
        for program in programs:
            binary = root / (program + "-" + platform)
            run(["go", "build", "-tags", tags, "-trimpath", "-ldflags", flags,
                 "-o", str(binary), "./cmd/" + program], server, dict(env, GOOS=goos, GOARCH=arch))
            with binary.open("rb") as src, gzip.open(artifacts / (binary.name + ".gz"), "wb") as dst:
                shutil.copyfileobj(src, dst)
    ancestors = run(["git", "rev-list", f"--max-count={HISTORY_LIMIT}", selected["commit"]], source, output=True).splitlines()
    return {"ancestors": ancestors, "artifacts": str(artifacts)}


def verify(path):
    if path.is_symlink() or not path.is_dir() or not NAME.fullmatch(path.name):
        raise ValueError("not an owned snapshot directory")
    manifest_path = path / "manifest.json"
    if manifest_path.is_symlink():
        raise ValueError("linked snapshot manifest")
    manifest = json.loads(manifest_path.read_text())
    if manifest.get("owner") != OWNER or manifest.get("id") != path.name:
        raise ValueError("snapshot ownership mismatch")
    files = manifest.get("files", {})
    if "files.tar.gz" not in files or not any(n.endswith(".dump") for n in files):
        raise ValueError("snapshot is missing history or database data")
    if {p.name for p in path.iterdir()} != set(files) | {"manifest.json"}:
        raise ValueError("snapshot contains unowned entries")
    for name, expected in files.items():
        if Path(name).name != name:
            raise ValueError("invalid snapshot member")
        item = path / name
        if item.is_symlink() or not item.is_file() or digest(item) != expected:
            raise ValueError("snapshot checksum mismatch")
    for program in list(manifest.get("programs", {}).values()) + list(manifest.get("nodes", {}).values()):
        if program["file"] not in files:
            raise ValueError("recovery program is missing")
        native = hashlib.sha256()
        with gzip.open(path / program["file"], "rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                native.update(chunk)
        if native.hexdigest() != program["sha256"]:
            raise ValueError("recovery program checksum mismatch")
    with tarfile.open(path / "files.tar.gz", "r:gz") as archive:
        for member in archive:
            if member.name.startswith("/") or ".." in Path(member.name).parts or not (member.isfile() or member.isdir() or member.issym() or member.islnk()):
                raise ValueError("invalid history archive member")
            if (member.issym() or member.islnk()) and (member.linkname.startswith("/") or ".." in Path(member.linkname).parts):
                raise ValueError("unsafe history archive link")
    return manifest


def backup_store(request):
    store = Path(request["runtime"]).resolve(strict=True) / "update/development-backups"
    if store.is_symlink():
        raise ValueError("linked backup directory")
    return store


def backup_version(path):
    # Listing reads only metadata. Full payload verification happens before use.
    if path.is_symlink() or not path.is_dir() or not NAME.fullmatch(path.name):
        raise ValueError("not an owned snapshot directory")
    file = path / "manifest.json"
    if file.is_symlink():
        raise ValueError("linked snapshot manifest")
    m = json.loads(file.read_text())
    if (not isinstance(m, dict) or m.get("owner") != OWNER or m.get("id") != path.name or
            not isinstance(m.get("from_commit"), str) or not re.fullmatch(r"[a-f0-9]{40}", m["from_commit"]) or
            not m.get("from_version") or not isinstance(m.get("from_build"), int) or m["from_build"] <= 0 or
            not isinstance(m.get("platform"), str) or m["platform"] not in PLATFORMS or
            not isinstance(m.get("programs"), dict) or "personad" not in m["programs"]):
        raise ValueError("snapshot has no installable program version")
    return {"id": path.name, "version": m["from_version"], "build": m["from_build"],
            "commit": m["from_commit"], "updated_at": m.get("from_updated_at", ""),
            "created_at": m["created_at"]}


def backups(request):
    store = backup_store(request)
    versions = []
    if store.exists():
        for path in sorted(store.iterdir(), reverse=True):
            try:
                versions.append(backup_version(path))
            except (ValueError, OSError, KeyError, json.JSONDecodeError):
                pass
    return {"backups": versions}


def prepare_backup(request):
    ident = request["backup_id"]
    if not NAME.fullmatch(ident):
        raise ValueError("invalid snapshot ID")
    store = backup_store(request)
    with (store / ".lock").open("a") as lock:
        lock_backup(lock)
        path = store / ident
        version = backup_version(path)
        m = verify(path)
        if m["platform"] != request["platform"]:
            raise ValueError("snapshot belongs to a different Hub platform")
        if request.get("candidate") and (version["commit"] != request["candidate"]["commit"] or
                version["version"] != request["version"] or version["build"] != request["build"]):
            raise ValueError("snapshot version changed")
        packages = {"personad-" + p: v for p, v in m.get("nodes", {}).items() if p in PLATFORMS}
        packages.update({name + "-" + m["platform"]: v for name, v in m["programs"].items()
                         if name in ("personad", "persona-hub", "persona-manager")})
        if any("personad-" + p not in packages for p in request.get("platforms", [])):
            raise ValueError("snapshot is missing a required Node platform")
        artifacts = Path(request["work"]) / "artifacts"
        artifacts.mkdir()
        # Copy only programs, never database dumps, configuration or history.
        # These private copies also survive rotation of the selected snapshot.
        for name, info in packages.items():
            target = artifacts / (name + ".gz")
            shutil.copyfile(path / info["file"], target)
            if digest(target) != m["files"][info["file"]]:
                raise ValueError("staged program checksum mismatch")
        return {"artifacts": str(artifacts)}


def inventory(request):
    runtime = Path(request["runtime"]).resolve(strict=True)
    hub = json.loads((runtime / "hub.json").read_text())
    databases = [request.get("hub_database") or hub["database_url"]]
    roots = [runtime]
    # Include every local manager, not just the built-in one. Missing/malformed
    # configuration is an error rather than silently omitting its history.
    manager_root = runtime / "managers"
    if manager_root.exists():
        for file in sorted(manager_root.glob("*/manager.json")):
            manager = json.loads(file.read_text())
            if file.parent.name == "assistant" and not manager.get("moved_to_own_database", {}).get("at"):
                pass  # Its live tables are still in the Hub database until the move commits.
            elif manager.get("database_url"):
                databases.append(manager["database_url"])
            elif manager.get("database_name"):
                url = urlsplit(databases[0])
                databases.append(urlunsplit(url._replace(path="/" + manager["database_name"])))
            kb = manager.get("kb_dir")
            if kb:
                kb = Path(kb).expanduser()
                roots.append((file.parent / kb).resolve(strict=True))
    backup = request["config"]["backup"]
    roots += [Path(p).expanduser().resolve(strict=True) for p in backup.get("extra_roots", [])]
    databases += backup.get("extra_databases", [])
    # Contained roots are already archived, and duplicate databases are dumped once.
    roots = sorted(set(roots), key=lambda p: (len(p.parts), str(p)))
    roots = [p for i, p in enumerate(roots) if not any(p.is_relative_to(q) for q in roots[:i])]
    return runtime, roots, list(dict.fromkeys(databases))


def snapshot(request):
    runtime, roots, databases = inventory(request)
    backup = request["config"]["backup"]
    if not backup.get("quiesce") or not backup.get("resume"):
        raise ValueError("configure writer quiesce and resume commands before updating")
    store = runtime / "update" / "development-backups"
    store.mkdir(mode=0o700, parents=True, exist_ok=True)
    if store.is_symlink():
        raise ValueError("linked backup directory")
    with (store / ".lock").open("a") as lock:
        lock_backup(lock)
        coordination = store / "coordination-pending.json"
        if coordination.exists():
            raise ValueError("previous writer coordination needs recovery; inspect the retained marker")
        if any(store.glob(".incomplete-*")):
            raise ValueError("an incomplete snapshot needs inspection before retrying")
        ident = "snapshot-" + datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%d%H%M%S%f") + "-" + request["candidate"]["commit"][:12]
        temporary = Path(tempfile.mkdtemp(prefix=".incomplete-", dir=store))
        published = store / ident
        manifest = {"owner": OWNER, "id": ident, "commit": request["candidate"]["commit"],
                    "from_version": request.get("from_version", ""), "from_commit": request.get("from_commit", ""),
                    "from_build": request.get("from_build", 0), "from_updated_at": request.get("from_updated_at", ""),
                    "created_at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "platform": request.get("platform", ""),
                    "roots": [str(p) for p in roots], "databases": len(databases), "programs": {}, "nodes": {}, "files": {}}
        write_json(coordination, {"owner": OWNER, "snapshot": ident, "phase": "quiescing"})
        snapshot_error = None
        try:
            try:
                print("development backup: pausing writers", flush=True)
                run(backup["quiesce"], timeout=60)
                # Hooks must stop every DB/file writer in this snapshot's scope,
                # including independent managers, before returning successfully.
                for i, database in enumerate(databases):
                    print(f"development backup: database {i}", flush=True)
                    target = temporary / f"database-{i}.dump"
                    # The DSN goes in the environment, never argv or the receipt.
                    run(["pg_dump", "--format=custom", "--no-owner", "--no-acl", "--file", str(target)],
                        env=dict(os.environ, PGDATABASE=database))
                    with target.open("rb") as stream:
                        header = stream.read(5)
                    if target.stat().st_size < 5 or header != b"PGDMP":
                        raise ValueError("database dump is incomplete")
                    run(["pg_restore", "--list", str(target)], output=True)
                    manifest["files"][target.name] = digest(target)
                excluded = {runtime / n for n in ("update", "bin", "cache", "logs")}
                count = 0

                def include(info):
                    nonlocal count
                    # Keep all config, key, history and attachment files. Only known
                    # runtime artifacts and non-data sockets/FIFOs are omitted.
                    root_index, *parts = Path(info.name).parts
                    original = roots[int(root_index.removeprefix("root-"))].joinpath(*parts)
                    if any(original == p or original.is_relative_to(p) for p in excluded):
                        return None
                    if info.issym():
                        # Absolute and escaping links need their own explicitly
                        # inventoried root; fail instead of making a hollow backup.
                        if Path(info.linkname).is_absolute() or ".." in Path(info.linkname).parts:
                            raise ValueError("history contains a link requiring an explicit snapshot boundary")
                    if not (info.isfile() or info.isdir() or info.issym() or info.islnk()):
                        return None
                    count += info.isfile()
                    info.uid = info.gid = 0
                    info.uname = info.gname = ""
                    return info

                print("development backup: configuration and history files", flush=True)
                with tarfile.open(temporary / "files.tar.gz", "w:gz", dereference=False) as archive:
                    for i, root in enumerate(roots):
                        archive.add(root, arcname=f"root-{i}", filter=include)
                if count == 0:
                    raise ValueError("no history or configuration files found")
                manifest["files"]["files.tar.gz"] = digest(temporary / "files.tar.gz")
                # Effective environment overrides can differ from hub.json. Keep
                # restore DSNs only in this private backup, never in its manifest/API.
                write_json(temporary / "effective-databases.json", databases)
                manifest["files"]["effective-databases.json"] = digest(temporary / "effective-databases.json")
                print("development backup: installed recovery programs", flush=True)
                for name, path in request.get("executables", {}).items():
                    if name not in ("personad", "persona-hub", "persona-manager"):
                        raise ValueError("unknown recovery program")
                    artifact = temporary / ("program-" + name + ".gz")
                    native = hashlib.sha256()
                    with Path(path).open("rb") as src, gzip.open(artifact, "wb") as dst:
                        for chunk in iter(lambda: src.read(1024 * 1024), b""):
                            native.update(chunk)
                            dst.write(chunk)
                    manifest["files"][artifact.name] = digest(artifact)
                    manifest["programs"][name] = {"file": artifact.name, "target": path, "sha256": native.hexdigest()}
                # Preserve all locally available packages for the installed
                # version, including platforms with no currently registered Node.
                node_dir = Path(request["node_directory"]) if request.get("node_directory") else None
                if node_dir is not None and node_dir.is_symlink():
                    raise ValueError("linked Node package directory")
                for platform in sorted(PLATFORMS) if node_dir is not None else []:
                    path = node_dir / ("personad-" + platform)
                    if path.is_symlink() or not path.is_file():
                        continue
                    version = path.with_name(path.name + ".version")
                    if version.exists() and version.read_text().strip() != request["from_version"]:
                        continue
                    artifact = temporary / ("node-" + platform + ".gz")
                    with Path(path).open("rb") as src, gzip.open(artifact, "wb") as dst:
                        shutil.copyfileobj(src, dst)
                    manifest["files"][artifact.name] = digest(artifact)
                    manifest["nodes"][platform] = {"file": artifact.name, "sha256": digest(Path(path))}
                write_json(temporary / "manifest.json", manifest)
            except BaseException as exc:
                snapshot_error = exc
                print("development backup: snapshot failed: " + failure_detail(exc), file=sys.stderr, flush=True)
                raise
            finally:
                # Even partially failed quiesce hooks can have stopped a writer.
                # A failed resume preserves its marker and blocks adoption/retry.
                print("development backup: resuming writers", flush=True)
                try:
                    run(backup["resume"], timeout=60)
                except BaseException as exc:
                    print("development backup: resume failed: " + failure_detail(exc), file=sys.stderr, flush=True)
                    if snapshot_error is not None:
                        raise exc from snapshot_error
                    raise
                coordination.unlink()
                sync_dir(store)
            for member in temporary.iterdir():
                sync_dir(member)
            os.rename(temporary, published)
            sync_dir(store)
            verify(published)
            print("development backup: verified " + ident, flush=True)
        except BaseException:
            # Interrupted/failed snapshots are never counted or pruned automatically.
            raise
        pruned = []
        owned = []
        for path in store.iterdir():
            if NAME.fullmatch(path.name) and not path.is_symlink():
                try:
                    verify(path)
                    owned.append(path)
                except (ValueError, OSError, json.JSONDecodeError):
                    pass  # Unknown/damaged data is retained for inspection.
        for old in sorted(owned)[:-3]:
            print("development backup: cleaning " + old.name, flush=True)
            shutil.rmtree(old)
            pruned.append(old.name)
        sync_dir(store)
        return {"id": ident, "retained": min(len(owned), 3), "pruned": pruned}


def main():
    os.umask(0o077)
    if len(sys.argv) == 3 and sys.argv[1] == "verify":
        verify(Path(sys.argv[2]))
        print("Snapshot verified")
        return
    request = json.load(sys.stdin)
    # No interactive credentials or indefinite network prompt in an updater.
    os.environ["GIT_TERMINAL_PROMPT"] = "0"
    signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(InterruptedError()))
    operation = request["operation"]
    result = {"resolve": resolve, "build": build, "backup": snapshot,
              "backups": backups, "prepare_backup": prepare_backup}[operation](request)
    write_json(Path(request["result"]), result)


if __name__ == "__main__":
    try:
        main()
    except BaseException as exc:
        # Child output is in a private local log; no exception/DSN/argv enters the API.
        print("Development update helper failed: " + failure_detail(exc), file=sys.stderr)
        sys.exit(1)
