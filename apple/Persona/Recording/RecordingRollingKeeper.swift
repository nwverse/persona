import Foundation
import Observation

/// Decides which rolling-recording segments stay past the window (health signals, bookmarks, moods) and deletes the rest.
@Observable
final class RecordingRollingKeeper {
    static let shared = RecordingRollingKeeper()
    /// Without health data (no HealthKit, or protected data while locked) only the timeout rules apply.
    static let defaultFetch: @Sendable (Date, Date) async throws -> RecordingSignals = {
        #if os(iOS)
        return try await RecordingHealthSignals.fetch(from: $0, to: $1)
        #else
        return RecordingSignals()
        #endif
    }
    private static let debounce: Duration = .seconds(2)

    @ObservationIgnored private let library: RecordingLibrary
    @ObservationIgnored private let fetch: @Sendable (Date, Date) async throws -> RecordingSignals
    @ObservationIgnored private var running = false
    @ObservationIgnored private var again = false
    @ObservationIgnored private var pending: Task<Void, Never>?

    init(library: RecordingLibrary = .shared,
         fetch: @escaping @Sendable (Date, Date) async throws -> RecordingSignals = RecordingRollingKeeper.defaultFetch) {
        self.library = library; self.fetch = fetch
    }

    func run(now: Date = .now) async {
        // A run asked for while one is going (new segments arrived) happens right after it.
        guard !running else { again = true; return }
        running = true
        defer {
            running = false
            if again { again = false; schedule() }
        }
        let pad = RecordingRolling.padMinutes * 60
        for entry in library.entries where entry.isRolling {
            let id = entry.id
            // Entries that stopped before endedAt existed start their grace period now.
            if !entry.active, entry.endedAt == nil { library.change(id) { $0.endedAt = now } }
            var evaluated = true
            let candidates = RecordingRolling.candidates(entry, now: now)
            if let from = candidates.compactMap(\.startedAt).min(),
               let to = candidates.compactMap(\.endedAt).max() {
                var signals = RecordingSignals()
                do {
                    signals = try await fetch(from.addingTimeInterval(-pad), to.addingTimeInterval(pad))
                } catch {
                    // Bookmarks don't need health data: they still keep their segments; the rest waits for unevaluatedHours.
                    evaluated = false
                }
                if let current = library.entry(id) {
                    let keeps = RecordingKeepRules.evaluate(current, candidates: candidates, signals: signals)
                    if !keeps.isEmpty { library.keep(id, keeps) }
                }
            }
            if let latest = library.entry(id) {
                // A segment that arrived during the fetch was not covered by it: it waits for the next run.
                let covered = Set(candidates.map(\.index))
                let kept = Set(latest.segments.filter { $0.kept != nil }.map(\.index))
                let doomed = RecordingRolling.expired(latest, now: now, evaluated: evaluated).filter { covered.contains($0) || kept.contains($0) }
                library.prune(id, indexes: doomed)
            }
        }
        for entry in library.entries where RecordingRolling.finished(entry, now: now) {
            try? library.remove(entry.id)
        }
    }

    /// Runs shortly after new segments arrive; several arrivals in a row share one run.
    func schedule() {
        guard pending == nil else { return }
        pending = Task { [weak self] in
            try? await Task.sleep(for: RecordingRollingKeeper.debounce)
            guard let self else { return }
            self.pending = nil
            await self.run()
        }
    }
}
