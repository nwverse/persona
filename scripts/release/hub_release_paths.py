"""Constrain private Hub feeds to real directories below the release root."""
from pathlib import Path
import re
import stat


def release_directory(root, namespace='hub'):
    if not isinstance(namespace, str) or not re.fullmatch(r'hub(?:-[a-z0-9][a-z0-9-]{0,62})?', namespace):
        raise ValueError('release_namespace must be hub or a named hub feed')
    return Path(root) / 'releases' / namespace


def _directories(root, namespace):
    root = Path(root)
    if not root.is_absolute() or '..' in root.parts:
        raise ValueError('Release root must be an absolute path without traversal')
    selected = release_directory(root, namespace)
    return list(dict.fromkeys([root, root / 'releases', release_directory(root), selected]))


def _staged_path(selected, staged):
    staged = Path(staged)
    if not staged.is_absolute() or '..' in staged.parts or staged.parent != selected:
        raise ValueError('Staging directory must be a direct child of the selected feed')
    return staged


def validate_release_paths(root, namespace='hub', staged=None, create=False):
    """Check each directory without following links; allow canonical root ancestors."""
    directories = _directories(root, namespace)
    canonical_root = None
    for index, directory in enumerate(directories):
        try:
            metadata = directory.lstat()
        except FileNotFoundError:
            if not create or index == 0:
                raise
            directory.mkdir(mode=0o700)
            metadata = directory.lstat()
        if not stat.S_ISDIR(metadata.st_mode):
            raise ValueError(f'Release path must be a non-symlink directory: {directory}')
        canonical = directory.resolve(strict=True)
        if canonical_root is None:
            canonical_root = canonical
        elif canonical != canonical_root / directory.relative_to(directories[0]):
            raise ValueError(f'Release path escapes its root: {directory}')
    if staged is not None:
        staged = _staged_path(release_directory(root, namespace), staged)
        if not stat.S_ISDIR(staged.lstat().st_mode):
            raise ValueError('Staging path must be a non-symlink directory')
        if staged.resolve(strict=True) != canonical_root / staged.relative_to(directories[0]):
            raise ValueError('Staging directory escapes its selected feed')
    return release_directory(root, namespace)


def validate_remote_release_paths(sftp, root, namespace='hub', staged=None, create=False):
    """Apply the same boundaries before any SFTP write."""
    directories = _directories(root, namespace)
    canonical_root = None
    for index, directory in enumerate(directories):
        path = str(directory)
        try:
            metadata = sftp.lstat(path)
        except FileNotFoundError:
            if not create or index == 0:
                raise
            sftp.mkdir(path, mode=0o700)
            metadata = sftp.lstat(path)
        if not stat.S_ISDIR(metadata.st_mode):
            raise ValueError(f'Remote release path must be a non-symlink directory: {directory}')
        canonical = Path(sftp.normalize(path))
        if not canonical.is_absolute():
            raise ValueError('Remote release path must resolve to an absolute directory')
        if canonical_root is None:
            canonical_root = canonical
        elif canonical != canonical_root / directory.relative_to(directories[0]):
            raise ValueError(f'Remote release path escapes its root: {directory}')
    selected = release_directory(root, namespace)
    if staged is not None:
        staged = _staged_path(selected, staged)
        if not stat.S_ISDIR(sftp.lstat(str(staged)).st_mode):
            raise ValueError('Remote staging path must be a non-symlink directory')
        if Path(sftp.normalize(str(staged))) != canonical_root / staged.relative_to(directories[0]):
            raise ValueError('Remote staging directory escapes its selected feed')
    return selected


def prepare_remote_staging(sftp, root, namespace, staged):
    selected = validate_remote_release_paths(sftp, root, namespace, create=True)
    staged = _staged_path(selected, staged)
    # A fresh directory prevents a previous upload from supplying linked files.
    sftp.mkdir(str(staged), mode=0o700)
    validate_remote_release_paths(sftp, root, namespace, staged)


def validate_remote_upload(sftp, root, namespace, staged, name):
    if not name or Path(name).name != name or name in {'.', '..'}:
        raise ValueError('Invalid artifact name')
    validate_remote_release_paths(sftp, root, namespace, staged)
    destination = str(Path(staged) / name)
    try:
        sftp.lstat(destination)
    except FileNotFoundError:
        return destination
    raise ValueError('Remote upload destination must not already exist')
