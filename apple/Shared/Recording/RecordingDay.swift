import Foundation

// All-day recording between the watch and the phone (docs/assistant/day-recap.md): the contract's constants and keys,
// the chunk header and its framing, acknowledgements, the watch's state for the phone, the local-day calendar, the
// level the phone skips silence by, and the cached hub flag. Pure Foundation and `nonisolated`, so the watch, the
// phone's WatchConnectivity queue, its store queue and the tests all use the same code. Keys marked [ext] are not in
// the contract yet and are proposed on it; a phone or watch that does not know them ignores them.

nonisolated enum RecordingDayContract {
    /// The header version this build writes, and the `recordingDay` capability value the phone advertises.
    static let version = 1
    /// The hub's feature flag (`hello.features`): only a hub listing it gets day recordings uploaded.
    static let feature = "recording_day"
    /// Application context, phone → watch: Int, the contract version the phone takes (absent: an older phone).
    static let phoneKey = "recordingDay"
    /// Application context, phone → watch: Bool, whether the phone's hub lists `feature`.
    static let phoneHubKey = "recordingDayHub"
    /// [ext] Application context, watch → phone: Data, a JSON `RecordingDayWatchState`.
    static let watchStateKey = "recordingDayWatch"
    /// [ext] Message or user info, phone → watch: String, `Command.start` or `Command.stop`, stamped with when it was
    /// sent (`WatchDayCommandOrder.sentAtKey`) so the watch acts only on the newest.
    static let commandKey = "recordingDayCommand"
    /// Message or user info, phone → watch: Data, a JSON `RecordingDayAck`.
    static let ackKey = "recordingDayAck"
    /// User info, watch → phone: Data, a JSON `RecordingDayLost`.
    static let lostKey = "recordingDayLost"
    /// `transferFile` metadata, and user info for header-only markers: Data, a JSON `RecordingDayHeader`.
    static let chunkKey = "recordingDayChunk"

    /// Chunk lengths: while the phone is reachable, and while chunks wait for it.
    static let liveChunkSeconds = 10.0, backlogChunkSeconds = 60.0
    /// A `sendMessageData` frame stays under this; a bigger chunk goes by `transferFile`.
    static let maxMessageBytes = 48_000
    /// Resending: no acknowledgement this long after a message, or after a file the system says it delivered; then a
    /// backoff from `backoffFirst` doubling to `backoffMax`.
    static let ackTimeout = 30.0, fileAckTimeout = 600.0, backoffFirst = 2.0, backoffMax = 300.0
    /// More audio than this waiting for the phone goes by `transferFile`, one file per chunk.
    static let backlogFileSeconds = 300.0
    /// Messages awaiting their reply at once.
    static let maxInFlightMessages = 3
    /// The watch keeps the records of acknowledged chunks (not their audio) this long, so "Erase 10 min" still reaches
    /// chunks the phone already holds; longer than `RecordingRolling.eraseMinutes`.
    static let keepDeliveredSeconds = 900.0
    /// The phone transcribes past a missing seq once it has been missing this long while later chunks keep coming.
    static let gapWaitSeconds = 120.0
    /// A chunk quieter than this (`RecordingDayLevel`) is not transcribed.
    static let silenceDBFS = -50.0
    /// The phone transcribes contiguous chunks together, in runs up to this long, so words aren't cut at chunk edges.
    static let runSeconds = 60.0
    /// Tries to transcribe a run before its chunks count as untranscribed.
    static let maxAttempts = 3
    /// The phone deletes a day's audio this many days after its final recap, and at the latest this many after the day.
    static let audioDaysAfterRecap = 3.0, audioMaxDays = 7.0
    /// The phone appends to the hub at most this often while the day records, and this many sentences per request.
    static let appendInterval = 20.0, maxAppendSegments = 500

    /// The phone's commands to the watch [ext].
    nonisolated enum Command {
        static let start = "start"
        static let stop = "stop"
    }

    /// Why there is no audio before a chunk (a header's `after`, the hub's coverage gap reasons). An unknown reason is
    /// kept as it is.
    nonisolated enum Reason {
        /// The user paused, or stopped for now (the day stays open).
        static let paused = "paused"
        /// A call, Siri or another app took the microphone until the user continued.
        static let interrupted = "interrupted"
        /// Recording stopped by itself: battery, storage, a failed write, the audio service reset, the app ended.
        static let stopped = "stopped"
    }
}

