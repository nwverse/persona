import SwiftUI

// What the sidebar, the To-dos page and the Agents page draw of the hub's direction tree (#4352, `DirectionTree`): one
// state word per to-do in one tone, and a direction's two counts. The tree decides both; these only draw them.

extension DirectionTree.TaskState {
    /// The word's color: Needs You's for needs you, Working's for working, Caution's for stuck and to verify (the words
    /// say which), and the quiet idle grey for not started (#5860, the web's text-ink-3).
    var tone: Color {
        switch self {
        case .needsYou: StatusPalette.needsYou.text
        case .working: StatusPalette.working.text
        case .stuck, .review: Theme.cautionText
        case .planned: StatusPalette.idle.text
        }
    }
}

/// A to-do's state as a word in its tone (`DirectionTree.TaskState`).
struct DirectionTaskStateWord: View {
    var state: DirectionTree.TaskState
    var font: Font = Theme.Font.caption

    var body: some View {
        Text(state.text)
            .font(font)
            .foregroundStyle(state.tone)
            .lineLimit(1)
    }
}

nonisolated extension DirectionTree.Counts {
    /// "N working · N need you", only the parts that are not zero (`DirectionCountsText`, the sidebar's lead rows'
    /// words); nil while the direction holds neither.
    var label: String? { DirectionCountsText.label(working: working, needsYou: needsYou) }
}

/// A direction's counts in a To-dos or Agents section header: the sidebar's words (`DirectionTree.Counts.label`), and
/// nothing while both are zero.
struct DirectionCountsLine: View {
    var counts: DirectionTree.Counts
    var font: Font = Theme.Font.caption

    var body: some View {
        if let label = counts.label {
            Text(label)
                .font(font.monospacedDigit())
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
    }
}

/// The To-dos page grouped by Pinned direction (#4352): the hub's tree decides which to-dos are open under which
/// direction; the page draws its own rows for them.
nonisolated enum DirectionTodoSections {
    nonisolated struct Section: Identifiable, Sendable {
        var direction: DirectionTree.Direction
        var todos: [TodoItem]
        var id: String { direction.id }
    }

    /// The tree's directions in its order (the secretary's last), each with its to-dos in the tree's order: those this
    /// device does not hold, or `keep` leaves out (a search), are dropped, and a direction left with none.
    static func build(_ tree: DirectionTree, todos: [String: TodoItem], keep: (TodoItem) -> Bool = { _ in true }) -> [Section] {
        tree.withTasks.compactMap { d in
            let rows = d.tasks.compactMap { todos[$0.id] }.filter(keep)
            return rows.isEmpty ? nil : Section(direction: d, todos: rows)
        }
    }
}
