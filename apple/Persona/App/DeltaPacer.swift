import Foundation

/// How often a chat draws its streamed reply. The hub sends a delta every 80 ms with the whole text so far; drawing
/// each one re-laid the reply 12 times a second while it streamed. The first delta of a pause draws at once, then at
/// most one every `every` (5 a second), always the newest text; what is kept is dropped when the reply lands in the
/// transcript, the stream is cleared, or the socket resets, so an old text never comes back over the final message.
/// A streamed reply is not critical (docs/apple/ios-state.md「Timely」): questions, approvals and errors are other events.
nonisolated struct DeltaPacer {
    static let every: TimeInterval = 0.2

    enum Decision: Equatable {
        case draw(String) // now
        case later(TimeInterval) // kept: call `due` after this long
        case kept // kept: a `due` is already on its way
    }

    private var pending: [SessionKey: String] = [:]
    private var drawn: [SessionKey: Date] = [:]

    mutating func offer(_ key: SessionKey, _ text: String, now: Date) -> Decision {
        if text.isEmpty { // the stream was cleared: at once, and nothing older may follow
            pending[key] = nil
            drawn[key] = now
            return .draw(text)
        }
        if pending[key] != nil {
            pending[key] = text
            return .kept
        }
        let wait = Self.every - now.timeIntervalSince(drawn[key] ?? .distantPast)
        if wait <= 0 {
            drawn[key] = now
            return .draw(text)
        }
        pending[key] = text
        return .later(wait)
    }

    /// The text kept for `key`, once (nil when it was dropped meanwhile).
    mutating func due(_ key: SessionKey, now: Date) -> String? {
        guard let text = pending.removeValue(forKey: key) else { return nil }
        drawn[key] = now
        return text
    }

    func pendingText(_ key: SessionKey) -> String? { pending[key] }

    mutating func drop(_ key: SessionKey) { pending[key] = nil }
}
