#!/usr/bin/env python3
"""Isolated, read-only restore drill for personad's whole-install backup (docs/server/server.md "Restore drill").

The drill proves that a real `personad backup create`, `verify` and `restore` bring back a lost install with its work in
flight untouched, without starting the restored hub or manager: it builds a private PostgreSQL cluster that listens on a
Unix socket only, applies the real hub and manager migrations, seeds synthetic tasks, Agent links, approvals and outboxes,
runs the real backup commands, damages the databases, loses the runtime directory, restores, and then inspects the result
over read-only connections. It accepts no hub URL, database URL, runtime directory or bundle: everything it touches it
made itself, in a temporary directory it removes on success, failure, timeout and interruption.

It is not a failover test: nothing is promoted, switched or started (see the doc section for what stays unverified).
"""
import argparse
import hashlib
import hmac
import json
import os
from pathlib import Path
import platform
import re
import secrets
import shutil
import signal
import struct
import subprocess
import sys
import tarfile
import tempfile
import time
import uuid
from urllib.parse import quote

ROOT = Path(__file__).resolve().parents[2]
MIGRATIONS = ROOT / "server" / "internal" / "db" / "migrations"
EXIT_PASS, EXIT_FAIL, EXIT_USAGE, EXIT_TIMEOUT = 0, 1, 2, 124
PG_PROGRAMS = ("initdb", "postgres", "psql", "pg_dump", "pg_restore", "pg_isready")
# The drill only ever runs these personad commands: nothing that starts a hub, manager, node or connector.
PERSONAD_COMMANDS = {("backup", "create"), ("backup", "verify"), ("backup", "restore")}
# Inputs that would point the drill at a real install. It has no such options; naming one is refused outright, so a
# mistyped production invocation stops with a reason instead of argparse's generic error.
FORBIDDEN_OPTIONS = (
    "--database-url", "--manager-database-url", "--hub-url", "--hub", "--home", "--runtime", "--persona-home",
    "--bundle", "--output", "--kb", "--kb-dir", "--token", "--hub-docker-container", "--manager-docker-container",
)
FAULTS = ("restore-fails", "kb-drift", "approval-resolved", "outbox-settled", "write-allowed", "timeout")
SECRET_KEY_SIZE = 32  # server/internal/secretkey Size
FINGERPRINT_LABEL = b"persona secret.key fingerprint v1"  # secretkey.Fingerprint
READ_ONLY_SQLSTATE = "25006"  # read_only_sql_transaction
INSPECTOR = "persona-restore-drill"  # the application_name of the drill's own connections
# ELF e_machine of the machines a static personad is built for, by platform.machine().
ELF_MACHINES = {"x86_64": 62, "aarch64": 183}
POSTGRES_URL = re.compile(r"postgres(?:ql)?://[^\s\"']+")
# The tables the checks read, each with the order its rows are compared in. Whole tables, not only the seeded rows: a
# row the restore (or anything else) added counts as a change too, such as a new client operation id.
MANAGER_TABLES = {
    "todos": "id", "todo_agents": "todo_id, machine_id, session_id", "task_plans": "id",
    "interactions": "created_at, id", "manager_outbox": "seq", "sealing_key": "one",
}
HUB_TABLES = {"client_ops": "created_at, id", "handed_over": "manager, id"}


class DrillFailure(Exception):
    """A check or step failed; the message is already free of secrets."""


class DrillTimeout(Exception):
    pass


class Interrupted(Exception):
    def __init__(self, signum):
        super().__init__(signal.Signals(signum).name)
        self.signum = signum


class UsageError(Exception):
    pass


# ---------------------------------------------------------------------------------------------------------------------
# Inputs


def parse_arguments(argv):
    for token in argv:
        name = token.split("=", 1)[0]
        if name in FORBIDDEN_OPTIONS:
            raise UsageError(f"{name} is not accepted: the drill uses only the synthetic install it creates, never a "
                             "hub, database, runtime directory or bundle of a real one")
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0], allow_abbrev=False)
    parser.add_argument("--execute", action="store_true",
                        help="actually run the drill (it starts a private PostgreSQL cluster and runs several minutes)")
    parser.add_argument("--personad", required=True, help="a statically linked Linux personad to drill")
    parser.add_argument("--pg-bin", required=True,
                        help="directory with initdb, postgres, psql, pg_dump, pg_restore and pg_isready of one major version")
    parser.add_argument("--timeout", type=float, default=600, help="overall limit in seconds (default 600)")
    parser.add_argument("--report", help="also write the redacted JSON report to this new file (mode 0600)")
    parser.add_argument("--inject", choices=FAULTS, help="inject one fault; the drill must then fail (tests the drill)")
    args = parser.parse_args(argv)
    if not args.execute:
        raise UsageError("pass --execute to run the drill: it starts a private PostgreSQL cluster and real backup commands")
    if sys.platform != "linux":
        raise UsageError("it runs on Linux (the personad it drills is a static Linux build)")
    if not 30 <= args.timeout <= 3600:
        raise UsageError("--timeout must be between 30 and 3600 seconds")
    args.personad = check_static_linux_binary(Path(args.personad))
    args.pg_bin = check_pg_bin(Path(args.pg_bin))
    if args.report:
        report = Path(args.report).expanduser().absolute()
        if report.exists():
            raise UsageError("--report must name a new file; an existing one is never overwritten")
        if not report.parent.is_dir():
            raise UsageError("--report's directory does not exist")
        args.report = report
    return args


