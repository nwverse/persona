import Foundation
import Testing
@testable import Persona

/// A lazy picture's ref names its chat's group of hubs, so its bytes are asked of that group's hub (#1555: a chat on
/// another group than the current one sent them to the current group's hub, which did not have them).
struct ImageRefTests {
    private let home = SessionKey(machineID: "mac", sessionID: "s1")
    private let work = SessionKey(group: "work", machineID: "mac", sessionID: "s1")

    @Test func aRefNamesItsGroup() {
        #expect(ImageRef.make(home, item: "u#1") == "mac/s1/u#1")
        #expect(ImageRef.make(work, item: "u#1") == "work|mac/s1/u#1")
        let (group, ref) = ImageRef.split("work|mac/s1/u#1")
        #expect(group == "work" && ref == "mac/s1/u#1")
        let (none, same) = ImageRef.split("mac/s1/u#1")
        #expect(none.isEmpty && same == "mac/s1/u#1")
        // A bar after the first slash is part of the session or the item, not a group.
        #expect(ImageRef.split("mac/s1/a|b").group.isEmpty)
        #expect(ImageRef.session("work|mac/s1/u#1") == work)
        #expect(ImageRef.session("mac/s1/u#1") == home)
        #expect(ImageRef.session("mac/s1") == nil)
    }

    @Test func aPageAndAFoldOfAnotherGroupsChatGetItsGroup() {
        var it = Item(id: "u#1", seq: 3, ts: nil, role: "user", kind: .image, text: nil, toolUseID: nil, toolName: nil, toolInput: nil, isError: nil, image: nil)
        var img = ImagePayload(mime: "image/png", data: "")
        img.lazy = true
        it.image = img
        #expect(ChatStore.withImageRefs([it], work)[0].image?.ref == "work|mac/s1/u#1")
        #expect(ChatStore.withImageRefs([it], home)[0].image?.ref == "mac/s1/u#1")
        // A ref a cache kept from before is written again for the chat it is in.
        var old = it
        old.image?.ref = "mac/s1/u#1"
        #expect(ChatStore.withImageRefs([old], work)[0].image?.ref == "work|mac/s1/u#1")
        // A fold's pictures (the work a page left out), new and kept from before.
        var fold = Item(id: "f#0", seq: 4, ts: nil, role: "assistant", kind: .fold, text: nil, toolUseID: nil, toolName: nil, toolInput: nil, isError: nil, image: nil)
        let seen = FoldImage(itemID: "t#1", toolUseID: "call1", tool: "Read", source: "read")
        var kept = seen
        kept.ref = "mac/s1/t#1"
        fold.fold = Fold(items: 2, tools: 1, thoughts: 0, throughSeq: 5, throughID: "t#1", images: [seen, kept])
        #expect(ChatStore.withImageRefs([fold], work)[0].fold?.images?.map(\.ref) == ["work|mac/s1/t#1", "work|mac/s1/t#1"])
    }
}
