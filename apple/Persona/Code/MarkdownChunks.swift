import MarkdownUI
import PersonaKit
import SwiftUI

/// A long Markdown file cut between its top-level blocks into pieces of about `lines` lines (docs/apple/ios.md「File viewer」):
/// each piece is its own `Markdown` in a lazy stack, so a 5 000-line README lays out only what is on screen.
/// Never cut inside a code fence, before an indented line (a list item's continuation, indented code) or a table's
/// rows; link definitions (`[x]: url`) go with every piece so references still resolve.
nonisolated enum MarkdownChunks {
    static func split(_ text: String, lines target: Int = 12) -> [String] {
        let lines = text.components(separatedBy: "\n")
        guard lines.count > target else { return [text] }
        let definitions = lines.filter { $0.range(of: #"^ {0,3}\[[^\]]+\]:\s*\S"#, options: .regularExpression) != nil }
        var out: [String] = []
        var current: [String] = []
        var fence: MarkdownFence?
        for (i, line) in lines.enumerated() {
            let t = line.trimmingCharacters(in: .whitespaces)
            if let f = fence {
                if f.isClosed(by: t) { fence = nil }
            } else {
                fence = MarkdownFence.opening(t)
            }
            current.append(line)
            guard fence == nil, current.count >= target, t.isEmpty, i + 1 < lines.count else { continue }
            let next = lines[i + 1]
            if next.hasPrefix(" ") || next.hasPrefix("\t") || next.trimmingCharacters(in: .whitespaces).isEmpty { continue }
            out.append(current.joined(separator: "\n"))
            current = []
        }
        if !current.isEmpty { out.append(current.joined(separator: "\n")) }
        guard !definitions.isEmpty, out.count > 1 else { return out }
        let tail = "\n\n" + definitions.joined(separator: "\n")
        return out.map { $0 + tail }
    }
}

/// A parsed piece for the lazy stack (MarkdownContent is not Sendable; it is parsed off the main thread and only read
/// after).
nonisolated struct MarkdownChunk: Identifiable, @unchecked Sendable {
    var id: Int
    var content: MarkdownContent
}

/// Highlighted blocks shared by Markdown views. Evict only the least recently used block, not every visible block
/// when a long file fills the cache. Both the entry count and retained source bytes are bounded; oversized blocks
/// are returned without retaining them. Relative fonts and dynamic syntax colors still resolve in the view's environment.
@MainActor
final class MarkdownCodeMemo {
    private nonisolated struct Key: Hashable {
        var code: String
        var language: String?
        var scale: CGFloat
    }

    private struct Entry {
        var text: Text
        var bytes: Int
        var used: UInt64
    }

    static let highlightedByteLimit = 60_000
    private var entries: [Key: Entry] = [:]
    private var retainedBytes = 0
    private var clock: UInt64 = 0
    private let limit: Int
    private let byteLimit: Int

    init(limit: Int = 300, byteLimit: Int = 2 << 20) {
        self.limit = max(0, limit)
        self.byteLimit = max(0, byteLimit)
    }

    func value(_ code: String, language: String?, scale: CGFloat, make: () -> Text) -> Text {
        // Large blocks already skip lexing; never let their cheap plain text evict highlighted hot blocks.
        guard code.utf8.count < Self.highlightedByteLimit else { return make() }
        let key = Key(code: code, language: language, scale: scale)
        clock += 1
        if var entry = entries[key] {
            entry.used = clock
            entries[key] = entry
            return entry.text
        }
        let text = make()
        let bytes = code.utf8.count + (language?.utf8.count ?? 0)
        guard limit > 0, bytes <= byteLimit else { return text }
        while entries.count >= limit || retainedBytes + bytes > byteLimit {
            guard let oldest = entries.min(by: { $0.value.used < $1.value.used }) else { break }
            retainedBytes -= oldest.value.bytes
            entries.removeValue(forKey: oldest.key)
        }
        entries[key] = Entry(text: text, bytes: bytes, used: clock)
        retainedBytes += bytes
        return text
    }
}

/// Code fences in rendered Markdown colored by the file viewer's lexer, by the fence's language (```swift).
/// Unknown languages and long blocks stay plain.
struct PersonaCodeHighlighter: CodeSyntaxHighlighter {
    /// MarkdownUI asks again on every layout of the block: the colored text is kept by text size, language and code
    /// (its font is the code level at the text size it was made at, so Increase / Decrease Text Size make it again, #2736).
    @MainActor private static let memo = MarkdownCodeMemo()

    @MainActor func highlightCode(_ code: String, language: String?) -> Text {
        Self.memo.value(code, language: language, scale: Theme.Font.scale) {
            Self.color(code, language: language)
        }
    }

    @MainActor private static func color(_ code: String, language: String?) -> Text {
        // The code level's monospaced font: a Text from here does not take the theme's code block font.
        guard let language, code.utf8.count < MarkdownCodeMemo.highlightedByteLimit,
              let lang = SyntaxLanguage.detect(name: "x." + Self.extensionFor(language.lowercased())) else { return Text(code).font(Theme.Font.code) }
        let doc = CodeDocument(text: code, language: lang)
        doc.highlight { _ in }
        var out = AttributedString()
        doc.bytes.withUnsafeBufferPointer { b in
            for i in 0..<doc.lineCount {
                let r = doc.lineRange(i)
                var line = AttributedString(doc.text(i))
                if let tokens = doc.tokens(i) {
                    let map = SyntaxStyle.utf16Offsets(UnsafeBufferPointer(rebasing: b[r]))
                    let ns = NSAttributedString(line)
                    let utf16 = ns.length
                    for t in tokens {
                        let s = Int(t.start), e = Int(t.start + t.length)
                        let a = map.map { Int($0[s]) } ?? s, z = map.map { Int($0[e]) } ?? e
                        guard a < z, z <= utf16, let range = Range(NSRange(location: a, length: z - a), in: line),
                              let color = Self.color(of: t.kind) else { continue }
                        line[range].foregroundColor = color
                    }
                }
                out += line
                if i < doc.lineCount - 1 { out += AttributedString("\n") }
            }
        }
        return Text(out).font(Theme.Font.code)
    }

    private static func color(of k: SyntaxKind) -> Color? {
        switch k {
        case .keyword, .heading: Theme.Syntax.keyword
        case .type, .variable: Theme.Syntax.type
        case .string, .code: Theme.Syntax.string
        case .number, .link: Theme.Syntax.number
        case .function, .meta: Theme.Syntax.function
        case .comment, .quote: Theme.Syntax.comment
        case .tag: Theme.Syntax.tag
        case .attribute: Theme.Syntax.attribute
        case .added: Theme.Diff.add
        case .removed: Theme.Diff.del
        case .plain, .emphasis, .strong: nil
        }
    }

    /// A fence's language name as a file extension the detector knows.
    static func extensionFor(_ name: String) -> String {
        switch name {
        case "python", "python3": "py"
        case "javascript", "node": "js"
        case "typescript": "ts"
        case "bash", "shell", "console", "shellscript", "zsh", "sh": "sh"
        case "golang": "go"
        case "rust": "rs"
        case "kotlin": "kt"
        case "ruby": "rb"
        case "c++": "cpp"
        case "objective-c", "objc": "m"
        case "csharp", "c#": "cs"
        case "markdown": "md"
        case "dockerfile", "docker": "dockerfile"
        case "make", "makefile": "mk"
        case "terraform": "tf"
        case "text", "plaintext", "txt": "txt"
        default: name
        }
    }
}
