import Foundation

/// Segments describe themselves, so the watch's uploads can arrive out of order. The format comes from Hourhand's WatchProtocol.
nonisolated struct RecordingSegment: Codable, Hashable, Sendable {
    var index: Int
    var start: Double
    var duration: Double
    var sentences: [RecordingSentence]? = nil
    var delivered = false
    /// Wall-clock start of the segment (rolling recordings); nil for old clips.
    var startedAt: Date? = nil
    var kept: RecordingKeep? = nil
    // All-day recordings only (docs/assistant/day-recap.md); nil everywhere else, so old JSON decodes unchanged.
    /// The chunk's mean RMS level in dBFS over all its samples (`RecordingDayLevel`), for skipping silence.
    var level: Double? = nil
    /// Why audio is missing just before this chunk (`RecordingDayContract.Reason`); nil when it follows the previous one.
    var after: String? = nil
    /// Erased by the user: the record keeps its seq and times, its audio and text are gone, and it travels as a marker.
    var erased: Bool? = nil
    /// The last chunk of the day recording, or the header-only end marker (`RecordingLibrary.closeDay`).
    var end: Bool? = nil
    /// The watch could not keep this chunk's audio; it travels as a `lost` marker so the phone stops waiting for it.
    var lost: Bool? = nil

    static func fileName(_ index: Int) -> String { String(format: "seg-%03d.m4a", index) }
    var endedAt: Date? { startedAt?.addingTimeInterval(duration) }
    /// Wall-clock time of a point on the recording timeline that falls in this segment.
    func wallTime(_ timeline: Double) -> Date? { startedAt?.addingTimeInterval(timeline - start) }
    func overlaps(_ from: Date, _ to: Date) -> Bool {
        guard let startedAt, let endedAt else { return false }
        return startedAt <= to && from <= endedAt
    }

    /// A day record that carries no audio over the wire: erased, lost, or the zero-length end marker
    /// (`RecordingDayHeader.isMarker`).
    var isDayMarker: Bool { erased == true || lost == true || duration <= 0 }

    /// How far before an erased range's end the chunk the erase cut opened may start. The erase cuts the open chunk and
    /// then names the range up to "now"; the next chunk starts with the samples still in flight at the cut (one tap
    /// buffer, a fraction of a second), so it starts a moment before the range's end although it holds what came after.
    static let dayEraseCutAllowance: Double = 0.5
    /// How far past the range's end a chunk that ended at the cut may reach: its end is counted in samples from a
    /// host-clock start, a few milliseconds off the wall clock that names the range.
    static let dayEraseEndTolerance: Double = 0.1

    /// Whether this day chunk falls in an erased range: it overlaps it (half-open: one that ends right where the range
    /// starts, or starts right where it ends, doesn't), unless it is the chunk the erase cut opened, which starts within
    /// `dayEraseCutAllowance` of the range's end and goes on past it. A chunk the cut closed ends at the cut, so it is
    /// erased however short it is. Header-only records hold no audio and never fall in one. Rolling recordings use
    /// `overlaps`, inclusive at both ends.
    func fallsInDayErase(_ range: RecordingErase) -> Bool {
        guard let startedAt, let endedAt, duration > 0, lost != true else { return false }
        guard startedAt < range.to, endedAt > range.from else { return false }
        let openedByTheCut = startedAt >= range.to.addingTimeInterval(-Self.dayEraseCutAllowance)
            && endedAt > range.to.addingTimeInterval(Self.dayEraseEndTolerance)
        return !openedByTheCut
    }
}

/// Why a rolling segment is kept past the 6 hour window.
nonisolated struct RecordingKeep: Codable, Hashable, Sendable {
    nonisolated enum Reason: String, Codable, Sendable { case heartRate, hrv, mark, mood }
    var reason: Reason
    var at: Date
    var title: String {
        switch reason {
        case .heartRate: String(localized: "Elevated heart rate")
        case .hrv: String(localized: "Low HRV")
        case .mark: String(localized: "Bookmark")
        case .mood: String(localized: "Logged mood")
        }
    }
}

