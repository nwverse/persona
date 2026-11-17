#!/usr/bin/env python3
"""Count Android literal animation specs per file and reject new ones above a reviewed baseline."""
import argparse
import json
import re
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from android_token_baseline import MODULE_GROUPS, ROOT, code_only  # noqa: E402

BASELINE = ROOT / 'android/core/designsystem/tools/motion-baseline.json'
GENERATED = {'android/core/designsystem/src/main/kotlin/persona/core/designsystem/Tokens.kt'}
# Specs that already read Tokens.Motion / SharedMotion / the style's PersonaMotion are not literals.
TOKENISH = re.compile(
    r'Tokens\.Motion|IosSpring\(|IosMassSpring\(|SharedMotion\.|ComposerMotion\.|Motions\.of|'
    r'PersonaTheme\.motion|motion\.(enter|size|quick|standard|count|breathe|state|layout)|'
    r'PageTransitionTokens|sidebarColumnSpec|drawerSpec|columnDragSettleSpring|ColumnDrag\.'
)
TWEEN_NUM = re.compile(r'\btween\s*\(\s*(?:duration\s*=\s*)?\d')
SPRING_LIT = re.compile(
    r'\bspring\s*\(\s*(?:[^)]*?(?:dampingRatio|stiffness)\s*=\s*(?:[\d.]+|Spring\.))'
)
KEYFRAMES = re.compile(r'\bkeyframes\s*\(')
SNAP_NUM = re.compile(r'\bsnap\s*\(\s*\d')
ANIMATED_VISIBILITY = re.compile(r'\bAnimatedVisibility\s*\(')
CROSSFADE = re.compile(r'\bCrossfade\s*\(')


def matching_paren(code, open_idx):
    depth = 0
    i = open_idx
    while i < len(code):
        if code[i] == '(':
            depth += 1
        elif code[i] == ')':
            depth -= 1
            if depth == 0:
                return i
        i += 1
    return len(code) - 1


def literal_hits(code):
    """Each literal animation spec: (line, kind)."""
    hits = []
    for pattern, kind in (
        (TWEEN_NUM, 'tween'),
        (SPRING_LIT, 'spring'),
        (KEYFRAMES, 'keyframes'),
        (SNAP_NUM, 'snap'),
    ):
        for match in pattern.finditer(code):
            start = match.start()
            end = matching_paren(code, code.find('(', start))
            snip = code[start:end + 1]
            if TOKENISH.search(snip):
                continue
            hits.append((code.count('\n', 0, start) + 1, kind))
    for match in ANIMATED_VISIBILITY.finditer(code):
        start = match.start()
        # Args before the trailing lambda: a default AnimatedVisibility has neither enter= nor exit=.
        chunk = code[start:start + 600]
        head = chunk.split('{', 1)[0]
        if re.search(r'\b(enter|exit)\s*=', head):
            continue
        hits.append((code.count('\n', 0, start) + 1, 'AnimatedVisibility-default'))
    for match in CROSSFADE.finditer(code):
        start = match.start()
        chunk = code[start:start + 400]
        head = chunk.split('{', 1)[0]
        if 'animationSpec' in head:
            continue
        hits.append((code.count('\n', 0, start) + 1, 'Crossfade-default'))
    return hits


def count(source):
    return len(literal_hits(code_only(source)))


def scan(root):
    files = {}
    for group in MODULE_GROUPS:
        base = root / 'android' / group
        if not base.is_dir():
            continue
        candidates = [base] if (base / 'src').is_dir() else sorted(base.iterdir())
        for module in candidates:
            if not module.is_dir() or not (module / 'src').is_dir():
                continue
            for source_set in sorted((module / 'src').iterdir()):
                if 'test' in source_set.name.lower() or not source_set.is_dir():
                    continue
                for path in sorted(source_set.rglob('*.kt')):
                    name = path.relative_to(root).as_posix()
                    if name in GENERATED:
                        continue
                    n = count(path.read_text())
                    if n:
                        files[name] = n
    return {'version': 1, 'files': files}


def baseline_of(current):
    return {'version': current['version'], 'files': dict(sorted(current['files'].items()))}


def regressions(current, baseline):
    failures = []
    for path, actual in current['files'].items():
        limit = baseline['files'].get(path, 0)
        if actual > limit:
            failures.append(f'{path}: {actual} literal animation specs, baseline {limit}')
    for path in baseline['files']:
        if path not in current['files'] and baseline['files'][path]:
            # A file that disappeared is fine (deleted or cleared); only rising counts fail.
            pass
    return failures


def reduction_hint(current, baseline):
    total_now = sum(current['files'].values())
    total_base = sum(baseline['files'].values())
    return (
        f'literal animation specs: {total_now} across {len(current["files"])} files '
        f'(baseline {total_base} across {len(baseline["files"])} files). '
        f'To accept a reviewed reduction: python3 scripts/app/android_motion_baseline.py --update-baseline'
    )


def main():
    parser = argparse.ArgumentParser(
        description='Count Android literal animation specs and reject counts above the baseline')
    parser.add_argument('--root', type=Path, default=ROOT, help='repository root')
    parser.add_argument('--baseline', type=Path, default=BASELINE, help='baseline file')
    parser.add_argument('--report', type=Path, help="also write this run's counts here")
    parser.add_argument('--update-baseline', action='store_true',
                        help='accept the current counts and rewrite the baseline')
    args = parser.parse_args()
    current = scan(args.root)
    payload = json.dumps(current, ensure_ascii=False, indent=2, sort_keys=True) + '\n'
    if args.report:
        args.report.write_text(payload)
    if args.update_baseline:
        args.baseline.write_text(json.dumps(baseline_of(current), ensure_ascii=False, indent=2, sort_keys=True) + '\n')
        print('motion baseline written')
        return 0
    if not args.baseline.is_file():
        parser.error('no baseline; run --update-baseline first')
    baseline = json.loads(args.baseline.read_text())
    if baseline.get('version') != current['version']:
        parser.error('baseline version does not match')
    print(reduction_hint(current, baseline))
    failures = regressions(current, baseline)
    if failures:
        print('\nAbove the baseline (counts may only go down):\n' + '\n'.join(failures))
        return 1
    print('motion baseline check passed')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
