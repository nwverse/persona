import Foundation
import Observation
import PersonaWire

/// What the To-dos list needs from the app (docs/apple/ios.md "Logic-layer choice"): the store whose rows, views and
/// numbers it draws, the hub, the clock, toasts and error reporting, the projects' names, and how a re-filing that
/// waited lands. `live` is the app's (`TodoListView.swift`); tests pass a store of their own and a `FakeHub`'s client.
struct TodosListDeps {
    var store: TodoStore
    var api: @MainActor () -> APIClient?
    var features: @MainActor () -> TodoFeatures
    var now: @MainActor () -> Date
    var calendar: @MainActor () -> Calendar
    var toast: @MainActor (Toast) -> Void
    var report: @MainActor (Error) -> Void
    var projects: @MainActor () -> [TodoProject]
    /// Runs a re-filing that waited for the list to rest: in the view's fade (`Theme.Motion.conceal`), at once with Reduce
    /// Motion.
    var land: @MainActor (() -> Void) -> Void = { $0() }
}

/// The V1 To-dos list (#10465; docs/design/proposals/todos-redesign.md 3.3, 3.4; DESIGN_SYSTEM 3.4 "To-dos list"): the
/// current assistant's rows as the hub files them (`TodoStore.countsSource.filed`, the rows its numbers are counted
/// from), drawn in sections for the bar's choice, the grouping and the sort.
///
/// - Rows never move under a finger: a new filing waits while the list is scrolled or out of sight (`hold`, the page
///   reports what it does) and then lands at once, faded; a row that comes to need the owner lands at once. What a row
///   says (its line, its time) changes in its own cell at once, and the numbers are the store's, never held.
/// - The structure holds keys; each row reads its own cell (`Row`), so a run's new line draws its row alone (P5).
@MainActor @Observable
final class TodosListModel {
    typealias Deps = TodosListDeps

    /// One row as the list draws it.
    @MainActor @Observable
    final class Row: Identifiable {
        /// Its key in the list: the manager and the id (`TodosListLayout.key`).
        let id: String
        let todoID: String
        fileprivate(set) var content: TodoRowContent
        /// What the content was worked out from: worked out again only when one of these or the clock changes.
        @ObservationIgnored fileprivate var inputs: Inputs

        fileprivate struct Inputs: Equatable {
            var item: TodoItem
            var view: TodoSummary?
            var filed: TodoFiled
            var grouping: TodosListGrouping
            var projects: [TodoProject]
        }

        fileprivate init(id: String, inputs: Inputs, content: TodoRowContent) {
            self.id = id
            todoID = inputs.filed.id
            self.inputs = inputs
            self.content = content
        }
    }

    /// The bar's choice: All each time the list is made, never stored, so a remembered choice can't hide what needs the
    /// owner (proposal 3.3).
    var choice: TodosChoice = .all {
        didSet { if choice != oldValue { relayout() } }
    }
    var grouping: TodosListGrouping = .phase {
        didSet { if grouping != oldValue { relayout(contents: true) } }
    }
    var sort: TodosListSort = .suggested {
        didSet { if sort != oldValue { relayout() } }
    }
    /// The folds the owner opened (Can wait, Someday, Done today): this list's, not kept.
    private(set) var unfolded: Set<String> = []
    private(set) var layout = TodosListLayout.Output.empty
    /// Whether the list can draw: the views are known (kept from the last run, or the first read ended). Until then it
    /// draws nothing rather than filing its rows twice (`TodoStore.viewsRead`).
    private(set) var ready = false

    /// Scrolled or out of sight: the page says which, the hold says when a new filing lands. Made at first use: the
    /// page makes this model in a `@State` initial value, which must stay cheap (P3).
    @ObservationIgnored private(set) lazy var hold = Hold("To-dos list")
    @ObservationIgnored private let deps: Deps
    @ObservationIgnored private var cells: [String: Row] = [:]
    @ObservationIgnored private var snapshot = Snapshot.empty
    @ObservationIgnored private var watching = false
    @ObservationIgnored private var following = false
    /// The time the rows' times are said from: set when the list starts and each minute after.
    @ObservationIgnored private var now = Date.distantPast

    /// What the list reads of the store and the app.
    private struct Snapshot: Equatable {
        var filed: [TodoFiled] = []
        var items: [String: TodoItem] = [:]
        var views: [String: TodoSummary] = [:]
        var ready = false
        var projects: [TodoProject] = []
        static let empty = Snapshot()
    }

    init(deps: Deps) {
        self.deps = deps
    }

    var groups: [TodosListGroup] { layout.groups }
    var calm: Bool { layout.calm }
    /// The bar's choices with the store's counts: never held, so they change in the same update as every other place
    /// that shows them.
    var entries: [TodosBar.Entry] {
        TodosBar.entries(deps.store.countsSource.counts, doneRows: layout.doneRows, chosen: choice)
    }
    /// N, as the drawer, the bar and the Needs you header say it.
    var needsYou: Int { deps.store.countsSource.needsYou }
    /// Nothing to list under the current choice.
    var isEmpty: Bool { layout.groups.allSatisfy { $0.keys.isEmpty && $0.folds.isEmpty } }
    /// The list has rows, under any choice: the bar is drawn (unless calm), so an empty choice can be left.
    var hasRows: Bool { !layout.sections.isEmpty }

