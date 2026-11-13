import Foundation
import Testing
@testable import Persona

private let anchor = Date(timeIntervalSince1970: 1_800_000_000)

@MainActor struct RecordingRollingTests {
    private func library() throws -> RecordingLibrary {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        return RecordingLibrary(root: root)
    }

    private func utc() -> Calendar {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = .gmt
        return calendar
    }

    /// A 60 second segment that started `ago` seconds before `anchor`.
    private func segment(_ index: Int, ago: Double, duration: Double = 60) -> RecordingSegment {
        RecordingSegment(index: index, start: Double(index) * 60, duration: duration, startedAt: anchor.addingTimeInterval(-ago))
    }

    private func rolling(_ segments: [RecordingSegment]) -> RecordingEntry {
        var entry = RecordingEntry(id: UUID(), createdAt: anchor, source: .watch, kind: .rolling)
        entry.segments = segments
        return entry
    }

    /// Twenty 60 second segments; segment 4 starts 360 s before `anchor`, segment 15 starts 300 s after it.
    private func window() -> [RecordingSegment] {
        (0..<20).map { i in
            RecordingSegment(index: i, start: Double(i) * 60, duration: 60, startedAt: anchor.addingTimeInterval(-600 + Double(i) * 60))
        }
    }

    /// Two high heart-rate readings 30 seconds apart.
    private func pair() -> [RecordingSignals.Sample] {
        [RecordingSignals.Sample(at: anchor, value: 110), RecordingSignals.Sample(at: anchor.addingTimeInterval(30), value: 115)]
    }

    @Test func oldJSONWithoutNewKeysDecodes() throws {
        var entry = RecordingEntry(id: UUID(), createdAt: anchor, source: .phone)
        entry.segments = [RecordingSegment(index: 0, start: 0, duration: 30, startedAt: anchor,
                                           kept: RecordingKeep(reason: .mark, at: anchor))]
        let data = try JSONEncoder().encode(entry)
        let object = try JSONSerialization.jsonObject(with: data)
        var json = object as? [String: Any] ?? [:]
        for key in ["kind", "prunedBelow", "erased"] { json[key] = nil }
        var segments = json["segments"] as? [[String: Any]] ?? []
        for i in segments.indices { segments[i]["startedAt"] = nil; segments[i]["kept"] = nil }
        json["segments"] = segments
        let old = try JSONSerialization.data(withJSONObject: json)
        let decoded = try JSONDecoder().decode(RecordingEntry.self, from: old)
        #expect(decoded.kind == nil && decoded.prunedBelow == nil && decoded.erased == nil)
        #expect(decoded.segments.first?.startedAt == nil && decoded.segments.first?.kept == nil)
        #expect(decoded.segments.first?.duration == 30)
    }

    @Test func expiredHonorsWindowsAndHealthCheck() {
        var kept = segment(1, ago: 7 * 3600)
        kept.kept = RecordingKeep(reason: .mark, at: anchor)
        var keptOld = segment(3, ago: 8 * 86_400)
        keptOld.kept = RecordingKeep(reason: .mood, at: anchor)
        let entry = rolling([segment(0, ago: 7 * 3600), kept, segment(2, ago: 2 * 3600), keptOld, segment(4, ago: 25 * 3600)])
        #expect(RecordingRolling.expired(entry, now: anchor).sorted() == [0, 3, 4])
        #expect(RecordingRolling.expired(entry, now: anchor, evaluated: false).sorted() == [3, 4])
        #expect(RecordingRolling.candidates(entry, now: anchor).map(\.index) == [0, 4])
        var clip = entry
        clip.kind = nil
        #expect(RecordingRolling.expired(clip, now: anchor).isEmpty)
    }

    @Test func pruneLeavesTombstoneAndDropsLateSegments() throws {
        let lib = try library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let entry = try lib.create(source: .watch, kind: .rolling)
        #expect(lib.entry(entry.id)?.isRolling == true)
        lib.addSegment(entry.id, segment(0, ago: 7 * 3600))
        lib.addSegment(entry.id, segment(1, ago: 60))
        lib.prune(entry.id, indexes: [0])
        #expect(lib.entry(entry.id)?.segments.map(\.index) == [1])
        #expect(lib.entry(entry.id)?.prunedBelow == 1)
        // A late copy of the pruned segment is dropped; a newer one is kept.
        let current = try #require(lib.entry(entry.id))
        try lib.receive(RecordingTransfer(entry: current, segment: segment(0, ago: 7 * 3600)))
        try lib.receive(RecordingTransfer(entry: current, segment: segment(2, ago: 30)))
        #expect(lib.entry(entry.id)?.segments.map(\.index) == [1, 2])
    }