/// [ext] A bookmark or meeting mark on an all-day recording, on the wall clock: the writer's timeline restarts when a
/// day continues, so a timeline value could name two moments.
nonisolated struct RecordingDayMark: Codable, Sendable, Hashable {
    /// The label of a bookmark and of a meeting mark (the hub reads `meeting` as a hint for its recap).
    static let bookmark = ""
    static let meeting = "meeting"

    var at: Date
    var label: String
}

/// What travels before each chunk's audio (`sendMessageData`), in a `transferFile`'s metadata, or alone for a marker.
/// The contract's keys are snake_case JSON; the [ext] ones are optional and ignored by a phone that does not know them.
nonisolated struct RecordingDayHeader: Sendable, Equatable {
    /// [ext] A mark carried on the first chunk that ends after it: `at` is seconds from this chunk's `startedAt` (below
    /// zero for a mark made while paused, before the chunk began).
    nonisolated struct Mark: Codable, Sendable, Hashable {
        var at: Double
        var label: String
    }

    var v = RecordingDayContract.version
    /// The day recording's id on the watch; the phone uploads it to the hub under the same id.
    var recording: UUID
    /// The local date the recording belongs to, `YYYY-MM-DD`.
    var day: String
    /// 0, 1, 2… per day recording, never reused, without gaps on the watch.
    var seq: Int
    /// The wall clock of the chunk's first sample.
    var startedAt: Date
    /// Seconds of audio; 0 for the header-only end marker.
    var duration: Double
    /// Mean RMS level in dBFS (`RecordingDayLevel`).
    var level: Double?
    /// Erased by the user: no audio follows.
    var erased = false
    /// The last chunk of the day recording (the day ended, or a day left open was closed).
    var end = false
    /// [ext] Why audio is missing before this chunk (`RecordingDayContract.Reason`).
    var after: String?
    /// [ext] Marks made since the previous chunk ended.
    var marks: [Mark]?
    /// [ext] When the day recording began on the watch: the phone's `started_at` for the hub, without waiting for seq 0.
    var recordingStartedAt: Date?
    /// [ext] The watch could not keep this chunk: no audio follows, and the phone reports the time as a `lost` gap.
    var lost = false

    /// Nothing follows the header: an erased or lost chunk, or the end marker.
    var isMarker: Bool { erased || lost || duration == 0 }
}

nonisolated extension RecordingDayHeader {
    /// A header that the watch could have written: anything else is refused and never acknowledged.
    var valid: Bool {
        let numbers = v >= 1 && (0..<1_000_000).contains(seq) && duration.isFinite && (0...600).contains(duration)
            && (level?.isFinite ?? true)
        let times = startedAt.timeIntervalSince1970.isFinite && (recordingStartedAt?.timeIntervalSince1970.isFinite ?? true)
        let extras = (after?.count ?? 0) <= 100 && (marks?.count ?? 0) <= 100
            && (marks?.allSatisfy { $0.at.isFinite && $0.label.count <= 100 } ?? true)
        return numbers && times && extras && RecordingDayCalendar.isDay(day)
    }

    /// The header as JSON (what `chunkKey` carries); empty only for a header no JSON can hold (a non-finite duration).
    var json: Data { (try? JSONEncoder().encode(self)) ?? Data() }

    /// A transfer's metadata or a marker's user info.
    var userInfo: [String: Any] { [RecordingDayContract.chunkKey: json] }

    /// From a transfer's metadata or a user info; nil when it holds no valid header.
    init?(userInfo: [String: Any]?) {
        guard let data = userInfo?[RecordingDayContract.chunkKey] as? Data,
              let header = try? JSONDecoder().decode(Self.self, from: data), header.valid else { return nil }
        self = header
    }

    /// [ext] A `sendMessageData` payload: the header's length as a 4-byte big-endian integer, the header JSON, then the
    /// audio file's bytes (none for a marker).
    func frame(audio: Data?) -> Data {
        let header = json
        var out = Data(capacity: 4 + header.count + (audio?.count ?? 0))
        withUnsafeBytes(of: UInt32(header.count).bigEndian) { out.append(contentsOf: $0) }
        out.append(header)
        if let audio { out.append(audio) }
        return out
    }

    /// A frame back into its header and audio; nil for a frame that is cut short or holds no valid header.
    static func unframe(_ data: Data) -> (header: RecordingDayHeader, audio: Data)? {
        guard data.count >= 4 else { return nil }
        let length = data.prefix(4).reduce(0) { $0 << 8 | Int($1) }
        let start = data.startIndex + 4
        guard length > 0, length <= maxHeaderBytes, data.count - 4 >= length,
              let header = try? JSONDecoder().decode(Self.self, from: data.subdata(in: start..<start + length)),
              header.valid else { return nil }
        return (header, data.subdata(in: start + length..<data.endIndex))
    }

    /// A header is a few hundred bytes; a length past this is a frame from something else.
    private static let maxHeaderBytes = 64 * 1024
}

