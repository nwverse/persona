import Foundation
import os
import Testing
@testable import Persona

private let anchor = Date(timeIntervalSince1970: 1_800_000_000) // 2027-01-15T08:00:00Z, 16:00 in Shanghai
private let day: TimeInterval = 86_400

private func date(_ iso: String) throws -> Date { try Date.ISO8601FormatStyle().parse(iso) }

/// Chunk `seq` of a day, 10 s at `offset` from `anchor`, with its audio on the phone unless `hasAudio` is false.
private func chunk(_ seq: Int, at offset: Double, state: RecordingDayChunk.State, hasAudio: Bool = true) -> RecordingDayChunk {
    RecordingDayChunk(seq: seq, startedAt: anchor.addingTimeInterval(offset), duration: 10, receivedAt: anchor, state: state,
                      sentences: state == .transcribed ? [RecordingSentence(start: 1, end: 2, text: "x")] : nil, hasAudio: hasAudio)
}

private func record(day: String = "2027-01-15", zone: String = "Asia/Shanghai", chunks: [RecordingDayChunk],
                    finals: [Date?] = []) -> RecordingDayRecord {
    RecordingDayRecord(id: UUID(), day: day, timeZone: zone, startedAt: anchor,
                       chunks: Dictionary(uniqueKeysWithValues: chunks.map { ($0.seq, $0) }),
                       parts: finals.enumerated().map { RecordingDayPart(hubID: UUID(), part: $0.offset + 1, startedAt: anchor,
                                                                          recapFinalAt: $0.element) })
}

/// All-day recording's retention on the phone (docs/assistant/day-recap.md "Retention and privacy", C21): audio 3 days
/// after the final recap and at most 7 days after the day whatever reached the hub; the whole day at `recording_days`.
@MainActor struct RecordingDayKeeperTests {
    @Test func audioGoesThreeDaysAfterTheFinalRecapAndSevenAfterTheDayAtTheLatest() throws {
        // The day ends at midnight in Shanghai: 2027-01-15T16:00:00Z.
        let end = try date("2027-01-15T16:00:00Z")
        let recap = end.addingTimeInterval(3600)
        let held = record(chunks: [chunk(0, at: 0, state: .transcribed), chunk(1, at: 10, state: .skipped),
                                   chunk(2, at: 20, state: .failed), chunk(3, at: 30, state: .waiting),
                                   chunk(4, at: 40, state: .transcribed, hasAudio: false)],
                          finals: [recap])
        #expect(RecordingDayRetention.dayEnd(held) == end)
        #expect(RecordingDayRetention.audioDue(held, now: recap.addingTimeInterval(3 * day - 1)).isEmpty)
        #expect(RecordingDayRetention.audioDue(held, now: recap.addingTimeInterval(3 * day)) == [0, 1])
        #expect(RecordingDayRetention.nextDeadline(held, now: recap.addingTimeInterval(3 * day)) == end.addingTimeInterval(7 * day))
        // Untranscribed audio waits for a retry until the cap, then goes too: its time stays an untranscribed gap.
        #expect(RecordingDayRetention.audioDue(held, now: end.addingTimeInterval(7 * day)) == [0, 1, 2, 3])

        // No hub ever wrote a recap (an older hub, no flag): the cap alone, whether or not anything was uploaded.
        let alone = record(chunks: [chunk(0, at: 0, state: .transcribed)])
        #expect(RecordingDayRetention.audioDue(alone, now: end.addingTimeInterval(7 * day - 1)).isEmpty)
        #expect(RecordingDayRetention.audioDue(alone, now: end.addingTimeInterval(7 * day)) == [0])

        // Several parts: the clock starts once every part has its final recap, from the latest.
        let later = recap.addingTimeInterval(day)
        let parts = record(chunks: [chunk(0, at: 0, state: .transcribed)], finals: [recap, later])
        #expect(RecordingDayRetention.recapFinalAt(parts) == later)
        #expect(RecordingDayRetention.recapFinalAt(record(chunks: [], finals: [recap, nil])) == nil)
        #expect(RecordingDayRetention.audioDue(parts, now: later.addingTimeInterval(3 * day)) == [0])
    }

