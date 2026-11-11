import Foundation
import Observation

// All-day recording's "phone to hub" (docs/assistant/day-recap.md): each day the watch records becomes one hub recording
// of kind `day` (more parts when a day outgrows one), created with a `PUT`, grown by appends by position, and replaced
// by a `PUT` only to repair it. Everything here is decided from the day's `RecordingDayRecord` (its parts' counters, the
// sentences' positions) and written back to it before the request that depends on it, so a crash, a lost answer or a
// retry never sends a sentence under two positions (C17). Only on a hub that lists `recording_day`.

// MARK: - Wire

/// What reconciliation (`GET /api/v1/recordings`) lists for one hub recording of a day.
nonisolated struct RecordingDayHubRow: Sendable, Equatable {
    var id: UUID
    var revision: Int64? = nil
    var part: Int? = nil
    /// The hub's count of the recording's positions, and the revision of its last `PUT`.
    var next: Int? = nil
    var base: Int64? = nil
    var recapState: String? = nil
}

/// A sentence as the hub stores it: wall-clock seconds since the part's `started_at`, pauses included.
nonisolated struct RecordingDaySegment: Codable, Sendable, Equatable {
    var start: Double
    var end: Double
    var text: String
}

nonisolated struct RecordingDayBookmark: Codable, Sendable, Equatable {
    var at: Double
    /// "" for a bookmark, "meeting" for a meeting mark [ext].
    var label: String
}

/// What a create, a repair and an append all say beside the segments; the last one the hub stored is remembered so an
/// append that would change none of it isn't sent.
nonisolated struct RecordingDayMeta: Sendable, Equatable {
    var endedAt: Date?
    var duration: Double
    var bookmarks: [RecordingDayBookmark]
    var coverage: RecordingDayCoverage
    var complete: Bool
}

/// `PUT /api/v1/recordings/{id}` of a day part: its creation (no segments) or a repair (every positioned sentence,
/// empty ones included, so the hub's count equals the phone's after it).
nonisolated struct RecordingDayPutBody: Encodable, Sendable {
    var revision: Int64
    var day: String
    var part: Int
    var startedAt: Date
    var language: String
    var meta: RecordingDayMeta
    var segments: [RecordingDaySegment]

    private nonisolated enum CodingKeys: String, CodingKey {
        case revision, kind, day, part, duration, language, device, title, bookmarks, coverage, segments, complete
        case startedAt = "started_at", endedAt = "ended_at"
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(revision, forKey: .revision)
        try c.encode("day", forKey: .kind)
        try c.encode(day, forKey: .day)
        try c.encode(part, forKey: .part)
        try c.encode(RecordingDayUpload.timestamp(startedAt), forKey: .startedAt)
        try c.encodeIfPresent(meta.endedAt.map(RecordingDayUpload.timestamp), forKey: .endedAt)
        try c.encode(meta.duration, forKey: .duration)
        try c.encode(language, forKey: .language)
        try c.encode("watch", forKey: .device)
        try c.encode("", forKey: .title)
        try c.encode(meta.bookmarks, forKey: .bookmarks)
        try c.encode(meta.coverage, forKey: .coverage)
        try c.encode(segments, forKey: .segments)
        try c.encode(meta.complete, forKey: .complete)
    }
}

/// `POST /api/v1/recordings/{id}/segments`: positions `from…` on the base of the last `PUT`, with the other fields whole.
nonisolated struct RecordingDayAppendBody: Encodable, Sendable {
    var base: Int64
    var revision: Int64
    var from: Int
    var segments: [RecordingDaySegment]
    var meta: RecordingDayMeta

    private nonisolated enum CodingKeys: String, CodingKey {
        case base, revision, from, segments, duration, bookmarks, coverage, complete
        case endedAt = "ended_at"
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(base, forKey: .base)
        try c.encode(revision, forKey: .revision)
        try c.encode(from, forKey: .from)
        try c.encode(segments, forKey: .segments)
        try c.encode(meta.duration, forKey: .duration)
        try c.encodeIfPresent(meta.endedAt.map(RecordingDayUpload.timestamp), forKey: .endedAt)
        try c.encode(meta.bookmarks, forKey: .bookmarks)
        try c.encode(meta.coverage, forKey: .coverage)
        try c.encode(meta.complete, forKey: .complete)
    }
}

/// The `200` of a `PUT` (`stored`, `stale`, `expired`) and of an append (`stored`, `expired`, with the hub's count).
nonisolated struct RecordingDayAnswer: Decodable, Sendable {
    var revision: Int64?
    var next: Int?
    var status: String?
}

/// How the upload answers a refused or failed request, by HTTP status and the contract's code (C22).
nonisolated enum RecordingDayReaction: Equatable, Sendable {
    /// `409 base_changed`: a `PUT` the phone may or may not have sent replaced the text (`base`, the hub's `next`).
    case baseChanged(base: Int64?, next: Int?)
    /// `409 segments_gap`: `from` was above the hub's count; send from `next`.
    case segmentsGap(next: Int?)
    /// `404 recording_not_found`: create it (again).
    case notFound
    /// `400`, or a `404` without that code (the route is missing): an older manager. Nothing goes until its next hello.
    case unsupported
    /// `413 too_large`: the body was over 1 MiB; fewer segments per append.
    case tooLarge
    /// `413 day_full`: the part is full at the hub's `next`; the day continues in a next part.
    case dayFull(next: Int?)
    /// `409 not_day`, `409 kind_changed`: the id is another kind of recording there. Never written over.
    case conflict(String)
    /// `403`: read-only or not allowed; paused until the app comes to the front, a hello, or a retry.
    case forbidden
    /// `410`: this part was deleted on the hub.
    case deleted
    case cancelled
    /// `503`, no network, a timeout, anything else: again later, with a backoff.
    case retry
}

nonisolated extension RecordingDayReaction {
    init(_ error: Error) {
        if error.isCancellation {
            self = .cancelled
            return
        }
        guard let api = error as? APIError else {
            self = .retry
            return
        }
        let code = api.fields["code"] ?? ""
        switch (api.status, code) {
        case (409, "base_changed"): self = .baseChanged(base: Self.int64(api, "base"), next: Self.int(api, "next"))
        case (409, "segments_gap"): self = .segmentsGap(next: Self.int(api, "next"))
        case (409, _): self = .conflict(code.isEmpty ? "conflict" : code)
        case (404, "recording_not_found"): self = .notFound
        case (404, _), (400, _): self = .unsupported
        case (413, "day_full"): self = .dayFull(next: Self.int(api, "next"))
        case (413, _): self = .tooLarge
        case (403, _): self = .forbidden
        case (410, _): self = .deleted
        default: self = .retry
        }
    }

    /// A number in the error's body: the client keeps it as text (`APIError.fields`), its JSON in `details`.
    private static func int(_ error: APIError, _ key: String) -> Int? {
        error.fields[key].flatMap { Int($0) } ?? error.details?[key]?.int
    }

    private static func int64(_ error: APIError, _ key: String) -> Int64? {
        error.fields[key].flatMap { Int64($0) } ?? error.details?[key]?.double.flatMap { Int64(exactly: $0) }
    }
}

// MARK: - Pure decisions

