import Foundation
import Observation

/// Move To… (#11516, docs/apple/sidebar.md "Pinned directions"): where the owner files a session (an assistant
/// conversation or an Agent) under one of the current directions, or on its own. The hub decides and keeps every
/// placement (`PUT /api/v1/assistant/sessions/{mid}/{sid}/direction`); this store only sends the owner's pick once,
/// draws it at once where the session is drawn under a direction (the sidebar row's expansion, the page) while the
/// answer is out, and then reads the tree (and the open page) again.
///
/// - **Gate.** Offered only while the hub lists `direction_sessions` and the app holds the default manager's direction
///   tree (`AppModel.directionTree`, which another manager's page never holds). A manager that reconnected at an older
///   version answers the routes 404 or 405 without their codes: hidden then until the next hello (`routesMissing`).
/// - **Undo** sends the answer's `previous` exactly, `null` written out (`DirectionFilingBody`).
/// - **Refusals** put the row back and show the hub's sentence; nothing is retried.
/// - **Offline and lost answers.** Sent only while the hub is reachable, and never held for later. An answer lost after
///   sending reads the tree and the page again, sends nothing again and offers no Undo.
/// - Filing makes no model call and sends nothing else.
@MainActor @Observable final class DirectionFilings {
    static let shared = DirectionFilings(env: .live)

    /// A choice of Move To…: a current direction by the name its row shows, in the tree's order.
    nonisolated struct Choice: Hashable, Sendable, Identifiable {
        var id: String
        var name: String
    }

    /// A move sent and not settled yet: where the session is drawn meanwhile.
    nonisolated struct Pending: Hashable, Sendable {
        /// The direction it is drawn under: its id, "" for none.
        var to: String
        /// Where it was drawn before the pick, when known: what an Undo draws it under again.
        var from: String?
        var title: String
        /// The session's own state when picked (`AgentState`'s raw value), to draw it under a direction meanwhile.
        var state: String
        /// The hub answered; the rows wait for the tree's read to draw it from there.
        var answered = false
        /// Which pick it is: a later pick of the same session replaces it, and only its own answer settles it.
        var serial = 0
    }

    /// What the store needs of the app (`live`), and of a test.
    struct Env {
        var api: @MainActor () -> APIClient?
        /// The hub's socket is up: a move is sent only then.
        var reachable: @MainActor () -> Bool
        var networkUp: @MainActor () -> Bool
        var toast: @MainActor (Toast) -> Void
        /// Reads the direction tree again.
        var rereadTree: @MainActor () async -> Void

        static var live: Env {
            Env(api: { AppModel.shared.api },
                reachable: { AppModel.shared.isLive },
                networkUp: { AppModel.shared.connection.networkUp },
                toast: { AppModel.shared.toast = $0 },
                rereadTree: { await AppModel.shared.refreshDirectionTree() })
        }
    }

    /// The moves out, by session.
    private(set) var pending: [SessionKey: Pending] = [:]
    /// The routes answered as missing since the last hello: no entry is drawn until the next one.
    private(set) var routesMissing = false
    /// Counts the moves this device made that the hub answered (or whose answer was lost): an open page reads again.
    private(set) var written = 0

    @ObservationIgnored private let env: Env
    @ObservationIgnored private var serial = 0
    /// The pages open now, by direction id: read again after a write, together with the tree.
    @ObservationIgnored private var pages: [String: () async -> Void] = [:]

    init(env: Env) { self.env = env }

    // MARK: Gate and choices

    /// Whether Move To…, the filed rows and the page are drawn: `direction_sessions` listed, the default manager's tree
    /// held, and the routes not answered as missing since the last hello.
    nonisolated static func shows(features: Set<String>, tree: DirectionTree?, routesMissing: Bool) -> Bool {
        features.contains(DirectionRoutes.feature) && tree != nil && !routesMissing
    }

    func shows(_ model: AppModel) -> Bool {
        Self.shows(features: model.hubFeatures, tree: model.directionTree, routesMissing: routesMissing)
    }

    /// The choices in the tree's order, each by the name its sidebar row shows.
    func choices(_ model: AppModel) -> [Choice] {
        (model.directionTree?.leaders ?? []).map { Choice(id: $0.id, name: model.directionTitle($0)) }
    }

    /// The choices Move To… offers for session `key` (none: no Move To…). Not for the main conversation, the background
    /// supervisor or a current leader conversation; not for an Agent that follows its supervised task (or a sub-agent
    /// session under one: it follows its task, and the hub refuses it with 409 `direction_supervised_task`); not for
    /// another hub's session. The caller reads this when its menu is built, from the smallest view that draws it.
    func offers(_ key: SessionKey, in model: AppModel) -> [Choice] {
        guard shows(model), key.group == (model.hub?.group ?? ""), !key.sessionID.isEmpty else { return [] }
        if key == model.assistantMain || model.isBackgroundSupervisor(key.sessionID) { return [] }
        if model.directionTree?.leader(key.sessionID) != nil || model.assistantLeaders.contains(key.sessionID) { return [] }
        let followers = TodoStore.shared.taskFollowers
        if Self.follows(key, followers: followers, parent: { model.session($0)?.parentSessionID }) { return [] }
        return choices(model)
    }

