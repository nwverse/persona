import Foundation
import Testing
@testable import Persona

/// The pictures a message may carry come from the hub's hello (#12973, docs/protocol/protocol.md "Pictures in a message"):
/// read on their own, so no shape of them can lose the hello, with the standard ones where the hub says nothing usable.
struct ImageLimitsTests {
    private func hello(_ limits: String) -> Data { Data(#"{"type":"hello","features":["a"],"image_limits":\#(limits)}"#.utf8) }

    @Test func aHubWithoutThemHasTheStandardOnes() {
        #expect(ImageLimitsTable.decode(hello: Data(#"{"type":"hello","features":[]}"#.utf8)) == nil)
        #expect(ImageLimits.standard == ImageLimits(requestBytes: 33_554_432, imageBytes: 3_750_000, maxSide: 1600, perMessage: 20))
    }

    @Test func helloSaysThemAndAnAgentsOwnComeFirst() throws {
        let table = try #require(ImageLimitsTable.decode(hello: hello(
            #"{"request_bytes":1000,"image_bytes":500,"max_side":800,"per_message":6,"agents":{"codex":{"request_bytes":2000,"image_bytes":900,"max_side":900,"per_message":9},"claude":{"per_message":3}}}"#)))
        #expect(table.limits(for: "codex") == ImageLimits(requestBytes: 2000, imageBytes: 900, maxSide: 900, perMessage: 9))
        #expect(table.limits(for: "pi") == ImageLimits(requestBytes: 1000, imageBytes: 500, maxSide: 800, perMessage: 6))
        #expect(table.limits(for: nil).perMessage == 3) // an empty agent is Claude Code's, as on the hub
        #expect(table.limits(for: "").perMessage == 3)
    }

    @Test func aValueNoClientCanUseKeepsTheStandardOneAndTheHello() throws {
        let table = try #require(ImageLimitsTable.decode(hello: hello(#"{"request_bytes":"lots","per_message":0,"max_side":-1,"agents":7}"#)))
        #expect(table == ImageLimitsTable())
        #expect(ImageLimitsTable.decode(hello: hello(#""nonsense""#)) == nil)
        // the hello itself still goes through
        guard case .hello(let h)? = ServerEvent.decode(hello(#"{"per_message":"x"}"#)) else { Issue.record("the hello was lost"); return }
        #expect(h.features == ["a"])
        #expect(h.imageLimits == ImageLimitsTable())
    }

    @Test func theRoomLeavesAMegabyteForTheWordsAndCountsEveryPicture() {
        let l = ImageLimits(requestBytes: ImageLimits.bodyRoom + 100, imageBytes: 50, maxSide: 1600, perMessage: 3)
        #expect(l.room(held: [], size: 100) == .ok)
        #expect(l.room(held: [60], size: 41) == .bytes)
        #expect(l.room(held: [1, 1, 1], size: 1) == .count)
        #expect(l.roomLeft(holding: 2) == 1)
        #expect(l.roomLeft(holding: 5) == 0)
    }

    @Test func theTableKeepsItsAgentsWhenStored() throws {
        let table = ImageLimitsTable(limits: .standard, agents: ["codex": ImageLimits(requestBytes: 1, imageBytes: 2, maxSide: 3, perMessage: 4)])
        let again = try JSONDecoder().decode(ImageLimitsTable.self, from: JSONEncoder().encode(table))
        #expect(again == table)
    }
}