/// What the upload sends and in what order, decided from the record alone (unit tested).
nonisolated enum RecordingDayUpload {
    /// Where a placed sentence is: its chunk and its index in the chunk's `sentences`.
    nonisolated struct Ref: Sendable, Equatable {
        var seq: Int
        var index: Int
    }

    /// The next thing a part needs.
    nonisolated enum Step: Sendable, Equatable {
        /// A create or repair `PUT` (one decided on and not stored yet goes again, with its revision).
        case put
        /// Positions the hub never had were dropped (an erase before they went): close the hole locally.
        case compact
        /// Give the transcribed sentences without a position the next ones in this part.
        case assign
        /// Send positions `from..<to` (also a retry of ones assigned before).
        case append(from: Int, to: Int)
        /// Only the other fields changed (coverage, duration, the end): an append without segments.
        case meta
        /// Nothing until then (the append throttle).
        case wait(Date)
        case done
    }

    /// An append's body stays under the contract's 1 MiB with room for its coverage and bookmarks.
    static let maxAppendBytes = 900_000
    /// JSON around one segment's text (`{"start":…,"end":…,"text":""}`), counted generously.
    private static let segmentOverhead = 80

    /// The revision for a new request: the phone's clock in milliseconds, above every revision this part sent and every
    /// one the hub was seen to hold (C17).
    static func revision(_ part: RecordingDayPart, now: Date, floor: Int64 = 0) -> Int64 {
        max(millis(now), part.maxSentRevision + 1, (part.base ?? 0) + 1, floor + 1)
    }

    static func millis(_ date: Date) -> Int64 {
        let ms = (date.timeIntervalSince1970 * 1000).rounded()
        return ms.isFinite ? Int64(ms) : 0
    }

    /// RFC 3339 with milliseconds: the offsets are measured from this exact moment.
    static func timestamp(_ date: Date) -> String {
        date.formatted(Date.ISO8601FormatStyle(includingFractionalSeconds: true))
    }

    /// The sentences placed in `part`, by position, and whether no position is held twice.
    static func layout(_ record: RecordingDayRecord, part: Int) -> (placed: [Int: Ref], clean: Bool) {
        var placed: [Int: Ref] = [:]
        var clean = true
        for (seq, chunk) in record.chunks {
            let positions = chunk.positions ?? []
            for index in 0..<min(positions.count, chunk.sentences?.count ?? 0) where positions[index].part == part {
                if placed.updateValue(Ref(seq: seq, index: index), forKey: positions[index].pos) != nil { clean = false }
            }
        }
        return (placed, clean)
    }

    /// The step `record.parts[index]` needs next. `isLast`: the part new sentences go to. `lastAppend`: when this part's
    /// last request was stored; `sentMeta`: what it said. `force`: no throttle (the catch-up, a day that ended).
    static func step(_ record: RecordingDayRecord, partIndex index: Int, isLast: Bool, now: Date, lastAppend: Date?,
                     sentMeta: RecordingDayMeta?, force: Bool) -> Step {
        let part = record.parts[index]
        guard part.pendingPut == nil, part.created else { return .put }
        let layout = layout(record, part: part.part)
        guard layout.clean, !layout.placed.keys.contains(where: { $0 < 0 || $0 >= part.count }) else { return .put }
        let missing = (0..<part.count).filter { layout.placed[$0] == nil }
        if let first = missing.first {
            // The hub has the dropped text when it's below its count: only a full replacement takes it out.
            return first >= part.next ? .compact : .put
        }
        if part.next < part.count {
            return .append(from: part.next, to: batchEnd(record, placed: layout.placed, from: part.next, count: part.count,
                                                         limit: RecordingDayContract.maxAppendSegments))
        }
        let throttled = !force && part.endedAt == nil && record.endedAt == nil
            && lastAppend.map { now.timeIntervalSince($0) < RecordingDayContract.appendInterval } == true
        let due = lastAppend?.addingTimeInterval(RecordingDayContract.appendInterval) ?? now
        if isLast, record.chunks.values.contains(where: \.hasUnplacedText) { return throttled ? .wait(due) : .assign }
        if part.complete { return .done }
        let current = meta(record, partIndex: index, isLast: isLast, sent: part.next, now: now)
        guard current != sentMeta else { return .done }
        return throttled ? .wait(due) : .meta
    }

    /// The end of a batch from `from`: at most `limit` segments and `maxAppendBytes` of text.
    static func batchEnd(_ record: RecordingDayRecord, placed: [Int: Ref], from: Int, count: Int, limit: Int) -> Int {
        var end = from
        var bytes = 0
        while end < count, end - from < max(1, limit) {
            let size = placed[end].flatMap { record.chunks[$0.seq]?.sentences?[$0.index].text.utf8.count } ?? 0
            if end > from, bytes + size + segmentOverhead > maxAppendBytes { break }
            bytes += size + segmentOverhead
            end += 1
        }
        return end
    }

    /// The segments at positions `from..<to` of `part`, in position order; nil when one is missing.
    static func segments(_ record: RecordingDayRecord, part: RecordingDayPart, placed: [Int: Ref], from: Int, to: Int) -> [RecordingDaySegment]? {
        var out: [RecordingDaySegment] = []
        out.reserveCapacity(max(0, to - from))
        for pos in from..<max(from, to) {
            guard let ref = placed[pos], let chunk = record.chunks[ref.seq], let sentences = chunk.sentences,
                  sentences.indices.contains(ref.index) else { return nil }
            out.append(segment(sentences[ref.index], of: chunk, since: part.startedAt))
        }
        return out
    }

    /// A chunk-relative sentence on the part's clock: wall-clock seconds since its start, pauses included (the contract:
    /// "started_at + start is the moment it was said"). A time no JSON can carry is sent as the chunk's start. Never
    /// below 0: after `413 day_full` a late backlog sentence said before the newest part began still goes to that part
    /// (the full one can't take it), and the contract has no negative times, so it is placed at the part's start.
    static func segment(_ sentence: RecordingSentence, of chunk: RecordingDayChunk, since base: Date) -> RecordingDaySegment {
        let offset = chunk.startedAt.timeIntervalSince(base)
        let start = max(0, finite(offset + sentence.start, or: offset))
        return RecordingDaySegment(start: start, end: max(start, finite(offset + sentence.end, or: start)), text: sentence.text)
    }

    /// Gives every transcribed sentence without a position the next positions of `record.parts[index]`, chunks in seq
    /// order and sentences in their order.
    static func assign(_ record: inout RecordingDayRecord, partIndex index: Int) {
        let part = record.parts[index].part
        var count = record.parts[index].count
        for seq in record.chunks.keys.sorted() {
            guard var chunk = record.chunks[seq], chunk.hasUnplacedText else { continue }
            var positions = chunk.positions ?? []
            while positions.count < (chunk.sentences?.count ?? 0) {
                positions.append(RecordingDayPosition(part: part, pos: count))
                count += 1
            }
            chunk.positions = positions
            record.chunks[seq] = chunk
        }
        record.parts[index].count = count
    }

    /// Numbers `part`'s sentences `0..<n` in their current order (a repair `PUT` carries them so), and returns n.
    /// Positions past a chunk's sentences (left by a build that crashed mid-write) are dropped.
    @discardableResult static func renumber(_ record: inout RecordingDayRecord, part: Int) -> Int {
        renumber(&record, part: part, from: 0)
    }

    /// Closes the holes among `part`'s positions at or above `next`, which the hub never had: they move down in order.
    /// Returns the new count.
    @discardableResult static func compact(_ record: inout RecordingDayRecord, part: Int, next: Int) -> Int {
        renumber(&record, part: part, from: next)
    }

    private static func renumber(_ record: inout RecordingDayRecord, part: Int, from floor: Int) -> Int {
        var refs: [(pos: Int, seq: Int, index: Int)] = []
        for (seq, chunk) in record.chunks {
            guard var positions = chunk.positions else { continue }
            let kept = chunk.sentences?.count ?? 0
            if positions.count > kept {
                positions.removeLast(positions.count - kept)
                record.chunks[seq]?.positions = positions.isEmpty ? nil : positions
            }
            for (index, position) in positions.enumerated() where position.part == part && position.pos >= floor {
                refs.append((position.pos, seq, index))
            }
        }
        refs.sort { ($0.pos, $0.seq, $0.index) < ($1.pos, $1.seq, $1.index) }
        for (offset, ref) in refs.enumerated() { record.chunks[ref.seq]?.positions?[ref.index].pos = floor + offset }
        return floor + refs.count
    }

    /// Hands every position of `part` at or above `held` back (none of them reached the hub: `413 day_full`), so they go
    /// to the next part. Positions in a chunk are in its sentences' order, so each chunk keeps a prefix.
    static func unplace(_ record: inout RecordingDayRecord, part: Int, from held: Int) {
        for (seq, chunk) in record.chunks {
            guard let positions = chunk.positions,
                  let cut = positions.firstIndex(where: { $0.part == part && $0.pos >= held }) else { continue }
            let kept = Array(positions.prefix(cut))
            record.chunks[seq]?.positions = kept.isEmpty ? nil : kept
        }
    }

    /// The wall-clock start of the earliest transcribed sentence without a position: where a next part begins.
    static func earliestUnplaced(_ record: RecordingDayRecord) -> Date? {
        var earliest: Date?
        for chunk in record.chunks.values where chunk.hasUnplacedText {
            let sentences = chunk.sentences ?? []
            for sentence in sentences[(chunk.positions?.count ?? 0)...] {
                let at = chunk.startedAt.addingTimeInterval(finite(sentence.start, or: 0))
                earliest = min(earliest ?? at, at)
            }
        }
        return earliest
    }

    /// What a request for `record.parts[index]` says beside its segments, the hub holding positions below `sent` after it.
    static func meta(_ record: RecordingDayRecord, partIndex index: Int, isLast: Bool, sent: Int, now: Date) -> RecordingDayMeta {
        let part = record.parts[index]
        let first = record.parts.map(\.part).min() == part.part
        let coverage = RecordingDayCoverage.compute(record: record, part: part, sentToPositionBelow: sent, now: now)
        let complete = RecordingDayCoverage.isComplete(record: record, part: part, sentToPositionBelow: sent, coverage: coverage,
                                                       isLast: isLast)
        // A part's time: from its start (the first part takes anything earlier too) to the next part's start.
        func inPart(_ at: Date) -> Bool {
            (first || at >= part.startedAt) && (part.endedAt.map { at < $0 } ?? true)
        }
        let duration = record.chunks.values.reduce(0.0) { sum, chunk in
            chunk.duration > 0 && !chunk.erased && !chunk.lost && inPart(chunk.startedAt) ? sum + chunk.duration : sum
        }
        var bookmarks: [RecordingDayBookmark] = []
        for chunk in record.chunks.values {
            for mark in chunk.marks ?? [] {
                let at = chunk.startedAt.addingTimeInterval(finite(mark.at, or: 0))
                guard inPart(at) else { continue }
                bookmarks.append(RecordingDayBookmark(at: max(0, at.timeIntervalSince(part.startedAt)), label: mark.label))
            }
        }
        bookmarks.sort { ($0.at, $0.label) < ($1.at, $1.label) }
        return RecordingDayMeta(endedAt: part.endedAt ?? (isLast ? record.endedAt : nil), duration: duration,
                                bookmarks: bookmarks, coverage: coverage, complete: complete)
    }

    private static func finite(_ value: Double, or fallback: Double) -> Double { value.isFinite ? value : fallback }
}

