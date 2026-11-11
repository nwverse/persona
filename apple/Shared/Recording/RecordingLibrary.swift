import Foundation
import Observation
import os

/// Recordings are not a cache to clear. Each gets its own directory and the index is written atomically; shared by phone and watch.
@Observable
final class RecordingLibrary {
    static let shared = RecordingLibrary()
    nonisolated static let directory = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        .appendingPathComponent("Recordings", isDirectory: true)
    private(set) var entries: [RecordingEntry] = []
    private(set) var error: String?
    @ObservationIgnored nonisolated let root: URL
    @ObservationIgnored private var deleted: Set<UUID> = [] {
        didSet { let snapshot = deleted; tombstones.withLock { $0 = snapshot } }
    }
    /// A copy of `deleted` for readers off the main actor (the phone stores all-day chunks on the WatchConnectivity
    /// queue). Updated before a removal deletes any file, so a chunk stored during it is refused or cleaned up.
    @ObservationIgnored private nonisolated let tombstones = OSAllocatedUnfairLock(initialState: Set<UUID>())
    /// Called after a recording is saved (the phone syncs its text to the knowledge base from here).
    @ObservationIgnored var didChange: ((UUID) -> Void)?

    init(root: URL = RecordingLibrary.directory) {
        self.root = root
        do {
            try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
            if let data = try? Data(contentsOf: root.appendingPathComponent("deleted.json")),
               let ids = try? JSONDecoder().decode(Set<UUID>.self, from: data) { deleted = ids }
            let tombstoned = deleted
            tombstones.withLock { $0 = tombstoned }
            let dirs = try FileManager.default.contentsOfDirectory(at: root, includingPropertiesForKeys: nil)
            entries = dirs.compactMap { dir in
                guard let id = UUID(uuidString: dir.lastPathComponent) else { return nil }
                // A deleted recording whose folder came back (a write already under way when it was deleted).
                guard !tombstoned.contains(id) else {
                    try? FileManager.default.removeItem(at: dir)
                    return nil
                }
                guard let data = try? Data(contentsOf: dir.appendingPathComponent("recording.json")),
                      let entry = try? JSONDecoder().decode(RecordingEntry.self, from: data),
                      entry.id.uuidString == dir.lastPathComponent else { return nil }
                return entry
            }.sorted { $0.createdAt > $1.createdAt }
        } catch { self.error = String(localized: "Can't read the recordings folder") }
    }

    nonisolated func folder(_ id: UUID) -> URL { root.appendingPathComponent(id.uuidString, isDirectory: true) }
    nonisolated func audio(_ id: UUID, _ index: Int) -> URL { folder(id).appendingPathComponent(RecordingSegment.fileName(index)) }
    func entry(_ id: UUID) -> RecordingEntry? { entries.first { $0.id == id } }

    /// A new recording. `id` / `createdAt` / `day` / `timeZone` are for all-day recordings: the watch names the next
    /// day's recording before midnight, and its writer may already have put audio in that folder, which stays. An id
    /// that already has an entry returns that entry unchanged.
    @discardableResult func create(source: RecordingEntry.Source, locale: String = "zh-CN", title: String = "",
                                   meeting: RecordingMeeting? = nil, kind: RecordingEntry.Kind = .clip, id: UUID = UUID(),
                                   createdAt: Date = .now, day: String? = nil, timeZone: String? = nil) throws -> RecordingEntry {
        if let existing = entry(id) { return existing }
        // A clip stays nil on disk, as entries from before kinds were.
        var entry = RecordingEntry(id: id, createdAt: createdAt, source: source, locale: locale, kind: kind == .clip ? nil : kind)
        entry.title = title
        entry.meeting = meeting
        entry.day = day
        entry.timeZone = timeZone
        try save(entry)
        return entry
    }

