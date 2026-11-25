@testable import Persona

extension Session {
    /// The row as the hub's next list sends it once the owner saw it: a finished turn reads as seen. The app never makes
    /// this change itself (an optimistic update never invents a state, docs/plans/plan-typed-core.md 2.4); tests that
    /// follow a row past being seen take the hub's answer from here.
    mutating func seenByTheHub() {
        if state == .unread { state = .idle }
        if work?.justFinished == true { work?.justFinished = nil }
        view = nil
    }
}
