"""The Android modules where a dropped must-use result is still only a warning (#13024).

Every Android module compiles a dropped must-use result as an error (android/build-logic/convention/src/main/kotlin/
persona/buildlogic/ReturnValues.kt), except the modules its stillWarnings map lists, each with the call sites it has left.
That list may only shrink, and this suite holds it to that:

- a module outside FROZEN, the modules listed when the error was turned on, fails: a new module never joins;
- a listed site that no longer drops its result fails: drop the fixed site from the list;
- a listed module with no sites left fails: drop the module, and it compiles a dropped result as an error from then on.

A site is "<module-relative file>: <statement text>". It still drops its result while that text, as written, stands as a
statement somewhere in the file: after only indentation, `{`, `;`, `->`, `else` or an `if (...)` on its line, and
followed by nothing, `;`, `}` or a comment. `val _ = <text>`, a result that is read (`<text>.getOrNull()`,
`return <text>`) or a reworded statement no longer counts, so the site has to leave the list.
"""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[2]
ANDROID = ROOT / "android"
RETURN_VALUES = ANDROID / "build-logic/convention/src/main/kotlin/persona/buildlogic/ReturnValues.kt"
SETTINGS = ANDROID / "settings.gradle.kts"
# The modules stillWarnings listed when the error was turned on (#13024). One may leave; none may join.
FROZEN = frozenset({":feature:chat"})

_MAP = re.compile(r"^private val stillWarnings: Map<String, List<String>> = mapOf\($(.*?)^\)$", re.M | re.S)
# Kotlin tokens of the map: a string literal, a word, one punctuation character; whitespace and // comments between.
_TOKEN = re.compile(r'\s+|//[^\n]*|"((?:[^"\\\n]|\\.)*)"|([A-Za-z_]\w*)|(.)')
# What may stand before and after a statement whose result is dropped, on its own line.
_LEAD = re.compile(r"(?:^|[{;)]|->|\belse)\s*$")
_TRAIL = re.compile(r"\s*(?:$|[;}]|//)")


def literal(body):
    """A Kotlin string literal's value; None when it holds a template, which a site must not."""
    if re.search(r"(?<!\\)\$", body):
        return None
    return re.sub(r"\\(.)", lambda m: {"n": "\n", "t": "\t"}.get(m.group(1), m.group(1)), body)


def tokens(text):
    """(kind, value) for each token: ("string", its value or None for a template), ("word", w) or ("mark", c)."""
    out = []
    for m in _TOKEN.finditer(text):
        if m.group(1) is not None:
            out.append(("string", literal(m.group(1))))
        elif m.group(2) is not None:
            out.append(("word", m.group(2)))
        elif m.group(3) is not None:
            out.append(("mark", m.group(3)))
    return out


def entries(source):
    """stillWarnings as (module, [site, ...]) pairs, and the problems reading it."""
    found = _MAP.search(source)
    if not found:
        return [], ["ReturnValues.kt: no `private val stillWarnings: Map<String, List<String>> = mapOf(` ... `)` block"]
    toks, at, result = tokens(found.group(1)), 0, []

    def take(kind, value=None):
        nonlocal at
        if at < len(toks) and toks[at][0] == kind and (value is None or toks[at][1] == value):
            at += 1
            return toks[at - 1]
        raise ValueError("expected %s at %s" % (value or kind, toks[at] if at < len(toks) else "the end"))

    def string():
        value = take("string")[1]
        if value is None:
            raise ValueError("a site or module holds a string template")
        return value

    try:
        while at < len(toks):
            module = string()
            take("word", "to"), take("word", "listOf"), take("mark", "(")
            sites = []
            while toks[at] != ("mark", ")"):
                site = string()
                while toks[at] == ("mark", "+"):
                    take("mark", "+")
                    site += string()
                sites.append(site)
                if toks[at] != ("mark", ")"):
                    take("mark", ",")
            take("mark", ")")
            if at < len(toks) and toks[at] == ("mark", ","):
                take("mark", ",")
            result.append((module, sites))
    except (ValueError, IndexError) as error:
        return result, ["ReturnValues.kt: stillWarnings is not `\":module\" to listOf(\"<file>: <statement>\", ...)`, "
                        "with string literals joined by +: %s" % (error or "it ends early")]
    return result, []