    @Test func theCapCountsFromTheDatesOwnMidnightOnDSTDays() throws {
        // America/New_York: 2026-03-08 has 23 hours, 2026-11-01 has 25.
        let spring = record(day: "2026-03-08", zone: "America/New_York", chunks: [chunk(0, at: 0, state: .waiting)])
        let springEnd = try date("2026-03-09T04:00:00Z")
        #expect(RecordingDayRetention.dayEnd(spring) == springEnd)
        let fall = record(day: "2026-11-01", zone: "America/New_York", chunks: [chunk(0, at: 0, state: .waiting)])
        let fallEnd = try date("2026-11-02T05:00:00Z")
        #expect(RecordingDayRetention.dayEnd(fall) == fallEnd)
        let held = try #require(fall.chunks[0])
        #expect(RecordingDayRetention.audioDeadline(held, in: fall) == fallEnd.addingTimeInterval(7 * day))
    }

    @Test func theKeeperDeletesDueAudioAndPurgesDaysPastRecordingDays() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent("day-keeper-\(UUID().uuidString)", isDirectory: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let library = RecordingLibrary(root: root)
        let lib = library
        let clock = OSAllocatedUnfairLock(initialState: anchor)
        let store = RecordingDayStore(root: root, isDeleted: { lib.isDeletedSnapshot($0) }, zone: { TimeZone(identifier: "Asia/Shanghai")! },
                                      now: { clock.withLock { $0 } }, writeDelay: 3600)
        /// A day recorded `ago` days before `anchor`, with two chunks, neither transcribed yet.
        func recorded(daysAgo ago: Double) throws -> UUID {
            let id = UUID()
            let start = anchor.addingTimeInterval(-ago * day)
            let name = RecordingDayCalendar.day(of: start, in: TimeZone(identifier: "Asia/Shanghai")!)
            for seq in 0..<2 {
                let header = RecordingDayHeader(recording: id, day: name, seq: seq, startedAt: start.addingTimeInterval(Double(seq) * 10),
                                                duration: 10, level: -30, recordingStartedAt: start)
                _ = try store.store(header: header, audio: Data([1, 2, 3]))
            }
            library.adoptDay(id: id, day: name, timeZone: "Asia/Shanghai", createdAt: start)
            return id
        }
        let old = try recorded(daysAgo: 15), recent = try recorded(daysAgo: 5)
        await store.update(recent) { record in
            record.chunks[0]?.state = .transcribed
            record.chunks[0]?.sentences = []
            record.parts = [RecordingDayPart(hubID: record.id, part: 1, startedAt: anchor, recapFinalAt: anchor.addingTimeInterval(-4 * day))]
        }
        var forgotten: [UUID] = []
        var window: Int? = nil
        let keeper = RecordingDayKeeper(store: store, library: library, days: { window }, now: { anchor },
                                        forget: { forgotten.append($0); store.didRemove($0) })

        // Until the hub said how long it keeps recordings, no day goes for its age.
        await keeper.run(force: true)
        #expect(library.entry(old) != nil && forgotten.isEmpty)
        // The recent day's transcribed chunk is 4 days past its final recap; the other waits for the 7-day cap.
        #expect(!FileManager.default.fileExists(atPath: store.audioURL(recent, seq: 0).path))
        #expect(FileManager.default.fileExists(atPath: store.audioURL(recent, seq: 1).path))
        #expect(await store.record(recent)?.chunks[0]?.hasAudio == false)
        #expect(await store.record(recent)?.chunks[0]?.state == .transcribed, "the text stays")
        // The old day's audio is past the cap, transcribed or not.
        #expect(!FileManager.default.fileExists(atPath: store.audioURL(old, seq: 0).path))
        #expect(!FileManager.default.fileExists(atPath: store.audioURL(old, seq: 1).path))

        window = 14
        await keeper.run(force: true)
        #expect(forgotten == [old])
        #expect(library.entry(old) == nil && library.isDeleted(old), "tombstoned: a late chunk never brings it back")
        #expect(!FileManager.default.fileExists(atPath: store.folder(old).path))
        #expect(await store.record(old) == nil)
        #expect(library.entry(recent) != nil)
        #expect(await store.record(recent) != nil)
    }
}
