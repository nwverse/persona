"""hub-systemd.sh with systemd: the units unchanged and the hub running, it switches the hub's edge to the new binary
(blue-green, docs/operations/hub-restart.md "Blue-green switching: the hub's edge") instead of restarting; no edge, it restarts as before; the new binary
failing to take over puts the old one back and fails the deploy."""
import json
import os
from pathlib import Path
import plistlib
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / 'scripts/deploy/hub-systemd.sh'


@unittest.skipUnless(shutil.which('bash') and os.name == 'posix', 'needs bash on a Unix')
class HubSwitchOnDeploy(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.home = Path(self.tmp.name)
        self.root = self.home / '.persona'
        (self.root / 'bin').mkdir(parents=True)
        (self.root / 'hub.json').write_text('{"listen": ":18660"}')
        fake = self.home / 'fakebin'
        fake.mkdir()
        # systemctl: every call logged; the hub and its socket are active.
        (fake / 'systemctl').write_text('#!/bin/sh\necho "systemctl $*" >> "$HOME/calls"\nexit 0\n')
        (fake / 'systemctl').chmod(0o755)
        self.fake = fake

    def tearDown(self):
        self.tmp.cleanup()

    def personad(self, rc: int, content: str = 'new'):
        """The new binary: `hub switch` is logged and exits rc."""
        p = self.root / 'bin/personad'
        p.write_text(f'#!/bin/sh\n# {content}\necho "personad $*" >> "$HOME/calls"\nexit {rc}\n')
        p.chmod(0o755)

    def run_script(self, **extra) -> subprocess.CompletedProcess:
        env = {**os.environ, 'HOME': str(self.home), 'XDG_CONFIG_HOME': str(self.home / '.config'),
               'PATH': f'{self.fake}:{os.environ["PATH"]}', '_PERSONA_STATE_DIR': str(self.root)}
        env.pop('PERSONA_HUB_SOCKET', None)
        env.pop('PERSONA_HUB_EDGE', None)
        env.update(extra)
        return subprocess.run(['bash', str(SCRIPT)], capture_output=True, text=True, env=env)

    def unit(self) -> str:
        return (self.home / '.config/systemd/user/personad.service').read_text()

    def test_edge_off_stays_across_deploys(self):
        self.personad(3)
        self.assertEqual(self.run_script(PERSONA_HUB_EDGE='0').returncode, 0)
        self.assertIn('Environment="PERSONA_HUB_EDGE=0"', self.unit())
        self.assertEqual(self.run_script().returncode, 0)
        self.assertIn('Environment="PERSONA_HUB_EDGE=0"', self.unit(), 'a deploy without it dropped the setting')
        self.assertEqual(self.run_script(PERSONA_HUB_EDGE='1').returncode, 0)
        from test_persona_script_identity import identity
        unit = self.home / '.config/systemd/user/personad.service'
        effective = identity._unit_environment(unit, str(self.home), ('PERSONA_HUB_EDGE', 'JARVIS_HUB_EDGE'))
        self.assertEqual(identity.env_value('HUB_EDGE', effective), '1')

    def calls(self) -> list:
        p = self.home / 'calls'
        lines = p.read_text().splitlines() if p.exists() else []
        p.unlink(missing_ok=True)
        return lines

    def first_deploy(self):
        """Writes the units (they change: a restart, no switch)."""
        self.personad(3)
        r = self.run_script()
        self.assertEqual(r.returncode, 0, r.stderr)
        calls = self.calls()
        self.assertFalse(any('hub switch' in c for c in calls), calls)

    def test_switches_when_the_units_are_unchanged(self):
        self.first_deploy()
        self.personad(0)
        (self.root / 'bin/personad.prev').write_text('#!/bin/sh\n# old\n')
        (self.root / 'bin/personad.prev').chmod(0o755)
        r = self.run_script()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn('switched to the new binary', r.stdout)
        calls = self.calls()
        self.assertIn(f'personad hub switch --binary {self.root}/bin/personad --timeout 8m --prev {self.root}/bin/personad.prev', calls)
        self.assertFalse(any(c.startswith('systemctl --user restart') or c.startswith('systemctl --user stop') for c in calls), calls)

    def test_restarts_without_an_edge(self):
        self.first_deploy()
        self.personad(3)
        r = self.run_script()
        self.assertEqual(r.returncode, 0, r.stderr)
        calls = self.calls()
        self.assertTrue(any('hub switch' in c for c in calls), calls)
        self.assertIn('systemctl --user restart personad', calls)

    def test_a_new_binary_that_fails_goes_back(self):
        self.first_deploy()
        self.personad(1)
        (self.root / 'bin/personad.prev').write_text('#!/bin/sh\n# old\n')
        (self.root / 'bin/personad.prev').chmod(0o755)
        r = self.run_script()
        self.assertEqual(r.returncode, 1)
        self.assertIn('did not take over', r.stderr)
        self.assertIn('# old', (self.root / 'bin/personad').read_text())
        self.assertFalse(any(c.startswith('systemctl --user restart') for c in self.calls()))

    def test_handing_the_port_to_the_socket_keeps_the_socket_up(self):
        """The socket's addresses changed: the hub must end on its socket. A stop of the service alone while the socket
        listens is undone by the next connection (socket activation), and the socket then cannot start while the
        service runs: on a hub (2026-11-08) that left personad.socket inactive and failed the deploy."""
        self.personad(3)
        self.assertEqual(self.run_script().returncode, 0)  # the units of an earlier deploy
        state = self.home / 'state'
        state.mkdir()
        (state / 'personad').write_text('active')
        (state / 'personad.socket').write_text('active')
        # systemd as it behaves here: a client is always waiting to connect.
        (self.fake / 'systemctl').write_text(r'''#!/bin/bash
echo "systemctl $*" >> "$HOME/calls"
s="$HOME/state"
get() { cat "$s/$1" 2>/dev/null || echo inactive; }
set_() { echo "$2" > "$s/$1"; }
start_socket() {
  [ "$(get personad)" = active ] && { echo "Job for personad.socket failed." >&2; return 1; }
  set_ personad.socket active
}
args=("$@"); [ "${args[0]}" = --user ] && args=("${args[@]:1}")
verb=${args[0]}; units=("${args[@]:1}")
case $verb in
  is-active)
    u=${units[${#units[@]}-1]}; [ "${units[0]}" = --quiet ] || echo "$(get "$u")"
    [ "$(get "$u")" = active ] ;;
  stop)
    for u in "${units[@]}"; do set_ "$u" inactive; done
    [[ " ${units[*]} " == *" personad.socket "* ]] || { [ "$(get personad.socket)" = active ] && set_ personad active; }
    exit 0 ;;
  start|restart)
    for u in "${units[@]}"; do
      if [ "$u" = personad.socket ]; then
        [ "$verb" = restart ] && set_ personad.socket inactive
        start_socket || exit 1
      else set_ "$u" active; fi
    done ;;
  disable)
    [ "${units[0]}" = --now ] && set_ "${units[${#units[@]}-1]}" inactive; exit 0 ;;
  *) exit 0 ;;
esac
''')
        (self.root / 'hub.json').write_text('{"listen": ":18661"}')
        self.calls()
        r = self.run_script()
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual((state / 'personad.socket').read_text().strip(), 'active', self.calls())
        self.assertEqual((state / 'personad').read_text().strip(), 'active')


def executable(path: Path, text: str):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)
    path.chmod(0o755)


