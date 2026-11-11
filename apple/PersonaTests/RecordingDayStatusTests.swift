import Foundation
import Testing
@testable import Persona

private let anchor = Date(timeIntervalSince1970: 1_800_000_000) // 2027-01-15T08:00:00Z, 16:00 in Shanghai

/// A day's summary as `RecordingDaySummaries` computes it, with what each test changes.
private func summary(sync: RecordingDaySyncState = .synced, recapState: String? = nil, recorded: Double = 3600,
                     transcribedTo: Date? = anchor.addingTimeInterval(3600), chunks: Int = 360, waiting: Int = 0, skipped: Int = 0,
                     gaps: [RecordingDayGap] = []) -> RecordingDaySummary {
    RecordingDaySummary(id: UUID(), day: "2027-01-15", timeZone: "Asia/Shanghai", firstAt: anchor, lastAt: anchor.addingTimeInterval(3600),
                        recordedSeconds: recorded, transcribedTo: transcribedTo, chunks: chunks, waiting: waiting, skipped: skipped,
                        failed: 0, gaps: gaps, sync: sync, recapState: recapState, hubIDs: [], complete: false)
}

private func watch(_ state: RecordingDayWatchState.State, since: Date? = anchor, pending: Int? = 0) -> RecordingDayWatchState {
    RecordingDayWatchState(recording: UUID(), day: "2027-01-15", state: state, since: since, next: 12, pending: pending)
}

/// A day row of the library: `day` in `zone`, begun at `createdAt`.
private func dayEntry(_ day: String, zone: String, createdAt: Date, id: UUID = UUID()) -> RecordingEntry {
    var entry = RecordingEntry(id: id, createdAt: createdAt, source: .watch)
    entry.kind = .day
    entry.day = day
    entry.timeZone = zone
    return entry
}

/// The All-Day Recording switch, the words of a day's rows and page, and which day is today's (docs/assistant/day-recap.md).
@MainActor struct RecordingDayStatusTests {
    // MARK: The switch

    @Test func theSwitchMirrorsTheWatch() {
        let at = anchor.addingTimeInterval(60)
        #expect(RecordingDayStatus.line(watch: nil, pending: nil, now: at) == .off)
        #expect(!RecordingDayStatus.isOn(watch: nil, pending: nil, now: at), "a watch that never said is off")
        let cases: [(RecordingDayWatchState.State, RecordingDayStatus.Line, Bool)] = [
            (.off, .off, false),
            (.recording, .recording(since: anchor), true),
            (.paused, .paused, true),
            (.interrupted, .interrupted, true),
            (.stopped, .stopped, false),
        ]
        for (state, line, on) in cases {
            #expect(RecordingDayStatus.line(watch: watch(state), pending: nil, now: at) == line, "\(state)")
            #expect(RecordingDayStatus.isOn(watch: watch(state), pending: nil, now: at) == on, "\(state)")
        }
        #expect(RecordingDayStatus.line(watch: watch(.recording, since: nil), pending: nil, now: at) == .recording(since: nil))
    }

    @Test func aStartIsShownOnItsWayUntilTheWatchAnswers() {
        let sent = RecordingDayStatus.Pending(start: true, at: anchor)
        let off = watch(.off)
        #expect(RecordingDayStatus.line(watch: off, pending: sent, now: anchor.addingTimeInterval(5)) == .starting)
        #expect(RecordingDayStatus.isOn(watch: off, pending: sent, now: anchor.addingTimeInterval(5)))
        #expect(RecordingDayStatus.line(watch: off, pending: sent, now: anchor.addingTimeInterval(119)) == .starting)
        // No answer in two minutes: the switch goes back to the watch's state and says so for a while, then lets it go.
        let late = anchor.addingTimeInterval(RecordingDayStatus.answerWait + 1)
        #expect(RecordingDayStatus.line(watch: off, pending: sent, now: late) == .noAnswer)
        #expect(!RecordingDayStatus.isOn(watch: off, pending: sent, now: late))
        let later = late.addingTimeInterval(RecordingDayStatus.noAnswerShown)
        #expect(RecordingDayStatus.line(watch: off, pending: sent, now: later) == .off)
        // The watch started: answered, whenever it comes.
        #expect(RecordingDayStatus.settle(sent, watch: watch(.recording)) == nil)
        #expect(RecordingDayStatus.line(watch: watch(.recording), pending: sent, now: late) == .recording(since: anchor))
        // Paused after a call counts as on: the start was carried out.
        #expect(RecordingDayStatus.settle(sent, watch: watch(.interrupted)) == nil)
    }

