import AppKit
import Foundation

/// What was sent from a composer, per conversation, for ↑ / ↓ recall and the ⌃R search (docs/apple/mac.md「Details」, the
/// web's `web/src/lib/send-history.ts`, #2434): the last 100 messages of each conversation, on this Mac only (the app's
/// state cache, never sent anywhere). The rules are pure functions here; `MacSendHistory.shared` keeps the lists.
@MainActor final class MacSendHistory {
    static let shared = MacSendHistory()

    nonisolated static let historyMax = 100
    nonisolated static let searchMax = 100
    private nonisolated static let file = "sent-history"

    nonisolated struct Entry: Codable, Hashable, Sendable {
        /// The message as it was typed.
        var text: String
        /// When it was sent: orders entries of different conversations in the search.
        var at: Date
    }

    private var lists: [String: [Entry]]?

    private var all: [String: [Entry]] {
        if let lists { return lists }
        var loaded = StateCache.load(Self.file, as: [String: [Entry]].self) ?? [:]
        #if DEBUG
        // `-showSentSamples YES`: a few made-up sent messages (in memory only), for screenshots of ↑ and ⌃R.
        if UserDefaults.standard.bool(forKey: "showSentSamples") { loaded = ["sample": Self.samples] }
        #endif
        lists = loaded
        return loaded
    }

    /// The conversation's sent messages, oldest first.
    func history(_ key: String) -> [Entry] {
        #if DEBUG
        if UserDefaults.standard.bool(forKey: "showSentSamples") { return Self.samples }
        #endif
        return all[key] ?? []
    }

    #if DEBUG
    private static let samples = [
        "跑一下全部测试，失败的先修", "把小组件的降雨条改成和 App 里一样的样式", "提交并推到新分支，PR 标题用英文",
        "看看 /orders 接口为什么变慢", "fix the flaky login test and rerun it",
    ].enumerated().map { Entry(text: $1, at: Date(timeIntervalSinceNow: Double($0 - 10) * 600)) }
    #endif

    /// Every conversation's sent messages, newest first: what the search looks through.
    func everything() -> [Entry] { all.values.flatMap { $0 }.sorted { $0.at > $1.at } }

    /// Remembers a message that went out.
    func record(_ key: String, _ text: String, at: Date = .now) {
        var lists = all
        let next = Self.withSent(lists[key] ?? [], text, at: at)
        guard next != lists[key] ?? [] else { return }
        lists[key] = next
        self.lists = lists
        StateCache.save(Self.file, lists)
    }

