import Foundation
import PersonaWire
#if os(iOS)
import SwiftUI
import UIKit

/// What a To-dos row offers besides opening it (#10465; docs/design/proposals/todos-redesign.md 3.4, "Swipe"; DESIGN_SYSTEM
/// 3.4 "To-dos list"): one trailing swipe, Done (the full swipe) and Someday, as the Inbox swipes; and the long-press
/// menu: Today, Date…, Project, Someday, Copy Link and Delete…. A closed to-do swipes to Reopen; one ticked a moment ago
/// to Undo; one still being saved offers nothing yet.
struct TodoRowActions: ViewModifier {
    var model: TodosListModel
    var key: String
    var row: TodoRowContent
    /// The tick's own action (`TodoListView.tick`): it may ask first.
    var tick: () -> Void
    var pickDate: () -> Void
    var askDelete: () -> Void
    var copyLink: () -> Void

    func body(content: Content) -> some View {
        content
            .swipeActions(edge: .trailing, allowsFullSwipe: true) { swipe }
            .contextMenu { menu }
    }

    @ViewBuilder private var swipe: some View {
        if row.saving {
            EmptyView()
        } else if row.struck {
            Button(action: tick) { Label(String(localized: "Undo [To-dos Actions]"), systemImage: Theme.Icon.undo) }
                .tint(Theme.accent)
                .accessibilityIdentifier("todos.swipe.undo")
        } else if row.closed {
            Button(action: tick) { Label(String(localized: "Reopen [To-dos Actions]"), systemImage: Theme.Icon.undo) }
                .tint(Theme.accent)
                .accessibilityIdentifier("todos.swipe.reopen")
        } else {
            Button(action: tick) { Label(String(localized: "Done [To-dos Actions]"), systemImage: Theme.Icon.done) }
                .tint(Theme.positive)
                .accessibilityIdentifier("todos.swipe.done")
            somedayButton.tint(Theme.somedayTint).accessibilityIdentifier("todos.swipe.someday")
        }
    }

    @ViewBuilder private var somedayButton: some View {
        if row.someday {
            Button { model.outOfSomeday(key) } label: {
                Label(String(localized: "Remove from Someday [To-dos Actions]"), systemImage: Theme.Icon.someday)
            }
        } else {
            Button { model.someday(key) } label: { Label(StateName.todoSomeday, systemImage: Theme.Icon.someday) }
        }
    }

    @ViewBuilder private var menu: some View {
        if !row.saving {
            if row.closed && !row.struck {
                Button(action: tick) { Label(String(localized: "Reopen [To-dos Actions]"), systemImage: Theme.Icon.undo) }
                    .accessibilityIdentifier("todos.menu.reopen")
            } else if !row.struck {
                Section {
                    Button { model.setDue(key, day: Date()) } label: {
                        Label(String(localized: "Today [To-dos Actions]"), systemImage: Theme.Icon.today)
                    }
                    .accessibilityIdentifier("todos.menu.today")
                    Button(action: pickDate) { Label(String(localized: "Date… [To-dos Actions]"), systemImage: Theme.Icon.dueDate) }
                        .accessibilityIdentifier("todos.menu.date")
                    TodoProjectMenu(model: model, key: key, current: model.item(key)?.project ?? "")
                    somedayButton.accessibilityIdentifier("todos.menu.someday")
                }
            }
            Section {
                Button(action: copyLink) { Label(String(localized: "Copy Link [To-dos Actions]"), systemImage: Theme.Icon.link) }
                    .accessibilityIdentifier("todos.menu.copyLink")
            }
            Section {
                Button(role: .destructive, action: askDelete) {
                    Label(String(localized: "Delete… [To-dos Actions]"), systemImage: Theme.Icon.delete)
                }
                .accessibilityIdentifier("todos.menu.delete")
            }
        }
    }
}

/// Project in a row's menu: the knowledge base's projects to file it under, or none. Reads the projects itself, so only
/// an open menu follows them (P5).
private struct TodoProjectMenu: View {
    var model: TodosListModel
    var key: String
    var current: String

    var body: some View {
        Menu {
            Picker(selection: Binding(get: { current }, set: { model.setProject(key, $0) })) {
                Text("No Project [To-dos Actions]").tag("")
                ForEach(TodoProjectLogic.choices(TodoProjects.shared.list, current: current.isEmpty ? nil : current)) { p in
                    Text(p.name).tag(p.slug)
                }
            } label: {
                Text("Project [To-dos Actions]")
            }
            .pickerStyle(.inline)
        } label: {
            Label(String(localized: "Project [To-dos Actions]"), systemImage: Theme.Icon.space)
        }
    }
}

/// New To-Do, floating at the list's foot (the V1 frames' pill): the Theme Color's pill, as wide as its words.
struct TodoNewPill: View {
    var action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: Theme.Space.xs) {
                ThemedIcon(Theme.Icon.add)
                Text("New To-Do [To-dos Actions]").buttonLabelLine(minimumScale: 1)
            }
            .floatingPill(prominent: true)
            .fixedSize(horizontal: true, vertical: false)
        }
        .buttonStyle(.plain)
        .accessibilityShowsLargeContentViewer()
        .accessibilityIdentifier("todos.new")
        .padding(.vertical, Theme.Space.s)
        .frame(maxWidth: .infinity) // the foot's blur across the page, the pill in its middle
    }
}

