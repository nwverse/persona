import Foundation
import Testing
@testable import Persona

private typealias Outbox = RecordingDayOutbox

private let anchor = Date(timeIntervalSince1970: 1_800_000_000) // 2027-01-15T08:00:00Z
private let today = UUID(uuidString: "00000000-0000-0000-0000-00000000d001")!
private let yesterday = UUID(uuidString: "00000000-0000-0000-0000-00000000d000")!

/// `seconds` after the anchor (before it when negative).
private func time(_ seconds: Double) -> Date { anchor.addingTimeInterval(seconds) }

private func utc(_ text: String) throws -> Date { try Date.ISO8601FormatStyle().parse(text) }

private func key(_ seq: Int, of recording: UUID = today) -> Outbox.Key { Outbox.Key(recording: recording, seq: seq) }

/// A chunk with `seconds` of audio that ended `end` seconds after the anchor; a 10 s live chunk fits a message.
private func chunk(_ seq: Int, endsAt end: Double, seconds: Double = RecordingDayContract.liveChunkSeconds,
                   bytes: Int = 42_000, of recording: UUID = today) -> Outbox.Item {
    Outbox.Item(key: key(seq, of: recording), finishedAt: time(end), startedAt: time(end - seconds), seconds: seconds,
                frameBytes: bytes, isMarker: false)
}

/// A header-only marker (erased, lost, the end) at `at` seconds after the anchor.
private func marker(_ seq: Int, at moment: Double, of recording: UUID = today) -> Outbox.Item {
    Outbox.Item(key: key(seq, of: recording), finishedAt: time(moment), startedAt: time(moment), seconds: 0,
                frameBytes: Outbox.frameOverheadBytes, isMarker: true)
}

/// A minute of audio: too big for a message.
private func minute(_ seq: Int, endsAt end: Double) -> Outbox.Item {
    chunk(seq, endsAt: end, seconds: RecordingDayContract.backlogChunkSeconds, bytes: 240_000)
}

private extension RecordingDayOutbox {
    /// `plan` with the phone in reach at full power and nothing in flight or queued: each test sets what it is about.
    mutating func planned(_ items: [Item], at now: Date = anchor, reachable: Bool = true, lowPower: Bool = false,
                          suspending: Bool = false, inFlight: Set<Key> = [], outstanding: Set<Key> = []) -> [Action] {
        plan(items, now: now, reachable: reachable, lowPower: lowPower, suspending: suspending, inFlight: inFlight,
             outstandingFiles: outstanding)
    }

    /// Plans, then reports every action as sent, as the watch's transport does.
    mutating func send(_ items: [Item], at now: Date = anchor, reachable: Bool = true, lowPower: Bool = false,
                       suspending: Bool = false, inFlight: Set<Key> = [], outstanding: Set<Key> = []) -> [Action] {
        let actions = planned(items, at: now, reachable: reachable, lowPower: lowPower, suspending: suspending,
                              inFlight: inFlight, outstanding: outstanding)
        for action in actions { sent(action, at: now) }
        return actions
    }
}

/// The plan of an outbox that has sent nothing yet.
private func firstPlan(_ items: [Outbox.Item], at now: Date = anchor, reachable: Bool = true, lowPower: Bool = false,
                       suspending: Bool = false, outstanding: Set<Outbox.Key> = []) -> [Outbox.Action] {
    var outbox = Outbox()
    return outbox.planned(items, at: now, reachable: reachable, lowPower: lowPower, suspending: suspending,
                          outstanding: outstanding)
}

/// The watch's all-day transport decisions (docs/assistant/day-recap.md): which chunk goes, when, and which way, plus
/// what each record's header says. Pure, so nonisolated.
struct RecordingDayOutboxTests {
    // MARK: Live path

    @Test func aLiveChunkGoesByMessageAtOnce() {
        var outbox = Outbox()
        let fresh = chunk(0, endsAt: 0)
        let first = outbox.send([fresh])
        #expect(first == [.message(key(0))])
        // Sent, it waits for its reply instead of going again.
        let waiting = outbox.planned([fresh], at: time(1))
        #expect(waiting == [])
        // The acknowledgement takes it out; the next chunk goes the same way.
        outbox.acked([key(0)])
        let next = outbox.planned([chunk(1, endsAt: 10)], at: time(10))
        #expect(next == [.message(key(1))])
    }

