import PhotosUI
import SwiftUI

/// The Files tab: the session's directory as a tree you walk into, with git change marks; tap a file to read it.
/// Files go into the folder shown from this device (Upload: the Files app or the photo library, or dropped on it on an iPad)
/// and out of it (Save to Files); a long press renames, moves or moves to the trash (Undo in the toast), and Select does
/// it for several. docs/protocol/protocol.md "Changing files". The machine page opens it on the home (`cwd` "~").
struct FilesPane: View {
    @Environment(AppModel.self) private var model
    let machineID: String
    /// The group of hubs the machine is in (docs/protocol/multi-hub.md).
    var group: String = ""
    let cwd: String
    var git: GitStatus?
    var key: SessionKey? = nil
    /// Reads the folder's Git status again (its marks come from the chat's): live refresh when it changes (#3089).
    var reloadGit: (() async -> Void)? = nil
    @State private var stack: [String] = [] // folders entered below cwd
    @State private var listing: FileListing?
    @State private var listed = "" // the path `listing` belongs to (the server returns it cleaned)
    @State private var error: String?
    @State private var file: FileRef?
    @AppStorage("files.showHidden") private var showHidden = false
    @State private var transfers = FolderTransfers()
    @State private var pickingFiles = false
    @State private var pickingPhotos = false
    @State private var photoPicks: [PhotosPickerItem] = []
    @State private var edits = FolderEdits()
    @State private var selecting = false
    @State private var selection: Set<String> = [] // names in the folder shown
    @State private var renaming: FileEntry?
    @State private var newName = ""
    @State private var trashing: [String] = [] // names waiting for the confirmation
    @State private var moving: [String] = [] // names the folder picker is open for
    @State private var dropping = false

    private var path: String { stack.reduce(cwd) { ($0 as NSString).appendingPathComponent($1) } }
    private var machine: MachineRef { MachineRef(group: group, id: machineID) }
    private var canEdit: Bool { FolderEdits.available(model, group: group) }
    private func full(_ name: String) -> String { (path as NSString).appendingPathComponent(name) }

