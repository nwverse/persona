import AppKit
import SwiftUI

/// What the window's commands open over the page (⌘K, ⌘N): set from the menu, the menu bar or ⌘K itself.
@Observable final class MacShellState {
    static let shared = MacShellState()
    var quickOpen = false
    var newAgent = false
    /// Knowledge or Machines while picked (they are not iPhone tabs); nil shows the tab's section.
    var section: MacSection? = MacSection.remembered() {
        didSet { MacSection.remember(section) }
    }
    /// A page a plugin put in the sidebar (nav_page, docs/extensions/extensions.md「UI slots」) while it is picked: "<plugin>/<ui>".
    /// Not remembered across launches (the plugin may be gone).
    var pluginPage: String?
    /// A conversation the menus asked to open in its own window (MacWindowOpener does it).
    var chatWindow: SessionKey?
    /// An Agent's terminal the menus asked to open in its own window.
    var terminalWindow: SessionKey?
    /// What ⌘K opens with typed in (DEBUG `-openQuickOpen <query>`).
    var seed = ""
    /// A file ⌘K's Files picked: the window shows it in the file viewer, for the conversation `fileKey`.
    var file: FileRef?
    var fileKey: SessionKey?
    /// What the Agent menu asks of the chat showing `key` (a new value each time): toggle the inspector
    /// (Show Session Info ⌥⌘I), or bring up its own Rename / Clear Context / Close Agent dialogs.
    var chatRequest: ChatRequest?
    /// What the Agents list's menu acts on (its folds, its machine filter, Machines): the shell draws that menu in the
    /// window's toolbar while Agents shows, and View › Collapse All / Expand All use it too. The list stays in the window
    /// behind other sections (`MacPageStack`), holding still there (its `AgentsFeed` is out of sight, #390).
    var agentsList: AgentsListMenu.Actions?
    /// Counts ⇧Tab presses in a composer: the list beside it takes the keys again (`MacListKeys`).
    var focusList = 0
    /// The key window's number, and the chat each window shows (ChatView registers itself): what the Agent menu acts on.
    var keyWindow: Int?
    var chatsByWindow: [Int: SessionKey] = [:]
    /// `openWindow(id: "main")`, from a view that is always there (the menu bar item's label).
    @ObservationIgnored var openMain: (() -> Void)?
    @ObservationIgnored private var keyObservers: [Any] = []

    struct ChatRequest: Equatable {
        enum Kind { case inspector, rename, clear, close, delete, terminal }
        let key: SessionKey
        let kind: Kind
        /// The window it is for: only the chat in that window answers (the same chat can be open in two).
        var window: Int?
        let id = UUID()
    }
}

/// ⌘K: a floating panel near the window's top, like Xcode's Open Quickly / Spotlight. The field keeps the focus;
/// ↑↓ move the selection, ⏎ opens it, Esc or a click outside closes.
struct MacQuickOpenPanel: View {
    @Environment(AppModel.self) private var model
    @State private var query = ""
    @State private var selected = 0
    @FocusState private var focused: Bool
    /// The directory Files searches: the open conversation's, when it has one (read anew as the window's chat and its
    /// session arrive).
    private var place: FilePlace? { Self.filePlace(model) }
    @State private var files: MacFileIndex.State?
    @State private var fileHits: [MacFileFind.Hit] = []

    struct FilePlace: Equatable { var key: SessionKey; var cwd: String }

