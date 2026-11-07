from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock

from hub_release_paths import (release_directory, validate_release_paths,
                               validate_remote_release_paths, prepare_remote_staging,
                               validate_remote_upload)


class ReleaseDirectoryTests(unittest.TestCase):
    def test_existing_feed_is_the_default(self):
        self.assertEqual(release_directory('/example'), Path('/example/releases/hub'))

    def test_named_feed_is_contained(self):
        self.assertEqual(release_directory('/example', 'hub-canonical-main'),
                         Path('/example/releases/hub-canonical-main'))

    def test_invalid_and_traversal_names_are_rejected(self):
        for name in ['', None, '../hub', 'hub/other', '/hub', 'other', 'hub-',
                     'hub-UPPER', 'hub-new\n', 'hub-' + 'a' * 65]:
            with self.subTest(name=name), self.assertRaises(ValueError):
                release_directory('/example', name)



class ReleasePathBoundaryTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.base = Path(self.temporary.name)
        self.root = self.base / 'runtime'
        self.root.mkdir()
        self.feed = self.root / 'releases/hub-main'
        self.staged = self.feed / '.upload-test'
        self.staged.mkdir(parents=True)
        (self.root / 'releases/hub').mkdir()

    def sftp(self):
        client = Mock()
        client.lstat.side_effect = lambda path: Path(path).lstat()
        client.normalize.side_effect = lambda path: str(Path(path).resolve(strict=True))
        client.mkdir.side_effect = lambda path, mode: Path(path).mkdir(mode=mode)
        return client

    def test_canonical_root_ancestors_are_supported(self):
        alias = self.base / 'alias'
        alias.symlink_to(self.base, target_is_directory=True)
        root = alias / 'runtime'
        self.assertEqual(validate_release_paths(root, 'hub-main', root / 'releases/hub-main/.upload-test'),
                         root / 'releases/hub-main')

    def test_each_directory_rejects_symlinks_to_internal_and_external_targets(self):
        for relative in ['.', 'releases', 'releases/hub', 'releases/hub-main',
                         'releases/hub-main/.upload-test']:
            for internal in [True, False]:
                with self.subTest(path=relative, internal=internal):
                    path = self.root / relative
                    moved = path.with_name(path.name + '-real')
                    path.rename(moved)
                    target = moved if internal else self.base
                    path.symlink_to(target, target_is_directory=True)
                    try:
                        with self.assertRaises(ValueError):
                            validate_release_paths(self.root, 'hub-main', self.staged)
                        with self.assertRaises(ValueError):
                            validate_remote_release_paths(self.sftp(), self.root, 'hub-main', self.staged)
                    finally:
                        path.unlink()
                        moved.rename(path)

    def test_staged_must_be_a_real_direct_child_of_selected_feed(self):
        for staged in [self.feed, self.feed / 'nested/upload',
                       self.root / 'releases/hub/.upload-test',
                       self.feed / '../hub/.upload-test']:
            with self.subTest(staged=staged), self.assertRaises(ValueError):
                validate_release_paths(self.root, 'hub-main', staged)

    def test_file_and_dangling_link_are_not_created_over(self):
        path = self.root / 'releases/hub'
        path.rmdir()
        for linked in [False, True]:
            with self.subTest(linked=linked):
                if linked:
                    path.symlink_to(self.base / 'missing')
                else:
                    path.write_text('occupied')
                try:
                    with self.assertRaises(ValueError):
                        validate_release_paths(self.root, 'hub-main', self.staged, create=True)
                finally:
                    path.unlink()

    def test_remote_normalized_sibling_and_external_paths_are_rejected(self):
        client = self.sftp()
        for target in [self.root / 'releases/hub', self.base / 'runtime-evil/hub-main']:
            client.normalize.side_effect = lambda path: str(target if Path(path) == self.feed
                                                           else Path(path).resolve(strict=True))
            with self.subTest(target=target), self.assertRaises(ValueError):
                validate_remote_release_paths(client, self.root, 'hub-main', self.staged)

    def test_remote_staging_is_fresh_and_creates_shared_lock_directory(self):
        (self.root / 'releases/hub').rmdir()
        staged = self.feed / '.upload-new'
        prepare_remote_staging(self.sftp(), self.root, 'hub-main', staged)
        self.assertTrue((self.root / 'releases/hub').is_dir())
        self.assertTrue(staged.is_dir())
        with self.assertRaises(FileExistsError):
            prepare_remote_staging(self.sftp(), self.root, 'hub-main', staged)

    def test_remote_upload_rejects_existing_link_and_rechecks_staging(self):
        destination = self.staged / 'latest.json'
        destination.symlink_to(self.base / 'outside')
        with self.assertRaisesRegex(ValueError, 'already exist'):
            validate_remote_upload(self.sftp(), self.root, 'hub-main', self.staged, 'latest.json')
        destination.unlink()
        moved = self.staged.with_name('.upload-moved')
        self.staged.rename(moved)
        self.staged.symlink_to(moved, target_is_directory=True)
        with self.assertRaisesRegex(ValueError, 'non-symlink'):
            validate_remote_upload(self.sftp(), self.root, 'hub-main', self.staged, 'latest.json')


if __name__ == '__main__':
    unittest.main()
