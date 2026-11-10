import SwiftUI

/// iOS modifiers visionOS marks unavailable, given visionOS meanings so the shared pages stay as they are
/// (docs/apple/vision.md「Approach」). The solver prefers these over the unavailable system ones.
extension View {
    /// The visionOS keyboard floats in its own window: nothing on the page scrolls it away.
    func scrollDismissesKeyboard(_ mode: ScrollDismissesKeyboardModeCompat) -> some View { self }

    /// A visionOS window's title bar has no subtitle line: the page shows what matters in its own header.
    func navigationSubtitle<S: StringProtocol>(_ subtitle: S) -> some View { self }

    /// visionOS scroll views have no soft edge blur to hide.
    func scrollEdgeEffectHidden(_ hidden: Bool = true, for edges: Edge.Set = .all) -> some View { self }

    /// Nor a hard or soft edge to choose between.
    func scrollEdgeEffectStyle(_ style: ScrollEdgeEffectStyleCompat?, for edges: Edge.Set) -> some View { self }
}

/// iOS's `ScrollEdgeEffectStyle` cases, which visionOS does not have.
enum ScrollEdgeEffectStyleCompat {
    case automatic, hard, soft
}

/// visionOS has no Liquid Glass containers: its windows are glass already, so the group just lays its content out.
struct GlassEffectContainer<Content: View>: View {
    var spacing: CGFloat?
    @ViewBuilder var content: Content

    init(spacing: CGFloat? = nil, @ViewBuilder content: () -> Content) {
        self.spacing = spacing
        self.content = content()
    }

    var body: some View { content }
}

/// iOS's `ScrollDismissesKeyboardMode` cases, which visionOS does not have.
enum ScrollDismissesKeyboardModeCompat {
    case automatic, immediately, interactively, never
}

extension ToolbarContent {
    /// visionOS toolbars are not Liquid Glass and share no background between items: nothing to hide.
    func sharedBackgroundVisibility(_ visibility: Visibility) -> some ToolbarContent { self }
}
