import base64
import copy
import unittest
from promote_hub_release import validate_next


class ReleaseOrderTests(unittest.TestCase):
    def setUp(self):
        self.old = {'build':202611070001, 'commit':'abcdef123456'+'0'*28}
        self.new = {'build':202611070002, 'commit':'0123456789ab'+'0'*28,
                    'ancestors':base64.b64encode(bytes.fromhex('abcdef1234560123456789ab')).decode(),
                    'files':{'linux-amd64':{}}}

    def test_newer_descendant(self):
        validate_next(self.old,self.new)

    def test_out_of_order_or_same_number(self):
        for number in [202611070000,202611070001]:
            with self.subTest(number=number), self.assertRaises(ValueError):
                self.new['build']=number
                validate_next(self.old,self.new)

    def test_other_branch_and_malformed_ancestry(self):
        for data in [b'xxxxxx',b'abcdefg']:
            with self.subTest(data=data), self.assertRaises(ValueError):
                self.new['ancestors']=base64.b64encode(data).decode()
                validate_next(self.old,self.new)

    def test_no_other_platform_or_programs(self):
        for key,value in [('files',{'darwin-arm64':{}}),('programs',{'other':{}})]:
            with self.subTest(key=key),self.assertRaises(ValueError):
                changed=copy.deepcopy(self.new);changed[key]=value
                validate_next(None,changed)

    def test_identical_publication_can_be_reconciled(self):
        validate_next(self.new,self.new)

class WorkerRecoveryTests(unittest.TestCase):
    def test_interrupted_publication_reuses_build_and_validated_artifact(self):
        import json
        from pathlib import Path
        import tempfile
        from unittest.mock import patch
        import hub_release
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            state = root / 'state'
            state.mkdir()
            sha = 'a' * 40
            build = '202611070001'
            attempt = state / f'{build}-{sha[:12]}'
            attempt.mkdir()
            (attempt / 'latest.json').write_text('{}')
            (attempt / 'validation.log').write_text('passed\n')
            (state / 'last.json').write_text(json.dumps({'sha':sha,'build':build,'result':'pending',
                'repository':'example/project','source_branch':'main','update_url':'file:///example'}))
            cfg = root / 'config.json'
            cfg.write_text(json.dumps({'checkout':directory,'state_dir':str(state),
                                       'repository':'example/project','update_url':'file:///example',
                                       'publish_command':['publisher']}))
            calls = []
            def output(argv, cwd):
                return sha if argv[1] == 'rev-parse' else ('https://github.com/example/project.git' if argv[1] == 'remote' else '')
            def run(argv, cwd, **kwargs):
                calls.append(argv)
            with patch('sys.argv',['hub_release','--config',str(cfg)]), patch.object(hub_release,'output',output), patch.object(hub_release,'run',run):
                hub_release.main()
            self.assertFalse(any('build_hub_release.py' in ' '.join(c) for c in calls))
            self.assertTrue(any(c[0]=='publisher' for c in calls))
            self.assertEqual(json.loads((state/'last.json').read_text())['build'],build)
            self.assertEqual((attempt/'validation.log').read_text(),'passed\n')

    def test_failed_validation_never_invokes_publisher(self):
        import json
        from pathlib import Path
        import subprocess
        import tempfile
        from unittest.mock import patch
        import hub_release
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            cfg = root / 'config.json'
            cfg.write_text(json.dumps({'checkout':directory,'state_dir':str(root/'state'),
                                       'repository':'example/project','update_url':'file:///example',
                                       'publish_command':['publisher']}))
            calls = []
            def run(argv,cwd,**kwargs):
                calls.append(argv)
                if 'scripts/release/build_hub_release.py' in argv:
                    raise subprocess.CalledProcessError(1,argv)
            with patch('sys.argv',['hub_release','--config',str(cfg)]), patch.object(hub_release,'output',lambda argv,cwd:'b'*40 if argv[1]=='rev-parse' else ('https://github.com/example/project.git' if argv[1]=='remote' else '')), patch.object(hub_release,'run',run):
                with self.assertRaises(subprocess.CalledProcessError):
                    hub_release.main()
            self.assertFalse(any(c[0]=='publisher' for c in calls))
            result = json.loads((root/'state/last.json').read_text())
            self.assertEqual(result['result'],'failure')
            self.assertGreater(result['retry_after'],0)


class NodeCacheTests(unittest.TestCase):
    def test_hub_only_cache_does_not_satisfy_node_publication(self):
        import json
        import tempfile
        from pathlib import Path
        from hub_release import cache_ready
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'latest.json').write_text(json.dumps({'version': 'fixture'}))
            self.assertTrue(cache_ready(root, False))
            self.assertFalse(cache_ready(root, True))

    def test_enabling_nodes_revisits_successful_hub_only_release(self):
        import json
        import tempfile
        from pathlib import Path
        from unittest.mock import patch
        import hub_release
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            state = root / 'state'
            state.mkdir()
            sha, build = 'a' * 40, '202611070001'
            attempt = state / f'{build}-{sha[:12]}'
            attempt.mkdir()
            (attempt / 'latest.json').write_text('{}')
            (state / 'last.json').write_text(json.dumps({'sha': sha, 'build': build,
                'result': 'success', 'evidence': str(attempt)}))
            config = root / 'config.json'
            config.write_text(json.dumps({'checkout': directory, 'state_dir': str(state),
                'repository': 'example/project', 'update_url': 'file:///fixture',
                'publish_command': ['publisher'], 'publish_nodes': True}))
            calls = []
            with patch('sys.argv', ['worker', '--config', str(config)]), \
                    patch.object(hub_release, 'output', lambda args, cwd: sha if args[1] == 'rev-parse' else ('https://github.com/example/project.git' if args[1] == 'remote' else '')), \
                    patch.object(hub_release, 'run', lambda args, cwd, **kwargs: calls.append(args)):
                hub_release.main()
            builds = [args for args in calls if 'scripts/release/build_hub_release.py' in args]
            self.assertEqual(len(builds), 1)
            self.assertIn('--publish-nodes', builds[0])
            self.assertTrue(any(args[0] == 'publisher' for args in calls))


if __name__ == '__main__':
    unittest.main()
