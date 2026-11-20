import Foundation

/// The live panes on a lead's Team page. The hub identifies workers and their lead; ordinary children
/// and side sessions keep their own entry points.
nonisolated enum TeamWorkers {
    static func lead(of session: Session) -> SessionKey? {
        guard session.foldedAs == "worker", !session.assistant, !SideFold.isSideRow(session) else { return nil }
        return ChildSessions.parentKey(session)
    }

    static func rows(_ rows: [Session], of lead: SessionKey) -> [Session] {
        rows.filter { self.lead(of: $0) == lead && $0.state != .offline }.sorted(by: TabPanes.inPaneOrder)
    }

    /// A partial page may contain a worker before its lead. Keep its chat addressable through the stored row.
    static func listed(_ rows: [Session]) -> [Session] {
        guard rows.contains(where: { lead(of: $0) != nil || ($0.children?.workers ?? 0) > 0 }) else { return rows }
        return rows.compactMap { row in
            guard lead(of: row) == nil else { return nil }
            guard (row.children?.workers ?? 0) > 0 else { return row }
            var copy = row
            copy.state = ChildSessions.foldedState(row)
            return copy
        }
    }

    static func shown(in rows: [Session], requested: SessionKey?, last: SessionKey?) -> Session? {
        if let requested, let match = rows.first(where: { $0.key == requested }) { return match }
        if let last, let match = rows.first(where: { $0.key == last }) { return match }
        return rows.first { $0.state == .needsYou } ?? rows.first
    }

    enum Visibility: Equatable { case open, close }

    /// Apply zero after the grace period used by the lead page. An arriving lead has no prior count.
    static func reveal(count: Int, previous: Int?, wide: Bool, manuallyClosed: Bool, requested: Bool) -> Visibility? {
        if count == 0 { return (previous ?? 0) > 0 ? .close : nil }
        if requested { return .open }
        guard wide else { return nil }
        if let previous { return previous == 0 ? .open : nil }
        return manuallyClosed ? nil : .open
    }
}
