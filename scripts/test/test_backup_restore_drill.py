"""The isolated restore drill (backup_restore_drill.py): input safety, read-only checks, redaction, timeouts and cleanup.

Most tests drive the real drill code against an in-memory cluster and a fake personad that keep the real commands'
semantics (a bundle with checksums, a restore that replaces databases and runtime files). The real run, with a static
personad and PostgreSQL binaries, happens only when both are provided explicitly:

    PERSONA_DRILL_PERSONAD=<static linux personad> PERSONA_DRILL_PG_BIN=<postgres bin dir> \
        python3 -m unittest test_backup_restore_drill
"""
import copy
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import signal
import struct
import subprocess
import sys
import tarfile
import tempfile
import time
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import backup_restore_drill as drill  # noqa: E402


def elf(interpreter=False, osabi=0):
    """A minimal 64-bit little-endian ELF header with one program header (PT_LOAD, or PT_INTERP)."""
    header = bytearray(64)
    header[:4] = b"\x7fELF"
    header[4], header[5], header[6], header[7] = 2, 1, 1, osabi
    struct.pack_into("<HHI", header, 16, 2, 62, 1)  # ET_EXEC, x86-64, version
    struct.pack_into("<Q", header, 32, 64)  # e_phoff
    struct.pack_into("<HH", header, 54, 56, 1)  # e_phentsize, e_phnum
    program = bytearray(56)
    struct.pack_into("<I", program, 0, 3 if interpreter else 1)
    return bytes(header + program)


class Inputs:
    """A synthetic static personad and PostgreSQL bin directory for argument parsing."""

    def __init__(self, test):
        directory = Path(tempfile.mkdtemp(prefix="drill-inputs-"))
        test.addCleanup(shutil.rmtree, directory, True)
        self.directory = directory
        self.personad = directory / "personad"
        self.personad.write_bytes(elf())
        self.personad.chmod(0o755)
        self.pg_bin = directory / "pg"
        self.pg_bin.mkdir()
        for name in drill.PG_PROGRAMS:
            program = self.pg_bin / name
            program.write_text("#!/bin/sh\nexit 1\n")
            program.chmod(0o755)

    def argv(self, *extra):
        return ["--execute", "--personad", str(self.personad), "--pg-bin", str(self.pg_bin), *extra]


# ---------------------------------------------------------------------------------------------------------------------
# An in-memory cluster and personad with the real commands' semantics


def seeded_manager(fixture, fingerprint):
    return {
        "todos": [{"id": task, "done_at": None, "origin": fixture.conversation, "plan_id": fixture.plan} for task in fixture.tasks],
        "todo_agents": [{"todo_id": fixture.tasks[0], "machine_id": fixture.machine, "session_id": "drill-session-1",
                         "released_at": None}],
        "task_plans": [{"id": fixture.plan, "state": "open"}],
        "interactions": [{"id": fixture.settled_approval, "status": "resolved", "resolution": {"allow": True},
                          "resolved_at": "t", "user_notified_at": None, "auto_authorized_at": None}] +
                        [{"id": approval, "status": "pending", "resolution": None, "resolved_at": None,
                          "user_notified_at": None, "auto_authorized_at": None} for approval in fixture.pending_approvals],
        "manager_outbox": [{"seq": index + 1, "id": item, "kind": "push", "body": "\\x00"} for index, item in enumerate(fixture.outbox)],
        "sealing_key": [{"one": True, "fingerprint": fingerprint}],
    }


def seeded_hub(fixture):
    return {"client_ops": [{"id": fixture.client_ops[state], "state": state} for state in ("pending", "unknown", "done")],
            "handed_over": []}


