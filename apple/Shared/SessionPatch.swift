import Foundation

/// How a machine's session list is kept from what the hub sends (docs/protocol/protocol.md「Session deltas」). An older hub sends the
/// whole list each time; a hub with `sessions_delta` sends it once on connect, then the sessions that changed (whole)
/// and the ids that went, and leaves the Codex history out of both: the app has that from GET /api/v1/sessions.
nonisolated enum SessionPatch {
    static let feature = "sessions_delta"

    /// A delta over `base`: changed sessions replace theirs in place, new ones go after, gone ones go.
    static func apply(_ base: [Session], upsert: [Session], remove: [String]) -> [Session] {
        let gone = Set(remove)
        var byID = Dictionary(upsert.map { ($0.sessionID, $0) }, uniquingKeysWith: { _, b in b })
        var out = base.compactMap { s -> Session? in
            if gone.contains(s.sessionID) { return nil }
            return byID.removeValue(forKey: s.sessionID) ?? s
        }
        out += upsert.filter { byID[$0.sessionID] != nil } // new: in the order the hub sent them
        return out
    }

    /// A whole list from the hub. From a delta hub it has no Codex history: the history the app already has stays.
    static func snapshot(_ list: [Session], over old: [Session], deltaHub: Bool) -> [Session] {
        guard deltaHub else { return list }
        let ids = Set(list.map(\.sessionID))
        return list + old.filter { $0.isCodexHistory && !ids.contains($0.sessionID) }
    }

    /// The lists from GET /api/v1/sessions over what the app has. While a delta hub's socket is connected, what it
    /// pushed is newer than this answer can be (the request went out before): only the Codex history comes from it,
    /// the history the app has included. No push carries it, so this answer is the only way its state moves (seen,
    /// gone offline, #2425). Otherwise the answer is the whole truth.
    static func refreshed(_ rest: [Session], over old: [Session], connectedToDeltaHub: Bool) -> [Session] {
        guard connectedToDeltaHub else { return rest }
        let ids = Set(old.map(\.sessionID))
        let fresh = Dictionary(rest.filter(\.isCodexHistory).map { ($0.sessionID, $0) }, uniquingKeysWith: { _, b in b })
        let history = rest.filter { $0.isCodexHistory && !ids.contains($0.sessionID) }
        // History that is gone from the machine goes, the rest is the hub's copy; the pushed sessions stay as pushed.
        let kept = old.compactMap { s -> Session? in s.isCodexHistory ? fresh[s.sessionID] : s }
        return kept + history
    }
}

nonisolated extension Session {
    /// A Codex session known only from its rollout on the machine (`source_kind: codex_history`).
    var isCodexHistory: Bool { sourceKind == "codex_history" }
}