    var body: some View {
        VStack(spacing: 0) {
            breadcrumb.accessibilityElement(children: .contain).accessibilityIdentifier("files.pane")
            List {
                if let listing, listed == path {
                    let entries = listing.entries.filter { showHidden || !$0.name.hasPrefix(".") }
                    if entries.isEmpty {
                        Text(listing.entries.isEmpty ? String(localized: "Empty folder") : String(localized: "Only hidden files")).foregroundStyle(.secondary)
                    }
                    ForEach(entries) { e in entryRow(e) }
                    if listing.truncated {
                        Text("Too many files; showing the first 2,000").font(Theme.Font.label).foregroundStyle(.secondary)
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
            .refreshable { await load() }
            // Files dragged in from another app (an iPad beside Files or Photos) upload into the folder shown.
            .dropDestination(for: PickedLibraryFile.self) { files, _ in
                upload(files.map(\.url), owned: true)
                return !files.isEmpty
            } isTargeted: { dropping = $0 }
            .overlay {
                if dropping {
                    RoundedRectangle(cornerRadius: Theme.Radius.card).strokeBorder(Theme.accent, lineWidth: 2)
                        .padding(Theme.Space.s)
                        .allowsHitTesting(false)
                        .accessibilityIdentifier("files.drop")
                }
            }
            .safeAreaInset(edge: .bottom) {
                VStack(spacing: Theme.Space.s) {
                    if !transfers.items.isEmpty {
                        FolderTransfersBar(transfers: transfers)
                    }
                    if selecting { selectionBar }
                }
                .padding(.horizontal, Theme.Space.l)
                .padding(.bottom, Theme.Space.s)
            }
        }
        .task(id: path) { await load() }
        .onChange(of: path) { selection = []; selecting = false }
        // Live refresh (#3089): the folder shown, and the Git marks when the chat can read them again.
        .fsLive(group: group, machineID: machineID, cwd: cwd, paths: [path], git: reloadGit != nil) { change in
            if change.dirs.contains(path) { Task { await load() } }
            if change.git, let reloadGit { Task { await reloadGit() } }
        }
        .sheet(item: $file) { ref in FileViewer(ref: ref, key: key) }
        .fileUploadPicker(isPresented: $pickingFiles) { upload($0) }
        .photosPicker(isPresented: $pickingPhotos, selection: $photoPicks, maxSelectionCount: 20, matching: .any(of: [.images, .videos]))
        .onChange(of: photoPicks) { _, picks in
            guard !picks.isEmpty else { return }
            photoPicks = []
            Task {
                let got = await PickedLibraryFile.load(picks)
                if got.failed > 0 { model.toast = Toast(String(localized: "\(got.failed) items could not be read"), style: .error) }
                upload(got.urls, owned: true)
            }
        }
        // An alert, not a confirmation dialog: from a list that one is a popover with no Cancel (lessons/ios.md, #1830).
        .alert(String(localized: "“\(transfers.conflictName)” already exists"),
               isPresented: Binding(get: { transfers.conflict != nil }, set: { if !$0 { transfers.answer(.cancel) } })) {
            Button("Replace") { transfers.answer(.replace) }
            Button("Keep Both") { transfers.answer(.keep) }
            Button("Cancel", role: .cancel) { transfers.answer(.cancel) }
        } message: {
            Text("This folder already has a file with that name. Replace overwrites it; Keep Both saves the new file as “name 2”.")
        }
        .fileMover(isPresented: Binding(get: { transfers.saving != nil }, set: { if !$0 { transfers.saved() } }),
                   file: transfers.saving) { _ in transfers.saved() }
        .alert("Rename [Chats View 0d0cba]", isPresented: Binding(get: { renaming != nil }, set: { if !$0 { renaming = nil } })) {
            TextField("Name [Personal Agent Prefs d44e9b]", text: $newName)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .accessibilityIdentifier("files.rename.field")
            Button("Rename [Chats View 0d0cba]") {
                if let e = renaming { edits.rename(full(e.name), to: newName, cwd: cwd, machine: machine, model: model) { Task { await load() } } }
                renaming = nil
            }
            Button("Cancel", role: .cancel) { renaming = nil }
        }
        .alert(trashTitle, isPresented: Binding(get: { !trashing.isEmpty }, set: { if !$0 { trashing = [] } })) {
            Button("Move to Trash", role: .destructive) {
                let names = trashing
                trashing = []
                selecting = false
                selection = []
                edits.trash(names.map(full), cwd: cwd, machine: machine, model: model) { Task { await load() } }
            }
            Button("Cancel", role: .cancel) { trashing = [] }
        } message: {
            Text("It goes to the computer’s Trash. You can undo right after, or put it back from the Trash there.")
        }
        .alert(String(localized: "“\(edits.conflictName)” already exists"),
               isPresented: Binding(get: { edits.conflict != nil }, set: { if !$0 { edits.answer(nil) } })) {
            Button("Replace") { edits.answer("replace") }
            Button("Keep Both") { edits.answer("keep") }
            Button("Cancel", role: .cancel) { edits.answer(nil) }
        } message: {
            Text("The folder already has an item with that name. Replace moves the one there to the Trash; Keep Both names this one “name 2”.")
        }
        .sheet(isPresented: Binding(get: { !moving.isEmpty }, set: { if !$0 { moving = [] } })) {
            FolderPicker(machineID: machineID, group: group, cwd: cwd, start: stack, moving: moving.map(full)) { dir in
                let names = moving
                moving = []
                selecting = false
                selection = []
                edits.move(names.map(full), into: dir, cwd: cwd, machine: machine, model: model) { Task { await load() } }
            }
        }
        .onDisappear { transfers.stop() }
        #if DEBUG
        .task { // `-showFilesDrop YES`: the folder looks as it does with files dragged over it (iPad), for screenshots
            if UserDefaults.standard.bool(forKey: "showFilesDrop") { dropping = true }
        }
        .task { // `-showFolderUpload <name>`: a made-up 24 MB file of that name uploading here (an existing name asks first), for screenshots
            if let name = UserDefaults.standard.string(forKey: "showFolderUpload") {
                try? await Task.sleep(for: .seconds(2))
                let dir = FileManager.default.temporaryDirectory.appending(path: "uploads/" + UUID().uuidString)
                try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
                let url = dir.appending(path: name)
                try? Data(count: 24 << 20).write(to: url)
                upload([url], owned: true)
            }
        }
        #endif
        #if os(macOS)
        .pageToolbar { MacRefreshItem(id: [machineID, cwd]) { await load() } } // no pull to refresh on the Mac
        #endif
    }

    private var breadcrumb: some View {
        HStack(spacing: Theme.Space.s) {
            if !stack.isEmpty {
                Button {
                    Haptics.tap()
                    stack.removeLast()
                } label: {
                    ThemedIcon(Theme.Icon.back).font(Theme.Font.captionStrong).scaledFrame(width: Theme.Size.wellS, height: Theme.Size.wellS)
                }
                .themedButton()
                .clipShape(.circle)
                .accessibilityLabel("Parent Folder")
            }
            ScrollViewReader { proxy in
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: Theme.Space.xs) {
                        crumb((cwd as NSString).lastPathComponent, level: 0)
                        ForEach(Array(stack.enumerated()), id: \.offset) { i, name in
                            ThemedIcon("chevron.compact.right").font(Theme.Font.label).foregroundStyle(.tertiary)
                            crumb(name, level: i + 1)
                        }
                    }
                    .padding(.vertical, Theme.Space.xxs)
                }
                .onChange(of: stack.count) { _, n in withAnimation { proxy.scrollTo(n, anchor: .trailing) } }
            }
            if selecting {
                Button {
                    selecting = false
                    selection = []
                } label: {
                    Text("Done [Extension System Presentation c0b3fb]").font(Theme.Font.captionStrong).lineLimit(1)
                        .padding(.horizontal, Theme.Space.m)
                        .scaledFrame(height: Theme.Size.wellS)
                }
                .themedButton()
                .clipShape(.capsule)
                .accessibilityIdentifier("files.select.done")
            } else {
            Menu {
                Button { pickingFiles = true } label: { Label("Choose from Files", systemImage: Theme.Icon.file) }
                Button { pickingPhotos = true } label: { Label("Choose from Photos", systemImage: Theme.Icon.photos) }
            } label: {
                ThemedIcon(Theme.Icon.upload).font(Theme.Font.captionStrong).scaledFrame(width: Theme.Size.wellS, height: Theme.Size.wellS)
            }
            #if os(macOS)
            .menuStyle(.button)
            .menuIndicator(.hidden)
            #endif
            .themedButton()
            .clipShape(.circle)
            .accessibilityLabel("Upload to This Folder")
            .accessibilityIdentifier("files.upload")
            Menu {
                if canEdit, listing?.entries.isEmpty == false {
                    Button {
                        selection = []
                        selecting = true
                    } label: { Label("Select [Translation c11330]", systemImage: Theme.Icon.success) }
                }
                Toggle(isOn: $showHidden) { Label("Show Hidden Files", systemImage: Theme.Icon.view) }
                Button { UIPasteboard.general.string = path } label: { Label("Copy Path", systemImage: Theme.Icon.copy) }
            } label: {
                ThemedIcon(Theme.Icon.more).font(Theme.Font.captionStrong).scaledFrame(width: Theme.Size.wellS, height: Theme.Size.wellS)
            }
            #if os(macOS)
            // A Mac menu draws as a pop-up button with its own arrow: the ⋯ was squeezed to two dots inside the circle.
            .menuStyle(.button)
            .menuIndicator(.hidden)
            #endif
            .themedButton()
            .clipShape(.circle)
            .accessibilityIdentifier("files.more")
            }
        }
        .padding(.horizontal, Theme.Space.l)
        .padding(.vertical, Theme.Space.sm)
    }

    private var trashTitle: String {
        trashing.count == 1 ? String(localized: "Move “\(trashing[0])” to the Trash?") : String(localized: "Move \(trashing.count) items to the Trash?")
    }

    /// Under the list while selecting: what is picked, and Move / Move to Trash for all of it.
    private var selectionBar: some View {
        HStack(spacing: Theme.Space.m) {
            Text(selection.isEmpty ? String(localized: "Select Items") : String(localized: "\(selection.count) Selected"))
                .font(Theme.Font.secondaryMedium)
                .lineLimit(1)
                .accessibilityIdentifier("files.select.count")
            Spacer(minLength: Theme.Space.s)
            Button { moving = Array(selection).sorted() } label: {
                Label("Move", systemImage: Theme.Icon.folder).lineLimit(1)
            }
            .themedButton()
            .disabled(selection.isEmpty || edits.busy)
            .accessibilityIdentifier("files.select.move")
            Button(role: .destructive) { trashing = Array(selection).sorted() } label: {
                Label("Move to Trash", systemImage: Theme.Icon.delete).labelStyle(.iconOnly)
            }
            .themedButton()
            .disabled(selection.isEmpty || edits.busy)
            .accessibilityLabel("Move to Trash")
            .accessibilityIdentifier("files.select.trash")
        }
        .padding(.horizontal, Theme.Space.l)
        .padding(.vertical, Theme.Space.m)
        .surface(in: .capsule)
    }

    private func crumb(_ name: String, level: Int) -> some View {
        let current = level == stack.count
        return Button {
            guard !current else { return }
            Haptics.tap()
            stack = Array(stack.prefix(level))
        } label: {
            Text(name.isEmpty ? "/" : name)
                .font(current ? Theme.Font.captionStrong : Theme.Font.caption)
                .lineLimit(1)
                .foregroundStyle(current ? Color.primary : Color.secondary)
                .padding(.horizontal, Theme.Space.s)
                .padding(.vertical, Theme.Space.xs)
                .background(current ? Theme.Fill.chip : .clear, in: .capsule)
        }
        .buttonStyle(.plain)
        .id(level)
    }

    private func entryRow(_ e: FileEntry) -> some View {
        let full = (path as NSString).appendingPathComponent(e.name)
        let mark = gitMark(full, dir: e.dir)
        return Button {
            if selecting {
                Haptics.tap()
                if selection.contains(e.name) { selection.remove(e.name) } else { selection.insert(e.name) }
            } else if e.dir {
                Haptics.tap()
                stack.append(e.name)
            } else {
                file = FileRef(group: group, machineID: machineID, path: full, cwd: cwd)
            }
        } label: {
            HStack(spacing: Theme.Space.m) {
                if selecting {
                    ThemedIcon(selection.contains(e.name) ? Theme.Icon.successFilled : Theme.Icon.unchecked)
                        .font(Theme.Font.body)
                        .foregroundStyle(selection.contains(e.name) ? Theme.accent : Color.secondary)
                        .scaledFrame(width: Theme.Size.icon)
                }
                if e.dir {
                    ThemedIcon(Theme.Icon.folderFilled)
                        .font(Theme.Font.body)
                        .foregroundStyle(Theme.accent)
                        .scaledFrame(width: Theme.Size.icon)
                } else {
                    FileThumbnailIcon(ref: FileRef(group: group, machineID: machineID, path: full, cwd: cwd), size: e.size, modified: e.mtime,
                                      symbol: FileViewer.symbol(for: e.name))
                }
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    HStack(spacing: Theme.Space.xs) {
                        Text(e.name).font(Theme.Font.secondary).foregroundStyle(e.name.hasPrefix(".") ? Color.secondary : Color.primary).lineLimit(1)
                        if e.symlink == true { ThemedIcon("arrow.turn.up.right").font(Theme.Font.label).foregroundStyle(.tertiary) }
                    }
                    HStack(spacing: Theme.Space.sm) {
                        if !e.dir { Text(e.size.storageText) }
                        if let m = e.mtime { Text(Fmt.relative(m)) }
                    }
                    .font(Theme.Font.label)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                }
                Spacer(minLength: 6)
                if let mark {
                    if e.dir {
                        Circle().fill(GitBadge(letter: mark).color).scaledFrame(width: Theme.Size.dotS, height: Theme.Size.dotS)
                    } else {
                        GitBadge(letter: mark)
                    }
                }
                if e.dir && !selecting { ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.labelStrong).foregroundStyle(.tertiary) }
            }
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(selecting && selection.contains(e.name) ? .isSelected : [])
        .contextMenu {
            if !e.dir {
                Button { save(e, full) } label: { Label("Save to Files", systemImage: Theme.Icon.save) }
            }
            Button { UIPasteboard.general.string = full } label: { Label("Copy Path", systemImage: Theme.Icon.copy) }
            if canEdit {
                Divider()
                Button { startRename(e) } label: { Label("Rename [Chats View 0d0cba]", systemImage: Theme.Icon.edit) }
                Button { moving = [e.name] } label: { Label("Move", systemImage: Theme.Icon.folder) }
                Button(role: .destructive) { trashing = [e.name] } label: { Label("Move to Trash", systemImage: Theme.Icon.delete) }
            }
        }
        .accessibilityActions {
            if !e.dir { Button("Save to Files") { save(e, full) } }
            if canEdit {
                Button("Rename [Chats View 0d0cba]") { startRename(e) }
                Button("Move") { moving = [e.name] }
                Button("Move to Trash") { trashing = [e.name] }
            }
        }
    }