class FakeCluster:
    password = "fake-cluster-password-5f1c9a2e"

    def __init__(self):
        self.databases, self.schema, self.started, self.stopped, self.stop_result = {}, {}, False, False, True
        self.fixture = None

    def env(self, read_only=False):
        return {"PGPASSWORD": self.password}

    def url(self, database):
        return f"postgres://drill:{self.password}@/{database}?host=%2Fsocket&sslmode=disable"

    def start(self):
        self.started = True

    def stop(self):
        self.stopped = True
        return self.stop_result

    def part(self, database):
        return "manager" if database.endswith("_manager") else "hub"

    def psql(self, database, sql, read_only=False, check=True):
        tables = self.databases.get(database)
        if sql.startswith("CREATE DATABASE"):
            self.databases[sql.split()[2].rstrip(";")] = {}
            return 0, "", ""
        if "pg_sleep" in sql:
            raise drill.DrillTimeout()
        if "INSERT INTO schema_migrations" in sql:
            self.schema[database] = [[int(v), n, p] for v, n, p in re.findall(r"VALUES \((\d+), '([^']+)', '(\w+)'\)", sql)]
            return 0, "", ""
        if "INSERT INTO sealing_key" in sql:
            tables.update(seeded_manager(self.fixture, re.search(r"INSERT INTO sealing_key .*?VALUES \('([0-9a-f]+)'", sql).group(1)))
            return 0, "", ""
        if "INSERT INTO client_ops" in sql:
            tables.update(seeded_hub(self.fixture))
            return 0, "", ""
        if sql.startswith("SELECT json_build_object("):
            out = {"read_only": "on" if read_only else "off", "schema": self.schema.get(database, []), "other_sessions": 0}
            names = drill.MANAGER_TABLES if self.part(database) == "manager" else drill.HUB_TABLES
            out.update({name: copy.deepcopy(tables.get(name, [])) for name in names})
            return 0, json.dumps(out) + "\n", ""
        if "VERBOSITY verbose" in sql and read_only:
            return 3, "", "psql:<stdin>:3: ERROR:  25006: cannot execute UPDATE in a read-only transaction"
        if read_only:
            raise AssertionError("an unexpected statement on a read-only connection")
        self.write(tables, sql)
        return 0, "", ""

    def write(self, tables, sql):
        def matching(table):
            ident = re.search(r"WHERE id = '([^']+)'", sql)
            return [row for row in tables[table] if ident is None or row["id"] == ident.group(1)]
        if "UPDATE interactions SET status = 'cancelled'" in sql or "UPDATE interactions SET status = 'resolved'" in sql:
            for row in matching("interactions"):
                row.update(status="resolved", resolved_at="now")
        if "DELETE FROM manager_outbox" in sql:
            for row in matching("manager_outbox"):
                tables["manager_outbox"].remove(row)
        if "DELETE FROM todo_agents" in sql:
            tables["todo_agents"] = []
        if "UPDATE todos SET done_at" in sql:
            for row in tables["todos"]:
                row["done_at"] = "now"
        if "UPDATE client_ops SET state = 'done'" in sql:
            for row in matching("client_ops"):
                row["state"] = "done"
        if "INSERT INTO handed_over" in sql:
            tables["handed_over"].append({"manager": "assistant", "id": "x"})


