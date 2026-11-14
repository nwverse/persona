import OSLog
import SwiftUI

/// A list and the pages it opens (Agents, Inbox, Settings), laid out for the width there is (docs/apple/ios.md「iPhone Duo」).
///
/// Compact width — every iPhone, and iPhone Duo closed — is one stack with the list at its root, exactly as before.
/// Regular width — iPhone Duo open — keeps the list as a sidebar column and opens its pages in the detail column,
/// the way Mail shows a list and a message side by side; the split view also keeps its columns clear of the fold
/// when the device is partly folded. Both share `path`, so folding or unfolding keeps the page that was open.
///
/// In the split, the list's own links (`NavigationLink(value:)`) replace what the detail column shows; code in the list
/// should do the same (`path = NavigationPath([value])`, which in the single stack is the same as appending).
struct SplitNavigation<Sidebar: View, Empty: View, Destinations: ViewModifier>: View {
    @Environment(\.horizontalSizeClass) private var widthClass
    /// Whether the list column shows (its button in the toolbar closes it).
    @State private var columns = SplitColumns.shown
    @Binding var path: NavigationPath
    /// iOS: whether the list column is shown, remembered under this key (`SplitListMemory`); nil: nothing kept.
    var listKey: String? = nil
    /// The pages the list opens (`navigationDestination`s), registered in both layouts.
    var destinations: Destinations
    /// iOS beside the one sidebar (#4206): what the detail column shows while nothing is open, in place of the stack.
    /// Persona's page has a stack of its own (its pushed pages), which can't be inside this one; an agent opened takes
    /// the column back to the stack.
    var home: AnyView? = nil
    @ViewBuilder var sidebar: Sidebar
    /// The detail column before anything is open.
    @ViewBuilder var empty: Empty
    #if os(iOS)
    /// The list column lies over the page instead of beside it: where the page beside it would be narrower than
    /// `Theme.Size.splitPageMin` (the open iPhone Duo and an iPad mini, upright, #4852). Set from the split's width
    /// (`SplitListMemory`); nil before it is laid out.
    @State private var overlays: Bool?
    /// The system's bars down the side beside the one sidebar (the open Duo on its side): nothing is above the columns.
    @Environment(\.oneSidebarSideBars) private var sideBars
    /// Each column's own top safe area, outside the margin it is given (the page's has the system's bar room under side
    /// bars, the list's does not).
    @State private var listSafeTop: CGFloat = 0
    @State private var pageSafeTop: CGFloat = 0
    /// The list column as UIKit has it (`SplitColumnControl`): a finger moving it, the split's size as a turn lays it out.
    @State private var columnState = SplitColumnState()
    #endif

    init(path: Binding<NavigationPath>, listKey: String? = nil, destinations: Destinations, home: AnyView? = nil,
         @ViewBuilder sidebar: () -> Sidebar, @ViewBuilder empty: () -> Empty) {
        _path = path
        self.listKey = listKey
        self.destinations = destinations
        self.home = home
        self.sidebar = sidebar()
        self.empty = empty()
        // A list closed last time starts closed (#2624). Closed only once it appeared, the split put it back out as it
        // first laid itself out, and the list came back on every launch.
        _columns = State(initialValue: SplitListMemory.startsClosed(listKey) ? .detailOnly : SplitColumns.shown)
    }

    var body: some View {
        layout.environment(\.splitPush, SplitPush { [path = $path] value in path.wrappedValue.append(value) })
    }

