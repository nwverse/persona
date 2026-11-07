import Foundation

/// Claude Code's ultracode on plan cards and to-dos (#12; the switch itself is docs/protocol/protocol.md "Claude Code
/// ultracode", #3427): the assistant turns it on per task, the card can change it before the task starts, and a to-do
/// carries the Agent list row's mark while one of its agents runs with it. The web's rules (web/src/lib/ultracode.ts,
/// web/src/lib/feed.ts), the same here; the views are in Persona/Views/Assistant/Tasks/UltracodeViews.swift.
nonisolated enum Ultracode {
    /// The hub takes it (hello `features`): an older hub shows and sends nothing about it.
    static let feature = "ultracode"
    /// Its name everywhere: a proper name, never translated (docs/protocol/i18n.md).
    static let name = "ultracode"
    /// The first Claude Code with the independent switch; the node refuses older ones.
    static let minVersion = [2, 1, 284]

    /// A known Claude Code version older than `minVersion` (the node's own rule): an unknown one may be tried.
    static func versionTooOld(_ version: String?) -> Bool {
        let v = (version ?? "").trimmingCharacters(in: .whitespaces)
        let core = v.hasPrefix("v") ? v.dropFirst() : Substring(v)
        let head = core.split(whereSeparator: { $0 == "-" || $0 == "+" }).first ?? ""
        let parts = head.split(separator: ".", omittingEmptySubsequences: false)
        guard parts.count == 3, !core.hasPrefix("-"), !core.hasPrefix("+") else { return false }
        let nums = parts.compactMap { p in p.allSatisfy(\.isASCII) ? Int(p) : nil }
        guard nums.count == 3 else { return false }
        for (n, m) in zip(nums, minVersion) where n != m { return n < m }
        return false
    }

    /// Why an agent can't start with it on a computer whose Claude Code is `claudeVersion`, nil when it can or when the
    /// hub has no ultracode (then nothing about it is shown or sent).
    static func startBlocked(claudeVersion: String?, hubHasIt: Bool) -> String? {
        guard hubHasIt, versionTooOld(claudeVersion), let version = claudeVersion?.trimmingCharacters(in: .whitespaces) else { return nil }
        let min = minVersion.map(String.init).joined(separator: ".")
        return String(localized: "Claude Code on this computer is \(version); ultracode needs \(min) or later")
    }

    /// The mark on an Agent list row, and so on a to-do: the node saw it on (unknown is not on), on a Claude Code agent
    /// that is not offline (the node keeps the last value it saw after the process is gone; a resume starts it off).
    static func markShown(_ s: Session) -> Bool {
        s.ultracode == true && !s.assistant && s.isClaude && s.state != .offline
    }

    /// The mark on a to-do: one of its agents still in it shows it; before any agent started, the plan task it starts
    /// from asks for it. Never on a to-do that is done or did not start.
    static func todoShown(_ todo: TodoItem, lookup: TodosLogic.SessionLookup, planned: PlanTask? = nil) -> Bool {
        if todo.doneAt != nil || todo.failedToStart { return false }
        // The plan's choice only until an agent starts: one moved to another to-do leaves nothing running here.
        if todo.agents.isEmpty { return planned?.ultracode == true }
        return todo.activeAgents.contains { a in lookup(a.machineID, a.sessionID).map(markShown) ?? false }
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
