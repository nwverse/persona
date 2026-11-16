import SwiftUI

/// The tabs' switch on iPhone Duo in the Separate layout (#4231, the design of #4206): one component on both displays,
/// the vertical `ShellSwitch` (each page's symbol alone in a glass capsule), at the bottom-trailing corner, where the thumb
/// of the hand holding the phone reaches it.
///
/// - Where the system puts its bars down the side (the outer display, the inner one on its side), it stands at the foot
///   of that column, outside the content (`ShellSideRail`): on the outer display in the shell's rail, on the inner one
///   over the tabs (`TabSwitchOverlay`), in the place of the system's tab bar.
/// - Where the bars run across the top (the inner display upright), it floats over the page's bottom-trailing corner, its
///   foot level with the composer's, and what sits at the page's foot leaves it room (`tabSwitchRoom`, `tabSwitchListRoom`).
///
/// While the keyboard is up it fades away, and with it the room: the composer takes the whole width back.
nonisolated enum TabSwitchLayout {
    /// Where the switch stands over a phone's tabs.
    enum Placement: Equatable {
        /// At the foot of the system's side column on that edge (the open Duo on its side).
        case rail(HorizontalEdge)
        /// Over the page's bottom-trailing corner (the open Duo upright).
        case floating
    }

    /// Where the tabs' switch stands: on a phone's tabs (regular width, the open iPhone Duo) by where the system puts its
    /// bars; nil on the iPad, which keeps the system's tab bar along the top.
    static func placement(phone: Bool, barEdge: HorizontalEdge?) -> Placement? {
        guard phone else { return nil }
        return barEdge.map { .rail($0) } ?? .floating
    }

    /// The capsule's width: a tap target and the capsule's padding on both sides (52 pt). The segments keep the default
    /// text size (`Theme.Shell.switchTypeSize`), so it is the same at every text size.
    static let width = Theme.Size.tapTarget + Theme.Space.xs * 2

    /// The capsule's height with `items` segments: 140 pt with Messages, 96 pt without.
    static func height(items: Int) -> CGFloat { Theme.Size.tapTarget * CGFloat(items) + Theme.Space.xs * 2 }

    /// The floating switch from the page's trailing edge and from its foot (the safe area's): the footer's own margins,
    /// so its foot is level with the composer's.
    static let trailingInset = Theme.Space.m
    static let bottomInset = Theme.Space.s

    /// What a row at the page's foot leaves free at its trailing end, outside its own margin (at least the footer's
    /// `trailingInset`): the switch and a gap. Nothing while the keyboard is up, when the switch is away.
    static func trailingRoom(floats: Bool, keyboardUp: Bool) -> CGFloat {
        floats && !keyboardUp ? width + Theme.Space.s : 0
    }

    /// The room a list keeps past its last row, so the end scrolls clear of the floating switch: its height, its inset
    /// from the foot and a gap. Kept while the keyboard is up (the list's end is then over the keyboard anyway), so typing
    /// in a search field never moves the list.
    static func listRoom(floats: Bool, items: Int) -> CGFloat {
        floats ? height(items: items) + bottomInset + Theme.Space.s : 0
    }

    /// How many segments the switch has: Assistant and Agents, and Messages when it is on (`LarkHomePreference.shows`).
    static func items(messages: Bool) -> Int { messages ? 3 : 2 }
}

extension EnvironmentValues {
    /// The tabs' switch floats over this page's bottom-trailing corner (the open iPhone Duo upright, the Separate layout,
    /// `TabSwitchLayout`): what sits at its foot leaves it room. Set by the tabs on a phone only (`TabSwitchOverlay`);
    /// false everywhere else, where the modifiers below change nothing.
    @Entry var tabSwitchFloats = false
}

extension View {
    /// Room at the trailing end beside the floating tabs' switch while it shows (`TabSwitchLayout.trailingRoom`): on a
    /// page's foot (the composer and the rows over it, a bar of actions), outside the rows' own margin and inside any fill
    /// that spans the page. Nothing anywhere the switch does not float.
    func tabSwitchRoom() -> some View { modifier(TabSwitchRoom()) }

    /// The same, with the row's own margin on both sides in the one modifier (`padding(.horizontal, padding)` and the
    /// room): one step of the modifier chain, where the chain is deep (#6735).
    func tabSwitchRoom(padding: CGFloat) -> some View { modifier(TabSwitchRoom(margin: padding)) }

    /// Room at a list's end past its last row, clear of the floating tabs' switch (`TabSwitchLayout.listRoom`): on the
    /// list or scroll view itself, not on a container (it would reach the rows' own scroll views). Nothing anywhere the
    /// switch does not float.
    func tabSwitchListRoom() -> some View { modifier(TabSwitchListRoom()) }

    /// Room at the trailing end of the system's bottom toolbar beside the floating tabs' switch: an empty item after the
    /// page's own `.bottomBar` items, only while the switch floats (none anywhere else, so those bars are as they were).
    /// `shown`: the page's own bottom bar is up (selecting); an item alone would bring up an empty bar.
    func tabSwitchBottomBarRoom(shown: Bool = true) -> some View { modifier(TabSwitchBottomBarRoom(shown: shown)) }

