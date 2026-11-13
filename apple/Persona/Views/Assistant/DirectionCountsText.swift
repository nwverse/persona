import Foundation

/// A direction's work under its row in the sidebar (#4331): "N working · N need you", only the parts that are not
/// zero, and nothing while it holds nothing. Kept for the fold chevron's VoiceOver label and the To-dos / Chats
/// section headers; the sidebar row itself no longer shows the working half (#6626).
nonisolated enum DirectionCountsText {
    /// The line, or nil when neither part is non-zero.
    static func label(working: Int, needsYou: Int) -> String? {
        switch (working > 0, needsYou > 0) {
        case (true, true): return String(localized: "\(working) working · \(needsYou) need you")
        case (true, false): return String(localized: "\(working) working")
        case (false, true): return String(localized: "\(needsYou) need you")
        case (false, false): return nil
        }
    }

    /// The lead row's one attention phrase (#6626): "N need you", or nil. The busy half is the spinner, so no working
    /// count is spelled out on the row.
    static func needsYouLabel(needsYou: Int) -> String? {
        needsYou > 0 ? String(localized: "\(needsYou) need you") : nil
    }
}
