import Foundation
import Testing
@testable import Persona

/// Hand to AI (docs/assistant/inbox.md "Item actions", #1194): Persona's main chat is the default and goes through the
/// item's hand-off, Also Mark as Done follows the target until the user sets it, a retry of the same hand-off keeps its
/// client id and text only while it may have gone, and an older hub gets the text as a plain message.
@MainActor
struct InboxHandOffModelTests {
    private final class Calls {
        var handOffs: [(id: String, text: String, clientID: UUID)] = []
        var opened: [(key: SessionKey, text: String, clientID: UUID)] = []
        var asked: [String] = []
        var started: [String] = []
        var agents: [(key: SessionKey, text: String)] = []
        var done = 0
        /// What the hand-off throws first, in order; then it answers.
        var failures: [Error] = []
        var askGoes = true
        /// The item's preview from the next load on, as when a new message came in.
        var preview: String?
    }

    private static let main = SessionKey(machineID: "server", sessionID: "main-1")
    private static let item = """
    {"id":"lark:f1:11","source":"lark","kind":"dm","account":"feishu","account_name":"飞书","title":"Sample Contact","preview":"合入了吗",
     "count":1,"lark":{"feed_id":"f1","is_group":false}}
    """

    private func model(_ calls: Calls, hub: APIClient? = nil) -> InboxHandOffModel {
        InboxHandOffModel(itemID: "lark:f1:11", deps: .init(
            load: { _ in
                var it = try JSON.decoder.decode(InboxItem.self, from: Data(Self.item.utf8))
                if let p = calls.preview { it.preview = p }
                return it
            },
            handOff: { id, text, clientID in
                calls.handOffs.append((id, text, clientID))
                if let hub { return try await hub.inboxHandoff(id, text: text, clientID: clientID).session }
                if !calls.failures.isEmpty { throw calls.failures.removeFirst() }
                return Self.main
            },
            openMain: { calls.opened.append(($0, $1, $2)) },
            plainToMain: {
                calls.asked.append($0)
                if !calls.askGoes { throw APIError(status: 502, message: "Persona is offline") }
            },
            startNew: { calls.started.append($0) },
            sendToAgent: { calls.agents.append(($0, $1)) },
            markDone: { calls.done += 1 }))
    }

    @Test func theMainChatIsTheDefaultAndLeavesTheItemOpen() async throws {
        let calls = Calls(), m = model(calls)
        #expect(m.target == .main)
        #expect(!m.markDone)
        let went = await m.send(ask: "帮我查一下")
        #expect(went)
        let h = try #require(calls.handOffs.first)
        #expect(calls.handOffs.count == 1 && h.id == "lark:f1:11")
        let parsed = ContextRef.parse(h.text)
        #expect(parsed.refs.count == 1 && parsed.rest == "帮我查一下")
        // The conversation opens on the message as sent, under the id its transcript copy will carry.
        let o = try #require(calls.opened.first)
        #expect(o.key == Self.main && o.text == h.text && o.clientID == h.clientID)
        #expect(calls.done == 0 && calls.started.isEmpty && calls.asked.isEmpty && calls.agents.isEmpty)
    }

    @Test func markAsDoneFollowsTheTargetUntilTheUserSetsIt() async {
        let m = model(Calls())
        m.target = .newChat
        #expect(m.markDone)
        m.target = .agent(SessionKey(machineID: "m", sessionID: "a"))
        #expect(m.markDone)
        m.target = .main
        #expect(!m.markDone)
        m.setMarkDone(true)
        m.target = .newChat
        m.target = .main
        #expect(m.markDone) // the user's choice stays

        let calls = Calls(), checked = model(calls)
        checked.setMarkDone(true)
        _ = await checked.send(ask: "a")
        #expect(calls.done == 1 && calls.opened.count == 1)
    }

    @Test func aNewChatAndAnAgentGetTheTextAndFileTheItem() async throws {
        let calls = Calls(), m = model(calls)
        m.target = .newChat
        let started = await m.send(ask: "a")
        #expect(started && calls.started.count == 1 && calls.done == 1 && calls.handOffs.isEmpty)
        let agent = SessionKey(machineID: "m", sessionID: "a")
        m.target = .agent(agent)
        let sent = await m.send(ask: "b")
        let a = try #require(calls.agents.first)
        #expect(sent && a.key == agent && ContextRef.parse(a.text).rest == "b" && calls.done == 2 && calls.handOffs.isEmpty)
    }