    var body: some View {
        let groups = MacQuickOpen.sections(query, sessions: model.allSessions, machines: model.machines, inbox: InboxStore.shared.items,
                                           files: fileHits, supportsTodos: TodoStore.shared.isSupported,
                                           showsMessages: LarkHomePreference.shared.shows)
        let hits = groups.flatMap(\.hits)
        VStack(spacing: 0) {
            HStack(spacing: Theme.Space.s) {
                Image(systemName: Theme.Icon.search)
                    .foregroundStyle(.secondary)
                    .accessibilityHidden(true)
                TextField(place == nil ? String(localized: "Quick Open: agents, Persona chats, Inbox…") : String(localized: "Quick Open: agents, files, Inbox…"), text: $query)
                    .textFieldStyle(.plain)
                    .font(Theme.Font.title)
                    .focused($focused)
                    .onSubmit { if hits.indices.contains(selected) { open(hits[selected]) } }
                    .onKeyPress(.downArrow) { selected = MacQuickOpen.move(selected, by: 1, count: hits.count); return .handled }
                    .onKeyPress(.upArrow) { selected = MacQuickOpen.move(selected, by: -1, count: hits.count); return .handled }
                    .onKeyPress(.escape) { close(); return .handled }
                    .accessibilityLabel("Quick Open")
            }
            .padding(Theme.Space.m)
            if !hits.isEmpty {
                Divider()
                ScrollViewReader { proxy in
                    ScrollView {
                        LazyVStack(alignment: .leading, spacing: 0) {
                            ForEach(groups) { g in
                                Text(g.title)
                                    .font(Theme.Font.caption.weight(.semibold))
                                    .foregroundStyle(.secondary)
                                    .padding(.horizontal, Theme.Space.m)
                                    .padding(.top, Theme.Space.s)
                                    .padding(.bottom, Theme.Space.xs)
                                ForEach(g.hits) { hit in
                                    let i = hits.firstIndex(of: hit) ?? 0
                                    QuickOpenRow(hit: hit, selected: i == selected)
                                        .id(hit.id)
                                        .contentShape(Rectangle())
                                        .onTapGesture { open(hit) }
                                        .onHover { if $0 { selected = i } }
                                }
                            }
                        }
                        fileStatus
                            .padding(.horizontal, Theme.Space.m)
                            .padding(.top, Theme.Space.xs)
                        Color.clear.frame(height: Theme.Space.s)
                    }
                    .frame(maxHeight: Theme.Size.macQuickOpenListMax)
                    .fixedSize(horizontal: false, vertical: true)
                    .onChange(of: selected) { _, i in
                        if hits.indices.contains(i) { proxy.scrollTo(hits[i].id) }
                    }
                }
            } else if !query.isEmpty {
                Divider()
                if case .loading = files {
                    fileStatus.padding(Theme.Space.l)
                } else {
                    Text("No Results for “\(query)”")
                        .foregroundStyle(.secondary)
                        .padding(Theme.Space.l)
                }
            }
        }
        .frame(width: Theme.Size.macQuickOpenWidth)
        .background(Theme.Fill.macPanel, in: RoundedRectangle(cornerRadius: Theme.Radius.panel))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.panel).strokeBorder(.separator))
        .shadow(radius: Theme.Space.xl, y: Theme.Space.s)
        .onAppear {
            query = MacShellState.shared.seed
            MacShellState.shared.seed = ""
            focused = true
        }
        .task(id: place) { await loadFiles() }
        .onChange(of: query) {
            selected = 0
            searchFiles()
        }
    }

    /// Files' line under the list while it has something to say: the index on its way, a directory it cannot read.
    @ViewBuilder private var fileStatus: some View {
        if !query.trimmingCharacters(in: .whitespaces).isEmpty {
            switch files {
            case .loading:
                Text("Indexing Files…").font(Theme.Font.caption).foregroundStyle(.secondary)
            case .failed:
                Text("Can't read the files in this folder").font(Theme.Font.caption).foregroundStyle(.secondary)
            default: EmptyView()
            }
        }
    }

    /// The conversation the window shows, with a directory to search; nil elsewhere (no Files then).
    static func filePlace(_ model: AppModel) -> FilePlace? {
        guard let key = MacShellState.shared.focusedChat(model),
              let cwd = model.session(key)?.cwd.trimmingCharacters(in: .whitespaces), !cwd.isEmpty else { return nil }
        return FilePlace(key: key, cwd: cwd)
    }

    private func loadFiles() async {
        guard let place, let api = model.api(place.key.group) else { files = nil; return }
        files = .loading
        let state = await MacFileIndex.shared.load(api, machine: place.key.machineID, cwd: place.cwd)
        guard !Task.isCancelled else { return }
        files = state
        searchFiles()
        #if DEBUG
        // `-openQuickOpenPick YES` (with `-openQuickOpen <query>`): ⏎ on the first file once the list is in, for
        // offscreen screenshots of what it opens.
        if UserDefaults.standard.bool(forKey: "openQuickOpenPick"), let f = fileHits.first {
            open(MacQuickOpen.Hit(id: f.path, title: f.path, subtitle: "", icon: "", target: .file(f.path), score: 0))
        }
        #endif
    }

    private func searchFiles() {
        guard case .ready(let search, _) = files else { fileHits = []; return }
        fileHits = search.search(query)
    }

    private func open(_ hit: MacQuickOpen.Hit) {
        close()
        switch hit.target {
        case .agent(let k), .conversation(let k): MacShellState.shared.open(k, model)
        case .inbox(let id):
            MacShellState.shared.willOpen()
            model.selectedTab = .mail
            MacInboxState.shared.selection = [.item(id)]
        case .file(let rel):
            guard let place else { return }
            MacShellState.shared.fileKey = place.key
            MacShellState.shared.file = FileRef(group: place.key.group, machineID: place.key.machineID,
                                                path: MacFileFind.join(place.cwd, rel), cwd: place.cwd)
        case .section(let s): MacShellState.shared.go(s, model)
        case .newAgent: MacShellState.shared.newAgent = true
        case .newConversation:
            MacShellState.shared.willOpen()
            model.selectedTab = .assistant
            model.assistantCurrent = nil
        }
    }

    private func close() { MacShellState.shared.quickOpen = false }
}

