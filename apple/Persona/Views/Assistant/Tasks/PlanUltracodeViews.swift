import SwiftUI

// Workflow controls for tasks before they start. The manager identifies which workflow the launcher takes.

@MainActor
extension Ultracode {
    static func planWorkflow(_ task: PlanTask) -> AgentWorkflowKind? {
        let features = task.workflowKind == nil ? AppModel.shared.hubFeatures : []
        return planWorkflow(task, features: features)
    }

    /// The version check applies to the Claude Code workflow. Codex support comes from the manager's projection.
    static func blocked(_ task: PlanTask) -> String? {
        guard planWorkflow(task) == .claudeUltracode else { return nil }
        let model = AppModel.shared
        return startBlocked(claudeVersion: model.machine(model.hubRef(task.machine))?.claudeVersion)
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
    var kind: AgentWorkflowKind
    var on: Bool
    var blocked: String?
    var change: (Bool) -> Void

    var body: some View {
        let shown = on && blocked == nil
        Button {
            withAnimation(Theme.Motion.state) { change(!shown) }
        } label: {
            Chip(text: kind.name, symbol: Theme.Icon.workflow,
                 look: blocked != nil ? .outline : shown ? .tint(Theme.aiTint, text: Theme.ai) : .neutral)
        }
        .buttonStyle(.plain)
        .disabled(blocked != nil)
        .help(blocked ?? help(on: shown))
        .accessibilityAddTraits(shown ? .isSelected : [])
        .accessibilityIdentifier("plan.ultracode")
    }

    private func help(on: Bool) -> String {
        if kind == .codexMultiAgentV2 { return kind.summary }
        return on
            ? String(localized: "Starts with ultracode: every task runs as a multi-agent workflow, which uses a lot of quota")
            : String(localized: "Starts without ultracode. With it, every task runs as a multi-agent workflow, which uses a lot of quota")
    }
}

/// Edit's switch for a task about to start, under its permissions: New Agent's row (`UltracodeStartRow`) without the
/// quota. `blocked`: why its computer can't start with it, grayed and said instead of the line.
struct PlanUltracodeRow: View {
    var kind: AgentWorkflowKind
    @Binding var on: Bool
    var blocked: String?

    var body: some View {
        Toggle(isOn: Binding(get: { on && blocked == nil }, set: { on = $0 })) {
            // The mark beside the name and the line under it: a Label here stacks the mark above the name on iPhone.
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
                ThemedIcon(Theme.Icon.workflow).foregroundStyle(Theme.ai)
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(verbatim: kind.name)
                    Text(blocked ?? kind.summary)
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

private extension AgentWorkflowKind {
    var summary: String {
        if self == .codexMultiAgentV2 {
            return String(localized: "Lets Codex delegate parts of the task to other agents.")
        }
        return Ultracode.subtitle
    }
}
