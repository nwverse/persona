import SwiftUI

/// Shared by the iOS and Mac Agents list; the tag is a sibling of the fold button, never nested inside it.
struct TodoSpaceTag: View {
    @Environment(AppModel.self) private var model
    let group: SpaceGroup
    var open: (String) -> Void

    var body: some View {
        if let todo = linkedTodo {
            Button { open(todo.id) } label: {
                ThemedIcon(Theme.Icon.todo).font(Theme.Font.caption).foregroundStyle(.secondary)
                    .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
                    .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .accessibilityLabel("To-do: \(todo.title)")
            .help("To-do: \(todo.title)")
            .accessibilityIdentifier("todo.space." + group.id)
        }
    }

    private var linkedTodo: TodoItem? { Self.linkedTodo(group, model: model) }

    /// The to-do the space is linked to, on the hub that keeps the to-dos.
    @MainActor static func linkedTodo(_ group: SpaceGroup, model: AppModel) -> TodoItem? {
        let store = TodoStore.shared
        guard let space = group.todoSpace, store.spacesSupported, TodosLogic.isOnTodosHub(sessionGroup: group.group, hubGroup: model.hub?.group) else { return nil }
        return TodosLogic.findTodoForSpace(store.todos, machineID: group.machineID, space: space)
    }
}

/// The space's to-do as an item of a menu (By Repository's repository on iOS, #2531: its long-press menu, where the
/// tag was beside its header).
struct TodoSpaceMenuItem: View {
    @Environment(AppModel.self) private var model
    let group: SpaceGroup
    var open: (String) -> Void

    var body: some View {
        if let todo = TodoSpaceTag.linkedTodo(group, model: model) {
            Button { open(todo.id) } label: { Label("To-do: \(todo.title)", systemImage: Theme.Icon.todo) }
                .accessibilityIdentifier("todo.space." + group.id)
        }
    }
}

struct SpaceTodoMenu: View {
    @Environment(AppModel.self) private var model
    let group: SpaceGroup
    var link: () -> Void

    var body: some View {
        let store = TodoStore.shared
        let appModel: AppModel = model
        if let space = group.todoSpace, store.spacesSupported, TodosLogic.isOnTodosHub(sessionGroup: group.group, hubGroup: appModel.hub?.group) {
            let todo = TodosLogic.findTodoForSpace(store.todos, machineID: group.machineID, space: space)
            Button(action: link) {
                Label(todo == nil ? String(localized: "Add to To-Do…") : String(localized: "Move to Another To-do…"), systemImage: Theme.Icon.todo).lineLimit(1)
            }
            .disabled(store.changingSpaces)
            if let todo {
                Button(role: .destructive) {
                    Task {
                        do {
                            try await store.setSpace(todoID: todo.id,
                                                     space: TodoItem.Space(machineID: group.machineID, space: space), linked: false)
                        } catch { AppModel.shared.show(error) }
                    }
                } label: {
                    Label("Remove from To-Do", systemImage: Theme.Icon.link).lineLimit(1)
                }
                .disabled(store.changingSpaces)
            }
        }
    }
}

/// The same detail view is embedded in the Mac todo page.
struct TodoSpacesSection: View {
    let todo: TodoItem
    let sessions: [Session]
    var machineName: (String) -> String
    /// The todos' hub group and whether a computer of it is connected (`TodosLogic.hasLiveAgent`).
    var hubGroup: String?
    var machineOnline: (String) -> Bool

    var body: some View {
        if TodoStore.shared.spacesSupported, let spaces = todo.spaces, !spaces.isEmpty {
            Section {
                ForEach(spaces, id: \.self) { project in
                    HStack(spacing: Theme.Space.m) {
                        VStack(alignment: .leading, spacing: Theme.Space.xs) {
                            Text("\(project.space) · \(machineName(project.machineID))")
                                .font(Theme.Font.body).foregroundStyle(Color.primary).lineLimit(1)
                            if !TodosLogic.hasLiveAgent(in: project, sessions: sessions, hubGroup: hubGroup, machineOnline: machineOnline) {
                                Text("No agents right now").font(Theme.Font.caption).foregroundStyle(.secondary)
                            }
                        }
                        Spacer()
                        Button(role: .destructive) {
                            Task {
                                do { try await TodoStore.shared.setSpace(todoID: todo.id, space: project, linked: false) }
                                catch { AppModel.shared.show(error) }
                            }
                        } label: {
                            Text("Remove [Todo Space Views 95861c]").foregroundStyle(Theme.danger)
                        }
                        .font(Theme.Font.captionStrong)
                        .lineLimit(1)
                        .buttonStyle(.borderless)
                        .accessibilityLabel("Remove \(project.space) [Todo Space Views cf56a9]")
                        .disabled(TodoStore.shared.changingSpaces)
                    }
                    .padding(.vertical, Theme.Space.xs)
                }
            } header: {
                Text("Projects · \(spaces.count)").font(Theme.Font.sectionHeader).foregroundStyle(.secondary)
            }
        }
    }
}
