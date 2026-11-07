import SwiftUI

// Claude Code's ultracode on plan cards and to-dos (#12): the plan task's chip, Edit's switch row and the mark after a
// task's title. The rules are Shared/Ultracode.swift.

@MainActor
extension Ultracode {
    /// The hub has it: the card shows each task's and Edit its switch.
    static var offered: Bool { AppModel.shared.hubFeatures.contains(feature) }

    /// Why `machine` can't start an agent with it now, nil where it can or where the hub has no ultracode.
    static func blocked(machine: String) -> String? {
        let model = AppModel.shared
        return startBlocked(claudeVersion: model.machine(model.hubRef(machine))?.claudeVersion, hubHasIt: offered)
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

/// The workflow mark in the AI color after a task's title while it runs with ultracode.
struct UltracodeMark: View {
    var font: Font = Theme.Font.labelStrong

    var body: some View {
        Image(systemName: Theme.Icon.workflow)
            .font(font)
            .foregroundStyle(Theme.ai)
            .help(String(localized: "ultracode is on: every task runs as a multi-agent workflow, which uses a lot of quota"))
            .accessibilityLabel(Text("ultracode on"))
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

/// Edit's switch for a task about to start, under its permissions. `blocked`: why its computer can't start with it,
/// grayed and said instead of the line.
struct UltracodeStartRow: View {
    @Binding var on: Bool
    var blocked: String?

    var body: some View {
        Toggle(isOn: Binding(get: { on && blocked == nil }, set: { on = $0 })) {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Label {
                    Text(verbatim: Ultracode.name).lineLimit(1)
                } icon: {
                    Image(systemName: Theme.Icon.workflow).foregroundStyle(Theme.ai)
                }
                Text(blocked ?? String(localized: "Runs every task as a multi-agent workflow · uses a lot of quota · this session only"))
                    .font(Theme.Font.caption)
                    .foregroundStyle(blocked == nil ? Color.secondary : Theme.cautionText)
            }
        }
        .disabled(blocked != nil)
        .accessibilityIdentifier("plan.edit.ultracode")
    }
}