    /// Presented over the tabs (a sheet): the tabs' switch is under it, so nothing in it leaves the switch room. A sheet
    /// takes the environment of the page it rises from; one that can hold a page with room (`tabSwitchRoom`) sets this.
    func overTabSwitch() -> some View { environment(\.tabSwitchFloats, false) }
}

private struct TabSwitchRoom: ViewModifier {
    /// The row's own margin on both sides, drawn here too (`tabSwitchRoom(padding:)`); none for `tabSwitchRoom()`.
    var margin: CGFloat = 0
    @Environment(\.tabSwitchFloats) private var floats

    func body(content: Content) -> some View {
        // The keyboard is read only where the switch floats: everywhere else this draws nothing again.
        let up = floats && KeyboardState.shared.isUp
        let room = TabSwitchLayout.trailingRoom(floats: floats, keyboardUp: up)
        // Only the room moves with the keyboard's curve, not what else changes in the footer as it comes up.
        content.animation(Theme.Motion.keyboard) {
            $0.padding(EdgeInsets(top: 0, leading: margin, bottom: 0, trailing: margin + room))
        }
    }
}

private struct TabSwitchBottomBarRoom: ViewModifier {
    let shown: Bool
    @Environment(\.tabSwitchFloats) private var floats

    func body(content: Content) -> some View {
        #if os(iOS)
        content.toolbar {
            if floats && shown {
                ToolbarItem(placement: .bottomBar) {
                    Color.clear.frame(width: TabSwitchLayout.width, height: Theme.Size.tapTarget).accessibilityHidden(true)
                }
                .sharedBackgroundVisibility(.hidden)
            }
        }
        #else
        content
        #endif
    }
}

private struct TabSwitchListRoom: ViewModifier {
    @Environment(\.tabSwitchFloats) private var floats

    func body(content: Content) -> some View {
        #if os(iOS) || os(visionOS)
        let messages = floats && LarkHomePreference.shared.shows
        #else
        let messages = false
        #endif
        content.safeAreaPadding(.bottom, TabSwitchLayout.listRoom(floats: floats, items: TabSwitchLayout.items(messages: messages)))
    }
}

#if os(iOS)
/// The tabs on a phone (the open iPhone Duo in the Separate layout, #4231): the system's tab bar is hidden on every tab
/// (`PhoneTabBarHidden`), and the shell's vertical switch stands in its place at the bottom-trailing corner, over the tabs
/// and outside their splits and stacks, so it stays put on a push and a pop. The iPad keeps the system's tab bar.
///
/// Where the system's bars are is read here, in the same update as the tabs, not a beat later through a state: the switch
/// and the pages' room never start in the upright posture on the open Duo on its side.
struct TabSwitchOverlay: ViewModifier {
    private let phone = UIDevice.current.userInterfaceIdiom == .phone

    func body(content: Content) -> some View {
        if phone {
            #if compiler(>=6.4)
            if #available(iOS 27.1, *) {
                content.modifier(TabSwitchSideBars())
            } else {
                content.modifier(TabSwitchPlaced(edge: nil))
            }
            #else
            content.modifier(TabSwitchPlaced(edge: nil))
            #endif
        } else {
            content
        }
    }
}

#if compiler(>=6.4)
@available(iOS 27.1, *)
private struct TabSwitchSideBars: ViewModifier {
    @Environment(\.toolbarVerticalEdge) private var edge

    func body(content: Content) -> some View { content.modifier(TabSwitchPlaced(edge: edge)) }
}
#endif

private struct TabSwitchPlaced: ViewModifier {
    let edge: HorizontalEdge?

    func body(content: Content) -> some View {
        let placement = TabSwitchLayout.placement(phone: true, barEdge: edge)
        content
            .environment(\.tabSwitchFloats, placement == .floating)
            .overlay { if let placement { TabSwitchStand(placement: placement) } }
    }
}

/// The switch where `placement` puts it. Its own view: only it (and the switch in it) reads the tab.
private struct TabSwitchStand: View {
    let placement: TabSwitchLayout.Placement
    @Environment(AppModel.self) private var model

    var body: some View {
        @Bindable var model = model
        let switcher = ShellSwitch(tab: $model.selectedTab, axis: .vertical) { EmptyView() } workMenu: { EmptyView() }
            .accessibilityIdentifier("tab-switch")
        switch placement {
        case .rail(let edge):
            ShellSideRail(edge: edge) { EmptyView() } switcher: { switcher }
                .environment(\.shellRailEdge, edge) // the badges toward the page
                .ignoresSafeArea(.keyboard)
        case .floating:
            switcher
                .padding(.trailing, TabSwitchLayout.trailingInset)
                .padding(.bottom, TabSwitchLayout.bottomInset)
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottomTrailing)
                .ignoresSafeArea(.keyboard)
        }
    }
}

/// The system's tab bar hidden on a tab's page, on a phone (the open iPhone Duo: `TabSwitchOverlay` stands in its place).
/// Set once on each tab's root and never toggled: a tab bar shown and hidden on a push flashed the list, and one changed
/// during a drag laid the whole page out again (docs/lessons/ios.md). The iPad keeps its bar.
struct PhoneTabBarHidden: ViewModifier {
    private let phone = UIDevice.current.userInterfaceIdiom == .phone

    func body(content: Content) -> some View {
        if phone { content.toolbarVisibility(.hidden, for: .tabBar) } else { content }
    }
}
#endif