class FakeDrill(drill.Drill):
    """The real drill with the fake cluster and a personad that keeps backup create/verify/restore's semantics."""

    def __init__(self, args, root, processes, deadline, **kwargs):
        super().__init__(args, root, processes, deadline, cluster=FakeCluster(), **kwargs)
        self.cluster.fixture = self.fixture
        self.environments = []

    def execute_personad(self, args, env):
        self.environments.append(dict(env))
        command, values = args[1], [value for value in args[2:] if value != "--writes-quiesced"]
        options = dict(zip(values[::2], values[1::2]))
        if command == "create":
            return self.create(Path(options["--home"]), Path(options["--output"]))
        problem = self.damaged()
        if problem:
            return 1, "", problem
        if command == "restore":
            self.restore_bundle(Path(options["--home"]))
        return 0, "verified\n", ""

    def create(self, home, output):
        dumps = {part: json.dumps(self.cluster.databases[name]).encode()
                 for part, name in (("hub", self.hub_db), ("manager", self.manager_db))}
        manifest = {"created_at": "2026-01-01T00:00:00Z",
                    "hub_key_fingerprint": drill.key_fingerprint((home / "secret.key").read_bytes()),
                    "manager_key_fingerprint": drill.key_fingerprint((home / "managers/assistant/secret.key").read_bytes()),
                    "files": {f"db/{part}.dump": hashlib.sha256(data).hexdigest() for part, data in dumps.items()}}
        with tarfile.open(output, "w") as archive:
            def add(name, data):
                info = tarfile.TarInfo(name)
                info.size = len(data)
                archive.addfile(info, io.BytesIO(data))
            add("manifest.json", json.dumps(manifest).encode())
            for part, data in dumps.items():
                add(f"db/{part}.dump", data)
            archive.add(home, arcname="runtime")
        return 0, "created and verified\n", ""

    def damaged(self):
        with tarfile.open(self.bundle) as archive:
            manifest = json.load(archive.extractfile("manifest.json"))
            for name, digest in manifest["files"].items():
                if hashlib.sha256(archive.extractfile(name).read()).hexdigest() != digest:
                    return f"SHA-256 mismatch for {name} at {self.cluster.url(self.hub_db)}"
        return None

    def restore_bundle(self, home):
        with tarfile.open(self.bundle) as archive:
            for part, name in (("hub", self.hub_db), ("manager", self.manager_db)):
                self.cluster.databases[name] = json.loads(archive.extractfile(f"db/{part}.dump").read())
            staging = self.root / "tmp" / "extract"
            archive.extractall(staging, **({"filter": "data"} if hasattr(tarfile, "data_filter") else {}))
        for item in (staging / "runtime").iterdir():
            destination = home / item.name
            shutil.copytree(item, destination) if item.is_dir() else shutil.copy2(item, destination)


class Run:
    def __init__(self, test, *extra, factory=FakeDrill):
        self.inputs = Inputs(test)
        self.drills = []

        def make(args, root, processes, deadline):
            made = factory(args, root, processes, deadline)
            self.drills.append(made)
            return made
        out = io.StringIO()
        self.code = drill.main(self.inputs.argv(*extra), out=out, drill_factory=make)
        self.output = out.getvalue()
        self.verdict_line = self.output.strip().splitlines()[-1]
        self.report = json.loads(self.output[:self.output.rindex("}") + 1])
        self.drill = self.drills[0] if self.drills else None

    def phase(self, name):
        return next(entry for entry in self.report["phases"] if entry["phase"] == name)

    def failed_checks(self):
        return [entry["check"] for entry in self.report["checks"] if not entry["ok"]]


# ---------------------------------------------------------------------------------------------------------------------


