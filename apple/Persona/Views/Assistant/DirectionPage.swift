import SwiftUI

// The page of everything under one direction, first version (#11516, docs/design/DESIGN_SYSTEM.md 3.4 "A direction's
// page"): its name, In Progress (the held tree's to-dos and their Agents, drawn as the sidebar draws them, live) and
// Added Here (the sessions placed in it, from `GET /api/v1/assistant/directions/{id}`). Opened from a direction's All ›
// in the sidebar and from its leader conversation's title menu, pushed one level over that conversation. Drawn only
// while the hub files sessions (`DirectionFilings.shows`). Notes, progress and ended tasks come in the next step.

/// The page's value on the Assistant page's path.
nonisolated struct DirectionPageRoute: Hashable, Sendable {
    var id: String
}

/// Asks the Assistant page to open a direction's page (`AssistantView.openDirectionPage`): from the sidebar, its
/// leader conversation becomes the current place with the page already over it; from the title menu it is pushed.
@MainActor @Observable final class DirectionPages {
    static let shared = DirectionPages()

    nonisolated struct Ask: Equatable, Sendable {
        var id: String
        var lead: SessionKey
        var fromSidebar: Bool
        var serial: Int
    }

    private(set) var ask: Ask?
    @ObservationIgnored private var serial = 0

    func open(_ id: String, over lead: SessionKey, fromSidebar: Bool) {
        serial += 1
        ask = Ask(id: id, lead: lead, fromSidebar: fromSidebar, serial: serial)
    }

    /// The ask with this serial, once: the page that takes it opens it.
    func take(_ serial: Int) -> Ask? {
        guard let a = ask, a.serial == serial else { return nil }
        ask = nil
        return a
    }
}

/// What the page works out, apart from its views (tested in `DirectionPageTests`).
enum DirectionPageLogic {
    /// Added Here shows this many, then Show All N (docs/design/DESIGN_SYSTEM.md 3.4, a long list on a detail page).
    static let firstCount = 5

    /// The rows Added Here shows: the first five unless all are asked for.
    static func shown<T>(_ rows: [T], all: Bool) -> [T] { all ? rows : Array(rows.prefix(firstCount)) }

    /// "N need you" over In Progress: the sidebar row's words for the same tree (`DirectionTree.Counts.label`).
    static func needsYou(_ tree: DirectionTree?, _ id: String) -> String? { tree?.leader(id)?.counts.label }

    /// A filed session's title: the one it was filed with, else its row's, else Agent.
    static func title(_ s: DirectionTree.FiledSession, session: Session?) -> String {
        if !s.title.isEmpty { return s.title }
        if let t = session?.displayTitle, !t.isEmpty { return t }
        return String(localized: "Agent")
    }

    /// Where a filed row says it stands (`docs/assistant/unified-entry.md` "Where a session stands").
    enum Standing: Equatable {
        /// The session's own state (`offline`: Not Running, stopped on a connected computer).
        case state(AgentState)
        /// Its computer is not connected: never Not Running.
        case computerOffline
        /// Its computer has not given its list: nothing is claimed.
        case notKnownYet
        /// Its computer's list lacks it: archived, quiet or gone can't be told apart, so only when it was added.
        case added(Date)
        case computerRemoved
        /// Read earlier in this run; the page can't read now.
        case offline(since: Date)
    }

    /// `live`: the session's state as the app lists it, which follows it after the read. A value this app does not
    /// know draws no word (nil).
    static func standing(_ s: DirectionTree.FiledSession, live: AgentState?) -> Standing? {
        switch s.standing {
        case .computerOffline: return .computerOffline
        case .listUnknown: return .notKnownYet
        case .notListed: return s.at.map { .added($0) }
        case .computerRemoved: return .computerRemoved
        case nil:
            guard let own = s.ownState else { return nil }
            return .state(live ?? own)
        }
    }