def check_static_linux_binary(path):
    """A regular, executable ELF file without a program interpreter (PT_INTERP): statically linked, for Linux."""
    path = path.expanduser().absolute()
    if not path.is_file() or not os.access(path, os.X_OK):
        raise UsageError("--personad must be an executable file")
    with path.open("rb") as binary:
        header = binary.read(64)
        if len(header) < 52 or header[:4] != b"\x7fELF":
            raise UsageError("--personad must be a Linux ELF executable (build with GOOS=linux CGO_ENABLED=0)")
        elf_class, data = header[4], header[5]
        if data not in (1, 2) or elf_class not in (1, 2):
            raise UsageError("--personad has an unknown ELF layout")
        endian = "<" if data == 1 else ">"
        if elf_class == 2:
            phoff, phentsize, phnum = struct.unpack_from(endian + "Q", header, 32)[0], *struct.unpack_from(endian + "HH", header, 54)
        else:
            phoff, phentsize, phnum = struct.unpack_from(endian + "I", header, 28)[0], *struct.unpack_from(endian + "HH", header, 42)
        if header[7] not in (0, 3):  # ELFOSABI_NONE (System V) or ELFOSABI_LINUX
            raise UsageError("--personad is not a Linux executable")
        machine = struct.unpack_from(endian + "H", header, 18)[0]
        expected = ELF_MACHINES.get(platform.machine().lower())
        if expected is not None and machine != expected:
            raise UsageError(f"--personad is built for another processor than this {platform.machine()} machine")
        for index in range(phnum):
            binary.seek(phoff + index * phentsize)
            entry = binary.read(4)
            if len(entry) < 4:
                raise UsageError("--personad has a truncated ELF program header")
            if struct.unpack(endian + "I", entry)[0] == 3:  # PT_INTERP: dynamically linked
                raise UsageError("--personad must be statically linked (CGO_ENABLED=0)")
    return path


def check_pg_bin(path):
    """Every program the drill and personad backup use, all of one PostgreSQL major version (pg_dump refuses a newer
    server, and personad's backup wants the server's own)."""
    path = path.expanduser().absolute()
    missing = [name for name in PG_PROGRAMS if not os.access(path / name, os.X_OK)]
    if missing:
        raise UsageError("--pg-bin lacks " + ", ".join(missing))
    majors, processes = {}, Processes()
    for name in PG_PROGRAMS:
        try:
            code, stdout, _ = processes.run([str(path / name), "--version"], env=minimal_environment(), timeout=10)
        except (OSError, DrillTimeout):
            raise UsageError(f"--pg-bin's {name} does not run") from None
        finally:
            processes.stop_all()
        found = re.search(r"\(PostgreSQL\) (\d+)", stdout)
        if code != 0 or not found:
            raise UsageError(f"--pg-bin's {name} does not say its PostgreSQL version")
        majors[name] = found.group(1)
    if len(set(majors.values())) != 1:
        raise UsageError("--pg-bin mixes PostgreSQL major versions: " + ", ".join(f"{k} {v}" for k, v in majors.items()))
    return path


# ---------------------------------------------------------------------------------------------------------------------
# Processes and time


class Deadline:
    def __init__(self, seconds, clock=time.monotonic):
        self.clock = clock
        self.end = clock() + seconds

    def remaining(self, cap=None):
        left = self.end - self.clock()
        if left <= 0:
            raise DrillTimeout()
        return left if cap is None else min(cap, left)


class Processes:
    """Every child runs in a process group of its own, so a timeout or interruption stops it and all it started."""

    def __init__(self):
        self.live = {}
        self.unstoppable = []  # leaders or group members that would not stop: cleanup is incomplete

    def run(self, args, *, env, timeout, stdin=None, cwd=None):
        process = subprocess.Popen(args, stdin=subprocess.PIPE if stdin is not None else subprocess.DEVNULL,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env, cwd=cwd,
                                   start_new_session=True)
        self.live[process.pid] = process
        try:
            stdout, stderr = process.communicate(stdin.encode() if stdin is not None else None, timeout=timeout)
        except BaseException as error:
            # Stays in live until stopped, so an interruption here still leaves it to stop_all.
            self.stop(process)
            self.drain(process)
            if isinstance(error, subprocess.TimeoutExpired):
                raise DrillTimeout() from None
            raise
        # What the step left in its group (a background child) goes with it.
        if kill_group_members(process.pid):
            self.unstoppable.append(process.pid)
        self.live.pop(process.pid, None)
        return process.returncode, stdout.decode(errors="replace"), stderr.decode(errors="replace")

    @staticmethod
    def drain(process):
        """Close a stopped step's pipes (a background child that kept them open was stopped with its group)."""
        try:
            process.communicate(timeout=5)
        except (subprocess.TimeoutExpired, ValueError, OSError):
            for stream in (process.stdin, process.stdout, process.stderr):
                if stream:
                    stream.close()

    def spawn(self, args, *, env, log):
        process = subprocess.Popen(args, stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT, env=env,
                                   start_new_session=True)
        self.live[process.pid] = process
        return process

    def stop(self, process, first=signal.SIGTERM, grace=10):
        """Signal the whole group, then kill it; True once the leader is gone."""
        for signum, wait in ((first, grace), (signal.SIGKILL, 5)):
            try:
                os.killpg(process.pid, signum)
            except (ProcessLookupError, PermissionError):
                pass
            try:
                process.wait(timeout=wait)
                break
            except subprocess.TimeoutExpired:
                continue
        left = kill_group_members(process.pid)  # anything the leader left behind in its group
        self.live.pop(process.pid, None)
        if process.poll() is not None and not left:
            return True
        self.unstoppable.append(process.pid)  # not tried again: it already had every signal
        return False

    def stop_all(self):
        stopped = [self.stop(process) for process in list(self.live.values())]
        return all(stopped) and not self.unstoppable


