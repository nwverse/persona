import Foundation

/// Display provenance is independent of the model input role. Raw additive metadata is retained in `Item.source`
/// so a newer server's unknown fields or kind cannot make an older Apple client drop the transcript.
nonisolated struct MessageSource: Hashable, Sendable {
    var kind: String
    var conversationID: String?
    var title: String?

    var identity: String { kind + "|" + (conversationID ?? title ?? "") }
    var automated: Bool { kind != "conversation" }

    var name: String {
        if let title, !title.isEmpty { return title }
        switch kind {
        case "conversation": return String(localized: "Conversation [Message Source]")
        case "patrol": return String(localized: "Patrol [Message Source]")
        case "followup": return String(localized: "Follow-up [Message Source]")
        case "supervision": return String(localized: "Supervision [Message Source]")
        default: return String(localized: "System [Message Source]")
        }
    }

    var mention: String { "@" + name }
    var origin: String? {
        guard kind == "conversation", let title, !title.isEmpty else { return nil }
        return String(localized: "From conversation “\(title)” [Message Source]")
    }

    static func of(_ item: Item) -> MessageSource? {
        guard item.role == "user", item.kind == .text else { return nil }
        if let value = item.source, let kind = value["kind"]?.string, !kind.isEmpty {
            guard kind != "user" else { return nil }
            return MessageSource(kind: kind, conversationID: value["conversation_id"]?.string,
                                 title: clean(value["title"]?.string))
        }
        // An app-sent message is genuinely theirs even when its words resemble an old automation envelope.
        guard item.clientMsgID == nil, item.from == nil else { return nil }
        if let injected = Injected.of(item) {
            if injected.kind == "followup" {
                return MessageSource(kind: "followup", title: clean(injected.title))
            }
            if injected.kind == "system", injected.state == "supervise" {
                return MessageSource(kind: "supervision", title: nil)
            }
        }
        return legacy(item.text ?? "")
    }

    /// What is shown and copied, without the model-only delivery envelope. Parsed once by the text memo.
    static func body(_ item: Item) -> String {
        if let injected = Injected.of(item), injected.kind == "followup" {
            return injected.detail.flatMap { $0.isEmpty ? nil : $0 } ?? injected.line
        }
        return legacyBody(item.cutText)
    }

    private static func clean(_ text: String?) -> String? {
        text.map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }.flatMap { $0.isEmpty ? nil : $0 }
    }

    private static let memo = TextMemo<MessageSource?>(limit: 1024)
    private static let bodyMemo = TextMemo<String>(limit: 1024)
    private static let context = try! NSRegularExpression(
        pattern: #"^\s*<"# + HubNaming.contextPattern + #"\b([^>]*)>([\s\S]*?)</"# + HubNaming.contextPattern + #">\s*"#)
    private static let titleAttribute = try! NSRegularExpression(pattern: #"\btitle="([^"]*)""#)
    private static let relayLine = try! NSRegularExpression(
        pattern: #"^这条消息不是用户本人发的，是 Persona 的另一段对话（([A-Za-z0-9._-]{1,128})）用 conversation_(?:send|new) 发来的，第 \d+ 跳（最多 \d+ 跳）。(?:\n|$)"#) // i18n-ignore: exact legacy relay envelope
    private static let supervisionHead = try! NSRegularExpression(
        pattern: #"^📋 督办：你派出去的任务有 \d+ 个要你处理\n（这是 Persona 的督办消息，不是用户发的。[^\n]*）(?:\n|$)"#) // i18n-ignore: exact legacy supervision envelope

    private static func legacy(_ text: String) -> MessageSource? { memo(text) { envelope($0).source } }
    private static func legacyBody(_ text: String) -> String { bodyMemo(text) { envelope($0).body } }

    private static func envelope(_ raw: String) -> (source: MessageSource?, body: String) {
        var rest = raw
        var relay: MessageSource?
        while let match = context.firstMatch(in: rest, range: NSRange(rest.startIndex..., in: rest)),
              let whole = Range(match.range, in: rest), let attrs = Range(match.range(at: 1), in: rest),
              let inner = Range(match.range(at: 2), in: rest) {
            let attributes = String(rest[attrs])
            let content = String(rest[inner]).trimmingCharacters(in: .whitespacesAndNewlines)
            if attributes.contains(#"kind="context""#),
               let t = titleAttribute.firstMatch(in: attributes, range: NSRange(attributes.startIndex..., in: attributes)),
               let r = Range(t.range(at: 1), in: attributes),
               let m = relayLine.firstMatch(in: content, range: NSRange(content.startIndex..., in: content)),
               let id = Range(m.range(at: 1), in: content) {
                let title = unescape(String(attributes[r]))
                if title.hasPrefix("来自对话「"), title.hasSuffix("」") { // i18n-ignore: legacy context title
                    relay = MessageSource(kind: "conversation", conversationID: String(content[id]),
                                          title: clean(String(title.dropFirst(5).dropLast())))
                }
            }
            rest = String(rest[whole.upperBound...])
        }
        if let relay { return (relay, rest.trimmingCharacters(in: .whitespacesAndNewlines)) }
        let trimmed = rest.trimmingCharacters(in: .whitespacesAndNewlines)
        if let match = supervisionHead.firstMatch(in: trimmed, range: NSRange(trimmed.startIndex..., in: trimmed)),
           let range = Range(match.range, in: trimmed) {
            let body = String(trimmed[range.upperBound...])
                .replacingOccurrences(of: #"(?m)^<persona-supervision\b[^>]*?/>\s*"#, with: "", options: .regularExpression)
                .trimmingCharacters(in: .whitespacesAndNewlines)
            return (MessageSource(kind: "supervision", title: nil), body.isEmpty ? String(trimmed[..<range.upperBound]) : body)
        }
        return (nil, raw)
    }

    private static func unescape(_ text: String) -> String {
        text.replacingOccurrences(of: "&quot;", with: "\"").replacingOccurrences(of: "&lt;", with: "<")
            .replacingOccurrences(of: "&gt;", with: ">").replacingOccurrences(of: "&amp;", with: "&")
    }
}
