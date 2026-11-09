import SwiftUI

// Claude Code's ultracode on plan cards and to-dos (#12): the plan task's chip and Edit's switch row; the mark after a
// task's title is the Agent list row's (`UltracodeMark`, Views/Chat/UltracodeViews.swift). The rules are
// Shared/PlanUltracode.swift on top of Shared/Ultracode.swift.

@MainActor
extension Ultracode {
    /// The hub has it: the card shows each task's and Edit its switch.
    static var planOffered: Bool { AppModel.shared.hubFeatures.contains(feature) }

    /// Why `machine` can't start an agent with it now, nil where it can or where the hub has no ultracode.
    static func blocked(machine: String) -> String? {
        guard planOffered else { return nil }
        let model = AppModel.shared
        return startBlocked(claudeVersion: model.machine(model.hubRef(machine))?.claudeVersion)
    }

    /// The computers of `tasks` that can't start with it now: what Start sends checks as it goes.
    static func blockedMachines(_ tasks: [PlanTask]) -> Set<String> {
        Set(tasks.map(\.machine).filter { blocked(machine: $0) != nil })
    }
}

extension TasksStore {
    /// The Agent list row's ultracode mark on a task: its agents', else its plan task's before one started.
    func ultracode(_ todo: TodoItem) -> Bool {
        let planned = todo.planID.flatMap { plans[$0] }?.tasks.first { $0.todoID == todo.id }
        return Ultracode.todoShown(todo, lookup: TodosLogic.makeSessionLookup(sessions(todo)), planned: planned)
    }
}

/// A plan task's ultracode beside its computer, folder, model and permission chips: a toggle in the AI color while on,
/// quiet while off. `blocked`: why its computer can't start with it, outlined and said in its help.
struct UltracodeChip: View {
    var on: Bool
    var blocked: String?
    var change: (Bool) -> Void

    var body: some View {
        let shown = on && blocked == nil
        Button {
            withAnimation(Theme.Motion.state) { change(!shown) }
        } label: {
            Chip(text: Ultracode.name, symbol: Theme.Icon.workflow,
                 look: blocked != nil ? .outline : shown ? .tint(Theme.aiTint, text: Theme.ai) : .neutral)
        }
        .buttonStyle(.plain)
        .disabled(blocked != nil)
        .help(blocked ?? (shown
            ? String(localized: "Starts with ultracode: every task runs as a multi-agent workflow, which uses a lot of quota")
            : String(localized: "Starts without ultracode. With it, every task runs as a multi-agent workflow, which uses a lot of quota")))
        .accessibilityAddTraits(shown ? .isSelected : [])
        .accessibilityIdentifier("plan.ultracode")
    }
}

/// Edit's switch for a task about to start, under its permissions: New Agent's row (`UltracodeStartRow`) without the
/// quota. `blocked`: why its computer can't start with it, grayed and said instead of the line.
struct PlanUltracodeRow: View {
    @Binding var on: Bool
    var blocked: String?

    var body: some View {
        Toggle(isOn: Binding(get: { on && blocked == nil }, set: { on = $0 })) {
            // The mark beside the name and the line under it: a Label here stacks the mark above the name on iPhone.
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
                ThemedIcon(Theme.Icon.workflow).foregroundStyle(Theme.ai)
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(verbatim: Ultracode.name).lineLimit(1)
                    Text(blocked ?? Ultracode.subtitle)
                        .font(Theme.Font.label)
                        .foregroundStyle(blocked == nil ? Color.secondary : Theme.cautionText)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
        }
        .disabled(blocked != nil)
        .accessibilityIdentifier("plan.edit.ultracode")
    }
}