@unittest.skipUnless(shutil.which('bash') and os.name == 'posix', 'needs bash on a Unix')
class HubProgramKeptOnDeploy(unittest.TestCase):
    """The program the hub's unit runs stays the one it runs: persona-hub once its owner switched to it (`hub setup`),
    personad otherwise. A deploy never switches an install from one to the other."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        # A data directory whose path systemd has to quote and escape.
        self.home = Path(self.tmp.name)
        self.root = self.home / 'data $ with " \\ %h'
        (self.root / 'bin').mkdir(parents=True)
        (self.root / 'hub.json').write_text('{"listen": ":18660"}')
        self.fake = self.home / 'fakebin'
        executable(self.fake / 'systemctl', '#!/bin/sh\necho "systemctl $*" >> "$HOME/calls"\nexit 0\n')
        self.unit_dir = self.home / '.config/systemd/user'

    def tearDown(self):
        self.tmp.cleanup()

    def program(self, name: str, rc: int = 3, content: str = 'new'):
        """A program in bin/: `hub switch` is logged and exits rc."""
        executable(self.root / 'bin' / name, f'#!/bin/sh\n# {content}\necho "{name} $*" >> "$HOME/calls"\nexit {rc}\n')

    def prev(self, name: str):
        executable(self.root / 'bin' / (name + '.prev'), f'#!/bin/sh\n# old {name}\n')

    def run_script(self, **extra) -> subprocess.CompletedProcess:
        env = {**os.environ, 'HOME': str(self.home), 'XDG_CONFIG_HOME': str(self.home / '.config'),
               'PATH': f'{self.fake}:{os.environ["PATH"]}', 'PERSONA_HOME': str(self.root)}
        for key in ('PERSONA_HUB_SOCKET', 'PERSONA_HUB_EDGE', '_PERSONA_STATE_DIR', '_PERSONA_HUB_UNIT'):
            env.pop(key, None)
        env.update(extra)
        return subprocess.run(['bash', str(SCRIPT)], capture_output=True, text=True, env=env)

    def calls(self) -> list:
        p = self.home / 'calls'
        lines = p.read_text().splitlines() if p.exists() else []
        p.unlink(missing_ok=True)
        return lines

    def quoted(self, name: str) -> str:
        """bin/<name> as hub-systemd.sh and hub setup write it in ExecStart."""
        value = str(self.root / 'bin' / name)
        return '"' + value.replace('\\', '\\\\').replace('"', '\\"').replace('%', '%%').replace('$', '$$') + '"'

    def exec_starts(self) -> list:
        service = self.unit_dir / 'personad.service'
        files = [service] + sorted(service.with_name(service.name + '.d').glob('*.conf'))
        return [line for f in files if f.exists() for line in f.read_text().splitlines() if line.startswith('ExecStart=')]

    def switched_unit(self):
        """The unit as a switched install has it: persona-hub run, otherwise the deploy's own text."""
        self.program('personad')
        self.program('persona-hub')
        r = self.run_script()  # personad's unit first, then the owner's switch
        self.assertEqual(r.returncode, 0, r.stderr)
        unit = self.unit_dir / 'personad.service'
        text = unit.read_text().replace('ExecStart=' + self.quoted('personad') + ' hub', 'ExecStart=' + self.quoted('persona-hub') + ' run')
        self.assertIn('persona-hub', text)
        unit.write_text(text.replace('Description=Persona hub (personad)', 'Description=Persona hub (persona-hub)'))
        self.calls()

    def test_a_personad_unit_stays_on_personad(self):
        self.program('personad')
        self.program('persona-hub')
        self.program('persona-manager')
        for _ in range(2):
            r = self.run_script()
            self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.exec_starts(), ['ExecStart=' + self.quoted('personad') + ' hub'])
        self.assertFalse(any(c.startswith('persona-hub') for c in self.calls()))

    def test_a_persona_hub_unit_stays_on_persona_hub(self):
        self.switched_unit()
        before = (self.unit_dir / 'personad.service').read_text()
        self.program('persona-hub', rc=0)
        self.prev('persona-hub')
        r = self.run_script()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual((self.unit_dir / 'personad.service').read_text(), before, 'the unit was rewritten')
        self.assertFalse((self.unit_dir / 'personad.service.d').exists(), 'a drop-in overrides the unit')
        self.assertIn('switched to the new binary', r.stdout)
        calls = self.calls()
        self.assertIn(f'persona-hub hub switch --binary {self.root}/bin/persona-hub --timeout 8m --prev {self.root}/bin/persona-hub.prev', calls)
        self.assertFalse(any(c.startswith('personad') for c in calls), calls)

    def test_a_persona_hub_drop_in_stays(self):
        # An ExecStart in a drop-in (one an owner wrote) counts, as systemd reads it: the drop-in keeps persona-hub.
        self.switched_unit()
        unit = self.unit_dir / 'personad.service'
        dropin = unit.with_name(unit.name + '.d') / '50-mine.conf'
        dropin.parent.mkdir()
        dropin.write_text('[Service]\nExecStart=\nExecStart=' + self.quoted('persona-hub') + ' run\n')
        unit.write_text(unit.read_text().replace(self.quoted('persona-hub') + ' run', '/usr/bin/true'))
        r = self.run_script()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.exec_starts()[-1], 'ExecStart=' + self.quoted('persona-hub') + ' run')

    def test_a_failed_switch_puts_every_program_back(self):
        self.switched_unit()
        for name in ('personad', 'persona-hub', 'persona-manager'):
            self.program(name, rc=1)
            self.prev(name)
        r = self.run_script()
        self.assertEqual(r.returncode, 1)
        self.assertIn('did not take over', r.stderr)
        for name in ('personad', 'persona-hub', 'persona-manager'):
            self.assertIn(f'# old {name}', (self.root / 'bin' / name).read_text(), name)
        self.assertFalse(any(c.startswith('systemctl --user restart') for c in self.calls()))

    def test_a_program_that_cannot_be_put_back_is_named(self):
        # A put-back that fails (here a leftover directory where its copy goes) is said, not reported as back in place;
        # the others still go back.
        self.switched_unit()
        for name in ('personad', 'persona-hub', 'persona-manager'):
            self.program(name, rc=1)
            self.prev(name)
        (self.root / 'bin/persona-manager.new').mkdir()
        r = self.run_script()
        self.assertEqual(r.returncode, 1)
        self.assertNotIn('back in place', r.stderr)
        self.assertIn(f'could not put the old build back for {self.root}/bin/persona-manager (', r.stderr)
        self.assertIn('# new', (self.root / 'bin/persona-manager').read_text())
        for name in ('personad', 'persona-hub'):
            self.assertIn(f'# old {name}', (self.root / 'bin' / name).read_text(), name)

    def test_a_personad_unit_puts_only_personad_back(self):
        # As before: a hub run as personad runs nothing else of the deploy.
        self.program('personad')
        self.assertEqual(self.run_script().returncode, 0)
        for name in ('personad', 'persona-hub', 'persona-manager'):
            self.program(name, rc=1)
            self.prev(name)
        r = self.run_script()
        self.assertEqual(r.returncode, 1)
        self.assertIn('# old personad', (self.root / 'bin/personad').read_text())
        for name in ('persona-hub', 'persona-manager'):
            self.assertIn('# new', (self.root / 'bin' / name).read_text(), name)

    def test_a_persona_hub_unit_without_persona_hub_fails_and_changes_nothing(self):
        self.switched_unit()
        before = (self.unit_dir / 'personad.service').read_text()
        (self.root / 'bin/persona-hub').unlink()
        r = self.run_script()
        self.assertEqual(r.returncode, 1)
        self.assertIn('which is not there', r.stderr)
        self.assertEqual((self.unit_dir / 'personad.service').read_text(), before)
        self.assertFalse(any(c.startswith(('systemctl --user restart', 'systemctl --user stop')) for c in self.calls()))

    def test_a_persona_hub_unit_in_any_form_systemd_reads_stays(self):
        # Under %h, in single quotes, with spaces around the key's =: all persona-hub to systemd, so to the deploy too.
        def escaped(value):
            return value.replace('\\', '\\\\').replace('%', '%%').replace('$', '$$')
        rel = str(self.root.relative_to(self.home))
        for start in ('="%h/' + escaped(rel).replace('"', '\\"') + '/bin/persona-hub" run',
                      "='" + escaped(str(self.root)) + "/bin/persona-hub' run",
                      ' = ' + self.quoted('persona-hub') + ' run'):
            with self.subTest(start=start):
                shutil.rmtree(self.unit_dir, ignore_errors=True)
                self.switched_unit()
                unit = self.unit_dir / 'personad.service'
                unit.write_text(unit.read_text().replace('ExecStart=' + self.quoted('persona-hub') + ' run', 'ExecStart' + start))
                self.assertIn('ExecStart' + start, unit.read_text())
                r = self.run_script()
                self.assertEqual(r.returncode, 0, r.stderr)
                self.assertEqual(self.exec_starts()[-1], 'ExecStart=' + self.quoted('persona-hub') + ' run')

    def test_a_unit_naming_another_persona_hub_is_left_alone(self):
        # The backstop: an ExecStart that names persona-hub without being this root's (a wrapper, another copy, a bare
        # name) fails the deploy before the unit is rewritten.
        self.program('personad')
        self.program('persona-hub')
        self.unit_dir.mkdir(parents=True)
        unit = self.unit_dir / 'personad.service'
        for start in ("/bin/sh -c '" + str(self.root / 'bin/persona-hub') + " run'", '/opt/other/bin/persona-hub run', 'persona-hub run'):
            with self.subTest(start=start):
                text = '[Unit]\nDescription=hub\n\n[Service]\nExecStart=' + start + '\n'
                unit.write_text(text)
                r = self.run_script()
                self.assertEqual(r.returncode, 1)
                self.assertIn('the unit is left as it is', r.stderr)
                self.assertEqual(unit.read_text(), text)
                self.assertFalse((self.unit_dir / 'personad.service.d').exists())
                self.assertFalse((self.unit_dir / 'personad.socket').exists())
                self.assertFalse(any(c.startswith(('systemctl', 'persona')) for c in self.calls()))

    def test_a_persona_hub_unit_without_its_prev_restarts(self):
        # Nothing to go back to if the switch failed: the service restarts on the build on disk instead.
        self.switched_unit()
        self.program('persona-hub', rc=1)
        r = self.run_script()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn('persona-hub.prev to go back to', r.stdout)
        calls = self.calls()
        self.assertFalse(any('hub switch' in c for c in calls), calls)
        self.assertIn('systemctl --user restart personad', calls)

    def test_a_failed_switch_puts_back_through_a_link(self):
        # An install under the former name keeps personad as a link to that file: the old build goes back into the
        # file, and the link stays.
        from test_persona_script_identity import identity
        former = identity.BINARY_NAMES['hub'][1]
        self.switched_unit()
        for name in ('personad', 'persona-hub', 'persona-manager'):
            self.program(name, rc=1)
            self.prev(name)
        personad = self.root / 'bin/personad'
        personad.rename(self.root / 'bin' / former)
        personad.symlink_to(former)
        r = self.run_script()
        self.assertEqual(r.returncode, 1, r.stdout)
        self.assertTrue(personad.is_symlink())
        self.assertIn('# old personad', (self.root / 'bin' / former).read_text())
        for name in ('persona-hub', 'persona-manager'):
            self.assertIn(f'# old {name}', (self.root / 'bin' / name).read_text(), name)