class ArgumentSafetyTests(unittest.TestCase):
    def test_nothing_runs_without_execute(self):
        inputs = Inputs(self)
        out = io.StringIO()
        with mock.patch.object(drill.tempfile, "mkdtemp", side_effect=AssertionError("must not create anything")):
            code = drill.main(inputs.argv()[1:], out=out, drill_factory=FakeDrill)
        self.assertEqual(code, drill.EXIT_USAGE)
        self.assertIn("REFUSED", out.getvalue())
        self.assertIn("--execute", out.getvalue())

    def test_production_inputs_are_refused_by_name(self):
        inputs = Inputs(self)
        for option in drill.FORBIDDEN_OPTIONS:
            for spelling in ([option, "postgres://user:pw@db.example.invalid/persona"], [option + "=/somewhere"]):
                with self.subTest(option=spelling[0]):
                    with self.assertRaisesRegex(drill.UsageError, "synthetic install"):
                        drill.parse_arguments(inputs.argv(*spelling))

    def test_abbreviated_options_are_not_accepted(self):
        inputs = Inputs(self)
        with self.assertRaises(SystemExit), mock.patch("sys.stderr", io.StringIO()):
            drill.parse_arguments(["--exec", "--personad", str(inputs.personad), "--pg-bin", str(inputs.pg_bin)])

    def test_personad_must_be_a_static_linux_executable(self):
        inputs = Inputs(self)
        self.assertEqual(drill.check_static_linux_binary(inputs.personad), inputs.personad)
        cases = {"dynamic": elf(interpreter=True), "script": b"#!/bin/sh\n", "other os": elf(osabi=9), "truncated": b"\x7fELF\x02"}
        for label, data in cases.items():
            with self.subTest(label):
                path = inputs.directory / label.replace(" ", "-")
                path.write_bytes(data)
                path.chmod(0o755)
                with self.assertRaises(drill.UsageError):
                    drill.check_static_linux_binary(path)
        not_executable = inputs.directory / "plain"
        not_executable.write_bytes(elf())
        not_executable.chmod(0o644)
        with self.assertRaises(drill.UsageError):
            drill.check_static_linux_binary(not_executable)

    def test_a_dynamically_linked_system_program_is_refused(self):
        program = Path("/bin/sh").resolve()
        with program.open("rb") as binary:
            if binary.read(4) != b"\x7fELF":
                self.skipTest("/bin/sh is not an ELF program here")
        try:
            drill.check_static_linux_binary(program)
        except drill.UsageError as error:
            self.assertIn("statically linked", str(error))
        else:
            self.skipTest("/bin/sh is statically linked here")

    def test_pg_bin_needs_every_program(self):
        inputs = Inputs(self)
        (inputs.pg_bin / "pg_restore").unlink()
        with self.assertRaisesRegex(drill.UsageError, "pg_restore"):
            drill.parse_arguments(inputs.argv())

    def test_timeout_and_report_limits(self):
        inputs = Inputs(self)
        for value in ("5", "7200"):
            with self.assertRaisesRegex(drill.UsageError, "--timeout"):
                drill.parse_arguments(inputs.argv("--timeout", value))
        existing = inputs.directory / "report.json"
        existing.write_text("{}")
        with self.assertRaisesRegex(drill.UsageError, "new file"):
            drill.parse_arguments(inputs.argv("--report", str(existing)))

    def test_only_backup_commands_reach_personad(self):
        inputs = Inputs(self)
        args = drill.parse_arguments(inputs.argv())
        root = Path(tempfile.mkdtemp(prefix="drill-allow-"))
        self.addCleanup(shutil.rmtree, root, True)
        made = FakeDrill(args, root, drill.Processes(), drill.Deadline(60))
        made.execute_personad = mock.Mock(side_effect=AssertionError("must not run"))
        for command in (("hub", "run"), ("hub",), ("assistant",), ("manager", "run"), ("node", "--attach"), ("backup", "drill")):
            with self.subTest(command=command), self.assertRaisesRegex(drill.DrillFailure, "refusing"):
                made.personad_run(*command)
        self.assertEqual(made.report["personad_commands"], [])


