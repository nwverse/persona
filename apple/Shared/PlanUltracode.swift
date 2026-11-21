import Foundation

// A task's workflow can be set before Start. The wire request keeps its tri-state ultracode field for both engines.

nonisolated extension Ultracode {
    static func planWorkflow(_ task: PlanTask, features: Set<String>) -> AgentWorkflowKind? {
        switch task.workflowKind {
        case .unsupported?: nil
        case let kind?: kind
        case nil: features.contains(feature) ? .claudeUltracode : nil
        }
    }

    /// The mark on a to-do: one of its agents still in it shows the Agent list row's (`rowMark`); before any agent
    /// started, the plan task it starts from asks for it. Never on a to-do that is done or did not start.
    static func todoShown(_ todo: TodoItem, lookup: TodosLogic.SessionLookup, planned: PlanTask? = nil) -> Bool {
        if todo.doneAt != nil || todo.failedToStart { return false }
        // The plan's choice only until an agent starts: one moved to another to-do leaves nothing running here.
        if todo.agents.isEmpty { return planned?.ultracode == true }
        return todo.activeAgents.contains { a in lookup(a.machineID, a.sessionID).map(rowMark) ?? false }
    }

    // MARK: What Start sends

    /// Start sends only a changed workflow choice. A known unsupported launcher has no workflow control.
    static func toSend(planned: PlanTask?, on: Bool, blocked: Bool) -> Bool? {
        if planned?.workflowKind == .unsupported { return nil }
        let sent = on && !blocked
        return sent == (planned?.ultracode == true) ? nil : sent
    }

    /// Each task's choice on the card: the card's own where it has one, else the assistant's.
    static func choices(_ plan: TaskPlan, card: [Int: Bool]) -> [Bool] {
        plan.tasks.indices.map { card[$0] ?? (plan.tasks[$0].ultracode == true) }
    }

    /// Where Edit starts: the plan's tasks, each with its choice on the card.
    static func editsOf(_ plan: TaskPlan, choices: [Bool]) -> [PlanTask] {
        plan.tasks.enumerated().map { i, t in
            var e = t
            e.ultracode = t.workflowKind == .unsupported ? nil : (i < choices.count ? choices[i] : t.ultracode == true)
            return e
        }
    }

    /// What the card's own Start sends: the plan's tasks as they are, `ultracode` set only on those whose choice, as
    /// sent, differs from the assistant's; nil when nothing differs, so a plain Start still sends no `tasks`.
    /// `blocked(task)`: its computer cannot start the requested workflow.
    static func cardEdits(_ plan: TaskPlan, choices: [Bool], blocked: (PlanTask) -> Bool) -> [PlanTask]? {
        var changed = false
        let edits = plan.tasks.enumerated().map { i, t -> PlanTask in
            var e = t
            e.ultracode = toSend(planned: t, on: i < choices.count && choices[i], blocked: blocked(t))
            if e.ultracode != nil { changed = true }
            return e
        }
        return changed ? edits : nil
    }

    /// What Edit's Start sends: the edited tasks, `ultracode` left out where it is the assistant's own.
    static func editsToSend(_ plan: TaskPlan, edits: [PlanTask], blocked: (PlanTask) -> Bool) -> [PlanTask] {
        edits.enumerated().map { i, e in
            var out = e
            let planned = i < plan.tasks.count ? plan.tasks[i] : nil
            if e.workflowKind == .unsupported {
                out.ultracode = nil
            } else {
                out.ultracode = toSend(planned: planned, on: e.ultracode == true, blocked: blocked(e))
            }
            return out
        }
    }
}
