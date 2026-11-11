import Foundation
import Testing
@testable import Persona

private let start = Date(timeIntervalSince1970: 1_800_000_000) // 2027-01-15T08:00:00Z

/// Chunk `seq` `offset` seconds into the day as the phone holds it. A transcribed chunk gets one sentence per position
/// in `positions` (part 1) unless `sentences` says otherwise; it arrived `received` seconds after `start` (its end by
/// default).
private func chunk(_ seq: Int, at offset: Double, duration: Double = 10, state: RecordingDayChunk.State = .transcribed,
                   sentences: Int? = nil, positions: [Int] = [], part: Int = 1, after: String? = nil, erased: Bool = false,
                   lost: Bool = false, end: Bool = false, received: Double? = nil) -> RecordingDayChunk {
    let count = sentences ?? positions.count
    let text = state == .transcribed ? (0..<count).map { RecordingSentence(start: Double($0) + 1, end: Double($0) + 2, text: "s\($0)") } : nil
    return RecordingDayChunk(seq: seq, startedAt: start.addingTimeInterval(offset), duration: duration, level: -30, after: after,
                             erased: erased, lost: lost, end: end, receivedAt: start.addingTimeInterval(received ?? offset + duration),
                             state: state, sentences: text, hasAudio: !erased && !lost && duration > 0,
                             positions: positions.isEmpty ? nil : positions.map { RecordingDayPosition(part: part, pos: $0) })
}

private func day(_ chunks: [RecordingDayChunk], lost: Set<Int> = [], expected: Int? = nil, endedAt: Date? = nil) -> RecordingDayRecord {
    RecordingDayRecord(id: UUID(), day: "2027-01-15", timeZone: "Asia/Shanghai", startedAt: start,
                       chunks: Dictionary(uniqueKeysWithValues: chunks.map { ($0.seq, $0) }), lost: lost, expected: expected, endedAt: endedAt)
}

private func part(_ number: Int = 1, at offset: Double = 0, count: Int, endedAt: Double? = nil) -> RecordingDayPart {
    RecordingDayPart(hubID: UUID(), part: number, startedAt: start.addingTimeInterval(offset),
                     endedAt: endedAt.map { start.addingTimeInterval($0) }, created: true, base: 1, next: count, count: count)
}

/// The coverage the phone sends with every create, repair and append (docs/assistant/day-recap.md "coverage"): `to`
/// never passes text the hub won't hold after the request (C19), and the gaps say why time has no text.
struct RecordingDayCoverageTests {
    private let now = start.addingTimeInterval(3600)

    @Test func toStopsAtTextTheHubDoesNotHaveYet() {
        let record = day([chunk(0, at: 0, positions: [0, 1]), chunk(1, at: 10, positions: [2]), chunk(2, at: 20, positions: [3])])
        let p = part(count: 4)
        #expect(RecordingDayCoverage.compute(record: record, part: p, sentToPositionBelow: 0, now: now).to == 0)
        #expect(RecordingDayCoverage.compute(record: record, part: p, sentToPositionBelow: 2, now: now).to == 10)
        #expect(RecordingDayCoverage.compute(record: record, part: p, sentToPositionBelow: 3, now: now).to == 20,
                "chunk 2's sentence is at position 3, past what this request leaves on the hub")
        #expect(RecordingDayCoverage.compute(record: record, part: p, sentToPositionBelow: 4, now: now).to == 30)

