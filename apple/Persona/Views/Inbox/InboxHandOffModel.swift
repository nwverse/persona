import Foundation
import Observation

/// Hand to AI's logic (docs/assistant/inbox.md "Item actions", #1194): where the item goes, whether it is filed with it,
/// and the send. Persona's main conversation is the default: the item's hand-off (`POST /api/v1/inbox/{id}/handoff`)
/// puts the item and the ask there and links the item to it, so that conversation may write back to the item. An older
/// hub or manager answers 404 / 405, and the main chat gets the text as before. A new Persona conversation and a running
/// Agent get it as a message. `InboxHandOffSheet` draws it.
@MainActor @Observable
final class InboxHandOffModel {
    enum Target: Hashable, Sendable {
        /// Persona's main conversation, through the item's hand-off.
        case main
        /// A new Persona conversation.
        case newChat
        case agent(SessionKey)
    }

    struct Deps {
        /// The item with its conversation text (`GET /api/v1/inbox/{id}`).
        var load: (String) async throws -> InboxItem
        /// The main chat's hand-off (item id, text, client id): the conversation it went to.
        var handOff: (String, String, UUID) async throws -> SessionKey
        /// Opens that conversation on the message as sent under the client id, which its transcript copy carries.
        var openMain: (SessionKey, String, UUID) -> Void
        /// An older hub's main chat: sends the text there as a plain message (starting the conversation when there is
        /// none) and opens it.
        var plainToMain: (String) async throws -> Void
        /// Starts a new Persona conversation with the text and opens it.
        var startNew: (String) async throws -> Void
        /// Sends the text to an Agent and opens its chat.
        var sendToAgent: (SessionKey, String) -> Void
        /// Files the item.
        var markDone: () -> Void
    }

    var target: Target = .main {
        didSet { if !doneChosen { markDone = Self.marksDone(target) } }
    }
    /// Also Mark as Done: off for the main chat, which may still write back to the item and file it there; on for the
    /// others, which never do. Switching targets follows that until the user sets it.
    private(set) var markDone = false
    private(set) var sending = false
    /// Why the last send failed.
    private(set) var error: String?

    @ObservationIgnored private var doneChosen = false
    /// The main chat's hand-off being tried: the same text sent again after no answer, while the hub still runs it
    /// (its receipt says pending), after the hub said it may have gone (`assistant_in_doubt`) or after the manager never
    /// got it (`assistant_offline`) goes with the same client id, which the manager sends once. The hub keeps any other
    /// answer under that id and gives it back for every retry, so the next try takes a new one.
    @ObservationIgnored private var attempt: (text: String, id: UUID)?
    @ObservationIgnored private let itemID: String
    @ObservationIgnored private let deps: Deps

    init(itemID: String, deps: Deps) {
        self.itemID = itemID
        self.deps = deps
    }

    /// The app's own: the hub of the moment, its Persona and the inbox store.
    static func live(_ item: InboxItem, model: AppModel) -> InboxHandOffModel {
        // Without a hub nothing happens and nothing is said.
        func api() throws -> APIClient {
            guard let api = model.api else { throw CancellationError() }
            return api
        }
        return InboxHandOffModel(itemID: item.id, deps: Deps(
            load: { try await api().inboxItem($0) },
            handOff: { id, text, clientID in
                let answer = try await api().inboxHandoff(id, text: text, clientID: clientID)
                if let it = answer.item { InboxStore.shared.put(it) }
                return answer.session
            },
            openMain: { key, text, clientID in
                model.showSent(key, text: text, images: [], id: clientID)
                model.openAssistantConversation(key)
                Task { await model.refreshAssistant() } // the hand-off may have started the main conversation
            },
            plainToMain: { text in
                let key: SessionKey
                if let main = model.assistantMain, model.session(main)?.canSend == true {
                    model.sendNow(main, text: text) // a failure shows in the conversation, with Retry
                    key = main
                } else if let main = model.assistantMain { // sendNow does nothing for a conversation not listed
                    try await api().send(main, text: text)
                    key = main
                } else {
                    let s = try await api().startConversation(prompt: text, main: true)
                    model.began(s, text: text)
                    key = s.key
                    Task { await model.refreshAssistant() }
                }
                model.openAssistantConversation(key)
            },
            startNew: { text in
                let s = try await api().startConversation(prompt: text)
                model.began(s, text: text)
                model.assistantCurrent = s.key
                model.selectedTab = .assistant
            },
            sendToAgent: { key, text in
                model.sendNow(key, text: text)
                model.openChat = key
                model.selectedTab = .chats
            },
            markDone: { InboxStore.shared.setDone(item, true, haptic: false) }))
    }

    static func marksDone(_ target: Target) -> Bool { target != .main }

    func setMarkDone(_ on: Bool) {
        markDone = on
        doneChosen = true
    }

    /// Sends `ask` after the item as context; true once it went, and the sheet closes.
    func send(ask: String) async -> Bool {
        guard !sending else { return false }
        sending = true
        error = nil
        defer { sending = false }
        do {
            let text = InboxHandOffSheet.message(try await deps.load(itemID), ask: ask)
            switch target {
            case .main:
                try await handOff(text)
                return true
            case .newChat:
                try await deps.startNew(text)
            case .agent(let key):
                deps.sendToAgent(key, text)
            }
            if markDone { deps.markDone() }
            return true
        } catch {
            self.error = Self.words(for: error)
            return false
        }
    }

    /// What the sheet says for a failed send. Timeouts and no network are quiet elsewhere, but here the sheet stays open
    /// on them: a send that may have gone says to look first, since a new try may type it twice (a connection lost midway
    /// counts as offline but may have carried the request); no network at all says so.
    static func words(for error: Error) -> String? {
        if error.isCancellation { return nil }
        let api = error as? APIError, code = api?.code, delivered = api?.fields["delivered"]
        if (error as? URLError)?.code == .networkConnectionLost || error.isTimeout || code == "timeout" || delivered == "unknown"
            || code == OpReceipt.unknownCode || code == OpReceipt.pendingCode || (code == "node_unreachable" && delivered != "none") {
            return String(localized: "It may have reached the computer already. Check the chat before sending it again.")
        }
        if error.isOffline { return String(localized: "Can't reach the hub") }
        return error.shown
    }

    private func handOff(_ text: String) async throws {
        let id = attempt.flatMap { $0.text == text ? $0.id : nil } ?? UUID()
        attempt = (text, id)
        let to: SessionKey
        do {
            to = try await deps.handOff(itemID, text, id)
        } catch let e as APIError where e.status == 404 || e.status == 405 {
            attempt = nil
            // An older hub or manager: the text goes to the main chat as a plain message, with nothing to write back to.
            try await deps.plainToMain(text)
            if markDone { deps.markDone() }
            return
        } catch {
            if !Self.mayHaveGone(error) { attempt = nil }
            throw error
        }
        attempt = nil
        deps.openMain(to, text, id)
        if markDone { deps.markDone() }
    }

    private static func mayHaveGone(_ error: Error) -> Bool {
        if error.isGap { return true }
        let code = (error as? APIError)?.code
        return code == "assistant_in_doubt" || code == "assistant_offline"
    }
}
