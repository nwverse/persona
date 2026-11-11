#!/usr/bin/env python3
"""Ratchet expensive class construction in @State (docs/apple/performance.md, P3).

An init is trivial only when empty or made of assignments without calls or blocks.
An eager stored-property initializer is non-trivial if it contains a call or block.
Static/lazy properties, stored closure bodies, computed properties and ordinary methods do not run on init.
All Swift sources under apple/ are scanned, including tests, but not build outputs.

  python3 scripts/test/test_apple_state_initial_values.py
  python3 scripts/test/test_apple_state_initial_values.py --update  lower the baseline
  python3 scripts/test/test_apple_state_initial_values.py --list    print file:line sites
"""
from collections import Counter
import json
from pathlib import Path
import re
import sys
import unittest

from test_apple_perf_patterns import ROOT, apple_comments, blank_comments, compare

BASELINE = Path(__file__).with_name("apple_state_initial_values_baseline.json")
STRINGS = re.compile(r'(?P<hashes>\#*)(?P<quote>"""|")(?:\\.|(?!(?P=quote)(?P=hashes)).)*(?P=quote)(?P=hashes)', re.S)
CLASS = re.compile(r"\bclass\s+(?!func\b|var\b|let\b)(\w+)[^{}]*\{")
STATE = re.compile(r"@State\s+(?:(?:private|fileprivate|public|internal)(?:\(set\))?\s+)?"
                   r"var\s+(\w+)\s*(?::[^=\n;{}]+)?\s*=\s*(\w+(?:\.\w+)*)\s*(?:<[^>{}\n]+>)?\s*\(")
CALL = re.compile(r"\b\w+(?:\s*<[^{};\n]+>)?\s*\(")
ASSIGNMENT = re.compile(r"(?:(?:let|var)\s+)?(?:self\.)?\w+(?:\.\w+)*\s*=(?!=)[^{}()]+")


def source_code(src):
    def string_value(match):
        text = match.group()
        value = "f()" if re.search(r"\\#*\(", text) else "0"
        return value + "".join("\n" if c == "\n" else " " for c in text[len(value):])
    return STRINGS.sub(string_value, blank_comments(src))


def members(code):
    """Immediate statements/members, retaining multiline calls and brace bodies."""
    start, depth = 0, 0
    for i, c in enumerate(code):
        if c in "({[":
            depth += 1
        elif c in ")}]":
            depth -= 1
        elif c in "\n;" and depth == 0 and not code[start:i].rstrip().endswith("="):
            yield code[start:i].strip()
            start = i + 1
    yield code[start:].strip()


def nontrivial(value):
    return bool(CALL.search(value) or "{" in value)


def class_work(code):
    """Class name -> first non-trivial initializer, without examining method bodies."""
    stack, ends = [], {}
    for i, c in enumerate(code):
        if c == "{":
            stack.append(i)
        elif c == "}" and stack:
            ends[stack.pop()] = i
    found = {}
    # shortcut: lexical class names, no overload/inheritance resolution; use compiler lint when that is needed.
    for match in CLASS.finditer(code):
        opening = match.end() - 1
        if opening not in ends:
            continue
        for member in members(code[opening + 1:ends[opening]]):
            header = member.split("{", 1)[0]
            constructor = re.search(r"\binit[?!]?\s*\(", header)
            if constructor and not re.search(r"\b(?:func|let|var)\b", header):
                depth = 1
                for i in range(constructor.end(), len(member)):
                    depth += (member[i] == "(") - (member[i] == ")")
                    if depth == 0:
                        break
                body = member[member.find("{", i + 1) + 1:member.rfind("}")]
                statements = [s for s in members(body) if s and not s.startswith("#")]
                if any(nontrivial(s) or not ASSIGNMENT.fullmatch(s) for s in statements):
                    found[match[1]] = "init"
                    break
            prop = re.search(r"\b(?:let|var)\s+(\w+)[^=]*=(?!=)", header)
            if prop and not re.search(r"\b(?:static|class|lazy)\b", header[:prop.start()]):
                value = re.split(r"\{\s*(?:willSet|didSet)\b", member[prop.end():], maxsplit=1)[0].strip()
                if value.startswith("{") and value.endswith("}"):
                    continue
                if nontrivial(value):
                    found[match[1]] = f"stored property {prop[1]}"
                    break
    return found


