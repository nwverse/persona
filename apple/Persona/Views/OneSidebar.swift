#if os(iOS)
import SwiftUI

// One sidebar at regular width (#4206): iPad and the open iPhone Duo show Persona's pages, its Pinned chats and the Agents
// list in one column, as ChatGPT's open layout and the web's sidebar do, with the page or the agent's chat beside it. No
// Assistant | Agents switch: the Agents page (`ChatsView`) holds the split, Persona's home is its detail column's first
// page, and these are the column's first rows (`AgentsList`), its header and its foot.

extension EnvironmentValues {
    /// Folds the split's list column away (the one sidebar's header button); nil outside a split.
    @Entry var splitHideList: SplitShowList? = nil
    /// Slides the list column away if it is laid over the page (the open iPhone Duo upright, #4852), after a pick that
    /// changes the page; beside the page it stays. Nil outside a split.
    @Entry var splitHideOverlay: SplitShowList? = nil
    /// The system puts its bars down the side of the window (the open iPhone Duo on its side, `toolbarVerticalEdge`): Persona's
    /// page hands New Chat to the system's side column there (#4854), as the agent's chat hands its actions (#3680).
    @Entry var oneSidebarSideBars = false
}

enum OneSidebarMetrics {
    /// What the window gives up at its top under side bars (#4853): no status bar above the columns there, so the header
    /// and the page's top bar sat at the display's edge, the column's mark under its rounded corner. As ChatGPT's open Duo,
    /// a margin; both columns get it, so the fold and the page's top stay on one line.
    static func topMargin(sideBars: Bool) -> CGFloat { sideBars ? Theme.Space.l : 0 }

    /// The column's one color, behind its rows and its header and over the status bar above it (#4855), on the iPad and
    /// the open Duo in every style: a neutral grey, as ChatGPT's and the iPhone's drawer (`Theme.Shell.drawer`, which the
    /// shell's drawer has in every style), not the system sidebar's grey-blue (`Theme.systemSidebar`, still the Mac's).
    /// The Duo's rows kept the system's and its header the page's: in dark three bands down the column, black, grey-blue,
    /// black (2026-11-12, the owner). Until 2026-11-13 only the glass styles had it, and in the others the rows were the
    /// system sidebar's grey under a white header (the owner, on the phone: one color, as ChatGPT's).
    @MainActor static var columnColor: Color? { Theme.Shell.drawer }

    /// The header's background: the column's, so the rows scroll under it unseen and it reads as part of the column.
    @MainActor static var chromeBackground: Color { columnColor ?? Theme.pageBackground }
}

extension EnvironmentValues {
    /// The one sidebar's Persona rows for the Agents list to draw first (`ChatsView`); nil elsewhere.
    @Entry var oneSidebarLead: OneSidebarLead? = nil
}

/// What the Agents list needs to draw Persona's rows atop itself.
struct OneSidebarLead {
    /// Closes the agent open beside the column.
    var leaveAgent: () -> Void
    /// An agent's page is open beside the column (no Persona row is the selected one then).
    var agentOpen: Bool
}

/// What the detail column beside the one sidebar shows while no agent is open (`SplitNavigation.home`): Messages when its
/// row (under More) is the one picked, Persona's page otherwise. Each has a stack of its own.
struct OneSidebarHome: View {
    @Environment(AppModel.self) private var model
    @Environment(\.splitDetail) private var split
    @Environment(\.splitShowList) private var showList
    @Environment(\.oneSidebarSideBars) private var sideBars

