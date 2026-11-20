import Foundation
import Observation
import PersonaWire

/// A To-dos place (#10465; docs/design/proposals/todos-redesign.md 3.3, "Places have identity"): All To-dos, Today,
/// Upcoming or a knowledge-base project, by the hub's place id (`TodoPlace.id`: all, today, upcoming, project:<slug>).
nonisolated struct TodoPlaceRef: Hashable, Identifiable, Sendable {
    var id: String

    static let all = TodoPlaceRef(id: TodoPlaceKind.allValue)
    static let today = TodoPlaceRef(id: TodoPlaceKind.today)
    static let upcoming = TodoPlaceRef(id: TodoPlaceKind.upcoming)
    static func project(_ slug: String) -> TodoPlaceRef { TodoPlaceRef(id: projectPrefix + slug) }
    private static let projectPrefix = TodoPlaceKind.project + ":"

    /// The project's slug, for a project's place.
    var project: String? { id.hasPrefix(Self.projectPrefix) ? String(id.dropFirst(Self.projectPrefix.count)) : nil }
    var isAll: Bool { id == TodoPlaceKind.allValue }
}

/// What a new to-do made in a place starts with (proposal 3.3 "Capture default"): Today's is due today, a project's
/// is in it, Upcoming asks for a date; All To-dos sets nothing.
nonisolated struct TodoPlaceCapture: Equatable, Sendable {
    /// Due on this day.
    var due: Date?
    var project: String?
    /// A date is asked for (Upcoming): the sheet offers tomorrow.
    var asksDate = false
}