    @ViewBuilder private var layout: some View {
        if widthClass == .regular {
            #if os(macOS)
            // The list in the content column (flush; the sidebar column floats as a glass card on macOS 26 and stays
            // empty). A value link in the content column replaces the detail column's stack, as one in the sidebar did.
            NavigationSplitView(columnVisibility: $columns) {
                SplitColumns.noSidebar
            } content: {
                // The look the list had in the sidebar column (no row lines, footers as before) on the window's white,
                // as the Persona and Agents lists beside the sections are; lists that pick a style keep it. Inside the
                // destinations, so the style stays in this column whichever environment the pushed pages are built in.
                sidebar
                    .listStyle(.sidebar)
                    .scrollContentBackground(.hidden)
                    .modifier(destinations)
                    .modifier(SidebarWidth())
            } detail: {
                NavigationStack(path: $path) {
                    empty.modifier(destinations)
                }
                // The page keeps a width it can be read at; narrowing the window narrows the list first (#1200).
                .navigationSplitViewColumnWidth(min: Theme.Size.macDetailMin, ideal: Theme.Size.macWindowWidth)
                .onGeometryChange(for: Bool.self) { $0.size.width < Theme.Size.macSplitSquished } action: { squished in
                    SplitColumns.log.notice("detail squished: \(squished) (columns \(String(describing: columns), privacy: .public), depth \(path.count))")
                }
            }
            .navigationSplitViewStyle(.balanced)
            .environment(\.splitDetail, SplitDetail(isFirstPage: path.count <= 1))
            // The detail column once came out a few points wide, the chat squeezed into it (2026-11-02, not reproduced):
            // what the columns and the stack did, for the next time (`log show --predicate 'category == "split"'`).
            .onChange(of: columns) { old, new in
                SplitColumns.log.notice("columns \(String(describing: old), privacy: .public) → \(String(describing: new), privacy: .public)")
            }
            .onChange(of: path.count) { old, new in SplitColumns.log.notice("depth \(old) → \(new)") }
            #else
            NavigationSplitView(columnVisibility: $columns) {
                // No stack of its own: a value link in the sidebar column replaces the detail column's stack. A sidebar
                // stack that refused its pushes (a path binding that always read empty) first slid the list away, then
                // back: the sidebar folded and unfolded on every tap.
                sidebar.modifier(destinations)
                    #if os(iOS)
                    .safeAreaPadding(.top, topPad(listSafeTop))
                    .onGeometryChange(for: CGFloat.self) { $0.safeAreaInsets.top } action: { listSafeTop = $0 }
                    #endif
                    // Last, on the column's root: under the top margin's padding the column's width no longer reached the
                    // split (on the Duo on its side).
                    .modifier(SidebarWidth())
            } detail: {
                if let home {
                    // Both kept in one root that never changes kind: when the column's root went from the stack to the
                    // home, the split kept the stack's controller and the chat that had been open stayed on screen (#4206).
                    // The stack stays, at its root and out of sight, under the home while nothing is open.
                    ZStack {
                        NavigationStack(path: $path) {
                            empty.modifier(destinations)
                        }
                        .opacity(path.isEmpty ? 0 : 1)
                        .allowsHitTesting(!path.isEmpty)
                        .accessibilityHidden(path.isEmpty)
                        if path.isEmpty { home }
                    }
                    #if os(iOS)
                    .safeAreaPadding(.top, topPad(pageSafeTop))
                    .onGeometryChange(for: CGFloat.self) { $0.safeAreaInsets.top } action: { pageSafeTop = $0 }
                    .background { columnControl }
                    .modifier(SplitPageMin())
                    #endif
                } else {
                    NavigationStack(path: $path) {
                        empty.modifier(destinations)
                    }
                    #if os(iOS)
                    .background { columnControl }
                    .modifier(SplitPageMin())
                    #endif
                }
            }
            .navigationSplitViewStyle(.balanced)
            #if os(iOS)
            // An agent picked, or anything else that changes the page: a list laid over the page goes (#4852).
            .onChange(of: path) { hideOverlay() }
            #endif
            .modifier(SidebarTopFill(shown: columns != .detailOnly, color: topFillColor))
            #if os(iOS)
            .modifier(SplitListMemory(columns: $columns, key: listKey, overlays: $overlays, state: columnState))
            #else
            .modifier(SplitListMemory(columns: $columns, key: listKey))
            #endif
            // On the whole split, not the detail stack: its pages are built from the destinations the sidebar
            // registered, in the sidebar's environment.
            .environment(\.splitDetail, SplitDetail(isFirstPage: path.count <= 1, listHidden: columns == .detailOnly))
            .environment(\.splitShowList, SplitShowList { [columns = $columns] in
                withAnimation(Theme.Motion.layout) { columns.wrappedValue = SplitColumns.shown }
            })
            #if os(iOS)
            .environment(\.splitHideList, SplitShowList { [columns = $columns] in
                withAnimation(Theme.Motion.layout) { columns.wrappedValue = .detailOnly }
            })
            .environment(\.splitHideOverlay, SplitShowList { hideOverlay() })
            #endif
            #endif
        } else {
            NavigationStack(path: $path) {
                sidebar.modifier(destinations)
            }
        }
    }

