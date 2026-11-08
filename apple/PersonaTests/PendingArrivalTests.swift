import Foundation
import Testing
@testable import Persona

/// A message sent from the phone shows as a pending bubble until its copy in the transcript arrives (ChatStore.merge).
@MainActor
struct PendingArrivalTests {
    private func user(_ seq: Int, _ text: String, at: Date?, fullSize: Int? = nil) -> Item {
        var it = Item(id: "u\(seq)#0", seq: seq, ts: at, role: "user", kind: .text, text: text, toolUseID: nil, toolName: nil,
                      toolInput: nil, isError: nil, image: nil)
        it.fullSize = fullSize
        return it
    }

    private func tagged(_ seq: Int, _ text: String, _ id: UUID, role: String = "user", kind: ItemKind = .text) -> Item {
        var it = Item(id: "t\(seq)#0", seq: seq, ts: Date(), role: role, kind: kind, text: text, toolUseID: nil, toolName: nil,
                      toolInput: nil, isError: nil, image: nil)
        it.clientMsgID = id.uuidString.lowercased() // the app sends upper case; a hub that lowered it still ties it
        return it
    }

    private func store() -> ChatStore { ChatStore(key: SessionKey(machineID: "box", sessionID: "s1")) }

    // A long message comes back cut for the page (it ends in the Truncated line): it is still the message, and the bubble
    // went away only after a minute, the message shown twice until then (2026-10-31, a 16 KB paste).
    @Test func aLongMessageCutByThePageArrives() {
        let s = store()
        let long = (0..<500).map { String(format: "%05d 编造的一行", $0) }.joined(separator: "\n")
        s.pending = [PendingMessage(text: long, images: [])]
        let cut = String(long.prefix(4000)) + "\n…（已截断，点开看全文，共 31561 字节）"
        s.merge([user(5, cut, at: Date(), fullSize: 31561)])
        #expect(s.pending.isEmpty)
    }

    // A hub from before the rename gets the context block under the old tag name (HubNaming.outbound) and keeps it so in
    // the transcript: still the message. The bubble of a new chat started from Hand to AI stayed beside it for a minute.
    @Test func aContextBlockUnderTheOldTagNameArrives() {
        let s = store()
        let text = ContextRef(kind: "context", title: "Email · Sample", url: "", text: "Hi, can we move it to Tuesday?").xml + "\n\nSummarize it"
        s.pending = [PendingMessage(text: text, images: [])]
        s.merge([user(5, HubNaming.outbound(text, canonical: false), at: Date())])
        #expect(s.pending.isEmpty)
    }

    // A hand-off retried after its copy reached the open conversation (the hub answers the retry from its receipt): the
    // message is there already, no bubble beside it.
    @Test func aSentMessageWhoseCopyArrivedIsNotShownAgain() {
        let model = AppModel(), key = SessionKey(machineID: "box", sessionID: "sent-\(UUID().uuidString)"), id = UUID()
        model.chat(key).merge([tagged(5, "看一下这封邮件", id)])
        model.showSent(key, text: "看一下这封邮件", images: [], id: id)
        #expect(model.chat(key).pending.isEmpty)
    }

    // An older 「继续」 in a history page is not the 「继续」 just written: the unsent bubble used to vanish with it.
    @Test func anOlderMessageWithTheSameWordsIsNotIt() {
        let s = store()
        var p = PendingMessage(text: "继续", images: [])
        p.state = .waiting
        s.pending = [p]
        s.merge([user(3, "继续", at: Date().addingTimeInterval(-3600))], backlog: true)
        #expect(s.pending.count == 1)
        s.merge([user(9, "继续", at: Date())])
        #expect(s.pending.isEmpty)
    }

    // A batch that brings something new along with a message already shown does not match the old one again.
    @Test func onlyNewItemsCount() {
        let s = store()
        s.merge([user(3, "继续", at: nil)])
        s.pending = [PendingMessage(text: "继续", images: [])]
        s.merge([user(3, "继续", at: nil), user(4, "别的", at: nil)])
        #expect(s.pending.count == 1)
    }

    // Take Back to Edit / Delete takes a queued message back out of the agent, so no copy ever arrives. Its sent bubble, hidden
    // while it was queued, came back at the end of the chat and stayed while the agent worked: the message looked sent
    // again (2026-11-05).
    @Test func aQueuedMessageTakenBackLeavesNoBubble() {
        let s = store()
        var first = PendingMessage(text: "提交前先看看之前那几个 PR 的写法", images: [])
        first.state = .sent
        var other = PendingMessage(text: "顺便看看日志里有没有报错", images: [])
        other.state = .sent
        var unsent = PendingMessage(text: "提交前先看看之前那几个 PR 的写法", images: [])
        unsent.state = .waiting
        s.pending = [first, other, unsent]
        // the queue keeps the words without the phone's spacing
        #expect(s.pulledBack("提交前先看看之前那几个PR的写法"))
        #expect(s.pending.map(\.id) == [other.id, unsent.id])
        // one not sent yet was never queued; nothing else matches
        #expect(!s.pulledBack("提交前先看看之前那几个 PR 的写法"))
        #expect(!s.pulledBack("没发过的话"))
        #expect(s.pending.map(\.id) == [other.id, unsent.id])
    }