    func row(_ key: String) -> Row? { cells[key] }

    /// Follows the store from now on; the first layout lands at once. The store's minute clock for rows filed by the
    /// app's own rules starts with the list's first draw.
    func start() {
        guard !following else { return }
        following = true
        now = deps.now()
        hold.land = { [weak self] in self?.landWaiting() }
        pull(animated: false)
        deps.store.listShown()
    }

    func toggle(_ fold: String) {
        if unfolded.contains(fold) { unfolded.remove(fold) } else { unfolded.insert(fold) }
        relayout()
    }

    func setFold(_ fold: String, open: Bool) {
        guard open != unfolded.contains(fold) else { return }
        toggle(fold)
    }

    /// A minute passed: the rows' times are said again, and rows that moved by the clock (Done today) are filed again.
    func tick() {
        now = deps.now()
        updateCells(force: true)
        changed(clock: true)
    }

    /// Pull to refresh.
    func refresh() async {
        await deps.store.refresh(api: deps.api())
    }

    // MARK: Following the store

    private func current() -> Snapshot {
        guard following else { return snapshot }
        guard !watching else { return read() }
        watching = true
        return withObservationTracking { read() } onChange: { [weak self] in
            // Told before the change: read it after.
            Task { @MainActor [weak self] in
                self?.watching = false
                self?.changed()
            }
        }
    }

    private func read() -> Snapshot {
        let store = deps.store
        return Snapshot(filed: store.countsSource.filed, items: store.byID, views: store.views, ready: store.viewsRead,
                        projects: deps.projects())
    }

    /// The store changed (or, `clock`, a minute passed): rows say what is new at once; a new filing lands now, or when
    /// the list rests.
    private func changed(clock: Bool = false) {
        let next = current()
        guard next != snapshot || clock else { return }
        if next != snapshot {
            snapshot = next
            if ready != next.ready { ready = next.ready }
            updateCells(force: false)
        }
        let laid = makeLayout()
        guard laid != layout else {
            hold.landed()
            return
        }
        if !hold.wait() || bringsANeed(laid) { apply(laid, animated: true) }
    }

    /// What waited lands.
    private func landWaiting() {
        let laid = makeLayout()
        if laid != layout { apply(laid, animated: true) } else { hold.landed() }
    }

    /// The first read of the list.
    private func pull(animated: Bool) {
        snapshot = current()
        ready = snapshot.ready
        updateCells(force: true)
        apply(makeLayout(), animated: animated)
    }

    /// A choice, the grouping, the sort or a fold changed: the owner's own tap, never held.
    private func relayout(contents: Bool = false) {
        if contents { updateCells(force: true) }
        let laid = makeLayout()
        if laid != layout { layout = laid }
    }

    private func apply(_ laid: TodosListLayout.Output, animated: Bool) {
        hold.landed()
        guard laid != layout else { return }
        if animated && !layout.groups.isEmpty { deps.land { self.layout = laid } } else { layout = laid }
    }

    /// A row that comes to need the owner is never held (DESIGN_SYSTEM 3.4: what needs the user is given to its row at
    /// once).
    private func bringsANeed(_ laid: TodosListLayout.Output) -> Bool {
        laid.sections.contains { key, section in
            section == TodoListSection.needsYou && layout.sections[key] != TodoListSection.needsYou
        }
    }

    private func makeLayout() -> TodosListLayout.Output {
        let projects = snapshot.projects
        return TodosListLayout.layout(.init(
            rows: snapshot.filed, items: snapshot.items, choice: choice, grouping: grouping, sort: sort, unfolded: unfolded,
            now: now, calendar: deps.calendar(),
            projectName: { TodoProjectLogic.name($0, in: projects) }))
    }

    /// Each row's content, worked out again only where its facts, its view, its filing or the clock (`force`) changed.
    private func updateCells(force: Bool) {
        let calendar = deps.calendar()
        let projects = snapshot.projects
        var seen = Set<String>()
        for filed in snapshot.filed {
            guard let item = snapshot.items[filed.id] else { continue }
            let key = TodosListLayout.key(filed)
            seen.insert(key)
            let inputs = Row.Inputs(item: item, view: snapshot.views[filed.id], filed: filed, grouping: grouping, projects: projects)
            if let cell = cells[key], !force, cell.inputs == inputs { continue }
            let content = TodoRowContentLogic.content(filed, item: item, view: inputs.view, grouping: grouping, now: now,
                                                      calendar: calendar,
                                                      projectName: { TodoProjectLogic.name($0, in: projects) })
            if let cell = cells[key] {
                cell.inputs = inputs
                if cell.content != content { cell.content = content }
            } else {
                cells[key] = Row(id: key, inputs: inputs, content: content)
            }
        }
        if cells.count > seen.count { cells = cells.filter { seen.contains($0.key) } }
    }
}