    /// The one sidebar's column has a color of its own (#4855): the strip over it matches; nil, the system's.
    private var topFillColor: Color? {
        #if os(iOS)
        home == nil ? nil : OneSidebarMetrics.columnColor
        #else
        nil
        #endif
    }

    #if os(iOS)
    /// Beside the one sidebar under side bars both columns start lower, together (#4853): at least the margin, and as low
    /// as the lower of their own tops (the system gives the page room for its bar there), so the fold and the page's face
    /// stay on one line. Set inside each column, as the split's columns do not take a safe area given around it.
    private func topPad(_ own: CGFloat) -> CGFloat {
        let margin = home == nil ? 0 : OneSidebarMetrics.topMargin(sideBars: sideBars)
        guard margin > 0 else { return 0 }
        return max(0, max(margin, listSafeTop, pageSafeTop) - own)
    }
    #endif

    #if os(iOS)
    /// The list column's control from UIKit (`SplitColumnControl`): slid by a finger where the list's shown state is kept
    /// (`listKey`). In the detail column, which is there while the list is away.
    private var columnControl: some View {
        SplitColumnControl(drags: listKey != nil, state: columnState) { shown in
            var t = Transaction()
            t.disablesAnimations = true
            withTransaction(t) { columns = shown ? SplitColumns.shown : .detailOnly }
            SplitListMemory.keep(listKey, shown: shown, state: columnState)
        }
    }

    /// A pick in a list laid over the page (the open Duo upright) changed what the page shows: the list slides away, as
    /// the system's sidebar does on an iPad in portrait. Beside the page (on its side, the iPad) it stays.
    private func hideOverlay() {
        guard overlays == true, columns != .detailOnly else { return }
        withAnimation(Theme.Motion.layout) { columns = .detailOnly }
    }
    #endif
}

/// What a split shows when its list is out: the list beside the page. On the Mac the list is the content column of a
/// three-column split whose sidebar column stays empty (`noSidebar`): macOS 26 draws the sidebar column as a glass card
/// floating off the window's edges, the window's backdrop showing round it (2026-11-02 user: like floating on a backdrop, with a gap).
/// The shell's sections column is the window's sidebar there.
enum SplitColumns {
    #if os(macOS)
    static let shown = NavigationSplitViewVisibility.doubleColumn

    /// The empty sidebar column, and no system button to bring it out.
    static var noSidebar: some View { Color.clear.toolbar(removing: .sidebarToggle) }

    static let log = Logger(subsystem: "persona", category: "split")
    #else
    static let shown = NavigationSplitViewVisibility.all
    static let log = Logger(subsystem: "persona", category: "split")

    #if os(iOS)
    /// The list column's one width here (`SidebarWidth`).
    @MainActor static var listWidth: CGFloat {
        UIDevice.current.userInterfaceIdiom == .pad ? Theme.Size.sidebar : Theme.Size.sidebarPhone
    }

    /// In a split this wide the page beside the list column keeps its least width (`SplitPageMin`): the column lies beside
    /// it; else over it.
    @MainActor static func fitsBeside(width: CGFloat) -> Bool { width - listWidth >= Theme.Size.splitPageMin }
    #endif
    #endif
}