/// The places' rules (#10465; proposal 3.3; DESIGN_SYSTEM 3.5 "To-dos places"), pure: which rows a place holds, which
/// places the sidebar and the title menu list, and what a place says when it is empty.
nonisolated enum TodosPlaces {
    /// The projects the sidebar and the title menu list before All Projects….
    static let shownProjects = 5

    /// The places the sidebar's To-dos row folds open and the title menu lists, from the hub's (`countsSource.places`).
    struct Entries: Equatable, Sendable {
        /// Today and Upcoming, as the hub lists them.
        var fixed: [TodoPlace] = []
        /// Up to five projects, in the hub's order.
        var projects: [TodoPlace] = []
        /// All Projects… is offered: there are projects to go to, beyond the ones listed or with nothing open.
        var more = false
        /// No places from this hub (it does not list `todo_places`, or answered 404 or 405): All To-dos only.
        var isEmpty: Bool { fixed.isEmpty && projects.isEmpty && !more }
    }

    /// The hub's places as the sidebar lists them: Today and Upcoming, then up to five projects. Those with a part of N
    /// are listed first when there are more than five, so a project that needs the owner is never behind All Projects…;
    /// the ones listed keep the hub's order, so rows only come and go and never trade places. `knowsProjects`: the
    /// knowledge base has projects, which All Projects… lists too.
    static func entries(_ places: [TodoPlace], knowsProjects: Bool = false) -> Entries {
        let fixed = places.filter { $0.kind == TodoPlaceKind.today || $0.kind == TodoPlaceKind.upcoming }
        // A hub that lists no places sends All alone (`TodoCountsSource`): nothing to fold open.
        guard !fixed.isEmpty else { return Entries() }
        let projects = places.filter { $0.kind == TodoPlaceKind.project && !($0.project ?? "").isEmpty }
        var chosen = Set(projects.filter { $0.needsYou > 0 }.prefix(shownProjects).map(\.id))
        for p in projects where chosen.count < shownProjects { chosen.insert(p.id) }
        let shown = projects.filter { chosen.contains($0.id) }
        return Entries(fixed: fixed, projects: shown, more: !projects.isEmpty || knowsProjects)
    }

    /// The place drawn: `place` while the hub lists it, or, for a project, while the hub or the knowledge base knows it;
    /// else All To-dos. A hub that lists no places (an older one, or a hello that stopped listing them) and another
    /// assistant made current (its projects are others) never leave the list narrowed with no way back.
    static func effective(_ place: TodoPlaceRef, places: [TodoPlace], projects: [TodoProject] = []) -> TodoPlaceRef {
        guard !place.isAll else { return place }
        guard !entries(places).isEmpty else { return .all }
        if let slug = place.project {
            return places.contains(where: { $0.id == place.id }) || projects.contains(where: { $0.slug == slug }) ? place : .all
        }
        return places.contains(where: { $0.id == place.id }) ? place : .all
    }

    /// The place's name: Today and Upcoming in the app's words, a project by the hub's title, else the knowledge base's
    /// name, else its slug.
    static func title(_ place: TodoPlaceRef, places: [TodoPlace], projects: [TodoProject]) -> String {
        switch place.id {
        case TodoPlaceKind.allValue: return String(localized: "All To-dos [To-dos List]")
        case TodoPlaceKind.today: return String(localized: "Today [To-dos Places]")
        case TodoPlaceKind.upcoming: return String(localized: "Upcoming [To-dos Places]")
        default:
            guard let slug = place.project else { return place.id }
            if let title = places.first(where: { $0.id == place.id })?.title, !title.isEmpty { return title }
            return TodoProjectLogic.name(slug, in: projects)
        }
    }

    // MARK: What a place holds

    /// Whether `row` belongs in `place` (proposal 3.3; plan S2-4: the places filter the one list on the device).
    /// - Today: open, and due today, overdue, or reminding today.
    /// - Upcoming: open with a due date, Someday left out.
    /// - A project: its rows, closed ones too (the bar's Done).
    static func holds(_ place: TodoPlaceRef, _ row: TodoFiled, item: TodoItem?, now: Date, calendar: Calendar) -> Bool {
        switch place.id {
        case TodoPlaceKind.allValue: return true
        case TodoPlaceKind.today:
            guard !row.closed, row.section != TodoListSection.someday, let item else { return false }
            if let by = TodosLogic.dueBy(item, calendar: calendar), by < now || calendar.isDate(dueDay(item, calendar) ?? by, inSameDayAs: now) {
                return true
            }
            return item.remindAt.map { calendar.isDate($0, inSameDayAs: now) } ?? false
        case TodoPlaceKind.upcoming:
            guard !row.closed, row.section != TodoListSection.someday, let item else { return false }
            return TodosLogic.dueBy(item, calendar: calendar) != nil
        default:
            guard let slug = place.project else { return false }
            return row.project == slug
        }
    }

    /// The day a to-do is due on: its due moment's day, or its due date.
    static func dueDay(_ item: TodoItem, _ calendar: Calendar) -> Date? {
        if let at = item.dueAt { return calendar.startOfDay(for: at) }
        return TodosLogic.dueDay(item, calendar: calendar)
    }

    /// The rows of `rows` that `place` holds, in the order given; the steps their parents carry (`carried`, worked out
    /// over the whole list) are left out, as the list leaves them out.
    static func rows(_ rows: [TodoFiled], in place: TodoPlaceRef, items: [String: TodoItem], now: Date, calendar: Calendar)
        -> [TodoFiled] {
        guard !place.isAll else { return rows }
        let carried = TodoCountsFallback.carried(rows)
        return rows.filter { !carried.contains($0.id) && holds(place, $0, item: items[$0.id], now: now, calendar: calendar) }
    }

    /// "3 coming up this week" (Today's empty state): open to-dos due in the seven days after today.
    static func comingUp(_ rows: [TodoFiled], items: [String: TodoItem], now: Date, calendar: Calendar) -> Int {
        let today = calendar.startOfDay(for: now)
        guard let first = calendar.date(byAdding: .day, value: 1, to: today),
              let end = calendar.date(byAdding: .day, value: 8, to: today) else { return 0 }
        let carried = TodoCountsFallback.carried(rows)
        return rows.count { row in
            guard !row.closed, !carried.contains(row.id), row.section != TodoListSection.someday,
                  let item = items[row.id], let day = dueDay(item, calendar) else { return false }
            return day >= first && day < end
        }
    }

    /// What a to-do made in `place` starts with.
    static func capture(_ place: TodoPlaceRef, now: Date, calendar: Calendar) -> TodoPlaceCapture {
        switch place.id {
        case TodoPlaceKind.today: TodoPlaceCapture(due: calendar.startOfDay(for: now))
        case TodoPlaceKind.upcoming: TodoPlaceCapture(due: calendar.date(byAdding: .day, value: 1, to: calendar.startOfDay(for: now)),
                                                      asksDate: true)
        default: TodoPlaceCapture(project: place.project)
        }
    }
}