    @Test func erasedRangeRemovesAndDropsLateSegments() throws {
        let lib = try library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let entry = try lib.create(source: .watch, kind: .rolling)
        lib.addSegment(entry.id, segment(0, ago: 1200))
        lib.addSegment(entry.id, segment(1, ago: 300))
        lib.erase(entry.id, from: anchor.addingTimeInterval(-600), to: anchor)
        #expect(lib.entry(entry.id)?.segments.map(\.index) == [0])
        #expect(lib.entry(entry.id)?.erased?.count == 1)
        // A range arriving from the other device (on a segment transfer) removes what is left in it.
        var other = RecordingEntry(id: entry.id, createdAt: entry.createdAt, source: .watch, kind: .rolling)
        other.erased = [RecordingErase(from: anchor.addingTimeInterval(-1300), to: anchor.addingTimeInterval(-1100))]
        try lib.receive(RecordingTransfer(entry: other, segment: segment(5, ago: 1200)))
        #expect(lib.entry(entry.id)?.segments.isEmpty == true)
        #expect(lib.entry(entry.id)?.erased?.count == 2)
        // A late segment inside the erased range is dropped.
        let current = try #require(lib.entry(entry.id))
        try lib.receive(RecordingTransfer(entry: current, segment: segment(2, ago: 200)))
        #expect(lib.entry(entry.id)?.segments.isEmpty == true)
    }

    @Test func saveClipRenumbersAndShiftsText() throws {
        let lib = try library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let entry = try lib.create(source: .watch, kind: .rolling)
        var first = RecordingSegment(index: 0, start: 0, duration: 60, startedAt: anchor.addingTimeInterval(-300))
        first.sentences = [RecordingSentence(start: 10, end: 20, text: "甲")]
        var second = RecordingSegment(index: 1, start: 60, duration: 60, startedAt: anchor.addingTimeInterval(-240))
        second.sentences = [RecordingSentence(start: 65, end: 70, text: "乙")]
        var third = RecordingSegment(index: 2, start: 120, duration: 60, startedAt: anchor.addingTimeInterval(-180))
        third.sentences = [RecordingSentence(start: 125, end: 130, text: "丙")]
        lib.addSegment(entry.id, first)
        lib.addSegment(entry.id, second)
        lib.addSegment(entry.id, third)
        lib.change(entry.id) { $0.marks = [30, 90, 150] }
        try Data([1]).write(to: lib.audio(entry.id, 0))
        try Data([2]).write(to: lib.audio(entry.id, 2))
        // Segment 1 has no audio file, so it is skipped and the rest close the gap.
        let clip = try lib.saveClip(from: entry.id, indexes: [2, 0, 1])
        #expect(clip.kind == nil)
        #expect(clip.segments.map(\.index) == [0, 1])
        #expect(clip.segments.map(\.start) == [0, 60])
        #expect(clip.segments.first?.sentences == [RecordingSentence(start: 10, end: 20, text: "甲")])
        #expect(clip.segments.last?.sentences == [RecordingSentence(start: 65, end: 70, text: "丙")])
        #expect(clip.marks == [30, 90])
        #expect(clip.createdAt == anchor.addingTimeInterval(-300))
        #expect(clip.receivedAll)
        #expect(lib.entry(clip.id) != nil)
        #expect(FileManager.default.fileExists(atPath: lib.audio(clip.id, 1).path))
    }

    @Test func heartRatePairKeepsFiveMinutesEitherSide() {
        let entry = rolling(window())
        var signals = RecordingSignals()
        signals.restingHeartRate = 60
        signals.heartRate = pair()
        let keeps = RecordingKeepRules.evaluate(entry, candidates: entry.segments, signals: signals, calendar: utc())
        #expect(Set(keeps.keys) == Set(4...15))
        #expect(keeps[4]?.reason == .heartRate)
    }

    @Test func singleHeartRateSpikeIsIgnored() {
        let entry = rolling(window())
        var signals = RecordingSignals()
        signals.restingHeartRate = 60
        signals.heartRate = [RecordingSignals.Sample(at: anchor, value: 120)]
        #expect(RecordingKeepRules.evaluate(entry, candidates: entry.segments, signals: signals, calendar: utc()).isEmpty)
    }