/// The list column's width: one width that never changes while the column shows or slides (#4206). Hidden and shown, the
/// column only slides in and out, as Feishu's and ChatGPT's do; while it followed a dragged handle (#2624) every row was
/// laid out again on every frame. iPad: `Theme.Size.sidebar`, wider than the system's sidebar, which wrapped the agents'
/// card titles. iPhone Duo open: `Theme.Size.sidebarPhone` (ChatGPT's width there; the system's was about 199 pt upright),
/// which leaves the page most of the 951 pt on its side. A Mac window keeps its range (the Mac app is frozen).
struct SidebarWidth: ViewModifier {
    func body(content: Content) -> some View {
        switch UIDevice.current.userInterfaceIdiom {
        case .mac:
            content.navigationSplitViewColumnWidth(min: Theme.Size.sidebarMin, ideal: Theme.Size.sidebar, max: Theme.Size.sidebarMax)
                .background(edge)
        case .pad: content.navigationSplitViewColumnWidth(Theme.Size.sidebar).background(edge)
        case .phone: content.navigationSplitViewColumnWidth(Theme.Size.sidebarPhone).background(edge)
        default: content
        }
    }

    /// Where the column ends (`SidebarEdge`).
    private var edge: some View {
        GeometryReader { g in Color.clear.preference(key: SidebarEdge.self, value: g.frame(in: .global).maxX) }
    }
}

/// Where the list column ends, for `SidebarTopFill`.

struct SidebarEdge: PreferenceKey {
    static let defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) { value = max(value, nextValue()) }
}

/// iPad: the list column starts below the status bar (its background cannot reach above it), so the strip over it was
/// the window's white and the list looked cut off at the top (2026-10-31). The split fills that strip with the
/// column's color, as wide as the column is.
struct SidebarTopFill: ViewModifier {
    /// The column is open (closed, its last frame stays reported: the fill must go by this).
    var shown: Bool
    /// The column's own color, where it has one (the one sidebar on the iPad, #4855); nil: the system sidebar's.
    var color: Color? = nil
    @State private var edge: CGFloat = 0

    func body(content: Content) -> some View {
        if UIDevice.current.userInterfaceIdiom == .pad {
            content
                .onPreferenceChange(SidebarEdge.self) { edge = $0 }
                .background(alignment: .topLeading) {
                    (color ?? (AppStyle.current.usesGlass ? Theme.systemSidebar : Theme.pageBackground))
                        .frame(width: shown ? max(edge, 0) : 0)
                        .animation(Theme.Motion.layout, value: shown)
                        .ignoresSafeArea()
                }
        } else {
            content
        }
    }
}

/// Set on the detail column of a `SplitNavigation` in regular width; nil in a single stack.
struct SplitDetail: Equatable {
    /// The page is the first one the list opened: there is nothing to go back to (the list is beside it).
    var isFirstPage: Bool
    /// The list column is closed (iOS: its button in the list's toolbar closed it). A first page with its own header
    /// (an Agents chat) has no system button to bring it back: its back does (`splitShowList`).
    var listHidden = false
    /// The page is a window of its own (the Mac's chat window), not a split's detail column: the window's title bar has
    /// its title.
    var ownWindow = false
}

extension EnvironmentValues {
    @Entry var splitDetail: SplitDetail?
    /// Pushes a page by value onto the stack the view is in (an extension's `push`, docs/extensions/extensions.md「Page navigation」): its
    /// destination is registered with the list's.
    @Entry var splitPush: SplitPush?
    /// Opens the list column of the split the page is in again (iOS); nil in a single stack.
    @Entry var splitShowList: SplitShowList?
}

/// Brings a `SplitNavigation`'s list column back.
struct SplitShowList {
    var show: () -> Void
}

/// Appends a value to a `SplitNavigation`'s path.
struct SplitPush {
    var push: (any Hashable) -> Void
}

