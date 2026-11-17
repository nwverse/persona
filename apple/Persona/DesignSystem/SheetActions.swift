import SwiftUI

// The way out of a sheet on a Mac (#1248). A Mac sheet has no navigation bar: only its bottom row of actions is drawn,
// so a Close at the leading edge (`.topBarLeading`, `.navigation` there) never shows (#743). It cannot be swiped down
// and a click outside it only beeps, so that row's Cancel / Done (Esc) is the only way out, and Esc is easy to hit.

extension View {
    /// Cancel in an edit sheet's bar (Esc on a Mac). On a Mac, with unsaved edits it asks before throwing them away;
    /// iOS cancels at once, as it always has.
    /// `when`: only in a sheet, for a page that is also pushed.
    func sheetCancel(_ title: LocalizedStringKey = "Cancel", unsaved: Bool, when active: Bool = true,
                     cancel: @escaping () -> Void) -> some View {
        modifier(SheetCancel(title: title, unsaved: unsaved, active: active, cancel: cancel))
    }

    /// A sheet bar's text button that only closes (Cancel) in the primary ink, not the accent (#9460): the bar's glass
    /// turns lighter in a dark sheet, the more so over a light page, and the accent on it read 2.1:1 (Send To over
    /// Export Diagnostics) and 3.1:1 (Forward To over a chat); iOS draws a bar's plain items in the label color.
    /// A confirming button (Done, Send) keeps the accent.
    func sheetBarLabel() -> some View {
        foregroundStyle(Color.primary)
    }

    /// Done for a sheet that only shows things, in a Mac sheet's bottom row (Esc too). Nothing on iOS: there the page
    /// keeps its own Close or the swipe down.
    func macSheetDone(_ done: @escaping () -> Void) -> some View {
        #if os(macOS)
        toolbar { ToolbarItem(placement: .cancellationAction) { Button(action: done) { Text("Done [Extension System Presentation c0b3fb]").lineLimit(1) } } }
        #else
        self
        #endif
    }
}

private struct SheetCancel: ViewModifier {
    var title: LocalizedStringKey
    var unsaved: Bool
    var active: Bool
    var cancel: () -> Void
    @State private var asking = false

    func body(content: Content) -> some View {
        if active { guarded(content) } else { content }
    }

    private func guarded(_ content: Content) -> some View {
        content
            .pageToolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button {
                        #if os(macOS)
                        if unsaved { asking = true; return }
                        #endif
                        cancel()
                    } label: {
                        Text(title).lineLimit(1)
                    }
                }
            }
            // On the content, not on the bar's button: the bar is hosted apart from the page.
            .confirmationDialog("Discard Unsaved Changes?", isPresented: $asking, titleVisibility: .visible) {
                Button("Discard Changes", role: .destructive, action: cancel)
                Button("Keep Editing", role: .cancel) {}
            } message: {
                Text("If you close it, the changes you made won't be saved.")
            }
            #if DEBUG && os(macOS)
            // `-showDiscardConfirm YES`: the question up as the sheet opens, for screenshots.
            .task {
                guard UserDefaults.standard.bool(forKey: "showDiscardConfirm") else { return }
                try? await Task.sleep(for: .seconds(1))
                asking = true
            }
            #endif
    }
}
