import Foundation
import PersonaWire
#if os(iOS)
import SwiftUI
#endif

/// Upcoming (#10465; docs/design/proposals/todos-redesign.md 3.3): the open to-dos with a due date under day headings,
/// the overdue ones first, then each day that has one, soonest first. Inside a day, by the due moment, then as the hub
/// orders them. Pure: the list's model lays it out when the rows or the clock change (P4).
nonisolated enum TodoUpcomingLayout {
    /// The overdue group's id; each day's is "day:" and its date ("day:2026-11-20").
    static let overdue = "upcoming:overdue"
    private static let dayPrefix = "day:"

    static func layout(_ rows: [TodoFiled], items: [String: TodoItem], now: Date, calendar: Calendar,
                       words: TodoWords = TodoWords()) -> TodosListLayout.Output {
        var out = TodosListLayout.Output()
        var late: [(row: TodoFiled, by: Date)] = []
        var byDay: [Date: [(row: TodoFiled, by: Date)]] = [:]
        for row in rows {
            guard let item = items[row.id], let by = TodosLogic.dueBy(item, calendar: calendar),
                  let day = TodosPlaces.dueDay(item, calendar) else { continue }
            out.sections[TodosListLayout.key(row)] = row.section
            if by < now { late.append((row, by)) } else { byDay[day, default: []].append((row, by)) }
        }
        // By the due moment; rows due at the same one keep the hub's order (the rows come in the list's order).
        func keys(_ list: [(row: TodoFiled, by: Date)]) -> [String] {
            list.enumerated().sorted { a, b in a.element.by != b.element.by ? a.element.by < b.element.by : a.offset < b.offset }
                .map { TodosListLayout.key($0.element.row) }
        }
        if !late.isEmpty {
            out.groups.append(TodosListGroup(id: overdue, title: String(localized: "Overdue [To-dos Due]"), count: late.count,
                                             keys: keys(late)))
        }
        for day in byDay.keys.sorted() {
            let list = byDay[day] ?? []
            out.groups.append(TodosListGroup(id: dayPrefix + TodosLogic.dayKey(day, calendar: calendar),
                                             title: title(day, now: now, calendar: calendar, words: words), count: list.count,
                                             keys: keys(list)))
        }
        return out
    }

    /// A day's heading: "Today", "Tomorrow", then "Sat, Nov 21", with the year when it is not this one.
    static func title(_ day: Date, now: Date, calendar: Calendar, words: TodoWords = TodoWords()) -> String {
        if calendar.isDate(day, inSameDayAs: now) { return words("Today [To-dos Due]") }
        if let tomorrow = calendar.date(byAdding: .day, value: 1, to: now), calendar.isDate(day, inSameDayAs: tomorrow) {
            return words("Tomorrow [To-dos Due]")
        }
        var style = Date.FormatStyle(calendar: calendar, timeZone: calendar.timeZone).locale(words.formatting)
            .weekday(.abbreviated).month(.abbreviated).day()
        if calendar.component(.year, from: day) != calendar.component(.year, from: now) { style = style.year() }
        return day.formatted(style)
    }

    /// The day a heading stands for, for its +; nil for Overdue.
    static func day(_ group: String, calendar: Calendar) -> Date? {
        guard group.hasPrefix(dayPrefix) else { return nil }
        let parts = group.dropFirst(dayPrefix.count).split(separator: "-").compactMap { Int($0) }
        guard parts.count == 3 else { return nil }
        return calendar.date(from: DateComponents(year: parts[0], month: parts[1], day: parts[2]))
    }

    /// The colour a to-do takes on Upcoming's calendar (⋯ › Show as Calendar), from its view's phase: what the hub filed
    /// it as, as the list draws it, instead of what the app works out from its sessions. A row without a view takes
    /// `fallback`.
    static func calendarState(_ view: TodoSummary?, fallback: TaskState) -> TaskState {
        guard let view else { return fallback }
        if view.reason == TodoReason.failedStart { return .failed }
        switch view.phase {
        case TodoPhase.needsYou: return .needsYou
        case TodoPhase.working: return .working
        case TodoPhase.waiting: return .waiting
        case TodoPhase.ready: return .unread
        case TodoPhase.done: return .done
        default: return .notStarted
        }
    }
}

#if os(iOS)
/// A day's heading on Upcoming: its name and how many, and + to add a to-do due that day (proposal 3.3, "a day heading's
/// + fills it"). Overdue has no +. Spoken as a heading, the + as its own button.
struct TodoUpcomingHeader: View {
    var group: TodosListGroup
    var add: (Date) -> Void

    var body: some View {
        let title = group.title ?? ""
        HStack(spacing: Theme.Space.s) {
            SectionHeader(title: title, count: group.count, tone: StatusPalette.idle)
                .font(Theme.Font.sectionHeader)
                .textCase(nil)
                .lineLimit(1)
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(title)
                .accessibilityValue(String(group.count))
                .accessibilityAddTraits(.isHeader)
                .accessibilityIdentifier("todos.section." + group.id)
            Spacer(minLength: Theme.Space.s)
            if let day = TodoUpcomingLayout.day(group.id, calendar: TodoCalendar.userCalendar) {
                Button {
                    Haptics.tap()
                    add(day)
                } label: {
                    ThemedIcon(Theme.Icon.add)
                        .font(Theme.Font.labelStrong)
                        .foregroundStyle(Theme.accent)
                        .tapTargetFrame()
                        .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .accessibilityLabel(String(localized: "New To-Do on \(title) [To-dos Places]"))
                .accessibilityIdentifier("todos.upcoming.add." + group.id)
            }
        }
    }
}

/// What a place says with nothing in it (proposal 3.3, each place's own empty state): Today points to what comes this
/// week; Upcoming says what shows up there; a project offers a to-do made in it.
struct TodoPlaceEmptyState: View {
    var place: TodoPlaceRef
    var title: String
    /// Today's "3 coming up this week".
    var comingUp: Int
    var show: (TodoPlaceRef) -> Void
    var capture: () -> Void

