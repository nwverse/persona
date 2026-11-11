import Foundation

/// The phone's view of a day for the hub (docs/assistant/day-recap.md): `coverage` in every create, repair and append,
/// sent whole each time. `to` is how far, in seconds since the part's `started_at`, every chunk the phone holds is
/// transcribed AND on the hub after the request that carries it (C19): the hub reads the day only up to there, so a
/// `to` past text it hasn't received yet would make it note an hour as finished without that text. `gaps` is the time
/// with no text and why (`RecordingDayGap.gaps(in:since:)`, the same the day's page shows), cut to the part and to at
/// most `maxGaps`. Pure: computed from the record as it is, for the request about to go.
nonisolated struct RecordingDayCoverage: Codable, Sendable, Equatable {
    var to: Double
    var gaps: [RecordingDayGap]
}

nonisolated extension RecordingDayCoverage {
    /// The contract's limit on `gaps`.
    static let maxGaps = 500
    /// A chunk that ends this little before the part starts still counts as the part's (clock rounding).
    private static let edge = 0.001

    /// Coverage of `part` for a request after which the hub holds this part's positions below `sentToPositionBelow`.
    /// - Covered chunks: transcribed with every sentence placed and each of this part's positions below that count;
    ///   skipped, failed (reported as `untranscribed`), erased, lost, and the header-only end marker (a point at the
    ///   day's end, so `to` reaches `ended_at`). Chunks of another part's time don't count either way.
    /// - A seq the phone doesn't hold stops `to` until the first later chunk the phone holds arrived `gapWaitSeconds`
    ///   ago (its persisted `receivedAt`, so a relaunch doesn't restart the wait); `to` may pass it then, and the gap stays
    ///   `pending` in `gaps` until the chunk arrives. A seq the watch reported lost never stops it.
    /// - A part ended by `413 day_full` takes no new text (it goes to the next part), so only its own positions hold `to`
    ///   back: it reaches the part's end once the request leaves every one of them on the hub, and stops before the
    ///   first chunk holding one it doesn't (reconciliation found the hub lost them, and they go again).
    static func compute(record: RecordingDayRecord, part: RecordingDayPart, sentToPositionBelow sent: Int, now: Date) -> RecordingDayCoverage {
        let base = part.startedAt
        let end = part.endedAt.map { $0.timeIntervalSince(base) }
        let walk = reach(record: record, part: part, sent: sent, now: now)
        let to: Double
        if let end {
            to = max(0, walk.stopped ? min(walk.to, end) : end)
        } else {
            to = max(0, walk.to)
        }
        var gaps = RecordingDayGap.gaps(in: record, since: base)
        if let end {
            gaps = gaps.compactMap { gap in
                guard gap.from < end else { return nil }
                return RecordingDayGap(from: gap.from, to: min(gap.to, end), reason: gap.reason)
            }
        }
        return RecordingDayCoverage(to: to, gaps: capped(gaps, to: maxGaps))
    }

    /// Whether a request with this coverage can say `complete` (the contract: `ended_at` set, no gap `pending`, every
    /// chunk transcribed and appended). Skipped, erased, lost and failed chunks are settled. A part that `413 day_full`
    /// ended is complete once its own positions are all on the hub and no gap in its time is pending; the last part needs
    /// the day's end marker and every seq below it.
    static func isComplete(record: RecordingDayRecord, part: RecordingDayPart, sentToPositionBelow sent: Int,
                           coverage: RecordingDayCoverage, isLast: Bool) -> Bool {
        guard sent >= part.count, !coverage.gaps.contains(where: { $0.reason == RecordingDayGap.Reason.pending }) else { return false }
        guard isLast else { return part.endedAt != nil }
        guard record.endedAt != nil, let expected = record.expected else { return false }
        for seq in 0..<expected {
            guard let chunk = record.chunks[seq] else {
                if record.lost.contains(seq) { continue }
                return false
            }
            if chunk.needsTranscription || chunk.hasUnplacedText { return false }
        }
        return !record.chunks.values.contains { $0.seq >= expected && ($0.needsTranscription || $0.hasUnplacedText) }
    }

    /// When a missing seq's wait (`gapWaitSeconds` from the first later chunk's arrival) ends after `now`, the soonest:
    /// `to` may move past it then, so the upload looks again. nil when nothing is waiting.
    static func nextGapExpiry(record: RecordingDayRecord, now: Date) -> Date? {
        let held = record.chunks.keys.sorted()
        guard let last = held.last else { return nil }
        var soonest: Date?
        var index = 0
        var seq = 0
        while seq < last {
            if record.chunks[seq] != nil || record.lost.contains(seq) {
                seq += 1
                continue
            }
            // The first held chunk after this missing run.
            while index < held.count, held[index] <= seq { index += 1 }
            guard index < held.count, let later = record.chunks[held[index]] else { break }
            let expiry = later.receivedAt.addingTimeInterval(RecordingDayContract.gapWaitSeconds)
            if expiry > now { soonest = min(soonest ?? expiry, expiry) }
            seq = held[index]
        }
        return soonest
    }

    /// How far the part's covered chunks reach, walking seqs in order, and whether a chunk (or a seq the phone is still
    /// waiting for) stopped the walk before the last one (`compute`).
    private static func reach(record: RecordingDayRecord, part: RecordingDayPart, sent: Int, now: Date) -> (to: Double, stopped: Bool) {
        let base = part.startedAt
        // A part `day_full` closed: text not on its positions goes to a later part, so only its own positions count.
        let closed = part.endedAt != nil
        let held = record.chunks.keys.sorted()
        let last = max(held.last ?? -1, (record.expected ?? 0) - 1)
        guard last >= 0 else { return (0, false) }
        var to = 0.0
        var index = 0
        for seq in 0...last {
            if let chunk = record.chunks[seq] {
                // Before the part's start: an earlier part's time, neither covering nor holding this one up.
                if chunk.duration > 0, chunk.endedAt.timeIntervalSince(base) <= edge { continue }
                let clear = closed ? !holdsUnsent(chunk, part: part.part, sent: sent) : covered(chunk, part: part.part, sent: sent)
                guard clear else { return (to, true) }
                to = max(to, chunk.endedAt.timeIntervalSince(base))
            } else if !closed, !record.lost.contains(seq) {
                // The watch still holds it: wait for it, until the chunk after it has been here `gapWaitSeconds`.
                while index < held.count, held[index] <= seq { index += 1 }
                guard index < held.count, let later = record.chunks[held[index]] else { return (to, true) }
                if later.startedAt.timeIntervalSince(base) <= edge { continue } // an earlier part's time
                guard now.timeIntervalSince(later.receivedAt) >= RecordingDayContract.gapWaitSeconds else { return (to, true) }
            }
        }
        return (to, false)
    }

    /// Whether a chunk holds nothing back for `part`: no audio to read, no text, or its text placed and on the hub.
    private static func covered(_ chunk: RecordingDayChunk, part: Int, sent: Int) -> Bool {
        if chunk.isMarker { return true }
        switch chunk.state {
        case .skipped, .failed:
            return true
        case .waiting:
            return false
        case .transcribed:
            guard !chunk.hasUnplacedText else { return false }
            return !holdsUnsent(chunk, part: part, sent: sent)
        }
    }

    /// Whether a chunk has a sentence at one of `part`'s positions the hub won't hold after the request.
    private static func holdsUnsent(_ chunk: RecordingDayChunk, part: Int, sent: Int) -> Bool {
        (chunk.positions ?? []).contains { $0.part == part && $0.pos >= sent }
    }

    /// At most `limit` gaps: while there are more, the two neighbours with the least time between them become one,
    /// under the reason of the longer of the two (the shortest interruptions of the text are the ones folded away).
    static func capped(_ gaps: [RecordingDayGap], to limit: Int) -> [RecordingDayGap] {
        var out = gaps
        while out.count > max(1, limit) {
            var best = 0
            var space = Double.infinity
            for i in 0..<(out.count - 1) {
                let between = out[i + 1].from - out[i].to
                if between < space {
                    space = between
                    best = i
                }
            }
            let a = out[best], b = out[best + 1]
            let reason = (a.to - a.from) >= (b.to - b.from) ? a.reason : b.reason
            out[best] = RecordingDayGap(from: a.from, to: max(a.to, b.to), reason: reason)
            out.remove(at: best + 1)
        }
        return out
    }
}
