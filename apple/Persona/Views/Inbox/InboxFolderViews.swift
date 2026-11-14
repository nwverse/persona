import SwiftUI

/// "Move to Folder…" (a row's long-press menu, the item's ⋯; docs/assistant/inbox.md "Folders"): the path, the folders
/// in use to pick from, then No Folder and Let AI Decide. Filing by hand keeps the model away from the item.
struct InboxFolderMoveSheet: View {
    @Environment(\.dismiss) private var dismiss
    @State private var model: InboxFolderMoveModel
    @FocusState private var focused: Bool

    init(item: InboxItem, deps: InboxFolderMoveModel.Deps = .live) {
        _model = State(initialValue: InboxFolderMoveModel(item: item, deps: deps))
    }

    var body: some View {
        @Bindable var model = model
        NavigationStack {
            Form {
                Section {
                    TextField(String(localized: "Folder Path"), text: $model.text, prompt: Text("e.g. work/alerts/engine"))
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .focused($focused)
                        .submitLabel(.done)
                        .onSubmit { if model.canMove { move(model.path) } }
                        .accessibilityIdentifier("inbox.folder.path")
                } header: {
                    Text(model.item.title).lineLimit(2).textCase(nil)
                } footer: {
                    Text("Use / between levels. AI won’t move it again once you file it.")
                }
                if !model.suggestions.isEmpty {
                    Section {
                        ForEach(model.suggestions, id: \.self) { f in
                            Button { model.text = f } label: {
                                Label { Text(f).lineLimit(1).foregroundStyle(.primary) } icon: { ThemedIcon(Theme.Icon.inboxFolder) }
                            }
                        }
                    } header: {
                        Text("Folders in Use")
                    }
                }
                Section {
                    Button { move("") } label: { Label("No Folder", systemImage: Theme.Icon.inboxFolderNone).lineLimit(1) }
                        .disabled(!model.canClear)
                        .accessibilityIdentifier("inbox.folder.none")
                    Button { move(nil) } label: { Label("Let AI Decide", systemImage: Theme.Icon.ai).lineLimit(1) }
                        .disabled(!model.canAuto)
                        .accessibilityIdentifier("inbox.folder.auto")
                }
            }
            .themedGroupedPage()
            .navigationTitle(String(localized: "Move to Folder"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button { dismiss() } label: { Text("Cancel").lineLimit(1) }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button { if model.canMove { move(model.path) } } label: {
                        if model.busy { ProgressView().controlSize(.small) } else { Text("Move [Inbox Folders]").lineLimit(1) }
                    }
                    .disabled(!model.canMove)
                    .accessibilityIdentifier("inbox.folder.move")
                }
            }
            .onAppear { focused = true }
        }
    }

    /// A path, "" (No Folder) or nil (Let AI Decide); the sheet closes once the hub took it.
    private func move(_ folder: String?) {
        Task { if await model.move(folder) { dismiss() } }
    }
}

/// "Move to Folder…" in an item's menus, the folder it is in under it.
struct InboxFolderMoveButton: View {
    var item: InboxItem
    var open: () -> Void

    var body: some View {
        Button(action: open) {
            Label("Move to Folder…", systemImage: Theme.Icon.inboxFolder)
            if let folder = item.folder, !folder.isEmpty { Text(folder) }
        }
        .accessibilityIdentifier("inbox.moveToFolder")
    }
}

/// A row's folder when the list is not already in it: the folder icon and its path.
struct InboxFolderLine: View {
    var path: String

    var body: some View {
        HStack(spacing: Theme.Space.xs) {
            ThemedIcon(Theme.Icon.inboxFolder).font(Theme.Font.label)
            Text(path).font(Theme.Font.caption).lineLimit(1).truncationMode(.middle)
        }
        .foregroundStyle(.secondary)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(Text("Folder: \(path) [Inbox Folders]"))
        .accessibilityIdentifier("inbox.rowFolder")
    }
}

/// Settings › Inbox Folders (`InboxFolderSettingsModel`; the web's "Folders…"): automatic filing on or off, then the
/// user's folder rules. Off, the Inbox has no folders at all; on, it gets the folder bar and Move to Folder….
struct InboxFolderSettingsView: View {
    @State private var model = InboxFolderSettingsModel()

    var body: some View {
        @Bindable var model = model
        SettingsForm {
            if let error = model.error {
                Section {
                    Label(error, systemImage: Theme.Icon.error).font(Theme.Font.caption).foregroundStyle(Theme.danger)
                }
            }
            if model.unsupported {
                Section {
                    Label("This hub doesn't support Inbox folders yet. Update the hub, then turn them on here.", systemImage: Theme.Icon.info)
                        .font(Theme.Font.secondary).foregroundStyle(.secondary)
                }
            } else {
                Section {
                    SettingsSwitchRow(title: String(localized: "File Items Automatically"),
                                      detail: String(localized: "AI puts each new item in a folder such as work/alerts/engine, reusing the folders you already have. Uses the triage model."),
                                      symbol: Theme.Icon.inboxFolder, titleLines: 1, accessibilityID: "inbox-folders-enabled",
                                      isOn: Binding { model.settings?.enabled ?? false } set: { on in Task { await model.setEnabled(on) } })
                        .disabled(!model.canChange)
                } footer: {
                    Text("Folders show in the Inbox while this is on: browse by folder, and move items with Move to Folder….")
                }
                Section {
                    TextField(String(localized: "Folder Rules"), text: $model.rules,
                              prompt: Text("e.g. Messages from the build bot go to work/builds; reviews go to projects/<name>/reviews"),
                              axis: .vertical)
                        .lineLimit(3...8)
                        .disabled(!model.canChange)
                        .accessibilityIdentifier("inbox-folders-rules")
                    if model.rulesChanged {
                        FormAction {
                            Button { Task { await model.saveRules() } } label: {
                                Text("Save Rules").lineLimit(1).formActionWidth()
                            }
                            .formButton(prominent: true)
                            .disabled(!model.canChange)
                            .accessibilityIdentifier("inbox-folders-save")
                        }
                    }
                } header: {
                    Text("Folder Rules")
                } footer: {
                    Text("In your own words. They come before AI’s own choices.")
                }
            }
        }
        .overlay { if model.settings == nil && model.error == nil && !model.unsupported { ProgressView() } }
        .themedGroupedPage()
        .detailTitle(String(localized: "Inbox Folders"))
        .navigationBarTitleDisplayMode(.inline)
        .task { await model.load() }
        .refreshable { await model.load() }
    }
}