class HelperTests(unittest.TestCase):
    def test_key_fingerprint_matches_secretkey_fingerprint(self):
        # secretkey.Fingerprint(bytes 0..31), computed with the Go implementation.
        self.assertEqual(drill.key_fingerprint(bytes(range(32))), "577562a1d8d8b402fa2b69a7ca995e68")

    def test_tree_digest_follows_content_names_and_absence(self):
        root = Path(tempfile.mkdtemp(prefix="drill-digest-"))
        self.addCleanup(shutil.rmtree, root, True)
        (root / "a").mkdir()
        (root / "a" / "one.md").write_text("one")
        first = drill.tree_digest(root)
        self.assertEqual(drill.tree_digest(root), first)
        (root / "a" / "one.md").write_text("One")
        self.assertNotEqual(drill.tree_digest(root), first)
        (root / "a" / "one.md").write_text("one")
        self.assertEqual(drill.tree_digest(root), first)
        (root / "a" / "one.md").rename(root / "a" / "two.md")
        self.assertNotEqual(drill.tree_digest(root), first)
        self.assertIsNone(drill.tree_digest(root / "missing"))

    def test_redaction_removes_secrets_urls_and_the_drill_directory(self):
        text = "failed at postgres://drill:hunter2@/db?host=%2Fx with token abc123 in /tmp/persona-drill-x/source"
        out = drill.redact(text, ["hunter2", "abc123", ""], "/tmp/persona-drill-x")
        for secret in ("hunter2", "abc123", "postgres://", "/tmp/persona-drill-x"):
            self.assertNotIn(secret, out)
        self.assertTrue(drill.leaked(text, ["abc123"]))
        self.assertFalse(drill.leaked(out, ["hunter2", "abc123"]))

    def test_changes_are_reported_by_column_without_values(self):
        before = {"interactions": [{"id": "a", "status": "pending", "payload": "secret body"}]}
        after = {"interactions": [{"id": "a", "status": "resolved", "payload": "other secret body"}]}
        detail = drill.differing(before, after, "interactions")
        self.assertEqual(detail, "interactions: column(s) payload, status changed")
        self.assertIsNone(drill.differing(before, copy.deepcopy(before), "interactions"))
        self.assertEqual(drill.differing(before, {"interactions": []}, "interactions"), "interactions: 1 row(s) before, 0 after")

    def test_work_in_flight_checks(self):
        fixture = drill.Fixture("2026-01-01T00:00:00Z")
        good = {**seeded_manager(fixture, "f" * 32), **seeded_hub(fixture)}
        self.assertEqual(drill.semantic_checks(good, fixture), [])
        mutations = {
            "notified approval": lambda s: s["interactions"][1].update(user_notified_at="now"),
            "auto-authorized approval": lambda s: s["interactions"][1].update(auto_authorized_at="now"),
            "expired approval": lambda s: s["interactions"][2].update(status="expired"),
            "missing approval": lambda s: s["interactions"].pop(1),
            "closed task": lambda s: s["todos"][0].update(done_at="now"),
            "released link": lambda s: s["todo_agents"][0].update(released_at="now"),
            "reordered outbox": lambda s: s["manager_outbox"].reverse(),
            "drained outbox": lambda s: s["manager_outbox"].clear(),
            "acknowledged op": lambda s: s["client_ops"][0].update(state="done"),
            "new op id": lambda s: s["client_ops"][1].update(id="00000000-0000-0000-0000-000000000000"),
            "extra op": lambda s: s["client_ops"].append({"id": "x", "state": "pending"}),
            "hand-over": lambda s: s["handed_over"].append({"manager": "assistant", "id": "x"}),
        }
        for label, mutate in mutations.items():
            with self.subTest(label):
                snapshot = copy.deepcopy(good)
                mutate(snapshot)
                self.assertTrue(drill.semantic_checks(snapshot, fixture))

    def test_migration_script_applies_each_real_migration_once_in_order(self):
        for part in ("hub", "manager"):
            files = drill.migration_files(part)
            script = drill.migration_script(part, files)
            records = re.findall(r"INSERT INTO schema_migrations \(version, name, part\) VALUES \((\d+), '([^']+)', '(\w+)'\);", script)
            self.assertEqual([name for _, name, _ in records], [path.name for path in files])
            self.assertEqual({p for _, _, p in records}, {part})
            versions = [int(v) for v, _, _ in records]
            self.assertEqual(versions, sorted(versions))
            self.assertEqual(script.count("\nBEGIN;\n"), len(files))
            self.assertEqual(script.count("\nCOMMIT;\n"), len(files))
            self.assertIn("schema_migrations_version_part", script)

    def test_snapshots_read_every_table_and_say_whether_they_are_read_only(self):
        for part, tables in (("hub", drill.HUB_TABLES), ("manager", drill.MANAGER_TABLES)):
            sql = drill.snapshot_sql(part)
            self.assertIn("current_setting('transaction_read_only')", sql)
            self.assertIn("backend_type = 'client backend'", sql)
            for table in tables:
                self.assertIn(f"FROM {table} t", sql)
            self.assertNotRegex(sql, r"(?i)\b(insert|update|delete|alter|drop|create)\b")

    def test_guarded_writes_target_work_in_flight(self):
        fixture = drill.Fixture("2026-01-01T00:00:00Z")
        self.assertIn(fixture.pending_approvals[0], drill.guarded_write_sql("manager", fixture))
        self.assertIn(fixture.client_ops["pending"], drill.guarded_write_sql("hub", fixture))
        self.assertIn("VERBOSITY verbose", drill.guarded_write_sql("hub", fixture))  # the SQLSTATE is in the error

    def test_cluster_connections_are_read_only_when_asked_and_isolated(self):
        cluster = drill.Cluster(Path("/pg/bin"), Path("/tmp/drill-root"), drill.Processes(), drill.Deadline(60))
        self.assertEqual(cluster.env(read_only=True)["PGOPTIONS"], "-c default_transaction_read_only=on")
        self.assertNotIn("PGOPTIONS", cluster.env())
        with mock.patch.dict(os.environ, {"PERSONA_DATABASE_URL": "postgres://x@db.example.invalid/p", "PGHOST": "db.example.invalid",
                                          "HTTPS_PROXY": "http://proxy.example.invalid"}):
            env = cluster.env()
        self.assertFalse({"PERSONA_DATABASE_URL", "PGHOST", "HTTPS_PROXY"} & set(env))
        self.assertEqual(env["PGPASSFILE"], os.devnull)
        self.assertIn("host=%2Ftmp%2Fdrill-root%2Fs", cluster.url("drill_x"))
        self.assertNotIn("localhost", cluster.url("drill_x"))