    static func words(_ standing: Standing) -> String {
        switch standing {
        case .state(let st): st.label
        case .computerOffline: String(localized: "Offline [Filed Session]")
        case .notKnownYet: String(localized: "Not known yet [Filed Session]")
        case .added(let at): String(localized: "Added \(at.formatted(.relative(presentation: .named))) [Filed Session]")
        case .computerRemoved: String(localized: "Computer removed [Filed Session]")
        case .offline(let since): String(localized: "Offline · \(Fmt.relative(since))")
        }
    }

    /// The dot beside it: the state's, or a quiet one where no state is known.
    static func dot(_ standing: Standing?) -> AgentState {
        switch standing {
        case .state(let st): st
        case .computerOffline, .computerRemoved: .offline
        default: .unknown
        }
    }

    /// The tone of its words.
    static func tone(_ standing: Standing?) -> Color {
        if case .state(let st) = standing { return st.tone.text }
        return .secondary
    }
}

/// The page's read of its direction (`GET /api/v1/assistant/directions/{id}`): on open, on reconnect and after a move
/// made on this device (P7 / P9), never on a `directions` push. What this run read of it is kept, so it still draws
/// (marked Offline · time) when a later read fails.
@MainActor @Observable final class DirectionPageModel {
    nonisolated enum Status: Equatable, Sendable {
        case reading
        case read
        /// The read failed; what was read before in this run stays.
        case failed
        /// 404 `direction_not_found`: the page is no longer available.
        case gone
    }

    let id: String
    private(set) var read: DirectionRead?
    private(set) var readAt: Date?
    private(set) var status: Status = .reading
    /// Added Here shows them all.
    var showsAll = false
    /// The hello (`AppModel.hubHellos`) this page last read for.
    @ObservationIgnored var readFor: Int?

    /// What this run read, by direction (memory only).
    private static var kept: [String: (read: DirectionRead, at: Date)] = [:]

    init(id: String) {
        self.id = id
        if let k = Self.kept[id] { read = k.read; readAt = k.at }
    }

    /// Reads the direction. A read already out is not doubled: this one waits for nothing and lands last.
    func load(_ api: APIClient?) async {
        guard let api else { status = .failed; return }
        if status != .read { status = .reading }
        do {
            let r = try await api.direction(id)
            let now = Date.now
            read = r
            readAt = now
            Self.kept[id] = (r, now)
            status = .read
        } catch let e as APIError where e.status == 404 && e.code == "direction_not_found" {
            status = .gone
        } catch where DirectionRoutes.missing(error) {
            DirectionFilings.shared.routesAnsweredMissing()
            status = .failed
        } catch is CancellationError {
            if status == .reading { status = read == nil ? .failed : .read }
        } catch {
            status = .failed
        }
    }

    #if DEBUG
    /// Tests start each page without what an earlier one read.
    static func forgetForTesting() { kept = [:] }
    #endif
}

/// The page (#11516). The name and In Progress come from the held tree, so they draw before the read returns; Added
/// Here waits for the read, and says so while it is out or when it fails, never as empty or as "no tasks".
struct DirectionPage: View {
    @Environment(AppModel.self) private var model
    /// Agents' chats go on this page's path, over it (`conversationAgentPush`).
    @Environment(\.pushInboxChat) private var pushChat
    let id: String
    @State private var page: DirectionPageModel
    @State private var shownTodo: TodoDetailRoute?
    @State private var openTasks: Set<String> = []
    @State private var openAgents: Set<String> = []

