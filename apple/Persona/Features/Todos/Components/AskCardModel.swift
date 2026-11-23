import Foundation
import Observation
import PersonaWire

/// What this device did with a to-do's ask (`DecisionAnswers`, `AskCardModel`), as its row and its card say it.
nonisolated enum TodoAnswerMark: Equatable, Sendable {
    /// "✓ You chose …": held, with Undo (`undo`), or gone to the hub.
    case chosen(String, undo: Bool)
    /// The round ended before this device's answer reached it.
    case elsewhere
    /// Put off with Later for this app session.
    case later
    /// An answer of it is on its way (Don't handle): its buttons wait.
    case busy
}

/// What a To-dos row offers at its foot for its most urgent ask (#10465; DESIGN_SYSTEM 3.4 "To-dos list", the row; the
/// owner's row R, 11-21): one prominent choice and Options ⌄, after line 3. Worked out with the row's content in the
/// list's model, never in a view's body (P4); the view picks the OK's words from `okWidth` and the text size alone (P6).
nonisolated struct TodoRowAnswers: Equatable, Sendable {
    nonisolated enum Primary: Equatable, Sendable {
        /// "OK: ‹option›": a safe decision's recommendation, by its option's label.
        case ok(option: String)
        /// Retry: a start that failed, on a to-do Persona handed out (the hub lists the ask only then, S2-8).
        case retry
    }

    /// The ask it answers: the view's first, the most urgent.
    var ask: OwnerAsk
    var primary: Primary?
    /// Options ⌄: the ask's card unfolds in place under the row (`AskCard`).
    var options = false
    /// What this device did with it: "✓ You chose … · Undo" or "Answered elsewhere" in line 2's place, Later.
    var mark: TodoAnswerMark?
    /// About how wide "OK: ‹option›" is at the default text size, its capsule's padding included (`okWidth`).
    var okWidth: CGFloat = 0

    /// The round answers are kept by on the device (`DecisionAnswers.key`): the decision's, else the ask's id.
    var round: String { ask.round ?? ask.id }

    /// "OK: ‹option›", or "OK" when that would not fit `Theme.Size.todoAnswerMax` at text scale `scale` (S2-25): line 2
    /// already names the recommendation, and a button's words are never cut.
    func okTitle(scale: CGFloat) -> String {
        guard case .ok(let option)? = primary else { return "" }
        return okWidth * scale <= Theme.Size.todoAnswerMax ? Self.okLong(option) : String(localized: "OK [To-dos Answers]")
    }

    static func okLong(_ option: String) -> String { String(localized: "OK: \(option) [To-dos Answers]") }

    /// The answers' font size at the default text size (subheadline) and their capsule's padding on each side.
    static let em: CGFloat = 15
    static let padding: CGFloat = 14

    /// About how wide `text` is in the answers' font: a wide character (Chinese, Japanese, a full-width form) an em, any
    /// other 0.6 of one, generous for a semibold Latin face. Never measured: it is worked out once per row content.
    static func width(_ text: String) -> CGFloat {
        let units = text.unicodeScalars.reduce(CGFloat.zero) { sum, scalar in sum + (wide(scalar) ? 1 : 0.6) }
        return units * em + 2 * padding
    }

    private static func wide(_ s: Unicode.Scalar) -> Bool {
        switch s.value {
        case 0x1100...0x115F, 0x2E80...0xA4CF, 0xAC00...0xD7A3, 0xF900...0xFAFF, 0xFE30...0xFE4F, 0xFF00...0xFF60, 0xFFE0...0xFFE6,
             0x20000...0x3FFFD: true
        default: false
        }
    }
}

