import SwiftUI
import Testing
import UIKit
@testable import Persona

/// The file viewer's lexer (docs/apple/ios.md「File viewer」): what each language's words, strings and comments come out as,
/// and what carries over from one line to the next.
@MainActor
struct SyntaxTests {
    /// Each token of the lines as (text, kind), plain runs left out; lines lexed in order with one state.
    private func lex(_ lang: SyntaxLanguage, _ lines: String...) -> [[(String, SyntaxKind)]] {
        let lexer = SyntaxLexer(lang)
        var state = SyntaxState()
        return lines.map { line in
            let bytes = Array(line.utf8)
            var out: [SyntaxToken] = []
            bytes.withUnsafeBufferPointer { lexer.line($0, 0, $0.count, &state, &out) }
            return out.map { t in
                (String(decoding: bytes[Int(t.start)..<Int(t.start + t.length)], as: UTF8.self), t.kind)
            }
        }
    }

    private func kind(_ tokens: [(String, SyntaxKind)], _ text: String) -> SyntaxKind? {
        tokens.first { $0.0 == text }?.1
    }

    @Test func swift() {
        let t = lex(.swift, #"@MainActor func load(_ id: Int) async throws -> String { return "x\(id)" } // done"#)[0]
        #expect(kind(t, "@MainActor") == .attribute)
        #expect(kind(t, "func") == .keyword)
        #expect(kind(t, "load") == .function)
        #expect(kind(t, "Int") == .type)
        #expect(kind(t, "async") == .keyword)
        #expect(kind(t, "String") == .type)
        #expect(kind(t, #""x\(id)""#) == .string)
        #expect(kind(t, "// done") == .comment)
        // A multi-line string and a block comment carry over.
        let m = lex(.swift, #"let s = """"#, "  text: \"quoted\"", #"  """ + x"#, "/* a", "b */ let")
        #expect(kind(m[1], "  text: \"quoted\"") == .string)
        #expect(kind(m[2], #"  """"#) == .string)
        #expect(kind(m[4], "b */") == .comment)
        #expect(kind(m[4], "let") == .keyword)
    }

    @Test func go() {
        let t = lex(.go, "func main() { fmt.Println(len(xs), nil, 0x1F, 3.5e-2) }", "s := `raw", "still raw` + \"x\"")
        #expect(kind(t[0], "Println") == .function)
        #expect(kind(t[0], "len") == .function)
        #expect(kind(t[0], "nil") == .number)
        #expect(kind(t[0], "0x1F") == .number)
        #expect(kind(t[0], "3.5e-2") == .number)
        #expect(kind(t[1], "`raw") == .string)
        #expect(kind(t[2], "still raw`") == .string)
        #expect(kind(t[2], "\"x\"") == .string)
    }

    @Test func python() {
        let t = lex(.python, "@dataclass", "def f(x: int = None) -> str:", "    return f'{x}' + r\"\\d\"  # c", "    \"\"\"doc", "    more\"\"\"")
        #expect(kind(t[0], "@dataclass") == .attribute)
        #expect(kind(t[1], "def") == .keyword)
        #expect(kind(t[1], "int") == .type)
        #expect(kind(t[1], "None") == .number)
        #expect(kind(t[2], "f'{x}'") == .string)
        #expect(kind(t[2], "r\"\\d\"") == .string)
        #expect(kind(t[2], "# c") == .comment)
        #expect(kind(t[4], "    more\"\"\"") == .string)
    }

    @Test func shellAndDockerfile() {
        let s = lex(.shell, "if [ -n \"$HOME\" ]; then echo ${PATH} $1 a#b # note; fi")[0]
        #expect(kind(s, "if") == .keyword)
        #expect(kind(s, "echo") == .function)
        #expect(kind(s, "${PATH}") == .variable)
        #expect(kind(s, "$1") == .variable)
        #expect(kind(s, "# note; fi") == .comment)
        #expect(!s.contains { $0.0.hasPrefix("#b") }) // a#b is not a comment
        let d = lex(.dockerfile, "FROM golang:1.23 AS build", "RUN apt-get update && \\", "    copy files $X", "# c")
        #expect(kind(d[0], "FROM") == .keyword)
        #expect(kind(d[0], "AS") == .keyword)
        #expect(kind(d[1], "RUN") == .keyword)
        #expect(kind(d[2], "copy") == nil) // a continued line has no instruction
        #expect(kind(d[2], "$X") == .variable)
        #expect(kind(d[3], "# c") == .comment)
    }

    @Test func dataFormats() {
        let y = lex(.yaml, "name: web # the app", "  - image: \"nginx:1.27\"", "    ports: [80, 443]", "  base: &b true", "url: http://x:1")
        #expect(kind(y[0], "name") == .tag)
        #expect(kind(y[0], "# the app") == .comment)
        #expect(kind(y[1], "-") == .keyword)
        #expect(kind(y[1], "image") == .tag)
        #expect(kind(y[1], "\"nginx:1.27\"") == .string)
        #expect(kind(y[2], "80") == .number)
        #expect(kind(y[3], "&b") == .attribute)
        #expect(kind(y[3], "true") == .number)
        #expect(kind(y[4], "url") == .tag)
        let toml = lex(.toml, "[package]", "name = \"persona\" # n", "edition = 2021")
        #expect(kind(toml[0], "[package]") == .type)
        #expect(kind(toml[1], "name") == .tag)
        #expect(kind(toml[1], "\"persona\"") == .string)
        #expect(kind(toml[2], "2021") == .number)
        let j = lex(.json, #"{"name": "x", "n": -1.5, "ok": true, "v": null}"#)[0]
        #expect(kind(j, #""name""#) == .tag)
        #expect(kind(j, #""x""#) == .string)
        #expect(kind(j, "-1.5") == nil || kind(j, "1.5") == .number)
        #expect(kind(j, "true") == .number)
        let sql = lex(.sql, "SELECT id, count(*) from users WHERE name = 'a' -- c")[0]
        #expect(kind(sql, "SELECT") == .keyword)
        #expect(kind(sql, "from") == .keyword)
        #expect(kind(sql, "count") == .function)
        #expect(kind(sql, "'a'") == .string)
        #expect(kind(sql, "-- c") == .comment)
    }

    @Test func markupAndStyles() {
        let h = lex(.html, #"<div class="a" id='b'>&amp; text <!-- c"#, "still --> <br/>")
        #expect(kind(h[0], "<div") == .tag)
        #expect(kind(h[0], "class") == .attribute)
        #expect(kind(h[0], #""a""#) == .string)
        #expect(kind(h[0], "&amp;") == .number)
        #expect(kind(h[0], "<!-- c") == .comment)
        #expect(kind(h[1], "still -->") == .comment)
        #expect(kind(h[1], "<br") == .tag)
        let c = lex(.css, ".card:hover > a { color: #fff; margin: 0 1.5rem !important; }", "@media (max-width: 600px) {")
        #expect(kind(c[0], ".card") == .attribute)
        #expect(kind(c[0], ":hover") == .keyword)
        #expect(kind(c[0], "color") == .tag)
        #expect(kind(c[0], "#fff") == .number)
        #expect(kind(c[0], "1.5rem") == .number)
        #expect(kind(c[0], "!important") == .keyword)
        #expect(kind(c[1], "@media") == .keyword)
    }

    @Test func rustAndMarkdownAndDiff() {
        let r = lex(.rust, "#[derive(Debug)]", "fn f<'a>(s: &'a str) -> char { println!(\"{}\", s); 'x' }")
        #expect(kind(r[0], "#[derive(Debug)]") == .attribute)
        #expect(kind(r[1], "'a") == .variable)
        #expect(kind(r[1], "println!") == .function)
        #expect(kind(r[1], "'x'") == .string)
        #expect(kind(r[1], "str") == .type)
        let m = lex(.markdown, "# Title", "Some `code` and **bold** and [a link](https://x.y).", "```swift", "let # not a heading", "```", "- item")
        #expect(kind(m[0], "# Title") == .heading)
        #expect(kind(m[1], "`code`") == .code)
        #expect(kind(m[1], "**bold**") == .strong)
        #expect(kind(m[1], "[a link](https://x.y)") == .link)
        #expect(kind(m[2], "```swift") == .meta)
        #expect(kind(m[3], "let # not a heading") == .code)
        #expect(kind(m[5], "-") == .keyword)
        let d = lex(.diff, "@@ -1,2 +1,3 @@", "+added", "-removed", " same")
        #expect(d.map { $0.first?.1 } == [.meta, .added, .removed, nil])
    }

    @Test func detection() {
        #expect(SyntaxLanguage.detect(name: "a/b/Main.swift")?.id == "swift")
        #expect(SyntaxLanguage.detect(name: "Dockerfile")?.id == "dockerfile")
        #expect(SyntaxLanguage.detect(name: "docker/Dockerfile.dev")?.id == "dockerfile")
        #expect(SyntaxLanguage.detect(name: "Makefile")?.id == "makefile")
        #expect(SyntaxLanguage.detect(name: ".env.local")?.id == "ini")
        #expect(SyntaxLanguage.detect(name: ".env")?.id == "ini")
        #expect(SyntaxLanguage.detect(name: "app/.envrc")?.id == "shell")
        #expect(SyntaxLanguage.detect(name: "deploy", firstLine: "#!/usr/bin/env bash")?.id == "shell")
        #expect(SyntaxLanguage.detect(name: "tool", firstLine: "#!/usr/bin/env python3")?.id == "python")
        #expect(SyntaxLanguage.detect(name: "Info.plist")?.id == "xml")
        #expect(SyntaxLanguage.detect(name: "notes.txt") == nil)
        #expect(SyntaxLanguage.detect(name: "x.tsx")?.id == "typescript")
    }

    /// Token byte offsets become the right UTF-16 ranges in a line with Chinese and emoji before them.
    private func rgb(_ c: UIColor?) -> [Int] {
        var r: CGFloat = 0, g: CGFloat = 0, b: CGFloat = 0, a: CGFloat = 0
        c?.resolvedColor(with: UITraitCollection(userInterfaceStyle: .light)).getRed(&r, green: &g, blue: &b, alpha: &a)
        return [r, g, b].map { Int(($0 * 255).rounded()) }
    }

    @Test func offsetsInWideText() {
        let line = "let 名字 = \"你好👋\" // 注释"
        let bytes = Array(line.utf8)
        let lexer = SyntaxLexer(.swift)
        var state = SyntaxState()
        var tokens: [SyntaxToken] = []
        bytes.withUnsafeBufferPointer { lexer.line($0, 0, $0.count, &state, &tokens) }
        let a = bytes.withUnsafeBufferPointer { SyntaxStyle.line(line, bytes: $0, tokens: tokens[...], cut: false) }
        func colored(_ s: String) -> UIColor? {
            let r = (line as NSString).range(of: s)
            return a.attribute(.foregroundColor, at: r.location, effectiveRange: nil) as? UIColor
        }
        #expect(rgb(colored("\"你好👋\"")) == rgb(UIColor(Theme.Syntax.string)))
        #expect(rgb(colored("// 注释")) == rgb(UIColor(Theme.Syntax.comment)))
        #expect(rgb(colored("let")) == rgb(UIColor(Theme.Syntax.keyword)))
        #expect(rgb(colored("名字")) == rgb(UIColor.label))
    }
}

/// Every syntax color reads on the code background (≥ 4.5:1) in light and dark, in every style (the background is
/// the same in all of them).
@MainActor
struct SyntaxPaletteTests {
    @Test func everyColorReadsOnTheCodeBackground() {
        var low: [String] = []
        for (name, color) in Theme.Syntax.all {
            for style in [UIUserInterfaceStyle.light, .dark] {
                let c = StyleSpecTests.contrast(color, Theme.codeBackground, style)
                if c < 4.5 { low.append("\(name) \(style == .dark ? "dark" : "light") \(String(format: "%.2f", c))") }
            }
        }
        #expect(low.isEmpty, "under 4.5:1 on the code background: \(low)")
    }
}