def group_members(pgid):
    """The processes still in process group pgid, other than its (reaped) leader: Linux /proc. A pid still used as a
    group id is never reused, so these are the group's; the pid itself may be reused once the group is empty."""
    members = []
    for stat in Path("/proc").glob("[0-9]*/stat"):
        try:
            fields = stat.read_text().rsplit(")", 1)[1].split()
        except (OSError, IndexError):
            continue
        pid = int(stat.parent.name)
        # fields: state, ppid, pgrp. A zombie has exited (only its parent can reap it; SIGKILL changes nothing).
        if pid != pgid and int(fields[2]) == pgid and fields[0] != "Z":
            members.append(pid)
    return members


def kill_group_members(pgid, wait=5):
    """SIGKILL what is left of a process group whose leader is gone; True if something could not be stopped."""
    deadline = time.monotonic() + wait
    while True:
        members = group_members(pgid)
        if not members:
            return False
        if time.monotonic() > deadline:
            return True
        for pid in members:
            try:
                os.kill(pid, signal.SIGKILL)
            except (ProcessLookupError, PermissionError):
                pass
        time.sleep(0.05)


def minimal_environment(**extra):
    """Nothing inherited but what locates system programs: no PERSONA_*, PG*, proxy, profile or home of the caller."""
    env = {"PATH": os.defpath, "LANG": "C", "LC_ALL": "C", "TZ": "UTC", "PGTZ": "UTC"}
    env.update(extra)
    return env


# ---------------------------------------------------------------------------------------------------------------------
# Pure helpers (unit tested)


def key_fingerprint(key):
    return hmac.new(key, FINGERPRINT_LABEL, hashlib.sha256).hexdigest()[:32]


def tree_digest(root):
    """SHA-256 over every regular file's relative path, mode bits and content, in path order. Missing: None."""
    root = Path(root)
    if not root.is_dir():
        return None
    digest = hashlib.sha256()
    for path in sorted(p for p in root.rglob("*") if p.is_file() or p.is_symlink()):
        relative = path.relative_to(root).as_posix()
        if path.is_symlink():
            digest.update(b"L" + relative.encode() + b"\0" + os.readlink(path).encode() + b"\0")
            continue
        mode = path.lstat().st_mode & 0o7777
        digest.update(b"F" + relative.encode() + b"\0" + str(mode).encode() + b"\0" + hashlib.sha256(path.read_bytes()).digest())
    return digest.hexdigest()


def root_forms(root):
    return [str(root), quote(str(root), safe="")] if root else []


def redact(text, secrets_list, root=None):
    for secret in sorted({s for s in secrets_list if s}, key=len, reverse=True):
        text = text.replace(secret, "<redacted>")
    text = POSTGRES_URL.sub("<database url>", text)
    for form in root_forms(root):
        text = text.replace(form, "<drill dir>")
    return text


def leaked(text, secrets_list, root=None):
    """Whether text holds a secret, a database URL or the drill directory's path (plain or URL-encoded)."""
    return any(secret in text for secret in [*secrets_list, *root_forms(root)] if secret) or bool(POSTGRES_URL.search(text))


def migration_files(part, directory=MIGRATIONS):
    files = sorted((directory / part).glob("*.sql"))
    if not files:
        raise DrillFailure(f"no {part} migrations found")
    return files


def migration_script(part, files):
    """What db.Open does for one set (server/internal/db/db.go migrateFrom): schema_migrations as after the split, then
    each file in its own transaction together with its record."""
    out = ["\\set ON_ERROR_STOP on",
           "CREATE TABLE IF NOT EXISTS schema_migrations (version int NOT NULL, name text NOT NULL, "
           "applied_at timestamptz NOT NULL DEFAULT now(), part text);",
           "CREATE UNIQUE INDEX IF NOT EXISTS schema_migrations_version_part ON schema_migrations "
           "(version, (coalesce(part, '')));"]
    for path in files:
        version = int(path.name.split("_", 1)[0])
        if not re.fullmatch(r"[0-9A-Za-z_.-]+", path.name):
            raise DrillFailure("unexpected migration file name")
        out += ["BEGIN;", path.read_text(), ";",
                f"INSERT INTO schema_migrations (version, name, part) VALUES ({version}, '{path.name}', '{part}');",
                "COMMIT;"]
    return "\n".join(out) + "\n"


def sql_literal(value):
    return "'" + str(value).replace("'", "''") + "'"