    func save(_ entry: RecordingEntry) throws {
        guard !deleted.contains(entry.id) else { throw CocoaError(.fileNoSuchFile) }
        var entry = entry
        // Strictly later than the copy it replaces, even within one millisecond or after the clock went back.
        let floor = (self.entry(entry.id)?.updatedAt ?? entry.updatedAt ?? entry.createdAt).addingTimeInterval(0.001)
        entry.updatedAt = max(Date.now, floor)
        try FileManager.default.createDirectory(at: folder(entry.id), withIntermediateDirectories: true)
        try JSONEncoder().encode(entry).write(to: folder(entry.id).appendingPathComponent("recording.json"), options: .atomic)
        entries.removeAll { $0.id == entry.id }
        entries.append(entry)
        entries.sort { $0.createdAt > $1.createdAt }
        error = nil
        didChange?(entry.id)
    }

    #if DEBUG
    /// Screenshots and UI tests put the same made-up recording back after a run deleted it.
    func restore(_ id: UUID) {
        guard deleted.remove(id) != nil else { return }
        try? JSONEncoder().encode(deleted).write(to: root.appendingPathComponent("deleted.json"), options: .atomic)
    }
    #endif

    /// Whether this id was deleted here (its tombstone stops late watch segments from recreating it).
    func isDeleted(_ id: UUID) -> Bool { deleted.contains(id) }

    /// `isDeleted` from any thread: the phone's day store asks before and after it writes a chunk.
    nonisolated func isDeletedSnapshot(_ id: UUID) -> Bool { tombstones.withLock { $0.contains(id) } }

    func change(_ id: UUID, _ edit: (inout RecordingEntry) -> Void) {
        guard var entry = entry(id) else { return }
        edit(&entry)
        do { try save(entry) } catch { self.error = String(localized: "The recording info wasn't saved. Keep the app installed and try again.") }
    }

    func addSegment(_ id: UUID, _ segment: RecordingSegment) {
        guard let current = entry(id) else { return }
        // A segment already registered keeps its audio: the writer re-adds its finished segments when capture stops.
        guard !current.segments.contains(where: { $0.index == segment.index }) else { return }
        if current.isDay {
            addDayChunk(id, segment, to: current)
            return
        }
        // Segments that finish after being pruned or erased are dropped.
        guard !current.isPruned(segment.index), !current.isErased(segment) else {
            try? FileManager.default.removeItem(at: audio(id, segment.index))
            return
        }
        change(id) { entry in
            guard !entry.segments.contains(where: { $0.index == segment.index }) else { return }
            entry.segments.append(segment)
            entry.segments.sort { $0.index < $1.index }
        }
    }

    /// The end message and the audio can arrive in any order; a repeated message never overwrites a transcribed segment.
    func receive(_ transfer: RecordingTransfer) throws {
        guard transfer.valid else { throw CocoaError(.fileReadCorruptFile) }
        // A watch segment arriving late or retried after a delete must not recreate the deleted recording, but is still acknowledged so the watch cleans up.
        if deleted.contains(transfer.id) {
            try? FileManager.default.removeItem(at: folder(transfer.id))
            return
        }
        var entry = entry(transfer.id) ?? RecordingEntry(id: transfer.id, createdAt: transfer.createdAt, source: .watch,
                                                          locale: transfer.locale, kind: transfer.kind)
        entry.marks = Array(Set(entry.marks + transfer.marks)).sorted()
        // A meeting started on the watch: the phone names it after the calendar (`MeetingRecorder.tagWatchRecording`).
        if transfer.meeting == true, entry.meeting == nil { entry.meeting = RecordingMeeting() }
        if let erased = transfer.erased, !erased.isEmpty {
            var merged = entry.erased ?? []
            for range in erased where !merged.contains(range) { merged.append(range) }
            entry.erased = merged.isEmpty ? nil : merged
            let snapshot = entry
            for segment in entry.segments where snapshot.isErased(segment) {
                try? FileManager.default.removeItem(at: audio(entry.id, segment.index))
            }
            entry.segments.removeAll { snapshot.isErased($0) }
        }
        if var segment = transfer.segment, !entry.segments.contains(where: { $0.index == segment.index }) {
            segment.sentences = nil; segment.delivered = false
            // The watch holds bookmarked segments it couldn't send for longer; one arriving after newer segments
            // were pruned still gets checked instead of falling into the tombstone.
            let tombstoned = entry.isPruned(segment.index) && !RecordingRolling.nearMark(segment, marks: entry.marks)
            if tombstoned || entry.isErased(segment) {
                try? FileManager.default.removeItem(at: audio(entry.id, segment.index))
            } else {
                entry.segments.append(segment)
                entry.segments.sort { $0.index < $1.index }
            }
        }
        if let total = transfer.total {
            entry.active = false; entry.expectedSegments = total
            if entry.endedAt == nil { entry.endedAt = .now }
        }
        try save(entry)
    }

