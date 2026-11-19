import Foundation
import Testing
@testable import Persona
import PersonaWire

/// The owner's messages the assistant keeps after they could not go in (#10194): the row carries them, a kept send
/// leaves the outbox for the row, and Retry / Delete reach the hub.
@MainActor
struct UndeliveredTests {
    private let ref = MachineRef(group: "", id: "m")
    private let key = SessionKey(machineID: "m", sessionID: "s")

    private func session(_ extra: String) throws -> Session {
        let json = #"{"machine_id":"m","session_id":"s","title":"t","cwd":"/tmp/p","status":"offline","state":"offline","assistant":true"# + extra + "}"
        return try JSON.decoder.decode(Session.self, from: Data(json.utf8))
    }

    @Test func theRowCarriesKeptMessages() throws {
        let row = try session(#","undelivered":[{"id":"k1","text":"hello","images":1,"at":"2026-11-17T10:00:00Z","state":"waiting","attempts":2,"next_at":"2026-11-17T10:01:00Z","error":"The assistant could not start"},{"id":"k2","text":"later","at":"2026-11-17T10:02:00Z","state":"failed"}]"#)
        let kept = try #require(row.undelivered)
        #expect(kept.map(\.id) == ["k1", "k2"])
        #expect(kept[0].images == 1 && kept[0].nextAt != nil && !kept[0].gaveUp)
        #expect(kept[1].gaveUp)
        #expect(try session("").undelivered == nil, "an older hub sends none")
        #expect(try session(#","undelivered":"broken""#).undelivered == nil, "a malformed list never hides the row")
    }

    /// Both of the hub's names read and write (#10966): `next_at` (when it is tried again) and `delete_only` (a message
    /// from a signed-out device: Delete, no Retry), once two enums each knew only one of them.
    @Test func nextAtAndDeleteOnlyRoundTrip() throws {
        let json = #"{"id":"k3","text":"from the old phone","at":"2026-11-17T10:00:00Z","state":"failed","attempts":3,"next_at":"2026-11-17T10:05:00Z","delete_only":true}"#
        let kept = try JSON.decoder.decode(UndeliveredMessage.self, from: Data(json.utf8))
        #expect(kept.nextAt == ISO8601DateFormatter().date(from: "2026-11-17T10:05:00Z"))
        #expect(kept.deleteOnly == true && kept.gaveUp && !kept.canRetry)
        let written = try JSONSerialization.jsonObject(with: JSON.encoder.encode(kept)) as? [String: Any]
        #expect(written?["delete_only"] as? Bool == true)
        #expect(written?["next_at"] != nil && written?["nextAt"] == nil && written?["deleteOnly"] == nil)
        let again = try JSON.decoder.decode(UndeliveredMessage.self, from: JSON.encoder.encode(kept))
        #expect(again.deleteOnly == true && again.attempts == 3)
        let retryable = try JSON.decoder.decode(UndeliveredMessage.self, from: Data(#"{"id":"k4","text":"t","at":"2026-11-17T10:00:00Z","state":"failed"}"#.utf8))
        #expect(retryable.deleteOnly == nil && retryable.nextAt == nil && retryable.canRetry)
    }

    @Test func aKeptSendLeavesTheOutboxForTheRow() async throws {
        let hub = FakeHub()
        hub.on("POST", "/api/v1/sessions/m/s/messages",
               json: #"{"delivered":"later","undelivered":{"id":"x","text":"hello","at":"2026-11-17T10:00:00Z","state":"waiting","error":"The assistant could not start"}}"#)
        let model = AppModel()
        model.apiForTests = hub.client
        model.applySessions([ref: [try session("")]])
        let msg = PendingMessage(text: "hello", images: [])
        model.chat(key).pending = [msg]
        model.chat(key).beginSendFeedback(msg)
        let outcome = await model.sendPending(key, msg)
        model.settleSend(key, msg.id, outcome)
        #expect(outcome == .kept)
        #expect(model.chat(key).pending.isEmpty, "the row draws it from now on")
        #expect(model.session(key)?.undelivered?.map(\.id) == ["x"])
        #expect(model.chat(key).sendFeedback?.phase == .finished, "no Processing left hanging while nothing runs")
    }

    @Test func aKeptHandOffIsNotFiledAsDone() async throws {
        let hub = FakeHub()
        hub.on("POST", "/api/v1/sessions/m/s/messages", json: #"{"delivered":"later","undelivered":{"id":"x","text":"context","at":"2026-11-17T10:00:00Z","state":"waiting"}}"#)
        let model = AppModel()
        model.apiForTests = hub.client
        model.applySessions([ref: [try session("")]])
        var filings: [PendingMessage.InboxHandOff] = []
        model.inboxHandOffDoneForTests = { filings.append($0) }
        let item = try JSON.decoder.decode(InboxItem.self, from: Data(#"{"id":"mail:a:1","source":"mail","kind":"mail","account":"a","account_name":"A","title":"t","preview":"p","count":1,"manager":"mgr"}"#.utf8))
        let result = await model.sendForHandOff(key, text: "context", item: item, file: true)
        #expect(result.delivered && result.kept && !result.filed)
        #expect(filings.isEmpty, "a message the agent has not got yet does not file the item")
    }

    @Test func shownLeavesOutWhatTheTranscriptOrThisPhoneHas() {
        let at = Date(timeIntervalSince1970: 0)
        let list = ["A", "b", "c"].map { UndeliveredMessage(id: $0, text: $0, at: at, state: "waiting") }
        #expect(UndeliveredMessage.shown(list, landed: ["a"], drawn: ["B"]).map(\.id) == ["c"])
        #expect(UndeliveredMessage.shown(nil, landed: [], drawn: []).isEmpty)
    }

    @Test func retryAndDeleteReachTheHub() async throws {
        let hub = FakeHub()
        hub.on("POST", "/api/v1/assistant/conversations/s/undelivered/k2/retry", json: #"{"ok":true}"#)
        hub.on("DELETE", "/api/v1/assistant/conversations/s/undelivered/k2", json: #"{"ok":true}"#)
        let model = AppModel()
        model.apiForTests = hub.client
        model.applySessions([ref: [try session(#","undelivered":[{"id":"k2","text":"later","at":"2026-11-17T10:02:00Z","state":"failed","error":"gone"}]"#)]])
        await model.retryUndelivered(key, "k2")
        #expect(hub.sent("POST", "/api/v1/assistant/conversations/s/undelivered/k2/retry").count == 1)
        #expect(model.session(key)?.undelivered?.first?.gaveUp == false)
        await model.deleteUndelivered(key, "k2")
        #expect(hub.sent("DELETE", "/api/v1/assistant/conversations/s/undelivered/k2").count == 1)
        #expect(model.session(key)?.undelivered?.isEmpty == true)
    }

    private let heldJSON = #"{"id":"x","text":"Review the notes","at":"2026-11-17T10:00:00Z","state":"failed","computer":"Laptop","error":"Might not have arrived. Retry checks the chat first."}"#

    private func agent(_ held: String? = nil) throws -> Session {
        var row = try session(held.map { #","undelivered":["# + $0 + "]" } ?? "")
        row.assistant = false
        row.manager = "another-assistant"
        return row
    }

    private func heldModel(_ hub: FakeHub, full: Bool = true, feature: Bool = true) throws -> AppModel {
        let model = AppModel(stateFolder: FileManager.default.temporaryDirectory.appending(path: "held-" + UUID().uuidString))
        model.linkHooksForTests = (transport: try #require(hub.client.transport), socket: { SyncSocket() })
        model.registry = HubRegistry(hubs: [PairedHub(config: ServerConfig(url: hub.url, token: "fixture"), deviceID: full ? "phone" : nil)])
        model.buildLinks()
        model.link("")?.received(HubHello(features: feature ? ["agent_message_hold", "sessions_lookup"] : []))
        var client = hub.client.showing("another-manager")
        client.sessionLookup = true
        model.apiForTests = client
        model.replaceMachines("", [try JSON.decoder.decode(Machine.self, from: Data(#"{"id":"m","name":"Laptop","kind":"computer","online":false,"manager":"owner"}"#.utf8))])
        model.applySessions([ref: [try agent(heldJSON)]])
        return model
    }

    @Test func heldRetryUsesTheComputerAndKeepsTheManagersReply() async throws {
        let hub = FakeHub()
        let model = try heldModel(hub)
        let path = "/api/v1/sessions/m/s/held/x"
        let after = heldJSON.replacingOccurrences(of: "failed", with: "waiting")
            .replacingOccurrences(of: "Might not have arrived. Retry checks the chat first.", with: "Will check the chat when Laptop is back")
        hub.on("POST", path + "/retry", json: #"{"ok":true}"#)
        hub.on("GET", "/api/v1/sessions", json: #"{"sessions":[{"machine_id":"m","session_id":"s","title":"t","cwd":"","status":"offline","state":"offline","undelivered":["# + after + "]}]}")
        hub.on("DELETE", path, json: #"{"ok":true}"#)
        await model.retryUndelivered(key, "x")
        #expect(hub.sent("POST", path + "/retry").map { $0.headers[APIClient.managerHeader] } == ["owner"])
        #expect(model.session(key)?.undelivered?.first?.error == "Will check the chat when Laptop is back")
        await model.deleteUndelivered(key, "x")
        #expect(hub.sent("DELETE", path).map { $0.headers[APIClient.managerHeader] } == ["owner"])
        #expect(model.session(key)?.undelivered?.isEmpty == true)
        #expect(hub.requests.allSatisfy { $0.headers[APIClient.managerHeader] != "another-manager" })
    }

    @Test func partialRowsPreserveHeldMessagesAndACompleteRowCanRemoveThem() throws {
        let model = try heldModel(FakeHub())
        var summary = try agent()
        summary.listWaiting = false
        model.landSessionSummaries([summary], group: "")
        #expect(model.session(key)?.undelivered?.map(\.id) == ["x"])
        model.applySessions([ref: [try agent()]])
        #expect(model.session(key)?.undelivered?.isEmpty != false)
    }

    @Test func restrictedOfflineSendsAndHeldActionsLeaveTheDraftInPlace() async throws {
        for (full, feature) in [(false, true), (true, false)] {
            let hub = FakeHub()
            let model = try heldModel(hub, full: full, feature: feature)
            model.drafts[key] = "Still editing"
            #expect(!model.sendNow(key, text: "Still editing"))
            await model.retryUndelivered(key, "x")
            await model.deleteUndelivered(key, "x")
            #expect(model.drafts[key] == "Still editing")
            #expect(model.chat(key).pending.isEmpty)
            #expect(hub.requests.isEmpty)
        }
    }

    @Test func anExplicitRetryDoesNotOverrideAnotherDevicesDecision() async throws {
        let hub = FakeHub()
        let model = try heldModel(hub)
        let newRow = try agent(heldJSON.replacingOccurrences(of: "Might not have arrived. Retry checks the chat first.", with: "Not sent: the waiting time expired."))
        hub.on("POST", "/api/v1/sessions/m/s/held/x/retry") { _ in
            model.applySessions([self.ref: [newRow]])
            return Data(#"{"ok":true}"#.utf8)
        }
        await model.retryUndelivered(key, "x")
        #expect(model.session(key)?.undelivered?.first?.error == "Not sent: the waiting time expired.")
        #expect(model.session(key)?.undelivered?.first?.gaveUp == true)
        #expect(hub.sent("GET", "/api/v1/sessions").isEmpty)
    }

    @Test func holdingUsesThePairedHubsFeatureAndLeavesNewTextAlone() async throws {
        let hub = FakeHub()
        let model = try heldModel(hub)
        let item = PendingMessage(text: "Review the notes", images: [])
        hub.on("POST", "/api/v1/sessions/m/s/messages", json: #"{"delivered":"later","undelivered":{"id":"y","text":"Review the notes","at":"2026-11-17T10:00:00Z","state":"waiting","computer":"Laptop","error":"Will send when Laptop is back"}}"#)
        #expect(model.offlineAgent(try agent())?.canHold == true)
        #expect(!model.canManageHeld("other"))
        #expect(model.offlineAgent(try session("")) == nil, "Assistant keeps its own delivery rules")
        model.chat(key).pending = [item]
        model.chat(key).beginSendFeedback(item)
        model.drafts[key] = "A new draft"
        let outcome = await model.sendPending(key, item)
        #expect(outcome == .kept)
        #expect(hub.sent("POST", "/api/v1/sessions/m/s/messages").first?.headers["X-Persona-Features"] == "agent_message_hold")
        #expect(model.session(key)?.undelivered?.last?.heldStatus == "Will send when Laptop is back")
        #expect(model.drafts[key] == "A new draft")
        #expect(model.chat(key).pending.isEmpty && model.chat(key).sendFeedback?.phase == .finished)
        var stopped = try agent()
        stopped.capabilities = try JSON.decoder.decode(SessionCapabilities.self, from: Data(#"{"read":true,"terminal":true}"#.utf8))
        #expect(stopped.isStopped && !stopped.canSend)
        #expect(model.offlineAgent(stopped) == nil)
        var history = stopped
        history.sourceKind = "readonly"
        #expect(!history.isStopped && model.offlineAgent(history) == nil)
    }

    @Test func onlyTheIOSAppAdvertisesAgentHolding() {
        let phone = RequestClientMetadata(platform: .iOS, surface: .app, version: "1")
        let watch = RequestClientMetadata(platform: .watchOS, surface: .app, version: "1")
        let widget = RequestClientMetadata(platform: .iOS, surface: .widget, version: "1")
        #expect(LiveConnection.helloFeatures(phone).contains(UndeliveredMessage.agentHoldFeature))
        #expect(!LiveConnection.helloFeatures(watch).contains(UndeliveredMessage.agentHoldFeature))
        #expect(!LiveConnection.helloFeatures(widget).contains(UndeliveredMessage.agentHoldFeature))
    }

    @Test func heldStatusesDisplayExactlyWhatTheManagerSent() throws {
        for (state, line) in [("waiting", "Will send when Laptop is back"), ("waiting", "Sending…"),
                              ("waiting", "Waits behind the message above"), ("failed", "Might not have arrived. Retry checks the chat first."),
                              ("failed", "Not sent: the waiting time expired.")] {
            var message = try JSON.decoder.decode(UndeliveredMessage.self, from: Data(heldJSON.utf8))
            message.state = state
            message.error = line
            #expect(message.heldStatus == line)
            #expect(message.canRetry == (state == "failed"))
            message.deleteOnly = true
            #expect(!message.canRetry && message.heldStatus == line)
            let restored = try JSON.decoder.decode(UndeliveredMessage.self, from: JSON.encoder.encode(message))
            #expect(restored.computer == "Laptop" && restored.heldStatus == line)
        }
    }
}
