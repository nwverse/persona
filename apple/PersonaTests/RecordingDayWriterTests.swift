import AVFoundation
import Foundation
import os
import Testing
@testable import Persona

private let anchor = Date(timeIntervalSince1970: 1_800_000_000) // 2027-01-15T08:00:00Z
private let rate = RecordingAudioSpec.sampleRate

/// 16 kHz mono audio made of runs of one value each (`(value, seconds)`), as the tap hands it to the writer.
private func audio(_ runs: [(value: Float, seconds: Double)]) throws -> AVAudioPCMBuffer {
    let format = try #require(AVAudioFormat(commonFormat: .pcmFormatFloat32, sampleRate: rate, channels: 1, interleaved: false))
    let counts = runs.map { Int(($0.seconds * rate).rounded()) }
    let buffer = try #require(AVAudioPCMBuffer(pcmFormat: format, frameCapacity: AVAudioFrameCount(counts.reduce(0, +))))
    let data = try #require(buffer.floatChannelData?[0])
    var at = 0
    for (run, count) in zip(runs, counts) {
        (data + at).update(repeating: run.value, count: count)
        at += count
    }
    buffer.frameLength = AVAudioFrameCount(at)
    return buffer
}

private func audio(_ value: Float, seconds: Double) throws -> AVAudioPCMBuffer { try audio([(value, seconds)]) }

/// A day recording's folder as the library names it: the recording's id.
private func folder(in root: URL) -> URL { root.appendingPathComponent(UUID().uuidString, isDirectory: true) }

private func id(of folder: URL) -> UUID? { UUID(uuidString: folder.lastPathComponent) }

private func samples(_ segment: RecordingSegmentWriter.Segment) -> Int { Int((segment.duration * rate).rounded()) }

private func files(in folder: URL) -> [String] {
    ((try? FileManager.default.contentsOfDirectory(atPath: folder.path)) ?? []).filter { $0.hasSuffix(".m4a") }.sorted()
}

/// Stands in for a writer event in `RecordingCapture.applyInOrder`: a name to follow it by, and the recordings it concerns.
private struct LoggedEvent {
    let name: String
    let owners: [UUID]
}

/// The writer of an all-day recording across midnight, fed without a microphone (`append`): which day every sample
/// lands in, the level of each chunk, and what it reports to whoever registers the chunks (docs/assistant/day-recap.md).
struct RecordingDayWriterTests {
    private let root = FileManager.default.temporaryDirectory.appendingPathComponent("day-writer-\(UUID().uuidString)")

    /// A writer of 10-second chunks into a new folder, which exists (the library makes it with the recording).
    private func makeWriter(firstIndex: Int = 0) throws -> (RecordingSegmentWriter, URL) {
        let today = folder(in: root)
        try FileManager.default.createDirectory(at: today, withIntermediateDirectories: true)
        let writer = RecordingSegmentWriter(directory: today, firstIndex: firstIndex)
        writer.segmentLength = RecordingDayContract.liveChunkSeconds
        return (writer, today)
    }

    @Test func midnightCutsOnTheExactSample() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let (writer, today) = try makeWriter()
        let tomorrow = folder(in: root)
        let midnight = anchor.addingTimeInterval(4)
        writer.setBoundary(at: midnight, nextDirectory: tomorrow)
        // One buffer from 4 s before midnight to 6 s after it.
        writer.append(try audio(0.1, seconds: 10), startedAt: anchor)

        let events = writer.takeEvents()
        #expect(events.count == 1)
        guard case .rotated(let rotation)? = events.first else {
            Issue.record("expected the midnight, got \(events)")
            return
        }
        let last = try #require(rotation.last, "the chunk open at midnight ends there")
        #expect(samples(last) == 64_000, "every sample before midnight and none after")
        #expect(last.owner == id(of: today) && last.index == 0 && last.startedAt == anchor)
        #expect(last.endedAt == midnight)
        #expect(rotation.previous == today && rotation.next == tomorrow)
        #expect(rotation.boundary == midnight && rotation.firstSampleAt == midnight)