nonisolated extension RecordingDayHeader: Codable {
    private nonisolated enum CodingKeys: String, CodingKey {
        case v, recording, day, seq, duration, level, erased, end, after, marks, lost
        case startedAt = "started_at"
        case recordingStartedAt = "recording_started_at"
    }

    /// Tolerant: unknown keys are ignored, and a missing or malformed optional key reads as absent; the contract's
    /// required keys (`recording`, `day`, `seq`, `started_at`, `duration`) must be there.
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let id = try c.decode(String.self, forKey: .recording)
        guard let recording = UUID(uuidString: id) else {
            throw DecodingError.dataCorruptedError(forKey: .recording, in: c, debugDescription: "not a UUID")
        }
        self.init(
            v: (try? c.decodeIfPresent(Int.self, forKey: .v)) ?? RecordingDayContract.version,
            recording: recording,
            day: try c.decode(String.self, forKey: .day),
            seq: try c.decode(Int.self, forKey: .seq),
            startedAt: try Self.date(c, .startedAt),
            duration: try c.decode(Double.self, forKey: .duration),
            level: try? c.decodeIfPresent(Double.self, forKey: .level),
            erased: (try? c.decodeIfPresent(Bool.self, forKey: .erased)) ?? false,
            end: (try? c.decodeIfPresent(Bool.self, forKey: .end)) ?? false,
            after: try? c.decodeIfPresent(String.self, forKey: .after),
            marks: try? c.decodeIfPresent([Mark].self, forKey: .marks),
            recordingStartedAt: c.contains(.recordingStartedAt) ? (try? Self.date(c, .recordingStartedAt)) : nil,
            lost: (try? c.decodeIfPresent(Bool.self, forKey: .lost)) ?? false
        )
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(v, forKey: .v)
        // Lowercase, as the hub writes ids.
        try c.encode(recording.uuidString.lowercased(), forKey: .recording)
        try c.encode(day, forKey: .day)
        try c.encode(seq, forKey: .seq)
        try c.encode(Self.timestamp(startedAt), forKey: .startedAt)
        try c.encode(duration, forKey: .duration)
        try c.encodeIfPresent(level.flatMap { $0.isFinite ? $0 : nil }, forKey: .level)
        try c.encode(erased, forKey: .erased)
        try c.encode(end, forKey: .end)
        try c.encodeIfPresent(after, forKey: .after)
        try c.encodeIfPresent(marks, forKey: .marks)
        try c.encodeIfPresent(recordingStartedAt.map(Self.timestamp), forKey: .recordingStartedAt)
        if lost { try c.encode(true, forKey: .lost) }
    }

    /// ISO 8601 with milliseconds and this device's offset (`2026-11-11T09:41:07.250+08:00`), as the contract writes it.
    private static func timestamp(_ date: Date) -> String {
        date.formatted(Date.ISO8601FormatStyle(timeZoneSeparator: .colon, includingFractionalSeconds: true, timeZone: .current))
    }

    /// An ISO 8601 time with or without fractional seconds, or seconds since 1970.
    private static func date(_ c: KeyedDecodingContainer<CodingKeys>, _ key: CodingKeys) throws -> Date {
        if let seconds = try? c.decode(Double.self, forKey: key) { return Date(timeIntervalSince1970: seconds) }
        let text = try c.decode(String.self, forKey: key)
        for fractional in [true, false] {
            if let date = try? Date.ISO8601FormatStyle(timeZoneSeparator: .colon, includingFractionalSeconds: fractional).parse(text) {
                return date
            }
        }
        throw DecodingError.dataCorruptedError(forKey: key, in: c, debugDescription: "not an ISO 8601 time")
    }
}

/// The phone's acknowledgement: these seqs of the recording are on its disk (inclusive `[from, to]` ranges). The reply
/// to a `sendMessageData`, or a message / user info under `RecordingDayContract.ackKey` for chunks sent otherwise.
nonisolated struct RecordingDayAck: Codable, Sendable, Equatable {
    var recording: UUID
    var acks: [[Int]]
}