    /// Whether `key` is an Agent of an open supervised to-do, or a session under one (its `parent_session_id`s, or a
    /// running sub-agent's `<parent>@<id>`), as the hub's `taskFollowers.follows` walks it: at most eight levels.
    nonisolated static func follows(_ key: SessionKey, followers: Set<String>,
                                    parent: (SessionKey) -> String?) -> Bool {
        guard !followers.isEmpty else { return false }
        var k = key
        for _ in 0..<8 {
            if followers.contains(k.machineID + "/" + k.sessionID) { return true }
            let up = parent(k) ?? k.sessionID.split(separator: "@", maxSplits: 1).first.map(String.init).flatMap {
                $0 == k.sessionID ? nil : $0
            }
            guard let up, !up.isEmpty, up != k.sessionID else { return false }
            k.sessionID = up
        }
        return false
    }

    // MARK: Drawing

    /// The direction the session is drawn under while its move is out ("" none), or nil when no move is out.
    func drawnUnder(_ key: SessionKey) -> String? { pending[key]?.to }

    /// A filed session as a row under direction `direction` draws it: the tree's or the read's, with the moves out
    /// applied (moved away: gone; moved here: added, marked pending). `live` keeps only what the tree lists (working
    /// or needing you), as the sidebar row's expansion draws; the page draws them all.
    nonisolated struct Row: Hashable, Sendable, Identifiable {
        var session: DirectionTree.FiledSession
        var key: SessionKey
        /// Its move is out (not answered yet).
        var moving: Bool
        var id: String { session.id }
    }

    func rows(_ listed: [DirectionTree.FiledSession], in direction: String, group: String, live: Bool) -> [Row] {
        var out: [Row] = []
        var seen: Set<SessionKey> = []
        for s in listed {
            let key = s.key(group: group)
            guard seen.insert(key).inserted else { continue }
            if let p = pending[key], p.to != direction { continue }
            out.append(Row(session: s, key: key, moving: pending[key].map { !$0.answered } ?? false))
        }
        for (key, p) in pending.sorted(by: { $0.key.path < $1.key.path })
        where p.to == direction && key.group == group && !seen.contains(key) {
            guard !live || Self.liveStates.contains(p.state) else { continue }
            let s = DirectionTree.FiledSession(machineID: key.machineID, sessionID: key.sessionID, title: p.title,
                                               source: "owner", at: nil, state: p.state)
            out.append(Row(session: s, key: key, moving: !p.answered))
        }
        return out
    }

    /// The states the tree lists a filed session in.
    nonisolated static let liveStates: Set<String> = [AgentState.working.rawValue, AgentState.needsYou.rawValue]

    // MARK: Moving

    /// Moves session `key` (titled `title`, its own state `state`) under `choice`'s direction, or on its own (nil).
    /// `from` is the direction it is drawn under now, when known.
    func move(_ key: SessionKey, title: String, state: AgentState, to choice: Choice?, from: String?) {
        let target: DirectionTarget = choice.map { .direction($0.id) } ?? .own
        send(key, title: title, state: state.rawValue, target: target, drawnUnder: target.drawnUnder, from: from,
             name: choice?.name, undo: false)
    }

    /// The hub's reachability is known before sending; a move is never held for later.
    private func send(_ key: SessionKey, title: String, state: String, target: DirectionTarget, drawnUnder: String,
                      from: String?, name: String?, undo: Bool) {
        guard let api = env.api() else { return }
        guard env.reachable() else {
            // Nothing was drawn yet: say why nothing happened (the same words as any action the hub did not get).
            env.toast(Toast(env.networkUp() ? String(localized: "Can't reach the hub. That action didn't go through.")
                                            : String(localized: "No network. That action didn't go through."), style: .info))
            Haptics.attention()
            return
        }
        Haptics.tap()
        serial += 1
        let pick = serial
        pending[key] = Pending(to: drawnUnder, from: from, title: title, state: state, serial: pick)
        Task { [weak self] in
            let outcome: Result<DirectionFilingAnswer, Error>
            do { outcome = .success(try await api.fileSession(key, to: target)) } catch { outcome = .failure(error) }
            await self?.settle(key, pick: pick, outcome: outcome, title: title, state: state, name: name, undo: undo)
        }
    }

