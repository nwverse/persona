import Foundation

/// The watch's all-day notifications: categories, actions and request ids. Top level and `nonisolated`, so the
/// notification delegate's nonisolated methods can read them.
nonisolated enum AllDayNotice {
    /// Paused or stopped: its Continue action brings the app to the front, where the microphone can open again.
    static let pausedCategory = "allday.paused"
    static let continueAction = "allday.continue"
    /// The phone's switch asked to start while the watch app was not in front.
    static let startCategory = "allday.start"
    static let startAction = "allday.start"
    /// The phone deleted the recording being made: nothing to continue, so no action; a tap opens the page.
    static let deletedCategory = "allday.deleted"
    /// One request per kind, so a newer notice replaces the older one instead of piling up.
    static let pausedRequest = "allday.paused"
    /// Also the dead-man's switch: armed while recording, it fires only if the app stops refreshing it (it died).
    static let stoppedRequest = "allday.stopped"
    static let startRequest = "allday.start"
    static let deletedRequest = "allday.deleted"

    static let categories: Set<String> = [pausedCategory, startCategory, deletedCategory]
    static let requests = [pausedRequest, stoppedRequest, startRequest, deletedRequest]

    /// What answering one of our notices does (`identifier`: the response's action). Its Continue or Start button does
    /// just that; a tap on the notice itself opens the page, where the same button is one tap away, so the microphone
    /// never opens without a tap that says so.
    static func link(answering identifier: String) -> AllDayLink.Action {
        switch identifier {
        case continueAction: .resume
        case startAction: .start
        default: .open
        }
    }

    /// Whether the answer carries out the phone's own request: only the phone's start notice has a Start button, and
    /// the phone asks only once its user agreed to the consent sheet (`AllDayAlertPolicy.mayStart`).
    static func askedByPhone(answering identifier: String) -> Bool { identifier == startAction }
}

