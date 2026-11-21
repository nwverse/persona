import Foundation
import Testing
@testable import Persona
import PersonaWire

/// Move To… (#11516, `DirectionFilings`): the write's body and its Undo, every refusal, the hub out of reach and a lost
/// answer, the routes answered as missing, and what the rows draw while a move is out. Against `FakeHub`, with the
/// store's own requests and decoding.
@Suite(.serialized)
@MainActor
struct DirectionFilingsTests {
    private static let key = SessionKey(machineID: "m", sessionID: "s")
    private static let path = "/api/v1/assistant/sessions/m/s/direction"
    private static let product = DirectionFilings.Choice(id: "lead-a", name: "Product")

    /// A store over `hub`, with what it shows and asks for recorded.
    private final class Probe {
        var toasts: [Toast] = []
        var treeReads = 0
        var reachable = true
    }

    private func store(_ hub: FakeHub, _ probe: Probe) -> DirectionFilings {
        DirectionFilings(env: DirectionFilings.Env(api: { hub.client }, reachable: { probe.reachable },
                                                   networkUp: { true }, toast: { probe.toasts.append($0) },
                                                   rereadTree: { probe.treeReads += 1 }))
    }

    /// Waits (3 s at most) until `done`.
    private func until(_ what: String, _ done: () -> Bool, sourceLocation: SourceLocation = #_sourceLocation) async throws {
        for _ in 0..<300 {
            if done() { return }
            try await Task.sleep(for: .milliseconds(10))
        }
        #expect(done(), "timed out waiting: \(what)", sourceLocation: sourceLocation)
    }

    private func body(_ r: LocalRequest) -> String { String(decoding: r.body ?? Data(), as: UTF8.self) }

    private func refusal(_ status: Int, _ code: String, _ words: String) -> APIError {
        APIError(status: status, message: words, fields: ["code": code, "error": words])
    }

    // MARK: Bodies and Undo

    @Test func theBodyNamesTheDirectionOrOnItsOwnAndNullIsWrittenOut() throws {
        let encode = { (t: DirectionTarget) in String(decoding: try JSON.encoder.encode(DirectionFilingBody(target: t)), as: UTF8.self) }
        #expect(try encode(.direction("lead-a")) == #"{"direction":"lead-a"}"#)
        #expect(try encode(.own) == #"{"direction":""}"#)
        #expect(try encode(.remove) == #"{"direction":null}"#, "a body without the key would mean on its own")
        #expect(DirectionTarget(answer: nil) == .remove && DirectionTarget(answer: "") == .own)
        #expect(DirectionTarget(answer: "lead-b") == .direction("lead-b"))
    }

