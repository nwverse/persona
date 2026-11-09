import Foundation
import Observation

/// A new conversation's first message that did not start one (#1368): it stays on the new-conversation page, drawn as
/// the chat draws a message that did not go (`PendingBubble`, failed), with the reason, Resend and Delete, and the
/// composer stays empty. Kept across launches, like the outbox (`StateCache`); one at a time, since the page takes no
/// other first message while it is there.
@MainActor @Observable
final class FailedStart {
    static let shared = FailedStart()

    nonisolated struct Message: Codable, Equatable, Sendable {
        var text: String
        var images: [ImagePayload]
        /// The space it was started in (Health's), nil for Persona's own.
        var space: String?
        /// The op id it went with (`AssistantStart`).
        var op: UUID
        /// It is unclear whether the conversation started (cut off on the way, `202 unknown`, no answer at all):
        /// Resend asks the hub about `op` before choosing an op id.
        var mayHaveStarted: Bool
        /// Why, for the line over the composer; nil when the readiness card says it (`CodedFailure`).
        var reason: String?
        var at: Date
        /// The manager whose Persona page it was sent from (`AppModel.assistantManagerID`), nil for the default's: that
        /// page alone shows it and sends it again.
        var manager: String? = nil
    }

    private static let name = "assistant-failed-start"
    private(set) var message: Message?

    private init() { message = StateCache.load(Self.name, as: [Message].self)?.first }

    func fail(_ m: Message) { set(m) }
    func clear() { if message != nil { set(nil) } }

    private func set(_ m: Message?) {
        message = m
        StateCache.save(Self.name, m.map { [$0] } ?? []) // saved, not removed: two writes stay in order of arrival
    }

    /// Whether `error` leaves the start possibly done: the hub may have started it though the app was told otherwise.
    nonisolated static func mayHaveStarted(_ error: Error) -> Bool {
        if let e = error as? APIError {
            if e.fields["delivered"] == "unknown" || e.code == OpReceipt.unknownCode || e.code == OpReceipt.pendingCode { return true }
            if e.code != nil || (400..<500).contains(e.status) { return false }
        }
        return error.isGap || error.isOffline || error.isTimeout
    }

    /// The op id Resend goes with, as a chat's Resend chooses (`AppModel.retry`): when it may have started, the hub is
    /// asked about the old id first; done (its answer is the conversation) or still running keeps it, anything else is
    /// a new one, since the old id would be answered with its first outcome.
    static func resendOp(_ m: Message, api: APIClient, receipts: Bool) async -> UUID {
        guard receipts, m.mayHaveStarted, let state = try? await api.opState(m.op) else { return UUID() }
        return state == "done" || state == "pending" ? m.op : UUID()
    }
}
