import Foundation

/// An agent's live text (its `delta`) against what its transcript already holds (docs/integrations/claude-integration.md
/// "Live replies (MessageDisplay)"; the web's `lib/live-text.ts`, the node's `unlanded` in `livereply.go`). The live text
/// is never the source: once the transcript has a reply, the reply's item replaces it, matched by its start. The node
/// already sends only what its transcript lacks, again right after each push of items; this is the app's side of it, for
/// a delta that reaches the chat after items that hold it (a chat whose own read of the transcript got ahead of the
/// node's last push). Only the words of the agent's latest API message can be the live message's: a message lands whole,
/// with its tool call's result, so words followed by anything not the agent's (a tool's result, a message typed
/// meanwhile) are an earlier message's, which may begin alike.
///
/// Matching goes by Unicode scalars and a cut item's size by UTF-8 bytes, as the node and the web do (not by Swift's
/// characters, which would join a combining mark to the letter before it). Pure: compiled for the watch too.
nonisolated enum LiveText {
    /// An agent's words as the transcript has them; `size`: a cut item's whole length in bytes (the hub cuts long text
    /// for pages and pushes, `text` holding only its start), 0 for a whole one.
    nonisolated struct Landed: Equatable, Sendable {
        var text: String
        var size: Int

        init(_ text: String, size: Int = 0) {
            self.text = text
            self.size = size
        }
    }

    /// The words of the agent's latest message: its text items at the end of the transcript so far, oldest first; none
    /// when the transcript ends in anything else (a tool's result, a message of the user's, a notice).
    static func landedWords<C: BidirectionalCollection>(_ items: C) -> [Landed] where C.Element == Item {
        var out: [Landed] = []
        var i = items.endIndex
        while i > items.startIndex {
            let j = items.index(before: i)
            let it = items[j]
            guard it.role == "assistant" else { break }
            if it.kind == .text, !isBlank(Substring(it.text ?? "").unicodeScalars) { out.append(landed(it)) }
            i = j
        }
        return out.reversed()
    }

    private static func landed(_ it: Item) -> Landed {
        let text = it.text ?? ""
        guard let size = it.fullSize, size > 0 else { return Landed(text) }
        // The line a cut text ends in (the hub's note).
        guard let note = text.range(of: "\n…", options: [.backwards, .literal]) else { return Landed(text, size: size) }
        return Landed(String(text[..<note.lowerBound]), size: size)
    }

    /// What of a live message the transcript does not have yet, "" for nothing (the node's `unlanded`): the latest
    /// message's words that begin like it are taken off its start, block by block (a cut item holds its whole block,
    /// `size` bytes); live text an item begins with is all there. What is left is as it came.
    static func unlandedText(_ text: String, _ words: [Landed]) -> String {
        var s = Substring(text).unicodeScalars
        var trimmed = false
        for w in words {
            let t = trim(Substring(w.text).unicodeScalars)
            if t.isEmpty { continue }
            let body = s.drop(while: isLiveSpace)
            if isBlank(body) { break }
            if t.starts(with: dropTrailingLiveSpace(body)) { return "" }
            guard body.starts(with: t) else { break }
            var rest: Substring.UnicodeScalarView
            if w.size > 0 {
                let bytes = Array(String(Substring(body)).utf8)
                let lead = Substring(w.text).unicodeScalars.prefix(while: isLiveSpace)
                var n = max(0, w.size - String(Substring(lead)).utf8.count)
                while n < bytes.count, bytes[n] & 0xC0 == 0x80 { n += 1 } // on a character's first byte
                if n >= bytes.count { return "" }
                rest = Substring(String(decoding: bytes[n...], as: UTF8.self)).unicodeScalars
            } else {
                let after = body.index(body.startIndex, offsetBy: t.count)
                guard wordEnd(body, at: after, t) else { break }
                rest = body[after...]
            }
            s = rest.drop(while: isLiveSpace)
            trimmed = true
        }
        if isBlank(s) { return "" }
        return trimmed ? String(Substring(s)) : text
    }

    /// What of a live message the chat's newest items do not hold: `unlandedText` against the latest message's words. A
    /// live text that is all of the message before them (its words, then a tool's result) is that message's copy come
    /// late: a message's final batch comes before its tool call, so a copy left from before its items landed is whole.
    static func unlandedLive<C: BidirectionalCollection>(_ text: String, _ items: C) -> String where C.Element == Item {
        let rest = unlandedText(text, landedWords(items))
        guard !rest.isEmpty else { return "" }
        let whole = trim(Substring(text).unicodeScalars)
        var i = items.endIndex
        func previous() -> Item? { i > items.startIndex ? items[items.index(before: i)] : nil }
        while let it = previous(), it.role == "assistant" { i = items.index(before: i) } // the latest message
        while let it = previous(), it.role != "assistant" { // what came between
            if opens(it) { return rest }
            i = items.index(before: i)
        }
        while let it = previous(), it.role == "assistant" {
            if it.kind == .text, (it.fullSize ?? 0) == 0, trim(Substring(it.text ?? "").unicodeScalars).elementsEqual(whole) { return "" }
            i = items.index(before: i)
        }
        return rest
    }

    /// The message that opens an exchange: a prompt of the user's, or a background task waking the agent.
    static func opens(_ it: Item) -> Bool {
        it.wake == true || (it.role == "user" && it.kind == .text && it.queued != true)
    }

    // MARK: Words and spaces

    /// `t`, a start of `body` that ends at `after`, ends where a word does: body goes on with a space or nothing, or t
    /// ends in punctuation ("OK" is not the start of "OK, the tests pass"; the node's `wordEnd`).
    private static func wordEnd(_ body: Substring.UnicodeScalarView, at after: Substring.UnicodeScalarView.Index,
                                _ t: Substring.UnicodeScalarView) -> Bool {
        guard after < body.endIndex, let last = t.last else { return true }
        return isSpace(body[after]) || isPunctuation(last)
    }

    /// The space a live text's lines begin and end with (the node's `liveSpace`).
    private static func isLiveSpace(_ c: Unicode.Scalar) -> Bool { c == " " || c == "\t" || c == "\r" || c == "\n" }

    /// Any white space, as `String.prototype.trim` and Go's `strings.TrimSpace` take off.
    private static func isSpace(_ c: Unicode.Scalar) -> Bool { c.properties.isWhitespace || c == "\u{FEFF}" }

    private static func isPunctuation(_ c: Unicode.Scalar) -> Bool {
        switch c.properties.generalCategory {
        case .connectorPunctuation, .dashPunctuation, .openPunctuation, .closePunctuation, .initialPunctuation,
             .finalPunctuation, .otherPunctuation: true
        default: false
        }
    }

    private static func isBlank(_ s: Substring.UnicodeScalarView) -> Bool { s.allSatisfy(isSpace) }

    private static func trim(_ s: Substring.UnicodeScalarView) -> Substring.UnicodeScalarView {
        let head = s.drop(while: isSpace)
        var end = head.endIndex
        while end > head.startIndex, isSpace(head[head.index(before: end)]) { end = head.index(before: end) }
        return head[head.startIndex..<end]
    }

    private static func dropTrailingLiveSpace(_ s: Substring.UnicodeScalarView) -> Substring.UnicodeScalarView {
        var end = s.endIndex
        while end > s.startIndex, isLiveSpace(s[s.index(before: end)]) { end = s.index(before: end) }
        return s[s.startIndex..<end]
    }
}
