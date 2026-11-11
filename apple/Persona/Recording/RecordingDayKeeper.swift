import Foundation

// All-day recording's retention on the phone (docs/assistant/day-recap.md, "Retention and privacy"): a chunk's audio
// goes 3 days after the day's final recap and at most 7 days after the day, whether or not the hub ever got its text
// (C21); the whole day (audio, text, upload state, the library's row) goes once the day is older than the hub's
// `recording_days`. Never reads health data.

/// When a day's audio and the day itself go, from its record alone (unit tested).
nonisolated enum RecordingDayRetention {
    static let day: TimeInterval = 86_400

    /// The end of the recording's local date in its own time zone (a 23- or 25-hour day ends where it does): the 7-day
    /// cap counts from here, so a day that was never closed is capped too. Without a readable date, the recording's end
    /// or its last audio.
    static func dayEnd(_ record: RecordingDayRecord) -> Date? {
        let zone = TimeZone(identifier: record.timeZone) ?? .autoupdatingCurrent
        if let start = RecordingDayCalendar.start(of: record.day, in: zone) {
            return RecordingDayCalendar.nextMidnight(after: start, in: zone)
        }
        return record.endedAt ?? record.chunks.values.map(\.endedAt).max()
    }

    /// When the day's final recap was written: the latest across its parts, once every part the hub keeps has one (the
    /// audio serves all of them). nil until then.
    static func recapFinalAt(_ record: RecordingDayRecord) -> Date? {
        let kept = record.parts.filter { !$0.deleted && !$0.expired }
        guard !kept.isEmpty, kept.allSatisfy({ $0.recapFinalAt != nil }) else { return nil }
        return kept.compactMap(\.recapFinalAt).max()
    }

    /// When a chunk's audio goes: transcribed or skipped ones `audioDaysAfterRecap` after the final recap, and every one,
    /// transcribed or not, `audioMaxDays` after the day (C21: the cap waits on nothing; an untranscribed chunk's time is
    /// then an `untranscribed` gap). nil for a chunk without audio.
    static func audioDeadline(_ chunk: RecordingDayChunk, in record: RecordingDayRecord) -> Date? {
        guard chunk.hasAudio else { return nil }
        let cap = dayEnd(record).map { $0.addingTimeInterval(RecordingDayContract.audioMaxDays * day) }
        let done = !chunk.isMarker && (chunk.state == .transcribed || chunk.state == .skipped)
        let afterRecap = done ? recapFinalAt(record).map { $0.addingTimeInterval(RecordingDayContract.audioDaysAfterRecap * day) } : nil
        switch (cap, afterRecap) {
        case let (cap?, recap?): return min(cap, recap)
        case let (cap?, nil): return cap
        case let (nil, recap?): return recap
        case (nil, nil): return nil
        }
    }

    /// The seqs whose audio is due at `now`.
    static func audioDue(_ record: RecordingDayRecord, now: Date) -> [Int] {
        record.chunks.values.filter { chunk in audioDeadline(chunk, in: record).map { $0 <= now } ?? false }.map(\.seq).sorted()
    }

    /// The soonest audio deadline after `now`, to wake for.
    static func nextDeadline(_ record: RecordingDayRecord, now: Date) -> Date? {
        record.chunks.values.compactMap { audioDeadline($0, in: record) }.filter { $0 > now }.min()
    }

    /// Whether a day that began at `start` is older than `days` (the hub's `recording_days`): the hub no longer keeps its
    /// text, and the phone doesn't either.
    static func isExpired(start: Date, now: Date, days: Int) -> Bool {
        start < now.addingTimeInterval(-Double(days) * day)
    }

    /// When the day began, as the hub measures its window: the recording's start, else its date's midnight.
    static func start(_ record: RecordingDayRecord) -> Date? {
        record.hubStartedAt ?? RecordingDayCalendar.start(of: record.day, in: TimeZone(identifier: record.timeZone) ?? .autoupdatingCurrent)
    }
}

