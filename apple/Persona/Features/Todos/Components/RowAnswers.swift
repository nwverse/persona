#if os(iOS)
import SwiftUI

/// A To-dos row's answers, on one line at its foot after line 3 (#10465; DESIGN_SYSTEM 3.4 "To-dos list", the row; the
/// owner's row R): the one prominent choice, "OK: Ryokan Hanami" or Retry, in the style's prominent button, then Options
/// ⌄ in the accent, which unfolds the ask's card in place under the row (`AskCard`; ⌃ while open, the choice folding into
/// it so one primary shows). Each a 32-pt shape hit as a finger's height, `Space.m` apart; where they do not fit whole
/// (a narrow column, a long translation, a large text size) they stack, the choice first (`StackingButtonRow`, packed),
/// never cutting a label: "OK: ‹option›" says "OK" when it would pass `Theme.Size.todoAnswerMax`, decided from its
/// length and the text size (`TodoRowAnswers.okTitle`), never measured. Put off with Later, the line says so and opens
/// the card again. It reads its row's cell and the list's model, never closures (P6).
struct TodoRowAnswersLine: View {
    var row: TodosListModel.Row
    var answers: TodoRowAnswers
    var model: TodosListModel
    @Environment(\.dynamicTypeSize) private var typeSize

    var body: some View {
        if answers.mark == .later {
            laterLine
        } else {
            StackingButtonRow(spacing: Theme.Space.m, stackedSpacing: Theme.Space.s, packed: true) {
                if let primary = answers.primary, !row.optionsOpen { choice(primary) }
                if answers.options { optionsButton }
            }
            .disabled(answers.mark == .busy)
        }
    }

    private func choice(_ primary: TodoRowAnswers.Primary) -> some View {
        let words = primary == .retry ? String(localized: "Retry [To-dos Answers]") : answers.okTitle(scale: Theme.textScale(typeSize))
        // The style's own button around one line: 32 pt in Liquid Glass at the default size, hit as a finger's height.
        return Button { model.answer(row.id) } label: {
            Text(words)
                .lineLimit(1)
                .tapTargetOutset()
        }
        .font(Theme.Font.secondaryStrong)
        .themedButton(prominent: true)
        .fixedSize()
        .accessibilityLabel(primary == .retry ? words : TodoRowAnswers.okLong(answerOption(primary)))
        .accessibilityIdentifier((primary == .retry ? "todos.answer.retry." : "todos.answer.ok.") + row.todoID)
    }

    private func answerOption(_ primary: TodoRowAnswers.Primary) -> String {
        if case .ok(let option) = primary { return option }
        return ""
    }

    /// Options ⌄ / ⌃: words in the accent with the fold's chevron, as the list's folds draw theirs.
    private var optionsButton: some View {
        Button { withAnimation(Theme.Motion.reduced ? nil : Theme.Motion.state) { model.toggleOptions(row.id) } } label: {
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.xs) {
                Text("Options [To-dos Answers]")
                ThemedIcon(row.optionsOpen ? Theme.Icon.collapse : Theme.Icon.expand)
                    .font(Theme.Font.labelHeavy)
                    .accessibilityHidden(true)
            }
            .lineLimit(1)
            .foregroundStyle(Theme.accent)
            .scaledFrame(minHeight: Theme.Size.well)
            .tapTargetOutset()
        }
        .buttonStyle(.plain)
        .font(Theme.Font.secondaryMedium)
        .fixedSize()
        .accessibilityValue(row.optionsOpen ? String(localized: "Expanded") : String(localized: "Collapsed [Readiness Card ee6046]"))
        .accessibilityIdentifier("todos.answer.options." + row.todoID)
    }

    /// Later: "Later today ›", which opens the card again.
    private var laterLine: some View {
        Button { withAnimation(Theme.Motion.reduced ? nil : Theme.Motion.state) { model.toggleOptions(row.id) } } label: {
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.xs) {
                Text("Later today [To-dos Answers]")
                ThemedIcon(Theme.Icon.forward)
                    .font(Theme.Font.labelHeavy)
                    .foregroundStyle(.tertiary)
                    .accessibilityHidden(true)
            }
            .lineLimit(1)
            .foregroundStyle(.secondary)
            .scaledFrame(minHeight: Theme.Size.well)
            .tapTargetOutset()
        }
        .buttonStyle(.plain)
        .font(Theme.Font.secondary)
        .fixedSize()
        .accessibilityIdentifier("todos.answer.later." + row.todoID)
    }
}
#endif
