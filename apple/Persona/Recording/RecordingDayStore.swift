import Foundation
import Observation
import os

// The phone's side of all-day recording (docs/assistant/day-recap.md): what it holds of each day the watch records,
// and what the Recordings page reads of it.
//
// A day lives in the library's folder for its id (`<root>/<id>/`), next to the library's small index row
// (`recording.json`, `RecordingLibrary.adoptDay`), so deleting the recording deletes all of it:
// - `seg-NNN.m4a`: a chunk's audio, until retention deletes it;
// - `seg-NNN.json`: the chunk's header as the phone holds it (`RecordingDaySidecar`), written before the chunk is
//   acknowledged. This is what makes a chunk durable: everything else can be rebuilt from these;
// - `lost.json`: seqs the watch reported it could not keep (`RecordingDayLost`), also written before the acknowledgement;
// - `day.json`: the whole `RecordingDayRecord` (transcription, upload state), written by the store's queue at most every
//   couple of seconds and on `flush`, never on the main actor;
// - `journal.log`: the sidecars and loss list written since `day.json` was, so a recovery knows what to merge in without
//   trusting file dates; emptied by each write of `day.json`.
// Foundation and Observation only, so the Mac build compiles it too (the receiver that feeds it is iOS and visionOS
// only).

// MARK: - What the phone holds of a day

/// Where an appended sentence sits on the hub: its part and its position (the hub's `segments` order). The upload
/// assigns positions and persists them before its request, so a retry sends the same sentence at the same position.
nonisolated struct RecordingDayPosition: Codable, Sendable, Hashable {
    var part: Int
    var pos: Int
}

