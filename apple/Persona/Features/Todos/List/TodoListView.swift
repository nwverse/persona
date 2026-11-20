#if os(iOS)
import PersonaWire
import SwiftUI

extension TodosListDeps {
    /// The app's: the shared store and outbox, the hub, the knowledge base's projects and the app's toast.
    @MainActor static var live: TodosListDeps {
        let app = AppModel.shared
        return TodosListDeps(store: TodoStore.shared, outbox: TodoOutbox.shared, api: { app.api }, now: { Date() },
                             calendar: { TodoCalendar.userCalendar }, projects: { TodoProjects.shared.list },
                             toast: { app.toast = $0 }, show: { app.show($0) })
    }
}

/// The V1 To-dos list (#10465; docs/design/proposals/todos-redesign.md 3.3, 3.4; DESIGN_SYSTEM 3.4 "To-dos list"): the
/// section bar (`TodosBar`, on the Inbox's `CapsuleFilterBar`, the grouping at its end), the sections in the hub's order
/// with Can wait and Someday folded, and the rows (`TodoListRow`). The calm rule hides the bar and the headers while only
/// plain To do rows exist. A row ticks, swipes (Done, Someday) and has a long-press menu (`TodoRowActions`); New To-Do
/// floats at the foot; the writes wait out their Undo on the device (`TodoOutbox`). While the hub is out of reach the
/// list is the one last read, under one line that says so. Rows open today's to-do page until the to-do's own page
/// arrives. iOS only: visionOS keeps today's To-dos.
struct TodoListView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.shownTree) private var shownTree
    @Environment(\.builtTree) private var builtTree
    @State private var model = TodosListModel(deps: .live)
    @StoredValue("todos.v1.grouping") private var grouping = TodosListGrouping.phase
    @StoredValue("todos.v1.sort") private var sort = TodosListSort.suggested
    /// The rows in sight, for where the list rests (not observed: scrolling redraws nothing).
    @State private var sight = TodoListSight()
    /// Whether this page has put its list back where the place was left: once, as the page is made. A list kept in its
    /// navigation stack keeps its own place when a to-do pushed over it pops.
    @State private var placed = false
    /// The row whose tick asks first ("Mark it done and stop?"), the row Delete… asks about, Date…'s row, New To-Do.
    @State private var stopping: String?
    @State private var deleting: String?
    @State private var dating: TodoDueSheet.Target?
    @State private var creating = false
    private let workspace = TodoWorkspace.shared

    /// Whether this page's tree is the one the window shows: a tree on its way out leaves the workspace alone.
    private var shownHere: Bool { builtTree == nil || shownTree?.tree === builtTree }

    /// iOS 26 pins the bar over the list, which scrolls under it behind the scroll edge's effect. iOS 18 has no such
    /// effect to draw behind a pinned bar: the bar is the list's first row there, as the Inbox's (DESIGN_SYSTEM 3.4,
    /// "To-dos list").
    private static var pinsBar: Bool {
        if #available(iOS 26.0, *) { true } else { false }
    }

    private var drawsBar: Bool { model.ready && !model.calm && model.hasRows }

    /// The hub is out of reach, as the titles say it (`ConnectionStatus`, after its delay): the list is the one last read.
    private var offline: Bool {
        switch app.connection.shown {
        case .waitingForNetwork, .connecting, .rejected: true
        case .online, .updating: false
        }
    }

    var body: some View {
        ScrollViewReader { proxy in
            List {
                if model.ready {
                    if drawsBar && !Self.pinsBar {
                        TodosSectionBar(model: model, grouping: $grouping)
                            .listRowInsets(EdgeInsets())
                            .listRowSeparator(.hidden)
                            .themedRow()
                    }
                    if offline && model.hasRows {
                        TodosOfflineLine(model: model)
                            .listRowSeparator(.hidden)
                            .listRowBackground(Color.clear)
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
            .floatingFooter(over: Theme.pageBackground) {
                if model.ready { TodoNewPill { creating = true } }
            }
            .onScrollGeometryChange(for: CGFloat.self) { $0.contentInsets.top } action: { _, top in sight.covered = top }
            .onScrollPhaseChange { old, phase in
                model.hold.set(.scrolling, phase != .idle)
                if phase != .idle { model.settleArrivals() } // nothing fades in under a finger
                // Where a scroll came to rest; not the idle a page is made in, before its rows are drawn.
                if phase == .idle, old != .idle, let top = sight.top(in: model.groups) {
                    workspace.rested(workspace.place, at: top, shown: shownHere)
                }
            }
            .onAppear {
                model.grouping = grouping
                model.sort = sort
                model.start()
                model.hold.set(.outOfSight, false)
            }
            .onDisappear { model.hold.set(.outOfSight, true) }
            // Back where the place's list last rested, once its rows are laid out: only for a page made anew (another
            // place opened in between), never when a to-do pushed over it pops.
            .task(id: model.ready) {
                guard model.ready, !placed else { return }
                guard let key = workspace.scrolled(in: workspace.place), model.row(key) != nil else {
                    placed = true
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
                placed = true
            }
        }
        .detailTitle(String(localized: "All To-dos [To-dos List]"))
        .navigationBarTitleDisplayMode(.inline)
        .pageToolbar {
            ToolbarItem(placement: .primaryAction) { moreMenu }
        }
        .accessibleConfirmationDialog(String(localized: "Persona is still working on this. Mark it done and stop? [To-dos Actions]"),
                                      isPresented: Binding(get: { stopping != nil }, set: { if !$0 { stopping = nil } }),
                                      titleVisibility: .visible, hasCancel: true, presenting: stopping) { key in
            Button(String(localized: "Done and Stop [To-dos Actions]")) { model.close(key) }
                .accessibilityIdentifier("todos.stop.confirm")
            Button(String(localized: "Cancel"), role: .cancel) {}
        } message: { key in
            // Which to-do: the dialog is the page's, so it may point away from the row.
            Text(verbatim: model.item(key)?.title ?? "")
        }
        .accessibleConfirmationDialog(deleteTitle, isPresented: Binding(get: { deleting != nil }, set: { if !$0 { deleting = nil } }),
                                      titleVisibility: .visible, hasCancel: true, presenting: deleting) { key in
            Button(String(localized: "Delete To-Do [To-dos Actions]"), role: .destructive) { model.delete(key) }
                .accessibilityIdentifier("todos.delete.confirm")
            Button(String(localized: "Cancel"), role: .cancel) {}
        } message: { _ in
            Text("It's removed from your to-dos on every device. Work already under way on it keeps going. [To-dos Actions]")
        }
        .sheet(item: $dating) { target in
            TodoDueSheet(target: target) { day in model.setDue(target.key, day: day) }
        }
        .sheet(isPresented: $creating) {
            TodoNewSheet { title, notes in model.create(title: title, notes: notes) }
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
                    if let title = group.title { TodosSectionHeader(model: model, group: group, title: title) }
                }
                .listSectionSeparator(.hidden, edges: .top) // no line over a section's first row, the calm list's included
            }
        }
    }

    @ViewBuilder private func row(_ key: String) -> some View {
        if let cell = model.row(key) {
            // The row is the to-do: the link under it, so no chevron on every row (as the Inbox's rows; iOS 18 draws the
            // chevron of a link that is the row whatever it is told). A new to-do opens once the hub has it.
            TodoListRow(row: cell, tick: { tick(key) }, markDone: { tick(key) })
                .background {
                    if !cell.content.saving {
                        NavigationLink(value: TodoDetailRoute(id: cell.todoID)) { EmptyView() }.opacity(0)
                    }
                }
                .themedRow()
                .modifier(TodoRowActions(model: model, key: key, row: cell.content, tick: { tick(key) },
                                         pickDate: { dating = TodoDueSheet.Target(key: key, day: model.dueDay(key)) },
                                         askDelete: { deleting = key }, copyLink: { copyLink(key) }))
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

    // MARK: Writes

    /// The tick, the full swipe and Mark Done: a to-do someone works on asks first.
    private func tick(_ key: String) {
        switch model.tick(of: key) {
        case .close?: model.close(key)
        case .askFirst?: stopping = key
        case .takeBack(let id)?: model.undo(id)
        case .reopen?: model.reopen(key)
        case nil: break
        }
    }

    /// Delete…'s confirmation names the to-do.
    private var deleteTitle: String {
        let title = deleting.flatMap { model.item($0)?.title } ?? ""
        return String(localized: "Delete “\(title)”? [To-dos Actions]")
    }

    private func copyLink(_ key: String) {
        // The hub-level group's address its links are written with, else the one this device uses.
        guard let id = model.item(key)?.id, let link = app.hub,
              let url = TodoLink.web(id, on: link.hello.publicURL ?? link.config.url) else { return }
        UIPasteboard.general.url = url
        Haptics.success()
        app.toast = Toast(String(localized: "Copied [To-dos Toast]"), style: .success)
    }

    // MARK: Menus

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
                    Label(String(localized: "Show Someday [To-dos List]"), systemImage: Theme.Icon.someday)
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

/// "Can't reach your hub · showing what was saved 10:14": the list's one line while the hub is out of reach. A view of its
/// own, so only it follows when the list was last read.
private struct TodosOfflineLine: View {
    var model: TodosListModel

    var body: some View {
        NoticeBanner(text: model.offlineLine, tone: .caution, symbol: Theme.Icon.offline)
            .accessibilityIdentifier("todos.offline")
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