/// A wall-clock range the user erased; segments overlapping it never come back.
nonisolated struct RecordingErase: Codable, Hashable, Sendable {
    var from: Date
    var to: Date
    var valid: Bool { from.timeIntervalSince1970.isFinite && to.timeIntervalSince1970.isFinite && from <= to }
}

nonisolated struct RecordingSentence: Codable, Hashable, Sendable {
    var start: Double
    var end: Double
    var text: String
}

/// What makes a recording a meeting's (docs/apple/recording.md「Meeting recordings」): started as one, and the phone's calendar
/// event it happened during, when there was one. The event's own fields are a copy from the time it started; nothing here
/// follows later calendar edits.
nonisolated struct RecordingMeeting: Codable, Hashable, Sendable {
    /// The calendar event (`EKEvent.calendarItemExternalIdentifier`, the same on every device), nil without one.
    var eventID: String?
    var title: String?
    var start: Date?
    var end: Date?
    var location: String?
    /// Who was invited, by display name (the organizer first); empty without an event or attendees.
    var attendees: [String] = []

    init(eventID: String? = nil, title: String? = nil, start: Date? = nil, end: Date? = nil, location: String? = nil, attendees: [String] = []) {
        self.eventID = eventID; self.title = title; self.start = start; self.end = end; self.location = location; self.attendees = attendees
    }

    enum CodingKeys: String, CodingKey { case eventID, title, start, end, location, attendees }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        eventID = try c.decodeIfPresent(String.self, forKey: .eventID)
        title = try c.decodeIfPresent(String.self, forKey: .title)
        start = try c.decodeIfPresent(Date.self, forKey: .start)
        end = try c.decodeIfPresent(Date.self, forKey: .end)
        location = try c.decodeIfPresent(String.self, forKey: .location)
        attendees = try c.decodeIfPresent([String].self, forKey: .attendees) ?? []
    }
}