        let first = try #require(writer.stop(), "the new day's open chunk")
        #expect(samples(first) == 96_000)
        #expect(first.owner == id(of: tomorrow) && first.index == 0 && first.start == 0)
        #expect(first.startedAt == midnight)
        // Both files hold their audio: the old one about 4 s, the new one about 6 s (AAC packets are 1024 samples).
        let before = try AVAudioFile(forReading: last.url).length
        let after = try AVAudioFile(forReading: first.url).length
        #expect(abs(before - 64_000) <= 2_048 && abs(after - 96_000) <= 2_048, "\(before) and \(after) samples")
    }

    @Test func aMidnightBetweenTwoSamplesGoesToTheLaterOne() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let tenSeconds = AVAudioFrameCount(10 * rate)
        let count = { (start: Date, boundary: Date) in RecordingSegmentWriter.frames(from: start, before: boundary, count: tenSeconds) }
        #expect(count(anchor, anchor.addingTimeInterval(4)) == 64_000)
        // Half a sample after sample 64 000: that sample is still before it.
        #expect(count(anchor, anchor.addingTimeInterval(4 + 0.5 / rate)) == 64_001)
        // The tiny error of subtracting two dates never moves the cut by a sample.
        #expect(count(anchor.addingTimeInterval(0.1), anchor.addingTimeInterval(4.1)) == 64_000)
        #expect(count(anchor, anchor.addingTimeInterval(-1)) == 0, "a midnight before the buffer: all of it is the next day's")
        #expect(count(anchor, anchor.addingTimeInterval(60)) == tenSeconds, "a midnight after it: none")

        let (writer, _) = try makeWriter()
        let midnight = anchor.addingTimeInterval(4 + 0.5 / rate)
        writer.setBoundary(at: midnight, nextDirectory: folder(in: root))
        writer.append(try audio(0.1, seconds: 10), startedAt: anchor)
        guard case .rotated(let rotation)? = writer.takeEvents().first else {
            Issue.record("expected the midnight")
            return
        }
        let last = try #require(rotation.last)
        #expect(samples(last) == 64_001)
        #expect(rotation.firstSampleAt == anchor.addingTimeInterval(64_001 / rate), "the next day starts at its first sample")
        let first = try #require(writer.stop())
        #expect(samples(first) == 160_000 - 64_001)
    }

    @Test func rotationThenStopKeepsEachDaysChunksApart() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let (writer, today) = try makeWriter()
        let tomorrow = folder(in: root)
        let reported = OSAllocatedUnfairLock(initialState: [String]())
        writer.onSegment = { segment in reported.withLock { $0.append("segment \(segment.index)") } }
        writer.onRotate = { last, next in reported.withLock { $0.append("midnight after \(last?.index ?? -1) into \(next.lastPathComponent)") } }
        writer.setBoundary(at: anchor.addingTimeInterval(25), nextDirectory: tomorrow)
        #expect(!FileManager.default.fileExists(atPath: tomorrow.path))

        writer.append(try audio(0.1, seconds: 30), startedAt: anchor)
        // Chunks 0 and 1 close by length, chunk 2 at midnight; the next day's chunk 0 is open.
        #expect(reported.withLock { $0 } == ["segment 0", "segment 1", "midnight after 2 into \(tomorrow.lastPathComponent)"])
        #expect(FileManager.default.fileExists(atPath: tomorrow.path), "the next day's folder is made before its first file")
        #expect(abs(writer.elapsed - 5) < 0.001, "the new day's timeline starts at midnight")

        let stopped = try #require(writer.stop())
        #expect(stopped.owner == id(of: tomorrow) && stopped.index == 0 && stopped.start == 0)
        #expect(stopped.startedAt == anchor.addingTimeInterval(25) && stopped.duration == 5)

        let finished = writer.finishedSegments
        #expect(finished.map(\.owner) == [today, today, today, tomorrow].map(id(of:)))
        #expect(finished.map(\.index) == [0, 1, 2, 0], "indexes start again with the new day")
        #expect(finished.map(\.startedAt) == [0, 10, 20, 25].map { anchor.addingTimeInterval($0) })
        #expect(finished.map(\.duration) == [10, 10, 5, 5])
        #expect(writer.finishedSegments.isEmpty, "taken once: a later stop or wake-up registers nothing again")
        #expect(files(in: today) == ["seg-000.m4a", "seg-001.m4a", "seg-002.m4a"])
        #expect(files(in: tomorrow) == ["seg-000.m4a"])
    }

    /// What capture already took is never reported again: a stop after a midnight has only the new day's tail, so the
    /// old day's chunks can't be registered a second time under the new day.
    @Test func stopAfterTheDayWasTakenReportsOnlyTheNewDay() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let (writer, today) = try makeWriter()
        let tomorrow = folder(in: root)
        writer.setBoundary(at: anchor.addingTimeInterval(25), nextDirectory: tomorrow)
        writer.append(try audio(0.1, seconds: 30), startedAt: anchor)

        let taken = writer.takeEvents()
        #expect(taken.count == 3)
        guard taken.count == 3, case .segment(let zero) = taken[0], case .segment(let one) = taken[1],
              case .rotated(let rotation) = taken[2] else {
            Issue.record("expected two chunks then the midnight, got \(taken)")
            return
        }
        #expect([zero, one].map(\.index) == [0, 1] && [zero, one].allSatisfy { $0.owner == id(of: today) })
        #expect(rotation.last?.index == 2 && rotation.last?.owner == id(of: today))

        let tail = try #require(writer.stop())
        #expect(writer.takeEvents() == [.segment(tail)])
        #expect(tail.owner == id(of: tomorrow) && tail.index == 0)
    }

    @Test func twoMidnightsInARow() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let (writer, today) = try makeWriter()
        let second = folder(in: root), third = folder(in: root)
        writer.setBoundary(at: anchor.addingTimeInterval(25), nextDirectory: second)
        writer.append(try audio(0.1, seconds: 30), startedAt: anchor)
        #expect(writer.takeEvents().count == 3)

        // The next midnight falls exactly where the second day's chunk 1 ends by length: no chunk is open there.
        writer.setBoundary(at: anchor.addingTimeInterval(45), nextDirectory: third)
        writer.append(try audio(0.1, seconds: 30), startedAt: anchor.addingTimeInterval(30))
        let events = writer.takeEvents()
        #expect(events.count == 4)
        guard events.count == 4, case .segment(let b0) = events[0], case .segment(let b1) = events[1],
              case .rotated(let rotation) = events[2], case .segment(let c0) = events[3] else {
            Issue.record("expected two chunks, the midnight, a chunk; got \(events)")
            return
        }
        #expect([b0, b1].map(\.index) == [0, 1] && [b0, b1].allSatisfy { $0.owner == id(of: second) })
        #expect(b1.endedAt == anchor.addingTimeInterval(45))
        #expect(rotation.last == nil && rotation.previous == second && rotation.next == third)
        #expect(rotation.firstSampleAt == anchor.addingTimeInterval(45))
        #expect(c0.owner == id(of: third) && c0.index == 0 && c0.startedAt == anchor.addingTimeInterval(45))
        let tail = try #require(writer.stop())
        #expect(tail.owner == id(of: third) && tail.index == 1)
        #expect(files(in: today).count == 3 && files(in: second).count == 2 && files(in: third).count == 2)
    }

    /// A day continued after a pause past midnight (no buffer came at midnight): the old day's last chunk ends where the
    /// audio stopped, and the new day starts at its first sample, so its date comes from that sample.
    @Test func audioBackAfterMidnightStartsTheDayAtItsFirstSample() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let (writer, today) = try makeWriter()
        let tomorrow = folder(in: root)
        writer.append(try audio(0.1, seconds: 15), startedAt: anchor)
        writer.setBoundary(at: anchor.addingTimeInterval(20), nextDirectory: tomorrow)
        writer.append(try audio(0.1, seconds: 5), startedAt: anchor.addingTimeInterval(3_600))

        let events = writer.takeEvents()
        #expect(events.count == 2)
        guard events.count == 2, case .rotated(let rotation) = events[1] else {
            Issue.record("expected a chunk then the midnight, got \(events)")
            return
        }
        let last = try #require(rotation.last)
        #expect(last.owner == id(of: today) && last.index == 1 && last.duration == 5)
        #expect(last.endedAt == anchor.addingTimeInterval(15))
        #expect(rotation.boundary == anchor.addingTimeInterval(20))
        #expect(rotation.firstSampleAt == anchor.addingTimeInterval(3_600))
        let first = try #require(writer.stop())
        #expect(first.owner == id(of: tomorrow) && first.index == 0 && first.startedAt == anchor.addingTimeInterval(3_600))
    }

    @Test func levelIsEachChunksMeanPowerInDBFS() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let (writer, today) = try makeWriter()
        let tomorrow = folder(in: root)
        writer.append(try audio(0, seconds: 10), startedAt: anchor)
        writer.append(try audio(0.1, seconds: 10), startedAt: anchor.addingTimeInterval(10))
        // Half speech, half silence: the power average (−23 dB), not the average of the two levels.
        writer.append(try audio([(0.1, 5), (0, 5)]), startedAt: anchor.addingTimeInterval(20))
        // A loud chunk cut at midnight, and a silent one after it: each file counts only its own samples.
        writer.setBoundary(at: anchor.addingTimeInterval(35), nextDirectory: tomorrow)
        writer.append(try audio([(0.5, 5), (0, 5)]), startedAt: anchor.addingTimeInterval(30))
        writer.stop()

        let chunks = writer.finishedSegments
        #expect(chunks.map(\.owner) == [today, today, today, today, tomorrow].map(id(of:)))
        let expected = [RecordingDayLevel.floor, 20 * log10(0.1), 10 * log10(0.005), 20 * log10(0.5), RecordingDayLevel.floor]
        #expect(chunks.count == expected.count)
        for (chunk, level) in zip(chunks, expected) {
            #expect(abs(chunk.level - level) < 0.01, "chunk \(chunk.index) of \(chunk.owner?.uuidString ?? "?"): \(chunk.level) dBFS, expected \(level)")
        }
        #expect(chunks[0].level < RecordingDayContract.silenceDBFS && chunks[2].level > RecordingDayContract.silenceDBFS)
    }

    @Test func switchDirectoryCutsMovesOnAndDropsTheOldMidnight() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let (writer, today) = try makeWriter()
        let next = folder(in: root), stale = folder(in: root)
        writer.append(try audio(0.1, seconds: 15), startedAt: anchor)
        writer.setBoundary(at: anchor.addingTimeInterval(100), nextDirectory: stale)

        // Today's recording was already open with chunks up to 6: the writer goes on at its next seq.
        let cut = try #require(writer.switchDirectory(to: next, firstIndex: 7), "the open chunk is cut first")
        #expect(cut.owner == id(of: today) && cut.index == 1 && cut.duration == 5)
        #expect(FileManager.default.fileExists(atPath: next.path))
        #expect(writer.elapsed == 0, "a new timeline")

        // Audio past the old midnight no longer rotates: that midnight belonged to the day left behind.
        writer.append(try audio(0.1, seconds: 25), startedAt: anchor.addingTimeInterval(90))
        let tail = try #require(writer.stop())
        let events = writer.takeEvents()
        let segments = events.compactMap { event -> RecordingSegmentWriter.Segment? in
            if case .segment(let segment) = event { return segment }
            return nil
        }
        #expect(segments.count == events.count, "no midnight was crossed")
        #expect(segments.map(\.index) == [0, 1, 7, 8, 9])
        #expect(segments.map(\.owner) == [today, today, next, next, next].map(id(of:)))
        #expect(segments.suffix(3).map(\.start) == [0, 10, 20])
        #expect(segments.last == tail)
        #expect(!FileManager.default.fileExists(atPath: stale.path))
        #expect(files(in: next) == ["seg-007.m4a", "seg-008.m4a", "seg-009.m4a"])
    }

    /// "Erase 10 min" cuts the open chunk and erases up to where its audio ends: the chunk cut falls in the range however
    /// short it is, and the chunk after it starts exactly there, so it keeps all of its audio (C5/C12).
    @Test func anEraseCutEndsWhereTheAudioEndsAndTheNextChunkStartsThere() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let (writer, today) = try makeWriter()
        writer.append(try audio(0.1, seconds: 15), startedAt: anchor)
        let cut = writer.cutForErase()
        let closed = try #require(cut.segment, "the open chunk is cut")
        #expect(closed.owner == id(of: today) && closed.index == 1 && closed.duration == 5)
        let through = try #require(cut.through)
        #expect(through == closed.endedAt && through == anchor.addingTimeInterval(15))
        #expect(writer.cutForErase() == RecordingSegmentWriter.Cut(segment: nil, through: through), "nothing open: nothing cut")

        // The next buffer's host-clock time says it began 20 ms before the cut's last sample ended.
        writer.append(try audio(0.1, seconds: 3), startedAt: through.addingTimeInterval(-0.02))
        let next = try #require(writer.stop())
        #expect(next.index == 2 && next.startedAt == through, "starts no earlier than the erased range ends")

        let range = RecordingErase(from: through.addingTimeInterval(-RecordingRolling.eraseMinutes * 60), to: through)
        let record = { (segment: RecordingSegmentWriter.Segment) in
            RecordingSegment(index: segment.index, start: segment.start, duration: segment.duration, startedAt: segment.startedAt)
        }
        #expect(record(closed).fallsInDayErase(range))
        #expect(!record(next).fallsInDayErase(range))
    }

    /// The floor an erase leaves only absorbs the host clock's jitter: a clock that really moved back keeps its time.
    @Test func anEraseCutKeepsAClockThatMovedBack() throws {
        defer { try? FileManager.default.removeItem(at: root) }
        let (writer, _) = try makeWriter()
        writer.append(try audio(0.1, seconds: 5), startedAt: anchor)
        let through = try #require(writer.cutForErase().through)
        writer.append(try audio(0.1, seconds: 2), startedAt: through.addingTimeInterval(-3))
        let next = try #require(writer.stop())
        #expect(next.startedAt == through.addingTimeInterval(-3))
    }
}

