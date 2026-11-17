#!/usr/bin/env python3
"""One-off pure Windows lexical-path fixture; NOT a native Windows/Node run.

Copies the unchanged resolver and Windows tests into a temporary module. Only
its filepath import is redirected to lexical Go standard-library functions
selected from the installed Windows sources. No OS/IO/permission operations are
emulated. Native tests remain the authoritative target-platform regression.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--repo', type=Path, required=True)
parser.add_argument('--work', type=Path, required=True)
args = parser.parse_args()
repo, work = args.repo.resolve(), args.work.resolve()
goro = Path(subprocess.check_output(['go', 'env', 'GOROOT'], text=True).strip())
module = 'github.com/nwverse/persona/server/reviewfixture'
work.mkdir(parents=True, exist_ok=False)
(work / 'go.mod').write_text(f'module {module}\n\ngo 1.27.3\n\nrequire github.com/nwverse/persona/server v0.0.0\n\nreplace github.com/nwverse/persona/server => {repo / "server"}\n')
shutil.copy(repo / 'server/go.sum', work / 'go.sum')
inputs = {}

def read(path):
    text = path.read_text()
    key = str(path.relative_to(goro)) if path.is_relative_to(goro) else str(path.relative_to(repo))
    inputs[key] = hashlib.sha256(path.read_bytes()).hexdigest()
    return text

def function(source, name):
    match = re.search(r'(?m)^func (?:\([^\n]+\) )?' + re.escape(name) + r'\([^\n]*[\s\S]*?^}', source)
    if not match:
        raise ValueError(f'missing Go function: {name}')
    return match.group(0)

lite = read(goro / 'src/internal/filepathlite/path.go')
winlite = read(goro / 'src/internal/filepathlite/path_windows.go')
common = read(goro / 'src/path/filepath/path.go')
win = read(goro / 'src/path/filepath/path_windows.go')
parts = [re.search(r'(?m)^type lazybuf struct \{[\s\S]*?^}', lite).group(0)]
parts += [function(lite, name) for name in ('index', 'append', 'prepend', 'string', 'Clean', 'ToSlash', 'FromSlash', 'replaceStringByte', 'VolumeName')]
parts += [function(winlite, name) for name in ('IsPathSeparator', 'IsAbs', 'volumeNameLen', 'validVolumeNameLen', 'pathHasPrefixFold', 'toUpper', 'uncLen', 'cutPath', 'postClean')]
parts += [function(common, name) for name in ('Join', 'Rel')]
parts += [function(win, name) for name in ('join', 'sameWord')]
source = '\n\n'.join(parts)
# Mechanical import substitutions preserve the selected Windows algorithms.
source = source.replace('stringslite.', 'strings.').replace('filepathlite.VolumeNameLen(', 'volumeNameLen(').replace('os.IsPathSeparator(', 'IsPathSeparator(')
source = source.replace('bytealg.CountString(', 'countByte(')
header = '''// Copyright 2024 The Go Authors. All rights reserved.
// BSD-3-Clause; copied lexical functions only. See GO-LICENSE.
package filepath
import ("errors"; "strings"; "slices")
const Separator = '\\\\'
func countByte(s string, c byte) int { return strings.Count(s, string(c)) }
'''
(work / 'windowspath').mkdir()
(work / 'windowspath/path.go').write_text(header + source + '\n')
shutil.copy(goro / 'LICENSE', work / 'GO-LICENSE')
(work / 'naming').mkdir()
naming = read(repo / 'server/internal/workspacenaming/naming.go')
assert naming.count('"path/filepath"') == 1
(work / 'naming/naming.go').write_text(naming.replace('"path/filepath"', f'"{module}/windowspath"'))
tests = read(repo / 'server/internal/workspacenaming/naming_windows_test.go')
(work / 'naming/path_fixture_test.go').write_text(tests)
manifest = {'kind': 'pure lexical Windows path fixture on Linux, not native Windows', 'goVersion': subprocess.check_output(['go', 'version'], text=True).strip(), 'inputsSHA256': inputs, 'resolverChange': 'filepath import only', 'testChange': 'filename only, to execute the Windows table in the fixture', 'pathFunctionChanges': ['stringslite -> strings', 'filepathlite.VolumeNameLen -> same copied volumeNameLen', 'os.IsPathSeparator -> same copied Windows IsPathSeparator', 'bytealg.CountString -> strings.Count with a one-byte separator'], 'excluded': ['Windows kernel and filesystem', 'Node authorization and RPC', 'Git', 'OS path permissions and symlinks']}
(work / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
subprocess.run(['gofmt', '-w', str(work / 'windowspath/path.go')], check=True)
print(json.dumps(manifest, indent=2), flush=True)
subprocess.run(['go', '-C', str(work), 'test', '-mod=mod', './naming', '-run', '^TestWindowsWorkspaceNaming', '-v', '-count=1'], check=True)