class Fixture:
    """The synthetic install: fixed ids, synthetic text and a time point. No real account, address or message."""

    def __init__(self, t0):
        self.t0 = t0  # ISO time the seeded rows were written at
        self.conversation = "drill-conversation"
        self.machine = "drill-machine"
        self.tasks = ["drill-task-1", "drill-task-2"]
        self.plan = "drill-plan-1"
        self.pending_approvals = ["drill-approval-permission", "drill-approval-plan"]
        self.settled_approval = "drill-approval-settled"
        self.outbox = ["drill-outbox-1", "drill-outbox-2", "drill-outbox-3"]
        self.client_ops = {"pending": str(uuid.uuid4()), "unknown": str(uuid.uuid4()), "done": str(uuid.uuid4())}

    def at(self, seconds):
        return f"(timestamptz {sql_literal(self.t0)} + interval '{int(seconds)} seconds')"

    def manager_sql(self, sealing_fingerprint):
        tasks = json.dumps([{"title": "Synthetic task one"}, {"title": "Synthetic task two"}])
        rows = [
            f"INSERT INTO task_plans (id, conversation, tasks, state, created_at, updated_at) VALUES "
            f"({sql_literal(self.plan)}, {sql_literal(self.conversation)}, {sql_literal(tasks)}::jsonb, 'open', {self.at(0)}, {self.at(0)});",
        ]
        for index, task in enumerate(self.tasks):
            rows.append(f"INSERT INTO todos (id, title, created_at, updated_at, origin, plan_id) VALUES ({sql_literal(task)}, "
                        f"'Synthetic task {index + 1}', {self.at(index)}, {self.at(index)}, {sql_literal(self.conversation)}, {sql_literal(self.plan)});")
        rows.append(f"INSERT INTO todo_agents (todo_id, machine_id, session_id, linked_at, title, agent) VALUES "
                    f"({sql_literal(self.tasks[0])}, {sql_literal(self.machine)}, 'drill-session-1', {self.at(2)}, 'Synthetic Agent', 'claude');")
        for index, (approval, kind) in enumerate(zip(self.pending_approvals, ("permission", "plan"))):
            rows.append(f"INSERT INTO interactions (id, machine_id, session_id, kind, payload, status, created_at, todo_id) VALUES "
                        f"({sql_literal(approval)}, {sql_literal(self.machine)}, 'drill-session-1', {sql_literal(kind)}, "
                        f"'{{\"text\": \"Synthetic approval\"}}'::jsonb, 'pending', {self.at(10 + index)}, {sql_literal(self.tasks[0])});")
        rows.append(f"INSERT INTO interactions (id, machine_id, session_id, kind, payload, status, resolution, created_at, resolved_at) VALUES "
                    f"({sql_literal(self.settled_approval)}, {sql_literal(self.machine)}, 'drill-session-1', 'permission', "
                    f"'{{\"text\": \"Synthetic approval\"}}'::jsonb, 'resolved', '{{\"allow\": true}}'::jsonb, {self.at(5)}, {self.at(6)});")
        for index, (item, kind) in enumerate(zip(self.outbox, ("push", "broadcast", "push"))):
            rows.append(f"INSERT INTO manager_outbox (id, kind, body, created_at) VALUES ({sql_literal(item)}, "
                        f"{sql_literal(kind)}, convert_to('synthetic outbox body {index + 1}', 'UTF8'), {self.at(20 + index)});")
        rows.append(f"INSERT INTO sealing_key (fingerprint, rotated_at) VALUES ({sql_literal(sealing_fingerprint)}, {self.at(0)});")
        return "\\set ON_ERROR_STOP on\nBEGIN;\n" + "\n".join(rows) + "\nCOMMIT;\n"

    def hub_sql(self):
        rows = []
        for index, state in enumerate(("pending", "unknown", "done")):
            result = "'{\"ok\": true}'::jsonb" if state == "done" else "NULL"
            rows.append(f"INSERT INTO client_ops (id, device_id, kind, target, digest, state, result, created_at, updated_at) VALUES "
                        f"({sql_literal(self.client_ops[state])}, 'drill-device', 'send', '{{\"to\": \"drill-session-1\"}}'::jsonb, "
                        f"'drill-digest-{index + 1}', {sql_literal(state)}, {result}, {self.at(30 + index)}, {self.at(30 + index)});")
        return "\\set ON_ERROR_STOP on\nBEGIN;\n" + "\n".join(rows) + "\nCOMMIT;\n"

    def disaster_sql(self, part):
        """What a lost or damaged install looks like before restore: work settled, links dropped, outboxes drained."""
        if part == "manager":
            return ("\\set ON_ERROR_STOP on\nBEGIN;\n"
                    "UPDATE interactions SET status = 'cancelled', resolved_at = now();\n"
                    "DELETE FROM manager_outbox;\nDELETE FROM todo_agents;\nUPDATE todos SET done_at = now();\n"
                    "UPDATE task_plans SET state = 'done';\nCOMMIT;\n")
        return ("\\set ON_ERROR_STOP on\nBEGIN;\nUPDATE client_ops SET state = 'done', updated_at = now();\n"
                f"INSERT INTO handed_over (manager, id) VALUES ('assistant', {sql_literal(self.outbox[0])});\nCOMMIT;\n")


def snapshot_sql(part):
    tables = MANAGER_TABLES if part == "manager" else HUB_TABLES
    fields = ["'read_only', current_setting('transaction_read_only')",
              "'schema', (SELECT coalesce(json_agg(json_build_array(version, name, part) ORDER BY version, name, part), '[]') FROM schema_migrations)",
              # A hub, manager or anything else connected to this database would show here: the drill starts
              # nothing, and its own connections (one at a time, the last perhaps still closing) say who they are.
              "'other_sessions', (SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() "
              f"AND backend_type = 'client backend' AND pid <> pg_backend_pid() AND application_name IS DISTINCT FROM '{INSPECTOR}')"]
    for table, order in tables.items():
        fields.append(f"'{table}', (SELECT coalesce(json_agg(to_jsonb(t) ORDER BY {order}), '[]') FROM {table} t)")
    return "SELECT json_build_object(" + ", ".join(fields) + ");\n"


def guarded_write_sql(part, fixture):
    if part == "manager":
        statement = (f"UPDATE interactions SET status = 'resolved', resolved_at = now() WHERE id = "
                     f"{sql_literal(fixture.pending_approvals[0])};")
    else:
        statement = f"UPDATE client_ops SET state = 'done' WHERE id = {sql_literal(fixture.client_ops['pending'])};"
    return "\\set VERBOSITY verbose\n\\set ON_ERROR_STOP on\n" + statement + "\n"


def rows(snapshot):
    """A snapshot's content: schema and tables, without what describes the inspecting connection."""
    return {key: value for key, value in snapshot.items() if key not in ("read_only", "other_sessions")}


def state_counts(snapshot):
    counts = {}
    for table, key in (("interactions", "status"), ("client_ops", "state"), ("manager_outbox", "kind")):
        if table in snapshot:
            values = {}
            for row in snapshot[table]:
                values[row[key]] = values.get(row[key], 0) + 1
            counts[table] = dict(sorted(values.items()))
    for table in ("todos", "todo_agents", "task_plans", "handed_over"):
        if table in snapshot:
            counts[table] = len(snapshot[table])
    return counts