/// Capture's all-day decisions that need no microphone: the day check, the audio session events, the launch halt.
struct RecordingDayCaptureRulesTests {
    private let newYork = TimeZone(identifier: "America/New_York")!
    private let london = TimeZone(identifier: "Europe/London")!

    private func utc(_ text: String) throws -> Date { try Date.ISO8601FormatStyle().parse(text) }

    @Test func dayCheckKeepsTheDayUntilItsOwnMidnightAcrossDST() throws {
        // 2026-11-01 has 25 hours in New York, 2026-03-08 has 23.
        let fallBack = try #require(RecordingCapture.endOfDay("2026-11-01", in: newYork))
        let fallBackStart = try utc("2026-11-01T04:00:00Z")
        #expect(fallBack.timeIntervalSince(fallBackStart) == 25 * 3_600)
        let springForward = try #require(RecordingCapture.endOfDay("2026-03-08", in: newYork))
        let springStart = try utc("2026-03-08T05:00:00Z")
        #expect(springForward.timeIntervalSince(springStart) == 23 * 3_600)

        let noon = try utc("2026-11-01T17:00:00Z")
        #expect(RecordingCapture.dayCheck(entryDay: "2026-11-01", now: noon, zone: newYork, running: true, plannedBoundary: nil)
                == .keep(boundary: fallBack))
        // An hour before the end of the 25-hour day, which a 24-hour count would already call tomorrow.
        let late = try utc("2026-11-02T04:00:00Z")
        #expect(RecordingCapture.dayCheck(entryDay: "2026-11-01", now: late, zone: newYork, running: false, plannedBoundary: fallBack)
                == .keep(boundary: fallBack))
    }

    @Test func dayCheckLetsTheWriterCutAtItsOwnMidnight() throws {
        let midnight = try #require(RecordingCapture.endOfDay("2026-11-01", in: newYork))
        let justAfter = midnight.addingTimeInterval(0.2)
        #expect(RecordingCapture.dayCheck(entryDay: "2026-11-01", now: justAfter, zone: newYork, running: true, plannedBoundary: midnight)
                == .awaitWriter, "a buffer is due: the cut stays exact to the sample")
        #expect(RecordingCapture.dayCheck(entryDay: "2026-11-01", now: justAfter, zone: newYork, running: false, plannedBoundary: midnight)
                == .switchNow, "paused past midnight: nothing else will move it")
        #expect(RecordingCapture.dayCheck(entryDay: "2026-11-01", now: justAfter, zone: newYork, running: true, plannedBoundary: nil)
                == .switchNow)
        #expect(RecordingCapture.dayCheck(entryDay: "2026-11-01", now: midnight.addingTimeInterval(3 * 86_400), zone: newYork,
                                          running: false, plannedBoundary: midnight) == .switchNow, "paused over several midnights")
        #expect(RecordingCapture.dayCheck(entryDay: "not a day", now: justAfter, zone: newYork, running: true, plannedBoundary: nil)
                == .switchNow)
    }

    @Test func dayCheckFollowsTheTimeZone() throws {
        // 22:00 in New York on March 8 is 02:00 on March 9 in London: flying east ends the day now, although the
        // writer's New York midnight is still two hours away.
        let evening = try utc("2026-03-09T02:00:00Z")
        let newYorkMidnight = try #require(RecordingCapture.endOfDay("2026-03-08", in: newYork))
        #expect(RecordingCapture.dayCheck(entryDay: "2026-03-08", now: evening, zone: london, running: true, plannedBoundary: newYorkMidnight)
                == .switchNow)
        // Flying west: March 9 began in London and has not come yet in New York; the recording keeps it, up to its end there.
        let west = try utc("2026-03-09T03:00:00Z")
        let newYorkEnd = try utc("2026-03-10T04:00:00Z")
        #expect(RecordingCapture.dayCheck(entryDay: "2026-03-09", now: west, zone: newYork, running: true, plannedBoundary: nil)
                == .keep(boundary: newYorkEnd))
    }

    /// What the writer logged is applied in order; one that can't be saved holds everything after it that concerns any
    /// of its recordings (a midnight concerns both days), and the rest goes on. The next try starts with what was held.
    @Test func writerEventsApplyInOrderAndHoldWhatFollowsAFailedSave() {
        let a = UUID(), b = UUID(), c = UUID()
        let log = [
            LoggedEvent(name: "a0", owners: [a]), LoggedEvent(name: "b0", owners: [b]), LoggedEvent(name: "a1", owners: [a]),
            LoggedEvent(name: "b1", owners: [b]), LoggedEvent(name: "midnight b→c", owners: [b, c]),
            LoggedEvent(name: "c0", owners: [c]), LoggedEvent(name: "a2", owners: [a]),
        ]
        var tried: [String] = []
        let first = RecordingCapture.applyInOrder(log, owners: { $0.owners }) { event in
            tried.append(event.name)
            return event.name == "b0" ? nil : event.owners
        }
        #expect(tried == ["a0", "b0", "a1", "a2"], "nothing after b0 that concerns b, or c through the midnight, is tried")
        #expect(first.changed == [a])
        #expect(first.kept.map(\.name) == ["b0", "b1", "midnight b→c", "c0"])

        tried = []
        let retry = RecordingCapture.applyInOrder(first.kept, owners: { $0.owners }) { event in
            tried.append(event.name)
            return event.owners
        }
        #expect(tried == ["b0", "b1", "midnight b→c", "c0"])
        #expect(retry.changed == [b, c] && retry.kept.isEmpty)
    }

    /// A chunk carries the reason for the audio missing before it only when it ends after the microphone opened again:
    /// one finished before the pause and saved late takes none.
    @Test func theGapGoesToTheFirstChunkThatEndsAfterTheMicrophoneOpened() {
        let paused = RecordingCapture.PendingGap(reason: RecordingDayContract.Reason.paused, since: anchor.addingTimeInterval(100))
        let early = RecordingCapture.gap(endingAt: anchor.addingTimeInterval(90), in: [paused])
        #expect(early.reason == nil && early.rest == [paused])
        let first = RecordingCapture.gap(endingAt: anchor.addingTimeInterval(110), in: [paused])
        #expect(first.reason == RecordingDayContract.Reason.paused && first.rest.isEmpty)
        // The microphone came back and a call took it again before any audio: the newest gap is the one before the chunk.
        let call = RecordingCapture.PendingGap(reason: RecordingDayContract.Reason.interrupted, since: anchor.addingTimeInterval(200))
        let both = RecordingCapture.gap(endingAt: anchor.addingTimeInterval(250), in: [paused, call])
        #expect(both.reason == RecordingDayContract.Reason.interrupted && both.rest.isEmpty)
    }

    /// A day chunk is never dropped (an erased one is kept as an erased marker), so a missing record after `addSegment`
    /// is a save that failed and is tried again; a clip's audio in an erased range, or a pruned seq, is dropped on purpose.
    @Test func aDayChunkCountsAsSavedOnlyWhenItsRecordIsThere() {
        var day = RecordingEntry(id: UUID(), createdAt: anchor, source: .watch, locale: "en-US", kind: .day)
        day.erased = [RecordingErase(from: anchor, to: anchor.addingTimeInterval(60))]
        let chunk = RecordingSegment(index: 3, start: 30, duration: 10, startedAt: anchor.addingTimeInterval(30))
        #expect(!RecordingCapture.isSettled(chunk, in: day))
        #expect(!RecordingCapture.isSettled(chunk, in: nil))
        var saved = day
        saved.segments = [chunk]
        #expect(RecordingCapture.isSettled(chunk, in: saved))
        var pruned = day
        pruned.prunedBelow = 5
        #expect(RecordingCapture.isSettled(chunk, in: pruned))
        var clip = day
        clip.kind = nil
        #expect(RecordingCapture.isSettled(chunk, in: clip))
    }

    @Test func sessionEventsPauseAndResumeOnlyWhatTheyShould() {
        let reaction = RecordingCapture.reaction
        #expect(reaction(.interrupted(reason: .default), true, false) == .pause, "a call during a recording takes the old began path")
        #expect(reaction(.interrupted(reason: nil), false, false) == .ignore, "nothing running: nothing to pause")
        #expect(reaction(.interruptionEnded(shouldResume: false), false, true) == .resume, "the meeting goes on whatever the advice")
        #expect(reaction(.interruptionEnded(shouldResume: true), false, false) == .ignore, "the user's own pause stays")
        for recording in [true, false] {
            for paused in [true, false] {
                #expect(reaction(.deactivatedByApp, recording, paused) == .ignore, "our own setActive(false) is no interruption")
                #expect(reaction(.activated, recording, paused) == .ignore)
            }
        }
    }
}