class ProcessTests(unittest.TestCase):
    def test_a_timeout_stops_the_whole_process_group(self):
        directory = Path(tempfile.mkdtemp(prefix="drill-group-"))
        self.addCleanup(shutil.rmtree, directory, True)
        marker = directory / "child"
        processes = drill.Processes()
        with self.assertRaises(drill.DrillTimeout):
            processes.run(["/bin/sh", "-c", f"sleep 30 & echo $! > {marker}; wait"], env=drill.minimal_environment(), timeout=1)
        child = int(marker.read_text())
        for _ in range(50):
            try:
                os.kill(child, 0)
            except ProcessLookupError:
                break
            time.sleep(0.1)
        else:
            self.fail("the background child of a timed-out step is still running")
        self.assertEqual(processes.live, {})

    def test_deadline(self):
        now = [0.0]
        deadline = drill.Deadline(10, clock=lambda: now[0])
        self.assertEqual(deadline.remaining(3), 3)
        now[0] = 9.5
        self.assertAlmostEqual(deadline.remaining(3), 0.5)
        now[0] = 10
        with self.assertRaises(drill.DrillTimeout):
            deadline.remaining()


class DrillFlowTests(unittest.TestCase):
    def assert_cleaned(self, run):
        self.assertEqual(run.report["cleanup"], {"cluster_stopped": True, "processes_stopped": True, "directory_removed": True})
        self.assertFalse(run.drill.root.exists())
        self.assertTrue(run.drill.cluster.stopped)

    def assert_redacted(self, run):
        for secret in run.drill.secrets:
            self.assertNotIn(secret, run.output)
        self.assertNotIn("postgres://", run.output)
        self.assertNotIn(str(run.drill.root), run.output)
        for body in ("Synthetic task one", "Synthetic approval", "synthetic outbox body", "Synthetic note"):
            self.assertNotIn(body, run.output)

    def test_a_clean_restore_passes_without_starting_anything(self):
        run = Run(self)
        self.assertEqual(run.code, drill.EXIT_PASS, run.output)
        self.assertTrue(run.verdict_line.startswith("PASS backup restore drill"))
        self.assertEqual(run.failed_checks(), [])
        self.assertFalse(run.report["services_started"])
        self.assertEqual(run.report["personad_commands"], ["backup create", "backup verify", "backup restore", "backup verify"])
        self.assertEqual([p["phase"] for p in run.report["phases"]], [
            "build synthetic install", "backup create", "backup verify", "disaster", "backup restore",
            "backup verify after restore", "offline read-only checks"])
        self.assertTrue(all(isinstance(p["seconds"], float) for p in run.report["phases"]))
        self.assertEqual(run.report["restored_counts"], run.report["seeded_counts"])
        self.assertEqual(run.report["restored_counts"]["manager"]["interactions"], {"pending": 2, "resolved": 1})
        self.assertEqual(run.report["restored_counts"]["hub"]["client_ops"], {"done": 1, "pending": 1, "unknown": 1})
        self.assertEqual(run.report["data_point"], "2026-01-01T00:00:00Z")
        self.assert_cleaned(run)
        self.assert_redacted(run)

    def test_personad_gets_no_database_credentials_or_caller_profile(self):
        with mock.patch.dict(os.environ, {"PERSONA_DATABASE_URL": "postgres://x@db.example.invalid/p", "PERSONA_HOME": "/somewhere"}):
            run = Run(self)
        self.assertEqual(run.code, drill.EXIT_PASS, run.output)
        for env in run.drill.environments:
            self.assertFalse({key for key in env if key in ("PGPASSWORD", "PERSONA_DATABASE_URL", "PGHOST", "PGOPTIONS")})
            self.assertTrue(env["PERSONA_HOME"].startswith(str(run.drill.root)))
            self.assertTrue(env["TMPDIR"].startswith(str(run.drill.root)))

    def test_backup_verify_alone_misses_changed_work_in_flight(self):
        """The existing verification passes on a restore whose approvals or outboxes changed; the drill's checks fail."""
        for fault, expected in (("approval-resolved", "manager interactions unchanged"),
                                ("outbox-settled", "manager manager_outbox unchanged")):
            with self.subTest(fault):
                run = Run(self, "--inject", fault)
                self.assertEqual(run.phase("backup verify after restore")["status"], "ok")
                self.assertEqual(run.phase("offline read-only checks")["status"], "failed")
                self.assertIn(expected, run.failed_checks())
                self.assertIn("tasks, Agent links, pending approvals and outboxes still in flight", run.failed_checks())
                self.assertEqual(run.code, drill.EXIT_FAIL)

    def test_every_fault_fails_and_cleans_up(self):
        expectations = {
            "restore-fails": (drill.EXIT_FAIL, "backup restore", []),
            "kb-drift": (drill.EXIT_FAIL, "offline read-only checks", ["knowledge base digest"]),
            "approval-resolved": (drill.EXIT_FAIL, "offline read-only checks", ["manager interactions unchanged"]),
            "outbox-settled": (drill.EXIT_FAIL, "offline read-only checks", ["hub client_ops unchanged", "manager manager_outbox unchanged"]),
            "write-allowed": (drill.EXIT_FAIL, "offline read-only checks",
                              ["guarded hub write refused by the read-only connection",
                               "guarded manager write refused by the read-only connection",
                               "restored rows unchanged after the guarded writes"]),
            "timeout": (drill.EXIT_TIMEOUT, "inject fault", []),
        }
        self.assertEqual(set(expectations), set(drill.FAULTS))
        for fault, (code, phase, checks) in expectations.items():
            with self.subTest(fault):
                run = Run(self, "--inject", fault)
                self.assertEqual(run.code, code, run.output)
                self.assertEqual(run.report["injected_fault"], fault)
                self.assertIn(run.phase(phase)["status"], ("failed", "timeout"))
                for check in checks:
                    self.assertIn(check, run.failed_checks())
                self.assertNotEqual(run.report["verdict"], "PASS")
                self.assertTrue(run.verdict_line.startswith(("FAIL", "TIMEOUT")))
                self.assert_cleaned(run)
                self.assert_redacted(run)

    def test_a_failed_restore_reports_personad_redacted(self):
        run = Run(self, "--inject", "restore-fails")
        self.assertIn("SHA-256 mismatch", run.report["reason"])
        self.assertIn("<database url>", run.report["reason"])
        self.assertNotIn(FakeCluster.password, run.output)

    def test_incomplete_cleanup_turns_a_pass_into_a_failure(self):
        class Leaky(FakeDrill):
            def __init__(self, *args, **kwargs):
                super().__init__(*args, **kwargs)
                self.cluster.stop_result = False
        run = Run(self, factory=Leaky)
        self.assertEqual(run.code, drill.EXIT_FAIL)
        self.assertIn("cleanup incomplete", run.report["reason"])
        self.assertFalse(run.report["cleanup"]["cluster_stopped"])

    def test_a_report_that_would_leak_is_withheld(self):
        class Leaking(FakeDrill):
            def offline_checks(self):
                super().offline_checks()
                self.report["note"] = self.secrets[1]  # the hub token
        run = Run(self, factory=Leaking)
        self.assertEqual(run.code, drill.EXIT_FAIL)
        self.assertIn("withheld", run.output)
        self.assertNotIn(run.drill.secrets[1], run.output)

    def test_interruption_and_unexpected_errors_still_clean_up(self):
        class Interrupting(FakeDrill):
            def restore(self):
                raise drill.Interrupted(signal.SIGTERM)

        class Crashing(FakeDrill):
            def restore(self):
                raise RuntimeError(f"boom at {self.cluster.url(self.hub_db)}")
        run = Run(self, factory=Interrupting)
        self.assertEqual(run.code, 128 + signal.SIGTERM)
        self.assertEqual(run.report["verdict"], "INTERRUPTED")
        self.assert_cleaned(run)
        run = Run(self, factory=Crashing)
        self.assertEqual(run.code, drill.EXIT_FAIL)
        self.assertIn("RuntimeError", run.report["reason"])
        self.assert_cleaned(run)
        self.assert_redacted(run)

    def test_the_report_file_is_private(self):
        inputs_dir = Path(tempfile.mkdtemp(prefix="drill-report-"))
        self.addCleanup(shutil.rmtree, inputs_dir, True)
        report = inputs_dir / "report.json"
        run = Run(self, "--report", str(report))
        self.assertEqual(run.code, drill.EXIT_PASS)
        self.assertEqual(report.stat().st_mode & 0o777, 0o600)
        self.assertEqual(json.loads(report.read_text())["verdict"], "PASS")

    def test_a_drill_that_cannot_be_made_still_ends_with_a_verdict(self):
        def broken(*args):
            raise drill.DrillFailure("could not prepare")
        inputs, made, out, original = Inputs(self), [], io.StringIO(), tempfile.mkdtemp

        def mkdtemp(**kwargs):
            made.append(original(**kwargs))
            return made[-1]
        with mock.patch.object(drill.tempfile, "mkdtemp", side_effect=mkdtemp):
            code = drill.main(inputs.argv(), out=out, drill_factory=broken)
        self.assertEqual(code, drill.EXIT_FAIL)
        self.assertIn("FAIL backup restore drill", out.getvalue())
        self.assertIn("could not prepare", out.getvalue())
        self.assertEqual(len(made), 1)
        self.assertFalse(Path(made[0]).exists())