def still_dropped(text, statement):
    """Whether statement stands somewhere in text as a statement of its own (the module docstring's rule)."""
    at = text.find(statement)
    while at >= 0:
        start = text.rfind("\n", 0, at) + 1
        end = text.find("\n", at)
        end = len(text) if end < 0 else end
        if _LEAD.search(text[start:at]) and _TRAIL.match(text[at + len(statement):end]):
            return True
        at = text.find(statement, at + 1)
    return False


def problems(source, settings, read):
    """What breaks the rules: source is ReturnValues.kt, settings settings.gradle.kts, and read(path) a module file's
    text (None when the file is gone), with paths relative to android/."""
    listed, found = entries(source)
    seen = set()
    for module, sites in listed:
        if module in seen:
            found.append("%s is listed twice" % module)
        seen.add(module)
        if module not in FROZEN:
            found.append("%s is not one of the modules listed when the error was turned on (%s): the list may only "
                         "shrink, so fix its dropped results instead (val _ = call() for one dropped on purpose)"
                         % (module, ", ".join(sorted(FROZEN))))
        if '"%s"' % module not in settings:
            found.append("%s is not a module in android/settings.gradle.kts: drop it from stillWarnings" % module)
        if not sites:
            found.append("%s has no sites left: drop it from stillWarnings, and it compiles a dropped result as an "
                         "error like every other module" % module)
        directory = module.strip(":").replace(":", "/")
        for site in sites:
            path, sep, statement = site.partition(": ")
            if not sep or not path or not statement.strip():
                found.append("%s: %r is not \"<module-relative file>: <statement text>\"" % (module, site))
                continue
            text = read("%s/%s" % (directory, path))
            if text is None:
                found.append("%s: %s is gone; drop its site from stillWarnings" % (module, path))
            elif not still_dropped(text, statement):
                found.append("%s: %s no longer drops the result of %r; drop the fixed site from stillWarnings"
                             % (module, path, statement))
    return found


def read_android(path):
    file = ANDROID / path
    return file.read_text(encoding="utf-8") if file.is_file() else None


SOURCE = '''fun Project.configureReturnValueChecks() {}

private val stillWarnings: Map<String, List<String>> = mapOf(
    // #1 rewrites this statement.
    ":feature:chat" to listOf(
        "src/main/A.kt: " +
            "runCatching { a() }",
        "src/test/B.kt: b()",
    ),
)
'''
SETTINGS_TEXT = 'include(":app")\ninclude(\n    ":feature:chat", ":feature:files",\n)\n'
FILES = {"feature/chat/src/main/A.kt": "fun x() {\n    runCatching { a() }\n}\n",
         "feature/chat/src/test/B.kt": "    b()\n"}


class ReadingTests(unittest.TestCase):
    def test_modules_and_joined_sites(self):
        self.assertEqual(entries(SOURCE), ([(":feature:chat", ["src/main/A.kt: runCatching { a() }",
                                                               "src/test/B.kt: b()"])], []))

    def test_an_empty_list_and_an_empty_module(self):
        empty = SOURCE.replace(SOURCE[SOURCE.index("    // #1"):SOURCE.rindex(")")], "")
        self.assertEqual(entries(empty), ([], []))
        self.assertEqual(entries('private val stillWarnings: Map<String, List<String>> = mapOf(\n'
                                 '    ":feature:chat" to listOf(),\n)\n'), ([(":feature:chat", [])], []))

    def test_a_missing_block_and_a_template(self):
        self.assertTrue(entries("val other = 1\n")[1][0].startswith("ReturnValues.kt: no "))
        templated = SOURCE.replace('"src/test/B.kt: b()"', '"src/test/B.kt: ${b}()"')
        self.assertIn("a site or module holds a string template", entries(templated)[1][0])
        self.assertIn("expected , at", entries(SOURCE.replace('" +\n', '"\n'))[1][0])

    def test_the_real_list_reads_cleanly(self):
        listed, found = entries(RETURN_VALUES.read_text(encoding="utf-8"))
        self.assertEqual(found, [])
        self.assertTrue(all(sites for _, sites in listed), listed)