/// What the sidebar's To-dos row folds open and the list's title menu lists (#10465; proposal 3.3): the places from the
/// store (`TodoCountsSource.places`: the hub's with one manager, the current assistant's projects with several, each
/// with its part of N), the place shown (`TodoWorkspace.place`) and its name. A pick in the sidebar opens To-dos on that
/// place at its root; a pick in the title menu changes the list in place.
@MainActor @Observable
final class TodosPlacesModel {
    struct Deps {
        var store: TodoStore
        var workspace: TodoWorkspace
        /// The knowledge base's projects (`TodoProjects`): All Projects… lists those with nothing open too.
        var projects: @MainActor () -> [TodoProject]
    }

    @ObservationIgnored private let deps: Deps

    init(deps: Deps) {
        self.deps = deps
    }

    /// The hub's places as the store counts them.
    var places: [TodoPlace] { deps.store.countsSource.places }
    var entries: TodosPlaces.Entries { TodosPlaces.entries(places, knowsProjects: !deps.projects().isEmpty) }
    /// The place shown: the owner's pick, or All To-dos once the hub lists no places.
    var current: TodoPlaceRef {
        TodosPlaces.effective(TodoPlaceRef(id: deps.workspace.place), places: places, projects: deps.projects())
    }

    func title(_ place: TodoPlaceRef) -> String { TodosPlaces.title(place, places: places, projects: deps.projects()) }

    /// Every project to go to (All Projects…): the hub's project places, then the knowledge base's projects with
    /// nothing open, by name.
    var allProjects: [TodoPlace] {
        let listed = places.filter { $0.kind == TodoPlaceKind.project && !($0.project ?? "").isEmpty }
        let slugs = Set(listed.compactMap(\.project))
        let quiet = TodoProjectLogic.choices(deps.projects(), current: nil).filter { !slugs.contains($0.slug) }
            .map { TodoPlace(id: TodoPlaceRef.project($0.slug).id, kind: TodoPlaceKind.project, title: $0.name, project: $0.slug) }
        return listed + quiet
    }

    /// The title menu's pick: the list shows `place` at once, where it is.
    func show(_ place: TodoPlaceRef) {
        deps.workspace.pick(place.id)
    }

    /// The sidebar's pick: To-dos opens on `place`, at its root (`TodoWorkspace.open`).
    func open(_ place: TodoPlaceRef) {
        deps.workspace.open(place.id)
    }

    /// The sidebar's To-dos row: All To-dos while To-dos is in front (`inFront`, or its page on screen under the
    /// drawer), its root by the tab-bar rule; otherwise To-dos as it was left, on its last place and with what it kept
    /// (proposal 3.3: places keep their stacks, option 2 on phones and option 3 at regular widths). Returns whether a
    /// place other than the one shown was picked; either way the sidebar then opens To-dos.
    @discardableResult
    func reopen(inFront: Bool) -> Bool {
        guard inFront || deps.workspace.pageUp, deps.workspace.place != TodoPlaceKind.allValue else { return false }
        open(.all)
        return true
    }
}

extension TodosPlacesModel.Deps {
    /// The app's: the shared store and workspace, and the knowledge base's projects as To-dos last read them.
    @MainActor static var live: Self {
        Self(store: TodoStore.shared, workspace: TodoWorkspace.shared, projects: { TodoProjects.shared.list })
    }
}