/// The launch halt (C35): a day recording left open while the user wanted all-day on is reported once, the newest.
@MainActor struct RecordingDayLaunchHaltTests {
    @Test func aDayLeftOpenWithAllDayOnIsReportedAtLaunch() throws {
        let lib = RecordingLibrary(root: FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString))
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let yesterday = try lib.create(source: .watch, kind: .day, createdAt: anchor.addingTimeInterval(-86_400), day: "2027-01-14",
                                       timeZone: "Asia/Shanghai")
        let today = try lib.create(source: .watch, kind: .day, createdAt: anchor, day: "2027-01-15", timeZone: "Asia/Shanghai")
        let rolling = try lib.create(source: .watch, kind: .rolling, createdAt: anchor.addingTimeInterval(60))

        let recovered = lib.recover(source: .watch)
        #expect(Set(recovered) == [yesterday.id, today.id], "only day recordings stay open")
        #expect(lib.entry(rolling.id)?.active == false)
        #expect(lib.entry(today.id)?.gapReason == RecordingDayContract.Reason.stopped)

        let halt = try #require(RecordingCapture.launchHalt(recovered: recovered, entries: lib.entries, intent: true))
        #expect(halt.id == today.id && halt.reason == .crashed)
        #expect(RecordingCapture.launchHalt(recovered: recovered, entries: lib.entries, intent: false) == nil,
                "all-day off: the user stopped for today")
        #expect(RecordingCapture.launchHalt(recovered: [], entries: lib.entries, intent: true) == nil)
        #expect(RecordingCapture.launchHalt(recovered: [rolling.id], entries: lib.entries, intent: true) == nil)
    }
}
