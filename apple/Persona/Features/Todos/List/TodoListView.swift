#if os(iOS)
import PersonaWire
import SwiftUI

extension TodosListDeps {
    /// The app's: the shared store, the hub and the knowledge base's projects.
    @MainActor static var live: TodosListDeps {
        let app = AppModel.shared
        return TodosListDeps(store: TodoStore.shared, api: { app.api }, now: { Date() }, calendar: { TodoCalendar.userCalendar },
                             projects: { TodoProjects.shared.list })
    }
}

/// The V1 To-dos list (#10465; docs/design/proposals/todos-redesign.md 3.3, 3.4; DESIGN_SYSTEM 3.4 "To-dos list"): the
/// section bar (`TodosBar`, on the Inbox's `CapsuleFilterBar`, the grouping at its end), the sections in the hub's order
/// with Can wait and Someday folded, and the rows (`TodoListRow`). The calm rule hides the bar and the headers while only
/// plain To do rows exist. It shows one place (`TodosPlacesModel`, DESIGN_SYSTEM 3.5 "To-dos places"), picked in the
/// sidebar or from its title's menu: Upcoming by day, with its calendar a ⋯ away; every place but All To-dos says what
/// needs the owner elsewhere. Rows open today's to-do page until the to-do's own page arrives; ticking, swiping and New
/// To-Do arrive in another step. iOS only: visionOS keeps today's To-dos.
struct TodoListView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.shownTree) private var shownTree
    @Environment(\.builtTree) private var builtTree
    @State private var model = TodosListModel(deps: .live)
    @StoredValue("todos.v1.grouping") private var grouping = TodosListGrouping.phase
    @StoredValue("todos.v1.sort") private var sort = TodosListSort.suggested
    /// The rows in sight, for where the list rests (not observed: scrolling redraws nothing).
    @State private var sight = TodoListSight()
    /// The place this page last put its list back for (where it was left, or its top): once per place, as the page is
    /// made or another place is picked. A list kept in its navigation stack keeps its own place when a to-do pushed over
    /// it pops.
    @State private var placedIn: String?
    private let workspace = TodoWorkspace.shared
    /// The places: the title's menu, and the place the list shows (`model.place`) and its name, which the page takes
    /// from them only when they change (`TodoPlaceFollower`), so a place's number changing redraws nothing here (P5).
    @State private var places = TodosPlacesModel(deps: .live)
    @State private var placeTitle: String?
    /// Upcoming drawn as a calendar (⋯ › Show as Calendar), kept on the device.
    @StoredValue("todos.v1.upcomingCalendar") private var upcomingCalendar = false
    @State private var allProjects = false
    /// A new to-do from a day heading's + on Upcoming, due that day.
    @State private var addingDay: TodoCalendarSlot?
    /// A new to-do from a project's empty state, made in it.
    @State private var newInProject: TodoPlaceRef?
    /// A to-do opened from Upcoming's calendar.
    @State private var pushedTodo: TodoDetailRoute?

    /// Whether this page's tree is the one the window shows: a tree on its way out leaves the workspace alone.
    private var shownHere: Bool { builtTree == nil || shownTree?.tree === builtTree }

    /// iOS 26 pins the bar over the list, which scrolls under it behind the scroll edge's effect. iOS 18 has no such
    /// effect to draw behind a pinned bar: the bar is the list's first row there, as the Inbox's (DESIGN_SYSTEM 3.4,
    /// "To-dos list").
    private static var pinsBar: Bool {
        if #available(iOS 26.0, *) { true } else { false }
    }

    private var drawsBar: Bool { model.ready && !model.calm && model.hasRows && model.place != .upcoming }

    var body: some View {
        Group {
            if model.place == .upcoming && upcomingCalendar {
                TodoUpcomingCalendar(asCalendar: $upcomingCalendar) { pushedTodo = TodoDetailRoute(id: $0) }
            } else {
                list
            }
        }
        .background {
            TodoPlaceFollower(places: places) { place, title in
                if model.place != place { model.place = place }
                if placeTitle != title { placeTitle = title }
            }
            // On screen or not, for the sidebar's To-dos row (`TodoWorkspace.pageUp`).
            .onAppear { workspace.page(up: true) }
            .onDisappear { workspace.page(up: false) }
        }
        .detailTitle(title)
        .toolbarTitleMenu { TodoTitleMenu(places: places) { allProjects = true } }
        .navigationBarTitleDisplayMode(.inline)
        .pageToolbar {
            ToolbarItem(placement: .primaryAction) { moreMenu }
        }
        .onChange(of: grouping) { _, g in model.grouping = g }
        .onChange(of: sort) { _, s in model.sort = s }
        // Each minute while the page is in front, and as it comes back to the front: the rows' times, and what the clock
        // moves (Done today, the due days).
        .tabActiveTask {
            while !Task.isCancelled {
                model.tick()
                try? await Task.sleep(for: .seconds(60))
            }
        }
        // The projects' names, read when the page opens and for the assistant made current.
        .task(id: app.assistantSwitches) { await TodoProjects.shared.load(app.api?.currentAssistantSettings()) }
        .task { await model.refresh() }
        .navigationDestination(item: $pushedTodo) { r in TodoDetailView(todoID: r.id) }
        .sheet(item: $addingDay) { slot in TodoQuickAddSheet(slot: slot) }
        .sheet(item: $newInProject) { place in TodoPlaceNewSheet(project: place.project ?? "", projectName: places.title(place)) }
        .sheet(isPresented: $allProjects) {
            TodoAllProjectsSheet(places: places) { place in
                allProjects = false
                places.show(place)
            }
        }
    }

    private var list: some View {
        ScrollViewReader { proxy in
            List {
                if model.ready {
                    if drawsBar && !Self.pinsBar {
                        TodosSectionBar(model: model, grouping: $grouping)
                            .listRowInsets(EdgeInsets())
                            .listRowSeparator(.hidden)
                            .themedRow()
                            .id(Self.barRow)
                    }
                    if model.hiddenNeeds > 0 {
                        TodoPlaceNotice(count: model.hiddenNeeds, place: title) { places.show(.all) }
                            .listRowInsets(EdgeInsets(top: Theme.Space.xs, leading: Theme.Space.page, bottom: Theme.Space.xs,
                                                      trailing: Theme.Space.page))
                            .listRowSeparator(.hidden)
                            .listRowBackground(Color.clear)
                            .id(Self.noticeRow)
                    }
                    content
                }
            }
            .listStyle(.plain)
            .accessibilityIdentifier("todos.v1") // the list itself: on the page, the bar's grouping menu took it too
            .themedPage()
            .contentMargins(.top, Theme.Space.xs, for: .scrollContent)
            .safeAreaBarWhenSupported(edge: .top, spacing: Theme.Space.flush) {
                if drawsBar && Self.pinsBar { TodosSectionBar(model: model, grouping: $grouping) }
            }
            .refreshable { await model.refresh() }
            .onScrollGeometryChange(for: CGFloat.self) { $0.contentInsets.top } action: { _, top in sight.covered = top }
            .onScrollPhaseChange { old, phase in
                model.hold.set(.scrolling, phase != .idle)
                if phase != .idle { model.settleArrivals() } // nothing fades in under a finger
                // Where a scroll came to rest; not the idle a page is made in, before its rows are drawn.
                if phase == .idle, old != .idle, let top = sight.top(in: model.groups) {
                    workspace.rested(model.place.id, at: top, shown: shownHere)
                }
            }
            .onAppear {
                model.grouping = grouping
                model.sort = sort
                model.place = places.current
                model.start()
                model.hold.set(.outOfSight, false)
            }
            .onDisappear { model.hold.set(.outOfSight, true) }
            // Back where the place's list last rested, once its rows are laid out: only for a page made anew (another
            // page opened in between) or another place picked, never when a to-do pushed over it pops. A place picked
            // with nothing kept starts at its top.
            .task(id: PlacedKey(ready: model.ready, place: model.place.id)) {
                let place = model.place.id
                guard model.ready, placedIn != place else { return }
                let switched = placedIn != nil
                guard let key = workspace.scrolled(in: place), model.row(key) != nil else {
                    placedIn = place
                    if switched, let top = topRow { proxy.scrollTo(top, anchor: .top) }
                    return
                }
                // Once the list has drawn its first rows under its bars (a second at most): until then it has nothing to
                // scroll to, and a bar laid out after moves it again. Once more if a late layout moved it all the same;
                // a page that went meanwhile tries again as it comes back.
                for _ in 0..<20 where (sight.visible.isEmpty || sight.covered == 0) && !Task.isCancelled {
                    try? await Task.sleep(for: .milliseconds(50))
                }
                guard !Task.isCancelled else { return }
                proxy.scrollTo(key, anchor: .top)
                try? await Task.sleep(for: .milliseconds(150))
                guard !Task.isCancelled else { return }
                if sight.top(in: model.groups) != key { proxy.scrollTo(key, anchor: .top) }
                placedIn = place
            }
        }
    }

    /// The place's name: the page's title.
    private var title: String { placeTitle ?? String(localized: "All To-dos [To-dos List]") }

    private static let barRow = "todos.bar.row"
    private static let noticeRow = "todos.places.notice"

    /// The list's first row: the bar where it is a row (iOS 18), the place's notice, or the first to-do.
    private var topRow: String? {
        if drawsBar && !Self.pinsBar { return Self.barRow }
        if model.hiddenNeeds > 0 { return Self.noticeRow }
        return model.groups.first.flatMap { $0.keys.first }
    }

    private struct PlacedKey: Equatable {
        var ready: Bool
        var place: String
    }

    // MARK: The list

    @ViewBuilder private var content: some View {
        if model.isEmpty {
            emptyState
                .listRowBackground(Color.clear)
                .listRowSeparator(.hidden)
        } else {
            ForEach(model.groups) { group in
                Section {
                    ForEach(group.keys, id: \.self) { key in row(key) }
                    ForEach(group.folds) { fold in
                        foldLine(fold)
                        if fold.open { ForEach(fold.keys, id: \.self) { key in row(key) } }
                    }
                } header: {
                    if model.place == .upcoming {
                        TodoUpcomingHeader(group: group) { addingDay = TodoCalendarSlot(day: $0, time: nil) }
                    } else if let title = group.title {
                        TodosSectionHeader(model: model, group: group, title: title)
                    }
                }
                .listSectionSeparator(.hidden, edges: .top) // no line over a section's first row, the calm list's included
            }
        }
    }

    @ViewBuilder private func row(_ key: String) -> some View {
        if let cell = model.row(key) {
            // The row is the to-do: the link under it, so no chevron on every row (as the Inbox's rows; iOS 18 draws the
            // chevron of a link that is the row whatever it is told).
            TodoListRow(row: cell)
                .background { NavigationLink(value: TodoDetailRoute(id: cell.todoID)) { EmptyView() }.opacity(0) }
                .themedRow()
            .onAppear { sight.visible.insert(key) }
            .onDisappear {
                sight.visible.remove(key)
                sight.middles[key] = nil
            }
            // Where its middle is in the list's frame, kept unobserved: whether the bars cover it.
            .onGeometryChange(for: CGFloat.self) { $0.frame(in: .scrollView).midY } action: { sight.middles[key] = $0 }
            .id(key)
        }
    }

    /// "Can wait · 1 ›": what is folded at a section's foot, opened in place. A row of the list, the whole row its button
    /// (at least a finger's height, as every row), its words in the titles' column (a tick's room before them, unseen);
    /// spoken as `FoldButton` speaks a fold.
    private func foldLine(_ fold: TodosListGroup.Fold) -> some View {
        let detail = String(localized: "\(fold.keys.count) to-dos [To-dos List]")
        let state = fold.open ? String(localized: "Expanded") : String(localized: "Collapsed [Readiness Card ee6046]")
        return Button {
            Haptics.tap()
            withAnimation(Theme.Motion.reduced ? nil : Theme.Motion.state) { model.toggle(fold.id) }
        } label: {
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.m) {
                TodoTickGlyph(closed: false).hidden()
                Text(verbatim: fold.title + TodoTimeWords.separator + String(fold.keys.count))
                    .font(Theme.Font.secondary)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                Spacer(minLength: Theme.Space.sm)
                FoldChevron(folded: !fold.open)
            }
        }
        .themedRow()
        .accessibilityElement(children: .ignore)
        .accessibilityLabel([fold.title, state, detail]
            .joined(separator: String(localized: ", ", comment: "Separator between parts of an accessibility label")))
        .accessibilityAddTraits(.isButton)
        .accessibilityIdentifier("todos.fold." + fold.id)
    }

    @ViewBuilder private var emptyState: some View {
        if let offline = app.assistantManagerOffline, !model.hasRows {
            AssistantOfflineNotice(manager: offline)
        } else if !model.place.isAll && !model.hasRows {
            TodoPlaceEmptyState(place: model.place, title: title, comingUp: model.comingUp,
                                show: { places.show($0) }, capture: { newInProject = model.place })
        } else {
            switch model.choice {
            case .all:
                EmptyStateView(symbol: Theme.Icon.todo, title: String(localized: "Nothing to do yet. [To-dos List]"),
                               message: String(localized: "Write down anything you want done. Persona can help with the rest. [To-dos List]"))
            case .needsYou:
                EmptyStateView(symbol: Theme.Icon.todo, title: String(localized: "Nothing needs you [To-dos List]"),
                               message: String(localized: "When a to-do needs your answer, it shows up here. [To-dos List]"))
            case .inProgress:
                EmptyStateView(symbol: Theme.Icon.todo, title: String(localized: "Nothing in progress [To-dos List]"),
                               message: String(localized: "To-dos Persona or an Agent works on show up here. [To-dos List]"))
            case .todo:
                EmptyStateView(symbol: Theme.Icon.todo, title: String(localized: "Nothing to do [To-dos List]"),
                               message: String(localized: "Your own to-dos that nobody works on show up here. [To-dos List]"))
            case .done:
                EmptyStateView(symbol: Theme.Icon.todo, title: String(localized: "Nothing done yet [To-dos List]"),
                               message: String(localized: "Closed to-dos show up here by the day they closed. [To-dos List]"))
            }
        }
    }

    // MARK: Menus

    /// The list's ⋯: the order inside each section, and Someday and Done today shown or folded; on Upcoming, Show as
    /// Calendar.
    private var moreMenu: some View {
        Menu {
            if model.place == .upcoming {
                Toggle(isOn: $upcomingCalendar) {
                    Label(String(localized: "Show as Calendar [To-dos Places]"), systemImage: Theme.Icon.calendarView)
                }
                .accessibilityIdentifier("todos.places.showCalendar")
            } else {
                listMenu
            }
        } label: {
            Label(String(localized: "More [To-dos List]"), systemImage: Theme.Icon.more)
                .labelStyle(.iconOnly)
        }
        .accessibilityIdentifier("todos.more")
    }

    @ViewBuilder private var listMenu: some View {
        Picker(selection: $sort) {
            ForEach(TodosListSort.allCases) { Text($0.title).tag($0) }
        } label: {
            Text("Sort By [To-dos List]")
            Text(sort.title)
        }
        .pickerStyle(.menu)
        Section {
            Toggle(isOn: foldBinding(TodoListSection.someday)) {
                Label(String(localized: "Show Someday [To-dos List]"), systemImage: Theme.Icon.time)
            }
            Toggle(isOn: foldBinding(TodoListSection.done)) {
                Label(String(localized: "Show Done Today [To-dos List]"), systemImage: Theme.Icon.success)
            }
        }
    }

    private func foldBinding(_ fold: String) -> Binding<Bool> {
        Binding(get: { model.unfolded.contains(fold) }, set: { open in model.setFold(fold, open: open) })
    }
}