def differing(before, after, table):
    """Where a table changed, by column name and row position only: values are never reported."""
    old, new = before.get(table, []), after.get(table, [])
    if old == new:
        return None
    if len(old) != len(new):
        return f"{table}: {len(old)} row(s) before, {len(new)} after"
    columns = sorted({column for a, b in zip(old, new) for column in set(a) | set(b) if a.get(column) != b.get(column)})
    return f"{table}: column(s) {', '.join(columns)} changed"


def semantic_checks(snapshot, fixture):
    """The work in flight is still in flight: what the comparison with the backed-up state means, stated directly."""
    problems = []
    approvals = {row["id"]: row for row in snapshot.get("interactions", [])}
    for approval in fixture.pending_approvals:
        row = approvals.get(approval)
        if row is None:
            problems.append("a pending approval is missing")
        elif (row["status"] != "pending" or row.get("resolution") is not None or row.get("resolved_at") is not None
              or row.get("user_notified_at") is not None or row.get("auto_authorized_at") is not None):
            problems.append("a pending approval was resolved, cancelled, expired, authorized or notified")
    todos = {row["id"]: row for row in snapshot.get("todos", [])}
    for task in fixture.tasks:
        if task not in todos or todos[task].get("done_at") is not None:
            problems.append("a task is missing or was closed")
    links = [row for row in snapshot.get("todo_agents", []) if row.get("released_at") is None]
    if [row["todo_id"] for row in links] != fixture.tasks[:1]:
        problems.append("a task's Agent link is missing or was released")
    if [row["id"] for row in snapshot.get("manager_outbox", [])] != fixture.outbox:
        problems.append("the manager outbox lost, gained or reordered items")
    ops = snapshot.get("client_ops", [])
    if [(row["id"], row["state"]) for row in ops] != [(fixture.client_ops[s], s) for s in ("pending", "unknown", "done")]:
        problems.append("client operations changed state, order or ids")
    if snapshot.get("handed_over"):
        problems.append("the hub recorded an outbox hand-over")
    return problems


# ---------------------------------------------------------------------------------------------------------------------
# The private cluster


class Cluster:
    """A PostgreSQL cluster in the drill directory that accepts connections on a Unix socket only (no TCP listener),
    authenticated by a password made for this run."""

    user = "drill"

    def __init__(self, bin_dir, root, processes, deadline):
        self.bin, self.root, self.processes, self.deadline = bin_dir, root, processes, deadline
        self.data, self.socket = root / "pgdata", root / "s"
        self.password = secrets.token_hex(24)
        self.server = None

    def env(self, read_only=False):
        env = minimal_environment(PATH=f"{self.bin}{os.pathsep}{os.defpath}", HOME=str(self.root / "home"),
                                  PGPASSWORD=self.password, PGPASSFILE=os.devnull, PGSERVICEFILE=os.devnull,
                                  PGSYSCONFDIR=str(self.root / "home"), PGCONNECT_TIMEOUT="10", PGAPPNAME=INSPECTOR)
        if read_only:
            env["PGOPTIONS"] = "-c default_transaction_read_only=on"
        return env

    def url(self, database):
        return f"postgres://{self.user}:{self.password}@/{database}?host={quote(str(self.socket), safe='')}&sslmode=disable"

    def start(self):
        self.socket.mkdir(mode=0o700)
        if len(str(self.socket)) > 90:
            raise DrillFailure("the temporary directory's path is too long for a Unix socket; set TMPDIR to a shorter one")
        pwfile = self.root / "home" / "pw"
        pwfile.write_text(self.password + "\n")
        pwfile.chmod(0o600)
        code, out, err = self.processes.run(
            [str(self.bin / "initdb"), "-D", str(self.data), "-U", self.user, "--pwfile", str(pwfile), "-E", "UTF8",
             "--no-locale", "--auth-local=scram-sha-256", "--auth-host=reject", "--no-sync"],
            env=self.env(), timeout=self.deadline.remaining(120))
        pwfile.unlink()
        if code != 0:
            raise DrillFailure("initdb failed: " + (err or out).strip()[-400:])
        self.log = open(self.root / "postgres.log", "wb")
        self.server = self.processes.spawn(
            [str(self.bin / "postgres"), "-D", str(self.data), "-c", "listen_addresses=", "-c",
             f"unix_socket_directories={self.socket}", "-c", "unix_socket_permissions=0700", "-c", "fsync=off",
             "-c", "full_page_writes=off", "-c", "synchronous_commit=off", "-c", "max_connections=40"],
            env=self.env(), log=self.log)
        wait_until = self.deadline.remaining(60)
        started = time.monotonic()
        while True:
            code, _, _ = self.processes.run([str(self.bin / "pg_isready"), "-q", "-h", str(self.socket), "-d", "postgres"],
                                            env=self.env(), timeout=self.deadline.remaining(10))
            if code == 0:
                return
            if self.server.poll() is not None:
                raise DrillFailure("the private PostgreSQL server exited during start")
            if time.monotonic() - started > wait_until:
                raise DrillTimeout()
            time.sleep(0.2)

    def psql(self, database, sql, read_only=False, check=True):
        code, out, err = self.processes.run(
            [str(self.bin / "psql"), "-X", "-q", "-At", "-v", "ON_ERROR_STOP=1", "-h", str(self.socket), "-U", self.user,
             "-d", database, "-f", "-"], env=self.env(read_only), timeout=self.deadline.remaining(300), stdin=sql)
        if check and code != 0:
            raise DrillFailure(f"a query on the {database_role(database)} database failed: " + err.strip()[-400:])
        return code, out, err

    def stop(self):
        if self.server is None:
            return True
        # SIGINT is PostgreSQL's fast shutdown; the group is killed if it does not finish.
        stopped = self.processes.stop(self.server, first=signal.SIGINT, grace=20)
        self.log.close()
        return stopped


def database_role(name):
    return "manager" if name.endswith("_manager") else "hub" if name.startswith("drill_") else "maintenance"


