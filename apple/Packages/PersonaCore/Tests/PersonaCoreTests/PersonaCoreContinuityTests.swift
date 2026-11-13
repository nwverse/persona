import XCTest
import Synchronization
@testable import PersonaCore

final class PersonaCoreContinuityTests: XCTestCase {
    func testEveryFixtureThroughTheRealCBinding() throws {
        let result = try ContinuityBindingReplay.replayAll()
        XCTAssertEqual(result.cases, 34)
        XCTAssertEqual(result.steps, 79)
        print("PersonaCore C binding: \(result.cases) cases, \(result.steps) steps, \(result.patches) drained patches")
    }

    func testRepeatedItemUsesUpdateAndIndependentChatWakes() throws {
        let changes = Mutex<[String: [UInt64]]>([:])
        let client = try PersonaCoreClient(config: Config(manualTimers: true), callbacks: PersonaCoreCallbacks(changed: { version in
            // The version is global but does not identify a chat. This smoke test uses the drain below for each chat;
            // two callbacks before either drain proves the old global wake loss cannot reappear.
            changes.withLock { $0["wake", default: []].append(version) }
        }, run: { _ in }))
        for chat in ["a", "b"] {
            try client.dispatch(.openChat(chat: chat, snapshot: ChatSnapshot(
                itemsJson: #"[{"id":"1#0","seq":1,"role":"assistant","kind":"text","text":"old"}]"#,
                history: "h", covered: 1, hasMore: false, loadedOnce: true
            )))
        }
        try client.flush()
        XCTAssertEqual(changes.withLock { $0["wake"]?.count }, 2)
        for chat in ["a", "b"] {
            let patch = try client.drain(chat: chat)
            XCTAssertEqual(patch.ops.count, 1)
        }
        let newest = WireBatch(
            itemsJson: #"[{"id":"1#0","seq":1,"role":"assistant","kind":"text","text":"newest"}]"#,
            history: "h", fromSeq: 0, toSeq: 1, backlog: false, hasMore: nil, moreAfter: false
        )
        try client.dispatch(.chat(chat: "a", event: .pushed(batch: newest)))
        try client.flush()
        let patch = try client.drain(chat: "a")
        guard patch.ops.count == 1, case let .update(index: index, id: id, json: json) = patch.ops[0] else {
            return XCTFail("a newer full item must be one Update, got \(patch.ops)")
        }
        XCTAssertEqual(index, 0)
        XCTAssertEqual(id, "1#0")
        XCTAssertTrue(json.contains("newest"))
        client.close()
    }
}
