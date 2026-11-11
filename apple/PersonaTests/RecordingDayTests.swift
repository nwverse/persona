import Foundation
import Testing
@testable import Persona

private let anchor = Date(timeIntervalSince1970: 1_800_000_000) // 2027-01-15T08:00:00Z
private let newYork = TimeZone(identifier: "America/New_York")!

private func utc(_ text: String) throws -> Date { try Date.ISO8601FormatStyle().parse(text) }

/// A header with every key set, at whole milliseconds so it survives the wire's ISO 8601 times.
private func fullHeader(_ id: UUID = UUID()) -> RecordingDayHeader {
    RecordingDayHeader(recording: id, day: "2027-01-15", seq: 41, startedAt: anchor.addingTimeInterval(0.25), duration: 10,
                       level: -38.5, erased: false, end: true, after: RecordingDayContract.Reason.interrupted,
                       marks: [.init(at: -2.5, label: RecordingDayMark.meeting), .init(at: 3, label: RecordingDayMark.bookmark)],
                       recordingStartedAt: anchor.addingTimeInterval(-3600), lost: false)
}

/// The wire: header, framing, ranges, acknowledgements, the watch's state, the calendar, the level, the cached flags.
/// Nonisolated on purpose: the phone runs all of it on WatchConnectivity's and its store's queues.
struct RecordingDayWireTests {
    // MARK: Header