    init(id: String) {
        self.id = id
        _page = State(initialValue: DirectionPageModel(id: id))
    }

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("DirectionPage")
        #endif
        let direction = model.directionTree?.leader(id)
        let tasks = direction?.tasks ?? []
        Group {
            if page.status == .gone {
                EmptyStateView(symbol: Theme.Icon.info, title: String(localized: "Not Available [Direction Page]"),
                               message: String(localized: "This page is no longer available. [Direction Page]"))
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if direction == nil && page.read == nil {
                // Neither the tree nor a read has it yet: the page is being read.
                LoadableContent(state: Loadable<Void>.loading, retry: nil) { _ in EmptyView() }
            } else {
                List {
                    if page.read?.endedAt != nil {
                        Section {
                            Label(String(localized: "This has ended. You can look, but nothing here can be moved. [Direction Page]"),
                                  systemImage: Theme.Icon.info)
                                .font(Theme.Font.secondary).foregroundStyle(.secondary)
                                .themedRow()
                                .accessibilityIdentifier("direction-page-ended")
                        }
                    }
                    if !tasks.isEmpty {
                        Section {
                            ForEach(tasks) { task in
                                DirectionTaskRow(task: task, inset: 0, unfolded: openTasks.contains(task.id),
                                                 toggle: { openTasks.formSymmetricDifference([task.id]) },
                                                 open: { shownTodo = TodoDetailRoute(id: task.id) })
                                    .themedRow()
                                if openTasks.contains(task.id) {
                                    ForEach(task.agentLines(opened: openAgents)) { line in
                                        DirectionAgentRow(line: line, inset: 0, unfolded: openAgents.contains(line.id),
                                                          toggle: { openAgents.formSymmetricDifference([line.id]) }) { key in
                                            openTodoAgentChat(key, in: model, push: conversationAgentPush(pushChat))
                                        }
                                        .themedRow()
                                    }
                                }
                            }
                        } header: {
                            HStack {
                                Text("In Progress [Direction Page]")
                                Spacer(minLength: Theme.Space.s)
                                if let direction { DirectionCountsLine(direction: direction) }
                            }
                            .accessibilityElement(children: .combine)
                            .accessibilityIdentifier("direction-page-in-progress")
                        }
                    }
                    DirectionAddedSection(page: page, direction: id, hasTasks: !tasks.isEmpty, pushChat: pushChat)
                }
                .themedPage()
            }
        }
        .pageTitle(DirectionPageTitle.of(id, read: page.read, in: model))
        .sheet(item: $shownTodo) { r in
            NavigationStack {
                TodoDetailView(todoID: r.id).sheetDone { shownTodo = nil }
            }
        }
        // On open and on every hello (a reconnect), and after this device's moves (`DirectionFilings.page`); never on
        // the tree's pushes, which In Progress follows by itself.
        .task(id: model.hubHellos) {
            // Coming back from a chat pushed over it is not opening it again (P9): read once per hello.
            guard page.readFor != model.hubHellos else { return }
            page.readFor = model.hubHellos
            await page.load(model.api)
        }
        .onAppear {
            DirectionFilings.shared.page(id) { [weak page = page, weak model = model] in await page?.load(model?.api) }
        }
        .onDisappear { DirectionFilings.shared.page(id, read: nil) }
        .accessibilityIdentifier("direction-page")
    }
}

/// The page's title: the direction's name as its sidebar row says it; from the read once it is no longer in the tree.
enum DirectionPageTitle {
    @MainActor static func of(_ id: String, read: DirectionRead?, in model: AppModel) -> String {
        if let d = model.directionTree?.leader(id) { return model.directionTitle(d) }
        if let name = read?.name, !name.isEmpty { return name }
        return model.directionTitle(DirectionTree.Direction(id: id))
    }
}

/// Added Here: the sessions placed in the direction, by who placed them, the first five then Show All N; each says
/// where it stands, opens on a tap and has Move To… in its menu (not once the direction has ended). Its own view, so
/// the read and the moves out redraw only it (P5).
private struct DirectionAddedSection: View {
    @Environment(AppModel.self) private var model
    let page: DirectionPageModel
    let direction: String
    /// In Progress has rows: a read with nothing added then says nothing of its own.
    let hasTasks: Bool
    let pushChat: ((InboxChatRoute) -> Void)?

