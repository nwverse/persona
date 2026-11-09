import Foundation
import Testing
@testable import Persona

@MainActor
struct MessageSourceTests {
    private func item(_ text: String = "Update", source: JSONValue? = nil, id: String = "m1") -> Item {
        var item = Item(id: id, seq: 1, ts: nil, role: "user", kind: .text, text: text,
                        toolUseID: nil, toolName: nil, toolInput: nil, isError: nil, image: nil)
        item.source = source
        return item
    }

    private func metadata(_ kind: String = "conversation", id: String = "source-1", title: String = "Release review") -> JSONValue {
        .object(["kind": .string(kind), "conversation_id": .string(id), "title": .string(title)])
    }

    private func relay(_ title: String = "Release &amp; review") -> String {
        """
        <persona-context kind="context" title="来自对话「\(title)」" url="">
        这条消息不是用户本人发的，是 Persona 的另一段对话（source-1）用 conversation_send 发来的，第 1 跳（最多 4 跳）。
        按里面说的做。
        </persona-context>
        Ready for review.
        """ // i18n-ignore: exact legacy relay fixture
    }

    @Test func additiveMetadataSurvivesDecodingAndCacheEncoding() throws {
        let raw = #"{"id":"m","seq":1,"role":"user","kind":"text","text":"Update","source":{"kind":"conversation","conversation_id":"source-1","title":"Review","future":true}}"#
        let decoded = try JSON.decoder.decode(Item.self, from: Data(raw.utf8))
        #expect(MessageSource.of(decoded)?.name == "Review")
        #expect(MessageSource.of(decoded)?.conversationID == "source-1")
        #expect(try JSON.decoder.decode(Item.self, from: JSON.encoder.encode(decoded)).source == decoded.source)
        #expect(ChatCache.folder == "chats-v12")
    }

    @Test func missingMalformedAndFutureFieldsDoNotDropTheItem() throws {
        for value in ["null", "false", "42", "[]", #"{"kind":42,"title":false}"#, #"{"kind":"future","title":42}"#] {
            let raw = #"{"id":"m","seq":1,"role":"user","kind":"text","text":"Update","source":\#(value)}"#
            let decoded = try JSON.decoder.decode(Item.self, from: Data(raw.utf8))
            #expect(decoded.text == "Update")
            if value.contains("future") { #expect(MessageSource.of(decoded)?.kind == "future") }
        }
        #expect(MessageSource.of(item()) == nil)
    }

    @Test func preciseLegacyRelayEnvelopeIsNotUserInput() {
        let message = item(relay())
        #expect(MessageSource.of(message)?.name == "Release & review")
        #expect(MessageSource.body(message) == "Ready for review.")
        #expect(MessageSource.of(item(relay().replacingOccurrences(of: "</persona-context>", with: ""))) == nil)
        #expect(MessageSource.of(item("Please inspect this example:\n" + relay())) == nil)
        #expect(MessageSource.of(item("⏰ Check this later")) == nil)
        #expect(MessageSource.of(item("📋 Review my notes")) == nil)
        var typed = message
        typed.clientMsgID = "sent-by-app"
        #expect(MessageSource.of(typed) == nil)
        typed.clientMsgID = nil
        typed.from = "Peer"
        #expect(MessageSource.of(typed) == nil)
        typed.from = nil
        typed.role = "assistant"
        #expect(MessageSource.of(typed) == nil)
    }

    @Test func legacySchedulingAndSupervisionStayOnSourceSide() {
        let scheduled = item("⏰ Morning review（定时任务，每 30 分钟）\n\nReview build status.") // i18n-ignore: exact legacy schedule fixture
        #expect(MessageSource.of(scheduled)?.kind == "followup")
        #expect(MessageSource.body(scheduled) == "Review build status.")
        let supervision = item("""
        📋 督办：你派出去的任务有 1 个要你处理
        （这是 Persona 的督办消息，不是用户发的。这一轮默认折叠。）
        <persona-supervision level="triage" />
        1. Check the test report.
        """) // i18n-ignore: exact legacy supervision fixture
        #expect(MessageSource.of(supervision)?.kind == "supervision")
        #expect(MessageSource.body(supervision) == "1. Check the test report.")
        #expect(MessageSource.of(item("📋 督办：你派出去的任务有 1 个要你处理\nMy own notes")) == nil) // i18n-ignore: partial legacy envelope
    }

    @Test func structuredSourceWinsOverTextHeuristics() {
        var message = item(relay(), source: metadata("supervision", title: "Patrol"))
        message.clientMsgID = "runtime-generated"
        #expect(MessageSource.of(message)?.kind == "supervision")
        #expect(MessageSource.of(message)?.name == "Patrol")
        message.source = .object(["kind": .string("user")])
        #expect(MessageSource.of(message) == nil)
    }

    @Test func onlyAdjacentIdenticalSourceIdentitiesShareTheirLabel() {
        let a = item(source: metadata(), id: "a")
        let b = item(source: metadata(title: "Renamed review"), id: "b")
        let c = item(source: metadata(id: "source-2"), id: "c")
        let own = item(id: "own")
        let lines = ChatLines.sourceRuns([.row(.user(a)), .row(.user(b)), .row(.user(c)), .row(.user(own)), .row(.user(a))])
        #expect(lines.map(\.showsSource) == [true, false, true, true, true])
        #expect(EdgeScrubState.marks(lines).map(\.id) == ["own"])
        let separated = ChatLines.sourceRuns([.row(.user(a)), .row(.assistant(own)), .row(.user(b))])
        #expect(separated.last?.showsSource == true)
    }

    @Test func watchPreservesProvenanceAndStripsTheModelEnvelope() {
        let result = WatchMessage.latest([item(relay())])
        #expect(result.first?.source?.kind == "conversation")
        #expect(result.first?.text == "Ready for review.")
    }

    @Test func aRelayCannotConsumeAnUnrelatedPendingMessage() {
        let external = item("Update", source: metadata())
        #expect(!PendingMessage.arrived(PendingMessage(text: "Update", images: [], createdAt: nil), as: external))
    }
}
