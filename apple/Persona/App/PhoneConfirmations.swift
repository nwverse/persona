import Foundation
import Observation

/// One phone write waiting for the user's Allow / Deny (docs/apple/phone-tools.md "Confirmation flow").
struct PhoneConfirmation: Identifiable, Equatable {
    let id = UUID()
    let summary: String
    /// The pairing that received this call. Another hub may be selected in Settings.
    let hub: ObjectIdentifier?
}

/// The phone's writes ask the user one at a time (#4456): a second write that comes while the first is on screen
/// waits behind it and shows once the first is answered, instead of taking its place and leaving the first call
/// unanswered until the hub stops waiting. Each answer goes to the confirmation it names (`PhoneConfirmation.id`) and
/// counts once: a dialog's dismissal that arrives after its button already answered names the one it showed, which is
/// answered already, so it does nothing to the next one.
@MainActor @Observable final class PhoneConfirmations {
    /// The one on screen.
    private(set) var current: PhoneConfirmation?
    @ObservationIgnored private var waiting: [PhoneConfirmation] = []
    @ObservationIgnored private var answers: [UUID: CheckedContinuation<Bool, Never>] = [:]

    /// Asks the user about `summary`: shown now, or after the ones before it are answered.
    func ask(_ summary: String, hub: ObjectIdentifier? = nil) async -> Bool {
        await withCheckedContinuation { cont in
            let c = PhoneConfirmation(summary: summary, hub: hub)
            answers[c.id] = cont
            if current == nil { current = c } else { waiting.append(c) }
        }
    }

    /// The user's answer to `id` (its Allow / Deny, or Deny when its dialog went away unanswered). Nothing for an id that
    /// is answered already or unknown.
    func answer(_ id: UUID, _ allow: Bool) {
        guard let cont = answers.removeValue(forKey: id) else { return }
        waiting.removeAll { $0.id == id }
        if current?.id == id { current = waiting.isEmpty ? nil : waiting.removeFirst() }
        cont.resume(returning: allow)
    }

    /// Every confirmation shown or waiting, denied (the pairing is gone: nobody can answer them any more).
    func denyAll() {
        let all = answers
        answers = [:]
        waiting = []
        current = nil
        for cont in all.values { cont.resume(returning: false) }
    }

    /// How many are waiting behind the one on screen.
    var queued: Int { waiting.count }
}