    var body: some View {
        let filings = DirectionFilings.shared
        let read = page.read
        let rows = filings.rows(read?.sessions ?? [], in: direction, group: model.hub?.group ?? "", live: false)
        let ended = read?.endedAt != nil
        let stale = page.status == .failed && read != nil
        if read == nil {
            // Never read in this run: reading, or Couldn't read with Retry. Never drawn as empty.
            Section {
                if page.status == .failed {
                    retryRow(String(localized: "Couldn't read [Direction Page]"))
                } else {
                    HStack { Spacer(); ProgressView(); Spacer() }
                        .padding(.vertical, Theme.Space.s)
                        .themedRow()
                        .accessibilityIdentifier("direction-page-reading")
                }
            } header: { header(nil) }
        } else if read?.sessionsUnavailable == true {
            Section {
                retryRow(String(localized: "Couldn't read what was added here. [Direction Page]"))
            } header: { header(nil) }
        } else if !rows.isEmpty {
            Section {
                ForEach(DirectionPageLogic.shown(rows, all: page.showsAll)) { row in
                    DirectionAddedRow(row: row, direction: direction, ended: ended, stale: stale ? page.readAt : nil,
                                      pushChat: pushChat)
                        .themedRow()
                }
                if rows.count > DirectionPageLogic.firstCount {
                    Button {
                        withAnimation(Theme.Motion.standard) { page.showsAll.toggle() }
                    } label: {
                        Text(page.showsAll ? String(localized: "Show First \(DirectionPageLogic.firstCount) [Direction Page]")
                                           : String(localized: "Show All \(rows.count)"))
                            .font(Theme.Font.secondaryMedium)
                            .lineLimit(1)
                    }
                    .themedRow()
                    .accessibilityIdentifier("direction-page-more")
                }
            } header: { header(rows.count) }
        } else if !hasTasks {
            // Read, and nothing is under it at all: what the page is for, and how something gets here.
            EmptyStateView(symbol: Theme.Icon.list, title: String(localized: "Nothing Here Yet [Direction Page]"),
                           message: String(localized: "Use Move To… on a chat or an Agent to add it here. [Direction Page]"))
                .frame(maxWidth: .infinity)
                .listRowBackground(Color.clear)
                .accessibilityIdentifier("direction-page-empty")
        }
    }

    private func header(_ count: Int?) -> some View {
        SectionHeader(title: String(localized: "Added Here [Direction Page]"), count: count, tone: StatusPalette.idle)
            .accessibilityIdentifier("direction-page-added")
    }

    private func retryRow(_ words: String) -> some View {
        HStack(spacing: Theme.Space.m) {
            Label(words, systemImage: Theme.Icon.error)
                .font(Theme.Font.secondary).foregroundStyle(.secondary)
                .accessibilityIdentifier("direction-page-unread")
            Spacer(minLength: Theme.Space.s)
            Button { Task { await page.load(model.api) } } label: { Text("Retry").lineLimit(1) }
                .themedButton()
                .accessibilityIdentifier("direction-page-retry")
        }
        .themedRow()
    }
}

/// One session in Added Here: its dot, title and who placed it, and where it stands; a tap opens it.
private struct DirectionAddedRow: View {
    @Environment(AppModel.self) private var model
    let row: DirectionFilings.Row
    let direction: String
    let ended: Bool
    /// When the rows were read, while the page can't read now: they say Offline · that time.
    let stale: Date?
    let pushChat: ((InboxChatRoute) -> Void)?

