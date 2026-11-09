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

    @Test func onlyAClaudeInATerminalSteersItsQueue() throws {
        #expect(try session(#","agent":"claude","pane_id":"w1:p1""#).steersQueue)
        #expect(try session(#","pane_id":"w1:p1""#).steersQueue, "an older hub that does not name the agent: Claude")
        #expect(try !session(#","agent":"codex","pane_id":"w1:p1""#).steersQueue)
        #expect(try !session(#","agent":"claude""#).steersQueue, "not in a terminal pane")
        #expect(try !session(#","agent":"claude","pane_id":"w1:p1","assistant":true"#).steersQueue)
    }
}