    var body: some View {
        if model.selectedTab == .feishu, LarkHomePreference.shared.shows {
            LarkConversationsView()
                // Its own stack has no system button for the column: with the column away (folded, or slid off after the
                // pick upright, #4852) the page's top corner brings it back, where Persona's page has its own. Not under
                // side bars (the open Duo on its side): the split puts its own Show Sidebar in the side column there, and
                // this one sat under it, two of them, as on Persona's page (`AssistantTopBar`).
                .overlay(alignment: .topLeading) {
                    if split?.listHidden == true, !sideBars, let showList {
                        AssistantTopBar.circle(Theme.Icon.sidebar, label: String(localized: "Show Sidebar"), id: "feishu-sidebar-button") {
                            Haptics.tap()
                            showList.show()
                        }
                        .padding(.leading, Theme.Space.l)
                        .padding(.top, AssistantTopBar.topInset)
                    }
                }
        } else {
            AssistantView()
        }
    }
}

/// The one sidebar's column around the Agents list: Persona's header above, the foot below, no navigation bar.
struct OneSidebarChrome: ViewModifier {
    var enabled: Bool
    var leaveAgent: () -> Void

    func body(content: Content) -> some View {
        if enabled {
            content
                .modifier(OneSidebarColumnColor())
                .toolbarVisibility(.hidden, for: .navigationBar)
                .safeAreaInset(edge: .top, spacing: 0) { OneSidebarHeader() }
                .safeAreaInset(edge: .bottom, spacing: 0) { OneSidebarFoot(leaveAgent: leaveAgent) }
        } else {
            content
        }
    }
}

/// The column's own color where it has one (`OneSidebarMetrics.columnColor`), in place of the system sidebar's.
private struct OneSidebarColumnColor: ViewModifier {
    func body(content: Content) -> some View {
        if let color = OneSidebarMetrics.columnColor {
            content.scrollContentBackground(.hidden).background(color)
        } else {
            content
        }
    }
}

/// Persona's part of the one sidebar: the main conversation and the places (the rarer ones, and Messages, under More),
/// Pinned (the directions' conversations) and All Chats, as every client lists Persona's chats (#4610), and the Agents
/// heading with the list's ⋯ and New Session, the grouping's segments under it. Rows of the Agents list's own `List`,
/// above its filters.
struct OneSidebarSections: View {
    @Environment(AppModel.self) private var model
    @Environment(\.padRouter) private var padRouter
    /// A pick that changes the page slides an overlaid column away (#4852); folds, More and the groupings don't.
    @Environment(\.splitHideOverlay) private var hideOverlay
    /// Closes the agent open beside the column, for a Persona page to show there.
    var leaveAgent: () -> Void
    /// An agent's page is open beside the column.
    var agentOpen: Bool
    /// The Agents list's ⋯ beside its heading; nil: no Agents heading and segments (Persona's own column with separate
    /// sidebars, `OneSidebarPersonaColumn`).
    var menuActions: AgentsListMenu.Actions?
    @Binding var showNew: Bool
    @State private var moreOpen = false
    /// All Chats, over the window as from the drawer (`AutoConversationsView`).
    @State private var allChats: AutoConversationsRoute?
    /// The main conversation asked to end from its long press (`MainChatRowMenu`).
    @State private var ending: Session?

    /// Messages is in the detail column (`OneSidebarHome`): its row is the selected one, none of Persona's.
    private var messagesOpen: Bool { !agentOpen && model.selectedTab == .feishu && LarkHomePreference.shared.shows }
    /// A Persona row is the one in the detail column: no agent and not Messages there.
    private var personaOpen: Bool { !agentOpen && !messagesOpen }