    @Test func stepsMakeTheSpikeNotQuiet() {
        let entry = rolling(window())
        var signals = RecordingSignals()
        signals.restingHeartRate = 60
        signals.heartRate = pair()
        signals.steps = [RecordingSignals.Span(from: anchor.addingTimeInterval(-100), to: anchor.addingTimeInterval(100), value: 150)]
        #expect(RecordingKeepRules.evaluate(entry, candidates: entry.segments, signals: signals, calendar: utc()).isEmpty)
    }

    @Test func workoutExcludesHeartRate() {
        let entry = rolling(window())
        var signals = RecordingSignals()
        signals.restingHeartRate = 60
        signals.heartRate = pair()
        signals.workouts = [RecordingSignals.Span(from: anchor.addingTimeInterval(-600), to: anchor.addingTimeInterval(600), value: 0)]
        #expect(RecordingKeepRules.evaluate(entry, candidates: entry.segments, signals: signals, calendar: utc()).isEmpty)
    }

    @Test func markKeepsItsNeighbourhood() {
        var entry = rolling(window())
        // Segment 10 starts at timeline 600, so 610 is 10 seconds into it, at wall-clock anchor + 10 s.
        entry.marks = [610]
        let keeps = RecordingKeepRules.evaluate(entry, candidates: entry.segments, signals: RecordingSignals(), calendar: utc())
        #expect(keeps[10]?.reason == .mark)
        #expect(Set(keeps.keys) == Set(5...15))
    }

    @Test func moodKeepsItsNeighbourhood() {
        let entry = rolling(window())
        var signals = RecordingSignals()
        signals.moods = [anchor]
        let keeps = RecordingKeepRules.evaluate(entry, candidates: entry.segments, signals: signals, calendar: utc())
        #expect(Set(keeps.keys) == Set(4...15))
        #expect(keeps.values.allSatisfy { $0.reason == .mood })
    }

    @Test func dailyCapBlocksHeartRateWhenBudgetIsSpent() {
        var segments = window()
        // 120 minutes already kept for heart rate today.
        for i in 0..<12 {
            var kept = RecordingSegment(index: 100 + i, start: 0, duration: 600, startedAt: anchor.addingTimeInterval(-10_800 - Double(i) * 700))
            kept.kept = RecordingKeep(reason: .heartRate, at: anchor)
            segments.append(kept)
        }
        let entry = rolling(segments)
        var signals = RecordingSignals()
        signals.restingHeartRate = 60
        signals.heartRate = pair()
        #expect(RecordingKeepRules.evaluate(entry, candidates: entry.segments, signals: signals, calendar: utc()).isEmpty)
    }

    @Test func heartRateOnlyMarksCandidatesAndChargesTheirMinutes() {
        var segments = window()
        // 114 minutes already kept for heart rate today: six more candidate minutes fit, twelve would not.
        for i in 0..<12 {
            var kept = RecordingSegment(index: 100 + i, start: 0, duration: 570, startedAt: anchor.addingTimeInterval(-10_800 - Double(i) * 700))
            kept.kept = RecordingKeep(reason: .heartRate, at: anchor)
            segments.append(kept)
        }
        let entry = rolling(segments)
        // Only segments 0...9 are past the window; 10...19 are still inside it.
        let candidates = entry.segments.filter { $0.index < 10 }
        var signals = RecordingSignals()
        signals.restingHeartRate = 60
        signals.heartRate = pair()
        let keeps = RecordingKeepRules.evaluate(entry, candidates: candidates, signals: signals, calendar: utc())
        #expect(Set(keeps.keys) == Set(4...9))
        // A bookmark is uncapped and still keeps its whole neighbourhood.
        var marked = rolling(window())
        marked.marks = [610]
        let markKeeps = RecordingKeepRules.evaluate(marked, candidates: marked.segments.filter { $0.index < 10 },
                                                    signals: RecordingSignals(), calendar: utc())
        #expect(Set(markKeeps.keys) == Set(5...15))
    }

    @Test func watchKeepsWhatThePhoneHasNotConfirmed() {
        var sent = segment(0, ago: 7 * 3600)
        sent.delivered = true
        let unsent = segment(1, ago: 7 * 3600)
        let unsentOld = segment(2, ago: 25 * 3600)
        let entry = rolling([sent, unsent, unsentOld])
        #expect(RecordingRolling.expiredOnWatch(entry, now: anchor).sorted() == [0, 2])
        var clip = entry
        clip.kind = nil
        #expect(RecordingRolling.expiredOnWatch(clip, now: anchor).isEmpty)
    }