    @Test func headerUsesTheContractKeys() throws {
        let header = fullHeader()
        let object = try #require(try JSONSerialization.jsonObject(with: header.json) as? [String: Any])
        #expect(Set(object.keys) == ["v", "recording", "day", "seq", "started_at", "duration", "level", "erased", "end", "after",
                                     "marks", "recording_started_at"], "lost is written only when set")
        #expect(object["recording"] as? String == header.recording.uuidString.lowercased())
        #expect(object["v"] as? Int == RecordingDayContract.version)
        // ISO 8601 with milliseconds and an offset, as in the contract's example.
        let started = try #require(object["started_at"] as? String)
        #expect(started.wholeMatch(of: /\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.250(Z|[+-]\d{2}:\d{2})/) != nil, "\(started)")
        let decoded = try JSONDecoder().decode(RecordingDayHeader.self, from: header.json)
        #expect(decoded == header)

        var lost = header
        lost.lost = true
        let lostObject = try #require(try JSONSerialization.jsonObject(with: lost.json) as? [String: Any])
        #expect(lostObject["lost"] as? Bool == true)
    }

    @Test func headerDecodesWhatOthersMayWrite() throws {
        let id = UUID()
        // Only the contract's required keys, an upper-case id, a time without fractions, an unknown key, bad [ext] keys.
        let json = """
        {"recording": "\(id.uuidString.uppercased())", "day": "2027-01-15", "seq": 0, "started_at": "2027-01-15T16:00:00+08:00",
         "duration": 60, "future": {"x": 1}, "marks": "not a list", "level": "loud", "recording_started_at": 12}
        """
        let header = try JSONDecoder().decode(RecordingDayHeader.self, from: Data(json.utf8))
        #expect(header.recording == id && header.seq == 0 && header.duration == 60)
        #expect(header.startedAt == anchor)
        #expect(header.v == 1 && !header.erased && !header.end && !header.lost)
        #expect(header.marks == nil && header.level == nil && header.after == nil)
        #expect(header.recordingStartedAt == Date(timeIntervalSince1970: 12), "seconds since 1970 are read too")
        #expect(header.valid)

        let utcTime = """
        {"v": 1, "recording": "\(id)", "day": "2027-01-15", "seq": 3, "started_at": "2027-01-15T08:00:00.500Z", "duration": 10}
        """
        #expect(try JSONDecoder().decode(RecordingDayHeader.self, from: Data(utcTime.utf8)).startedAt == anchor.addingTimeInterval(0.5))

        for broken in [#"{"recording": "x", "day": "2027-01-15", "seq": 0, "started_at": 0, "duration": 1}"#,
                       #"{"recording": "\#(id)", "day": "2027-01-15", "started_at": 0, "duration": 1}"#,
                       #"{"recording": "\#(id)", "day": "2027-01-15", "seq": 0, "started_at": "yesterday", "duration": 1}"#] {
            #expect(throws: (any Error).self) { try JSONDecoder().decode(RecordingDayHeader.self, from: Data(broken.utf8)) }
        }
    }

    @Test func invalidHeadersAreRefused() {
        #expect(fullHeader().valid)
        let broken: [(String, (inout RecordingDayHeader) -> Void)] = [
            ("version 0", { $0.v = 0 }),
            ("negative seq", { $0.seq = -1 }),
            ("seq too large", { $0.seq = 1_000_000 }),
            ("duration over 600 s", { $0.duration = 600.5 }),
            ("negative duration", { $0.duration = -1 }),
            ("NaN duration", { $0.duration = .nan }),
            ("infinite level", { $0.level = .infinity }),
            ("unpadded day", { $0.day = "2027-1-15" }),
            ("letters in the day", { $0.day = "2027-01-1a" }),
            ("full-width digits", { $0.day = "\u{FF12}\u{FF10}\u{FF12}\u{FF17}-01-15" }),
            ("too many marks", { $0.marks = Array(repeating: .init(at: 0, label: ""), count: 101) }),
            ("non-finite mark", { $0.marks = [.init(at: .nan, label: "")] }),
        ]
        for (name, edit) in broken {
            var header = fullHeader()
            edit(&header)
            #expect(!header.valid, "\(name)")
            // Never read back as it was: refused, or (a level JSON can't hold) written without it.
            #expect(RecordingDayHeader(userInfo: header.userInfo) != header, "\(name)")
        }
    }

    @Test func markersCarryNoAudio() {
        var header = fullHeader()
        #expect(!header.isMarker)
        header.erased = true
        #expect(header.isMarker)
        header.erased = false; header.lost = true
        #expect(header.isMarker)
        header.lost = false; header.duration = 0
        #expect(header.isMarker)
    }

    // MARK: Framing

    @Test func framesRoundTripAudioAndMarkers() throws {
        let header = fullHeader()
        let audio = Data((0..<2000).map { UInt8($0 % 251) })
        let frame = header.frame(audio: audio)
        #expect(frame.prefix(4) == Data([0, 0, UInt8(header.json.count >> 8), UInt8(header.json.count & 0xFF)]))
        #expect(frame.count == 4 + header.json.count + audio.count)
        let back = try #require(RecordingDayHeader.unframe(frame))
        #expect(back.header == header && back.audio == audio)

        var marker = header
        marker.erased = true
        let markerBack = try #require(RecordingDayHeader.unframe(marker.frame(audio: nil)))
        #expect(markerBack.header == marker && markerBack.audio.isEmpty)

        // A slice of a larger buffer (its indices don't start at 0) reads the same.
        let slice = (Data([9, 9, 9]) + frame).dropFirst(3)
        #expect(RecordingDayHeader.unframe(slice)?.audio == audio)
    }

    @Test func brokenFramesAreRefused() {
        let header = fullHeader()
        let frame = header.frame(audio: Data([1, 2, 3]))
        #expect(RecordingDayHeader.unframe(Data()) == nil)
        #expect(RecordingDayHeader.unframe(Data([0, 0, 1])) == nil)
        #expect(RecordingDayHeader.unframe(frame.prefix(4 + header.json.count - 1)) == nil, "header cut short")
        #expect(RecordingDayHeader.unframe(Data([0, 0, 0, 0]) + header.json) == nil, "zero length")
        #expect(RecordingDayHeader.unframe(Data([0xFF, 0xFF, 0xFF, 0xFF]) + header.json) == nil, "absurd length")
        #expect(RecordingDayHeader.unframe(Data([0, 0, 0, 4]) + Data("nope".utf8)) == nil, "not JSON")
        var invalid = header
        invalid.day = "today"
        #expect(RecordingDayHeader.unframe(invalid.frame(audio: Data([1]))) == nil, "an invalid header")
    }

    @Test func userInfoRoundTrips() throws {
        let header = fullHeader()
        #expect(header.userInfo.keys.sorted() == [RecordingDayContract.chunkKey])
        #expect(RecordingDayHeader(userInfo: header.userInfo) == header)
        #expect(RecordingDayHeader(userInfo: nil) == nil)
        #expect(RecordingDayHeader(userInfo: ["personaRecording": header.json]) == nil)
        #expect(RecordingDayHeader(userInfo: [RecordingDayContract.chunkKey: "text"]) == nil)
    }

    // MARK: Ranges and acknowledgements

    @Test func rangesMergeAndExpand() {
        #expect(RecordingDayRanges.ranges([5, 1, 2, 3, 8, 7, 8, -1]) == [[1, 3], [5, 5], [7, 8]])
        #expect(RecordingDayRanges.ranges([Int]()) == [])
        #expect(RecordingDayRanges.expand([[1, 3], [5, 5], [7, 8]]) == [1, 2, 3, 5, 7, 8])
        // One number is a range of one; malformed ranges are skipped; overlaps count once.
        #expect(RecordingDayRanges.expand([[3], [5, 4], [-1, 2], [1, 2, 3], [], [9, 10], [10, 11]]) == [3, 9, 10, 11])
        // A range that names far too many seqs stops at the limit.
        #expect(RecordingDayRanges.expand([[0, Int.max]], limit: 5) == [0, 1, 2, 3, 4])
        let seqs = Set((0..<500).filter { $0 % 7 != 3 })
        #expect(Set(RecordingDayRanges.expand(RecordingDayRanges.ranges(seqs))) == seqs)
    }

    @Test func ackAndLostHaveTheContractShape() throws {
        let id = UUID()
        let ack = RecordingDayAck(recording: id, seqs: [2, 0, 1, 5])
        #expect(ack.acks == [[0, 2], [5, 5]] && ack.seqs == [0, 1, 2, 5])
        let object = try #require(try JSONSerialization.jsonObject(with: ack.data) as? [String: Any])
        #expect(Set(object.keys) == ["recording", "acks"])
        #expect(RecordingDayAck(data: ack.data) == ack)
        #expect(RecordingDayAck(data: Data("{}".utf8)) == nil && RecordingDayAck(data: nil) == nil)

        let lost = RecordingDayLost(recording: id, seqs: [7, 8, 9])
        let lostObject = try #require(try JSONSerialization.jsonObject(with: lost.data) as? [String: Any])
        #expect(Set(lostObject.keys) == ["recording", "lost"])
        #expect(RecordingDayLost(data: lost.data)?.seqs == [7, 8, 9])
    }

    // MARK: The watch's state

    @Test func watchStateRoundTripsAndReadsNewStates() throws {
        let state = RecordingDayWatchState(recording: UUID(), day: "2027-01-15", state: .interrupted, since: anchor, next: 12, pending: 3)
        #expect(RecordingDayWatchState(data: state.data) == state)
        #expect(state.data == state.data, "the same state is the same bytes, so an unchanged push is skipped")
        #expect(state.isOn)
        #expect(!RecordingDayWatchState(state: .off).isOn && !RecordingDayWatchState(state: .stopped).isOn)
        #expect(RecordingDayWatchState(state: .paused).isOn && RecordingDayWatchState(state: .recording).isOn)

        let newer = Data(#"{"state": "charging", "next": 4, "extra": true}"#.utf8)
        let read = try #require(RecordingDayWatchState(data: newer))
        #expect(read.state == .stopped && read.next == 4 && read.recording == nil)
        #expect(RecordingDayWatchState(data: Data(#"{"next": 4}"#.utf8)) == nil, "a state is required")
    }

    // MARK: Calendar

    @Test func dayFollowsTheTimeZone() throws {
        #expect(RecordingDayCalendar.day(of: anchor, in: TimeZone(identifier: "Asia/Shanghai")!) == "2027-01-15")
        // Exactly local midnight belongs to the day it starts.
        #expect(RecordingDayCalendar.day(of: anchor, in: TimeZone(identifier: "America/Los_Angeles")!) == "2027-01-15")
        #expect(RecordingDayCalendar.day(of: anchor, in: TimeZone(identifier: "Pacific/Honolulu")!) == "2027-01-14")
        // 01:30 in the hour New York repeats when DST ends is still that day.
        let repeated = try utc("2026-11-01T06:30:00Z")
        #expect(RecordingDayCalendar.day(of: repeated, in: newYork) == "2026-11-01")
    }

    @Test func midnightsAcrossDaylightSavingChanges() throws {
        // 2026-03-08 in New York has 23 hours, 2026-11-01 has 25.
        let march8 = try #require(RecordingDayCalendar.start(of: "2026-03-08", in: newYork))
        let march9 = try #require(RecordingDayCalendar.start(of: "2026-03-09", in: newYork))
        let (march8UTC, march9UTC, march8Late) = (try utc("2026-03-08T05:00:00Z"), try utc("2026-03-09T04:00:00Z"), try utc("2026-03-08T15:00:00Z"))
        #expect(march8 == march8UTC && march9 == march9UTC)
        #expect(march9.timeIntervalSince(march8) == 23 * 3600)
        #expect(RecordingDayCalendar.nextMidnight(after: march8Late, in: newYork) == march9)

        let november1 = try #require(RecordingDayCalendar.start(of: "2026-11-01", in: newYork))
        let november2 = try #require(RecordingDayCalendar.start(of: "2026-11-02", in: newYork))
        let (repeatedHour, november3) = (try utc("2026-11-01T06:30:00Z"), try utc("2026-11-03T05:00:00Z"))
        #expect(november2.timeIntervalSince(november1) == 25 * 3600)
        #expect(RecordingDayCalendar.nextMidnight(after: repeatedHour, in: newYork) == november2)
        // Midnight itself: the next one, a day later.
        #expect(RecordingDayCalendar.nextMidnight(after: november2, in: newYork) == november3)
        #expect(RecordingDayCalendar.nextMidnight(after: november2.addingTimeInterval(-1), in: newYork) == november2)
        for day in ["2026-03-08", "2026-03-09", "2026-11-01", "2026-11-02"] {
            let start = try #require(RecordingDayCalendar.start(of: day, in: newYork))
            #expect(RecordingDayCalendar.day(of: start, in: newYork) == day)
        }
    }

    @Test func aDayWhoseMidnightIsSkippedStartsAtOne() throws {
        // Havana moves its clocks from 00:00 to 01:00 on 2026-03-08: that day starts at 01:00 local (05:00 UTC).
        let havana = try #require(TimeZone(identifier: "America/Havana"))
        let (start, noonBefore) = (try utc("2026-03-08T05:00:00Z"), try utc("2026-03-07T17:00:00Z"))
        #expect(RecordingDayCalendar.start(of: "2026-03-08", in: havana) == start)
        #expect(RecordingDayCalendar.nextMidnight(after: noonBefore, in: havana) == start)
        #expect(RecordingDayCalendar.day(of: start, in: havana) == "2026-03-08")
    }

    @Test func onlyRealDatesHaveAStart() {
        for text in ["2026-02-30", "2026-13-01", "2026-00-10", "26-11-11", "2026/11/11", "2026-11-11 ", ""] {
            #expect(RecordingDayCalendar.start(of: text, in: newYork) == nil, "\(text)")
        }
        #expect(RecordingDayCalendar.start(of: "2028-02-29", in: newYork) != nil)
        #expect(RecordingDayCalendar.isDay("2026-11-11") && !RecordingDayCalendar.isDay("2026-11-1"))
    }

    // MARK: Level

    @Test func levelIsTheRMSInDBFS() {
        // A full-scale sine has a mean square of 1/2: -3.01 dBFS.
        let sine = RecordingDayLevel.dBFS(sumOfSquares: 0.5 * 16_000, count: 16_000)
        #expect(abs(sine - (-3.0103)) < 0.001)
        #expect(RecordingDayLevel.dBFS(sumOfSquares: 16_000, count: 16_000) == 0)
        // Quiet speech at RMS 0.01 is -40 dBFS, above the silence line.
        #expect(abs(RecordingDayLevel.dBFS(sumOfSquares: 0.0001 * 100, count: 100) - (-40)) < 0.0001)
        #expect(RecordingDayLevel.dBFS(sumOfSquares: 0.0001 * 100, count: 100) > RecordingDayContract.silenceDBFS)
        let floors: [(Double, Int)] = [(0, 100), (1, 0), (.nan, 10), (.infinity, 10), (1e-30, 16_000), (-1, 10)]
        for (sum, count) in floors {
            #expect(RecordingDayLevel.dBFS(sumOfSquares: sum, count: count) == -160, "\(sum) / \(count)")
        }
    }

    // MARK: Erase ranges

    @Test func dayEraseKeepsTheChunkAfterTheCut() {
        let range = RecordingErase(from: anchor.addingTimeInterval(5), to: anchor.addingTimeInterval(38.2))
        func chunk(_ start: Double, _ duration: Double = 10) -> RecordingSegment {
            RecordingSegment(index: 0, start: 0, duration: duration, startedAt: anchor.addingTimeInterval(start))
        }
        #expect(chunk(0).fallsInDayErase(range), "a chunk reaching into the start counts")
        #expect(!chunk(-5).fallsInDayErase(range), "one ending right where the range starts doesn't")
        #expect(chunk(30, 8).fallsInDayErase(range), "the chunk the erase cut")
        #expect(chunk(37.5, 0.2).fallsInDayErase(range), "a short chunk inside it")
        // A chunk opened a moment before the tap and cut at it holds only audio from before the tap, however short.
        #expect(chunk(38, 0.2).fallsInDayErase(range), "a chunk starting in the last half second, ending at the cut")
        #expect(chunk(38.1, 0.15).fallsInDayErase(range), "one reaching a few milliseconds past `to` ended at the cut too")
        #expect(!chunk(38).fallsInDayErase(range), "the chunk the cut opened, a moment before `to`, goes on after it")
        #expect(chunk(37.6).fallsInDayErase(range), "a chunk going on past `to` that began earlier than any cut's next chunk")
        #expect(!chunk(38.2).fallsInDayErase(range), "one starting right at `to` doesn't")
        #expect(!chunk(40).fallsInDayErase(range))
        var marker = chunk(10, 0)
        #expect(!marker.fallsInDayErase(range), "a header-only marker holds no audio")
        marker = chunk(10)
        marker.lost = true
        #expect(!marker.fallsInDayErase(range))
    }

    // MARK: Cached flags

    @Test func hubFlagIsCachedAndWrittenOnlyOnChange() throws {
        let name = "recording-day-\(UUID().uuidString)"
        let defaults = try #require(CountingDefaults(suiteName: name))
        defer { defaults.removePersistentDomain(forName: name) }
        #expect(!RecordingDayDefaults.offered([], defaults: defaults))
        #expect(RecordingDayDefaults.offered([RecordingDayContract.feature, "x"], defaults: defaults))
        RecordingDayDefaults.remember(offered: false, defaults: defaults)
        #expect(defaults.writes == 1, "the first answer is kept, even false")
        RecordingDayDefaults.remember(offered: false, defaults: defaults)
        #expect(defaults.writes == 1)
        RecordingDayDefaults.remember(offered: true, defaults: defaults)
        RecordingDayDefaults.remember(offered: true, defaults: defaults)
        #expect(defaults.writes == 2)
        #expect(RecordingDayDefaults.offered([], defaults: defaults), "without a hello, the last one")
        #expect(!RecordingDayDefaults.offered(["meeting_attendance"], defaults: defaults), "a hello without the flag wins")
    }

    @Test func phoneCapabilitiesChangeOnlyWithFullContexts() throws {
        let name = "watch-capabilities-\(UUID().uuidString)"
        let defaults = try #require(CountingDefaults(suiteName: name))
        defer { defaults.removePersistentDomain(forName: name) }
        let full: [String: Any] = ["recordingRolling": true, RecordingDayContract.phoneKey: 1, RecordingDayContract.phoneHubKey: false,
                                   "summary": Data()]
        #expect(WatchPhoneCapabilities.store(full, full: true, in: defaults) == 3)
        #expect(defaults.integer(forKey: WatchPhoneCapabilities.dayKey) == 1)
        #expect(defaults.object(forKey: WatchPhoneCapabilities.dayHubKey) as? Bool == false, "false is kept apart from absent")
        // The phone resends its whole context with every summary: nothing is written again.
        let before = defaults.writes
        #expect(WatchPhoneCapabilities.store(full, full: true, in: defaults) == 0)
        #expect(defaults.writes == before)
        // A pairing push by user info, or a config reply, carries no day keys and removes nothing.
        #expect(WatchPhoneCapabilities.store(["configs": Data()], full: false, in: defaults) == 0)
        #expect(defaults.integer(forKey: WatchPhoneCapabilities.dayKey) == 1)
        // The hub gained the flag.
        var hub = full
        hub[RecordingDayContract.phoneHubKey] = true
        #expect(WatchPhoneCapabilities.store(hub, full: true, in: defaults) == 1)
        #expect(defaults.bool(forKey: WatchPhoneCapabilities.dayHubKey))
        // A phone that no longer takes day recordings (an older build): its full context lacks the keys.
        #expect(WatchPhoneCapabilities.store(["recordingRolling": true], full: true, in: defaults) == 2)
        #expect(defaults.object(forKey: WatchPhoneCapabilities.dayKey) == nil && defaults.object(forKey: WatchPhoneCapabilities.dayHubKey) == nil)
        #expect(WatchPhoneCapabilities.store(["recordingRolling": true], full: true, in: defaults) == 0)
        #expect(defaults.bool(forKey: WatchPhoneCapabilities.rollingKey), "rolling keeps its value")
        #expect(WatchSync.phoneDayKey == WatchPhoneCapabilities.dayKey && WatchSync.phoneDayHubKey == WatchPhoneCapabilities.dayHubKey)
    }

    // MARK: Command order

    @Test func aLateCommandNeverUndoesANewerOne() throws {
        let name = "watch-day-commands-\(UUID().uuidString)"
        let defaults = try #require(UserDefaults(suiteName: name))
        defer { defaults.removePersistentDomain(forName: name) }
        // The phone sends "start", then "stop"; start's message fails only after stop went out.
        let stamps = WatchDayCommandOrder.Stamps()
        let start = stamps.next(now: anchor.addingTimeInterval(-30))
        let stop = stamps.next(now: anchor.addingTimeInterval(-20))
        #expect(!stamps.isNewest(start), "the failed start is not queued as user info")
        #expect(stamps.isNewest(stop))
        // Had it been queued anyway, the watch gets stop, then the late start.
        #expect(WatchDayCommandOrder.accept(sentAt: stop, now: anchor, defaults: defaults))
        #expect(!WatchDayCommandOrder.accept(sentAt: start, now: anchor, defaults: defaults), "the late start is dropped")
        #expect(!WatchDayCommandOrder.accept(sentAt: stop, now: anchor, defaults: defaults), "stop arriving twice counts once")
        let relaunched = try #require(UserDefaults(suiteName: name))
        #expect(!WatchDayCommandOrder.accept(sentAt: start, now: anchor, defaults: relaunched), "the order outlives a relaunch")
        // A newer command is acted on, even when the phone's clock stepped back a little.
        let again = stamps.next(now: anchor.addingTimeInterval(-25))
        #expect(again > stop)
        #expect(WatchDayCommandOrder.accept(sentAt: again, now: anchor, defaults: defaults))
        #expect(stamps.next(now: anchor) < stamps.next(now: anchor), "two commands in the same instant still have an order")
    }

    @Test func aStaleCommandIsDroppedWithoutMovingTheOrder() throws {
        let name = "watch-day-commands-\(UUID().uuidString)"
        let defaults = try #require(UserDefaults(suiteName: name))
        defer { defaults.removePersistentDomain(forName: name) }
        let sent = anchor.timeIntervalSince1970
        let late = anchor.addingTimeInterval(WatchDayCommandOrder.freshness + 1)
        #expect(!WatchDayCommandOrder.accept(sentAt: sent, now: late, defaults: defaults), "queued too long ago")
        #expect(defaults.object(forKey: WatchDayCommandOrder.lastKey) == nil)
        #expect(WatchDayCommandOrder.accept(sentAt: sent, now: anchor.addingTimeInterval(WatchDayCommandOrder.freshness), defaults: defaults))
        #expect(!WatchDayCommandOrder.accept(sentAt: .nan, now: anchor, defaults: defaults))
        #expect(WatchDayCommandOrder.accept(sentAt: nil, now: anchor, defaults: defaults), "an unstamped command can't be ordered")
        #expect(defaults.double(forKey: WatchDayCommandOrder.lastKey) == sent)
    }
}

