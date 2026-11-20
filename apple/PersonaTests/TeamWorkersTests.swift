import Foundation
import PersonaWire
import Testing
@testable import Persona

@MainActor struct TeamWorkersTests {
    private let lead = SessionKey(group: "home", machineID: "computer", sessionID: "lead")

    private func row(_ id: String, parent: String = "lead", kind: String = "worker", group: String = "home",
                     machine: String = "computer", state: String = "idle", pane: Int = 1,
                     throughSide: String? = nil) throws -> Session {
        var fields: [String: Any] = [
            "machine_id": machine, "session_id": id, "app_hub_group": group,
            "title": "Worker", "cwd": "/work", "status": state, "state": state,
            "folded_under": parent, "folded_as": kind, "pane_index": pane,
        ]
        if let throughSide { fields["folded_side"] = throughSide }
        return try JSON.decoder.decode(Session.self, from: JSONSerialization.data(withJSONObject: fields))
    }

    @Test func showsOnlyTheLeadsRunningWorkersInPaneOrder() throws {
        let early = try row("second-name", pane: 0)
        let late = try row("first-name", pane: 4)
        let rows = try [late, row("side", kind: "side"), row("child", kind: "child"), early,
                        row("away", state: "offline"), row("other-parent", parent: "another"),
                        row("second-name", group: "office"), row("second-name", machine: "another"),
                        row("nested", throughSide: "side")]
        #expect(TeamWorkers.rows(rows, of: lead).map(\.key) == [early.key, late.key])
        #expect(TeamWorkers.lead(of: early) == lead)
        #expect(TeamWorkers.lead(of: try row("self", parent: "self")) == nil)
    }

    @Test func countsTheLeadOnceAndPreservesItsOwnChatState() throws {
        var parent = try row("lead", parent: "", kind: "")
        parent.children = SessionChildren(total: 3, working: 1, needsYou: 1, workers: 3)
        let worker = try row("worker")
        let ordinary = try row("child", kind: "subagent")
        let legacy = try row("solo", parent: "", kind: "")
        let malformed = try row("self", parent: "self")
        let listed = TeamWorkers.listed([parent, worker, ordinary, legacy, malformed])
        #expect(listed.map(\.key) == [parent.key, ordinary.key, legacy.key, malformed.key])
        #expect(listed.first?.state == .needsYou)
        #expect(parent.state == .idle)
        #expect(TeamWorkers.listed([worker]).isEmpty)
        #expect(TeamWorkers.listed([legacy, ordinary]).map(\.state) == [.idle, .idle])
        parent.children?.needsYou = 0
        #expect(TeamWorkers.listed([parent]).first?.state == .working)
        parent.state = .unread
        #expect(TeamWorkers.listed([parent]).first?.state == .unread)
    }

    @Test func selectsTheRequestedWorkerThenTheLastPickThenTheFirstWaitingWorker() throws {
        let first = try row("first")
        let waiting = try row("waiting", state: "needs_you")
        let picked = try row("picked")
        let missing = SessionKey(group: "office", machineID: "computer", sessionID: first.sessionID)
        let rows = [first, waiting, picked]
        #expect(TeamWorkers.shown(in: rows, requested: first.key, last: picked.key)?.key == first.key)
        #expect(TeamWorkers.shown(in: rows, requested: missing, last: picked.key)?.key == picked.key)
        #expect(TeamWorkers.shown(in: rows, requested: missing, last: nil)?.key == waiting.key)
        #expect(TeamWorkers.shown(in: [first], requested: nil, last: nil)?.key == first.key)
        #expect(TeamWorkers.shown(in: [], requested: picked.key, last: nil) == nil)
    }

    @Test func phonesOpenOnRequestAndWideLayoutsRespectTheLeadsLastDismissal() {
        #expect(TeamWorkers.reveal(count: 2, previous: nil, wide: false, manuallyClosed: false, requested: false) == nil)
        #expect(TeamWorkers.reveal(count: 2, previous: nil, wide: false, manuallyClosed: true, requested: true) == .open)
        #expect(TeamWorkers.reveal(count: 2, previous: nil, wide: true, manuallyClosed: false, requested: false) == .open)
        #expect(TeamWorkers.reveal(count: 2, previous: nil, wide: true, manuallyClosed: true, requested: false) == nil)
        #expect(TeamWorkers.reveal(count: 3, previous: 2, wide: true, manuallyClosed: true, requested: false) == nil)
        #expect(TeamWorkers.reveal(count: 2, previous: 0, wide: true, manuallyClosed: true, requested: false) == .open)
        #expect(TeamWorkers.reveal(count: 0, previous: 2, wide: false, manuallyClosed: false, requested: false) == .close)
        #expect(TeamWorkers.reveal(count: 0, previous: nil, wide: true, manuallyClosed: false, requested: false) == nil)
    }
}