    @Test func atMostThreeMessagesAreInFlight() {
        var outbox = Outbox()
        let chunks = (0..<5).map { chunk($0, endsAt: Double($0) * 10) }
        let first = outbox.send(chunks, at: time(40))
        #expect(first == [.message(key(0)), .message(key(1)), .message(key(2))])
        let full = outbox.planned(chunks, at: time(40))
        #expect(full == [])
        outbox.acked([key(0)])
        let freed = outbox.planned(Array(chunks.dropFirst()), at: time(41))
        #expect(freed == [.message(key(3))], "one acknowledgement frees one place")
        // Messages the transport is still framing count too.
        var framing = Outbox()
        let behind = framing.planned(chunks, at: time(40), inFlight: [key(0), key(1)])
        #expect(behind == [.message(key(2))])
    }

    @Test func aFrameTooBigForAMessageGoesAsAFile() {
        let big = minute(0, endsAt: 0)
        let live = chunk(1, endsAt: 10)
        #expect(firstPlan([big, live], at: time(10)) == [.file(key(0)), .message(key(1))])
        let exact = chunk(2, endsAt: 0, bytes: RecordingDayContract.maxMessageBytes)
        #expect(firstPlan([exact]) == [.message(key(2))], "the limit itself still fits")
    }

    // MARK: Markers