// MARK: - The upload

/// Uploads every day the phone holds to the hub (docs/assistant/day-recap.md "Phone to hub"), one request at a time.
/// Follows the day store's changes (a chunk stored, text transcribed, an erase): appends go at most every
/// `appendInterval` while the day records, at once once it ended. The hub's word on a day comes from `RecordingSync`'s
/// reconciliation (`reconciled`), the flag from each `hello` (`helloReceived`).
@MainActor @Observable final class RecordingDaySync {
    static let shared = RecordingDaySync()

    /// How a day's visit ended.
    nonisolated enum Outcome: Sendable, Equatable {
        /// Nothing more to send now.
        case done
        /// Look again then (the append throttle, a missing seq's wait).
        case later(Date)
        /// The hub or the network failed: again after a backoff.
        case failed
        /// Waits for something else: the flag, a hello, the app in front, the user.
        case blocked
        case cancelled
    }

    /// The day whose text is on its way now.
    private(set) var uploading: UUID?

    @ObservationIgnored private let store: RecordingDayStore
    @ObservationIgnored private let library: RecordingLibrary
    @ObservationIgnored private let api: () -> APIClient?
    @ObservationIgnored private let features: () -> Set<String>
    @ObservationIgnored private let now: () -> Date
    @ObservationIgnored private let days: () -> Int
    @ObservationIgnored private let appState: RecordingDayAppState
    @ObservationIgnored private let noteParts: (UUID, [UUID]) -> Void
    @ObservationIgnored private let removed: (UUID) -> Void
    @ObservationIgnored private let recapFinal: () -> Void
    @ObservationIgnored private let debounce: Duration

    @ObservationIgnored private var started = false
    @ObservationIgnored private var listening: Task<Void, Never>?
    @ObservationIgnored private var worker: Task<Void, Never>?
    @ObservationIgnored private var timer: Task<Void, Never>?
    /// Days to visit with the next pass.
    @ObservationIgnored private var dirty: Set<UUID> = []
    /// Days to visit again then.
    @ObservationIgnored private var waits: [UUID: Date] = [:]
    /// Days whose last visit failed, and when they may go again (C22: 5 s doubling to 10 min). A change to such a day
    /// waits for it too: the upload's own bookkeeping changes the day with every request, and a failing hub or network is
    /// not asked again at once.
    @ObservationIgnored private var retry: [UUID: Backoff] = [:]
    /// Days the last visit found with nothing to send (left out of a foreground sweep until they change).
    @ObservationIgnored private var settled: Set<UUID> = []
    /// Days with a hub recording this process saw created (reconciliation's absence check applies to them).
    @ObservationIgnored private var knownCreated: Set<UUID> = []
    /// Days refused as unsupported since the last hello (a relaunch or hello lets each try once more, C22).
    @ObservationIgnored private var unsupportedSeen: Set<UUID> = []
    /// `403`: nothing goes until the app is in front again, a hello, or a retry.
    @ObservationIgnored private(set) var paused = false
    /// What the last hello said about the flag; nil before the launch's first.
    @ObservationIgnored private var wasOffered: Bool?
    /// Per hub recording: when its last request was stored, what it said, the batch size after `413 too_large`, the
    /// highest revision the hub was seen to hold, and when its detail may be read again for a final recap (none: now).
    @ObservationIgnored private var lastAppend: [UUID: Date] = [:]
    @ObservationIgnored private var lastStored: [UUID: Date] = [:]
    @ObservationIgnored private var sentMeta: [UUID: RecordingDayMeta] = [:]
    @ObservationIgnored private var batch: [UUID: Int] = [:]
    @ObservationIgnored private var hubRevision: [UUID: Int64] = [:]
    @ObservationIgnored private var recapProbeAt: [UUID: Date] = [:]
    /// Per day, when the soonest of its parts waiting for a final recap may be read again: reconciliation visits the day
    /// then even when its rows say nothing new.
    @ObservationIgnored private var finalDue: [UUID: Date] = [:]
    /// Reconciliation's word, applied at the day's next visit (one request at a time).
    @ObservationIgnored private var pendingRows: [UUID: (rows: [RecordingDayHubRow], at: Date)] = [:]
    @ObservationIgnored private var pendingDeletes: [UUID: Set<UUID>] = [:]
    @ObservationIgnored private var noted: [UUID: [UUID]] = [:]
    /// Per day, its created parts as the last visit left them (base, count, recap state), to tell whether
    /// reconciliation says anything new without reading the day.
    @ObservationIgnored private var believed: [UUID: [RecordingDayHubRow]] = [:]
    @ObservationIgnored private var grant: RecordingDayGrant?
    /// The catch-up runs (`drain`): no throttle, its task is the upload's time, and it alone starts the worker meanwhile.
    @ObservationIgnored private var draining = false

