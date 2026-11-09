import AppKit
import SwiftUI

extension View {
    /// ⌥-click does `action` instead of what a click on this view does (docs/apple/mac.md「Separate chat windows」: ⌥-click a list row to open it in a new window, as
    /// Finder and Mail): an AppKit layer over the view that only an option-click hits, so the row's link, its list's
    /// selection and the buttons in it never see that click; every other click goes through to them.
    func macOptionClick(_ action: @escaping () -> Void) -> some View {
        overlay(MacOptionClickCatcher(action: action))
    }
}

struct MacOptionClickCatcher: NSViewRepresentable {
    var action: () -> Void

    func makeNSView(context: Context) -> Catcher { Catcher(action: action) }
    func updateNSView(_ view: Catcher, context: Context) { view.action = action }

    /// Whether `event` is one this layer takes: a left-button press, drag or release with ⌥ and no other modifier.
    nonisolated static func catches(_ event: NSEvent?) -> Bool {
        guard let event, [.leftMouseDown, .leftMouseDragged, .leftMouseUp].contains(event.type) else { return false }
        return event.modifierFlags.intersection([.command, .shift, .option, .control]) == .option
    }

    final class Catcher: NSView {
        var action: () -> Void
        /// The event being dispatched (tests hand one in; the app's is `NSApp.currentEvent`).
        var currentEvent: () -> NSEvent? = { NSApp.currentEvent }

        init(action: @escaping () -> Void) {
            self.action = action
            super.init(frame: .zero)
        }
        required init?(coder: NSCoder) { nil }

        /// Transparent to everything but an option-click: hover, the wheel, the cursor and plain clicks find what is
        /// under it.
        override func hitTest(_ point: NSPoint) -> NSView? {
            MacOptionClickCatcher.catches(currentEvent()) ? super.hitTest(point) : nil
        }

        /// The click opens even in a window that is not key (as Finder does).
        override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

        override func mouseDown(with event: NSEvent) {
            guard MacOptionClickCatcher.catches(event) else { return super.mouseDown(with: event) }
            action()
        }

        // The rest of the click is the catcher's too: the row under it never sees half of one.
        override func mouseDragged(with event: NSEvent) {}
        override func mouseUp(with event: NSEvent) {}
    }
}
