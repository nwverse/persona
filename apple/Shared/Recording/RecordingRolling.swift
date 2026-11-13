import Foundation

/// Rolling recordings keep the last few hours; health signals decide what stays longer.
nonisolated enum RecordingRolling {
    static let keepHours: Double = 6
    static let keptDays: Double = 7
    static let padMinutes: Double = 5
    static let eraseMinutes: Double = 10
    static let pullMinutes = [5, 15, 30]
    static let heartRateDelta: Double = 30          // bpm above resting
    static let quietSteps: Double = 100             // steps per 5 minutes
    static let hrvDrop: Double = 0.4                // 40% below the 30-day median
    static let autoKeptMinutesPerDay: Double = 120  // heart rate / HRV only; marks and moods are not capped
    static let unevaluatedHours: Double = 24        // prune without the health check once this old
    static let undeliveredHours: Double = 24        // the watch keeps segments the phone hasn't confirmed this long
    static let pruneInterval: Double = 300

    /// Segments past the window that haven't been checked for a reason to keep them.
    static func candidates(_ entry: RecordingEntry, now: Date) -> [RecordingSegment] {
        guard entry.isRolling else { return [] }
        let cutoff = now.addingTimeInterval(-keepHours * 3_600)
        return entry.segments.filter { segment in
            guard segment.kept == nil, let started = segment.startedAt else { return false }
            return started < cutoff
        }
    }

    /// Indexes to delete now. Unkept segments go after keepHours, or after unevaluatedHours when health data
    /// could not be read. Kept segments go after keptDays.
    static func expired(_ entry: RecordingEntry, now: Date, evaluated: Bool = true) -> [Int] {
        guard entry.isRolling else { return [] }
        let plainCutoff = now.addingTimeInterval(-(evaluated ? keepHours : unevaluatedHours) * 3_600)
        let keptCutoff = now.addingTimeInterval(-keptDays * 86_400)
        return entry.segments.filter { segment in
            guard let started = segment.startedAt else { return false }
            return started < (segment.kept == nil ? plainCutoff : keptCutoff)
        }.map(\.index)
    }

    /// Indexes the watch can delete. It can't read health data and only the phone decides what is kept, so it goes by
    /// age alone: confirmed segments (their audio is already gone) after keepHours, segments the phone hasn't confirmed
    /// after undeliveredHours, and anything near a bookmark after keptDays so the phone still gets to keep it.
    static func expiredOnWatch(_ entry: RecordingEntry, now: Date) -> [Int] {
        guard entry.isRolling else { return [] }
        let pad = padMinutes * 60
        let marks = entry.markTimes
        let sentCutoff = now.addingTimeInterval(-keepHours * 3_600)
        let unsentCutoff = now.addingTimeInterval(-undeliveredHours * 3_600)
        let markedCutoff = now.addingTimeInterval(-keptDays * 86_400)
        return entry.segments.filter { segment in
            guard let started = segment.startedAt else { return false }
            let marked = marks.contains { segment.overlaps($0.addingTimeInterval(-pad), $0.addingTimeInterval(pad)) }
            return started < (marked ? markedCutoff : segment.delivered ? sentCutoff : unsentCutoff)
        }.map(\.index)
    }

    /// Whether a bookmark is within padMinutes of this segment on the recording timeline. A late segment like this
    /// gets past the tombstone so the phone can still keep it.
    static func nearMark(_ segment: RecordingSegment, marks: [Double]) -> Bool {
        let pad = padMinutes * 60
        return marks.contains { $0 >= segment.start - pad && $0 <= segment.start + segment.duration + pad }
    }

    /// A stopped rolling recording with nothing left can go: once every segment the watch reported was pruned, or
    /// once the watch would have dropped anything still unsent (bookmarked segments wait up to keptDays there).
    /// The end message can arrive before late segments, and an erase or a gap leaves the pruned count short.
    static func finished(_ entry: RecordingEntry, now: Date) -> Bool {
        guard entry.isRolling, !entry.active, entry.segments.isEmpty, let expected = entry.expectedSegments else { return false }
        if entry.prunedCount >= expected { return true }
        guard let ended = entry.endedAt else { return false }
        let grace = entry.marks.isEmpty ? undeliveredHours * 3_600 : keptDays * 86_400
        return ended < now.addingTimeInterval(-grace)
    }
}

/// Health data for one time range, already reduced to what the keep rules need.
nonisolated struct RecordingSignals: Sendable {
    nonisolated struct Sample: Sendable, Hashable { var at: Date; var value: Double }
    nonisolated struct Span: Sendable, Hashable { var from: Date; var to: Date; var value: Double }
    var heartRate: [Sample] = []
    var restingHeartRate: Double? = nil
    var hrv: [Sample] = []
    var hrvMedian: Double? = nil
    var steps: [Span] = []
    var workouts: [Span] = []
    var moods: [Date] = []
}