        // Transcribed text without a position yet, or a chunk still waiting, holds `to` back whatever the count.
        let unplaced = day([chunk(0, at: 0, positions: [0]), chunk(1, at: 10, sentences: 2, positions: [1]), chunk(2, at: 20, positions: [3])])
        #expect(RecordingDayCoverage.compute(record: unplaced, part: p, sentToPositionBelow: 100, now: now).to == 10)
        let waiting = day([chunk(0, at: 0, positions: [0]), chunk(1, at: 10, state: .waiting), chunk(2, at: 20, positions: [1])])
        #expect(RecordingDayCoverage.compute(record: waiting, part: p, sentToPositionBelow: 100, now: now).to == 10)
    }

    @Test func settledChunksCountAsCoveredAndReportTheirGap() {
        let record = day([
            chunk(0, at: 0, state: .skipped),
            chunk(1, at: 10, state: .waiting, erased: true),
            chunk(2, at: 20, state: .waiting, lost: true),
            chunk(3, at: 30, state: .failed),
            chunk(4, at: 40, positions: [0]),
        ])
        let coverage = RecordingDayCoverage.compute(record: record, part: part(count: 1), sentToPositionBelow: 1, now: now)
        #expect(coverage.to == 50)
        #expect(coverage.gaps == [
            RecordingDayGap(from: 10, to: 20, reason: RecordingDayGap.Reason.erased),
            RecordingDayGap(from: 20, to: 30, reason: RecordingDayGap.Reason.lost),
            RecordingDayGap(from: 30, to: 40, reason: RecordingDayGap.Reason.untranscribed),
        ], "silence is no gap; an untranscribed chunk is one [ext], not silence (C20)")
    }

    @Test func aMissingSeqHoldsToUntilTheChunkAfterItHasWaited() {
        // Seq 1 is still on the watch; seq 2 came 40 s after the start.
        let record = day([chunk(0, at: 0, positions: [0]), chunk(2, at: 20, positions: [1], received: 40)])
        let p = part(count: 2)
        let waiting = RecordingDayCoverage.compute(record: record, part: p, sentToPositionBelow: 2,
                                                   now: start.addingTimeInterval(40 + RecordingDayContract.gapWaitSeconds - 1))
        #expect(waiting.to == 10)
        #expect(waiting.gaps == [RecordingDayGap(from: 10, to: 20, reason: RecordingDayGap.Reason.pending)])
        // Measured from the persisted arrival, so a relaunch doesn't start the two minutes over (C19).
        let passed = RecordingDayCoverage.compute(record: record, part: p, sentToPositionBelow: 2,
                                                  now: start.addingTimeInterval(40 + RecordingDayContract.gapWaitSeconds))
        #expect(passed.to == 30)
        #expect(passed.gaps.map(\.reason) == [RecordingDayGap.Reason.pending], "still pending: the audio may yet come")
        #expect(RecordingDayCoverage.nextGapExpiry(record: record, now: start.addingTimeInterval(50))
                == start.addingTimeInterval(40 + RecordingDayContract.gapWaitSeconds))
        #expect(RecordingDayCoverage.nextGapExpiry(record: record, now: start.addingTimeInterval(400)) == nil)

        // A seq the watch reported lost never holds it back.
        let lost = day(record.chunks.values.map { $0 }, lost: [1])
        let reported = RecordingDayCoverage.compute(record: lost, part: p, sentToPositionBelow: 2, now: start.addingTimeInterval(41))
        #expect(reported.to == 30)
        #expect(reported.gaps == [RecordingDayGap(from: 10, to: 20, reason: RecordingDayGap.Reason.lost)])
    }

    @Test func theEndMarkerReachesEndedAtWithItsReason() {
        // Recorded until 10 s, stopped by itself, the day closed an hour in.
        let marker = chunk(1, at: 3600, duration: 0, state: .waiting, after: RecordingDayContract.Reason.stopped, end: true)
        let record = day([chunk(0, at: 0, positions: [0]), marker], expected: 2, endedAt: start.addingTimeInterval(3600))
        let p = part(count: 1)
        let coverage = RecordingDayCoverage.compute(record: record, part: p, sentToPositionBelow: 1, now: now)
        #expect(coverage.to == 3600)
        #expect(coverage.gaps == [RecordingDayGap(from: 10, to: 3600, reason: RecordingDayContract.Reason.stopped)])
        #expect(RecordingDayCoverage.isComplete(record: record, part: p, sentToPositionBelow: 1, coverage: coverage, isLast: true))

        // The last chunk carrying `end` with audio ends where its audio does.
        let audio = day([chunk(0, at: 0, positions: [0]), chunk(1, at: 10, positions: [1], end: true)], expected: 2,
                        endedAt: start.addingTimeInterval(20))
        #expect(RecordingDayCoverage.compute(record: audio, part: part(count: 2), sentToPositionBelow: 2, now: now).to == 20)
    }

    @Test func completeNeedsTheEndEverySeqAndEverySentenceOnTheHub() {
        let marker = chunk(2, at: 20, duration: 0, state: .waiting, end: true)
        let whole = day([chunk(0, at: 0, positions: [0]), chunk(1, at: 10, state: .failed), marker], expected: 3,
                        endedAt: start.addingTimeInterval(20))
        let p = part(count: 1)
        func complete(_ record: RecordingDayRecord, sent: Int = 1) -> Bool {
            let coverage = RecordingDayCoverage.compute(record: record, part: p, sentToPositionBelow: sent, now: now)
            return RecordingDayCoverage.isComplete(record: record, part: p, sentToPositionBelow: sent, coverage: coverage, isLast: true)
        }
        #expect(complete(whole), "a failed chunk is settled: its time is an untranscribed gap")
        #expect(!complete(whole, sent: 0), "its text isn't on the hub yet")
        #expect(!complete(day([chunk(0, at: 0, positions: [0]), chunk(1, at: 10, state: .failed)])), "not ended")
        #expect(!complete(day([chunk(0, at: 0, positions: [0]), marker], expected: 3, endedAt: start.addingTimeInterval(20))),
                "seq 1 is still pending")
        #expect(complete(day([chunk(0, at: 0, positions: [0]), marker], lost: [1], expected: 3, endedAt: start.addingTimeInterval(20))))
        #expect(!complete(day([chunk(0, at: 0, positions: [0]), chunk(1, at: 10, state: .waiting), marker], expected: 3,
                              endedAt: start.addingTimeInterval(20))), "seq 1 isn't transcribed")
    }

    @Test func eachPartCountsFromItsOwnStartAndAFullPartUpToItsEnd() {
        // Part 1 filled up at 20 s (413 day_full); part 2 holds what came after.
        let record = day([
            chunk(0, at: 0, positions: [0]), chunk(1, at: 10, positions: [1]),
            chunk(2, at: 20, positions: [0], part: 2), chunk(3, at: 30, positions: [1], part: 2),
            chunk(4, at: 45, positions: [2], part: 2, after: RecordingDayContract.Reason.interrupted),
        ])
        let first = part(1, count: 2, endedAt: 20)
        let second = part(2, at: 20, count: 3)
        let full = RecordingDayCoverage.compute(record: record, part: first, sentToPositionBelow: 2, now: now)
        #expect(full.to == 20 && full.gaps.isEmpty)
        #expect(RecordingDayCoverage.isComplete(record: record, part: first, sentToPositionBelow: 2, coverage: full, isLast: false))
        let open = RecordingDayCoverage.compute(record: record, part: second, sentToPositionBelow: 1, now: now)
        #expect(open.to == 10, "chunk 3's sentence is at part 2's position 1")
        let all = RecordingDayCoverage.compute(record: record, part: second, sentToPositionBelow: 3, now: now)
        #expect(all.to == 35)
        #expect(all.gaps == [RecordingDayGap(from: 20, to: 25, reason: RecordingDayContract.Reason.interrupted)])
    }

    @Test func aFullPartTheHubLostTextOfIsCoveredOnlyUpToIt() {
        // Part 1 filled up at 20 s; reconciliation then found the hub holds only its first position.
        let record = day([chunk(0, at: 0, positions: [0]), chunk(1, at: 10, positions: [1]), chunk(2, at: 20, positions: [0], part: 2)])
        let first = part(1, count: 2, endedAt: 20)
        let resend = RecordingDayCoverage.compute(record: record, part: first, sentToPositionBelow: 1, now: now)
        #expect(resend.to == 10, "chunk 1's sentence goes again with a later request (C19)")
        #expect(!RecordingDayCoverage.isComplete(record: record, part: first, sentToPositionBelow: 1, coverage: resend, isLast: false))
        #expect(RecordingDayCoverage.compute(record: record, part: first, sentToPositionBelow: 0, now: now).to == 0)
        #expect(RecordingDayCoverage.compute(record: record, part: first, sentToPositionBelow: 2, now: now).to == 20)
        // Text a full part no longer takes (still waiting, or placed in the next part) never holds it back.
        let leftover = day([chunk(0, at: 0, positions: [0]), chunk(1, at: 10, state: .waiting), chunk(2, at: 20, positions: [0], part: 2)])
        #expect(RecordingDayCoverage.compute(record: leftover, part: part(1, count: 1, endedAt: 20), sentToPositionBelow: 1, now: now).to == 20)
    }

    @Test func atMostFiveHundredGapsTheClosestOnesFolded() {
        // 501 one-second gaps 10 s apart, except two that are 1.5 s apart.
        var gaps = (0..<501).map { RecordingDayGap(from: Double($0) * 10, to: Double($0) * 10 + 1, reason: "paused") }
        gaps[201] = RecordingDayGap(from: 2002.5, to: 2005, reason: "interrupted")
        let capped = RecordingDayCoverage.capped(gaps, to: RecordingDayCoverage.maxGaps)
        #expect(capped.count == 500)
        #expect(capped.contains(RecordingDayGap(from: 2000, to: 2005, reason: "interrupted")),
                "the two closest became one, named after the longer")
        #expect(capped.first == gaps.first && capped.last == gaps.last)
        #expect(zip(capped, capped.dropFirst()).allSatisfy { $0.to <= $1.from }, "still sorted and apart")
        #expect(RecordingDayCoverage.capped(Array(gaps.prefix(3)), to: 500) == Array(gaps.prefix(3)))
    }
}
