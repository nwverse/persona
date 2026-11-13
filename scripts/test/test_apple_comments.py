"""The code comments under apple/ are in English (#5055).

Runs `scripts/i18n/apple_comments.py check` on the repository, and checks its comment lexer: string literals are not
comments, paired corner brackets preserve a documentation heading or matched text, and stripping comments leaves the code.
"""
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "i18n"))

import apple_comments  # noqa: E402


class RepositoryTest(unittest.TestCase):
    def test_apple_comments_are_english(self):
        r = subprocess.run([sys.executable, str(ROOT / "scripts/i18n/apple_comments.py"), "check"],
                           capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, r.stdout[-4000:] + r.stderr[-2000:])


class LexerTest(unittest.TestCase):
    def test_strings_are_not_comments(self):
        src = 'let a = "// 不是注释 /* 也不是 */"\nlet b = #"原样 // 字"#\nlet c = """\n  // 多行里的\n  """\n'
        self.assertEqual(apple_comments.problems("a.swift", src), [])

    def test_comments_inside_interpolations_and_nested_blocks(self):
        src = 'let a = "\\(x /* 中文 */)"\n/* outer /* 内层 */ still */\n'
        self.assertEqual([p.split(":")[0] for p in apple_comments.problems("a.swift", src)], ["1", "2"])

    def test_quoted_headings_are_kept(self):
        ok = '/// See spec/README.md「夹具约定」; the hub sends 「终端等你处理」.\n'
        self.assertEqual(apple_comments.problems("a.swift", ok), [])
        self.assertEqual(len(apple_comments.problems("a.swift", "// The 形象's icon\n")), 1)
        self.assertEqual(len(apple_comments.problems("a.swift", "// 日本語のコメント\n")), 1)

    def test_yaml_and_html(self):
        self.assertEqual(len(apple_comments.problems("p.yml", 'a: "# 不是" # 是注释\n')), 1)
        self.assertEqual(apple_comments.problems("p.yml", 'a: "值 # 中"\n'), [])
        self.assertEqual(len(apple_comments.problems("r.html", "<p>正文</p><!-- 注释 -->")), 1)

    def test_strip_keeps_the_code(self):
        src = 'let a = 1 // 一\n/* 二 */ let b = "// 三"\n'
        self.assertEqual(apple_comments.strip(src), 'let a = 1 \n let b = "// 三"\n')


if __name__ == "__main__":
    unittest.main()