    private func startRename(_ e: FileEntry) {
        newName = e.name
        renaming = e
    }

    private func save(_ e: FileEntry, _ full: String) {
        transfers.download(FileRef(group: group, machineID: machineID, path: full, cwd: cwd), name: e.name, size: e.size, model: model)
    }

    /// Files from this device into the folder shown; the list reloads after each (the live refresh may lag).
    private func upload(_ urls: [URL], owned: Bool = false) {
        guard !urls.isEmpty else { return }
        transfers.upload(urls, into: path, cwd: cwd, machine: machine, model: model, owned: owned) { _ in
            Task { await load() }
        }
    }

    /// The git letter of a file, or for a folder the letter of anything changed inside it.
    private func gitMark(_ full: String, dir: Bool) -> String? {
        guard let git, let root = git.root, !git.files.isEmpty, full.hasPrefix(root + "/") else { return nil }
        let rel = String(full.dropFirst(root.count + 1))
        if dir {
            return git.files.first { $0.path.hasPrefix(rel + "/") }?.letter
        }
        return git.files.first { $0.path == rel }?.letter
    }

    private func load() async {
        guard let api = model.api(group), !cwd.isEmpty else { return }
        let target = path
        do {
            let l = try await api.listDir(machine: machineID, path: target, cwd: cwd)
            if target == path { listing = l; listed = target; error = nil }
        } catch {
            if target == path { self.error = error.shown }
        }
    }
}
