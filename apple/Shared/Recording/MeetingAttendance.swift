import Foundation

/// Persona's part in a meeting being recorded (#2981; the contract is docs/assistant/meeting-minutes.md「Attending a
/// meeting」, the phone's side docs/apple/recording.md「Persona in meetings」): off, listening in, or taking part. Chosen
/// each time a meeting recording starts, pre-selected from the hub's `meeting_attendance_default`. Offered only by a hub
/// that lists the `meeting_attendance` feature; only a phone's meeting recording has one (notes and watch recordings never).
nonisolated enum MeetingAttendance: String, Codable, CaseIterable, Sendable {
    /// Today's behaviour: the hub writes minutes after the meeting, and nothing reaches it while the meeting goes on.
    case off
    /// Persona follows the meeting and answers on screen only. It never speaks.
    case listener
    /// Persona also says its answers out loud, through a connected headset or the loudspeaker.
    case participant

    var attends: Bool { self != .off }
    var speaks: Bool { self == .participant }

    /// The segmented choice's word.
    var title: String {
        switch self {
        case .off: String(localized: "Off")
        case .listener: String(localized: "Listen [Meeting Role]")
        case .participant: String(localized: "Take Part [Meeting Role]")
        }
    }

    /// One plain sentence on what the choice does.
    var detail: String {
        switch self {
        case .off: String(localized: "Persona stays out of the meeting. The minutes come after it ends.")
        case .listener: String(localized: "Persona follows the meeting. Ask it from this phone; it answers on screen and never speaks.")
        case .participant: String(localized: "Persona follows the meeting and answers out loud when you tap Ask Persona.")
        }
    }
}

/// The attendance of one meeting recording, kept with the recording on this phone: the role, the session that attends,
/// the phone's live log of what it sent the hub, and the stretches Persona spoke in.
nonisolated struct RecordingAttendance: Codable, Hashable, Sendable {
    var role: MeetingAttendance
    /// The attending session's `SessionKey.path`; nil is the main chat (the upload then leaves `attending_session` out).
    var session: String?
    /// Its name when it was chosen.
    var sessionTitle: String?
    /// The stretches of the recording's clock the role was listener or participant: only a sentence that ends inside
    /// one enters the live log (the contract: what is said while attending is off is never sent). The last one is open
    /// (`end` nil) while attending.
    var periods: [RecordingPeriod] = []
    /// The phone's append-only live log (`POST /api/v1/recordings/{id}/live`): a sentence's position is its index here,
    /// never renumbered. A re-transcription or a dropped echo line changes nothing already in it.
    var live: [RecordingSentence] = []
    /// The segments whose sentences were already looked at for the log, so a segment transcribed again adds nothing.
    var liveSegments: [Int] = []
    /// Where on the recording Persona said an answer out loud: the microphone may have heard it, so a sentence at least
    /// half inside one is Persona's, not the meeting's (left out of the transcript, the upload and the live log).
    var spoken: [RecordingSpan] = []
    /// Why the phone stopped attending on its own, said on the recording: the hub can't follow meetings (an older
    /// manager), or it takes no more live lines for this one.
    var stopped: StopReason?

    nonisolated enum StopReason: String, Codable, Sendable {
        case unsupported
        case liveFull = "live_full"
    }

    init(role: MeetingAttendance, session: String? = nil, sessionTitle: String? = nil, at: Double = 0) {
        self.role = .off; self.session = session; self.sessionTitle = sessionTitle
        set(role, at: at)
    }

    enum CodingKeys: String, CodingKey { case role, session, sessionTitle, periods, live, liveSegments, spoken, stopped }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        // A role a later app wrote and this one doesn't know reads as off: nothing attends.
        role = (try? c.decode(MeetingAttendance.self, forKey: .role)) ?? .off
        session = try c.decodeIfPresent(String.self, forKey: .session)
        sessionTitle = try c.decodeIfPresent(String.self, forKey: .sessionTitle)
        periods = try c.decodeIfPresent([RecordingPeriod].self, forKey: .periods) ?? []
        live = try c.decodeIfPresent([RecordingSentence].self, forKey: .live) ?? []
        liveSegments = try c.decodeIfPresent([Int].self, forKey: .liveSegments) ?? []
        spoken = try c.decodeIfPresent([RecordingSpan].self, forKey: .spoken) ?? []
        stopped = try? c.decodeIfPresent(StopReason.self, forKey: .stopped)
    }

    /// The attending session, nil for the main chat.
    var key: SessionKey? { session.flatMap(SessionKey.init(path:)) }

    /// Sets the role at `at` (seconds on the recording's clock), opening or closing the attending period.
    mutating func set(_ next: MeetingAttendance, at: Double) {
        let open = periods.last.map { $0.end == nil } ?? false
        if next.attends, !open { periods.append(RecordingPeriod(start: max(0, at))) }
        if !next.attends, open, let last = periods.indices.last { periods[last].end = max(at, periods[last].start) }
        role = next
        if next.attends { stopped = nil }
    }

    /// The sentence ended while Persona attended.
    func attended(_ line: RecordingSentence) -> Bool {
        periods.contains { $0.holds(line.end) }
    }

    /// Persona's own voice: at least half of the line is inside a stretch it spoke in (`open`: one still going).
    func spokenBy(_ line: RecordingSentence, open: RecordingSpan? = nil) -> Bool {
        spoken.contains { $0.holds(line) } || (open?.holds(line) ?? false)
    }
}