    private nonisolated struct Backoff: Sendable {
        var failures: Int
        var at: Date
    }

    /// Steps one part may take in one visit (a backlog of 20000 sentences is 40 appends).
    private static let maxSteps = 200
    /// Reconciliation's list may predate a request stored this long before it reached the upload (the list's own
    /// request): such a part's base and count are the phone's newer word, and it may not be listed yet.
    private static let listSlack: TimeInterval = 60
    /// `stale` answers to a `PUT` in one visit before waiting for reconciliation to tell the hub's revision.
    private static let maxStale = 3
    /// How long after a detail that said its recap isn't the final one yet it is read again (C21). Reconciliation lists
    /// the part `ready` the whole time, so a final recap written between two of its lists is seen only this way.
    static let recapProbeInterval: TimeInterval = 3600

    /// - Parameters:
    ///   - features: the hub's features from its last `hello` (empty: not known, the cached flag decides).
    ///   - days: the hub's `recording_days` (`RecordingSync`): an older day is not uploaded.
    ///   - noteParts: a day's hub ids past its first part, for deletes (`RecordingSync.noteDayParts`).
    ///   - removed: a day was removed here because the hub deleted it (cancels its transcription).
    ///   - recapFinal: a day's final recap time became known (the keeper's 3-day clock).
    init(store: RecordingDayStore = .shared, library: RecordingLibrary = .shared,
         api: @escaping () -> APIClient? = { AppModel.shared.api },
         features: @escaping () -> Set<String> = { AppModel.shared.hubFeatures },
         now: @escaping () -> Date = { .now },
         days: @escaping () -> Int = { RecordingSync.shared.store.days },
         appState: RecordingDayAppState = RecordingDaySystemState.shared,
         noteParts: @escaping (UUID, [UUID]) -> Void = { RecordingSync.shared.noteDayParts($0, $1) },
         removed: @escaping (UUID) -> Void = { RecordingDayTranscriber.shared.cancel($0) },
         recapFinal: @escaping () -> Void = { RecordingDayKeeper.shared.schedule() },
         debounce: Duration = .seconds(1)) {
        self.store = store; self.library = library; self.api = api; self.features = features; self.now = now
        self.days = days; self.appState = appState; self.noteParts = noteParts; self.removed = removed
        self.recapFinal = recapFinal; self.debounce = debounce
    }

    // MARK: Triggers

    /// Follows the day store from now on (launch, before WatchConnectivity activates). Idempotent.
    func start() {
        guard !started else { return }
        started = true
        let changes = store.changes
        listening = Task { [weak self] in
            for await id in changes { self?.changed(id) }
        }
    }

    /// A day changed (a chunk, its text, an erase, its library row): visited soon.
    func changed(_ id: UUID) {
        settled.remove(id)
        dirty.insert(id)
        kick()
    }

    /// The app came to the front: a `403` pause ends, a failed day goes now, and every day not known to be done is
    /// visited.
    func foreground() {
        paused = false
        releaseBackoff()
        dirty.formUnion(dayIDs().filter { !settled.contains($0) })
        kick()
    }

    /// The app went to the background (C9): a pass in progress goes on only inside a grant; none given stops it now (its
    /// request is cancelled, and the day goes again when the upload may work again).
    func background() {
        guard let worker, !draining else { return }
        guard ensureGrant() else {
            worker.cancel()
            return
        }
    }

    /// The hub's `hello` (hub group only): the pause ends, a day an older manager refused is tried once more, a failed
    /// one goes now, and when the flag is new (or this is the launch's first hello) every day is visited: one may have
    /// waited days for it. A reconnect to the same hub visits nothing else.
    func helloReceived(offered: Bool) {
        let refused = unsupportedSeen
        unsupportedSeen = []
        paused = false
        defer { wasOffered = offered }
        guard offered else { return }
        if wasOffered != true {
            settled = []
            dirty.formUnion(dayIDs())
        }
        dirty.formUnion(refused)
        releaseBackoff()
        kick()
    }

    /// Reconciliation's rows of day recordings, by the local day each belongs to (`RecordingSync.reconcile`). Every
    /// local day is named, with no rows when the hub listed none of its parts: a part this phone created that the hub
    /// doesn't list any more (a new hub) is created again. A day whose recap may have become the final one since its
    /// detail was last read is visited too, whatever its rows say (C21).
    func reconciled(_ rows: [UUID: [RecordingDayHubRow]]) {
        let at = now()
        for (day, list) in rows where !list.isEmpty || knownCreated.contains(day) {
            let probe = finalDue[day].map { $0 <= at } ?? false
            // The hub says what the phone already holds (the usual case, every ten minutes): no day to read.
            guard probe || !agrees(day, list) else { continue }
            pendingRows[day] = (list, at)
            dirty.insert(day)
        }
        kick()
    }

