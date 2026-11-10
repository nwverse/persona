import Foundation

/// An assistant conversation's title in the hub's format 「MMDD | 类型 | 主题」 (docs/assistant/assistant.md「Chat titles」), split so
/// the list can show the topic first; nil for any other title (the user's own, an older conversation's).
public nonisolated struct ConversationTitle: Equatable, Sendable {
    public var head: String   // "1101 | fix | "
    public var topic: String  // "search field overlaps the top bar"

    public init?(_ title: String) {
        let parts = title.components(separatedBy: " | ")
        guard parts.count >= 3, parts[0].count == 4, parts[0].allSatisfy(\.isASCIIDigit),
              (1...4).contains(parts[1].count) else { return nil }
        let topic = parts[2...].joined(separator: " | ").trimmingCharacters(in: .whitespaces)
        guard !topic.isEmpty else { return nil }
        head = parts[0] + " | " + parts[1] + " | "
        self.topic = topic
    }
}

extension Character {
    var isASCIIDigit: Bool { isASCII && isNumber }
}