nonisolated extension RecordingDayAck {
    init(recording: UUID, seqs: some Sequence<Int>) {
        self.init(recording: recording, acks: RecordingDayRanges.ranges(seqs))
    }

    var seqs: [Int] { RecordingDayRanges.expand(acks) }
    var data: Data { (try? JSONEncoder().encode(self)) ?? Data() }

    init?(data: Data?) {
        guard let data, let value = try? JSONDecoder().decode(Self.self, from: data) else { return nil }
        self = value
    }
}

/// The watch's report of seqs it could not keep (a crash took the chunk being written, a file it cannot read), so the
/// phone stops waiting for them and reports the time as a gap. User info under `RecordingDayContract.lostKey`.
nonisolated struct RecordingDayLost: Codable, Sendable, Equatable {
    var recording: UUID
    var lost: [[Int]]
}

nonisolated extension RecordingDayLost {
    init(recording: UUID, seqs: some Sequence<Int>) {
        self.init(recording: recording, lost: RecordingDayRanges.ranges(seqs))
    }

    var seqs: [Int] { RecordingDayRanges.expand(lost) }
    var data: Data { (try? JSONEncoder().encode(self)) ?? Data() }

    init?(data: Data?) {
        guard let data, let value = try? JSONDecoder().decode(Self.self, from: data) else { return nil }
        self = value
    }
}

/// Seqs as the contract's inclusive ranges (`[[from, to]]`), and back.
nonisolated enum RecordingDayRanges {
    /// Sorted, merged ranges of the non-negative seqs given (duplicates and order don't matter).
    static func ranges(_ seqs: some Sequence<Int>) -> [[Int]] {
        var out: [[Int]] = []
        for seq in Set(seqs.filter { $0 >= 0 }).sorted() {
            if let last = out.last, last[1] + 1 == seq { out[out.count - 1][1] = seq } else { out.append([seq, seq]) }
        }
        return out
    }

    /// The seqs the ranges name, sorted and unique. A malformed range (not one or two numbers, negative, or from > to)
    /// is skipped, and at most `limit` seqs are read, so a bad message can't make the receiver count forever.
    static func expand(_ ranges: [[Int]], limit: Int = 100_000) -> [Int] {
        var out = Set<Int>()
        var budget = limit
        for range in ranges {
            guard (1...2).contains(range.count), let from = range.first, let to = range.last, from >= 0, from <= to else { continue }
            for seq in from...to {
                guard budget > 0 else { return out.sorted() }
                budget -= 1
                out.insert(seq)
            }
        }
        return out.sorted()
    }
}

/// [ext] What the watch's all-day recording is doing, for the phone's Today row (application context, watch → phone):
/// the state between chunks, and how far the watch has got.
nonisolated struct RecordingDayWatchState: Sendable, Equatable {
    nonisolated enum State: String, Codable, Sendable { case off, recording, paused, interrupted, stopped }

    /// The open day recording, nil when none.
    var recording: UUID?
    var day: String?
    var state: State
    /// When `state` began.
    var since: Date?
    /// The seq the next chunk gets: every seq below it exists on the watch.
    var next: Int?
    /// Chunks the phone has not acknowledged yet.
    var pending: Int?
}

nonisolated extension RecordingDayWatchState: Codable {
    private nonisolated enum CodingKeys: String, CodingKey { case recording, day, state, since, next, pending }

    /// Tolerant of a newer watch: a state this build does not know reads as `stopped` (not recording, needs a look).
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let state = try c.decode(String.self, forKey: .state)
        self.init(recording: try? c.decodeIfPresent(UUID.self, forKey: .recording),
                  day: try? c.decodeIfPresent(String.self, forKey: .day),
                  state: State(rawValue: state) ?? .stopped,
                  since: try? c.decodeIfPresent(Date.self, forKey: .since),
                  next: try? c.decodeIfPresent(Int.self, forKey: .next),
                  pending: try? c.decodeIfPresent(Int.self, forKey: .pending))
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encodeIfPresent(recording, forKey: .recording)
        try c.encodeIfPresent(day, forKey: .day)
        try c.encode(state, forKey: .state)
        try c.encodeIfPresent(since, forKey: .since)
        try c.encodeIfPresent(next, forKey: .next)
        try c.encodeIfPresent(pending, forKey: .pending)
    }
}

