import SwiftUI

extension EnvironmentValues {
    /// The edge the system puts its bars on, down the side, under the shell (iPhone Duo's outer display, and its inner
    /// display on its side, docs/apple/ios.md「iPhone Duo」): the shell's top bars become a rail there (`ShellSideRail`). nil:
    /// bars across the top, as on every other iPhone and on the inner display upright.
    @Entry var shellRailEdge: HorizontalEdge?
}

#if os(iOS) || os(visionOS)
extension View {
    /// Where the system presents bars vertically (`toolbarVerticalEdge`, iOS 27.1), nil when it does not: always nil
    /// before iOS 27.1 and when built with Xcode 26 (Swift ≤ 6.3 has no such key path).
    func readsVerticalBarEdge(_ edge: Binding<HorizontalEdge?>) -> some View { modifier(VerticalBarEdgeReader(edge: edge)) }
}

private struct VerticalBarEdgeReader: ViewModifier {
    @Binding var edge: HorizontalEdge?

    func body(content: Content) -> some View {
        #if compiler(>=6.4) && os(iOS)
        if #available(iOS 27.1, *) {
            content.modifier(SystemVerticalBarEdge(edge: $edge))
        } else {
            content
        }
        #else
        content
        #endif
    }
}

#if compiler(>=6.4) && os(iOS)
@available(iOS 27.1, *)
private struct SystemVerticalBarEdge: ViewModifier {
    @Environment(\.toolbarVerticalEdge) private var system
    @Binding var edge: HorizontalEdge?

    func body(content: Content) -> some View {
        content.onChange(of: system, initial: true) { _, e in edge = e }
    }
}
#endif

/// The shell's top bar where the system puts its bars down the side (iPhone Duo, `shellRailEdge`): the page's buttons in
/// the column beside the status bar, low in it where the thumb of the hand holding the phone reaches, and the switch at
/// its foot, in the order the system's own bars keep there (Apple's iPhone Duo guidance: navigation first, then the
/// prominent action, the tab bar below). The column is the side's safe area, so the content stays clear of it; the top
/// of the column is the status bar's and the camera's.
struct ShellSideRail<Buttons: View, Switcher: View>: View {
    let edge: HorizontalEdge
    @ViewBuilder var buttons: Buttons
    @ViewBuilder var switcher: Switcher

    var body: some View {
        // The column is what the page gives up on that side: its width with the side's safe area, less its width
        // without (a reader that ignores the safe area reports no inset of its own there).
        GeometryReader { inside in
            GeometryReader { whole in
                let column = max(0, whole.size.width - inside.size.width)
                VStack(spacing: Theme.Space.m) {
                    buttons
                    switcher.padding(.top, Theme.Space.s)
                }
                .frame(width: max(column, Theme.Shell.railSegment))
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: edge == .trailing ? .bottomTrailing : .bottomLeading)
                .padding(.bottom, Theme.Space.l)
            }
            .ignoresSafeArea(.container, edges: edge == .trailing ? .trailing : .leading)
        }
    }
}
#endif