nonisolated struct RecordingEntry: Codable, Identifiable, Hashable, Sendable {
    nonisolated enum Source: String, Codable, Sendable { case phone, watch }
    var id: UUID
    var createdAt: Date
    var source: Source
    var title = ""
    var locale = "zh-CN"
    var active = true
    var expectedSegments: Int? = nil
    var segments: [RecordingSegment] = []
    var marks: [Double] = []
    var endDelivered = false
    /// `day`: an all-day recording, one per local day (docs/assistant/day-recap.md). An app from before the case can't
    /// decode such an entry, so the watch records them only for a phone that says it takes them (`recordingDay`).
    nonisolated enum Kind: String, Codable, Sendable { case clip, rolling, day }
    /// nil means a clip.
    var kind: Kind? = nil
    /// Tombstone: every segment below this index was pruned and must not come back.
    var prunedBelow: Int? = nil
    /// Pruned indexes above a gap (a segment that never arrived), so the gap itself is not tombstoned.
    var prunedAbove: [Int]? = nil
    var erased: [RecordingErase]? = nil
    /// When this device learned the recording stopped (rolling); an empty entry is removed a while after it.
    var endedAt: Date? = nil
    /// Last local change: the knowledge-base revision (ms) the phone uploads it as (docs/apple/recording.md「Synchronization」).
    var updatedAt: Date? = nil
    /// Set when it was started as a meeting recording (or a watch recording arrived during a calendar event); nil for a note.
    var meeting: RecordingMeeting? = nil
    /// Persona's part in the meeting and the session that attends (#2981); nil for a note, a watch recording, and a
    /// meeting recording Persona was never asked to attend (its upload is then exactly as before).
    var attendance: RecordingAttendance? = nil
    // All-day recordings only; nil for every other kind.
    /// The local date the day recording belongs to, `YYYY-MM-DD` in `timeZone` (`RecordingDayCalendar`).
    var day: String? = nil
    /// The time zone `day` is a date in (an identifier such as `Asia/Shanghai`), so the date reads the same anywhere.
    var timeZone: String? = nil
    /// Watch: why the microphone is off while the day stays open (`RecordingDayContract.Reason`); the next chunk carries
    /// it as `after`. nil while recording.
    var gapReason: String? = nil
    /// Watch: bookmarks and meeting marks on the wall clock, kept until the chunk that carries them is acknowledged.
    var dayMarks: [RecordingDayMark]? = nil
    /// Watch: the end marker's time (midnight, or when a day left open was closed).
    var dayEndAt: Date? = nil

    /// Monotonic per recording: every save moves `updatedAt` forward, so a later upload always carries a larger one.
    var revision: Int64 { Int64(((updatedAt ?? createdAt).timeIntervalSince1970 * 1000).rounded()) }

    var isRolling: Bool { kind == .rolling }
    var isDay: Bool { kind == .day }
    /// Whether a day chunk falls in one of the ranges the user erased (`RecordingSegment.fallsInDayErase`).
    func isDayErased(_ segment: RecordingSegment) -> Bool {
        (erased ?? []).contains { segment.fallsInDayErase($0) }
    }
    /// Index the next segment would get; also the segment count to report when a rolling recording ends.
    var nextIndex: Int { max((segments.map(\.index).max() ?? -1) + 1, prunedBelow ?? 0, (prunedAbove?.max() ?? -1) + 1) }
    func isPruned(_ index: Int) -> Bool { index < (prunedBelow ?? 0) || (prunedAbove?.contains(index) ?? false) }
    var prunedCount: Int { (prunedBelow ?? 0) + (prunedAbove?.count ?? 0) }
    func isErased(_ segment: RecordingSegment) -> Bool {
        (erased ?? []).contains { segment.overlaps($0.from, $0.to) }
    }
    /// Segments with a wall-clock time that overlap from...to, sorted by index.
    func segments(from: Date, to: Date) -> [RecordingSegment] {
        self.segments.filter { $0.overlaps(from, to) }.sorted { $0.index < $1.index }
    }
    /// Bookmarks as wall-clock times; marks outside every segment are skipped.
    var markTimes: [Date] {
        marks.compactMap { mark in
            segments.first { mark >= $0.start && mark < $0.start + $0.duration }?.wallTime(mark)
        }
    }

    var duration: Double { segments.map { $0.start + $0.duration }.max() ?? 0 }
    var isMeeting: Bool { meeting != nil }
    /// A meeting being recorded on this phone that Persona attends: asking is open, live lines go to the hub.
    var attended: Bool {
        active && isMeeting && source == .phone && attendance?.role.attends == true && attendance?.stopped != .unsupported
    }
    var displayTitle: String {
        guard title.isEmpty else { return title }
        if isDay, let date = dayTitleDate { return String(localized: "Day · \(date)") }
        let when = createdAt.formatted(date: .abbreviated, time: .shortened)
        if isRolling { return String(localized: "Rolling · \(when)") }
        return isMeeting ? String(localized: "Meeting · \(when)") : String(localized: "Recording · \(when)")
    }
    /// A day recording's date as the title shows it: written in the recording's own time zone, so a day recorded in
    /// Shanghai reads as that date on a phone set to New York too. nil without a valid `day`.
    private var dayTitleDate: String? {
        let zone = timeZone.flatMap(TimeZone.init(identifier:)) ?? .autoupdatingCurrent
        guard let day, let start = RecordingDayCalendar.start(of: day, in: zone) else { return nil }
        return start.formatted(Date.FormatStyle(date: .abbreviated, time: .omitted, timeZone: zone))
    }
    var sourceTitle: String { source == .watch ? "Apple Watch" : String(localized: "iPhone") }
    var receivedAll: Bool {
        guard !active, let count = expectedSegments, count > 0 else { return false }
        return Set(segments.map(\.index)) == Set(0..<count)
    }
    var transcribedAll: Bool { receivedAll && segments.allSatisfy { $0.sentences != nil } }
    /// The transcript, in order. Lines the microphone took from Persona's own answers out loud (#2981) are not the
    /// meeting's and are left out (`RecordingAttendance.spoken`).
    var sentences: [RecordingSentence] {
        let all = segments.sorted { $0.index < $1.index }.flatMap { $0.sentences ?? [] }
        guard let attendance, !attendance.spoken.isEmpty else { return all }
        return all.filter { !attendance.spokenBy($0) }
    }
    var transcript: String { sentences.map { "[\(RecordingFormat.clock($0.start))] \($0.text)" }.joined(separator: "\n") }
}