def scan_sources(sources):
    code = {name: source_code(src) for name, src in sources.items()}
    expensive = {}
    for src in code.values():
        expensive.update(class_work(src))
    counts, sites = {}, []
    for name, src in sorted(code.items()):
        found = []
        for match in STATE.finditer(src):
            kind = match[2].split(".")[-1]
            if kind in expensive:
                found.append(kind)
                line = src.count("\n", 0, match.start()) + 1
                sites.append(f"{name}:{line} {match[1]} = {match[2]}(...) ({expensive[kind]})")
        if found:
            counts[name] = dict(sorted(Counter(found).items()))
    return counts, sites


def scan():
    return scan_sources({str(Path(p).relative_to(ROOT)): Path(p).read_text(encoding="utf-8", errors="replace")
                         for p in apple_comments.sources() if p.endswith(".swift")})


def load_baseline():
    return json.loads(BASELINE.read_text(encoding="utf-8"))


def update():
    counts, _ = scan()
    grown, _ = compare(counts, load_baseline())
    if grown:
        print("refusing to raise the baseline: keep @State class initializers trivial (P3)")
        print("\n".join(grown))
        return 1
    BASELINE.write_text(json.dumps(counts, indent=1, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"apple state initial values baseline: {sum(sum(r.values()) for r in counts.values())} sites in {len(counts)} files")
    return 0


class AppleStateInitialValueTests(unittest.TestCase):
    def test_no_file_grows_its_expensive_initial_values(self):
        counts, sites = scan()
        grown, shrunk = compare(counts, load_baseline())
        self.assertEqual(grown, [], "expensive @State initial value added (P3): defer setup to .task or use lazy properties\n"
                         + "\n".join(grown + sites))
        self.assertEqual(shrunk, [], "fewer expensive @State initial values: run "
                         "python3 scripts/test/test_apple_state_initial_values.py --update and commit the baseline")

    def test_what_counts(self):
        models = '''
        class AttachPanel { init() { PerfTrace.event("init") } }
        class AttachLibrary { let manager = PHCachingImageManager() }
        class Scheduled { init() { Task { await load() } } }
        class Trivial {
            var count = 0
            var label = "not a call: Timer() { }"
            lazy var manager = PHCachingImageManager()
            static let shared = Trivial()
            var computed: Manager { PHCachingImageManager() }
            init(value: Int) { self.count = value; self.label = "ready" }
            func load() { Task { await fetch() } }
        }
        struct Value { let manager = PHCachingImageManager() }
        '''
        views = '''
        @State private var attachPanel = AttachPanel()
        @State private var library: AttachLibrary = AttachLibrary()
        @State
        private var scheduled =
            Scheduled()
        @State private var trivial = Trivial(value: 1)
        @State private var value = Value()
        @State private var framework = PHCachingImageManager()
        // @State private var commented = AttachLibrary()
        /* class Trivial { init() { log() } } */
        '''
        counts, sites = scan_sources({"models.swift": models, "views.swift": views})
        self.assertEqual(counts, {"views.swift": {"AttachLibrary": 1, "AttachPanel": 1, "Scheduled": 1}})
        self.assertTrue(sites[0].startswith("views.swift:2 attachPanel = AttachPanel"))
        self.assertTrue(sites[2].startswith("views.swift:4 scheduled = Scheduled"))
        self.assertEqual(class_work(source_code('class Empty { init() {} }')), {})
        self.assertEqual(class_work(source_code('class Branch { init() { if ready { count = 1 } } }')), {"Branch": "init"})
        self.assertEqual(class_work(source_code('class Multi { let manager =\n PHCachingImageManager() }')),
                         {"Multi": "stored property manager"})
        self.assertEqual(class_work(source_code('class Outer { class Inner { let manager = Timer() } }')),
                         {"Inner": "stored property manager"})
        self.assertEqual(class_work(source_code('class Observer { var count = 0 { didSet { log() } } }')), {})
        self.assertEqual(class_work(source_code('class Callback { var action: () -> Void = { log() } }')), {})
        self.assertEqual(class_work(source_code('class Eager { let manager = { makeManager() }() }')),
                         {"Eager": "stored property manager"})
        self.assertEqual(class_work(source_code('class FixedPanel { lazy var library = AttachLibrary() }')), {})
        self.assertEqual(class_work(source_code('class DefaultCallback { '
                                               'init(load: () -> Void = { fetch() }) { self.load = load } }')), {})
        self.assertEqual(class_work(source_code('class Strings { let text = #"Timer() { }"# }')), {})


if __name__ == "__main__":
    if sys.argv[1:] == ["--update"]:
        sys.exit(update())
    if sys.argv[1:] == ["--list"]:
        print("\n".join(scan()[1]))
        sys.exit(0)
    unittest.main()
