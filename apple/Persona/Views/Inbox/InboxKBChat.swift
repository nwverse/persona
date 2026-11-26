import Foundation
import Observation
import PersonaWire
import SwiftUI

// An Inbox item's knowledge base chat (#15356, docs/assistant/knowledge.md "A chat's Inbox items"), as the web's
// InboxKBChat.tsx: the item's own read (GET /api/v1/inbox/{id}) names it as `kb_chat`, and the item's More menu gets
// Knowledge Page, which opens that chat's page as a link to it does. Asked once per item a detail shows; an item with
// no chat, of another assistant than the knowledge pages read, or from a hub before it shows nothing.

/// The knowledge base chat an Inbox item came from, as its read named it: `id` and its page's path.
nonisolated struct InboxKBChat: Decodable, Hashable, Sendable {
    var id: String
    var path: String?
}

extension APIClient {
    /// The knowledge base chat item `id` came from (`kb_chat` of GET /api/v1/inbox/{id}): its id, nil when it has none,
    /// or the hub is from before it.
    func inboxKBChat(_ id: String) async throws -> String? {
        struct Read: Decodable {
            var kbChat: InboxKBChat?
            enum CodingKeys: String, CodingKey { case kbChat = "kb_chat" }
        }
        let read: Read = try await get("/api/v1/inbox/" + id)
        return read.kbChat.flatMap { $0.id.isEmpty ? nil : $0.id }
    }
}

/// Each Inbox item's knowledge base chat, by the item's key (`InboxItem.key`), as its read said: asked once per item and
/// kept for the run; a read that failed is asked again the next time the item opens.
@MainActor @Observable
final class InboxKBChats {
    static let shared = InboxKBChats { item in
        // Not connected: nothing is known, and the next opening asks again.
        guard let api = AppModel.shared.api?.manager(AppModel.shared.itemManager(item.manager)) else { throw CancellationError() }
        return try await api.inboxKBChat(item.id)
    }

    /// Item key → its chat's id, "" for none.
    private(set) var chats: [String: String] = [:]
    @ObservationIgnored private var asking: Set<String> = []
    private let read: @MainActor (InboxItem) async throws -> String?

    /// `read`: the item's chat, as its read names it (nil: none).
    init(read: @escaping @MainActor (InboxItem) async throws -> String?) {
        self.read = read
    }

    /// Only an item of a chat, a mail correspondent or a connector's thread can have one (the others are not asked).
    nonisolated static func asks(_ item: InboxItem) -> Bool { item.lark != nil || item.mail != nil || item.connector != nil }

    /// The chat item came from, once its read said so; nil for none, or not said yet.
    func chat(of item: InboxItem) -> String? {
        guard let id = chats[item.key], !id.isEmpty else { return nil }
        return id
    }

    /// Reads which chat item came from, unless that is known or being asked.
    func load(_ item: InboxItem) async {
        let key = item.key
        guard Self.asks(item), chats[key] == nil, !asking.contains(key) else { return }
        asking.insert(key)
        defer { asking.remove(key) }
        do {
            chats[key] = try await read(item) ?? ""
        } catch {
            // Not known: the next time the item opens asks again.
        }
    }
}

/// Knowledge Page in an opened item's More menu: its chat's page in the knowledge base, which is the current assistant's.
/// Nothing for an item whose read names no chat, or of another assistant.
struct InboxKBChatButton: View {
    var item: InboxItem
    @Environment(AppModel.self) private var model

    var body: some View {
        if let chat = InboxKBChats.shared.chat(of: item), model.assistantScope.has(item.manager) {
            Button { model.openKBChat(chat) } label: { Label("Knowledge Page", systemImage: Theme.Icon.knowledge) }
                .accessibilityIdentifier("inbox.detail.kbChat")
        }
    }
}
