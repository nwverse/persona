import Foundation
import Testing
import UserNotifications
@testable import Persona

private let anchor = Date(timeIntervalSince1970: 1_800_000_000)
private typealias Policy = AllDayAlertPolicy

/// The watch's bookkeeping as `WatchAllDay` keeps it, driven only by the policy's outputs: when the dead-man's switch was
/// last armed, and the complication's snapshot as last written.
private struct WatchBook {
    private(set) var armedAt: Date?
    private(set) var snapshot: AllDaySnapshot
    private(set) var heartbeats = 0

    init(startedAt: Date) {
        snapshot = AllDaySnapshot(state: .off, updatedAt: startedAt)
        apply(Policy.decide(.started, appActive: false), at: startedAt)
    }

    var deadManFires: Date? { armedAt?.addingTimeInterval(Policy.deadManSeconds) }

    mutating func chunk(at t: Date, recording: Bool = true) {
        let out = Policy.decide(.chunkSaved(deadManLeft: deadManFires.map { $0.timeIntervalSince(t) },
                                            sinceHeartbeat: t.timeIntervalSince(snapshot.updatedAt), recording: recording),
                                appActive: false)
        if out.snapshot != nil { heartbeats += 1 }
        apply(out, at: t)
    }

    mutating func apply(_ out: Policy.Output, at t: Date) {
        switch out.deadMan {
        case .arm: armedAt = t
        case .disarm: armedAt = nil
        case .keep: break
        }
        if let state = out.snapshot {
            // A heartbeat keeps when the state began; a new state begins now.
            snapshot = AllDaySnapshot(state: state, since: state == snapshot.state ? snapshot.since : t, updatedAt: t)
        }
    }
}

/// All-day recording's access, alerts, dead-man's switch and complication snapshot (docs/assistant/day-recap.md),
/// decided off the main actor as the watch's notification delegate needs them.
struct AllDayAlertPolicyTests {
    private static let pausing: [RecordingDayHalt] = [.interrupted, .resumeFailed, .microphoneChanged]
    private static let stopping: [RecordingDayHalt] = [.audioReset, .lowBattery, .lowStorage, .writeFailed, .crashed]
    private static let running: [AllDaySnapshot.State] = [.recording, .paused, .interrupted]

    // MARK: - Access: the consent is the iPhone's

    @Test func aPhoneFromBeforeHidesAllDayRecording() {
        // No `recordingDay` at all: nothing offers it.
        #expect(Policy.access(phoneDay: nil, state: .off) == .hidden)
    }

    @Test func aPhoneWithoutConsentYetPointsThere() {
        #expect(Policy.access(phoneDay: 0, state: .off) == .needsConsent)
        // Stopped by itself earlier: still shown (Stop for Today ends the wish), but no tap continues it.
        #expect(Policy.access(phoneDay: 0, state: .stopped) == .needsConsent)
        #expect(Policy.access(phoneDay: nil, state: .stopped) == .needsConsent)
        // A value the watch can't make sense of is no agreement.
        #expect(Policy.access(phoneDay: -1, state: .off) == .needsConsent)
    }

    @Test func anAgreedPhoneStartsWithOneTapAtAnyVersion() {
        for version in [RecordingDayContract.version, RecordingDayContract.version + 1, 7] {
            #expect(Policy.access(phoneDay: version, state: .off) == .ready)
            #expect(Policy.access(phoneDay: version, state: .stopped) == .ready)
        }
    }

    @Test func aRunningRecordingKeepsItsControlsWhateverThePhoneSaysNow() {
        // A new consent version takes effect at the next start: the recording running goes on under its agreement.
        for state in Self.running {
            for phoneDay in [nil, 0, 1] as [Int?] {
                #expect(Policy.access(phoneDay: phoneDay, state: state) == .ready)
            }
        }
    }

    @Test func onlyAnAgreementOrThePhonesOwnRequestStartsTheMicrophone() {
        #expect(Policy.mayStart(.ready, askedByPhone: false))
        #expect(!Policy.mayStart(.needsConsent, askedByPhone: false))
        #expect(!Policy.mayStart(.hidden, askedByPhone: false))
        // The phone asks only after its consent sheet; its context saying so may still be on the way.
        #expect(Policy.mayStart(.needsConsent, askedByPhone: true))
        #expect(Policy.mayStart(.hidden, askedByPhone: true))
    }

    // MARK: - Events