    func remove(_ id: UUID) throws {
        var next = deleted; next.insert(id)
        try JSONEncoder().encode(next).write(to: root.appendingPathComponent("deleted.json"), options: .atomic)
        deleted = next
        if FileManager.default.fileExists(atPath: folder(id).path) { try FileManager.default.removeItem(at: folder(id)) }
        entries.removeAll { $0.id == id }
    }

    /// Rolling recordings drop expired segments and remember which indexes went, so late copies of them never return while a segment that never arrived is not tombstoned.
    func prune(_ id: UUID, indexes: [Int]) {
        guard !indexes.isEmpty, entry(id) != nil else { return }
        let gone = Set(indexes)
        change(id) { entry in
            entry.segments.removeAll { gone.contains($0.index) }
            var floor = entry.prunedBelow ?? 0
            var above = Set(entry.prunedAbove ?? []).union(gone.filter { $0 >= floor })
            while above.remove(floor) != nil { floor += 1 }
            entry.prunedBelow = floor
            entry.prunedAbove = above.isEmpty ? nil : above.sorted()
        }
        for index in gone { try? FileManager.default.removeItem(at: audio(id, index)) }
    }

    /// Segments kept for bookmarks, heart-rate spikes, or mood. Already-kept ones stay as they are.
    func keep(_ id: UUID, _ keeps: [Int: RecordingKeep]) {
        guard !keeps.isEmpty else { return }
        change(id) { entry in
            for position in entry.segments.indices where entry.segments[position].kept == nil {
                if let keep = keeps[entry.segments[position].index] { entry.segments[position].kept = keep }
            }
        }
    }

    /// Erase a wall-clock range; later syncs that land in it are refused too.
    func erase(_ id: UUID, from: Date, to: Date) {
        let range = RecordingErase(from: from, to: to)
        guard range.valid, entry(id) != nil else { return }
        var removed: [Int] = []
        change(id) { entry in
            entry.erased = (entry.erased ?? []) + [range]
            removed = entry.segments.filter { $0.overlaps(from, to) }.map(\.index)
            entry.segments.removeAll { $0.overlaps(from, to) }
        }
        for index in removed { try? FileManager.default.removeItem(at: audio(id, index)) }
    }

    /// Save the chosen segments as a normal clip: indexes renumbered, transcript and bookmarks shifted.
    func saveClip(from id: UUID, indexes: [Int]) throws -> RecordingEntry {
        guard let original = entry(id) else { throw CocoaError(.fileNoSuchFile) }
        let chosen = Set(indexes)
        let picked = original.segments.filter { chosen.contains($0.index) && FileManager.default.fileExists(atPath: audio(id, $0.index).path) }
            .sorted { $0.index < $1.index }
        var clip = RecordingEntry(id: UUID(), createdAt: picked.first?.startedAt ?? .now, source: original.source, locale: original.locale)
        try FileManager.default.createDirectory(at: folder(clip.id), withIntermediateDirectories: true)
        var placed: [(old: RecordingSegment, start: Double)] = []
        var start = 0.0
        for segment in picked {
            do { try FileManager.default.copyItem(at: audio(id, segment.index), to: audio(clip.id, placed.count)) } catch { continue }
            let shift = start - segment.start
            var next = segment
            next.index = placed.count
            next.start = start
            next.delivered = true
            next.kept = nil
            next.sentences = segment.sentences?.map { RecordingSentence(start: $0.start + shift, end: $0.end + shift, text: $0.text) }
            placed.append((segment, start))
            clip.segments.append(next)
            start += segment.duration
        }
        guard !clip.segments.isEmpty else {
            try? FileManager.default.removeItem(at: folder(clip.id))
            throw CocoaError(.fileNoSuchFile)
        }
        clip.marks = original.marks.compactMap { mark in
            placed.first { mark >= $0.old.start && mark < $0.old.start + $0.old.duration }.map { $0.start + mark - $0.old.start }
        }.sorted()
        clip.active = false
        clip.expectedSegments = clip.segments.count
        clip.endDelivered = true
        try save(clip)
        return clip
    }

