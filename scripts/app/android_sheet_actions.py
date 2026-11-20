#!/usr/bin/env python3
"""Keep Android presented-page exits in JSheetClose and out of trailing actions (#9661)."""
import argparse
import re
from pathlib import Path

from android_token_baseline import code_only

ROOT = Path(__file__).resolve().parents[2]
BUTTON = re.compile(r'\b(JSheetClose|JIconButton|JTextButton|JButton|IconButton|Icon)\s*\(')
EXIT_ICON = re.compile(r'\b(?:PhIcons\.X|PersonaIcons\.close|Icons\.(?:Default|Filled|Outlined)\.Close)\b')
EXIT_LABEL = re.compile(r'\bR\.string\.(?:\w+_)?(?:close|cancel)\b')
DONE_LABEL = re.compile(r'\bR\.string\.(?:\w+_)?done\b')
ICON_LITERAL = re.compile(EXIT_ICON.pattern + r'\s*,\s*(?:contentDescription\s*=\s*)?"(?:Close|Cancel)"')
EXIT_CALLBACK = re.compile(r'\b(?:onDismiss(?:Request)?|onClose|onCancel)\b|::close\b')
PURE_DISMISS = re.compile(r',\s*(?:onClick\s*=\s*)?(?:(?:onDismiss(?:Request)?|onClose|onCancel|::close)|'
                          r'\{\s*(?:onDismiss(?:Request)?|onClose|onCancel)\(\s*\)\s*\})\s*[,)]')
END_SLOT = re.compile(r'\b(?:actions|pageActions|trailing|endActions|confirmButton)\s*=\s*\(?\s*$')
END_EDGE = re.compile(r'\bAlignment\.(?:Top|Center|Bottom)End\b|\balign\(\s*Alignment\.End\b')
FUN = re.compile(r'\bfun\s+([\w.]+)\s*\(')

# These controls act on a data item or inline notice, not the presentation.
INLINE = {
    ('CompactionStatusLine.kt', 'CompactionStatusLine'),
    ('DelegatedAnswers.kt', 'DelegatedCard'),
    ('InteractionPanel.kt', 'InteractionPanel'),
    ('TerminalPanesScreen.kt', 'PaneRow'),
}


def closing(code, start):
    """Index just after this function call's final parenthesis."""
    depth = 1
    for i in range(start + 1, len(code)):
        depth += (code[i] == '(') - (code[i] == ')')
        if depth == 0:
            return i + 1
    return len(code)


def end_position(code, at, end, include_row=True):
    if END_EDGE.search(code[at:end]):
        return True
    # Walk the enclosing Kotlin blocks. Inspect a slot's declaration, never text
    # in a closed sibling lambda (e.g. the preceding row's own trailing action).
    braces = []
    for match in re.finditer('[{}]', code[:at]):
        if match[0] == '{':
            braces.append(match.start())
        elif braces:
            braces.pop()
    for opening in reversed(braces):
        prefix = code[max(0, opening - 650):opening].rstrip()
        if END_SLOT.search(prefix):
            return True
        if prefix.endswith(')'):
            row = re.search(r'\bRow\s*\([^{}]*\)\s*$', prefix)
            # Discard other branches of a when or a preceding child container.
            direct = []
            depth = 0
            for token in re.split('([{}])', code[opening + 1:at]):
                if token == '{':
                    depth += 1
                elif token == '}':
                    depth -= 1
                elif depth == 0:
                    direct.append(token)
            if include_row and row and ('Arrangement.End' in row[0] or
                        re.search(r'\b(?:Text|Spacer|JIconButton|JTextButton|JButton)\s*\(|\.weight\(',
                                  ''.join(direct))):
                return True
        if re.search(r'\bfun\s+\w+\s*\([^{}]*\)\s*$', prefix):
            break
    return False


def violations(path, source):
    if not re.search(r'\b(?:JSheetClose|PhIcons\.X|PersonaIcons\.close|Close|\w+_close|\w+_cancel|\w+_done)\b', source):
        return []
    code = code_only(source)
    functions = list(FUN.finditer(code))
    findings = []
    for call in BUTTON.finditer(code):
        name, at = call[1], call.start()
        if re.search(r'\bfun\s*$', code[max(0, at - 12):at]):
            continue
        end = closing(code, call.end() - 1)
        args = code[call.end():end]
        owner = next((f[1] for f in reversed(functions) if f.start() < at), '')
        if name == 'JSheetClose':
            if end_position(code, at, end):
                findings.append((at, 'put JSheetClose at the top start, before the title and actions'))
        elif not (path.name == 'SheetActions.kt' and owner == 'JSheetClose') and (path.name, owner) not in INLINE:
            caption, dismiss = EXIT_LABEL.search(args), EXIT_CALLBACK.search(args)
            literal_exit = ICON_LITERAL.search(source[call.end():end])
            if EXIT_ICON.search(args) and (caption or literal_exit or (dismiss and 'R.string.' not in args)):
                findings.append((at, 'use JSheetClose for a presented-page close'))
            elif (caption or DONE_LABEL.search(args) and PURE_DISMISS.search(args)) and dismiss and end_position(code, at, end, include_row=False):
                findings.append((at, 'use a leading JSheetClose instead of a trailing dismissal'))
    return [(code.count('\n', 0, at) + 1, message) for at, message in findings]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=ROOT, help='repository checkout')
    root = parser.parse_args().root
    findings = []
    for path in sorted((root / 'android').glob('**/src/main/**/*.kt')):
        for line, message in violations(path, path.read_text()):
            findings.append(f'{path.relative_to(root)}:{line}: {message}')
    if findings:
        print('\n'.join(findings))
    print(f'Android presented-page close check: {len(findings)} violations')
    return bool(findings)


if __name__ == '__main__':
    raise SystemExit(main())