    /// Whether reconciliation's rows for a day are exactly its parts as the phone last left them.
    private func agrees(_ day: UUID, _ rows: [RecordingDayHubRow]) -> Bool {
        guard let parts = believed[day], !parts.isEmpty, parts.count == rows.count else { return false }
        let byID = Dictionary(rows.map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first })
        return parts.allSatisfy { part in
            guard let row = byID[part.id] else { return false }
            return row.base == part.base && row.next == part.next && row.recapState == part.recapState
        }
    }

    /// The hub deleted these hub recordings of a day (reconciliation's `deleted`): those parts stop; the day goes here
    /// too once every part of it is gone.
    func hubDeleted(day: UUID, hubIDs: [UUID]) {
        pendingDeletes[day, default: []].formUnion(hubIDs)
        dirty.insert(day)
        kick()
    }

    /// The day was deleted here (`RecordingSync.delete`) or purged (`RecordingDayKeeper`): forget it.
    func removedHere(_ id: UUID) {
        forget(id)
        removed(id)
        store.didRemove(id)
    }

    /// The day's language changed (`RecordingDayTranscriber.setLocale`, after its chunks were reset). Only a `PUT` carries
    /// a language, so every part the hub has, or is being given, gets a repair decided now and on disk before this
    /// returns (a relaunch still sends it), whether or not any of its text changed: audio retention deleted may have left
    /// every sentence as it was, and a silent day has none (C17). Its revision is above a `PUT` in flight, so that one's
    /// answer doesn't clear it.
    func languageChanged(_ id: UUID) async {
        let at = now()
        let floors = hubRevision
        await store.update(id) { record in
            for i in record.parts.indices where !record.parts[i].expired && !record.parts[i].deleted
                && (record.parts[i].created || record.parts[i].pendingPut != nil) {
                let part = record.parts[i]
                let revision = max(RecordingDayUpload.revision(part, now: at, floor: floors[part.hubID] ?? 0), (part.pendingPut ?? 0) + 1)
                record.parts[i].pendingPut = revision
                record.parts[i].maxSentRevision = max(part.maxSentRevision, revision)
            }
        }
        _ = await store.flush(id)
        changed(id)
    }

    /// A pass soon (several changes in a row become one), when a day may go now and the upload may work: in front, or in
    /// the background inside a grant. While the catch-up runs, it alone starts passes (`drain`).
    func kick(immediately: Bool = false) {
        guard worker == nil, !draining, !dueDays().isEmpty else { return }
        guard appState.inForeground || ensureGrant() else { return }
        startWorker(immediately: immediately)
    }

    /// Visits one day now, without debounce, throttle or backoff wait (tests).
    @discardableResult func syncNow(_ id: UUID, force: Bool = false) async -> Outcome {
        while let worker { await worker.value }
        let outcome = await visit(id, force: force)
        note(outcome, for: id)
        return outcome
    }

    /// Returns once no pass is running (tests).
    func whenIdle() async {
        while let worker { await worker.value }
    }

    /// The background catch-up: every day the phone holds, throttle aside, inside the catch-up's own time; a day that
    /// failed before gets one more try. True when something is left to send. Cancelled, nothing starts again after it.
    func drain() async -> Bool {
        draining = true
        defer { draining = false }
        dirty.formUnion(await store.ids())
        releaseBackoff()
        while !Task.isCancelled {
            // A pass already going (in front, or in a grant) becomes the catch-up's; the next ones start here.
            if worker == nil {
                guard !dueDays().isEmpty else { break }
                startWorker(immediately: true)
            }
            guard let pass = worker else { break }
            await withTaskCancellationHandler { await pass.value } onCancel: { pass.cancel() }
        }
        return !retry.isEmpty || !waits.isEmpty || !dirty.isEmpty
    }

    // MARK: Passes

    @discardableResult private func startWorker(immediately: Bool) -> Task<Void, Never> {
        let wait = immediately ? nil : debounce
        let task = Task<Void, Never> { [weak self] in await self?.runPasses(debounce: wait) }
        worker = task
        return task
    }

    /// The upload's grant of background time: the one it holds, or a new one; false when the system gives none.
    private func ensureGrant() -> Bool {
        if grant == nil { grant = appState.beginGrant(named: "recording-day-sync") { [weak self] in self?.grantExpired() } }
        return grant != nil
    }

    /// Whether the next visit may go: the app in front, the upload's grant, or the catch-up.
    private var mayWork: Bool { draining || appState.inForeground || grant != nil }

    /// Visits the days that may go, until none is left. Ends at once when its time does: the app left the front without a
    /// grant, the grant or the catch-up ran out (C9). The cleanup starts nothing; what is left waits for its timer or the
    /// next change, foreground, hello or catch-up.
    private func runPasses(debounce: Duration?) async {
        var interrupted = false
        defer { finish() }
        if let debounce { try? await Task.sleep(for: debounce) }
        while !interrupted {
            let ids = dueDays()
            guard !ids.isEmpty else { return }
            dirty.subtract(ids)
            for (i, id) in ids.enumerated() {
                guard !Task.isCancelled, mayWork else {
                    dirty.formUnion(ids[i...])
                    interrupted = true
                    break
                }
                note(await visit(id, force: draining), for: id)
            }
        }
    }

    private func finish() {
        worker = nil
        grant?.end()
        grant = nil
        scheduleWake()
    }

    /// The grant ran out: stop at once (the request in flight is cancelled; its day is visited again later).
    private func grantExpired() {
        grant = nil
        worker?.cancel()
    }

    private func note(_ outcome: Outcome, for id: UUID) {
        waits[id] = nil
        switch outcome {
        case .done:
            retry[id] = nil
            settled.insert(id)
        case .later(let at):
            retry[id] = nil
            // More steps than one visit takes: on with the next pass, after the other days.
            if at <= now() { dirty.insert(id) } else { waits[id] = at }
        case .failed:
            backOff(id)
        case .cancelled:
            // Stopped because its time ran out: again with the next pass. A request the system cancelled by itself counts
            // as a failure, so it can't come round again at once for ever.
            if Task.isCancelled || !mayWork { dirty.insert(id) } else { backOff(id) }
        case .blocked:
            retry[id] = nil
        }
    }

    private func backOff(_ id: UUID) {
        let failures = (retry[id]?.failures ?? 0) + 1
        retry[id] = Backoff(failures: failures, at: now().addingTimeInterval(Self.backoff(after: failures)))
    }

    /// The wait after `failures` failed visits of a day in a row: 5 s, doubling to 10 min.
    static func backoff(after failures: Int) -> TimeInterval {
        min(600, 5 * pow(2, Double(min(max(failures, 1), 8) - 1)))
    }

    /// Failed days may go at once (the app in front, a hello, the catch-up). Their count stays, so another failure waits
    /// longer than the last.
    private func releaseBackoff() {
        let at = now()
        for id in Array(retry.keys) { retry[id]?.at = at }
        dirty.formUnion(retry.keys)
    }

    /// Dirty days that may go now, in id order: a day waiting out its backoff doesn't, whatever changed (C22).
    private func dueDays() -> [UUID] {
        let at = now()
        return dirty.filter { retry[$0].map { $0.at <= at } ?? true }.sorted { $0.uuidString < $1.uuidString }
    }

    /// A timer for the soonest throttle or gap wait, or backoff.
    private func scheduleWake() {
        timer?.cancel()
        timer = nil
        guard let at = (Array(waits.values) + retry.values.map(\.at)).min() else { return }
        let delay = max(0, at.timeIntervalSince(now()))
        timer = Task { [weak self] in
            try? await Task.sleep(for: .seconds(delay))
            guard !Task.isCancelled else { return }
            self?.wake()
        }
    }

    /// The timer fired: the days whose wait ended go, if the upload may work now (`kick`). With nothing due yet (the
    /// timer came early), the next one; with no time to work, the next change or foreground.
    private func wake() {
        timer = nil
        let at = now()
        for (id, date) in waits where date <= at {
            waits[id] = nil
            dirty.insert(id)
        }
        for (id, backoff) in retry where backoff.at <= at { dirty.insert(id) }
        if dueDays().isEmpty { scheduleWake() } else { kick(immediately: true) }
    }

    /// Day ids from the library's index rows (in memory; a day's record is read only when visited).
    private func dayIDs() -> [UUID] { library.entries.filter(\.isDay).map(\.id) }

    private func forget(_ id: UUID) {
        dirty.remove(id); waits[id] = nil; retry[id] = nil; settled.remove(id); knownCreated.remove(id)
        unsupportedSeen.remove(id); pendingRows[id] = nil; pendingDeletes[id] = nil; noted[id] = nil; believed[id] = nil
        finalDue[id] = nil
    }

    // MARK: One day

    private func visit(_ id: UUID, force: Bool) async -> Outcome {
        if let gone = pendingDeletes.removeValue(forKey: id), await applyDeletes(gone, to: id) { return .done }
        if let rows = pendingRows.removeValue(forKey: id) { await apply(rows.rows, listedAt: rows.at, to: id) }
        guard !paused, let api = api(), !api.config.isDemo else { return .blocked }
        guard RecordingDayDefaults.offered(features()) else { return .blocked }
        guard var record = await store.record(id) else { return .done }
        guard record.problem == nil else { return .blocked }
        if record.unsupportedUntilHello {
            guard !unsupportedSeen.contains(id) else { return .blocked }
            // Refused before this hello (or this launch): one more try.
            await store.update(id) { $0.unsupportedUntilHello = false }
        }
        guard let start = record.hubStartedAt else { return .done }
        // Past the hub's window nothing is uploaded (it would answer `expired`); the keeper purges it.
        guard start >= now().addingTimeInterval(-Double(days()) * 86_400) else { return .done }
        if record.parts.isEmpty {
            let first = RecordingDayPart(hubID: id, part: 1, startedAt: start)
            await store.update(id) { if $0.parts.isEmpty { $0.parts = [first] } }
            guard let created = await store.record(id) else { return .done }
            record = created
        }
        notePartsIfNeeded(id, record)
        await readRecaps(id, record, api: api)
        var soonest: Date?
        var number = record.parts.map(\.part).min() ?? 1
        while let part = await store.record(id)?.parts.first(where: { $0.part == number }) {
            if !part.expired, !part.deleted {
                switch await visit(id, part: number, api: api, force: force) {
                case .done: break
                case .later(let at): soonest = min(soonest ?? at, at)
                case let other: return other
                }
            }
            number += 1
        }
        guard let latest = await store.record(id) else { return .done }
        if latest.parts.contains(where: \.created) { knownCreated.insert(id) }
        believed[id] = latest.parts.filter { $0.created && !$0.expired && !$0.deleted }.map {
            RecordingDayHubRow(id: $0.hubID, part: $0.part, next: $0.next, base: $0.base, recapState: $0.recapState)
        }
        finalDue[id] = latest.parts.filter { awaitsFinalRecap($0, of: latest) }.map { recapProbeAt[$0.hubID] ?? .distantPast }.min()
        if let expiry = RecordingDayCoverage.nextGapExpiry(record: latest, now: now()) { soonest = min(soonest ?? expiry, expiry) }
        return soonest.map(Outcome.later) ?? .done
    }

    /// One part's steps until it has nothing more to send now.
    private func visit(_ id: UUID, part number: Int, api: APIClient, force: Bool) async -> Outcome {
        var stale = 0
        for _ in 0..<Self.maxSteps {
            guard !Task.isCancelled, mayWork else { return .cancelled }
            guard let record = await store.record(id), let index = record.parts.firstIndex(where: { $0.part == number }) else { return .done }
            let part = record.parts[index]
            guard !part.expired, !part.deleted, record.problem == nil, !record.unsupportedUntilHello else { return .done }
            let isLast = record.parts.filter { !$0.expired && !$0.deleted }.map(\.part).max() == number
            let ended = force || record.endedAt != nil
            let step = RecordingDayUpload.step(record, partIndex: index, isLast: isLast, now: now(), lastAppend: lastAppend[part.hubID],
                                               sentMeta: sentMeta[part.hubID], force: ended)
            let flow: Flow
            switch step {
            case .done: return .done
            case .wait(let at): return .later(at)
            case .compact:
                await store.update(id) { record in
                    guard let i = record.parts.firstIndex(where: { $0.part == number }) else { return }
                    let next = record.parts[i].next
                    let count = RecordingDayUpload.compact(&record, part: number, next: next)
                    record.parts[i].count = count
                }
                continue
            case .assign:
                // On disk with the request that sends them first (every request flushes the day before it goes), so a
                // retry sends each sentence at the same position (C17).
                await store.update(id) { record in
                    guard let i = record.parts.firstIndex(where: { $0.part == number }), record.parts[i].pendingPut == nil else { return }
                    RecordingDayUpload.assign(&record, partIndex: i)
                }
                continue
            case .put:
                flow = await put(id, part: number, api: api, stale: &stale)
            case .append(let from, let to):
                flow = await append(id, part: number, from: from, to: min(to, from + (batch[part.hubID] ?? RecordingDayContract.maxAppendSegments)),
                                    api: api)
            case .meta:
                flow = await append(id, part: number, from: part.next, to: part.next, api: api)
            }
            switch flow {
            case .again: continue
            case .stop(let outcome): return outcome
            }
        }
        // A part still busy after this many steps: the rest goes with the next visit.
        return .later(now())
    }

    private nonisolated enum Flow: Sendable {
        case again
        case stop(Outcome)
    }

    // MARK: Requests

    /// A create or repair `PUT`: its revision and numbering are decided and on disk before it goes (C17); one decided
    /// before (a crash, a lost answer) goes again with its revision.
    private func put(_ id: UUID, part number: Int, api: APIClient, stale: inout Int) async -> Flow {
        guard let before = await store.record(id), let current = before.parts.first(where: { $0.part == number }) else { return .stop(.done) }
        let fresh = RecordingDayUpload.revision(current, now: now(), floor: hubRevision[current.hubID] ?? 0)
        await store.update(id) { record in
            guard let i = record.parts.firstIndex(where: { $0.part == number }) else { return }
            if record.parts[i].pendingPut == nil {
                let revision = max(fresh, record.parts[i].maxSentRevision + 1)
                record.parts[i].pendingPut = revision
                record.parts[i].maxSentRevision = revision
            }
            let count = RecordingDayUpload.renumber(&record, part: number)
            record.parts[i].count = count
        }
        guard await store.flush(id), let record = await store.record(id),
              let index = record.parts.firstIndex(where: { $0.part == number }), let revision = record.parts[index].pendingPut else {
            return .stop(.failed)
        }
        let part = record.parts[index]
        let isLast = record.parts.filter { !$0.expired && !$0.deleted }.map(\.part).max() == number
        let layout = RecordingDayUpload.layout(record, part: number)
        guard let segments = RecordingDayUpload.segments(record, part: part, placed: layout.placed, from: 0, to: part.count) else {
            return .stop(.failed)
        }
        let meta = RecordingDayUpload.meta(record, partIndex: index, isLast: isLast, sent: part.count, now: now())
        let language = library.entry(id)?.locale ?? RecordingDefaults.locale
        let body = RecordingDayPutBody(revision: revision, day: record.day, part: number, startedAt: part.startedAt,
                                       language: language, meta: meta, segments: segments)
        let data: Data
        do {
            data = try await send(id, api, "PUT", Self.path(part.hubID), body)
        } catch {
            return await react(RecordingDayReaction(error), id: id, part: number, appending: false)
        }
        // A 200 is the hub's word whatever its body; one it can't read counts as stored.
        let answer = try? JSON.decoder.decode(RecordingDayAnswer.self, from: data)
        if let revision = answer?.revision { hubRevision[part.hubID] = max(hubRevision[part.hubID] ?? 0, revision) }
        switch answer?.status {
        case "expired":
            await store.update(id) { record in
                guard let i = record.parts.firstIndex(where: { $0.part == number }) else { return }
                record.parts[i].expired = true
                record.parts[i].pendingPut = nil
            }
            return .again
        case "stale":
            // The hub holds a newer revision than this one: a higher one, and the same replacement again.
            stale += 1
            guard stale < Self.maxStale else { return .stop(.failed) }
            let floor = max(answer?.revision ?? 0, revision, hubRevision[part.hubID] ?? 0)
            hubRevision[part.hubID] = floor
            await store.update(id) { record in
                guard let i = record.parts.firstIndex(where: { $0.part == number }), record.parts[i].pendingPut == revision else { return }
                let bumped = max(floor, record.parts[i].maxSentRevision) + 1
                record.parts[i].pendingPut = bumped
                record.parts[i].maxSentRevision = bumped
            }
            return .again
        default:
            let sent = segments.count
            await store.update(id) { record in
                guard let i = record.parts.firstIndex(where: { $0.part == number }), record.parts[i].pendingPut == revision else { return }
                record.parts[i].created = true
                record.parts[i].base = revision
                record.parts[i].next = sent
                record.parts[i].pendingPut = nil
                record.parts[i].complete = meta.complete
            }
            sentMeta[part.hubID] = meta
            lastStored[part.hubID] = now()
            return .again
        }
    }

    /// An append of positions `from..<to` (none: only the other fields), on the base of the last stored `PUT`.
    private func append(_ id: UUID, part number: Int, from: Int, to: Int, api: APIClient) async -> Flow {
        guard let record = await store.record(id), let index = record.parts.firstIndex(where: { $0.part == number }),
              let base = record.parts[index].base else { return .stop(.done) }
        let part = record.parts[index]
        let isLast = record.parts.filter { !$0.expired && !$0.deleted }.map(\.part).max() == number
        let layout = RecordingDayUpload.layout(record, part: number)
        guard let segments = RecordingDayUpload.segments(record, part: part, placed: layout.placed, from: from, to: to) else {
            return .stop(.failed)
        }
        let revision = RecordingDayUpload.revision(part, now: now(), floor: hubRevision[part.hubID] ?? 0)
        // On disk before it goes, with any positions just assigned: after a crash and a clock set back, the next revision
        // is still above every one the hub may have seen (C17).
        await store.update(id) { record in
            guard let i = record.parts.firstIndex(where: { $0.part == number }) else { return }
            record.parts[i].maxSentRevision = max(record.parts[i].maxSentRevision, revision)
        }
        guard await store.flush(id) else { return .stop(.failed) }
        let meta = RecordingDayUpload.meta(record, partIndex: index, isLast: isLast, sent: to, now: now())
        let body = RecordingDayAppendBody(base: base, revision: revision, from: from, segments: segments, meta: meta)
        let data: Data
        do {
            data = try await send(id, api, "POST", Self.path(part.hubID) + "/segments", body)
        } catch {
            return await react(RecordingDayReaction(error), id: id, part: number, appending: true)
        }
        let answer = try? JSON.decoder.decode(RecordingDayAnswer.self, from: data)
        if let revision = answer?.revision { hubRevision[part.hubID] = max(hubRevision[part.hubID] ?? 0, revision) }
        if answer?.status == "expired" {
            // The day is past the hub's window, or was trimmed there: nothing of it is written any more.
            await store.update(id) { record in
                guard let i = record.parts.firstIndex(where: { $0.part == number }) else { return }
                record.parts[i].expired = true
            }
            return .again
        }
        let hubNext = answer?.next ?? to
        await store.update(id) { record in
            guard let i = record.parts.firstIndex(where: { $0.part == number }), record.parts[i].base == base else { return }
            if hubNext > record.parts[i].count {
                // The hub holds positions this phone never gave: only a replacement puts both in step again.
                record.parts[i].created = false
            } else {
                // Below what was sent only if the hub lost some: they go again from there.
                record.parts[i].next = hubNext
                if meta.complete, hubNext >= to { record.parts[i].complete = true }
            }
        }
        if hubNext >= to { sentMeta[part.hubID] = meta }
        lastAppend[part.hubID] = now()
        lastStored[part.hubID] = now()
        return .again
    }

    /// What a refusal or failure changes (C22). The part's state is written before anything is sent again.
    private func react(_ reaction: RecordingDayReaction, id: UUID, part number: Int, appending: Bool) async -> Flow {
        switch reaction {
        case .cancelled:
            return .stop(.cancelled)
        case .retry:
            return .stop(.failed)
        case .baseChanged(let base, let next):
            await store.update(id) { record in
                guard let i = record.parts.firstIndex(where: { $0.part == number }) else { return }
                if let base, base == record.parts[i].pendingPut {
                    // A replacement this phone decided on was stored, its answer lost: it is the base.
                    Self.adopt(&record.parts[i], base: base, next: next)
                } else {
                    // A base this phone can't vouch for (a restore, another device): replace the text.
                    record.parts[i].created = false
                }
            }
            return .again
        case .segmentsGap(let next):
            await store.update(id) { record in
                guard let i = record.parts.firstIndex(where: { $0.part == number }) else { return }
                if let next, next >= 0, next <= record.parts[i].count {
                    record.parts[i].next = next
                } else {
                    record.parts[i].created = false
                }
            }
            return .again
        case .notFound:
            await store.update(id) { record in
                guard let i = record.parts.firstIndex(where: { $0.part == number }) else { return }
                record.parts[i].created = false
            }
            return .again
        case .unsupported:
            unsupportedSeen.insert(id)
            await store.update(id) { $0.unsupportedUntilHello = true }
            return .stop(.blocked)
        case .tooLarge:
            guard appending, let hubID = await store.record(id)?.parts.first(where: { $0.part == number })?.hubID else {
                return await halt(id, problem: "too_large")
            }
            let size = batch[hubID] ?? RecordingDayContract.maxAppendSegments
            // One sentence alone over the limit can never go: the day stops with a problem rather than retry for ever.
            guard size > 1 else { return await halt(id, problem: "too_large") }
            batch[hubID] = size / 2
            return .again
        case .dayFull(let next):
            guard appending else { return await halt(id, problem: "day_full") }
            await startNextPart(id, after: number, hubNext: next)
            return .again
        case .conflict(let code):
            return await halt(id, problem: code)
        case .forbidden:
            paused = true
            return .stop(.blocked)
        case .deleted:
            await store.update(id) { record in
                guard let i = record.parts.firstIndex(where: { $0.part == number }) else { return }
                record.parts[i].deleted = true
                record.parts[i].pendingPut = nil
            }
            if await removeIfAllDeleted(id) { return .stop(.done) }
            return .again
        }
    }

    private func halt(_ id: UUID, problem: String) async -> Flow {
        await store.update(id) { $0.problem = problem }
        return .stop(.blocked)
    }

    private nonisolated static func adopt(_ part: inout RecordingDayPart, base: Int64, next: Int?) {
        part.created = true
        part.base = base
        part.pendingPut = nil
        if let next, next > part.count {
            part.created = false
        } else if let next, next >= 0 {
            part.next = next
        }
    }

    /// `413 day_full` (C18): positions from the hub's count on were not stored and go to a next part, which starts at
    /// the earliest of the sentences left over. The new part and its id are on disk before its `PUT`, so a crash creates
    /// no second one. The full part then gets one last append with its end and `complete`.
    private func startNextPart(_ id: UUID, after number: Int, hubNext: Int?) async {
        let hubID = UUID()
        let fallback = now()
        await store.update(id) { record in
            guard let i = record.parts.firstIndex(where: { $0.part == number }),
                  !record.parts.contains(where: { $0.part > number }) else { return }
            let held = max(0, min(hubNext ?? record.parts[i].next, record.parts[i].count))
            RecordingDayUpload.unplace(&record, part: number, from: held)
            let start = RecordingDayUpload.earliestUnplaced(record) ?? fallback
            record.parts[i].count = held
            record.parts[i].next = min(record.parts[i].next, held)
            record.parts[i].endedAt = max(start, record.parts[i].startedAt)
            record.parts.append(RecordingDayPart(hubID: hubID, part: number + 1, startedAt: max(start, record.parts[i].startedAt)))
        }
        _ = await store.flush(id)
        if let record = await store.record(id) { notePartsIfNeeded(id, record) }
    }

    /// The hub deleted these parts: marked; the day goes here too once none is left. True when it went.
    private func applyDeletes(_ hubIDs: Set<UUID>, to id: UUID) async -> Bool {
        await store.update(id) { record in
            for i in record.parts.indices where hubIDs.contains(record.parts[i].hubID) {
                record.parts[i].deleted = true
                record.parts[i].pendingPut = nil
            }
        }
        guard let record = await store.record(id) else { return true }
        // A day not uploaded in parts yet is the watch id's alone.
        if record.parts.isEmpty, hubIDs.contains(id) {
            removeLocally(id)
            return true
        }
        return await removeIfAllDeleted(id)
    }

    private func removeIfAllDeleted(_ id: UUID) async -> Bool {
        guard let record = await store.record(id), !record.parts.isEmpty, record.parts.allSatisfy(\.deleted) else { return false }
        removeLocally(id)
        return true
    }

    /// Deleted in the knowledge base (every part): the text there is the truth, so the day goes here too, like a
    /// recording deleted on the web (`RecordingSync`).
    private func removeLocally(_ id: UUID) {
        try? library.remove(id)
        removedHere(id)
    }

    /// Reconciliation's word on the day's parts: the recap's state, and the hub's base and count. On the same base a
    /// count below the phone's is sent again from there; another base, a count above the phone's, or a part the hub no
    /// longer lists (`absent`) is replaced. A base equal to a replacement decided on is that replacement, stored.
    private func apply(_ rows: [RecordingDayHubRow], listedAt: Date, to id: UUID) async {
        let byID = Dictionary(rows.map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first })
        for row in rows { if let revision = row.revision { hubRevision[row.id] = max(hubRevision[row.id] ?? 0, revision) } }
        let absentMatters = knownCreated.contains(id)
        let before = await store.record(id)?.parts ?? []
        // Parts with a request stored since about when the list was made: the list may not show it yet.
        let newer = Set(before.map(\.hubID).filter { hubID in
            lastStored[hubID].map { $0 >= listedAt.addingTimeInterval(-Self.listSlack) } ?? false
        })
        await store.update(id) { record in
            for i in record.parts.indices where !record.parts[i].expired && !record.parts[i].deleted {
                let fresh = newer.contains(record.parts[i].hubID)
                guard let row = byID[record.parts[i].hubID] else {
                    if absentMatters, !fresh, record.parts[i].created, record.parts[i].pendingPut == nil { record.parts[i].created = false }
                    continue
                }
                record.parts[i].recapState = row.recapState
                guard !fresh, let base = row.base, let next = row.next else { continue }
                if let pending = record.parts[i].pendingPut {
                    if base == pending { Self.adopt(&record.parts[i], base: base, next: next) }
                    continue
                }
                guard record.parts[i].created else { continue }
                if base != record.parts[i].base || next > record.parts[i].count {
                    record.parts[i].created = false
                } else if next < record.parts[i].next {
                    record.parts[i].next = next
                }
            }
        }
        // A recap that turned ready on a part the hub has whole: its detail says now whether it's the final one.
        for part in before where byID[part.hubID]?.recapState == "ready" && part.recapState != "ready" {
            recapProbeAt[part.hubID] = nil
        }
    }

    /// The final recap's time for each complete part whose recap is ready (C21), read from the detail: at once after
    /// `pending → ready` and once per launch, and every `recapProbeInterval` while it says the recap isn't final (the
    /// final one may be written between two reconciliations that both list `ready`). Starts the keeper's 3-day clock.
    private func readRecaps(_ id: UUID, _ record: RecordingDayRecord, api: APIClient) async {
        var found = false
        for part in record.parts where awaitsFinalRecap(part, of: record) && (recapProbeAt[part.hubID].map { $0 <= now() } ?? true) {
            // Not again before then, whatever the answer.
            recapProbeAt[part.hubID] = now().addingTimeInterval(Self.recapProbeInterval)
            let probe: RecordingDayRecapProbe
            do {
                probe = try await api.getDecodedOffMain("/api/v1/kb/recordings/\(part.hubID.uuidString.lowercased())",
                                                        as: RecordingDayRecapProbe.self)
            } catch {
                // Not read: the next visit tries again.
                recapProbeAt[part.hubID] = nil
                continue
            }
            guard let recap = probe.detail?.recap, recap.final == true else { continue }
            let at = recap.generatedAt.flatMap(JSON.parseDate) ?? now()
            let hubID = part.hubID
            await store.update(id) { record in
                guard let i = record.parts.firstIndex(where: { $0.hubID == hubID }) else { return }
                record.parts[i].recapFinalAt = at
            }
            found = true
        }
        if found { recapFinal() }
    }

    /// A complete part whose recap is ready but not known to be the final one, within the audio's days: after them its
    /// time changes nothing (`RecordingDayRetention` deletes the audio by then whatever the recap).
    private func awaitsFinalRecap(_ part: RecordingDayPart, of record: RecordingDayRecord) -> Bool {
        guard part.complete, part.recapState == "ready", part.recapFinalAt == nil, !part.deleted, !part.expired,
              let start = record.hubStartedAt else { return false }
        return now().timeIntervalSince(start) < (RecordingDayContract.audioMaxDays + 1) * 86_400
    }

    /// The day's hub ids past its first part, for `RecordingSync`'s deletes, once per change.
    private func notePartsIfNeeded(_ id: UUID, _ record: RecordingDayRecord) {
        let more = record.parts.filter { $0.hubID != id }.map(\.hubID)
        guard !more.isEmpty, noted[id] != more else { return }
        noted[id] = more
        noteParts(id, more)
    }

    private static func path(_ hubID: UUID) -> String { "/api/v1/recordings/\(hubID.uuidString.lowercased())" }

    /// One request for day `id`, shown as uploading while it is out. Sent off the main actor: a repair's body is the
    /// whole day's text, encoded where the UI isn't.
    private func send(_ id: UUID, _ api: APIClient, _ method: String, _ path: String,
                      _ body: some Encodable & Sendable) async throws -> Data {
        uploading = id
        defer { uploading = nil }
        let request = Task.detached(priority: .utility) { try await api.request(method, path, body: body, timeout: 60) }
        return try await withTaskCancellationHandler { try await request.value } onCancel: { request.cancel() }
    }
}

/// The little of a day's detail (`GET /api/v1/kb/recordings/{id}`) that says whether its recap is the final one.
nonisolated struct RecordingDayRecapProbe: Decodable, Sendable {
    nonisolated struct Detail: Decodable, Sendable {
        var recap: Recap?
    }

    nonisolated struct Recap: Decodable, Sendable {
        var final: Bool?
        var generatedAt: String?

        private nonisolated enum CodingKeys: String, CodingKey {
            case final
            case generatedAt = "generated_at"
        }
    }

    var detail: Detail?
}
