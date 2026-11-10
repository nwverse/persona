import Foundation

/// One hub group's last successful watch fetch (docs/protocol/multi-hub.md「Watch, Siri, Mac, and demo mode」): the live sessions, pending cards and
/// machines its own hub returned. A failed refresh keeps the group's last value, so a group's cards are never
/// replaced with nothing just because that group did not answer this time (#4369).
nonisolated struct WatchGroupData: Equatable, Sendable {
    var sessions: [Session]
    var interactions: [Interaction]
    var machines: [Machine]

    static let empty = WatchGroupData(sessions: [], interactions: [], machines: [])
}

/// Pure decisions behind the watch's refresh: keeping a failed group's last cards, never buzzing twice for the same
/// card, and what a phone summary carries and whether it is still news. The watch and its tests use these; Foundation only, no UI.
nonisolated enum WatchGroupRefresh {
    /// Keep each failed group's last successful data and replace every group that answered. Groups not attempted this
    /// round (no credential yet) keep their old data as well. `staleFailed` lists the failed groups whose earlier data
    /// stays on screen, sorted for a stable banner order; a group that failed before its first success is absent —
    /// there is no earlier data of it to show.
    static func mergeGroups(old: [String: WatchGroupData], new: [String: WatchGroupData], failures: Set<String>)
        -> (data: [String: WatchGroupData], staleFailed: [String]) {
        var stored = old
        for (group, data) in new { stored[group] = data }
        return (stored, failures.filter { stored[$0] != nil }.sorted())
    }

    /// Whether a phone summary is news to the watch. Its application context replays at launch and can arrive late, so
    /// a summary older than what the watch already shows (say, its own direct fetch) must not put old state back (#6257).
    static func acceptsSummary(at: Date, lastUpdate: Date?) -> Bool {
        guard let lastUpdate else { return true }
        return at > lastUpdate
    }

    /// When a summary's data dates from: its own time, never later than now, in case the phone's clock runs ahead.
    static func dataDate(ofSummaryAt at: Date, now: Date = .now) -> Date { min(at, now) }

    /// The agents a phone summary carries, most recently updated first: everyone who needs the user or is working,
    /// however long ago they last updated, and the others up to `limit` in all. The watch replaces its list with the
    /// summary, so a busy agent left out would vanish from the wrist until the next direct fetch (#6257).
    static func summarySessions(_ sessions: [Session], limit: Int = 20) -> [Session] {
        func busy(_ s: Session) -> Bool { s.state == .needsYou || s.state == .working }
        var room = limit - sessions.filter(busy).count
        return sessions.sorted { ($0.updatedAt ?? .distantPast) > ($1.updatedAt ?? .distantPast) }.filter { s in
            if busy(s) { return true }
            guard room > 0 else { return false }
            room -= 1
            return true
        }
    }

    /// The uids the wrist has never been told about: only those get a haptic. A card that vanished with a failed
    /// group and returns when it recovers is already in `seen`, so it stays quiet.
    static func newHapticUids(seen: Set<String>, current: [Interaction]) -> [String] {
        var seen = seen
        return current.map(\.uid).filter { seen.insert($0).inserted }
    }

    /// The uids already buzzed for, oldest first and de-duplicated. New uids append; when more than `limit` are kept
    /// the oldest drop, so the persisted record cannot grow without end. A card answered on the wrist is removed by
    /// the caller before the next save.
    static func rememberSeen(_ stored: [String], adding uids: [String], limit: Int = 200) -> [String] {
        var known = Set(stored)
        var out = stored
        for uid in uids where known.insert(uid).inserted { out.append(uid) }
        if out.count > limit { out.removeFirst(out.count - limit) }
        return out
    }
}
