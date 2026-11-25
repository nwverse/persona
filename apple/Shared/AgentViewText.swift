import Foundation
import os
import PersonaWire
import SwiftUI

/// How the app reads and draws an Agent's five-phase view (docs/protocol/agent-states.md "Five phases"): the phase's
/// word and color, the reason's word under it, and the few questions screens ask of it. Every switch over the generated
/// `AgentPhase` / `AgentReason` lives here and is exhaustive; a value this app does not know draws as idle.
nonisolated extension AgentView {
    /// Seen, and its last turn asks the owner something nobody answered yet (D-A: done, never counted).
    var asksSeen: Bool {
        if case .asks(let r) = reason { return r.unseen != true }
        return false
    }
    /// It finished and the owner has not seen the result (asking something or not).
    var isUnseen: Bool {
        if case .unseen = reason { return true }
        if case .asks(let r) = reason { return r.unseen == true }
        return false
    }
    /// Seen, and its turn finished before anybody looked (#9751).
    var isJustFinished: Bool {
        if case .justFinished = reason { return true }
        return false
    }
    /// Idle, finished and seen: the quiet rows' word that rows leave out.
    var isSeen: Bool {
        if case .seen = reason { return true }
        return false
    }
    /// Working, but all it runs in the background only waits (#6045).
    var waitsInBackground: Bool {
        if case .background(let r) = reason { return r.waiting == true }
        return false
    }
    /// Idle and still there (seen, or its host does not say): folded away in busy spaces. One that is not running is not
    /// live, an error or a finished turn not quiet.
    var isQuiet: Bool {
        switch phase {
        case .idle:
            if case .notRunning = reason { return false }
            return true
        case .unknown: return true
        case .needsYou, .working, .done, .error: return false
        }
    }
    /// It needs the owner and its row counts in the Agents list's Needs you (fleetcount's decision).
    var countsInNeedsYou: Bool { phase == .needsYou && counted == true }

    /// The legacy six-state word the screens not yet moved to the view take (docs/plans/plan-typed-core.md, P5b): read
    /// off the view, never off the wire's frozen `state`. An error reads as Seen: it has its own section and words.
    var legacyState: AgentState {
        switch phase {
        case .needsYou: return .needsYou
        case .working: return .working
        case .done: return isUnseen ? .unread : .idle
        case .error: return .idle
        case .idle:
            if case .notRunning = reason { return .offline }
            if case .hostUnknown = reason { return .unknown }
            return .idle
        case .unknown: return .unknown
        }
    }

    /// The phase's place among the five, lower first: the Agents list's By Status sections (`AgentPhaseSection`).
    var section: AgentPhaseSection { AgentPhaseSection(phase) }

    /// The reason's words, as a row says where it stands (the phase is its section's heading); a muted one that asks or
    /// just finished reads as Seen, as it did.
    func reasonText(muted: Bool) -> String {
        switch reason {
        case .cards, .terminalDialog: return AgentViewWords.needsYou
        case .turn: return AgentViewWords.working
        case .background(let r): return r.waiting == true ? AgentViewWords.backgroundWaiting : AgentViewWords.backgroundWorking
        case .unseen: return AgentViewWords.unread
        case .justFinished: return muted ? AgentViewWords.seen : AgentViewWords.justFinished
        case .asks(let r): return r.unseen == true ? AgentViewWords.unread : muted ? AgentViewWords.seen : AgentViewWords.awaitsReply
        case .usageLimit(let r): return r.resumes == true ? AgentViewWords.usageLimitResumes : AgentViewWords.usageLimit
        case .model(let r): return AgentViewWords.model(code: r.code)
        case .startRefused: return AgentViewWords.couldNotStart
        case .restartInterrupted: return AgentViewWords.interrupted
        case .seen: return AgentViewWords.seen
        case .notRunning: return AgentViewWords.notRunning
        case .hostUnknown: return AgentViewWords.statusUnknown
        case .unknown, nil: return section.title
        }
    }

    /// The colors of `reasonText`: the phase's, Waiting in the Background lighter, and a muted finished turn plain.
    func tone(muted: Bool) -> StatusTone {
        if waitsInBackground { return StatusPalette.backgroundWaiting }
        if muted, isJustFinished || asksSeen { return StatusPalette.idle }
        if case .notRunning = reason { return StatusPalette.offline }
        return section.tone
    }
}

