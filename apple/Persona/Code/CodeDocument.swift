import Foundation

/// A text file in the viewer (docs/apple/ios.md「File viewer」): its UTF-8, where each line starts, and its colors as far
/// as they have been worked out. Lines show at once as plain text; `highlight` colors them off the main thread a chunk
/// at a time from the top, up to the caps (past them the rest stays plain and `capped` says so). Only the lines on
/// screen are ever made into Strings.
nonisolated final class CodeDocument: @unchecked Sendable {
    /// Coloring stops after this many lines or bytes (a log of a million lines would take seconds for little).
    static let maxHighlightLines = 100_000
    static let maxHighlightBytes = 8 << 20
    /// A line longer than this shows cut (a minified file's one line), and is colored only that far.
    static let maxLineBytes = 4000

    let bytes: [UInt8]
    /// Where each line starts, and one more: the end.
    let lineStarts: [Int]
    let language: SyntaxLanguage?
    /// The longest line in bytes (capped): the content's width.
    let widestLine: Int

    private let lock = NSLock()
    private var tokens: [SyntaxToken] = []
    /// tokens[lineEnds[i - 1] ..< lineEnds[i]] are line i's.
    private var lineEnds: [Int] = []
    private var state = SyntaxState()

    var lineCount: Int { lineStarts.count - 1 }
    var highlightedLines: Int { lock.withLock { lineEnds.count } }
    /// Coloring stopped at the caps before the last line.
    var capped: Bool { language != nil && min(lineCount, Self.highlightLimit(lineStarts)) < lineCount }

    init(text: String, language: SyntaxLanguage?) {
        var bytes = Array(text.utf8)
        if bytes.last == 10 { bytes.removeLast() } // a final newline is not an empty last line
        self.bytes = bytes
        self.language = language
        var starts = [0]
        starts.reserveCapacity(bytes.count / 40 + 2)
        var widest = 0
        bytes.withUnsafeBufferPointer { b in
            var last = 0
            for i in 0..<b.count where b[i] == 10 {
                widest = max(widest, i - last)
                starts.append(i + 1)
                last = i + 1
            }
            widest = max(widest, b.count - last)
        }
        starts.append(bytes.count + 1)
        lineStarts = starts
        widestLine = min(widest, Self.maxLineBytes)
    }

    /// The same file with the next page after it (`FileContent.next`), keeping the colors worked out so far: the
    /// page before ended at a line's end, so nothing above changes.
    func appending(_ text: String) -> CodeDocument {
        let joined = String(decoding: bytes, as: UTF8.self) + "\n" + text
        let doc = CodeDocument(text: joined, language: language)
        lock.withLock {
            doc.tokens = tokens
            doc.lineEnds = Array(lineEnds.prefix(lineCount))
            doc.state = state
        }
        return doc
    }

    /// Line i's bytes, without its newline (or a CR before it), cut at maxLineBytes.
    func lineRange(_ i: Int) -> Range<Int> {
        let s = lineStarts[i]
        var e = lineStarts[i + 1] - 1
        if e > s, bytes[e - 1] == 13 { e -= 1 }
        return s..<min(e, s + Self.maxLineBytes)
    }

    func isCut(_ i: Int) -> Bool { lineStarts[i + 1] - 1 - lineStarts[i] > Self.maxLineBytes }

    func text(_ i: Int) -> String {
        let r = lineRange(i)
        return bytes.withUnsafeBufferPointer { String(decoding: UnsafeBufferPointer(rebasing: $0[r]), as: UTF8.self) }
    }

    /// The whole text (Copy All, Share).
    var string: String { String(decoding: bytes, as: UTF8.self) }

    /// Line i's tokens, or nil while it is not colored yet.
    func tokens(_ i: Int) -> ArraySlice<SyntaxToken>? {
        lock.withLock {
            guard i < lineEnds.count else { return nil }
            return tokens[(i == 0 ? 0 : lineEnds[i - 1])..<lineEnds[i]]
        }
    }

    private static func highlightLimit(_ starts: [Int]) -> Int {
        var n = min(starts.count - 1, maxHighlightLines)
        while n > 0, starts[n] > maxHighlightBytes { n -= 1 }
        return n
    }

    /// Colors the lines not colored yet, `chunk` at a time, calling `progress` with each chunk's lines (on this
    /// thread). Stops at the caps, or when `cancelled` says so.
    func highlight(chunk: Int = 2000, cancelled: () -> Bool = { false }, progress: (Range<Int>) -> Void) {
        guard let language else { return }
        let lexer = SyntaxLexer(language)
        let limit = Self.highlightLimit(lineStarts)
        var from = highlightedLines
        var state = lock.withLock { self.state }
        while from < limit, !cancelled() {
            let to = min(limit, from + chunk)
            var out: [SyntaxToken] = []
            var ends: [Int] = []
            ends.reserveCapacity(to - from)
            let base = lock.withLock { tokens.count }
            bytes.withUnsafeBufferPointer { b in
                for i in from..<to {
                    let r = lineRange(i)
                    lexer.line(b, r.lowerBound, r.upperBound, &state, &out)
                    ends.append(base + out.count)
                }
            }
            lock.withLock {
                tokens.append(contentsOf: out)
                lineEnds.append(contentsOf: ends)
                self.state = state
            }
            progress(from..<to)
            from = to
        }
    }
}