    var body: some View {
        let session = model.session(row.key)
        let title = DirectionPageLogic.title(row.session, session: session)
        let standing: DirectionPageLogic.Standing? = stale.map { .offline(since: $0) }
            ?? DirectionPageLogic.standing(row.session, live: session?.state)
        let word = row.moving ? String(localized: "Moving… [Move To]") : standing.map(DirectionPageLogic.words)
        let source = row.session.addedByOwner ? String(localized: "Added by you [Filed Session]")
                                              : String(localized: "Opened from this chat [Filed Session]")
        Button { openTodoAgentChat(row.key, in: model, push: conversationAgentPush(pushChat)) } label: {
            HStack(spacing: Theme.Space.s) {
                StatusDot(state: DirectionPageLogic.dot(standing))
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(title).font(Theme.Font.secondary).foregroundStyle(Color.primary).lineLimit(1)
                    Text(source).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer(minLength: Theme.Space.xs)
                if let word {
                    Text(word).font(Theme.Font.caption)
                        .foregroundStyle(row.moving ? Color.secondary : DirectionPageLogic.tone(standing)).lineLimit(1)
                }
            }
            .padding(.vertical, Theme.Sidebar.rowPadding)
            .scaledFrame(minHeight: Theme.Sidebar.row)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel([title, source, word].compactMap { $0 }
            .joined(separator: String(localized: ", ", comment: "Separator between the parts of a VoiceOver label")))
        .accessibilityAddTraits(.isButton)
        .accessibilityIdentifier("direction-page-filed")
        .contextMenu {
            if !ended {
                MoveToMenu(key: row.key, title: title, state: session?.state ?? row.session.ownState ?? .unknown,
                           from: direction)
            }
        }
    }
}

/// Move To… in a session's long-press menu (#11516): the current directions by name in the tree's order, then On
/// Its Own. Drawn only where `DirectionFilings.offers` offers it, read in this view's own body (P5). `from`: the
/// direction the menu's row is drawn under, when it is one (the page).
struct MoveToMenu: View {
    @Environment(AppModel.self) private var model
    let key: SessionKey
    var title: String
    var state: AgentState
    var from: String? = nil

    var body: some View {
        let filings = DirectionFilings.shared
        let choices = filings.offers(key, in: model)
        if !choices.isEmpty {
            MoveToChoices(choices: choices) { choice in
                filings.move(key, title: title, state: state, to: choice,
                             from: from ?? filings.placed(key, in: model.directionTree))
            }
        }
    }
}

/// The submenu itself, for a menu that already holds its choices (the Agent menus' facts).
struct MoveToChoices: View {
    let choices: [DirectionFilings.Choice]
    let pick: (DirectionFilings.Choice?) -> Void

    var body: some View {
        Menu {
            ForEach(choices) { c in
                Button { pick(c) } label: { Text(c.name).lineLimit(1) }
            }
            Divider()
            Button { pick(nil) } label: { Text("On Its Own [Move To]").lineLimit(1) }
                .accessibilityIdentifier("move-to-own")
        } label: {
            Label(String(localized: "Move To… [Move To]"), systemImage: Theme.Icon.moveTo).lineLimit(1)
        }
        .accessibilityIdentifier("move-to")
    }
}

/// The leader conversation's title menu: its first item opens the page (#11516). Its own view, so the gate and the
/// tree are read here, not by the bar.
struct DirectionPageMenuItem: View {
    @Environment(AppModel.self) private var model
    let key: SessionKey?

    var body: some View {
        if let key, DirectionFilings.shared.shows(model), model.directionTree?.leader(key.sessionID) != nil {
            Button { DirectionPages.shared.open(key.sessionID, over: key, fromSidebar: false) } label: {
                Label(String(localized: "Tasks and Chats [Direction Page]"), systemImage: Theme.Icon.list).lineLimit(1)
            }
            .accessibilityIdentifier("assistant-direction-page")
        }
    }
}

/// The Unpin confirmation's words (#6214's), and while the hub files sessions one more sentence of its own: what was
/// added to the direction stays, and comes back if the same chat is pinned again (#11516,
/// docs/assistant/unified-entry.md "The direction tree"). #11394's sentence about its open tasks is left as it is.
enum UnpinWords {
    @MainActor static func message(_ base: String, model: AppModel) -> String {
        guard DirectionFilings.shared.shows(model) else { return base }
        return base + "\n" + String(localized: "What you added to it stays. [Move To]")
    }
}