@unittest.skipUnless(os.environ.get("PERSONA_DRILL_PERSONAD") and os.environ.get("PERSONA_DRILL_PG_BIN"),
                     "set PERSONA_DRILL_PERSONAD (static Linux personad) and PERSONA_DRILL_PG_BIN (PostgreSQL bin directory) "
                     "to run the real drill; not run, so not verified here")
class RealDrillTests(unittest.TestCase):
    """The drill with the real personad and PostgreSQL: each run is a few seconds on a private socket-only cluster."""

    def drill(self, *extra, timeout=600):
        command = [sys.executable, "-B", str(Path(drill.__file__)), "--execute", "--personad", os.environ["PERSONA_DRILL_PERSONAD"],
                   "--pg-bin", os.environ["PERSONA_DRILL_PG_BIN"], "--timeout", str(timeout), *extra]
        before = set(Path(tempfile.gettempdir()).glob("persona-drill-*"))
        result = subprocess.run(command, capture_output=True, text=True, timeout=timeout + 60)
        self.assertEqual(set(Path(tempfile.gettempdir()).glob("persona-drill-*")) - before, set(), "the drill left its directory")
        report = json.loads(result.stdout[:result.stdout.rindex("}") + 1])
        self.assertEqual(report["cleanup"], {"cluster_stopped": True, "processes_stopped": True, "directory_removed": True})
        self.assertFalse(report["services_started"])
        self.assertNotIn("postgres://", result.stdout)
        return result.returncode, report

    def test_restore_keeps_work_in_flight(self):
        code, report = self.drill()
        self.assertEqual(code, 0, report)
        self.assertEqual(report["restored_counts"], report["seeded_counts"])
        self.assertTrue(report["data_point"])

    def test_faults_fail(self):
        for fault in drill.FAULTS:
            with self.subTest(fault):
                code, report = self.drill("--inject", fault, timeout=30 if fault == "timeout" else 600)
                self.assertEqual(code, drill.EXIT_TIMEOUT if fault == "timeout" else drill.EXIT_FAIL, report)
                if fault in ("approval-resolved", "outbox-settled"):
                    phases = {p["phase"]: p["status"] for p in report["phases"]}
                    self.assertEqual(phases["backup verify after restore"], "ok")  # what backup verify alone sees


if __name__ == "__main__":
    unittest.main()