extension View {
    /// A page the list opens: in the split's detail column its first page has no back button. `ownsBack`: the page shows or
    /// hides the system's back itself (an Agents chat); on iOS this leaves it alone, as an outer
    /// `navigationBarBackButtonHidden(false)` wins over the page's own `true` (iPhone Duo's side bars, 2026-11-04: the
    /// system's back beside the chat's own, two backs). On the Mac the title row draws no ‹ for it: the chat's header
    /// has its own, shown only with a page under the chat (#1337).
    func splitDetailPage(ownsBack: Bool = false) -> some View { modifier(SplitDetailPage(ownsBack: ownsBack)) }
}

private struct SplitDetailPage: ViewModifier {
    @Environment(\.splitDetail) private var split
    var ownsBack = false

    func body(content: Content) -> some View {
        #if os(macOS)
        content.modifier(MacDetailBack(shown: !ownsBack && split?.isFirstPage == false))
        #else
        // `ownsBack` is fixed for a page (no identity change as the path grows).
        if ownsBack { content } else { content.navigationBarBackButtonHidden(split?.isFirstPage == true) }
        #endif
    }
}

/// The detail column before anything is open: a quiet hint.
struct SplitPlaceholder: View {
    @Environment(\.splitDetail) private var split
    @Environment(\.splitShowList) private var showList
    #if os(iOS)
    @Environment(\.oneSidebarSideBars) private var sideBars
    #endif
    var symbol: String
    var title: String
    var message: String

    var body: some View {
        ContentUnavailableView {
            Label(title, systemImage: symbol)
        } description: {
            Text(message)
        } actions: {
            // The list closed with nothing open: this page hides its bar, where the system's button to open the list
            // would be, so it filled the screen with no way back to the list.
            if split?.listHidden == true, let showList {
                Button { showList.show() } label: { Text("Show List").lineLimit(1) }.themedButton()
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Theme.pageBackground)
        // Under the one sidebar's home (#4206), out of sight: on its side the bar there is the home's (New Chat, #4854).
        .toolbarVisibility(homeHasBar ? .automatic : .hidden, for: .navigationBar)
    }

    private var homeHasBar: Bool {
        #if os(iOS)
        sideBars
        #else
        false
        #endif
    }
}

/// Where a split's list lies, beside the page or over it, and whether it is shown, remembered under `key` (#2624, #4206;
/// iPad and iPhone Duo open).
///
/// - **Beside or over** follows the split's width alone (`SplitColumns.fitsBeside`, the rule the split itself lays the
///   columns by, `SplitPageMin`): beside where the page keeps its least width, over it elsewhere (the open Duo and an
///   iPad mini, upright; #4852). Turned to over, a list that was out goes away (as an iPad's sidebar does in portrait);
///   turned back, it comes back as it was left beside the page.
/// - **Remembered** only as a person leaves it beside the page: the column's own buttons and a finger's slide. Not over the
///   page (there its going away is a pick's or a tap on the page), not a turn's (beside by both SwiftUI's geometry and the
///   split's own size, `SplitColumnState`: as a turn lays the split out either can have the new size first; nor in a tab
///   not shown, whose size is not read until it shows), not a drag's moves (its end
///   sets it), not in the background (the system lays the app out in the other orientation for the app switcher's
///   snapshots, and a write there ran the phone out of the scene's 10 seconds, 0x8BADF00D). The split starts as the list
///   was left. nil key: the posture only, nothing kept.
///
/// The list has one width (`SidebarWidth`); a width kept by the old handle (`<key>.width`) is no longer read. Not on the Mac
/// or Apple Vision Pro.
struct SplitListMemory: ViewModifier {
    @Binding var columns: NavigationSplitViewVisibility
    let key: String?
    @StoredValue private var closed: Bool
    @Environment(\.scenePhase) private var scenePhase
    #if os(iOS)
    /// Where the list lies, set here for the split (nil before the split is laid out).
    var overlays: Binding<Bool?>?
    var state: SplitColumnState?
    /// The posture as last measured, and as last acted on (in front only).
    @State private var measured: Bool?
    @State private var fits: Bool?
    #endif