    @Test func aRetryKeepsTheClientIDOnlyWhileTheHandOffMayHaveGone() async {
        let calls = Calls(), m = model(calls)
        calls.failures = [APIError(status: 502, message: "cut off", fields: ["code": "assistant_in_doubt"]),
                          URLError(.timedOut),
                          APIError(status: 503, message: "offline", fields: ["code": "assistant_offline"]),
                          APIError(status: 502, message: "no answer", fields: ["code": "timeout"]),
                          APIError(status: 502, message: "typing broke off", fields: ["code": "internal", "delivered": "unknown"]),
                          APIError(status: 502, message: "typed, then failed", fields: ["code": "internal", "delivered": "yes"]),
                          APIError(status: 502, message: "node gone", fields: ["code": "node_unreachable"]),
                          APIError(status: 202, message: "unknown", fields: ["code": OpReceipt.unknownCode]),
                          APIError(status: 202, message: "pending", fields: ["code": OpReceipt.pendingCode]),
                          APIError(status: 409, message: "busy", fields: ["code": "input_draft"])]
        var outcomes: [Bool] = []
        for _ in 0..<11 { outcomes.append(await m.send(ask: "a")) }
        #expect(outcomes == Array(repeating: false, count: 10) + [true])
        let ids = calls.handOffs.map(\.clientID)
        #expect(ids.count == 11)
        #expect(Set(ids[0...9]).count == 1) // anything that may have gone, or that the hub kept no answer for: the same hand-off asked again
        #expect(ids[10] != ids[9])          // after an answer that was final: a new one
        #expect(calls.opened.count == 1 && calls.opened.first?.clientID == ids.last)

        // A refusal that says nothing was typed is final too: the hub gives it back for that id.
        calls.failures = [APIError(status: 503, message: "node offline", fields: ["code": "node_unreachable", "delivered": "none"])]
        _ = await m.send(ask: "a")
        _ = await m.send(ask: "a")
        let refused = calls.handOffs.map(\.clientID).suffix(2)
        #expect(Set(refused).count == 2)

        // Another ask is another hand-off, and so is the same ask again after one went.
        calls.failures = [APIError(status: 502, message: "cut off", fields: ["code": "assistant_in_doubt"])]
        _ = await m.send(ask: "b")
        _ = await m.send(ask: "a")
        _ = await m.send(ask: "a")
        let more = calls.handOffs.map(\.clientID).suffix(3)
        #expect(Set(more).count == 3 && !more.contains(ids[10]))
    }

    /// A new message on the item between the first try and the retry (likely after a 120 s timeout) changes the text the
    /// item builds: the retry still sends the first text under the first client id, which the hub and the manager know.
    @Test func aRetrySendsTheFirstTextAfterTheItemChanged() async throws {
        let calls = Calls(), m = model(calls)
        calls.failures = [APIError(status: 502, message: "no answer", fields: ["code": "timeout"])]
        _ = await m.send(ask: "a")
        calls.preview = "又来了一条"
        let went = await m.send(ask: "a")
        #expect(went && calls.handOffs.count == 2)
        let first = try #require(calls.handOffs.first), retry = try #require(calls.handOffs.last)
        #expect(retry.clientID == first.clientID && retry.text == first.text)
        #expect(calls.opened.first?.text == first.text && calls.opened.first?.clientID == first.clientID)

        // Once it went, the next hand-off is new and carries the item as it is now.
        _ = await m.send(ask: "a")
        let next = try #require(calls.handOffs.last)
        #expect(next.clientID != first.clientID && next.text.contains("又来了一条") && next.text != first.text)
    }

    @Test func aFailureSaysWhyAndKeepsTheSheet() async {
        let calls = Calls(), m = model(calls)
        calls.failures = [APIError(status: 409, message: "The assistant can't run on a work machine", fields: ["code": "work_machine"])]
        let went = await m.send(ask: "a")
        #expect(!went && m.error == "The assistant can't run on a work machine" && !m.sending && calls.opened.isEmpty && calls.done == 0)
        _ = await m.send(ask: "a")
        #expect(m.error == nil)
    }