    @Test func watchKeepsSegmentsNearABookmarkForKeptDays() {
        // Segments 0...19 started 2 days ago; a bookmark 10 seconds into segment 10.
        var segments = (0..<20).map { i in
            RecordingSegment(index: i, start: Double(i) * 60, duration: 60, startedAt: anchor.addingTimeInterval(-2 * 86_400 - 600 + Double(i) * 60))
        }
        // The bookmarked one itself already reached the phone; its record stays so the mark still maps to a time.
        segments[10].delivered = true
        var entry = rolling(segments)
        entry.marks = [610]
        #expect(Set(RecordingRolling.expiredOnWatch(entry, now: anchor)) == Set(0...19).subtracting(5...15))
        #expect(Set(RecordingRolling.expiredOnWatch(entry, now: anchor.addingTimeInterval(6 * 86_400))) == Set(0...19))
    }

    @Test func lateBookmarkedSegmentGetsPastTheTombstone() throws {
        let lib = try library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let entry = try lib.create(source: .watch, kind: .rolling)
        lib.addSegment(entry.id, segment(30, ago: 60))
        lib.prune(entry.id, indexes: Array(0...30))
        #expect(lib.entry(entry.id)?.prunedBelow == 31)
        var current = try #require(lib.entry(entry.id))
        current.marks = [1330]
        // Segment 22 (timeline 1320...1380) holds the bookmark; segment 0 is far from it and stays tombstoned.
        try lib.receive(RecordingTransfer(entry: current, segment: segment(22, ago: 7 * 3600)))
        try lib.receive(RecordingTransfer(entry: current, segment: segment(0, ago: 7 * 3600)))
        #expect(lib.entry(entry.id)?.segments.map(\.index) == [22])
        #expect(lib.entry(entry.id)?.marks == [1330])
    }

    @Test func receiveCreatesRollingEntryAndMergesErasedOnce() throws {
        let lib = try library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        var watch = RecordingEntry(id: UUID(), createdAt: anchor, source: .watch, kind: .rolling)
        let range = RecordingErase(from: anchor.addingTimeInterval(-100), to: anchor.addingTimeInterval(-50))
        watch.erased = [range]
        try lib.receive(RecordingTransfer(entry: watch, segment: segment(0, ago: 3600)))
        try lib.receive(RecordingTransfer(entry: watch, segment: segment(1, ago: 3500)))
        let entry = try #require(lib.entry(watch.id))
        #expect(entry.isRolling)
        #expect(entry.erased == [range])
        #expect(entry.segments.map(\.index) == [0, 1])
    }

    @Test func pruningAboveAGapDoesNotTombstoneTheGap() throws {
        let lib = try library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let entry = try lib.create(source: .watch, kind: .rolling)
        lib.addSegment(entry.id, segment(5, ago: 7 * 3600))
        lib.prune(entry.id, indexes: [5])
        var current = try #require(lib.entry(entry.id))
        #expect(current.prunedBelow == nil || current.prunedBelow == 0)
        #expect(current.prunedAbove == [5])
        #expect(current.isPruned(5) && !current.isPruned(2))
        #expect(current.nextIndex == 6)
        // Segment 2 never reached the phone before 5 was pruned; it still arrives and waits to be checked.
        try lib.receive(RecordingTransfer(entry: current, segment: segment(2, ago: 7 * 3600)))
        try lib.receive(RecordingTransfer(entry: current, segment: segment(5, ago: 7 * 3600)))
        #expect(lib.entry(entry.id)?.segments.map(\.index) == [2])
        // Once the gap closes the floor moves up and the sparse list empties.
        lib.prune(entry.id, indexes: [0, 1, 2, 3, 4])
        current = try #require(lib.entry(entry.id))
        #expect(current.prunedBelow == 6)
        #expect(current.prunedAbove == nil)
    }

    @Test func aFinishedSegmentAlreadyRegisteredKeepsItsAudio() throws {
        let lib = try library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let entry = try lib.create(source: .watch, kind: .rolling)
        try FileManager.default.createDirectory(at: lib.folder(entry.id), withIntermediateDirectories: true)
        let seg = segment(3, ago: 7 * 3600)
        try Data([1]).write(to: lib.audio(entry.id, 3))
        lib.addSegment(entry.id, seg)
        lib.change(entry.id) { $0.prunedBelow = 10; $0.segments[0].kept = RecordingKeep(reason: .mark, at: anchor) }
        // Stopping capture re-adds the writer's finished segments: the retained one must not lose its file.
        lib.addSegment(entry.id, seg)
        #expect(FileManager.default.fileExists(atPath: lib.audio(entry.id, 3).path))
        #expect(lib.entry(entry.id)?.segments.map(\.index) == [3])
    }
}