/// Counts writes to user defaults (a set or a removal is one, however Foundation carries it out inside).
private final class CountingDefaults: UserDefaults, @unchecked Sendable {
    private(set) var writes = 0
    private var depth = 0

    private func counting(_ write: () -> Void) {
        if depth == 0 { writes += 1 }
        depth += 1
        write()
        depth -= 1
    }

    override func set(_ value: Any?, forKey defaultName: String) { counting { super.set(value, forKey: defaultName) } }
    override func set(_ value: Bool, forKey defaultName: String) { counting { super.set(value, forKey: defaultName) } }
    override func set(_ value: Int, forKey defaultName: String) { counting { super.set(value, forKey: defaultName) } }
    override func removeObject(forKey defaultName: String) { counting { super.removeObject(forKey: defaultName) } }
}

/// The library's all-day operations, on a temporary folder.
@MainActor struct RecordingDayTests {
    private func library() -> RecordingLibrary {
        RecordingLibrary(root: FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString))
    }

    /// An open day recording on the watch that began at `anchor`.
    private func day(_ lib: RecordingLibrary, id: UUID = UUID(), createdAt: Date = anchor, day: String = "2027-01-15") throws -> UUID {
        try lib.create(source: .watch, kind: .day, id: id, createdAt: createdAt, day: day, timeZone: "Asia/Shanghai").id
    }

    /// Chunk `seq`, `duration` seconds from `start` seconds after `anchor`, with its audio on disk.
    @discardableResult
    private func chunk(_ lib: RecordingLibrary, _ id: UUID, _ seq: Int, at start: Double, duration: Double = 10,
                       register: Bool = true) throws -> RecordingSegment {
        try Data([UInt8(seq)]).write(to: lib.audio(id, seq))
        let segment = RecordingSegment(index: seq, start: start, duration: duration, startedAt: anchor.addingTimeInterval(start), level: -30)
        if register { lib.addSegment(id, segment) }
        return segment
    }

    private func hasAudio(_ lib: RecordingLibrary, _ id: UUID, _ seq: Int) -> Bool {
        FileManager.default.fileExists(atPath: lib.audio(id, seq).path)
    }

    @Test func createKeepsTheKindTheDayAndAudioAlreadyThere() throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        // The writer of the next day's recording can put audio in its folder before the entry exists.
        let next = UUID()
        try FileManager.default.createDirectory(at: lib.folder(next), withIntermediateDirectories: true)
        try Data([1]).write(to: lib.audio(next, 0))
        let id = try day(lib, id: next)
        let entry = try #require(lib.entry(id))
        #expect(entry.kind == .day && entry.isDay && entry.day == "2027-01-15" && entry.timeZone == "Asia/Shanghai")
        #expect(entry.createdAt == anchor && entry.active && entry.source == .watch)
        #expect(hasAudio(lib, id, 0))
        #expect(RecordingLibrary(root: lib.root).entry(id)?.kind == .day, "the kind is saved")
        // Asking again for the same id changes nothing.
        let again = try lib.create(source: .watch, title: "Other", kind: .day, id: id, createdAt: anchor.addingTimeInterval(60))
        #expect(again.title.isEmpty && again.createdAt == anchor)
        let clip = try lib.create(source: .watch)
        let rolling = try lib.create(source: .watch, kind: .rolling)
        #expect(clip.kind == nil, "a clip stays nil, as before kinds")
        #expect(rolling.kind == .rolling)
    }

    @Test func eraseWhileAChunkIsOpenLeavesNoSeqWithoutARecord() throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let id = try day(lib)
        for seq in 0..<3 { try chunk(lib, id, seq, at: Double(seq) * 10) }
        lib.markDelivered(id, seqs: [0])
        // Chunk 3 is being written (30…38 s); Erase cuts it at 38 s and names the range up to 0.2 s later.
        let open = try chunk(lib, id, 3, at: 30, duration: 8, register: false)
        #expect(lib.eraseDay(id, from: anchor.addingTimeInterval(5), to: anchor.addingTimeInterval(38.2)) == [0, 1, 2])
        lib.addSegment(id, open)
        try chunk(lib, id, 4, at: 38)

        let entry = try #require(lib.entry(id))
        #expect(entry.segments.map(\.index) == [0, 1, 2, 3, 4], "every seq has a record")
        #expect(entry.nextIndex == 5)
        for seq in 0...3 {
            let record = try #require(entry.segments.first { $0.index == seq })
            #expect(record.erased == true && !record.delivered, "seq \(seq) goes again, as an erased marker")
            #expect(record.startedAt != nil && record.duration > 0, "seq \(seq) keeps its times")
            #expect(!hasAudio(lib, id, seq), "seq \(seq) has no audio")
        }
        #expect(entry.segments.last?.erased == nil && hasAudio(lib, id, 4), "the chunk after the cut keeps its audio")
        #expect(entry.erased?.count == 1)
        #expect(lib.eraseDay(id, from: anchor.addingTimeInterval(5), to: anchor.addingTimeInterval(38.2)) == [], "nothing new")
    }

    @Test func aChunkOpenedJustBeforeTheTapIsErasedToo() throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let id = try day(lib)
        try chunk(lib, id, 0, at: 0)
        // Chunk 1 opened at 10 s; Erase cuts it at 10.2 s and names the range up to 10.25 s.
        let cut = try chunk(lib, id, 1, at: 10, duration: 0.2)
        #expect(lib.eraseDay(id, from: anchor.addingTimeInterval(-590), to: anchor.addingTimeInterval(10.25)) == [0, 1])
        try chunk(lib, id, 2, at: 10.15)
        // The same cut with its chunk still on the way: it is registered after the erase, and erased then.
        let late = try day(lib, id: UUID())
        try chunk(lib, late, 0, at: 0)
        #expect(lib.eraseDay(late, from: anchor.addingTimeInterval(-590), to: anchor.addingTimeInterval(10.25)) == [0])
        try Data([1]).write(to: lib.audio(late, 1))
        lib.addSegment(late, cut)
        try chunk(lib, late, 2, at: 10.15)

        for recording in [id, late] {
            let entry = try #require(lib.entry(recording))
            #expect(entry.segments.map(\.index) == [0, 1, 2])
            #expect(entry.segments.prefix(2).allSatisfy { $0.erased == true }, "nothing from before the tap is left")
            #expect(!hasAudio(lib, recording, 1))
            #expect(entry.segments[2].erased == nil && hasAudio(lib, recording, 2), "the chunk the cut opened keeps its audio")
        }
    }

    @Test func eraseLeavesMarkersAndOtherKindsAlone() throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let id = try day(lib)
        try chunk(lib, id, 0, at: 0)
        lib.change(id) { $0.segments[0].lost = true }
        lib.closeDay(id, at: anchor.addingTimeInterval(20), after: RecordingDayContract.Reason.paused)
        #expect(lib.eraseDay(id, from: anchor, to: anchor.addingTimeInterval(60)) == [])
        #expect(lib.entry(id)?.segments.allSatisfy { $0.erased == nil } == true)

        let rolling = try lib.create(source: .watch, kind: .rolling)
        lib.addSegment(rolling.id, RecordingSegment(index: 0, start: 0, duration: 10, startedAt: anchor))
        #expect(lib.eraseDay(rolling.id, from: anchor, to: anchor.addingTimeInterval(60)) == [])
        #expect(lib.entry(rolling.id)?.erased == nil, "rolling recordings erase with `erase`")
    }

    @Test func acknowledgedChunksLoseTheirAudioAndMarks() throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let id = try day(lib)
        try chunk(lib, id, 0, at: 0)
        try chunk(lib, id, 1, at: 10)
        let bookmark = RecordingDayMark(at: anchor.addingTimeInterval(4), label: RecordingDayMark.bookmark)
        let meeting = RecordingDayMark(at: anchor.addingTimeInterval(14), label: RecordingDayMark.meeting)
        lib.change(id) { $0.dayMarks = [bookmark, meeting] }

        lib.markDelivered(id, seqs: [0], removingMarks: [bookmark])
        var entry = try #require(lib.entry(id))
        #expect(entry.segments.map(\.delivered) == [true, false], "the record stays")
        #expect(!hasAudio(lib, id, 0) && hasAudio(lib, id, 1))
        #expect(entry.dayMarks == [meeting])
        // The same acknowledgement again (a resent file's) writes nothing.
        let saved = entry.updatedAt
        lib.markDelivered(id, seqs: [0])
        #expect(lib.entry(id)?.updatedAt == saved)

        lib.markDelivered(id, seqs: Set([1, 7]), removingMarks: [meeting])
        entry = try #require(lib.entry(id))
        #expect(entry.segments.allSatisfy(\.delivered) && entry.dayMarks == nil && !hasAudio(lib, id, 1))
    }

    @Test func deliveredRecordsArePrunedWithATombstone() throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let id = try day(lib)
        for seq in 0..<3 { try chunk(lib, id, seq, at: Double(seq) * 10) }
        lib.markDelivered(id, seqs: [0, 1])
        // Chunk 0 ended at 10 s, chunk 1 at 20 s.
        lib.pruneDelivered(id, olderThan: anchor.addingTimeInterval(15))
        var entry = try #require(lib.entry(id))
        #expect(entry.segments.map(\.index) == [1, 2] && entry.prunedBelow == 1 && entry.nextIndex == 3)
        // A late copy of a pruned chunk (the writer's replay at stop) never comes back.
        try chunk(lib, id, 0, at: 0)
        #expect(lib.entry(id)?.segments.map(\.index) == [1, 2])
        #expect(!hasAudio(lib, id, 0))
        // An unacknowledged chunk stays however old it is.
        lib.pruneDelivered(id, olderThan: anchor.addingTimeInterval(3600))
        entry = try #require(lib.entry(id))
        #expect(entry.segments.map(\.index) == [2] && entry.nextIndex == 3 && hasAudio(lib, id, 2))
    }

    @Test func closingADayAddsOneEndMarker() throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let id = try day(lib)
        try chunk(lib, id, 0, at: 0)
        try chunk(lib, id, 1, at: 10)
        let end = anchor.addingTimeInterval(16 * 3600)
        lib.closeDay(id, at: end, after: RecordingDayContract.Reason.paused)
        let entry = try #require(lib.entry(id))
        let marker = try #require(entry.segments.last)
        #expect(marker.index == 2 && marker.duration == 0 && marker.end == true && marker.isDayMarker)
        #expect(marker.startedAt == end && marker.after == RecordingDayContract.Reason.paused)
        #expect(!entry.active && entry.expectedSegments == 3 && entry.dayEndAt == end)
        lib.closeDay(id, at: end.addingTimeInterval(60), after: nil)
        #expect(lib.entry(id)?.segments.count == 3, "one end marker only")

        // A day whose last chunk already says `end` (cut at midnight while recording) gets no marker.
        let other = try day(lib, createdAt: anchor.addingTimeInterval(1))
        try chunk(lib, other, 0, at: 0)
        lib.change(other) { $0.segments[0].end = true }
        lib.closeDay(other, at: end, after: nil)
        #expect(lib.entry(other)?.segments.count == 1 && lib.entry(other)?.active == false)
    }

    @Test func openDaysAreFoundNewestFirst() throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let older = try day(lib, createdAt: anchor)
        let newer = try day(lib, createdAt: anchor.addingTimeInterval(600))
        let closed = try day(lib, createdAt: anchor.addingTimeInterval(1200))
        lib.closeDay(closed, at: anchor.addingTimeInterval(1300), after: nil)
        let yesterday = try day(lib, createdAt: anchor.addingTimeInterval(-86_400), day: "2027-01-14")
        try lib.create(source: .phone, kind: .day, createdAt: anchor.addingTimeInterval(1800), day: "2027-01-15")
        try lib.create(source: .watch, kind: .rolling)
        #expect(lib.openDay(source: .watch, day: "2027-01-15")?.id == newer)
        #expect(lib.openDay(source: .watch, day: "2027-01-16") == nil)
        #expect(lib.openDays(source: .watch).map(\.id) == [newer, older, yesterday])
    }

    @Test func recoverKeepsDaysOpenAndNamesTheGap() throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let recording = try day(lib)
        try chunk(lib, recording, 0, at: 0)
        try chunk(lib, recording, 1, at: 10, register: false) // half written when the app ended
        let paused = try day(lib, createdAt: anchor.addingTimeInterval(1))
        lib.change(paused) { $0.gapReason = RecordingDayContract.Reason.paused }
        let clip = try lib.create(source: .watch)

        let restored = RecordingLibrary(root: lib.root)
        #expect(Set(restored.recover(source: .watch)) == [recording, paused])
        let entry = try #require(restored.entry(recording))
        #expect(entry.active && entry.gapReason == RecordingDayContract.Reason.stopped && entry.expectedSegments == nil)
        #expect(hasAudio(lib, recording, 0) && !hasAudio(lib, recording, 1))
        #expect(entry.nextIndex == 1, "the unregistered seq is free again: the phone never saw it")
        #expect(restored.entry(paused)?.gapReason == RecordingDayContract.Reason.paused, "a pause keeps its reason")
        #expect(restored.entry(clip.id)?.active == false, "other recordings end as before")
        #expect(restored.recover(source: .phone).isEmpty)
    }

    @Test func aDeletedRecordingStaysDeleted() async throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let id = try day(lib)
        let kept = try day(lib, createdAt: anchor.addingTimeInterval(1))
        #expect(await Self.offMain(lib, id) == (false, false))
        try lib.remove(id)
        #expect(await Self.offMain(lib, id) == (false, true))
        #expect(await Self.offMain(lib, kept) == (false, false))
        // A write already under way when it was deleted put its folder back.
        try FileManager.default.createDirectory(at: lib.folder(id), withIntermediateDirectories: true)
        try JSONEncoder().encode(RecordingEntry(id: id, createdAt: anchor, source: .watch, kind: .day))
            .write(to: lib.folder(id).appendingPathComponent("recording.json"))
        let restored = RecordingLibrary(root: lib.root)
        #expect(restored.entry(id) == nil && restored.entry(kept) != nil)
        #expect(!FileManager.default.fileExists(atPath: lib.folder(id).path), "its folder is gone")
        #expect(restored.isDeletedSnapshot(id) && restored.isDeleted(id))
    }

    /// Asks from a thread that isn't the main one, as the phone's chunk store does.
    nonisolated private static func probe(_ lib: RecordingLibrary, _ id: UUID) -> (main: Bool, deleted: Bool) {
        (Thread.isMainThread, lib.isDeletedSnapshot(id))
    }

    private static func offMain(_ lib: RecordingLibrary, _ id: UUID) async -> (Bool, Bool) {
        let result = await Task.detached { probe(lib, id) }.value
        return (result.main, result.deleted)
    }

    @Test func phoneIndexRowIsWrittenOnlyWhenItChanges() throws {
        let lib = library()
        defer { try? FileManager.default.removeItem(at: lib.root) }
        let id = UUID()
        let first = try #require(lib.adoptDay(id: id, day: "2027-01-15", timeZone: "Asia/Shanghai", createdAt: anchor.addingTimeInterval(60)))
        #expect(first.isDay && first.source == .watch && first.active && first.segments.isEmpty)
        #expect(first.day == "2027-01-15" && first.timeZone == "Asia/Shanghai" && first.createdAt == anchor.addingTimeInterval(60))
        let saved = first.updatedAt
        // The next chunk of the same day: nothing to write.
        #expect(lib.adoptDay(id: id, day: "2027-01-15", timeZone: "Asia/Shanghai", createdAt: anchor.addingTimeInterval(60))?.updatedAt == saved)
        // The recording's real start (from a later header) is earlier; a later time never moves it back.
        lib.adoptDay(id: id, day: "2027-01-15", timeZone: "Asia/Shanghai", createdAt: anchor)
        lib.adoptDay(id: id, day: "2027-01-15", timeZone: "Asia/Shanghai", createdAt: anchor.addingTimeInterval(120))
        #expect(lib.entry(id)?.createdAt == anchor)
        lib.adoptDay(id: id, day: "2027-01-15", timeZone: "Asia/Shanghai", createdAt: anchor, closed: true)
        lib.adoptDay(id: id, day: "2027-01-15", timeZone: "Asia/Shanghai", createdAt: anchor)
        #expect(lib.entry(id)?.active == false, "an ended day is never reopened")
        #expect(RecordingLibrary(root: lib.root).entry(id)?.createdAt == anchor)

        try lib.remove(id)
        #expect(lib.adoptDay(id: id, day: "2027-01-15", timeZone: "Asia/Shanghai", createdAt: anchor) == nil)
        #expect(lib.entry(id) == nil && !FileManager.default.fileExists(atPath: lib.folder(id).path))
    }

    @Test func entriesFromBeforeDaysDecode() throws {
        var entry = RecordingEntry(id: UUID(), createdAt: anchor, source: .watch, kind: .rolling)
        entry.segments = [RecordingSegment(index: 0, start: 0, duration: 30, startedAt: anchor)]
        var json = try #require(try JSONSerialization.jsonObject(with: JSONEncoder().encode(entry)) as? [String: Any])
        for key in ["day", "timeZone", "gapReason", "dayMarks", "dayEndAt"] { #expect(json[key] == nil, "\(key) is not written when unset") }
        json["segments"] = [["index": 0, "start": 0, "duration": 30, "delivered": false]]
        let old = try JSONDecoder().decode(RecordingEntry.self, from: JSONSerialization.data(withJSONObject: json))
        #expect(old.day == nil && old.dayMarks == nil && old.segments.first?.level == nil && old.segments.first?.erased == nil)
        #expect(!old.isDay && old.isRolling)
    }

    @Test func aDayIsTitledByItsDateInItsOwnTimeZone() throws {
        // 2027-01-15 in Kiritimati (UTC+14) begins at 10:00 UTC the day before: still the 15th wherever the phone is.
        let zone = try #require(TimeZone(identifier: "Pacific/Kiritimati"))
        var entry = RecordingEntry(id: UUID(), createdAt: anchor, source: .watch, kind: .day)
        entry.day = "2027-01-15"
        entry.timeZone = zone.identifier
        let noon = try #require(RecordingDayCalendar.start(of: "2027-01-15", in: zone)).addingTimeInterval(12 * 3600)
        #expect(entry.displayTitle.contains(noon.formatted(Date.FormatStyle(date: .abbreviated, time: .omitted, timeZone: zone))))
        entry.title = "Offsite"
        #expect(entry.displayTitle == "Offsite")
    }
}
