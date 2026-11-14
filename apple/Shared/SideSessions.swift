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
        /// Each row drawn a level in → its group's top, the row it is drawn under. Empty when nothing moved.
        var top: [Key: Key] = [:]
        /// Session rows (`arrange`): the side sessions under each top, for its Side sessions row (#7755). Empty otherwise.
        var sides = Groups()

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
        var nested = Set<Key>(), lead: [Key: Key] = [:], topKey: [Key: Key] = [:], placed = Set<Int>()
        for i in rows.indices {
            let top = topOf[i] ?? i
            guard let under = members[top] else { // in no group
                out.append(rows[i])
                group.append(-1)
                continue
            }
            guard placed.insert(top).inserted else { continue } // its group is drawn already, where its first member was
            let number = placed.count - 1, first = key(rows[min(top, under[0])]), head = key(rows[top])
            out.append(rows[top])
            group.append(number)
            lead[head] = first
            for m in under {
                out.append(rows[m])
                group.append(number)
                nested.insert(key(rows[m]))
                lead[key(rows[m])] = first
                topKey[key(rows[m])] = head
            }
        }
        return Arranged(rows: out, nested: nested, lead: lead, group: group, top: topKey)
    }

    /// `sessions` (in the list's order) with each side session under the session that started it, when that one is
    /// among them. Most lists have none: they come back as they are, with nothing built.
    static func arrange(_ sessions: [Session]) -> Arranged<Session, SessionKey> {
        guard sessions.contains(where: { $0.parentAgentID != nil }) else { return Arranged(rows: sessions) }
        var arranged = nestUnderParents(sessions, key: \.key, parentKey: \.sideParent)
        arranged.sides = Groups(arranged, key: \.key, sessions: { [$0] })
        return arranged
    }

    /// Where the Side sessions row under `parent` is kept folded (`dev.folded`, #7755): "side:" and the parent's key
    /// path, so it is no other fold's id (theirs start "state/", "machine/", "app/", "repo/", "tree/", "tab/", "todo:",
    /// "direction:", or with a machine's path and "/").
    static func foldID(_ parent: SessionKey) -> String { "side:" + parent.path }

    /// The folds of every parent among `sessions` with a side session among them (Collapse All, #7755). Most lists
    /// have none: one scan.
    static func foldIDs(_ sessions: [Session]) -> [String] {
        let parents = Set(sessions.lazy.compactMap(\.sideParent))
        guard !parents.isEmpty else { return [] }
        return sessions.filter { parents.contains($0.key) }.map { foldID($0.key) }
    }

    /// A section's side sessions by the row they are drawn under (#7755): what each "Side sessions (N)" row folds.
    nonisolated struct Groups: Equatable, Sendable {
        nonisolated struct Under: Equatable, Sendable {
            /// The session they were started from, whose fold (`foldID`) folds them: the top row's, or of a tab's
            /// agents the one the first of them names.
            var parent: SessionKey
            /// How many are under it, in view or folded away.
            var count: Int
        }

        /// By the session whose row the Side sessions row goes after: the top's, or a tab's last agent.
        var under: [SessionKey: Under] = [:]
        /// Each side session drawn a level in → the key of `under` it is counted in.
        var top: [SessionKey: SessionKey] = [:]

        var isEmpty: Bool { under.isEmpty }

        init() {}

        /// From rows as `nestUnderParents` arranged them, each group's top then the rows under it; `sessions` gives a
        /// row's agents (one, or a tab's in pane order).
        init<Row, Key: Hashable>(_ arranged: Arranged<Row, Key>, key: (Row) -> Key, sessions: (Row) -> [Session]) {
            guard !arranged.top.isEmpty else { return }
            var under: [SessionKey: Under] = [:], top: [SessionKey: SessionKey] = [:]
            var heads: [Session] = [], sides: [Session] = []
            func close() {
                guard let anchor = heads.last, !sides.isEmpty else { return }
                let parent = heads.count == 1 ? anchor.key
                    : sides.lazy.compactMap(\.sideParent).first { k in heads.contains { $0.key == k } } ?? heads[0].key
                under[anchor.key] = Under(parent: parent, count: sides.count)
                for s in sides { top[s.key] = anchor.key }
            }
            for row in arranged.rows {
                if arranged.top[key(row)] != nil {
                    sides += sessions(row)
                } else {
                    close()
                    (heads, sides) = (sessions(row), [])
                }
            }
            close()
            (self.under, self.top) = (under, top)
        }

        /// These groups with each one's fold as `isFolded` (a fold id) has it now.
        func folding(_ isFolded: (String) -> Bool) -> Folds {
            guard !isEmpty else { return Folds() }
            return Folds(groups: self, folded: Set(under.filter { isFolded(SideSessions.foldID($0.value.parent)) }.keys))
        }
    }

    /// A section's side sessions as drawn now (#7755): open unless folded. Folding hides rows, not sessions: every
    /// count stays as it was, and a side session that needs the user stays a row of its own a level in
    /// (docs/design/DESIGN_SYSTEM.md 3.4), as a subtask that does is never folded.
    nonisolated struct Folds: Sendable {
        nonisolated struct Fold: Equatable, Sendable {
            /// Its fold id (`foldID`).
            var id: String
            var count: Int
            var open: Bool
        }

        var groups = Groups()
        /// The keys of `groups.under` folded now.
        var folded: Set<SessionKey> = []

        /// Left out while its group is folded: a side session that does not need the user.
        func hides(_ s: Session) -> Bool {
            guard !folded.isEmpty, let top = groups.top[s.key], folded.contains(top) else { return false }
            return s.countedState != .needsYou
        }

        /// The Side sessions row after `key`'s row, when side sessions are drawn under it.
        func fold(after key: SessionKey) -> Fold? {
            guard let under = groups.under[key] else { return nil }
            return Fold(id: SideSessions.foldID(under.parent), count: under.count, open: !folded.contains(key))
        }
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
