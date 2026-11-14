import Foundation

/// Sessions drawn under the session that started them while staying sessions of their own (#5935): a Claude desktop
/// app's side chat, /btw question or spawned session names its parent in `parent_agent_id` (`Session.parentAgentID`,
/// docs/protocol/protocol.md "Session hierarchy"). Unlike a subtask (`CodexTasks`) it keeps its row, its state, its
/// unread and its place in every count; a list only moves it under its parent, a level in. The web's rules
/// (`web/src/lib/side-sessions.ts`), as a plain function of the rows so they are unit-tested (`SideSessionsTests`).
nonisolated enum SideSessions {
    /// Rows arranged by `nestUnderParents`.
    nonisolated struct Arranged<Row, Key: Hashable> {
        /// The rows in the order to draw them: each group where its first member was, its top first, then the rest.
        var rows: [Row]
        /// The keys of the rows drawn a level in, under their group's top.
        var nested: Set<Key> = []
        /// Each moved group's member's lead: the group's first row given, the one that places it. Empty when nothing
        /// moved; a row not in it leads itself (`lead(of:)`).
        var lead: [Key: Key] = [:]
        /// Beside `rows` when something moved: each row's group (0, 1, …), -1 for a row in none. Empty otherwise.
        var group: [Int] = []

        /// The row that places `key`'s group: the first of it in the order given (the most urgent, or the most recent).
        func lead(of key: Key) -> Key { lead[key] ?? key }

        /// The rows that stay out of a fold and those that fold, keeping each group together: a group stays whole
        /// while any member of it `stays`, so a side session in view keeps its parent and siblings in view with it,
        /// and a busy parent its idle side sessions.
        func split(_ stays: (Row) -> Bool) -> (stay: [Row], fold: [Row]) {
            guard group.count == rows.count, !group.isEmpty else { return (rows.filter(stays), rows.filter { !stays($0) }) }
            let own = rows.map(stays)
            var groupStays = [Bool](repeating: false, count: (group.max() ?? -1) + 1)
            for i in rows.indices where group[i] >= 0 && own[i] { groupStays[group[i]] = true }
            var stay: [Row] = [], fold: [Row] = []
            for i in rows.indices {
                if group[i] >= 0 ? groupStays[group[i]] : own[i] { stay.append(rows[i]) } else { fold.append(rows[i]) }
            }
            return (stay, fold)
        }

        /// How many of `shown` (these rows, or a part of them `split` kept) to show before a "Show All" row so that
        /// it splits no group: `cap`, or past the side sessions right after it.
        func unsplit(_ cap: Int, _ shown: [Row], key: (Row) -> Key) -> Int {
            guard !nested.isEmpty, cap > 0, cap < shown.count else { return cap }
            var end = cap
            while end < shown.count && nested.contains(key(shown[end])) { end += 1 }
            return end
        }
    }

    /// Arranges `rows` (in the list's order) so each row whose parent is among them comes under that parent. A row
    /// nests under its topmost ancestor in `rows`, one level in whatever its depth; a row whose parent is not in `rows`
    /// (another page, another section, archived, filtered out) stays where it is; a loop links nothing. A group sits
    /// where its first member was: the top first, then its other members in their own order. Nothing is added or left
    /// out, so every count over the rows stays the same. `parentKey` gives the key of a row's parent as `key` gives a
    /// row's.
    ///
    /// Only a row that names a parent can move, and only under a row one of them names: every row is read once, and
    /// what is built is for those few (a list of thousands with one side session costs a pass, not a map of it).
    static func nestUnderParents<Row, Key: Hashable>(_ rows: [Row], key: (Row) -> Key, parentKey: (Row) -> Key?) -> Arranged<Row, Key> {
        var parentOf: [Int: Key] = [:] // a row that names a parent → that parent's key
        for (i, row) in rows.enumerated() { if let up = parentKey(row) { parentOf[i] = up } }
        guard !parentOf.isEmpty else { return Arranged(rows: rows) }
        let wanted = Set(parentOf.values)
        var indexOf: [Key: Int] = [:] // a named parent's row
        for (i, row) in rows.enumerated() {
            let k = key(row)
            if wanted.contains(k), indexOf[k] == nil { indexOf[k] = i }
        }
        // A row under a listed parent → its topmost listed ancestor; a row in a loop stays a top of its own.
        var topOf: [Int: Int] = [:]
        for i in parentOf.keys.sorted() {
            var seen: Set<Int> = [i]
            var at = i
            while let up = parentOf[at], let parent = indexOf[up] {
                guard seen.insert(parent).inserted else { at = i; break }
                at = parent
            }
            if at != i { topOf[i] = at }
        }
        guard !topOf.isEmpty else { return Arranged(rows: rows) }
        var members: [Int: [Int]] = [:] // a top → the rows under it, in list order
        for i in topOf.keys.sorted() { members[topOf[i] ?? i, default: []].append(i) }
        var out: [Row] = [], group: [Int] = []
        out.reserveCapacity(rows.count)
        group.reserveCapacity(rows.count)
        var nested = Set<Key>(), lead: [Key: Key] = [:], placed = Set<Int>()
        for i in rows.indices {
            let top = topOf[i] ?? i
            guard let under = members[top] else { // in no group
                out.append(rows[i])
                group.append(-1)
                continue
            }
            guard placed.insert(top).inserted else { continue } // its group is drawn already, where its first member was
            let number = placed.count - 1, first = key(rows[min(top, under[0])])
            out.append(rows[top])
            group.append(number)
            lead[key(rows[top])] = first
            for m in under {
                out.append(rows[m])
                group.append(number)
                nested.insert(key(rows[m]))
                lead[key(rows[m])] = first
            }
        }
        return Arranged(rows: out, nested: nested, lead: lead, group: group)
    }

    /// `sessions` (in the list's order) with each side session under the session that started it, when that one is
    /// among them. Most lists have none: they come back as they are, with nothing built.
    static func arrange(_ sessions: [Session]) -> Arranged<Session, SessionKey> {
        guard sessions.contains(where: { $0.parentAgentID != nil }) else { return Arranged(rows: sessions) }
        return nestUnderParents(sessions, key: \.key, parentKey: \.sideParent)
    }
}

extension SideSessions.Arranged: Sendable where Row: Sendable, Key: Sendable {}

nonisolated extension SideSessions.Arranged where Row == Session, Key == SessionKey {
    /// Levels a row goes in past its section's: 1 for a side session drawn under its parent, else 0.
    func level(_ s: Session) -> Int { nested.contains(s.key) ? 1 : 0 }
    /// `unsplit` over `shown`, these rows when nil.
    func unsplit(_ cap: Int, _ shown: [Session]? = nil) -> Int { unsplit(cap, shown ?? rows, key: \.key) }
}

extension Session {
    /// The session it is drawn under (`parentAgentID`, #5935): on its own hub and machine; nil for none, and for a row
    /// naming itself.
    nonisolated var sideParent: SessionKey? {
        guard let parent = parentAgentID, !parent.isEmpty, parent != sessionID else { return nil }
        return SessionKey(group: group, machineID: machineID, sessionID: parent)
    }
}