nonisolated enum TodoRowAnswersLogic {
    /// The row's answers for `view`'s first ask, while the row is in Needs you or Can wait and open: a safe decision with a
    /// recommendation among its options, OK and Options; a failed start's retry, Retry and Options; a run's safe question
    /// whose card the hub holds, Options alone (its card is the run's own form). Plans, tools, forms, secrets, open
    /// decisions and an ask without a risk (an older manager's, read as open) offer none: the row's tap opens the to-do,
    /// where the ask is. `mark`: what this device did with it.
    static func answers(_ view: TodoSummary?, section: String, closed: Bool, mark: TodoAnswerMark?) -> TodoRowAnswers? {
        guard let view, !closed, [TodoListSection.needsYou, TodoListSection.canWait].contains(section),
              let ask = view.asks?.first else { return nil }
        var out = TodoRowAnswers(ask: ask, mark: mark)
        let safe = ask.risk == OwnerAskRisk.safe
        switch ask.kind {
        case OwnerAskKind.decision where safe:
            let labels = (ask.options ?? []).map(\.label)
            if let recommend = ask.recommend, labels.isEmpty || labels.contains(recommend) {
                out.primary = .ok(option: recommend)
                out.okWidth = TodoRowAnswers.width(TodoRowAnswers.okLong(recommend))
            }
            out.options = !labels.isEmpty
        case OwnerAskKind.retry where safe:
            out.primary = .retry
            out.options = true
        case OwnerAskKind.question where safe && ask.home == OwnerAskHome.run && ask.interaction != nil:
            out.options = true
        default:
            break
        }
        guard out.primary != nil || out.options || mark != nil else { return nil }
        return out
    }
}

/// What answering a to-do's asks needs from the app (docs/apple/ios.md "Logic-layer choice"): where answers are held
/// (`DecisionAnswers`), the store whose numbers they move, the hub and its features, and where it says what it did.
/// `live` is the app's; tests pass their own and a `FakeHub`'s client.
struct AskCardDeps {
    var answers: DecisionAnswers
    var store: TodoStore
    var api: @MainActor () -> APIClient?
    /// The hub-level group answers are kept for.
    var group: @MainActor () -> String?
    var features: @MainActor () -> TodoFeatures
    var toast: @MainActor (Toast) -> Void
    /// Later on a decision also puts it off in the Needs You tray, for the same app session.
    var laterInTray: @MainActor (TodoItem) -> Void = { _ in }
    /// Don't handle through today's route (a hub without `todo_asks`): its toast offers Undo itself.
    var declineToday: @MainActor (TodoItem) async -> Bool = { _ in false }

    @MainActor static var live: AskCardDeps {
        let app = AppModel.shared
        return AskCardDeps(answers: .shared, store: .shared, api: { app.api }, group: { app.hub?.group },
                           features: { app.todoFeatures }, toast: { app.toast = $0 },
                           laterInTray: { TasksStore.shared.laterDecision($0) },
                           declineToday: { await TasksStore.shared.dismissDecision($0) })
    }
}

/// Answering a to-do's own asks from its row and its card (#10465, stage-2 plan PR 14; proposal 3.7): OK, an option and
/// Retry are held through their Undo window (`DecisionAnswers`, S2-7); Later puts an ask off for this app session; Don't
/// handle goes at once and is undone with its receipt. Every answer moves the numbers once, through the store's
/// `TodoCountsSource`. One for the app (`shared`), so a row and its card say the same thing.
@MainActor @Observable
final class AskCardModel {
    static let shared = AskCardModel(deps: .live)

    /// Asks put off with Later in this app session (`DecisionAnswers.key`).
    private(set) var later: Set<String> = []
    /// Don't handle on its way, by the same key: the card's buttons wait.
    private(set) var busy: Set<String> = []
    @ObservationIgnored private let deps: AskCardDeps
    @ObservationIgnored private var abouts: [String: String?] = [:]

    init(deps: AskCardDeps) {
        self.deps = deps
    }

    private func key(_ ask: OwnerAsk, _ todo: TodoItem) -> String? {
        deps.group().map { DecisionAnswers.key(todo: todo.id, round: ask.round ?? ask.id, group: $0) }
    }