    @Test func aStopIsShownOnItsWayUntilTheWatchIsOff() {
        let sent = RecordingDayStatus.Pending(start: false, at: anchor)
        let recording = watch(.recording)
        #expect(RecordingDayStatus.line(watch: recording, pending: sent, now: anchor.addingTimeInterval(1)) == .stopping)
        #expect(!RecordingDayStatus.isOn(watch: recording, pending: sent, now: anchor.addingTimeInterval(1)))
        #expect(RecordingDayStatus.settle(sent, watch: watch(.off)) == nil)
        #expect(RecordingDayStatus.settle(sent, watch: nil) == nil, "a watch that never said is off")
    }

    /// The watch publishes its state again whenever its count of chunks waiting for the phone changes: that is no answer.
    @Test func aStateThatDoesNotMatchLeavesTheCommandWaiting() {
        let stop = RecordingDayStatus.Pending(start: false, at: anchor)
        #expect(RecordingDayStatus.settle(stop, watch: watch(.recording, pending: 3)) == stop)
        #expect(RecordingDayStatus.settle(stop, watch: watch(.paused, pending: 4)) == stop)
        let start = RecordingDayStatus.Pending(start: true, at: anchor)
        #expect(RecordingDayStatus.settle(start, watch: watch(.stopped)) == start)
    }