/// The five phases as the Agents list's By Status sections go, in rank order (docs/protocol/agent-states.md "Rank"):
/// needs you, error, working, done, idle. A phase this app does not know goes with idle.
nonisolated enum AgentPhaseSection: Int, CaseIterable, Sendable {
    case needsYou, error, working, done, idle

    init(_ phase: AgentPhase) {
        switch phase {
        case .needsYou: self = .needsYou
        case .error: self = .error
        case .working: self = .working
        case .done: self = .done
        case .idle, .unknown: self = .idle
        }
    }

    /// The section a rank sorts into: its tens (`AgentViewFallback.Rank`), idle past the last one.
    init(rank: Int) { self = AgentPhaseSection(rawValue: min(max(rank / 10, 0), Self.idle.rawValue)) ?? .idle }

    /// The fold id's key (`state/<key>`).
    var key: String {
        switch self {
        case .needsYou: "needs"
        case .error: "error"
        case .working: "working"
        case .done: "done"
        case .idle: "idle"
        }
    }

    var title: String {
        switch self {
        case .needsYou: AgentViewWords.needsYou
        case .error: AgentViewWords.error
        case .working: AgentViewWords.working
        case .done: AgentViewWords.done
        case .idle: AgentViewWords.idle
        }
    }

    var tone: StatusTone {
        switch self {
        case .needsYou: StatusPalette.needsYou
        case .error: StatusPalette.error
        case .working: StatusPalette.working
        case .done: StatusPalette.unread
        case .idle: StatusPalette.idle
        }
    }
}

/// The view's words, resolved once per interface locale (docs/apple/performance.md P4): rows draw them on every redraw.
/// The phases and reasons the agent states already named keep `StateName`'s words (#3960).
nonisolated enum AgentViewWords {
    static var needsYou: String { StateName.needsYou.text }
    static var working: String { StateName.working.text }
    static var done: String { StateName.done.text }
    static var unread: String { StateName.unread.text }
    static var justFinished: String { StateName.justFinished.text }
    static var awaitsReply: String { StateName.awaitsReply.text }
    static var seen: String { StateName.seen.text }
    static var notRunning: String { StateName.notRunning.text }
    static var backgroundWaiting: String { kept("backgroundWaiting") { interfaceText("Waiting in the background") } } // perf-ok: resolved once per locale
    static var backgroundWorking: String { kept("backgroundWorking") { interfaceText("Working (Background)") } } // perf-ok: resolved once per locale
    static var error: String { kept("error") { interfaceText("Error [Agent View]") } } // perf-ok: resolved once per locale
    static var idle: String { kept("idle") { interfaceText("Idle [Agent View]") } } // perf-ok: resolved once per locale
    /// D-B: its host does not say where it stands.
    static var statusUnknown: String { kept("statusUnknown") { interfaceText("Status unknown [Agent View]") } } // perf-ok: resolved once per locale
    /// D-C: its computer restarted while it ran; Continue picks it up.
    static var interrupted: String { kept("interrupted") { interfaceText("Interrupted [Agent View]") } } // perf-ok: resolved once per locale
    static var usageLimit: String { kept("usageLimit") { interfaceText("Usage limit reached [Agent View]") } } // perf-ok: resolved once per locale
    static var usageLimitResumes: String { kept("usageLimitResumes") { interfaceText("Resumes after the usage limit [Agent View]") } } // perf-ok: resolved once per locale
    static var couldNotStart: String { kept("couldNotStart") { interfaceText("Couldn't start [Agent View]") } } // perf-ok: resolved once per locale

    /// A model error in plain words: what is wrong when the hub can tell (`turn_error_code`), else that the model failed.
    static func model(code: String?) -> String {
        switch code {
        case "not_logged_in": kept("notSignedIn") { interfaceText("Not signed in [Agent View]") } // perf-ok: resolved once per locale
        case "auth_failed": kept("signInFailed") { interfaceText("Sign-in failed [Agent View]") } // perf-ok: resolved once per locale
        case "model_route": kept("modelUnavailable") { interfaceText("Model unavailable [Agent View]") } // perf-ok: resolved once per locale
        case "rate_limited": kept("rateLimited") { interfaceText("Too many requests [Agent View]") } // perf-ok: resolved once per locale
        default: kept("modelError") { interfaceText("Model error [Agent View]") } // perf-ok: resolved once per locale
        }
    }

    private static let resolved = OSAllocatedUnfairLock<[String: String]>(initialState: [:])

    private static func kept(_ name: String, _ resolve: () -> String) -> String {
        let key = interfaceTextLocale.identifier + "|" + name
        if let known = resolved.withLock({ $0[key] }) { return known }
        let text = resolve()
        resolved.withLock { $0[key] = text }
        return text
    }
}