    /// An answer that leaves open whether the hand-off went (the node's `delivered: unknown`, a timeout behind the hub or
    /// here, the hub's unknown receipt, the connection dropping after the request went out) says to look before sending
    /// again; no connection at all says the hub is out of reach. Neither was said: those errors show nothing, and the
    /// sheet just stopped spinning.
    @Test func aFailureThatMayHaveGoneSaysToLookFirst() async {
        let calls = Calls(), m = model(calls)
        calls.failures = [APIError(status: 502, message: "no answer", fields: ["code": "timeout"]),
                          APIError(status: 502, message: "typing broke off", fields: ["code": "internal", "delivered": "unknown"]),
                          APIError(status: 202, message: "unknown", fields: ["code": OpReceipt.unknownCode]),
                          URLError(.timedOut), URLError(.networkConnectionLost), URLError(.notConnectedToInternet)]
        let look = String(localized: "It may have reached the computer already. Check the chat before sending it again.")
        for _ in 0..<5 {
            let went = await m.send(ask: "a")
            #expect(!went && m.error == look)
        }
        let went = await m.send(ask: "a")
        #expect(!went && m.error == String(localized: "Can't reach the hub"))
        // Whatever says to look first keeps the hand-off's client id, so the hub and the manager send it once.
        #expect(calls.handOffs.count == 6 && Set(calls.handOffs.map(\.clientID)).count == 1)
    }

    @Test func anOlderHubGetsThePlainMessage() async {
        let calls = Calls(), m = model(calls)
        calls.failures = [APIError(status: 404, message: "404 page not found")]
        let went = await m.send(ask: "a")
        #expect(went && calls.asked == calls.handOffs.map(\.text) && calls.opened.isEmpty && calls.done == 0)

        // Checked, the item is filed once the message went, not when it didn't.
        let c = Calls(), checked = model(c)
        checked.setMarkDone(true)
        c.failures = [APIError(status: 405, message: "Method Not Allowed")]
        c.askGoes = false
        let wentAnyway = await checked.send(ask: "a")
        #expect(!wentAnyway && c.done == 0 && checked.error == "Persona is offline")
        c.failures = [APIError(status: 405, message: "Method Not Allowed")]
        c.askGoes = true
        _ = await checked.send(ask: "a")
        #expect(c.done == 1 && c.asked.count == 2)
    }

    @Test func theHandOffPostsTheTextAndClientID() async throws {
        let hub = FakeHub(), path = "/api/v1/inbox/lark:f1:11/handoff"
        hub.on("POST", path, json: """
        {"item":\(Self.item),"session":{"machine_id":"server","session_id":"main-1"}}
        """)
        let id = UUID()
        let answer = try await hub.client.inboxHandoff("lark:f1:11", text: "看看", clientID: id)
        #expect(answer.session == Self.main && answer.item?.id == "lark:f1:11")
        let sent = try #require(hub.sent("POST", path).first)
        let body = try JSON.decoder.decode([String: String].self, from: try #require(sent.body))
        #expect(body == ["text": "看看", "client_id": id.uuidString.lowercased()])
    }

    /// A retry while the hub still runs the first hand-off gets its receipt (202 pending), not an answer: the hand-off may
    /// still go, so the next try keeps the client id.
    @Test func aHandOffTheHubStillRunsKeepsItsClientID() async throws {
        let hub = FakeHub(), path = "/api/v1/inbox/lark:f1:11/handoff"
        let answers = Answers([#"{"state":"pending"}"#, #"{"session":{"machine_id":"server","session_id":"main-1"}}"#])
        hub.on("POST", path) { _ in Data(answers.next().utf8) }
        let m = model(Calls(), hub: hub.client)
        let first = await m.send(ask: "a")
        #expect(!first && m.error == String(localized: "It may have reached the computer already. Check the chat before sending it again."))
        let second = await m.send(ask: "a")
        #expect(second)
        let ids = try hub.sent("POST", path).map { try JSON.decoder.decode([String: String].self, from: try #require($0.body))["client_id"] }
        #expect(ids.count == 2 && ids[0] != nil && ids[0] == ids[1])
    }

    private final class Answers {
        private var left: [String]
        init(_ answers: [String]) { left = answers }
        func next() -> String { left.removeFirst() }
    }

    /// The hand-off went: an item in the answer this app can't read is left out, not taken for a failure.
    @Test func anUnreadableItemStillAnswersTheConversation() async throws {
        let hub = FakeHub()
        hub.on("POST", "/api/v1/inbox/lark:f1:11/handoff", json: """
        {"item":{"id":"lark:f1:11"},"session":{"machine_id":"server","session_id":"main-1"}}
        """)
        let answer = try await hub.client.inboxHandoff("lark:f1:11", text: "看看", clientID: UUID())
        #expect(answer.session == Self.main && answer.item == nil)
    }
}
