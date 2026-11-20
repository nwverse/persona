import Foundation

/// The sidebar keeps Agent execution counts separate from to-dos waiting on the user.
nonisolated enum DirectionCountsText {
    /// The words, or nil when nothing waits on you.
    static func label(needsYou: Int) -> String? {
        needsYou > 0 ? String(localized: "\(needsYou) need you") : nil
    }

    static func agents(_ counts: DirectionTree.AgentCounts) -> String {
        String(localized: "Running \(counts.running) · Stopped \(counts.stopped)")
    }
}