    @Test func theLineLooksAgainWhenAWaitRunsOut() {
        let sent = RecordingDayStatus.Pending(start: true, at: anchor)
        let off = watch(.off)
        #expect(RecordingDayStatus.nextChange(watch: off, pending: sent, now: anchor) == anchor.addingTimeInterval(RecordingDayStatus.answerWait))
        let noAnswer = anchor.addingTimeInterval(RecordingDayStatus.answerWait + 1)
        #expect(RecordingDayStatus.nextChange(watch: off, pending: sent, now: noAnswer)
                == anchor.addingTimeInterval(RecordingDayStatus.answerWait + RecordingDayStatus.noAnswerShown))
        #expect(RecordingDayStatus.nextChange(watch: off, pending: sent, now: noAnswer.addingTimeInterval(RecordingDayStatus.noAnswerShown)) == nil)
        #expect(RecordingDayStatus.nextChange(watch: watch(.recording), pending: sent, now: anchor) == nil, "answered: nothing to wait for")
    }

    @Test func theSwitchSendsTheCommandAndWaitsForTheWatch() {
        var sent: [String] = []
        let control = RecordingDaySwitch(send: { sent.append($0) }, now: { anchor })
        control.set(true)
        #expect(sent == [RecordingDayContract.Command.start])
        #expect(control.pending == RecordingDayStatus.Pending(start: true, at: anchor))
        control.watchChanged(watch(.off, pending: 2))
        #expect(control.pending != nil, "still off: not answered")
        control.watchChanged(watch(.recording))
        #expect(control.pending == nil)
        control.set(false)
        #expect(sent == [RecordingDayContract.Command.start, RecordingDayContract.Command.stop])
        #expect(control.pending?.start == false)
    }

    /// The Today section's follow: an unanswered start reads "on its way", then "no answer" after two minutes, then the
    /// watch's state ten minutes later; each change is waited for in turn, and the follow ends when none is left.
    @Test func theSwitchLooksAgainAtEveryDeadlineUntilNoneIsLeft() async {
        var clock = anchor
        var looks: [Date] = []
        var waits: [Date] = []
        let control = RecordingDaySwitch(send: { _ in }, now: { clock }, sleep: { until in
            looks.append(clock)
            waits.append(until)
            clock = until
        })
        control.set(true)
        let off = watch(.off)
        await control.follow { off }
        let gaveUp = anchor.addingTimeInterval(RecordingDayStatus.answerWait)
        let letGo = gaveUp.addingTimeInterval(RecordingDayStatus.noAnswerShown)
        #expect(waits == [gaveUp, letGo])
        #expect(looks.map { RecordingDayStatus.line(watch: off, pending: control.pending, now: $0) } == [.starting, .noAnswer])
        #expect(control.lookedAt == letGo)
        #expect(RecordingDayStatus.line(watch: off, pending: control.pending, now: control.lookedAt) == .off,
                "ten minutes on, the line is the watch's again")
        // Entering the section again later starts from the time it is then, with nothing left to wait for.
        clock = letGo.addingTimeInterval(3600)
        await control.follow { off }
        #expect(control.lookedAt == clock)
        #expect(waits.count == 2)
    }

    @Test func theFollowStopsWhenAnsweredOrCancelled() async {
        var waits = 0
        let answered = RecordingDaySwitch(send: { _ in }, now: { anchor }, sleep: { _ in waits += 1 })
        answered.set(false)
        await answered.follow { nil }
        #expect(waits == 0, "a watch that never said is off: the stop is done")
        let cancelled = RecordingDaySwitch(send: { _ in }, now: { anchor }, sleep: { _ in
            waits += 1
            throw CancellationError()
        })
        cancelled.set(true)
        await cancelled.follow { watch(.off) }
        #expect(waits == 1)
        #expect(cancelled.lookedAt == anchor, "a cancelled wait moves nothing on")
    }

    // MARK: A day's word

    @Test func theRecapLeadsOnceTheHubHasOneUnderWay() {
        #expect(RecordingDayStatus.word(summary(recapState: "ready", waiting: 4)) == .recapReady)
        #expect(RecordingDayStatus.word(summary(recapState: "pending")) == .recapWriting)
        #expect(RecordingDayStatus.word(summary(recapState: "failed")) == .recapFailed)
        // An upload that stopped for good is said first: nothing newer reaches the recap.
        #expect(RecordingDayStatus.word(summary(sync: .problem, recapState: "ready")) == .notAccepted)
        // A state a newer hub knows reads as no recap yet.
        #expect(RecordingDayStatus.word(summary(recapState: "queued")) == .synced)
    }

    @Test func withoutARecapTheWordSaysWhereTheTextIs() {
        let pending = [RecordingDayGap(from: 600, to: 720, reason: RecordingDayGap.Reason.pending)]
        #expect(RecordingDayStatus.word(summary(recorded: 0, chunks: 0)) == .noAudio)
        #expect(RecordingDayStatus.word(summary(recorded: 0, chunks: 0, gaps: pending)) == .waitingForWatch)
        #expect(RecordingDayStatus.word(summary(waiting: 3, gaps: pending)) == .waitingToTranscribe)
        #expect(RecordingDayStatus.word(summary(gaps: pending)) == .waitingForWatch)
        #expect(RecordingDayStatus.word(summary(sync: .notOffered)) == .keptOnPhone)
        #expect(RecordingDayStatus.word(summary(sync: .unsupported)) == .keptOnPhone)
        #expect(RecordingDayStatus.word(summary(sync: .expired)) == .pastRetention)
        #expect(RecordingDayStatus.word(summary(sync: .pending)) == .waitingToSync)
        #expect(RecordingDayStatus.word(summary(chunks: 30, skipped: 30)) == .noSpeech)
        #expect(RecordingDayStatus.word(summary()) == .synced)
    }

    @Test func wordsCarryTheirTone() {
        #expect(RecordingDayStatus.Word.recapReady.tone == .positive)
        #expect(RecordingDayStatus.Word.synced.tone == .positive)
        #expect(RecordingDayStatus.Word.recapWriting.tone == .accent)
        #expect(RecordingDayStatus.Word.recapFailed.tone == .danger)
        #expect(RecordingDayStatus.Word.notAccepted.tone == .danger)
        #expect(RecordingDayStatus.Word.keptOnPhone.tone == .secondary)
        #expect(RecordingDayStatus.Word.waitingToTranscribe.tone == .secondary)
    }

    @Test func progressSaysHowFarTheDayIsTranscribed() {
        #expect(RecordingDayStatus.progress(summary(recorded: 0)) == nil)
        #expect(RecordingDayStatus.progress(summary(recorded: 900, transcribedTo: nil)) == .recorded(seconds: 900))
        let to = anchor.addingTimeInterval(600)
        #expect(RecordingDayStatus.progress(summary(recorded: 900, transcribedTo: to)) == .transcribed(seconds: 900, to: to))
        #expect(RecordingDayStatus.span(summary()) == anchor..<anchor.addingTimeInterval(3600))
    }

    @Test func gapsAddUpByReasonInTheirOrder() {
        let gaps = [
            RecordingDayGap(from: 0, to: 60, reason: "paused"),
            RecordingDayGap(from: 100, to: 400, reason: "interrupted"),
            RecordingDayGap(from: 500, to: 530, reason: "paused"),
            RecordingDayGap(from: 600, to: 600, reason: "erased"),
            RecordingDayGap(from: 700, to: 760, reason: "untranscribed"),
            RecordingDayGap(from: 800, to: 810, reason: "a-newer-reason"),
            RecordingDayGap(from: 900, to: 1000, reason: "lost"),
        ]
        #expect(RecordingDayStatus.gapTotals(gaps) == [
            .init(reason: .paused, seconds: 90),
            .init(reason: .interrupted, seconds: 300),
            .init(reason: .lost, seconds: 100),
            .init(reason: .untranscribed, seconds: 60),
            .init(reason: .other, seconds: 10),
        ])
        #expect(RecordingDayStatus.GapReason("pending") == .pending)
        #expect(RecordingDayStatus.GapReason("stopped") == .stopped)
    }

    // MARK: Today

    @Test func todayIsTheWatchsDayElseTheLatestBegun() {
        let shanghai = "Asia/Shanghai" // anchor is 2027-01-15 16:00 there
        let yesterday = dayEntry("2027-01-14", zone: shanghai, createdAt: anchor.addingTimeInterval(-86_400))
        let morning = dayEntry("2027-01-15", zone: shanghai, createdAt: anchor.addingTimeInterval(-6 * 3600))
        let afternoon = dayEntry("2027-01-15", zone: shanghai, createdAt: anchor.addingTimeInterval(-3600))
        let clip = RecordingEntry(id: UUID(), createdAt: anchor, source: .phone)
        let entries = [clip, afternoon, yesterday, morning]
        #expect(RecordingDayStatus.todayID(entries, watching: nil, now: anchor) == afternoon.id)
        #expect(RecordingDayStatus.todayID(entries, watching: morning.id, now: anchor) == morning.id)
        #expect(RecordingDayStatus.todayID(entries, watching: yesterday.id, now: anchor) == afternoon.id, "the watch's day isn't today's")
        #expect(RecordingDayStatus.todayID([clip, yesterday], watching: nil, now: anchor) == nil)
    }

    /// Each day is today in its own zone, across a DST change: 2026-11-01 in New York has 25 hours, 2026-03-08 has 23.
    @Test func todayFollowsTheDaysOwnZoneAcrossDST() {
        let newYork = "America/New_York"
        let fallBack = dayEntry("2026-11-01", zone: newYork, createdAt: Date(timeIntervalSince1970: 1_793_538_000)) // 2026-11-01 08:00 EST
        // 23:30 EST on Nov 1 (04:30 UTC on Nov 2): still that day there.
        #expect(RecordingDayStatus.todayID([fallBack], watching: nil, now: Date(timeIntervalSince1970: 1_793_593_800)) == fallBack.id)
        // 00:00 EST on Nov 2: the next day.
        #expect(RecordingDayStatus.todayID([fallBack], watching: nil, now: Date(timeIntervalSince1970: 1_793_595_600)) == nil)
        let springForward = dayEntry("2026-03-08", zone: newYork, createdAt: Date(timeIntervalSince1970: 1_772_971_200)) // 08:00 EDT
        // 23:59 EDT on Mar 8 (03:59 UTC on Mar 9).
        #expect(RecordingDayStatus.todayID([springForward], watching: nil, now: Date(timeIntervalSince1970: 1_773_028_740)) == springForward.id)
        #expect(RecordingDayStatus.isToday(day: "2026-03-08", timeZone: newYork, now: Date(timeIntervalSince1970: 1_773_028_740)))
        #expect(!RecordingDayStatus.isToday(day: "2026-03-08", timeZone: newYork, now: Date(timeIntervalSince1970: 1_773_028_800)))
    }

    /// The day's name is the library's for an untitled day (one test of the localized key, `Day · %@`).
    @Test func aDaysTitleIsItsDateInItsZone() {
        let zone = TimeZone(identifier: "Asia/Shanghai")!
        let start = RecordingDayCalendar.start(of: "2027-01-15", in: zone)!
        let date = start.formatted(Date.FormatStyle(date: .abbreviated, time: .omitted, timeZone: zone))
        #expect(RecordingDayStatus.title(day: "2027-01-15", timeZone: "Asia/Shanghai") == String(localized: "Day · \(date)"))
        var entry = dayEntry("2027-01-15", zone: "Asia/Shanghai", createdAt: anchor)
        entry.title = ""
        #expect(RecordingDayStatus.title(day: "2027-01-15", timeZone: "Asia/Shanghai") == entry.displayTitle)
        #expect(RecordingDayStatus.title(day: "not-a-day", timeZone: "Asia/Shanghai") == "not-a-day")
    }
}