class RuleTests(unittest.TestCase):
    def check(self, source=SOURCE, files=None):
        files = FILES if files is None else files
        return problems(source, SETTINGS_TEXT, files.get)

    def test_the_planted_list_passes(self):
        self.assertEqual(self.check(), [])

    def test_a_module_may_not_join(self):
        added = SOURCE.replace("\n)\n", '\n    ":feature:files" to listOf("src/main/C.kt: c()"),\n)\n')
        files = dict(FILES, **{"feature/files/src/main/C.kt": "c()\n"})
        found = self.check(added, files)
        self.assertEqual(len(found), 1, found)
        self.assertIn(":feature:files is not one of the modules listed when the error was turned on", found[0])

    def test_a_statement_that_still_drops_its_result(self):
        statement = "runCatching { a() }"
        for line in ("runCatching { a() }", "    if (draft) runCatching { a() }", "} else runCatching { a() } // why",
                     "onClick = { runCatching { a() } }", "x(); runCatching { a() };", "is A -> runCatching { a() }",
                     "    val b = 1\n    runCatching { a() }\n"):
            with self.subTest(line=line):
                self.assertTrue(still_dropped(line, statement))
        for line in ("val _ = runCatching { a() }", "return runCatching { a() }", "runCatching { a() }.getOrNull()",
                     "val ok = runCatching { a() }.isSuccess", "runCatching { a() + 1 }", "f(runCatching { a() })"):
            with self.subTest(line=line):
                self.assertFalse(still_dropped(line, statement))

    def test_a_fixed_site_fails_until_it_is_dropped(self):
        rewritten = dict(FILES, **{"feature/chat/src/main/A.kt": "fun x() {\n    if (y) runCatching { a() }\n}\n"})
        self.assertEqual(self.check(files=rewritten), [])  # still dropped, only under an if
        for fixed in ("fun x() {\n    val _ = runCatching { a() }\n}\n", "fun x() {\n    a()\n}\n"):
            with self.subTest(fixed=fixed):
                found = self.check(files=dict(FILES, **{"feature/chat/src/main/A.kt": fixed}))
                self.assertEqual(found, [":feature:chat: src/main/A.kt no longer drops the result of "
                                         "'runCatching { a() }'; drop the fixed site from stillWarnings"])

    def test_a_gone_file_and_a_clean_module(self):
        found = self.check(files={"feature/chat/src/test/B.kt": "b()\n"})
        self.assertEqual(found, [":feature:chat: src/main/A.kt is gone; drop its site from stillWarnings"])
        clean = ('private val stillWarnings: Map<String, List<String>> = mapOf(\n'
                 '    ":feature:chat" to listOf(),\n)\n')
        self.assertEqual(len(self.check(clean)), 1)
        self.assertIn(":feature:chat has no sites left", self.check(clean)[0])

    def test_a_malformed_site_and_a_module_not_in_settings(self):
        bad = SOURCE.replace('"src/test/B.kt: b()"', '"src/test/B.kt"')
        self.assertIn("is not \"<module-relative file>: <statement text>\"", self.check(bad)[0])
        self.assertIn("is not a module in android/settings.gradle.kts",
                      problems(SOURCE, 'include(":app")\n', FILES.get)[0])


class RepositoryTests(unittest.TestCase):
    def test_still_warnings_only_shrinks_and_every_site_is_still_there(self):
        found = problems(RETURN_VALUES.read_text(encoding="utf-8"), SETTINGS.read_text(encoding="utf-8"), read_android)
        self.assertEqual(found, [], "\n".join(["stillWarnings in %s:" % RETURN_VALUES.relative_to(ROOT)] + found))


if __name__ == "__main__":
    unittest.main()
