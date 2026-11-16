#if DEBUG
import Foundation
import OSLog
import PersonaCore
import PersonaWire
import Synchronization

/// The single process-wide DEBUG continuity mirror. One private queue serializes JSON, FFI and comparisons for every
/// chat; one Rust worker serves them all. Native results are already final when an immutable input arrives. No method
/// waits for this queue, and core effects never reach native transport.
nonisolated final class ProductionContinuityShadow: @unchecked Sendable {
    struct Report: Codable, Hashable, Sendable {
        let domain: String
        let rule: String
        let inputShape: String
    }

    static let shared = ProductionContinuityShadow { report in
        // A diagnostic is deduplicated on the shadow queue before this existing disk queue is used.
        Task.detached(priority: .utility) {
            ErrorReportQueue.shared.write(ErrorReports.report(
                kind: "log", level: "warning",
                message: "domain=\(report.domain) rule=\(report.rule) input=\(report.inputShape)"))
        }
    }
    private static let logger = Logger(subsystem: "Persona.Diagnostics", category: "ContinuityShadow")

    private enum RequestKind: Hashable, Sendable { case after, latest, older, subscribe, range }

    private struct Chat {
        var items: [Item] = []
        var requests: [RequestKind: Int] = [:]
        var olderRequested = false
        var pendingRequests: [Int: (kind: RequestKind, shape: String)] = [:]
        var compared = 0
        var mismatches = 0
    }

    private struct Input: Sendable {
        let snapshot: NativeContinuitySnapshot
        let value: NativeContinuityInput
        let native: NativeContinuityResult
        let gap: ChatStore.Gap?
        let fold: Item?
    }

    private struct WeakChatStore {
        weak var store: ChatStore?
    }

    private final class Effects: @unchecked Sendable {
        struct Entry {
            let id: UInt
            let effect: Effect
        }
        let values = Mutex<[Entry]>([])
    }

    private let queue = DispatchQueue(label: "Persona.ContinuityShadow", qos: .utility)
    private let effects = Effects()
    private let enabled = Mutex(true)
    private var client: PersonaCoreClient?
    // Everything below belongs to `queue`. MainActor never reads or mutates identity or core state.
    private var chats: [UInt: Chat] = [:]
    private var failedChats: Set<UInt> = []
    private var generations: [ObjectIdentifier: UInt] = [:]
    private var stores: [ObjectIdentifier: WeakChatStore] = [:]
    private var retiredThrough: UInt = 0
    private var nextGeneration: UInt = 1
    private var nextEffect = 1
    private var reportSink: @Sendable (Report) -> Void
    private var mismatches = 0
    private var reports: [Report: Int] = [:]

    init(reportSink: @escaping @Sendable (Report) -> Void) {
        self.reportSink = reportSink
    }

    @MainActor func install() {
        #if os(iOS)
        install(enabled: UserDefaults.standard.object(forKey: "showContinuityShadow") as? Bool ?? true)
        #else
        install(enabled: true)
        #endif
    }

    /// Production adapter tests explicitly require a real comparison; they do not inherit the simulator's A/B opt-out.
    @MainActor func installForTests() { install(enabled: true) }

    @MainActor private func install(enabled: Bool) {
        self.enabled.withLock { $0 = enabled }
        guard enabled else {
            enqueue { [weak self] in self?.removeAllChats() }
            return
        }
        ChatStore.installContinuityShadowForTests { [weak self] store, input, observation in
            self?.accept(input, into: store, snapshot: observation.snapshot, native: observation.native, gap: observation.gap)
        }
    }

    @MainActor func accept(_ input: NativeContinuityInput, into store: ChatStore, native: NativeContinuityResult,
                gap: ChatStore.Gap?) {
        accept(input, into: store, snapshot: store.nativeSnapshot(), native: native, gap: gap)
    }

    @MainActor func accept(_ input: NativeContinuityInput, into store: ChatStore, snapshot: NativeContinuitySnapshot,
                native: NativeContinuityResult, gap: ChatStore.Gap?) {
        guard enabled.withLock({ $0 }) else { return }
        let value = Input(snapshot: snapshot, value: input, native: native, gap: gap, fold: input.fold)
        // The immutable input captures content for this queued comparison; identity is assigned only on this queue.
        enqueue { [weak self, weak store] in
            guard let self, let store, let id = self.generation(for: store) else { return }
            self.process(id, input: value)
        }
    }

    private func generation(for store: ChatStore) -> UInt? {
        let pointer = ObjectIdentifier(store)
        if let id = generations[pointer], stores[pointer]?.store === store {
            return id > retiredThrough && !failedChats.contains(id) ? id : nil
        }
        if let old = generations.removeValue(forKey: pointer) {
            retire(old)
        }
        let id = nextGeneration
        nextGeneration += 1
        generations[pointer] = id
        stores[pointer] = WeakChatStore(store: store)
        return id
    }

    private func retire(_ id: UInt) {
        retiredThrough = max(retiredThrough, id)
        removeChat(id)
    }

    private func start(_ id: UInt, snapshot: NativeContinuitySnapshot) throws {
        guard chats[id] == nil else { return }
        chats[id] = Chat()
        try core().dispatch(.openChat(chat: chatID(id), snapshot: Self.snapshot(snapshot)))
        try flush(id, shape: "kind=snapshot items=\(snapshot.items.count)")
        try applyInitialSnapshot(id, snapshot: snapshot)
    }

    @MainActor func loadOlder(_ store: ChatStore) {
        let snapshot = store.nativeSnapshot()
        enqueue { [weak self, weak store] in
            guard let self, let store, let id = self.generation(for: store) else { return }
            do {
                try self.start(id, snapshot: snapshot)
                try self.core().dispatch(.chat(chat: self.chatID(id), event: .loadOlder))
                try self.flush(id, shape: "kind=loadOlder items=0")
                try self.compareQueued(id, shape: "kind=loadOlder items=0")
            } catch { self.fail(id, rule: "older-request", shape: "kind=loadOlder items=0") }
        }
    }

    @MainActor func failed(_ kind: NativeContinuityInput.Kind, in store: ChatStore, fold: Item? = nil) {
        let snapshot = store.nativeSnapshot()
        let event: ChatEvent = switch kind {
        case .fillFailed: .fillFailed
        case .olderFailed: .olderFailed
        default: .rangeFailed(fold_id: fold?.id)
        }
        enqueue { [weak self, weak store] in
            guard let self, let store, let id = self.generation(for: store) else { return }
            do {
                try self.start(id, snapshot: snapshot)
                try self.core().dispatch(.chat(chat: self.chatID(id), event: event))
                try self.flush(id, shape: "kind=\(kind.rawValue) items=0")
                try self.compareQueued(id, shape: "kind=\(kind.rawValue) items=0")
            } catch { self.fail(id, rule: "failed-input", shape: "kind=\(kind.rawValue) items=0") }
        }
    }

    @MainActor func close(_ store: ChatStore) {
        let pointer = ObjectIdentifier(store)
        enqueue { [weak self] in
            guard let self, let id = self.generations.removeValue(forKey: pointer) else { return }
            self.stores.removeValue(forKey: pointer)
            self.retire(id)
        }
    }

    @MainActor func closeAll() {
        enqueue { [weak self] in self?.removeAllChats() }
    }

    private func removeAllChats() {
        for (_, id) in generations { retire(id) }
        for id in chats.keys { removeChat(id) }
        for id in failedChats { removeChat(id) }
        generations.removeAll()
        stores.removeAll()
        effects.values.withLock { $0.removeAll() }
    }

    func compare(_ store: ChatStore) async throws -> (compared: Int, mismatches: Int) {
        try await withCheckedThrowingContinuation { resume in
            enqueue { [weak self, weak store] in
                guard let self else { resume.resume(throwing: PersonaCoreError.closed); return }
                guard let store, let id = self.generation(for: store) else {
                    resume.resume(throwing: PersonaCoreError.invalidPatch)
                    return
                }
                do {
                    try self.core().flush(timeoutMilliseconds: 100)
                    try self.compareQueued(id, shape: "kind=test-barrier items=0")
                    resume.resume(returning: (self.chats[id]?.compared ?? 0, self.chats[id]?.mismatches ?? 0))
                } catch {
                    self.fail(id, rule: "core-flush", shape: "kind=test-barrier items=0")
                    resume.resume(throwing: error)
                }
            }
        }
    }

    private func enqueue(work: @escaping @Sendable () -> Void) { queue.async(execute: work) }

    private func process(_ id: UInt, input: Input) {
        do {
            try start(id, snapshot: input.snapshot)
            try core().dispatch(try Self.action(id, input: input))
            try flush(id, shape: Self.shape(input.value))
            try compareQueued(id, input: input, shape: Self.shape(input.value))
        } catch {
            fail(id, rule: "core-input", shape: Self.shape(input.value))
        }
    }

    private func flush(_ id: UInt, shape: String) throws {
        do { try core().flush(timeoutMilliseconds: 16) }
        catch PersonaCoreError.flushTimedOut {
            // A timeout says nothing about continuity correctness. Leave this queued comparison behind; the action's
            // worker completion or the test's bounded barrier can finish it without blocking the main actor.
            report(id, rule: "core-timeout", shape: shape)
            throw PersonaCoreError.flushTimedOut
        }
    }

    private func applyInitialSnapshot(_ id: UInt, snapshot: NativeContinuitySnapshot) throws {
        let patch = try core().drain(chat: chatID(id))
        guard var chat = chats[id] else { return }
        try Self.apply(patch, to: &chat)
        chats[id] = chat
        if chat.items != snapshot.items || patch.covered != snapshot.covered
            || patch.history != snapshot.history || (patch.hasMore ?? false) != snapshot.hasMore
            || patch.loadedOnce != snapshot.loadedOnce {
            report(id, rule: "initial-snapshot", shape: "kind=snapshot items=\(snapshot.items.count)")
        }
    }

    private func core() throws -> PersonaCoreClient {
        if let client { return client }
        let client = try PersonaCoreClient(config: Config(), callbacks: PersonaCoreCallbacks(changedChat: { [weak self] chat, _ in
            guard let id = Self.identifier(chat) else { return }
            self?.enqueue { [weak self] in self?.drainChanged(id) }
        }, run: { [weak self, effects] effect in
            // Rejections carry no chat identity. Do not attribute one to whichever chat happens to drain next.
            guard case .fetch(let chat, _) = effect, let id = Self.identifier(chat) else {
                self?.enqueue { [weak self] in self?.reportGlobal("unknown-effect") }
                return
            }
            effects.values.withLock { $0.append(.init(id: id, effect: effect)) }
        }))
        self.client = client
        return client
    }

    private func drainChanged(_ id: UInt) {
        // A changed wake is informative only: process already applies each input's patch in order on this queue.
    }

    private func compareQueued(_ id: UInt, input: Input? = nil, shape: String) throws {
        guard var chat = chats[id] else { return }
        let patch = try core().drain(chat: chatID(id))
        if input != nil, patch.version == 0, !patch.ops.isEmpty {
            report(id, rule: "patch-state", shape: shape)
            return
        }
        try Self.apply(patch, to: &chat)
        chats[id] = chat
        defer { chats[id]?.compared += 1 }
        if let input {
            let expected = input.native
            if chat.items != expected.items { report(id, rule: "items", shape: shape) }
            if patch.covered != expected.covered { report(id, rule: "covered", shape: shape) }
            if patch.history != expected.history { report(id, rule: "history", shape: shape) }
            if (patch.hasMore ?? false) != expected.hasMore { report(id, rule: "has-more", shape: shape) }
            if patch.loadedOnce != expected.loadedOnce { report(id, rule: "loaded-once", shape: shape) }
        }
        let pending = effects.values.withLock { current in
            let mine = current.filter { $0.id == id }
            current.removeAll { $0.id == id }
            return mine
        }
        var currentRequests: Set<RequestKind> = []
        for entry in pending {
            guard let kind = Self.requestKind(entry.effect) else { continue }
            chats[id]?.requests[kind, default: 0] += 1
            if input != nil { currentRequests.insert(kind) }
            if kind == .older { chats[id]?.olderRequested = true }
        }
        if let input {
            let needed: RequestKind? = switch input.value.kind {
            case .latest, .pushed:
                switch input.gap {
                case .fill: .after
                case .reload: .latest
                default: nil
                }
            case .older: chats[id]?.olderRequested == true ? nil : .older
            case .loadOlder: .older
            case .reconnected: .subscribe
            case .openFold: .range
            default: nil
            }
            if input.value.kind == .older, chats[id]?.olderRequested == true {
                let requests = chats[id]?.pendingRequests.filter { $0.value.kind != .older } ?? [:]
                chats[id]?.pendingRequests = requests
            }
            if let needed, !currentRequests.contains(needed), !hasPendingRequest(id, needed) {
                chats[id]?.pendingRequests[nextEffect] = (needed, shape)
                nextEffect += 1
            }
        }
        // `nextEffect` marks the next observation, not an assumed core request. Every requested category must have
        // arrived by then; a category used by an older, unrelated input does not satisfy this evidence.
        for (serial, expected) in chats[id]?.pendingRequests ?? [:] where serial < nextEffect {
            chats[id]?.pendingRequests.removeValue(forKey: serial)
            report(id, rule: "missing-request", shape: expected.shape)
        }
        if (chats[id]?.compared ?? 0).isMultiple(of: 10) {
            Self.logger.debug("continuity shadow compared=\(self.chats[id]?.compared ?? 0, privacy: .public) mismatches=\(self.mismatches, privacy: .public)")
        }
    }

    private static func apply(_ patch: ChatPatch, to chat: inout Chat) throws {
        for operation in patch.ops {
            switch operation {
            case .remove(let index):
                guard index < chat.items.count else { throw PersonaCoreError.invalidPatch }
                chat.items.remove(at: Int(index))
            case .insert(let index, let id, let json):
                guard index <= chat.items.count else { throw PersonaCoreError.invalidPatch }
                let item = try itemJSON(json)
                guard item.id == id else { throw PersonaCoreError.invalidPatch }
                chat.items.insert(item, at: Int(index))
            case .update(let index, let id, let json):
                guard index < chat.items.count else { throw PersonaCoreError.invalidPatch }
                let item = try itemJSON(json)
                guard item.id == id, chat.items[Int(index)].id == id else { throw PersonaCoreError.invalidPatch }
                chat.items[Int(index)] = item
            }
        }
    }

    private static func requestKind(_ effect: Effect) -> RequestKind? {
        guard case .fetch(_, let request) = effect else { return nil }
        return switch request {
        case .fetchAfter: .after
        case .fetchLatest: .latest
        case .fetchOlder: .older
        case .subscribe: .subscribe
        case .fetchRange: .range
        }
    }

    private func hasPendingRequest(_ id: UInt, _ kind: RequestKind) -> Bool {
        chats[id]?.pendingRequests.values.contains { $0.kind == kind } == true
    }

    private func removeChat(_ id: UInt) {
        generations = generations.filter { $0.value != id }
        effects.values.withLock { $0.removeAll { $0.id == id } }
        chats.removeValue(forKey: id)
        // Closing an unknown chat is intentional: a partial start must not leave old Rust state behind.
        try? core().dispatch(.closeChat(chat: chatID(id)))
        Self.logger.debug("continuity shadow closed mismatches=\(self.mismatches, privacy: .public)")
    }

    private func fail(_ id: UInt, rule: String, shape: String) {
        guard !failedChats.contains(id) else { return }
        failedChats.insert(id)
        report(id, rule: rule, shape: shape)
        removeChat(id)
    }

    private func report(_ id: UInt, rule: String, shape: String) {
        mismatches += 1
        chats[id]?.mismatches += 1
        let value = Report(domain: "continuity", rule: rule, inputShape: shape)
        Self.logger.error("continuity mismatch domain=\(value.domain, privacy: .public) rule=\(value.rule, privacy: .public) input=\(value.inputShape, privacy: .public)")
        let count = reports[value, default: 0] + 1
        reports[value] = count
        // Identical persistent mismatches cannot flood or evict the app's unrelated crash diagnostics.
        if count <= 3 { reportSink(value) }
    }

    private func reportGlobal(_ rule: String) {
        report(0, rule: rule, shape: "kind=core items=0")
    }

    private func chatID(_ id: UInt) -> String { String(id, radix: 16) }

    private static func identifier(_ chat: String) -> UInt? { UInt(chat, radix: 16) }

    static func encode(_ items: [Item]) throws -> String {
        guard let value = String(data: try JSON.encoder.encode(items), encoding: .utf8) else { throw PersonaCoreError.invalidPatch }
        return value
    }

    private static func itemJSON(_ json: String) throws -> Item { try JSON.decoder.decode(Item.self, from: Data(json.utf8)) }

    static func snapshot(_ native: NativeContinuitySnapshot) throws -> ChatSnapshot {
        ChatSnapshot(itemsJson: try encode(native.items), history: native.history, covered: Int64(native.covered),
                     hasMore: native.hasMore, loadedOnce: native.loadedOnce)
    }

    static func batch(_ input: NativeContinuityInput) throws -> WireBatch {
        WireBatch(itemsJson: try encode(input.items), history: input.span.history,
                  fromSeq: input.span.from.map(Int64.init), toSeq: input.span.to.map(Int64.init),
                  backlog: input.backlog, hasMore: input.hasMore, moreAfter: input.moreAfter)
    }

    private static func action(_ id: UInt, input: Input) throws -> Action {
        let event: ChatEvent
        switch input.value.kind {
        case .latest: event = .latest(batch: try batch(input.value))
        case .pushed: event = .pushed(batch: try batch(input.value))
        case .after: event = .after(batch: try batch(input.value))
        case .reloaded:
            event = .reloaded(items_json: try encode(input.value.items), history: input.value.span.history,
                              to_seq: input.value.span.to.map(Int64.init), has_more: input.value.hasMore ?? false)
        case .older:
            event = .older(items_json: try encode(input.value.items), has_more: input.value.hasMore ?? false)
        case .range:
            event = .range(fold_id: input.fold?.id, items_json: try encode(input.value.items), more: input.value.rangeMore)
        case .reconnected: event = .reconnected
        case .openFold:
            event = .openFold(fold_id: input.fold?.id ?? "")
        case .loadOlder:
            event = .loadOlder
        case .fillFailed: event = .fillFailed
        case .olderFailed: event = .olderFailed
        case .rangeFailed: event = .rangeFailed(fold_id: input.fold?.id)
        }
        return .chat(chat: String(id, radix: 16), event: event)
    }

    static func shape(_ input: NativeContinuityInput) -> String {
        "kind=\(input.kind.rawValue) items=\(input.items.count) history=\(input.span.history == nil ? "unset" : "set") " +
        "from=\(input.span.from == nil ? "unset" : "set") to=\(input.span.to == nil ? "unset" : "set") " +
        "backlog=\(input.backlog) hasMore=\(input.hasMore.map(String.init) ?? "null") moreAfter=\(input.moreAfter) " +
        "rangeMore=\(input.rangeMore) fold=\(input.fold == nil ? "unset" : "set")"
    }
}
#endif
