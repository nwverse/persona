import SwiftUI

/// A list that selects on the Mac (Persona's conversations): the open one is its selection, so ↑ / ↓ open the one
/// above / below as in Mail, ⏎ or Tab go to the composer and Delete archives (`MacListKeys`, #2113). Elsewhere the
/// list it always was. Not Agents': in the split's content column a list that selects clears the detail column whenever
/// its selection changes, and the chat it opened closed again.
struct MacSelectingList<Selection: Hashable, Content: View>: View {
    var selection: Binding<Selection?>
    @ViewBuilder var content: Content

    var body: some View {
        List { content }
    }
}

extension View {
    /// A row of a `MacSelectingList`. On the Mac the system's selected-row highlight is covered by the page's color and
    /// the shell's own shade (`Theme.Fill.macSelected`) drawn in `shape` when `selected` (the system's accent turned the
    /// row's own colors, its state dot and Needs You, into blue on blue: Inbox's rows do the same). `drawsShade: false` for a
    /// row that draws its own.
    func macListRow(selected: Bool, in shape: some Shape, outset: CGFloat = 0, drawsShade: Bool = true) -> some View {
        modifier(MacListRow(selected: selected, shape: shape, outset: outset, drawsShade: drawsShade))
    }
}

private struct MacListRow<S: Shape>: ViewModifier {
    var selected: Bool
    var shape: S
    var outset: CGFloat
    var drawsShade: Bool

    func body(content: Content) -> some View {
        content
    }
}

