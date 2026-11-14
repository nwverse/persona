import Foundation
import Synchronization

/// Test-only shadow of the iOS continuity adapter. It never selects the production implementation or carries message
/// contents into diagnostics. The fixture harness supplies the same actions to native ChatStore and this shadow.
public struct CoreContinuityShadow: ~Copyable, Sendable {
    public struct Mismatch: Error, Equatable, Sendable {
        public let domain: String
        public let rule: String
        public let inputShape: String
        public init(domain: String = "continuity", rule: String, inputShape: String) {
            self.domain = domain
            self.rule = rule
            self.inputShape = inputShape
        }
    }

    public let client: PersonaCoreClient
    public let chat: String
    private let changes: Mutex<[(String, UInt64)]>
    private let effects: Mutex<[Effect]>

    public init(chat: String) throws {
        self.chat = chat
        let changes = Mutex<[(String, UInt64)]>([])
        let effects = Mutex<[Effect]>([])
        self.changes = changes
        self.effects = effects
        client = try PersonaCoreClient(config: Config(), callbacks: PersonaCoreCallbacks(changedChat: { chat, version in
            changes.withLock { $0.append((chat, version)) }
        }, run: { effect in
            effects.withLock { $0.append(effect) }
        }))
    }

    public func dispatch(_ action: Action) throws { try client.dispatch(action) }
    public func drain() throws -> ChatPatch { try client.drain(chat: chat) }
    public func flush() throws { try client.flush() }
    public func wakeChatIDs() -> [String] { changes.withLock { $0.map(\.0) } }
    public func effectsSnapshot() -> [Effect] { effects.withLock { $0 } }
    public func drainEffects() -> [Effect] { effects.withLock { current in
        defer { current.removeAll() }
        return current
    } }
    public func close() { client.close() }
}