/// All · Needs You · In Progress · To Do · Done with their counts, the grouping at its end (the Inbox's bar,
/// DESIGN_SYSTEM 3.4 "List controls"). A choice swaps the list at once. A view of its own, so a count that changes
/// redraws the bar alone (P5).
private struct TodosSectionBar: View {
    var model: TodosListModel
    @Binding var grouping: TodosListGrouping

    var body: some View {
        CapsuleFilterBar(items: model.entries.map { e in
            CapsuleFilterItemData(id: e.id, title: e.choice.title, count: e.count, countLook: e.look.map(Self.badgeLook))
        }, selection: model.choice.rawValue, choose: { id in
            guard let choice = TodosChoice(rawValue: id), choice != model.choice else { return }
            Haptics.tap()
            model.choice = choice
        }, scrollID: "todos.bar", itemIDPrefix: "todos.bar.", revealsSelection: true, showsTrailing: model.choice != .done) {
            groupingMenu
        }
        .padding(.horizontal, Theme.Space.page)
        .padding(.vertical, Theme.Space.xs)
    }

    private static func badgeLook(_ look: TodosBar.Look) -> CountBadge.Look {
        switch look {
        case .needs: .needs
        case .neutral: .neutral
        }
    }

    /// Group By: Phase · Project · Due Date, kept on this device; Needs you stays a section at the top in each.
    private var groupingMenu: some View {
        Menu {
            // Under its heading, as the frame draws it (an inline picker's own title is not drawn in a menu).
            Section(String(localized: "Group By [To-dos Grouping]")) {
                ForEach(TodosListGrouping.allCases) { g in
                    Toggle(g.title, isOn: Binding(get: { grouping == g }, set: { on in
                        guard on, g != grouping else { return }
                        Haptics.tap()
                        grouping = g
                    }))
                }
            }
        } label: {
            ThemedIcon(Theme.Icon.capsuleGrouping)
                .font(Theme.Font.secondaryMedium)
                .foregroundStyle(Color.primary)
                .tapTargetFrame()
                .contentShape(.rect)
        }
        .labelOnlyMenu()
        .buttonStyle(.plain)
        .accessibilityIdentifier("todos.grouping")
        .accessibilityLabel(String(localized: "Group By: \(grouping.title) [To-dos Grouping]"))
    }
}