/// The New To-Do sheet: a title and notes, and Add. It closes at once: the to-do is listed while it is saved, and goes
/// to the hub with its op id when it can (`TodosListModel.create`).
struct TodoNewSheet: View {
    var add: (_ title: String, _ notes: String?) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var title = ""
    @State private var notes = ""
    /// The notes' mic, made as the sheet appears (P3: a `@State` initial value does nothing).
    @State private var dictation: Dictation?
    @FocusState private var focused: Bool

    private var ready: Bool { !title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField(String(localized: "Title [To-dos New]"), text: $title)
                        .lineLimit(1)
                        .focused($focused)
                        .submitLabel(.done)
                        .onSubmit(save)
                        .accessibilityIdentifier("todos.new.title")
                    HStack(alignment: .top, spacing: Theme.Space.s) {
                        TextField(String(localized: "Notes [To-dos New]"), text: $notes, axis: .vertical)
                            .lineLimit(3...8)
                            .accessibilityIdentifier("todos.new.notes")
                        if let dictation {
                            FieldMicButton(dictation: dictation, text: $notes, id: "todos.new.notes.mic")
                                .font(Theme.Font.body)
                                .foregroundStyle(Theme.accent)
                        }
                    }
                }
            }
            .themedPage()
            .onDisappear { dictation?.stop() } // cancelled, swiped away or added
            .navigationTitle(String(localized: "New To-Do [To-dos Actions]"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                SheetClose { dismiss() }
                SheetConfirm(id: "todos.new.add", name: String(localized: "Add [To-dos New]"), disabled: !ready, action: save)
            }
            .onAppear {
                if dictation == nil { dictation = Dictation() }
                focused = true
            }
        }
        .presentationDetents([.medium])
    }

    private func save() {
        guard ready else { return }
        dictation?.stop() // what is saved is what the field shows
        add(title, notes)
        dismiss()
    }
}

/// Date… on a row: a day for it to be due, or none.
struct TodoDueSheet: View {
    struct Target: Identifiable {
        var key: String
        var day: Date?
        var id: String { key }
    }

    var target: Target
    var save: (_ day: Date?) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var day: Date

    init(target: Target, save: @escaping (_ day: Date?) -> Void) {
        self.target = target
        self.save = save
        _day = State(initialValue: target.day ?? Date())
    }

    var body: some View {
        NavigationStack {
            Form {
                DatePicker(String(localized: "Due Date [To-dos Due Sheet]"), selection: $day, displayedComponents: .date)
                    .datePickerStyle(.graphical)
                    .accessibilityIdentifier("todos.due.day")
                if target.day != nil {
                    Button(role: .destructive) {
                        save(nil)
                        dismiss()
                    } label: {
                        Text("Remove Date [To-dos Due Sheet]")
                    }
                    .accessibilityIdentifier("todos.due.remove")
                }
            }
            .themedPage()
            .navigationTitle(String(localized: "Due Date [To-dos Due Sheet]"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                SheetClose { dismiss() }
                SheetConfirm(id: "todos.due.save", name: String(localized: "Save [To-dos Due Sheet]")) {
                    save(day)
                    dismiss()
                }
            }
        }
    }
}
#endif

// The pure parts, on every platform: the list's model says the offline line.

/// A to-do's web link (docs/protocol/deep-links.md, "A to-do": `/todos/<id>`) on the hub's address its links are written
/// with (its `public_url`, else the one this device uses), so it opens in a browser too.
nonisolated enum TodoLink {
    static func web(_ id: String, on hub: URL) -> URL? {
        var base = hub.absoluteString
        while base.hasSuffix("/") { base.removeLast() }
        let path: String = "/todos/" + APIClient.todoPath(id)
        return URL(string: base + path)
    }
}

/// The list's one line while the hub is out of reach (proposal 3.4, "Offline"): "Can't reach your hub · showing what
/// was saved 10:14", the time the hub last answered a read of the list (another day's with its day); no time when it
/// never answered on this device.
nonisolated enum TodoOfflineLine {
    static func text(readAt: Date?, now: Date, words: TodoWords = TodoWords(), calendar: Calendar = .current) -> String {
        guard let at = readAt else { return words("Can't reach your hub · showing what was saved [To-dos Offline]") }
        var style = Date.FormatStyle(calendar: calendar, timeZone: calendar.timeZone).locale(words.formatting).hour().minute()
        if !calendar.isDate(at, inSameDayAs: now) {
            let days = abs(calendar.dateComponents([.day], from: calendar.startOfDay(for: at), to: calendar.startOfDay(for: now)).day ?? 99)
            style = days <= 6 ? style.weekday(.abbreviated) : style.month(.abbreviated).day()
        }
        return words("Can't reach your hub · showing what was saved \(at.formatted(style)) [To-dos Offline]")
    }
}
