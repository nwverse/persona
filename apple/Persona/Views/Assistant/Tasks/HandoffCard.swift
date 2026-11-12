import SwiftUI

/// One dispatch, one card per task (docs/design/task-cards.md §3.2; the web's `HandoffCard`): an arrow, the task's title with
/// how its Agent came to it (New, Already Active, Resumed), when the Agent last moved and its state, and an arrow out. A plan
/// names only its own tasks, each independently openable. Titles come from the live to-do when loaded; a task whose Agent
/// did not start says why in red, and one with none yet says it is starting. The web draws one card per Agent; here a
/// task is one card, its lead Agent.
struct HandoffCards: View {
    var tasks: [DispatchedTasks.Entry]

    var body: some View {
        if !tasks.isEmpty {
            VStack(alignment: .leading, spacing: Theme.Space.s) {
                ForEach(tasks) { HandoffCard(entry: $0) }
            }
        }
    }
}

struct HandoffCard: View {
    var entry: DispatchedTasks.Entry
    private var store: TasksStore { .shared }

    /// "3 min. ago": minutes are enough for when an Agent last moved.
    private static let relative: RelativeDateTimeFormatter = {
        let f = RelativeDateTimeFormatter()
        f.unitsStyle = .short
        return f
    }()

    var body: some View {
        let todo = entry.todoID.flatMap { store.todos[$0] }
        let lead = todo.flatMap { store.leadSession($0) }
        let failure = failure(todo)
        let state: TaskState? = lead == nil ? nil : todo.map { store.state($0) }
        let card = HStack(spacing: Theme.Space.m) {
            ThemedIcon(Theme.Icon.followOn).font(Theme.Font.bodyStrong)
                .scaledFrame(width: Theme.Size.well, height: Theme.Size.well)
                .background(Theme.Fill.well, in: .circle)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                HStack(spacing: Theme.Space.s) {
                    Text(entry.title).font(Theme.Font.bodyStrong).lineLimit(1)
                    badge
                }
                meta(todo: todo, lead: lead, state: state, failure: failure)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            if entry.todoID != nil {
                ThemedIcon(Theme.Icon.open).font(Theme.Font.labelStrong).foregroundStyle(.secondary)
                    .accessibilityHidden(true)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .card(padding: Theme.Space.m)
        if let id = entry.todoID {
            Button { open(id) } label: { card.contentShape(.rect) }
                .buttonStyle(.plain)
                .accessibilityElement(children: .combine)
                .accessibilityAddTraits(.isButton)
                .accessibilityIdentifier("dispatch.task.\(id)")
        } else {
            card
                .accessibilityElement(children: .combine)
                .accessibilityIdentifier("dispatch.unstarted")
        }
    }

    /// How the Agent came to the task: filled for New and Resumed, outlined for one that was running already.
    @ViewBuilder private var badge: some View {
        let active = entry.wake == "active"
        Text(active ? String(localized: "Already Active") : entry.wake == "resumed" ? String(localized: "Resumed") : String(localized: "New"))
            .font(Theme.Font.labelHeavy)
            .textCase(.uppercase)
            .lineLimit(1)
            .fixedSize()
            .foregroundStyle(active ? AnyShapeStyle(.primary) : AnyShapeStyle(Theme.pageBackground))
            .padding(.horizontal, Theme.Space.s)
            .padding(.vertical, Theme.Space.xxs)
            .background { if !active { Capsule().fill(.primary) } }
            .overlay { if active { Capsule().strokeBorder(.primary, lineWidth: Theme.Size.hairline) } }
            .accessibilityIdentifier("dispatch.badge")
    }

    @ViewBuilder private func meta(todo: TodoItem?, lead: Session?, state: TaskState?, failure: String?) -> some View {
        if let lead, let state {
            HStack(spacing: Theme.Space.xs) {
                if let at = lead.activityAt ?? lead.updatedAt, at.isMeaningful {
                    ThemedIcon(Theme.Icon.recent).font(Theme.Font.label).foregroundStyle(.secondary)
                    VisibleTimelineView(interval: 60) { now in
                        Text(Self.relative.localizedString(for: min(at, now), relativeTo: now)).lineLimit(1)
                    }
                } else {
                    Text("No activity yet").lineLimit(1).foregroundStyle(.tertiary)
                }
                Text(verbatim: "·").foregroundStyle(.tertiary)
                TaskMark(state: state).scaledFrame(width: Theme.Size.markM)
                Text(state.label).lineLimit(1).foregroundStyle(state == .needsYou ? AnyShapeStyle(state.labelColor) : AnyShapeStyle(.secondary))
            }
            .font(Theme.Font.caption)
            .foregroundStyle(.secondary)
        } else if let failure {
            Text("Didn't start: \(failure) [Tasks Store 38bbb7]").font(Theme.Font.caption).foregroundStyle(Theme.danger).lineLimit(2)
                .accessibilityIdentifier("dispatch.failure")
        } else if todo?.doneAt != nil || (todo == nil && entry.todoID != nil && !store.todos.isEmpty) {
            Text(todo == nil ? String(localized: "This task no longer exists") : String(localized: "Closed before an Agent started"))
                .font(Theme.Font.caption).foregroundStyle(.tertiary).lineLimit(1)
        } else {
            Text("Starting the agent").font(Theme.Font.caption).foregroundStyle(.tertiary).lineLimit(1)
        }
    }

    /// Why the task's Agent did not start: the hub's word on the to-do, its plan's, the Agent's own row, or the plan task's.
    private func failure(_ todo: TodoItem?) -> String? {
        let why = todo.flatMap { store.startFailure($0) ?? $0.agents.first(where: { !($0.startError ?? "").isEmpty })?.startError } ?? entry.error
        let trimmed = why?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return trimmed.isEmpty ? nil : trimmed
    }

    private func open(_ id: String) {
        Haptics.tap()
        let agent = store.todos[id].flatMap { store.leadSession($0)?.key }
        #if os(iOS)
        store.openDispatchedTask(id, agent: agent, pushAgent: true)
        #else
        store.openDispatchedTask(id, agent: agent, pushAgent: false)
        #endif
    }
}
