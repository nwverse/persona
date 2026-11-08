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
    /// The main chat's hand-off being tried, by its ask: the same ask sent again sends the first text again under the same
    /// client id, whatever came in on the item since, and the hub and the manager send it once. It stays through a failure
    /// that says nothing about a try under that id (`saysNothing`) and, once a try may have gone (`mayHaveGone`), through
    /// every answer short of success, a missing route included. Another ask or a reopened sheet is a new hand-off.
    @ObservationIgnored private var attempt: Attempt?
    @ObservationIgnored private let itemID: String
    @ObservationIgnored private let deps: Deps

    private struct Attempt {
        var ask: String
        var text: String
        var id: UUID
        /// A try under `id` may have gone.
        var inDoubt = false
    }

    /// A failed retry of a hand-off already in doubt: whatever it was answered, a try before it under the same client id
    /// may have gone, and the same ask sent again, with no other ask handed to the main chat in between, keeps that id.
    nonisolated private struct InDoubt: Error {}

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
                    let client = try api()
                    let s = try await AssistantStart.run(receipts: model.hubFeatures.contains(AssistantStart.feature)) { op in
                        try await client.startConversation(prompt: text, main: true, opID: op)
                    }
                    model.began(s, text: text)
                    key = s.key
                    Task { await model.refreshAssistant() }
                }
                model.openAssistantConversation(key)
            },
            startNew: { text in
                let client = try api()
                let s = try await AssistantStart.run(receipts: model.hubFeatures.contains(AssistantStart.feature)) { op in
                    try await client.startConversation(prompt: text, opID: op)
                }
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
        let text: String
        if target == .main, let pending = attempt, pending.ask == ask {
            text = pending.text
        } else {
            do {
                text = InboxHandOffSheet.message(try await deps.load(itemID), ask: ask)
            } catch {
                // Nothing went yet: it never says to look in the chat.
                self.error = error.isOffline || Self.mayHaveGone(error) ? String(localized: "Can't reach the hub") : error.shown
                return false
            }
        }
        do {
            switch target {
            case .main:
                try await handOff(ask: ask, text: text)
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
    /// on them: a send that may have gone says to look first, since a new try may type it twice; no network at all says so.
    static func words(for error: Error) -> String? {
        if error.isCancellation { return nil }
        if mayHaveGone(error) {
            let look = String(localized: "It may have reached the computer already. Check the chat before sending it again.")
            guard error is InDoubt else { return look }
            return look + "\n" + String(localized: "To send it anyway, change the instructions or open Hand to AI again.")
        }
        if error.isOffline { return String(localized: "Can't reach the hub") }
        return error.shown
    }

    /// A failed send whose text may still have reached the conversation: no answer (a timeout here or behind the hub, the
    /// connection lost after the request went out, a 5xx without a `code` from something in front of the hub or from the
    /// hub unable to keep its receipt), the hub's pending or unknown receipt, or an answer saying so (`assistant_in_doubt`,
    /// `delivered: unknown` or `yes`, the node lost without `delivered: none`). The sheet says to look first, and the same
    /// ask again is the same hand-off.
    private static func mayHaveGone(_ error: Error) -> Bool {
        if error is InDoubt { return true }
        if error.isCancellation { return false }
        if (error as? URLError)?.code == .networkConnectionLost || error.isTimeout { return true }
        guard let api = error as? APIError else { return false }
        let code = api.code, delivered = api.fields["delivered"]
        return (api.status >= 500 && code == nil && delivered == nil) || code == "timeout" || code == "assistant_in_doubt"
            || delivered == "unknown" || delivered == "yes" || code == OpReceipt.unknownCode || code == OpReceipt.pendingCode
            || (code == "node_unreachable" && delivered != "none")
    }

    /// A failure that says nothing about an earlier try under the same client id: no connection to the hub, a cancelled
    /// request or no hub at all, the manager offline (`assistant_offline`, whose receipt the hub drops).
    private static func saysNothing(_ error: Error) -> Bool {
        error.isCancellation || (error.isOffline && !mayHaveGone(error)) || (error as? APIError)?.code == "assistant_offline"
    }

    private func handOff(ask: String, text: String) async throws {
        let tried = attempt.flatMap { $0.ask == ask ? $0 : nil } ?? Attempt(ask: ask, text: text, id: UUID())
        attempt = tried
        let to: SessionKey
        do {
            to = try await deps.handOff(itemID, text, tried.id)
        } catch let e as APIError where e.status == 404 || e.status == 405 {
            // After a try that may have gone, the manager may have lost the item since: look first, and keep the hand-off
            // so the same ask never sends it as a plain message as well.
            if tried.inDoubt { throw InDoubt() }
            // An older hub or manager: the text goes to the main chat as a plain message, with nothing to write back to.
            do {
                try await deps.plainToMain(text)
            } catch {
                settle(tried, after: error)
                throw error
            }
            attempt = nil
            if markDone { deps.markDone() }
            return
        } catch let error where Self.saysNothing(error) {
            throw error
        } catch {
            // After a try that may have gone, an answer under its id may be what the hub kept for the id, a relayed answer
            // cut off on the way (a bare `internal`) or, after a receipt the hub dropped (`assistant_in_doubt`, which may
            // never have reached the phone), a new run's answer. The phone can't tell which of these it got, so none is
            // taken as final.
            let answer: Error = tried.inDoubt ? InDoubt() : error
            settle(tried, after: answer)
            throw answer
        }
        attempt = nil
        deps.openMain(to, text, tried.id)
        if markDone { deps.markDone() }
    }

    /// After a failed try: kept in doubt while it may have gone, else over.
    private func settle(_ tried: Attempt, after error: Error) {
        guard Self.mayHaveGone(error) else {
            attempt = nil
            return
        }
        var kept = tried
        kept.inDoubt = true
        attempt = kept
    }
}