@unittest.skipUnless(shutil.which('bash') and os.name == 'posix', 'needs bash on a Unix')
class HubProgramFormsKept(unittest.TestCase):
    """A unit whose only ExecStart names this root's persona-hub in a form systemd accepts stays on persona-hub after
    a deploy (the forms a review found moved back to personad)."""

    def test_forms(self):
        for start in ('ExecStart="<root>/bin/persona-hub" run', 'ExecStart=%h/.persona/bin/persona-hub run',
                      "ExecStart='<root>/bin/persona-hub' run", 'ExecStart = <root>/bin/persona-hub run'):
            with self.subTest(start=start), tempfile.TemporaryDirectory() as d:
                home = Path(d)
                root = home / '.persona'
                (root / 'hub.json').parent.mkdir(parents=True)
                (root / 'hub.json').write_text('{"listen": ":18660"}')
                executable(home / 'fakebin/systemctl', '#!/bin/sh\necho "systemctl $*" >> "$HOME/calls"\nexit 0\n')
                for name in ('personad', 'persona-hub'):
                    executable(root / 'bin' / name, f'#!/bin/sh\necho "{name} $*" >> "$HOME/calls"\nexit 3\n')
                service = home / '.config/systemd/user/personad.service'
                service.parent.mkdir(parents=True)
                service.write_text('[Unit]\nDescription=Persona hub (persona-hub)\n\n[Service]\n' + start.replace('<root>', str(root))
                                   + '\nRestart=always\n')
                env = {**os.environ, 'HOME': str(home), 'XDG_CONFIG_HOME': str(home / '.config'),
                       'PATH': f'{home / "fakebin"}:{os.environ["PATH"]}', 'PERSONA_HOME': str(root)}
                for key in ('PERSONA_HUB_SOCKET', 'PERSONA_HUB_EDGE', '_PERSONA_STATE_DIR', '_PERSONA_HUB_UNIT'):
                    env.pop(key, None)
                r = subprocess.run(['bash', str(SCRIPT)], capture_output=True, text=True, env=env)
                self.assertEqual(r.returncode, 0, r.stderr)
                files = [service] + sorted((service.parent / 'personad.service.d').glob('*.conf'))
                starts = [line for f in files for line in f.read_text().splitlines() if line.strip().startswith('ExecStart')]
                effective = [line for line in starts if line.split('=', 1)[1].strip()][-1]
                self.assertEqual(effective, f'ExecStart="{root}/bin/persona-hub" run')