/// Applies `RecordingDayRetention` to every day the phone holds: soon after launch, when the app comes to the front, in
/// the catch-up task, when a final recap's time becomes known, and at the next deadline while the app runs.
@MainActor final class RecordingDayKeeper {
    static let shared = RecordingDayKeeper()

    private let store: RecordingDayStore
    private let library: RecordingLibrary
    private let days: () -> Int?
    private let now: () -> Date
    private let forget: (UUID) -> Void
    private var running = false
    private var again = false
    private var pending: Task<Void, Never>?
    private var timer: Task<Void, Never>?
    private var lastRun: Date?

    /// Runs asked for by triggers are at least this far apart; a deadline or `run(force:)` doesn't wait.
    private static let minimumGap: TimeInterval = 3600
    /// Looked at again at least this often while the app runs.
    private static let maximumGap: TimeInterval = 6 * 3600
    private static let debounce: Duration = .seconds(2)

    /// - Parameters:
    ///   - days: the hub's `recording_days` once it said it (`RecordingSync`), nil before: until then no day is purged
    ///     for its age (the hub may keep them longer than the default).
    ///   - forget: a purged day's transcription and upload let go of it.
    init(store: RecordingDayStore = .shared, library: RecordingLibrary = .shared,
         days: @escaping () -> Int? = { RecordingSync.shared.store.daysKnown ? RecordingSync.shared.store.days : nil },
         now: @escaping () -> Date = { .now },
         forget: @escaping (UUID) -> Void = { RecordingDaySync.shared.removedHere($0) }) {
        self.store = store; self.library = library; self.days = days; self.now = now; self.forget = forget
    }

    /// A run soon (several triggers in a row share it), unless one ran within the hour.
    func schedule() {
        guard pending == nil else { return }
        pending = Task { [weak self] in
            try? await Task.sleep(for: RecordingDayKeeper.debounce)
            guard let self, !Task.isCancelled else { return }
            self.pending = nil
            await self.run()
        }
    }

    /// Deletes the audio that is due and purges the days past `recording_days`. `force`: whatever the last run's time
    /// (the catch-up, a deadline).
    func run(force: Bool = false) async {
        if !force, let last = lastRun, now().timeIntervalSince(last) < Self.minimumGap, last <= now() { return }
        guard !running else {
            again = true
            return
        }
        running = true
        defer {
            running = false
            if again {
                again = false
                schedule()
            }
        }
        lastRun = now()
        let window = days()
        var soonest: Date?
        let ids = await store.ids()
        for id in ids {
            guard let record = await store.record(id) else { continue }
            if let window, let start = RecordingDayRetention.start(record),
               RecordingDayRetention.isExpired(start: start, now: now(), days: window) {
                purge(id)
                continue
            }
            let due = RecordingDayRetention.audioDue(record, now: now())
            if !due.isEmpty { await store.removeAudio(id, seqs: due) }
            if let next = RecordingDayRetention.nextDeadline(record, now: now()) { soonest = min(soonest ?? next, next) }
        }
        // A day row whose chunks are gone (its folder emptied by hand, a crash before the first chunk was written).
        if let window {
            let held = Set(ids)
            for entry in library.entries where entry.isDay && !held.contains(entry.id)
                && RecordingDayRetention.isExpired(start: entry.createdAt, now: now(), days: window) {
                purge(entry.id)
            }
        }
        wake(at: min(soonest ?? .distantFuture, now().addingTimeInterval(Self.maximumGap)))
    }

    /// The whole day goes: its folder (audio, sidecars, text, upload state) with the library's row, tombstoned so a late
    /// chunk never brings it back. The hub trims its own copy.
    private func purge(_ id: UUID) {
        try? library.remove(id)
        forget(id)
    }

    private func wake(at date: Date) {
        timer?.cancel()
        let delay = max(1, date.timeIntervalSince(now()))
        timer = Task { [weak self] in
            try? await Task.sleep(for: .seconds(delay))
            guard !Task.isCancelled else { return }
            await self?.run(force: true)
        }
    }
}
