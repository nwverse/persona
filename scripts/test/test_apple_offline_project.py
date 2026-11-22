"""The offline simulator graph retains the real test host and only reachable targets."""

from copy import deepcopy
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import unittest

import yaml


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / 'scripts/ci/apple_offline_project.py'
sys.path.insert(0, str(ROOT / 'scripts/ci'))
import apple_offline_project


class AppleOfflineProjectTests(unittest.TestCase):
    def setUp(self):
        self.base = yaml.safe_load((ROOT / 'apple/project.yml').read_text())
        self.project = deepcopy(self.base)
        self.project.pop('include')
        self.project['schemes'] = {name: self.base['schemes'][name] for name in (
            'Persona', 'PersonaOfflineUnitTests', 'AboutVersionVisualTests',
            'AboutVersionAccessibilityHost', 'PersonaWatch', 'PersonaVision')}
        self.project['targets']['Persona']['dependencies'] = [
            {'target': 'PersonaWatch'}, {'target': 'PersonaShare'},
            *self.project['targets']['Persona']['dependencies'],
        ]
        self.result = apple_offline_project.filtered(self.project)

    def test_real_host_dependencies_survive_but_watch_and_share_do_not(self):
        dependencies = self.result['targets']['Persona']['dependencies']
        expected = self.project['targets']['Persona']['dependencies'][2:]
        self.assertEqual(dependencies, expected)
        self.assertIn({'target': 'PersonaWidgets'}, dependencies)
        self.assertIn({'target': 'PersonaNotify'}, dependencies)
        self.assertTrue(any('package' in dependency for dependency in dependencies))
        self.assertEqual(set(self.result['targets']), {
            'Persona', 'PersonaTests', 'PersonaWidgets', 'PersonaNotify',
        })
        self.assertEqual(set(self.result['schemes']), {'PersonaOfflineUnitTests'})
        self.assertEqual(self.result['schemes']['PersonaOfflineUnitTests'],
                         self.project['schemes']['PersonaOfflineUnitTests'])
        for name in ('PersonaWatch', 'PersonaWatchWidgets', 'PersonaShare', 'PersonaUITests'):
            self.assertNotIn(name, self.result['targets'])
            self.assertNotIn(name, dependencies)

    def test_test_host_and_loader_are_canonical(self):
        tests = self.result['targets']['PersonaTests']
        self.assertEqual(tests, self.base['targets']['PersonaTests'])
        self.assertIn({'target': 'Persona'}, tests['dependencies'])
        self.assertEqual(tests['settings']['base']['TEST_HOST'], '$(BUILT_PRODUCTS_DIR)/Persona.app/Persona')
        self.assertEqual(tests['settings']['base']['BUNDLE_LOADER'], '$(TEST_HOST)')
        self.assertEqual(self.result['packages'], self.project['packages'])

    def test_host_or_selection_changes_fail_closed(self):
        changed = deepcopy(self.project)
        changed['targets']['PersonaTests']['settings']['base']['TEST_HOST'] = 'another.app/Another'
        with self.assertRaisesRegex(ValueError, 'bundle loader'):
            apple_offline_project.filtered(changed)
        changed = deepcopy(self.project)
        changed['targets']['PersonaTests']['dependencies'] = []
        with self.assertRaisesRegex(ValueError, 'host dependency'):
            apple_offline_project.filtered(changed)
        changed = deepcopy(self.project)
        changed['schemes']['PersonaOfflineUnitTests']['test']['targets'] = []
        with self.assertRaisesRegex(ValueError, 'test selection'):
            apple_offline_project.filtered(changed)
        changed = deepcopy(self.project)
        changed['schemes']['PersonaOfflineUnitTests']['build']['targets']['Persona'] = 'none'
        with self.assertRaisesRegex(ValueError, 'build must retain'):
            apple_offline_project.filtered(changed)

    def test_cli_preserves_target_graph_in_the_generated_json(self):
        with tempfile.TemporaryDirectory() as directory:
            dump, output = Path(directory) / 'dump.json', Path(directory) / 'offline.json'
            dump.write_text(json.dumps(self.project))
            subprocess.run([sys.executable, '-I', str(SCRIPT), str(dump), str(output)],
                           check=True, capture_output=True, text=True, timeout=30)
            self.assertEqual(json.loads(output.read_text()), self.result)

    @unittest.skipUnless(shutil.which('xcodegen'), 'needs xcodegen')
    def test_xcodegen_materializes_only_the_real_host_and_its_two_extensions(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            shutil.copytree(ROOT / 'apple', root / 'source/apple',
                            ignore=shutil.ignore_patterns('*.xcodeproj', 'Config.xcconfig'))
            about = ROOT / 'android/refract/about/AboutVersionRow.swift'
            (root / 'source/android/refract/about').mkdir(parents=True)
            shutil.copy2(about, root / 'source/android/refract/about/AboutVersionRow.swift')
            (root / 'source/apple/Config.xcconfig').write_text('')
            environment = dict(os.environ, USER='fixture', PERSONA_CONFIG=str(root / 'config.json'))
            whole = root / 'whole.json'
            with whole.open('w') as output:
                subprocess.run(['xcodegen', 'dump', '--spec', str(root / 'source/apple/project.yml'),
                                '--type', 'json'], env=environment, check=True, stderr=subprocess.PIPE,
                               text=True, timeout=60, stdout=output)
            spec = root / 'offline.json'
            subprocess.run([sys.executable, '-I', str(SCRIPT), str(whole), str(spec)],
                           env=environment, check=True, capture_output=True, text=True, timeout=30)
            generated = subprocess.run(['xcodegen', 'generate', '--spec', str(spec), '--project-root',
                                       str(root / 'source/apple'), '--project', str(root / 'source/apple')],
                                      env=environment, capture_output=True, text=True, timeout=60)
            self.assertEqual(generated.returncode, 0, generated.stdout + generated.stderr)
            project = root / 'source/apple/PersonaOfflineUnitTests.xcodeproj'
            pbx = (project / 'project.pbxproj').read_text()
            native = pbx.split('/* Begin PBXNativeTarget section */', 1)[1].split(
                '/* End PBXNativeTarget section */', 1)[0]
            targets = re.findall(r'/\* (Persona\w*|AboutVersion\w*) \*/ = \{\n\s*isa = PBXNativeTarget;', native)
            self.assertEqual(sorted(targets), ['Persona', 'PersonaNotify', 'PersonaTests', 'PersonaWidgets'])
            copy = pbx.split('/* Begin PBXCopyFilesBuildPhase section */', 1)[1].split(
                '/* End PBXCopyFilesBuildPhase section */', 1)[0]
            for required in ('PersonaWidgets', 'PersonaNotify'):
                self.assertIn(required, copy)
            for excluded in ('PersonaWatch', 'PersonaShare'):
                self.assertNotIn(excluded, copy)
            self.assertIn('TEST_HOST = "$(BUILT_PRODUCTS_DIR)/Persona.app/Persona";', pbx)
            self.assertIn('BUNDLE_LOADER = "$(TEST_HOST)";', pbx)
            scheme = (project / 'xcshareddata/xcschemes/PersonaOfflineUnitTests.xcscheme').read_text()
            self.assertIn('BuildableName = "Persona.app"', scheme)
            self.assertIn('BuildableName = "PersonaTests.xctest"', scheme)
            self.assertNotIn('PersonaWatch', scheme)
            self.assertNotIn('PersonaUITests', scheme)


if __name__ == '__main__':
    unittest.main()
