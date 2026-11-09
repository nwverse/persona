import SwiftUI

// Renaming, moving and deleting in the file browser (docs/protocol/protocol.md "Changing files", #315): the same writes the
// machine allows for uploads (only folders in the home). A delete moves to the machine's trash, and the toast's Undo
// puts it back; there is no permanent delete. A move to a taken name asks Replace / Keep Both first.

@MainActor @Observable
final class FolderEdits {
    /// A move waiting for the answer to "already exists".
    struct Conflict {
        var name: String
        fileprivate var retry: (String) -> Void
    }

    private(set) var busy = false
    var conflict: Conflict?
    /// The last conflict's name, kept after the answer so the alert's title does not go blank as it closes.
    private(set) var conflictName = ""

    /// The hub can rename, move and trash (hello `file_edit`); an unknown list (older hub reply) offers it and lets
    /// the answer tell.
    static func available(_ model: AppModel, group: String) -> Bool {
        let features = model.features(group)
        return features.isEmpty || features.contains("file_edit")
    }

    /// Why name can't be a file's (nil: it can). The machine checks again; this is only to say it before asking.
    nonisolated static func nameProblem(_ name: String) -> String? {
        let n = name.trimmingCharacters(in: .whitespaces)
        if n.isEmpty { return String(localized: "Enter a name") }
        if n == "." || n == ".." { return String(localized: "A name can’t be “.” or “..”") }
        if n.contains("/") || n.contains("\0") { return String(localized: "A name can’t contain “/”") }
        return nil
    }

    func rename(_ path: String, to name: String, cwd: String, machine: MachineRef, model: AppModel, done: @escaping () -> Void) {
        let name = name.trimmingCharacters(in: .whitespaces)
        if let problem = Self.nameProblem(name) {
            model.toast = Toast(problem, style: .error)
            return
        }
        guard name != (path as NSString).lastPathComponent, let api = model.api(machine.group) else { return }
        run(model) {
            _ = try await api.renameFile(machine: machine.id, path: path, cwd: cwd, name: name)
            Haptics.tap()
            done()
        }
    }

    /// Moves paths into dir; a taken name asks, and the answer moves them again with it.
    func move(_ paths: [String], into dir: String, cwd: String, machine: MachineRef, model: AppModel, conflict policy: String? = nil,
              done: @escaping () -> Void) {
        guard !paths.isEmpty, let api = model.api(machine.group) else { return }
        run(model) { [weak self] in
            do {
                _ = try await api.moveFiles(machine: machine.id, paths: paths, cwd: cwd, dir: dir, conflict: policy)
            } catch let e as APIError where e.code == "exists" && policy == nil {
                let name = e.fields["name"] ?? (paths[0] as NSString).lastPathComponent
                self?.conflictName = name
                self?.conflict = Conflict(name: name) { [weak self] policy in
                    self?.move(paths, into: dir, cwd: cwd, machine: machine, model: model, conflict: policy, done: done)
                }
                return
            }
            Haptics.tap()
            model.toast = Toast(paths.count == 1
                ? String(localized: "Moved “\((paths[0] as NSString).lastPathComponent)” to \(Self.folderName(dir))")
                : String(localized: "Moved \(paths.count) items to \(Self.folderName(dir))"), style: .success)
            done()
        }
    }

    /// The answer to "already exists": "replace", "keep", or nil for Cancel.
    func answer(_ policy: String?) {
        let c = conflict
        conflict = nil
        if let policy { c?.retry(policy) }
    }

    /// Moves paths to the machine's trash; the toast's Undo puts them back. changed runs after either.
    func trash(_ paths: [String], cwd: String, machine: MachineRef, model: AppModel, changed: @escaping () -> Void) {
        guard !paths.isEmpty, let api = model.api(machine.group) else { return }
        run(model) {
            let trashed = try await api.trashFiles(machine: machine.id, paths: paths, cwd: cwd)
            Haptics.success()
            changed()
            let ids = trashed.map(\.trashID)
            let message = paths.count == 1
                ? String(localized: "Moved “\((paths[0] as NSString).lastPathComponent)” to the Trash")
                : String(localized: "Moved \(paths.count) items to the Trash")
            model.toast = Toast(message, style: .success, action: ids.isEmpty ? nil : Toast.Action(title: String(localized: "Undo")) {
                Task { @MainActor in
                    do {
                        _ = try await api.restoreFiles(machine: machine.id, trashIDs: ids)
                        changed()
                    } catch {
                        model.toast = Toast(String(localized: "Couldn’t put it back: \(Self.message(error))"), style: .error)
                    }
                }
            })
        }
    }

    private func run(_ model: AppModel, _ work: @escaping () async throws -> Void) {
        busy = true
        Task {
            defer { busy = false }
            do { try await work() } catch {
                if !error.isCancellation { model.toast = Toast(Self.message(error), style: .error) }
            }
        }
    }