# ---------------------------------------------------------------------------------------------------------------------
# The drill


class Drill:
    def __init__(self, args, root, processes, deadline, cluster=None, now=None):
        self.args, self.root, self.processes, self.deadline = args, root, processes, deadline
        self.cluster = cluster or Cluster(args.pg_bin, root, processes, deadline)
        suffix = secrets.token_hex(5)
        self.hub_db, self.manager_db = f"drill_{suffix}", f"drill_{suffix}_manager"
        self.fixture = Fixture(now or time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()))
        self.source, self.target = root / "source" / "runtime", root / "target" / "runtime"
        self.bundle = root / "bundles" / "install.backup"
        self.personad = root / "bin" / "personad"
        self.secrets = [self.cluster.password]
        self.report = {"drill": "persona whole-install restore drill", "verdict": "FAIL", "services_started": False,
                       "personad_commands": [], "phases": [], "checks": [], "injected_fault": args.inject}

    # -- reporting ------------------------------------------------------------------------------------------------

    def phase(self, name, action):
        started = time.monotonic()
        entry = {"phase": name, "status": "running"}
        self.report["phases"].append(entry)
        try:
            result = action()
            entry["status"] = "ok"
            return result
        except DrillTimeout:
            entry["status"] = "timeout"
            raise
        except BaseException:
            entry["status"] = "failed"
            raise
        finally:
            entry["seconds"] = round(time.monotonic() - started, 3)

    def check(self, name, ok, detail=None):
        entry = {"check": name, "ok": bool(ok)}
        if detail:
            entry["detail"] = self.redact(detail)
        self.report["checks"].append(entry)
        return ok

    def redact(self, text):
        return redact(text, self.secrets, self.root)

    # -- steps ------------------------------------------------------------------------------------------------------

    def personad_run(self, *args):
        if tuple(args[:2]) not in PERSONAD_COMMANDS:
            raise DrillFailure("refusing a personad command outside backup create/verify/restore")
        self.report["personad_commands"].append(" ".join(args[:2]))
        # Only what finds the PostgreSQL clients: the databases come from the runtime's (or bundle's) hub.json, as on
        # a real machine, and the default runtime directory is one that does not exist.
        env = minimal_environment(PATH=f"{self.args.pg_bin}{os.pathsep}{os.defpath}", HOME=str(self.root / "home"),
                                  PERSONA_HOME=str(self.root / "home" / "absent"), TMPDIR=str(self.root / "tmp"),
                                  PGPASSFILE=os.devnull,
                                  PGSERVICEFILE=os.devnull, PGSYSCONFDIR=str(self.root / "home"))
        code, out, err = self.execute_personad(list(args), env)
        if code != 0:
            raise DrillFailure(f"personad {' '.join(args[:2])} exited {code}: " + self.redact((err or out).strip()[-600:]))
        return out

    def execute_personad(self, args, env):
        return self.processes.run([str(self.personad), *args], env=env, timeout=self.deadline.remaining(600),
                                  cwd=str(self.root))

    def write_private(self, path, data):
        path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        if isinstance(data, str):
            data = data.encode()
        path.write_bytes(data)
        path.chmod(0o600)

    def build_install(self):
        for name in ("home", "bin", "bundles", "tmp"):  # tmp: personad's verify and restore staging
            (self.root / name).mkdir(mode=0o700)
        shutil.copyfile(self.args.personad, self.personad)
        self.personad.chmod(0o700)
        self.cluster.start()
        for name in (self.hub_db, self.manager_db):
            self.cluster.psql("postgres", f"CREATE DATABASE {name};\n")
        self.cluster.psql(self.hub_db, migration_script("hub", migration_files("hub")))
        self.cluster.psql(self.manager_db, migration_script("manager", migration_files("manager")))
        hub_key, manager_key = secrets.token_bytes(SECRET_KEY_SIZE), secrets.token_bytes(SECRET_KEY_SIZE)
        token = secrets.token_hex(24)
        self.secrets += [token, hub_key.hex(), manager_key.hex()]
        self.write_private(self.source / "hub.json", json.dumps(
            {"database_url": self.cluster.url(self.hub_db), "token": token, "update": {"url": "off"},
             "node_packages": {"url": "off"}, "auto_update_nodes": False, "machines": []}, indent=2))
        self.write_private(self.source / "secret.key", hub_key)
        assistant = self.source / "managers" / "assistant"
        self.write_private(assistant / "manager.json", json.dumps({"database_name": self.manager_db}))
        self.write_private(assistant / "secret.key", manager_key)
        for relative, text in (("index.md", "# Synthetic knowledge base\n"), ("notes/drill.md", "Synthetic note.\n"),
                               ("notes/deep/fact.md", "Synthetic fact.\n")):
            self.write_private(self.source / "data" / relative, text)
        self.cluster.psql(self.manager_db, self.fixture.manager_sql(key_fingerprint(manager_key)))
        self.cluster.psql(self.hub_db, self.fixture.hub_sql())
        self.keys = {"hub": key_fingerprint(hub_key), "manager": key_fingerprint(manager_key)}
        self.kb_digest = tree_digest(self.source / "data")
        self.before = self.snapshots()

    def snapshots(self, read_only=True):
        out = {}
        for part, database in (("hub", self.hub_db), ("manager", self.manager_db)):
            _, text, _ = self.cluster.psql(database, snapshot_sql(part), read_only=read_only)
            try:
                out[part] = json.loads(text.strip().splitlines()[-1])
            except (ValueError, IndexError):
                raise DrillFailure(f"the {part} snapshot could not be read") from None
        return out

    def backup_create(self):
        self.personad_run("backup", "create", "--writes-quiesced", "--home", str(self.source), "--output", str(self.bundle))
        with tarfile.open(self.bundle) as archive:
            member = archive.extractfile("manifest.json")
            if member is None:
                raise DrillFailure("the bundle has no manifest")
            self.manifest = json.load(member)
        self.report["data_point"] = self.manifest.get("created_at")
        self.report["seeded_at"] = self.fixture.t0

    def backup_verify(self, home):
        self.personad_run("backup", "verify", "--home", str(home), "--bundle", str(self.bundle))

    def disaster(self):
        for part, database in (("hub", self.hub_db), ("manager", self.manager_db)):
            self.cluster.psql(database, self.fixture.disaster_sql(part))
        damaged = self.snapshots()
        if not self.check("disaster changed both databases",
                          all(rows(damaged[part]) != rows(self.before[part]) for part in ("hub", "manager"))):
            raise DrillFailure("the injected disaster did not change the databases")
        shutil.rmtree(self.source)  # the runtime directory is lost with the machine
        self.target.mkdir(parents=True, mode=0o700)  # an empty one on the replacement machine
        if self.args.inject == "restore-fails":
            self.corrupt_bundle()

    def corrupt_bundle(self):
        """Damage the hub dump inside the bundle while its manifest still names the original checksum."""
        with tarfile.open(self.bundle) as archive:
            member = archive.getmember("db/hub.dump")
            offset = member.offset_data + member.size // 2
        with self.bundle.open("r+b") as bundle:
            bundle.seek(offset)
            byte = bundle.read(1)
            bundle.seek(offset)
            bundle.write(bytes([byte[0] ^ 0xFF]))

    def restore(self):
        self.personad_run("backup", "restore", "--writes-quiesced", "--home", str(self.target), "--bundle", str(self.bundle))

    def inject_after_restore(self):
        fault = self.args.inject
        if fault == "kb-drift":
            path = self.target / "data" / "notes" / "drill.md"
            path.write_text(path.read_text() + "drift\n")
        elif fault == "approval-resolved":
            self.cluster.psql(self.manager_db, f"UPDATE interactions SET status = 'resolved', resolved_at = now() "
                                               f"WHERE id = {sql_literal(self.fixture.pending_approvals[0])};\n")
        elif fault == "outbox-settled":
            self.cluster.psql(self.manager_db, f"DELETE FROM manager_outbox WHERE id = {sql_literal(self.fixture.outbox[0])};\n")
            self.cluster.psql(self.hub_db, f"UPDATE client_ops SET state = 'done' WHERE id = "
                                           f"{sql_literal(self.fixture.client_ops['pending'])};\n")
        elif fault == "timeout":
            self.cluster.psql("postgres", f"SELECT pg_sleep({int(self.deadline.remaining()) + 60});\n")

    def offline_checks(self):
        problems = []
        after = self.snapshots(read_only=True)
        for part in ("hub", "manager"):
            if not self.check(f"{part} connection is read-only", after[part]["read_only"] == "on"):
                problems.append(f"the {part} inspection connection is not read-only")
            if not self.check(f"no hub or manager connected to the restored {part} database", after[part]["other_sessions"] == 0):
                problems.append(f"something is connected to the restored {part} database")
            same = after[part]["schema"] == self.before[part]["schema"]
            if not self.check(f"{part} schema identity", same and len(after[part]["schema"]) == len(migration_files(part))):
                problems.append(f"the restored {part} schema differs")
        self.report["restored_counts"] = {part: state_counts(after[part]) for part in ("hub", "manager")}

        hub_key = (self.target / "secret.key").read_bytes() if (self.target / "secret.key").is_file() else b""
        manager_key_path = self.target / "managers" / "assistant" / "secret.key"
        manager_key = manager_key_path.read_bytes() if manager_key_path.is_file() else b""
        keys_ok = (key_fingerprint(hub_key) == self.keys["hub"] == self.manifest.get("hub_key_fingerprint")
                   and key_fingerprint(manager_key) == self.keys["manager"] == self.manifest.get("manager_key_fingerprint"))
        if not self.check("restored keys match the manifest", keys_ok):
            problems.append("the restored keys differ")
        sealing = after["manager"].get("sealing_key") or [{}]
        if not self.check("sealed secrets name the restored manager key", sealing[0].get("fingerprint") == key_fingerprint(manager_key)):
            problems.append("the manager database is sealed with another key")

        for part, tables in (("hub", HUB_TABLES), ("manager", MANAGER_TABLES)):
            for table in tables:
                detail = differing(self.before[part], after[part], table)
                if not self.check(f"{part} {table} unchanged", detail is None, detail):
                    problems.append(detail)
        merged = {**after["hub"], **after["manager"]}
        semantic = semantic_checks(merged, self.fixture)
        self.check("tasks, Agent links, pending approvals and outboxes still in flight", not semantic, "; ".join(semantic))
        problems += semantic

        digest = tree_digest(self.target / "data")
        if not self.check("knowledge base digest", digest == self.kb_digest):
            problems.append("the restored knowledge base differs")
        try:
            hub_config = json.loads((self.target / "hub.json").read_text())
            manager_config = json.loads((self.target / "managers" / "assistant" / "manager.json").read_text())
            config_ok = (hub_config.get("database_url") == self.cluster.url(self.hub_db)
                         and manager_config.get("database_name") == self.manager_db)
        except (OSError, ValueError):
            config_ok = False
        if not self.check("restored configuration parses and names the restored databases", config_ok):
            problems.append("the restored configuration does not parse or names other databases")

        for part, database in (("hub", self.hub_db), ("manager", self.manager_db)):
            read_only = self.args.inject != "write-allowed"
            code, _, err = self.cluster.psql(database, guarded_write_sql(part, self.fixture), read_only=read_only, check=False)
            refused = code != 0 and READ_ONLY_SQLSTATE in err
            if not self.check(f"guarded {part} write refused by the read-only connection", refused):
                problems.append(f"a write to the restored {part} database was not refused")
        final = self.snapshots(read_only=True)
        unchanged = all(rows(final[part]) == rows(after[part]) for part in ("hub", "manager"))
        if not self.check("restored rows unchanged after the guarded writes", unchanged):
            problems.append("the restored rows changed during inspection")
        if problems:
            raise DrillFailure("offline checks failed: " + "; ".join(problems))

    def run(self):
        self.phase("build synthetic install", self.build_install)
        self.report["seeded_counts"] = {part: state_counts(self.before[part]) for part in ("hub", "manager")}
        self.phase("backup create", self.backup_create)
        self.phase("backup verify", lambda: self.backup_verify(self.source))
        self.phase("disaster", self.disaster)
        self.phase("backup restore", self.restore)
        self.phase("backup verify after restore", lambda: self.backup_verify(self.target))
        if self.args.inject and self.args.inject != "restore-fails":
            self.phase("inject fault", self.inject_after_restore)
        self.phase("offline read-only checks", self.offline_checks)

    def cleanup(self):
        result = {}
        result["cluster_stopped"] = self.cluster.stop()
        result["processes_stopped"] = self.processes.stop_all()
        shutil.rmtree(self.root, ignore_errors=True)
        result["directory_removed"] = not self.root.exists()
        return result


