import Foundation
import Testing
@testable import Persona

/// What the inbox's source page opens (docs/assistant/inbox.md, "Plugin-rendered details"): the item's own chat block
/// sends it to the conversation, and nothing else does. The rule is the block, not the source's name: a document or a
/// note from the same connector keeps today's slot, so no extension's page goes away because of where its item came
/// from, and a chat that a connector carries for bookkeeping still opens as a chat.
@MainActor
struct InboxSourceSlotScopeTests {
    private func item(source: String, kind: String = "doc", _ blocks: String) throws -> InboxItem {
        try JSON.decoder.decode(InboxItem.self, from: Data("""
        {"id":"inbox-1","source":"\(source)","kind":"\(kind)","account":"a","account_name":"Example",
         "title":"An item","preview":"","count":1,\(blocks)}
        """.utf8))
    }

    @Test func aDocumentFromTheSameSourceKeepsTheSlot() throws {
        // A mention on a document in the source whose chats open directly: no `lark` block, so today's page.
        let doc = try item(source: "lark", #""doc":{"url":"https://example.com/base","type":"base","notice":"mention"}"#)
        #expect(doc.larkConversation == nil)
        #expect(InboxSourcePage.Selection.of(item: doc) == .slot(.document))

        // A chain position names a message in a chat: it changes no document's page.
        #expect(InboxSourcePage.Selection.of(item: doc, position: "42") == .slot(.document))
    }

    @Test func aConnectorsChatItemOpensTheChat() throws {
        // A migrated connector's chat item carries the connector for bookkeeping and the chat for the page.
        let chat = try item(source: "examplechat", kind: "group", #"""
        "lark":{"feed_id":"feed-1","is_group":true,"start_position":"7"},
        "connector":{"thread":"thread-1","label":"Example"}
        """#)
        let conversation = try #require(chat.larkConversation)
        #expect(InboxSourcePage.Selection.of(item: chat) == .chat(conversation))

        var positioned = conversation
        positioned.focus = "9"
        #expect(InboxSourcePage.Selection.of(item: chat, position: "9") == .chat(positioned))
    }

    @Test func anItemWithoutABlockKeepsTheEmptyPage() throws {
        let bare = try item(source: "examplechat", kind: "note", "")
        #expect(InboxSourcePage.Selection.of(item: bare) == .slot(.empty))
    }
}