nonisolated enum RecordingKeepRules {
    nonisolated private struct Event {
        var at: Date
        var reason: RecordingKeep.Reason
        var score: Double
        var capped: Bool
    }

    /// Which segments to keep, index -> reason. Never returns a segment that is already kept.
    static func evaluate(_ entry: RecordingEntry, candidates: [RecordingSegment], signals: RecordingSignals,
                         calendar: Calendar = .current) -> [Int: RecordingKeep] {
        let pad = RecordingRolling.padMinutes * 60
        func inWorkout(_ at: Date) -> Bool { signals.workouts.contains { $0.from <= at && at <= $0.to } }
        func quiet(_ at: Date) -> Bool {
            let from = at.addingTimeInterval(-150), to = at.addingTimeInterval(150)
            let steps = signals.steps.reduce(0.0) { total, span in
                let length = span.to.timeIntervalSince(span.from)
                if length <= 0 { return span.from >= from && span.from <= to ? total + span.value : total }
                let overlap = min(span.to, to).timeIntervalSince(max(span.from, from))
                return overlap > 0 ? total + span.value * overlap / length : total
            }
            return steps < RecordingRolling.quietSteps
        }

        var events = entry.markTimes.map { Event(at: $0, reason: .mark, score: 0, capped: false) }
        events += signals.moods.map { Event(at: $0, reason: .mood, score: 0, capped: false) }
        if let resting = signals.restingHeartRate {
            let limit = resting + RecordingRolling.heartRateDelta
            // A spike needs a second high sample within 10 minutes, so one noisy reading is ignored.
            let high = signals.heartRate.filter { $0.value >= limit && quiet($0.at) && !inWorkout($0.at) }
                .sorted { $0.at < $1.at }
            for (i, sample) in high.enumerated() {
                let before = i > 0 && sample.at.timeIntervalSince(high[i - 1].at) <= 600
                let after = i + 1 < high.count && high[i + 1].at.timeIntervalSince(sample.at) <= 600
                if before || after {
                    events.append(Event(at: sample.at, reason: .heartRate, score: sample.value - limit, capped: true))
                }
            }
        }
        if let median = signals.hrvMedian, median > 0 {
            let limit = median * (1 - RecordingRolling.hrvDrop)
            for sample in signals.hrv where sample.value <= limit && quiet(sample.at) && !inWorkout(sample.at) {
                events.append(Event(at: sample.at, reason: .hrv, score: (median - sample.value) / median * 100, capped: true))
            }
        }

        // Only events whose window reaches a candidate segment matter.
        let ordered = events.filter { event in
            candidates.contains { $0.overlaps(event.at.addingTimeInterval(-pad), event.at.addingTimeInterval(pad)) }
        }.sorted { a, b in
            if a.capped != b.capped { return !a.capped }
            return a.capped ? a.score > b.score : a.at < b.at
        }

        // Minutes already kept today for automatic reasons count against the daily cap.
        var budget: [Date: Double] = [:]
        for segment in entry.segments {
            guard let started = segment.startedAt, let kept = segment.kept,
                  kept.reason == .heartRate || kept.reason == .hrv else { continue }
            budget[calendar.startOfDay(for: started), default: 0] += segment.duration / 60
        }

        // Heart rate / HRV only mark candidates, so segments still inside the window don't use up the daily cap early;
        // bookmarks and moods are uncapped and keep their whole neighbourhood.
        let candidateIndexes = Set(candidates.map(\.index))
        var result: [Int: RecordingKeep] = [:]
        for event in ordered {
            let from = event.at.addingTimeInterval(-pad), to = event.at.addingTimeInterval(pad)
            let chosen = entry.segments.filter {
                $0.kept == nil && result[$0.index] == nil && $0.overlaps(from, to)
                    && (!event.capped || candidateIndexes.contains($0.index))
            }
            guard !chosen.isEmpty else { continue }
            let day = calendar.startOfDay(for: event.at)
            if event.capped {
                let minutes = chosen.reduce(0.0) { $0 + $1.duration / 60 }
                guard (budget[day] ?? 0) + minutes <= RecordingRolling.autoKeptMinutesPerDay else { continue }
                budget[day, default: 0] += minutes
            }
            for segment in chosen { result[segment.index] = RecordingKeep(reason: event.reason, at: event.at) }
        }
        return result
    }
}
