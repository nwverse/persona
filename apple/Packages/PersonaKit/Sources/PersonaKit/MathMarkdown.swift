import Foundation

/// Formulas in Markdown (docs/apple/ios.md「Math and diagrams」), rewritten into what MarkdownUI can carry before it parses:
/// - display math — `$$…$$` and `\[…\]` on lines of their own, or a line that is one formula — becomes a ```math
///   fence (`RenderedBlock` draws it);
/// - inline math — `$…$` and `\(…\)` — becomes an inline image `![…](persona-math:…)` (`MathInlineImages`).
/// Code spans, fences and indented code are left alone. A `$` opens only before a non-space and closes only after one
/// and not before a digit (pandoc's rule), so "$5 and $10" stays text; `\$` is a dollar. A span with CJK words in it
/// is prose, not a formula.
public nonisolated enum MathMarkdown {
    public static let scheme = "persona-math"
    public static let fence = "math"

    public static func rewrite(_ md: String) -> String {
        guard md.contains("$") || md.contains("\\(") || md.contains("\\[") else { return md }
        var out: [String] = []
        var fence: MarkdownFence?
        var block: (end: String, lines: [String])?
        for line in md.split(separator: "\n", omittingEmptySubsequences: false).map(String.init) {
            let t = line.trimmingCharacters(in: .whitespaces)
            if var b = block {
                if t == b.end {
                    out += ["```" + Self.fence] + b.lines + ["```"]
                    block = nil
                } else {
                    b.lines.append(line)
                    block = b
                }
                continue
            }
            if let f = fence {
                if f.isClosed(by: t) { fence = nil }
                out.append(line)
                continue
            }
            if let f = MarkdownFence.opening(t) {
                fence = f
                out.append(line)
                continue
            }
            if line.hasPrefix("    ") || line.hasPrefix("\t") {
                out.append(line)
                continue
            }
            if t == "$$" || t == "\\[" {
                block = (t == "$$" ? "$$" : "\\]", [])
                continue
            }
            if let tex = display(t) {
                out += ["```" + Self.fence, tex, "```"]
                continue
            }
            out.append(inline(line))
        }
        if let b = block { // never closed: as it was
            out += [b.end == "$$" ? "$$" : "\\["] + b.lines
        }
        return out.joined(separator: "\n")
    }

    /// A line that is one formula: `$$x$$`, `\[x\]`, or `$x$` alone.
    public static func display(_ t: String) -> String? {
        for (open, close) in [("$$", "$$"), ("\\[", "\\]")] where t.count > open.count + close.count && t.hasPrefix(open) && t.hasSuffix(close) {
            let tex = String(t.dropFirst(open.count).dropLast(close.count))
            if !tex.contains(close), !tex.trimmingCharacters(in: .whitespaces).isEmpty { return tex.trimmingCharacters(in: .whitespaces) }
        }
        let spans = inlineSpans(Array(t))
        if spans.count == 1, spans[0].range == 0..<t.count { return spans[0].tex }
        return nil
    }

    public struct Span: Equatable {
        public var range: Range<Int>
        public var tex: String
    }

    /// The inline formulas of a line, outside code spans.
    public static func inlineSpans(_ c: [Character]) -> [Span] {
        var spans: [Span] = []
        var i = 0
        var tick = 0 // inside a code span of this many backticks
        while i < c.count {
            if c[i] == "`" {
                var n = 0
                while i + n < c.count, c[i + n] == "`" { n += 1 }
                tick = tick == 0 ? n : (tick == n ? 0 : tick)
                i += n
                continue
            }
            if tick > 0 { i += 1; continue }
            if c[i] == "\\", i + 1 < c.count {
                if c[i + 1] == "(", let end = find(c, ["\\", ")"], from: i + 2) {
                    spans.append(Span(range: i..<(end + 2), tex: String(c[(i + 2)..<end])))
                    i = end + 2
                } else {
                    i += 2 // an escape (\$ is a dollar)
                }
                continue
            }
            if c[i] == "$", i + 1 < c.count, c[i + 1] != "$", !c[i + 1].isWhitespace {
                var j = i + 1
                var close: Int?
                while j < c.count {
                    if c[j] == "\\" { j += 2; continue }
                    if c[j] == "$" {
                        let afterDigit = j + 1 < c.count && c[j + 1].isNumber
                        if !c[j - 1].isWhitespace, !afterDigit, j + 1 >= c.count || c[j + 1] != "$" { close = j }
                        break
                    }
                    j += 1
                }
                if let close, plausible(c[(i + 1)..<close]) {
                    spans.append(Span(range: i..<(close + 1), tex: String(c[(i + 1)..<close])))
                    i = close + 1
                    continue
                }
            }
            i += 1
        }
        return spans
    }

    /// A formula has no Chinese or Japanese words in it (outside `\text{…}`): between 「$10 一个月：」 and the next `$`
    /// is prose with two dollars in it.
    private static func plausible(_ tex: ArraySlice<Character>) -> Bool {
        guard tex.count <= 400 else { return false }
        let wide = tex.contains { ch in ch.unicodeScalars.contains { (0x3000...0x9FFF).contains($0.value) || (0xFF00...0xFFEF).contains($0.value) } }
        return !wide || String(tex).contains("\\text")
    }

    private static func find(_ c: [Character], _ pair: [Character], from: Int) -> Int? {
        var j = from
        while j + 1 < c.count {
            if c[j] == pair[0], c[j + 1] == pair[1] { return j }
            j += 1
        }
        return nil
    }

    private static func inline(_ line: String) -> String {
        let c = Array(line)
        let spans = inlineSpans(c)
        guard !spans.isEmpty else { return line }
        var out = ""
        var at = 0
        for s in spans {
            out += String(c[at..<s.range.lowerBound])
            out += "![\(label(s.tex))](\(url(s.tex)))"
            at = s.range.upperBound
        }
        return out + String(c[at...])
    }

    /// The image's alt text (what VoiceOver reads): the TeX, without what would end the brackets.
    private static func label(_ tex: String) -> String {
        tex.replacingOccurrences(of: "[", with: "(").replacingOccurrences(of: "]", with: ")").replacingOccurrences(of: "\n", with: " ")
    }

    public static func url(_ tex: String) -> String {
        scheme + ":" + (tex.addingPercentEncoding(withAllowedCharacters: .alphanumerics) ?? "")
    }

    /// The TeX of a `persona-math:` URL.
    public static func tex(_ url: URL) -> String? {
        guard url.scheme == scheme else { return nil }
        let raw = url.absoluteString.dropFirst(scheme.count + 1)
        return String(raw).removingPercentEncoding
    }
}