    var body: some View {
        mainRow
        place(String(localized: "Inbox"), Theme.Icon.inbox, id: "sidebar-inbox", count: InboxStore.shared.unreadCount) { show(.inbox) }
        if TodoStore.shared.isSupported {
            place(String(localized: "To-dos"), Theme.Icon.todo, id: "sidebar-todos", count: TodoStore.shared.badgeCount) { show(.todos) }
        }
        place(String(localized: "Scheduled Tasks"), Theme.Icon.schedule, id: "sidebar-routines") { show(.routines) }
        place(String(localized: "Knowledge"), Theme.Icon.knowledge, id: "sidebar-knowledge") { show(.knowledge) }
        moreRow
        if moreOpen {
            if LarkHomePreference.shared.shows {
                place(String(localized: "Messages [Quote Views 4da199]"), Theme.Icon.feishuChats, id: "sidebar-messages",
                      selected: messagesOpen, indent: true, run: openMessages)
            }
            place(String(localized: "Recordings"), Theme.Icon.recording, id: "sidebar-recordings", indent: true) { show(.recordings) } // the page, as the drawer's row (#5517)
            place(String(localized: "Health"), Theme.Icon.health, id: "sidebar-health", indent: true) { show(.health) }
            place(String(localized: "Machines"), Theme.Icon.machines, id: "sidebar-machines", indent: true) { show(.machines) }
        }
        VStack { Divider() }.padding(.horizontal, Theme.Space.m).padding(.vertical, Theme.Space.s).sidebarRow() // a row lays a bare Divider out upright
        pinned
        allChatsRow
        if let menuActions {
            agentsHeading(menuActions)
            AgentsGroupingSegments()
                .padding(.horizontal, Theme.Space.m)
                .padding(.bottom, Theme.Space.s)
                .sidebarRow()
        }
    }

    // MARK: Places

    private var mainRow: some View {
        let key = model.assistantMain
        let selected = personaOpen && model.assistantCurrent != nil && model.assistantCurrent == key
        return row(id: "sidebar-main", selected: selected, action: {
            if let key { open(key) } else { show(.newConversation) }
        }) {
            glyph(Theme.Icon.conversation)
            Text("Main Chat").font(Theme.Font.body).foregroundStyle(Color.primary).lineLimit(1)
            Spacer(minLength: 0)
            if model.badges.assistant > 0 {
                Circle().fill(Theme.accent).scaledFrame(width: Theme.Size.dot, height: Theme.Size.dot)
            }
        }
        .disabled(key == nil && model.assistantConfig == nil) // as the column's: the main one comes with the hub's config
        .contextMenu {
            if let key { MainChatRowMenu(key: key, main: model.assistantConversations.first { $0.key == key }) { ending = $0 } }
        }
        .confirmClose($ending)
    }

    /// More: Messages, Recordings, Health and Machines, folded under one row (#4206: the first screen on its side is
    /// short). Open while the row picked is one of them (Messages), as the prototype's does.
    private var moreRow: some View {
        row(id: "sidebar-more", action: { withAnimation(Theme.Motion.standard) { moreOpen.toggle() } }) {
            glyph(Theme.Icon.more)
            Text(moreOpen ? String(localized: "Collapse") : String(localized: "More")).font(Theme.Font.body).foregroundStyle(Color.primary).lineLimit(1)
            Spacer(minLength: 0)
            FoldChevron(folded: !moreOpen)
        }
        .onChange(of: messagesOpen, initial: true) { _, open in if open { moreOpen = true } }
    }

    private func place(_ title: String, _ symbol: String, id: String, count: Int = 0, selected: Bool = false, indent: Bool = false,
                       run: @escaping () -> Void) -> some View {
        row(id: id, selected: selected, action: run) {
            glyph(symbol)
            Text(title).font(Theme.Font.body).foregroundStyle(Color.primary).lineLimit(1)
            Spacer(minLength: 0)
            if count > 0 { CountBadge(count: count, look: .solid(Theme.needsYou)) }
        }
        .padding(.leading, indent ? Theme.Space.l : 0)
    }

    /// A Persona page beside the column: the agent open there goes, the page comes (`PadRouter`, as the iPad's tabs did).
    private func show(_ page: ShellPage) {
        toPersona()
        padRouter?.show(page)
    }

    /// One of Persona's conversations beside the column.
    private func open(_ key: SessionKey) {
        toPersona()
        model.openAssistantConversation(key)
    }

