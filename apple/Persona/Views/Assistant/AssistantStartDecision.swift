import Foundation

/// What a first-message completion may change after awaiting the hub. Accepted results still count as begun; only the
/// visible page, feedback and failure UI become stale when a newer New Chat has taken over.
nonisolated struct AssistantStartDecision: Equatable, Sendable {
    enum Haptic: Equatable, Sendable { case success, error }
    enum FailureAction: Equatable, Sendable { case none, clear, show }

    let began: Bool
    let open: Bool
    let haptic: Haptic?
    let clearPending: Bool
    let failure: FailureAction

    init(startGeneration: Int, currentGeneration: Int, pendingOp: UUID?, op: UUID,
         succeeded: Bool, space: String?, showsPage: Bool) {
        let current = startGeneration == currentGeneration
        let here = space != nil || showsPage
        began = succeeded
        open = succeeded && current && here
        haptic = current ? (succeeded ? .success : .error) : nil
        clearPending = pendingOp == op
        failure = succeeded && current ? .clear : !succeeded && current && here ? .show : .none
    }
}
