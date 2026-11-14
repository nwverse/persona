import Foundation
import PersonaCore
import XCTest
@testable import Persona

/// Optimized, simulator-labelled microbenchmarks of the existing native input path and the real C binding (#6871).
/// No production adapter exists in phase 1: native ChatStore and core patch-apply are deliberately separate columns.
@MainActor
final class PersonaCoreReferencePerformanceTests: XCTestCase {
    private static let warmups = 100
    private static let samples = 1_000
    private let chat = "benchmark/continuity"
    private let snapshot = ChatSnapshot(
        itemsJson: #"[{"id":"benchmark-item","kind":"text","role":"assistant","text":"synthetic streaming text for a bounded continuity benchmark","seq":1}]"#,
        history: "history", covered: 1, hasMore: nil, loadedOnce: true)

    func testDispatchDrainAndNativeInputCosts() async throws {
        let client = try PersonaCoreClient(config: Config(), callbacks: PersonaCoreCallbacks(run: { _ in }))
        defer { client.close() }
        try client.dispatch(.openChat(chat: chat, snapshot: snapshot))
        try await prepareWorker(client)
        _ = try client.drain(chat: chat)
        let loadOlder = Action.chat(chat: chat, event: .loadOlder)
        var reports: [[String: Any]] = []
        var flush: [Double] = []
        var patchOps: [Int] = []
        var list: [String] = []
        var decodedItems: [Item] = []
        let nativeItems = try JSON.decoder.decode([Item].self, from: Data(snapshot.itemsJson.utf8))

        for index in 0..<(Self.warmups + Self.samples) {
            let start = ContinuousClock.now
            try client.dispatch(loadOlder)
            let elapsed = microseconds(start)
            let flushStart = ContinuousClock.now
            try await prepareWorker(client)
            if index >= Self.warmups {
                reports.append(sample("core_dispatch_encode_and_queue", index, elapsed))
                flush.append(microseconds(flushStart))
            }
        }

        for index in 0..<(Self.warmups + Self.samples) {
            let action = Action.openChat(chat: chat, snapshot: snapshot)
            let flushStart = ContinuousClock.now
            try client.dispatch(action)
            try await prepareWorker(client)
            list.removeAll(keepingCapacity: true)
            decodedItems.removeAll(keepingCapacity: true)
            let start = ContinuousClock.now
            let patch = try client.drain(chat: chat)
            let drained = ContinuousClock.now
            for operation in patch.ops {
                switch operation {
                case let .remove(index): list.remove(at: Int(index))
                case let .insert(index, _, json): list.insert(json, at: Int(index))
                case let .update(index, _, json): list[Int(index)] = json
                }
            }
            let elapsed = microseconds(start)
            XCTAssertFalse(patch.ops.isEmpty, "an empty patch is not a valid frame workload")
            XCTAssertEqual(list.count, 1)
            XCTAssertTrue(list[0].contains("benchmark-item"))
            if index >= Self.warmups {
                reports.append(sample("core_drain_decode", index, microseconds(start, drained)))
                reports.append(sample("core_apply", index, microseconds(drained)))
                reports.append(sample("core_drain_decode_apply", index, elapsed))
                flush.append(microseconds(flushStart))
                patchOps.append(patch.ops.count)
                // A separately named native Item consumer. It decodes the exact patch JSON, but is not ChatStore:
                // ChatPatch does not retain original event span/fold semantics, so no production equivalence is implied.
                let itemStart = ContinuousClock.now
                for operation in patch.ops {
                    switch operation {
                    case let .remove(index): decodedItems.remove(at: Int(index))
                    case let .insert(index, _, json): decodedItems.insert(try JSON.decoder.decode(Item.self, from: Data(json.utf8)), at: Int(index))
                    case let .update(index, _, json): decodedItems[Int(index)] = try JSON.decoder.decode(Item.self, from: Data(json.utf8))
                    }
                }
                reports.append(sample("core_patch_json_decode_item_apply", index, microseconds(itemStart)))
                XCTAssertEqual(decodedItems, nativeItems)
            }
        }

        // Same JSON input and a real native Store, but NOT the same output contract as core's [String] patch apply.
        let input = Data(snapshot.itemsJson.utf8)
        let key = SessionKey(machineID: "benchmark", sessionID: "continuity")
        let store = ChatStore(key: key)
        let span = ItemSpan(history: "history", from: nil, to: 1)
        for index in 0..<(Self.warmups + Self.samples) {
            let start = ContinuousClock.now
            let items = try JSON.decoder.decode([Item].self, from: input)
            let decoded = ContinuousClock.now
            _ = store.apply(items, span: span, backlog: true)
            let elapsed = microseconds(start)
            XCTAssertEqual(store.items.count, 1)
            if index >= Self.warmups {
                reports.append(sample("native_json_decode", index, microseconds(start, decoded)))
                reports.append(sample("native_store_apply", index, microseconds(decoded)))
                reports.append(sample("native_json_decode_store_apply", index, elapsed))
            }
        }
        try attach(["scope": "synthetic native input and C binding; not a production core-driven screen",
                    "samples_per_metric": Self.samples, "warmups_per_metric": Self.warmups,
                    "worker_flush_is_outside_timed_spans": true, "patch_op_counts": patchOps,
                    "input_json_utf8_bytes": snapshot.itemsJson.utf8.count,
                    "timer_overhead_us": timerOverhead(),
                    "raw_samples": reports, "statistics": statistics(reports),
                    "worker_flush_diagnostic_us": summary(flush),
                    "budgets": ["dispatch_p99_us": 50, "drain_decode_apply_p99_us": 1_000]])
    }