/// One chunk of a day recording as the phone holds it: the watch's header, when it first arrived, and how far
/// transcription and the upload got.
nonisolated struct RecordingDayChunk: Codable, Sendable, Equatable {
    /// Transcription: `waiting` until it runs, then `transcribed`, `skipped` (quieter than `silenceDBFS`) or `failed`
    /// (after `maxAttempts` tries). Markers (erased, lost, the end) stay `waiting` and need nothing (`needsTranscription`).
    nonisolated enum State: String, Codable, Sendable {
        case waiting, transcribed, skipped, failed

        /// A state a newer build wrote reads as `waiting`: transcribing it again loses nothing.
        init(from decoder: Decoder) throws {
            self = State(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .waiting
        }
    }

    var seq: Int
    /// The wall clock of the chunk's first sample.
    var startedAt: Date
    /// Seconds of audio; 0 for the header-only end marker.
    var duration: Double
    /// Mean RMS level in dBFS (`RecordingDayLevel`).
    var level: Double? = nil
    /// Why audio is missing just before this chunk (`RecordingDayContract.Reason`).
    var after: String? = nil
    var erased = false
    var lost = false
    var end = false
    /// When the phone first stored it (an update keeps the first time): how long a seq before it has been missing, and
    /// the latency the loopback test reads.
    var receivedAt: Date
    var marks: [RecordingDayHeader.Mark]? = nil
    var state: State = .waiting
    /// Transcription runs that failed on this chunk (`maxAttempts`).
    var attempts = 0
    /// The text, at seconds from `startedAt`; nil until transcribed, and again once erased.
    var sentences: [RecordingSentence]? = nil
    /// `seg-NNN.m4a` is on disk.
    var hasAudio = false
    /// Where each of `sentences` went on the hub, same order and count; nil until the upload assigns them. An erase
    /// drops them with the sentences, so a part whose `count` is above `positionsHeld(in:)` needs a repair `PUT`.
    var positions: [RecordingDayPosition]? = nil
}

/// One hub recording of a day: the first part has the watch's id, a day too long for one recording continues in more
/// (`413 day_full`). Kept and changed only by the upload (`RecordingDaySync`).
nonisolated struct RecordingDayPart: Codable, Sendable, Equatable {
    var hubID: UUID
    var part: Int
    var startedAt: Date
    var endedAt: Date? = nil
    /// The create `PUT` was stored.
    var created = false
    /// The revision of the last `PUT`, which appends name.
    var base: Int64? = nil
    /// Positions below this are on the hub; positions below `count` are assigned.
    var next = 0
    var count = 0
    var maxSentRevision: Int64 = 0
    /// A repair `PUT` decided on (its revision), sent again after a relaunch until stored.
    var pendingPut: Int64? = nil
    var complete = false
    /// The hub keeps no text of it any more (`recording_days`), or it was deleted there (`410`).
    var expired = false
    var deleted = false
    /// The hub's `recap_state`, and when its final recap was written (the audio's 3-day clock).
    var recapState: String? = nil
    var recapFinalAt: Date? = nil
}

/// Everything the phone holds of one day recording (`day.json`).
nonisolated struct RecordingDayRecord: Codable, Sendable, Equatable {
    var id: UUID
    /// The local date, `YYYY-MM-DD`, and the time zone it is a date in (the phone's when the day's first chunk arrived).
    var day: String
    var timeZone: String
    /// When the watch began recording the day (`recording_started_at`); nil from a watch that doesn't send it.
    var startedAt: Date? = nil
    var chunks: [Int: RecordingDayChunk] = [:]
    /// Seqs the watch reported lost by `RecordingDayLost` and the phone doesn't hold.
    var lost: Set<Int> = []
    var parts: [RecordingDayPart] = []
    /// The end marker's seq + 1: every seq below it exists.
    var expected: Int? = nil
    /// When the day recording ended (the end marker's time, or the end of a last chunk carrying `end`).
    var endedAt: Date? = nil
    /// Why the upload stopped for good (`409 not_day`…), for the day's page.
    var problem: String? = nil
    /// The hub refused the day routes (an older manager): nothing is sent until its next `hello`.
    var unsupportedUntilHello = false
}

// MARK: - Merging what the watch sends

nonisolated extension RecordingDayChunk {
    /// A chunk as it first arrives.
    init(header: RecordingDayHeader, receivedAt: Date) {
        self.init(seq: header.seq, startedAt: header.startedAt, duration: header.duration, level: header.level,
                  after: header.after, erased: header.erased, lost: header.lost, end: header.end, receivedAt: receivedAt,
                  marks: header.marks?.isEmpty == false ? header.marks : nil)
    }

    var endedAt: Date { startedAt.addingTimeInterval(duration) }
    /// No audio belongs to it: erased, lost, or the zero-length end marker (`RecordingDayHeader.isMarker`).
    var isMarker: Bool { erased || lost || duration <= 0 }
    /// Audio the transcriber still has to read.
    var needsTranscription: Bool { !isMarker && state == .waiting }
    /// Sentences the upload hasn't given a hub position yet (transcribed since its last append): text the hub doesn't
    /// have, whatever the parts' counters say.
    var hasUnplacedText: Bool { (positions?.count ?? 0) < (sentences?.count ?? 0) }

    /// The chunk after the watch sent `header` for it again. Only ever more final (C1/C11): an erase deletes the audio
    /// and the text and nothing brings them back, the end and the marks the watch adds later are kept, and a timing or
    /// audio the phone already holds never changes. A loss reported for a chunk the phone holds changes nothing either
    /// (the contract: a seq the phone holds "is acknowledged again and not stored twice"; a loss is reported "so the phone
    /// stops waiting"). Seqs are never reused, so the phone's copy is that chunk, whole. The watch reports a loss for a
    /// chunk it has no acknowledgement for whose audio it can't read or no longer has (`WatchDayTransfer.markLost`): the
    /// acknowledgement went missing and the resend's read failed, or the watch deleted its copy on the acknowledgement
    /// and its note of the delivery wasn't saved. Deleting the phone's copy then would turn a delivery hiccup into lost
    /// audio, text and hub text. The loss is acknowledged as a duplicate, so the watch forgets it.
    /// The other way round, audio arriving for a chunk the watch had reported lost (a file transfer the system was still
    /// delivering) is kept: it is more of the day, and the user erased none of it. Only an erase is final.
    func merged(with header: RecordingDayHeader) -> RecordingDayChunk {
        if lost, !erased, !header.isMarker {
            // `hasAudio` stays false until the store has written the audio.
            var found = RecordingDayChunk(header: header, receivedAt: receivedAt)
            found.end = found.end || end
            found.after = found.after ?? after
            found.marks = Self.union(marks, found.marks)
            return found
        }
        var next = self
        if header.erased, !erased {
            next.erased = true
            next.hasAudio = false
            next.sentences = nil
            next.positions = nil
        }
        if header.end { next.end = true }
        if next.after == nil { next.after = header.after }
        if next.level == nil { next.level = header.level }
        next.marks = Self.union(next.marks, header.marks)
        return next
    }

    /// The marks of both, each once, in the order they came; nil when there are none.
    private static func union(_ held: [RecordingDayHeader.Mark]?, _ new: [RecordingDayHeader.Mark]?) -> [RecordingDayHeader.Mark]? {
        let all = (held ?? []) + (new ?? []).filter { !(held ?? []).contains($0) }
        return all.isEmpty ? nil : all
    }

    /// The header this chunk is now, for its sidecar.
    func header(recording: UUID, day: String, recordingStartedAt: Date?) -> RecordingDayHeader {
        RecordingDayHeader(recording: recording, day: day, seq: seq, startedAt: startedAt, duration: duration, level: level,
                           erased: erased, end: end, after: after, marks: marks, recordingStartedAt: recordingStartedAt, lost: lost)
    }
}

nonisolated extension RecordingDayRecord {
    /// The chunk `header` leaves for its seq: a new one, the held one made more final, or nil when nothing changes (a
    /// duplicate, acknowledged again and not stored twice).
    func merging(_ header: RecordingDayHeader, receivedAt: Date) -> RecordingDayChunk? {
        guard let held = chunks[header.seq] else { return RecordingDayChunk(header: header, receivedAt: receivedAt) }
        let merged = held.merged(with: header)
        return merged == held ? nil : merged
    }

    /// Puts a chunk in, with what it tells about the whole recording: its start, and its end once the last chunk came.
    mutating func put(_ chunk: RecordingDayChunk, recordingStartedAt: Date?) {
        chunks[chunk.seq] = chunk
        lost.remove(chunk.seq)
        if let start = recordingStartedAt { startedAt = min(startedAt ?? start, start) }
        // A header-only end marker ends at its start, a last chunk with audio at its end.
        if chunk.end, chunk.seq + 1 >= (expected ?? 0) {
            expected = chunk.seq + 1
            endedAt = chunk.endedAt
        }
    }

    /// An erase is final whatever an edit writes (C11): a chunk the watch erased stays erased and keeps no text, hub
    /// positions or audio. A transcription that started before the erase arrived and saves its text after it is the
    /// case this is for; an edit that drops an erased chunk gets it back, since its seq must stay accounted for.
    mutating func keepErasures(of held: RecordingDayRecord) {
        for (seq, chunk) in held.chunks where chunk.erased {
            var kept = chunks[seq] ?? chunk
            kept.erased = true
            kept.hasAudio = false
            kept.sentences = nil
            kept.positions = nil
            chunks[seq] = kept
        }
    }

    /// The hub's `started_at`: when the watch began recording the day, else the earliest chunk the phone holds.
    var hubStartedAt: Date? { startedAt ?? chunks.values.map(\.startedAt).min() }

    /// Sentence positions still held in a part: below the part's `count`, sentences the hub has were dropped here.
    func positionsHeld(in part: Int) -> Int {
        chunks.values.reduce(0) { sum, chunk in sum + (chunk.positions ?? []).count(where: { $0.part == part }) }
    }

    /// The hub's copy of the text differs from the phone's, or soon will: the day isn't created there, a part has a
    /// repair `PUT` waiting or positions assigned but not sent, sentences have no position yet, or an erase dropped text
    /// a part still holds (a repair is due before the upload has decided on it). Parts the hub no longer keeps text of
    /// are left out: nothing more goes to them.
    var hasUnsentText: Bool {
        guard !parts.isEmpty else { return !chunks.isEmpty }
        let live = parts.filter { !$0.expired && !$0.deleted }
        guard !live.isEmpty else { return false }
        if live.contains(where: { !$0.created || $0.pendingPut != nil || $0.next < $0.count }) { return true }
        if chunks.values.contains(where: \.hasUnplacedText) { return true }
        return live.contains { positionsHeld(in: $0.part) < $0.count }
    }

    /// The day is over, every seq below its end arrived (or was reported lost), all of it is transcribed, and the hub
    /// has it all.
    var isComplete: Bool {
        guard endedAt != nil, let expected else { return false }
        let settled = (0..<expected).allSatisfy { seq in chunks[seq].map { !$0.needsTranscription } ?? lost.contains(seq) }
        return settled && !hasUnsentText
    }

    /// The recap's state across the day's parts: failed when one failed, else pending while one is being written, else
    /// ready; nil before any is due. An unknown state of the hub's is shown as it is.
    var recapState: String? {
        let states = parts.filter { !$0.deleted }.compactMap(\.recapState)
        for state in ["failed", "pending", "ready"] where states.contains(state) { return state }
        return states.last
    }

    /// The hub recordings of the day, in part order.
    var hubIDs: [UUID] { parts.filter { !$0.deleted }.sorted { $0.part < $1.part }.map(\.hubID) }
}

// MARK: - day.json

// Hand-written so that a field a newer build adds, or one missing from an older file, never makes the whole day
// unreadable: a day that can't be read is rebuilt from its sidecars, without its transcript.

nonisolated extension RecordingDayChunk {
    private nonisolated enum CodingKeys: String, CodingKey {
        case seq, startedAt, duration, level, after, erased, lost, end, receivedAt, marks, state, attempts, sentences, hasAudio, positions
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.init(seq: try c.decode(Int.self, forKey: .seq),
                  startedAt: try c.decode(Date.self, forKey: .startedAt),
                  duration: try c.decode(Double.self, forKey: .duration),
                  level: try c.decodeIfPresent(Double.self, forKey: .level),
                  after: try c.decodeIfPresent(String.self, forKey: .after),
                  erased: try c.decodeIfPresent(Bool.self, forKey: .erased) ?? false,
                  lost: try c.decodeIfPresent(Bool.self, forKey: .lost) ?? false,
                  end: try c.decodeIfPresent(Bool.self, forKey: .end) ?? false,
                  receivedAt: try c.decodeIfPresent(Date.self, forKey: .receivedAt) ?? .distantPast,
                  marks: try? c.decodeIfPresent([RecordingDayHeader.Mark].self, forKey: .marks),
                  state: try c.decodeIfPresent(State.self, forKey: .state) ?? .waiting,
                  attempts: try c.decodeIfPresent(Int.self, forKey: .attempts) ?? 0,
                  sentences: try c.decodeIfPresent([RecordingSentence].self, forKey: .sentences),
                  hasAudio: try c.decodeIfPresent(Bool.self, forKey: .hasAudio) ?? false,
                  positions: try? c.decodeIfPresent([RecordingDayPosition].self, forKey: .positions))
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(seq, forKey: .seq)
        try c.encode(startedAt, forKey: .startedAt)
        try c.encode(duration, forKey: .duration)
        try c.encodeIfPresent(level.flatMap { $0.isFinite ? $0 : nil }, forKey: .level)
        try c.encodeIfPresent(after, forKey: .after)
        try c.encode(erased, forKey: .erased)
        try c.encode(lost, forKey: .lost)
        try c.encode(end, forKey: .end)
        try c.encode(receivedAt, forKey: .receivedAt)
        try c.encodeIfPresent(marks, forKey: .marks)
        try c.encode(state, forKey: .state)
        try c.encode(attempts, forKey: .attempts)
        try c.encodeIfPresent(sentences, forKey: .sentences)
        try c.encode(hasAudio, forKey: .hasAudio)
        try c.encodeIfPresent(positions, forKey: .positions)
    }
}

nonisolated extension RecordingDayPart {
    private nonisolated enum CodingKeys: String, CodingKey {
        case hubID, part, startedAt, endedAt, created, base, next, count, maxSentRevision, pendingPut, complete, expired, deleted
        case recapState, recapFinalAt
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.init(hubID: try c.decode(UUID.self, forKey: .hubID),
                  part: try c.decode(Int.self, forKey: .part),
                  startedAt: try c.decode(Date.self, forKey: .startedAt),
                  endedAt: try c.decodeIfPresent(Date.self, forKey: .endedAt),
                  created: try c.decodeIfPresent(Bool.self, forKey: .created) ?? false,
                  base: try c.decodeIfPresent(Int64.self, forKey: .base),
                  next: try c.decodeIfPresent(Int.self, forKey: .next) ?? 0,
                  count: try c.decodeIfPresent(Int.self, forKey: .count) ?? 0,
                  maxSentRevision: try c.decodeIfPresent(Int64.self, forKey: .maxSentRevision) ?? 0,
                  pendingPut: try c.decodeIfPresent(Int64.self, forKey: .pendingPut),
                  complete: try c.decodeIfPresent(Bool.self, forKey: .complete) ?? false,
                  expired: try c.decodeIfPresent(Bool.self, forKey: .expired) ?? false,
                  deleted: try c.decodeIfPresent(Bool.self, forKey: .deleted) ?? false,
                  recapState: try c.decodeIfPresent(String.self, forKey: .recapState),
                  recapFinalAt: try c.decodeIfPresent(Date.self, forKey: .recapFinalAt))
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(hubID, forKey: .hubID)
        try c.encode(part, forKey: .part)
        try c.encode(startedAt, forKey: .startedAt)
        try c.encodeIfPresent(endedAt, forKey: .endedAt)
        try c.encode(created, forKey: .created)
        try c.encodeIfPresent(base, forKey: .base)
        try c.encode(next, forKey: .next)
        try c.encode(count, forKey: .count)
        try c.encode(maxSentRevision, forKey: .maxSentRevision)
        try c.encodeIfPresent(pendingPut, forKey: .pendingPut)
        try c.encode(complete, forKey: .complete)
        try c.encode(expired, forKey: .expired)
        try c.encode(deleted, forKey: .deleted)
        try c.encodeIfPresent(recapState, forKey: .recapState)
        try c.encodeIfPresent(recapFinalAt, forKey: .recapFinalAt)
    }
}

nonisolated extension RecordingDayRecord {
    /// The layout of `day.json` this build writes.
    static let fileVersion = 1

    private nonisolated enum CodingKeys: String, CodingKey {
        case version, id, day, timeZone, startedAt, chunks, lost, parts, expected, endedAt, problem, unsupportedUntilHello
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        // Chunks are a list in seq order on disk (a dictionary keyed by Int would be one flat array of keys and values).
        let chunks = try c.decodeIfPresent([RecordingDayChunk].self, forKey: .chunks) ?? []
        self.init(id: try c.decode(UUID.self, forKey: .id),
                  day: try c.decode(String.self, forKey: .day),
                  timeZone: try c.decode(String.self, forKey: .timeZone),
                  startedAt: try c.decodeIfPresent(Date.self, forKey: .startedAt),
                  chunks: Dictionary(chunks.map { ($0.seq, $0) }, uniquingKeysWith: { _, last in last }),
                  lost: Set(try c.decodeIfPresent([Int].self, forKey: .lost) ?? []),
                  parts: try c.decodeIfPresent([RecordingDayPart].self, forKey: .parts) ?? [],
                  expected: try c.decodeIfPresent(Int.self, forKey: .expected),
                  endedAt: try c.decodeIfPresent(Date.self, forKey: .endedAt),
                  problem: try c.decodeIfPresent(String.self, forKey: .problem),
                  unsupportedUntilHello: try c.decodeIfPresent(Bool.self, forKey: .unsupportedUntilHello) ?? false)
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(Self.fileVersion, forKey: .version)
        try c.encode(id, forKey: .id)
        try c.encode(day, forKey: .day)
        try c.encode(timeZone, forKey: .timeZone)
        try c.encodeIfPresent(startedAt, forKey: .startedAt)
        try c.encode(chunks.values.sorted { $0.seq < $1.seq }, forKey: .chunks)
        try c.encode(lost.sorted(), forKey: .lost)
        try c.encode(parts, forKey: .parts)
        try c.encodeIfPresent(expected, forKey: .expected)
        try c.encodeIfPresent(endedAt, forKey: .endedAt)
        try c.encodeIfPresent(problem, forKey: .problem)
        try c.encode(unsupportedUntilHello, forKey: .unsupportedUntilHello)
    }
}

/// A chunk's durable record on the phone (`seg-NNN.json`, beside its audio), written before the chunk is acknowledged:
/// the header as the phone holds it (an erase or the end merged in), when it first arrived, and the phone's time zone
/// then. `day.json` is rebuilt from these when it is missing or older.
nonisolated struct RecordingDaySidecar: Codable, Sendable, Equatable {
    var header: RecordingDayHeader
    var receivedAt: Date
    var timeZone: String?
}

nonisolated extension RecordingDaySidecar {
    private nonisolated enum CodingKeys: String, CodingKey {
        case header
        case receivedAt = "received_at"
        case timeZone = "time_zone"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.init(header: try c.decode(RecordingDayHeader.self, forKey: .header),
                  receivedAt: Date(timeIntervalSince1970: try c.decode(Double.self, forKey: .receivedAt)),
                  timeZone: try? c.decodeIfPresent(String.self, forKey: .timeZone))
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(header, forKey: .header)
        try c.encode(receivedAt.timeIntervalSince1970, forKey: .receivedAt)
        try c.encodeIfPresent(timeZone, forKey: .timeZone)
    }
}

// MARK: - The store

/// The phone's all-day chunks and everything known about them, behind one serial queue: the receiver's stores, the
/// transcriber's and the upload's edits, retention and deletion all happen in one order, so nothing older ever lands
/// after something newer, and nothing written for a day recreates it after the user deleted it (C14, C29).
///
/// The receiver's calls are synchronous, from WatchConnectivity's queue: a chunk is on disk (audio, then its sidecar)
/// when they return, and only then acknowledged. Everyone else awaits. Never call the synchronous ones on the main actor.
nonisolated final class RecordingDayStore: @unchecked Sendable {
    nonisolated enum StoreResult: Sendable, Equatable {
        /// A seq the phone didn't hold.
        case stored
        /// A held seq made more final (erased after its audio came, the end flag): the transcript and the hub may need
        /// repairing.
        case updated
        /// Already held as it is: acknowledged again, stored once.
        case duplicate
        /// The day was deleted here: acknowledged, so the watch lets go of it, and nothing kept.
        case refused
    }

    nonisolated enum Failure: Error, Equatable {
        /// Not a header the watch could have written.
        case invalidHeader
        /// A chunk that should carry audio came without it.
        case missingAudio
    }

    static let shared = RecordingDayStore(root: RecordingLibrary.directory, isDeleted: { id in
        RecordingDayStore.appLibrary.withLock { $0 }?.isDeletedSnapshot(id) ?? false
    })

    /// The app library whose tombstones `shared` checks. It is the main actor's, so `shared` can't reach it by itself;
    /// `RecordingDayReceiver.start()` hands it over at launch, before WatchConnectivity can deliver any chunk.
    private static let appLibrary = OSAllocatedUnfairLock<RecordingLibrary?>(initialState: nil)

    @MainActor static func useTombstones(of library: RecordingLibrary) {
        appLibrary.withLock { $0 = library }
    }

    let root: URL
    private let isDeleted: @Sendable (UUID) -> Bool
    private let zone: @Sendable () -> TimeZone
    private let now: @Sendable () -> Date
    private let writeDelay: TimeInterval
    private let queue = DispatchQueue(label: "persona.recording-day-store", qos: .utility)
    private let subscribers = OSAllocatedUnfairLock(initialState: [UUID: AsyncStream<UUID>.Continuation]())

    // Only touched on `queue`.
    private var cache: [UUID: RecordingDayRecord] = [:]
    private var lastUse: [UUID: Int] = [:]
    private var uses = 0
    private var dirty: Set<UUID> = []
    private var writeScheduled = false
    /// Days this process has brought up to their folder (`reconcile`): every sidecar it writes for them afterwards stays
    /// in `cache` until a later `day.json` holds it, so from then on `day.json` alone is current.
    private var reconciled: Set<UUID> = []
    /// Every folder was brought up to date once (`sweep`): from then on a day without `day.json` is one this process
    /// holds in `cache`.
    private var swept = false
    /// Days whose folder held more than their `day.json` (or had none), not yet returned by `recoverOnLaunch`.
    private var unreported: Set<UUID> = []

    /// Days kept decoded at once (today's, the one transcribing, the one uploading, the one on screen); a day is a few
    /// MB of text, and fourteen are kept.
    private static let cacheLimit = 4
    private static let dayFile = "day.json"
    private static let lostFile = "lost.json"
    private static let unreadableFile = "day-unreadable.json"
    private static let journalFile = "journal.log"
    /// The journal's line for a write of `lost.json` (the others are seqs).
    private static let lostEntry = "lost"
    /// How much earlier than `day.json` a sidecar may be dated and still be merged in as possibly newer. The journal is
    /// what recovery relies on; file dates only add to it, for a journal line a crash of the whole system lost.
    private static let clockSlack: TimeInterval = 5
    /// Chunks arrive while the phone is locked in a pocket, and are transcribed then: every file of a day stays readable
    /// and writable once the phone was unlocked after a restart, whatever the app's default protection becomes.
    private static let writing: Data.WritingOptions = [.atomic, .completeFileProtectionUntilFirstUserAuthentication]

    /// - Parameters:
    ///   - root: the recordings folder (the library's).
    ///   - isDeleted: the library's tombstones, readable from any thread (`RecordingLibrary.isDeletedSnapshot`).
    ///   - zone: the time zone a new day is a date in.
    ///   - writeDelay: how long changes gather before `day.json` is written.
    init(root: URL, isDeleted: @escaping @Sendable (UUID) -> Bool, zone: @escaping @Sendable () -> TimeZone = { .current },
         now: @escaping @Sendable () -> Date = { .now }, writeDelay: TimeInterval = 2) {
        self.root = root
        self.isDeleted = isDeleted
        self.zone = zone
        self.now = now
        self.writeDelay = writeDelay
    }

    func folder(_ id: UUID) -> URL { root.appendingPathComponent(id.uuidString, isDirectory: true) }
    func audioURL(_ id: UUID, seq: Int) -> URL { folder(id).appendingPathComponent(RecordingSegment.fileName(seq)) }
    private func sidecarURL(_ id: UUID, seq: Int) -> URL { folder(id).appendingPathComponent(String(format: "seg-%03d.json", seq)) }
    private func dayURL(_ id: UUID) -> URL { folder(id).appendingPathComponent(Self.dayFile) }
    private func lostURL(_ id: UUID) -> URL { folder(id).appendingPathComponent(Self.lostFile) }
    private func journalURL(_ id: UUID) -> URL { folder(id).appendingPathComponent(Self.journalFile) }

    // MARK: Receiver side (synchronous, any queue but the main one)

    /// A chunk or marker from `sendMessageData` (`audio`: the frame's audio part; nil or empty for a marker).
    func store(header: RecordingDayHeader, audio: Data?) throws -> StoreResult {
        try queue.sync { try commit(header, audio: audio.map(Audio.data)) }
    }

    /// A chunk from `transferFile`: the file is moved in before this returns (the system deletes it right after).
    func store(header: RecordingDayHeader, movingFile: URL) throws -> StoreResult {
        try queue.sync { try commit(header, audio: .file(movingFile)) }
    }

    /// Seqs the watch could not keep: written down (`lost.json`) before this returns, so the phone stops waiting for them
    /// even after a crash. Seqs the phone holds are left out: it has them.
    func recordLost(_ lost: RecordingDayLost) throws {
        try queue.sync { try commitLost(lost) }
    }

    // MARK: Everyone else

    /// The day as the phone holds it; nil for a day it doesn't have, or one deleted.
    func record(_ id: UUID) async -> RecordingDayRecord? {
        await onQueue { self.forgetIfDeleted(id) ? nil : self.load(id) }
    }

    /// Changes a day (the transcriber's text, the upload's state), on the store's queue against its newest state. An edit
    /// that changes nothing writes and announces nothing; a day the phone doesn't hold (or deleted) is left alone. An
    /// erase stays final whatever the edit writes (`keepErasures`). Written with the next coalesced write; `flush` when it
    /// must be on disk before something else happens.
    func update(_ id: UUID, _ edit: @escaping @Sendable (inout RecordingDayRecord) -> Void) async {
        await onQueue { self.apply(edit, to: id) }
    }

    /// Writes the day's `day.json` now if it changed since the last write, and returns once it is on disk: true then (or
    /// when there was nothing to write), false when the write failed or the day is gone. The upload sends positions it
    /// assigned only after this said true (C17).
    @discardableResult func flush(_ id: UUID) async -> Bool {
        await onQueue { self.write(id) }
    }

    /// Writes every day changed since its last write (the app is about to be suspended).
    func flushAll() async {
        await onQueue { self.writeDirty() }
    }

    /// Deletes these chunks' audio (retention); their records, text and sidecars stay.
    func removeAudio(_ id: UUID, seqs: [Int]) async {
        await onQueue { self.dropAudio(id, seqs: seqs) }
    }

    /// Every day the phone holds, deleted ones left out. The first call in a process brings every folder up to date first
    /// (what `recoverOnLaunch` does), so a day a crash left with sidecars only is listed even before the launch's
    /// recovery ran (a background task that starts before it).
    func ids() async -> [UUID] {
        await onQueue {
            if !self.swept { self.sweep() }
            return self.dayIDs()
        }
    }

    /// At launch: brings each day's record up to its folder. A crash can leave chunks that were stored and acknowledged
    /// only in their sidecars (no `day.json` yet, or an older one), audio whose sidecar was never written (never
    /// acknowledged: the watch sends it again), or audio an erase didn't get to delete. A day something reads first (a
    /// chunk WatchConnectivity delivers before this runs) is brought up to its folder by that read, and reported here
    /// too. Returns the days that changed and weren't returned before; their `day.json` is written before this returns.
    func recoverOnLaunch() async -> [UUID] {
        await onQueue { self.recoverAll() }
    }

    /// The library deleted this day (`RecordingLibrary.remove`, which tombstones it first): forgets it now and tells
    /// everyone following `changes`, so its summary, transcription and upload let go of it without waiting for their next
    /// read. Callable from any thread.
    /// Every delete of a day goes through `RecordingDaySync.removedHere`, which calls this.
    func didRemove(_ id: UUID) {
        queue.async {
            _ = self.forgetIfDeleted(id)
            self.emit(id)
        }
    }

    /// Lets go of every decoded day that is on disk as it is (a memory warning); each is read again when next needed.
    /// Callable from any thread.
    func trimMemory() {
        queue.async {
            for id in Array(self.cache.keys) where !self.dirty.contains(id) {
                self.cache[id] = nil
                self.lastUse[id] = nil
            }
        }
    }

    /// The id of each day that changed (a chunk stored or updated, an edit, a recovery), for as long as the stream is
    /// iterated. Each access is a new subscription, so the transcriber, the upload and the summaries each get every
    /// change. Bursts are not coalesced here.
    var changes: AsyncStream<UUID> {
        let (stream, continuation) = AsyncStream.makeStream(of: UUID.self)
        let key = UUID()
        subscribers.withLock { $0[key] = continuation }
        continuation.onTermination = { @Sendable [weak self] _ in
            self?.subscribers.withLock { $0[key] = nil }
        }
        return stream
    }

    // MARK: On the queue

    private nonisolated enum Audio {
        case data(Data)
        case file(URL)

        var isEmpty: Bool {
            if case .data(let data) = self { return data.isEmpty }
            return false
        }
    }

    private func onQueue<T: Sendable>(_ work: @escaping @Sendable () -> T) async -> T {
        await withCheckedContinuation { continuation in
            queue.async { continuation.resume(returning: work()) }
        }
    }

    private func commit(_ header: RecordingDayHeader, audio: Audio?) throws -> StoreResult {
        guard header.valid else { throw Failure.invalidHeader }
        let id = header.recording
        guard !forgetIfDeleted(id) else { return .refused }
        var record = load(id) ?? newRecord(for: header)
        let held = record.chunks[header.seq]
        guard var chunk = record.merging(header, receivedAt: now()) else {
            // Sent again: an erase whose audio deletion failed or was cut short is finished before it is acknowledged.
            if let held, held.isMarker { try deleteAudioFile(id, seq: held.seq) }
            return .duplicate
        }
        // Audio is taken for a new chunk, or one the watch had reported lost; never again for a chunk the phone holds
        // (retention may have deleted its audio on purpose).
        if !chunk.isMarker, held?.isMarker ?? true {
            // The audio first: a sidecar is never without its audio. A crash in between leaves audio without a sidecar,
            // which was never acknowledged; the resend replaces it, and recovery deletes it.
            guard let audio, !audio.isEmpty else { throw Failure.missingAudio }
            try makeFolder(id)
            try put(audio, at: audioURL(id, seq: header.seq))
            chunk.hasAudio = true
        }
        try writeSidecar(chunk, of: record, recordingStartedAt: header.recordingStartedAt ?? record.startedAt)
        record.put(chunk, recordingStartedAt: header.recordingStartedAt)
        // A delete that landed while this wrote: the library removed the folder (or is about to), and what was just
        // written must not bring the day back.
        guard !removeIfDeleted(id) else { return .refused }
        cache[id] = record
        touch(id)
        markDirty(id)
        emit(id)
        // Last, once the record holds the erase, so its text is gone whatever happens to the file. An erase is
        // acknowledged only with its audio deleted: when that fails this throws, and the marker the watch sends again
        // tries once more (as does recovery after a crash here).
        if chunk.isMarker { try deleteAudioFile(id, seq: header.seq) }
        return held == nil ? .stored : .updated
    }

    private func commitLost(_ report: RecordingDayLost) throws {
        let id = report.recording
        guard !forgetIfDeleted(id) else { return }
        let loaded = load(id)
        let seqs = Set(report.seqs).subtracting(loaded.map { Array($0.chunks.keys) } ?? [])
        guard !seqs.isEmpty else { return }
        let known = readLostFile(id)
        if !seqs.isSubset(of: known) {
            try makeFolder(id)
            try note(Self.lostEntry, in: id)
            try JSONEncoder().encode(known.union(seqs).sorted()).write(to: lostURL(id), options: Self.writing)
        }
        guard !removeIfDeleted(id) else { return }
        // Without a chunk yet the day can't be named; the first one that arrives reads the list (`newRecord`).
        guard var record = loaded, !seqs.isSubset(of: record.lost) else { return }
        record.lost.formUnion(seqs)
        cache[id] = record
        touch(id)
        markDirty(id)
        emit(id)
    }

    private func newRecord(for header: RecordingDayHeader) -> RecordingDayRecord {
        var record = RecordingDayRecord(id: header.recording, day: header.day, timeZone: zone().identifier)
        record.lost = readLostFile(header.recording)
        return record
    }

    /// The day's folder, created when missing with the protection its files get (`writing`).
    private func makeFolder(_ id: UUID) throws {
        try FileManager.default.createDirectory(at: folder(id), withIntermediateDirectories: true,
                                                attributes: [.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication])
    }

    private func put(_ audio: Audio, at url: URL) throws {
        switch audio {
        case .data(let data):
            try data.write(to: url, options: Self.writing)
        case .file(let source):
            // A copy from an earlier try whose sidecar was never written would stop the move.
            if FileManager.default.fileExists(atPath: url.path) { try FileManager.default.removeItem(at: url) }
            try FileManager.default.moveItem(at: source, to: url)
            // A moved file keeps the protection it was delivered with; the transcriber reads it while the phone is locked.
            try? FileManager.default.setAttributes([.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication],
                                                   ofItemAtPath: url.path)
        }
    }

    /// The chunk's sidecar, noted in the journal first: a crash between the two leaves a line for a sidecar that is older
    /// or missing, which merging ignores.
    private func writeSidecar(_ chunk: RecordingDayChunk, of record: RecordingDayRecord, recordingStartedAt: Date?) throws {
        let header = chunk.header(recording: record.id, day: record.day, recordingStartedAt: recordingStartedAt)
        let sidecar = RecordingDaySidecar(header: header, receivedAt: chunk.receivedAt, timeZone: record.timeZone)
        try makeFolder(record.id)
        try note(String(chunk.seq), in: record.id)
        try JSONEncoder().encode(sidecar).write(to: sidecarURL(record.id, seq: chunk.seq), options: Self.writing)
    }

    /// Appends a line to the day's journal (a seq whose sidecar is about to be written, or `lostEntry`): what `day.json`
    /// may not hold yet, for `reconcile`. File dates can't say that: the clock that dates them can be set back between two
    /// writes, and a sidecar dated before `day.json` would be skipped, an erase in it with it. The folder exists.
    private func note(_ entry: String, in id: UUID) throws {
        let line = Data((entry + "\n").utf8)
        guard let handle = try? FileHandle(forWritingTo: journalURL(id)) else {
            // None yet: `day.json` was written since the last line, or never was.
            try line.write(to: journalURL(id), options: Self.writing)
            return
        }
        defer { try? handle.close() }
        try handle.seekToEnd()
        try handle.write(contentsOf: line)
    }

    /// What the journal names: the seqs whose sidecars changed since `day.json` was written, and whether the loss list
    /// did. A line a crash cut short is skipped.
    private func readJournal(_ id: UUID) -> (seqs: Set<Int>, lost: Bool) {
        guard let data = try? Data(contentsOf: journalURL(id)) else { return ([], false) } // perf-ok: store queue
        var seqs: Set<Int> = []
        var lost = false
        for line in String(decoding: data, as: UTF8.self).split(separator: "\n") {
            if line == Self.lostEntry {
                lost = true
            } else if let seq = Int(line), seq >= 0 {
                seqs.insert(seq)
            }
        }
        return (seqs, lost)
    }

    /// Deletes a chunk's audio; done when the file is gone, whether this deleted it or it never was there. Any other
    /// failure throws: an erase must not be acknowledged while its audio is still on the phone.
    private func deleteAudioFile(_ id: UUID, seq: Int) throws {
        let url = audioURL(id, seq: seq)
        do {
            try FileManager.default.removeItem(at: url)
        } catch {
            guard FileManager.default.fileExists(atPath: url.path) else { return }
            throw error
        }
    }

    private func apply(_ edit: (inout RecordingDayRecord) -> Void, to id: UUID) {
        guard !forgetIfDeleted(id), let record = load(id) else { return }
        var next = record
        edit(&next)
        next.id = id
        next.keepErasures(of: record)
        guard next != record else { return }
        cache[id] = next
        touch(id)
        markDirty(id)
        emit(id)
    }

    private func dropAudio(_ id: UUID, seqs: [Int]) {
        guard !forgetIfDeleted(id), var record = load(id) else { return }
        var changed = false
        for seq in Set(seqs) {
            try? FileManager.default.removeItem(at: audioURL(id, seq: seq))
            if record.chunks[seq]?.hasAudio == true {
                record.chunks[seq]?.hasAudio = false
                changed = true
            }
        }
        guard changed else { return }
        cache[id] = record
        dirty.insert(id)
        // Written at once: a recovery after a crash trusts a current day.json, which would still say the audio is there.
        write(id)
        emit(id)
    }

    // MARK: Loading, recovery

    /// The day from memory, else from its folder. The first read of a day in this process brings it up to its folder
    /// (`reconcile`), whether the launch's recovery gets there first or a chunk, an edit or a reader does: a crash can
    /// leave sidecars newer than `day.json`, and a chunk merged into the older record, or an edit written over it, would
    /// bury them, an erase among them (C11/C14). Later reads trust `day.json`, else rebuild the day from its sidecars.
    private func load(_ id: UUID) -> RecordingDayRecord? {
        if let record = cache[id] {
            touch(id)
            return record
        }
        let reconciliation = reconciled.contains(id) ? nil : reconcile(id)
        let read = reconciliation?.record ?? readDayFile(id)
        let rebuilt = read == nil ? rebuild(id) : nil
        guard let record = read ?? rebuilt else { return nil }
        cache[id] = record
        touch(id)
        if reconciliation?.changed == true {
            // Written and announced like the days `recoverOnLaunch` recovers, and reported by it.
            unreported.insert(id)
            markDirty(id)
            emit(id)
        } else if rebuilt != nil {
            markDirty(id)
        }
        trimCache()
        return record
    }

    /// A day without a readable `day.json`, from all its sidecars: the app ended before the day's first write, or a
    /// build that reads it differently wrote it (`readDayFile` set it aside).
    private func rebuild(_ id: UUID) -> RecordingDayRecord? {
        let listing = list(id)
        guard listing.isDay else { return nil }
        var record: RecordingDayRecord?
        merge(listing, seqs: nil, into: &record, id)
        return record
    }

    private func readDayFile(_ id: UUID) -> RecordingDayRecord? {
        guard let data = try? Data(contentsOf: dayURL(id)) else { return nil } // perf-ok: store queue
        if let record = try? Self.dayDecoder().decode(RecordingDayRecord.self, from: data), record.id == id { return record }
        // Written by a build that reads it differently: kept aside, not overwritten, and the day rebuilt from its sidecars.
        let aside = folder(id).appendingPathComponent(Self.unreadableFile)
        try? FileManager.default.removeItem(at: aside)
        try? FileManager.default.moveItem(at: dayURL(id), to: aside)
        return nil
    }

    private func readSidecar(_ url: URL) -> RecordingDaySidecar? {
        guard let data = try? Data(contentsOf: url) else { return nil } // perf-ok: store queue
        return try? JSONDecoder().decode(RecordingDaySidecar.self, from: data)
    }

    private func readLostFile(_ id: UUID) -> Set<Int> {
        guard let data = try? Data(contentsOf: lostURL(id)), // perf-ok: store queue
              let seqs = try? JSONDecoder().decode([Int].self, from: data) else { return [] }
        return Set(seqs.filter { $0 >= 0 })
    }

    /// A folder's files by kind, with when each sidecar, `day.json` and `lost.json` were last written.
    private nonisolated struct Listing {
        var sidecars: [Int: (url: URL, modified: Date)] = [:]
        var audio: [Int: URL] = [:]
        var dayWrittenAt: Date?
        var lostWrittenAt: Date?
        var hasJournal = false

        /// A day recording's folder (clips and rolling recordings share the root): it has a sidecar or a `day.json`.
        var isDay: Bool { !sidecars.isEmpty || dayWrittenAt != nil }

        /// The seqs of the sidecars dated at or after `date`.
        func sidecars(writtenSince date: Date) -> Set<Int> {
            Set(sidecars.filter { $0.value.modified >= date }.keys)
        }
    }

    private func list(_ id: UUID) -> Listing {
        let keys: [URLResourceKey] = [.contentModificationDateKey]
        let files = (try? FileManager.default.contentsOfDirectory(at: folder(id), includingPropertiesForKeys: keys)) ?? [] // perf-ok: store queue
        var listing = Listing()
        for file in files {
            let name = file.lastPathComponent
            let modified = (try? file.resourceValues(forKeys: Set(keys)))?.contentModificationDate ?? .distantPast
            if name == Self.dayFile {
                listing.dayWrittenAt = modified
            } else if name == Self.lostFile {
                listing.lostWrittenAt = modified
            } else if name == Self.journalFile {
                listing.hasJournal = true
            } else if let seq = Self.seq(in: name, ext: "json") {
                listing.sidecars[seq] = (file, modified)
            } else if let seq = Self.seq(in: name, ext: "m4a") {
                listing.audio[seq] = file
            }
        }
        return listing
    }

    /// The seq in `seg-NNN.<ext>`.
    private static func seq(in name: String, ext: String) -> Int? {
        guard name.hasPrefix("seg-"), name.hasSuffix("." + ext),
              let seq = Int(name.dropFirst(4).dropLast(ext.count + 1)), seq >= 0 else { return nil }
        return seq
    }

    /// Merges the sidecars of `seqs` (all of them when nil) into `record`, creating it from the first one when there is
    /// none; then the lost list, and the audio files as they are on disk. Merging a sidecar the record already holds
    /// changes nothing (`merging` only ever makes a chunk more final).
    private func merge(_ listing: Listing, seqs: Set<Int>?, into record: inout RecordingDayRecord?, _ id: UUID) {
        for (seq, file) in listing.sidecars.sorted(by: { $0.key < $1.key }) where seqs?.contains(seq) ?? true {
            // A sidecar names its own recording and seq; one that doesn't is not this day's.
            guard let sidecar = readSidecar(file.url), sidecar.header.recording == id, sidecar.header.seq == seq else { continue }
            var current = record ?? RecordingDayRecord(id: id, day: sidecar.header.day, timeZone: sidecar.timeZone ?? zone().identifier)
            if var chunk = current.merging(sidecar.header, receivedAt: sidecar.receivedAt) {
                if !chunk.isMarker { chunk.hasAudio = listing.audio[seq] != nil }
                current.put(chunk, recordingStartedAt: sidecar.header.recordingStartedAt)
            }
            record = current
        }
        guard var current = record else { return }
        if listing.lostWrittenAt != nil {
            current.lost.formUnion(readLostFile(id).subtracting(current.chunks.keys))
        }
        for (seq, chunk) in current.chunks {
            let onDisk = listing.audio[seq] != nil
            if chunk.isMarker {
                // An erase the app ended in the middle of: the sidecar says erased, the audio is still there. It was not
                // acknowledged, so if this fails too, the marker the watch sends again tries once more (`commit`).
                if onDisk { try? FileManager.default.removeItem(at: audioURL(id, seq: seq)) }
                if chunk.hasAudio { current.chunks[seq]?.hasAudio = false }
            } else if chunk.hasAudio != onDisk {
                current.chunks[seq]?.hasAudio = onDisk
            }
        }
        record = current
    }

    /// `recoverOnLaunch`: every folder brought up to date, then the days that changed since the last call.
    private func recoverAll() -> [UUID] {
        sweep()
        let changed = unreported.filter { !forgetIfDeleted($0) }
        unreported = []
        return changed.sorted { $0.uuidString < $1.uuidString }
    }

    /// Brings every folder this process hasn't read yet up to date, writing each day that changed (`reconcile`), and
    /// writes the days an earlier read brought up to date (a chunk came in before this ran). Each is on disk when this
    /// returns, and noted for `recoverOnLaunch`. Folders already read are skipped, so running it again is cheap.
    private func sweep() {
        for id in unreported.sorted(by: { $0.uuidString < $1.uuidString }) where !forgetIfDeleted(id) { write(id) }
        let folders = (try? FileManager.default.contentsOfDirectory(at: root, includingPropertiesForKeys: nil)) ?? [] // perf-ok: store queue
        for folder in folders {
            // A deleted day's folder is the library's to remove (its init does).
            guard let id = UUID(uuidString: folder.lastPathComponent), !reconciled.contains(id), !forgetIfDeleted(id) else { continue }
            let reconciliation = reconcile(id)
            guard reconciliation.changed, let record = reconciliation.record else { continue }
            cache[id] = record
            touch(id)
            dirty.insert(id)
            // One at a time, so at most `cacheLimit` days stay decoded however many a crash left behind.
            write(id)
            emit(id)
            trimCache()
            unreported.insert(id)
        }
        swept = true
    }

    /// Brings one day up to its folder, the first time this process reads it: the `day.json` an earlier run wrote, with
    /// the sidecars and loss list written after it merged in (all of them without a readable one). Returns the record
    /// when it read one, and whether the folder changed it; no record when the folder is no day's, or when `day.json`
    /// holds it all (not even decoded then: fourteen days of text at every launch).
    private func reconcile(_ id: UUID) -> (record: RecordingDayRecord?, changed: Bool) {
        reconciled.insert(id)
        var listing = list(id)
        guard listing.isDay else { return (nil, false) }
        // Audio without its sidecar was never acknowledged: the watch still has it and sends it again. (Nothing of this
        // process is half-written here: a chunk's audio and sidecar are written in one turn of the queue.)
        for (seq, url) in listing.audio where listing.sidecars[seq] == nil {
            try? FileManager.default.removeItem(at: url)
            listing.audio[seq] = nil
        }
        let unsaved = unsavedChanges(listing, id)
        if listing.dayWrittenAt != nil, unsaved.seqs.isEmpty, !unsaved.lost { return (nil, false) }
        let current = listing.dayWrittenAt == nil ? nil : readDayFile(id)
        // Without a readable record every sidecar counts; with one, only those it may not hold.
        var merged = current
        merge(listing, seqs: current == nil ? nil : unsaved.seqs, into: &merged, id)
        return (merged, merged != current)
    }

    /// What `day.json` may not hold: the sidecars and loss list the journal names (written after it, whatever their
    /// dates), and any dated after it or a little before (in case a crash of the whole system lost a journal line).
    private func unsavedChanges(_ listing: Listing, _ id: UUID) -> (seqs: Set<Int>, lost: Bool) {
        var unsaved: (seqs: Set<Int>, lost: Bool) = listing.hasJournal ? readJournal(id) : ([], false)
        guard let since = listing.dayWrittenAt?.addingTimeInterval(-Self.clockSlack) else { return unsaved }
        unsaved.seqs.formUnion(listing.sidecars(writtenSince: since))
        if let lost = listing.lostWrittenAt, lost >= since { unsaved.lost = true }
        return unsaved
    }

    private func dayIDs() -> [UUID] {
        let folders = (try? FileManager.default.contentsOfDirectory(at: root, includingPropertiesForKeys: nil)) ?? [] // perf-ok: store queue
        var ids = Set(cache.keys)
        for folder in folders {
            guard let id = UUID(uuidString: folder.lastPathComponent),
                  FileManager.default.fileExists(atPath: folder.appendingPathComponent(Self.dayFile).path) else { continue }
            ids.insert(id)
        }
        var out: [UUID] = []
        for id in ids where !forgetIfDeleted(id) { out.append(id) }
        return out.sorted { $0.uuidString < $1.uuidString }
    }

    // MARK: Writing, deletion, the cache

    private func markDirty(_ id: UUID) {
        dirty.insert(id)
        guard !writeScheduled else { return }
        writeScheduled = true
        queue.asyncAfter(deadline: .now() + writeDelay) { [weak self] in
            guard let self else { return }
            self.writeScheduled = false
            self.writeDirty()
        }
    }

    private func writeDirty() {
        for id in dirty.sorted(by: { $0.uuidString < $1.uuidString }) { write(id) }
        trimCache()
    }

    /// Writes the day's `day.json` if it changed since its last write. True when it is on disk as the store holds it;
    /// false when the write failed (it stays dirty for the next change or flush; the sidecars still hold every chunk) or
    /// the day was deleted.
    @discardableResult private func write(_ id: UUID) -> Bool {
        guard !forgetIfDeleted(id) else { return false }
        guard dirty.contains(id) else { return true }
        guard let record = cache[id] else {
            dirty.remove(id)
            return false
        }
        do {
            let data = try Self.dayEncoder().encode(record)
            try makeFolder(id)
            try data.write(to: dayURL(id), options: Self.writing)
        } catch {
            return false
        }
        dirty.remove(id)
        // `day.json` now holds every line of the journal: the record in `cache` took in each sidecar this process wrote,
        // and the ones an earlier run left were merged in when the day was first read (`reconcile`). One left behind
        // only costs a few sidecar reads at the next launch.
        try? FileManager.default.removeItem(at: journalURL(id))
        return !removeIfDeleted(id)
    }

    /// `day.json`'s coders: a time that is not a number (a transcriber's bad value) is written as text rather than
    /// failing every later write of the day.
    private static func dayEncoder() -> JSONEncoder {
        let encoder = JSONEncoder()
        encoder.nonConformingFloatEncodingStrategy = .convertToString(positiveInfinity: "+Infinity", negativeInfinity: "-Infinity", nan: "NaN")
        return encoder
    }

    private static func dayDecoder() -> JSONDecoder {
        let decoder = JSONDecoder()
        decoder.nonConformingFloatDecodingStrategy = .convertFromString(positiveInfinity: "+Infinity", negativeInfinity: "-Infinity", nan: "NaN")
        return decoder
    }

    /// The day was deleted: forget it here (the library removes its folder).
    private func forgetIfDeleted(_ id: UUID) -> Bool {
        guard isDeleted(id) else { return false }
        forget(id)
        return true
    }

    /// The day was deleted while this queue wrote into its folder: remove what was written.
    private func removeIfDeleted(_ id: UUID) -> Bool {
        guard isDeleted(id) else { return false }
        try? FileManager.default.removeItem(at: folder(id))
        forget(id)
        return true
    }

    private func forget(_ id: UUID) {
        cache[id] = nil
        lastUse[id] = nil
        dirty.remove(id)
    }

    private func touch(_ id: UUID) {
        uses += 1
        lastUse[id] = uses
    }

    /// Drops the least recently used days that are on disk as they are.
    private func trimCache() {
        guard cache.count > Self.cacheLimit else { return }
        let clean = cache.keys.filter { !dirty.contains($0) }.sorted { (lastUse[$0] ?? 0) < (lastUse[$1] ?? 0) }
        for id in clean.prefix(cache.count - Self.cacheLimit) {
            cache[id] = nil
            lastUse[id] = nil
        }
    }

    private func emit(_ id: UUID) {
        for continuation in subscribers.withLock({ Array($0.values) }) { continuation.yield(id) }
    }
}

// MARK: - What the Recordings page reads

/// A stretch of a day with no text, and why: the contract's `coverage.gaps[]` shape, in seconds since the day's start.
nonisolated struct RecordingDayGap: Codable, Sendable, Equatable {
    /// The contract's gap reasons; an unknown one (a newer watch's `after`) is kept as it is.
    nonisolated enum Reason {
        static let paused = RecordingDayContract.Reason.paused
        static let interrupted = RecordingDayContract.Reason.interrupted
        static let stopped = RecordingDayContract.Reason.stopped
        /// The watch still holds the audio.
        static let pending = "pending"
        /// The watch could not keep it.
        static let lost = "lost"
        /// The user erased it.
        static let erased = "erased"
        /// [ext] The phone could not transcribe it after `maxAttempts` tries; its audio is kept and retried.
        static let untranscribed = "untranscribed"
    }

    var from: Double
    var to: Double
    var reason: String
}

nonisolated extension RecordingDayGap {
    /// A chunk that starts this close to where the previous one ended follows it (the writer's clocks differ by a
    /// fraction of a second).
    static let tolerance = 1.0

    /// The day's stretches without text in time order, as seconds since `base` (the day's start), touching ones of
    /// the same reason merged: time between chunks (the next chunk's `after`, `paused` without one), seqs the phone
    /// doesn't hold (`pending`, or `lost` once the watch said so), erased, lost and untranscribed chunks, and the time
    /// from the last chunk to the end marker. Recorded silence is not a gap.
    static func gaps(in record: RecordingDayRecord, since base: Date) -> [RecordingDayGap] {
        let last = max(record.chunks.keys.max() ?? -1, (record.expected ?? 0) - 1)
        guard last >= 0 else { return [] }
        var out: [RecordingDayGap] = []
        func add(_ from: Date, _ to: Date, _ reason: String) {
            let start = max(0, from.timeIntervalSince(base)), end = to.timeIntervalSince(base)
            guard end > start else { return }
            if let previous = out.last, previous.reason == reason, start - previous.to <= tolerance {
                out[out.count - 1].to = max(previous.to, end)
            } else {
                out.append(RecordingDayGap(from: start, to: end, reason: reason))
            }
        }
        var cursor = base
        var missing: [Int] = []
        for seq in 0...last {
            guard let chunk = record.chunks[seq] else {
                missing.append(seq)
                continue
            }
            if !missing.isEmpty {
                add(cursor, chunk.startedAt, missing.allSatisfy { record.lost.contains($0) } ? Reason.lost : Reason.pending)
                missing = []
            } else if chunk.startedAt.timeIntervalSince(cursor) > tolerance {
                add(cursor, chunk.startedAt, chunk.after ?? Reason.paused)
            }
            if chunk.duration > 0 {
                if chunk.erased {
                    add(chunk.startedAt, chunk.endedAt, Reason.erased)
                } else if chunk.lost {
                    add(chunk.startedAt, chunk.endedAt, Reason.lost)
                } else if chunk.state == .failed {
                    add(chunk.startedAt, chunk.endedAt, Reason.untranscribed)
                }
            }
            cursor = max(cursor, chunk.endedAt)
        }
        return out
    }
}

/// Where the day's text stands with the hub, for the day's rows and page.
nonisolated enum RecordingDaySyncState: String, Sendable, Equatable, CaseIterable {
    /// The hub doesn't list `recording_day`: the text stays on this phone until it does.
    case notOffered
    /// The hub refused the day routes (an older manager): nothing is sent until its next `hello`.
    case unsupported
    /// The upload stopped for good (`RecordingDayRecord.problem`).
    case problem
    /// Text is on its way to the hub.
    case pending
    /// The hub has everything transcribed so far.
    case synced
    /// The hub keeps no text of the day any more (past `recording_days`, or deleted there).
    case expired
}

nonisolated extension RecordingDaySyncState {
    init(_ record: RecordingDayRecord, hubOffered: Bool) {
        if record.problem != nil {
            self = .problem
        } else if record.unsupportedUntilHello {
            self = .unsupported
        } else if !hubOffered {
            self = .notOffered
        } else if !record.parts.isEmpty, record.parts.allSatisfy({ $0.expired || $0.deleted }) {
            self = .expired
        } else {
            self = record.hasUnsentText ? .pending : .synced
        }
    }
}

/// What the Recordings page shows of a day, computed off the main actor from its record.
nonisolated struct RecordingDaySummary: Sendable, Equatable {
    var id: UUID
    var day: String
    var timeZone: String
    /// When the day's recording began (the watch's start, else the earliest chunk), and when its last audio ends.
    var firstAt: Date?
    var lastAt: Date?
    /// Seconds of audio the phone holds or held (erased and lost chunks left out).
    var recordedSeconds: Double
    /// The end of the transcribed audio: every chunk the phone holds up to here is transcribed (or needs no text).
    var transcribedTo: Date?
    /// Chunks with audio received, then by transcription state.
    var chunks: Int
    var waiting: Int
    var skipped: Int
    var failed: Int
    /// Seconds since `firstAt` (`RecordingDayGap.gaps(in:since:)`).
    var gaps: [RecordingDayGap]
    var sync: RecordingDaySyncState
    var recapState: String?
    var hubIDs: [UUID]
    var complete: Bool
}

nonisolated extension RecordingDaySummary {
    init(record: RecordingDayRecord, hubOffered: Bool) {
        let ordered = record.chunks.values.sorted { $0.seq < $1.seq }
        let withAudio = ordered.filter { $0.duration > 0 }
        let kept = withAudio.filter { !$0.isMarker }
        let firstAt = [record.startedAt, withAudio.map(\.startedAt).min()].compactMap { $0 }.min()
        var transcribedTo: Date?
        for chunk in ordered {
            if chunk.needsTranscription { break }
            if chunk.duration > 0 { transcribedTo = max(transcribedTo ?? chunk.endedAt, chunk.endedAt) }
        }
        self.init(id: record.id, day: record.day, timeZone: record.timeZone, firstAt: firstAt,
                  lastAt: withAudio.map(\.endedAt).max(),
                  recordedSeconds: kept.reduce(0) { $0 + $1.duration },
                  transcribedTo: transcribedTo,
                  chunks: withAudio.count,
                  waiting: kept.count(where: { $0.state == .waiting }),
                  skipped: kept.count(where: { $0.state == .skipped }),
                  failed: kept.count(where: { $0.state == .failed }),
                  gaps: firstAt.map { RecordingDayGap.gaps(in: record, since: $0) } ?? [],
                  sync: RecordingDaySyncState(record, hubOffered: hubOffered),
                  recapState: record.recapState,
                  hubIDs: record.hubIDs,
                  complete: record.isComplete)
    }
}

/// The days as the Recordings page and the day's page read them, and the watch's state for the Today row. Changes
/// from the store are gathered for `coalesce` before the summaries are recomputed off the main actor, and a summary is
/// assigned only when it changed: a chunk arrives every ten seconds all day.
@MainActor @Observable final class RecordingDaySummaries {
    static let shared = RecordingDaySummaries()

    private(set) var byID: [UUID: RecordingDaySummary] = [:]
    /// The last state the watch published (`recordingDayWatch`); nil until it has.
    private(set) var watch: RecordingDayWatchState?

    /// Today's day, each day judged in its own time zone: the one the watch is recording if that is today's, else the
    /// latest begun.
    var today: RecordingDaySummary? {
        let at = now()
        let current = byID.values.filter { summary in
            RecordingDayCalendar.day(of: at, in: TimeZone(identifier: summary.timeZone) ?? .autoupdatingCurrent) == summary.day
        }
        if let id = watch?.recording, let recording = current.first(where: { $0.id == id }) { return recording }
        return current.max { ($0.firstAt ?? .distantPast, $0.id.uuidString) < ($1.firstAt ?? .distantPast, $1.id.uuidString) }
    }

    @ObservationIgnored private let store: RecordingDayStore
    @ObservationIgnored private let hubOffered: @Sendable () -> Bool
    @ObservationIgnored private let now: @Sendable () -> Date
    @ObservationIgnored private let coalesce: Duration
    @ObservationIgnored private var waiting: Set<UUID> = []
    @ObservationIgnored private var pass: Task<Void, Never>?
    @ObservationIgnored private var listening: Task<Void, Never>?

    /// - Parameter hubOffered: whether the hub takes day recordings (the flag cached from its last `hello`).
    init(store: RecordingDayStore = .shared, hubOffered: @escaping @Sendable () -> Bool = { RecordingDayDefaults.offered([]) },
         now: @escaping @Sendable () -> Date = { .now }, coalesce: Duration = .seconds(1)) {
        self.store = store
        self.hubOffered = hubOffered
        self.now = now
        self.coalesce = coalesce
    }

    /// Follows the store from now on: a day that changes is summarized again. Reads no other day; `readAll` does that,
    /// once the app is in front (a launch in the background to take a chunk shows nothing and doesn't decode fourteen
    /// days for it, P9). Idempotent.
    func start() {
        guard listening == nil else { return }
        let changes = store.changes
        listening = Task { [weak self] in
            for await id in changes { self?.changed(id) }
        }
    }

    /// A day changed, or was deleted (its summary goes): recomputed with the next pass.
    func changed(_ id: UUID) {
        waiting.insert(id)
        schedule()
    }

    /// Every day the phone holds, read and summarized: the receiver's call, once per launch with the app in front. Days a
    /// crash left with sidecars only are included even before the launch's recovery is done (`RecordingDayStore.ids`).
    func readAll() {
        Task { [weak self] in
            guard let self else { return }
            let ids = await self.store.ids()
            self.waiting.formUnion(ids)
            self.waiting.formUnion(self.byID.keys)
            self.schedule()
        }
    }

    /// The days already summarized, again: the hub's flag changed (it changes every day's `sync`). Days not read yet get
    /// the new flag when `readAll` reads them, so this reads no more than is already shown, in the background too.
    /// `AppModel` calls it when `RecordingDayDefaults.offered` changes.
    func refreshAll() {
        waiting.formUnion(byID.keys)
        schedule()
    }

    /// The watch's state (`WatchSync.onDayWatchState`).
    func receive(watch state: RecordingDayWatchState) {
        guard state != watch else { return }
        watch = state
    }

    /// Recomputes these days now (a pass calls it; tests await it).
    func refresh(_ ids: Set<UUID>) async {
        guard !ids.isEmpty else { return }
        let store = self.store, offered = self.hubOffered
        let computed = await Task.detached(priority: .utility) {
            let hub = offered()
            var out: [UUID: RecordingDaySummary] = [:]
            for id in ids {
                if let record = await store.record(id) { out[id] = RecordingDaySummary(record: record, hubOffered: hub) }
            }
            return out
        }.value
        var next = byID
        for id in ids { next[id] = computed[id] }
        if next != byID { byID = next }
    }

    private func schedule() {
        guard pass == nil, !waiting.isEmpty else { return }
        pass = Task { [weak self, delay = self.coalesce] in
            try? await Task.sleep(for: delay)
            guard let self else { return }
            let ids = self.waiting
            self.waiting = []
            await self.refresh(ids)
            self.pass = nil
            self.schedule()
        }
    }
}
