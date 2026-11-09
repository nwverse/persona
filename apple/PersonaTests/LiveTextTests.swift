import Foundation
import Testing
@testable import Persona

/// An agent's live text against its transcript (`LiveText`, docs/integrations/claude-integration.md "Live replies
/// (MessageDisplay)"), case by case as the web's `lib/live-text.test.ts`.
struct LiveTextTests {
    private func at(_ seq: Int, _ role: String, _ kind: ItemKind, _ text: String? = nil, fullSize: Int? = nil, wake: Bool? = nil,
                    tool: String? = nil, name: String? = nil) -> Item {
        Item(id: "i\(seq)", seq: seq, ts: nil, role: role, kind: kind, text: text, toolUseID: tool, toolName: name, toolInput: nil,
             isError: nil, fullSize: fullSize, image: nil, wake: wake)
    }

    private func whole(_ text: String) -> LiveText.Landed { LiveText.Landed(text) }

    @Test func keepsWhatTheTranscriptDoesNotHaveAsItCame() {
        #expect(LiveText.unlandedText("Line 1.\nLine 2.\n", []) == "Line 1.\nLine 2.\n")
        #expect(LiveText.unlandedText("\n\nLine 1.\n", []) == "\n\nLine 1.\n")
        #expect(LiveText.unlandedText("\n \n", []) == "")
    }

    @Test func dropsWhatAnItemAlreadyHoldsTheFinalBatchsLastLineToo() {
        let words = [whole("Line 1.\nLine 2.\nLine 3.")]
        #expect(LiveText.unlandedText("Line 1.\nLine 2.\n", words) == "")
        #expect(LiveText.unlandedText("Line 1.\nLine 2.\nLine 3.", words) == "")
    }

    @Test func showsOnlyWhatGoesPastTheBlocksItBeginsWith() {
        #expect(LiveText.unlandedText("Block one.\n\nBlock two.\n", [whole("Block one.")]) == "Block two.\n")
        #expect(LiveText.unlandedText("Block one.Block two.", [whole("Block one."), whole("Block two.")]) == "")
    }

    @Test func takesAnItemOffOnlyWhereAWordEnds() {
        #expect(LiveText.unlandedText("好的，测试已通过。\n", [whole("好的")]) == "好的，测试已通过。\n")
        #expect(LiveText.unlandedText("OK, the tests pass.\n", [whole("OK")]) == "OK, the tests pass.\n")
    }

    @Test func countsACutItemAsItsWholeBlockWhatGoesPastItStillShows() {
        let long = String(repeating: "Line of a long reply.\n", count: 30)
        #expect(LiveText.unlandedText(long, [LiveText.Landed(String(long.prefix(40)), size: long.utf8.count - 1)]) == "")
        #expect(LiveText.unlandedText(long + "\nAnother block.\n", [LiveText.Landed(String(long.prefix(40)), size: long.utf8.count)])
                == "Another block.\n")
        let wide = String(repeating: "测试结果：全部通过。\n", count: 20) // its size counts bytes, not characters
        #expect(LiveText.unlandedText(wide + "下一段。\n", [LiveText.Landed(String(wide.prefix(12)), size: wide.utf8.count)]) == "下一段。\n")
    }

    @Test func leavesTheNextMessageAlone() {
        #expect(LiveText.unlandedText("Mountain 1.\n", [whole("Sea 1.\nSea 2.")]) == "Mountain 1.\n")
    }

    @Test func takesOnlyTheAgentsLatestMessageACutItemWithoutItsNote() {
        let items = [
            at(1, "user", .text, "Earlier question"),
            at(2, "assistant", .text, "Earlier answer."),
            at(3, "user", .text, "Write about the sea, then list the folder."),
            at(4, "assistant", .thinking, "Plan."),
            at(5, "assistant", .text, "Sea 1.\nSea 2."),
            at(6, "assistant", .toolUse, tool: "t1", name: "Bash"),
            at(7, "user", .toolResult, "total 0", tool: "t1"),
            at(8, "assistant", .text, "A long reply\n…（已截断，共 40000 字节）", fullSize: 40000), // the hub's note
            at(9, "assistant", .text, "And a second block."),
        ]
        #expect(LiveText.landedWords(items) == [LiveText.Landed("A long reply", size: 40000), whole("And a second block.")])
        // words followed by a tool's result are an earlier message's: never the live one's
        #expect(LiveText.landedWords(items.prefix(7)) == [])
        #expect(LiveText.landedWords(items.prefix(6)) == [whole("Sea 1.\nSea 2.")])
        #expect(LiveText.landedWords(items + [at(10, "system", .notice, "Background task done", wake: true)]) == [])
    }

    @Test func doesNotLetTheMessageBeforeWhichBeginsAlikeTakeTheNextOne() {
        let items = [
            at(1, "user", .text, "Rename them and run the tests."),
            at(2, "assistant", .text, "I'll update the names.\nThen I'll run the tests."),
            at(3, "assistant", .toolUse, tool: "t1", name: "Bash"),
            at(4, "user", .toolResult, "ok", tool: "t1"),
        ]
        #expect(LiveText.unlandedText("I'll update the names.\n", LiveText.landedWords(items)) == "I'll update the names.\n")
        #expect(LiveText.unlandedLive("I'll update the names.\n", items) == "I'll update the names.\n")
    }

    @Test func dropsAWholeCopyOfTheMessageBeforeComeLateAndNothingElseOfIt() {
        let before = "I'll update the names.\nThen I'll run the tests."
        let items = [
            at(1, "user", .text, "Rename them and run the tests."),
            at(2, "assistant", .text, before),
            at(3, "assistant", .toolUse, tool: "t1", name: "Bash"),
            at(4, "user", .toolResult, "ok", tool: "t1"),
        ]
        #expect(LiveText.unlandedLive(before, items) == "")
        #expect(LiveText.unlandedLive("Running the tests now.\n", items) == "Running the tests now.\n")
        // the turn before's last words are not this turn's
        #expect(LiveText.unlandedLive("Rename them and run the tests.", items.prefix(1)) == "Rename them and run the tests.")
        #expect(LiveText.unlandedLive("Done.", [at(1, "assistant", .text, "Done."), at(2, "user", .text, "Again?"),
                                                 at(3, "assistant", .thinking, "…")]) == "Done.")
    }

    /// Matching goes by Unicode scalars, as the node's bytes and the web's code units: a combining mark after the
    /// words an item holds is not part of a Swift character the item would have to hold too.
    @Test func matchesByScalarsNotCharacters() {
        #expect(LiveText.unlandedText("Cafe\u{301} is open.\n", [whole("Cafe")]) == "Cafe\u{301} is open.\n")
        #expect(LiveText.unlandedText("Café.\nNext.\n", [whole("Café.")]) == "Next.\n")
    }
}