    /// The list after sending `text`: blank messages are not kept, an earlier copy of the same text moves to the end
    /// (recalling it once is enough), and only the last `historyMax` stay.
    nonisolated static func withSent(_ list: [Entry], _ text: String, at: Date) -> [Entry] {
        guard !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return list }
        return Array((list.filter { $0.text != text } + [Entry(text: text, at: at)]).suffix(historyMax))
    }

    /// The search: the query split on spaces, every word in the message (case-insensitive); newest first, one row per
    /// text, at most `max`. An empty query lists the newest.
    nonisolated static func search(_ candidates: [Entry], _ query: String, max: Int = searchMax) -> [String] {
        let words = query.lowercased().split(separator: " ").map(String.init)
        var seen = Set<String>(), out: [String] = []
        for e in candidates where !seen.contains(e.text) {
            let t = e.text.lowercased()
            guard words.allSatisfy({ t.contains($0) }) else { continue }
            seen.insert(e.text)
            out.append(e.text)
            if out.count >= max { break }
        }
        return out
    }

    // MARK: ↑ / ↓

    nonisolated enum Direction { case up, down }

    /// Whether ↑ / ↓ walk the history: the box is empty or still shows the message it recalled. A multi-line message
    /// keeps the arrows for its own lines until the cursor is on the first (↑) or the last (↓) one.
    nonisolated static func canRecall(draft: String, shown: String?, _ dir: Direction, firstLine: Bool, lastLine: Bool) -> Bool {
        if draft.isEmpty { return true }
        guard let shown, draft == shown else { return false }
        return dir == .up ? firstLine : lastLine
    }

    /// One step: ↑ to an older message (the newest from an empty box, stopping at the oldest), ↓ to a newer one and
    /// past the newest back to an empty box. -1 is the empty box.
    nonisolated static func step(count: Int, index: Int, _ dir: Direction) -> Int {
        guard count > 0 else { return -1 }
        switch dir {
        case .up: return index < 0 ? count - 1 : max(0, index - 1)
        case .down: return index < 0 || index + 1 >= count ? -1 : index + 1
        }
    }

    /// Whether the cursor (a UTF-16 range of `text`) is on the text's first line, and on its last.
    nonisolated static func lines(_ text: String, cursor: NSRange) -> (first: Bool, last: Bool) {
        let s = text as NSString
        let start = min(max(cursor.location, 0), s.length), end = min(start + max(cursor.length, 0), s.length)
        let before = s.range(of: "\n", options: [], range: NSRange(location: 0, length: start))
        let after = s.range(of: "\n", options: [], range: NSRange(location: end, length: s.length - end))
        return (before.location == NSNotFound, after.location == NSNotFound)
    }

    // MARK: keys

    nonisolated enum Key: Equatable {
        case recall(Direction)
        case openSearch
    }

    nonisolated static let upArrow: UInt16 = 126
    nonisolated static let downArrow: UInt16 = 125
    private nonisolated static let keyR: UInt16 = 15, keyP: UInt16 = 35, keyS: UInt16 = 1, keyN: UInt16 = 45, keyG: UInt16 = 5
    private nonisolated static let tab: UInt16 = 48, escape: UInt16 = 53

    private nonisolated static func mods(_ m: NSEvent.ModifierFlags) -> NSEvent.ModifierFlags {
        m.intersection([.command, .shift, .option, .control])
    }

    /// What a key in a focused composer asks of the history, if anything: bare ↑ / ↓, and ⌃R (the terminal's; ⌘R is
    /// the window's Refresh). Never while an input method holds unconfirmed text.
    nonisolated static func key(keyCode: UInt16, modifiers: NSEvent.ModifierFlags, composing: Bool) -> Key? {
        guard !composing else { return nil }
        let m = mods(modifiers)
        if m.isEmpty && keyCode == upArrow { return .recall(.up) }
        if m.isEmpty && keyCode == downArrow { return .recall(.down) }
        if m == .control && keyCode == keyR { return .openSearch }
        return nil
    }

    nonisolated enum SearchAction: Equatable { case up, down, pick, cancel }

    /// The search panel's keys: ↑ ↓ and Emacs' ⌃R / ⌃P (up) and ⌃S / ⌃N (down) move, ⏎ or Tab picks, Esc or ⌃G
    /// cancels. nil: the search field's own (typing).
    nonisolated static func searchAction(keyCode: UInt16, modifiers: NSEvent.ModifierFlags, composing: Bool) -> SearchAction? {
        guard !composing else { return nil }
        let m = mods(modifiers)
        if m.isEmpty {
            switch keyCode {
            case upArrow: return .up
            case downArrow: return .down
            case MacComposerKeys.returnKey, MacComposerKeys.keypadEnter, tab: return .pick
            case escape: return .cancel
            default: return nil
            }
        }
        guard m == .control else { return nil }
        switch keyCode {
        case keyR, keyP: return .up
        case keyS, keyN: return .down
        case keyG: return .cancel
        default: return nil
        }
    }

    /// The highlighted row after a move: up toward the top row (the newest), wrapping round.
    nonisolated static func move(_ active: Int, count: Int, _ action: SearchAction) -> Int {
        guard count > 0 else { return 0 }
        return action == .up ? (active - 1 + count) % count : (active + 1) % count
    }
}
