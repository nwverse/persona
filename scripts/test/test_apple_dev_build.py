"""The iOS development build (#10713): the switch that leaves the watch app and the share extension out, and the commit
stamp that runs only in Release. The project checks need xcodegen (skipped without it); the rest reads the scripts."""
import json
import os
from pathlib import Path
import plistlib
import re
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
APPLE = ROOT / 'apple'
SWITCH = 'PERSONA_LEAN_DEV_BUILD'
# What the iPhone app depends on in every build, in project.yml's order, and what only the whole build adds.
ALWAYS = ['LiveCommunicationKit.framework', 'PersonaWire', 'PersonaWidgets', 'PersonaNotify', 'MarkdownUI', 'BeautifulUI',
          'PersonaSSH', 'PersonaKit', 'PersonaCore', 'MarqueeGlow', 'PersonaScreenKit', 'PersonaInputKit']
WHOLE_ONLY = ['PersonaWatch', 'PersonaShare']


def dump(**switches):
    # Only the switches given: none of the caller's (persona_config.py env exports every PERSONA_* switch).
    env = {k: v for k, v in os.environ.items() if not k.startswith('PERSONA_')}
    env.setdefault('USER', 'fixture')
    env.update(switches)
    out = subprocess.run(['xcodegen', 'dump', '--spec', str(APPLE / 'project.yml'), '--type', 'json'],
                         env=env, capture_output=True, text=True, timeout=120, check=True).stdout
    return json.loads(out)


def dependencies(project, target='Persona'):
    return [d.get('target') or d.get('package') or d.get('sdk') for d in project['targets'][target]['dependencies']]


@unittest.skipUnless(shutil.which('xcodegen'), 'needs xcodegen')
class LeanDevBuildTests(unittest.TestCase):
    def test_unset_or_no_is_the_whole_project(self):
        # CI, release and the UI tests never set the switch: the app embeds the watch app and the share extension.
        for switches in ({}, {SWITCH: 'NO'}):
            with self.subTest(switches=switches):
                project = dump(**switches)
                self.assertEqual(dependencies(project), WHOLE_ONLY + ALWAYS)
                self.assertIn('PersonaShare', project['targets'])
                self.assertIn({'target': 'PersonaWatchWidgets'}, project['targets']['PersonaWatch']['dependencies'])

    def test_yes_leaves_out_only_the_watch_app_and_the_share_extension(self):
        whole, lean = dump(), dump(**{SWITCH: 'YES'})
        self.assertEqual(dependencies(lean), ALWAYS)
        # Nothing else changes: the targets stay (the PersonaWatch scheme still builds the watch on its own).
        lean_targets = {name: {k: v for k, v in target.items() if not (name == 'Persona' and k == 'dependencies')}
                        for name, target in lean['targets'].items()}
        whole_targets = {name: {k: v for k, v in target.items() if not (name == 'Persona' and k == 'dependencies')}
                         for name, target in whole['targets'].items()}
        self.assertEqual(lean_targets, whole_targets)
        self.assertEqual(lean['schemes'], whole['schemes'])

    def test_a_later_include_keeps_its_own_dependencies(self):
        # The switch empties only what the includes before it added; siri-calls.yml comes after it.
        lean = dump(**{SWITCH: 'YES', 'PERSONA_SIRI_CALLS': 'YES'})
        self.assertEqual(dependencies(lean), ['PersonaCallIntents'] + ALWAYS)
        whole = dump(PERSONA_SIRI_CALLS='YES')
        self.assertEqual(dependencies(whole), WHOLE_ONLY + ['PersonaCallIntents'] + ALWAYS)

    def test_the_switch_include_stays_before_every_include_that_adds_dependencies(self):
        project = (APPLE / 'project.yml').read_text()
        paths = re.findall(r'^  - path: (\S+)$', project.split('\nconfigFiles:')[0], re.M)
        self.assertEqual(paths[:3], ['app-entitlements.yml', 'watch-and-share.yml', 'lean-dev-build.yml'])
        self.assertNotIn('dependencies', (APPLE / 'app-entitlements.yml').read_text())