/// What the watch does when all-day recording changes (docs/assistant/day-recap.md「Retention and privacy」): whether
/// it is offered at all, the wrist tap, the notification, the complication's state and the dead-man's switch. Pure, so
/// the decisions are tested on the phone; `WatchAllDay` carries them out.
///
/// A system haptic plays only while the app is in front (watchOS ignores it otherwise), so the notification is the
/// real alert. The dead-man's switch is a notification scheduled `deadManSeconds` ahead and renewed by every saved
/// chunk once less than `deadManRenewBelow` is left: if the app dies, nothing renews it and it fires.
nonisolated enum AllDayAlertPolicy {
    static let deadManSeconds: TimeInterval = 600
    static let deadManRenewBelow: TimeInterval = 300
    /// While recording, the complication's snapshot is rewritten (and its timeline reloaded) at most this often, so it
    /// never reaches `AllDaySnapshot.staleAfter` while chunks keep coming, and the reloads stay few.
    static let heartbeatEvery: TimeInterval = 600
    /// A day recording left open while nothing records is closed by a background refresh this long after its midnight.
    static let closeAfterMidnight: TimeInterval = 5 * 60

    // MARK: - Whether it is offered

    /// What the watch offers, from the phone's `recordingDay` (docs/assistant/day-recap.md「Watch and phone
    /// capabilities」). The consent sheet is the iPhone's: the watch never asks, it only points there.
    nonisolated enum Access: Sendable, Equatable {
        /// Nothing offers All-Day Recording: the phone doesn't take day recordings (a phone from before, or one that
        /// hasn't said yet).
        case hidden
        /// Offered, but a start waits for the user to agree on the iPhone: the watch explains it and points there.
        case needsConsent
        /// One tap starts or continues it.
        case ready
    }

    /// `phoneDay`: the stored `recordingDay` (nil when the phone never set it, 0 before its user agreed, else the
    /// contract version, where a higher one than this watch knows still means it can).
    static func access(phoneDay: Int?, state: AllDaySnapshot.State) -> Access {
        switch state {
        case .recording, .paused, .interrupted:
            // A recording already running goes on under the agreement it started with: its controls stay whatever the
            // phone says now, and a new consent takes effect at the next start.
            return .ready
        case .off, .stopped:
            guard let phoneDay else {
                // A wish to record from before (it stopped by itself) still shows, so Stop for Today can end it.
                return state == .stopped ? .needsConsent : .hidden
            }
            return phoneDay > 0 ? .ready : .needsConsent
        }
    }

    /// Whether a start may open the microphone now. The phone's own start request (its switch, or the Start button of
    /// its notice) stands for its consent, which its application context may not have brought yet: the two travel
    /// separately, and a context can arrive after a message sent later.
    static func mayStart(_ access: Access, askedByPhone: Bool) -> Bool { askedByPhone || access == .ready }

    // MARK: - What a change does

    nonisolated enum Event: Sendable, Equatable {
        case started
        case resumed
        case pausedByUser
        case stoppedByUser
        /// The capture stopped or paused by itself, and why.
        case halted(RecordingDayHalt)
        /// A chunk was saved. `deadManLeft`: seconds until the armed dead-man's switch fires (nil: none armed);
        /// `sinceHeartbeat`: seconds since the snapshot was last written (nil: never); `recording`: the capture still
        /// records (a chunk cut by a pause or a halt arrives after it, and must not undo it).
        case chunkSaved(deadManLeft: TimeInterval?, sinceHeartbeat: TimeInterval?, recording: Bool)
        /// The phone's switch was turned on.
        case phoneAskedToStart
        /// The phone deleted the day recording being made (docs/assistant/day-recap.md「Deleting」): the capture
        /// stopped, and All-Day Recording is off until the user starts it again, in a new recording.
        case deletedOnPhone
    }

    nonisolated enum Haptic: Sendable, Equatable { case start, stop, failure, notification }

    nonisolated enum Notice: Sendable, Equatable {
        case paused(RecordingDayHalt)
        case stopped(RecordingDayHalt)
        case start
        case deleted

        var request: String {
            switch self {
            case .paused: AllDayNotice.pausedRequest
            case .stopped: AllDayNotice.stoppedRequest
            case .start: AllDayNotice.startRequest
            case .deleted: AllDayNotice.deletedRequest
            }
        }

        /// Paused and stopped share the Continue action; the start request has its own Start; a deletion has none.
        var category: String {
            switch self {
            case .paused, .stopped: AllDayNotice.pausedCategory
            case .start: AllDayNotice.startCategory
            case .deleted: AllDayNotice.deletedCategory
            }
        }
    }

    nonisolated enum DeadMan: Sendable, Equatable { case arm, disarm, keep }

    nonisolated struct Output: Sendable, Equatable {
        var haptic: Haptic? = nil
        var notify: Notice? = nil
        /// Takes back the delivered all-day notices (they are answered) before anything new is posted.
        var withdraw = false
        /// The complication's new state (written with the time now); nil leaves the snapshot as it is.
        var snapshot: AllDaySnapshot.State? = nil
        var deadMan: DeadMan = .keep
        /// The app may be suspended next: chunks waiting for the phone go by the system's own transfers now.
        var handOff = false
        /// The phone asked while the app is in front: start as a tap would.
        var startsNow = false
    }

    static func decide(_ event: Event, appActive: Bool) -> Output {
        let haptic: (Haptic) -> Haptic? = { appActive ? $0 : nil }
        switch event {
        case .started, .resumed:
            return Output(haptic: haptic(.start), withdraw: true, snapshot: .recording, deadMan: .arm)
        case .pausedByUser:
            return Output(haptic: haptic(.stop), withdraw: true, snapshot: .paused, deadMan: .disarm, handOff: true)
        case .stoppedByUser:
            return Output(haptic: haptic(.stop), withdraw: true, snapshot: .off, deadMan: .disarm, handOff: true)
        case .halted(let why):
            // The notice says it now: once the session is interrupted the app is suspended, and nothing later is sure
            // to run. A stopped notice takes the dead-man's request id, so it also replaces the pending one.
            let state = state(after: why)
            return Output(haptic: haptic(.failure), notify: state == .stopped ? .stopped(why) : .paused(why),
                          withdraw: true, snapshot: state, deadMan: .disarm, handOff: true)
        case .chunkSaved(let left, let sinceHeartbeat, let recording):
            guard recording else { return Output() }
            let renew = left.map { $0 < deadManRenewBelow } ?? true
            let beat = sinceHeartbeat.map { $0 >= heartbeatEvery } ?? true
            return Output(snapshot: beat ? .recording : nil, deadMan: renew ? .arm : .keep)
        case .phoneAskedToStart:
            // The microphone opens only with the app in front: in front, go ahead; otherwise a notification whose tap
            // brings it there.
            return appActive ? Output(haptic: .notification, startsNow: true) : Output(notify: .start)
        case .deletedOnPhone:
            // The watch says so itself: the delete came from elsewhere, so the wrist may not know the microphone went
            // off. What the paused notices offered is moot, and the other days' waiting chunks go before a suspension.
            return Output(haptic: haptic(.stop), notify: .deleted, withdraw: true, snapshot: .off, deadMan: .disarm,
                          handOff: true)
        }
    }

    /// Where a halt leaves the mode: paused (the capture still holds the day and continues on a tap) or stopped (the
    /// capture ended; a tap starts it again on the same day).
    static func state(after halt: RecordingDayHalt) -> AllDaySnapshot.State {
        switch halt {
        case .interrupted, .resumeFailed, .microphoneChanged: .interrupted
        case .audioReset, .lowBattery, .lowStorage, .writeFailed, .crashed: .stopped
        }
    }

    /// A day found open at launch (the app ended while the user wanted it on) alerts only when it was recording then,
    /// and the dead-man's switch or an earlier notice hasn't already said so: after a pause or an interruption the
    /// user already knows, and a notice hours later would only repeat it. `gapReason`: the reason of the day's last
    /// `paused` event since it last started (nil: it was recording).
    static func alertsOnLaunch(gapReason: String?, alreadyAlerted: Bool) -> Bool {
        guard !alreadyAlerted else { return false }
        return gapReason == nil || gapReason == RecordingDayContract.Reason.stopped
    }

    // MARK: - Midnight

    /// A day recording still open: its local date and the zone it is fixed to (docs/assistant/day-recap.md「The day
    /// recording on the watch」).
    nonisolated struct OpenDay: Sendable, Equatable {
        var day: String
        var zone: TimeZone
    }

    /// When to wake in the background to close the day recordings left open while nothing records: just after the
    /// first of their midnights still ahead, each in the recording's own zone (moving to another zone never moves it; a
    /// day of 23 or 25 hours ends where it does). A wake whose time has passed is not asked for again: that day closes
    /// at whatever wakes or opens the app next (`WatchAllDay.closeStaleDays`), and asking again would only wake the
    /// watch over and over. nil when nothing is ahead.
    static func closeRefreshDate(openDays: [OpenDay], now: Date) -> Date? {
        openDays.compactMap { RecordingDayCalendar.end(of: $0.day, in: $0.zone)?.addingTimeInterval(closeAfterMidnight) }
            .filter { $0 > now }
            .min()
    }
}
