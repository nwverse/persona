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
    struct Report: Codable, Equatable, Sendable {
        let domain: String
        let rule: String
        let inputShape: String
    }

    static let shared = ProductionContinuityShadow { report in
        // The same queue every app error report uses. Its message is a fixed shape, never a decoded item or error text.
        Task.detached(priority: .utility) {
            ErrorReportQueue.shared.write(ErrorReports.report(
                kind: "log", level: "warning",
                message: "domain=\(report.domain) rule=\(report.rule) input=\(report.inputShape)"))
        }
    }
    private static let logger = Logger(subsystem: "Persona.Diagnostics", category: "ContinuityShadow")

    private struct Chat {
        var items: [Item] = []
        var compared = 0
        var mismatches = 0
        var failed = false
    }

    private struct Input: Sendable {
        let snapshot: NativeContinuitySnapshot
        let value: NativeContinuityInput
        let native: NativeContinuityResult
        let gap: ChatStore.Gap?
        let fold: Item?
    }

    private final class Effects: @unchecked Sendable {
        struct Entry {
            let id: UInt?
            let effect: Effect
        }
        let values = Mutex<[Entry]>([])
    }

    private let queue = DispatchQueue(label: "Persona.ContinuityShadow", qos: .utility)
    private let effects = Effects()
    private var enabled = true
    private var client: PersonaCoreClient?
    private var chats: [UInt: Chat] = [:]
    private var generations: [UInt: UInt] = [:]
    private var nextGeneration: UInt = 1
    private var reportSink: @Sendable (Report) -> Void
    private var mismatches = 0

    init(reportSink: @escaping @Sendable (Report) -> Void) {
        self.reportSink = reportSink
    }

    @MainActor func install() {
        #if os(iOS)
        guard UserDefaults.standard.object(forKey: "showContinuityShadow") as? Bool ?? true else {
            enabled = false
            return
        }
        enabled = true
        #endif
        ChatStore.installContinuityShadowForTests { [weak self] store, input, observation in
            self?.accept(input, into: store, snapshot: observation.snapshot, native: observation.native, gap: observation.gap)
        }
    }

    @MainActor func accept(_ input: NativeContinuityInput, into store: ChatStore, native: NativeContinuityResult,
                gap: ChatStore.Gap?) {
        guard enabled else { return }
        accept(input, into: store, snapshot: store.nativeSnapshot(), native: native, gap: gap)
    }

    @MainActor func accept(_ input: NativeContinuityInput, into store: ChatStore, snapshot: NativeContinuitySnapshot,
                native: NativeContinuityResult, gap: ChatStore.Gap?) {
        let id = generation(for: store)
        let value = Input(snapshot: snapshot, value: input, native: native, gap: gap, fold: input.fold)
        // Encoding copies the immutable items before the next native mutation, but never runs on the caller.
        enqueue { [weak self] in self?.process(id, input: value) }
    }

    private func generation(for store: ChatStore) -> UInt {
        let pointer = UInt(bitPattern: ObjectIdentifier(store))
        if let existing = generations[pointer] { return existing }
        let id = nextGeneration
        nextGeneration += 1
        generations[pointer] = id
        return id
    }

    private func start(_ id: UInt, snapshot: NativeContinuitySnapshot) throws {
        guard chats[id] == nil else { return }
        chats[id] = Chat()
        try core().dispatch(.openChat(chat: chatID(id), snapshot: Self.snapshot(snapshot)))
        try core().flush(timeoutMilliseconds: 16)
        try applyInitialSnapshot(id, input: Input(snapshot: snapshot, value: .event(.reconnected), native: .init(
            items: snapshot.items, covered: snapshot.covered, history: snapshot.history,
            hasMore: snapshot.hasMore, loadedOnce: snapshot.loadedOnce), gap: nil, fold: nil))
    }

    @MainActor func loadOlder(_ store: ChatStore) {
        guard enabled else { return }
        let id = generation(for: store)
        let snapshot = store.nativeSnapshot()
        enqueue { [weak self] in
            guard let self else { return }
            do {
                if self.chats[id] == nil {
                    self.chats[id] = Chat()
                    try self.core().dispatch(.openChat(chat: self.chatID(id), snapshot: Self.snapshot(snapshot)))
                }
                try self.core().dispatch(.chat(chat: self.chatID(id), event: .loadOlder))
            } catch { self.fail(id, rule: "older-request", shape: "kind=loadOlder items=0") }
        }
    }

    @MainActor func failed(_ kind: NativeContinuityInput.Kind, in store: ChatStore, fold: Item? = nil) {
        guard enabled else { return }
        let id = generation(for: store)
        let snapshot = store.nativeSnapshot()
        let event: ChatEvent = switch kind {
        case .fillFailed: .fillFailed
        case .olderFailed: .olderFailed
        default: .rangeFailed(fold_id: fold?.id)
        }
        enqueue { [weak self] in
            guard let self else { return }
            do {
                try self.start(id, snapshot: snapshot)
                try self.core().dispatch(.chat(chat: self.chatID(id), event: event))
            } catch { self.fail(id, rule: "failed-input", shape: "kind=\(kind.rawValue) items=0") }
        }
    }
    @MainActor func close(_ store: ChatStore) {
        guard enabled else { return }
        let id = generation(for: store)
        enqueue { [weak self] in self?.removeChat(id) }
    }

    func compare(_ store: ChatStore) async throws -> (compared: Int, mismatches: Int) {
        let id = generation(for: store)
        return try await withCheckedThrowingContinuation { resume in
            enqueue { [weak self] in
                guard let self else { resume.resume(throwing: PersonaCoreError.closed); return }
                do {
                    try self.core().flush()
                    self.drainChanged(id)
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
            if chats[id] == nil {
                try start(id, snapshot: input.snapshot)
            }
            // Dispatch is cheap on the caller. This queue waits only for the current input's core action, never
            // the main actor. A stalled core disables this chat's shadow rather than holding five seconds of work.
            try core().dispatch(try Self.action(id, input: input))
            try core().flush(timeoutMilliseconds: 16)
            try compareQueued(id, input: input, shape: Self.shape(input.value))
        } catch {
            fail(id, rule: "core-input", shape: Self.shape(input.value))
        }
    }

    private func applyInitialSnapshot(_ id: UInt, input: Input) throws {
        let patch = try core().drain(chat: chatID(id))
        guard var chat = chats[id] else { return }
        try Self.apply(patch, to: &chat)
        chats[id] = chat
        if chat.items != input.snapshot.items || patch.covered != input.snapshot.covered
            || patch.history != input.snapshot.history || (patch.hasMore ?? false) != input.snapshot.hasMore
            || patch.loadedOnce != input.snapshot.loadedOnce {
            report(id, rule: "initial-snapshot", shape: "kind=snapshot items=\(input.snapshot.items.count)")
        }
    }

    private func core() throws -> PersonaCoreClient {
        if let client { return client }
        let client = try PersonaCoreClient(config: Config(), callbacks: PersonaCoreCallbacks(changedChat: { [weak self] chat, _ in
            guard let id = Self.identifier(chat) else { return }
            self?.enqueue { [weak self] in self?.drainChanged(id) }
        }, run: { [effects] effect in
            guard case .fetch(let chat, _) = effect, let id = Self.identifier(chat) else {
                effects.values.withLock { $0.append(.init(id: nil, effect: effect)) }
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
        guard var chat = chats[id], !chat.failed else { return }
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
            switch input.gap {
            case .fill where input.value.kind == .latest || input.value.kind == .pushed:
                if !effects.values.withLock({ $0.contains { $0.id == id && Self.isAfter($0.effect) } }) {
                    report(id, rule: "immediate-fill", shape: shape)
                }
            case .reload where input.value.kind == .latest || input.value.kind == .pushed:
                if !effects.values.withLock({ $0.contains { $0.id == id && Self.isLatest($0.effect) } }) {
                    report(id, rule: "reload-request", shape: shape)
                }
            default: break
            }
            if input.value.kind == .older,
               !effects.values.withLock({ $0.contains { $0.id == id && Self.isOlder($0.effect) } }) {
                report(id, rule: "older-request", shape: shape)
            }
            if input.value.kind == .reconnected,
               !effects.values.withLock({ $0.contains { $0.id == id && Self.isSubscribe($0.effect) } }) {
                report(id, rule: "reconnect-request", shape: shape)
            }
            if input.value.kind == .openFold,
               !effects.values.withLock({ $0.contains { $0.id == id && Self.isRange($0.effect) } }) {
                report(id, rule: "fold-request", shape: shape)
            }
        }
        let pending = effects.values.withLock { current in
            let mine = current.filter { $0.id == id || $0.id == nil }
            current.removeAll { $0.id == id || $0.id == nil }
            return mine
        }
        for entry in pending {
            if case .rejected = entry.effect { report(id, rule: "core-rejected", shape: shape) }
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

    private func removeChat(_ id: UInt) {
        generations = generations.filter { $0.value != id }
        effects.values.withLock { $0.removeAll { $0.id == id || $0.id == nil } }
        guard chats.removeValue(forKey: id) != nil else { return }
        do { try core().dispatch(.closeChat(chat: chatID(id))) }
        catch { fail(id, rule: "core-close", shape: "kind=close items=0") }
        Self.logger.debug("continuity shadow closed mismatches=\(self.mismatches, privacy: .public)")
    }

    private func fail(_ id: UInt, rule: String, shape: String) {
        guard chats[id]?.failed == false else { return }
        chats[id]?.failed = true
        report(id, rule: rule, shape: shape)
    }

    private func report(_ id: UInt, rule: String, shape: String) {
        mismatches += 1
        chats[id]?.mismatches += 1
        let value = Report(domain: "continuity", rule: rule, inputShape: shape)
        Self.logger.error("continuity mismatch domain=\(value.domain, privacy: .public) rule=\(value.rule, privacy: .public) input=\(value.inputShape, privacy: .public)")
        reportSink(value)
    }

    private func chatID(_ id: UInt) -> String { String(id, radix: 16) }

    private static func identifier(_ chat: String) -> UInt? { UInt(chat, radix: 16) }

    private static func isAfter(_ effect: Effect) -> Bool {
        if case .fetch(_, .fetchAfter) = effect { return true }
        return false
    }

    private static func isLatest(_ effect: Effect) -> Bool {
        if case .fetch(_, .fetchLatest) = effect { return true }
        return false
    }

    private static func isOlder(_ effect: Effect) -> Bool {
        if case .fetch(_, .fetchOlder) = effect { return true }
        return false
    }

    private static func isSubscribe(_ effect: Effect) -> Bool {
        if case .fetch(_, .subscribe) = effect { return true }
        return false
    }

    private static func isRange(_ effect: Effect) -> Bool {
        if case .fetch(_, .fetchRange) = effect { return true }
        return false
    }

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