@unittest.skipUnless(shutil.which('bash') and os.name == 'posix', 'needs bash on a Unix')
class UnitProgramReader(unittest.TestCase):
    """hub-systemd.sh reads a unit's ExecStart as the hub's self-update does (unitPrograms): both over one table,
    server/internal/hub/testdata/exec_start.json (TestExecProgram there)."""

    def read(self, home: Path, unit: Path, **extra) -> subprocess.CompletedProcess:
        return subprocess.run(['bash', str(SCRIPT), '--unit-program', str(unit)], capture_output=True, text=True,
                              env={**os.environ, 'HOME': str(home), **extra})

    def test_the_table(self):
        cases = json.loads((ROOT / 'server/internal/hub/testdata/exec_start.json').read_text())
        with tempfile.TemporaryDirectory() as d:
            home = Path(d)
            unit = home / 'a.service'
            for case in cases:
                with self.subTest(line=case['line']):
                    unit.write_text('[Service]\n' + case['line'] + '\n')
                    r = self.read(home, unit)
                    self.assertEqual(r.returncode, 0, r.stderr)
                    want = case['program'].replace('<home>', str(home))
                    self.assertEqual(r.stdout.rstrip('\n'), os.path.realpath(want) if want else '')

    def test_another_persona_hub_fails(self):
        with tempfile.TemporaryDirectory() as d:
            home = Path(d)
            split = home / 'p/bin/persona-hub'
            unit = home / 'a.service'
            for start, fails in (('"%h/p/bin/persona-hub" run', False), ('/srv/persona-hub-data/bin/personad hub', False),
                                 ('/srv/persona-hub/bin/personad hub', False), ("/bin/sh -c '%h/p/bin/persona-hub run'", True),
                                 ('/opt/other/persona-hub run', True), ('persona-hub run', True)):
                with self.subTest(start=start):
                    unit.write_text('[Service]\nExecStart=' + start + '\n')
                    r = self.read(home, unit, SPLIT_HUB=str(split))
                    self.assertEqual(r.returncode != 0, fails, r.stdout + r.stderr)