    // Two 「好」 sent one after the other: the first copy takes one bubble.
    @Test func oneCopyTakesOneBubble() {
        let s = store()
        s.pending = [PendingMessage(text: "好", images: []), PendingMessage(text: "好", images: [])]
        s.merge([user(7, "好", at: Date())])
        #expect(s.pending.count == 1)
    }

    // A new Persona conversation's first message (#511), shown as sent once the start request returns: the node answers
    // a session claude has not written to yet with no items, which must leave the bubble (the page read No messages yet);
    // the first line, written a moment later but stamped before the bubble was made, takes it.
    @Test func aNewConversationsFirstMessageStaysUntilWritten() {
        let s = store()
        var p = PendingMessage(text: "帮我看看今天的安排", images: [])
        p.state = .sent
        s.pending = [p]
        s.merge([], backlog: true, hasMore: false)
        #expect(s.pending.count == 1)
        s.merge([user(1, "帮我看看今天的安排", at: Date().addingTimeInterval(-2))])
        #expect(s.pending.isEmpty)
    }

    // The hub ties an item to the message it came from (client_msg_id, #968): that settles the bubble even when the words
    // are not the message's (the hub rewrote them) or the message became a notice (/compact → 「压缩上下文」).
    @Test func theHubsTieSettlesWhateverTheWords() {
        let s = store()
        let rewritten = PendingMessage(text: "/agent 看看这个群在聊什么", images: [])
        let compact = PendingMessage(text: "/compact", images: [])
        s.pending = [rewritten, compact]
        s.merge([tagged(4, "<persona-context>…</persona-context>\n看看这个群在聊什么", rewritten.id)])
        #expect(s.pending.map(\.id) == [compact.id])
        s.merge([tagged(5, "压缩上下文", compact.id, role: "system", kind: .notice)])
        #expect(s.pending.isEmpty)
    }

    // Two 「继续」 sent one after the other: each tied copy takes its own bubble, never the other's by its words.
    @Test func aTiedItemTakesOnlyItsOwnBubble() {
        let s = store()
        let a = PendingMessage(text: "继续", images: []), b = PendingMessage(text: "继续", images: [])
        s.pending = [a, b]
        s.merge([tagged(6, "继续", b.id)])
        #expect(s.pending.map(\.id) == [a.id])
        // Another item of B's message (a queued copy) finds its bubble gone and takes none: not A's by its words.
        s.merge([tagged(7, "继续", b.id)])
        #expect(s.pending.map(\.id) == [a.id])
        s.merge([tagged(8, "继续", a.id)])
        #expect(s.pending.isEmpty)
    }

    // Older hubs send no tie: the words still settle the bubble.
    @Test func anUntiedItemSettlesByItsWords() {
        let s = store()
        let p = PendingMessage(text: "继续", images: [])
        s.pending = [p]
        s.merge([user(9, "继续", at: Date())])
        #expect(s.pending.isEmpty)
        #expect(PendingMessage.tied(user(9, "继续", at: nil), to: p.id) == nil)
    }

    // The field decodes from the hub's JSON and is absent from an older hub's.
    @Test func theTieDecodes() throws {
        let tied = #"{"id":"a#0","seq":1,"role":"user","kind":"text","text":"好","client_msg_id":"6F9619FF-8B86-D011-B42D-00C04FC964FF"}"#
        let old = #"{"id":"a#0","seq":1,"role":"user","kind":"text","text":"好"}"#
        #expect(try JSONDecoder().decode(Item.self, from: Data(tied.utf8)).clientMsgID == "6F9619FF-8B86-D011-B42D-00C04FC964FF")
        #expect(try JSONDecoder().decode(Item.self, from: Data(old.utf8)).clientMsgID == nil)
    }

    // Outboxes saved before createdAt was kept still load.
    @Test func oldOutboxesDecode() throws {
        let old = #"{"id":"6F9619FF-8B86-D011-B42D-00C04FC964FF","text":"好","images":[],"state":{"waiting":{}}}"#
        let p = try JSONDecoder().decode(PendingMessage.self, from: Data(old.utf8))
        #expect(p.createdAt == nil && p.state == .waiting)
        #expect(PendingMessage.arrived(p, as: user(1, "好", at: Date(timeIntervalSince1970: 0))))
    }
}
