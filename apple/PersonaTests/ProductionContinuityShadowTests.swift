import Foundation
import Testing
@testable import Persona

@MainActor
struct ProductionContinuityShadowTests {
    init() { ProductionContinuityShadow.shared.installForTests() }

    private func item(_ seq: Int, kind: ItemKind = .text) -> Item {
        Item(id: "\(seq)#0", seq: seq, ts: nil, role: "assistant", kind: kind, text: "Synthetic shadow fixture",
             toolUseID: nil, toolName: nil, toolInput: nil, isError: nil, image: nil)
    }

    private func fold(_ seq: Int, through: Int) -> Item {
        var item = item(seq, kind: .fold)
        item.id = "fold:\(seq)#0"
        item.fold = Fold(items: through - seq + 1, tools: through - seq + 1, thoughts: 0, until: nil,
                         throughSeq: through, throughID: "\(through)#0")
        return item
    }

    @Test func productionInputsComparePatchesAndState() async throws {
        let shadow = ProductionContinuityShadow.shared
        let store = ChatStore(key: SessionKey(machineID: "fixture", sessionID: "production-shadow"))
        let first = [item(1), fold(2, through: 3)]
        _ = store.apply(.batch(.latest, items: first, span: ItemSpan(history: "h", to: 2), backlog: true, hasMore: true))
        _ = store.apply(.event(.openFold, fold: first[1]))
        _ = store.apply(.range([item(2, kind: .toolUse), item(3, kind: .toolResult)], fold: first[1], more: false))
        shadow.loadOlder(store)
        _ = store.apply(.batch(.older, items: [item(0)], span: ItemSpan(), hasMore: false))
        _ = store.apply(.batch(.pushed, items: [item(4)], span: ItemSpan(history: "h", from: 2, to: 4)))
        let result = try await shadow.compare(store)
        #expect(result.compared >= 5)
        #expect(result.mismatches == 0)
        shadow.close(store)
    }

    @Test func restoredCacheAndIndependentFoldsKeepTheirOrder() async throws {
        let shadow = ProductionContinuityShadow.shared
        let key = SessionKey(machineID: "fixture", sessionID: "shadow-cache")
        let folder = FileManager.default.temporaryDirectory.appending(path: "shadow-cache-\(UUID())", directoryHint: .isDirectory)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: folder) }
        ChatCache.save(key, items: [item(1), fold(2, through: 3), fold(5, through: 6)], hasMore: true,
                       history: "h", covered: 3, in: folder)
        let store = ChatStore(key: key, cacheFolder: folder)
        let folds = store.items.filter { $0.kind == .fold }
        _ = store.apply(.batch(.latest, items: store.items, span: ItemSpan(history: "h", to: 3), backlog: true, hasMore: true))
        _ = store.apply(.event(.openFold, fold: folds[0]))
        _ = store.apply(.event(.openFold, fold: folds[1]))
        _ = store.apply(.range([item(5, kind: .toolUse), item(6, kind: .toolResult)], fold: folds[1], more: false))
        _ = store.apply(.range([item(2, kind: .toolUse), item(3, kind: .toolResult)], fold: folds[0], more: false))
        let result = try await shadow.compare(store)
        #expect(result.compared >= 3)
        #expect(result.mismatches == 0)
        shadow.close(store)
    }

    @Test func sameInputTwiceDoesNotLosePatchState() async throws {
        let shadow = ProductionContinuityShadow.shared
        let store = ChatStore(key: SessionKey(machineID: "fixture", sessionID: "same-input"))
        let input = NativeContinuityInput.batch(.latest, items: [item(1)], span: ItemSpan(history: "h", to: 1), backlog: true, hasMore: false)
        _ = store.apply(input)
        _ = store.apply(input)
        let result = try await shadow.compare(store)
        #expect(result.mismatches == 0)
        shadow.close(store)
    }

    @Test func diagnosticsHaveOnlyDomainRuleAndShape() async throws {
        let reports = MutexBox<[ProductionContinuityShadow.Report]>([])
        let shadow = ProductionContinuityShadow { report in reports.withLock { $0.append(report) } }
        let store = ChatStore(key: SessionKey(machineID: "fixture", sessionID: "private-identifiers"))
        var secret = item(1)
        secret.text = "synthetic-message-content-that-must-not-be-reported"
        secret.id = "synthetic-item-identifier"
        _ = store.apply(.batch(.latest, items: [secret], span: ItemSpan(history: "h", to: 1), backlog: true, hasMore: false))
        // A native result that deliberately disagrees proves the mismatch reporter rather than a silent comparison.
        var wrong = store.nativeResult()
        wrong = NativeContinuityResult(items: [], covered: 999, history: "synthetic-history", hasMore: true, loadedOnce: false)
        shadow.accept(.batch(.pushed, items: [item(2)], span: ItemSpan(history: "h", from: 1, to: 2)),
                      into: store, snapshot: store.nativeSnapshot(), native: wrong, gap: nil)
        _ = try await shadow.compare(store)
        let data = try JSONEncoder().encode(reports.withLock { $0 })
        let text = try #require(String(data: data, encoding: .utf8))
        for forbidden in [secret.text ?? "", secret.id, "synthetic-history", "private-identifiers", "999"] {
            #expect(!text.contains(forbidden))
        }
        #expect(reports.withLock { $0.allSatisfy { $0.domain == "continuity" && !$0.rule.isEmpty && $0.inputShape.contains("kind=pushed") } })
        shadow.close(store)
    }

    @Test func shapeCountsAndPresenceFlagsNeverCarrySequences() {
        let input = NativeContinuityInput.batch(.pushed, items: [item(982451653)],
            span: ItemSpan(history: "private-history", from: 1234567, to: 9876543), backlog: true, hasMore: nil, moreAfter: true)
        let shape = ProductionContinuityShadow.shape(input)
        #expect(shape.contains("kind=pushed"))
        #expect(shape.contains("items=1"))
        #expect(shape.contains("history=set"))
        #expect(!shape.contains("private-history"))
        #expect(!shape.contains("1234567"))
        #expect(!shape.contains("982451653"))
    }
}

private final class MutexBox<Value>: @unchecked Sendable {
    private let lock = NSLock()
    private var value: Value
    init(_ value: Value) { self.value = value }
    func withLock<T>(_ body: (inout Value) -> T) -> T { lock.withLock { body(&value) } }
}