    /// Hourhand's cleanup after an abnormal exit: keep only finalized segments, delete half-written ones with no tail.
    /// An all-day recording stays open (the day goes on with the same id and the next seq when recording starts again);
    /// the ids of those left open are returned. One that was recording when the app ended reports its gap as `stopped`.
    @discardableResult func recover(source: RecordingEntry.Source) -> [UUID] {
        var open: [UUID] = []
        for var entry in entries where entry.active && entry.source == source {
            let known = Set(entry.segments.map { RecordingSegment.fileName($0.index) })
            let files = (try? FileManager.default.contentsOfDirectory(at: folder(entry.id), includingPropertiesForKeys: nil)) ?? []
            for file in files where file.pathExtension == "m4a" && !known.contains(file.lastPathComponent) {
                try? FileManager.default.removeItem(at: file)
            }
            if entry.isDay {
                open.append(entry.id)
                // A pause or an interruption before the app ended keeps its own reason.
                guard entry.gapReason == nil else { continue }
                entry.gapReason = RecordingDayContract.Reason.stopped
                do { try save(entry) } catch { self.error = String(localized: "The end of the last recording wasn't saved") }
                continue
            }
            entry.active = false
            entry.expectedSegments = entry.nextIndex
            entry.endedAt = .now
            do { try save(entry) } catch { self.error = String(localized: "The end of the last recording wasn't saved") }
        }
        return open
    }
}

// MARK: - All-day recordings (docs/assistant/day-recap.md)

extension RecordingLibrary {
    /// A finished chunk of a day recording. Only a seq the phone acknowledged and the watch already pruned is dropped:
    /// a chunk that finishes inside a range the user erased (the one being written when they tapped Erase) is
    /// registered as erased, without its audio, so its seq still reaches the phone as an erased marker and no seq is
    /// left without a record.
    private func addDayChunk(_ id: UUID, _ segment: RecordingSegment, to current: RecordingEntry) {
        guard !current.isPruned(segment.index) else {
            try? FileManager.default.removeItem(at: audio(id, segment.index))
            return
        }
        var record = segment
        let erased = record.erased == true || current.isDayErased(record)
        if erased { record.erased = true; record.delivered = false; record.sentences = nil }
        change(id) { entry in
            guard !entry.segments.contains(where: { $0.index == record.index }) else { return }
            entry.segments.append(record)
            entry.segments.sort { $0.index < $1.index }
        }
        // After the save: a crash in between leaves an erased record whose leftover audio pruning deletes, never a seq
        // without a record.
        if erased { try? FileManager.default.removeItem(at: audio(id, record.index)) }
    }

    /// The user erased `from…to` of a day. Every chunk in it, acknowledged or not, becomes erased: its audio and text
    /// are deleted, its seq and times stay, and it goes to the phone again as an erased marker. The range is kept, so a
    /// chunk that finishes in it later is erased too (`addSegment`). Returns the seqs newly erased.
    @discardableResult func eraseDay(_ id: UUID, from: Date, to: Date) -> [Int] {
        let range = RecordingErase(from: from, to: to)
        guard range.valid, let current = entry(id), current.isDay else { return [] }
        let hit = Set(current.segments.filter { $0.erased != true && $0.fallsInDayErase(range) }.map(\.index))
        change(id) { entry in
            if !(entry.erased ?? []).contains(range) { entry.erased = (entry.erased ?? []) + [range] }
            for position in entry.segments.indices where hit.contains(entry.segments[position].index) {
                entry.segments[position].erased = true
                entry.segments[position].delivered = false
                entry.segments[position].sentences = nil
            }
        }
        for index in hit { try? FileManager.default.removeItem(at: audio(id, index)) }
        return hit.sorted()
    }

