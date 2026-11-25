import Foundation
import PersonaWire

/// An Agent's five-phase view (`AgentView`, docs/protocol/agent-states.md "Five phases") for a row that came without
/// one: a hub without the `agent_view` feature, or a row it sent no `view` for. The one place the app infers a view
/// (docs/plans/plan-typed-core.md 2.4): the hub's own rules (`server/internal/assistant/agentview` and
/// `fleetcountadapt.ViewSession`), read off the legacy fields, so an old hub's rows draw, sort and count as a new
/// hub's do. `PersonaTests/AgentViewFallbackFixtureTests` replays spec/fixtures/agent-view through it. Everything else
/// reads `Session.agentView` and never derives a phase of its own.
nonisolated enum AgentViewFallback {
    /// Where the Agents list's Needs you put the session (fleetcount's row on the hub; `AgentsNeedsYou.fallbackRows`
    /// in the app, which replays spec/fixtures/needs-you).
    enum Row: Hashable, Sendable {
        /// A row of its own its cards stand for (its folded ones' too); `held`: none of them is the owner's yet.
        case cards(n: Int, held: Bool, counted: Bool)
        /// A row of its own for a dialog only its terminal shows.
        case dialog(counted: Bool)
        /// Cards and no row of its own: they sit under another row, or count nowhere.
        case elsewhere(n: Int, held: Bool)
        /// No row: its task's supervisor holds every card it asked (D11).
        case supervised
        /// No row and no card.
        case noRow
    }

    /// The ranks, lower first (docs/protocol/agent-states.md "Rank"); a muted session in error, working or done takes its
    /// phase's last place.
    enum Rank {
        static let needsYouCounted = 0
        static let needsYouUncounted = 1
        static let error = 10
        static let errorResuming = 11
        static let workingTurn = 20
        static let workingBackground = 21
        static let workingWaiting = 22
        static let doneUnseen = 30
        static let doneAsks = 31
        static let doneJustFinished = 32
        static let idleSeen = 40
        static let idleHostUnknown = 41
        static let idleNotRunning = 42

        /// The last rank of the phase `rank` is in, where a muted one goes.
        static func muted(_ rank: Int) -> Int { rank / 10 * 10 + 9 }
    }

    /// The row a session's own fields give when no list decided one: a session that needs the user is its own row,
    /// counted unless it is muted, a quiet task's, directory-hidden or one of the assistant's.
    static func ownRow(_ s: Session, muted: Bool = false) -> Row {
        guard s.state == .needsYou else { return .noRow }
        let counted = !muted && !s.assistant && !s.quiet && s.hiddenByDirectory != true
        return s.pendingInteractions > 0 ? .cards(n: s.pendingInteractions, held: false, counted: counted) : .dialog(counted: counted)
    }

    /// `s`'s view in `row`, `muted` when the owner muted it: needs you from its row first, then an interrupted session
    /// and an error (D-C), work, and a finished turn: asking the owner is done and never counted (D-A); a host that does
    /// not say is idle (D-B).
    static func view(_ s: Session, row: Row, muted: Bool) -> AgentView {
        var v = byRow(row) ?? byTurn(s)
        if muted, v.phase == .error || v.phase == .working || v.phase == .done { v.rank = Rank.muted(v.rank) }
        return v
    }

    private static func byRow(_ row: Row) -> AgentView? {
        switch row {
        case .cards(let n, let held, let counted): waits(.cards(ReasonCards(n: n, held: flag(held))), counted: counted)
        case .dialog(let counted): waits(.terminalDialog(ReasonTerminalDialog()), counted: counted)
        case .elsewhere(let n, let held): waits(.cards(ReasonCards(n: n, held: flag(held))), counted: false)
        case .supervised: AgentView(phase: .working, reason: .turn(ReasonTurn()), rank: Rank.workingTurn)
        case .noRow: nil
        }
    }

    private static func waits(_ reason: AgentReason, counted: Bool) -> AgentView {
        AgentView(phase: .needsYou, reason: reason, counted: flag(counted), rank: counted ? Rank.needsYouCounted : Rank.needsYouUncounted)
    }

    private static func byTurn(_ s: Session) -> AgentView {
        let failure = Failure(s)
        switch (s.state, failure) {
        case (.needsYou, _): // no row and no card: one of the assistant's, or a hidden one
            return waits(.terminalDialog(ReasonTerminalDialog()), counted: false)
        case (.working, _) where s.backgroundWorking && s.backgroundWaiting:
            return AgentView(phase: .working, reason: .background(ReasonBackground(waiting: true)), rank: Rank.workingWaiting)
        case (.working, _) where s.backgroundWorking:
            return AgentView(phase: .working, reason: .background(ReasonBackground()), rank: Rank.workingBackground)
        case (.working, _):
            return AgentView(phase: .working, reason: .turn(ReasonTurn()), rank: Rank.workingTurn)
        case (.unknown, _) where s.wasInterrupted:
            let resumable = s.capabilities.map { $0.send || $0.terminal } ?? true
            return AgentView(phase: .error, reason: .restartInterrupted(ReasonRestartInterrupted(resumable: flag(resumable))), rank: Rank.error)
        case (_, .usageLimit(let resumes)):
            let reason = ReasonUsageLimit(resetsAt: s.turnErrorResetsAt.map { $0.formatted(.iso8601) }, resumes: flag(resumes))
            return AgentView(phase: .error, reason: .usageLimit(reason), rank: resumes ? Rank.errorResuming : Rank.error)
        case (_, .startRefused):
            let args = s.startErrorArgs.flatMap { $0.isEmpty ? nil : $0 }
            return AgentView(phase: .error, reason: .startRefused(ReasonStartRefused(code: text(s.startErrorCode), args: args)), rank: Rank.error)
        case (_, .model):
            return AgentView(phase: .error, reason: .model(ReasonModel(code: text(s.turnErrorCode))), rank: Rank.error)
        case (.unread, .noFailure) where s.awaitsReply:
            return AgentView(phase: .done, reason: .asks(ReasonAsks(question: text(s.asks), unseen: true)), rank: Rank.doneUnseen)
        case (.unread, .noFailure):
            return AgentView(phase: .done, reason: .unseen(ReasonUnseen()), rank: Rank.doneUnseen)
        case (.idle, .noFailure) where s.awaitsReply:
            return AgentView(phase: .done, reason: .asks(ReasonAsks(question: text(s.asks))), rank: Rank.doneAsks)
        case (.idle, .noFailure) where s.work?.justFinished == true:
            return AgentView(phase: .done, reason: .justFinished(ReasonJustFinished()), rank: Rank.doneJustFinished)
        case (.idle, .noFailure):
            return AgentView(phase: .idle, reason: .seen(ReasonSeen()), rank: Rank.idleSeen)
        case (.offline, .noFailure):
            return AgentView(phase: .idle, reason: .notRunning(ReasonNotRunning()), rank: Rank.idleNotRunning)
        case (.unknown, .noFailure):
            return AgentView(phase: .idle, reason: .hostUnknown(ReasonHostUnknown()), rank: Rank.idleHostUnknown)
        }
    }

    /// The error that ended its start or its last turn: a start error first, then a turn error (Persona's own Stop is
    /// none).
    private enum Failure: Equatable {
        case noFailure, startRefused, model
        case usageLimit(resumes: Bool)

        init(_ s: Session) {
            if AgentViewFallback.text(s.startError) != nil || AgentViewFallback.text(s.startErrorCode) != nil {
                self = .startRefused
            } else if AgentViewFallback.text(s.turnError) == nil || s.turnErrorKind == "stopped" {
                self = .noFailure
            } else if s.turnErrorKind == "usage_limit" {
                self = .usageLimit(resumes: s.turnErrorResumes == true)
            } else {
                self = .model
            }
        }
    }

    /// An optional flag as the hub writes it: only when true.
    private static func flag(_ on: Bool) -> Bool? { on ? true : nil }

    /// Text as the hub writes it: trimmed, and left out when empty.
    private static func text(_ s: String?) -> String? {
        guard let t = s?.trimmingCharacters(in: .whitespacesAndNewlines), !t.isEmpty else { return nil }
        return t
    }
}

nonisolated extension Session {
    /// Its five-phase view: the hub's (`view`), or for a row without one the fallback's from its own fields
    /// (`AgentViewFallback`). What every screen draws, sorts and counts by.
    var agentView: AgentView { view ?? AgentViewFallback.view(self, row: AgentViewFallback.ownRow(self), muted: false) }

    /// Its view as a list that knows whether the owner muted it sorts it: the hub's, which counted the mute already, or
    /// the fallback's with `muted`.
    func agentView(muted: Bool) -> AgentView {
        view ?? AgentViewFallback.view(self, row: AgentViewFallback.ownRow(self, muted: muted), muted: muted)
    }
}