SIGNALS = (signal.SIGINT, signal.SIGTERM, signal.SIGHUP)


def install_signal_handlers():
    def interrupted(signum, frame):
        # Only the first signal interrupts; cleanup then runs with signals only recorded (quiet_signals).
        for other in SIGNALS:
            signal.signal(other, signal.SIG_IGN)
        raise Interrupted(signum)
    return {signum: signal.signal(signum, interrupted) for signum in SIGNALS}


def quiet_signals(received):
    """From here on a signal is noted, never raised: nothing may cut the cleanup short."""
    for signum in SIGNALS:
        signal.signal(signum, lambda number, frame: received.append(number))


def main(argv=None, out=sys.stdout, drill_factory=Drill):
    started = time.monotonic()
    try:
        args = parse_arguments(sys.argv[1:] if argv is None else argv)
    except UsageError as error:
        print(f"REFUSED backup restore drill: {error}", file=out, flush=True)
        return EXIT_USAGE
    previous = install_signal_handlers()
    try:
        return run_drill(args, out, drill_factory, started)
    finally:
        for signum, handler in previous.items():
            signal.signal(signum, handler)


def run_drill(args, out, drill_factory, started):
    code, verdict, reasons, drill, root, received = EXIT_FAIL, "FAIL", [], None, None, []
    try:
        try:
            root = Path(tempfile.mkdtemp(prefix="persona-drill-"))
            root.chmod(0o700)
            drill = drill_factory(args, root, Processes(), Deadline(args.timeout))
            drill.run()
            failed = [entry["check"] for entry in drill.report["checks"] if not entry["ok"]]
            if failed:
                raise DrillFailure("failed checks: " + ", ".join(failed))
            code, verdict = EXIT_PASS, "PASS"
        except DrillTimeout:
            code, verdict = EXIT_TIMEOUT, "TIMEOUT"
            reasons.append("the drill exceeded --timeout")
        except DrillFailure as error:
            reasons.append(str(error))
        except Interrupted as error:
            code, verdict = 128 + error.signum, "INTERRUPTED"
            reasons.append(str(error))
        except Exception as error:  # an unexpected error still ends in cleanup and a verdict
            reasons.append(f"{type(error).__name__}: {error}")
        finally:
            quiet_signals(received)
    except Interrupted as late:  # the first signal came while an outcome above was being taken down
        received.append(late.signum)
    quiet_signals(received)  # from here on signals are only noted: cleanup and the verdict always happen

    try:
        if drill is not None:
            cleanup = drill.cleanup()
        else:
            if root is not None:
                shutil.rmtree(root, ignore_errors=True)
            cleanup = {"directory_removed": root is None or not root.exists()}
    except Exception as error:
        cleanup = {"cleanup_completed": False}
        reasons.append(f"cleanup failed: {type(error).__name__}")

    def fail(reason):
        nonlocal code, verdict
        if reason not in reasons:
            reasons.append(reason)
        if code == EXIT_PASS:
            code, verdict = EXIT_FAIL, "FAIL"
    if received and verdict != "INTERRUPTED":
        code, verdict = 128 + received[0], "INTERRUPTED"
        reasons.append(signal.Signals(received[0]).name + " during cleanup")
    if not all(cleanup.values()):
        fail("cleanup incomplete")
    secrets_list = drill.secrets if drill else []
    report = drill.report if drill else {"drill": "persona whole-install restore drill", "services_started": False}

    def reason_text():
        text = redact("; ".join(reasons), secrets_list, root)
        return text if not leaked(text, secrets_list, root) else "a reason was withheld: it would have named a secret"

    def render():
        report.update(verdict=verdict, reason=reason_text() or None, cleanup=cleanup,
                      seconds=round(time.monotonic() - started, 3))
        text = json.dumps(report, indent=2, sort_keys=True)
        if leaked(text, secrets_list, root):
            fail("the report would have contained a secret or a path of the drill; withheld")
            text = json.dumps({"verdict": verdict, "reason": reason_text(), "cleanup": cleanup}, indent=2, sort_keys=True)
        return text
    text = render()
    if args.report:
        try:
            fd = os.open(args.report, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        except OSError as error:
            fail(f"the report file could not be written ({error.strerror})")
            text = render()
        else:
            with os.fdopen(fd, "w") as output:
                output.write(text + "\n")
    print(text, file=out)
    shown = reason_text()
    print(f"{verdict} backup restore drill ({time.monotonic() - started:.1f}s, exit {code})" + (f": {shown}" if shown else ""),
          file=out, flush=True)
    return code


if __name__ == "__main__":
    sys.exit(main())
