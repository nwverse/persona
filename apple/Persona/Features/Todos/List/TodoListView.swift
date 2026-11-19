#if os(iOS)
import PersonaWire
import SwiftUI

extension TodosListDeps {
    /// The app's: the shared store, the hub, the knowledge base's projects, toasts and errors on the app.
    @MainActor static var live: TodosListDeps {
        let app = AppModel.shared
        return TodosListDeps(store: TodoStore.shared, api: { app.api }, features: { app.todoFeatures }, now: { Date() },
                             calendar: { TodoCalendar.userCalendar }, toast: { app.toast = $0 }, report: { app.show($0) },
                             projects: { TodoProjects.shared.list },
                             land: { body in withAnimation(Theme.Motion.reduced ? nil : Theme.Motion.conceal) { body() } })
    }
}

/// The V1 To-dos list (#10465; docs/design/proposals/todos-redesign.md 3.3, 3.4; DESIGN_SYSTEM 3.4 "To-dos list"): the
/// section bar (`TodosBar`, on the Inbox's `CapsuleFilterBar`, the grouping at its end), the sections in the hub's order
/// with Can wait and Someday folded, and the rows (`TodoListRow`). The calm rule hides the bar and the headers while only
/// plain To do rows exist. Rows open today's to-do page until the to-do's own page arrives; ticking, swiping and New To-Do
/// arrive in the next step. iOS only: visionOS keeps today's To-dos.
struct TodoListView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.shownTree) private var shownTree
    @Environment(\.builtTree) private var builtTree
    @State private var model = TodosListModel(deps: .live)
    @StoredValue("todos.v1.grouping") private var grouping = TodosListGrouping.phase
    @StoredValue("todos.v1.sort") private var sort = TodosListSort.suggested
    /// The rows in sight, for where the list is left (not observed: scrolling redraws nothing).
    @State private var sight = TodoListSight()
    private let workspace = TodoWorkspace.shared

    /// Whether this page's tree is the one the window shows: a tree on its way out leaves the workspace alone.
    private var shownHere: Bool { builtTree == nil || shownTree?.tree === builtTree }

    var body: some View {
        ScrollViewReader { proxy in
            List {
                if model.ready { content }
            }
            .listStyle(.plain)
            .accessibilityIdentifier("todos.v1") // the list itself: on the page, the bar's grouping menu took it too
            .themedPage()
            .contentMargins(.top, Theme.Space.xs, for: .scrollContent)
            .safeAreaBarWhenSupported(edge: .top, spacing: Theme.Space.flush) {
                if model.ready && !model.calm && model.hasRows { sectionBar }
            }
            .refreshable { await model.refresh() }
            .onScrollPhaseChange { _, phase in model.hold.set(.scrolling, phase != .idle) }
            .onAppear {
                model.grouping = grouping
                model.sort = sort
                model.start()
                model.hold.set(.outOfSight, false)
                if let key = workspace.scrolled(in: workspace.place) { proxy.scrollTo(key, anchor: .top) }
            }
            .onDisappear {
                model.hold.set(.outOfSight, true)
                workspace.leave(workspace.place, at: sight.top(in: model.groups), shown: shownHere)
            }
        }
        .detailTitle(String(localized: "All To-dos [To-dos List]"))
        .navigationBarTitleDisplayMode(.inline)
        .pageToolbar {
            ToolbarItem(placement: .primaryAction) { moreMenu }
        }
        .onChange(of: grouping) { _, g in model.grouping = g }
        .onChange(of: sort) { _, s in model.sort = s }
        // Each minute while the page is in front: the rows' times, and what the clock moves.
        .tabActiveTask {
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(60))
                if !Task.isCancelled { model.tick() }
            }
        }
        // The projects' names, read when the page opens and for the assistant made current.
        .task(id: app.assistantSwitches) { await TodoProjects.shared.load(app.api?.currentAssistantSettings()) }
        .task { await model.refresh() }
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
                    if let title = group.title { header(group, title: title) }
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
            .onDisappear { sight.visible.remove(key) }
            .id(key)
        }
    }

    /// A section's title with its count: Needs you's is N in the needs-you tone, as the bar and the drawer say it; the
    /// others count the rows under them (DESIGN_SYSTEM 3.4, the Agents list's state headers).
    private func header(_ group: TodosListGroup, title: String) -> some View {
        let count = group.needs ? model.needsYou : group.count
        return SectionHeader(title: title, count: count, tone: group.needs ? StatusPalette.needsYou : StatusPalette.idle)
            .font(Theme.Font.sectionHeader)
            .textCase(nil)
            .lineLimit(1)
            .accessibilityElement(children: .combine)
            .accessibilityAddTraits(.isHeader)
            .accessibilityValue(String(count))
            .accessibilityIdentifier("todos.section." + group.id)
    }

    /// "Can wait · 1 ›": what is folded at a section's foot, opened in place.
    private func foldLine(_ fold: TodosListGroup.Fold) -> some View {
        let detail = String(localized: "\(fold.keys.count) to-dos [To-dos List]")
        return FoldButton(title: fold.title, folded: !fold.open, detail: detail) {
            Haptics.tap()
            withAnimation(Theme.Motion.reduced ? nil : Theme.Motion.state) { model.toggle(fold.id) }
        } label: {
            Text(verbatim: fold.title + TodoTimeWords.separator + String(fold.keys.count))
                .font(Theme.Font.secondary)
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
        .themedRow()
        .accessibilityIdentifier("todos.fold." + fold.id)
    }

    @ViewBuilder private var emptyState: some View {
        if let offline = app.assistantManagerOffline, !model.hasRows {
            AssistantOfflineNotice(manager: offline)
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

    // MARK: Bar and menus

    /// All · Needs you · In progress · To do · Done with their counts, the grouping at its end (the Inbox's bar,
    /// DESIGN_SYSTEM 3.4 "List controls"). A choice swaps the list at once.
    private var sectionBar: some View {
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
            Picker(String(localized: "Group By [To-dos Grouping]"), selection: Binding(get: { grouping }, set: { g in
                Haptics.tap()
                grouping = g
            })) {
                ForEach(TodosListGrouping.allCases) { Text($0.title).tag($0) }
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

    /// The list's ⋯: the order inside each section, and Someday and Done today shown or folded.
    private var moreMenu: some View {
        Menu {
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
        } label: {
            Label(String(localized: "More [To-dos List]"), systemImage: Theme.Icon.more)
                .labelStyle(.iconOnly)
        }
        .accessibilityIdentifier("todos.more")
    }

    private func foldBinding(_ fold: String) -> Binding<Bool> {
        Binding(get: { model.unfolded.contains(fold) }, set: { open in model.setFold(fold, open: open) })
    }
}

/// The rows a list has in sight, as they appear and go: where the list was left, by its top row in sight.
@MainActor final class TodoListSight {
    var visible: Set<String> = []

    func top(in groups: [TodosListGroup]) -> String? {
        for group in groups {
            for key in group.keys + group.folds.filter(\.open).flatMap(\.keys) where visible.contains(key) { return key }
        }
        return nil
    }
}
#endif