    /// What a card says its ask is about: the recommendation's consequence, then what happens when nobody answers. Kept
    /// by ask and day (the default's day is said from today), never worked out in a view's body (P4).
    func about(_ ask: OwnerAsk, now: Date = .now, calendar: Calendar = .current) -> String? {
        let key = ask.id + "|" + String(calendar.startOfDay(for: now).timeIntervalSince1970)
        if let kept = abouts[key] { return kept }
        let pick = ask.kind == OwnerAskKind.decision ? ask.recommend ?? ask.options?.first?.label : nil
        let leads = ask.options?.first { $0.label == pick }?.leadsTo
        let fallback = ask.defaultAt.flatMap(JSON.parseDate).map { DecisionDefaultLine.text(at: $0, pick: pick, now: now, calendar: calendar) }
        let lines = [leads, fallback].compactMap { $0 }.filter { !$0.isEmpty }
        let text = lines.isEmpty ? nil : lines.joined(separator: "\n")
        if abouts.count >= 64 { abouts.removeAll() }
        abouts[key] = text
        return text
    }

    /// What this device did with `ask` (`TodoAnswerMark`): what the row and the card say in its place.
    func mark(_ ask: OwnerAsk, todo: String) -> TodoAnswerMark? {
        guard let group = deps.group() else { return nil }
        let round = ask.round ?? ask.id
        switch deps.answers.state(todo: todo, round: round, group: group) {
        case .chosen(let label)?: return .chosen(label, undo: deps.answers.canUndo(todo: todo, round: round, group: group))
        case .answered(let label)?: return .chosen(label, undo: false)
        case .elsewhere?, .alreadyHandled?: return .elsewhere
        case .busy?: return .busy
        case .sentBack?, nil: break
        }
        let key = DecisionAnswers.key(todo: todo, round: round, group: group)
        if busy.contains(key) { return .busy }
        return later.contains(key) ? .later : nil
    }

    /// The option of an open decision a first tap armed ("Confirm: …"), by its index in the ask's options.
    func armed(_ ask: OwnerAsk, todo: String) -> Int? {
        guard let group = deps.group() else { return nil }
        return deps.answers.armedOption(todo: todo, round: ask.round ?? ask.id, group: group)
    }

    /// OK: the recommendation.
    func ok(_ ask: OwnerAsk, todo: TodoItem) async {
        guard let option = ask.recommend else { return }
        await choose(option, of: ask, todo: todo)
    }

    /// One of a decision's options. An open decision (it cannot be undone, or others will see it) takes two taps: the
    /// first arms it ("Confirm: …"), as the tray's rows always did.
    func choose(_ option: String, of ask: OwnerAsk, todo: TodoItem) async {
        guard let group = deps.group() else { return }
        let arm = ask.risk == OwnerAskRisk.safe ? nil : (ask.options ?? []).firstIndex { $0.label == option }
        let answer = TodoOutbox.AskAnswer(ask: ask.id, kind: OwnerAskKind.decision, round: ask.round, option: option,
                                          label: String(localized: "You chose \(option) [To-dos Answers]"))
        later.remove(DecisionAnswers.key(todo: todo.id, round: ask.round ?? ask.id, group: group))
        await deps.answers.answer(answer, todo: todo.id, group: group, arm: arm)
    }

    /// Retry on a start that failed: started again once its Undo window ends.
    func retry(_ ask: OwnerAsk, todo: TodoItem) async {
        guard let group = deps.group() else { return }
        let answer = TodoOutbox.AskAnswer(ask: ask.id, kind: OwnerAskKind.retry, label: String(localized: "Starting again [To-dos Answers]"))
        later.remove(DecisionAnswers.key(todo: todo.id, round: ask.round ?? ask.id, group: group))
        await deps.answers.answer(answer, todo: todo.id, group: group)
    }

