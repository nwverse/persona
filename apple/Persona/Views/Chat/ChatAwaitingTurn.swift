import Foundation

/// The wait between a message of yours and the hub saying the session is at work (#3853; the web's `awaitingTurn`,
/// #3654). The node reports working only once it reads the agent's hook, terminal title or transcript, and the hub
/// pushes it with its next list, a second to ten and more after the send; until then the session's status still says
/// idle or done and the chat drew neither the getting-ready line nor the turn's work line. During that wait the turn
/// counts as starting: before the transcript has your message (it is a pending bubble) and after it (the newest turn is
/// your message and nothing has answered it). The hub's working takes over from there; an answer, a status that is not
/// idle / done, a failure the hub reported after the message, or `window` without either ends it.
nonisolated enum ChatAwaitingTurn: Equatable {
    /// Nothing to wait for: the hub's status decides.
    case none
    /// Sent from here, the transcript does not have it yet.
    case sending
    /// The transcript has it: the turn `id` is the newest block, your message with nothing after it.
    case echoed(id: String)

    /// How long a message of yours counts as the start of a turn the hub has not reported yet (the web's `AWAIT_MS`).
    static let window: TimeInterval = 60

    /// The turn that counts as running while it waits, once the transcript has the message.
    var turnID: String? { if case .echoed(let id) = self { id } else { nil } }

    /// - Parameters:
    ///   - pending: messages sent from here that the transcript does not have yet.
    ///   - last: the chat's newest block.
    ///   - status: the session's status as the hub last pushed it.
    ///   - failedSince: when the hub reported the session's failure (its `state_since`; `.distantFuture` when it gave
    ///     no time, so the failure counts as this message's), nil without one.
    static func of(pending: [PendingMessage], last: ChatBlock?, status: SessionStatus, failedSince: Date?,
                   now: Date) -> ChatAwaitingTurn {
        // Working or blocked: the hub drives it. Offline: nothing is worked on.
        guard status == .idle || status == .done || status == .unknown else { return .none }
        if let at = sentAt(pending), within(at, now), !failed(after: at, failedSince) { return .sending }
        guard case .turn(let t) = last, !t.users.isEmpty, t.steps.isEmpty, t.answer == nil,
              let at = t.startedAt, within(at, now), !failed(after: at, failedSince) else { return .none }
        return .echoed(id: t.id)
    }

    /// Whether the work the hub reports is for a message of yours the transcript does not have yet, not for the chat's
    /// newest turn: the session went to work (`stateSince`) after one of them was written, within `window` of it, and
    /// the transcript has nothing newer than that message. Drawn on the newest turn's work line, that work would read as
    /// more of the turn before until the message comes in. A message sent while the agent was already at work joins the
    /// running turn (Claude takes it in mid-turn) and leaves that line alone. So does work the transcript shows after
    /// the message: the hub resets `stateSince` on every change of state (blocked and back, its own restart), and a
    /// bubble whose copy is never matched would keep the running turn's line dark to its end.
    /// - Parameters:
    ///   - pending: messages sent from here that the transcript does not have yet, minus those Claude holds in its queue.
    ///   - status: the session's status as the hub last pushed it.
    ///   - stateSince: when the hub saw the session enter its current state.
    ///   - newestLine: the time of the chat's newest line that has one (nil: none has).
    static func workIsForPending(_ pending: [PendingMessage], status: SessionStatus, stateSince: Date?,
                                 newestLine: Date?) -> Bool {
        guard status.isActive, let since = stateSince else { return false }
        return pending.contains { p in
            guard onItsWay(p), let at = p.createdAt else { return false }
            if let newestLine, newestLine >= at { return false }
            return since >= at && within(at, since)
        }
    }

    /// When the wait that starts at the newest of these ends: nil for none still going.
    static func deadline(pending: [PendingMessage], last: ChatBlock?) -> Date? {
        let echoed: Date? = { if case .turn(let t) = last, t.steps.isEmpty, t.answer == nil { t.startedAt } else { nil } }()
        return [sentAt(pending), echoed].compactMap { $0 }.max().map { $0.addingTimeInterval(window) }
    }

    /// The newest message still on its way: one that waits for the network or failed is not being worked on. One with
    /// no time (an outbox saved before messages kept it) starts no wait: taking now for it would never let it age out.
    private static func sentAt(_ pending: [PendingMessage]) -> Date? {
        pending.filter(onItsWay).compactMap(\.createdAt).max()
    }

    /// Sending or sent: on its way to the agent (not waiting for the network, not failed).
    private static func onItsWay(_ p: PendingMessage) -> Bool { p.state == .sending || p.state == .sent }

    private static func within(_ at: Date, _ now: Date) -> Bool { now.timeIntervalSince(at) < window }

    /// The turn failed before writing anything (its failure note says so): nothing is worked on. A failure older than
    /// the message is the previous turn's (the hub clears it once a message goes in).
    private static func failed(after at: Date, _ since: Date?) -> Bool {
        guard let since else { return false }
        return since >= at
    }
}
