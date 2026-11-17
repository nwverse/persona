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
# tween / spring / snap may carry a type argument: tween<Float>(900), spring<T>(...), snap<Float>(0).
TYPE_ARGS = r'(?:<[^<>]*>)?'
# Compose's named duration is durationMillis; older / wrong "duration =" still matched so drift fails.
DURATION_NAMED = r'(?:(?:durationMillis|duration)\s*=\s*)?'
TWEEN_NUM = re.compile(rf'\btween\s*{TYPE_ARGS}\s*\(\s*{DURATION_NAMED}\d')
SPRING_LIT = re.compile(
    rf'\bspring\s*{TYPE_ARGS}\s*\(\s*(?:[^)]*?(?:dampingRatio|stiffness)\s*=\s*(?:[\d.]+|Spring\.))'
)
KEYFRAMES = re.compile(r'\bkeyframes\s*\(')
SNAP_NUM = re.compile(rf'\bsnap\s*{TYPE_ARGS}\s*\(\s*\d')
ANIMATED_VISIBILITY = re.compile(r'\bAnimatedVisibility\s*\(')
CROSSFADE = re.compile(r'\bCrossfade\s*\(')
ANIMATED_CONTENT = re.compile(r'\bAnimatedContent\s*\(')
ANIMATE_CONTENT_SIZE = re.compile(r'\.animateContentSize\s*\(')
ANIMATE_AS_STATE = re.compile(
    r'\banimate(?:Float|Dp|Int|Offset|Color|Value|Size|IntSize|IntOffset)AsState\s*\('
)


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


def call_head(code, start, limit=800):
    """Arguments of a call before its trailing lambda / trailing brace content."""
    open_idx = code.find('(', start)
    if open_idx < 0:
        return ''
    end = matching_paren(code, open_idx)
    return code[open_idx + 1:end]


def split_top_level_args(args):
    """Comma-split call arguments, ignoring commas inside nested () [] {} <> and strings."""
    parts = []
    buf = []
    depth = 0
    angle = 0
    quote = None
    i = 0
    while i < len(args):
        ch = args[i]
        if quote:
            buf.append(ch)
            if ch == '\\' and i + 1 < len(args):
                buf.append(args[i + 1])
                i += 2
                continue
            if ch == quote:
                quote = None
            i += 1
            continue
        if ch in ('"', "'"):
            quote = ch
            buf.append(ch)
        elif ch in '([{':
            depth += 1
            buf.append(ch)
        elif ch in ')]}':
            depth = max(0, depth - 1)
            buf.append(ch)
        elif ch == '<' and depth == 0:
            angle += 1
            buf.append(ch)
        elif ch == '>' and angle:
            angle -= 1
            buf.append(ch)
        elif ch == ',' and depth == 0 and angle == 0:
            parts.append(''.join(buf).strip())
            buf = []
        else:
            buf.append(ch)
        i += 1
    tail = ''.join(buf).strip()
    if tail:
        parts.append(tail)
    return parts


def is_named(arg):
    return bool(re.match(r'^[A-Za-z_][A-Za-z0-9_]*\s*=', arg))


def has_animation_spec_arg(args):
    """True when the call passes an animationSpec (named or a positional FiniteAnimationSpec)."""
    if re.search(r'\banimationSpec\s*=', args):
        return True
    for arg in split_top_level_args(args):
        if is_named(arg):
            # Named args after the target: only animationSpec counts as a spec.
            continue
        # Positional: first is targetValue; a second positional is the animationSpec.
        # We only know we have a positional after seeing more than one positional.
        # Handled below by counting positionals.
    positionals = [a for a in split_top_level_args(args) if not is_named(a)]
    # targetValue [, animationSpec] [, label] is unusual as positional label; Compose uses named label.
    # A second positional argument is the animationSpec.
    return len(positionals) >= 2


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
    for match in ANIMATED_CONTENT.finditer(code):
        start = match.start()
        args = call_head(code, start)
        if re.search(r'\btransitionSpec\s*=', args):
            continue
        hits.append((code.count('\n', 0, start) + 1, 'AnimatedContent-default'))
    for match in ANIMATE_CONTENT_SIZE.finditer(code):
        start = match.start()
        args = call_head(code, start)
        # Default when there is no positional FiniteAnimationSpec and no animationSpec=.
        # Named alignment= alone still uses the default animationSpec.
        if re.search(r'\banimationSpec\s*=', args):
            continue
        if any(not is_named(a) for a in split_top_level_args(args) if a):
            continue
        hits.append((code.count('\n', 0, start) + 1, 'animateContentSize-default'))
    for match in ANIMATE_AS_STATE.finditer(code):
        start = match.start()
        args = call_head(code, start)
        if has_animation_spec_arg(args):
            continue
        hits.append((code.count('\n', 0, start) + 1, 'animateAsState-default'))
    hits.sort(key=lambda item: (item[0], item[1]))
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
    return failures


def update_raises(current, baseline):
    """Files that would raise the baseline (higher count or a new file). Counts may only go down."""
    rises = []
    for path, actual in sorted(current['files'].items()):
        limit = baseline['files'].get(path)
        if limit is None:
            rises.append(f'{path}: new file with {actual} literal animation specs')
        elif actual > limit:
            rises.append(f'{path}: {actual} literal animation specs, baseline {limit}')
    return rises


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
                        help='accept a reduction of the current counts and rewrite the baseline '
                             '(refuses any rise or new file)')
    args = parser.parse_args()
    current = scan(args.root)
    payload = json.dumps(current, ensure_ascii=False, indent=2, sort_keys=True) + '\n'
    if args.report:
        args.report.write_text(payload)
    if args.update_baseline:
        if args.baseline.is_file():
            baseline = json.loads(args.baseline.read_text())
            rises = update_raises(current, baseline)
            if rises:
                print('Refusing to raise the motion baseline (counts may only go down):\n' + '\n'.join(rises))
                return 1
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
