import Foundation
import Testing
@testable import Persona

private let now = Date(timeIntervalSince1970: 1_800_000_000)

private struct Locked: Error {}

/// Counts fetches and can hold the first one until released, to test runs that overlap.
private actor FetchGate {
    private(set) var calls = 0
    private var waiting: CheckedContinuation<Void, Never>?
    private var held = true
    func enter() async {
        calls += 1
        guard held, calls == 1 else { return }
        await withCheckedContinuation { waiting = $0 }
    }
    func release() { held = false; waiting?.resume(); waiting = nil }
}

@MainActor struct RecordingRollingKeeperTests {
    private func library() -> RecordingLibrary {
        RecordingLibrary(root: FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString))
    }

    /// Twenty 60 second segments, all past the 6 hour window; segment 10 starts exactly 7 hours before `now`.
    private func old(_ lib: RecordingLibrary, _ id: UUID, base: Date = now) {
        for i in 0..<20 {
            lib.addSegment(id, RecordingSegment(index: i, start: Double(i) * 60, duration: 60,
                                                startedAt: base.addingTimeInterval(-7 * 3_600 - 600 + Double(i) * 60)))
        }
    }

    @Test func bookmarksStillKeepWhenHealthDataCannotBeRead() async throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let entry = try lib.create(source: .watch, kind: .rolling)
        old(lib, entry.id)
        lib.change(entry.id) { $0.marks = [610] }
        let keeper = RecordingRollingKeeper(library: lib) { _, _ in throw Locked() }
        await keeper.run(now: now)
        let after = try #require(lib.entry(entry.id))
        // Unchecked segments wait for unevaluatedHours instead of the 6 hour window.
        #expect(after.segments.map(\.index) == Array(0..<20))
        #expect(Set(after.segments.filter { $0.kept != nil }.map(\.index)) == Set(5...15))
        #expect(after.segments.first { $0.index == 10 }?.kept?.reason == .mark)
        // Past unevaluatedHours the unkept ones go; the bookmarked ones stay.
        await keeper.run(now: now.addingTimeInterval(18 * 3_600))
        #expect(lib.entry(entry.id)?.segments.map(\.index) == Array(5...15))
    }

    @Test func stoppedEntryWithErasedTailIsRemovedAfterTheGrace() async throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let entry = try lib.create(source: .watch, kind: .rolling)
        for i in 0..<3 {
            lib.addSegment(entry.id, RecordingSegment(index: i, start: Double(i) * 60, duration: 60,
                                                      startedAt: now.addingTimeInterval(-300 + Double(i) * 60)))
        }
        lib.change(entry.id) { $0.active = false; $0.expectedSegments = 3; $0.endedAt = now }
        // The last segments were erased, so prunedBelow never reaches the total.
        lib.erase(entry.id, from: now.addingTimeInterval(-600), to: now)
        #expect(lib.entry(entry.id)?.segments.isEmpty == true)
        #expect((lib.entry(entry.id)?.prunedBelow ?? 0) < 3)
        let keeper = RecordingRollingKeeper(library: lib) { _, _ in RecordingSignals() }
        await keeper.run(now: now.addingTimeInterval(3_600))
        #expect(lib.entry(entry.id) != nil)
        await keeper.run(now: now.addingTimeInterval((RecordingRolling.undeliveredHours + 1) * 3_600))
        #expect(lib.entry(entry.id) == nil)
    }

    @Test func emptyEntryWithBookmarksWaitsForKeptSegmentsStillOnTheWatch() async throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let entry = try lib.create(source: .watch, kind: .rolling)
        lib.change(entry.id) { $0.active = false; $0.expectedSegments = 5; $0.marks = [30] }
        let keeper = RecordingRollingKeeper(library: lib) { _, _ in RecordingSignals() }
        // Stopped before endedAt existed: the grace starts at the first run.
        await keeper.run(now: now)
        #expect(lib.entry(entry.id)?.endedAt == now)
        await keeper.run(now: now.addingTimeInterval(2 * 86_400))
        #expect(lib.entry(entry.id) != nil)
        await keeper.run(now: now.addingTimeInterval((RecordingRolling.keptDays + 1) * 86_400))
        #expect(lib.entry(entry.id) == nil)
    }

    @Test func fullyPrunedEntryIsRemovedRightAway() throws {
        var entry = RecordingEntry(id: UUID(), createdAt: now, source: .watch, kind: .rolling)
        entry.active = false; entry.expectedSegments = 4; entry.prunedBelow = 4
        #expect(RecordingRolling.finished(entry, now: now))
        entry.prunedBelow = 3
        #expect(!RecordingRolling.finished(entry, now: now))
        entry.active = true; entry.prunedBelow = 4
        #expect(!RecordingRolling.finished(entry, now: now))
    }

    @Test func runAskedForDuringARunHappensAfterIt() async throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let entry = try lib.create(source: .watch, kind: .rolling)
        old(lib, entry.id, base: .now)
        let gate = FetchGate()
        let keeper = RecordingRollingKeeper(library: lib) { _, _ in
            await gate.enter()
            return RecordingSignals()
        }
        let first = Task { await keeper.run() }
        for _ in 0..<200 where await gate.calls == 0 { try await Task.sleep(for: .milliseconds(10)) }
        await keeper.run() // returns at once and asks for another run
        #expect(await gate.calls == 1)
        await gate.release()
        await first.value
        // The follow-up run comes after the debounce. All segments were pruned by the first, so it only runs to a fetch
        // if something is left: add one more old segment for it to check.
        lib.addSegment(entry.id, RecordingSegment(index: 30, start: 1_800, duration: 60, startedAt: Date.now.addingTimeInterval(-7 * 3_600)))
        for _ in 0..<500 where await gate.calls < 2 { try await Task.sleep(for: .milliseconds(10)) }
        #expect(await gate.calls == 2)
    }

    @Test func aSegmentArrivingDuringTheFetchWaitsForTheNextRun() async throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let entry = try lib.create(source: .watch, kind: .rolling)
        old(lib, entry.id)
        let late = RecordingSegment(index: 20, start: 1_200, duration: 60, startedAt: now.addingTimeInterval(-7 * 3_600 - 900))
        let id = entry.id
        let keeper = RecordingRollingKeeper(library: lib) { _, _ in
            // Arrives after the candidates were taken, so this fetch never covered it.
            await MainActor.run { lib.addSegment(id, late) }
            return RecordingSignals()
        }
        await keeper.run(now: now)
        let after = try #require(lib.entry(entry.id))
        #expect(after.segments.map(\.index) == [20])
        // The next run covers it.
        await keeper.run(now: now)
        #expect(lib.entry(entry.id)?.segments.isEmpty == true)
    }
}