    var body: some View {
        switch place.id {
        case TodoPlaceKind.today:
            EmptyStateView(symbol: Theme.Icon.today, title: String(localized: "Nothing for today. [To-dos Places]"),
                           message: String(localized: "To-dos due today or overdue show up here. [To-dos Places]"),
                           action: comingUp > 0
                               ? (String(localized: "\(comingUp) coming up this week [To-dos Places]"), { show(.upcoming) }) : nil)
        case TodoPlaceKind.upcoming:
            EmptyStateView(symbol: Theme.Icon.calendarView, title: String(localized: "Nothing scheduled. [To-dos Places]"),
                           message: String(localized: "To-dos with a date show up here. [To-dos Places]"))
        default:
            EmptyStateView(symbol: Theme.Icon.space, title: String(localized: "Nothing open in \(title). [To-dos Places]"),
                           message: String(localized: "To-dos you put in this project show up here. [To-dos Places]"),
                           action: (String(localized: "New To-Do in \(title) [To-dos Places]"), capture))
        }
    }
}

/// "2 more need you, not in Today · Show All" (plan S2-4; DESIGN_SYSTEM 3.4 "Sessions that need the user"): in every
/// place but All To-dos, what needs the owner elsewhere stays one tap away, as the old list's filter said it.
struct TodoPlaceNotice: View {
    var count: Int
    var place: String
    var showAll: () -> Void

    var body: some View {
        NoticeBanner(text: String(localized: "\(count) more need you, not in \(place) [To-dos Places]"), tone: .caution,
                     symbol: Theme.Icon.needsYou) {
            Button(String(localized: "Show All [To-dos Places]")) {
                Haptics.tap()
                showAll()
            }
            .lineLimit(1)
            .accessibilityIdentifier("todos.places.showAll")
        }
        .accessibilityIdentifier("todos.places.hiddenNeeds")
    }
}

/// A new to-do in a project's place (its empty state's New To-Do in ‹project›): a title, made in the project. Closes once
/// saved; a failed save keeps what was typed.
struct TodoPlaceNewSheet: View {
    @Environment(\.dismiss) private var dismiss
    var project: String
    var projectName: String
    @State private var title = ""
    @State private var saving = false
    @FocusState private var typing: Bool

    private var trimmed: String { title.trimmingCharacters(in: .whitespacesAndNewlines) }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField(String(localized: "Title"), text: $title)
                        .lineLimit(1)
                        .focused($typing)
                        .onSubmit(add)
                        .accessibilityIdentifier("todos.places.newTitle")
                } footer: {
                    Text("In \(projectName) [To-dos Places]")
                }
            }
            .themedPage()
            .navigationTitle(String(localized: "New To-Do"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                SheetClose { dismiss() }
                SheetConfirm(id: "todos.places.newAdd", name: String(localized: "Add [Beautiful U I Bridge 7a8a11]"),
                             disabled: trimmed.isEmpty || saving, action: add)
            }
        }
        .formSheetDetents([.medium])
        .onAppear { typing = true }
    }

    private func add() {
        guard !trimmed.isEmpty, !saving else { return }
        saving = true
        Task {
            do {
                let store = TodoStore.shared
                let made = try await store.create(title: trimmed)
                // The create route takes no project: the to-do is put in it right after.
                try await store.updatePlan(id: made.id, APIClient.TodoPlanChange(project: project))
                dismiss()
            } catch {
                saving = false
                AppModel.shared.show(error)
            }
        }
    }
}

/// Upcoming as a calendar (⋯ › Show as Calendar; proposal 3.3): today's to-dos calendar (`TodoCalendarView`, Day, Week,
/// Month, drag to reschedule) under its List | Calendar bar, each to-do coloured by the hub's view rather than by its
/// sessions.
struct TodoUpcomingCalendar: View {
    @Binding var asCalendar: Bool
    var open: (String) -> Void
    @StoredValue("todos.calendar.mode") private var mode = TodoCalendarMode.week
    @State private var anchor = Date.now
    private let store = TodoStore.shared

    var body: some View {
        VStack(spacing: Theme.Space.flush) {
            TodosModeBar(view: Binding(get: { asCalendar ? .calendar : .list }, set: { asCalendar = $0 == .calendar }), mode: $mode)
            TodoCalendarView(todos: store.scopedTodos, mode: $mode, anchor: $anchor, open: open, showList: { asCalendar = false },
                             state: { item in
                                 TodoUpcomingLayout.calendarState(TodoStore.shared.views[item.id], fallback: item.isDone ? .done : .notStarted)
                             })
        }
    }
}
#endif