/// A section's title with its count: Needs you's is N in the needs-you tone, as the bar and the drawer say it; the
/// others count the rows under them (DESIGN_SYSTEM 3.4, the Agents list's state headers). A view of its own, so N
/// changing redraws the header, not the list (P5). Spoken once: the title, then the count as its value.
private struct TodosSectionHeader: View {
    var model: TodosListModel
    var group: TodosListGroup
    var title: String

    var body: some View {
        let count = group.needs ? model.needsYou : group.count
        SectionHeader(title: title, count: count, tone: group.needs ? StatusPalette.needsYou : StatusPalette.idle)
            .font(Theme.Font.sectionHeader)
            .textCase(nil)
            .lineLimit(1)
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(title)
            .accessibilityValue(String(count))
            .accessibilityAddTraits(.isHeader)
            .accessibilityIdentifier("todos.section." + group.id)
    }
}

/// The rows a list has drawn, and where they are: where the list rests, by its top row in sight. Not observed: scrolling
/// redraws nothing.
@MainActor final class TodoListSight {
    /// The rows drawn, the ones under the bars included.
    var visible: Set<String> = []
    /// Each drawn row's middle in the list's frame.
    var middles: [String: CGFloat] = [:]
    /// How much of the list's top the bars cover (the navigation bar, the pinned section bar).
    var covered: CGFloat = 0

    /// The first row whose middle is clear of the bars.
    func top(in groups: [TodosListGroup]) -> String? {
        for group in groups {
            for key in group.keys + group.folds.filter(\.open).flatMap(\.keys) where visible.contains(key) {
                if (middles[key] ?? covered) >= covered { return key }
            }
        }
        return nil
    }
}
#endif
