import Foundation
import Observation

/// Recordings are not a cache to clear. Each gets its own directory and the index is written atomically; shared by phone and watch.
@Observable
final class RecordingLibrary {
    static let shared = RecordingLibrary()
    nonisolated static let directory = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        .appendingPathComponent("Recordings", isDirectory: true)
    private(set) var entries: [RecordingEntry] = []
    private(set) var error: String?
    @ObservationIgnored nonisolated let root: URL
    @ObservationIgnored private var deleted: Set<UUID> = []
    /// Called after a recording is saved (the phone syncs its text to the knowledge base from here).
    @ObservationIgnored var didChange: ((UUID) -> Void)?

    init(root: URL = RecordingLibrary.directory) {
        self.root = root
        do {
            try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
            if let data = try? Data(contentsOf: root.appendingPathComponent("deleted.json")),
               let ids = try? JSONDecoder().decode(Set<UUID>.self, from: data) { deleted = ids }
            let dirs = try FileManager.default.contentsOfDirectory(at: root, includingPropertiesForKeys: nil)
            entries = dirs.compactMap { dir in
                guard UUID(uuidString: dir.lastPathComponent) != nil,
                      let data = try? Data(contentsOf: dir.appendingPathComponent("recording.json")),
                      let entry = try? JSONDecoder().decode(RecordingEntry.self, from: data),
                      entry.id.uuidString == dir.lastPathComponent else { return nil }
                return entry
            }.sorted { $0.createdAt > $1.createdAt }
        } catch { self.error = String(localized: "Can't read the recordings folder") }
    }

    nonisolated func folder(_ id: UUID) -> URL { root.appendingPathComponent(id.uuidString, isDirectory: true) }
    nonisolated func audio(_ id: UUID, _ index: Int) -> URL { folder(id).appendingPathComponent(RecordingSegment.fileName(index)) }
    func entry(_ id: UUID) -> RecordingEntry? { entries.first { $0.id == id } }

    @discardableResult func create(source: RecordingEntry.Source, locale: String = "zh-CN", title: String = "",
                                   meeting: RecordingMeeting? = nil, kind: RecordingEntry.Kind = .clip) throws -> RecordingEntry {
        var entry = RecordingEntry(id: UUID(), createdAt: .now, source: source, locale: locale,
                                   kind: kind == .rolling ? .rolling : nil)
        entry.title = title
        entry.meeting = meeting
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

    func change(_ id: UUID, _ edit: (inout RecordingEntry) -> Void) {
        guard var entry = entry(id) else { return }
        edit(&entry)
        do { try save(entry) } catch { self.error = String(localized: "The recording info wasn't saved. Keep the app installed and try again.") }
    }

    func addSegment(_ id: UUID, _ segment: RecordingSegment) {
        guard let current = entry(id) else { return }
        // A segment already registered keeps its audio: the writer re-adds its finished segments when capture stops.
        guard !current.segments.contains(where: { $0.index == segment.index }) else { return }
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
    func recover(source: RecordingEntry.Source) {
        for var entry in entries where entry.active && entry.source == source {
            let known = Set(entry.segments.map { RecordingSegment.fileName($0.index) })
            let files = (try? FileManager.default.contentsOfDirectory(at: folder(entry.id), includingPropertiesForKeys: nil)) ?? []
            for file in files where file.pathExtension == "m4a" && !known.contains(file.lastPathComponent) {
                try? FileManager.default.removeItem(at: file)
            }
            entry.active = false
            entry.expectedSegments = entry.nextIndex
            entry.endedAt = .now
            do { try save(entry) } catch { self.error = String(localized: "The end of the last recording wasn't saved") }
        }
    }
}
