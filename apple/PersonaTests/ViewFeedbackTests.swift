import Foundation
import Testing
@testable import Persona

/// #4315: the node's `<persona-view-errors>` feedback before an assistant message is for the model. An older hub gave
/// it as a user item of its own (the line's block #0) or at the start of the message, and the bubble showed its tags.
struct ViewFeedbackTests {
    private let key = SessionKey(machineID: "mac", sessionID: "s1")
    private let feedback = "<persona-view-errors>\nThe view v1 failed its check.\n</persona-view-errors>"

    private func text(_ id: String, _ seq: Int, _ role: String, _ text: String) -> Item {
        var it = Item(id: id, seq: seq, ts: nil, role: role, kind: .text, text: text, toolUseID: nil, toolName: nil,
                      toolInput: nil, isError: nil, image: nil)
        if role == "user" { it.clientMsgID = "m1" }
        return it
    }

    @Test func anItemOfOnlyTheFeedbackGoes() {
        let items = [text("l5#0", 5, "user", feedback), text("l5#1", 5, "user", "Add a bakery stop."),
                     text("l6#0", 6, "assistant", "Bakery stop added.")]
        let kept = ChatStore.withImageRefs(items, key)
        #expect(kept.map(\.id) == ["l5#1", "l6#0"])
        let users = ChatStore.buildRows(kept).compactMap { row -> String? in
            if case .user(let it) = row { return it.text }
            return nil
        }
        #expect(users == ["Add a bakery stop."])
    }

    @Test func theFeedbackBeforeTheMessageIsTakenOut() {
        let kept = ChatStore.withoutViewFeedback([text("l5#0", 5, "user", feedback + "\nAdd a bakery stop.")])
        #expect(kept.map(\.text) == ["Add a bakery stop."])
    }

    @Test func otherItemsStayAsTheyAre() {
        let items = [text("l1#0", 1, "user", "Plan a weekend trip."), text("l2#0", 2, "assistant", feedback)]
        #expect(ChatStore.withoutViewFeedback(items) == items)
    }

    @Test func theWatchDoesNotShowIt() {
        #expect(WatchText.shown(feedback + "\nAdd a bakery stop.") == "Add a bakery stop.")
    }
}