    /// Persona's page in the detail column: no agent, not Messages. The page is built before it reads what was asked.
    private func toPersona() {
        leaveAgent()
        if model.selectedTab != .assistant { model.selectedTab = .assistant }
        hideOverlay?.show()
    }

    /// Messages in the detail column (`OneSidebarHome`), where a phone has its tab.
    private func openMessages() {
        leaveAgent()
        model.selectedTab = .feishu
        hideOverlay?.show()
    }

    // MARK: Pinned and All Chats

    /// Pinned: the directions' conversations in the hub's order under their names, with their work ("N working · N need
    /// you"), as the drawer and the web list them (#4610; `AssistantSidebarGroups.directions`, the direction tree's counts).
    @ViewBuilder private var pinned: some View {
        let leads = AssistantSidebarGroups.directions(model.assistantConversations, leaders: model.assistantLeaders, main: model.assistantMain)
        if !leads.isEmpty {
            Text("Directions").font(Theme.Font.secondaryMedium).foregroundStyle(.secondary).lineLimit(1)
                .accessibilityAddTraits(.isHeader)
                .padding(.horizontal, Theme.Space.m)
                .padding(.vertical, Theme.Sidebar.rowPadding)
                .scaledFrame(minHeight: Theme.Sidebar.row)
                .frame(maxWidth: .infinity, alignment: .leading)
                .sidebarRow()
                .accessibilityIdentifier("sidebar-pinned-heading")
            ForEach(leads) { s in pinnedRow(s) }
        }
    }

    private func pinnedRow(_ s: Session) -> some View {
        let selected = personaOpen && model.assistantCurrent == s.key
        let title = model.directionNames[s.sessionID]
            ?? (s.title.isEmpty || s.title == "Claude Code" ? String(localized: "New Chat [Quick Actions ea4b29]") : s.title)
        let counts = model.directionCounts[s.sessionID].flatMap { DirectionCountsText.label(working: $0.working, needsYou: $0.needsYou) }
        let name = Text(title).font(Theme.Font.body).foregroundStyle(Color.primary).lineLimit(1)
        return row(id: "sidebar-pinned", selected: selected, action: { open(s.key) }) {
            if let counts {
                let work = Text(counts).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                // The work beside the name while both fit; under it in a narrow column (the open Duo's, a quarter of its
                // screen on its side), where on one line both were cut to "…"; two lines get a little more room above
                // and below, or the rows run into each other.
                ViewThatFits(in: .horizontal) {
                    HStack(spacing: Theme.Sidebar.gap) { name; Spacer(minLength: Theme.Space.xs); work }
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) { name; work }
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.vertical, Theme.Space.xs)
                }
            } else {
                name
                Spacer(minLength: 0)
            }
        }
        .accessibilityLabel([title, counts].compactMap { $0 }.joined(separator: String(localized: ", ", comment: "Separator between parts of an accessibility label")))
        .contextMenu {
            Button { Task { await model.setDirectionLead(s.sessionID, false) } } label: {
                Label("Unpin [Assistant Sidebar b7c40e]", systemImage: Theme.Icon.unpin)
            }
        }
    }

    /// All Chats (#4610): every other conversation, over the window as from the drawer; one picked opens beside the column.
    private var allChatsRow: some View {
        row(id: "sidebar-all-chats", action: { allChats = AutoConversationsRoute(source: nil) }) {
            Text("All Chats").font(Theme.Font.body).foregroundStyle(Color.primary).lineLimit(1)
            Spacer(minLength: Theme.Space.xs)
            ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.labelStrong).foregroundStyle(.tertiary)
        }
        .sheet(item: $allChats) { route in
            AutoConversationsView(source: route.source) { key in
                allChats = nil
                open(key)
            }
        }
    }

    // MARK: Agents

    /// The Agents list's name, with its ⋯ (grouping, display, machines) and New Session, as the web's sidebar heads it.
    private func agentsHeading(_ menuActions: AgentsListMenu.Actions) -> some View {
        HStack(spacing: Theme.Space.m) {
            Text("Agents").font(Theme.Font.secondaryMedium).foregroundStyle(.secondary).lineLimit(1)
            Spacer(minLength: 0)
            AgentsListMenu(actions: menuActions)
            Button { showNew = true } label: {
                ThemedIcon(Theme.Icon.compose).font(Theme.Font.body).foregroundStyle(Color.primary)
            }
            .buttonStyle(.plain)
            .accessibilityLabel("New Session [Chats View 58e21b]")
            .accessibilityIdentifier("sidebar-agents-new")
        }
        .padding(.horizontal, Theme.Space.m)
        .padding(.top, Theme.Space.s)
        .padding(.vertical, Theme.Sidebar.rowPadding)
        .scaledFrame(minHeight: Theme.Sidebar.row)
        .sidebarRow()
        .accessibilityIdentifier("sidebar-agents-heading")
    }

    // MARK: Rows

    private func glyph(_ symbol: String) -> some View {
        // One box for every symbol: a wide one (Knowledge's books) scales down into it instead of pushing its words.
        Theme.Icon.image(symbol).resizable().scaledToFit().fontWeight(.medium).foregroundStyle(Color.primary)
            .scaledFrame(width: Theme.Sidebar.glyph, height: Theme.Sidebar.glyph)
            .scaledFrame(width: Theme.Sidebar.glyphColumn)
    }

    private func row<Label: View>(id: String, selected: Bool = false, action: @escaping () -> Void,
                                  @ViewBuilder label: () -> Label) -> some View {
        Button(action: action) {
            HStack(spacing: Theme.Sidebar.gap) { label() }
                .padding(.horizontal, Theme.Space.m)
                .padding(.vertical, Theme.Sidebar.rowPadding)
                .scaledFrame(minHeight: Theme.Sidebar.row)
                .background {
                    if selected { RoundedRectangle(cornerRadius: Theme.Sidebar.radius, style: .continuous).fill(Theme.Fill.well) }
                }
                .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(selected ? .isSelected : [])
        .accessibilityIdentifier(id)
        .sidebarRow()
    }
}

