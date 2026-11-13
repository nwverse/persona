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


def resolve():
    refs = run(["git", "ls-remote", REPOSITORY, "refs/heads/main"], output=True)
    match = re.fullmatch(r"([a-f0-9]{40})\trefs/heads/main\n?", refs)
    if not match:
        raise ValueError("project main branch did not return a full commit")
    return {"branch": "main", "commit": match[1]}


def build(request):
    root = Path(request["work"])
    source, artifacts = root / "source", root / "artifacts"
    source.mkdir()
    artifacts.mkdir()
    selected = request["candidate"]
    run(["git", "init", "--quiet", str(source)])
    run(["git", "-C", str(source), "remote", "add", "origin", REPOSITORY])
    # Fetch the captured object, never a moving branch head; detached checkout only.
    run(["git", "-C", str(source), "fetch", "--no-tags", "origin", selected["commit"]])
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
    ancestors = run(["git", "rev-list", selected["commit"]], source, output=True).splitlines()
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
    for program in manifest.get("programs", {}).values():
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
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
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
                    "roots": [str(p) for p in roots], "databases": len(databases), "programs": {}, "files": {}}
        write_json(coordination, {"owner": OWNER, "snapshot": ident, "phase": "quiescing"})
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
                write_json(temporary / "manifest.json", manifest)
            finally:
                # Even partially failed quiesce hooks can have stopped a writer.
                # A failed resume preserves its marker and blocks adoption/retry.
                print("development backup: resuming writers", flush=True)
                run(backup["resume"], timeout=60)
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
    result = {"resolve": lambda r: resolve(), "build": build, "backup": snapshot}[operation](request)
    write_json(Path(request["result"]), result)


if __name__ == "__main__":
    try:
        main()
    except BaseException as exc:
        # Child output is in a private local log; no exception/DSN/argv enters the API.
        detail = str(exc) if isinstance(exc, ValueError) else type(exc).__name__
        print("Development update helper failed: " + detail, file=sys.stderr)
        sys.exit(1)
