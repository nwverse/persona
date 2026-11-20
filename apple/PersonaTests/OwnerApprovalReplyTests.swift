import Foundation
import Testing
@testable import Persona
import PersonaWire

/// Exact owner replies stay separate from ordinary Allow and model delivery, and retry under their original event id.
@MainActor
struct OwnerApprovalReplyTests {
    private let path = "/api/v1/sessions/m/s/messages"

    private func proposal() throws -> Interaction {
        let json = #"{"id":"proposal-1","machine_id":"m","session_id":"s","kind":"permission","tool_name":"owner_action_prepare","approval_batch_id":"batch-1","owner_proposal":true,"no_expiry":true,"owner_approval_phrases":["发","同意"]}"#
        return try JSON.decoder.decode(Interaction.self, from: Data(json.utf8))
    }

    private func model(_ hub: FakeHub, _ ix: Interaction) throws -> AppModel {
        let model = AppModel()
        model.apiForTests = hub.client
        let session = #"{"machine_id":"m","session_id":"s","title":"Main","cwd":"/tmp","status":"idle","state":"needs_you"}"#
        let s = try JSON.decoder.decode(Session.self, from: Data(session.utf8))
        model.applySessions([MachineRef(group: "", id: "m"): [s]])
        model.replaceInteractions("", [ix])
        return model
    }

    @Test func proposalMetadataAndSettingsDecodeWhileTheMarkerNeverSerializes() throws {
        let ix = try proposal()
        #expect(ix.ownerApprovalPhrase == "发" && ix.noExpiry == true && ix.approvalBatchID == "batch-1")
        let ordinary = try JSON.decoder.decode(Interaction.self, from: Data(#"{"id":"ordinary","machine_id":"m","session_id":"s","kind":"permission","owner_approval_phrases":["发"]}"#.utf8))
        #expect(ordinary.ownerApprovalPhrase == nil)

        let resolution = Resolution(allow: true, ownerApprovalPhrase: "发")
        let object = try #require(JSONSerialization.jsonObject(with: JSONEncoder().encode(resolution)) as? [String: Any])
        #expect(object["allow"] as? Bool == true)
        #expect(object["ownerApprovalPhrase"] == nil && object["owner_approval_phrase"] == nil)

        var body = APIClient.AssistantConfigBody()
        body.ownerReplyApproval = true
        body.persistentOwnerActionCards = false
        body.ownerReplyApprovalPhrases = ["发", "Agree"]
        let settings = try #require(JSONSerialization.jsonObject(with: JSONEncoder().encode(body)) as? [String: Any])
        #expect(Set(settings.keys) == ["owner_reply_approval", "persistent_owner_action_cards", "owner_reply_approval_phrases"])
        #expect(settings["owner_reply_approval"] as? Bool == true && settings["persistent_owner_action_cards"] as? Bool == false)
        #expect(settings["owner_reply_approval_phrases"] as? [String] == ["发", "Agree"])
    }

    @Test func exactReplyUsesOnlyTheProofEnvelopeAndItsRetryKeepsTheEventID() async throws {
        let hub = FakeHub()
        var fail = true
        hub.on("POST", path) { _ in
            if fail {
                fail = false
                throw APIError(status: 400, message: "try again")
            }
            return Data(#"{"ok":true,"approval_reply":true,"interaction_id":"proposal-1"}"#.utf8)
        }
        let ix = try proposal()
        let model = try model(hub, ix)
        let answer = Resolution(allow: true, ownerApprovalPhrase: "发")
        await model.resolve(ix, answer)
        #expect(model.pendingInteractions.count == 1 && model.answerFailure(ix.uid) != nil)
        await model.retryAnswer(ix)
        #expect(model.pendingInteractions.isEmpty)

        let requests = hub.sent("POST", path)
        #expect(requests.count == 2)
        #expect(hub.sent("POST", "/api/v1/interactions/proposal-1/resolve").isEmpty)
        let bodies = try requests.map { request -> [String: Any] in
            let body = try #require(request.body)
            let object = try #require(JSONSerialization.jsonObject(with: body) as? [String: Any])
            #expect(Set(object.keys) == ["text", "client_msg_id", "approval_reply_to"])
            #expect(object["text"] as? String == "发" && object["approval_reply_to"] as? String == "proposal-1")
            #expect((object["client_msg_id"] as? String)?.caseInsensitiveCompare(request.headers[OpRetry.header] ?? "") == .orderedSame)
            return object
        }
        #expect((bodies[0]["client_msg_id"] as? String) == (bodies[1]["client_msg_id"] as? String))
    }

    @Test func anUnadvertisedReplyCannotFallBackToAllowOrAModelMessage() async throws {
        let hub = FakeHub()
        let ix = try proposal()
        let model = try model(hub, ix)
        await model.resolve(ix, Resolution(allow: true, ownerApprovalPhrase: "owner said yes"))
        #expect(hub.sent("POST", path).isEmpty)
        #expect(hub.sent("POST", "/api/v1/interactions/proposal-1/resolve").isEmpty)
        #expect(model.pendingInteractions.count == 1)
    }
}
