import Foundation
import Testing
@testable import Persona

/// docs/assistant/inbox.md「Corrections and learned rules」: ignored items are their own section apart from Done; a move
/// shows at once with "Moved by you"; the hub's answer to a move decodes with its rule flag; Ignore and the move post
/// what the hub reads.
@MainActor
struct InboxReclassTests {
    private func item(_ id: String, tier: String? = nil, status: String? = nil, doneBy: String? = nil) -> InboxItem {
        var it = InboxItem(id: id, source: "lark", kind: "group", account: "feishu", accountName: "feishu", title: id, preview: "", count: 1)
        it.tier = tier
        it.status = status
        if let doneBy {
            it.doneAt = Date(timeIntervalSince1970: 100)
            it.doneBy = doneBy
        }
        return it
    }

    @Test func ignoredItemsAreNotDone() {
        let p = InboxProjection(items: [item("open"), item("done", doneBy: "user"), item("ignored", doneBy: "ignored")], filter: .init())
        #expect(p.doneItems.map(\.id) == ["done"])
        #expect(p.ignoredItems.map(\.id) == ["ignored"])
        #expect(p.openItems.map(\.id) == ["open"])
    }

    @Test func aMoveShowsAtOnce() {
        var urgent = item("a", tier: "action", status: "action")
        urgent.urgent = true
        let moved = InboxStore.moved(urgent, to: "fyi")
        #expect(moved.triage == .fyi)
        #expect(moved.userMarked)
        #expect(moved.urgent == false)
        // the AI on it keeps its section (In Progress) until the hub answers
        var handed = item("b", tier: "action", status: "working")
        handed.progress = .init(kind: "handoff")
        #expect(InboxStore.moved(handed, to: "fyi").triage == .working)
    }

    @Test func theMoveAnswerDecodes() throws {
        let json = #"{"id":"a","source":"lark","kind":"group","account":"feishu","account_name":"Lark","title":"t","preview":"","count":1,"tier":"fyi","status":"fyi","user_tier":"fyi","progress":{"kind":"handoff"},"rule_drafting":true}"#
        let answer = try JSONDecoder().decode(InboxTierAnswer.self, from: Data(json.utf8))
        #expect(answer.ruleDrafting)
        #expect(answer.item.userTier == "fyi")
        #expect(answer.item.progress?.kind == "handoff")
    }

    @Test func movesAndIgnoresPostWhatTheHubReads() async throws {
        let hub = FakeHub()
        var tierBody: [String: String] = [:]
        var doneBody: [String: Bool] = [:]
        hub.on("POST", "/api/v1/inbox/a/tier") { req in
            tierBody = try JSONDecoder().decode([String: String].self, from: req.body ?? Data())
            return Data(#"{"id":"a","source":"lark","kind":"group","account":"feishu","account_name":"Lark","title":"t","preview":"","count":1}"#.utf8)
        }
        hub.on("POST", "/api/v1/inbox/a/done") { req in
            doneBody = try JSONDecoder().decode([String: Bool].self, from: req.body ?? Data())
            return Data("{}".utf8)
        }
        let answer = try await hub.client.setInboxTier("a", "fyi")
        #expect(answer.ruleDrafting == false)
        #expect(tierBody == ["tier": "fyi"])
        try await hub.client.ignoreInbox("a")
        #expect(doneBody == ["done": true, "ignore": true])
    }
}