    /// The Undo of a first filing (`previous: null`) sends `{"direction":null}`, which gives back exactly the
    /// placement before it, a chat its leader opened included; nothing else is sent.
    @Test func undoOfAFirstFilingSendsNull() async throws {
        let hub = FakeHub(), probe = Probe()
        hub.on("PUT", Self.path) { req in
            String(decoding: req.body ?? Data(), as: UTF8.self).contains("null")
                ? Data(#"{"direction":null,"previous":"lead-a","changed":true}"#.utf8)
                : Data(#"{"direction":"lead-a","previous":null,"changed":true}"#.utf8)
        }
        let s = store(hub, probe)
        s.move(Self.key, title: "Trip", state: .idle, to: Self.product, from: nil)
        #expect(s.drawnUnder(Self.key) == "lead-a", "drawn there at once, before the answer")
        try await until("the answer's toast") { !probe.toasts.isEmpty }
        let toast = try #require(probe.toasts.first)
        #expect(toast.message == "已移到「Product」下")
        let undo = try #require(toast.action, "a change offers Undo")
        undo.run()
        #expect(s.drawnUnder(Self.key) == "", "drawn back where it was (no direction)")
        try await until("the Undo's write") { hub.sent("PUT", Self.path).count == 2 }
        #expect(body(hub.sent("PUT", Self.path)[1]) == #"{"direction":null}"#)
        try await until("settled") { s.pending.isEmpty }
        #expect(probe.toasts.count == 1, "the Undo itself offers no second Undo")
    }

    /// From On Its Own and from another direction, Undo sends `previous` as it is.
    @Test func undoSendsAStringPreviousAsItIs() async throws {
        for previous in ["", "lead-b"] {
            let hub = FakeHub(), probe = Probe()
            hub.on("PUT", Self.path, json: #"{"direction":"lead-a","previous":"\#(previous)","changed":true}"#)
            let s = store(hub, probe)
            s.move(Self.key, title: "Trip", state: .working, to: Self.product, from: previous.isEmpty ? nil : previous)
            try await until("the answer") { probe.toasts.first?.action != nil }
            probe.toasts.first?.action?.run()
            #expect(s.drawnUnder(Self.key) == previous)
            try await until("the Undo's write") { hub.sent("PUT", Self.path).count == 2 }
            #expect(body(hub.sent("PUT", Self.path)[1]) == #"{"direction":"\#(previous)"}"#)
        }
    }

    /// The same place again answers `changed: false`: no Undo.
    @Test func anUnchangedAnswerOffersNoUndo() async throws {
        let hub = FakeHub(), probe = Probe()
        hub.on("PUT", Self.path, json: #"{"direction":"lead-a","previous":"lead-a","changed":false}"#)
        let s = store(hub, probe)
        s.move(Self.key, title: "Trip", state: .idle, to: Self.product, from: "lead-a")
        try await until("the answer") { !probe.toasts.isEmpty }
        #expect(probe.toasts.first?.action == nil)
        try await until("the tree read") { probe.treeReads == 1 }
    }

    // MARK: Refusals, out of reach, a lost answer

    /// Every refusal puts the row back, shows the hub's sentence and retries nothing; a 404 with one of the routes'
    /// codes is a refusal that keeps the entries.
    @Test func eachRefusalLeavesTheRowAndSaysTheHubsSentence() async throws {
        let cases: [(Int, String)] = [(403, "full_access_required"), (403, "forbidden"), (404, "session_not_found"),
                                      (409, "direction_supervised_task"), (409, "direction_full"),
                                      (503, "direction_unavailable")]
        for (status, code) in cases {
            let hub = FakeHub(), probe = Probe()
            let words = "The hub says no: \(code)"
            hub.on("PUT", Self.path) { [self] _ in throw refusal(status, code, words) }
            let s = store(hub, probe)
            s.move(Self.key, title: "Trip", state: .idle, to: Self.product, from: nil)
            try await until("\(code) settled") { s.pending.isEmpty }
            #expect(s.drawnUnder(Self.key) == nil, "\(code): back where it was")
            #expect(probe.toasts.first?.message.hasPrefix(words) == true, "\(code): \(probe.toasts.first?.message ?? "none")")
            #expect(probe.toasts.first?.style == .error && probe.toasts.first?.action == nil)
            #expect(hub.sent("PUT", Self.path).count == 1, "\(code): sent once, never again")
            #expect(!s.routesMissing, "\(code) is a refusal, not a missing route")
            #expect(probe.treeReads == 0)
        }
    }

    /// With the hub out of reach nothing is sent and nothing moves; the reason is said.
    @Test func outOfReachSendsNothing() async throws {
        let hub = FakeHub(), probe = Probe()
        probe.reachable = false
        let s = store(hub, probe)
        s.move(Self.key, title: "Trip", state: .idle, to: Self.product, from: nil)
        #expect(hub.requests.isEmpty)
        #expect(s.pending.isEmpty)
        #expect(probe.toasts.first?.message == "连不上 hub，刚才的操作没有生效")
    }

    /// Connections that never reached the hub put it back; answers lost after sending read the tree again, send
    /// nothing again and offer no Undo.
    @Test func aLostAnswerReadsAgainAndOffersNoUndo() async throws {
        let lost: [Error] = [URLError(.timedOut), URLError(.networkConnectionLost),
                             APIError(status: 502, message: "cut off", fields: ["code": "assistant_in_doubt", "delivered": "unknown"])]
        for error in lost {
            let hub = FakeHub(), probe = Probe()
            hub.on("PUT", Self.path) { _ in throw error }
            let s = store(hub, probe)
            s.move(Self.key, title: "Trip", state: .idle, to: Self.product, from: nil)
            try await until("settled") { s.pending.isEmpty && !probe.toasts.isEmpty }
            #expect(probe.toasts.first?.action == nil, "\(error): no Undo")
            #expect(probe.toasts.first?.message == "没能确认是否移好了，已重新读取它现在的位置。")
            #expect(probe.treeReads == 1, "\(error): the tree is read again")
            #expect(hub.sent("PUT", Self.path).count == 1, "\(error): never sent again")
        }
        let hub = FakeHub(), probe = Probe()
        hub.on("PUT", Self.path) { _ in throw URLError(.cannotConnectToHost) }
        let s = store(hub, probe)
        s.move(Self.key, title: "Trip", state: .idle, to: Self.product, from: nil)
        try await until("settled") { s.pending.isEmpty && !probe.toasts.isEmpty }
        #expect(probe.treeReads == 0, "never sent: nothing to read again")
        #expect(DirectionFilings.failure(URLError(.cannotConnectToHost)) == .notSent)
    }

    /// 404 or 405 without one of the routes' codes: an older manager; every entry hides until the next hello.
    @Test func missingRoutesHideTheEntriesUntilTheNextHello() async throws {
        let hub = FakeHub(), probe = Probe()
        hub.on("PUT", Self.path) { [self] _ in throw refusal(404, "not_found", "not found") }
        let s = store(hub, probe)
        s.move(Self.key, title: "Trip", state: .idle, to: Self.product, from: nil)
        try await until("settled") { s.pending.isEmpty }
        #expect(s.routesMissing)
        let tree = DirectionTree(directions: [DirectionTree.Direction(id: "lead-a", name: "Product")])
        let features: Set<String> = [DirectionRoutes.feature]
        #expect(!DirectionFilings.shows(features: features, tree: tree, routesMissing: s.routesMissing))
        s.hello()
        #expect(DirectionFilings.shows(features: features, tree: tree, routesMissing: s.routesMissing))
        #expect(DirectionRoutes.missing(APIError(status: 405, message: "")))
        #expect(!DirectionRoutes.missing(refusal(404, "direction_not_found", "gone")))
        #expect(!DirectionRoutes.missing(refusal(404, "machine_not_found", "gone")))
    }

    // MARK: Gate, followers and rows

    /// Nothing is drawn without the feature, without the default manager's tree (another manager's page), or once the
    /// routes were answered as missing.
    @Test func theGate() {
        let tree = DirectionTree(directions: [DirectionTree.Direction(id: "lead-a")])
        #expect(DirectionFilings.shows(features: [DirectionRoutes.feature], tree: tree, routesMissing: false))
        #expect(!DirectionFilings.shows(features: [], tree: tree, routesMissing: false))
        #expect(!DirectionFilings.shows(features: [DirectionRoutes.feature], tree: nil, routesMissing: false))
        #expect(!DirectionFilings.shows(features: [DirectionRoutes.feature], tree: tree, routesMissing: true))
    }

    /// An Agent of an open supervised to-do, and the sessions under it, follow their task: no Move To….
    @Test func followersAndTheSessionsUnderThem() throws {
        let todo = try JSON.decoder.decode(TodoItem.self, from: Data(#"""
        {"id":"t","title":"Ship","agents":[{"machine_id":"m","session_id":"a"},{"machine_id":"m","session_id":"gone","released_at":"2026-11-20T00:00:00Z"}],
         "supervision":{"supervisor":"lead-a"}}
        """#.utf8))
        let own = try JSON.decoder.decode(TodoItem.self, from: Data(#"{"id":"o","title":"Mine","agents":[{"machine_id":"m","session_id":"b"}]}"#.utf8))
        let followers = TodoStore.taskFollowers([todo, own])
        #expect(followers == ["m/a"], "a released Agent and the owner's own unsupervised to-do do not follow")
        let parents = ["child": "a", "grandchild": "child"]
        let parent = { (k: SessionKey) in parents[k.sessionID] }
        #expect(DirectionFilings.follows(SessionKey(machineID: "m", sessionID: "a"), followers: followers, parent: parent))
        #expect(DirectionFilings.follows(SessionKey(machineID: "m", sessionID: "grandchild"), followers: followers, parent: parent))
        #expect(DirectionFilings.follows(SessionKey(machineID: "m", sessionID: "a@sub"), followers: followers, parent: { _ in nil }))
        #expect(!DirectionFilings.follows(SessionKey(machineID: "m", sessionID: "b"), followers: followers, parent: parent))
        #expect(!DirectionFilings.follows(SessionKey(machineID: "other", sessionID: "a"), followers: followers, parent: parent))
    }

    /// While a move is out, the session leaves the rows of the direction it left and joins the one it goes to, marked
    /// as moving; the sidebar's rows (`live`) take only a working or waiting one, the page's every one.
    @Test func rowsFollowTheMovesOut() async throws {
        let hub = FakeHub(), probe = Probe()
        hub.onAsync("PUT", Self.path) { _ in
            try await Task.sleep(for: .seconds(30))
            return Data("{}".utf8)
        }
        let s = store(hub, probe)
        let listed = [DirectionTree.FiledSession(machineID: "m", sessionID: "s", title: "Trip", state: "working"),
                      DirectionTree.FiledSession(machineID: "m", sessionID: "x", title: "Other", state: "needs_you")]
        #expect(s.rows(listed, in: "lead-b", group: "", live: true).map(\.id) == ["m/s", "m/x"])
        s.move(Self.key, title: "Trip", state: .working, to: Self.product, from: "lead-b")
        #expect(s.rows(listed, in: "lead-b", group: "", live: true).map(\.id) == ["m/x"], "gone from where it was")
        let there = s.rows([], in: "lead-a", group: "", live: true)
        #expect(there.map(\.id) == ["m/s"] && there.first?.moving == true, "drawn there at once, marked moving")
        let quiet = SessionKey(machineID: "m", sessionID: "q")
        s.move(quiet, title: "Quiet", state: .idle, to: Self.product, from: nil)
        #expect(s.rows([], in: "lead-a", group: "", live: true).map(\.id) == ["m/s"], "the tree lists only live ones")
        #expect(s.rows([], in: "lead-a", group: "", live: false).map(\.id) == ["m/q", "m/s"], "the page lists every one")
    }

    // MARK: The page's words

    /// Each standing says where the session is; none says "not found" or "archived", and a value this app does not
    /// know says nothing.
    @Test func eachStandingSaysWhereItIs() {
        let at = Date(timeIntervalSinceNow: -3 * 86_400)
        func word(_ state: String, live: AgentState? = nil) -> String? {
            DirectionPageLogic.standing(DirectionTree.FiledSession(machineID: "m", sessionID: "s", at: at, state: state),
                                        live: live).map(DirectionPageLogic.words)
        }
        #expect(word("offline") == "未运行", "stopped on a connected computer: Not Running")
        #expect(word("computer_offline") == "离线")
        #expect(word("list_unknown") == "还不知道")
        #expect(word("computer_removed") == "电脑已移除")
        #expect(word("not_listed")?.hasSuffix("放进来") == true, "only when it was added: \(word("not_listed") ?? "nil")")
        #expect(word("idle", live: .working) == AgentState.working.label, "the live state follows it after the read")
        #expect(word("archived_somehow") == nil)
        #expect(DirectionPageLogic.shown(Array(0..<9), all: false) == [0, 1, 2, 3, 4])
        #expect(DirectionPageLogic.shown(Array(0..<9), all: true).count == 9)
    }

    /// For the same tree the page's "N need you" is the sidebar row's.
    @Test func thePagesCountIsTheSidebarRows() throws {
        let tree = try JSON.decoder.decode(DirectionTree.self, from: Data(#"""
        {"directions":[{"id":"lead-a","name":"Product","counts":{"needs_you":2,"working":1},"tasks":[],
          "sessions":[{"machine_id":"m","session_id":"f","state":"needs_you"}]}]}
        """#.utf8))
        let sidebarRow = tree.leader("lead-a")?.counts.label
        #expect(DirectionPageLogic.needsYou(tree, "lead-a") == sidebarRow)
        #expect(sidebarRow == "等你 2", "a filed session waiting on you does not change the count (#5974)")
    }
}
