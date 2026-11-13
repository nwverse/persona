#if os(macOS)
import SwiftUI

// iOS-only SwiftUI API under the same names, with what each means on a Mac (docs/apple/mac.md「Approach」): the pages are
// written once, for iOS, and compile here unchanged. Where a Mac has no such thing (a keyboard type, a status bar)
// the modifier does nothing.

// MARK: Navigation bar

enum NavigationBarTitleDisplayModeCompat { case automatic, inline, large }

extension View {
    /// A Mac window shows its title in the toolbar at one size: nothing to choose.
    func navigationBarTitleDisplayMode(_ mode: NavigationBarTitleDisplayModeCompat) -> some View { self }

    func statusBarHidden(_ hidden: Bool = true) -> some View { self }
}

extension ToolbarItemPlacement {
    /// The trailing end of the window's toolbar.
    static var topBarTrailing: ToolbarItemPlacement { .primaryAction }
    /// The leading end, next to the back button / sidebar toggle.
    static var topBarLeading: ToolbarItemPlacement { .navigation }
    /// Under the content on iOS; the window's toolbar is the only bar a Mac has.
    static var bottomBar: ToolbarItemPlacement { .automatic }
    /// Over the keyboard on iOS: a Mac has no on-screen keyboard to dock to.
    static var keyboard: ToolbarItemPlacement { .automatic }
}

/// Bars a page hides or tints on iOS (`for: .tabBar`, `for: .navigationBar`). A Mac window has neither: its
/// sidebar is not a tab bar and the toolbar belongs to the window, so these do nothing here.
struct BarPlacementCompat: Hashable {
    static let tabBar = BarPlacementCompat()
    static let navigationBar = BarPlacementCompat()
    static let bottomBar = BarPlacementCompat()
}

extension View {
    func toolbarVisibility(_ visibility: Visibility, for bars: BarPlacementCompat...) -> some View { self }
    func toolbar(_ visibility: Visibility, for bars: BarPlacementCompat...) -> some View { self }
    func toolbarBackground(_ visibility: Visibility, for bars: BarPlacementCompat...) -> some View { self }
    func toolbarBackground<S: ShapeStyle>(_ style: S, for bars: BarPlacementCompat...) -> some View { self }
    func toolbarColorScheme(_ scheme: ColorScheme?, for bars: BarPlacementCompat...) -> some View { self }
}

// MARK: Lists

extension ListStyle where Self == InsetListStyle {
    /// iOS's grouped cards: a Mac list's closest look is the inset style.
    static var insetGrouped: InsetListStyle { .inset }
}

struct ListSectionSpacingCompat {
    static let `default` = ListSectionSpacingCompat()
    static let compact = ListSectionSpacingCompat()
    static func custom(_ spacing: CGFloat) -> ListSectionSpacingCompat { ListSectionSpacingCompat() }
}

extension View {
    /// Mac lists space their sections on their own.
    func listSectionSpacing(_ spacing: ListSectionSpacingCompat) -> some View { self }
    func listSectionSpacing(_ spacing: CGFloat) -> some View { self }
}

// MARK: Text input

struct TextInputAutocapitalization {
    static let never = TextInputAutocapitalization()
    static let words = TextInputAutocapitalization()
    static let sentences = TextInputAutocapitalization()
    static let characters = TextInputAutocapitalization()
}

enum UIKeyboardType { case `default`, asciiCapable, numbersAndPunctuation, URL, numberPad, phonePad, namePhonePad, emailAddress, decimalPad, twitter, webSearch, asciiCapableNumberPad }

enum UITextAutocapitalizationType { case none, words, sentences, allCharacters }

extension View {
    /// A hardware keyboard types what it types: no capitalization or keyboard layout to ask for.
    func textInputAutocapitalization(_ autocapitalization: TextInputAutocapitalization?) -> some View { self }
    func keyboardType(_ type: UIKeyboardType) -> some View { self }
    func autocapitalization(_ style: UITextAutocapitalizationType) -> some View { self }
}

// MARK: Presentation

extension View {
    /// A cover on iOS; on a Mac a sheet as large as the window (`macLargeSheet`). Counted with bars: some covers are
    /// navigation stacks (the image editor), whose bars would push the sheet past the window.
    func fullScreenCover<Item: Identifiable, Content: View>(item: Binding<Item?>, onDismiss: (() -> Void)? = nil,
                                                            @ViewBuilder content: @escaping (Item) -> Content) -> some View {
        sheet(item: item, onDismiss: onDismiss) { content($0).macLargeSheet(bars: true) }
    }

    func fullScreenCover<Content: View>(isPresented: Binding<Bool>, onDismiss: (() -> Void)? = nil,
                                        @ViewBuilder content: @escaping () -> Content) -> some View {
        sheet(isPresented: isPresented, onDismiss: onDismiss) { content().macLargeSheet(bars: true) }
    }
}

// MARK: Size classes

/// A Mac window is wide: pages lay out as on a regular-width iPad (split columns), never as on a phone.
enum UserInterfaceSizeClass: Sendable { case compact, regular }

extension EnvironmentValues {
    var horizontalSizeClass: UserInterfaceSizeClass? { .regular }
    var verticalSizeClass: UserInterfaceSizeClass? { .regular }
}

// MARK: Search

enum NavigationBarDrawerDisplayModeCompat { case automatic, always }

extension SearchFieldPlacement {
    /// The search field under the navigation bar on iOS; in the window's toolbar on a Mac.
    static var navigationBarDrawer: SearchFieldPlacement { .toolbar }
    static func navigationBarDrawer(displayMode: NavigationBarDrawerDisplayModeCompat) -> SearchFieldPlacement { .toolbar }
}

// MARK: Transitions

extension NavigationTransition where Self == AutomaticNavigationTransition {
    /// iOS zooms a cover out of its source; a Mac sheet comes in its own way.
    static func zoom(sourceID: some Hashable, in namespace: Namespace.ID) -> AutomaticNavigationTransition { .automatic }
}

// MARK: Paging

extension TabViewStyle where Self == DefaultTabViewStyle {
    /// Swiping pages: a Mac TabView has no page style, the default one shows the same pages.
    static var page: DefaultTabViewStyle { .automatic }
    static func page(indexDisplayMode: PageIndexDisplayModeCompat) -> DefaultTabViewStyle { .automatic }
}

enum PageIndexDisplayModeCompat { case automatic, always, never }

extension View {
    func indexViewStyle<S>(_ style: S) -> some View { self }
}
#endif

#if os(macOS)
// MARK: Editing lists

/// iOS's Edit button turns on a list's edit mode (reorder handles, delete buttons). A Mac list reorders by dragging
/// and deletes from a row's context menu or the delete key without a mode, so there is nothing to switch.
struct EditButton: View {
    var body: some View { EmptyView() }
}
#endif

#if os(macOS)
// MARK: Rendering

extension ImageRenderer {
    /// UIKit's name for the rendered image.
    @MainActor var uiImage: NSImage? { nsImage }
}
#endif