    /// The phone acknowledged these chunks: they count as delivered and their audio goes, in one save. The records stay
    /// until `pruneDelivered`, so an erase can still reach them. `removingMarks`: the marks those chunks carried, which
    /// the phone now holds.
    func markDelivered(_ id: UUID, seqs: some Sequence<Int>, removingMarks marks: [RecordingDayMark] = []) {
        let acked = Set(seqs)
        let gone = Set(marks)
        guard let current = entry(id), !acked.isEmpty || !gone.isEmpty else { return }
        let newly = current.segments.filter { acked.contains($0.index) && !$0.delivered }.map(\.index)
        let dropsMarks = current.dayMarks?.contains { gone.contains($0) } ?? false
        guard !newly.isEmpty || dropsMarks else { return }
        change(id) { entry in
            for position in entry.segments.indices where acked.contains(entry.segments[position].index) {
                entry.segments[position].delivered = true
            }
            entry.dayMarks?.removeAll { gone.contains($0) }
            if entry.dayMarks?.isEmpty == true { entry.dayMarks = nil }
        }
        for index in newly { try? FileManager.default.removeItem(at: audio(id, index)) }
    }

    /// Drops the records the phone has held for a while (delivered, and ended before `cutoff`) with a tombstone, so
    /// their seqs are never used again and a late copy of one never comes back.
    func pruneDelivered(_ id: UUID, olderThan cutoff: Date) {
        guard let current = entry(id), current.isDay else { return }
        prune(id, indexes: current.segments.filter { $0.delivered && ($0.endedAt ?? .distantPast) < cutoff }.map(\.index))
    }

    /// Ends a day recording: closed, with a header-only end marker (the next seq, no audio, at `at`) that tells the phone
    /// the day is over and every seq below it exists. `after`: why there is no audio between the last chunk and `at`.
    /// A day that already ended (a chunk or marker says `end`, or `dayEndAt` is set) gets no second marker.
    func closeDay(_ id: UUID, at: Date, after: String?) {
        guard let current = entry(id), current.isDay else { return }
        guard current.dayEndAt == nil, !current.segments.contains(where: { $0.end == true }) else {
            if current.active { change(id) { $0.active = false } }
            return
        }
        change(id) { entry in
            let seq = entry.nextIndex
            var marker = RecordingSegment(index: seq, start: entry.duration, duration: 0, startedAt: at)
            marker.end = true
            marker.after = after
            entry.segments.append(marker)
            entry.active = false
            entry.expectedSegments = seq + 1
            entry.dayEndAt = at
            if entry.endedAt == nil { entry.endedAt = .now }
        }
    }

    /// This device's newest open day recording for a local date.
    func openDay(source: RecordingEntry.Source, day: String) -> RecordingEntry? {
        entries.first { $0.isDay && $0.active && $0.source == source && $0.day == day }
    }

    /// This device's open day recordings, newest first: today's, and a past day not closed yet.
    func openDays(source: RecordingEntry.Source) -> [RecordingEntry] {
        entries.filter { $0.isDay && $0.active && $0.source == source }
    }

    /// Phone: the index row of a day recording the watch sends. It has no segments: the chunks and their text live in
    /// the day store, in the same folder, which `remove` deletes with it. Written only when something changed (a chunk
    /// arrives every few seconds). `createdAt` only moves earlier: the recording's start, or until that is known the
    /// earliest chunk. `closed` ends it and never reopens it. nil for a deleted id, which stays deleted.
    @discardableResult func adoptDay(id: UUID, day: String, timeZone: String, createdAt: Date, closed: Bool = false) -> RecordingEntry? {
        guard !deleted.contains(id) else { return nil }
        let current = entry(id)
        // A new row starts in the phone's recording language, as a recording started on it would.
        var next = current ?? RecordingEntry(id: id, createdAt: createdAt, source: .watch,
                                             locale: UserDefaults.standard.string(forKey: RecordingDefaults.localeKey) ?? RecordingDefaults.locale,
                                             kind: .day)
        next.kind = .day
        next.day = day
        next.timeZone = timeZone
        next.createdAt = min(next.createdAt, createdAt)
        if closed { next.active = false }
        guard next != current else { return current }
        do { try save(next) } catch {
            self.error = String(localized: "The recording info wasn't saved. Keep the app installed and try again.")
            return nil
        }
        return entry(id)
    }
}
