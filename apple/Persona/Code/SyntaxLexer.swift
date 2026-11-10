import Foundation

/// What a line leaves open for the next: a block comment, a string that runs over lines, an HTML comment or tag,
/// a Markdown code fence; CSS's brace depth; a line ending in `\` (Dockerfile).
nonisolated struct SyntaxState: Equatable, Sendable {
    enum Inside: Equatable, Sendable { case none, comment, string(Int), markupComment, markupTag, fence }
    var inside: Inside = .none
    var depth: Int32 = 0
    var continued = false
}

/// Colors source a line at a time (docs/apple/ios.md「File viewer」): one pass over the line's UTF-8 bytes, words looked up
/// by a hash of their bytes. No regular expressions and no String per word: a few MB take a fraction of a second,
/// off the main thread (`CodeDocument.highlight`).
nonisolated struct SyntaxLexer {
    typealias Bytes = UnsafeBufferPointer<UInt8>
    let lang: SyntaxLanguage
    private let lineComments: [[UInt8]]
    private let blockOpen: [UInt8]?
    private let blockClose: [UInt8]

    init(_ lang: SyntaxLanguage) {
        self.lang = lang
        lineComments = lang.lineComments.map { Array($0.utf8) }
        blockOpen = lang.blockComment.map { Array($0.open.utf8) }
        blockClose = lang.blockComment.map { Array($0.close.utf8) } ?? []
    }

    /// FNV-1a over the bytes (ASCII letters lowered when `lower`).
    static func hash(_ b: Bytes, _ from: Int, _ to: Int, lower: Bool) -> UInt64 {
        var h: UInt64 = 0xcbf2_9ce4_8422_2325
        for i in from..<to {
            var c = b[i]
            if lower, c >= 65, c <= 90 { c += 32 }
            h = (h ^ UInt64(c)) &* 0x100_0000_01b3
        }
        return h
    }

    /// The tokens of the line `b[start..<end]` (no newline), offsets from `start`, after what the line before left
    /// open (`state`, updated for the next line).
    func line(_ b: Bytes, _ start: Int, _ end: Int, _ state: inout SyntaxState, _ out: inout [SyntaxToken]) {
        // The tokens move in and out (swap): a copy of the document's growing array per line would be quadratic.
        var o = Output(lineStart: start, tokens: [])
        swap(&o.tokens, &out)
        defer { swap(&o.tokens, &out) }
        switch lang.mode {
        case .code, .shell, .json:
            code(b, resume(b, start, end, &state, &o), start, end, &state, &o)
        case .yaml: yaml(b, start, end, &state, &o)
        case .toml: toml(b, start, end, &state, &o)
        case .markup: markup(b, start, end, &state, &o)
        case .css: css(b, start, end, &state, &o)
        case .dockerfile: dockerfile(b, start, end, &state, &o)
        case .makefile: makefile(b, start, end, &state, &o)
        case .markdown: markdown(b, start, end, &state, &o)
        case .diff: diff(b, start, end, &o)
        }
    }

    struct Output {
        var lineStart: Int
        var tokens: [SyntaxToken]
        mutating func emit(_ s: Int, _ e: Int, _ kind: SyntaxKind) {
            guard e > s, kind != .plain else { return }
            tokens.append(SyntaxToken(start: Int32(s - lineStart), length: Int32(e - s), kind: kind))
        }
    }

    // MARK: Bytes

    @inline(__always) static func isSpace(_ c: UInt8) -> Bool { c == 32 || c == 9 || c == 13 }
    @inline(__always) static func isDigit(_ c: UInt8) -> Bool { c >= 48 && c <= 57 }
    @inline(__always) static func isLetter(_ c: UInt8) -> Bool { (c >= 65 && c <= 90) || (c >= 97 && c <= 122) || c == 95 || c >= 0x80 }
    @inline(__always) private func isIdentStart(_ c: UInt8) -> Bool { Self.isLetter(c) || (c != 45 && lang.identifierExtraBytes.contains(c)) }
    @inline(__always) private func isIdent(_ c: UInt8) -> Bool { Self.isLetter(c) || Self.isDigit(c) || lang.identifierExtraBytes.contains(c) }

    @inline(__always) static func at(_ b: Bytes, _ i: Int, _ end: Int, _ p: [UInt8]) -> Bool {
        guard !p.isEmpty, i + p.count <= end else { return false }
        for k in 0..<p.count where b[i + k] != p[k] { return false }
        return true
    }

    static func find(_ b: Bytes, _ from: Int, _ end: Int, _ p: [UInt8]) -> Int? {
        guard !p.isEmpty else { return nil }
        var i = from
        while i + p.count <= end {
            if b[i] == p[0], at(b, i, end, p) { return i }
            i += 1
        }
        return nil
    }

    static func skipSpaces(_ b: Bytes, _ i: Int, _ end: Int) -> Int {
        var i = i
        while i < end, isSpace(b[i]) { i += 1 }
        return i
    }

    @inline(__always) static func next(_ b: Bytes, _ i: Int, _ end: Int) -> UInt8 {
        let j = skipSpaces(b, i, end)
        return j < end ? b[j] : 0
    }

    // MARK: The generic lexer (C-like languages, scripts, JSON, the values of YAML / TOML)

    /// Carries a block comment or a multi-line string over from the line before; where the rest of the line starts.
    private func resume(_ b: Bytes, _ start: Int, _ end: Int, _ state: inout SyntaxState, _ o: inout Output) -> Int {
        switch state.inside {
        case .comment:
            if let j = Self.find(b, start, end, blockClose) {
                state.inside = .none
                o.emit(start, j + blockClose.count, .comment)
                return j + blockClose.count
            }
            o.emit(start, end, .comment)
            return end
        case .string(let r):
            state.inside = .none
            return string(b, start, start, r, end, &state, &o)
        default:
            return start
        }
    }

    private func stringAt(_ b: Bytes, _ i: Int, _ end: Int) -> Int? {
        for (r, s) in lang.strings.enumerated() where Self.at(b, i, end, s.open) { return r }
        return nil
    }

    /// A string from `s` (its prefix or opener) whose body starts at `body`; where it ends (or the line's end, the
    /// string left open in `state` when it may run over lines).
    private func string(_ b: Bytes, _ s: Int, _ body: Int, _ r: Int, _ end: Int, _ state: inout SyntaxState, _ o: inout Output) -> Int {
        let rule = lang.strings[r]
        var i = body
        while i < end {
            if rule.escapes, b[i] == 92 {
                i += 2
                continue
            }
            if b[i] == rule.close[0], Self.at(b, i, end, rule.close) {
                i += rule.close.count
                o.emit(s, i, lang.keyedStrings && Self.next(b, i, end) == 58 ? .tag : .string)
                return i
            }
            i += 1
        }
        o.emit(s, end, .string)
        if rule.multiline { state.inside = .string(r) }
        return end
    }

    private func isLineComment(_ b: Bytes, _ i: Int, _ lineStart: Int, _ end: Int) -> Bool {
        for p in lineComments where Self.at(b, i, end, p) {
            if lang.hashNeedsSpace, p == [35], i > lineStart, !Self.isSpace(b[i - 1]) { continue }
            return true
        }
        return false
    }

    private func code(_ b: Bytes, _ from: Int, _ lineStart: Int, _ end: Int, _ state: inout SyntaxState, _ o: inout Output) {
        var i = from
        var prev: UInt8 = 0 // the last byte before i that is not a space
        while i < end {
            let c = b[i]
            if Self.isSpace(c) {
                i += 1
                continue
            }
            // Block comments first: Lua's --[[ starts like its -- line comments.
            if let open = blockOpen, c == open[0], Self.at(b, i, end, open) {
                if let j = Self.find(b, i + open.count, end, blockClose) {
                    o.emit(i, j + blockClose.count, .comment)
                    i = j + blockClose.count
                    continue
                }
                o.emit(i, end, .comment)
                state.inside = .comment
                return
            }
            if isLineComment(b, i, lineStart, end) {
                o.emit(i, end, .comment)
                return
            }
            if c == 35, lang.preprocessor, Self.skipSpaces(b, lineStart, i) == i {
                o.emit(i, end, .attribute)
                return
            }
            if c == 35, lang.hashAttributes, i + 1 < end, b[i + 1] == 91 || b[i + 1] == 33 {
                let j = Self.find(b, i, end, [93]) ?? (end - 1)
                o.emit(i, j + 1, .attribute)
                i = j + 1
                prev = 93
                continue
            }
            if let r = stringAt(b, i, end) {
                i = string(b, i, i + lang.strings[r].open.count, r, end, &state, &o)
                if state.inside != .none { return }
                prev = 34
                continue
            }
            if Self.isDigit(c) || (c == 46 && i + 1 < end && Self.isDigit(b[i + 1]) && !isIdent(prev)) {
                let s = i
                let hex = c == 48 && i + 1 < end && (b[i + 1] == 120 || b[i + 1] == 88)
                i += 1
                while i < end {
                    let d = b[i]
                    if isIdent(d) || d == 46 || (!hex && (d == 43 || d == 45) && (b[i - 1] == 101 || b[i - 1] == 69)) {
                        i += 1
                    } else {
                        break
                    }
                }
                o.emit(s, i, .number)
                prev = 48
                continue
            }
            if c == 64, lang.annotations, i + 1 < end, isIdentStart(b[i + 1]) {
                let s = i
                i += 1
                while i < end, isIdent(b[i]) || b[i] == 46 { i += 1 }
                o.emit(s, i, .attribute)
                prev = 97
                continue
            }
            if c == 36, lang.dollarVariables, i + 1 < end, let e = variableEnd(b, i, end) {
                o.emit(i, e, .variable)
                i = e
                prev = 97
                continue
            }
            if lang.mode == .yaml, c == 38 || c == 42 || c == 33, i + 1 < end, isIdentStart(b[i + 1]) || b[i + 1] == 33 {
                let s = i // &anchor, *alias, !!tag
                i += 1
                while i < end, !Self.isSpace(b[i]), b[i] != 44, b[i] != 93, b[i] != 125 { i += 1 }
                o.emit(s, i, .attribute)
                continue
            }
            if c == 39, lang.lifetimes, i + 1 < end {
                // 'a' and '\n' are characters; 'a with no quote right after is a lifetime.
                if b[i + 1] == 92, let j = Self.find(b, i + 2, min(end, i + 12), [39]) {
                    o.emit(i, j + 1, .string)
                    i = j + 1
                } else if i + 2 < end, b[i + 2] == 39 {
                    o.emit(i, i + 3, .string)
                    i += 3
                } else if isIdentStart(b[i + 1]) {
                    let s = i
                    i += 1
                    while i < end, isIdent(b[i]) { i += 1 }
                    o.emit(s, i, .variable)
                } else {
                    i += 1
                }
                prev = 39
                continue
            }
            if isIdentStart(c) {
                let s = i
                i += 1
                while i < end, isIdent(b[i]) { i += 1 }
                // A string's prefix: r"…", f'…', b"…".
                if i < end, let r = stringAt(b, i, end), !lang.strings[r].prefixes.isEmpty,
                   lang.strings[r].prefixes.contains(String(decoding: UnsafeBufferPointer(rebasing: b[s..<i]), as: UTF8.self)) {
                    i = string(b, s, i + lang.strings[r].open.count, r, end, &state, &o)
                    if state.inside != .none { return }
                    prev = 34
                    continue
                }
                let kind = word(b, s, &i, end, after: prev) // moves i past a macro's ! before the token is cut
                o.emit(s, i, kind)
                prev = b[i - 1]
                continue
            }
            prev = c
            i += 1
        }
    }

    /// What an identifier `b[s..<i]` is: a word of the language, a type by its capital, a call, a macro (`i` moves
    /// past the `!`).
    private func word(_ b: Bytes, _ s: Int, _ i: inout Int, _ end: Int, after prev: UInt8) -> SyntaxKind {
        if i - s <= lang.longestWord, let k = lang.words[Self.hash(b, s, i, lower: lang.caseInsensitive)] {
            // A member named like a keyword (x.type, obj.default) is a member, not the keyword.
            if prev == 46, k == .keyword { return Self.next(b, i, end) == 40 ? .function : .plain }
            return k
        }
        if lang.macroBang, i < end, b[i] == 33, !(i + 1 < end && b[i + 1] == 61) {
            i += 1
            return .function
        }
        if lang.capitalizedTypes, b[s] >= 65, b[s] <= 90 { return .type }
        if Self.next(b, i, end) == 40 { return .function }
        return .plain
    }

    /// `$name`, `${…}`, `$(…)` (Makefile) or `$1` / `$@`: where it ends; nil for a lone `$`.
    private func variableEnd(_ b: Bytes, _ i: Int, _ end: Int) -> Int? {
        let d = b[i + 1]
        if d == 123, let j = Self.find(b, i + 2, end, [125]) { return j + 1 }
        if d == 40 {
            if lang.mode == .makefile, let j = Self.find(b, i + 2, end, [41]) { return j + 1 }
            return i + 2 // $( of a command: the command itself is lexed as usual
        }
        if Self.isLetter(d) {
            var j = i + 2
            while j < end, Self.isLetter(b[j]) || Self.isDigit(b[j]) { j += 1 }
            return j
        }
        if Self.isDigit(d) || [64, 42, 35, 63, 36, 33, 95].contains(d) { return i + 2 }
        return nil
    }

    // MARK: YAML, TOML / INI

    private func yaml(_ b: Bytes, _ start: Int, _ end: Int, _ state: inout SyntaxState, _ o: inout Output) {
        var i = Self.skipSpaces(b, start, end)
        guard i < end else { return }
        if b[i] == 35 {
            o.emit(i, end, .comment)
            return
        }
        if i == start, Self.at(b, i, end, [45, 45, 45]) || Self.at(b, i, end, [46, 46, 46]) {
            o.emit(i, i + 3, .meta)
            i += 3
        }
        while i < end, b[i] == 45, i + 1 == end || Self.isSpace(b[i + 1]) {
            o.emit(i, i + 1, .keyword)
            i = Self.skipSpaces(b, i + 1, end)
        }
        if let k = keyEnd(b, i, end, separators: [58], needsSpaceAfter: true) {
            o.emit(i, k.end, .tag)
            i = k.separator + 1
        }
        let j = Self.skipSpaces(b, i, end)
        if j < end, b[j] == 124 || b[j] == 62, Self.next(b, j + 1, end) == 0 || Self.next(b, j + 1, end) == 45 || Self.next(b, j + 1, end) == 43 {
            o.emit(j, end, .keyword) // block scalar | > (the text under it stays plain)
            return
        }
        code(b, i, start, end, &state, &o)
    }

    private func toml(_ b: Bytes, _ start: Int, _ end: Int, _ state: inout SyntaxState, _ o: inout Output) {
        var i = resume(b, start, end, &state, &o)
        if i > start || state.inside != .none {
            code(b, i, start, end, &state, &o)
            return
        }
        i = Self.skipSpaces(b, start, end)
        guard i < end else { return }
        if isLineComment(b, i, start, end) {
            o.emit(i, end, .comment)
            return
        }
        if b[i] == 91, let j = Self.find(b, i, end, [93]) { // [section], [[array]]
            let e = j + 1 < end && b[j + 1] == 93 ? j + 2 : j + 1
            o.emit(i, e, .type)
            code(b, e, start, end, &state, &o)
            return
        }
        if let k = keyEnd(b, i, end, separators: [61, 58], needsSpaceAfter: false) {
            o.emit(i, k.end, .tag)
            i = k.separator + 1
        }
        code(b, i, start, end, &state, &o)
    }

    /// Where a key ends and its separator is, from `i`: a bare or quoted key before `:` / `=`; nil when the line has
    /// none (a list item, a value, a comment).
    private func keyEnd(_ b: Bytes, _ i: Int, _ end: Int, separators: [UInt8], needsSpaceAfter: Bool) -> (end: Int, separator: Int)? {
        guard i < end else { return nil }
        var j = i
        if b[j] == 34 || b[j] == 39 {
            let q = b[j]
            j += 1
            while j < end, b[j] != q { j += b[j] == 92 && q == 34 ? 2 : 1 }
            let close = min(j + 1, end)
            j = Self.skipSpaces(b, close, end)
            guard j < end, separators.contains(b[j]) else { return nil }
            return (close, j)
        }
        while j < end {
            let c = b[j]
            if separators.contains(c) {
                if needsSpaceAfter, j + 1 < end, !Self.isSpace(b[j + 1]) {
                    j += 1 // a colon inside a word (a URL, a time) is not the key's end
                    continue
                }
                var k = j
                while k > i, Self.isSpace(b[k - 1]) { k -= 1 }
                return k > i ? (k, j) : nil
            }
            if c == 35 || c == 123 || c == 91 || c == 44 || c == 34 || c == 39 { return nil }
            j += 1
        }
        return nil
    }

    // MARK: HTML / XML

    private func markup(_ b: Bytes, _ start: Int, _ end: Int, _ state: inout SyntaxState, _ o: inout Output) {
        var i = start
        if state.inside == .markupComment {
            guard let j = Self.find(b, i, end, Array("-->".utf8)) else {
                o.emit(i, end, .comment)
                return
            }
            o.emit(i, j + 3, .comment)
            i = j + 3
            state.inside = .none
        }
        if state.inside == .markupTag { i = tagBody(b, i, end, &state, &o) }
        while i < end {
            let c = b[i]
            if c == 60, Self.at(b, i, end, Array("<!--".utf8)) {
                if let j = Self.find(b, i + 4, end, Array("-->".utf8)) {
                    o.emit(i, j + 3, .comment)
                    i = j + 3
                    continue
                }
                o.emit(i, end, .comment)
                state.inside = .markupComment
                return
            }
            if c == 60, i + 1 < end, Self.isLetter(b[i + 1]) || b[i + 1] == 47 || b[i + 1] == 33 || b[i + 1] == 63 {
                let s = i
                i += 1
                if b[i] == 47 || b[i] == 33 || b[i] == 63 { i += 1 }
                while i < end, Self.isLetter(b[i]) || Self.isDigit(b[i]) || b[i] == 45 || b[i] == 58 || b[i] == 46 { i += 1 }
                o.emit(s, i, .tag)
                state.inside = .markupTag
                i = tagBody(b, i, end, &state, &o)
                continue
            }
            if c == 38, let j = Self.find(b, i, min(end, i + 12), [59]) { // &amp;
                o.emit(i, j + 1, .number)
                i = j + 1
                continue
            }
            i += 1
        }
    }

    /// Attributes up to the tag's `>` (the tag may go on over lines).
    private func tagBody(_ b: Bytes, _ from: Int, _ end: Int, _ state: inout SyntaxState, _ o: inout Output) -> Int {
        var i = from
        while i < end {
            let c = b[i]
            if c == 62 || (c == 47 && i + 1 < end && b[i + 1] == 62) || (c == 63 && i + 1 < end && b[i + 1] == 62) {
                let e = c == 62 ? i + 1 : i + 2
                o.emit(i, e, .tag)
                state.inside = .none
                return e
            }
            if c == 34 || c == 39 {
                let s = i
                i += 1
                while i < end, b[i] != c { i += 1 }
                i = min(i + 1, end)
                o.emit(s, i, .string)
                continue
            }
            if Self.isLetter(c) {
                let s = i
                while i < end, Self.isLetter(b[i]) || Self.isDigit(b[i]) || b[i] == 45 || b[i] == 58 || b[i] == 46 { i += 1 }
                o.emit(s, i, .attribute)
                continue
            }
            i += 1
        }
        return end
    }

    // MARK: CSS

    private func css(_ b: Bytes, _ start: Int, _ end: Int, _ state: inout SyntaxState, _ o: inout Output) {
        var i = resume(b, start, end, &state, &o)
        while i < end {
            let c = b[i]
            if Self.isSpace(c) {
                i += 1
                continue
            }
            if let open = blockOpen, Self.at(b, i, end, open) {
                if let j = Self.find(b, i + 2, end, blockClose) {
                    o.emit(i, j + 2, .comment)
                    i = j + 2
                    continue
                }
                o.emit(i, end, .comment)
                state.inside = .comment
                return
            }
            if isLineComment(b, i, start, end) {
                o.emit(i, end, .comment)
                return
            }
            if let r = stringAt(b, i, end) {
                i = string(b, i, i + 1, r, end, &state, &o)
                continue
            }
            if c == 123 { state.depth += 1 }
            if c == 125 { state.depth = max(0, state.depth - 1) }
            if c == 64 || c == 33, i + 1 < end, Self.isLetter(b[i + 1]) { // @media, !important
                let s = i
                i += 1
                while i < end, isIdent(b[i]) { i += 1 }
                o.emit(s, i, .keyword)
                continue
            }
            if c == 36, lang.dollarVariables, let e = variableEnd(b, i, end) {
                o.emit(i, e, .variable)
                i = e
                continue
            }
            if (c == 35 || c == 46) && i + 1 < end && (Self.isLetter(b[i + 1]) || Self.isDigit(b[i + 1]) || b[i + 1] == 45) {
                if state.depth > 0 && c == 46 && Self.isDigit(b[i + 1]) {
                    // .5em: a number
                } else {
                    let s = i // #fff in a rule; #id / .class in a selector
                    i += 1
                    while i < end, isIdent(b[i]) { i += 1 }
                    o.emit(s, i, state.depth > 0 && c == 35 ? .number : .attribute)
                    continue
                }
            }
            if Self.isDigit(c) || (c == 46 && i + 1 < end && Self.isDigit(b[i + 1])) || (c == 45 && i + 1 < end && Self.isDigit(b[i + 1])) {
                let s = i
                i += 1
                while i < end, Self.isDigit(b[i]) || b[i] == 46 || Self.isLetter(b[i]) || b[i] == 37 { i += 1 }
                o.emit(s, i, .number)
                continue
            }
            if c == 58, state.depth == 0, i + 1 < end, Self.isLetter(b[i + 1]) || b[i + 1] == 58 { // :hover, ::before
                let s = i
                i += b[i + 1] == 58 ? 2 : 1
                while i < end, isIdent(b[i]) { i += 1 }
                o.emit(s, i, .keyword)
                continue
            }
            if isIdentStart(c) || (c == 45 && i + 1 < end && isIdentStart(b[i + 1])) {
                let s = i
                i += 1
                while i < end, isIdent(b[i]) { i += 1 }
                let kind: SyntaxKind
                if state.depth > 0, Self.next(b, i, end) == 58 {
                    kind = .tag // a property
                } else if Self.next(b, i, end) == 40 {
                    kind = .function // rgb(…), var(…)
                } else if state.depth == 0 {
                    kind = .tag // an element in a selector
                } else {
                    kind = lang.words[Self.hash(b, s, i, lower: true)] ?? .plain
                }
                o.emit(s, i, kind)
                continue
            }
            i += 1
        }
    }

    // MARK: Dockerfile, Makefile

    /// Dockerfile instructions, lowered and hashed: a keyword only as a line's first word (inside a RUN line, `copy`
    /// is a word of the shell).
    private static let dockerInstructions: Set<UInt64> = Set(
        "from as run cmd label maintainer expose env add copy entrypoint volume user workdir arg onbuild stopsignal healthcheck shell"
            .split(separator: " ").map { w in Array(w.utf8).withUnsafeBufferPointer { hash($0, 0, $0.count, lower: false) } })

    private static let fromHash: UInt64 = Array("from".utf8).withUnsafeBufferPointer { hash($0, 0, $0.count, lower: false) }

    private func dockerfile(_ b: Bytes, _ start: Int, _ end: Int, _ state: inout SyntaxState, _ o: inout Output) {
        let continued = state.continued
        var i = resume(b, start, end, &state, &o)
        i = Self.skipSpaces(b, i, end)
        if !continued, i < end, i == Self.skipSpaces(b, start, end) {
            if b[i] == 35 {
                o.emit(i, end, .comment)
                state.continued = false
                return
            }
            var j = i
            while j < end, Self.isLetter(b[j]) { j += 1 }
            let instruction = Self.hash(b, i, j, lower: true)
            if j > i, Self.dockerInstructions.contains(instruction) {
                o.emit(i, j, .keyword)
                i = j
                // FROM image AS name
                if instruction == Self.fromHash,
                   let a = Self.find(b, i, end, Array(" AS ".utf8)) ?? Self.find(b, i, end, Array(" as ".utf8)) {
                    code(b, i, start, a + 1, &state, &o)
                    o.emit(a + 1, a + 3, .keyword)
                    i = a + 3
                }
            }
        }
        code(b, i, start, end, &state, &o)
        var k = end
        while k > start, Self.isSpace(b[k - 1]) { k -= 1 }
        state.continued = k > start && b[k - 1] == 92
    }

    private func makefile(_ b: Bytes, _ start: Int, _ end: Int, _ state: inout SyntaxState, _ o: inout Output) {
        if start < end, b[start] == 9 { // a recipe: shell
            code(b, start, start, end, &state, &o)
            return
        }
        var i = Self.skipSpaces(b, start, end)
        guard i < end else { return }
        if b[i] == 35 {
            o.emit(i, end, .comment)
            return
        }
        var j = i
        while j < end, Self.isLetter(b[j]) || b[j] == 45 { j += 1 }
        if j > i, lang.words[Self.hash(b, i, j, lower: false)] == .keyword {
            o.emit(i, j, .keyword)
            code(b, j, start, end, &state, &o)
            return
        }
        // NAME = / := / ?= / += value, or targets: prerequisites.
        var k = i
        while k < end, b[k] != 58, b[k] != 61, b[k] != 35 { k += 1 }
        if k < end, b[k] == 61 || (b[k] == 58 && k + 1 < end && b[k + 1] == 61) {
            var e = k
            while e > i, Self.isSpace(b[e - 1]) || b[e - 1] == 63 || b[e - 1] == 43 || b[e - 1] == 58 { e -= 1 }
            o.emit(i, e, .tag)
            i = k + 1
        } else if k < end, b[k] == 58 {
            o.emit(i, k, .function)
            i = k + 1
        }
        code(b, i, start, end, &state, &o)
    }

    // MARK: Markdown, diffs

    private func markdown(_ b: Bytes, _ start: Int, _ end: Int, _ state: inout SyntaxState, _ o: inout Output) {
        let t = Self.skipSpaces(b, start, end)
        let fence = t - start < 4 && (Self.at(b, t, end, [96, 96, 96]) || Self.at(b, t, end, [126, 126, 126]))
        if state.inside == .fence {
            o.emit(start, end, fence ? .meta : .code)
            if fence { state.inside = .none }
            return
        }
        guard t < end else { return }
        if fence {
            o.emit(t, end, .meta)
            state.inside = .fence
            return
        }
        if b[t] == 35 {
            var j = t
            while j < end, b[j] == 35 { j += 1 }
            if j - t <= 6, j == end || b[j] == 32 {
                o.emit(t, end, .heading)
                return
            }
        }
        if b[t] == 62 {
            o.emit(t, end, .quote)
            return
        }
        // A rule: three or more of - * _ and nothing else.
        if b[t] == 45 || b[t] == 42 || b[t] == 95 {
            var n = 0, other = false
            for k in t..<end where !Self.isSpace(b[k]) { if b[k] == b[t] { n += 1 } else { other = true } }
            if n >= 3, !other {
                o.emit(t, end, .meta)
                return
            }
        }
        var i = t
        if (b[t] == 45 || b[t] == 42 || b[t] == 43), t + 1 < end, b[t + 1] == 32 {
            o.emit(t, t + 1, .keyword)
            i = t + 2
        } else if Self.isDigit(b[t]) {
            var j = t
            while j < end, Self.isDigit(b[j]) { j += 1 }
            if j < end, b[j] == 46 || b[j] == 41, j + 1 == end || b[j + 1] == 32 {
                o.emit(t, j + 1, .keyword)
                i = j + 1
            }
        }
        inline(b, i, end, &o)
    }

    /// `code`, **strong**, *emphasis*, [links](…), <https://…>, bare URLs.
    private func inline(_ b: Bytes, _ from: Int, _ end: Int, _ o: inout Output) {
        var i = from
        while i < end {
            let c = b[i]
            if c == 96 {
                var n = 0
                while i + n < end, b[i + n] == 96 { n += 1 }
                if let j = Self.find(b, i + n, end, Array(repeating: 96, count: n)) {
                    o.emit(i, j + n, .code)
                    i = j + n
                } else {
                    i += n
                }
                continue
            }
            if (c == 42 || c == 95), i + 1 < end, b[i + 1] == c, let j = Self.find(b, i + 2, end, [c, c]), j > i + 2 {
                o.emit(i, j + 2, .strong)
                i = j + 2
                continue
            }
            if (c == 42 || (c == 95 && (i == from || !Self.isLetter(b[i - 1])))), i + 1 < end, !Self.isSpace(b[i + 1]),
               let j = Self.find(b, i + 1, end, [c]), j > i + 1, !Self.isSpace(b[j - 1]) {
                o.emit(i, j + 1, .emphasis)
                i = j + 1
                continue
            }
            if c == 91 || (c == 33 && i + 1 < end && b[i + 1] == 91), let close = Self.find(b, i, end, [93]) {
                if close + 1 < end, b[close + 1] == 40, let paren = Self.find(b, close + 1, end, [41]) {
                    o.emit(i, paren + 1, .link)
                    i = paren + 1
                    continue
                }
            }
            if c == 60, Self.at(b, i + 1, end, Array("http".utf8)), let j = Self.find(b, i, end, [62]) {
                o.emit(i, j + 1, .link)
                i = j + 1
                continue
            }
            if c == 104, Self.at(b, i, end, Array("http://".utf8)) || Self.at(b, i, end, Array("https://".utf8)),
               i == from || !Self.isLetter(b[i - 1]) {
                let s = i
                while i < end, !Self.isSpace(b[i]), b[i] < 0x80, b[i] != 41 { i += 1 }
                o.emit(s, i, .link)
                continue
            }
            i += 1
        }
    }

    private func diff(_ b: Bytes, _ start: Int, _ end: Int, _ o: inout Output) {
        guard start < end else { return }
        let kind: SyntaxKind
        if Self.at(b, start, end, Array("+++".utf8)) || Self.at(b, start, end, Array("---".utf8)) || Self.at(b, start, end, Array("@@".utf8)) {
            kind = .meta
        } else if b[start] == 43 {
            kind = .added
        } else if b[start] == 45 {
            kind = .removed
        } else if Self.at(b, start, end, Array("diff ".utf8)) || Self.at(b, start, end, Array("index ".utf8)) {
            kind = .comment
        } else {
            kind = .plain
        }
        o.emit(start, end, kind)
    }
}