    /// Undo on the answer chosen for `ask`, while it is held: nothing is sent, and the ask counts again.
    func undo(_ ask: OwnerAsk, todo: TodoItem) {
        guard let group = deps.group() else { return }
        deps.answers.undo(todo: todo.id, round: ask.round ?? ask.id, group: group)
    }

    /// Later: not now, for this app session (proposal 3.7, "Three outcomes"). Nothing is written; the ask still counts,
    /// and a decision is put off in the Needs You tray too.
    func putOff(_ ask: OwnerAsk, todo: TodoItem) {
        guard let key = key(ask, todo) else { return }
        Haptics.tap()
        later.insert(key)
        if ask.kind == OwnerAskKind.decision { deps.laterInTray(todo) }
    }

    /// Back from Later: the card opens again.
    func bringBack(_ ask: OwnerAsk, todo: TodoItem) {
        guard let key = key(ask, todo) else { return }
        later.remove(key)
    }

    /// Whether Don't handle is offered: on a decision, through the asks route or today's (which needs its review round).
    func declinable(_ ask: OwnerAsk, todo: TodoItem) -> Bool {
        ask.kind == OwnerAskKind.decision && (deps.features().asks || todo.decisionReview != nil)
    }

    /// Don't handle: declined and kept by the hub at once (nothing waits for Undo, since the hub undoes it with its
    /// receipt), the ask out of the numbers, and a toast with Undo. Through the asks route while the hub lists
    /// `todo_asks`, else today's `decision-dismiss`, whose own toast offers Undo.
    func decline(_ ask: OwnerAsk, todo: TodoItem) async {
        guard let key = key(ask, todo), !busy.contains(key) else { return }
        guard deps.features().asks, let api = deps.api()?.manager(deps.store.named(todo.id)) else {
            if await deps.declineToday(todo) { deps.store.countsSource.answered(ask.id, of: todo.id) }
            return
        }
        busy.insert(key)
        defer { busy.remove(key) }
        do {
            let answer = try await api.answerAsk(todo: todo.id, ask: ask.id, TodoAskAnswer(outcome: TodoAskAnswerOutcome.decline), opID: UUID())
            deps.store.countsSource.answered(ask.id, of: todo.id)
            deps.store.reload(api: api)
            Haptics.success()
            let receipt = answer.receipt ?? ""
            deps.toast(Toast(String(localized: "Won't handle. The to-do stays open. [To-dos Answers]"), style: .success,
                             action: receipt.isEmpty ? nil : Toast.Action(title: String(localized: "Undo")) { [weak self] in
                                 Task { await self?.undoDecline(ask, todo: todo, receipt: receipt) }
                             }, duration: Theme.Motion.draftUndo))
        } catch where TodoAskRefusal.answeredElsewhere(error) {
            deps.store.reload(api: api)
        } catch {
            Haptics.error()
            deps.toast(Toast(String(localized: "Couldn't dismiss: \(error.localizedDescription)"), style: .error))
        }
    }

    /// Undo of Don't handle, with its receipt: the ask waits again and counts again.
    func undoDecline(_ ask: OwnerAsk, todo: TodoItem, receipt: String) async {
        guard let key = key(ask, todo), !busy.contains(key), let api = deps.api()?.manager(deps.store.named(todo.id)) else { return }
        busy.insert(key)
        defer { busy.remove(key) }
        do {
            _ = try await api.answerAsk(todo: todo.id, ask: ask.id, TodoAskAnswer(outcome: TodoAskAnswerOutcome.undo, receipt: receipt),
                                        opID: UUID())
            deps.store.countsSource.unanswered(ask.id, of: todo.id)
            deps.store.reload(api: api)
            Haptics.tap()
        } catch {
            Haptics.error()
            deps.toast(Toast(String(localized: "Couldn't undo: \(error.localizedDescription)"), style: .error))
        }
    }
}
