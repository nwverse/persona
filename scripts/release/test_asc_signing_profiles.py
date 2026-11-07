"""Signing preflight and export use the same configured account resource names, without live Apple requests."""
import contextlib
import importlib.util
import io
from pathlib import Path
import sys
import types
import unittest
from unittest.mock import Mock, patch

ROOT = Path(__file__).resolve().parents[2]


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class SigningProfileTests(unittest.TestCase):
    def signing(self, cfg):
        config = load_module('signing_config_under_test', ROOT / 'scripts/persona_config.py')
        asc = types.ModuleType('asc')
        asc.CONFIG = cfg
        asc.BUNDLE_ID = cfg['app']['bundle_id']
        asc.APIError = RuntimeError
        asc.create = Mock(side_effect=AssertionError('no live Apple writes'))
        asc.get_all = Mock(side_effect=AssertionError('no live Apple reads'))
        asc.request = Mock(side_effect=AssertionError('no live Apple requests'))
        with patch.dict(sys.modules, {'asc': asc, 'persona_config': config}), patch.object(sys, 'path', list(sys.path)):
            return load_module('signing_under_test', ROOT / 'scripts/release/asc_signing.py'), config, asc

    def test_check_uses_export_mapping_without_account_writes(self):
        bid = 'com.example.persona'
        names = {bid: 'Existing Production', bid + '.notify': 'Notification Distribution'}
        cfg = {'app': {'bundle_id': bid}, 'app_store': {'provisioning_profiles': names}}
        signing, config, asc = self.signing(cfg)
        self.assertEqual({key: values[1] for key, values in signing.TARGETS.items()},
                         config.provisioning_profiles(cfg))
        with patch.object(signing, 'certificate', return_value='certificate'), \
                patch.object(signing, 'bundle_id', side_effect=lambda identifier, *_: identifier), \
                patch.object(signing, 'ensure_capability'), patch.object(signing, 'profile') as profile, \
                patch.object(sys, 'argv', ['asc_signing.py', '--check']), contextlib.redirect_stdout(io.StringIO()):
            signing.main()
        self.assertEqual(profile.call_count, 6)
        for call in profile.call_args_list:
            self.assertEqual(call.args[1], config.provisioning_profiles(cfg)[call.args[0]])
            self.assertIs(call.kwargs['check_only'], True)
        asc.create.assert_not_called()
        asc.request.assert_not_called()
        asc.get_all.assert_not_called()

    def test_custom_development_name_never_reuses_the_store_profile(self):
        bid = 'com.example.persona'
        signing, _, _ = self.signing({'app': {'bundle_id': bid}, 'app_store': {
            'provisioning_profiles': {bid + '.share': 'Share Production'}}})
        with patch.object(signing, 'certificate', side_effect=['distribution-certificate', 'development-certificate']), \
                patch.object(signing, 'bundle_id', return_value='share-resource'), \
                patch.object(signing, 'ensure_capability'), patch.object(signing, 'profile') as profile, \
                patch.object(sys, 'argv', ['asc_signing.py', '--check', '--dev', '--only', bid + '.share']), \
                contextlib.redirect_stdout(io.StringIO()):
            signing.main()
        self.assertEqual(profile.call_count, 2)
        self.assertEqual(profile.call_args_list[0].args[1], 'Share Production')
        self.assertEqual(profile.call_args_list[1].args, ('share-resource', 'Share Production Development',
                                                        'development-certificate', 'IOS_APP_DEVELOPMENT', True))

    def test_only_retains_the_other_targets(self):
        bid = 'com.example.persona'
        signing, _, _ = self.signing({'app': {'bundle_id': bid}, 'app_store': {
            'provisioning_profiles': {bid + '.share': 'Share Production'}}})
        with patch.object(signing, 'certificate', return_value='certificate'), \
                patch.object(signing, 'bundle_id', return_value='share-resource'), \
                patch.object(signing, 'ensure_capability'), patch.object(signing, 'profile') as profile, \
                patch.object(sys, 'argv', ['asc_signing.py', '--check', '--only', bid + '.share']), \
                contextlib.redirect_stdout(io.StringIO()):
            signing.main()
        profile.assert_called_once_with('share-resource', 'Share Production', 'certificate',
                                        check_only=True, group=False)


if __name__ == '__main__':
    unittest.main()