private struct QuickOpenRow: View {
    var hit: MacQuickOpen.Hit
    var selected: Bool

    /// A file's name starts after its folder and the slash.
    private var titleOffset: Int { hit.subtitle.isEmpty ? 0 : hit.subtitle.utf16.count + 1 }

    /// The text with the characters a file search matched drawn strong (and in the accent colour when not selected).
    private func marked(_ text: String, at offset: Int) -> AttributedString {
        guard !hit.marks.isEmpty else { return AttributedString(text) }
        var out = AttributedString()
        for run in MacFileFind.runs(text, offset: offset, matched: hit.marks) {
            var a = AttributedString(run.text)
            if run.hit {
                a.inlinePresentationIntent = .stronglyEmphasized
                if !selected { a.foregroundColor = Theme.accent }
            }
            out += a
        }
        return out
    }

    var body: some View {
        HStack(spacing: Theme.Space.s) {
            Group {
                if let state = hit.state, state != .idle {
                    StatusDot(state: state)
                } else {
                    Image(systemName: hit.icon).foregroundStyle(selected ? .white : .secondary)
                }
            }
            .frame(width: Theme.Size.iconXS)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(marked(hit.title, at: titleOffset)).lineLimit(1)
                if !hit.subtitle.isEmpty {
                    Text(marked(hit.subtitle, at: 0))
                        .font(Theme.Font.caption)
                        .foregroundStyle(selected ? .white : .secondary)
                        .lineLimit(1)
                }
            }
            Spacer(minLength: 0)
        }
        .foregroundStyle(selected ? .white : .primary)
        .padding(.horizontal, Theme.Space.s)
        .padding(.vertical, Theme.Space.xs)
        .background(selected ? Color.accentColor : .clear, in: RoundedRectangle(cornerRadius: Theme.Radius.badge))
        .padding(.horizontal, Theme.Space.xs)
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(selected ? [.isButton, .isSelected] : .isButton)
    }
}

/// ⌘K over the whole window: a faint dim that closes it on a click, the panel near the top.
struct MacQuickOpenLayer: View {
    var body: some View {
        if MacShellState.shared.quickOpen {
            ZStack(alignment: .top) {
                Theme.Fill.macScrim
                    .ignoresSafeArea()
                    .onTapGesture { MacShellState.shared.quickOpen = false }
                    .accessibilityHidden(true)
                MacQuickOpenPanel()
                    .padding(.top, Theme.Size.macQuickOpenTop)
            }
        }
    }
}

extension MacShellState {
    /// Shows a section: an iPhone tab moves the tab, Knowledge / Machines are picked on the Mac side.
    func go(_ s: MacSection, _ model: AppModel) {
        let p = MacSection.pick(s)
        pluginPage = nil
        section = p.override
        if let tab = p.tab { model.selectedTab = tab }
    }
}

extension MacShellState {
    /// The chat the menus act on: the key window's; with no key window (the app in the background, the menu bar's
    /// menu open), the last one shown. A key window without a chat (Knowledge, a sheet) has none.
    nonisolated static func focusedChat(keyWindow: Int?, chats: [Int: SessionKey], fallback: SessionKey?) -> SessionKey? {
        guard let keyWindow else { return fallback }
        return chats[keyWindow]
    }

    func focusedChat(_ model: AppModel) -> SessionKey? {
        Self.focusedChat(keyWindow: keyWindow, chats: chatsByWindow, fallback: model.visibleSession)
    }

    /// Follows the key window (the menus are rebuilt when it changes). Started once at launch.
    func watchKeyWindow() {
        guard keyObservers.isEmpty else { return }
        let update: (Notification) -> Void = { _ in
            MainActor.assumeIsolated { MacShellState.shared.keyWindow = NSApp.keyWindow?.windowNumber }
        }
        for name in [NSWindow.didBecomeKeyNotification, NSWindow.didResignKeyNotification] {
            keyObservers.append(NotificationCenter.default.addObserver(forName: name, object: nil, queue: .main, using: update))
        }
    }

    /// The main window to the front, opened again if it was closed (the Dock menu, the menu bar list).
    func showMain() {
        NSApp.activate()
        if let w = NSApp.windows.first(where: { $0.identifier?.rawValue.hasPrefix("main") == true }) {
            if w.isMiniaturized { w.deminiaturize(nil) }
            w.makeKeyAndOrderFront(nil)
        } else {
            openMain?()
        }
    }
}

extension MacShellState {
    /// Something is about to open a chat or a tab's page: Knowledge / Machines make way. Called directly because when the tab
    /// and the open chat are already the ones asked for, nothing changes for MacShell's onChange to see.
    func willOpen() { section = nil }

    /// `AppModel.open` for the Mac: the section comes to the front too.
    func open(_ key: SessionKey, _ model: AppModel, latestAnswer: Bool = false) {
        willOpen()
        model.open(key, latestAnswer: latestAnswer)
    }
}