/// A stretch of a recording, in seconds on its clock (`RecordingSentence.start`).
nonisolated struct RecordingSpan: Codable, Hashable, Sendable {
    var start: Double
    var end: Double

    /// At least half of the line was said inside this stretch (the contract's rule for Persona's own voice).
    func holds(_ line: RecordingSentence) -> Bool {
        let length = line.end - line.start
        guard length > 0 else { return line.start >= start && line.start <= end }
        let inside = min(line.end, end) - max(line.start, start)
        return inside / length >= 0.5
    }
}

/// A stretch with an open end: attending since `start`, until `end` (nil while it goes on).
nonisolated struct RecordingPeriod: Codable, Hashable, Sendable {
    var start: Double
    var end: Double?

    func holds(_ time: Double) -> Bool { time >= start && end.map { time <= $0 } ?? true }
}

/// The hub's side of attending (docs/assistant/meeting-minutes.md「Attendance fields」).
nonisolated enum MeetingAttendanceDefaults {
    /// Listed in `hello.features` by a hub whose manager takes everything about attending: only then the phone offers a role.
    static let feature = "meeting_attendance"
    /// The last the phone heard from the hub, for a start without the app in front (Siri, the Control, the Action
    /// button): whether it offers attending, and its default role. Rarely changes, so a user default.
    static let offeredKey = "recording.meeting.offered"
    static let roleKey = "recording.meeting.defaultRole"
    /// The session picked last (`SessionKey.path`, `main` for the main chat): the next meeting starts with it again while
    /// it can still attend.
    static let sessionKey = "recording.meeting.attendee"
    /// Its name then, for a start before the hub has listed it.
    static let sessionTitleKey = "recording.meeting.attendeeTitle"
    static let main = "main"

    /// The role a meeting starts with when nothing is picked: the hub's default while it offers attending, else off.
    static func cachedRole(_ store: UserDefaults = .standard) -> MeetingAttendance {
        guard store.bool(forKey: offeredKey) else { return .off }
        return store.string(forKey: roleKey).flatMap(MeetingAttendance.init(rawValue:)) ?? .off
    }

    /// Keeps what the hub said: whether it lists the feature, and `meeting_attendance_default` when known.
    static func remember(offered: Bool, role: MeetingAttendance?, _ store: UserDefaults = .standard) {
        if store.object(forKey: offeredKey) == nil || store.bool(forKey: offeredKey) != offered { store.set(offered, forKey: offeredKey) }
        if let role, store.string(forKey: roleKey) != role.rawValue { store.set(role.rawValue, forKey: roleKey) }
    }
}
