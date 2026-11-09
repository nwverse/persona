import SwiftUI

// MARK: - Long words folded to a few lines

/// Words drawn as Markdown, folded to `shown` lines of body text (nil: all of them; 0: none until Expand), with Expand /
/// Collapse under them when there is more (#2010: a long message from another session). `lineLimit` does not reach into
/// Markdown's blocks, so the words are cut at the height of that many body lines and fade out over the last one; words
/// that fit are drawn as they are, with no button. Open or not is kept by `id` (`OpenReports`, in the chat's scope), so
/// a row the lazy list builds again stays as it was left; `id` is also the key the row is pinned by (`chatPinned`), which
/// keeps its top where it is on screen while it opens or folds.
struct LineFold<Words: View>: View {
    var shown: Int?
    var id: String
    @ViewBuilder var words: () -> Words
    /// The words' whole height, and the height of `shown` body lines.
    @State private var full: CGFloat = 0
    @State private var cut: CGFloat = 0
    @Environment(\.rowMemoScope) private var scope
    @Environment(\.chatPin) private var pin

    private var key: String { scope + "/" + id }

    var body: some View {
        let open = OpenReports.shared.isOpen(key)
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            if let shown, !open {
                if shown > 0 {
                    measured
                        .frame(height: folds(open) ? cut : nil, alignment: .top)
                        .clipped()
                        .mask { if folds(open) { fade(shown) } else { Rectangle() } }
                        .background(alignment: .top) { probe(shown) }
                }
            } else {
                measured.background(alignment: .top) { if let shown, shown > 0 { probe(shown) } }
            }
            if folds(open) { FoldToggle(open: open, prefix: "message", action: { flip(open) }) }
        }
    }

    /// The words at their own height, which is read.
    private var measured: some View {
        words()
            .fixedSize(horizontal: false, vertical: true)
            .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { full = $0 }
    }

    /// `n` lines of body text, unseen: the height the words are cut at (a paragraph has the body's line height).
    private func probe(_ n: Int) -> some View {
        Text(verbatim: Array(repeating: "X", count: n).joined(separator: "\n"))
            .font(Theme.Font.body)
            .lineHeight(.body)
            .fixedSize(horizontal: false, vertical: true)
            .hidden()
            .accessibilityHidden(true)
            .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { cut = $0 }
    }

    /// Opaque down to the last line shown, which fades out.
    private func fade(_ n: Int) -> some View {
        VStack(spacing: 0) {
            Rectangle()
            LinearGradient(colors: [.black, .clear], startPoint: .top, endPoint: .bottom)
                .frame(height: cut / CGFloat(max(n, 1)))
        }
    }

    /// Something to open or fold: folded to none, cut at its lines, or opened from there.
    private func folds(_ open: Bool) -> Bool {
        guard let shown else { return false }
        if shown == 0 { return true }
        return cut > 0 && full > cut + 1
    }

    private func flip(_ open: Bool) {
        Haptics.tap()
        toggleInPlace(pin, id, animation: Theme.Motion.state, closing: open) { OpenReports.shared.set(key, !open) }
    }
}
