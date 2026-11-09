import Foundation

// Claude Code's ultracode on plan cards and to-dos (#12): the assistant turns it on per task, the card can change it
// before the task starts, and a to-do carries the Agent list row's mark while one of its agents runs with it. The web's
// rules (web/src/lib/ultracode.ts, web/src/lib/feed.ts), on top of the switch's own (`Ultracode`, Ultracode.swift); the
// views are in Persona/Views/Assistant/Tasks/PlanUltracodeViews.swift.

nonisolated extension Ultracode {
    /// The mark on a to-do: one of its agents still in it shows the Agent list row's (`rowMark`); before any agent
    /// started, the plan task it starts from asks for it. Never on a to-do that is done or did not start.
    static func todoShown(_ todo: TodoItem, lookup: TodosLogic.SessionLookup, planned: PlanTask? = nil) -> Bool {
        if todo.doneAt != nil || todo.failedToStart { return false }
        // The plan's choice only until an agent starts: one moved to another to-do leaves nothing running here.
        if todo.agents.isEmpty { return planned?.ultracode == true }
        return todo.activeAgents.contains { a in lookup(a.machineID, a.sessionID).map(rowMark) ?? false }
    }

    // MARK: What Start sends

    /// A task's ultracode as Start sends it: the choice, off on a computer that can't; nil when that is the assistant's
    /// own (left out, the hub keeps it).
    static func toSend(planned: PlanTask?, on: Bool, blocked: Bool) -> Bool? {
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
            e.ultracode = i < choices.count ? choices[i] : (t.ultracode == true)
            return e
        }
    }

    /// What the card's own Start sends: the plan's tasks as they are, `ultracode` set only on those whose choice, as
    /// sent, differs from the assistant's; nil when nothing differs, so a plain Start still sends no `tasks`.
    /// `blocked(machine)`: that computer can't start with it.
    static func cardEdits(_ plan: TaskPlan, choices: [Bool], blocked: (String) -> Bool) -> [PlanTask]? {
        var changed = false
        let edits = plan.tasks.enumerated().map { i, t -> PlanTask in
            var e = t
            e.ultracode = toSend(planned: t, on: i < choices.count && choices[i], blocked: blocked(t.machine))
            if e.ultracode != nil { changed = true }
            return e
        }
        return changed ? edits : nil
    }

    /// What Edit's Start sends: the edited tasks, `ultracode` left out where it is the assistant's own.
    static func editsToSend(_ plan: TaskPlan, edits: [PlanTask], blocked: (String) -> Bool) -> [PlanTask] {
        edits.enumerated().map { i, e in
            var out = e
            let planned = i < plan.tasks.count ? plan.tasks[i] : nil
            out.ultracode = toSend(planned: planned, on: e.ultracode == true, blocked: blocked(e.machine))
            return out
        }
    }
}