    /// The last part of a folder as the browser shows it ("~" for the home).
    nonisolated static func folderName(_ dir: String) -> String {
        let d = dir.hasSuffix("/") && dir.count > 1 ? String(dir.dropLast()) : dir
        return d == "~" || d == "/" ? d : (d as NSString).lastPathComponent
    }

    /// What to tell the user when a change did not happen.
    nonisolated static func message(_ error: Error) -> String {
        if let e = error as? APIError {
            switch e.code {
            case "read_only": return String(localized: "This can’t be changed from your phone. Only files in the home folder can (except ~/.persona and ~/Library).")
            case "exists": return String(localized: "“\(e.fields["name"] ?? "")” already exists there")
            case "not_found": return String(localized: "It isn’t there anymore. Pull down to refresh.")
            case "node_outdated": return e.message
            default: break
            }
            if e.status == 404 { return String(localized: "The hub is too old to change files. Update the hub and try again.") }
        }
        return error.localizedDescription
    }
}

/// Where Move puts things: the browser's folders from its top (`cwd`), opened on the one shown; Move Here takes the
/// folder open. The folders being moved can't be opened (nothing goes into itself).
struct FolderPicker: View {
    @Environment(\.dismiss) private var dismiss
    let machineID: String
    var group: String
    let cwd: String
    /// The full paths being moved.
    let moving: [String]
    let pick: (String) -> Void
    /// The folders opened below cwd, each as its full path.
    @State private var opened: [String]

    init(machineID: String, group: String, cwd: String, start: [String], moving: [String], pick: @escaping (String) -> Void) {
        self.machineID = machineID
        self.group = group
        self.cwd = cwd
        self.moving = moving
        self.pick = pick
        var paths: [String] = [], at = cwd
        for name in start {
            at = (at as NSString).appendingPathComponent(name)
            paths.append(at)
        }
        _opened = State(initialValue: paths)
    }

    var body: some View {
        NavigationStack(path: $opened) {
            level(cwd)
                .navigationDestination(for: String.self) { level($0) }
        }
    }

    private func level(_ path: String) -> some View {
        FolderPickerList(machineID: machineID, group: group, cwd: cwd, path: path, moving: Set(moving)) { opened.append($0) }
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Move Here") {
                        pick(path)
                        dismiss()
                    }
                    .lineLimit(1)
                    // Moving to where they all are changes nothing.
                    .disabled(Set(moving.map { ($0 as NSString).deletingLastPathComponent }) == [path])
                    .accessibilityIdentifier("files.move.here")
                }
            }
    }
}

/// One folder's subfolders in the Move picker.
private struct FolderPickerList: View {
    @Environment(AppModel.self) private var model
    let machineID: String
    var group: String
    let cwd: String
    let path: String
    let moving: Set<String>
    let open: (String) -> Void
    @State private var folders: [FileEntry]?
    @State private var error: String?

    private var title: String {
        let name = (path as NSString).lastPathComponent
        return name.isEmpty ? "/" : name
    }

    var body: some View {
        List {
            if let folders {
                if folders.isEmpty {
                    Text("No folders here").foregroundStyle(.secondary).themedRow()
                }
                ForEach(folders) { f in
                    let full = (path as NSString).appendingPathComponent(f.name)
                    let blocked = moving.contains(full)
                    Button {
                        Haptics.tap()
                        open(full)
                    } label: {
                        HStack(spacing: Theme.Space.m) {
                            ThemedIcon(Theme.Icon.folderFilled).font(Theme.Font.body).foregroundStyle(Theme.accent)
                                .scaledFrame(width: Theme.Size.icon)
                            Text(f.name).font(Theme.Font.secondary).foregroundStyle(blocked ? Color.secondary : Color.primary).lineLimit(1)
                            Spacer(minLength: Theme.Space.s)
                            ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.labelStrong).foregroundStyle(.tertiary)
                        }
                        .contentShape(.rect)
                    }
                    .buttonStyle(.plain)
                    .disabled(blocked)
                    .themedRow()
                }
            } else if let error {
                ContentUnavailableView("Can't Open This Folder", systemImage: Theme.Icon.error, description: Text(error))
                    .listRowBackground(Color.clear)
            } else {
                HStack { Spacer(); ProgressView(); Spacer() }.listRowBackground(Color.clear)
            }
        }
        .listStyle(.plain)
        .themedPage()
        .navigationTitle(title)
        #if !os(macOS)
        .navigationBarTitleDisplayMode(.inline)
        #endif
        .accessibilityIdentifier("files.move.list")
        .task(id: path) {
            guard let api = model.api(group) else { return }
            do {
                let l = try await api.listDir(machine: machineID, path: path, cwd: cwd)
                folders = l.entries.filter { $0.dir && !$0.name.hasPrefix(".") }
            } catch {
                self.error = error.shown
            }
        }
    }
}
