#!/usr/bin/env python3
"""The Apple sources' code comments are in English (#5055, CLAUDE.md "Conventions").

  apple_comments.py check            no comment under apple/ has Chinese or Japanese text outside corner brackets
  apple_comments.py strip <file>     prints the file's code with its comments removed (to check that a comment-only
                                     change left the code as it was: compare the output before and after)

Inside paired U+300C/U+300D corner brackets the original text stays: a documentation heading or text the code must
match word for word (hub output, a prompt, a test's expected UI string). Swift, Objective-C and JavaScript comments
(`//`, `/* */`, nested), `#` comments in YAML and shell, `<!-- -->` in HTML are checked; string literals are not
comments (the UI strings are checked by apple_l10n.py).
"""

import os
import re
import sys

ROOT = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".."))
APPLE = os.path.join(ROOT, "apple")
CJK = re.compile(r"[぀-ヿ㐀-䶿一-鿿豈-﫿ｦ-ﾟ]")
QUOTED = re.compile(r"「[^」\n]*」")
C_LIKE = (".swift", ".m", ".h", ".mm", ".c", ".js", ".ts")
HASH = (".yml", ".yaml", ".sh", ".py")
HTML = (".html",)
SKIP_DIRS = {".build", "DerivedData", "build", "node_modules", ".swiftpm"}


def c_comments(src):
    """The (start, end) spans of the comments in Swift / C-like source; string literals (raw, multi-line, with
    interpolations that hold strings and comments of their own) are skipped."""
    spans, n = [], len(src)

    def string(i):
        j = i
        while j < n and src[j] == "#":
            j += 1
        hashes = j - i
        multi = src.startswith('"""', j)
        close = ('"""' if multi else '"') + "#" * hashes
        esc = "\\" + "#" * hashes
        k = j + (3 if multi else 1)
        while k < n:
            if src.startswith(esc, k):
                m = k + len(esc)
                if m < n and src[m] == "(":
                    k = code(m + 1, True)
                    continue
                k = m + 1
                continue
            if src.startswith(close, k):
                return k + len(close)
            if not multi and src[k] == "\n":
                return k
            k += 1
        return k

    def code(i, paren=False):
        depth = 0
        while i < n:
            c = src[i]
            if src.startswith("//", i):
                j = src.find("\n", i)
                j = n if j < 0 else j
                spans.append((i, j))
                i = j
                continue
            if src.startswith("/*", i):
                start, d, i = i, 1, i + 2
                while i < n and d:
                    if src.startswith("/*", i):
                        d, i = d + 1, i + 2
                    elif src.startswith("*/", i):
                        d, i = d - 1, i + 2
                    else:
                        i += 1
                spans.append((start, i))
                continue
            if c == '"' or (c == "#" and re.match(r'#+"', src[i:i + 8])):
                i = string(i)
                continue
            if c == "'" and i + 2 < n and src[i + 2] == "'":
                i += 3  # a C / JS character literal like '"' (Swift has none)
                continue
            if paren:
                if c == "(":
                    depth += 1
                elif c == ")":
                    if depth == 0:
                        return i + 1
                    depth -= 1
            i += 1
        return i

    code(0)
    return spans


def hash_comments(src):
    """`#` comments of YAML and shell: from a `#` at the start of a line or after a space, outside quotes."""
    spans, pos = [], 0
    for line in src.splitlines(keepends=True):
        quote = None
        for k, c in enumerate(line):
            if quote:
                if c == quote:
                    quote = None
            elif c in "\"'":
                quote = c
            elif c == "#" and (k == 0 or line[k - 1].isspace()):
                spans.append((pos + k, pos + len(line.rstrip("\n"))))
                break
        pos += len(line)
    return spans


def html_comments(src):
    return [m.span() for m in re.finditer(r"<!--.*?-->", src, re.S)]


def comments(path, src):
    if path.endswith(C_LIKE):
        return c_comments(src)
    if path.endswith(HASH):
        return hash_comments(src)
    if path.endswith(HTML):
        return html_comments(src)
    return []


def offending(text):
    return bool(CJK.search(QUOTED.sub("", text)))


def problems(path, src):
    """`line: comment` for each comment with Chinese / Japanese text outside paired corner brackets."""
    out = []
    for s, e in comments(path, src):
        text = src[s:e]
        if offending(text):
            out.append(f"{src.count(chr(10), 0, s) + 1}: {text.strip().splitlines()[0][:120]}")
    return out


def strip(src, path=".swift"):
    spans, parts, p = comments(path, src), [], 0
    for s, e in spans:
        parts.append(src[p:s])
        p = e
    parts.append(src[p:])
    return "".join(parts)


def sources():
    for root, dirs, files in os.walk(APPLE):
        dirs[:] = sorted(d for d in dirs if d not in SKIP_DIRS)
        for f in sorted(files):
            if f.endswith(C_LIKE + HASH + HTML):
                yield os.path.join(root, f)


def check():
    bad = 0
    for path in sources():
        with open(path, encoding="utf-8", errors="replace") as fh:
            found = problems(path, fh.read())
        for p in found:
            print(f"{os.path.relpath(path, ROOT)}:{p}")
        bad += len(found)
    if bad:
        print(f"\n{bad} comment(s) under apple/ have Chinese / Japanese text outside 「…」: write them in English "
              "(keep 「…」 only for a Chinese doc heading that still exists, which test_doc_sections.py checks, or text the "
              "code matches word for word).")
        return 1
    print("OK: the comments under apple/ are in English")
    return 0


def main(argv):
    if len(argv) >= 2 and argv[1] == "check":
        return check()
    if len(argv) == 3 and argv[1] == "strip":
        with open(argv[2], encoding="utf-8") as fh:
            sys.stdout.write(strip(fh.read(), argv[2]))
        return 0
    print(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