    init(columns: Binding<NavigationSplitViewVisibility>, key: String?) {
        _columns = columns
        self.key = key
        _closed = StoredValue(wrappedValue: false, (key ?? "split.list") + ".closed")
    }

    #if os(iOS)
    init(columns: Binding<NavigationSplitViewVisibility>, key: String?, overlays: Binding<Bool?>? = nil, state: SplitColumnState?) {
        self.init(columns: columns, key: key)
        self.overlays = overlays
        self.state = state
    }
    #endif

    #if os(iOS)
    /// Where a finger's slide left the list, kept as a person's choice beside the page once the finger is off (the split's
    /// columns already say so as a slide out begins, so their change shows nothing as it ends).
    @MainActor static func keep(_ key: String?, shown: Bool, state: SplitColumnState) {
        guard let key, !state.dragging, state.split?.view.window != nil, state.fitsBeside == true else { return }
        StoredValue(wrappedValue: false, key + ".closed").wrappedValue = !shown
    }
    #endif

    /// Whether the list kept under `key` was closed when last left; the split starts so.
    static func startsClosed(_ key: String?) -> Bool {
        #if os(iOS)
        guard let key, UIDevice.current.userInterfaceIdiom == .pad || UIDevice.current.userInterfaceIdiom == .phone else { return false }
        return UserDefaults.standard.bool(forKey: key + ".closed")
        #else
        false
        #endif
    }

    private var shown: Bool { columns != .detailOnly }

    func body(content: Content) -> some View {
        #if os(iOS)
        if UIDevice.current.userInterfaceIdiom == .pad || UIDevice.current.userInterfaceIdiom == .phone {
            let list = SplitColumns.listWidth
            content
                .onAppear { if key != nil, closed, shown { columns = .detailOnly } }
                .onGeometryChange(for: Bool.self) { $0.size.width - list >= Theme.Size.splitPageMin } action: { now in
                    measured = now
                    if scenePhase == .active { turned(now) }
                }
                // Back in front: as it is laid out now (a turn in the background was not acted on).
                .onChange(of: scenePhase) { _, phase in if phase == .active, let measured { turned(measured) } }
                .onChange(of: columns) { _, now in
                    // Beside the page by both SwiftUI's geometry and the split's own size: as a turn lays the split out
                    // either can be the first to have the new one, and a list the turn put away was kept as closed. On
                    // screen only: with Separate sidebars the tab not shown is turned too, its size not read until it shows.
                    guard key != nil, scenePhase == .active, state?.dragging != true, state?.split?.view.window != nil,
                          fits == true, state?.fitsBeside != false else { return }
                    closed = now == .detailOnly
                }
        } else {
            content
        }
        #else
        content
        #endif
    }

    #if os(iOS)
    private func turned(_ now: Bool) {
        let was = fits
        fits = now
        if let overlays, overlays.wrappedValue != !now { overlays.wrappedValue = !now }
        guard was != now else { return }
        if !now {
            // Over the page: a list that was out goes away (a launch upright too); it comes from its button.
            if shown { columns = .detailOnly }
        } else if was == false {
            // Back beside the page: as it was left there.
            let kept: NavigationSplitViewVisibility = key != nil && closed ? .detailOnly : SplitColumns.shown
            if columns != kept { columns = kept }
        }
    }
    #endif
}

#if os(iOS)
/// A split's page is never narrower than `Theme.Size.splitPageMin` beside the list column: where the column would leave it
/// less, the split lays the column over the page (#4206). Said to SwiftUI, which sets the split's least page width
/// itself: set on the split from UIKit, SwiftUI set it back on every update.
struct SplitPageMin: ViewModifier {
    func body(content: Content) -> some View {
        content.navigationSplitViewColumnWidth(min: Theme.Size.splitPageMin, ideal: Theme.Size.splitPageMin)
    }
}
#endif