    func testOneHundredItemPageDrainDecodeApply() async throws {
        let items = (1...100).map {
            #"{"id":"benchmark-item-\#($0)","kind":"text","role":"assistant","text":"synthetic page content \#($0)","seq":\#($0)}"#
        }
        let snapshot = ChatSnapshot(itemsJson: "[" + items.joined(separator: ",") + "]",
                                    history: "history", covered: 100, hasMore: nil, loadedOnce: true)
        let client = try PersonaCoreClient(config: Config(), callbacks: PersonaCoreCallbacks(run: { _ in }))
        defer { client.close() }
        var timings: [Double] = []
        var operations = 0
        for index in 0..<(Self.warmups + Self.samples) {
            try client.dispatch(.openChat(chat: chat, snapshot: snapshot))
            try await prepareWorker(client)
            let start = ContinuousClock.now
            let patch = try client.drain(chat: chat)
            var list: [String] = []
            for operation in patch.ops {
                switch operation {
                case let .remove(index): list.remove(at: Int(index))
                case let .insert(index, _, json): list.insert(json, at: Int(index))
                case let .update(index, _, json): list[Int(index)] = json
                }
            }
            let elapsed = microseconds(start)
            XCTAssertEqual(list.count, 100)
            operations = patch.ops.count
            if index >= Self.warmups { timings.append(elapsed) }
        }
        try attach(["scope": "100-item snapshot; drain/bincode decode/string-list apply, not ChatStore or a presented frame",
                    "samples": timings, "statistics": summary(timings), "patch_op_count": operations,
                    "input_json_utf8_bytes": snapshot.itemsJson.utf8.count,
                    "budget_drain_decode_apply_p99_us": 1_000, "worker_flush_is_outside_timed_span": true])
    }

    func testCoreTenOpenCloseMemory() async throws {
        var baseline = 0
        var samples: [[String: Any]] = []
        for index in 0...10 {
            weak var released = try await createDrainAndClose()
            for _ in 0..<100 where released != nil { await Task.yield() }
            XCTAssertNil(released, "the closed core client was retained")
            let bytes = FullPageMemory.footprint()
            XCTAssertGreaterThan(bytes, 0)
            if index == 0 {
                baseline = bytes // warm up allocators and the first worker before taking the baseline
            } else {
                samples.append(["cycle": index, "phys_footprint_bytes": bytes, "delta_from_warmed_bytes": bytes - baseline])
            }
        }
        let finalDelta = (samples.last?["delta_from_warmed_bytes"] as? Int) ?? Int.max
        XCTAssertLessThanOrEqual(finalDelta, 5 * 1_024 * 1_024, "ten C-core open/close cycles exceeded the 5 MiB budget")
        try attach(["scope": "ten create/open/drain/close cycles, C destruction completed off the main thread",
                    "warmup_cycles": 1, "cycles": 10, "baseline_phys_footprint_bytes": baseline,
                    "final_delta_bytes": finalDelta, "budget_final_delta_bytes": 5 * 1_024 * 1_024, "samples": samples])
    }

    private func createDrainAndClose() async throws -> PersonaCoreClient? {
        let client = try PersonaCoreClient(config: Config(), callbacks: PersonaCoreCallbacks(run: { _ in }))
        try client.dispatch(.openChat(chat: chat, snapshot: snapshot))
        try await prepareWorker(client)
        _ = try client.drain(chat: chat)
        weak var released = client
        await closeOnBackground(client)
        return released
    }

    private func prepareWorker(_ client: PersonaCoreClient) async throws {
        // Suspends the test's MainActor task while the worker is joined; no main-thread flush wait is in either span.
        try await Task.detached { try client.flush() }.value
    }

    private func closeOnBackground(_ client: PersonaCoreClient) async {
        await Task.detached { client.close() }.value
    }

    private func timerOverhead() -> [Double] {
        (0..<100).map { _ in
            let start = ContinuousClock.now
            return microseconds(start)
        }
    }

    private func microseconds(_ start: ContinuousClock.Instant, _ end: ContinuousClock.Instant = .now) -> Double {
        Double(start.duration(to: end).components.attoseconds) / 1_000_000_000_000
            + Double(start.duration(to: end).components.seconds) * 1_000_000
    }

    private func sample(_ metric: String, _ index: Int, _ value: Double) -> [String: Any] {
        ["metric": metric, "sample": index - Self.warmups, "value_us": value,
         "measured_on_main_thread": Thread.isMainThread]
    }

    private func statistics(_ samples: [[String: Any]]) -> [String: Any] {
        Dictionary(grouping: samples, by: { $0["metric"] as? String ?? "invalid" }).mapValues { rows in
            summary(rows.compactMap { ($0["value_us"] as? NSNumber)?.doubleValue })
        }
    }

    private func summary(_ values: [Double]) -> [String: Any] {
        let sorted = values.sorted()
        func percentile(_ fraction: Double) -> Double {
            sorted.isEmpty ? -1 : sorted[Int(Double(sorted.count - 1) * fraction)]
        }
        return ["count": sorted.count, "p50_us": percentile(0.50), "p99_us": percentile(0.99),
                "max_us": sorted.last ?? -1, "percentile_rule": "floor((count - 1) * p), matching the Android reference benchmark"]
    }

    private func attach(_ report: [String: Any]) throws {
        let attachment = XCTAttachment(data: try JSONSerialization.data(withJSONObject: report, options: [.prettyPrinted, .sortedKeys]),
                                       uniformTypeIdentifier: "public.json")
        attachment.name = "frames.json"
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