private extension View {
    /// A row of the Agents list drawn as the sidebar's: no cell, no line.
    func sidebarRow() -> some View {
        listRowInsets(EdgeInsets(top: 0, leading: Theme.Space.s, bottom: 0, trailing: Theme.Space.s))
            .listRowBackground(Color.clear)
            .listRowSeparator(.hidden)
    }
}

/// Persona's column with separate sidebars (#4206, `SidebarMode.separate`): the one sidebar's look, its header, Persona's
/// places, Pinned and All Chats, and its foot, without the Agents list, which is a tab of its own then. In place of the
/// column Persona had before the one sidebar (`AssistantSidebar`), so both modes draw Persona's places alike.
struct OneSidebarPersonaColumn: View {
    var body: some View {
        List {
            OneSidebarSections(leaveAgent: {}, agentOpen: false, menuActions: nil, showNew: .constant(false))
        }
        .listStyle(.plain)
        .modifier(OneSidebarChrome(enabled: true, leaveAgent: {}))
    }
}

/// The one sidebar's header: Persona's mark and name, and the button that folds the column away (ChatGPT's open layout).
/// Several assistants: the current one and the way to another (`AssistantSwitcherHeader`), as the column's header has.
struct OneSidebarHeader: View {
    @Environment(AppModel.self) private var model
    @Environment(\.splitHideList) private var hideList