nonisolated struct RecordingTransfer: Codable, Sendable {
    var id: UUID
    var createdAt: Date
    var locale: String
    var segment: RecordingSegment?
    var total: Int?
    var marks: [Double]
    /// Started as a meeting recording on the watch (docs/apple/recording.md「Apple Watch」); nil from a note, and from a watch
    /// with an older app.
    var meeting: Bool?
    var kind: RecordingEntry.Kind? = nil
    var erased: [RecordingErase]? = nil

    static let key = "personaRecording"
    var dictionary: [String: Any] {
        let data = (try? JSONEncoder().encode(self)) ?? Data()
        return [Self.key: data, LegacyNames.recordingKey: data]
    }
    init(entry: RecordingEntry, segment: RecordingSegment? = nil) {
        id = entry.id; createdAt = entry.createdAt; locale = entry.locale
        self.segment = segment; total = segment == nil ? entry.expectedSegments : nil; marks = entry.marks
        meeting = entry.isMeeting ? true : nil
        kind = entry.kind; erased = entry.erased
    }
    init?(dictionary: [String: Any]?) {
        guard let data = (dictionary?[Self.key] ?? dictionary?[LegacyNames.recordingKey]) as? Data,
              let value = try? JSONDecoder().decode(Self.self, from: data), value.valid else { return nil }
        self = value
    }
    var valid: Bool {
        guard marks.allSatisfy({ $0.isFinite && $0 >= 0 }), marks.count <= 10_000,
              locale.count <= 100, createdAt.timeIntervalSince1970.isFinite else { return false }
        guard (erased?.count ?? 0) <= 1000, erased?.allSatisfy(\.valid) ?? true else { return false }
        if let segment {
            return segment.index >= 0 && segment.index < 100_000 && segment.start.isFinite && segment.start >= 0
                && segment.duration.isFinite && segment.duration > 0 && segment.duration <= 600 && total == nil
                && (segment.startedAt?.timeIntervalSince1970.isFinite ?? true)
        }
        return total.map { $0 >= 0 && $0 <= 100_000 } ?? false
    }
}

/// What every way of starting a recording on a phone shares (docs/apple/recording.md).
nonisolated enum RecordingDefaults {
    /// The language a recording starts in: the Recordings page's picker remembers it, Siri and the Control follow it.
    static let localeKey = "recording.locale"
    /// Chinese until chosen (the on-device Chinese model also writes the English words said among it).
    static let locale = "zh-CN"
}

nonisolated enum RecordingAudioSpec {
    static let sampleRate: Double = 16_000
    static let bitRate = 32_000
    static let segmentSeconds: Double = 60
    static let liveSegmentSeconds: Double = 30
    static let transferBatchSeconds: Double = 180
    static let lowPowerTransferBatchSeconds: Double = 300
}

nonisolated enum RecordingFormat {
    static func clock(_ seconds: Double) -> String {
        guard seconds.isFinite, seconds >= 0, seconds < Double(Int.max) else { return "0:00" }
        let value = Int(seconds)
        return value >= 3600 ? String(format: "%d:%02d:%02d", value / 3600, value / 60 % 60, value % 60)
            : String(format: "%d:%02d", value / 60, value % 60)
    }
}
