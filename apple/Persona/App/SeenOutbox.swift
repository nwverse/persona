import Foundation

/// The finished agents marked seen here that the hub has not taken yet (#1588). A `seen` over a socket that still says
/// connected but is gone (the hub restarted, the phone just came back) was lost without a word, and the next list from
/// the hub put every one of them back to Unread: Mark All as Read looked like it failed. A mark stays here until a list
/// from the hub shows it recorded (a later `seen_at`), or the agent moved on; until then the session is shown seen and
/// the mark goes again (`AppModel.sendSeen`).
///
/// Only the hub's own times are compared with each other (its `seen_at`, the machine's `updated_at`): the phone's
/// clock only says when to try again and when to give up.
nonisolated struct SeenOutbox {
    /// How long a mark sent over the socket gets before it goes again over HTTP, which answers.
    static let confirmWithin: TimeInterval = 5
    /// Given up after this: the hub was out of reach all along.
    static let keepFor: TimeInterval = 10 * 60
    /// The machine touched the finished agent's transcript without a new turn (the hub's own slack is 2 s).
    static let sameTurn: TimeInterval = 2

    enum Route: Equatable { case socket, http }

    struct Mark: Equatable {
        /// The hub's `seen_at` and the agent's `updated_at` when it was marked.
        var seenBefore: Date?
        var updatedBefore: Date?
        /// On the phone's clock.
        var markedAt: Date
        var sentAt: Date
        var route: Route = .socket
        /// The hub answered the HTTP request: nothing more to send, only the list to wait for.
        var answered = false
        /// The user's Mark as read, not a chat being looked at: only these count while Manual mark as read is on (#7356).
        var manual = false
    }

    private(set) var marks: [SessionKey: Mark] = [:]

    /// `session` as the app had it when the user looked at it. Only an unread one waits for the hub.
    mutating func mark(_ key: SessionKey, _ session: Session, at now: Date, manual: Bool = false) {
        guard session.state == .unread else { return }
        marks[key] = Mark(seenBefore: session.seenAt, updatedBefore: session.updatedAt, markedAt: now, sentAt: now, manual: manual)
    }

    /// Whether the waiting mark for `key` is the user's Mark as read.
    func isManual(_ key: SessionKey) -> Bool { marks[key]?.manual ?? false }

    /// Manual mark as read turned on (#7356): the marks made by looking go (the hub ignores them now); the sessions they
    /// held seen, still unread on the hub, are returned to be shown Unread again.
    mutating func dropLooks() -> [SessionKey] {
        let looked = marks.filter { !$0.value.manual }.map(\.key)
        for key in looked { marks[key] = nil }
        return looked
    }

    mutating func sent(_ key: SessionKey, _ route: Route, at now: Date) {
        marks[key]?.sentAt = now
        marks[key]?.route = route
        marks[key]?.answered = false
    }

    mutating func answered(_ key: SessionKey) { marks[key]?.answered = true }

    /// Sent over the socket and not taken within `confirmWithin`: for HTTP now.
    func due(at now: Date) -> [SessionKey] {
        Array(marks.filter { _, m in
            m.route == .socket && !m.answered && now.timeIntervalSince(m.sentAt) >= Self.confirmWithin && !expired(m, now)
        }.keys)
    }

    /// When the next one sent over the socket is due, if any.
    func nextDue(at now: Date) -> Date? {
        marks.values.filter { $0.route == .socket && !$0.answered && !expired($0, now) }
            .map { $0.sentAt.addingTimeInterval(Self.confirmWithin) }.min()
    }

    /// Not answered yet in this group: sent again once its socket is back (an HTTP request that failed waits for that).
    func unanswered(group: String, at now: Date) -> [SessionKey] {
        Array(marks.filter { key, m in key.group == group && !m.answered && !expired(m, now) }.keys)
    }

    /// Lists from the hub landing: marks it has recorded, or that no longer matter, go; the sessions still waiting are
    /// shown seen. Lists of other machines leave their marks alone.
    mutating func land(_ lists: [MachineRef: [Session]], at now: Date) -> [MachineRef: [Session]] {
        guard !marks.isEmpty else { return lists }
        var out = lists
        for (key, m) in marks {
            guard var list = out[key.machineRef] else { continue }
            guard let i = list.firstIndex(where: { $0.sessionID == key.sessionID }) else {
                marks[key] = nil // gone from its machine
                continue
            }
            let s = list[i]
            let recorded = (s.seenAt ?? .distantPast) > (m.seenBefore ?? .distantPast)
            let newTurn = (s.updatedAt ?? .distantPast) > (m.updatedBefore ?? .distantPast).addingTimeInterval(Self.sameTurn)
            if recorded || newTurn || s.state == .working || s.state == .needsYou || expired(m, now) {
                marks[key] = nil
                continue
            }
            if s.state == .unread {
                list[i].state = .idle
                out[key.machineRef] = list
            }
        }
        return out
    }

    private func expired(_ m: Mark, _ now: Date) -> Bool { now.timeIntervalSince(m.markedAt) > Self.keepFor }
}