nonisolated extension RecordingDayWatchState {
    /// The mode is on: recording, or paused until the user taps Continue.
    var isOn: Bool { state == .recording || state == .paused || state == .interrupted }

    /// The same state always gives the same bytes (sorted keys), so the watch can skip a push that changes nothing.
    var data: Data? {
        let encoder = JSONEncoder()
        encoder.outputFormatting = .sortedKeys
        return try? encoder.encode(self)
    }

    init?(data: Data?) {
        guard let data, let value = try? JSONDecoder().decode(Self.self, from: data) else { return nil }
        self = value
    }
}

/// Local days for all-day recording: the Gregorian date in a time zone, written `YYYY-MM-DD` whatever the device's
/// calendar or language, and its midnights found so that a day of 23 or 25 hours (a DST change) ends where it does.
nonisolated enum RecordingDayCalendar {
    /// The local date of a moment.
    static func day(of date: Date, in zone: TimeZone) -> String {
        let parts = calendar(zone).dateComponents([.year, .month, .day], from: date)
        return String(format: "%04d-%02d-%02d", parts.year ?? 0, parts.month ?? 0, parts.day ?? 0)
    }

    /// The first moment of the day after the one `date` falls in.
    static func nextMidnight(after date: Date, in zone: TimeZone) -> Date {
        let calendar = calendar(zone)
        // From noon, which every day has, a day later is never inside a DST jump; that day's start is the answer,
        // also where the clock skips midnight itself (the day then starts at 01:00).
        let start = calendar.startOfDay(for: date)
        let noon = calendar.date(bySettingHour: 12, minute: 0, second: 0, of: start) ?? start.addingTimeInterval(12 * 3600)
        let tomorrow = calendar.date(byAdding: .day, value: 1, to: noon) ?? noon.addingTimeInterval(86_400)
        return calendar.startOfDay(for: tomorrow)
    }

    /// The first moment of a `YYYY-MM-DD` date; nil for text that is not a real date.
    static func start(of day: String, in zone: TimeZone) -> Date? {
        guard isDay(day) else { return nil }
        let numbers = day.split(separator: "-").compactMap { Int($0) }
        let calendar = calendar(zone)
        guard numbers.count == 3,
              let noon = calendar.date(from: DateComponents(year: numbers[0], month: numbers[1], day: numbers[2], hour: 12)),
              // A date the calendar had to roll over (02-30 becomes 03-02) is not the date asked for.
              Self.day(of: noon, in: zone) == day else { return nil }
        return calendar.startOfDay(for: noon)
    }

    /// Whether text has the form `YYYY-MM-DD` (ASCII digits).
    static func isDay(_ text: String) -> Bool {
        let chars = Array(text.utf8)
        guard chars.count == 10 else { return false }
        return chars.enumerated().allSatisfy { i, c in i == 4 || i == 7 ? c == UInt8(ascii: "-") : (UInt8(ascii: "0")...UInt8(ascii: "9")).contains(c) }
    }

    private static func calendar(_ zone: TimeZone) -> Calendar {
        var calendar = Calendar(identifier: .gregorian)
        calendar.locale = Locale(identifier: "en_US_POSIX")
        calendar.timeZone = zone
        return calendar
    }
}

/// The level a chunk is skipped by: the RMS of all its samples in dBFS (a power average, so a few words in ten quiet
/// seconds still lift it), with digital silence at `floor`.
nonisolated enum RecordingDayLevel {
    static let floor = -160.0

    static func dBFS(sumOfSquares: Double, count: Int) -> Double {
        guard count > 0, sumOfSquares.isFinite, sumOfSquares > 0 else { return floor }
        let level = 10 * log10(sumOfSquares / Double(count))
        return level.isFinite ? max(floor, level) : floor
    }
}

/// The phone's copy of whether its hub takes day recordings (`recording_day`), for times without a hub `hello` yet
/// (a launch in the background to receive a chunk), like `MeetingAttendanceDefaults`. Rarely changes: a user default.
nonisolated enum RecordingDayDefaults {
    static let feature = RecordingDayContract.feature
    static let offeredKey = "recording.day.offered"

    /// From the hub's features when known, else what the last `hello` said.
    static func offered(_ hubFeatures: Set<String>, defaults: UserDefaults = .standard) -> Bool {
        hubFeatures.isEmpty ? defaults.bool(forKey: offeredKey) : hubFeatures.contains(feature)
    }

    /// Keeps what the hub said; writes only when it changed (every write redraws the views reading defaults).
    static func remember(offered: Bool, defaults: UserDefaults = .standard) {
        if defaults.object(forKey: offeredKey) == nil || defaults.bool(forKey: offeredKey) != offered {
            defaults.set(offered, forKey: offeredKey)
        }
    }
}