@unittest.skipUnless(shutil.which('bash') and shutil.which('python3') and os.name == 'posix', 'needs bash and python3')
class HubProgramOnAMac(unittest.TestCase):
    """The launchd branch (a Mac: uname says Darwin and there is no systemctl), run anywhere: a stand-in uname and a
    PATH with only the tools the branch uses. It finds the agent that runs this root's personad hub or persona-hub,
    switches with that program and restarts that agent when there is no edge."""

    def run_mac(self, jobs: dict, programs: dict) -> tuple:
        """jobs: label -> ProgramArguments, or the whole job; programs: name in bin/ -> what `hub switch` exits. The
        calls come back with the root as <root>."""
        with tempfile.TemporaryDirectory() as d:
            home = Path(d)
            root = home / '.persona'
            for name, rc in programs.items():
                executable(root / 'bin' / name, f'#!/bin/sh\necho "{name} $*" >> "$HOME/calls"\nexit {rc}\n')
            (home / 'Library/LaunchAgents').mkdir(parents=True)
            for label, args in jobs.items():
                job = args if isinstance(args, dict) else {'ProgramArguments': args}
                job = {k: [a.replace('<root>', str(root)) for a in v] if isinstance(v, list) else v.replace('<root>', str(root))
                       for k, v in job.items()}
                with open(home / 'Library/LaunchAgents' / f'{label}.plist', 'wb') as f:
                    plistlib.dump({'Label': label, **job}, f)
            tools = home / 'tools'
            executable(tools / 'uname', '#!/bin/sh\necho Darwin\n')
            executable(tools / 'launchctl', '#!/bin/sh\necho "launchctl $*" >> "$HOME/calls"\n')
            for tool in ('python3', 'dirname', 'id', 'cp', 'mv', 'cat', 'sh'):
                os.symlink(shutil.which(tool), tools / tool)
            env = {'HOME': str(home), 'PATH': str(tools), 'PERSONA_HOME': str(root)}
            r = subprocess.run([shutil.which('bash'), str(SCRIPT)], capture_output=True, text=True, env=env)
            calls = (home / 'calls').read_text().splitlines() if (home / 'calls').exists() else []
            return r, [c.replace(str(root), '<root>') for c in calls], os.getuid()

    def test_an_agent_running_persona_hub(self):
        r, calls, uid = self.run_mac({'a.node': ['<root>/bin/personad', 'node'], 'my.hub': ['<root>/bin/persona-hub', 'run']},
                                     {'personad': 3, 'persona-hub': 3, 'persona-hub.prev': 0})
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(calls, ['persona-hub hub switch --binary <root>/bin/persona-hub --timeout 8m --prev <root>/bin/persona-hub.prev',
                                 f'launchctl kickstart -k gui/{uid}/my.hub'])

    def test_an_agent_whose_program_is_persona_hub(self):
        # launchd runs Program, with ProgramArguments only as its argv.
        job = {'Program': '<root>/bin/persona-hub', 'ProgramArguments': ['<root>/bin/personad', 'hub']}
        r, calls, uid = self.run_mac({'my.hub': job}, {'personad': 3, 'persona-hub': 3, 'persona-hub.prev': 0})
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertTrue(calls and calls[0].startswith('persona-hub hub switch --binary <root>/bin/persona-hub '), calls)

    def test_an_agent_running_persona_hub_without_its_prev_restarts(self):
        r, calls, uid = self.run_mac({'my.hub': ['<root>/bin/persona-hub', 'run']}, {'personad': 0, 'persona-hub': 0})
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(calls, [f'launchctl kickstart -k gui/{uid}/my.hub'])

    def test_an_agent_running_personad_stays_on_personad(self):
        r, calls, uid = self.run_mac({'my.hub': ['<root>/bin/personad', 'hub']}, {'personad': 0, 'persona-hub': 0})
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(calls, ['personad hub switch --binary <root>/bin/personad --timeout 8m'])

    def test_no_agent_is_an_error(self):
        r, calls, _ = self.run_mac({'other': ['<root>/bin/persona-hub', 'pair']}, {'personad': 0, 'persona-hub': 0})
        self.assertEqual(r.returncode, 1)
        self.assertIn('no launchd agent', r.stderr)
        self.assertEqual(calls, [])


if __name__ == '__main__':
    unittest.main()