class LeanDevBuildCallersTests(unittest.TestCase):
    def test_only_sim_sh_turns_the_switch_on(self):
        listed = subprocess.run(['git', '-C', str(ROOT), 'grep', '-l', SWITCH + '=', '--', 'scripts', '.github'],
                                capture_output=True, text=True, timeout=60).stdout.split()
        self.assertEqual([p for p in listed if not p.startswith('scripts/test/')], ['scripts/app/sim.sh'])

    def test_sim_sh_builds_lean_unless_asked_for_the_watch(self):
        sim = (ROOT / 'scripts/app/sim.sh').read_text()
        self.assertIn('--with-watch) LEAN=NO', sim)
        self.assertIn('PERSONA_LEAN_DEV_BUILD=$LEAN xcodegen generate', sim)
        self.assertIn('build-apple.sh --simulator', sim)
        # The launch arguments still reach the app, without the flag.
        self.assertIn('${ARGS[@]+"${ARGS[@]}"}', sim)

    def test_release_ci_and_ui_tests_build_every_slice_and_the_whole_project(self):
        # Each clears the switch before it generates the project, so a value the shell exported cannot drop the watch
        # app from a TestFlight build, and asks for every slice of the Rust core.
        for path in ('scripts/release/release.sh', 'scripts/ci/apple.sh', 'scripts/app/ui-test.sh'):
            with self.subTest(path=path):
                text = (ROOT / path).read_text()
                self.assertIn('build-apple.sh', text)
                self.assertNotIn('--simulator', text)
                self.assertNotIn(SWITCH + '=', text)
                generate = text.index('xcodegen generate')
                self.assertLess(text.index('unset ' + SWITCH + '\n'), generate)


@unittest.skipUnless(shutil.which('xcodegen') and shutil.which('git'), 'needs xcodegen and git')
class CommitStampTests(unittest.TestCase):
    """Release stamps the commit exactly as before; Debug leaves the built Info.plist alone, so an unchanged build
    neither writes it again nor signs the app again."""

    def stamp(self, target, configuration, dirty, **settings):
        script = next(s['script'] for s in dump()['targets'][target]['postBuildScripts'] if s['name'] == 'Stamp the commit')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            git = ['git', '-C', str(root), '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.com',
                   '-c', 'commit.gpgsign=false', '-c', 'core.hooksPath=/dev/null']
            subprocess.run(['git', 'init', '-q', str(root)], check=True)
            (root / 'apple').mkdir()
            (root / 'apple/file.txt').write_text('one\n')
            subprocess.run(git + ['add', '.'], check=True)
            subprocess.run(git + ['commit', '-q', '-m', 'fixture'], check=True)
            if dirty:
                (root / 'apple/file.txt').write_text('two\n')
            head = subprocess.check_output(['git', '-C', str(root), 'rev-parse', '--short=8', 'HEAD'], text=True).strip()
            plist = root / 'build/Persona.app/Info.plist'
            plist.parent.mkdir(parents=True)
            plist.write_bytes(plistlib.dumps({'CFBundleName': 'Persona'}))
            before = plist.read_bytes()
            env = {k: v for k, v in os.environ.items() if k != 'PERSONA_STAMP_COMMIT'}
            env.update(SRCROOT=str(root / 'apple'), TARGET_BUILD_DIR=str(root / 'build'),
                       INFOPLIST_PATH='Persona.app/Info.plist', CONFIGURATION=configuration, **settings)
            run = subprocess.run(['bash', '-e', '-c', script], cwd=root / 'apple', env=env, capture_output=True,
                                 text=True, timeout=30)
            self.assertEqual(run.returncode, 0, run.stdout + run.stderr)
            return head, before, plist.read_bytes()

    def test_release_stamps_the_commit_and_debug_leaves_the_plist_untouched(self):
        for target in ('Persona', 'PersonaVision'):
            for dirty in (False, True):
                with self.subTest(target=target, dirty=dirty):
                    head, _, after = self.stamp(target, 'Release', dirty)
                    self.assertEqual(plistlib.loads(after)['PersonaCommit'], head + ('+' if dirty else ''))
                    _, before, after = self.stamp(target, 'Debug', dirty)
                    self.assertEqual(after, before)
                    # A Debug build that must name its commit (the README's captures) asks for the stamp.
                    head, _, after = self.stamp(target, 'Debug', dirty, PERSONA_STAMP_COMMIT='YES')
                    self.assertEqual(plistlib.loads(after)['PersonaCommit'], head + ('+' if dirty else ''))


if __name__ == '__main__':
    unittest.main()
