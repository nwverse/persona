import Foundation
import Testing
@testable import Persona

/// What Persona sent to its tasks' agents (#1542): only a `routed` item marked `from: assistant` is Persona's; a task's
/// card takes in the hand-off written right after it (same task, same conversation), never a later message or the
/// user's; the folded line shows the first line with words.
struct SentToAgentsTests {
    private let t0 = Date(timeIntervalSince1970: 1_800_000_000)

    private func item(_ id: String, _ kind: FeedItem.Kind, _ ref: String, after seconds: TimeInterval, conversation: String = "c1",
                      body: [String: JSONValue]? = nil) -> FeedItem {
        FeedItem(id: id, conversation: conversation, at: t0.addingTimeInterval(seconds), kind: kind, ref: ref, body: body.map { .object($0) })
    }

    private func persona(_ id: String, _ ref: String, after seconds: TimeInterval, conversation: String = "c1", text: String = "x",
                         interrupted: Bool? = nil) -> FeedItem {
        var body: [String: JSONValue] = ["text": .string(text), "from": .string("assistant")]
        if let interrupted { body["interrupted"] = .bool(interrupted) }
        return item(id, .routed, ref, after: seconds, conversation: conversation, body: body)
    }

    private func user(_ id: String, _ ref: String, after seconds: TimeInterval) -> FeedItem {
        item(id, .routed, ref, after: seconds, body: ["text": .string("顺便看看日志")])
    }

    @Test func onlyAMarkedRoutedItemIsPersonas() throws {
        let decoded = try JSON.decoder.decode([FeedItem].self, from: Data(#"""
        [{"id":"a","conversation":"c1","at":"2026-11-08T12:00:00Z","kind":"routed","ref":"t","body":{"text":"把内存降下来","from":"assistant"}},
         {"id":"b","conversation":"c1","at":"2026-11-08T12:01:00Z","kind":"routed","ref":"t","body":{"text":"顺便看看日志"}},
         {"id":"c","conversation":"c1","at":"2026-11-08T12:02:00Z","kind":"routed","ref":"t","body":{"text":"先停","interrupted":true}},
         {"id":"d","conversation":"c1","at":"2026-11-08T12:03:00Z","kind":"report","ref":"t","body":{"text":"x","from":"assistant"}}]
        """#.utf8))
        #expect(decoded.map(\.sentByPersona) == [true, false, false, false])
        #expect(decoded[0].text == "把内存降下来")
    }

    @Test func theCardTakesInOnlyItsOwnHandOff() {
        let feed = [
            item("card", .task, "t1", after: 0),
            persona("handoff", "t1", after: 0.008),
            user("said", "t1", after: 1),                         // the user's own word, right after: stays a bubble
            persona("rework", "t1", after: 600),                   // later: a folded line of its own
            persona("other", "t2", after: 2),                      // another task, no card of its own here
            persona("elsewhere", "t3", after: 3, conversation: "c2"),
            item("card3", .task, "t3", after: 2),                  // t3's card is in c1, the message in c2
            persona("before", "t4", after: 4),
            item("card4", .task, "t4", after: 5),                  // written before its card: not a hand-off
        ]
        #expect(SentToAgents.takenIn(feed) == ["handoff"])
        #expect(SentToAgents.shown(feed).map(\.id) == ["card", "said", "rework", "other", "elsewhere", "card3", "before", "card4"])
        #expect(SentToAgents.sent(feed, todo: "t1").map(\.id) == ["handoff", "rework"])
        #expect(SentToAgents.shown([item("card", .task, "t1", after: 0)]).map(\.id) == ["card"])
    }

    @Test func aHandOffComesWithinAMinuteOfItsCard() {
        let feed = [item("card", .task, "t1", after: 0), persona("late", "t1", after: SentToAgents.handOff + 1)]
        #expect(SentToAgents.takenIn(feed).isEmpty)
        let again = [item("card", .task, "t1", after: 0), persona("edge", "t1", after: SentToAgents.handOff)]
        #expect(SentToAgents.takenIn(again) == ["edge"])
    }

    @Test func theFoldedLineShowsTheFirstLineWithWords() {
        #expect(SentToAgents.firstLine("\n\n  把内存降下来，目标是不再被杀掉。  \n\n背景：\n- 一周 14 次") == "把内存降下来，目标是不再被杀掉。")
        #expect(SentToAgents.firstLine("（督办：验收没通过，第 1 轮返工，最多 2 轮）\n逐条验收：") == "（督办：验收没通过，第 1 轮返工，最多 2 轮）")
        #expect(SentToAgents.firstLine(" \n \n") == "")
        #expect(SentToAgents.firstLine("") == "")
    }

    @Test func theTitleSaysWhenPersonaCutIn() {
        #expect(persona("i", "t", after: 0, interrupted: true).interrupted)
        #expect(!persona("s", "t", after: 0).interrupted)
    }
}
