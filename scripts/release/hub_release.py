#!/usr/bin/env python3
"""Run a serialized Hub validation/build/publication cycle from a configured source."""
import argparse
import datetime as dt
import fcntl
import json
import os
import re
from pathlib import Path
import subprocess
import time
import shutil
import sys

from node_artifacts import node_manifest


def run(args, cwd, **kwargs):
    return subprocess.run(args, cwd=cwd, check=True, **kwargs)


def output(args, cwd):
    return subprocess.check_output(args, cwd=cwd, text=True).strip()


def atomic_json(path, value):
    temp = path.with_suffix('.tmp')
    temp.write_text(json.dumps(value, indent=2) + '\n')
    os.replace(temp, path)


def cache_ready(attempt, publish_nodes):
    latest = Path(attempt) / 'latest.json'
    if not latest.exists():
        return False
    if not publish_nodes:
        return True
    return node_manifest(attempt, json.loads(latest.read_text())) is not None




def repository_identity(remote):
    match = re.fullmatch(
        r'(?:https://github\.com/|ssh://git@github\.com/|git@github\.com:)'
        r'([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+?)(?:\.git)?/?', remote)
    if not match:
        raise ValueError('origin must be a canonical GitHub repository URL')
    return match.group(1).casefold()

def full_commit(value):
    if not re.fullmatch(r'[0-9a-f]{40}', value):
        raise argparse.ArgumentTypeError('commit must be a full lowercase Git SHA')
    return value

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', type=Path, required=True)
    parser.add_argument('--retry', action='store_true')
    parser.add_argument('--commit', type=full_commit,
                        help='pin a full commit that must belong to the configured source branch')
    parser.add_argument('--validate-only', action='store_true',
                        help='run complete build gates without publishing')
    args = parser.parse_args()
    cfg = json.loads(args.config.read_text())
    checkout, state = Path(cfg['checkout']), Path(cfg['state_dir'])
    branch = cfg.get('source_branch', 'main')
    source_ref = f'refs/heads/{branch}'
    tracking_ref = f'refs/remotes/origin/{branch}'
    run(['git', 'check-ref-format', source_ref], checkout)
    origin = output(['git', 'remote', 'get-url', 'origin'], checkout)
    if repository_identity(origin) != cfg['repository'].casefold():
        raise ValueError('Configured status repository does not match source origin')
    state.mkdir(parents=True, exist_ok=True)
    with (state / 'lock').open('w') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            print('Another Hub release owns the publication window.')
            return
        run(['git', 'fetch', '--no-tags', 'origin', f'{source_ref}:{tracking_ref}'], checkout)
        sha = args.commit or output(['git', 'rev-parse', tracking_ref], checkout)
        if args.commit:
            run(['git', 'merge-base', '--is-ancestor', sha, tracking_ref], checkout)
        record = state / 'last.json'
        previous = json.loads(record.read_text()) if record.exists() else {}
        source_matches = (previous.get('repository') == cfg['repository'] and
                          previous.get('source_branch') == branch and
                          previous.get('update_url') == cfg['update_url'])
        same = source_matches and previous.get('sha') == sha
        requirements_match = previous.get('publish_nodes', False) == cfg.get('publish_nodes', False)
        if same and not args.retry and requirements_match:
            success_ready = (previous.get('result') == 'success' and
                             (not cfg.get('publish_nodes', False) or
                              cache_ready(previous.get('evidence', ''), True)))
            if success_ready or time.time() < previous.get('retry_after', 0):
                print(f'{sha}: {previous["result"]}; no new attempt needed')
                return
        if output(['git', 'status', '--porcelain', '--untracked-files=no'], checkout):
            raise RuntimeError('Release checkout has tracked changes')
        run(['git', 'checkout', '--detach', sha], checkout)
        now = dt.datetime.now(dt.timezone.utc).replace(second=0, microsecond=0)
        missing_required_cache = (same and cfg.get('publish_nodes', False) and
                                  previous.get('evidence') and
                                  (Path(previous['evidence']) / 'latest.json').exists() and
                                  not cache_ready(previous['evidence'], True))
        if same and requirements_match and not missing_required_cache:
            build = previous['build']
        else:
            last = dt.datetime.strptime(previous.get('build', '200001010000'), '%Y%m%d%H%M').replace(tzinfo=dt.timezone.utc)
            build = max(now, last + dt.timedelta(minutes=1)).strftime('%Y%m%d%H%M')
        attempt = state / f'{build}-{sha[:12]}'
        attempt.mkdir(exist_ok=True)
        result = {'repository': cfg['repository'], 'source_branch': branch,
                  'update_url': cfg['update_url'],
                  'sha': sha, 'build': build, 'result': 'pending', 'evidence': str(attempt),
                  'publish_nodes': cfg.get('publish_nodes', False)}
        atomic_json(record, result)

        def status(value, description):
            run(['gh', 'api', f'repos/{cfg["repository"]}/statuses/{sha}',
                 '-f', f'state={value}', '-f', 'context=hub-release/validation' if args.validate_only else 'context=hub-release/worker',
                 '-f', f'description={description}'], checkout, stdout=subprocess.DEVNULL)

        try:
            status('pending', 'Validating release' if args.validate_only else 'Validating and publishing Hub-only release')
            with (attempt / 'validation.log').open('a') as log:
                if not cache_ready(attempt, cfg.get('publish_nodes', False)):
                    command = [sys.executable, 'scripts/release/build_hub_release.py', '--out', str(attempt),
                               '--build', build, '--update-url', cfg['update_url']]
                    if cfg.get('publish_nodes', False):
                        command.append('--publish-nodes')
                    run(command, checkout, stdout=log, stderr=subprocess.STDOUT)
            # A force-push or branch replacement must never publish an orphan.
            run(['git', 'fetch', '--no-tags', 'origin', f'{source_ref}:{tracking_ref}'], checkout)
            run(['git', 'merge-base', '--is-ancestor', sha, tracking_ref], checkout)
            if args.validate_only:
                result['result'] = 'validated'
                status('success', 'Complete release validation passed; publication was not requested')
                return
            with (attempt / 'publication.log').open('a') as log:
                run([*cfg['publish_command'], '--artifact-dir', str(attempt)], checkout,
                    stdout=log, stderr=subprocess.STDOUT)
            result['result'] = 'success'
            status('success', 'Validated Hub release installed and health confirmed')
        except (subprocess.CalledProcessError, OSError, RuntimeError):
            result['result'] = 'failure'
            result['retry_after'] = time.time() + 900
            status('failure', 'Hub release failed; see publisher validation logs')
            raise
        finally:
            atomic_json(record, result)
        # Retain recent attempts for diagnosis; installed/rollback binaries live on the Hub.
        attempts = sorted((p for p in state.iterdir() if p.is_dir() and (p / 'validation.log').exists()), key=lambda p: p.name)
        for old in attempts[:-5]:
            shutil.rmtree(old)


if __name__ == '__main__':
    main()
