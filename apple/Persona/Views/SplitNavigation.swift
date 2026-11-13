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
    /// Where the split, its list column's trailing edge and its page start (global x), as last laid out.
    @State private var splitStart: CGFloat = 0
    @State private var listEnd: CGFloat = 0
    @State private var pageStart: CGFloat = 0
    /// The list column slides in over the page instead of beside it: the system's choice where the page would be left too
    /// narrow (the open iPhone Duo held upright, #4852). Read while the list is out, kept while it is away; nil: not read
    /// yet (the split's own first moves, out or away, are no one's choice).
    @State private var overlays: Bool?
    /// The system's bars down the side beside the one sidebar (the open Duo on its side): nothing is above the columns.
    @Environment(\.oneSidebarSideBars) private var sideBars
    /// Each column's own top safe area, outside the margin it is given (the page's has the system's bar room under side
    /// bars, the list's does not).
    @State private var listSafeTop: CGFloat = 0
    @State private var pageSafeTop: CGFloat = 0
    /// The split is wider than it is tall: the open iPhone Duo on its side, where the columns stay side by side.
    @State private var sideways = false
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
                    #if os(iOS)
                    .background { SplitTilePin(tile: UIDevice.current.userInterfaceIdiom == .phone && sideways) }
                    #endif
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
                    .onGeometryChange(for: CGFloat.self) { $0.frame(in: .global).minX } action: { pageStart = $0; notePosture() }
                    #endif
                } else {
                    NavigationStack(path: $path) {
                        empty.modifier(destinations)
                    }
                    #if os(iOS)
                    .onGeometryChange(for: CGFloat.self) { $0.frame(in: .global).minX } action: { pageStart = $0; notePosture() }
                    #endif
                }
            }
            .navigationSplitViewStyle(.balanced)
            #if os(iOS)
            .onPreferenceChange(SidebarEdge.self) { listEnd = $0; notePosture() }
            .onGeometryChange(for: CGFloat.self) { $0.frame(in: .global).minX } action: { splitStart = $0; notePosture() }
            .onGeometryChange(for: Bool.self) { $0.size.width > $0.size.height } action: { sideways = $0 }
            // An agent picked, or anything else that changes the page: a list laid over the page goes (#4852).
            .onChange(of: path) { hideOverlay() }
            // Laid over the page the list comes out with nothing else moving: read it once it is out.
            .onChange(of: columns) { _, now in if now != .detailOnly { Task { notePosture() } } }
            #endif
            .modifier(SidebarTopFill(shown: columns != .detailOnly, color: topFillColor))
            .modifier(SplitListMemory(columns: $columns, key: listKey, overlays: overlaysNow))
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

    private var overlaysNow: Bool? {
        #if os(iOS)
        overlays
        #else
        false
        #endif
    }

    #if os(iOS)
    /// The posture: the page starting under the list (at the split's leading edge) is the page under the list; beside it,
    /// it starts where the list ends. Read only while the list is out (away, both postures look the same).
    private func notePosture() {
        // As the split first lays itself out the list is narrower than any it takes, and the page slides in from before
        // it: read then, an iPad's list looked laid over its page, and its closing went unremembered.
        guard columns != .detailOnly, listEnd - splitStart >= Theme.Size.sidebarMinPhone, pageStart >= splitStart else { return }
        // In the first half of the list, not merely before its end: while the handle drags the list the page's edge moves
        // a frame before the list's, and `pageStart < listEnd - 1` read every such frame as laid over, so a list dragged
        // closed on the open Duo on its side was not remembered closed (SplitDividerTests, 2026-11-13).
        let now = pageStart < splitStart + (listEnd - splitStart) / 2
        if overlays != now { overlays = now }
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

/// Where the list column ends, for `SidebarTopFill` and the posture (`SplitNavigation.notePosture`).
#if os(iOS)
/// Keeps the split's columns side by side on a phone held on its side (the open iPhone Duo, #4206). With `.balanced` the
/// system tiles or overlays by itself: past some width of the column it laid the one sidebar over the dimmed page, and
/// once it had, it kept it so after the column narrowed again, until the column folded (a width kept at 480 pt did it
/// from launch). Upright it stays the system's choice: the list over the page there is by design (#4852).
/// The split's `preferredSplitBehavior` is set from inside its column (`splitViewController`); what SwiftUI had there is
/// put back when the phone turns upright.
struct SplitTilePin: UIViewControllerRepresentable {
    var tile: Bool

    func makeUIViewController(context: Context) -> Pin { Pin() }
    func updateUIViewController(_ pin: Pin, context: Context) {
        pin.tile = tile
        pin.apply()
    }

    final class Pin: UIViewController {
        var tile = false
        /// The split's own behavior before it was pinned; nil while it is not.
        private var before: UISplitViewController.SplitBehavior?

        override func loadView() {
            view = UIView()
            view.isUserInteractionEnabled = false
        }

        override func didMove(toParent parent: UIViewController?) {
            super.didMove(toParent: parent)
            apply()
        }

        // SwiftUI sets the split up again as it updates it: kept on every layout of the column.
        override func viewWillLayoutSubviews() {
            super.viewWillLayoutSubviews()
            apply()
        }

        func apply() {
            guard let split = splitViewController else { return }
            if tile {
                if before == nil { before = split.preferredSplitBehavior }
                if split.preferredSplitBehavior != .tile {
                    SplitColumns.log.notice("tile pinned (was \(split.preferredSplitBehavior.rawValue), display \(split.displayMode.rawValue))")
                    split.preferredSplitBehavior = .tile
                }
            } else if let before {
                if split.preferredSplitBehavior != before { split.preferredSplitBehavior = before }
                self.before = nil
            }
        }
    }
}
#endif

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

/// Whether a split's list is shown or hidden, remembered under `key` (#2624, #4206; iPad and iPhone Duo open): the
/// column's own buttons hide and show it, and the split starts as the list was left. Only a list beside the page is
/// remembered: laid over it (the open Duo upright, #4852) its going away is a pick's or a tap on the page, not a choice to
/// keep it closed; nothing is kept until the posture is known (nil). The list has one width (`SidebarWidth`): no handle
/// resizes it any more, and a width kept by the handle before (`<key>.width`) is no longer read. nil key: nothing kept.
/// Not on the Mac or Apple Vision Pro.
struct SplitListMemory: ViewModifier {
    @Binding var columns: NavigationSplitViewVisibility
    let key: String?
    var overlays: Bool? = false
    @StoredValue private var closed: Bool

    init(columns: Binding<NavigationSplitViewVisibility>, key: String?, overlays: Bool? = false) {
        _columns = columns
        self.key = key
        self.overlays = overlays
        _closed = StoredValue(wrappedValue: false, (key ?? "split.list") + ".closed")
    }

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
        if key != nil, UIDevice.current.userInterfaceIdiom == .pad || UIDevice.current.userInterfaceIdiom == .phone {
            content
                .onAppear { if closed, shown { columns = .detailOnly } }
                .onChange(of: columns) { _, now in if overlays == false { closed = now == .detailOnly } }
                .onChange(of: overlays) { _, now in if now == false { closed = !shown } }
        } else {
            content
        }
        #else
        content
        #endif
    }
}
