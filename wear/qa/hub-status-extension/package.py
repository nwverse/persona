#!/usr/bin/env python3
"""Build an optional owner-installed provider; never contact or modify a Hub."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import zipfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--go', default='go')
parser.add_argument('--out', required=True)
args = parser.parse_args()
source = Path(__file__).resolve().parent
manifest = json.loads((source / 'persona-extension.json').read_text(encoding='utf-8'))
build = source / 'build'
env = os.environ.copy()
env.update(CGO_ENABLED='0', GOTOOLCHAIN='local')
go_root = Path(subprocess.check_output([args.go, 'env', 'GOROOT'], env=env, text=True).strip())
programs = []
for platform, relative in manifest['runtime']['command'].items():
    target = build / relative
    target.parent.mkdir(parents=True, exist_ok=True)
    env['GOOS'], env['GOARCH'] = platform.split('-')
    subprocess.run([args.go, 'build', '-buildvcs=false', '-trimpath', '-ldflags=-s -w', '-o', str(target), '.'], cwd=source, env=env, check=True)
    programs.append((target, relative))
output = Path(args.out).resolve()
output.parent.mkdir(parents=True, exist_ok=True)
with zipfile.ZipFile(output, 'w', compression=zipfile.ZIP_DEFLATED) as archive:
    for file, relative in programs + [(source / 'persona-extension.json', 'persona-extension.json'),
                                      (source / 'persona-extension.json', 'persona-plugin.json'),
                                      (source / 'README.md', 'README.md'),
                                      (source.parents[2] / 'LICENSE', 'LICENSE'),
                                      (go_root / 'LICENSE', 'THIRD_PARTY_LICENSES/Go-LICENSE')]:
        info = zipfile.ZipInfo(relative, date_time=(2026, 1, 1, 0, 0, 0))
        info.create_system = 3
        info.external_attr = ((0o100755 if relative.startswith('bin/') else 0o100644) << 16)
        info.compress_type = zipfile.ZIP_DEFLATED
        archive.writestr(info, file.read_bytes())
print(f'Prepared {output.name}; SHA-256 {hashlib.sha256(output.read_bytes()).hexdigest()}')
print('Not installed on a Hub. Owner preview and installation are separate.')