    var body: some View {
        HStack(spacing: Theme.Space.s) {
            if model.managerRoster?.isMulti == true, let current = model.currentAssistant {
                AssistantSwitcherHeader(manager: current, nameFont: Theme.Font.title, openSettings: { model.showSettings() })
            } else {
                AppGlyph(size: Theme.Sidebar.glyphColumn, scaled: true)
                Text(model.persona.name).font(Theme.Font.title).foregroundStyle(Color.primary).lineLimit(1)
            }
            Spacer(minLength: Theme.Space.s)
            AssistantTopBar.circle(Theme.Icon.sidebar, label: String(localized: "Hide Sidebar"), id: "assistant-sidebar-collapse") {
                Haptics.tap()
                hideList?.show()
            }
        }
        .padding(.horizontal, Theme.Space.l)
        // The page's top bar beside it starts its round buttons this far below the status bar (`AssistantTopBar.topInset`):
        // the fold here and New Chat there on one line (2026-11-11, the owner).
        .padding(.top, AssistantTopBar.topInset)
        .padding(.bottom, Theme.Space.s)
        .background(OneSidebarMetrics.chromeBackground)
    }
}

/// The one sidebar's foot: New Chat in the Theme Color on the left; search and Settings in one capsule on the right, where
/// ChatGPT has its account.
struct OneSidebarFoot: View {
    @Environment(AppModel.self) private var model
    @Environment(\.padRouter) private var padRouter
    @Environment(\.splitHideOverlay) private var hideOverlay
    var leaveAgent: () -> Void
    @State private var fullSearch = false

    var body: some View {
        HStack(spacing: Theme.Space.m) {
            Button {
                toPersona()
                padRouter?.show(.newConversation)
            } label: {
                // Its name while it fits, else its icon alone: at the system's width on the open Duo on its side (a
                // quarter of the screen) the name was cut to "…" (#4206).
                ViewThatFits(in: .horizontal) {
                    newChatLabel.labelStyle(.titleAndIcon)
                    newChatLabel.labelStyle(.iconOnly)
                }
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("sidebar-new")
            .layoutPriority(1) // offered the row's room before the spacer, so the name shows wherever it fits
            Spacer(minLength: Theme.Space.s)
            HStack(spacing: Theme.Space.flush) {
                footButton(Theme.Icon.search, label: String(localized: "Search Chats [Assistant Sidebar 55c9e4]"), id: "sidebar-search") { fullSearch = true }
                footButton(Theme.Icon.settings, label: String(localized: "Settings"), id: "sidebar-settings") { model.shellSettings = true }
                    .overlay(alignment: .topTrailing) { ExtensionSettingsBadge() }
            }
            .surface(interactive: true, in: .capsule)
        }
        .padding(.horizontal, Theme.Space.l)
        .padding(.vertical, Theme.Space.m)
        // No band of its own, as ChatGPT's foot: the buttons float on the column, the rows scroll on behind them.
        .fullScreenCover(isPresented: $fullSearch) {
            AssistantFullSearch { key in
                fullSearch = false
                toPersona()
                model.openAssistantConversation(key)
            }
        }
    }

    private var newChatLabel: some View {
        Label("New Chat [Quick Actions ea4b29]", systemImage: Theme.Icon.compose)
            .font(Theme.Font.bodyStrong)
            .foregroundStyle(.white)
            .lineLimit(1)
            .fixedSize()
            .padding(.horizontal, Theme.Space.xl)
            .scaledFrame(height: Theme.Size.avatarL)
            .background(Theme.accent, in: .capsule)
            .contentShape(.capsule)
    }

    /// Persona's page in the detail column (not an agent, not Messages), for what it is asked next; an overlaid column
    /// slides away (#4852).
    private func toPersona() {
        leaveAgent()
        if model.selectedTab != .assistant { model.selectedTab = .assistant }
        hideOverlay?.show()
    }

    private func footButton(_ symbol: String, label: String, id: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            ThemedIcon(symbol)
                .font(Theme.Font.heading)
                .foregroundStyle(Color.primary)
                .scaledFrame(width: Theme.Size.avatarL, height: Theme.Size.avatarL)
                .contentShape(.circle)
        }
        .buttonStyle(.plain)
        .accessibilityLabel(label)
        .accessibilityIdentifier(id)
    }
}
#endif
