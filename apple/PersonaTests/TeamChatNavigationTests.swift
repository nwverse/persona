import Foundation
import PersonaWire
import Testing
@testable import Persona

@MainActor struct TeamChatNavigationTests {
    private func freshModel() -> AppModel {
        AppModel(stateFolder: FileManager.default.temporaryDirectory.appendingPathComponent("team-\(UUID())"))
    }

    private func row(_ id: String, parent: String? = nil, group: String = "", children: [String: Int]? = nil) throws -> Session {
        var fields: [String: Any] = ["machine_id": "server", "session_id": id, "title": id, "cwd": "/work",
                                     "status": "working", "state": "working", "app_hub_group": group]
        if let parent { fields["folded_under"] = parent; fields["folded_as"] = "worker" }
        if let children { fields["children"] = children }
        return try JSON.decoder.decode(Session.self, from: JSONSerialization.data(withJSONObject: fields))
    }

    @Test func aWorkerContributesToTheLeadInsteadOfTheListsAndTheChildCards() throws {
        let model = freshModel()
        let lead = try row("lead", children: ["total": 1, "workers": 1, "needs_you": 1])
        let worker = try row("review", parent: "lead")
        let peer = try row("independent")
        model.applySessions([lead.machineRef: [lead, worker, peer]])
        #expect(Set(model.allSessions.map(\.key)) == [lead.key, peer.key])
        #expect(model.session(worker.key)?.key == worker.key)
        #expect(model.childRows(lead.key).isEmpty)
    }

    @Test func aWorkerLinkOpensItsOwnLeadsTeamAndLeavesItsDraftWithTheWorker() throws {
        let model = freshModel()
        let lead = try row("lead", children: ["total": 1, "workers": 1])
        let worker = try row("review", parent: "lead")
        let other = try row("lead", group: "second")
        model.applySessions([lead.machineRef: [lead, worker], other.machineRef: [other]])
        model.drafts[worker.key] = "Review notes"
        model.open(worker.key)
        #expect(model.openChat == lead.key)
        #expect(model.revealChild == nil)
        #expect(model.drafts[worker.key] == "Review notes")
        #expect(model.drafts[lead.key] == nil)
    }

    @Test func theDisplayedWorkerOwnsItsRequestAndTheOtherWorkersStayOnTheirLead() throws {
        let model = freshModel()
        let lead = try row("lead", children: ["total": 2, "workers": 2, "needs_you": 2])
        let review = try row("review", parent: "lead")
        let tests = try row("tests", parent: "lead")
        let other = try row("lead", group: "second")
        model.applySessions([lead.machineRef: [lead, review, tests], other.machineRef: [other]])
        func question(_ id: String, _ worker: Session) throws -> Interaction {
            let fields: [String: Any] = ["id": id, "machine_id": "server", "session_id": worker.sessionID,
                                        "kind": "question", "folded_as": "worker", "folded_under": "lead",
                                        "session_title": "Review", "questions": [["question": "Continue?"]]]
            return try JSON.decoder.decode(Interaction.self, from: JSONSerialization.data(withJSONObject: fields))
        }
        let a = try question("review-ask", review), b = try question("tests-ask", tests)
        model.replaceInteractions("", [a, b])
        #expect(Set(model.interactions(for: lead.key).map(\.id)) == [a.id, b.id])
        #expect(model.interactions(for: other.key).isEmpty)
        let waiting = AgentsNeedsYou.waiting(model.waitingRows([lead, review, tests], reading: model.session))
        #expect(waiting.map(\.key) == [lead.key])
        let needsYou = AgentsNeedsYou.rows(cards: [a, b], waiting: waiting)
        #expect(needsYou.map(\.key) == [lead.key])
        #expect(Set(needsYou.first?.cards.map(\.id) ?? []) == [a.id, b.id])
        #expect(model.interactions(for: lead.key, showingWorker: review.key).map(\.id) == [b.id])
        #expect(model.interactions(for: review.key).map(\.id) == [a.id])
        #expect(model.teamWorker(of: a, in: lead.key) == review.key)
        #expect(model.teamWorker(of: a, in: other.key) == nil)
        #expect(a.placeLine(machine: nil) == String(localized: "From worker · \("Review")"))
    }

    @Test func aWorkerTargetedBeforeTheFirstFrameLandsInTheRightTeam() throws {
        let model = freshModel()
        let other = try row("lead", group: "second")
        let lead = try row("lead", children: ["total": 1, "workers": 1])
        let worker = try row("review", parent: "lead")
        model.open(worker.key)
        #expect(model.openChat == worker.key)
        model.applySessions([other.machineRef: [other]])
        #expect(model.openChat == worker.key)
        // The navigation stack takes the destination as soon as it pushes and registers the visible chat.
        model.openChat = nil
        model.show(worker.key, by: UUID())
        model.applySessions([lead.machineRef: [lead, worker]])
        #expect(model.openChat == lead.key)
        #expect(model.teamLanding == TeamLanding(lead: lead.key, worker: worker.key))
        #expect(model.team(of: other.key).isEmpty)
        #expect(model.team(of: lead.key).map(\.key) == [worker.key])
        let peer = try row("peer")
        model.open(peer.key)
        #expect(model.teamLanding == nil)
    }

    @Test func aKnownZeroClosesTheTeamEvenBeforeItsPagedWorkerIsRemoved() throws {
        let model = freshModel()
        let worker = try row("worker", parent: "lead")
        model.childIndex.hold(worker)
        let lead = try row("lead", children: ["total": 0, "workers": 0])
        model.applySessions([lead.machineRef: [lead]])
        #expect(!model.team(of: lead.key).isEmpty)
        #expect(model.teamCount(lead.key) == 0)
    }

    @Test func aListedWorkerCanOpenBeforeItsLeadArrives() throws {
        let model = freshModel()
        let lead = try row("lead", children: ["total": 1, "workers": 1])
        let worker = try row("review", parent: "lead")
        model.applySessions([worker.machineRef: [worker]])
        model.open(worker.key)
        model.openChat = nil
        model.show(worker.key, by: UUID())
        model.applySessions([lead.machineRef: [lead, worker]])
        #expect(model.openChat == lead.key)
        #expect(model.teamLanding == TeamLanding(lead: lead.key, worker: worker.key))
    }

    @Test func forgettingTheLeadEvictsOnlyThatHubsPagedWorkers() throws {
        let children = ChildSessionIndex()
        let old = try row("worker", parent: "lead")
        let other = try row("worker", parent: "lead", group: "second")
        children.hold(old)
        children.hold(other)
        children.forget(old.group)
        #expect(children.row(old.key) == nil)
        #expect(children.row(other.key)?.key == other.key)
    }

    @Test func cachedClosedChoicesAreQualifiedAndForgettingOneHubKeepsTheOther() throws {
        let name = "TeamChatNavigationTests-\(UUID())"
        let defaults = try #require(UserDefaults(suiteName: name))
        defer { defaults.removePersistentDomain(forName: name) }
        let a = try row("lead").key, b = try row("lead", group: "second").key
        TeamPanelMemory.setClosed(true, for: a, defaults: defaults)
        #expect(TeamPanelMemory.closed(a, defaults: defaults))
        #expect(!TeamPanelMemory.closed(b, defaults: defaults))
        TeamPanelMemory.setClosed(true, for: b, defaults: defaults)
        TeamPanelMemory.forget(group: a.group, defaults: defaults)
        #expect(!TeamPanelMemory.closed(a, defaults: defaults))
        #expect(TeamPanelMemory.closed(b, defaults: defaults))
    }
}
