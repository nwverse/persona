import CoreGraphics
import Foundation

/// A streaming reply cut where a block has certainly ended: a blank line outside a code fence, followed by an
/// unindented line (the next block has started). Everything before the last such cut stays the same as the text grows.
public nonisolated enum StreamChunks {
    public static func split(_ text: String) -> (done: [String], tail: String) {
        let lines = text.components(separatedBy: "\n")
        var done: [String] = []
        var start = 0
        var fence: MarkdownFence?
        for (i, line) in lines.enumerated() {
            let t = line.trimmingCharacters(in: .whitespaces)
            if let f = fence {
                if f.isClosed(by: t) { fence = nil }
                continue
            }
            if let f = MarkdownFence.opening(t) { fence = f; continue }
            guard t.isEmpty, i > start, i + 1 < lines.count else { continue }
            let next = lines[i + 1]
            guard !next.isEmpty, !next.hasPrefix(" "), !next.hasPrefix("\t") else { continue }
            done.append(lines[start..<i].joined(separator: "\n"))
            start = i + 1
        }
        return (done, lines[start...].joined(separator: "\n"))
    }

    /// Where `split`'s pieces are in `text`, in UTF-16 offsets: each finished block from its first character to the end
    /// of its last line, then the block being written to the end of the text. The same cuts as `split`, so the text up
    /// to any block's end splits into the blocks before it and that block as its tail.
    public static func blocks(_ text: String) -> [Range<Int>] {
        let lines = text.components(separatedBy: "\n")
        var starts: [Int] = []
        starts.reserveCapacity(lines.count)
        var offset = 0
        for line in lines {
            starts.append(offset)
            offset += line.utf16.count + 1
        }
        var out: [Range<Int>] = []
        var start = 0
        var fence: MarkdownFence?
        for (i, line) in lines.enumerated() {
            let t = line.trimmingCharacters(in: .whitespaces)
            if let f = fence {
                if f.isClosed(by: t) { fence = nil }
                continue
            }
            if let f = MarkdownFence.opening(t) { fence = f; continue }
            guard t.isEmpty, i > start, i + 1 < lines.count else { continue }
            let next = lines[i + 1]
            guard !next.isEmpty, !next.hasPrefix(" "), !next.hasPrefix("\t") else { continue }
            out.append(starts[start]..<(starts[i] - 1))
            start = i + 1
        }
        out.append(starts[start]..<text.utf16.count)
        return out
    }

    /// Where the lines of `text` end: the UTF-16 offset of each line break.
    public static func lineEnds(_ text: String) -> [Int] {
        var out: [Int] = []
        for (i, unit) in text.utf16.enumerated() where unit == 0x0A { out.append(i) }
        return out
    }

    /// The space MarkdownUI's theme puts between the last block of one piece and the first of the next (the larger
    /// of the two margins, `MarkdownUI.Theme.persona`).
    public static func gap(after prev: String, before next: String) -> CGFloat {
        let last = prev.split(separator: "\n").last.map { $0.trimmingCharacters(in: .whitespaces) } ?? ""
        let first = next.split(separator: "\n").first.map { $0.trimmingCharacters(in: .whitespaces) } ?? ""
        func top(_ l: String) -> CGFloat {
            if l.hasPrefix("# ") { return 16 }
            if l.hasPrefix("## ") { return 14 }
            if l.hasPrefix("### ") { return 12 }
            if l.hasPrefix("```") || l.hasPrefix("~~~") || l.hasPrefix("|") { return 4 }
            return 0
        }
        func bottom(_ l: String) -> CGFloat {
            if l.hasPrefix("```") || l.hasPrefix("~~~") || l.hasPrefix("|") { return 12 }
            if l.hasPrefix("# ") { return 8 }
            if l.hasPrefix("## ") { return 6 }
            if l.hasPrefix("### ") { return 4 }
            return 10
        }
        return max(top(first), bottom(last))
    }
}

/// What of a streaming reply to lay out and how much of it to draw, once `StreamReveal` has said how much is shown
/// (#7287). Laying out what is shown changed the layout at every frame, and each change re-measured the whole chat
/// (about 4 ms). Instead the received text is laid out up to the end of the line the shown part ends in (that changes
/// only when text arrives or the reveal reaches the next line), and only the drawing of that line's block follows the
/// reveal: `fraction` of what of it is laid out. Not the whole block: a list's next markers, a table's next row and a
/// code block's next lines are drawn without text, and only text is what the renderer can hold back.
public nonisolated struct RevealPlan: Equatable, Sendable {
    /// The UTF-16 length of the received text to lay out.
    public var layoutEnd: Int
    /// The UTF-16 start of the block the reveal is in (the last block of what is laid out).
    public var blockStart: Int
    /// How much of that block, as laid out, to draw: 0…1.
    public var fraction: Double

    public init(layoutEnd: Int, blockStart: Int, fraction: Double) {
        self.layoutEnd = layoutEnd
        self.blockStart = blockStart
        self.fraction = fraction
    }

    /// The UTF-16 offset of what is drawn (where the reveal is).
    public var drawnEnd: Double { Double(blockStart) + fraction * Double(layoutEnd - blockStart) }

    /// `blocks`: `StreamChunks.blocks` of the received text; `lineEnds`: `StreamChunks.lineEnds` of it; `shown`: the
    /// UTF-16 length of what is shown.
    public static func make(blocks: [Range<Int>], lineEnds: [Int], shown: Int) -> RevealPlan {
        guard let last = blocks.last else { return RevealPlan(layoutEnd: 0, blockStart: 0, fraction: 1) }
        if shown >= last.upperBound {
            return RevealPlan(layoutEnd: last.upperBound, blockStart: last.lowerBound, fraction: 1)
        }
        // The block the shown part ends in: the last one starting before it (a shown part ending in the blank line
        // after a block is that block, whole).
        let block = blocks.last { $0.lowerBound < shown } ?? blocks[0]
        // To the end of the line the shown part ends in, within the block.
        let lineEnd = lineEnds.first { $0 >= shown } ?? Int.max
        let end = min(block.upperBound, lineEnd)
        let length = end - block.lowerBound
        let fraction = length > 0 ? min(1, max(0, Double(shown - block.lowerBound) / Double(length))) : 1
        return RevealPlan(layoutEnd: end, blockStart: block.lowerBound, fraction: fraction)
    }
}
