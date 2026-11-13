"""Isolated Git, process and history fixtures for Web development updates."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("webdev", ROOT / "server/internal/update/webdev.py")
webdev = importlib.util.module_from_spec(spec)
spec.loader.exec_module(webdev)


class DevelopmentFixtures(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="persona-web-dev-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bin = self.root / "tools"
        self.bin.mkdir()
        self.env = patch.dict(os.environ, {"PATH": str(self.bin) + os.pathsep + os.environ["PATH"],
                                          "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.devnull})
        self.env.start()
        self.addCleanup(self.env.stop)

    def tool(self, name, source):
        path = self.bin / name
        path.write_text("#!" + sys.executable + "\n" + source)
        path.chmod(0o755)
        return str(path)

    def git(self, *args):
        return subprocess.check_output(["git", "-C", str(self.repo), *args], text=True, stderr=subprocess.DEVNULL).strip()

    def test_project_main_and_fixed_compile_candidate(self):
        self.repo = self.root / "remote"
        self.repo.mkdir()
        self.git("init", "-b", "main")
        self.git("config", "user.name", "Fixture")
        self.git("config", "user.email", "fixture@example.com")
        for relative, content in {"server/build-tags.txt":"public", "web/package.json":"{}", "integrations/agent-sdk/package.json":"{}"}.items():
            file = self.repo / relative
            file.parent.mkdir(parents=True, exist_ok=True)
            file.write_text(content)
        self.git("add", ".")
        self.git("commit", "-m", "First fixture")
        repository = patch.object(webdev, "REPOSITORY", self.repo.as_uri())
        repository.start()
        self.addCleanup(repository.stop)
        config = {"node_platforms": ["linux-amd64", "darwin-arm64"]}
        selected = webdev.resolve()
        self.assertEqual(selected["branch"], "main")
        (self.repo / "newer").write_text("remote advanced after checking")
        self.git("add", ".")
        self.git("commit", "-m", "Second fixture")
        trace = self.root / "compile.jsonl"
        os.environ["FIXTURE_TRACE"] = str(trace)
        self.addCleanup(os.environ.pop, "FIXTURE_TRACE", None)
        fixture = '''import json, os, pathlib, subprocess, sys
args = sys.argv[1:]
with open(os.environ['FIXTURE_TRACE'], 'a') as log: log.write(json.dumps([pathlib.Path(sys.argv[0]).name, args])+'\\n')
if pathlib.Path(sys.argv[0]).name == 'npm' and args == ['run', 'build:development'] and pathlib.Path.cwd().name == 'web':
    pathlib.Path('dist').mkdir(); pathlib.Path('dist/index.html').write_text('fixture web')
if pathlib.Path(sys.argv[0]).name == 'go' and args[0] == 'build':
    pathlib.Path(args[args.index('-o')+1]).write_text(subprocess.check_output(['git','rev-parse','HEAD'], text=True))
'''
        self.tool("go", fixture)
        self.tool("npm", fixture)
        work = self.root / "build"
        work.mkdir()
        built = webdev.build({"config":config, "work":str(work), "candidate":selected,
                              "version":"web-dev-fixture", "build":202611130001, "platform":"linux-amd64"})
        self.assertEqual((work / "personad-linux-amd64").read_text().strip(), selected["commit"])
        self.assertNotEqual(selected["commit"], self.git("rev-parse", "HEAD"))
        self.assertEqual(len(list(Path(built["artifacts"]).glob("*.gz"))), 4)
        commands = [json.loads(line) for line in trace.read_text().splitlines()]
        self.assertFalse(any("test" in args or "vet" in args for _, args in commands))
        self.git("branch", "-m", "other")
        with self.assertRaisesRegex(ValueError, "project main branch"):
            webdev.resolve()

    def history_fixture(self):
        runtime = self.root / "runtime"
        (runtime / "data").mkdir(parents=True)
        (runtime / "hub.json").write_text(json.dumps({"database_url":"fixture-only"}))
        (runtime / "secret.key").write_bytes(b"fixture-key")
        (runtime / "data/history.txt").write_text("synthetic history")
        binary = self.root / "personad"
        binary.write_bytes(b"fixture installed program")
        database = self.root / "database.json"
        database.write_text('{"messages":["synthetic history"]}')
        self.env_db = patch.dict(os.environ, {"FIXTURE_DB":str(database)})
        self.env_db.start()
        self.addCleanup(self.env_db.stop)
        self.tool("pg_dump", "import os,pathlib,sys\np=pathlib.Path(sys.argv[sys.argv.index('--file')+1]);p.write_bytes(b'PGDMP'+pathlib.Path(os.environ['FIXTURE_DB']).read_bytes())\n")
        self.tool("pg_restore", "import pathlib,sys\nassert pathlib.Path(sys.argv[-1]).read_bytes().startswith(b'PGDMP')\nprint('fixture archive list')\n")
        events = self.root / "events"
        hook = self.tool("writer-hook", "import pathlib,sys\np=pathlib.Path(sys.argv[1]);p.write_text((p.read_text() if p.exists() else '')+sys.argv[2]+'\\n')\n")
        return {"runtime": str(runtime), "hub_database":"effective-fixture-database", "executables":{"personad":str(binary)}, "candidate":{"commit":"a"*40}, "config":{"backup":{
            "quiesce":[hook,str(events),"pause"], "resume":[hook,str(events),"resume"]}}}

    def test_three_verified_snapshots_and_failure_retention(self):
        request = self.history_fixture()
        store = Path(request["runtime"]) / "update/development-backups"
        receipts = []
        for _ in range(5):
            receipts.append(webdev.snapshot(request))
        snapshots = sorted(p for p in store.iterdir() if webdev.NAME.fullmatch(p.name))
        self.assertEqual(len(snapshots), 3)
        self.assertEqual([p.name for p in snapshots], [r["id"] for r in receipts[-3:]])
        self.assertEqual(receipts[-1]["retained"], 3)
        manifest = webdev.verify(snapshots[-1])
        self.assertEqual(manifest["databases"], 1)
        self.assertEqual(json.loads((snapshots[-1]/"effective-databases.json").read_text()),["effective-fixture-database"])
        self.assertIn("personad",manifest["programs"])
        with tarfile.open(snapshots[-1]/"files.tar.gz") as archive:
            self.assertEqual(archive.extractfile("root-0/data/history.txt").read(), b"synthetic history")
            self.assertEqual(archive.extractfile("root-0/secret.key").read(), b"fixture-key")
            self.assertFalse(any("update/development-backups" in m.name for m in archive))
        unrelated = store / "manual-backup"
        unrelated.mkdir()
        (unrelated/"keep").write_text("untouched")
        self.tool("pg_dump", "raise SystemExit(7)\n")
        with self.assertRaises(subprocess.CalledProcessError):
            webdev.snapshot(request)
        self.assertEqual(sorted(p.name for p in snapshots), sorted(p.name for p in store.iterdir() if webdev.NAME.fullmatch(p.name)))
        self.assertTrue((unrelated/"keep").exists())
        self.assertTrue((self.root/"events").read_text().endswith("pause\nresume\n"))
        self.assertFalse((store/"coordination-pending.json").exists())

    def test_resume_failure_and_unknown_coordination_block_retry(self):
        request = self.history_fixture()
        request["config"]["backup"]["resume"] = [self.tool("resume-fails", "raise SystemExit(2)\n")]
        with self.assertRaises(subprocess.CalledProcessError):
            webdev.snapshot(request)
        with self.assertRaisesRegex(ValueError,"coordination needs recovery"):
            webdev.snapshot(request)

    def test_real_writer_is_quiesced_for_the_whole_snapshot(self):
        request = self.history_fixture()
        runtime = Path(request["runtime"])
        pause, paused = self.root/"pause", self.root/"paused"
        writer = self.root/"writer.py"
        writer.write_text('''import pathlib,sys,time
root,pause,paused,db=map(pathlib.Path,sys.argv[1:])
counter=0
while True:
    if pause.exists():
        paused.touch()
        while pause.exists(): time.sleep(.002)
        paused.unlink(missing_ok=True)
    counter+=1
    (root/'data/history.txt').write_text(str(counter))
    db.write_text(str(counter))
    time.sleep(.002)
''')
        process = subprocess.Popen([sys.executable,str(writer),str(runtime),str(pause),str(paused),str(self.root/"database.json")])
        self.addCleanup(lambda: (process.terminate(),process.wait(timeout=3)))
        hook = self.tool("coordinate", '''import pathlib,sys,time
pause,paused=map(pathlib.Path,sys.argv[1:3])
if sys.argv[3]=='pause': pause.touch()
else: pause.unlink(missing_ok=True)
for _ in range(1000):
    if paused.exists()==(sys.argv[3]=='pause'): break
    time.sleep(.002)
else: raise SystemExit(3)
''')
        request["config"]["backup"].update(quiesce=[hook,str(pause),str(paused),"pause"],resume=[hook,str(pause),str(paused),"resume"])
        receipt = webdev.snapshot(request)
        path = runtime / "update/development-backups" / receipt["id"]
        with tarfile.open(path/"files.tar.gz") as archive:
            history = archive.extractfile("root-0/data/history.txt").read()
        self.assertEqual((path/"database-0.dump").read_bytes()[5:], history)
        self.assertFalse(pause.exists())


if __name__ == "__main__":
    unittest.main()