    @Test func startAndResumeWithdrawArmAndShowRecording() {
        for event in [Policy.Event.started, .resumed] {
            #expect(Policy.decide(event, appActive: true)
                    == Policy.Output(haptic: .start, withdraw: true, snapshot: .recording, deadMan: .arm))
            // Wrist down: no haptic (watchOS ignores it in the background), the rest the same.
            #expect(Policy.decide(event, appActive: false)
                    == Policy.Output(withdraw: true, snapshot: .recording, deadMan: .arm))
        }
    }

    @Test func userPauseAndStopDisarmAndHandOffWithoutNotice() {
        #expect(Policy.decide(.pausedByUser, appActive: true)
                == Policy.Output(haptic: .stop, withdraw: true, snapshot: .paused, deadMan: .disarm, handOff: true))
        #expect(Policy.decide(.stoppedByUser, appActive: false)
                == Policy.Output(withdraw: true, snapshot: .off, deadMan: .disarm, handOff: true))
    }

    @Test func everyHaltNotifiesAtOnceAsPausedOrStopped() {
        for why in Self.pausing {
            let out = Policy.decide(.halted(why), appActive: false)
            #expect(Policy.state(after: why) == .interrupted)
            #expect(out == Policy.Output(notify: .paused(why), withdraw: true, snapshot: .interrupted, deadMan: .disarm,
                                         handOff: true))
        }
        for why in Self.stopping {
            let out = Policy.decide(.halted(why), appActive: true)
            #expect(Policy.state(after: why) == .stopped)
            #expect(out == Policy.Output(haptic: .failure, notify: .stopped(why), withdraw: true, snapshot: .stopped,
                                         deadMan: .disarm, handOff: true))
        }
    }

    @Test func deletedOnThePhoneStopsSaysSoAndShowsOff() {
        #expect(Policy.decide(.deletedOnPhone, appActive: true)
                == Policy.Output(haptic: .stop, notify: .deleted, withdraw: true, snapshot: .off, deadMan: .disarm,
                                 handOff: true))
        // Wrist down: the notice is what says it.
        #expect(Policy.decide(.deletedOnPhone, appActive: false)
                == Policy.Output(notify: .deleted, withdraw: true, snapshot: .off, deadMan: .disarm, handOff: true))
    }

    @Test func noticesKeepOneRequestPerKind() {
        #expect(Policy.Notice.paused(.interrupted).request == AllDayNotice.pausedRequest)
        #expect(Policy.Notice.paused(.resumeFailed).category == AllDayNotice.pausedCategory)
        // A stopped notice replaces the pending dead-man's switch: the same request id.
        #expect(Policy.Notice.stopped(.crashed).request == AllDayNotice.stoppedRequest)
        // …and continues with the paused notice's Continue action.
        #expect(Policy.Notice.stopped(.lowBattery).category == AllDayNotice.pausedCategory)
        #expect(Policy.Notice.start.request == AllDayNotice.startRequest)
        #expect(Policy.Notice.start.category == AllDayNotice.startCategory)
        // A deletion is its own: nothing to continue, and the dead-man's request stays the dead-man's.
        #expect(Policy.Notice.deleted.request == AllDayNotice.deletedRequest)
        #expect(Policy.Notice.deleted.category == AllDayNotice.deletedCategory)
        #expect(AllDayNotice.categories == ["allday.paused", "allday.start", "allday.deleted"])
        #expect(AllDayNotice.requests == ["allday.paused", "allday.stopped", "allday.start", "allday.deleted"])
    }

    @Test func onlyANoticesButtonOpensTheMicrophone() {
        #expect(AllDayNotice.link(answering: AllDayNotice.continueAction) == .resume)
        #expect(AllDayNotice.link(answering: AllDayNotice.startAction) == .start)
        // A tap on the notice itself only opens the page, where the same button is one tap away.
        #expect(AllDayNotice.link(answering: UNNotificationDefaultActionIdentifier) == .open)
    }

    @Test func onlyThePhonesStartNoticeSpeaksForThePhone() {
        #expect(AllDayNotice.askedByPhone(answering: AllDayNotice.startAction))
        #expect(!AllDayNotice.askedByPhone(answering: AllDayNotice.continueAction))
        #expect(!AllDayNotice.askedByPhone(answering: UNNotificationDefaultActionIdentifier))
    }

    @Test func phoneStartWaitsForTheAppInFront() {
        #expect(Policy.decide(.phoneAskedToStart, appActive: false) == Policy.Output(notify: .start))
        #expect(Policy.decide(.phoneAskedToStart, appActive: true) == Policy.Output(haptic: .notification, startsNow: true))
    }

    // MARK: - Chunks: the dead-man's switch and the heartbeat

    @Test func chunkRenewsTheDeadManOnlyWhenItRunsLow() {
        func deadMan(_ left: TimeInterval?) -> Policy.DeadMan {
            Policy.decide(.chunkSaved(deadManLeft: left, sinceHeartbeat: 0, recording: true), appActive: false).deadMan
        }
        #expect(deadMan(nil) == .arm)
        #expect(deadMan(Policy.deadManSeconds - 10) == .keep)
        #expect(deadMan(Policy.deadManRenewBelow) == .keep)
        #expect(deadMan(Policy.deadManRenewBelow - 1) == .arm)
        // Already past (the clock moved): renew at once.
        #expect(deadMan(-5) == .arm)
    }

    @Test func chunkWritesAHeartbeatAtMostEveryTenMinutes() {
        func beat(_ since: TimeInterval?) -> AllDaySnapshot.State? {
            Policy.decide(.chunkSaved(deadManLeft: 500, sinceHeartbeat: since, recording: true), appActive: true).snapshot
        }
        #expect(beat(nil) == .recording)
        #expect(beat(Policy.heartbeatEvery - 1) == nil)
        #expect(beat(Policy.heartbeatEvery) == .recording)
        let out = Policy.decide(.chunkSaved(deadManLeft: nil, sinceHeartbeat: nil, recording: true), appActive: true)
        #expect(out.haptic == nil && out.notify == nil && !out.withdraw && !out.handOff && !out.startsNow)
    }

    @Test func aChunkArrivingAfterAPauseChangesNothing() {
        #expect(Policy.decide(.chunkSaved(deadManLeft: nil, sinceHeartbeat: nil, recording: false), appActive: true)
                == Policy.Output())
    }

    @Test(arguments: [RecordingDayContract.liveChunkSeconds, RecordingDayContract.backlogChunkSeconds])
    func fourHoursOfRecordingNeverLookStaleNorFireTheDeadMan(chunk: Double) throws {
        var book = WatchBook(startedAt: anchor)
        let end = anchor.addingTimeInterval(4 * 3600)
        var t = anchor
        while t < end {
            t = t.addingTimeInterval(chunk)
            book.chunk(at: t)
            // Up to the next chunk the face shows the recording since its start, and the dead-man's switch is ahead.
            let beforeNext = t.addingTimeInterval(chunk - 0.001)
            #expect(book.snapshot.face(at: beforeNext) == .recording(since: anchor))
            let fires = try #require(book.deadManFires)
            #expect(fires > beforeNext)
        }
        // One reload per heartbeat interval, not one per chunk.
        #expect(book.heartbeats == Int(4 * 3600 / Policy.heartbeatEvery))
    }

    @Test func chunksStoppingTurnTheFaceStaleAndFireTheDeadMan() throws {
        var book = WatchBook(startedAt: anchor)
        let lastChunk = anchor.addingTimeInterval(37 * 60 + 20)
        var t = anchor
        while t < lastChunk {
            t = t.addingTimeInterval(RecordingDayContract.liveChunkSeconds)
            book.chunk(at: t)
        }
        // The app died after its last chunk: only what is already scheduled happens.
        let fires = try #require(book.deadManFires)
        #expect(fires > lastChunk && fires <= lastChunk.addingTimeInterval(Policy.deadManSeconds))
        let stale = try #require(book.snapshot.staleDate)
        #expect(stale > lastChunk && stale <= lastChunk.addingTimeInterval(AllDaySnapshot.staleAfter))
        #expect(book.snapshot.face(at: stale) == .stale(lastHeard: book.snapshot.updatedAt))
        // The provider, asked right after the last write, already holds the stale entry.
        #expect(book.snapshot.timeline(now: book.snapshot.updatedAt) == [book.snapshot.updatedAt, stale])
    }

    @Test func aPauseWinsOverTheHeartbeatThatWasDue() {
        var book = WatchBook(startedAt: anchor)
        book.chunk(at: anchor.addingTimeInterval(540))
        let pausedAt = anchor.addingTimeInterval(545)
        book.apply(Policy.decide(.pausedByUser, appActive: true), at: pausedAt)
        // The chunk the pause cut arrives after it, past the heartbeat interval: the face stays paused, no dead-man.
        book.chunk(at: anchor.addingTimeInterval(700), recording: false)
        #expect(book.snapshot.state == .paused && book.snapshot.updatedAt == pausedAt && book.armedAt == nil)
        // A paused face doesn't age.
        let dayLater = pausedAt.addingTimeInterval(86_400)
        #expect(book.snapshot.staleDate == nil)
        #expect(book.snapshot.face(at: dayLater) == .paused)
        #expect(book.snapshot.timeline(now: dayLater) == [dayLater])
    }

    // MARK: - The complication's snapshot

    @Test func anAlreadyStaleRecordingShowsStaleAtOnce() {
        let snapshot = AllDaySnapshot(state: .recording, since: anchor, updatedAt: anchor)
        let now = anchor.addingTimeInterval(AllDaySnapshot.staleAfter + 60)
        #expect(snapshot.timeline(now: now) == [now])
        #expect(snapshot.face(at: now) == .stale(lastHeard: anchor))
        #expect(snapshot.face(at: now).link == .open)
    }

    @Test func aFreshRecordingSchedulesItsOwnStaleEntry() {
        let start = anchor.addingTimeInterval(-3600)
        let snapshot = AllDaySnapshot(state: .recording, since: start, updatedAt: anchor)
        let stale = anchor.addingTimeInterval(AllDaySnapshot.staleAfter)
        #expect(snapshot.timeline(now: anchor) == [anchor, stale])
        #expect(snapshot.face(at: stale.addingTimeInterval(-1)) == .recording(since: start))
        #expect(snapshot.face(at: stale) == .stale(lastHeard: anchor))
        // Two missed heartbeats still read as recording: the stale line has room for WidgetKit's scheduling.
        #expect(AllDaySnapshot.staleAfter > 2 * Policy.heartbeatEvery)
    }

    @Test func eachFaceOpensWhatItsTapShouldDo() {
        #expect(AllDaySnapshot.face(nil, at: anchor) == .off)
        let cases: [(AllDaySnapshot.State, AllDaySnapshot.Face, AllDayLink.Action)] = [
            (.off, .off, .start), (.paused, .paused, .resume), (.interrupted, .paused, .resume), (.stopped, .stopped, .resume),
        ]
        for (state, face, link) in cases {
            let snapshot = AllDaySnapshot(state: state, since: anchor, updatedAt: anchor)
            #expect(snapshot.face(at: anchor.addingTimeInterval(86_400)) == face)
            #expect(face.link == link)
        }
        #expect(AllDaySnapshot.Face.recording(since: anchor).link == .open)
    }

    @Test func snapshotRoundTripsAndReadsWithoutItsOptionalKeys() throws {
        let full = AllDaySnapshot(state: .interrupted, since: anchor, updatedAt: anchor.addingTimeInterval(5), day: "2027-01-15", pending: 3)
        #expect(try JSONDecoder().decode(AllDaySnapshot.self, from: JSONEncoder().encode(full)) == full)
        let minimal = try JSONDecoder().decode(AllDaySnapshot.self, from: Data(#"{"state":"stopped","updatedAt":0}"#.utf8))
        #expect(minimal.state == .stopped && minimal.since == nil && minimal.day == nil && minimal.pending == nil)
    }

    @Test func linksRoundTripAndLeaveOtherLinksAlone() throws {
        for action in [AllDayLink.Action.start, .resume, .open] {
            let url = AllDayLink.url(action)
            #expect(url.scheme == "persona" && url.host() == AllDayLink.host)
            #expect(AllDayLink.action(of: url) == action)
        }
        #expect(AllDayLink.url(.resume).absoluteString == "persona://allday?action=continue")
        let bare = try #require(URL(string: "persona://allday"))
        let unknown = try #require(URL(string: "persona://allday?action=dance"))
        let meeting = try #require(URL(string: "persona://record?meeting=1"))
        #expect(AllDayLink.action(of: bare) == .open)
        #expect(AllDayLink.action(of: unknown) == .open)
        // The Meeting Recording complication's link stays the meeting's.
        #expect(AllDayLink.action(of: meeting) == nil)
    }

    // MARK: - Launch and midnight

    @Test func launchAlertsOnlyForADayThatWasRecording() {
        #expect(Policy.alertsOnLaunch(gapReason: nil, alreadyAlerted: false))
        #expect(Policy.alertsOnLaunch(gapReason: RecordingDayContract.Reason.stopped, alreadyAlerted: false))
        // The dead-man's switch (or a stop notice) already said it.
        #expect(!Policy.alertsOnLaunch(gapReason: RecordingDayContract.Reason.stopped, alreadyAlerted: true))
        // The user paused, or a call did and its notice is out: nothing new to say.
        #expect(!Policy.alertsOnLaunch(gapReason: RecordingDayContract.Reason.paused, alreadyAlerted: false))
        #expect(!Policy.alertsOnLaunch(gapReason: RecordingDayContract.Reason.interrupted, alreadyAlerted: false))
    }

    @Test func aDayIsClosedJustAfterItsMidnightOnDSTDays() throws {
        let zone = try #require(TimeZone(identifier: "America/New_York"))
        let calendar = Self.calendar(zone)
        // Both 2026 DST days end at the next midnight: 23 hours after the spring one began, 25 after the fall one.
        for (day, hours) in [("2026-03-08", 23.0), ("2026-11-01", 25.0)] {
            let start = try #require(RecordingDayCalendar.start(of: day, in: zone))
            let close = try #require(Policy.closeRefreshDate(openDays: [Policy.OpenDay(day: day, zone: zone)], now: start))
            #expect(close.timeIntervalSince(start) == hours * 3600 + Policy.closeAfterMidnight)
            #expect(calendar.component(.hour, from: close) == 0)
            #expect(RecordingDayCalendar.day(of: close, in: zone) != day)
        }
        // 01:30 on 2026-11-01 happens twice; either way the wake is just after that day's own midnight.
        let first = try #require(calendar.date(from: DateComponents(year: 2026, month: 11, day: 1, hour: 1, minute: 30)))
        for now in [first, first.addingTimeInterval(3600)] {
            let close = try #require(Policy.closeRefreshDate(openDays: [Policy.OpenDay(day: "2026-11-01", zone: zone)], now: now))
            #expect(RecordingDayCalendar.day(of: close.addingTimeInterval(-Policy.closeAfterMidnight - 1), in: zone) == "2026-11-01")
        }
    }

    @Test func aDayEndsAtItsOwnZonesMidnightAfterTheWatchMoved() throws {
        let shanghai = try #require(TimeZone(identifier: "Asia/Shanghai"))
        let newYork = try #require(TimeZone(identifier: "America/New_York"))
        // Recorded in Shanghai, the watch now in New York: the day still ends at Shanghai's midnight (11:00 in New
        // York), never at New York's, 13 hours later.
        let midnight = try #require(RecordingDayCalendar.end(of: "2026-11-11", in: shanghai))
        let now = midnight.addingTimeInterval(-3600)
        let close = Policy.closeRefreshDate(openDays: [Policy.OpenDay(day: "2026-11-11", zone: shanghai)], now: now)
        #expect(close == midnight.addingTimeInterval(Policy.closeAfterMidnight))
        #expect(close != RecordingDayCalendar.end(of: "2026-11-11", in: newYork)?.addingTimeInterval(Policy.closeAfterMidnight))
    }

    @Test func theFirstMidnightAheadWinsAndAPastWakeIsNotAskedAgain() throws {
        let shanghai = try #require(TimeZone(identifier: "Asia/Shanghai"))
        let newYork = try #require(TimeZone(identifier: "America/New_York"))
        let today = Policy.OpenDay(day: "2026-11-11", zone: newYork)
        let earlier = Policy.OpenDay(day: "2026-11-11", zone: shanghai)
        let stale = Policy.OpenDay(day: "2026-11-09", zone: newYork)
        let shanghaiEnd = try #require(RecordingDayCalendar.end(of: "2026-11-11", in: shanghai))
        let newYorkEnd = try #require(RecordingDayCalendar.end(of: "2026-11-11", in: newYork))
        let now = shanghaiEnd.addingTimeInterval(-60)
        // Shanghai's midnight comes 13 hours before New York's: it is the wake asked for.
        #expect(Policy.closeRefreshDate(openDays: [today, earlier, stale], now: now)
                == shanghaiEnd.addingTimeInterval(Policy.closeAfterMidnight))
        // Just past Shanghai's midnight its wake is still ahead; once that wake's time has gone, New York's is next.
        #expect(Policy.closeRefreshDate(openDays: [today, earlier], now: shanghaiEnd.addingTimeInterval(60))
                == shanghaiEnd.addingTimeInterval(Policy.closeAfterMidnight))
        #expect(Policy.closeRefreshDate(openDays: [today, earlier], now: shanghaiEnd.addingTimeInterval(Policy.closeAfterMidnight))
                == newYorkEnd.addingTimeInterval(Policy.closeAfterMidnight))
        // Only days whose wake has passed, or none with a real date: no wake of ours.
        #expect(Policy.closeRefreshDate(openDays: [stale], now: now) == nil)
        #expect(Policy.closeRefreshDate(openDays: [Policy.OpenDay(day: "2026-02-30", zone: newYork)], now: now) == nil)
        #expect(Policy.closeRefreshDate(openDays: [], now: now) == nil)
    }

    private static func calendar(_ zone: TimeZone) -> Calendar {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = zone
        return calendar
    }
}
