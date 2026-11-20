#if os(iOS)
import PersonaWire
import SwiftUI

/// The list's title as a menu of the same places as the sidebar (#10465; proposal 3.3: "All To-dos ▾", the second way
/// to a place, which the HIG asks for): All To-dos, Today and Upcoming, then the projects with their part of N, then
/// All Projects…. The system's title menu (`toolbarTitleMenu`); a pick changes the list where it is, with no animation.
/// Without the hub's places there is nothing to pick and no menu.
struct TodoTitleMenu: View {
    var places: TodosPlacesModel
    /// All Projects…: the sheet of every project.
    var allProjects: () -> Void

    var body: some View {
        let entries = places.entries
        if !entries.isEmpty {
            let current = places.current
            Section {
                choice(.all, current: current)
                ForEach(entries.fixed, id: \.id) { place in choice(TodoPlaceRef(id: place.id), current: current) }
            }
            if !entries.projects.isEmpty || entries.more {
                Section {
                    ForEach(entries.projects, id: \.id) { place in
                        choice(TodoPlaceRef(id: place.id), current: current, needsYou: place.needsYou)
                    }
                    if entries.more {
                        Button(String(localized: "All Projects… [To-dos Places]"), action: allProjects)
                            .accessibilityIdentifier("todos.places.menu.more")
                    }
                }
            }
        }
    }

    /// A place, ticked while shown; a project's part of N in words under its name (a menu item draws no badge).
    private func choice(_ place: TodoPlaceRef, current: TodoPlaceRef, needsYou: Int = 0) -> some View {
        Toggle(isOn: Binding(get: { current == place }, set: { on in
            guard on, place != current else { return }
            Haptics.tap()
            places.show(place)
        })) {
            Text(places.title(place))
            if needsYou > 0 { Text(StateName.count(.needsYou, needsYou)) }
        }
        .accessibilityIdentifier("todos.places.menu." + place.id)
    }
}

/// Tells the page the place to show and its name, as the owner picks it or once the hub stops listing it: a view of its
/// own, so the places' numbers changing redraw this and not the page (P5).
struct TodoPlaceFollower: View {
    var places: TodosPlacesModel
    var follow: (TodoPlaceRef, String) -> Void

    var body: some View {
        let place = places.current
        let title = places.title(place)
        Color.clear
            .onChange(of: PlaceShown(place: place, title: title), initial: true) { _, shown in follow(shown.place, shown.title) }
    }

    private struct PlaceShown: Equatable {
        var place: TodoPlaceRef
        var title: String
    }
}

/// Every project as a place (All Projects…, from the sidebar or the title menu): the hub's projects with open to-dos,
/// each with its part of N, then the knowledge base's others, by name. A task over the page; a pick closes it and the
/// caller opens the place.
struct TodoAllProjectsSheet: View {
    @Environment(\.dismiss) private var dismiss
    @Environment(AppModel.self) private var app
    var places: TodosPlacesModel
    var pick: (TodoPlaceRef) -> Void

    var body: some View {
        NavigationStack {
            List {
                let projects = places.allProjects
                if projects.isEmpty {
                    EmptyStateView(symbol: Theme.Icon.space, title: String(localized: "No projects yet [To-dos Places]"),
                                   message: String(localized: "Projects come from your knowledge base. [To-dos Places]"))
                        .listRowBackground(Color.clear)
                }
                ForEach(projects, id: \.id) { place in
                    Button {
                        Haptics.tap()
                        pick(TodoPlaceRef(id: place.id))
                    } label: {
                        HStack(spacing: Theme.Space.m) {
                            ThemedIcon(Theme.Icon.space).foregroundStyle(.secondary)
                            Text(places.title(TodoPlaceRef(id: place.id))).foregroundStyle(Color.primary).lineLimit(1)
                            Spacer(minLength: Theme.Space.s)
                            if place.needsYou > 0 { CountBadge(count: place.needsYou, look: .needs) }
                        }
                        .contentShape(.rect)
                    }
                    .buttonStyle(.plain)
                    .themedRow()
                    .accessibilityIdentifier("todos.places.project." + place.id)
                }
            }
            .listStyle(.insetGrouped)
            .themedPage()
            .navigationTitle(String(localized: "Projects [To-dos Places]"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { SheetClose { dismiss() } }
        }
        // The knowledge base's projects, read as the sheet opens (one made meanwhile shows up).
        .task { await TodoProjects.shared.load(app.api?.currentAssistantSettings()) }
    }
}
#endif
