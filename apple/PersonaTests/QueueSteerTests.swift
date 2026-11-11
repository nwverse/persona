import Foundation
import Testing
@testable import Persona

/// Steering a Claude's queue (#6088): which agents get Send Now and Move Up / Down, and the order a move asks for.
@MainActor
struct QueueSteerTests {
    private func session(_ extra: String) throws -> Session {
        let json = #"{"machine_id":"m","session_id":"s","title":"t","cwd":"/tmp/p","status":"working","state":"working""# + extra + "}"
        return try JSON.decoder.decode(Session.self, from: Data(json.utf8))
    }

    @Test func aMoveIsTheQueueInItsNewOrder() {
        #expect(QueueOrder.moved(count: 3, index: 1, by: -1) == [1, 0, 2])
        #expect(QueueOrder.moved(count: 3, index: 1, by: 1) == [0, 2, 1])
        #expect(QueueOrder.moved(count: 3, index: 0, by: -1) == nil, "the first one goes no higher")
        #expect(QueueOrder.moved(count: 3, index: 2, by: 1) == nil, "the last one goes no lower")
        #expect(QueueOrder.moved(count: 3, index: 5, by: -1) == nil, "an index the queue no longer has")
    }

    @Test func ownQueueCountLeavesRelayedMessagesOutAndClampsTheirCount() throws {
        let queue = #","queued":["one","two","relay"]"#
        #expect(try session(queue).ownQueuedCount == 3, "an older hub has only the user's own messages")
        #expect(try session(queue + #","queued_relayed":1"#).ownQueuedCount == 2)
        #expect(try session(queue + #","queued_relayed":99"#).ownQueuedCount == 0, "a stale count cannot make the own count negative")
        #expect(try session(queue + #","queued_relayed":-2"#).ownQueuedCount == 3, "a malformed negative count is none")
    }

    @Test func onlyAClaudeInATerminalSteersItsQueue() throws {
        #expect(try session(#","agent":"claude","pane_id":"w1:p1""#).steersQueue)
        #expect(try session(#","pane_id":"w1:p1""#).steersQueue, "an older hub that does not name the agent: Claude")
        #expect(try !session(#","agent":"codex","pane_id":"w1:p1""#).steersQueue)
        #expect(try !session(#","agent":"claude""#).steersQueue, "not in a terminal pane")
        #expect(try !session(#","agent":"claude","pane_id":"w1:p1","assistant":true"#).steersQueue)
    }

    @Test func relayedMessagesCannotBeEditedMovedOrSentNow() async throws {
        let hub = FakeHub()
        hub.on("POST", "/api/v1/sessions/m/s/queue", json: #"{"queue":["one","two","relay"]}"#)
        hub.on("POST", "/api/v1/sessions/m/s/queue/send-now", json: #"{"sent":true}"#)
        let model = AppModel()
        model.apiForTests = hub.client
        let ref = MachineRef(group: "", id: "m")
        model.applySessions([ref: [try session(#","assistant":true,"queued":["one","two","relay"],"queued_relayed":1"#)]])
        let key = SessionKey(machineID: "m", sessionID: "s")
        await model.editQueued(key, index: 2, delete: true)
        await model.moveQueued(key, index: 2, by: -1)

        model.applySessions([ref: [try session(#","assistant":true,"queued":["relay"],"queued_relayed":1"#)]])
        await model.sendQueuedNow(key)
        #expect(hub.sent("POST", "/api/v1/sessions/m/s/queue").isEmpty, "relayed messages are not queue-edit indexes")
        #expect(hub.sent("POST", "/api/v1/sessions/m/s/queue/send-now").isEmpty, "nothing of the user's own waits to send")
    }

    @Test func queuePhotoMarksFollowTheOwnQueueAndOutbox() {
        let store = ChatStore(key: SessionKey(machineID: "m", sessionID: "s"))
        store.updateQueuePhotoMarks(queued: ["one", "relay"], own: 1)
        #expect(store.queuePhotoMarks == [0])

        store.pending = [PendingMessage(text: "one", images: [ImagePayload(mime: "image/png", data: "AA==")])]
        #expect(store.queuePhotoMarks == [1], "the outbox change derives the mark once for the view")

        store.updateQueuePhotoMarks(queued: ["relay"], own: 0)
        #expect(store.queuePhotoMarks.isEmpty, "relayed rows never borrow a local pending message's photos")
    }
}