    @Test func markersGoByMessageOrUserInfoNeverAsFiles() {
        let erased = marker(3, at: -30)
        let audio = minute(4, endsAt: 60)
        #expect(firstPlan([erased]) == [.message(key(3))], "in reach: a message, like any chunk")
        #expect(firstPlan([erased, audio], at: time(60), reachable: false) == [.userInfo(key(3))],
                "out of reach: user info at once, while the audio waits")
        #expect(firstPlan([erased, audio], at: time(60), suspending: true) == [.userInfo(key(3)), .file(key(4))])
        #expect(firstPlan([erased], lowPower: true) == [.message(key(3))], "an erase is never held for Low Power Mode")
        var crowded = erased
        crowded.frameBytes = RecordingDayContract.maxMessageBytes + 1
        #expect(firstPlan([crowded]) == [.userInfo(key(3))], "a header that outgrew a message still isn't a file")
        // In reach, a marker waits for a free message place like any chunk: the reply acknowledges it soonest.
        var busy = Outbox()
        let flying = (0..<3).map { chunk($0, endsAt: -50 + Double($0) * 10) }
        let full = busy.planned(flying + [erased], inFlight: Set(flying.map(\.key)))
        #expect(full == [])
    }

    // MARK: Low Power Mode

    @Test func lowPowerHoldsTheLivePathAtMostFiveMinutes() {
        var outbox = Outbox()
        // Six live chunks; the first started 10 s before the anchor and ended at it.
        let chunks = (0..<6).map { chunk($0, endsAt: Double($0) * 10) }
        let held = outbox.planned(chunks, at: time(50), lowPower: true)
        #expect(held == [])
        let release = time(Outbox.lowPowerHoldSeconds)
        #expect(outbox.nextWake(after: time(50)) == release, "the transport wakes when the hold ends")
        let almost = outbox.planned(chunks, at: release.addingTimeInterval(-0.1), lowPower: true)
        #expect(almost == [])

        let released = outbox.send(chunks, at: release, lowPower: true)
        #expect(released == [.message(key(0)), .message(key(1)), .message(key(2))])
        #expect(release.timeIntervalSince(chunks[0].startedAt) == RecordingAudioSpec.lowPowerTransferBatchSeconds,
                "the oldest audio leaves five minutes after it was recorded, not later")
        // The rest of the batch follows, although the oldest of it ended only 260 s ago.
        outbox.acked([key(0), key(1), key(2)])
        let rest = outbox.planned(Array(chunks.dropFirst(3)), at: release.addingTimeInterval(1), lowPower: true)
        #expect(rest == [.message(key(3)), .message(key(4)), .message(key(5))])
        // A chunk finished after the release starts the next hold.
        let later = outbox.planned([chunk(6, endsAt: 295)], at: time(296), lowPower: true)
        #expect(later == [])
        #expect(outbox.nextWake(after: time(296)) == time(295 + Outbox.lowPowerHoldSeconds))
        // Without Low Power Mode nothing waits.
        #expect(firstPlan(chunks, at: time(50)) == [.message(key(0)), .message(key(1)), .message(key(2))])
    }

    // MARK: Backlog

    @Test func aBacklogGoesAsFilesAndTheNewestByMessage() {
        // Forty live chunks waiting in reach (messages kept failing): more than five minutes of audio. The newest five
        // minutes stay on the live path, so a chunk finished now still goes at once; the rest goes as files.
        let chunks = (0..<40).map { chunk($0, endsAt: Double($0 - 39) * 10) }
        let files = (0..<10).map { Outbox.Action.file(key($0)) }
        #expect(firstPlan(chunks) == files + [.message(key(10)), .message(key(11)), .message(key(12))])
        #expect(firstPlan(Array(chunks.suffix(30))) == [.message(key(10)), .message(key(11)), .message(key(12))],
                "five minutes is still the live path")
    }

    @Test func outOfReachAudioWaitsFiveMinutesThenGoesAsFiles() {
        var outbox = Outbox()
        // One-minute chunks while the phone is away; the oldest ended at the anchor.
        let chunks = (0..<6).map { minute($0, endsAt: Double($0) * 60) }
        let early = outbox.planned(Array(chunks.prefix(5)), at: time(299), reachable: false)
        #expect(early == [])
        #expect(outbox.nextWake(after: time(299)) == time(RecordingDayContract.backlogFileSeconds))
        let due = outbox.planned(chunks, at: time(300), reachable: false)
        #expect(due == chunks.map { Outbox.Action.file($0.key) }, "then all of it, oldest first")
        // At most fifty with the system at once.
        var crowded = Outbox()
        let hour = (0..<60).map { minute($0, endsAt: Double($0) * 60) }
        let queued = Set((100..<145).map { key($0) })
        let five = crowded.planned(hour, at: time(3_600), reachable: false, outstanding: queued)
        #expect(five == (0..<5).map { Outbox.Action.file(key($0)) })
    }

    // MARK: Handing off

    @Test func handingOffQueuesEveryChunkWithTheSystem() {
        var outbox = Outbox()
        let failing = chunk(0, endsAt: -40)
        let flying = chunk(1, endsAt: -30)
        let queued = chunk(2, endsAt: -20)
        let cut = chunk(3, endsAt: 0)
        let end = marker(4, at: 0)
        outbox.sent(.message(key(0)), at: time(-35))
        outbox.finished(key(0), failed: true, at: time(-1))
        outbox.sent(.message(key(1)), at: time(-1))
        // On the live path nothing would go now: a backoff, a message awaiting its reply, a file with the system, and
        // Low Power Mode holding the chunk the pause just cut.
        let live = outbox.planned([failing, flying, queued, cut], lowPower: true, inFlight: [key(1)], outstanding: [key(2)])
        #expect(live == [])
        // Before the app stops running, all of it goes to the system, except what is already there.
        let handed = outbox.send([failing, flying, queued, cut, end], lowPower: true, suspending: true, inFlight: [key(1)],
                                 outstanding: [key(2)])
        #expect(handed == [.file(key(0)), .file(key(1)), .file(key(3)), .userInfo(key(4))])
        let again = outbox.planned([failing, flying, queued, cut, end], at: time(1), suspending: true, outstanding: [key(2)])
        #expect(again == [], "nothing is queued twice")
        // The cap on files with the system doesn't hold a hand-off back: nothing would send the rest until the app runs.
        let crowded = Set((100..<(100 + Outbox.maxOutstandingFiles)).map { key($0) })
        let handedAnyway = firstPlan([cut, end], suspending: true, outstanding: crowded)
        #expect(handedAnyway == [.file(key(3)), .userInfo(key(4))])
    }

    // MARK: Retries

    @Test func anUnacknowledgedMessageGoesAgainWithABackoffDoublingToFiveMinutes() {
        var outbox = Outbox()
        let item = chunk(0, endsAt: 0)
        var sentAt = anchor
        let first = outbox.send([item], at: sentAt)
        #expect(first == [.message(key(0))])
        for delay in [2.0, 4, 8, 16, 32, 64, 128, 256, 300, 300] {
            let overdue = sentAt.addingTimeInterval(RecordingDayContract.ackTimeout)
            let waiting = outbox.planned([item], at: overdue.addingTimeInterval(-0.001))
            #expect(waiting == [], "waits for the reply")
            let backingOff = outbox.planned([item], at: overdue)
            #expect(backingOff == [], "overdue: backs off \(delay) s")
            #expect(outbox.nextWake(after: overdue) == overdue.addingTimeInterval(delay))
            let early = outbox.planned([item], at: overdue.addingTimeInterval(delay - 0.001))
            #expect(early == [])
            sentAt = overdue.addingTimeInterval(delay)
            let again = outbox.send([item], at: sentAt)
            #expect(again == [.message(key(0))])
        }
    }

    @Test func aMessageErrorBacksOffAndIsNotCountedTwice() {
        var outbox = Outbox()
        let item = chunk(0, endsAt: 0)
        _ = outbox.send([item])
        outbox.finished(key(0), failed: true, at: time(1))
        let early = outbox.planned([item], at: time(2.999))
        #expect(early == [])
        let retried = outbox.send([item], at: time(3))
        #expect(retried == [.message(key(0))])
        // Overdue first, its error after: one failure, so the wait is the second failure's 4 s.
        let overdue = outbox.planned([item], at: time(33))
        #expect(overdue == [])
        outbox.finished(key(0), failed: true, at: time(34))
        #expect(outbox.nextWake(after: time(34)) == time(37))
        let third = outbox.planned([item], at: time(37))
        #expect(third == [.message(key(0))])
    }

    @Test func becomingReachableRetriesAtOnce() {
        var outbox = Outbox()
        let item = chunk(0, endsAt: 0)
        for _ in 0..<5 {
            outbox.sent(.message(key(0)), at: anchor)
            outbox.finished(key(0), failed: true, at: anchor)
        }
        #expect(outbox.nextWake(after: anchor) == time(32), "five failures: a 32 s backoff")
        let backingOff = outbox.planned([item], at: time(1))
        #expect(backingOff == [])
        outbox.reachabilityChanged(false)
        outbox.reachabilityChanged(true)
        let retried = outbox.send([item], at: time(1))
        #expect(retried == [.message(key(0))])
        outbox.finished(key(0), failed: true, at: time(2))
        #expect(outbox.nextWake(after: time(2)) == time(4), "the backoff starts again from 2 s")

        // A message that went out just before the link dropped goes again too, without waiting for its timeout.
        var dropped = Outbox()
        let other = chunk(1, endsAt: 0)
        _ = dropped.send([other])
        dropped.reachabilityChanged(false)
        dropped.reachabilityChanged(true)
        let resent = dropped.planned([other], at: time(1), inFlight: [key(1)])
        #expect(resent == [.message(key(1))])
    }

    @Test func aFileGoesAgainOnlyAfterAFailureOrALongSilence() {
        var outbox = Outbox()
        let item = minute(0, endsAt: 0)
        let first = outbox.send([item])
        #expect(first == [.file(key(0))])
        // While the system holds it, never again, however long: the phone may be away for hours.
        let held = outbox.planned([item], at: time(7_200), outstanding: [key(0)])
        #expect(held == [])
        // Delivered: its acknowledgement gets ten minutes, then it counts as failed and goes after its backoff.
        outbox.finished(key(0), failed: false, at: time(7_200))
        let waiting = outbox.planned([item], at: time(7_799))
        #expect(waiting == [])
        let silent = outbox.planned([item], at: time(7_800))
        #expect(silent == [])
        let resent = outbox.send([item], at: time(7_802))
        #expect(resent == [.file(key(0))])
        // A transfer the system reports failed goes again after its backoff (the second failure: 4 s).
        outbox.finished(key(0), failed: true, at: time(7_810))
        let early = outbox.planned([item], at: time(7_813))
        #expect(early == [])
        let again = outbox.planned([item], at: time(7_814))
        #expect(again == [.file(key(0))])

        // After a relaunch the system reports a transfer this outbox never saw: delivered, it waits for its acknowledgement.
        var relaunched = Outbox()
        relaunched.finished(key(0), failed: false, at: anchor)
        let quiet = relaunched.planned([item], at: time(1))
        #expect(quiet == [])
    }

    @Test func aChunkThatBecameAMarkerGoesAtOnce() {
        // Its audio could not be read: the message failed and backs off.
        var outbox = Outbox()
        let audio = chunk(0, endsAt: 0)
        _ = outbox.send([audio])
        outbox.finished(key(0), failed: true, at: time(1))
        let backingOff = outbox.planned([audio], at: time(2))
        #expect(backingOff == [])
        // Reported lost: what was sent no longer counts, and the lost marker goes without waiting for the backoff.
        outbox.reset(key(0))
        let lost = marker(0, at: -10)
        let live = outbox.planned([lost], at: time(2))
        #expect(live == [.message(key(0))])
        let queued = outbox.planned([lost], at: time(2), reachable: false)
        #expect(queued == [.userInfo(key(0))])

        // A failure for a send this outbox never saw (a transfer from before a relaunch) backs off, then the chunk goes
        // again: a failure never parks a chunk as if the system still held it.
        var relaunched = Outbox()
        relaunched.finished(key(0), failed: true, at: anchor)
        let early = relaunched.planned([lost], at: time(1))
        #expect(early == [])
        let retried = relaunched.planned([lost], at: time(2))
        #expect(retried == [.message(key(0))])
    }

    // MARK: Order and keeping

    @Test func theOldestGoesFirstAcrossDays() {
        let lastOfYesterday = chunk(7, endsAt: -600, of: yesterday)
        let endOfYesterday = marker(8, at: -595, of: yesterday)
        let first = chunk(0, endsAt: -580)
        let second = chunk(1, endsAt: -570)
        let items = [second, endOfYesterday, first, lastOfYesterday]
        #expect(firstPlan(items) == [.message(key(7, of: yesterday)), .message(key(8, of: yesterday)), .message(key(0))])
        #expect(firstPlan(items, reachable: false)
                == [.file(key(7, of: yesterday)), .userInfo(key(8, of: yesterday)), .file(key(0)), .file(key(1))])
    }

    @Test func noChunkIsDroppedForItsAge() throws {
        var outbox = Outbox()
        let old = chunk(0, endsAt: -3 * 86_400)
        let away = outbox.planned([old], reachable: false)
        #expect(away == [.file(key(0))])
        let near = outbox.planned([old])
        #expect(near == [.message(key(0))])
        // However often it fails, it comes back once its backoff ends.
        var now = anchor
        for _ in 0..<20 {
            let actions = outbox.send([old], at: now)
            #expect(actions == [.message(key(0))])
            outbox.finished(key(0), failed: true, at: now)
            now = try #require(outbox.nextWake(after: now))
        }
    }

    @Test func anAcknowledgedChunkIsForgotten() {
        var outbox = Outbox()
        let item = chunk(0, endsAt: 0)
        _ = outbox.send([item])
        outbox.finished(key(0), failed: true, at: anchor)
        #expect(outbox.nextWake(after: anchor) != nil)
        outbox.acked([key(0)])
        #expect(outbox.nextWake(after: anchor) == nil)
        // Offered again (its acknowledgement didn't reach the library), it goes at once: no backoff, no reply awaited.
        let again = outbox.planned([item])
        #expect(again == [.message(key(0))])
        // A chunk no longer offered (acknowledged another way, or pruned) is forgotten as well.
        _ = outbox.send([item])
        _ = outbox.planned([])
        #expect(outbox.nextWake(after: anchor) == nil)
    }

    @Test func backoffDoublesFromTwoSecondsToFiveMinutes() {
        #expect((1...10).map(Outbox.backoff) == [2, 4, 8, 16, 32, 64, 128, 256, 300, 300])
        #expect(Outbox.backoff(1_000) == RecordingDayContract.backoffMax)
    }

    // MARK: Records on the wire

    private func dayEntry() -> RecordingEntry {
        var entry = RecordingEntry(id: today, createdAt: time(-3_600), source: .watch, kind: .day)
        entry.day = "2027-01-15"
        entry.timeZone = "UTC"
        return entry
    }

    private func record(_ seq: Int, at start: Double, duration: Double = RecordingDayContract.liveChunkSeconds) -> RecordingSegment {
        RecordingSegment(index: seq, start: start, duration: duration, startedAt: time(start))
    }

    @Test func headersCarryTheEndTheMarksAndTheRecordingStart() {
        var entry = dayEntry()
        // Chunks at 0 and 10 s, a pause, a chunk at 60 s.
        entry.segments = [record(0, at: 0), record(1, at: 10), record(2, at: 60)]
        entry.segments[2].after = RecordingDayContract.Reason.paused
        entry.segments[2].level = -40
        let bookmark = RecordingDayMark(at: time(12), label: RecordingDayMark.bookmark)
        let meeting = RecordingDayMark(at: time(40), label: RecordingDayMark.meeting)
        entry.dayMarks = [meeting, bookmark]
        let carried = Outbox.carriedMarks(entry)
        #expect(carried[1] == [bookmark], "a mark goes with the chunk it falls in")
        #expect(carried[2] == [meeting], "one made while paused goes with the next chunk")
        #expect(carried[0] == nil)

        let second = Outbox.header(for: entry.segments[1], in: entry, marks: carried[1] ?? [])
        #expect(second.marks == [RecordingDayHeader.Mark(at: 2, label: RecordingDayMark.bookmark)])
        let third = Outbox.header(for: entry.segments[2], in: entry, marks: carried[2] ?? [])
        #expect(third.marks == [RecordingDayHeader.Mark(at: -20, label: RecordingDayMark.meeting)], "before the chunk began")
        #expect(third.after == RecordingDayContract.Reason.paused && third.level == -40)
        #expect(third.recording == today && third.day == "2027-01-15" && third.seq == 2)
        #expect(third.startedAt == time(60) && third.duration == 10 && third.recordingStartedAt == entry.createdAt)
        #expect(!third.end && !third.isMarker && third.valid)
        #expect(Outbox.header(for: entry.segments[0], in: entry, marks: []).marks == nil)

        // A closed day: its last seq says end, be it the chunk cut at midnight or the end marker.
        entry.active = false
        entry.expectedSegments = 3
        #expect(Outbox.header(for: entry.segments[2], in: entry, marks: []).end)
        #expect(!Outbox.header(for: entry.segments[1], in: entry, marks: []).end)
    }

    @Test func aMarkGoesWithTheFirstChunkNotYetAcknowledged() {
        var entry = dayEntry()
        entry.segments = [record(0, at: 0), record(1, at: 10)]
        entry.segments[0].delivered = true
        let early = RecordingDayMark(at: time(5), label: RecordingDayMark.bookmark)
        let onTheEdge = RecordingDayMark(at: time(20), label: RecordingDayMark.bookmark)
        let late = RecordingDayMark(at: time(25), label: RecordingDayMark.meeting)
        entry.dayMarks = [late, early, onTheEdge]
        let carried = Outbox.carriedMarks(entry)
        // Chunk 0 was acknowledged before the mark reached it: chunk 1 takes it, before its start.
        #expect(carried[1] == [early, onTheEdge])
        let header = Outbox.header(for: entry.segments[1], in: entry, marks: carried[1] ?? [])
        #expect(header.marks == [RecordingDayHeader.Mark(at: -5, label: RecordingDayMark.bookmark),
                                 RecordingDayHeader.Mark(at: 10, label: RecordingDayMark.bookmark)])
        #expect(!carried.values.joined().contains(late), "a mark after every chunk waits for the next one")
    }

    /// `count` bookmarks two seconds apart from `start` seconds after the anchor.
    private func bookmarks(_ count: Int, from start: Double) -> [RecordingDayMark] {
        (0..<count).map { RecordingDayMark(at: time(start + Double($0) * 2), label: RecordingDayMark.bookmark) }
    }

    @Test func noHeaderCarriesMoreMarksThanThePhoneTakes() {
        var entry = dayEntry()
        // Two acknowledged chunks, a long pause with 150 bookmarks in it, then two more chunks.
        entry.segments = [record(0, at: 0), record(1, at: 10), record(2, at: 400), record(3, at: 410)]
        entry.segments[0].delivered = true
        entry.segments[1].delivered = true
        let paused = bookmarks(150, from: 30)
        entry.dayMarks = paused.reversed()
        let carried = Outbox.carriedMarks(entry)
        let limit = Outbox.maxMarksPerHeader
        #expect(carried[2] == Array(paused.prefix(limit)), "the oldest hundred go with the chunk after the pause")
        #expect(carried[3] == Array(paused.dropFirst(limit)), "the rest with the chunk after it, none left behind")
        for seq in [2, 3] {
            #expect(Outbox.header(for: entry.segments[seq], in: entry, marks: carried[seq] ?? []).valid, "seq \(seq)")
        }
        let crowded = Outbox.header(for: entry.segments[2], in: entry, marks: Array(paused.prefix(limit + 1)))
        #expect(!crowded.valid, "the limit is the one the phone checks")
        // Chunk 2 acknowledged, its marks gone with it: the others keep their carrier.
        var acknowledged = entry
        acknowledged.segments[2].delivered = true
        acknowledged.dayMarks = Array(paused.dropFirst(limit))
        #expect(Outbox.carriedMarks(acknowledged) == [3: Array(paused.dropFirst(limit))])

        // While the day is open, marks no chunk has room for wait for the next chunk.
        var open = entry
        open.segments.removeLast()
        #expect(Outbox.carriedMarks(open) == [2: Array(paused.prefix(limit))])
    }

    @Test func aClosedDaysMarksAllFindACarrier() {
        var entry = dayEntry()
        // A chunk not acknowledged yet, then a pause with 150 bookmarks, until the day was closed.
        var end = RecordingSegment(index: 1, start: 10, duration: 0, startedAt: time(400))
        end.end = true
        entry.segments = [record(0, at: 0), end]
        entry.active = false
        entry.expectedSegments = 2
        let paused = bookmarks(150, from: 20)
        entry.dayMarks = paused
        let carried = Outbox.carriedMarks(entry)
        let limit = Outbox.maxMarksPerHeader
        #expect(carried[1] == Array(paused.prefix(limit)), "the end marker takes the oldest hundred")
        // No chunk follows the end marker: the rest go with the chunk before the pause, after its end.
        #expect(carried[0] == Array(paused.dropFirst(limit)))
        let first = Outbox.header(for: entry.segments[0], in: entry, marks: carried[0] ?? [])
        #expect(first.valid && first.marks?.first?.at == 220)
        #expect(Outbox.header(for: entry.segments[1], in: entry, marks: carried[1] ?? []).valid)
    }

    @Test func erasedLostAndEndRecordsTravelAsMarkers() {
        var entry = dayEntry()
        var erased = record(0, at: 0)
        erased.erased = true
        erased.level = -30
        var lost = record(1, at: 10)
        lost.lost = true
        lost.level = -30
        var end = RecordingSegment(index: 2, start: 20, duration: 0, startedAt: time(30))
        end.end = true
        end.after = RecordingDayContract.Reason.paused
        entry.segments = [erased, lost, end]
        let headers = entry.segments.map { Outbox.header(for: $0, in: entry, marks: []) }
        #expect(headers.allSatisfy { $0.isMarker && $0.valid })
        #expect(headers[0].erased && !headers[0].lost && headers[0].level == nil && headers[0].duration == 10,
                "an erased chunk keeps its times for the phone's gap, not its level")
        #expect(headers[1].lost && !headers[1].erased && headers[1].level == nil)
        #expect(headers[2].end && headers[2].after == RecordingDayContract.Reason.paused && headers[2].duration == 0)
        let items = entry.segments.map { Outbox.item($0, of: entry, frameBytes: 99_999) }
        #expect(items.allSatisfy { $0.isMarker && $0.seconds == 0 && $0.frameBytes <= RecordingDayContract.maxMessageBytes })
    }

    @Test func anErasedChunkTakesOnlyItsOwnMarkersAcknowledgement() {
        let audio = record(0, at: 0)
        #expect(Outbox.acceptsAck(of: audio, confirmsErase: false, eraseDelivered: false))
        var erased = audio
        erased.erased = true
        // A seq-only acknowledgement may be for the audio sent before the erase: the erased marker must still go.
        #expect(!Outbox.acceptsAck(of: erased, confirmsErase: false, eraseDelivered: false))
        #expect(Outbox.acceptsAck(of: erased, confirmsErase: true, eraseDelivered: false), "the reply to its own marker")
        #expect(Outbox.acceptsAck(of: erased, confirmsErase: false, eraseDelivered: true), "its marker reached the phone")
    }

    @Test func aTransferSpeaksOnlyForWhatItsRecordStillIs() {
        let entry = dayEntry()
        let audio = record(0, at: 0)
        var lost = audio
        lost.lost = true
        var erased = audio
        erased.erased = true
        var erasedAfterLoss = lost
        erasedAfterLoss.erased = true
        let sentAudio = Outbox.header(for: audio, in: entry, marks: [])
        #expect(Outbox.isCurrent(sentAudio, for: audio))
        #expect(!Outbox.isCurrent(sentAudio, for: lost), "audio queued before the chunk was lost")
        #expect(!Outbox.isCurrent(sentAudio, for: erased), "audio queued before the erase")
        let lostMarker = Outbox.header(for: lost, in: entry, marks: [])
        #expect(Outbox.isCurrent(lostMarker, for: lost))
        #expect(!Outbox.isCurrent(lostMarker, for: erasedAfterLoss), "erased after it was reported lost: the erase must go")
        #expect(Outbox.isCurrent(Outbox.header(for: erasedAfterLoss, in: entry, marks: []), for: erasedAfterLoss))
    }

    @Test func framesAreSizedFromTheFileOrEstimated() {
        let entry = dayEntry()
        let live = record(0, at: 0)
        let estimated = Outbox.item(live, of: entry, frameBytes: nil)
        #expect(estimated.frameBytes <= RecordingDayContract.maxMessageBytes, "a 10 s chunk fits a message")
        #expect(estimated.key == key(0) && estimated.startedAt == time(0) && estimated.finishedAt == time(10))
        #expect(estimated.seconds == 10 && !estimated.isMarker)
        let backlog = record(1, at: 10, duration: RecordingDayContract.backlogChunkSeconds)
        #expect(Outbox.item(backlog, of: entry, frameBytes: nil).frameBytes > RecordingDayContract.maxMessageBytes)
        #expect(Outbox.item(live, of: entry, frameBytes: 50_000).frameBytes == 50_000, "a measured size wins")
    }

    // MARK: Days

    @Test func aDayLeftOpenClosesAtItsOwnMidnight() throws {
        let newYork = try #require(TimeZone(identifier: "America/New_York"))
        // 2026-11-01 lasts 25 hours in New York, 2026-03-08 23.
        for (day, midnight) in [("2026-11-01", "2026-11-02T05:00:00Z"), ("2026-03-08", "2026-03-09T04:00:00Z")] {
            var entry = dayEntry()
            entry.day = day
            entry.timeZone = newYork.identifier
            let end = try utc(midnight)
            #expect(Outbox.closingTime(of: entry, now: end.addingTimeInterval(-1), zone: newYork) == nil, "\(day) still lasts")
            #expect(Outbox.closingTime(of: entry, now: end, zone: newYork) == end)
            #expect(Outbox.closingTime(of: entry, now: end.addingTimeInterval(3 * 86_400), zone: newYork) == end,
                    "days later, its own midnight")
            entry.active = false
            #expect(Outbox.closingTime(of: entry, now: end, zone: newYork) == nil, "a closed day stays as it is")
        }
        let clip = RecordingEntry(id: UUID(), createdAt: time(-86_400 * 3), source: .watch)
        #expect(Outbox.closingTime(of: clip, now: anchor, zone: newYork) == nil)
    }

    @Test func aDayAbroadClosesOnceTheWatchsDateMovesOn() throws {
        let shanghai = try #require(TimeZone(identifier: "Asia/Shanghai"))
        let newYork = try #require(TimeZone(identifier: "America/New_York"))
        let tokyo = try #require(TimeZone(identifier: "Asia/Tokyo"))
        var west = dayEntry()
        west.day = "2026-11-12"
        west.timeZone = shanghai.identifier
        // Its own midnight is 11:00 on the same date in New York: flown west, the capture may still record into it.
        let ownMidnight = try utc("2026-11-12T16:00:00Z")
        #expect(Outbox.closingTime(of: west, now: ownMidnight.addingTimeInterval(3_600), zone: newYork) == nil)
        // It did, past its own midnight: once New York's date moves on, it ends after that chunk, not before it.
        west.segments = [RecordingSegment(index: 0, start: 0, duration: 10, startedAt: ownMidnight.addingTimeInterval(7_200))]
        let newYorksNextDay = try utc("2026-11-13T05:00:00Z")
        #expect(Outbox.closingTime(of: west, now: newYorksNextDay, zone: newYork) == ownMidnight.addingTimeInterval(7_210))

        // Flown east: Tokyo is on the 13th while New York's 12th still lasts. It ends now, as a start would end it.
        var east = dayEntry()
        east.day = "2026-11-12"
        east.timeZone = newYork.identifier
        let morningInTokyo = try utc("2026-11-12T23:30:00Z")
        #expect(Outbox.closingTime(of: east, now: morningInTokyo, zone: tokyo) == morningInTokyo)
    }

    @Test func aDayPausedPastMidnightClosesOnceItsWriterHoldsNothingOfIt() throws {
        let utcZone = try #require(TimeZone(identifier: "UTC"))
        var paused = dayEntry()
        paused.day = "2027-01-14"
        paused.createdAt = time(-50_000)
        // Its last chunk ended at 23:40:10, then all-day recording was paused; it is 08:00 the next morning.
        paused.segments = [record(0, at: -30_000)]
        paused.gapReason = RecordingDayContract.Reason.paused
        let midnight = try utc("2027-01-15T00:00:00Z")
        func closing(_ entry: RecordingEntry, _ hold: Outbox.CaptureHold) -> Date? {
            Outbox.closingTime(of: entry, now: anchor, zone: utcZone, capture: hold)
        }
        #expect(closing(paused, .paused(unsavedAudio: false)) == midnight,
                "nothing comes into a paused day: it ends at its midnight without waiting for Continue")
        #expect(closing(paused, .paused(unsavedAudio: true)) == nil,
                "audio that reached the writer after the pause's cut would take the end marker's seq")
        #expect(closing(paused, .savingAudio) == nil, "a chunk waiting for its save would too")
        #expect(closing(paused, .recording) == nil, "a writer recording past midnight cuts there itself")
        #expect(closing(paused, .none) == midnight)
        // Paused within its own day: the day goes on when the microphone opens again.
        #expect(closing(dayEntry(), .paused(unsavedAudio: false)) == nil)
    }

    @Test func aDayIsFinishedOnceEverySeqIsAcknowledgedAndPruned() {
        var entry = dayEntry()
        entry.active = false
        entry.expectedSegments = 3
        entry.prunedBelow = 3
        #expect(Outbox.isFinished(entry))
        var open = entry
        open.active = true
        var short = entry
        short.prunedBelow = 2
        var kept = entry
        kept.segments = [record(2, at: 0)]
        var unended = entry
        unended.expectedSegments = nil
        for other in [open, short, kept, unended] {
            #expect(!Outbox.isFinished(other))
        }
    }

    @Test func theWatchStateSaysWhoStoppedTheMicrophone() {
        #expect(Outbox.watchState(allDay: true, recording: true, interrupted: false, dayOpen: true, intent: true) == .recording)
        #expect(Outbox.watchState(allDay: true, recording: false, interrupted: true, dayOpen: true, intent: true) == .interrupted)
        #expect(Outbox.watchState(allDay: true, recording: false, interrupted: false, dayOpen: true, intent: true) == .paused)
        #expect(Outbox.watchState(allDay: false, recording: false, interrupted: false, dayOpen: true, intent: true) == .stopped,
                "the system or a crash stopped it while the user wants it on")
        #expect(Outbox.watchState(allDay: false, recording: false, interrupted: false, dayOpen: true, intent: false) == .off,
                "Stop for Today")
        #expect(Outbox.watchState(allDay: false, recording: false, interrupted: false, dayOpen: false, intent: true) == .off)
    }
}