    private func settle(_ key: SessionKey, pick: Int, outcome: Result<DirectionFilingAnswer, Error>, title: String,
                        state: String, name: String?, undo: Bool) async {
        let before = pending[key]
        /// Only this pick's own entry: a later pick of the same session keeps its own.
        func clear() { if pending[key]?.serial == pick { pending[key] = nil } }
        switch outcome {
        case .success(let answer):
            if pending[key]?.serial == pick { pending[key]?.answered = true }
            if !undo { env.toast(Self.answered(answer, name: name) { [weak self] in
                self?.undo(key, answer: answer, title: title, state: state, back: before?.from)
            }) }
            Haptics.success()
            await reread()
            clear()
        case .failure(let error):
            switch Self.failure(error) {
            case .missingRoutes:
                routesMissing = true
                clear()
                env.toast(Toast(String(localized: "Not available on this hub"), style: .info))
                Haptics.attention()
            case .refused(let sentence):
                clear() // back where it was
                env.toast(Toast(sentence, style: .error))
                Haptics.error()
            case .notSent:
                clear()
                env.toast(Toast(env.networkUp() ? String(localized: "Can't reach the hub. That action didn't go through.")
                                                : String(localized: "No network. That action didn't go through."), style: .info))
                Haptics.attention()
            case .lost:
                // It may have been taken: read where it is now, send nothing again, offer no Undo.
                env.toast(Toast(String(localized: "Couldn't confirm the move. Showing where it is now."), style: .info))
                Haptics.attention()
                await reread()
                clear()
            }
        }
    }

    /// The Undo of an answered move: the answer's `previous`, exactly. Drawn back where it was before the move.
    private func undo(_ key: SessionKey, answer: DirectionFilingAnswer, title: String, state: String, back: String?) {
        let target = DirectionTarget(answer: answer.previous)
        let drawn = target == .remove ? (back ?? "") : target.drawnUnder
        send(key, title: title, state: state, target: target, drawnUnder: drawn, from: answer.direction, name: nil, undo: true)
    }

    /// The toast for an answer: where it went, with Undo when the book changed; "Already there" without one when not.
    static func answered(_ answer: DirectionFilingAnswer, name: String?, undo: @escaping @MainActor () -> Void) -> Toast {
        guard answer.changed else { return Toast(String(localized: "Already there [Move To]"), style: .info) }
        let words = name.map { String(localized: "Moved to “\($0)” [Move To]") } ?? String(localized: "Set on its own [Move To]")
        return Toast(words, style: .success, action: Toast.Action(title: String(localized: "Undo"), run: undo))
    }

    /// How a write that did not answer 2xx ended.
    nonisolated enum Failure: Equatable, Sendable {
        /// 404 / 405 without one of the routes' codes: an older manager; hidden until the next hello.
        case missingRoutes
        /// The hub refused it with a sentence of its own (any 4xx, or a 5xx with a `code`): nothing changed.
        case refused(String)
        /// It never left: no connection to the hub could be made.
        case notSent
        /// It went out and its answer did not come back (cut off, timed out, a 5xx with no code): it may have been taken.
        case lost
    }

    nonisolated static func failure(_ error: Error) -> Failure {
        if DirectionRoutes.missing(error) { return .missingRoutes }
        if let e = error as? APIError {
            // A relay cut off while the manager had it says so (`delivered: "unknown"`): it may have been taken.
            if e.fields["delivered"] == "unknown" || Self.inDoubt.contains(e.code ?? "") { return .lost }
            if (400..<500).contains(e.status) || (e.status >= 500 && e.code != nil) {
                return .refused(e.localizedDescription)
            }
            return .lost
        }
        let ns = error as NSError
        if ns.domain == NSURLErrorDomain, Self.neverSent.contains(ns.code) { return .notSent }
        return .lost
    }

    /// Codes that say the write may have been taken though its answer did not come back.
    nonisolated private static let inDoubt: Set<String> = ["assistant_in_doubt", "lost_receipt", "send_in_doubt"]

    /// URL errors that mean no request reached the hub.
    nonisolated private static let neverSent: Set<Int> = [
        NSURLErrorNotConnectedToInternet, NSURLErrorCannotConnectToHost, NSURLErrorCannotFindHost,
        NSURLErrorDNSLookupFailed, NSURLErrorDataNotAllowed, NSURLErrorInternationalRoamingOff, NSURLErrorCallIsActive,
        NSURLErrorSecureConnectionFailed,
    ]

    /// The tree and every open page, read again together (the page reads only on open, on reconnect and after a write
    /// of this device: P7 / P9).
    private func reread() async {
        written += 1
        for read in Array(pages.values) { await read() }
        await env.rereadTree()
    }

    // MARK: Pages and hello

    /// An open page, read again after this device's writes; `nil` when it closes.
    func page(_ id: String, read: (() async -> Void)?) { pages[id] = read }

    /// A read of the page answered as a missing route: the same as a write's (hidden until the next hello).
    func routesAnsweredMissing() { if !routesMissing { routesMissing = true } }

    /// The direction the tree lists session `key` under (a live filed session), when it does.
    func placed(_ key: SessionKey, in tree: DirectionTree?) -> String? {
        tree?.leaders.first { d in d.sessions.contains { $0.machineID == key.machineID && $0.sessionID == key.sessionID } }?.id
    }

    /// A hello from the hub: its features say again whether the routes are there.
    func hello() { if routesMissing { routesMissing = false } }

    /// Another hub or manager: the moves out were the one before's.
    func forget() {
        if !pending.isEmpty { pending = [:] }
        if routesMissing { routesMissing = false }
    }
}
